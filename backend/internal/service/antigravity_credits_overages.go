package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	// creditsExhaustedKey 是 model_rate_limits 中标记积分耗尽的特殊 key。
	// 与普通模型限流完全同构：通过 SetModelRateLimit / isRateLimitActiveForKey 读写。
	creditsExhaustedKey      = "AICredits"
	creditsExhaustedDuration = 5 * time.Hour
)

type antigravity429Category string

const (
	antigravity429Unknown        antigravity429Category = "unknown"
	antigravity429RateLimited    antigravity429Category = "rate_limited"
	antigravity429QuotaExhausted antigravity429Category = "quota_exhausted"
)

var (
	antigravityQuotaExhaustedKeywords = []string{
		"quota_exhausted",
		"quota exhausted",
	}

	creditsExhaustedKeywords = []string{
		"google_one_ai",
		"insufficient credit",
		"insufficient credits",
		"not enough credit",
		"not enough credits",
		"credit exhausted",
		"credits exhausted",
		"credit balance",
		"minimumcreditamountforusage",
		"minimum credit amount for usage",
		"minimum credit",
		"resource has been exhausted",
	}
)

// isCreditsExhausted 检查账号的 AICredits 限流 key 是否生效（积分是否耗尽）。
func (a *Account) isCreditsExhausted() bool {
	if a == nil {
		return false
	}
	return a.isRateLimitActiveForKey(creditsExhaustedKey)
}

// setCreditsExhausted 标记账号积分耗尽：写入 model_rate_limits["AICredits"] + 更新缓存。
func (s *AntigravityGatewayService) setCreditsExhausted(ctx context.Context, account *Account) {
	if account == nil || account.ID == 0 {
		return
	}
	resetAt := time.Now().Add(creditsExhaustedDuration)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, creditsExhaustedKey, resetAt); err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "set credits exhausted failed: account=%d err=%v", account.ID, err)
		return
	}
	s.updateAccountModelRateLimitInCache(ctx, account, creditsExhaustedKey, resetAt)
	logger.LegacyPrintf("service.antigravity_gateway", "credits_exhausted_marked account=%d reset_at=%s",
		account.ID, resetAt.UTC().Format(time.RFC3339))
}

// clearCreditsExhausted 清除账号的 AICredits 限流 key。
func (s *AntigravityGatewayService) clearCreditsExhausted(ctx context.Context, account *Account) {
	if account == nil || account.ID == 0 || account.Extra == nil {
		return
	}
	rawLimits, ok := account.Extra[modelRateLimitsKey].(map[string]any)
	if !ok {
		return
	}
	if _, exists := rawLimits[creditsExhaustedKey]; !exists {
		return
	}
	delete(rawLimits, creditsExhaustedKey)
	account.Extra[modelRateLimitsKey] = rawLimits
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		modelRateLimitsKey: rawLimits,
	}); err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "clear credits exhausted failed: account=%d err=%v", account.ID, err)
	}
}

// classifyAntigravity429 将 Antigravity 的 429 响应归类为配额耗尽、限流或未知。
func classifyAntigravity429(body []byte) antigravity429Category {
	if len(body) == 0 {
		return antigravity429Unknown
	}
	lowerBody := strings.ToLower(string(body))
	for _, keyword := range antigravityQuotaExhaustedKeywords {
		if strings.Contains(lowerBody, keyword) {
			return antigravity429QuotaExhausted
		}
	}
	if info := parseAntigravitySmartRetryInfo(body); info != nil && !info.IsModelCapacityExhausted {
		return antigravity429RateLimited
	}
	return antigravity429Unknown
}

// injectEnabledCreditTypes 在已序列化的 v1internal JSON body 中注入 AI Credits 类型。
func injectEnabledCreditTypes(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	payload["enabledCreditTypes"] = []string{"GOOGLE_ONE_AI"}
	result, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return result
}

// resolveCreditsOveragesModelKey 解析当前请求对应的 overages 状态模型 key。
func resolveCreditsOveragesModelKey(ctx context.Context, account *Account, upstreamModelName, requestedModel string) string {
	modelKey := strings.TrimSpace(upstreamModelName)
	if modelKey != "" {
		return modelKey
	}
	if account == nil {
		return ""
	}
	modelKey = resolveFinalAntigravityModelKey(ctx, account, requestedModel)
	if strings.TrimSpace(modelKey) != "" {
		return modelKey
	}
	return resolveAntigravityModelKey(requestedModel)
}

// shouldMarkCreditsExhausted 判断一次 credits 请求失败是否应标记为 credits 耗尽。
func shouldMarkCreditsExhausted(resp *http.Response, respBody []byte, reqErr error) bool {
	if reqErr != nil || resp == nil {
		return false
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
		return false
	}
	// 注意：不再检查 isURLLevelRateLimit。此函数仅在积分重试失败后调用，
	// 如果注入 enabledCreditTypes 后仍返回 "Resource has been exhausted"，
	// 说明积分也已耗尽，应该标记。clearCreditsExhausted 会在后续成功时自动清除。
	if info := parseAntigravitySmartRetryInfo(respBody); info != nil {
		return false
	}
	bodyLower := strings.ToLower(string(respBody))
	for _, keyword := range creditsExhaustedKeywords {
		if strings.Contains(bodyLower, keyword) {
			return true
		}
	}
	return false
}

type creditsOveragesRetryResult struct {
	handled bool
	resp    *http.Response
	failure *UpstreamFailoverError
}

// attemptCreditsOveragesRetry 在确认免费配额耗尽后，尝试注入 AI Credits 继续请求。
func (s *AntigravityGatewayService) attemptCreditsOveragesRetry(
	p antigravityRetryLoopParams,
	baseURL string,
	modelName string,
	waitDuration time.Duration,
	originalStatusCode int,
	respBody []byte,
) *creditsOveragesRetryResult {
	creditsBody := injectEnabledCreditTypes(p.body)
	if creditsBody == nil {
		return &creditsOveragesRetryResult{handled: false}
	}
	modelKey := resolveCreditsOveragesModelKey(p.ctx, p.account, modelName, p.requestedModel)
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=429 credit_overages_retry model=%s account=%d (injecting enabledCreditTypes)",
		p.prefix, modelKey, p.account.ID)

	creditsReq, err := antigravity.NewAPIRequestWithURL(p.ctx, baseURL, p.action, p.accessToken, creditsBody)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s account=%d build_request_err=%v",
			p.prefix, modelKey, p.account.ID, err)
		return &creditsOveragesRetryResult{handled: true}
	}

	prepareAntigravityCaptureAttempt(p, creditsReq)
	creditsResp, err := p.httpUpstream.Do(creditsReq, p.proxyURL, p.account.ID, p.account.Concurrency)
	wrapAntigravityCaptureResponse(p, creditsResp)
	if err == nil && creditsResp != nil && creditsResp.StatusCode < 400 {
		s.clearCreditsExhausted(p.ctx, p.account)
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d credit_overages_success model=%s account=%d",
			p.prefix, creditsResp.StatusCode, modelKey, p.account.ID)
		return &creditsOveragesRetryResult{handled: true, resp: creditsResp}
	}

	detached := s.handleCreditsRetryFailure(p.ctx, p.prefix, modelKey, p.account, creditsResp, err)
	var failure *UpstreamFailoverError
	if detached != nil {
		body, _ := io.ReadAll(detached.Body)
		detached.Body = io.NopCloser(bytes.NewReader(body))
		failure = newProviderHTTPError(p.account, detached, body, false)
	} else {
		// The credits request was the latest real provider attempt but produced no
		// HTTP response. Drop its request-only bridge and carry a non-HTTP failure
		// marker so later account-switch logic cannot pair the initial quota 429
		// with this credits request.
		_, _ = takeCaptureResult(p.c)
		failure = newProviderHTTPError(p.account, nil, nil, false)
		if err != nil {
			failure.ClientMessage = sanitizeUpstreamErrorMessage(err.Error())
		}
	}
	return &creditsOveragesRetryResult{handled: true, failure: failure}
}

func (s *AntigravityGatewayService) handleCreditsRetryFailure(
	ctx context.Context,
	prefix string,
	modelKey string,
	account *Account,
	creditsResp *http.Response,
	reqErr error,
) *http.Response {
	var creditsRespBody []byte
	creditsStatusCode := 0
	var detached *http.Response
	if creditsResp != nil {
		creditsStatusCode = creditsResp.StatusCode
		if creditsResp.Body != nil {
			detached, creditsRespBody, _ = readAntigravityRetryResponse(creditsResp, 64<<10)
		}
	}

	if shouldMarkCreditsExhausted(creditsResp, creditsRespBody, reqErr) && account != nil {
		s.setCreditsExhausted(ctx, account)
		logger.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s account=%d marked_exhausted=true status=%d body=%s",
			prefix, modelKey, account.ID, creditsStatusCode, truncateForLog(creditsRespBody, 200))
		return detached
	}
	if account != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s account=%d marked_exhausted=false status=%d err=%v body=%s",
			prefix, modelKey, account.ID, creditsStatusCode, reqErr, truncateForLog(creditsRespBody, 200))
	}
	return detached
}
