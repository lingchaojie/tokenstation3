package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// openaiCCRawAllowedHeaders 是 CC 直转路径专用的客户端 header 透传白名单。
//
// **关键**：不能复用 openaiAllowedHeaders——后者含 Codex 客户端专属 header
// （originator / session_id / x-codex-turn-state / x-codex-turn-metadata / conversation_id），
// 这些在 ChatGPT OAuth 上游是必需的，但透传给 DeepSeek/Kimi/GLM 等第三方
// OpenAI 兼容上游会造成：
//   - 完全忽略（多数友好厂商）——隐性污染上游统计
//   - 400 "unknown parameter"（严格上游）——可见错误
//
// 这里仅放行通用 HTTP header；content-type / authorization / accept 由上下文
// 显式设置，不依赖透传。
//
// 参见决策记录：
// pensieve/short-term/maxims/dont-reuse-shared-headers-whitelist-across-different-upstream-trust-domains
var openaiCCRawAllowedHeaders = map[string]bool{
	"accept-language": true,
	"user-agent":      true,
}

// forwardAsRawChatCompletions 直转客户端的 Chat Completions 请求到上游
// `{base_url}/v1/chat/completions`，**不**做 CC↔Responses 协议转换。
//
// 适用场景：account.platform=openai && account.type=apikey && 上游已被探测确认
// 不支持 /v1/responses 端点（如 DeepSeek/Kimi/GLM/Qwen 等第三方 OpenAI 兼容上游）。
//
// 与 ForwardAsChatCompletions 的关键差异：
//
//   - 不调用 apicompat.ChatCompletionsToResponses，body 仅做模型 ID 改写
//   - 上游 URL 拼到 /v1/chat/completions 而非 /v1/responses
//   - 流式响应 SSE 直接透传给客户端（上游 chunk 已是 CC 格式）
//   - 非流式响应 JSON 直接透传，仅按需提取 usage
//   - 不应用 codex OAuth transform（APIKey 路径无 OAuth）
//   - 不注入 prompt_cache_key（OAuth 专属机制）
//
// 调用入口：openai_gateway_chat_completions.go::ForwardAsChatCompletions
// 在函数顶部按 openai_compat.ShouldUseResponsesAPI 分流。
func (s *OpenAIGatewayService) forwardAsRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse minimal fields needed for routing/billing
	originalModel := gjson.GetBytes(body, "model").String()
	if originalModel == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()

	// 1b. Extract service tier from the raw body before any transformation.
	serviceTier := extractOpenAIServiceTierFromBody(body)

	// 2. Resolve model mapping (same as ForwardAsChatCompletions)
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	grokCacheIdentity := ""
	if account.Platform == PlatformGrok {
		// Resolve before image bridging or other body rewrites so the fallback is
		// anchored to the client's stable conversation prefix.
		grokCacheIdentity = resolveGrokCacheIdentity(c, body, "", upstreamModel)
	}
	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	// 国产模型默认 effort 补充：需要 mappedModel 判定，推迟到 billingModel 算出之后。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)

	// 3. Rewrite model in body (no protocol conversion)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}
	if normalizedBody, normalized := NormalizeGLMOpenAIReasoningEffort(upstreamBody, upstreamModel); normalized {
		upstreamBody = normalizedBody
	}

	// 4. Apply OpenAI fast policy on the CC body
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, upstreamBody)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, policyErr
	}
	upstreamBody = updatedBody

	// Grok Composer does not accept image_url parts directly, but Grok Build
	// can describe the images first. Bridge only this exact failure mode.
	token, tokenKind, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("account %d missing %s credential", account.ID, tokenKind)
	}

	var bridgeUsage OpenAIUsage
	if account.Platform == PlatformGrok {
		bridgedBody, usage, bridged, bridgeErr := s.bridgeGrokComposerImageInputs(ctx, c, account, upstreamBody, token)
		if bridgeErr != nil {
			var failoverErr *UpstreamFailoverError
			if !errors.As(bridgeErr, &failoverErr) && c != nil && c.Writer != nil && !c.Writer.Written() {
				writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", bridgeErr.Error())
			}
			return nil, bridgeErr
		}
		if bridged {
			upstreamBody = bridgedBody
			addOpenAIUsage(&bridgeUsage, usage)
		}
	}

	if clientStream {
		var usageErr error
		upstreamBody, usageErr = ensureOpenAIChatStreamUsage(upstreamBody)
		if usageErr != nil {
			return nil, fmt.Errorf("enable stream usage: %w", usageErr)
		}
	}
	if account.Platform == PlatformGrok {
		upstreamBody, err = stripGrokChatPromptCacheKey(upstreamBody)
		if err != nil {
			return nil, fmt.Errorf("remove Responses-only Grok prompt cache key: %w", err)
		}
	}

	logger.L().Debug("openai chat_completions raw: forwarding without protocol conversion",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// 5. Build and send upstream request via the shared CC pipeline
	targetURL, err := s.rawChatCompletionsURL(account)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, grokChatRawEndpoint)
	customUA := account.GetOpenAIUserAgent()
	if customUA == "" && account.IsGrokOAuth() {
		customUA = "sub2api-grok/1.0"
	}
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, upstreamBody, clientStream, token, customUA, grokCacheIdentity)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 7. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if account.Platform == PlatformGrok {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			if s.shouldFailoverUpstreamError(resp.StatusCode) {
				return nil, &UpstreamFailoverError{
					StatusCode:             resp.StatusCode,
					ResponseBody:           respBody,
					ResponseHeaders:        resp.Header.Clone(),
					RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
				}
			}
			return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
		}
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
	}

	if account.Platform == PlatformGrok {
		s.updateGrokUsageFromResponse(ctx, account, resp.Header, resp.StatusCode)
	}

	// 8. Forward response
	var result *OpenAIForwardResult
	var forwardErr error
	if clientStream {
		result, forwardErr = s.streamRawChatCompletions(ctx, c, resp, account, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime, len(body))
	} else {
		result, forwardErr = s.bufferRawChatCompletions(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	}
	if result != nil {
		addOpenAIUsage(&result.Usage, bridgeUsage)
		result.UpstreamEndpoint = grokChatRawEndpoint
	}
	return result, forwardErr
}

func (s *OpenAIGatewayService) rawChatCompletionsURL(account *Account) (string, error) {
	if account.Platform == PlatformGrok {
		targetURL, err := buildGrokChatCompletionsURL(account, s.cfg)
		if err != nil {
			return "", fmt.Errorf("invalid grok base_url: %w", err)
		}
		return targetURL, nil
	}

	return s.openAIChatCompletionsTargetURL(account)
}

// streamRawChatCompletions 透传上游 CC SSE 流到客户端，并提取 usage（包括
// 末尾 [DONE] 之前的 chunk 中的 usage 字段，按 OpenAI CC 协议）。
//
// usage 字段仅在客户端请求 stream_options.include_usage=true 时出现于上游响应中。
// 网关会对上游强制打开 include_usage 以保证计费完整，并原样向下游透传 usage，
// 让级联代理或下游计费系统也能拿到完整用量。
func (s *OpenAIGatewayService) streamRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	resp *http.Response,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
	requestBodyLen int,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
	scanner := s.newUpstreamSSEScanner(resp.Body)

	var usage OpenAIUsage
	var firstTokenMs *int
	clientDisconnected := false
	clientOutputStarted := false
	pendingLines := make([]string, 0, 8)
	refusalDetector := newOpenAIChatSilentRefusalDetector(requestBodyLen)

	writeCapturedLine := func(line string) {
		captureWebChatStreamString(ctx, line+"\n")
		if clientDisconnected {
			return
		}
		if _, werr := c.Writer.WriteString(line + "\n"); werr != nil {
			clientDisconnected = true
			logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
				zap.Error(werr),
				zap.String("request_id", requestID),
			)
		}
	}

	releasePendingLines := func() {
		if !clientDisconnected {
			writeStreamHeaders()
		}
		for _, pending := range pendingLines {
			writeCapturedLine(pending)
		}
		pendingLines = pendingLines[:0]
		clientOutputStarted = true
	}

	writeLine := func(line string) {
		if !clientOutputStarted && !refusalDetector.ShouldReleaseClientOutput() {
			pendingLines = append(pendingLines, line)
			return
		}
		if !clientOutputStarted {
			releasePendingLines()
		}
		writeCapturedLine(line)
	}

	for scanner.Scan() {
		line := scanner.Text()
		refusalDetector.ObserveSSELine(line)
		if payload, ok := extractOpenAISSEDataLine(line); ok {
			trimmedPayload := strings.TrimSpace(payload)
			if trimmedPayload != "[DONE]" {
				usageOnlyChunk := isOpenAIChatUsageOnlyStreamChunk(payload)
				mergeCCStreamUsage(&usage, payload)
				if firstTokenMs == nil && !usageOnlyChunk {
					elapsed := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &elapsed
				}
			}
		}

		writeLine(line)
		if line == "" {
			if !clientDisconnected && clientOutputStarted {
				c.Writer.Flush()
			}
			continue
		}
		if !clientDisconnected && clientOutputStarted {
			c.Writer.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions raw: stream read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	} else if !clientOutputStarted {
		if refusalDetector.IsSilentRefusal() {
			if !clientDisconnected {
				return nil, newOpenAISilentRefusalFailoverError(c, account, requestID)
			}
		} else if len(pendingLines) > 0 {
			releasePendingLines()
			if !clientDisconnected {
				c.Writer.Flush()
			}
		}
	}

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          true,
		Duration:        time.Since(startTime),
		FirstTokenMs:    firstTokenMs,
	}, nil
}

// ensureOpenAIChatStreamUsage 确保 raw Chat Completions 流式请求会让上游返回 usage。
// usage 也会继续向下游透传，支持级联代理和下游计费系统。
func ensureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	updated, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return body, err
	}
	return updated, nil
}

func isOpenAIChatUsageOnlyStreamChunk(payload string) bool {
	if strings.TrimSpace(payload) == "" {
		return false
	}
	if !gjson.Get(payload, "usage").Exists() {
		return false
	}
	choices := gjson.Get(payload, "choices")
	return choices.Exists() && choices.IsArray() && len(choices.Array()) == 0
}

// mergeCCStreamUsage applies only fields that are valid and present in the
// current chunk. Some compatibility providers repeat usage or split it across
// chunks; a later empty/malformed/partial object must not erase an earlier
// valid snapshot.
func mergeCCStreamUsage(dst *OpenAIUsage, payload string) bool {
	parsed, sawUsageObject := parseCCUsageFromGJSON(gjson.Get(payload, "usage"))
	if !sawUsageObject || !parsed.hasValidFields() {
		return false
	}
	parsed.mergeInto(dst)
	return true
}

// extractCCUsageFromJSONBytes extracts usage from a native Chat Completions
// response. Keep this separate from the Responses parser: compatibility
// providers occasionally return both naming dialects, and an explicitly
// present CC value (including zero) is authoritative on a CC endpoint.
func extractCCUsageFromJSONBytes(body []byte) (OpenAIUsage, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return OpenAIUsage{}, false
	}
	parsed, ok := parseCCUsageFromGJSON(gjson.GetBytes(body, "usage"))
	return parsed.Usage, ok
}

type parsedCCUsage struct {
	Usage OpenAIUsage

	inputTokensSet         bool
	outputTokensSet        bool
	cacheReadTokensSet     bool
	cacheCreationTokensSet bool
	imageOutputTokensSet   bool
	kiroCreditsSet         bool

	promptAudioTokens           int
	promptAudioTokensSet        bool
	outputAudioTokens           int
	outputAudioTokensSet        bool
	reasoningTokens             int
	reasoningTokensSet          bool
	acceptedPredictionTokens    int
	acceptedPredictionTokensSet bool
	rejectedPredictionTokens    int
	rejectedPredictionTokensSet bool
}

func parseCCUsageFromGJSON(value gjson.Result) (parsedCCUsage, bool) {
	if !value.Exists() || !value.IsObject() {
		return parsedCCUsage{}, false
	}

	inputTokens, inputTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("prompt_tokens"),
		value.Get("input_tokens"),
	)
	outputTokens, outputTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens"),
		value.Get("output_tokens"),
	)
	cacheReadTokens, cacheReadTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("prompt_tokens_details.cached_tokens"),
		value.Get("input_tokens_details.cached_tokens"),
		value.Get("cache_read_input_tokens"),
		value.Get("cache_read_tokens"),
		value.Get("cached_tokens"),
	)
	cacheCreationTokens, cacheCreationTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("prompt_tokens_details.cache_write_tokens"),
		value.Get("prompt_tokens_details.cache_creation_tokens"),
		value.Get("input_tokens_details.cache_write_tokens"),
		value.Get("input_tokens_details.cache_creation_tokens"),
		value.Get("cache_write_tokens"),
		value.Get("cache_creation_input_tokens"),
		value.Get("cache_write_input_tokens"),
		value.Get("cache_creation_tokens"),
	)
	imageOutputTokens, imageOutputTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens_details.image_tokens"),
		value.Get("output_tokens_details.image_tokens"),
	)
	promptAudioTokens, promptAudioTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("prompt_tokens_details.audio_tokens"),
		value.Get("input_tokens_details.audio_tokens"),
	)
	outputAudioTokens, outputAudioTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens_details.audio_tokens"),
		value.Get("output_tokens_details.audio_tokens"),
	)
	reasoningTokens, reasoningTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens_details.reasoning_tokens"),
		value.Get("output_tokens_details.reasoning_tokens"),
	)
	acceptedPredictionTokens, acceptedPredictionTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens_details.accepted_prediction_tokens"),
		value.Get("output_tokens_details.accepted_prediction_tokens"),
	)
	rejectedPredictionTokens, rejectedPredictionTokensSet := nonNegativeFirstValidGJSONInt(
		value.Get("completion_tokens_details.rejected_prediction_tokens"),
		value.Get("output_tokens_details.rejected_prediction_tokens"),
	)
	kiroCredits, kiroCreditsSet := nonNegativeFirstValidGJSONFloat(
		value.Get("_sub2api_kiro_credits"),
		value.Get("kiro_credits"),
		value.Get("kiroCredits"),
		value.Get("credits"),
		value.Get("creditsUsed"),
		value.Get("creditUsage"),
		value.Get("consumedCredits"),
	)

	parsed := parsedCCUsage{
		Usage: OpenAIUsage{
			InputTokens:              inputTokens,
			OutputTokens:             outputTokens,
			CacheReadInputTokens:     cacheReadTokens,
			CacheCreationInputTokens: cacheCreationTokens,
			ImageOutputTokens:        imageOutputTokens,
			KiroCredits:              kiroCredits,
		},
		inputTokensSet:              inputTokensSet,
		outputTokensSet:             outputTokensSet,
		cacheReadTokensSet:          cacheReadTokensSet,
		cacheCreationTokensSet:      cacheCreationTokensSet,
		imageOutputTokensSet:        imageOutputTokensSet,
		kiroCreditsSet:              kiroCreditsSet,
		promptAudioTokens:           promptAudioTokens,
		promptAudioTokensSet:        promptAudioTokensSet,
		outputAudioTokens:           outputAudioTokens,
		outputAudioTokensSet:        outputAudioTokensSet,
		reasoningTokens:             reasoningTokens,
		reasoningTokensSet:          reasoningTokensSet,
		acceptedPredictionTokens:    acceptedPredictionTokens,
		acceptedPredictionTokensSet: acceptedPredictionTokensSet,
		rejectedPredictionTokens:    rejectedPredictionTokens,
		rejectedPredictionTokensSet: rejectedPredictionTokensSet,
	}
	return parsed, true
}

func (parsed parsedCCUsage) hasValidFields() bool {
	return parsed.inputTokensSet || parsed.outputTokensSet || parsed.cacheReadTokensSet ||
		parsed.cacheCreationTokensSet || parsed.imageOutputTokensSet || parsed.kiroCreditsSet ||
		parsed.promptAudioTokensSet || parsed.outputAudioTokensSet || parsed.reasoningTokensSet ||
		parsed.acceptedPredictionTokensSet || parsed.rejectedPredictionTokensSet
}

// chatUsage projects the tolerant raw parser result back into the canonical
// Chat Completions shape consumed by protocol bridges. This keeps client-visible
// usage aligned with billing even when the provider uses top-level aliases or
// supplies a malformed canonical field alongside a valid fallback.
func (parsed parsedCCUsage) chatUsage() *apicompat.ChatUsage {
	if !parsed.hasValidFields() {
		return nil
	}
	usage := &apicompat.ChatUsage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
	}
	if parsed.cacheReadTokensSet || parsed.cacheCreationTokensSet || parsed.promptAudioTokensSet {
		usage.PromptTokensDetails = &apicompat.ChatTokenDetails{
			CachedTokens:     parsed.Usage.CacheReadInputTokens,
			CacheWriteTokens: parsed.Usage.CacheCreationInputTokens,
			AudioTokens:      parsed.promptAudioTokens,
		}
	}
	if parsed.imageOutputTokensSet || parsed.outputAudioTokensSet || parsed.reasoningTokensSet ||
		parsed.acceptedPredictionTokensSet || parsed.rejectedPredictionTokensSet {
		usage.CompletionTokensDetails = &apicompat.ChatTokenDetails{
			ImageTokens:              parsed.Usage.ImageOutputTokens,
			AudioTokens:              parsed.outputAudioTokens,
			ReasoningTokens:          parsed.reasoningTokens,
			AcceptedPredictionTokens: parsed.acceptedPredictionTokens,
			RejectedPredictionTokens: parsed.rejectedPredictionTokens,
		}
	}
	return usage
}

func (parsed parsedCCUsage) mergeInto(dst *OpenAIUsage) {
	if dst == nil {
		return
	}
	if parsed.inputTokensSet {
		dst.InputTokens = parsed.Usage.InputTokens
	}
	if parsed.outputTokensSet {
		dst.OutputTokens = parsed.Usage.OutputTokens
	}
	if parsed.cacheReadTokensSet {
		dst.CacheReadInputTokens = parsed.Usage.CacheReadInputTokens
	}
	if parsed.cacheCreationTokensSet {
		dst.CacheCreationInputTokens = parsed.Usage.CacheCreationInputTokens
	}
	if parsed.imageOutputTokensSet {
		dst.ImageOutputTokens = parsed.Usage.ImageOutputTokens
	}
	if parsed.kiroCreditsSet {
		dst.KiroCredits = parsed.Usage.KiroCredits
	}
}

func (parsed parsedCCUsage) mergeIntoParsed(dst *parsedCCUsage) {
	if dst == nil {
		return
	}
	parsed.mergeInto(&dst.Usage)
	if parsed.inputTokensSet {
		dst.inputTokensSet = true
	}
	if parsed.outputTokensSet {
		dst.outputTokensSet = true
	}
	if parsed.cacheReadTokensSet {
		dst.cacheReadTokensSet = true
	}
	if parsed.cacheCreationTokensSet {
		dst.cacheCreationTokensSet = true
	}
	if parsed.imageOutputTokensSet {
		dst.imageOutputTokensSet = true
	}
	if parsed.kiroCreditsSet {
		dst.kiroCreditsSet = true
	}
	if parsed.promptAudioTokensSet {
		dst.promptAudioTokens = parsed.promptAudioTokens
		dst.promptAudioTokensSet = true
	}
	if parsed.outputAudioTokensSet {
		dst.outputAudioTokens = parsed.outputAudioTokens
		dst.outputAudioTokensSet = true
	}
	if parsed.reasoningTokensSet {
		dst.reasoningTokens = parsed.reasoningTokens
		dst.reasoningTokensSet = true
	}
	if parsed.acceptedPredictionTokensSet {
		dst.acceptedPredictionTokens = parsed.acceptedPredictionTokens
		dst.acceptedPredictionTokensSet = true
	}
	if parsed.rejectedPredictionTokensSet {
		dst.rejectedPredictionTokens = parsed.rejectedPredictionTokens
		dst.rejectedPredictionTokensSet = true
	}
}

func nonNegativeFirstValidGJSONInt(values ...gjson.Result) (int, bool) {
	for _, value := range values {
		if !value.Exists() || value.Type != gjson.Number {
			continue
		}
		n, err := strconv.Atoi(value.Raw)
		if err != nil || n < 0 {
			continue
		}
		return n, true
	}
	return 0, false
}

func nonNegativeFirstValidGJSONFloat(values ...gjson.Result) (float64, bool) {
	for _, value := range values {
		if !value.Exists() || value.Type != gjson.Number {
			continue
		}
		n, err := strconv.ParseFloat(value.Raw, 64)
		if err != nil || n < 0 {
			continue
		}
		return n, true
	}
	return 0, false
}

// bufferRawChatCompletions 透传上游 CC 非流式 JSON 响应。
func (s *OpenAIGatewayService) bufferRawChatCompletions(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			writeChatCompletionsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	var usage OpenAIUsage
	if parsedUsage, ok := extractCCUsageFromJSONBytes(respBody); ok {
		usage = parsedUsage
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(respBody)

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// buildOpenAIChatCompletionsURL 拼接上游 Chat Completions 端点 URL。
//
//   - base 已是 /chat/completions：原样返回
//   - base 以 /v1 结尾：追加 /chat/completions
//   - base 以其他版本段结尾（如 /v4）：追加 /chat/completions
//   - 其他情况：追加 /v1/chat/completions
//
// 与 buildOpenAIResponsesURL 是姐妹函数。
func buildOpenAIChatCompletionsURL(base string) string {
	return buildOpenAIEndpointURL(base, "/v1/chat/completions")
}
