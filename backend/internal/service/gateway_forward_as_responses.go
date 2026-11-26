package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ForwardAsResponses accepts an OpenAI Responses API request body, converts it
// to Anthropic Messages format, forwards to the Anthropic upstream, and converts
// the response back to Responses format. This enables OpenAI Responses API
// clients to access Anthropic models through Anthropic platform groups.
//
// The method follows the same pattern as OpenAIGatewayService.ForwardAsAnthropic
// but in reverse direction: Responses → Anthropic upstream → Responses.
func (s *GatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (result *ForwardResult, err error) {
	defer func() {
		result, err = finalizeForwardResultWithUsage(c, result, err)
	}()
	startTime := time.Now()
	beginCaptureAttempt(c)
	captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil && CaptureMayApplyFor(c, string(account.Platform))

	normalizedBody, normalized, err := normalizeOpenAIResponsesLegacyIngressWithConfig(s.cfg, body)
	if err != nil {
		return nil, err
	}
	if normalized {
		body = normalizedBody
	}

	// 1. Lower Codex client-side tools to function tools understood by Anthropic.
	adaptedBody, clientToolMapping, err := adaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	// 2. Parse Responses request
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	clientStream := responsesReq.Stream
	isCompaction := account != nil && account.IsKiro() && apicompat.HasCompactionTrigger(&responsesReq)

	// 3. Convert Responses → Anthropic
	// Resolve the final upstream model before model-specific conversion.
	mappedModel := originalModel
	if account.Platform == PlatformKiro {
		if next := account.GetMappedModel(originalModel); next != "" {
			mappedModel = next
		}
	} else if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mappedModel = account.GetMappedModel(originalModel)
	}
	if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		normalized := normalizeVertexAnthropicModelID(claude.NormalizeModelID(originalModel))
		if normalized != originalModel {
			mappedModel = normalized
		}
	} else if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		normalized := claude.NormalizeModelID(originalModel)
		if normalized != originalModel {
			mappedModel = normalized
		}
	}
	if isCompaction {
		if compactModel, matched := account.ResolveCompactMappedModel(originalModel); matched {
			mappedModel = compactModel
		}
	}
	if err := validateClaude55RequestForAccount(body, account, mappedModel); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	responsesReq.Model = mappedModel
	anthropicReq, err := apicompat.ResponsesToAnthropicRequestWithOptions(&responsesReq, apicompat.ResponsesToAnthropicOptions{EnableCompaction: account != nil && account.IsKiro()})
	if err != nil {
		if isClaude55SignedThinkingModel(mappedModel) {
			writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 3. Force upstream streaming (Anthropic works best with streaming)
	anthropicReq.Stream = true
	reqStream := true
	if isCompaction {
		anthropicReq.ToolChoice = json.RawMessage(`{"type":"none"}`)
		if anthropicReq.MaxTokens < compactionMinMaxTokens {
			anthropicReq.MaxTokens = compactionMinMaxTokens
		}
		anthropicReq.Thinking = nil
		anthropicReq.OutputConfig = nil
	}

	logger.L().Debug("gateway forward_as_responses: model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("client_stream", clientStream),
	)

	// 5. Marshal Anthropic request body
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 6. Apply Claude Code mimicry for OAuth accounts (non-Claude-Code endpoints).
	// OpenAI Responses 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
	// 必须完整执行 /v1/messages 主路径上的伪装链路（system 重写 + normalize + metadata 注入），
	// 否则会被 Anthropic 判为第三方应用并扣 extra usage。
	// 见 applyClaudeCodeOAuthMimicryToBody 的 godoc。
	isClaudeCode := false
	shouldMimicClaudeCode := shouldMimicClaudeCodeForAccount(account, isClaudeCode)

	if shouldMimicClaudeCode {
		anthropicBody = s.applyClaudeCodeOAuthMimicryToBody(ctx, c, account, anthropicBody, anthropicReq.System, mappedModel)
	}

	// 7. Enforce cache_control block limit
	anthropicBody = enforceCacheControlLimit(anthropicBody)

	forwardedBody := anthropicBody
	kiroDirectMode := isKiroDirectModeAccount(account)
	var resp *http.Response
	finishCapture := func() {}
	if kiroDirectMode {
		if captureEnabled {
			setCapturePlatform(c, string(account.Platform))
			ctx = withCaptureUpstreamRequestContext(ctx, c, s.cfg.Gateway.Capture.MaxBodyBytes)
		}
		var group *Group
		if parsed != nil {
			group = parsed.Group
		}
		resp, _, err = s.openKiroAnthropicStreamResponse(ctx, c, account, parsed, anthropicBody, mappedModel, originalModel, c.Request.Header, group)
		if err != nil {
			var failoverErr *UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if failoverErr.Platform == "" {
					failoverErr.Platform = account.Platform
				}
				s.submitWebChatTerminalCapture(ctx, c, account, failoverErr)
				return nil, failoverErr
			}
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream request failed")
			return nil, fmt.Errorf("upstream request failed: %s", safeErr)
		}
	} else {
		// 8. Get access token
		token, tokenType, err := s.GetAccessToken(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("get access token: %w", err)
		}

		// 9. Get proxy URL
		proxyURL := ""
		if account.ProxyID != nil && account.Proxy != nil {
			proxyURL = account.Proxy.URL()
		}

		for retriedRotatedToken := false; ; {
			// Build and send each attempt with a fresh archive capture.
			upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, reqStream)
			upstreamReq, finalBody, err := s.buildUpstreamRequest(upstreamCtx, c, account, anthropicBody, token, tokenType, mappedModel, reqStream, shouldMimicClaudeCode)
			releaseUpstreamCtx()
			if err != nil {
				return nil, fmt.Errorf("build upstream request: %w", err)
			}

			forwardedBody = finalBody

			// 11. Send request
			s.captureOutboundRequest(c, account, upstreamReq, forwardedBody)
			resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
			finishCapture = s.beginGatewayCaptureResponse(c, account, resp)
			if err != nil {
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				finishCapture()
				return nil, s.handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{
					UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
				})
			}
			if resp.StatusCode == http.StatusUnauthorized && !retriedRotatedToken {
				if rotatedToken := s.rotatedClaudeAccessToken(ctx, account, token); rotatedToken != "" {
					_ = resp.Body.Close()
					finishCapture()
					AbortCaptureAttempt(c)
					token = rotatedToken
					retriedRotatedToken = true
					continue
				}
			}
			break
		}
	}
	defer func() { _ = resp.Body.Close() }()
	reasoningEffort := NormalizeClaudeOutputEffort(gjson.GetBytes(forwardedBody, "output_config.effort").String())
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, forwardedBody, mappedModel)
	if isCompaction {
		reasoningEffort = nil
	}

	// 12. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, responseTruncated, responseComplete, _ := s.readWebChatUpstreamErrorBody(ctx, resp)
		_ = resp.Body.Close()
		finishCapture()
		if responseTruncated || !responseComplete {
			markCaptureResultTruncated(c)
		}
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		// Apply account policies even for statuses outside the default failover list.
		shouldDisable := false
		if s.rateLimitService != nil {
			shouldDisable = s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
		}
		if shouldDisable || s.shouldFailoverUpstreamError(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				ProxyID:            opsUpstreamProxyID(account),
				ProxyName:          opsUpstreamProxyName(account),
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.submitWebChatFinalGatewayErrorCapture(ctx, c, account, originalModel, mappedModel, "/v1/messages", clientStream, resp, respBody, responseComplete)
			return nil, &UpstreamFailoverError{
				StatusCode:                resp.StatusCode,
				ResponseBody:              respBody,
				RequestHeaders:            captureRequestHeadersFromResponse(resp),
				ResponseHeaders:           resp.Header.Clone(),
				UpstreamEndpoint:          captureEndpointFromResponse(resp),
				Platform:                  string(account.Platform),
				HasUpstreamHTTPResponse:   true,
				CaptureResponseIncomplete: !responseComplete,
				RetryableOnSameAccount:    !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}

		// Non-failover error: return Responses-formatted error to client
		s.submitWebChatFinalGatewayErrorCapture(ctx, c, account, originalModel, mappedModel, "/v1/messages", clientStream, resp, respBody, responseComplete)
		if message, ok := kiroContextLengthClientMessage(resp.StatusCode, respBody); kiroDirectMode && ok {
			writeKiroResponsesError(c, http.StatusBadRequest, "invalid_request_error", message, isCompaction)
		} else {
			writeResponsesError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		}
		failure := newTerminalProviderHTTPError(account, resp, respBody)
		failure.CaptureResponseIncomplete = !responseComplete
		return nil, failure
	}

	// 13. Handle normal response (convert Anthropic → Responses)
	var forwardResult *ForwardResult
	var handleErr error
	writerSizeBeforeStream := c.Writer.Size()
	if isCompaction {
		forwardResult, handleErr = s.handleResponsesCompactionResponse(resp, c, originalModel, mappedModel, reasoningEffort, startTime, clientStream, kiroDirectMode)
	} else if clientStream {
		forwardResult, handleErr = s.handleResponsesStreamingResponse(resp, c, originalModel, mappedModel, reasoningEffort, startTime, kiroDirectMode, clientToolMapping)
	} else {
		forwardResult, handleErr = s.handleResponsesBufferedStreamingResponse(resp, c, originalModel, mappedModel, reasoningEffort, startTime, kiroDirectMode, clientToolMapping)
	}

	if failure := kiroStreamFailureFromResponse(resp, handleErr); failure != nil {
		if forwardResult == nil {
			err := s.handleKiroStreamFailure(ctx, c, account, mappedModel, anthropicBody, failure, func(c *gin.Context, status int, code, message string) {
				writeKiroResponsesError(c, status, code, message, isCompaction)
			})
			var failover *UpstreamFailoverError
			if errors.As(err, &failover) {
				s.submitWebChatTerminalCapture(ctx, c, account, failover)
			}
			return nil, err
		}
		failure.applyPartialUsage(&forwardResult.Usage)
		return finalizeForwardResult(c, forwardResult), failure
	}
	var sseErr *sseStreamErrorEventError
	if errors.As(handleErr, &sseErr) {
		failure := s.sseStreamErrorEventFailover(ctx, c, account, resp, sseErr.RawData, mappedModel, c.Writer.Size() != writerSizeBeforeStream || (forwardResult != nil && forwardResult.ClientDisconnect))
		failure.RequestHeaders = captureRequestHeadersFromResponse(resp)
		failure.ResponseHeaders = resp.Header.Clone()
		failure.UpstreamEndpoint = captureEndpointFromResponse(resp)
		failure.Platform = account.Platform
		failure.HasUpstreamHTTPResponse = true
		failure.UpstreamHTTPStatus = resp.StatusCode
		failure.CaptureResponseIncomplete = forwardResult == nil || !forwardResult.CaptureResponseComplete
		if forwardResult == nil {
			finishCapture()
			return nil, failure
		}
		handleErr = failure
	}
	if errors.Is(handleErr, errProviderStreamIdleTimeout) && s.rateLimitService != nil && (ctx == nil || ctx.Err() == nil) && (forwardResult == nil || !forwardResult.ClientDisconnect) {
		s.rateLimitService.HandleStreamTimeout(ctx, account, mappedModel)
	}

	finishCapture()
	if handleErr != nil && forwardResult == nil {
		forwardResult = failedForwardResultForError(c, resp, originalModel, mappedModel, clientStream, startTime, handleErr)
	}
	return finalizeForwardResult(c, forwardResult), handleErr
}

func adaptResponsesClientToolsForAnthropic(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	additionalToolsChanged, err := liftResponsesAdditionalTools(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}

	mapping, changed, err := apicompat.AdaptResponsesClientTools(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	changed = changed || additionalToolsChanged
	if !changed {
		return body, mapping, nil
	}
	rebuilt, err := json.Marshal(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	return rebuilt, mapping, nil
}

func liftResponsesAdditionalTools(requestBody map[string]any) (bool, error) {
	input, ok := requestBody["input"].([]any)
	if !ok {
		return false, nil
	}

	tools, _ := requestBody["tools"].([]any)
	kept := make([]any, 0, len(input))
	changed := false
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(item["type"])) != "additional_tools" {
			kept = append(kept, raw)
			continue
		}
		additional, ok := item["tools"].([]any)
		if !ok {
			return false, fmt.Errorf("additional_tools.tools must be an array")
		}
		tools = append(tools, additional...)
		changed = true
	}
	if !changed {
		return false, nil
	}
	requestBody["tools"] = tools
	requestBody["input"] = kept
	return true, nil
}

// ExtractResponsesReasoningEffortFromBody reads Responses API reasoning.effort
// and normalizes it for usage logging.
func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		return nil
	}
	model := firstNonEmpty(modelCandidates...)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	normalized := normalizeOpenAIReasoningEffortForModel(raw, model)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func mergeAnthropicUsage(dst *ClaudeUsage, src apicompat.AnthropicUsage) {
	if dst == nil {
		return
	}

	cacheReadTokens := max(src.CacheReadInputTokens, 0)
	hasCacheReadTokens := src.CacheReadInputTokensPresent || src.CacheReadInputTokens != 0
	if cacheReadTokens == 0 && (src.CachedTokensPresent || src.CachedTokens != 0) {
		cacheReadTokens = max(src.CachedTokens, 0)
		hasCacheReadTokens = true
	}
	if cacheReadTokens == 0 && src.PromptTokensDetails != nil && (src.PromptTokensDetails.CachedTokensPresent || src.PromptTokensDetails.CachedTokens != 0) {
		cacheReadTokens = max(src.PromptTokensDetails.CachedTokens, 0)
		hasCacheReadTokens = true
	}
	if cacheReadTokens == 0 && src.PromptCacheHitTokens != nil {
		cacheReadTokens = max(*src.PromptCacheHitTokens, 0)
		hasCacheReadTokens = true
	}
	mergeAnthropicCacheCreationUsage(dst, src)

	// Some Anthropic-compatible providers retain OpenAI-style prompt/cache
	// fields. Prefer those authoritative totals or hit/miss buckets over the
	// overloaded input_tokens field. This covers Kimi's changing stream
	// semantics as well as GLM/DeepSeek cache aliases.
	if src.PromptTokens <= 0 && src.PromptCacheHitTokens == nil && src.PromptCacheMissTokens == nil && !dst.promptTotalPresent {
		// Without an authoritative prompt total or miss bucket, input_tokens is
		// provider-specific: it may already be the uncached bucket, or it may be
		// a total from an earlier event. Do not infer a subtraction merely because
		// a later event contains cache buckets; that would corrupt providers whose
		// stream uses independent input and cache fields.
		if src.InputTokens > 0 {
			dst.InputTokens = src.InputTokens
		}
	}
	if hasCacheReadTokens && (cacheReadTokens > 0 || dst.CacheReadInputTokens <= 0) {
		dst.CacheReadInputTokens = cacheReadTokens
	}
	normalizeAnthropicPromptTotal(dst, src.PromptTokens, src.PromptCacheMissTokens)
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
}

// mergeAnthropicUsageFromPayload recognizes the complete final usage snapshot
// emitted by our KIRO translator. The raw-payload check is deliberately gated
// by the KIRO direct call site so ordinary providers keep positive-only delta
// semantics even if they send a similarly named field.
func mergeAnthropicUsageFromPayload(dst *ClaudeUsage, src apicompat.AnthropicUsage, payload string, allowKiroMarkedFinalUsage bool) bool {
	markedFinal := allowKiroMarkedFinalUsage && gjson.Get(payload, "usage."+kiroFinalUsageSSEField).Bool()
	if !markedFinal {
		mergeAnthropicUsage(dst, src)
		return false
	}
	if dst == nil {
		return true
	}
	dst.InputTokens = src.InputTokens
	dst.OutputTokens = src.OutputTokens
	dst.CacheReadInputTokens = src.CacheReadInputTokens
	dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	dst.CacheCreation5mTokens = 0
	dst.CacheCreation1hTokens = 0
	return true
}

func normalizeAnthropicPromptTotal(usage *ClaudeUsage, promptTokens int, promptCacheMissTokens *int) {
	if promptCacheMissTokens != nil {
		usage.promptTotalPresent = false
		usage.InputTokens = max(*promptCacheMissTokens, 0)
		return
	}
	if promptTokens > 0 {
		usage.promptTotalTokens = promptTokens
		usage.promptTotalPresent = true
	}
	if usage.promptTotalPresent {
		usage.InputTokens = max(usage.promptTotalTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0)
	}
}

func mergeAnthropicCacheCreationUsage(dst *ClaudeUsage, src apicompat.AnthropicUsage) {
	if src.HasCacheCreationInputTokens() && (src.CacheCreationInputTokens > 0 || dst.CacheCreationInputTokens <= 0) {
		dst.CacheCreationInputTokens = max(src.CacheCreationInputTokens, 0)
		dst.cacheCreationInputTokensPresent = true
		dst.cacheCreationInputTokensFromDetails = false
	}
	if src.CacheCreation != nil {
		if src.CacheCreation.Ephemeral5mInputTokens != nil {
			dst.CacheCreation5mTokens = max(*src.CacheCreation.Ephemeral5mInputTokens, 0)
		}
		if src.CacheCreation.Ephemeral1hInputTokens != nil {
			dst.CacheCreation1hTokens = max(*src.CacheCreation.Ephemeral1hInputTokens, 0)
		}
		if (!dst.cacheCreationInputTokensPresent || dst.cacheCreationInputTokensFromDetails || dst.CacheCreationInputTokens <= 0) && (src.CacheCreation.Ephemeral5mInputTokens != nil || src.CacheCreation.Ephemeral1hInputTokens != nil) {
			dst.CacheCreationInputTokens = dst.CacheCreation5mTokens + dst.CacheCreation1hTokens
			dst.cacheCreationInputTokensFromDetails = true
		}
	}
}

func mergeRawAnthropicCacheCreationUsage(node gjson.Result, dst *ClaudeUsage) {
	aggregate := node.Get("cache_creation_input_tokens")
	usage := apicompat.AnthropicUsage{
		CacheCreationInputTokens:        int(aggregate.Int()),
		CacheCreationInputTokensPresent: aggregate.Exists() && aggregate.Type != gjson.Null,
		CacheCreation:                   &apicompat.AnthropicCacheCreation{},
	}
	if tokens := node.Get("cache_creation.ephemeral_5m_input_tokens"); tokens.Exists() && tokens.Type != gjson.Null {
		value := int(tokens.Int())
		usage.CacheCreation.Ephemeral5mInputTokens = &value
	}
	if tokens := node.Get("cache_creation.ephemeral_1h_input_tokens"); tokens.Exists() && tokens.Type != gjson.Null {
		value := int(tokens.Int())
		usage.CacheCreation.Ephemeral1hInputTokens = &value
	}
	mergeAnthropicCacheCreationUsage(dst, usage)
}

func syncAnthropicResponsesUsage(state *apicompat.AnthropicEventToResponsesState, usage ClaudeUsage) {
	if state == nil {
		return
	}
	state.InputTokens = usage.InputTokens
	state.OutputTokens = usage.OutputTokens
	state.CacheReadInputTokens = usage.CacheReadInputTokens
	state.CacheCreationInputTokens = usage.CacheCreationInputTokens
}

func mergeKiroCreditsFromAnthropicPayload(dst *ClaudeUsage, payload string) {
	if dst == nil || payload == "" || !gjson.Valid(payload) {
		return
	}
	if credits := kiroCreditsFromUsageGJSON(gjson.Get(payload, "usage")); credits > 0 {
		dst.KiroCredits = credits
		return
	}
	if credits := kiroCreditsFromUsageGJSON(gjson.Get(payload, "message.usage")); credits > 0 {
		dst.KiroCredits = credits
	}
}

func kiroCreditsFromUsageGJSON(value gjson.Result) float64 {
	if !value.Exists() || !value.IsObject() {
		return 0
	}
	for _, field := range []string{
		"_sub2api_kiro_credits",
		"kiro_credits",
		"kiroCredits",
		"credits",
		"creditsUsed",
		"creditUsage",
		"consumedCredits",
	} {
		if v := value.Get(field); v.Exists() && v.Float() > 0 {
			return v.Float()
		}
	}
	return 0
}

func normalizeAnthropicEventUsageForResponses(event *apicompat.AnthropicStreamEvent, usage ClaudeUsage) {
	if event == nil {
		return
	}
	normalize := func(dst *apicompat.AnthropicUsage) {
		if dst == nil {
			return
		}
		dst.InputTokens = usage.InputTokens
		dst.OutputTokens = usage.OutputTokens
		dst.CacheReadInputTokens = usage.CacheReadInputTokens
		dst.CacheCreationInputTokens = usage.CacheCreationInputTokens
		dst.CacheCreationInputTokensPresent = true
		dst.CacheCreation = nil
	}
	normalize(event.Usage)
	if event.Message != nil {
		normalize(&event.Message.Usage)
	}
}

// parseAnthropicSSEField parses an SSE field line in the form "field:value" or "field: value".
// According to the SSE spec (https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation),
// the space after the colon is optional. This function handles both formats.
func parseAnthropicSSEField(line, field string) (string, bool) {
	prefix := field + ":"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}

// handleResponsesBufferedStreamingResponse reads all Anthropic SSE events from
// the upstream streaming response, assembles them into a complete Anthropic
// response, converts to Responses API JSON format, and writes it to the client.
func (s *GatewayService) handleResponsesBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	allowKiroMarkedFinalUsage bool,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*ForwardResult, error) {
	requestID := captureProviderRequestID(resp.Header)
	observer := beginUpstreamResponseModelObservation(c)

	lineReader := newProviderLineReader(resp, s.cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, s.cfg)
	})
	defer lineReader.Close()

	// Accumulate the final Anthropic response from streaming events
	var finalResp *apicompat.AnthropicResponse
	var contentAccumulator anthropicBufferedContentAccumulator
	var usage ClaudeUsage
	terminalObserved := false
	incompleteProviderTail := false
	var scanErr error

	for {
		line, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			break
		}
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			if payload, dataLine := parseAnthropicSSEField(line, "data"); dataLine && strings.TrimSpace(payload) != "" {
				incompleteProviderTail = true
				break
			}
			continue
		}

		// Read the data line
		dataLine, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			incompleteProviderTail = true
			break
		}
		payload, ok := parseAnthropicSSEField(dataLine, "data")
		if !ok {
			incompleteProviderTail = true
			break
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("forward_as_responses buffered: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("event_type", eventType),
			)
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, newIncompleteProviderStreamFailover(resp, fmt.Sprintf("invalid JSON for Anthropic event %q", eventType))
		}
		if event.Type == "message_stop" {
			terminalObserved = true
		}

		observer.ObserveAnthropic([]byte(payload))
		if event.Type == "error" {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, &sseStreamErrorEventError{RawData: payload}
		}

		// message_start carries the initial response structure
		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
			mergeAnthropicUsageFromPayload(&usage, event.Message.Usage, payload, allowKiroMarkedFinalUsage)
		}

		// message_delta carries final usage and stop_reason
		if event.Type == "message_delta" {
			if event.Usage != nil {
				mergeAnthropicUsageFromPayload(&usage, *event.Usage, payload, allowKiroMarkedFinalUsage)
			}
			mergeKiroCreditsFromAnthropicPayload(&usage, payload)
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}

		// Accumulate content blocks
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			contentAccumulator.start(finalResp, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			contentAccumulator.delta(*event.Index, event.Delta)
		}
	}

	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_responses buffered: read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
		failure := newIncompleteProviderStreamFailover(resp, "upstream stream read failed before message_stop: "+sanitizeStreamError(scanErr))
		return nil, errors.Join(failure, scanErr)
	}
	if incompleteProviderTail {
		lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
		return nil, newIncompleteProviderStreamFailover(resp, "upstream stream ended with an incomplete Anthropic provider event")
	}
	if !terminalObserved {
		return nil, newIncompleteProviderStreamFailover(resp, "upstream stream ended before message_stop")
	}

	if finalResp == nil {
		return nil, newIncompleteProviderStreamFailover(resp, "upstream stream ended without a message_start response")
	}
	contentAccumulator.materialize(finalResp)
	normalizeAnthropicEventUsageForResponses(&apicompat.AnthropicStreamEvent{Message: finalResp}, usage)

	// Convert to Responses format
	if isClaude55SignedThinkingModel(mappedModel) {
		finalResp.Model = mappedModel
	}
	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	responsesResp.Model = originalModel // Use original model name

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		respBytes, _, err = apicompat.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, responsesResp)
	}

	return &ForwardResult{
		RequestID:                     requestID,
		UpstreamHeaders:               resp.Header,
		Usage:                         usage,
		Model:                         originalModel,
		UpstreamModel:                 mappedModel,
		ReasoningEffort:               reasoningEffort,
		Stream:                        false,
		Duration:                      time.Since(startTime),
		UpstreamResponseModel:         observer.Model(),
		UpstreamResponseModelConflict: observer.Conflict(),
		UpstreamResponseServiceTier:   observer.ServiceTier(),
		CaptureResponseComplete:       true,
	}, nil
}

// handleResponsesStreamingResponse reads Anthropic SSE events from upstream,
// converts each to Responses SSE events, and writes them to the client.
func (s *GatewayService) handleResponsesStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	allowKiroMarkedFinalUsage bool,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*ForwardResult, error) {
	requestID := captureProviderRequestID(resp.Header)
	observer := beginUpstreamResponseModelObservation(c)
	writeHeaders := func() {
		if s.responseHeaderFilter != nil {
			responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
	}
	var staged stagedConvertedStream
	defer func() { _ = staged.close() }()

	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = originalModel
	state.PreserveThinkingSignatures = isClaude55SignedThinkingModel(mappedModel)
	clientToolRestorer := apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping)
	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	semanticOutput := false
	terminalObserved := false
	providerStopObserved := false
	providerPayloadObserved := false
	incompleteProviderTail := false
	clientDisconnected := false
	var stagedWriteErr error

	lineReader := newProviderLineReader(resp, s.cfg, func(r io.Reader) *bufio.Scanner {
		scanner := bufio.NewScanner(r)
		maxLineSize := defaultMaxLineSize
		if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
			maxLineSize = s.cfg.Gateway.MaxLineSize
		}
		scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
		scanner.Split(newBoundedSSESplit(s.cfg, maxLineSize, nil))
		return scanner
	})
	defer lineReader.Close()
	var scanErr error

	resultWithUsage := func() *ForwardResult {
		return &ForwardResult{
			RequestID:                     requestID,
			UpstreamHeaders:               resp.Header,
			Usage:                         usage,
			Model:                         originalModel,
			UpstreamModel:                 mappedModel,
			ReasoningEffort:               reasoningEffort,
			Stream:                        true,
			Duration:                      time.Since(startTime),
			FirstTokenMs:                  firstTokenMs,
			ClientDisconnect:              clientDisconnected,
			CaptureResponseComplete:       terminalObserved,
			UpstreamResponseModel:         observer.Model(),
			UpstreamResponseModelConflict: observer.Conflict(),
			UpstreamResponseServiceTier:   observer.ServiceTier(),
		}
	}

	// processEvent handles a single parsed Anthropic SSE event.
	processEvent := func(event *apicompat.AnthropicStreamEvent, payload string) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from message_delta
		if event.Type == "message_delta" && event.Delta != nil && event.Delta.StopReason != "" {
			providerStopObserved = true
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsageFromPayload(&usage, *event.Usage, payload, allowKiroMarkedFinalUsage)
		}
		// Also capture usage from message_start
		if event.Type == "message_start" && event.Message != nil {
			providerPayloadObserved = true
			mergeAnthropicUsageFromPayload(&usage, event.Message.Usage, payload, allowKiroMarkedFinalUsage)
		}
		if anthropicSSEEventHasSemanticOutput(payload) {
			semanticOutput = true
		}
		if event.Type == "message_stop" {
			terminalObserved = true
		}
		if clientDisconnected {
			return false
		}

		// Keep the terminal Responses usage aligned with the normalized billing
		// buckets. Normalize the converter input too, so message handlers cannot
		// restore the provider's overlapping raw input total.
		syncAnthropicResponsesUsage(state, usage)
		normalizeAnthropicEventUsageForResponses(event, usage)

		// Convert to Responses events
		events := apicompat.AnthropicEventToResponsesEvents(event, state)
		for _, evt := range events {
			payload, err := json.Marshal(evt)
			if err != nil {
				logger.L().Warn("forward_as_responses stream: failed to marshal event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			payload = reverseToolNamesIfPresent(c, payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				stagedWriteErr = fmt.Errorf("restore responses client tools: %w", err)
				return true
			}
			for _, restored := range payloads {
				if clientDisconnected {
					continue
				}
				eventType := gjson.GetBytes(restored, "type").String()
				wire := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, restored)
				if err := staged.write(c, writeHeaders, wire, semanticOutput || (terminalObserved && providerPayloadObserved)); err != nil {
					logger.L().Info("forward_as_responses stream: client disconnected",
						zap.String("request_id", requestID),
					)
					var clientWriteErr *stagedConvertedClientWriteError
					if errors.As(err, &clientWriteErr) {
						clientDisconnected = true
						continue
					}
					stagedWriteErr = err
					return true // client disconnected
				}
			}
		}
		return false
	}

	finalizeStream := func() (*ForwardResult, error) {
		if clientDisconnected {
			return resultWithUsage(), nil
		}
		if finalEvents := apicompat.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 {
			for _, evt := range finalEvents {
				sse, err := apicompat.ResponsesEventToSSE(evt)
				if err != nil {
					continue
				}
				out := string(reverseToolNamesIfPresent(c, []byte(sse)))
				if err := staged.write(c, writeHeaders, out, true); err != nil {
					return resultWithUsage(), err
				}
			}
		}
		return resultWithUsage(), nil
	}

	// Read Anthropic SSE events
	for {
		line, ok, err := nextAnthropicCompatProviderLine(lineReader, ginRequestContext(c), &clientDisconnected)
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			break
		}
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			if payload, dataLine := parseAnthropicSSEField(line, "data"); dataLine && strings.TrimSpace(payload) != "" {
				if strings.TrimSpace(payload) == "[DONE]" && providerPayloadObserved {
					providerStopObserved = true
					break
				}
				incompleteProviderTail = true
				break
			}
			continue
		}

		// Read data line
		dataLine, ok, err := nextAnthropicCompatProviderLine(lineReader, ginRequestContext(c), &clientDisconnected)
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			incompleteProviderTail = true
			break
		}
		payload, ok := parseAnthropicSSEField(dataLine, "data")
		if !ok {
			incompleteProviderTail = true
			break
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("forward_as_responses stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("event_type", eventType),
			)
			invalidEventErr := fmt.Errorf("invalid JSON for Anthropic event %q: %w", eventType, err)
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			if staged.committed || clientDisconnected {
				result := resultWithUsage()
				result.CaptureTerminalError = true
				return result, invalidEventErr
			}
			return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(invalidEventErr))
		}
		observer.ObserveAnthropic([]byte(payload))
		if event.Type == "error" {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			failure := anthropicCompatInBandFailure(payload)
			if staged.committed || clientDisconnected {
				if !clientDisconnected {
					writeAnthropicCompatResponsesStreamFailure(c, state, clientToolRestorer, failure)
				}
				result := resultWithUsage()
				result.CaptureTerminalError = true
				return result, failure.err
			}
			return nil, failure.err
		}
		mergeKiroCreditsFromAnthropicPayload(&usage, payload)

		if processEvent(&event, payload) {
			if stagedWriteErr != nil {
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "upstream pre-output stage failed: "+sanitizeStreamError(stagedWriteErr))
				}
				return resultWithUsage(), stagedWriteErr
			}
			return resultWithUsage(), nil
		}
	}

	if errors.Is(scanErr, errProviderStreamIdleTimeout) && (terminalObserved || providerStopObserved) {
		scanErr = nil
	}
	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_responses stream: read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
		if staged.committed || clientDisconnected {
			if !clientDisconnected && !terminalObserved && !providerStopObserved {
				failure := anthropicCompatProviderFailure(scanErr)
				writeAnthropicCompatResponsesStreamFailure(c, state, clientToolRestorer, failure)
			}
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, scanErr
		}
		failure := newIncompleteProviderStreamFailover(resp, "upstream stream read failed before semantic output: "+sanitizeStreamError(scanErr))
		if errors.Is(scanErr, errProviderStreamIdleTimeout) {
			failure.RetryableOnSameAccount = false
		}
		return nil, errors.Join(failure, scanErr)
	}
	if incompleteProviderTail {
		lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
		incompleteErr := errors.New("upstream stream ended with an incomplete Anthropic provider event")
		if staged.committed || clientDisconnected {
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, incompleteErr
		}
		return nil, newIncompleteProviderStreamFailover(resp, incompleteErr.Error())
	}
	if providerPayloadObserved && !terminalObserved && !providerStopObserved && !clientDisconnected {
		failure := &anthropicCompatStreamFailure{
			err:     errors.New("stream usage incomplete: missing terminal event"),
			errType: "stream_incomplete",
			message: "upstream stream ended without a terminal event",
		}
		if !staged.committed {
			return nil, errors.Join(newIncompleteProviderStreamFailover(resp, failure.message), failure.err)
		}
		writeAnthropicCompatResponsesStreamFailure(c, state, clientToolRestorer, failure)
		result := resultWithUsage()
		result.CaptureTerminalError = true
		return result, failure.err
	}

	return finalizeStream()
}

type anthropicBufferedBlockAccumulator struct {
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	input     json.RawMessage
}

type anthropicBufferedContentAccumulator struct {
	blocks []*anthropicBufferedBlockAccumulator
}

func (a *anthropicBufferedContentAccumulator) start(response *apicompat.AnthropicResponse, block apicompat.AnthropicContentBlock) {
	if a == nil || response == nil {
		return
	}
	state := &anthropicBufferedBlockAccumulator{}
	_, _ = state.text.WriteString(block.Text)
	_, _ = state.thinking.WriteString(block.Thinking)
	_, _ = state.signature.WriteString(block.Signature)
	state.input = append(json.RawMessage(nil), block.Input...)
	a.blocks = append(a.blocks, state)
	response.Content = append(response.Content, block)
}

func (a *anthropicBufferedContentAccumulator) delta(index int, delta *apicompat.AnthropicDelta) {
	if a == nil || delta == nil || index < 0 || index >= len(a.blocks) {
		return
	}
	state := a.blocks[index]
	switch delta.Type {
	case "text_delta":
		_, _ = state.text.WriteString(delta.Text)
	case "thinking_delta":
		_, _ = state.thinking.WriteString(delta.Thinking)
	case "signature_delta":
		_, _ = state.signature.WriteString(delta.Signature)
	case "input_json_delta":
		state.input = appendRawJSON(state.input, delta.PartialJSON)
	}
}

func (a *anthropicBufferedContentAccumulator) materialize(response *apicompat.AnthropicResponse) {
	if a == nil || response == nil {
		return
	}
	for index, state := range a.blocks {
		if index >= len(response.Content) || state == nil {
			break
		}
		response.Content[index].Text = state.text.String()
		response.Content[index].Thinking = state.thinking.String()
		response.Content[index].Signature = state.signature.String()
		if len(state.input) > 0 {
			response.Content[index].Input = append(json.RawMessage(nil), state.input...)
		}
	}
}

func appendRawJSON(existing json.RawMessage, fragment string) json.RawMessage {
	// Anthropic initializes tool_use.input to {} in content_block_start, then
	// streams the actual input through input_json_delta events. Treat that empty
	// object as a placeholder instead of prefixing it to the streamed JSON.
	var existingObject map[string]json.RawMessage
	isEmptyObject := json.Unmarshal(existing, &existingObject) == nil && existingObject != nil && len(existingObject) == 0
	if len(existing) == 0 || isEmptyObject {
		return json.RawMessage(fragment)
	}
	return json.RawMessage(string(existing) + fragment)
}

// writeResponsesError writes an error response in OpenAI Responses API format.
func writeKiroResponsesError(c *gin.Context, status int, code, message string, compaction bool) {
	if compaction && (message == kiroContextWindowFullMessage || message == kiroInputTooLongMessage) {
		code = "context_length_exceeded"
	}
	writeResponsesError(c, status, code, message)
}

func writeResponsesError(c *gin.Context, statusCode int, code, message string) {
	if StopOpenAICompactSSEKeepaliveCommitted(c) {
		writeOpenAICompactSSEFailureMessage(c, statusCode, code, message)
		MarkResponseCommitted(c)
		return
	}
	MarkResponseCommitted(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

// mapUpstreamStatusCode maps upstream HTTP status codes to appropriate client-facing codes.
func mapUpstreamStatusCode(code int) int {
	if code >= 500 {
		return http.StatusBadGateway
	}
	return code
}
