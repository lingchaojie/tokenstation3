package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const kiloUsageUpstreamTimeout = 20 * time.Second

type kiloUsageHTTPError struct {
	StatusCode int
	Body       string
}

func (e *kiloUsageHTTPError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, body)
}

func (s *AccountUsageService) getKiloUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
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
		if cached, ok := cache.kiloCache.Load(account.ID); ok {
			if usageCache, ok := cached.(*kiloUsageCache); ok && time.Since(usageCache.timestamp) < kiloCacheTTL(usageCache.usageInfo) {
				usage := usageCache.usageInfo
				s.addKiloWindowStats(ctx, account, usage)
				return usage, nil
			}
		}
	}

	flightKey := fmt.Sprintf("kilo-usage:%d", account.ID)
	result, flightErr, _ := cache.kiloFlight.Do(flightKey, func() (any, error) {
		if !force {
			if cached, ok := cache.kiloCache.Load(account.ID); ok {
				if usageCache, ok := cached.(*kiloUsageCache); ok && time.Since(usageCache.timestamp) < kiloCacheTTL(usageCache.usageInfo) {
					return usageCache.usageInfo, nil
				}
			}
		}

		fetchCtx, cancel := context.WithTimeout(context.Background(), kiloUsageUpstreamTimeout)
		defer cancel()

		usage := s.fetchKiloUsageInfo(fetchCtx, account)
		cache.kiloCache.Store(account.ID, &kiloUsageCache{
			usageInfo: usage,
			timestamp: time.Now(),
		})
		return usage, nil
	})
	if flightErr != nil {
		return nil, flightErr
	}
	usage, ok := result.(*UsageInfo)
	if !ok || usage == nil {
		usage = &UsageInfo{Source: "active", UpdatedAt: &now}
	}
	s.addKiloWindowStats(ctx, account, usage)
	return usage, nil
}

func (s *AccountUsageService) fetchKiloUsageInfo(ctx context.Context, account *Account) *UsageInfo {
	now := time.Now()
	usage := &UsageInfo{
		Source:    "active",
		UpdatedAt: &now,
		FiveHour:  &UsageProgress{Utilization: 0},
	}

	if strings.TrimSpace(account.GetKiloToken()) == "" {
		usage.ErrorCode = "configuration"
		usage.Error = "missing Kilo token"
		return usage
	}

	balance, err := fetchKiloBalance(ctx, account)
	if err != nil {
		applyKiloUsageError(usage, err)
		return usage
	}
	usage.KiloBalance = balance
	return usage
}

func (s *AccountUsageService) addKiloWindowStats(ctx context.Context, account *Account, usage *UsageInfo) {
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

func fetchKiloBalance(ctx context.Context, account *Account) (*KiloBalanceInfo, error) {
	token := strings.TrimSpace(account.GetKiloToken())
	if token == "" {
		return nil, fmt.Errorf("missing Kilo token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildKiloBalanceURL(account.GetKiloAPIBaseURL()), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	applyKiloHeaders(req.Header, account)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               kiloUsageUpstreamTimeout,
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
		return nil, &kiloUsageHTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseKiloBalancePayload(body)
}

func parseKiloBalancePayload(body []byte) (*KiloBalanceInfo, error) {
	var payload struct {
		Balance  json.RawMessage `json:"balance"`
		Currency string          `json:"currency"`
		Data     *struct {
			Balance  json.RawMessage `json:"balance"`
			Currency string          `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	balance, ok, err := parseKiloBalanceAmount(payload.Balance)
	if err != nil {
		return nil, err
	}
	currency := strings.TrimSpace(payload.Currency)
	if !ok && payload.Data != nil {
		balance, ok, err = parseKiloBalanceAmount(payload.Data.Balance)
		if err != nil {
			return nil, err
		}
		if currency == "" {
			currency = strings.TrimSpace(payload.Data.Currency)
		}
	}
	if !ok {
		return nil, fmt.Errorf("missing balance in Kilo response")
	}
	return &KiloBalanceInfo{Balance: balance, Currency: currency}, nil
}

func parseKiloBalanceAmount(raw json.RawMessage) (float64, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return 0, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, false, err
	}
	switch v := value.(type) {
	case json.Number:
		amount, err := v.Float64()
		return amount, true, err
	case string:
		amount, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return amount, true, err
	default:
		return 0, false, fmt.Errorf("unsupported Kilo balance type %T", value)
	}
}

func applyKiloUsageError(info *UsageInfo, err error) {
	if info == nil || err == nil {
		return
	}
	info.Error = fmt.Sprintf("kilo balance API error: %v", err)

	var httpErr *kiloUsageHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			info.ErrorCode = errorCodeUnauthenticated
			info.NeedsReauth = true
		case http.StatusTooManyRequests:
			info.ErrorCode = errorCodeRateLimited
		default:
			info.ErrorCode = errorCodeNetworkError
		}
		return
	}
	info.ErrorCode = errorCodeNetworkError
}

func kiloCacheTTL(info *UsageInfo) time.Duration {
	if info == nil {
		return kiloErrorTTL
	}
	if info.ErrorCode != "" || info.Error != "" {
		return kiloErrorTTL
	}
	return apiCacheTTL
}
