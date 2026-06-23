package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const kiroUsageUpstreamTimeout = 20 * time.Second

type kiroUsageHTTPError struct {
	StatusCode int
	Body       string
}

func (e *kiroUsageHTTPError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, body)
}

func (s *AccountUsageService) getKiroUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
	now := time.Now()
	if account == nil {
		return &UsageInfo{Source: "active", UpdatedAt: &now}, nil
	}
	cache := s.cache
	if cache == nil {
		cache = NewUsageCache()
		s.cache = cache
	}

	if !force {
		if cached, ok := cache.kiroCache.Load(account.ID); ok {
			if usageCache, ok := cached.(*kiroUsageCache); ok && time.Since(usageCache.timestamp) < kiroCacheTTL(usageCache.usageInfo) {
				usage := usageCache.usageInfo
				s.addKiroWindowStats(ctx, account, usage)
				return usage, nil
			}
		}
	}

	flightKey := fmt.Sprintf("kiro-usage:%d", account.ID)
	result, flightErr, _ := cache.kiroFlight.Do(flightKey, func() (any, error) {
		if !force {
			if cached, ok := cache.kiroCache.Load(account.ID); ok {
				if usageCache, ok := cached.(*kiroUsageCache); ok && time.Since(usageCache.timestamp) < kiroCacheTTL(usageCache.usageInfo) {
					return usageCache.usageInfo, nil
				}
			}
		}

		fetchCtx, cancel := context.WithTimeout(context.Background(), kiroUsageUpstreamTimeout)
		defer cancel()

		usage := s.fetchKiroUsageInfo(fetchCtx, account)
		cache.kiroCache.Store(account.ID, &kiroUsageCache{usageInfo: usage, timestamp: time.Now()})
		return usage, nil
	})
	if flightErr != nil {
		return nil, flightErr
	}
	usage, ok := result.(*UsageInfo)
	if !ok || usage == nil {
		usage = &UsageInfo{Source: "active", UpdatedAt: &now}
	}
	s.addKiroWindowStats(ctx, account, usage)
	return usage, nil
}

func (s *AccountUsageService) fetchKiroUsageInfo(ctx context.Context, account *Account) *UsageInfo {
	now := time.Now()
	usage := &UsageInfo{
		Source:    "active",
		UpdatedAt: &now,
		FiveHour:  &UsageProgress{Utilization: 0},
	}
	if strings.TrimSpace(account.GetKiroAccessToken()) == "" {
		usage.ErrorCode = "configuration"
		usage.Error = "missing Kiro access_token"
		return usage
	}
	limits, err := fetchKiroUsageLimits(ctx, account)
	if err != nil {
		applyKiroUsageError(usage, err)
		return usage
	}
	usage.KiroUsage = limits
	return usage
}

func (s *AccountUsageService) addKiroWindowStats(ctx context.Context, account *Account, usage *UsageInfo) {
	if s == nil || s.usageLogRepo == nil || account == nil || usage == nil {
		return
	}
	if s.cache == nil {
		s.cache = NewUsageCache()
	}
	if usage.FiveHour == nil {
		usage.FiveHour = &UsageProgress{Utilization: 0}
	}
	s.addWindowStats(ctx, account, usage)
}

func fetchKiroUsageLimits(ctx context.Context, account *Account) (*KiroUsageLimitsInfo, error) {
	token := strings.TrimSpace(account.GetKiroAccessToken())
	if token == "" {
		return nil, fmt.Errorf("missing Kiro access_token")
	}
	targetURL, err := buildKiroUsageLimitsURL(account)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	applyKiroRuntimeHeaders(req, account, token)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               kiroUsageUpstreamTimeout,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &kiroUsageHTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseKiroUsageLimitsPayload(body)
}

func buildKiroUsageLimitsURL(account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("missing Kiro account")
	}
	baseURL := strings.TrimRight(account.GetKiroBaseURL(), "/")
	if baseURL == "" {
		baseURL = kiroCodeWhispererBaseForProfileARN(account.GetKiroProfileARN())
	}
	baseURL = strings.TrimSuffix(baseURL, KiroCodeWhispererEndpointPath)
	parsed, err := url.Parse(baseURL + "/getUsageLimits")
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Kiro usage endpoint: %s", baseURL)
	}
	q := parsed.Query()
	q.Set("origin", kiroOriginForAuthMethod(account.GetKiroAuthMethod()))
	q.Set("resourceType", "AGENTIC_REQUEST")
	if profileARN := strings.TrimSpace(account.GetKiroProfileARN()); profileARN != "" {
		q.Set("profileArn", profileARN)
	} else {
		q.Set("isEmailRequired", "true")
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func parseKiroUsageLimitsPayload(body []byte) (*KiroUsageLimitsInfo, error) {
	var payload struct {
		DaysUntilReset int    `json:"daysUntilReset"`
		NextDateReset  string `json:"nextDateReset"`
		UserInfo       struct {
			Email string `json:"email"`
		} `json:"userInfo"`
		SubscriptionInfo struct {
			Subscription string `json:"subscription"`
			Tier         string `json:"tier"`
			Name         string `json:"name"`
		} `json:"subscriptionInfo"`
		UsageBreakdownList []struct {
			ResourceType              string   `json:"resourceType"`
			UsageLimit                float64  `json:"usageLimit"`
			CurrentUsage              float64  `json:"currentUsage"`
			UsageLimitWithPrecision   *float64 `json:"usageLimitWithPrecision"`
			CurrentUsageWithPrecision *float64 `json:"currentUsageWithPrecision"`
		} `json:"usageBreakdownList"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	info := &KiroUsageLimitsInfo{
		DaysUntilReset: payload.DaysUntilReset,
		NextDateReset:  payload.NextDateReset,
		UserEmail:      payload.UserInfo.Email,
		Subscription:   firstNonEmpty(payload.SubscriptionInfo.Subscription, payload.SubscriptionInfo.Tier, payload.SubscriptionInfo.Name),
	}
	for _, item := range payload.UsageBreakdownList {
		if info.ResourceType == "" || item.ResourceType == "AGENTIC_REQUEST" {
			currentUsage := item.CurrentUsage
			if item.CurrentUsageWithPrecision != nil {
				currentUsage = *item.CurrentUsageWithPrecision
			}
			usageLimit := item.UsageLimit
			if item.UsageLimitWithPrecision != nil {
				usageLimit = *item.UsageLimitWithPrecision
			}
			info.ResourceType = item.ResourceType
			info.CurrentUsage = currentUsage
			info.UsageLimit = usageLimit
			if usageLimit > 0 {
				info.Utilization = currentUsage / usageLimit * 100
			}
		}
		if item.ResourceType == "AGENTIC_REQUEST" {
			break
		}
	}
	if info.ResourceType == "" {
		return nil, fmt.Errorf("missing usageBreakdownList in Kiro response")
	}
	return info, nil
}

func kiroCacheTTL(info *UsageInfo) time.Duration {
	if info != nil && info.Error != "" {
		return kiroErrorTTL
	}
	return apiCacheTTL
}

func applyKiroUsageError(info *UsageInfo, err error) {
	if info == nil || err == nil {
		return
	}
	info.Error = sanitizeUpstreamErrorMessage(err.Error())
	switch e := err.(type) {
	case *kiroUsageHTTPError:
		switch e.StatusCode {
		case http.StatusUnauthorized:
			info.ErrorCode = "unauthenticated"
			info.NeedsReauth = true
		case http.StatusForbidden:
			info.ErrorCode = "forbidden"
			info.IsForbidden = true
		case http.StatusTooManyRequests:
			info.ErrorCode = "rate_limited"
		default:
			info.ErrorCode = "upstream_error"
		}
	default:
		info.ErrorCode = "network_error"
	}
}
