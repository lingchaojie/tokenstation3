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

// ForwardAsChatCompletions accepts an OpenAI Chat Completions API request body,
// converts it to Anthropic Messages format (chained via Responses format),
// forwards to the Anthropic upstream, and converts the response back to Chat
// Completions format. This enables Chat Completions clients to access Anthropic
// models through Anthropic platform groups.
func (s *GatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (*ForwardResult, error) {
	startTime := time.Now()
	beginCaptureAttempt(c)
	captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil && CaptureMayApplyFor(c, string(account.Platform))
	if captureEnabled {
		setCapturePlatform(c, string(account.Platform))
	}

	// 1. Parse Chat Completions request
	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	// 2. Convert CC → Responses → Anthropic (chained conversion)
	responsesReq, err := apicompat.ChatCompletionsToResponses(&ccReq)
	if err != nil {
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 3. Force upstream streaming
	anthropicReq.Stream = true
	reqStream := true

	// 4. Model mapping
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
	anthropicReq.Model = mappedModel

	logger.L().Debug("gateway forward_as_chat_completions: model mapping applied",
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

	// 6. Apply Claude Code mimicry for OAuth accounts.
	// Chat Completions 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
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

	kiroDirectMode := isKiroDirectModeAccount(account)
	var resp *http.Response
	finishCapture := func() {}
	if kiroDirectMode {
		if captureEnabled {
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
			writeGatewayCCError(c, http.StatusBadGateway, "server_error", "Upstream request failed")
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

		// 10. Build upstream request
		upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, reqStream)
		upstreamReq, _, err := s.buildUpstreamRequest(upstreamCtx, c, account, anthropicBody, token, tokenType, mappedModel, reqStream, shouldMimicClaudeCode)
		releaseUpstreamCtx()
		if err != nil {
			return nil, fmt.Errorf("build upstream request: %w", err)
		}

		// 11. Send request
		if captureEnabled {
			setCaptureUpstreamRequest(c, upstreamReq, s.cfg.Gateway.Capture.MaxBodyBytes)
		}
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
		if captureEnabled {
			setCaptureUpstreamResponse(c, resp)
			finishCapture = beginCaptureResponse(c, resp, true, s.cfg.Gateway.Capture.MaxBodyBytes)
		}
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
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
			writeGatewayCCError(c, http.StatusBadGateway, "server_error", "Upstream request failed")
			return nil, fmt.Errorf("upstream request failed: %s", safeErr)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// 12. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, responseTruncated, _ := s.readWebChatUpstreamErrorBody(ctx, resp)
		_ = resp.Body.Close()
		finishCapture()
		if responseTruncated {
			markCaptureResultTruncated(c)
		}
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
			s.submitWebChatFinalGatewayErrorCapture(ctx, c, account, originalModel, mappedModel, "/v1/messages", clientStream, resp, respBody)
			return nil, &UpstreamFailoverError{
				StatusCode:              resp.StatusCode,
				ResponseBody:            respBody,
				RequestHeaders:          captureRequestHeadersFromResponse(resp),
				ResponseHeaders:         resp.Header.Clone(),
				UpstreamEndpoint:        captureEndpointFromResponse(resp),
				Platform:                string(account.Platform),
				HasUpstreamHTTPResponse: true,
			}
		}

		s.submitWebChatFinalGatewayErrorCapture(ctx, c, account, originalModel, mappedModel, "/v1/messages", clientStream, resp, respBody)
		writeGatewayCCError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, newTerminalProviderHTTPError(account, resp, respBody)
	}

	// 13. Extract reasoning effort from CC request body
	reasoningEffort := extractCCReasoningEffortFromBody(body)
	// 国产模型默认 effort 补充：本路径是客户端 CC 请求 → Anthropic 上游，
	// 如果上游是 passback-required 国产模型 (Kimi-anthropic / GLM-anthropic / MiniMax)
	// 且客户端在 body 里传了 thinking.type=enabled，补中默认 effort。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, mappedModel)

	// 14. Handle normal response
	// Read Anthropic SSE → convert to Responses events → convert to CC format
	var result *ForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleCCStreamingFromAnthropic(ctx, resp, c, originalModel, mappedModel, reasoningEffort, startTime, includeUsage, kiroDirectMode)
	} else {
		result, handleErr = s.handleCCBufferedFromAnthropic(resp, c, originalModel, mappedModel, reasoningEffort, startTime, kiroDirectMode)
	}

	finishCapture()
	if handleErr != nil && result == nil {
		result = failedForwardResultForError(c, resp, originalModel, mappedModel, clientStream, startTime, handleErr)
	}
	return finalizeForwardResult(c, result), handleErr
}

// extractCCReasoningEffortFromBody reads reasoning effort from a Chat Completions
// request body. It checks both nested (reasoning.effort) and flat (reasoning_effort)
// formats used by OpenAI-compatible clients.
func extractCCReasoningEffortFromBody(body []byte) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		return nil
	}
	normalized := normalizeOpenAIReasoningEffort(raw)
	if normalized == "" {
		return nil
	}
	return &normalized
}

// handleCCBufferedFromAnthropic reads Anthropic SSE events, assembles the full
// response, then converts Anthropic → Responses → Chat Completions.
func (s *GatewayService) handleCCBufferedFromAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	allowKiroMarkedFinalUsage bool,
) (*ForwardResult, error) {
	requestID := captureProviderRequestID(resp.Header)

	lineReader := newProviderLineReader(resp, s.cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, s.cfg)
	})
	defer lineReader.Close()

	var finalResp *apicompat.AnthropicResponse
	var contentAccumulator anthropicBufferedContentAccumulator
	var usage ClaudeUsage
	hasKiroMarkedFinalUsage := false
	terminalObserved := false
	providerPhase := anthropicProviderAwaitingStart
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
		// SSE 规范允许 `event:xxx`（冒号后无空格）：Kimi 等 Anthropic 兼容上游
		// 返回紧凑格式，严格匹配 "event: " 会丢弃全部事件（#4653 同根因）。
		eventType, ok := extractOpenAISSEEventLine(line)
		if !ok {
			if payload, dataLine := extractOpenAISSEDataLine(line); dataLine && strings.TrimSpace(payload) != "" {
				incompleteProviderTail = true
				break
			}
			continue
		}

		dataLine, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			incompleteProviderTail = true
			break
		}
		payload, ok := extractOpenAISSEDataLine(dataLine)
		if !ok {
			incompleteProviderTail = true
			break
		}

		if _, err := validateAnthropicProviderJSONEvent(&providerPhase, eventType, []byte(payload)); err != nil {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(err))
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			lineReader.DrainCaptureOnParserFailure(ginRequestContext(c))
			return nil, newIncompleteProviderStreamFailover(resp, fmt.Sprintf("decode validated Anthropic event %q", eventType))
		}
		if event.Type == "message_stop" {
			terminalObserved = true
		}

		// message_start carries the initial response structure and cache usage
		if event.Type == "message_start" && event.Message != nil && validAnthropicMessageStartPayload([]byte(payload)) {
			finalResp = event.Message
			if mergeAnthropicUsageFromPayload(&usage, event.Message.Usage, payload, allowKiroMarkedFinalUsage) {
				hasKiroMarkedFinalUsage = true
			}
		}

		// message_delta carries final usage and stop_reason
		if event.Type == "message_delta" {
			if event.Usage != nil {
				if mergeAnthropicUsageFromPayload(&usage, *event.Usage, payload, allowKiroMarkedFinalUsage) {
					hasKiroMarkedFinalUsage = true
				}
			}
			mergeKiroCreditsFromAnthropicPayload(&usage, payload)
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			contentAccumulator.start(finalResp, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			contentAccumulator.delta(*event.Index, event.Delta)
		}
	}

	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_cc buffered: read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
		return nil, newIncompleteProviderStreamFailover(resp, "upstream stream read failed before message_stop: "+sanitizeStreamError(scanErr))
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

	// Update usage from accumulated delta
	if hasKiroMarkedFinalUsage || usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		finalResp.Usage = apicompat.AnthropicUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		}
	}

	// Chain: Anthropic → Responses → Chat Completions
	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	ccResp := apicompat.ResponsesToChatCompletions(responsesResp, originalModel)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Marshal then bytes-replace so tool name mapping is reversed at byte level
	// (parity with Parrot non-stream flow that marshals → restore → emit).
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, ccResp)
	}

	return &ForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// handleCCStreamingFromAnthropic reads Anthropic SSE events, converts each
// to Responses events, then to Chat Completions chunks, and writes them.
func (s *GatewayService) handleCCStreamingFromAnthropic(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	includeUsage bool,
	allowKiroMarkedFinalUsage bool,
) (*ForwardResult, error) {
	requestID := captureProviderRequestID(resp.Header)
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

	// Use Anthropic→Responses state machine, then convert Responses→CC
	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = originalModel
	ccState := apicompat.NewResponsesEventToChatState()
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false
	semanticOutput := false
	terminalObserved := false
	providerPayloadObserved := false
	incompleteProviderTail := false
	providerPhase := anthropicProviderAwaitingStart
	var stagedWriteErr error

	lineReader := newProviderLineReader(resp, s.cfg, func(r io.Reader) *bufio.Scanner {
		scanner := bufio.NewScanner(r)
		maxLineSize := defaultMaxLineSize
		if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
			maxLineSize = s.cfg.Gateway.MaxLineSize
		}
		scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
		return scanner
	})
	defer lineReader.Close()
	var scanErr error

	resultWithUsage := func() *ForwardResult {
		return &ForwardResult{
			RequestID:        requestID,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    mappedModel,
			ReasoningEffort:  reasoningEffort,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     firstTokenMs,
			ClientDisconnect: clientDisconnected,
		}
	}

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		// Reverse tool name mapping: fake → real, per-chunk bytes.Replace.
		// c 可能持有请求侧注入的 ToolNameRewrite；无则仅做静态前缀还原。
		out := string(reverseToolNamesIfPresent(c, []byte(sse)))
		if clientDisconnected {
			return false
		}
		if err := staged.write(c, writeHeaders, out, semanticOutput || (terminalObserved && providerPayloadObserved)); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if errors.As(err, &clientWriteErr) {
				clientDisconnected = true
				return false
			}
			stagedWriteErr = err
			return true
		}
		if staged.committed {
			captureWebChatStreamString(ctx, out)
		}
		return false
	}

	processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent, payload string) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from message_delta
		if event.Type == "message_delta" && event.Usage != nil {
			if mergeAnthropicUsageFromPayload(&usage, *event.Usage, payload, allowKiroMarkedFinalUsage) {
				replaceAnthropicResponsesStateUsage(anthState, usage)
			}
		}
		// Also capture usage from message_start (carries cache fields)
		if event.Type == "message_start" && event.Message != nil {
			providerPayloadObserved = validAnthropicMessageStartPayload([]byte(payload))
			if mergeAnthropicUsageFromPayload(&usage, event.Message.Usage, payload, allowKiroMarkedFinalUsage) {
				replaceAnthropicResponsesStateUsage(anthState, usage)
			}
		}
		if anthropicSSEEventHasSemanticOutput(payload) {
			semanticOutput = true
		}
		if event.Type == "message_stop" {
			terminalObserved = true
		}

		// Chain: Anthropic event → Responses events → CC chunks
		responsesEvents := apicompat.AnthropicEventToResponsesEvents(event, anthState)
		if conversionErr := anthState.Err(); conversionErr != nil {
			stagedWriteErr = conversionErr
			return true
		}
		for _, resEvt := range responsesEvents {
			ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				if disconnected := writeChunk(chunk); disconnected {
					return true
				}
			}
		}
		if !clientDisconnected && staged.committed {
			c.Writer.Flush()
		}
		return false
	}

	for {
		line, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			break
		}
		// 与缓冲路径一致：接受 SSE 紧凑格式（冒号后无空格，#4653 同根因）。
		eventType, ok := extractOpenAISSEEventLine(line)
		if !ok {
			if payload, dataLine := extractOpenAISSEDataLine(line); dataLine && strings.TrimSpace(payload) != "" {
				incompleteProviderTail = true
				break
			}
			continue
		}

		dataLine, ok, err := lineReader.Next()
		if err != nil {
			scanErr = err
			break
		}
		if !ok {
			incompleteProviderTail = true
			break
		}
		payload, ok := extractOpenAISSEDataLine(dataLine)
		if !ok {
			incompleteProviderTail = true
			break
		}

		if _, err := validateAnthropicProviderJSONEvent(&providerPhase, eventType, []byte(payload)); err != nil {
			lineReader.DrainCaptureOnParserFailure(ctx)
			if staged.committed || clientDisconnected {
				result := resultWithUsage()
				result.CaptureTerminalError = true
				return result, err
			}
			return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(err))
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			invalidEventErr := fmt.Errorf("invalid JSON for Anthropic event %q: %w", eventType, err)
			lineReader.DrainCaptureOnParserFailure(ctx)
			if staged.committed || clientDisconnected {
				result := resultWithUsage()
				result.CaptureTerminalError = true
				return result, invalidEventErr
			}
			return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(invalidEventErr))
		}
		mergeKiroCreditsFromAnthropicPayload(&usage, payload)

		if processAnthropicEvent(&event, payload) {
			if stagedWriteErr != nil {
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "upstream pre-output stage failed: "+sanitizeStreamError(stagedWriteErr))
				}
				return resultWithUsage(), stagedWriteErr
			}
			return resultWithUsage(), nil
		}
	}

	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_cc stream: read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
		if staged.committed || clientDisconnected {
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, scanErr
		}
		return nil, newIncompleteProviderStreamFailover(resp, "upstream stream read failed before semantic output: "+sanitizeStreamError(scanErr))
	}
	if incompleteProviderTail {
		lineReader.DrainCaptureOnParserFailure(ctx)
		incompleteErr := errors.New("upstream stream ended with an incomplete Anthropic provider event")
		if staged.committed || clientDisconnected {
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, incompleteErr
		}
		return nil, newIncompleteProviderStreamFailover(resp, incompleteErr.Error())
	}
	if !terminalObserved {
		missingTerminalErr := fmt.Errorf("stream usage incomplete: missing terminal event")
		if staged.committed || clientDisconnected {
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, missingTerminalErr
		}
		return nil, newIncompleteProviderStreamFailover(resp, missingTerminalErr.Error())
	}
	if !providerPayloadObserved {
		invalidStreamErr := fmt.Errorf("stream ended without a valid provider message_start")
		if staged.committed || clientDisconnected {
			result := resultWithUsage()
			result.CaptureTerminalError = true
			return result, invalidStreamErr
		}
		return nil, newIncompleteProviderStreamFailover(resp, invalidStreamErr.Error())
	}

	// Finalize both state machines
	finalResEvents := apicompat.FinalizeAnthropicResponsesStream(anthState)
	for _, resEvt := range finalResEvents {
		ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range ccChunks {
			writeChunk(chunk) //nolint:errcheck
		}
	}
	finalCCChunks := apicompat.FinalizeResponsesChatStream(ccState)
	for _, chunk := range finalCCChunks {
		writeChunk(chunk) //nolint:errcheck
	}

	// Write [DONE] marker
	if !clientDisconnected {
		if err := staged.write(c, writeHeaders, "data: [DONE]\n\n", true); err != nil {
			return resultWithUsage(), err
		}
		captureWebChatStreamString(ctx, "data: [DONE]\n\n")
	}

	return resultWithUsage(), nil
}

func newIncompleteProviderStreamFailover(resp *http.Response, message string) *UpstreamFailoverError {
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "upstream_disconnected",
			"message": message,
		},
	})
	return &UpstreamFailoverError{
		StatusCode:              http.StatusBadGateway,
		ResponseBody:            body,
		RequestHeaders:          captureRequestHeadersFromResponse(resp),
		ResponseHeaders:         resp.Header.Clone(),
		UpstreamEndpoint:        captureEndpointFromResponse(resp),
		HasUpstreamHTTPResponse: true,
		RetryableOnSameAccount:  true,
	}
}

// newInvalidProviderResponseFailover marks a fully selected HTTP 2xx response
// that cannot be consumed as a valid provider terminal. Retrying the identical
// account cannot repair that deterministic response shape, so switch accounts.
func newInvalidProviderResponseFailover(resp *http.Response, message string) *UpstreamFailoverError {
	failure := newIncompleteProviderStreamFailover(resp, message)
	failure.RetryableOnSameAccount = false
	return failure
}

// writeGatewayCCError writes an error in OpenAI Chat Completions format for
// the Anthropic-upstream CC forwarding path.
func writeGatewayCCError(c *gin.Context, statusCode int, errType, message string) {
	MarkResponseCommitted(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}
