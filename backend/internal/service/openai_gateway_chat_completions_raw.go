package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

const (
	maxOpenAIChatChoicesPerAttempt       = 1024
	maxOpenAIChatToolCallsPerChoice      = 1024
	maxOpenAIChatRetainedIdentifierBytes = 1024
)

func gjsonCollectionHasValues(value gjson.Result) bool {
	hasValues := false
	value.ForEach(func(_, _ gjson.Result) bool {
		hasValues = true
		return false
	})
	return hasValues
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
		upstreamBody, err = normalizeGrokChatReasoningEffort(upstreamBody, upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("normalize Grok chat reasoning effort: %w", err)
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
		finishOpenAIHTTPCapture(resp)
		if account.Platform == PlatformGrok {
			kind := "http_error"
			if s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody) {
				kind = "failover"
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
				Kind:               kind,
				Message:            upstreamMsg,
			})
			s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			if s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody) {
				return nil, &UpstreamFailoverError{
					StatusCode:                resp.StatusCode,
					ResponseBody:              respBody,
					RequestHeaders:            captureRequestHeadersFromResponse(resp),
					ResponseHeaders:           resp.Header.Clone(),
					UpstreamEndpoint:          captureEndpointFromResponse(resp),
					HasUpstreamHTTPResponse:   true,
					Platform:                  string(account.Platform),
					RetryableOnSameAccount:    account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
					CaptureResponseIncomplete: !openAIUpstreamErrorResponseComplete(resp, respBody, openAIUpstreamErrorBodyReadLimitForConfig(s.cfg)),
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
	finishOpenAIHTTPCapture(resp)
	var failoverErr *UpstreamFailoverError
	if result == nil && errors.As(forwardErr, &failoverErr) {
		return nil, forwardErr
	}
	captureOnlyFailure := forwardErr != nil && result == nil
	if forwardErr != nil && result == nil {
		result = &OpenAIForwardResult{
			Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
			Stream: clientStream, Duration: time.Since(startTime), ResponseHeaders: resp.Header.Clone(),
			UpstreamHTTPStatus: resp.StatusCode,
		}
	}
	if result != nil {
		addOpenAIUsage(&result.Usage, bridgeUsage)
		result.UpstreamEndpoint = grokChatRawEndpoint
		result.UpstreamFailed = captureOnlyFailure
		result.CaptureTerminalError = forwardErr != nil
		s.applyOpenAIHTTPSuccessCapture(c, account, result)
	}
	return result, forwardErr
}

func (s *OpenAIGatewayService) rawChatCompletionsURL(account *Account) (string, error) {
	if account.Platform == PlatformGrok {
		targetURL, err := buildGrokChatCompletionsURL(account, s.cfg, s.settingService)
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
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	requestID := captureProviderRequestID(resp.Header)
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
	readActivity := newProviderBodyReadActivity(resp.Body)
	scanner := s.newUpstreamSSEScanner(readActivity)

	var usage OpenAIUsage
	var firstTokenMs *int
	clientDisconnected := false
	clientOutputStarted := false
	semanticOutput := false
	terminalObserved := false
	applicationTerminalObserved := false
	providerPayloadObserved := false
	choiceState := openAIChatChoiceStreamState{}
	var staged stagedConvertedStream
	var stagedErr error
	defer func() { _ = staged.close() }()
	refusalDetector := newOpenAIChatSilentRefusalDetector(requestBodyLen)

	writeLine := func(line string) {
		if clientDisconnected {
			return
		}
		commit := semanticOutput || (applicationTerminalObserved && providerPayloadObserved)
		if refusalDetector.Enabled() {
			commit = (refusalDetector.ShouldReleaseClientOutput() && semanticOutput) ||
				(terminalObserved && applicationTerminalObserved && providerPayloadObserved)
		}
		if err := staged.write(c, writeStreamHeaders, line+"\n", commit); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				stagedErr = err
			}
			clientDisconnected = true
			logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
		if staged.committed {
			clientOutputStarted = true
			captureWebChatStreamString(ctx, line+"\n")
		}
	}
	result := func() *OpenAIForwardResult {
		return &OpenAIForwardResult{
			RequestID:                     requestID,
			Usage:                         usage,
			Model:                         originalModel,
			BillingModel:                  billingModel,
			UpstreamModel:                 upstreamModel,
			UpstreamResponseModel:         observedUpstreamResponseModel(c),
			UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
			ReasoningEffort:               reasoningEffort,
			ServiceTier:                   serviceTier,
			Stream:                        true,
			Duration:                      time.Since(startTime),
			FirstTokenMs:                  firstTokenMs,
		}
	}
	type rawChatScanEvent struct {
		line string
		err  error
	}
	events := make(chan rawChatScanEvent, openAIDefaultStreamQueueSize)
	stopScanner := make(chan struct{})
	scannerDone := make(chan struct{})
	var stopScannerOnce sync.Once
	sendScanEvent := func(event rawChatScanEvent) bool {
		select {
		case events <- event:
			return true
		case <-stopScanner:
			return false
		}
	}
	go func() {
		defer close(scannerDone)
		defer close(events)
		for scanner.Scan() {
			if !sendScanEvent(rawChatScanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendScanEvent(rawChatScanEvent{err: err})
		}
	}()
	defer func() {
		stopScannerOnce.Do(func() {
			close(stopScanner)
			closeCaptureResponseAndJoinScanner(resp, scannerDone)
		})
	}()
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	streamIdleTimeout := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamIdleTimeout = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		idleTimer = time.NewTimer(streamIdleTimeout)
		idleCh = idleTimer.C
		defer idleTimer.Stop()
	}
	resetIdleTimerAfter := func(delay time.Duration) {
		if idleTimer == nil {
			return
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(delay)
	}
	resetIdleTimer := func() { resetIdleTimerAfter(streamIdleTimeout) }
	drainCaptureBeforeParserReturn := func() {
		if !captureResponseNeedsDrain(resp.Body) {
			return
		}
		drainIdle := streamIdleTimeout
		if drainIdle <= 0 {
			drainIdle = captureOverflowDrainTimeout
		}
		timer := time.NewTimer(drainIdle)
		defer timer.Stop()
		requestCtx := ginRequestContext(c)
		abortAndJoin := func() {
			markCaptureResponseTruncated(resp.Body)
			closeCaptureResponseUnderlying(resp)
			for range events {
			}
			<-scannerDone
		}
		for captureResponseNeedsDrain(resp.Body) {
			select {
			case _, ok := <-events:
				if !ok {
					return
				}
			case <-requestCtx.Done():
				abortAndJoin()
				return
			case <-timer.C:
				remaining := drainIdle - time.Since(readActivity.LastReadTime())
				if remaining > 0 {
					timer.Reset(remaining)
					continue
				}
				abortAndJoin()
				return
			}
		}
	}
	providerProtocolFailure := func(protocolErr error) (*OpenAIForwardResult, error) {
		drainCaptureBeforeParserReturn()
		if staged.committed || clientDisconnected {
			return result(), protocolErr
		}
		return nil, newOpenAIIncompleteChatStreamFailover(resp, protocolErr.Error())
	}

	var scanErr error
scanLoop:
	for {
		var line string
		select {
		case event, ok := <-events:
			if !ok {
				break scanLoop
			}
			if event.err != nil {
				scanErr = event.err
				break scanLoop
			}
			line = event.line
			resetIdleTimer()
		case <-idleCh:
			remaining := streamIdleTimeout - time.Since(readActivity.LastReadTime())
			if remaining > 0 {
				resetIdleTimerAfter(remaining)
				continue
			}
			if staged.committed || clientDisconnected || semanticOutput {
				return result(), errors.New("OpenAI chat_completions stream data interval timeout")
			}
			return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions stream timed out before semantic output")
		}
		if payload, ok := extractOpenAISSEDataLine(line); ok {
			trimmedPayload := strings.TrimSpace(payload)
			if terminalObserved && trimmedPayload != "" {
				protocolErr := errors.New("OpenAI chat_completions data arrived after [DONE]")
				return providerProtocolFailure(protocolErr)
			}
			if trimmedPayload == "[DONE]" {
				if !choiceState.complete() {
					protocolErr := errors.New("OpenAI chat_completions [DONE] arrived before all choices had a finish_reason")
					return providerProtocolFailure(protocolErr)
				}
				terminalObserved = true
				if refusalDetector.IsSilentRefusal() {
					continue
				}
			} else {
				if !gjson.Valid(payload) {
					protocolErr := errors.New("OpenAI chat_completions returned malformed JSON data")
					return providerProtocolFailure(protocolErr)
				}
				providerPayload, protocolErr := classifyOpenAIChatStreamPayload(payload)
				if protocolErr != nil {
					return providerProtocolFailure(protocolErr)
				}
				if providerPayload {
					providerPayloadObserved = true
				}
				// Observe only after the strict per-frame shape/cap check above, but
				// before lifecycle validation so the intentional large-request empty
				// stop signal retains its dedicated failover classification.
				refusalDetector.ObservePayload([]byte(payload))
				applicationTerminalObserved, protocolErr = choiceState.observe(payload)
				if protocolErr != nil {
					if refusalDetector.IsSilentRefusal() && !clientDisconnected {
						drainCaptureBeforeParserReturn()
						return nil, newOpenAISilentRefusalFailoverError(c, account, requestID)
					}
					return providerProtocolFailure(protocolErr)
				}
				observer.ObserveOpenAI([]byte(payload), strings.TrimSpace(gjson.Get(payload, "type").String()))
				if openAIChatPayloadHasSemanticOutput(payload) {
					semanticOutput = true
				}
				usageOnlyChunk := isOpenAIChatUsageOnlyStreamChunk(payload)
				mergeCCStreamUsage(&usage, payload)
				if firstTokenMs == nil && !usageOnlyChunk {
					elapsed := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &elapsed
				}
			}
		} else {
			refusalDetector.ObserveSSELine(line)
		}

		writeLine(line)
		if stagedErr != nil {
			if !staged.committed {
				return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions pre-output stage exceeded limit")
			}
			return result(), stagedErr
		}
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

	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions raw: stream read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
		if staged.committed || clientDisconnected {
			return result(), fmt.Errorf("openai chat_completions stream read error: %w", scanErr)
		}
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions stream read failed before semantic output")
	}

	if !terminalObserved || !applicationTerminalObserved {
		if staged.committed || clientDisconnected {
			return result(), errors.New("OpenAI chat_completions stream ended before [DONE]")
		}
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions stream ended before semantic output")
	}
	// A large-request empty completion with finish_reason=stop is the
	// provider's recognizable silent-refusal signal. Preserve its dedicated
	// failover classification even though it deliberately contains no valid
	// provider payload/semantic output.
	if refusalDetector.IsSilentRefusal() && !clientDisconnected {
		return nil, newOpenAISilentRefusalFailoverError(c, account, requestID)
	}
	if !providerPayloadObserved {
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions stream ended without a valid provider payload")
	}

	if !clientOutputStarted && !clientDisconnected {
		if refusalDetector.IsSilentRefusal() {
			if !clientDisconnected {
				return nil, newOpenAISilentRefusalFailoverError(c, account, requestID)
			}
		} else if err := staged.write(c, writeStreamHeaders, "", true); err != nil {
			return result(), err
		}
	}

	return result(), nil
}

func openAIChatPayloadHasSemanticOutput(payload string) bool {
	choices := gjson.Get(payload, "choices")
	if !choices.Exists() || !choices.IsArray() {
		return false
	}
	semantic := false
	choices.ForEach(func(_, choice gjson.Result) bool {
		delta := choice.Get("delta")
		if !delta.Exists() {
			return true
		}
		if openAIChatDeltaHasSemanticOutput(delta) {
			semantic = true
			return false
		}
		return true
	})
	return semantic
}

func openAIChatDeltaHasSemanticOutput(delta gjson.Result) bool {
	if !delta.IsObject() {
		return false
	}
	for _, path := range []string{"content", "refusal", "reasoning", "reasoning_content", "reasoning_summary"} {
		if value := delta.Get(path); value.Type == gjson.String && value.String() != "" {
			return true
		}
	}
	toolSemantic := false
	delta.Get("tool_calls").ForEach(func(_, call gjson.Result) bool {
		if strings.TrimSpace(call.Get("function.name").String()) != "" ||
			strings.TrimSpace(call.Get("function.arguments").String()) != "" {
			toolSemantic = true
			return false
		}
		return true
	})
	if toolSemantic {
		return true
	}
	functionCall := delta.Get("function_call")
	if functionCall.IsObject() && (strings.TrimSpace(functionCall.Get("name").String()) != "" ||
		strings.TrimSpace(functionCall.Get("arguments").String()) != "") {
		return true
	}
	audio := delta.Get("audio")
	return audio.IsObject() && (strings.TrimSpace(audio.Get("data").String()) != "" ||
		strings.TrimSpace(audio.Get("transcript").String()) != "" ||
		strings.TrimSpace(audio.Get("id").String()) != "")
}

func validateOpenAIChatDeltaShape(delta gjson.Result) error {
	if !delta.IsObject() {
		return nil
	}
	if role := delta.Get("role"); role.Exists() && (role.Type != gjson.String || role.String() != "assistant") {
		return errors.New("OpenAI chat_completions role was not assistant")
	}
	for _, path := range []string{"content", "refusal", "reasoning", "reasoning_content", "reasoning_summary"} {
		value := delta.Get(path)
		if value.Exists() && value.Type != gjson.String && value.Type != gjson.Null {
			return fmt.Errorf("OpenAI chat_completions %s was not a string", path)
		}
	}
	toolCalls := delta.Get("tool_calls")
	if toolCalls.Exists() {
		if !toolCalls.IsArray() {
			return errors.New("OpenAI chat_completions tool_calls was not an array")
		}
		seenIndexes := make(map[int64]struct{})
		seenIDs := make(map[string]struct{})
		position := 0
		var shapeErr error
		toolCalls.ForEach(func(_, call gjson.Result) bool {
			if position >= maxOpenAIChatToolCallsPerChoice {
				shapeErr = errors.New("OpenAI chat_completions contained too many tool calls")
				return false
			}
			if !call.IsObject() || !call.Get("function").IsObject() {
				shapeErr = errors.New("OpenAI chat_completions tool call was malformed")
				return false
			}
			if index := call.Get("index"); index.Exists() && !nonNegativeIntegerGJSON(index) {
				shapeErr = errors.New("OpenAI chat_completions tool call index was not a non-negative integer")
				return false
			}
			index := int64(position)
			if explicit := call.Get("index"); explicit.Exists() {
				index = explicit.Int()
			}
			if _, duplicate := seenIndexes[index]; duplicate {
				shapeErr = fmt.Errorf("OpenAI chat_completions duplicated tool call index %d", index)
				return false
			}
			seenIndexes[index] = struct{}{}
			if id := call.Get("id"); id.Exists() && (id.Type != gjson.String || strings.TrimSpace(id.String()) == "") {
				shapeErr = errors.New("OpenAI chat_completions tool call id was not a non-empty string")
				return false
			}
			if id := call.Get("id"); id.Type == gjson.String && len(id.String()) > maxOpenAIChatRetainedIdentifierBytes {
				shapeErr = errors.New("OpenAI chat_completions tool call id was too long")
				return false
			}
			if id := strings.TrimSpace(call.Get("id").String()); id != "" {
				if _, duplicate := seenIDs[id]; duplicate {
					shapeErr = fmt.Errorf("OpenAI chat_completions duplicated tool call id %q", id)
					return false
				}
				seenIDs[id] = struct{}{}
			}
			if callType := call.Get("type"); callType.Exists() && (callType.Type != gjson.String || callType.String() != "function") {
				shapeErr = errors.New("OpenAI chat_completions tool call type was not function")
				return false
			}
			for _, path := range []string{"function.name", "function.arguments"} {
				value := call.Get(path)
				if value.Exists() && value.Type != gjson.String {
					shapeErr = fmt.Errorf("OpenAI chat_completions tool call %s was not a string", path)
					return false
				}
			}
			if name := call.Get("function.name"); name.Type == gjson.String && len(name.String()) > maxOpenAIChatRetainedIdentifierBytes {
				shapeErr = errors.New("OpenAI chat_completions tool call function.name was too long")
				return false
			}
			position++
			return true
		})
		if shapeErr != nil {
			return shapeErr
		}
	}
	for _, objectPath := range []string{"function_call", "audio"} {
		object := delta.Get(objectPath)
		if object.Exists() && !object.IsObject() {
			return fmt.Errorf("OpenAI chat_completions %s was not an object", objectPath)
		}
	}
	if audio := delta.Get("audio"); audio.Exists() {
		if !nonEmptyGJSONString(audio.Get("id")) || !nonEmptyGJSONString(audio.Get("data")) || !nonEmptyGJSONString(audio.Get("transcript")) ||
			!nonNegativeIntegerGJSON(audio.Get("expires_at")) {
			return errors.New("OpenAI chat_completions audio was incomplete")
		}
	}
	for _, path := range []string{"function_call.name", "function_call.arguments", "audio.data", "audio.transcript", "audio.id"} {
		value := delta.Get(path)
		if value.Exists() && value.Type != gjson.String {
			return fmt.Errorf("OpenAI chat_completions %s was not a string", path)
		}
	}
	if name := delta.Get("function_call.name"); name.Type == gjson.String && len(name.String()) > maxOpenAIChatRetainedIdentifierBytes {
		return errors.New("OpenAI chat_completions function_call name was too long")
	}
	return nil
}

func validateOpenAIChatBufferedMessageShape(message gjson.Result) error {
	if !message.IsObject() {
		return errors.New("OpenAI chat_completions terminal message was not an object")
	}
	if role := message.Get("role"); role.Type != gjson.String || role.String() != "assistant" {
		return errors.New("OpenAI chat_completions terminal message role was not assistant")
	}
	if err := validateOpenAIChatDeltaShape(message); err != nil {
		return err
	}
	toolCalls := message.Get("tool_calls")
	if toolCalls.Exists() {
		if !toolCalls.IsArray() || !gjsonCollectionHasValues(toolCalls) {
			return errors.New("OpenAI chat_completions terminal tool_calls was empty or malformed")
		}
		seenIDs := make(map[string]struct{})
		seenIndexes := make(map[int64]struct{})
		position := 0
		var shapeErr error
		toolCalls.ForEach(func(_, call gjson.Result) bool {
			if position >= maxOpenAIChatToolCallsPerChoice {
				shapeErr = errors.New("OpenAI chat_completions terminal contained too many tool calls")
				return false
			}
			if !nonEmptyGJSONString(call.Get("id")) || call.Get("type").Type != gjson.String || call.Get("type").String() != "function" ||
				!nonEmptyGJSONString(call.Get("function.name")) || call.Get("function.arguments").Type != gjson.String {
				shapeErr = errors.New("OpenAI chat_completions terminal tool call was incomplete")
				return false
			}
			id := call.Get("id").String()
			if _, duplicate := seenIDs[id]; duplicate {
				shapeErr = errors.New("OpenAI chat_completions terminal tool call id was duplicated")
				return false
			}
			seenIDs[id] = struct{}{}
			index := int64(position)
			if explicit := call.Get("index"); explicit.Exists() {
				index = explicit.Int()
			}
			if _, duplicate := seenIndexes[index]; duplicate {
				shapeErr = errors.New("OpenAI chat_completions terminal tool call index was duplicated")
				return false
			}
			seenIndexes[index] = struct{}{}
			position++
			return true
		})
		if shapeErr != nil {
			return shapeErr
		}
	}
	if functionCall := message.Get("function_call"); functionCall.Exists() &&
		(!nonEmptyGJSONString(functionCall.Get("name")) || functionCall.Get("arguments").Type != gjson.String) {
		return errors.New("OpenAI chat_completions terminal function_call was incomplete")
	}
	if name := message.Get("function_call.name"); name.Type == gjson.String && len(name.String()) > maxOpenAIChatRetainedIdentifierBytes {
		return errors.New("OpenAI chat_completions terminal function_call name was too long")
	}
	return nil
}

func openAIChatPayloadIsValidProviderPayload(payload string) bool {
	recognized, err := classifyOpenAIChatStreamPayload(payload)
	return err == nil && recognized
}

type openAIChatChoiceStreamState struct {
	seen         map[int64]struct{}
	finished     map[int64]struct{}
	semantic     map[int64]bool
	tools        map[int64]map[int64]*openAIChatToolCallStreamState
	legacy       map[int64]*openAIChatLegacyFunctionStreamState
	toolIDOwner  map[string]openAIChatToolCallOwner
	identity     map[string]openAIResponsesDoneFieldDigest
	trackedTools int
}

type openAIChatToolCallOwner struct {
	choiceIndex int64
	toolIndex   int64
}

type openAIChatToolCallStreamState struct {
	id            string
	callType      string
	name          string
	argumentsSeen bool
}

type openAIChatLegacyFunctionStreamState struct {
	name          string
	argumentsSeen bool
}

func (s *openAIChatChoiceStreamState) observe(payload string) (bool, error) {
	root := gjson.Parse(payload)
	if err := s.observeIdentity(root); err != nil {
		return false, err
	}
	choices := gjson.Get(payload, "choices")
	if !choices.IsArray() || !gjsonCollectionHasValues(choices) {
		return s.complete(), nil
	}
	if s.seen == nil {
		s.seen = make(map[int64]struct{})
		s.finished = make(map[int64]struct{})
		s.semantic = make(map[int64]bool)
		s.tools = make(map[int64]map[int64]*openAIChatToolCallStreamState)
		s.legacy = make(map[int64]*openAIChatLegacyFunctionStreamState)
		s.toolIDOwner = make(map[string]openAIChatToolCallOwner)
	}
	frameChoices := make(map[int64]struct{})
	position := 0
	var observeErr error
	choices.ForEach(func(_, choice gjson.Result) bool {
		if position >= maxOpenAIChatChoicesPerAttempt {
			observeErr = errors.New("OpenAI chat_completions contained too many choices")
			return false
		}
		index := int64(position)
		if explicit := choice.Get("index"); explicit.Exists() {
			index = explicit.Int()
		}
		if _, duplicate := frameChoices[index]; duplicate {
			observeErr = fmt.Errorf("OpenAI chat_completions duplicated choice index %d", index)
			return false
		}
		frameChoices[index] = struct{}{}
		finishReason := choice.Get("finish_reason")
		if _, alreadyFinished := s.finished[index]; alreadyFinished {
			annotation, err := validateOpenAIChatFilterAnnotation(choice)
			if err != nil {
				observeErr = err
				return false
			}
			if !annotation || choiceHasOpenAIChatSemanticOutput(choice) ||
				(finishReason.Type == gjson.String && strings.TrimSpace(finishReason.String()) != "") {
				observeErr = fmt.Errorf("OpenAI chat_completions choice %d emitted non-annotation data after finish_reason", index)
				return false
			}
			position++
			return true
		}
		if _, exists := s.seen[index]; !exists && len(s.seen) >= maxOpenAIChatChoicesPerAttempt {
			observeErr = errors.New("OpenAI chat_completions contained too many choices")
			return false
		}
		s.seen[index] = struct{}{}
		if choiceHasOpenAIChatSemanticOutput(choice) {
			s.semantic[index] = true
		}
		if err := s.observeToolCallDeltas(index, choice.Get("delta.tool_calls")); err != nil {
			observeErr = err
			return false
		}
		if err := s.observeLegacyFunctionDelta(index, choice.Get("delta.function_call")); err != nil {
			observeErr = err
			return false
		}
		if finishReason.Type == gjson.String && strings.TrimSpace(finishReason.String()) != "" {
			reason := strings.TrimSpace(finishReason.String())
			if (reason == "stop" || reason == "length") && !s.semantic[index] {
				observeErr = fmt.Errorf("OpenAI chat_completions choice %d finished %s without semantic output", index, reason)
				return false
			}
			if err := s.validateFinishedToolCalls(index, reason); err != nil {
				observeErr = err
				return false
			}
			s.finished[index] = struct{}{}
		}
		position++
		return true
	})
	if observeErr != nil {
		return false, observeErr
	}
	return s.complete(), nil
}

func (s *openAIChatChoiceStreamState) observeIdentity(root gjson.Result) error {
	for _, field := range []string{"id", "object", "model", "created"} {
		value := root.Get(field)
		if !value.Exists() {
			continue
		}
		canonical := value.String()
		if value.Type == gjson.Number {
			canonical = strconv.FormatInt(value.Int(), 10)
		}
		digest := openAIResponsesDoneField(canonical)
		if s.identity == nil {
			s.identity = make(map[string]openAIResponsesDoneFieldDigest, 4)
		}
		if expected, seen := s.identity[field]; seen {
			if expected != digest {
				return fmt.Errorf("OpenAI chat_completions response identity changed %s across frames", field)
			}
			continue
		}
		s.identity[field] = digest
	}
	return nil
}

func (s *openAIChatChoiceStreamState) observeLegacyFunctionDelta(choiceIndex int64, call gjson.Result) error {
	if !call.Exists() {
		return nil
	}
	state := s.legacy[choiceIndex]
	if state == nil {
		state = &openAIChatLegacyFunctionStreamState{}
		s.legacy[choiceIndex] = state
	}
	if name := call.Get("name"); name.Exists() && name.String() != "" {
		if len(name.String()) > maxOpenAIChatRetainedIdentifierBytes {
			return fmt.Errorf("OpenAI chat_completions choice %d legacy function_call name was too long", choiceIndex)
		}
		if state.name != "" && state.name != name.String() {
			return fmt.Errorf("OpenAI chat_completions choice %d changed legacy function_call name", choiceIndex)
		}
		state.name = name.String()
	}
	if call.Get("arguments").Exists() {
		state.argumentsSeen = true
	}
	return nil
}

func (s *openAIChatChoiceStreamState) observeToolCallDeltas(choiceIndex int64, calls gjson.Result) error {
	if !calls.Exists() {
		return nil
	}
	choiceTools := s.tools[choiceIndex]
	if choiceTools == nil {
		choiceTools = make(map[int64]*openAIChatToolCallStreamState)
		s.tools[choiceIndex] = choiceTools
	}
	frameIndexes := make(map[int64]struct{})
	position := 0
	var observeErr error
	calls.ForEach(func(_, call gjson.Result) bool {
		if position >= maxOpenAIChatToolCallsPerChoice {
			observeErr = errors.New("OpenAI chat_completions contained too many tool calls")
			return false
		}
		toolIndex := int64(position)
		if explicit := call.Get("index"); explicit.Exists() {
			toolIndex = explicit.Int()
		}
		if _, duplicate := frameIndexes[toolIndex]; duplicate {
			observeErr = fmt.Errorf("OpenAI chat_completions duplicated tool call index %d", toolIndex)
			return false
		}
		frameIndexes[toolIndex] = struct{}{}
		state := choiceTools[toolIndex]
		if state == nil {
			if s.trackedTools >= maxOpenAIChatToolCallsPerChoice {
				observeErr = errors.New("OpenAI chat_completions contained too many tool calls")
				return false
			}
			state = &openAIChatToolCallStreamState{}
			choiceTools[toolIndex] = state
			s.trackedTools++
		}
		for field, destination := range map[string]*string{"id": &state.id, "type": &state.callType, "function.name": &state.name} {
			value := call.Get(field)
			if !value.Exists() || value.String() == "" {
				continue
			}
			if (field == "id" || field == "function.name") && len(value.String()) > maxOpenAIChatRetainedIdentifierBytes {
				observeErr = fmt.Errorf("OpenAI chat_completions tool call %d %s was too long", toolIndex, field)
				return false
			}
			if *destination != "" && *destination != value.String() {
				observeErr = fmt.Errorf("OpenAI chat_completions tool call %d changed %s", toolIndex, field)
				return false
			}
			*destination = value.String()
		}
		if state.id != "" {
			owner := openAIChatToolCallOwner{choiceIndex: choiceIndex, toolIndex: toolIndex}
			if prior, exists := s.toolIDOwner[state.id]; exists && prior != owner {
				observeErr = fmt.Errorf("OpenAI chat_completions tool call id %q was reused across indexes", state.id)
				return false
			}
			s.toolIDOwner[state.id] = owner
		}
		if call.Get("function.arguments").Exists() {
			state.argumentsSeen = true
		}
		position++
		return true
	})
	if observeErr != nil {
		return observeErr
	}
	return nil
}

func (s *openAIChatChoiceStreamState) validateFinishedToolCalls(choiceIndex int64, finishReason string) error {
	tools := s.tools[choiceIndex]
	if finishReason == "tool_calls" && len(tools) == 0 {
		return fmt.Errorf("OpenAI chat_completions choice %d finished tool_calls without a tool call", choiceIndex)
	}
	if len(tools) > 0 && finishReason != "tool_calls" {
		return fmt.Errorf("OpenAI chat_completions choice %d emitted tool calls but finished with %s", choiceIndex, finishReason)
	}
	for toolIndex, tool := range tools {
		if strings.TrimSpace(tool.id) == "" || tool.callType != "function" || strings.TrimSpace(tool.name) == "" || !tool.argumentsSeen {
			return fmt.Errorf("OpenAI chat_completions choice %d tool call %d was incomplete at finish_reason", choiceIndex, toolIndex)
		}
	}
	legacy := s.legacy[choiceIndex]
	if finishReason == "function_call" && legacy == nil {
		return fmt.Errorf("OpenAI chat_completions choice %d finished function_call without a function call", choiceIndex)
	}
	if legacy != nil && finishReason != "function_call" {
		return fmt.Errorf("OpenAI chat_completions choice %d emitted legacy function_call but finished with %s", choiceIndex, finishReason)
	}
	if legacy != nil && (strings.TrimSpace(legacy.name) == "" || !legacy.argumentsSeen) {
		return fmt.Errorf("OpenAI chat_completions choice %d legacy function_call was incomplete at finish_reason", choiceIndex)
	}
	return nil
}

func choiceHasOpenAIChatSemanticOutput(choice gjson.Result) bool {
	return openAIChatDeltaHasSemanticOutput(choice.Get("delta")) ||
		openAIChatDeltaHasSemanticOutput(choice.Get("message")) ||
		(choice.Get("text").Type == gjson.String && strings.TrimSpace(choice.Get("text").String()) != "")
}

// Azure asynchronous content filtering emits annotation-only choices without
// delta/message/text. These frames are ancillary, not semantic or terminal,
// and may legitimately arrive after the choice's finish_reason.
func validateOpenAIChatFilterAnnotation(choice gjson.Result) (bool, error) {
	results := choice.Get("content_filter_results")
	offsets := choice.Get("content_filter_offsets")
	if !results.Exists() && !offsets.Exists() {
		return false, nil
	}
	if results.Exists() && !results.IsObject() {
		return false, errors.New("OpenAI chat_completions content_filter_results was not an object")
	}
	if offsets.Exists() {
		if !offsets.IsObject() {
			return false, errors.New("OpenAI chat_completions content_filter_offsets was not an object")
		}
		for _, field := range []string{"check_offset", "start_offset", "end_offset"} {
			if value := offsets.Get(field); value.Exists() && !nonNegativeIntegerGJSON(value) {
				return false, fmt.Errorf("OpenAI chat_completions content_filter_offsets.%s was not a non-negative integer", field)
			}
		}
	}
	return true, nil
}

func (s *openAIChatChoiceStreamState) complete() bool {
	return len(s.seen) > 0 && len(s.finished) == len(s.seen)
}

func openAIChatBufferedChoicesComplete(payload []byte) bool {
	choices := gjson.GetBytes(payload, "choices")
	if !choices.IsArray() || !gjsonCollectionHasValues(choices) {
		return false
	}
	complete := true
	count := 0
	choices.ForEach(func(_, choice gjson.Result) bool {
		if count >= maxOpenAIChatChoicesPerAttempt {
			complete = false
			return false
		}
		count++
		finishReason := choice.Get("finish_reason")
		if finishReason.Type != gjson.String || strings.TrimSpace(finishReason.String()) == "" {
			complete = false
			return false
		}
		message := choice.Get("message")
		reason := strings.TrimSpace(finishReason.String())
		if (reason == "stop" || reason == "length") && !choiceHasOpenAIChatSemanticOutput(choice) {
			complete = false
			return false
		}
		hasTools := message.Get("tool_calls").Exists()
		hasLegacy := message.Get("function_call").Exists()
		if (finishReason.String() == "tool_calls") != hasTools || (finishReason.String() == "function_call") != hasLegacy {
			complete = false
			return false
		}
		return true
	})
	return complete
}

func openAIChatPayloadContainsAudio(payload []byte) bool {
	choices := gjson.GetBytes(payload, "choices")
	if !choices.IsArray() {
		return false
	}
	containsAudio := false
	choices.ForEach(func(_, choice gjson.Result) bool {
		if choice.Get("delta.audio").Exists() || choice.Get("message.audio").Exists() {
			containsAudio = true
			return false
		}
		return true
	})
	return containsAudio
}

// classifyOpenAIChatStreamPayload distinguishes a valid non-semantic preamble
// from a malformed provider envelope. A later valid chunk must never wash an
// empty/structurally-invalid choice into a successful attempt.
func classifyOpenAIChatStreamPayload(payload string) (bool, error) {
	if !gjson.Valid(payload) || !gjson.Parse(payload).IsObject() {
		return false, errors.New("OpenAI chat_completions returned malformed JSON data")
	}
	if err := validateOpenAIResponsesNoDuplicateKnownFields([]byte(payload), providerJSONOpenAIChatRoot); err != nil {
		return false, fmt.Errorf("OpenAI chat_completions returned ambiguous JSON data: %w", err)
	}
	root := gjson.Parse(payload)
	for _, field := range []string{"id", "object", "model"} {
		if value := root.Get(field); value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIChatRetainedIdentifierBytes) {
			return false, fmt.Errorf("OpenAI chat_completions %s was not a bounded string", field)
		}
	}
	for _, field := range []string{"system_fingerprint", "service_tier"} {
		if value := root.Get(field); value.Exists() && value.Type != gjson.Null && (value.Type != gjson.String || len(value.String()) > maxOpenAIChatRetainedIdentifierBytes) {
			return false, fmt.Errorf("OpenAI chat_completions %s was not a bounded string or null", field)
		}
	}
	if created := root.Get("created"); created.Exists() && !nonNegativeIntegerGJSON(created) {
		return false, errors.New("OpenAI chat_completions created was not a non-negative integer")
	}
	if providerError := root.Get("error"); providerError.Exists() && providerError.Type != gjson.Null {
		return false, errors.New("OpenAI chat_completions returned an explicit provider error")
	}
	promptAnnotation, err := validateOpenAIChatPromptAnnotations(root)
	if err != nil {
		return false, err
	}
	if usage := gjson.Get(payload, "usage"); usage.Exists() && usage.Type != gjson.Null && !validOpenAIChatUsageShape(usage) {
		return false, errors.New("OpenAI chat_completions usage was malformed")
	}
	choices := gjson.Get(payload, "choices")
	if !choices.Exists() {
		if gjson.Get(payload, "usage").IsObject() {
			return false, nil
		}
		return false, errors.New("OpenAI chat_completions provider event omitted choices")
	}
	if !choices.IsArray() {
		return false, errors.New("OpenAI chat_completions choices was not an array")
	}
	if !gjsonCollectionHasValues(choices) {
		if gjson.Get(payload, "usage").IsObject() || promptAnnotation {
			return false, nil
		}
		return false, errors.New("OpenAI chat_completions provider event contained empty choices")
	}
	recognized := false
	seenChoiceIndexes := make(map[int64]struct{})
	position := 0
	var classifyErr error
	choices.ForEach(func(_, choice gjson.Result) bool {
		if position >= maxOpenAIChatChoicesPerAttempt {
			classifyErr = errors.New("OpenAI chat_completions contained too many choices")
			return false
		}
		if !choice.IsObject() {
			classifyErr = errors.New("OpenAI chat_completions choice was not an object")
			return false
		}
		index := choice.Get("index")
		if index.Exists() && !nonNegativeIntegerGJSON(index) {
			classifyErr = errors.New("OpenAI chat_completions choice index was not a non-negative integer")
			return false
		}
		choiceIndex := int64(position)
		if index.Exists() {
			choiceIndex = index.Int()
		}
		if _, duplicate := seenChoiceIndexes[choiceIndex]; duplicate {
			classifyErr = fmt.Errorf("OpenAI chat_completions duplicated choice index %d", choiceIndex)
			return false
		}
		seenChoiceIndexes[choiceIndex] = struct{}{}
		delta := choice.Get("delta")
		message := choice.Get("message")
		text := choice.Get("text")
		annotation, err := validateOpenAIChatFilterAnnotation(choice)
		if err != nil {
			classifyErr = err
			return false
		}
		if delta.Exists() && !delta.IsObject() {
			classifyErr = errors.New("OpenAI chat_completions delta was not an object")
			return false
		}
		if message.Exists() && !message.IsObject() {
			classifyErr = errors.New("OpenAI chat_completions message was not an object")
			return false
		}
		if text.Exists() && text.Type != gjson.String {
			classifyErr = errors.New("OpenAI chat_completions text was not a string")
			return false
		}
		if logprobs := choice.Get("logprobs"); logprobs.Exists() && logprobs.Type != gjson.Null && !logprobs.IsObject() {
			classifyErr = errors.New("OpenAI chat_completions logprobs was not an object or null")
			return false
		}
		if err := validateOpenAIChatDeltaShape(delta); err != nil {
			classifyErr = err
			return false
		}
		if err := validateOpenAIChatDeltaShape(message); err != nil {
			classifyErr = err
			return false
		}
		if message.IsObject() {
			if err := validateOpenAIChatBufferedMessageShape(message); err != nil {
				classifyErr = err
				return false
			}
		}
		if !delta.IsObject() && !message.IsObject() && text.Type != gjson.String && !annotation {
			classifyErr = errors.New("OpenAI chat_completions choice omitted delta/message/text")
			return false
		}
		finishReasonValue := choice.Get("finish_reason")
		if finishReasonValue.Exists() && finishReasonValue.Type != gjson.String && finishReasonValue.Type != gjson.Null {
			classifyErr = errors.New("OpenAI chat_completions finish_reason was not a string")
			return false
		}
		finishReason := strings.TrimSpace(finishReasonValue.String())
		if delta.IsObject() && !gjsonCollectionHasValues(delta) && finishReason == "" && !message.IsObject() && text.Type != gjson.String && !annotation {
			classifyErr = errors.New("OpenAI chat_completions choice contained an empty delta")
			return false
		}
		if message.IsObject() && !gjsonCollectionHasValues(message) && !delta.IsObject() && text.Type != gjson.String && !annotation {
			classifyErr = errors.New("OpenAI chat_completions choice contained an empty message")
			return false
		}
		if choiceHasOpenAIChatSemanticOutput(choice) {
			recognized = true
		}
		// A non-empty finish_reason is itself a recognizable provider terminal.
		// Azure/OpenAI content filtering can legitimately return no content with
		// finish_reason=content_filter. The large-request empty-stop refusal path
		// remains handled separately by the silent-refusal detector.
		if finishReason != "" {
			recognized = true
		}
		position++
		return true
	})
	if classifyErr != nil {
		return false, classifyErr
	}
	return recognized, nil
}

func validateOpenAIChatPromptAnnotations(root gjson.Result) (bool, error) {
	found := false
	for _, field := range []string{"prompt_annotations", "prompt_filter_results"} {
		annotations := root.Get(field)
		if !annotations.Exists() {
			continue
		}
		found = true
		if !annotations.IsArray() {
			return false, fmt.Errorf("OpenAI chat_completions %s was not an array", field)
		}
		annotationCount := 0
		var shapeErr error
		annotations.ForEach(func(_, annotation gjson.Result) bool {
			if annotationCount >= maxOpenAIChatChoicesPerAttempt {
				shapeErr = fmt.Errorf("OpenAI chat_completions %s contained too many items", field)
				return false
			}
			annotationCount++
			if !annotation.IsObject() {
				shapeErr = fmt.Errorf("OpenAI chat_completions %s item was not an object", field)
				return false
			}
			if index := annotation.Get("prompt_index"); index.Exists() && !nonNegativeIntegerGJSON(index) {
				shapeErr = fmt.Errorf("OpenAI chat_completions %s prompt_index was not a non-negative integer", field)
				return false
			}
			if results := annotation.Get("content_filter_results"); results.Exists() && !results.IsObject() {
				shapeErr = fmt.Errorf("OpenAI chat_completions %s content_filter_results was not an object", field)
				return false
			}
			return true
		})
		if shapeErr != nil {
			return false, shapeErr
		}
	}
	return found, nil
}

func newOpenAIIncompleteChatStreamFailover(resp *http.Response, message string) *UpstreamFailoverError {
	failure := &UpstreamFailoverError{
		StatusCode:                http.StatusBadGateway,
		ResponseBody:              []byte(`{"error":{"type":"upstream_error","message":` + strconv.Quote(message) + `}}`),
		RetryableOnSameAccount:    true,
		CaptureResponseIncomplete: true,
	}
	if resp == nil {
		return failure
	}
	failure.RequestHeaders = captureRequestHeadersFromResponse(resp)
	failure.ResponseHeaders = resp.Header.Clone()
	failure.UpstreamEndpoint = captureEndpointFromResponse(resp)
	failure.HasUpstreamHTTPResponse = true
	failure.UpstreamHTTPStatus = resp.StatusCode
	return failure
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
	return choices.Exists() && choices.IsArray() && !gjsonCollectionHasValues(choices)
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
	requestID := captureProviderRequestID(resp.Header)

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if err != nil {
		return nil, errors.Join(newOpenAIIncompleteChatStreamFailover(resp, "failed to read upstream Chat Completions response"), err)
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveOpenAI(respBody, strings.TrimSpace(gjson.GetBytes(respBody, "type").String()))
	if !gjson.ValidBytes(respBody) || !openAIChatPayloadIsValidProviderPayload(string(respBody)) || !openAIChatBufferedChoicesComplete(respBody) {
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "OpenAI chat_completions returned an invalid terminal JSON response")
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
		RequestID:                     requestID,
		Usage:                         usage,
		Model:                         originalModel,
		BillingModel:                  billingModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		ReasoningEffort:               reasoningEffort,
		ServiceTier:                   serviceTier,
		Stream:                        false,
		Duration:                      time.Since(startTime),
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
