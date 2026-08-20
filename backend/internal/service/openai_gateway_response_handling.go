package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openaiStreamingResult streaming response result
type openaiStreamingResult struct {
	usage            *OpenAIUsage
	firstTokenMs     *int
	responseID       string
	imageCount       int
	imageOutputSizes []string
	imageResults     []openAIResponsesImageResult
}

type openaiNonStreamingResult struct {
	*OpenAIUsage
	usage            *OpenAIUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
	imageResults     []openAIResponsesImageResult
}

func (s *OpenAIGatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel string) (*openaiStreamingResult, error) {
	return s.handleStreamingResponseWithReasoning(ctx, resp, c, account, startTime, originalModel, mappedModel, "")
}

func (s *OpenAIGatewayService) handleStreamingResponseWithReasoning(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (*openaiStreamingResult, error) {
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	firstOutputTimeout := time.Duration(0)
	if account != nil && account.Platform == PlatformOpenAI {
		firstOutputTimeout = s.openAIFirstOutputTimeout(reasoningEffort)
	}
	guardFirstOutput := firstOutputTimeout > 0
	var attemptResponseHeaders http.Header
	if s.responseHeaderFilter != nil {
		attemptResponseHeaders = responseheaders.FilterHeaders(resp.Header, s.responseHeaderFilter)
	} else if requestID := strings.TrimSpace(resp.Header.Get("x-request-id")); requestID != "" {
		attemptResponseHeaders = http.Header{"X-Request-Id": []string{requestID}}
	}

	// Set SSE response headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Pass through other headers
	applyAttemptResponseHeaders := func() {
		if len(attemptResponseHeaders) == 0 || c.Writer.Written() {
			return
		}
		for key, values := range attemptResponseHeaders {
			for _, value := range values {
				c.Writer.Header().Add(key, value)
			}
		}
		// These headers describe this gateway's SSE stream and are stable across
		// account attempts. Keep them authoritative over upstream values.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	var firstTokenMs *int
	bufferedWriter := bufio.NewWriterSize(w, 4*1024)
	firstOutputStage := newDefaultOpenAIFirstOutputStage()
	defer func() {
		if err := firstOutputStage.Close(); err != nil {
			logger.LegacyPrintf("service.openai_gateway", "OpenAI first-output staging cleanup failed: account=%d model=%s error=%v", account.ID, originalModel, err)
		}
	}()
	writePendingString := func(value string) (int, error) {
		if firstOutputStage != nil && firstTokenMs == nil && !firstOutputStage.closed {
			return firstOutputStage.WriteString(value)
		}
		return bufferedWriter.WriteString(value)
	}
	pendingBytes := func() int64 {
		if firstOutputStage != nil && firstTokenMs == nil && !firstOutputStage.closed {
			return firstOutputStage.Buffered()
		}
		return int64(bufferedWriter.Buffered())
	}
	flushBuffered := func() error {
		if firstOutputStage != nil && firstTokenMs == nil && !firstOutputStage.closed {
			_, commitErr := firstOutputStage.CommitTo(w)
			deliveryErr, cleanupErr := splitOpenAIFirstOutputCommitError(commitErr)
			if cleanupErr != nil {
				logger.LegacyPrintf("service.openai_gateway", "OpenAI first-output staging cleanup failed after commit: account=%d model=%s error=%v", account.ID, originalModel, cleanupErr)
			}
			if deliveryErr != nil {
				return deliveryErr
			}
		} else {
			if err := bufferedWriter.Flush(); err != nil {
				return err
			}
		}
		flusher.Flush()
		return nil
	}

	usage := &OpenAIUsage{}
	imageCounter := newOpenAIImageOutputCounter()
	retainImageResults := hasWebChatStreamCapture(ctx)
	var imageResults []openAIResponsesImageResult
	var imageResultSeen map[string]int
	var imageRetentionBudget *openAIResponsesImageRetentionBudget
	if retainImageResults {
		imageResults = make([]openAIResponsesImageResult, 0, 1)
		imageResultSeen = make(map[string]int)
		imageRetentionBudget = &openAIResponsesImageRetentionBudget{}
	}
	responseID := ""
	var firstOutputScanGuard atomic.Bool
	firstOutputScanGuard.Store(true)
	providerReader, readActivity := providerBodyReaderWithActivity(resp.Body)
	scanner := bufio.NewScanner(providerReader)
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	scanner.Split(openAIFirstOutputDynamicScanLines(&firstOutputScanGuard))
	documentScanner := newOpenAISSEJSONDocumentScanner(scanner)

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	// Grok: always enforce an upstream-read idle so hung SSE bodies fail over
	// instead of holding the OAuth slot until the client cancels. Prefer the
	// global gateway setting when set; otherwise apply a Grok-only default.
	if account != nil && account.Platform == PlatformGrok {
		cfgSec := 0
		if s.cfg != nil {
			cfgSec = s.cfg.Gateway.StreamDataIntervalTimeout
		}
		streamInterval = resolveGrokStreamIdleTimeout(cfgSec)
	}
	// 仅监控上游数据间隔超时，不被下游写入阻塞影响
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	keepaliveInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	// 下游 keepalive 仅用于防止代理空闲断开
	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}

	var firstOutputTimer *time.Timer
	var firstOutputCh <-chan time.Time
	if firstOutputTimeout > 0 {
		remaining := time.Until(startTime.Add(firstOutputTimeout))
		if remaining <= 0 {
			remaining = time.Nanosecond
		}
		firstOutputTimer = time.NewTimer(remaining)
		firstOutputCh = firstOutputTimer.C
		defer firstOutputTimer.Stop()
	}
	stopFirstOutputTimer := func() {
		if firstOutputTimer == nil {
			return
		}
		if !firstOutputTimer.Stop() {
			select {
			case <-firstOutputTimer.C:
			default:
			}
		}
		firstOutputTimer = nil
		firstOutputCh = nil
	}
	// Track downstream writes separately from upstream reads: pre-output failover
	// can buffer response.created / response.in_progress, so keepalive must be
	// based on downstream idle time.
	lastDownstreamWriteAt := time.Now()

	// 仅发送一次错误事件，避免多次写入导致协议混乱。
	// 注意：OpenAI `/v1/responses` streaming 事件必须符合 OpenAI Responses schema；
	// 否则下游 SDK（例如 OpenCode）会因为类型校验失败而报错。
	errorEventSent := false
	clientDisconnected := false // 客户端断开后继续 drain 上游以收集 usage
	sawTerminalEvent := false
	responsesState := openAIResponsesSSEAttemptState{}
	sawFailedEvent := false
	failedMessage := ""
	clientOutputStarted := false
	upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	var streamEarlyErr error
	eventInProgress := false
	eventStartsClientOutput := false
	eventShouldFlush := false
	pendingProviderEventType := ""
	handlePendingWriteError := func(err error) {
		if firstOutputStage != nil && firstTokenMs == nil && !firstOutputStage.closed {
			message := "OpenAI first-output staging failed"
			if errors.Is(err, errOpenAIFirstOutputStageLimit) {
				message = "OpenAI first-output staging limit exceeded"
			}
			logger.LegacyPrintf("service.openai_gateway", "%s: account=%d model=%s error=%v", message, account.ID, originalModel, err)
			failoverErr := s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, nil, message, resp)
			failoverErr.SafeToFailoverAfterWrite = true
			streamEarlyErr = failoverErr
			_ = resp.Body.Close()
			return
		}
		clientDisconnected = true
		logger.LegacyPrintf("service.openai_gateway", "Client disconnected during streaming, continuing to drain upstream for billing")
	}
	completeGuardedEvent := func(queueDrained bool) {
		completedSemanticEvent := eventStartsClientOutput
		shouldFlush := eventShouldFlush || (queueDrained && clientOutputStarted)
		eventInProgress = false
		if !clientDisconnected {
			if completedSemanticEvent {
				applyAttemptResponseHeaders()
			}
			if shouldFlush {
				if err := flushBuffered(); err != nil {
					clientDisconnected = true
					logger.LegacyPrintf("service.openai_gateway", "Client disconnected during streaming flush, continuing to drain upstream for billing")
				} else {
					clientOutputStarted = true
					lastDownstreamWriteAt = time.Now()
				}
			}
		}
		if completedSemanticEvent && firstTokenMs == nil {
			firstOutputScanGuard.Store(false)
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
			stopFirstOutputTimer()
		}
		eventStartsClientOutput = false
		eventShouldFlush = false
	}
	sendErrorEvent := func(reason string) {
		if errorEventSent || clientDisconnected {
			return
		}
		errorEventSent = true
		payload := `{"type":"error","sequence_number":0,"error":{"type":"upstream_error","message":` + strconv.Quote(reason) + `,"code":` + strconv.Quote(reason) + `}}`
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			return
		}
		if _, err := writePendingString("data: " + payload + "\n\n"); err != nil {
			clientDisconnected = true
			return
		}
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			return
		}
		clientOutputStarted = true
		lastDownstreamWriteAt = time.Now()
	}

	needModelReplace := originalModel != mappedModel
	streamOutputAccumulator := apicompat.NewBufferedResponseAccumulator()
	streamImageOutputs := make([]json.RawMessage, 0, 1)
	streamSeenImages := make(map[string]struct{})
	setOutputRetentionError := func(retainErr error, payload []byte) {
		if retainErr == nil {
			return
		}
		message := "OpenAI Responses output retention failed: " + retainErr.Error()
		if !openAIStreamClientOutputStarted(c, clientOutputStarted) {
			message = "OpenAI first-output staging limit exceeded: " + retainErr.Error()
			failure := s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, payload, message, resp)
			failure.SafeToFailoverAfterWrite = true
			streamEarlyErr = failure
		} else {
			streamEarlyErr = errors.New(message)
		}
	}
	resultWithUsage := func() *openaiStreamingResult {
		return &openaiStreamingResult{
			usage:            usage,
			firstTokenMs:     firstTokenMs,
			responseID:       responseID,
			imageCount:       imageCounter.Count(),
			imageOutputSizes: imageCounter.Sizes(),
			imageResults:     append([]openAIResponsesImageResult(nil), imageResults...),
		}
	}
	flushPending := func(disconnectMessage string) {
		if clientDisconnected || pendingBytes() == 0 {
			return
		}
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			logger.LegacyPrintf("service.openai_gateway", "%s", disconnectMessage)
			return
		}
		clientOutputStarted = true
		lastDownstreamWriteAt = time.Now()
	}
	finalizeStream := func() (*openaiStreamingResult, error) {
		if eventInProgress {
			// EOF dispatches the final SSE event even without a trailing blank line.
			completeGuardedEvent(true)
		}
		if sawTerminalEvent && !sawFailedEvent {
			s.clearOpenAIProxyStreamDisconnect(account)
		}
		if !sawTerminalEvent && !openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
			return resultWithUsage(), s.newOpenAIStreamFailoverErrorFromResponse(
				c,
				account,
				false,
				upstreamRequestID,
				nil,
				"OpenAI stream ended before a terminal event",
				resp,
			)
		}
		flushPending("Client disconnected during final flush, returning collected usage")
		if !sawTerminalEvent {
			if openAIStreamClientOutputStarted(c, clientOutputStarted) && !clientDisconnected {
				s.recordOpenAIProxyStreamDisconnect(account, errors.New("stream ended before terminal event"), upstreamRequestID)
			}
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: missing terminal event")
		}
		if sawFailedEvent {
			return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
		}
		return resultWithUsage(), nil
	}
	handleScanErr := func(scanErr error) (*openaiStreamingResult, error, bool) {
		if scanErr == nil {
			return nil, nil, false
		}
		if errors.Is(scanErr, errOpenAIFirstOutputScannerLimit) &&
			!openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
			logger.LegacyPrintf("service.openai_gateway", "SSE token exceeded guarded first-output limit: account=%d limit=%d error=%v", account.ID, openAIFirstOutputStageMaxBytes+openAIFirstOutputScannerFramingAllowance, scanErr)
			failoverErr := s.newOpenAIStreamFailoverErrorFromResponse(
				c, account, false, upstreamRequestID, nil,
				"OpenAI SSE line exceeds guarded first-output limit",
				resp,
			)
			failoverErr.SafeToFailoverAfterWrite = true
			return resultWithUsage(), failoverErr, true
		}
		if errors.Is(scanErr, bufio.ErrTooLong) &&
			!openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
			logger.LegacyPrintf("service.openai_gateway", "SSE line too long before first output: account=%d max_size=%d error=%v", account.ID, maxLineSize, scanErr)
			failoverErr := s.newOpenAIStreamFailoverErrorFromResponse(
				c, account, false, upstreamRequestID, nil,
				"OpenAI SSE line exceeds guarded first-output limit",
				resp,
			)
			failoverErr.SafeToFailoverAfterWrite = true
			return resultWithUsage(), failoverErr, true
		}
		// A protocol terminal followed by a transport/scanner error is not a
		// clean EOF. Preserve the already committed result as a partial error;
		// if nothing reached the client yet, the generic pre-output branch below
		// returns a response-aware failover instead.
		// 客户端断开/取消请求时，上游读取往往会返回 context canceled。
		// /v1/responses 的 SSE 事件必须符合 OpenAI 协议；这里不注入自定义 error event，避免下游 SDK 解析失败。
		if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
			if eventShouldFlush {
				flushPending("Client disconnected during canceled stream flush, returning collected usage")
			}
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", scanErr), true
		}
		if errors.Is(scanErr, bufio.ErrTooLong) {
			logger.LegacyPrintf("service.openai_gateway", "SSE line too long: account=%d max_size=%d error=%v", account.ID, maxLineSize, scanErr)
			sendErrorEvent("response_too_large")
			return resultWithUsage(), scanErr, true
		}
		if !openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
			msg := "OpenAI stream disconnected before completion"
			if errText := strings.TrimSpace(scanErr.Error()); errText != "" {
				msg += ": " + errText
			}
			return resultWithUsage(), s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, nil, msg, resp), true
		}
		// 客户端已断开时，上游出错仅影响体验，不影响计费；返回已收集 usage
		if clientDisconnected {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete after disconnect: %w", scanErr), true
		}
		s.recordOpenAIProxyStreamDisconnect(account, scanErr, upstreamRequestID)
		sendErrorEvent("stream_read_error")
		return resultWithUsage(), fmt.Errorf("stream read error: %w", scanErr), true
	}
	processSSELine := func(line string, queueDrained bool) {
		if streamEarlyErr != nil {
			return
		}
		if declared, ok := extractOpenAISSEEventLine(line); ok {
			pendingProviderEventType = declared
			return
		}
		if strings.TrimSpace(line) == "" && pendingProviderEventType != "" {
			// An event-only frame has no data payload. Its header must not leak
			// into the next independent SSE event or downstream wire.
			pendingProviderEventType = ""
			return
		}
		// Extract data from SSE line (supports both "data: " and "data:" formats)
		if data, ok := extractOpenAISSEDataLine(line); ok {
			dataBytes := []byte(data)
			trimmedData := strings.TrimSpace(data)
			declaredEventType := pendingProviderEventType
			protocolError := ""
			switch {
			case sawTerminalEvent && trimmedData != "[DONE]":
				protocolError = "OpenAI Responses data arrived after a terminal event"
			case trimmedData == "[DONE]" && !sawTerminalEvent:
				protocolError = "OpenAI Responses [DONE] arrived before a valid terminal event"
			case trimmedData != "[DONE]" && !gjson.ValidBytes(dataBytes):
				protocolError = "OpenAI Responses returned malformed JSON data"
			}
			if protocolError == "" && trimmedData != "[DONE]" {
				validatedType, err := validateOpenAIResponsesSSEPayload(dataBytes, declaredEventType)
				if err != nil {
					protocolError = err.Error()
				} else if err := responsesState.observe(dataBytes, validatedType); err != nil {
					protocolError = err.Error()
				} else if strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String()) == "" {
					if patched, patchErr := sjson.SetBytes(dataBytes, "type", validatedType); patchErr == nil {
						dataBytes = patched
						data = string(patched)
						line = "data: " + data
					}
				}
			}
			pendingProviderEventType = ""
			if protocolError != "" {
				if !openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
					streamEarlyErr = s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, dataBytes, protocolError, resp)
				} else {
					streamEarlyErr = errors.New(protocolError)
				}
				return
			}
			eventTypeRaw := gjson.GetBytes(dataBytes, "type").String()
			eventType := strings.TrimSpace(eventTypeRaw)
			terminalFailed, terminalFailureStatus := openAIResponsesTerminalFailureStatus(dataBytes, eventType)
			observer.ObserveOpenAI(dataBytes, eventTypeRaw)
			// A framing-only [DONE] or a malformed completed event is not an
			// application terminal. Validate the provider envelope before committing.
			switch eventType {
			case "response.completed", "response.done":
				if !validOpenAIResponsesObject(gjson.GetBytes(dataBytes, "response")) {
					message := "OpenAI terminal event omitted a valid response object"
					if !openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
						streamEarlyErr = s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, dataBytes, message, resp)
					} else {
						sawTerminalEvent = true
						sawFailedEvent = true
						failedMessage = message
						streamEarlyErr = errors.New(message)
					}
					return
				}
				sawTerminalEvent = true
				if terminalFailed {
					sawFailedEvent = true
				}
			case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
				sawTerminalEvent = true
				sawFailedEvent = true
			}
			if responseID == "" {
				responseID = extractOpenAIResponseIDFromJSONBytes(dataBytes)
			}
			forceFlushFailedEvent := false
			if terminalFailed {
				failedMessage = extractOpenAISSEErrorMessage(dataBytes)
				if failedMessage == "" {
					failedMessage = "upstream response ended with status " + terminalFailureStatus
				}
				// response.failed 自带上游已消耗的 usage（input token 通常已扣）；必须先解析
				// 再打 cyber 标记，否则 mark 记到的是解析前的 0，导致流式 cyber 按 0 token 计费
				// 而漏记真实用量。对齐 WS V2 / Chat 流式路径（均先解析 usage 再 Mark）。
				s.parseSSEUsageBytes(dataBytes, usage)
				if hit, code, msg := detectOpenAICyberPolicy(dataBytes); hit {
					MarkOpsCyberPolicy(c, CyberPolicyMark{
						Code:           code,
						Message:        msg,
						Body:           truncateString(string(dataBytes), 4096),
						UpstreamStatus: http.StatusOK,
						UpstreamInTok:  usage.InputTokens,
						UpstreamOutTok: usage.OutputTokens,
					})
				}
				if !openAIStreamClientOutputStarted(c, clientOutputStarted) {
					if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(c, account.Platform, dataBytes, failedMessage); matched {
						sawFailedEvent = true
						// 命中透传规则也要记录 ops 上游错误事件（对齐 CC/Messages 与
						// antigravity 先例），否则透传命中的 failed 在监控中不可见。
						s.recordOpenAIStreamUpstreamError(c, account, false, upstreamRequestID, "http_error", dataBytes, failedMessage)
						MarkResponseCommitted(c)
						c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
						c.JSON(status, gin.H{
							"error": gin.H{
								"type":    errType,
								"message": errMsg,
							},
						})
						streamEarlyErr = fmt.Errorf("upstream response failed: passthrough rule matched message=%s", errMsg)
						return
					}
					if terminalFailureStatus != "failed" || openAIStreamFailedEventShouldFailover(dataBytes, failedMessage) {
						sawFailedEvent = true
						streamEarlyErr = s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, upstreamRequestID, dataBytes, failedMessage, resp)
						return
					}
				}
				if eventType != "response.failed" {
					dataBytes = []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":` + strconv.Quote(failedMessage) + `}}}`)
					data = string(dataBytes)
					line = "data: " + data
					eventType = "response.failed"
				}
				forceFlushFailedEvent = true
				sawFailedEvent = true
			}
			if normalizedData, normalized := normalizeCompletedImageGenerationStatus(dataBytes); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				line = "data: " + data
			}
			imageCounter.AddSSEData(dataBytes)
			if retainImageResults {
				if retainErr := collectOpenAIResponsesImageResultsFromEventPayloadRetained(dataBytes, &imageResults, imageResultSeen, imageRetentionBudget, webChatMaxUploadBytes); retainErr != nil {
					setOutputRetentionError(retainErr, dataBytes)
					return
				}
			}
			// Correct Codex tool calls if needed (apply_patch -> edit, etc.)
			if correctedData, corrected := s.toolCorrector.CorrectToolCallsInSSEBytes(dataBytes); corrected {
				dataBytes = correctedData
				data = string(correctedData)
				line = "data: " + data
				eventType = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
			}
			if imageOutput, ok := extractImageGenerationOutputFromSSEData(dataBytes, streamSeenImages); ok {
				if retainErr := streamOutputAccumulator.RetainExternalOutput(len(imageOutput), 1); retainErr != nil {
					setOutputRetentionError(retainErr, dataBytes)
					return
				}
				streamImageOutputs = append(streamImageOutputs, imageOutput)
			}
			if responsesStreamEventMayContributeToOutput(eventType) {
				var streamEvent apicompat.ResponsesStreamEvent
				if err := json.Unmarshal(dataBytes, &streamEvent); err == nil {
					if retainErr := streamOutputAccumulator.ProcessEvent(&streamEvent); retainErr != nil {
						if openAIStreamClientOutputStarted(c, clientOutputStarted) {
							// The client already receives the authoritative delta stream. Stop
							// retaining a duplicate terminal reconstruction once its bounded
							// budget is exhausted; continuing to retain would make a long-lived
							// stream unbounded, while failing the provider would discard otherwise
							// valid output solely because response.output may later be empty.
							streamOutputAccumulator = nil
						} else {
							setOutputRetentionError(retainErr, dataBytes)
							return
						}
					}
				}
			}
			if normalizedData, normalized := normalizeResponsesStreamingTerminalOutput(dataBytes, streamOutputAccumulator, streamImageOutputs); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				line = "data: " + data
				eventType = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
			}
			restoredData, restoreErr := restoreGrokResponsesClientToolPayload(c, dataBytes)
			if restoreErr != nil {
				streamEarlyErr = fmt.Errorf("restore Grok Responses client tool response: %w", restoreErr)
				return
			}
			restoredData, restoreErr = restoreOpenAIResponsesNamespacePayload(c, restoredData)
			if restoreErr != nil {
				streamEarlyErr = fmt.Errorf("restore OpenAI namespace response: %w", restoreErr)
				return
			}
			if !bytes.Equal(restoredData, dataBytes) {
				dataBytes = restoredData
				data = string(restoredData)
				line = "data: " + data
				eventType = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
			}
			if sanitizedData, sanitized := sanitizeOpenAIResponseFailedEventForClient(
				dataBytes,
				eventType,
				openAIStreamClientOutputStarted(c, clientOutputStarted),
			); sanitized {
				dataBytes = sanitizedData
				data = string(sanitizedData)
				line = "data: " + data
			}
			// Replace model in response if needed.
			// Fast path: most events do not contain model field values.
			if needModelReplace && mappedModel != "" && strings.Contains(line, mappedModel) {
				line = s.replaceModelInSSELine(line, mappedModel, originalModel)
			}
			if declaredEventType != "" {
				line = "event: " + eventType + "\n" + line
			}
			startsClientOutput := forceFlushFailedEvent ||
				openAIResponsesEventIsSemanticPayload(dataBytes, eventType) ||
				(sawTerminalEvent && !sawFailedEvent)
			captureWebChatStreamString(ctx, line+"\n")
			eventStartsClientOutput = eventStartsClientOutput || startsClientOutput

			// 写入客户端（客户端断开后继续 drain 上游）
			if !clientDisconnected {
				shouldFlush := queueDrained && (clientOutputStarted || startsClientOutput)
				if firstTokenMs == nil && startsClientOutput {
					// 保证首个 token 事件尽快出站，避免影响 TTFT。
					shouldFlush = true
				}
				eventShouldFlush = eventShouldFlush || shouldFlush
				if _, err := writePendingString(line); err != nil {
					handlePendingWriteError(err)
				} else if _, err := writePendingString("\n"); err != nil {
					handlePendingWriteError(err)
				} else {
					eventInProgress = true
				}
			}

			s.parseSSEUsageBytes(dataBytes, usage)
			return
		}

		// A blank line dispatches an event from the attempt-local stage. Staging is
		// unconditional; the configured timeout only controls its timer.
		if line == "" {
			if !clientDisconnected {
				if _, err := writePendingString("\n"); err != nil {
					handlePendingWriteError(err)
				}
			}
			if streamEarlyErr == nil {
				completeGuardedEvent(queueDrained)
			}
			return
		}
		// A keepalive or queue-drain flush must never split an open SSE event.
		shouldFlush := false
		if !clientDisconnected {
			if _, err := writePendingString(line); err != nil {
				handlePendingWriteError(err)
			} else if _, err := writePendingString("\n"); err != nil {
				handlePendingWriteError(err)
			} else {
				eventInProgress = line != ""
				if shouldFlush {
					if err := flushBuffered(); err != nil {
						clientDisconnected = true
						logger.LegacyPrintf("service.openai_gateway", "Client disconnected during streaming flush, continuing to drain upstream for billing")
					} else {
						clientOutputStarted = true
						lastDownstreamWriteAt = time.Now()
					}
				}
			}
		}
	}

	// 无超时/无 keepalive 的常见路径走同步扫描，减少 goroutine 与 channel 开销。
	if streamInterval <= 0 && keepaliveInterval <= 0 && firstOutputTimeout <= 0 {
		defer putSSEScannerBuf64K(scanBuf)
		for documentScanner.Scan() {
			processSSELine(documentScanner.Text(), true)
			if streamEarlyErr != nil {
				drainCaptureResponseRemainderBounded(ctx, providerReader, captureOverflowDrainTimeout)
				return resultWithUsage(), streamEarlyErr
			}
		}
		if result, err, done := handleScanErr(documentScanner.Err()); done {
			return result, err
		}
		return finalizeStream()
	}

	type scanEvent struct {
		line      string
		err       error
		processed chan struct{}
	}
	// 独立 goroutine 读取上游，避免读取阻塞影响 keepalive/超时处理
	// Keep one queued token plus the token being processed. Provider tokens may
	// be large, so deeper queues would multiply the configured line ceiling.
	events := make(chan scanEvent, openAIFirstOutputEventQueueSize(guardFirstOutput))
	done := make(chan struct{})
	scanDone := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		if guardFirstOutput {
			ev.processed = make(chan struct{})
		}
		select {
		case events <- ev:
		case <-done:
			return false
		}
		if ev.processed == nil {
			return true
		}
		select {
		case <-ev.processed:
			return true
		case <-done:
			return false
		}
	}
	markEventProcessed := func(ev scanEvent) {
		if ev.processed != nil {
			close(ev.processed)
		}
	}
	go func(scanBuf *sseScannerBuf64K) {
		defer close(scanDone)
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for documentScanner.Scan() {
			if !sendEvent(scanEvent{line: documentScanner.Text()}) {
				return
			}
		}
		if err := documentScanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
	parserExitedEarly := true
	defer func() {
		if parserExitedEarly {
			drainCaptureScannerOnParserFailure(ctx, resp, events, scanDone, &readActivity.lastRead, streamInterval, markEventProcessed, func() {
				close(done)
			})
			return
		}
		close(done)
		closeCaptureResponseAndJoinScanner(resp, scanDone)
	}()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				parserExitedEarly = false
				if eventInProgress {
					// EOF dispatches the final SSE event even without a trailing blank
					// line. Do not synthesize extra bytes on the downstream wire.
					completeGuardedEvent(true)
				}
				return finalizeStream()
			}
			if result, err, done := handleScanErr(ev.err); done {
				markEventProcessed(ev)
				return result, err
			}
			processSSELine(ev.line, len(events) == 0)
			markEventProcessed(ev)
			if streamEarlyErr != nil {
				return resultWithUsage(), streamEarlyErr
			}

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return resultWithUsage(), fmt.Errorf("stream usage incomplete after timeout")
			}
			logger.LegacyPrintf("service.openai_gateway", "Stream data interval timeout: account=%d model=%s interval=%s", account.ID, originalModel, streamInterval)
			// 处理流超时，可能标记账户为临时不可调度或错误状态
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, originalModel)
			}
			// Grok: short cool + account failover when no client-visible bytes
			// were committed yet (pre-commit). After output started we keep the
			// legacy stream_timeout path so partial SSE is not dual-written.
			if account != nil && account.Platform == PlatformGrok {
				s.tempUnscheduleGrok(ctx, account, grokStreamIdleCooldown, "grok stream idle timeout")
				if !openAIStreamClientOutputStarted(c, clientOutputStarted) && !eventShouldFlush {
					markCaptureResponseTruncated(resp.Body)
					closeCaptureResponseUnderlying(resp)
					return resultWithUsage(), grokStreamIdleFailoverError(account, streamInterval)
				}
			}
			sendErrorEvent("stream_timeout")
			return resultWithUsage(), fmt.Errorf("stream data interval timeout")

		case <-firstOutputCh:
			if firstTokenMs != nil {
				stopFirstOutputTimer()
				continue
			}
			markCaptureResponseTruncated(resp.Body)
			closeCaptureResponseUnderlying(resp)
			for ev := range events {
				markEventProcessed(ev)
			}
			return nil, s.newOpenAIFirstOutputTimeoutError(
				ctx, c, account, startTime, originalModel, reasoningEffort,
				firstOutputTimeout, "semantic_output", resp,
			)

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if eventInProgress {
				continue
			}
			if time.Since(lastDownstreamWriteAt) < keepaliveInterval {
				continue
			}
			if _, err := writePendingString(":\n\n"); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.openai_gateway", "Client disconnected during streaming, continuing to drain upstream for billing")
				continue
			}
			if firstTokenMs == nil {
				continue
			}
			if err := flushBuffered(); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.openai_gateway", "Client disconnected during keepalive flush, continuing to drain upstream for billing")
			} else {
				lastDownstreamWriteAt = time.Now()
			}
		}
	}

}

// extractOpenAISSEDataLine 低开销提取 SSE `data:` 行内容。
// 兼容 `data: xxx` 与 `data:xxx` 两种格式。
func extractOpenAISSEDataLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	start := len("data:")
	for start < len(line) {
		if line[start] != ' ' && line[start] != '	' {
			break
		}
		start++
	}
	return line[start:], true
}

func extractOpenAISSEEventLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "event:") {
		return "", false
	}
	start := len("event:")
	for start < len(line) {
		if line[start] != ' ' && line[start] != '	' {
			break
		}
		start++
	}
	return strings.TrimSpace(line[start:]), true
}

type openAICompatSSEFrame struct {
	EventType string
	Data      string
}

type openAICompatSSEFrameParser struct {
	eventType     string
	data          strings.Builder
	maxEventBytes int64
}

func (p *openAICompatSSEFrameParser) AddLine(line string) (openAICompatSSEFrame, bool, error) {
	if line == "" {
		return p.dispatch()
	}
	if strings.HasPrefix(line, ":") {
		return openAICompatSSEFrame{}, false, nil
	}
	if eventType, ok := extractOpenAISSEEventLine(line); ok {
		p.eventType = eventType
		return openAICompatSSEFrame{}, false, nil
	}
	if data, ok := extractOpenAISSEDataLine(line); ok {
		additional := int64(len(data))
		if p.data.Len() > 0 {
			additional++
		}
		limit := p.maxEventBytes
		if limit <= 0 {
			limit = defaultUpstreamResponseReadMaxBytes
		}
		if int64(p.data.Len()) > limit-additional {
			return openAICompatSSEFrame{}, false, fmt.Errorf("%w: SSE event limit=%d", ErrUpstreamResponseBodyTooLarge, limit)
		}
		if p.data.Len() > 0 {
			_ = p.data.WriteByte('\n')
		}
		_, _ = p.data.WriteString(data)
	}
	return openAICompatSSEFrame{}, false, nil
}

func (p *openAICompatSSEFrameParser) Finish() (openAICompatSSEFrame, bool, error) {
	return p.dispatch()
}

func (p *openAICompatSSEFrameParser) HasPendingProviderData() bool {
	return p != nil && (strings.TrimSpace(p.eventType) != "" || p.data.Len() > 0)
}

func (p *openAICompatSSEFrameParser) dispatch() (openAICompatSSEFrame, bool, error) {
	frame := openAICompatSSEFrame{
		EventType: p.eventType,
		Data:      p.data.String(),
	}
	p.eventType = ""
	p.data = strings.Builder{}
	return frame, frame.Data != "", nil
}

func openAICompatPayloadWithEventType(payload, eventType string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return payload
	}
	if gjson.Get(payload, "type").Exists() {
		return payload
	}
	patched, err := sjson.Set(payload, "type", eventType)
	if err != nil {
		return payload
	}
	return patched
}

func (s *OpenAIGatewayService) replaceModelInSSELine(line, fromModel, toModel string) string {
	data, ok := extractOpenAISSEDataLine(line)
	if !ok {
		return line
	}
	if data == "" || data == "[DONE]" {
		return line
	}

	// 使用 gjson 精确检查 model 字段，避免全量 JSON 反序列化
	if m := gjson.Get(data, "model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	// 检查嵌套的 response.model 字段
	if m := gjson.Get(data, "response.model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "response.model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	return line
}

// correctToolCallsInResponseBody 修正响应体中的工具调用
func (s *OpenAIGatewayService) correctToolCallsInResponseBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	updated := body
	if s != nil && s.toolCorrector != nil {
		if corrected, changed := s.toolCorrector.CorrectToolCallsInSSEBytes(updated); changed {
			updated = corrected
		}
	}
	if normalized, changed := normalizeOpenAIResponsesFunctionCallArguments(updated); changed {
		updated = normalized
	}
	return updated
}

func normalizeOpenAIResponsesFunctionCallArguments(data []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(data)) == 0 || !bytes.Contains(data, []byte(`"arguments"`)) {
		return data, false
	}
	if !json.Valid(data) {
		return data, false
	}
	root := gjson.ParseBytes(data)
	type replacement struct {
		start int
		end   int
		value []byte
	}
	var replacements []replacement
	collectDedupedArgument := func(arg gjson.Result) {
		if !arg.Exists() || arg.Type != gjson.String {
			return
		}
		deduped, ok := dedupeRepeatedJSONArgumentString(arg.Str)
		if !ok {
			return
		}
		encoded, err := json.Marshal(deduped)
		if err != nil {
			return
		}
		end := arg.Index + len(arg.Raw)
		if arg.Index < 0 || arg.Raw == "" || end > len(data) {
			return
		}
		replacements = append(replacements, replacement{start: arg.Index, end: end, value: encoded})
	}

	eventType := strings.TrimSpace(root.Get("type").String())
	if eventType == "response.function_call_arguments.done" {
		collectDedupedArgument(root.Get("arguments"))
	}
	if itemType := strings.TrimSpace(root.Get("item.type").String()); isResponsesFunctionCallItemType(itemType) {
		collectDedupedArgument(root.Get("item.arguments"))
	}
	for _, output := range []gjson.Result{root.Get("response.output"), root.Get("output")} {
		if !output.Exists() || !output.IsArray() {
			continue
		}
		output.ForEach(func(_, item gjson.Result) bool {
			if isResponsesFunctionCallItemType(strings.TrimSpace(item.Get("type").String())) {
				collectDedupedArgument(item.Get("arguments"))
			}
			return true
		})
	}
	if len(replacements) == 0 {
		return data, false
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	updated := make([]byte, 0, len(data))
	cursor := 0
	for _, span := range replacements {
		if span.start < cursor || span.end < span.start || span.end > len(data) {
			return data, false
		}
		updated = append(updated, data[cursor:span.start]...)
		updated = append(updated, span.value...)
		cursor = span.end
	}
	updated = append(updated, data[cursor:]...)
	return updated, true
}

func isResponsesFunctionCallItemType(itemType string) bool {
	return itemType == "function_call" || itemType == "custom_tool_call"
}

func dedupeRepeatedJSONArgumentString(arguments string) (string, bool) {
	if len(arguments) == 0 || len(arguments)%2 != 0 {
		return "", false
	}
	halfLen := len(arguments) / 2
	first := arguments[:halfLen]
	if first != arguments[halfLen:] {
		return "", false
	}
	trimmed := strings.TrimSpace(first)
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return "", false
	}
	if !json.Valid([]byte(first)) {
		return "", false
	}
	return first, true
}

func (s *OpenAIGatewayService) parseSSEUsage(data string, usage *OpenAIUsage) {
	s.parseSSEUsageBytes([]byte(data), usage)
}

func (s *OpenAIGatewayService) parseSSEUsageBytes(data []byte, usage *OpenAIUsage) {
	if usage == nil || len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	// 选择性解析：仅在数据中包含终止事件标识时才进入字段提取。
	if len(data) < 72 {
		return
	}
	eventType := gjson.GetBytes(data, "type").String()
	if eventType != "response.completed" && eventType != "response.done" && eventType != "response.failed" &&
		eventType != "response.incomplete" && eventType != "response.cancelled" && eventType != "response.canceled" {
		return
	}

	if parsedUsage, ok := extractOpenAIUsageFromJSONBytes(data); ok {
		*usage = parsedUsage
	}
}

func extractOpenAIUsageFromJSONBytes(body []byte) (OpenAIUsage, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return OpenAIUsage{}, false
	}
	if usage, ok := openAIUsageFromGJSON(gjson.GetBytes(body, "usage")); ok {
		imageGen := gjson.GetBytes(body, "tool_usage.image_gen")
		if !imageGen.Exists() {
			imageGen = gjson.GetBytes(body, "response.tool_usage.image_gen")
		}
		mergeHostedImageGenToolUsage(imageGen, &usage)
		return usage, true
	}
	if usage, ok := openAIUsageFromGJSON(gjson.GetBytes(body, "response.usage")); ok {
		imageGen := gjson.GetBytes(body, "response.tool_usage.image_gen")
		if !imageGen.Exists() {
			imageGen = gjson.GetBytes(body, "tool_usage.image_gen")
		}
		mergeHostedImageGenToolUsage(imageGen, &usage)
		return usage, true
	}
	return OpenAIUsage{}, false
}

func mergeHostedImageGenToolUsage(imageGen gjson.Result, usage *OpenAIUsage) {
	if !imageGen.Exists() || !imageGen.IsObject() {
		return
	}
	if usage.ImageOutputTokens == 0 {
		if v := imageGen.Get("output_tokens_details.image_tokens").Int(); v > 0 {
			usage.ImageOutputTokens = int(v)
		}
	}
	if usage.ImageInputTokens == 0 {
		if v := imageGen.Get("input_tokens_details.image_tokens").Int(); v > 0 {
			usage.ImageInputTokens = int(v)
		}
	}
}

func extractOpenAIResponseIDFromJSONBytes(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	root := gjson.ParseBytes(body)
	if response := root.Get("response"); response.IsObject() {
		// In SSE envelopes the nested response is authoritative. A root id is
		// merely an extension and must not spoof sticky-account correlation.
		return strings.TrimSpace(response.Get("id").String())
	}
	return strings.TrimSpace(root.Get("id").String())
}

func (s *OpenAIGatewayService) bindHTTPResponseAccount(ctx context.Context, c *gin.Context, account *Account, responseID string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	ttl := s.openAIWSResponseStickyTTL()
	logOpenAIWSBindResponseAccountWarn(groupID, account.ID, responseID, store.BindResponseAccount(ctx, groupID, responseID, account.ID, ttl))
}

func openAIUsageFromGJSON(value gjson.Result) (OpenAIUsage, bool) {
	if !value.Exists() || !value.IsObject() {
		return OpenAIUsage{}, false
	}
	inputTokens := value.Get("input_tokens").Int()
	if inputTokens == 0 {
		inputTokens = value.Get("prompt_tokens").Int()
	}
	outputTokens := value.Get("output_tokens").Int()
	if outputTokens == 0 {
		outputTokens = value.Get("completion_tokens").Int()
	}
	cacheReadTokens := openAICacheReadTokensFromUsage(value)
	cacheCreationTokens := openAICacheCreationTokensFromUsage(value)
	imageOutputTokens := value.Get("output_tokens_details.image_tokens").Int()
	if imageOutputTokens == 0 {
		imageOutputTokens = value.Get("completion_tokens_details.image_tokens").Int()
	}
	// 图片输入 token（如 gpt-image-2 的 /v1/images/edits 带图请求），
	// 上游在 input_tokens_details.image_tokens 单独回传，用于图/文输入分价计费。
	// 普通文本请求该字段为 0，走原路径行为不变。
	imageInputTokens := firstPositiveGJSONInt(
		value.Get("input_tokens_details.image_tokens"),
		value.Get("prompt_tokens_details.image_tokens"),
	)
	return OpenAIUsage{
		InputTokens:              int(inputTokens),
		ImageInputTokens:         imageInputTokens,
		OutputTokens:             int(outputTokens),
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ImageOutputTokens:        int(imageOutputTokens),
		KiroCredits:              kiroCreditsFromUsageGJSON(value),
	}, true
}

func openAICacheReadTokensFromUsage(value gjson.Result) int {
	for _, nested := range []gjson.Result{
		value.Get("input_tokens_details.cached_tokens"),
		value.Get("prompt_tokens_details.cached_tokens"),
	} {
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	return firstPositiveGJSONInt(
		value.Get("cache_read_input_tokens"),
		value.Get("cache_read_tokens"),
		value.Get("cached_tokens"),
	)
}

func openAICacheCreationTokensFromUsage(value gjson.Result) int {
	for _, nested := range []gjson.Result{
		value.Get("input_tokens_details.cache_write_tokens"),
		value.Get("prompt_tokens_details.cache_write_tokens"),
		value.Get("input_tokens_details.cache_creation_tokens"),
		value.Get("prompt_tokens_details.cache_creation_tokens"),
	} {
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	return firstPositiveGJSONInt(
		value.Get("cache_write_tokens"),
		value.Get("cache_creation_input_tokens"),
		value.Get("cache_write_input_tokens"),
		value.Get("cache_creation_tokens"),
	)
}

func mergeOpenAIUsageKiroCreditsFromJSON(usage *OpenAIUsage, body []byte) {
	if usage == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	if credits := kiroCreditsFromUsageGJSON(gjson.GetBytes(body, "usage")); credits > 0 {
		usage.KiroCredits = credits
		return
	}
	if credits := kiroCreditsFromUsageGJSON(gjson.GetBytes(body, "response.usage")); credits > 0 {
		usage.KiroCredits = credits
	}
}

func (s *OpenAIGatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	stop := compactStopFunc(stopBeforeWrite...)
	body, err := ReadUpstreamResponseBodyWithContext(ctx, resp.Body, s.cfg, c, nil)
	if err != nil {
		stop()
		return nil, errors.Join(newOpenAIIncompleteChatStreamFailover(resp, "failed to read upstream Responses body"), err)
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	if bodyHasSSEFraming(body) {
		observeOpenAISSEBody(observer, string(body))
	} else {
		observer.ObserveOpenAI(body, strings.TrimSpace(gjson.GetBytes(body, "type").String()))
	}

	// Detect SSE responses for ALL account types via Content-Type header.
	// Some OpenAI-compatible upstreams (including other sub2api instances)
	// may return SSE even when stream=false was requested.
	if isEventStreamResponse(resp.Header) {
		return s.handleSSEToJSONWithWebChatCapture(ctx, resp, c, body, originalModel, mappedModel, stop)
	}
	// bodyLooksLikeSSE is a line-level heuristic: real SSE framing requires
	// "data:"/"event:" field names at the very start of a physical line. A
	// plain bytes.Contains scan would also match ordinary JSON responses
	// whose string content merely echoes the literal text "data:" or
	// "event:" (e.g. compact tool output), causing those JSON bodies to be
	// misrouted into handleSSEToJSON and lose their usage accounting.
	bodyLooksLikeSSE := bodyHasSSEFraming(body)

	// For OAuth accounts, also fall back to a body-content heuristic because
	// the upstream may omit the Content-Type header while still sending SSE.
	// This heuristic is NOT applied to API-key accounts to avoid false
	// positives on JSON responses that coincidentally contain "data:" or
	// "event:" in their text content.
	if account.Type == AccountTypeOAuth && bodyLooksLikeSSE {
		return s.handleSSEToJSONWithWebChatCapture(ctx, resp, c, body, originalModel, mappedModel, stop)
	}
	if account != nil && account.IsGrok() && isOpenAIResponsesCompactPath(c) {
		body, err = convertGrokResponseToOpenAICompact(body)
		if err != nil {
			return nil, fmt.Errorf("convert Grok compact response: %w", err)
		}
	}

	usageValue, usageOK := extractOpenAIUsageFromJSONBytes(body)
	if !usageOK || !validOpenAIResponsesJSON(body) {
		if bodyLooksLikeSSE {
			return s.handleSSEToJSONWithWebChatCapture(ctx, resp, c, body, originalModel, mappedModel, stop)
		}
		stop()
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "invalid upstream Responses JSON response")
	}
	if status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String())); openAIResponsesStatusIsExplicitlyIncomplete(status) {
		stop()
		return nil, s.newOpenAIStreamFailoverErrorFromResponse(c, account, false, captureProviderRequestID(resp.Header), body, "upstream response ended with status "+status, resp)
	}
	usage := &usageValue

	// Replace model in response if needed
	if originalModel != mappedModel {
		body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = restoreGrokResponsesClientToolPayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore Grok Responses client tool response: %w", err)
	}
	body, err = restoreOpenAIResponsesNamespacePayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI namespace response: %w", err)
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := "application/json"
	if s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}

	if !writeOpenAICompactSSEBridge(c, resp.StatusCode, body) {
		stop()
		c.Data(resp.StatusCode, contentType, body)
	}

	var imageResults []openAIResponsesImageResult
	if hasWebChatStreamCapture(ctx) {
		imageResults = collectOpenAIResponsesImageResultsFromJSONResponseBounded(body, webChatMaxUploadBytes)
	}
	return &openaiNonStreamingResult{
		OpenAIUsage:      usage,
		usage:            usage,
		responseID:       extractOpenAIResponseIDFromJSONBytes(body),
		imageCount:       countOpenAIResponseImageOutputsFromJSONBytes(body),
		imageOutputSizes: collectOpenAIResponseImageOutputSizesFromJSONBytes(body),
		imageResults:     imageResults,
	}, nil
}

func (s *OpenAIGatewayService) handleSSEToJSONWithWebChatCapture(ctx context.Context, resp *http.Response, c *gin.Context, body []byte, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	return s.handleSSEToJSONWithContext(ctx, resp, c, body, originalModel, mappedModel, stopBeforeWrite...)
}

func isEventStreamResponse(header http.Header) bool {
	contentType := strings.ToLower(header.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

// bodyHasSSEFraming reports whether body contains genuine SSE framing by
// scanning for physical lines that begin with the "data:" or "event:"
// field names, per the SSE spec. Unlike a raw substring scan, this does not
// match when those strings only appear embedded inside JSON string values
// (e.g. "data: foo" quoted as part of an assistant text field), since such
// occurrences never start a physical line in a valid JSON encoding.
func bodyHasSSEFraming(body []byte) bool {
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte("event:")) {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) handleSSEToJSON(resp *http.Response, c *gin.Context, body []byte, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	return s.handleSSEToJSONWithContext(context.Background(), resp, c, body, originalModel, mappedModel, stopBeforeWrite...)
}

func (s *OpenAIGatewayService) handleSSEToJSONWithContext(ctx context.Context, resp *http.Response, c *gin.Context, body []byte, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	stop := compactStopFunc(stopBeforeWrite...)
	if err := validateBufferedOpenAIResponsesSSEBody(body, int64(resolveUpstreamResponseReadLimit(s.cfg))); err != nil {
		stop()
		return nil, newOpenAIIncompleteChatStreamFailover(resp, err.Error())
	}
	bodyText := string(body)
	finalResponse, ok := extractCodexFinalResponse(bodyText)

	usage := &OpenAIUsage{}
	if ok {
		if parsedUsage, parsed := extractOpenAIUsageFromJSONBytes(finalResponse); parsed {
			*usage = parsedUsage
		}
		// When the terminal event has an empty output array, reconstruct
		// output from accumulated delta events so the client gets full content.
		// gjson Array() returns empty slice for null, missing, or empty arrays.
		if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
			outputJSON, reconstructed, reconstructErr := reconstructResponseOutputFromSSE(bodyText)
			if reconstructErr != nil {
				stop()
				return nil, fmt.Errorf("reconstruct OpenAI Responses output: %w", reconstructErr)
			}
			if reconstructed {
				if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
					finalResponse = patched
				}
			}
		}
		finalResponse = supplementCompactionItemFromSSE(c, finalResponse, bodyText)
		body = finalResponse
		if originalModel != mappedModel {
			body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
		}
		// Correct tool calls in final response
		body = s.correctToolCallsInResponseBody(body)
		restoredBody, restoreErr := restoreGrokResponsesClientToolPayload(c, body)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore Grok Responses client tool response: %w", restoreErr)
		}
		restoredBody, restoreErr = restoreOpenAIResponsesNamespacePayload(c, restoredBody)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI namespace response: %w", restoreErr)
		}
		body = restoredBody
	} else {
		terminalType, terminalPayload, terminalOK := extractOpenAISSETerminalEvent(bodyText)
		if terminalOK && terminalType == "response.failed" {
			msg := extractOpenAISSEErrorMessage(terminalPayload)
			if msg == "" {
				msg = "Upstream compact response failed"
			}
			stop()
			return nil, s.writeOpenAINonStreamingProtocolError(resp, c, msg)
		}
		stop()
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Responses stream ended without a completed response")
	}

	stop()
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := "application/json; charset=utf-8"
	if !ok {
		contentType = resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/event-stream"
		}
	}
	if !writeOpenAICompactSSEBridge(c, resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}

	var imageResults []openAIResponsesImageResult
	if hasWebChatStreamCapture(ctx) {
		imageResults = collectOpenAIResponsesImageResultsFromSSEBodyBounded(bodyText, webChatMaxUploadBytes)
	}
	return &openaiNonStreamingResult{
		OpenAIUsage:      usage,
		usage:            usage,
		responseID:       extractOpenAIResponseIDFromJSONBytes(body),
		imageCount:       countOpenAIImageOutputsFromSSEBody(bodyText),
		imageOutputSizes: collectOpenAIImageOutputSizesFromSSEBody(bodyText),
		imageResults:     imageResults,
	}, nil
}

func validateBufferedOpenAIResponsesSSEBody(body []byte, maxEventBytes int64) error {
	if len(body) == 0 {
		return errors.New("upstream Responses stream was empty")
	}
	parser := openAICompatSSEFrameParser{maxEventBytes: maxEventBytes}
	var state openAIResponsesSSEAttemptState
	terminalSeen := false
	processFrame := func(frame openAICompatSSEFrame) error {
		payload := strings.TrimSpace(frame.Data)
		if payload == "" {
			return nil
		}
		if payload == "[DONE]" {
			if !terminalSeen {
				return errors.New("OpenAI Responses [DONE] arrived before a valid terminal event")
			}
			return nil
		}
		if terminalSeen {
			return errors.New("OpenAI Responses data arrived after a terminal event")
		}
		payloadBytes := []byte(payload)
		eventType, err := validateOpenAIResponsesSSEPayload(payloadBytes, frame.EventType)
		if err != nil {
			return err
		}
		if err := state.observe(payloadBytes, eventType); err != nil {
			return err
		}
		if isOpenAICompatResponsesTerminalEvent(eventType) {
			terminalSeen = true
		}
		return nil
	}
	for start := 0; start < len(body); {
		end := bytes.IndexByte(body[start:], '\n')
		var line []byte
		if end < 0 {
			line = body[start:]
			start = len(body)
		} else {
			end += start
			line = body[start:end]
			start = end + 1
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if _, isDataLine := extractOpenAISSEDataLine(string(line)); isDataLine && parser.data.Len() > 0 {
			pending := strings.TrimSpace(parser.data.String())
			if pending == "[DONE]" || gjson.Valid(pending) {
				frame, ready, err := parser.dispatch()
				if err != nil {
					return err
				}
				if ready {
					if err := processFrame(frame); err != nil {
						return err
					}
				}
			}
		}
		frame, ready, err := parser.AddLine(string(line))
		if err != nil {
			return err
		}
		if ready {
			if err := processFrame(frame); err != nil {
				return err
			}
		}
	}
	hadPending := parser.HasPendingProviderData()
	frame, ready, err := parser.Finish()
	if err != nil {
		return err
	}
	if ready {
		if err := processFrame(frame); err != nil {
			return err
		}
	} else if hadPending {
		return errors.New("OpenAI Responses stream ended with an incomplete SSE event")
	}
	if !terminalSeen {
		return errors.New("upstream Responses stream ended without a valid terminal event")
	}
	return nil
}

func extractOpenAISSETerminalEvent(body string) (string, []byte, bool) {
	var terminalType string
	var terminalPayload []byte
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		if terminalPayload != nil {
			return
		}
		eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
		switch eventType {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			terminalType = eventType
			terminalPayload = append([]byte(nil), data...)
		}
	})
	if terminalPayload != nil {
		return terminalType, terminalPayload, true
	}
	return "", nil, false
}

func extractOpenAISSEErrorMessage(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	for _, path := range []string{"response.error.message", "error.message", "message"} {
		if msg := strings.TrimSpace(gjson.GetBytes(payload, path).String()); msg != "" {
			return sanitizeUpstreamErrorMessage(msg)
		}
	}
	return sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(payload)))
}

func sanitizeOpenAIResponseFailedEventForClient(payload []byte, eventType string, clientOutputStarted bool) ([]byte, bool) {
	eventType = strings.TrimSpace(eventType)
	isFailedEvent := eventType == "response.failed"
	if (!isFailedEvent && eventType != "error") || len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload, false
	}
	updated := payload
	// 容量降载码对 Codex CLI 是致命错误；事件既然要写给客户端（failover 已不可用），
	// 就改写为客户端可重试的错误码。error 帧与 response.failed 都要改：上游降载
	// 总是先推 error 帧再收 failed，两帧携带同一个错误。
	if rewritten, changed := sanitizeOpenAICapacityShedErrorCodeForClient(updated); changed {
		updated = rewritten
	}
	if !isFailedEvent {
		return updated, !bytes.Equal(updated, payload)
	}
	if clientOutputStarted && isOpenAIContextWindowError(extractOpenAISSEErrorMessage(payload), payload) {
		errorPath := ""
		switch {
		case gjson.GetBytes(updated, "response.error").Exists():
			errorPath = "response.error"
		case gjson.GetBytes(updated, "error").Exists():
			errorPath = "error"
		}
		if errorPath != "" {
			next, err := sjson.SetBytes(updated, errorPath+".type", "invalid_request_error")
			if err != nil {
				return payload, false
			}
			updated = next
			next, err = sjson.SetBytes(updated, errorPath+".code", "context_length_exceeded")
			if err != nil {
				return payload, false
			}
			updated = next
		}
	}
	if !gjson.GetBytes(updated, "response").Exists() {
		return updated, !bytes.Equal(updated, payload)
	}
	for _, path := range []string{
		"response.instructions",
		"response.output",
		"response.usage",
		"response.metadata",
		"response.reasoning",
		"response.tools",
		"response.tool_choice",
		"response.parallel_tool_calls",
		"response.text",
		"response.truncation",
		"response.max_output_tokens",
		"response.incomplete_details",
	} {
		next, err := sjson.DeleteBytes(updated, path)
		if err != nil {
			return payload, false
		}
		updated = next
	}
	return updated, !bytes.Equal(updated, payload)
}

func (s *OpenAIGatewayService) writeOpenAINonStreamingProtocolError(resp *http.Response, c *gin.Context, message string) error {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "Upstream returned an invalid non-streaming response"
	}
	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	// body-signal compact 心跳可能已把响应头提交为 200，此时只能以
	// response.failed 终止事件回传错误，不能再写 JSON+状态码。
	if openAICompactClientWantsStream(c) && StopOpenAICompactSSEKeepaliveCommitted(c) {
		writeOpenAICompactSSEFailureMessage(c, http.StatusBadGateway, "upstream_error", message)
		return fmt.Errorf("non-streaming openai protocol error: %s", message)
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	return fmt.Errorf("non-streaming openai protocol error: %s", message)
}

func extractCodexFinalResponse(body string) ([]byte, bool) {
	var finalResponse []byte
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		if finalResponse != nil {
			return
		}
		if normalized, changed := normalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		eventType := gjson.GetBytes(data, "type").String()
		if eventType == "response.done" || eventType == "response.completed" {
			if response := gjson.GetBytes(data, "response"); validOpenAIResponsesObject(response) && response.Raw != "" {
				finalResponse = []byte(response.Raw)
			}
		}
	})
	if finalResponse != nil {
		return finalResponse, true
	}
	return nil, false
}

func validOpenAIResponsesObject(response gjson.Result) bool {
	if !validOpenAIResponsesKnownObjectFields(response) {
		return false
	}
	output := response.Get("output")
	if output.Raw == "null" {
		return true
	}
	return output.IsArray()
}

const (
	maxOpenAIResponsesTrackedStateEntries = 1024
	maxOpenAIResponsesRetainedStringBytes = 1024
)

func validBoundedOpenAIResponsesArray(array gjson.Result, validate func(gjson.Result) bool) bool {
	if !array.IsArray() {
		return false
	}
	count := 0
	valid := true
	array.ForEach(func(_, value gjson.Result) bool {
		count++
		if count > maxOpenAIResponsesTrackedStateEntries || !validate(value) {
			valid = false
			return false
		}
		return true
	})
	return valid
}

func validOpenAIResponsesKnownObjectFields(response gjson.Result) bool {
	return validOpenAIResponsesKnownObjectFieldsWithOutput(response, true)
}

func validOpenAIResponsesKnownObjectFieldsWithOutput(response gjson.Result, strictOutput bool) bool {
	if !response.IsObject() {
		return false
	}
	for _, field := range []string{"id", "status"} {
		if value := response.Get(field); value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIResponsesRetainedStringBytes) {
			return false
		}
	}
	for _, field := range []string{"object", "model"} {
		if value := response.Get(field); value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIResponsesRetainedStringBytes) {
			return false
		}
	}
	if value := response.Get("created_at"); value.Exists() && !nonNegativeFiniteGJSONNumber(value) {
		return false
	}
	if value := response.Get("parallel_tool_calls"); value.Exists() && value.Type != gjson.True && value.Type != gjson.False {
		return false
	}
	for _, field := range []string{"temperature", "top_p"} {
		if value := response.Get(field); value.Exists() && value.Type != gjson.Null && !nonNegativeFiniteGJSONNumber(value) {
			return false
		}
	}
	if value := response.Get("metadata"); value.Exists() && value.Type != gjson.Null && !value.IsObject() {
		return false
	}
	if value := response.Get("tools"); value.Exists() && value.Type != gjson.Null && !value.IsArray() {
		return false
	}
	if value := response.Get("tool_choice"); value.Exists() && value.Type != gjson.Null && value.Type != gjson.String && !value.IsObject() {
		return false
	}
	if value := response.Get("instructions"); value.Exists() && value.Type != gjson.Null && value.Type != gjson.String && !value.IsArray() {
		return false
	}
	if usage := response.Get("usage"); usage.Exists() && usage.Type != gjson.Null && !validOpenAIResponsesUsageShape(usage) {
		return false
	}
	if output := response.Get("output"); output.Exists() && output.Raw != "null" {
		validateItem := func(gjson.Result) bool { return true }
		if strictOutput {
			validateItem = validOpenAIResponsesOutputItem
		}
		if !validBoundedOpenAIResponsesArray(output, validateItem) {
			return false
		}
	}
	if toolUsage := response.Get("tool_usage"); toolUsage.Exists() && !validOpenAIHostedToolUsageShape(toolUsage) {
		return false
	}
	if responseError := response.Get("error"); responseError.Exists() && responseError.Type != gjson.Null && !validOpenAIResponsesErrorObject(responseError) {
		return false
	}
	if incomplete := response.Get("incomplete_details"); incomplete.Exists() && incomplete.Type != gjson.Null && !validOpenAIResponsesIncompleteDetails(incomplete) {
		return false
	}
	if strings.TrimSpace(response.Get("status").String()) == "completed" {
		if responseError := response.Get("error"); responseError.Exists() && responseError.Type != gjson.Null {
			return false
		}
		if incomplete := response.Get("incomplete_details"); incomplete.Exists() && incomplete.Type != gjson.Null {
			return false
		}
	}
	return true
}

func validOpenAIResponsesErrorObject(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}
	for _, field := range []string{"type", "code", "message", "param"} {
		item := value.Get(field)
		if !item.Exists() || item.Type == gjson.Null {
			continue
		}
		if item.Type != gjson.String || len(item.String()) > maxOpenAIResponsesRetainedStringBytes {
			return false
		}
	}
	return true
}

func validOpenAIResponsesIncompleteDetails(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}
	reason := value.Get("reason")
	return !reason.Exists() || reason.Type == gjson.Null || reason.Type == gjson.String && len(reason.String()) <= maxOpenAIResponsesRetainedStringBytes
}

func validOpenAIHostedToolUsageShape(toolUsage gjson.Result) bool {
	if !toolUsage.IsObject() {
		return false
	}
	imageGen := toolUsage.Get("image_gen")
	if !imageGen.Exists() {
		return true
	}
	if !imageGen.IsObject() {
		return false
	}
	for _, field := range []string{"input_tokens", "output_tokens", "total_tokens", "images"} {
		if value := imageGen.Get(field); value.Exists() && !nonNegativeIntegerGJSON(value) {
			return false
		}
	}
	if images := imageGen.Get("images"); images.Exists() && images.Uint() > uint64(^uint32(0)) {
		return false
	}
	for _, path := range []string{"input_tokens_details.image_tokens", "output_tokens_details.image_tokens"} {
		if value := imageGen.Get(path); value.Exists() && !nonNegativeIntegerGJSON(value) {
			return false
		}
	}
	toolInput, toolOutput := imageGen.Get("input_tokens"), imageGen.Get("output_tokens")
	imageInput, imageOutput := imageGen.Get("input_tokens_details.image_tokens"), imageGen.Get("output_tokens_details.image_tokens")
	if imageInput.Exists() && (!toolInput.Exists() || imageInput.Int() > toolInput.Int()) {
		return false
	}
	if imageOutput.Exists() && (!toolOutput.Exists() || imageOutput.Int() > toolOutput.Int()) {
		return false
	}
	return true
}

func validOpenAIUsageShape(usage gjson.Result) bool {
	if !usage.IsObject() {
		return false
	}
	for _, field := range []string{
		"input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens",
		"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "cache_write_tokens",
		"cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens",
	} {
		if value := usage.Get(field); value.Exists() && !nonNegativeIntegerGJSON(value) {
			return false
		}
	}
	knownDetailCounters := map[string][]string{
		"input_tokens_details":      {"cached_tokens", "cache_write_tokens", "cache_creation_tokens", "image_tokens", "audio_tokens"},
		"output_tokens_details":     {"reasoning_tokens", "image_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"},
		"prompt_tokens_details":     {"cached_tokens", "cache_write_tokens", "cache_creation_tokens", "image_tokens", "audio_tokens"},
		"completion_tokens_details": {"reasoning_tokens", "image_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"},
	}
	for field, counters := range knownDetailCounters {
		details := usage.Get(field)
		if !details.Exists() {
			continue
		}
		if !details.IsObject() {
			return false
		}
		for _, counter := range counters {
			if value := details.Get(counter); value.Exists() && !nonNegativeIntegerGJSON(value) {
				return false
			}
		}
	}
	for _, field := range []string{"_sub2api_kiro_credits", "kiro_credits", "kiroCredits", "credits", "creditsUsed", "creditUsage", "consumedCredits"} {
		if value := usage.Get(field); value.Exists() && !nonNegativeFiniteGJSONNumber(value) {
			return false
		}
	}
	return true
}

func validOpenAIUsageRelations(usage, input, output, cacheRead, cacheWrite, imageInput, imageOutput gjson.Result) bool {
	if input.Exists() {
		cacheTotal, ok := checkedAddNonNegativeGJSON(cacheRead, cacheWrite)
		if !ok || cacheTotal > input.Int() {
			return false
		}
		if imageInput.Exists() && imageInput.Int() > input.Int() {
			return false
		}
	} else if (cacheRead.Exists() && cacheRead.Int() > 0) || (cacheWrite.Exists() && cacheWrite.Int() > 0) || (imageInput.Exists() && imageInput.Int() > 0) {
		return false
	}
	if output.Exists() {
		if imageOutput.Exists() && imageOutput.Int() > output.Int() {
			return false
		}
	} else if imageOutput.Exists() && imageOutput.Int() > 0 {
		return false
	}
	if total := usage.Get("total_tokens"); total.Exists() {
		if !input.Exists() || !output.Exists() {
			return false
		}
		combined, ok := checkedAddNonNegativeGJSON(input, output)
		if !ok || combined != total.Int() {
			return false
		}
	}
	return true
}

func validOpenAIChatUsageShape(usage gjson.Result) bool {
	if !usage.IsObject() {
		return false
	}
	parsed, _ := parseCCUsageFromGJSON(usage)
	familyPresent := func(paths ...string) bool {
		for _, path := range paths {
			if usage.Get(path).Exists() {
				return true
			}
		}
		return false
	}
	if familyPresent("prompt_tokens", "input_tokens") && !parsed.inputTokensSet ||
		familyPresent("completion_tokens", "output_tokens") && !parsed.outputTokensSet ||
		familyPresent("prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens", "cache_read_input_tokens", "cache_read_tokens", "cached_tokens") && !parsed.cacheReadTokensSet ||
		familyPresent("prompt_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_creation_tokens", "input_tokens_details.cache_write_tokens", "input_tokens_details.cache_creation_tokens", "cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens") && !parsed.cacheCreationTokensSet ||
		familyPresent("completion_tokens_details.image_tokens", "output_tokens_details.image_tokens") && !parsed.imageOutputTokensSet ||
		familyPresent("prompt_tokens_details.audio_tokens", "input_tokens_details.audio_tokens") && !parsed.promptAudioTokensSet ||
		familyPresent("completion_tokens_details.audio_tokens", "output_tokens_details.audio_tokens") && !parsed.outputAudioTokensSet ||
		familyPresent("completion_tokens_details.reasoning_tokens", "output_tokens_details.reasoning_tokens") && !parsed.reasoningTokensSet ||
		familyPresent("completion_tokens_details.accepted_prediction_tokens", "output_tokens_details.accepted_prediction_tokens") && !parsed.acceptedPredictionTokensSet ||
		familyPresent("completion_tokens_details.rejected_prediction_tokens", "output_tokens_details.rejected_prediction_tokens") && !parsed.rejectedPredictionTokensSet ||
		familyPresent("_sub2api_kiro_credits", "kiro_credits", "kiroCredits", "credits", "creditsUsed", "creditUsage", "consumedCredits") && !parsed.kiroCreditsSet {
		return false
	}
	if parsed.inputTokensSet {
		cacheTotal := int64(parsed.Usage.CacheReadInputTokens) + int64(parsed.Usage.CacheCreationInputTokens)
		if cacheTotal > int64(parsed.Usage.InputTokens) {
			return false
		}
	} else if parsed.Usage.CacheReadInputTokens > 0 || parsed.Usage.CacheCreationInputTokens > 0 {
		return false
	}
	if parsed.imageOutputTokensSet && (!parsed.outputTokensSet || parsed.Usage.ImageOutputTokens > parsed.Usage.OutputTokens) {
		return false
	}
	if total := usage.Get("total_tokens"); total.Exists() {
		if !nonNegativeIntegerGJSON(total) || !parsed.inputTokensSet || !parsed.outputTokensSet ||
			int64(parsed.Usage.InputTokens)+int64(parsed.Usage.OutputTokens) != total.Int() {
			return false
		}
	}
	return true
}

// Responses accepts Chat-style prompt/completion aliases for compatibility,
// but unlike raw Chat Completions it has no separate explicit-CC precedence
// contract. When both naming families are present they must describe the same
// canonical counters, and a supplied total must match that canonical pair.
func validOpenAIResponsesUsageShape(usage gjson.Result) bool {
	if !validOpenAIUsageShape(usage) ||
		!consistentOpenAIUsageIntegers(usage.Get("input_tokens"), usage.Get("prompt_tokens")) ||
		!consistentOpenAIUsageIntegers(usage.Get("output_tokens"), usage.Get("completion_tokens")) {
		return false
	}
	input := firstExistingOpenAIUsageCounter(usage, "input_tokens", "prompt_tokens")
	output := firstExistingOpenAIUsageCounter(usage, "output_tokens", "completion_tokens")
	return validOpenAIUsageRelations(
		usage,
		input,
		output,
		firstExistingOpenAIUsageCounter(usage, "input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "cache_read_tokens", "cached_tokens"),
		firstExistingOpenAIUsageCounter(usage, "input_tokens_details.cache_write_tokens", "input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_creation_tokens", "cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens"),
		firstExistingOpenAIUsageCounter(usage, "input_tokens_details.image_tokens", "prompt_tokens_details.image_tokens"),
		firstExistingOpenAIUsageCounter(usage, "output_tokens_details.image_tokens", "completion_tokens_details.image_tokens"),
	)
}

func consistentOpenAIUsageIntegers(values ...gjson.Result) bool {
	var expected int64
	found := false
	for _, value := range values {
		if !value.Exists() {
			continue
		}
		if !found {
			expected, found = value.Int(), true
			continue
		}
		if value.Int() != expected {
			return false
		}
	}
	return true
}

func canonicalOpenAIUsageShape(usage gjson.Result) string {
	if !usage.IsObject() {
		return ""
	}
	var canonical strings.Builder
	writeInt := func(label string, value gjson.Result) {
		_, _ = canonical.WriteString(label)
		_ = canonical.WriteByte('=')
		if value.Exists() {
			_, _ = canonical.WriteString(strconv.FormatInt(value.Int(), 10))
		} else {
			_ = canonical.WriteByte('-')
		}
		_ = canonical.WriteByte(';')
	}
	writeFloat := func(label string, value gjson.Result) {
		_, _ = canonical.WriteString(label)
		_ = canonical.WriteByte('=')
		if value.Exists() {
			_, _ = canonical.WriteString(strconv.FormatFloat(value.Float(), 'g', -1, 64))
		} else {
			_ = canonical.WriteByte('-')
		}
		_ = canonical.WriteByte(';')
	}
	writeInt("input", firstExistingOpenAIUsageCounter(usage, "input_tokens", "prompt_tokens"))
	writeInt("output", firstExistingOpenAIUsageCounter(usage, "output_tokens", "completion_tokens"))
	writeInt("total", usage.Get("total_tokens"))
	writeInt("cache_read", firstExistingOpenAIUsageCounter(usage,
		"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens",
		"cache_read_input_tokens", "cache_read_tokens", "cached_tokens"))
	writeInt("cache_write", firstExistingOpenAIUsageCounter(usage,
		"input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens",
		"input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_creation_tokens",
		"cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens"))
	for _, field := range []string{"image_tokens", "audio_tokens"} {
		writeInt("input_"+field, firstExistingOpenAIUsageCounter(usage, "input_tokens_details."+field, "prompt_tokens_details."+field))
	}
	for _, field := range []string{"reasoning_tokens", "image_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"} {
		writeInt("output_"+field, firstExistingOpenAIUsageCounter(usage, "output_tokens_details."+field, "completion_tokens_details."+field))
	}
	writeFloat("credits", firstExistingOpenAIUsageCounter(usage,
		"_sub2api_kiro_credits", "kiro_credits", "kiroCredits", "credits", "creditsUsed", "creditUsage", "consumedCredits"))
	return canonical.String()
}

func canonicalOpenAIHostedToolUsage(toolUsage gjson.Result) string {
	if !toolUsage.IsObject() {
		return ""
	}
	imageGen := toolUsage.Get("image_gen")
	if !imageGen.IsObject() {
		return ""
	}
	var canonical strings.Builder
	for _, path := range []string{"input_tokens", "output_tokens", "total_tokens", "images", "input_tokens_details.image_tokens", "output_tokens_details.image_tokens"} {
		_, _ = canonical.WriteString(path)
		_ = canonical.WriteByte('=')
		value := imageGen.Get(path)
		if value.Exists() {
			_, _ = canonical.WriteString(strconv.FormatInt(value.Int(), 10))
		} else {
			_ = canonical.WriteByte('-')
		}
		_ = canonical.WriteByte(';')
	}
	return canonical.String()
}

func firstExistingOpenAIUsageCounter(usage gjson.Result, paths ...string) gjson.Result {
	for _, path := range paths {
		if value := usage.Get(path); value.Exists() {
			return value
		}
	}
	return gjson.Result{}
}

func validOpenAIResponsesOutputItem(item gjson.Result) bool {
	if !item.IsObject() {
		return false
	}
	itemType := item.Get("type")
	if itemType.Type != gjson.String {
		return false
	}
	for _, field := range []string{"id", "status"} {
		if value := item.Get(field); value.Exists() && value.Type != gjson.String {
			return false
		}
	}
	// These composite field names are decoded by the compatibility bridges even
	// on forward-compatible item types. Validate and cap them before any typed
	// unmarshal so an unknown extension cannot smuggle a dense known field past
	// the strict provider boundary.
	if content := item.Get("content"); content.Exists() && !validBoundedOpenAIResponsesArray(content, validOpenAIResponsesMessageContentPart) {
		return false
	}
	if summary := item.Get("summary"); summary.Exists() && !validBoundedOpenAIResponsesArray(summary, validOpenAIResponsesReasoningPart) {
		return false
	}
	if encrypted := item.Get("encrypted_content"); encrypted.Exists() && encrypted.Type != gjson.String {
		return false
	}
	switch strings.TrimSpace(itemType.String()) {
	case "":
		return false
	case "message":
		if strings.TrimSpace(item.Get("role").String()) != "assistant" || !item.Get("content").IsArray() {
			return false
		}
		return true
	case "function_call":
		return nonEmptyGJSONString(item.Get("call_id")) &&
			nonEmptyGJSONString(item.Get("name")) &&
			item.Get("arguments").Type == gjson.String
	case "custom_tool_call":
		return nonEmptyGJSONString(item.Get("call_id")) &&
			nonEmptyGJSONString(item.Get("name")) &&
			item.Get("input").Type == gjson.String
	case "reasoning":
		recognized := item.Get("encrypted_content").Type == gjson.String
		for _, field := range []string{"summary", "content"} {
			parts := item.Get(field)
			if !parts.Exists() {
				continue
			}
			if !parts.IsArray() {
				return false
			}
			recognized = true
		}
		return recognized
	case "image_generation_call":
		for _, field := range []string{"result", "revised_prompt", "output_format", "size", "quality", "background"} {
			if value := item.Get(field); value.Exists() && value.Type != gjson.String {
				return false
			}
		}
		return true
	case "web_search_call":
		action := item.Get("action")
		if !action.Exists() {
			return true
		}
		if !action.IsObject() || !nonEmptyGJSONString(action.Get("type")) || len(action.Get("type").String()) > maxOpenAIResponsesRetainedStringBytes {
			return false
		}
		for _, field := range []string{"query", "url", "pattern"} {
			value := action.Get(field)
			if value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIResponsesRetainedStringBytes) {
				return false
			}
		}
		switch action.Get("type").String() {
		case "search":
			return nonEmptyGJSONString(action.Get("query"))
		case "open_page":
			return nonEmptyGJSONString(action.Get("url"))
		case "find_in_page":
			return nonEmptyGJSONString(action.Get("url")) && nonEmptyGJSONString(action.Get("pattern"))
		}
		return true
	default:
		// Responses is an extensible protocol. Unknown non-empty item types are
		// provider-owned and must remain forward-compatible.
		return true
	}
}

func validOpenAIResponsesReasoningPart(part gjson.Result) bool {
	if !part.IsObject() || !validOpenAIResponsesPartMetadata(part) || part.Get("type").Type != gjson.String || strings.TrimSpace(part.Get("type").String()) == "" {
		return false
	}
	switch strings.TrimSpace(part.Get("type").String()) {
	case "summary_text", "reasoning_text":
		return part.Get("text").Type == gjson.String
	default:
		return true
	}
}

func validOpenAIResponsesMessageContentPart(part gjson.Result) bool {
	if !part.IsObject() || !validOpenAIResponsesPartMetadata(part) {
		return false
	}
	partType := part.Get("type")
	if partType.Type != gjson.String {
		return false
	}
	switch strings.TrimSpace(partType.String()) {
	case "":
		return false
	case "output_text":
		return part.Get("text").Type == gjson.String
	case "refusal":
		return part.Get("refusal").Type == gjson.String
	default:
		return true
	}
}

func validOpenAIResponsesPartMetadata(part gjson.Result) bool {
	if id := part.Get("id"); id.Exists() && (id.Type != gjson.String || len(id.String()) > maxOpenAIResponsesRetainedStringBytes) {
		return false
	}
	for _, field := range []string{"annotations", "logprobs"} {
		value := part.Get(field)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		if !validBoundedOpenAIResponsesArray(value, func(gjson.Result) bool { return true }) {
			return false
		}
	}
	return true
}

type openAIResponsesSSEAttemptState struct {
	itemsByIndex          map[int64]*openAIResponsesTrackedItem
	indexByID             map[string]int64
	responseIdentity      map[string]openAIResponsesDoneFieldDigest
	trackedStateEntries   int
	authoritativeDoneOnly bool
}

type openAIResponsesTrackedItem struct {
	id                string
	itemType          string
	callID            string
	name              string
	done              bool
	inputDone         bool
	inputAggregate    openAIResponsesTrackedAggregate
	doneDigest        [sha256.Size]byte
	doneDigestSet     bool
	doneKnownFields   map[string]openAIResponsesDoneFieldDigest
	parts             map[int64]*openAIResponsesTrackedPart
	partByID          map[string]int64
	summaries         map[int64]*openAIResponsesTrackedPart
	reasoningTextSeen map[int64]bool
	reasoningTextDone map[int64]bool
	reasoningTexts    map[int64]*openAIResponsesTrackedAggregate
}

type openAIResponsesTrackedPart struct {
	id       string
	partType string
	textDone bool
	done     bool
	text     openAIResponsesTrackedAggregate
}

type openAIResponsesTrackedAggregate struct {
	hasher   hash.Hash
	length   int64
	sawDelta bool
	done     bool
	digest   [sha256.Size]byte
}

type openAIResponsesDoneFieldDigest struct {
	length int64
	digest [sha256.Size]byte
}

func openAIResponsesDoneField(value string) openAIResponsesDoneFieldDigest {
	return openAIResponsesDoneFieldDigest{length: int64(len(value)), digest: openAIResponsesStringDigest(value)}
}

func (item *openAIResponsesTrackedItem) retainDoneKnownFields(doneItem gjson.Result) {
	fields := []string{"status"}
	switch item.itemType {
	case "image_generation_call":
		fields = append(fields, "revised_prompt", "output_format", "size", "quality", "background")
	case "web_search_call":
		fields = append(fields, "action.type", "action.query", "action.url", "action.pattern")
	}
	for _, field := range fields {
		value := doneItem.Get(field)
		if !value.Exists() {
			continue
		}
		if item.doneKnownFields == nil {
			item.doneKnownFields = make(map[string]openAIResponsesDoneFieldDigest, len(fields))
		}
		item.doneKnownFields[field] = openAIResponsesDoneField(value.String())
	}
}

func (item *openAIResponsesTrackedItem) retainAddedStableFields(addedItem gjson.Result) {
	if item.itemType != "web_search_call" || !addedItem.Get("action").Exists() {
		return
	}
	for _, field := range []string{"action.type", "action.query", "action.url", "action.pattern"} {
		value := addedItem.Get(field)
		if !value.Exists() {
			continue
		}
		if item.doneKnownFields == nil {
			item.doneKnownFields = make(map[string]openAIResponsesDoneFieldDigest, 2)
		}
		item.doneKnownFields[field] = openAIResponsesDoneField(value.String())
	}
}

func (item *openAIResponsesTrackedItem) validateRetainedKnownFields(doneItem gjson.Result) error {
	for field, expected := range item.doneKnownFields {
		value := doneItem.Get(field)
		if !value.Exists() || openAIResponsesDoneField(value.String()) != expected {
			return fmt.Errorf("OpenAI Responses output item changed %s after it was published", field)
		}
	}
	return nil
}

func openAIResponsesStringDigest(value string) [sha256.Size]byte {
	h := sha256.New()
	_, _ = io.WriteString(h, value)
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func (a *openAIResponsesTrackedAggregate) addDelta(fragment string) {
	if a.hasher == nil {
		a.hasher = sha256.New()
	}
	_, _ = io.WriteString(a.hasher, fragment)
	a.length += int64(len(fragment))
	a.sawDelta = true
}

func (a *openAIResponsesTrackedAggregate) finish(fullValue, label string) error {
	if a.done {
		return fmt.Errorf("OpenAI Responses %s arrived after its done event", label)
	}
	digest := openAIResponsesStringDigest(fullValue)
	if a.sawDelta && (a.length != int64(len(fullValue)) || a.hasher == nil || !bytes.Equal(a.hasher.Sum(nil), digest[:])) {
		return fmt.Errorf("OpenAI Responses %s done value did not match accumulated deltas", label)
	}
	a.length = int64(len(fullValue))
	a.digest = digest
	a.done = true
	return nil
}

func (a *openAIResponsesTrackedAggregate) verify(fullValue, label string) error {
	if !a.done {
		return fmt.Errorf("OpenAI Responses %s aggregate was not completed", label)
	}
	digest := openAIResponsesStringDigest(fullValue)
	if a.length != int64(len(fullValue)) || a.digest != digest {
		return fmt.Errorf("OpenAI Responses %s value changed after its done event", label)
	}
	return nil
}

func retainedOpenAIResponsesString(value string) (string, error) {
	if len(value) > maxOpenAIResponsesRetainedStringBytes {
		return "", errors.New("OpenAI Responses retained string limit exceeded")
	}
	return value, nil
}

func (s *openAIResponsesSSEAttemptState) reserveTrackedStateEntry() error {
	if s.trackedStateEntries >= maxOpenAIResponsesTrackedStateEntries {
		return errors.New("OpenAI Responses correlation state limit exceeded")
	}
	s.trackedStateEntries++
	return nil
}

func (s *openAIResponsesSSEAttemptState) observe(payload []byte, eventType string) error {
	root := gjson.ParseBytes(payload)
	eventType = strings.TrimSpace(eventType)
	if response := root.Get("response"); response.IsObject() {
		if err := s.observeResponseIdentity(response); err != nil {
			return err
		}
	}
	switch eventType {
	case "response.output_item.added":
		if s.authoritativeDoneOnly {
			return errors.New("OpenAI Responses output_item.added arrived after authoritative done-only items")
		}
		index, itemID := root.Get("output_index"), root.Get("item.id")
		hasIndex := index.Exists()
		hasID := itemID.Exists()
		if !hasIndex && !hasID {
			// Compatibility relays may omit correlation fields altogether. Once
			// they provide them, however, they become protocol state and must not
			// conflict with later deltas.
			return nil
		}
		if !hasIndex || itemID.Type != gjson.String || strings.TrimSpace(itemID.String()) == "" {
			return errors.New("OpenAI Responses output_item.added only partially specified output_index/item.id")
		}
		retainedID, err := retainedOpenAIResponsesString(itemID.String())
		if err != nil {
			return err
		}
		retainedType, err := retainedOpenAIResponsesString(root.Get("item.type").String())
		if err != nil {
			return err
		}
		if s.itemsByIndex == nil {
			s.itemsByIndex = make(map[int64]*openAIResponsesTrackedItem)
			s.indexByID = make(map[string]int64)
		}
		if _, duplicate := s.itemsByIndex[index.Int()]; duplicate {
			return fmt.Errorf("OpenAI Responses duplicated output_index %d", index.Int())
		}
		if _, duplicate := s.indexByID[retainedID]; duplicate {
			return fmt.Errorf("OpenAI Responses duplicated item_id %q", retainedID)
		}
		if err := s.reserveTrackedStateEntry(); err != nil {
			return err
		}
		trackedItem := &openAIResponsesTrackedItem{id: retainedID, itemType: retainedType}
		if retainedType == "function_call" || retainedType == "custom_tool_call" {
			trackedItem.callID, err = retainedOpenAIResponsesString(root.Get("item.call_id").String())
			if err != nil {
				return err
			}
			trackedItem.name, err = retainedOpenAIResponsesString(root.Get("item.name").String())
			if err != nil {
				return err
			}
		}
		trackedItem.retainAddedStableFields(root.Get("item"))
		s.itemsByIndex[index.Int()] = trackedItem
		s.indexByID[retainedID] = index.Int()
		return nil
	case "response.output_item.done":
		index, itemID := root.Get("output_index"), root.Get("item.id")
		if !index.Exists() && !itemID.Exists() {
			// Some compact/compat relays publish a complete authoritative item
			// without opting into lifecycle correlation. Its payload shape remains
			// validated, while reconstruction consumes the provider-owned raw item.
			return nil
		}
		if _, exists := s.itemsByIndex[root.Get("output_index").Int()]; !exists && (len(s.itemsByIndex) == 0 || s.authoritativeDoneOnly) {
			return s.observeAuthoritativeOutputItemDone(root)
		}
		item, tracked, err := s.validateItemReference(root, root.Get("item.id"), root.Get("item.type").String())
		if err != nil || !tracked {
			return err
		}
		if item.done {
			return errors.New("OpenAI Responses duplicated output_item.done")
		}
		for _, part := range item.parts {
			if !part.done {
				return errors.New("OpenAI Responses output item completed with an active content part")
			}
		}
		for _, part := range item.summaries {
			if !part.done {
				return errors.New("OpenAI Responses output item completed with an active reasoning summary part")
			}
		}
		for contentIndex, seen := range item.reasoningTextSeen {
			if seen && !item.reasoningTextDone[contentIndex] {
				return errors.New("OpenAI Responses output item completed before reasoning_text.done")
			}
		}
		if (item.itemType == "function_call" || item.itemType == "custom_tool_call") && !item.inputDone {
			return errors.New("OpenAI Responses output item completed before its tool input done event")
		}
		if item.itemType == "function_call" || item.itemType == "custom_tool_call" {
			if root.Get("item.call_id").String() != item.callID || root.Get("item.name").String() != item.name {
				return errors.New("OpenAI Responses output_item.done changed tool call_id/name after output_item.added")
			}
		}
		if err := item.validateRetainedKnownFields(root.Get("item")); err != nil {
			return err
		}
		if err := s.validateOutputItemDoneAggregates(item, root.Get("item")); err != nil {
			return err
		}
		item.doneDigest = openAIResponsesOutputItemSemanticDigest(root.Get("item"))
		item.doneDigestSet = true
		item.retainDoneKnownFields(root.Get("item"))
		item.done = true
		return nil
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		return s.observeItemInput(root, "function_call", eventType == "response.function_call_arguments.done")
	case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		return s.observeItemInput(root, "custom_tool_call", eventType == "response.custom_tool_call_input.done")
	case "response.content_part.added":
		return s.observeContentPartAdded(root)
	case "response.output_text.delta", "response.output_text.done":
		return s.observeMessagePartText(root, "output_text", eventType == "response.output_text.done")
	case "response.refusal.delta", "response.refusal.done":
		return s.observeMessagePartText(root, "refusal", eventType == "response.refusal.done")
	case "response.content_part.done":
		return s.observeContentPartDone(root)
	case "response.reasoning_summary_part.added":
		return s.observeReasoningSummaryPartAdded(root)
	case "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		return s.observeReasoningSummaryText(root, eventType == "response.reasoning_summary_text.done")
	case "response.reasoning_summary_part.done":
		return s.observeReasoningSummaryPartDone(root)
	case "response.reasoning_text.delta", "response.reasoning_text.done":
		return s.observeReasoningText(root, eventType == "response.reasoning_text.done")
	case "response.completed", "response.done":
		return s.validateTerminal(root)
	default:
		return nil
	}
}

func (s *openAIResponsesSSEAttemptState) observeResponseIdentity(response gjson.Result) error {
	for _, field := range []string{"id", "object", "model", "created_at"} {
		value := response.Get(field)
		if !value.Exists() {
			continue
		}
		canonical := value.String()
		if value.Type == gjson.Number {
			canonical = strconv.FormatFloat(value.Float(), 'g', -1, 64)
		}
		digest := openAIResponsesDoneField(canonical)
		if s.responseIdentity == nil {
			s.responseIdentity = make(map[string]openAIResponsesDoneFieldDigest, 4)
		}
		if expected, seen := s.responseIdentity[field]; seen {
			if expected != digest {
				return fmt.Errorf("OpenAI Responses response identity changed %s across events", field)
			}
			continue
		}
		s.responseIdentity[field] = digest
	}
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeAuthoritativeOutputItemDone(root gjson.Result) error {
	index := root.Get("output_index")
	itemID := root.Get("item.id")
	if !index.Exists() || itemID.Type != gjson.String || strings.TrimSpace(itemID.String()) == "" {
		return errors.New("OpenAI Responses authoritative output_item.done only partially specified output_index/item.id")
	}
	retainedID, err := retainedOpenAIResponsesString(itemID.String())
	if err != nil {
		return err
	}
	retainedType, err := retainedOpenAIResponsesString(root.Get("item.type").String())
	if err != nil {
		return err
	}
	if s.itemsByIndex == nil {
		s.itemsByIndex = make(map[int64]*openAIResponsesTrackedItem)
		s.indexByID = make(map[string]int64)
	}
	if _, duplicate := s.itemsByIndex[index.Int()]; duplicate {
		return fmt.Errorf("OpenAI Responses duplicated output_index %d", index.Int())
	}
	if _, duplicate := s.indexByID[retainedID]; duplicate {
		return fmt.Errorf("OpenAI Responses duplicated item_id %q", retainedID)
	}
	if err := s.reserveTrackedStateEntry(); err != nil {
		return err
	}
	tracked := &openAIResponsesTrackedItem{
		id:            retainedID,
		itemType:      retainedType,
		done:          true,
		doneDigest:    openAIResponsesOutputItemSemanticDigest(root.Get("item")),
		doneDigestSet: true,
	}
	tracked.retainDoneKnownFields(root.Get("item"))
	if retainedType == "function_call" || retainedType == "custom_tool_call" {
		tracked.callID, err = retainedOpenAIResponsesString(root.Get("item.call_id").String())
		if err != nil {
			return err
		}
		tracked.name, err = retainedOpenAIResponsesString(root.Get("item.name").String())
		if err != nil {
			return err
		}
	}
	s.itemsByIndex[index.Int()] = tracked
	s.indexByID[retainedID] = index.Int()
	s.authoritativeDoneOnly = true
	return nil
}

func (s *openAIResponsesSSEAttemptState) validateItemReference(root, itemID gjson.Result, expectedType string) (*openAIResponsesTrackedItem, bool, error) {
	index := root.Get("output_index")
	if !index.Exists() && !itemID.Exists() {
		if len(s.itemsByIndex) == 0 {
			return nil, false, nil
		}
		return nil, false, errors.New("OpenAI Responses correlated provider event omitted output_index/item_id")
	}
	if !index.Exists() || itemID.Type != gjson.String || strings.TrimSpace(itemID.String()) == "" {
		return nil, false, errors.New("OpenAI Responses correlated provider event only partially specified output_index/item_id")
	}
	item, ok := s.itemsByIndex[index.Int()]
	if !ok || item.id != itemID.String() {
		return nil, false, fmt.Errorf("OpenAI Responses provider event referenced unknown or mismatched output item %d/%q", index.Int(), itemID.String())
	}
	if expectedType != "" && item.itemType != expectedType {
		return nil, false, fmt.Errorf("OpenAI Responses provider event referenced %s item as %s", item.itemType, expectedType)
	}
	if item.done {
		return nil, false, errors.New("OpenAI Responses provider event referenced a completed output item")
	}
	return item, true, nil
}

func (s *openAIResponsesSSEAttemptState) observeItemInput(root gjson.Result, expectedType string, done bool) error {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), expectedType)
	if err != nil || !tracked {
		return err
	}
	if item.inputDone {
		return errors.New("OpenAI Responses tool input arrived after its done event")
	}
	if callID := root.Get("call_id"); callID.Exists() && callID.String() != item.callID {
		return errors.New("OpenAI Responses tool input event changed call_id after output_item.added")
	}
	if name := root.Get("name"); name.Exists() && name.String() != item.name {
		return errors.New("OpenAI Responses tool input event changed name after output_item.added")
	}
	valueField := "delta"
	if done {
		valueField = "arguments"
		if expectedType == "custom_tool_call" {
			valueField = "input"
		}
		if err := item.inputAggregate.finish(root.Get(valueField).String(), expectedType+" input"); err != nil {
			return err
		}
	} else {
		item.inputAggregate.addDelta(root.Get(valueField).String())
	}
	if done {
		item.inputDone = true
	}
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeContentPartAdded(root gjson.Result) error {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), "message")
	if err != nil || !tracked {
		return err
	}
	contentIndex := root.Get("content_index")
	if !contentIndex.Exists() {
		return errors.New("OpenAI Responses correlated content_part.added omitted content_index")
	}
	part := root.Get("part")
	partID, err := retainedOpenAIResponsesString(strings.TrimSpace(part.Get("id").String()))
	if err != nil {
		return err
	}
	partType, err := retainedOpenAIResponsesString(part.Get("type").String())
	if err != nil {
		return err
	}
	if item.parts == nil {
		item.parts = make(map[int64]*openAIResponsesTrackedPart)
		item.partByID = make(map[string]int64)
	}
	if _, exists := item.parts[contentIndex.Int()]; exists {
		return fmt.Errorf("OpenAI Responses duplicated content_index %d", contentIndex.Int())
	}
	if partID != "" {
		if _, exists := item.partByID[partID]; exists {
			return fmt.Errorf("OpenAI Responses duplicated content part id %q", partID)
		}
		item.partByID[partID] = contentIndex.Int()
	}
	if err := s.reserveTrackedStateEntry(); err != nil {
		if partID != "" {
			delete(item.partByID, partID)
		}
		return err
	}
	item.parts[contentIndex.Int()] = &openAIResponsesTrackedPart{id: partID, partType: partType}
	return nil
}

func (s *openAIResponsesSSEAttemptState) trackedContentPart(root gjson.Result) (*openAIResponsesTrackedPart, bool, error) {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), "message")
	if err != nil || !tracked {
		return nil, tracked, err
	}
	contentIndex := root.Get("content_index")
	if !contentIndex.Exists() {
		return nil, false, errors.New("OpenAI Responses correlated content event omitted content_index")
	}
	part, ok := item.parts[contentIndex.Int()]
	if !ok {
		return nil, false, fmt.Errorf("OpenAI Responses content event referenced unknown content_index %d", contentIndex.Int())
	}
	return part, true, nil
}

func (s *openAIResponsesSSEAttemptState) observeMessagePartText(root gjson.Result, expectedType string, done bool) error {
	part, tracked, err := s.trackedContentPart(root)
	if err != nil || !tracked {
		return err
	}
	if part.partType != expectedType {
		return fmt.Errorf("OpenAI Responses %s event referenced %s content part", expectedType, part.partType)
	}
	if part.done || part.textDone {
		return errors.New("OpenAI Responses output text arrived after its done event")
	}
	if done {
		field := "text"
		if expectedType == "refusal" {
			field = "refusal"
		}
		if err := part.text.finish(root.Get(field).String(), expectedType); err != nil {
			return err
		}
		part.textDone = true
	} else {
		part.text.addDelta(root.Get("delta").String())
	}
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeContentPartDone(root gjson.Result) error {
	part, tracked, err := s.trackedContentPart(root)
	if err != nil || !tracked {
		return err
	}
	if part.done {
		return errors.New("OpenAI Responses duplicated content_part.done")
	}
	if payloadType := strings.TrimSpace(root.Get("part.type").String()); payloadType != "" && payloadType != part.partType {
		return fmt.Errorf("OpenAI Responses content_part.done changed part type from %s to %s", part.partType, payloadType)
	}
	if payloadID := root.Get("part.id"); payloadID.Exists() && payloadID.String() != part.id {
		return errors.New("OpenAI Responses content_part.done changed part id after content_part.added")
	}
	if (part.partType == "output_text" || part.partType == "refusal") && !part.textDone {
		return errors.New("OpenAI Responses content_part.done arrived before output_text.done")
	}
	if part.partType == "output_text" || part.partType == "refusal" {
		field := "text"
		if part.partType == "refusal" {
			field = "refusal"
		}
		if err := part.text.verify(root.Get("part."+field).String(), part.partType+" content_part.done"); err != nil {
			return err
		}
	}
	part.done = true
	return nil
}

func (s *openAIResponsesSSEAttemptState) validateOutputItemDoneAggregates(tracked *openAIResponsesTrackedItem, doneItem gjson.Result) error {
	if tracked == nil {
		return nil
	}
	switch tracked.itemType {
	case "function_call":
		return tracked.inputAggregate.verify(doneItem.Get("arguments").String(), "function_call output_item.done")
	case "custom_tool_call":
		return tracked.inputAggregate.verify(doneItem.Get("input").String(), "custom_tool_call output_item.done")
	case "message":
		if len(tracked.parts) == 0 {
			return nil
		}
		content := doneItem.Get("content")
		count := 0
		var aggregateErr error
		content.ForEach(func(_, partValue gjson.Result) bool {
			index := int64(count)
			count++
			part, ok := tracked.parts[index]
			if !ok || part.partType != partValue.Get("type").String() {
				aggregateErr = fmt.Errorf("OpenAI Responses output_item.done changed message content part %d", index)
				return false
			}
			field := "text"
			if part.partType == "refusal" {
				field = "refusal"
			}
			if err := part.text.verify(partValue.Get(field).String(), part.partType+" output_item.done"); err != nil {
				aggregateErr = err
				return false
			}
			return true
		})
		if aggregateErr != nil {
			return aggregateErr
		}
		if count != len(tracked.parts) {
			return fmt.Errorf("OpenAI Responses output_item.done message content count %d did not match tracked count %d", count, len(tracked.parts))
		}
	case "reasoning":
		if len(tracked.summaries) > 0 {
			count := 0
			var aggregateErr error
			doneItem.Get("summary").ForEach(func(_, partValue gjson.Result) bool {
				index := int64(count)
				count++
				part, ok := tracked.summaries[index]
				if !ok || part.partType != partValue.Get("type").String() {
					aggregateErr = fmt.Errorf("OpenAI Responses output_item.done changed reasoning summary part %d", index)
					return false
				}
				if err := part.text.verify(partValue.Get("text").String(), "reasoning summary output_item.done"); err != nil {
					aggregateErr = err
					return false
				}
				return true
			})
			if aggregateErr != nil {
				return aggregateErr
			}
			if count != len(tracked.summaries) {
				return fmt.Errorf("OpenAI Responses output_item.done reasoning summary count %d did not match tracked count %d", count, len(tracked.summaries))
			}
		}
		if len(tracked.reasoningTexts) > 0 {
			count := 0
			var aggregateErr error
			doneItem.Get("content").ForEach(func(_, partValue gjson.Result) bool {
				index := int64(count)
				count++
				aggregate, ok := tracked.reasoningTexts[index]
				if !ok || partValue.Get("type").String() != "reasoning_text" {
					aggregateErr = fmt.Errorf("OpenAI Responses output_item.done changed reasoning content part %d", index)
					return false
				}
				if err := aggregate.verify(partValue.Get("text").String(), "reasoning text output_item.done"); err != nil {
					aggregateErr = err
					return false
				}
				return true
			})
			if aggregateErr != nil {
				return aggregateErr
			}
			if count != len(tracked.reasoningTexts) {
				return fmt.Errorf("OpenAI Responses output_item.done reasoning content count %d did not match tracked count %d", count, len(tracked.reasoningTexts))
			}
		}
	}
	return nil
}

func writeOpenAIResponsesSemanticField(h hash.Hash, label string, value gjson.Result) {
	_, _ = io.WriteString(h, label)
	_, _ = io.WriteString(h, ":")
	if !value.Exists() {
		_, _ = io.WriteString(h, "missing;")
		return
	}
	text := value.String()
	_, _ = io.WriteString(h, strconv.Itoa(int(value.Type)))
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, strconv.Itoa(len(text)))
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, text)
	_, _ = io.WriteString(h, ";")
}

func openAIResponsesOutputItemSemanticDigest(item gjson.Result) [sha256.Size]byte {
	h := sha256.New()
	for _, field := range []string{
		"id", "type", "role", "call_id", "name", "arguments", "input", "encrypted_content", "result",
	} {
		writeOpenAIResponsesSemanticField(h, field, item.Get(field))
	}
	for _, collection := range []string{"content", "summary"} {
		count := 0
		item.Get(collection).ForEach(func(_, part gjson.Result) bool {
			prefix := collection + "." + strconv.Itoa(count) + "."
			for _, field := range []string{"id", "type", "text", "refusal"} {
				writeOpenAIResponsesSemanticField(h, prefix+field, part.Get(field))
			}
			count++
			return true
		})
		_, _ = io.WriteString(h, collection+".count:"+strconv.Itoa(count)+";")
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func (s *openAIResponsesSSEAttemptState) validateTerminal(root gjson.Result) error {
	for index, item := range s.itemsByIndex {
		if !item.done {
			return fmt.Errorf("OpenAI Responses terminal event arrived before output item %d completed", index)
		}
		for contentIndex, part := range item.parts {
			if !part.done {
				return fmt.Errorf("OpenAI Responses terminal event arrived before content part %d/%d completed", index, contentIndex)
			}
		}
		for summaryIndex, part := range item.summaries {
			if !part.done {
				return fmt.Errorf("OpenAI Responses terminal event arrived before reasoning summary part %d/%d completed", index, summaryIndex)
			}
		}
		for contentIndex, seen := range item.reasoningTextSeen {
			if seen && !item.reasoningTextDone[contentIndex] {
				return fmt.Errorf("OpenAI Responses terminal event arrived before reasoning text %d/%d completed", index, contentIndex)
			}
		}
	}
	output := root.Get("response.output")
	if !output.IsArray() || !gjsonCollectionHasValues(output) {
		// Empty terminal output is explicitly supported: compatibility handlers
		// reconstruct it from the already validated lifecycle events.
		return nil
	}
	if len(s.itemsByIndex) == 0 {
		// A relay that never opted into explicit lifecycle correlation may send a
		// complete terminal response directly. Its per-payload shape was already
		// validated; there is no cross-event aggregate to compare here.
		return nil
	}
	// Compact providers may emit the compaction item only through
	// response.output_item.done and omit it from a non-empty terminal output.
	// The compatibility bridge supplements that provider-owned item into the
	// terminal response. Compare every correlated non-compaction item by ID,
	// while permitting terminal-only items and an omitted compaction item.
	hasSupplementalCompaction := false
	for _, tracked := range s.itemsByIndex {
		if tracked.itemType == "compaction" {
			hasSupplementalCompaction = true
			break
		}
	}
	if hasSupplementalCompaction {
		terminalByID := make(map[string]gjson.Result)
		output.ForEach(func(_, terminalItem gjson.Result) bool {
			if id := strings.TrimSpace(terminalItem.Get("id").String()); id != "" {
				terminalByID[id] = terminalItem
			}
			return true
		})
		for index, tracked := range s.itemsByIndex {
			terminalItem, exists := terminalByID[tracked.id]
			if !exists {
				if tracked.itemType == "compaction" {
					continue
				}
				return fmt.Errorf("OpenAI Responses terminal output omitted tracked item %d", index)
			}
			if digest := openAIResponsesOutputItemSemanticDigest(terminalItem); digest != tracked.doneDigest {
				return fmt.Errorf("OpenAI Responses terminal output item %d differed from output_item.done", index)
			}
			for field, expected := range tracked.doneKnownFields {
				value := terminalItem.Get(field)
				if !value.Exists() || openAIResponsesDoneField(value.String()) != expected {
					return fmt.Errorf("OpenAI Responses terminal output item %d changed %s after output_item.done", index, field)
				}
			}
		}
		return nil
	}
	outputCount := 0
	var terminalErr error
	output.ForEach(func(_, terminalItem gjson.Result) bool {
		index := int64(outputCount)
		outputCount++
		tracked, ok := s.itemsByIndex[index]
		if !ok || !tracked.doneDigestSet {
			terminalErr = fmt.Errorf("OpenAI Responses terminal output referenced unknown output item %d", index)
			return false
		}
		if digest := openAIResponsesOutputItemSemanticDigest(terminalItem); digest != tracked.doneDigest {
			terminalErr = fmt.Errorf("OpenAI Responses terminal output item %d differed from output_item.done", index)
			return false
		}
		for field, expected := range tracked.doneKnownFields {
			value := terminalItem.Get(field)
			if !value.Exists() || openAIResponsesDoneField(value.String()) != expected {
				terminalErr = fmt.Errorf("OpenAI Responses terminal output item %d changed %s after output_item.done", index, field)
				return false
			}
		}
		return true
	})
	if terminalErr != nil {
		return terminalErr
	}
	if outputCount != len(s.itemsByIndex) {
		return fmt.Errorf("OpenAI Responses terminal output item count %d did not match tracked item count %d", outputCount, len(s.itemsByIndex))
	}
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeReasoningSummaryPartAdded(root gjson.Result) error {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), "reasoning")
	if err != nil || !tracked {
		return err
	}
	index := root.Get("summary_index")
	if !index.Exists() {
		return errors.New("OpenAI Responses correlated reasoning summary part omitted summary_index")
	}
	if item.summaries == nil {
		item.summaries = make(map[int64]*openAIResponsesTrackedPart)
	}
	if _, exists := item.summaries[index.Int()]; exists {
		return fmt.Errorf("OpenAI Responses duplicated reasoning summary_index %d", index.Int())
	}
	partType, err := retainedOpenAIResponsesString(root.Get("part.type").String())
	if err != nil {
		return err
	}
	if err := s.reserveTrackedStateEntry(); err != nil {
		return err
	}
	partID, err := retainedOpenAIResponsesString(strings.TrimSpace(root.Get("part.id").String()))
	if err != nil {
		return err
	}
	item.summaries[index.Int()] = &openAIResponsesTrackedPart{id: partID, partType: partType}
	return nil
}

func (s *openAIResponsesSSEAttemptState) trackedReasoningSummaryPart(root gjson.Result) (*openAIResponsesTrackedPart, bool, error) {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), "reasoning")
	if err != nil || !tracked {
		return nil, tracked, err
	}
	index := root.Get("summary_index")
	if !index.Exists() {
		return nil, false, errors.New("OpenAI Responses correlated reasoning summary event omitted summary_index")
	}
	part, ok := item.summaries[index.Int()]
	if !ok {
		return nil, false, fmt.Errorf("OpenAI Responses reasoning event referenced unknown summary_index %d", index.Int())
	}
	return part, true, nil
}

func (s *openAIResponsesSSEAttemptState) observeReasoningSummaryText(root gjson.Result, done bool) error {
	part, tracked, err := s.trackedReasoningSummaryPart(root)
	if err != nil || !tracked {
		return err
	}
	if part.partType != "summary_text" {
		return fmt.Errorf("OpenAI Responses reasoning summary text referenced %s part", part.partType)
	}
	if part.done || part.textDone {
		return errors.New("OpenAI Responses reasoning summary text arrived after its done event")
	}
	if done {
		if err := part.text.finish(root.Get("text").String(), "reasoning summary text"); err != nil {
			return err
		}
		part.textDone = true
	} else {
		part.text.addDelta(root.Get("delta").String())
	}
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeReasoningSummaryPartDone(root gjson.Result) error {
	part, tracked, err := s.trackedReasoningSummaryPart(root)
	if err != nil || !tracked {
		return err
	}
	if part.done {
		return errors.New("OpenAI Responses duplicated reasoning_summary_part.done")
	}
	if payloadType := strings.TrimSpace(root.Get("part.type").String()); payloadType != "" && payloadType != part.partType {
		return fmt.Errorf("OpenAI Responses reasoning_summary_part.done changed part type from %s to %s", part.partType, payloadType)
	}
	if payloadID := root.Get("part.id"); payloadID.Exists() && payloadID.String() != part.id {
		return errors.New("OpenAI Responses reasoning_summary_part.done changed part id after part.added")
	}
	if part.partType == "summary_text" && !part.textDone {
		return errors.New("OpenAI Responses reasoning_summary_part.done arrived before reasoning_summary_text.done")
	}
	if part.partType == "summary_text" {
		if err := part.text.verify(root.Get("part.text").String(), "reasoning_summary_part.done"); err != nil {
			return err
		}
	}
	part.done = true
	return nil
}

func (s *openAIResponsesSSEAttemptState) observeReasoningText(root gjson.Result, done bool) error {
	item, tracked, err := s.validateItemReference(root, root.Get("item_id"), "reasoning")
	if err != nil || !tracked {
		return err
	}
	index := root.Get("content_index")
	if !index.Exists() {
		return errors.New("OpenAI Responses correlated reasoning text omitted content_index")
	}
	if item.reasoningTextDone == nil {
		item.reasoningTextSeen = make(map[int64]bool)
		item.reasoningTextDone = make(map[int64]bool)
		item.reasoningTexts = make(map[int64]*openAIResponsesTrackedAggregate)
	}
	if item.reasoningTextDone[index.Int()] {
		return errors.New("OpenAI Responses reasoning text arrived after its done event")
	}
	if !item.reasoningTextSeen[index.Int()] {
		if err := s.reserveTrackedStateEntry(); err != nil {
			return err
		}
	}
	aggregate := item.reasoningTexts[index.Int()]
	if aggregate == nil {
		aggregate = &openAIResponsesTrackedAggregate{}
		item.reasoningTexts[index.Int()] = aggregate
	}
	item.reasoningTextSeen[index.Int()] = true
	if done {
		if err := aggregate.finish(root.Get("text").String(), "reasoning text"); err != nil {
			return err
		}
		item.reasoningTextDone[index.Int()] = true
	} else {
		aggregate.addDelta(root.Get("delta").String())
	}
	return nil
}

type openAIResponsesJSONObjectContext uint8

const (
	openAIResponsesJSONUnknown openAIResponsesJSONObjectContext = iota
	openAIResponsesJSONEvent
	openAIResponsesJSONResponse
	openAIResponsesJSONOutputItem
	openAIResponsesJSONPart
	openAIResponsesJSONUsage
	openAIResponsesJSONUsageDetails
	openAIResponsesJSONToolUsage
	openAIResponsesJSONImageGenUsage
	openAIResponsesJSONWebSearchAction
	openAIResponsesJSONError
	openAIResponsesJSONIncompleteDetails
	providerJSONGeminiRoot
	providerJSONGeminiCandidate
	providerJSONGeminiContent
	providerJSONGeminiPart
	providerJSONGeminiFunctionCall
	providerJSONGeminiFunctionResponse
	providerJSONGeminiInlineData
	providerJSONGeminiFileData
	providerJSONGeminiExecutableCode
	providerJSONGeminiCodeExecutionResult
	providerJSONGeminiPromptFeedback
	providerJSONGeminiUsage
	providerJSONGeminiTokenDetail
	providerJSONGeminiSafetyRating
	providerJSONGeminiGroundingMetadata
	providerJSONGeminiGroundingChunk
	providerJSONGeminiGroundingWeb
	providerJSONAnthropicEvent
	providerJSONAnthropicMessage
	providerJSONAnthropicContentBlock
	providerJSONAnthropicDelta
	providerJSONAnthropicUsage
	providerJSONAnthropicCacheCreation
	providerJSONAnthropicError
	providerJSONOpenAIChatRoot
	providerJSONOpenAIChatChoice
	providerJSONOpenAIChatMessage
	providerJSONOpenAIChatToolCall
	providerJSONOpenAIChatFunction
	providerJSONOpenAIChatPromptAnnotation
	providerJSONOpenAIChatFilterOffsets
	providerJSONOpenAIChatAudio
)

func openAIResponsesKnownKeyBit(ctx openAIResponsesJSONObjectContext, key string) uint64 {
	var fields []string
	switch ctx {
	case openAIResponsesJSONEvent:
		fields = []string{"type", "output_index", "content_index", "summary_index", "sequence_number", "item_id", "call_id", "name", "delta", "text", "refusal", "arguments", "input", "part", "item", "response", "error", "message", "usage", "tool_usage"}
	case openAIResponsesJSONResponse:
		fields = []string{"id", "object", "created_at", "model", "status", "output", "usage", "tool_usage", "error", "incomplete_details", "instructions", "metadata", "parallel_tool_calls", "temperature", "tool_choice", "tools", "top_p"}
	case openAIResponsesJSONOutputItem:
		fields = []string{"id", "type", "status", "role", "call_id", "name", "arguments", "input", "content", "summary", "encrypted_content", "result", "revised_prompt", "output_format", "size", "quality", "background", "action"}
	case openAIResponsesJSONPart:
		fields = []string{"id", "type", "text", "refusal", "annotations", "logprobs"}
	case openAIResponsesJSONUsage:
		fields = []string{
			"input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens",
			"input_tokens_details", "output_tokens_details", "prompt_tokens_details", "completion_tokens_details",
			"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "cache_write_tokens",
			"cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens",
			"_sub2api_kiro_credits", "kiro_credits", "kiroCredits", "credits", "creditsUsed", "creditUsage", "consumedCredits",
		}
	case openAIResponsesJSONUsageDetails:
		fields = []string{"cached_tokens", "audio_tokens", "image_tokens", "reasoning_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens", "cache_creation_tokens", "cache_write_tokens"}
	case openAIResponsesJSONToolUsage:
		fields = []string{"image_gen"}
	case openAIResponsesJSONImageGenUsage:
		fields = []string{"input_tokens", "output_tokens", "total_tokens", "images", "input_tokens_details", "output_tokens_details"}
	case openAIResponsesJSONWebSearchAction:
		fields = []string{"type", "query", "url", "pattern"}
	case openAIResponsesJSONError:
		fields = []string{"type", "code", "message", "param"}
	case openAIResponsesJSONIncompleteDetails:
		fields = []string{"reason"}
	case providerJSONGeminiRoot:
		fields = []string{"candidates", "promptFeedback", "usageMetadata", "modelVersion", "responseId", "createTime", "totalTokens"}
	case providerJSONGeminiCandidate:
		fields = []string{"index", "content", "finishReason", "finishMessage", "safetyRatings", "citationMetadata", "groundingMetadata", "avgLogprobs", "logprobsResult", "urlContextMetadata"}
	case providerJSONGeminiContent:
		fields = []string{"role", "parts"}
	case providerJSONGeminiPart:
		fields = []string{"text", "thought", "thoughtSignature", "functionCall", "functionResponse", "inlineData", "fileData", "executableCode", "codeExecutionResult"}
	case providerJSONGeminiFunctionCall:
		fields = []string{"id", "name", "args"}
	case providerJSONGeminiFunctionResponse:
		fields = []string{"id", "name", "response"}
	case providerJSONGeminiInlineData:
		fields = []string{"mimeType", "data", "displayName"}
	case providerJSONGeminiFileData:
		fields = []string{"mimeType", "fileUri"}
	case providerJSONGeminiExecutableCode:
		fields = []string{"language", "code"}
	case providerJSONGeminiCodeExecutionResult:
		fields = []string{"outcome", "output"}
	case providerJSONGeminiPromptFeedback:
		fields = []string{"blockReason", "blockReasonMessage", "safetyRatings"}
	case providerJSONGeminiUsage:
		fields = []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "toolUsePromptTokenCount", "promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "toolUsePromptTokensDetails"}
	case providerJSONGeminiTokenDetail:
		fields = []string{"modality", "tokenCount"}
	case providerJSONGeminiSafetyRating:
		fields = []string{"category", "probability", "severity", "blocked", "probabilityScore", "severityScore"}
	case providerJSONGeminiGroundingMetadata:
		fields = []string{"webSearchQueries", "groundingChunks", "groundingSupports", "retrievalMetadata", "searchEntryPoint"}
	case providerJSONGeminiGroundingChunk:
		fields = []string{"web", "retrievedContext", "maps", "image"}
	case providerJSONGeminiGroundingWeb:
		fields = []string{"title", "uri"}
	case providerJSONAnthropicEvent:
		fields = []string{"type", "message", "index", "content_block", "delta", "usage", "error"}
	case providerJSONAnthropicMessage:
		fields = []string{"id", "type", "role", "content", "model", "stop_reason", "stop_sequence", "usage"}
	case providerJSONAnthropicContentBlock:
		fields = []string{"type", "text", "thinking", "signature", "data", "id", "name", "input", "tool_use_id", "content", "is_error", "cache_control", "source"}
	case providerJSONAnthropicDelta:
		fields = []string{"type", "text", "partial_json", "thinking", "signature", "stop_reason", "stop_sequence", "citation"}
	case providerJSONAnthropicUsage:
		fields = []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "cached_tokens", "cache_creation", "_sub2api_kiro_credits", kiroFinalUsageSSEField}
	case providerJSONAnthropicCacheCreation:
		fields = []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"}
	case providerJSONAnthropicError:
		fields = []string{"type", "message"}
	case providerJSONOpenAIChatRoot:
		fields = []string{"id", "object", "created", "model", "choices", "usage", "system_fingerprint", "service_tier", "prompt_filter_results", "prompt_annotations", "error"}
	case providerJSONOpenAIChatChoice:
		fields = []string{"index", "delta", "message", "text", "finish_reason", "logprobs", "content_filter_results", "content_filter_offsets"}
	case providerJSONOpenAIChatMessage:
		fields = []string{"role", "content", "reasoning", "reasoning_content", "reasoning_summary", "refusal", "tool_calls", "function_call", "audio", "annotations"}
	case providerJSONOpenAIChatToolCall:
		fields = []string{"index", "id", "type", "function"}
	case providerJSONOpenAIChatFunction:
		fields = []string{"name", "arguments"}
	case providerJSONOpenAIChatPromptAnnotation:
		fields = []string{"prompt_index", "content_filter_results"}
	case providerJSONOpenAIChatFilterOffsets:
		fields = []string{"check_offset", "start_offset", "end_offset"}
	case providerJSONOpenAIChatAudio:
		fields = []string{"id", "data", "transcript", "expires_at"}
	}
	for index, field := range fields {
		if key == field {
			return uint64(1) << index
		}
	}
	return 0
}

func openAIResponsesKnownRawKey(ctx openAIResponsesJSONObjectContext, raw []byte) (uint64, string) {
	if len(raw) < 2 {
		return 0, ""
	}
	if !bytes.ContainsRune(raw, '\\') {
		body := raw[1 : len(raw)-1]
		if len(body) == 0 {
			return 0, ""
		}
		// The key is used only during this synchronous scan while payload remains
		// live and immutable. Avoid allocating one string per key in dense
		// provider arrays; escaped keys take the allocating Unquote path below.
		key := unsafe.String(unsafe.SliceData(body), len(body))
		return openAIResponsesKnownKeyBit(ctx, key), key
	}
	decoded, err := strconv.Unquote(string(raw))
	if err != nil {
		return 0, ""
	}
	return openAIResponsesKnownKeyBit(ctx, decoded), decoded
}

func openAIResponsesJSONChildContext(ctx openAIResponsesJSONObjectContext, key string) openAIResponsesJSONObjectContext {
	switch ctx {
	case openAIResponsesJSONEvent:
		switch key {
		case "response":
			return openAIResponsesJSONResponse
		case "item":
			return openAIResponsesJSONOutputItem
		case "part":
			return openAIResponsesJSONPart
		case "usage":
			return openAIResponsesJSONUsage
		case "tool_usage":
			return openAIResponsesJSONToolUsage
		case "error":
			return openAIResponsesJSONError
		}
	case openAIResponsesJSONResponse:
		switch key {
		case "output":
			return openAIResponsesJSONOutputItem
		case "usage":
			return openAIResponsesJSONUsage
		case "tool_usage":
			return openAIResponsesJSONToolUsage
		case "error":
			return openAIResponsesJSONError
		case "incomplete_details":
			return openAIResponsesJSONIncompleteDetails
		}
	case openAIResponsesJSONOutputItem:
		if key == "content" || key == "summary" {
			return openAIResponsesJSONPart
		}
		if key == "action" {
			return openAIResponsesJSONWebSearchAction
		}
	case openAIResponsesJSONUsage:
		if key == "input_tokens_details" || key == "output_tokens_details" || key == "prompt_tokens_details" || key == "completion_tokens_details" {
			return openAIResponsesJSONUsageDetails
		}
	case openAIResponsesJSONToolUsage:
		if key == "image_gen" {
			return openAIResponsesJSONImageGenUsage
		}
	case openAIResponsesJSONImageGenUsage:
		if key == "input_tokens_details" || key == "output_tokens_details" {
			return openAIResponsesJSONUsageDetails
		}
	case providerJSONGeminiRoot:
		switch key {
		case "candidates":
			return providerJSONGeminiCandidate
		case "promptFeedback":
			return providerJSONGeminiPromptFeedback
		case "usageMetadata":
			return providerJSONGeminiUsage
		}
	case providerJSONGeminiCandidate:
		switch key {
		case "content":
			return providerJSONGeminiContent
		case "safetyRatings":
			return providerJSONGeminiSafetyRating
		case "groundingMetadata":
			return providerJSONGeminiGroundingMetadata
		}
	case providerJSONGeminiContent:
		if key == "parts" {
			return providerJSONGeminiPart
		}
	case providerJSONGeminiPart:
		switch key {
		case "functionCall":
			return providerJSONGeminiFunctionCall
		case "functionResponse":
			return providerJSONGeminiFunctionResponse
		case "inlineData":
			return providerJSONGeminiInlineData
		case "fileData":
			return providerJSONGeminiFileData
		case "executableCode":
			return providerJSONGeminiExecutableCode
		case "codeExecutionResult":
			return providerJSONGeminiCodeExecutionResult
		}
	case providerJSONGeminiPromptFeedback:
		if key == "safetyRatings" {
			return providerJSONGeminiSafetyRating
		}
	case providerJSONGeminiUsage:
		switch key {
		case "promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "toolUsePromptTokensDetails":
			return providerJSONGeminiTokenDetail
		}
	case providerJSONGeminiGroundingMetadata:
		if key == "groundingChunks" {
			return providerJSONGeminiGroundingChunk
		}
	case providerJSONGeminiGroundingChunk:
		if key == "web" {
			return providerJSONGeminiGroundingWeb
		}
	case providerJSONAnthropicEvent:
		switch key {
		case "message":
			return providerJSONAnthropicMessage
		case "content_block":
			return providerJSONAnthropicContentBlock
		case "delta":
			return providerJSONAnthropicDelta
		case "usage":
			return providerJSONAnthropicUsage
		case "error":
			return providerJSONAnthropicError
		}
	case providerJSONAnthropicMessage:
		switch key {
		case "content":
			return providerJSONAnthropicContentBlock
		case "usage":
			return providerJSONAnthropicUsage
		}
	case providerJSONAnthropicUsage:
		if key == "cache_creation" {
			return providerJSONAnthropicCacheCreation
		}
	case providerJSONOpenAIChatRoot:
		switch key {
		case "choices":
			return providerJSONOpenAIChatChoice
		case "usage":
			return openAIResponsesJSONUsage
		case "prompt_filter_results", "prompt_annotations":
			return providerJSONOpenAIChatPromptAnnotation
		case "error":
			return openAIResponsesJSONError
		}
	case providerJSONOpenAIChatChoice:
		switch key {
		case "delta", "message":
			return providerJSONOpenAIChatMessage
		case "content_filter_offsets":
			return providerJSONOpenAIChatFilterOffsets
		}
	case providerJSONOpenAIChatMessage:
		switch key {
		case "tool_calls":
			return providerJSONOpenAIChatToolCall
		case "function_call":
			return providerJSONOpenAIChatFunction
		case "audio":
			return providerJSONOpenAIChatAudio
		}
	case providerJSONOpenAIChatToolCall:
		if key == "function" {
			return providerJSONOpenAIChatFunction
		}
	}
	return openAIResponsesJSONUnknown
}

type openAIResponsesDuplicateKeyScanner struct {
	payload []byte
}

func (s openAIResponsesDuplicateKeyScanner) skipWhitespace(index int) int {
	for index < len(s.payload) {
		switch s.payload[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func (s openAIResponsesDuplicateKeyScanner) scanString(index int) (int, error) {
	if index >= len(s.payload) || s.payload[index] != '"' {
		return index, errors.New("OpenAI Responses JSON object key was not a string")
	}
	for index++; index < len(s.payload); index++ {
		switch s.payload[index] {
		case '\\':
			index++
		case '"':
			return index + 1, nil
		}
	}
	return index, errors.New("OpenAI Responses JSON string was incomplete")
}

func (s openAIResponsesDuplicateKeyScanner) scanValue(index int, ctx openAIResponsesJSONObjectContext, depth int) (int, error) {
	if depth > 256 {
		return index, errors.New("OpenAI Responses JSON nesting limit exceeded")
	}
	index = s.skipWhitespace(index)
	if index >= len(s.payload) {
		return index, errors.New("OpenAI Responses JSON value was incomplete")
	}
	switch s.payload[index] {
	case '{':
		return s.scanObject(index, ctx, depth+1)
	case '[':
		index++
		for {
			index = s.skipWhitespace(index)
			if index >= len(s.payload) {
				return index, errors.New("OpenAI Responses JSON array was incomplete")
			}
			if s.payload[index] == ']' {
				return index + 1, nil
			}
			var err error
			index, err = s.scanValue(index, ctx, depth+1)
			if err != nil {
				return index, err
			}
			index = s.skipWhitespace(index)
			if index < len(s.payload) && s.payload[index] == ',' {
				index++
				continue
			}
		}
	case '"':
		return s.scanString(index)
	default:
		for index < len(s.payload) && !bytes.ContainsRune([]byte(" \t\r\n,]}"), rune(s.payload[index])) {
			index++
		}
		return index, nil
	}
}

func (s openAIResponsesDuplicateKeyScanner) scanObject(index int, ctx openAIResponsesJSONObjectContext, depth int) (int, error) {
	index++
	var seen uint64
	for {
		index = s.skipWhitespace(index)
		if index >= len(s.payload) {
			return index, errors.New("OpenAI Responses JSON object was incomplete")
		}
		if s.payload[index] == '}' {
			return index + 1, nil
		}
		keyStart := index
		keyEnd, err := s.scanString(index)
		if err != nil {
			return index, err
		}
		bit, key := openAIResponsesKnownRawKey(ctx, s.payload[keyStart:keyEnd])
		if bit != 0 {
			if seen&bit != 0 {
				return index, fmt.Errorf("OpenAI Responses JSON repeated known field %q", key)
			}
			seen |= bit
		}
		index = s.skipWhitespace(keyEnd)
		if index >= len(s.payload) || s.payload[index] != ':' {
			return index, errors.New("OpenAI Responses JSON object omitted colon")
		}
		index, err = s.scanValue(index+1, openAIResponsesJSONChildContext(ctx, key), depth+1)
		if err != nil {
			return index, err
		}
		index = s.skipWhitespace(index)
		if index < len(s.payload) && s.payload[index] == ',' {
			index++
			continue
		}
	}
}

func validateOpenAIResponsesNoDuplicateKnownFields(payload []byte, rootContext openAIResponsesJSONObjectContext) error {
	scanner := openAIResponsesDuplicateKeyScanner{payload: payload}
	end, err := scanner.scanValue(0, rootContext, 0)
	if err != nil {
		return err
	}
	if scanner.skipWhitespace(end) != len(payload) {
		return errors.New("OpenAI Responses JSON contained trailing data")
	}
	return nil
}

func validateOpenAIResponsesSSEPayload(payload []byte, declaredEventType string) (string, error) {
	if !gjson.ValidBytes(payload) {
		return "", errors.New("OpenAI Responses returned malformed JSON data")
	}
	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return "", errors.New("OpenAI Responses returned a non-object provider event")
	}
	if err := validateOpenAIResponsesNoDuplicateKnownFields(payload, openAIResponsesJSONEvent); err != nil {
		return "", err
	}
	declaredEventType = strings.TrimSpace(declaredEventType)
	eventTypeValue := root.Get("type")
	if eventTypeValue.Exists() && eventTypeValue.Type != gjson.String {
		return "", errors.New("OpenAI Responses provider event type was not a string")
	}
	eventType := strings.TrimSpace(eventTypeValue.String())
	if eventType == "" && declaredEventType == "" {
		return "", errors.New("OpenAI Responses provider event omitted type")
	}
	if declaredEventType != "" && eventType != "" && declaredEventType != eventType {
		return "", fmt.Errorf("OpenAI Responses event type mismatch: event=%s payload=%s", declaredEventType, eventType)
	}
	if eventType == "" {
		eventType = declaredEventType
	}
	if eventType == "" {
		return "", errors.New("OpenAI Responses provider event omitted type")
	}
	for _, field := range []string{"output_index", "content_index", "summary_index", "sequence_number"} {
		if value := root.Get(field); value.Exists() && !nonNegativeIntegerGJSON(value) {
			return "", fmt.Errorf("OpenAI Responses provider event %s was not a non-negative integer", field)
		}
	}
	if itemID := root.Get("item_id"); itemID.Exists() && itemID.Type != gjson.String {
		return "", errors.New("OpenAI Responses provider event item_id was not a string")
	}
	for _, field := range []string{"call_id", "name"} {
		if value := root.Get(field); value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIResponsesRetainedStringBytes) {
			return "", fmt.Errorf("OpenAI Responses provider event %s was not a bounded string", field)
		}
	}
	if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null && !validOpenAIResponsesUsageShape(usage) {
		return "", errors.New("OpenAI Responses provider event usage was invalid")
	}
	if response := root.Get("response"); response.Exists() {
		validResponseFields := validOpenAIResponsesKnownObjectFields(response)
		if eventType == "response.failed" || eventType == "response.incomplete" || eventType == "response.cancelled" || eventType == "response.canceled" {
			validResponseFields = validOpenAIResponsesFailureObjectFields(response)
		}
		if !validResponseFields {
			return "", errors.New("OpenAI Responses provider event response fields were invalid")
		}
	}
	if toolUsage := root.Get("tool_usage"); toolUsage.Exists() && !validOpenAIHostedToolUsageShape(toolUsage) {
		return "", errors.New("OpenAI Responses provider event tool usage was invalid")
	}
	if eventError := root.Get("error"); eventError.Exists() && eventError.Type != gjson.Null && !validOpenAIResponsesErrorObject(eventError) {
		return "", errors.New("OpenAI Responses provider event error was invalid")
	}
	rootUsage, nestedUsage := root.Get("usage"), root.Get("response.usage")
	if rootUsage.IsObject() && nestedUsage.IsObject() && canonicalOpenAIUsageShape(rootUsage) != canonicalOpenAIUsageShape(nestedUsage) {
		return "", errors.New("OpenAI Responses root and response usage disagreed")
	}
	rootToolUsage, nestedToolUsage := root.Get("tool_usage"), root.Get("response.tool_usage")
	if rootToolUsage.IsObject() && nestedToolUsage.IsObject() && canonicalOpenAIHostedToolUsage(rootToolUsage) != canonicalOpenAIHostedToolUsage(nestedToolUsage) {
		return "", errors.New("OpenAI Responses root and response tool usage disagreed")
	}

	requireString := func(path string) bool { return root.Get(path).Type == gjson.String }
	switch eventType {
	case "response.completed", "response.done":
		if !validOpenAIResponsesObject(root.Get("response")) {
			return "", errors.New("OpenAI terminal event omitted a valid response object")
		}
		if eventError := root.Get("error"); eventError.Exists() && eventError.Type != gjson.Null {
			return "", errors.New("OpenAI completed event contained a contradictory error")
		}
	case "response.created", "response.in_progress":
		if !root.Get("response").IsObject() {
			return "", fmt.Errorf("%s omitted response object", eventType)
		}
	case "response.output_item.added", "response.output_item.done":
		if !validOpenAIResponsesOutputItem(root.Get("item")) {
			return "", fmt.Errorf("%s omitted a valid output item", eventType)
		}
	case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
		if !requireString("delta") {
			return "", fmt.Errorf("%s omitted string delta", eventType)
		}
	case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
		if !requireString("text") {
			return "", fmt.Errorf("%s omitted string text", eventType)
		}
	case "response.refusal.done":
		if !requireString("refusal") {
			return "", errors.New("response.refusal.done omitted string refusal")
		}
	case "response.function_call_arguments.delta":
		if !requireString("delta") {
			return "", errors.New("response.function_call_arguments.delta omitted string delta")
		}
	case "response.function_call_arguments.done":
		if !requireString("arguments") {
			return "", errors.New("response.function_call_arguments.done omitted string arguments")
		}
	case "response.custom_tool_call_input.delta":
		if !requireString("delta") {
			return "", errors.New("response.custom_tool_call_input.delta omitted string delta")
		}
	case "response.custom_tool_call_input.done":
		if !requireString("input") {
			return "", errors.New("response.custom_tool_call_input.done omitted string input")
		}
	case "response.content_part.added", "response.content_part.done":
		if !validOpenAIResponsesMessageContentPart(root.Get("part")) {
			return "", fmt.Errorf("%s omitted a valid part", eventType)
		}
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if !validOpenAIResponsesReasoningPart(root.Get("part")) {
			return "", fmt.Errorf("%s omitted a valid reasoning summary part", eventType)
		}
	case "response.failed":
		if !root.Get("response").IsObject() && !root.Get("error").IsObject() {
			return "", errors.New("response.failed omitted response/error object")
		}
		if status := strings.TrimSpace(root.Get("response.status").String()); status != "" && status != "failed" {
			return "", errors.New("response.failed contained a contradictory response status")
		}
	case "response.incomplete", "response.cancelled", "response.canceled":
		if !root.Get("response").IsObject() {
			return "", fmt.Errorf("%s omitted response object", eventType)
		}
		status := strings.TrimSpace(root.Get("response.status").String())
		validStatus := status == ""
		if eventType == "response.incomplete" {
			validStatus = validStatus || status == "incomplete"
		} else {
			validStatus = validStatus || status == "cancelled" || status == "canceled"
		}
		if !validStatus {
			return "", fmt.Errorf("%s contained a contradictory response status", eventType)
		}
	case "error":
		message := root.Get("message")
		if message.Exists() && message.Type != gjson.String {
			return "", errors.New("OpenAI error event message was not a string")
		}
		if !root.Get("error").IsObject() && strings.TrimSpace(message.String()) == "" {
			return "", errors.New("OpenAI error event omitted error object")
		}
	}
	return eventType, nil
}

func validOpenAIResponsesFailureObjectFields(response gjson.Result) bool {
	// Failed/incomplete envelopes are sanitized before they reach an already
	// committed client. Keep known metadata strict and output count-bounded,
	// but do not impose successful assistant-item requirements on diagnostic
	// output that will be removed by the sanitizer.
	if !validOpenAIResponsesKnownObjectFieldsWithOutput(response, false) {
		return false
	}
	if output := response.Get("output"); output.Exists() && output.Raw != "null" &&
		!validBoundedOpenAIResponsesArray(output, validOpenAIResponsesFailureOutputItem) {
		return false
	}
	return true
}

func validOpenAIResponsesFailureOutputItem(item gjson.Result) bool {
	if !item.IsObject() {
		return false
	}
	itemType := item.Get("type")
	if itemType.Type != gjson.String || strings.TrimSpace(itemType.String()) == "" || len(itemType.String()) > maxOpenAIResponsesRetainedStringBytes {
		return false
	}
	for _, field := range []string{"id", "status"} {
		if value := item.Get(field); value.Exists() && (value.Type != gjson.String || len(value.String()) > maxOpenAIResponsesRetainedStringBytes) {
			return false
		}
	}
	for _, field := range []string{"content", "summary"} {
		if parts := item.Get(field); parts.Exists() && !validBoundedOpenAIResponsesArray(parts, func(part gjson.Result) bool { return part.IsObject() }) {
			return false
		}
	}
	return true
}

func validOpenAIResponsesJSON(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	if err := validateOpenAIResponsesNoDuplicateKnownFields(body, openAIResponsesJSONResponse); err != nil {
		return false
	}
	return validOpenAIResponsesObject(gjson.ParseBytes(body))
}

func normalizeCompletedImageGenerationStatus(data []byte) ([]byte, bool) {
	if len(data) == 0 || !json.Valid(data) {
		return data, false
	}
	root := gjson.ParseBytes(data)

	type replacement struct {
		start int
		end   int
	}
	var replacements []replacement
	collectReplacement := func(item gjson.Result) {
		if !item.Exists() || !item.IsObject() ||
			strings.TrimSpace(item.Get("type").String()) != "image_generation_call" {
			return
		}
		status := item.Get("status")
		switch strings.TrimSpace(status.String()) {
		case "generating", "in_progress":
			if strings.TrimSpace(item.Get("result").String()) == "" || status.Index < 0 || status.Raw == "" {
				return
			}
			end := status.Index + len(status.Raw)
			if end > len(data) {
				return
			}
			replacements = append(replacements, replacement{start: status.Index, end: end})
		default:
			return
		}
	}

	eventType := strings.TrimSpace(root.Get("type").String())
	switch eventType {
	case "response.output_item.done":
		collectReplacement(root.Get("item"))
	case "response.completed", "response.done":
		output := root.Get("response.output")
		if !output.Exists() || !output.IsArray() {
			return data, false
		}
		output.ForEach(func(_, item gjson.Result) bool {
			collectReplacement(item)
			return true
		})
	default:
		return data, false
	}
	if len(replacements) == 0 {
		return data, false
	}
	updated := make([]byte, 0, len(data))
	cursor := 0
	for _, span := range replacements {
		if span.start < cursor || span.end < span.start || span.end > len(data) {
			return data, false
		}
		updated = append(updated, data[cursor:span.start]...)
		updated = append(updated, `"completed"`...)
		cursor = span.end
	}
	updated = append(updated, data[cursor:]...)
	return updated, true
}

func normalizeResponsesStreamingTerminalOutput(data []byte, acc *apicompat.BufferedResponseAccumulator, imageOutputs []json.RawMessage) ([]byte, bool) {
	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return data, false
	}

	output := gjson.GetBytes(data, "response.output")
	hasAccumulatedOutput := (acc != nil && acc.HasContent()) || len(imageOutputs) > 0
	if output.Exists() && output.IsArray() {
		if gjsonCollectionHasValues(output) || !hasAccumulatedOutput {
			return data, false
		}
	}

	outputJSON := []byte("[]")
	if reconstructed, ok := buildResponsesOutputJSON(acc, imageOutputs); ok {
		outputJSON = reconstructed
	}
	updated, err := sjson.SetRawBytes(data, "response.output", outputJSON)
	if err != nil {
		return data, false
	}
	return updated, true
}

func responsesStreamEventMayContributeToOutput(eventType string) bool {
	switch eventType {
	case "response.output_text.delta",
		"response.output_text.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done",
		"response.refusal.delta",
		"response.refusal.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.reasoning_text.delta",
		"response.reasoning_text.done":
		return true
	default:
		return false
	}
}

// collectRawResponsesOutputItemsFromSSE 按到达顺序收集 SSE 流中
// response.output_item.done 携带的原始 item。除已产生结果但仍停留在进行中
// 的图片状态外，item 以 raw JSON 逐字节保留，
// 避免经窄结构体重建时丢弃 encrypted_content/summary/opaque 等 compact
// 专属或未来新增字段（#3777 问题 2）。若整条流没有任何 done 事件，退回
// 收集 output_item.added 中的 compaction 类 item——compaction 结果没有
// delta 事件，部分上游只在 added 事件中携带完整 item。
func collectRawResponsesOutputItemsFromSSE(bodyText string) ([]byte, bool) {
	var items []json.RawMessage
	seen := make(map[string]struct{})
	hasCompactionItem := false
	appendItem := func(item gjson.Result) {
		if !item.Exists() || !item.IsObject() {
			return
		}
		key := strings.TrimSpace(item.Get("id").String())
		if key == "" {
			key = item.Raw
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if isResponsesCompactionItemType(item.Get("type").String()) {
			hasCompactionItem = true
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	forEachOpenAISSEDataPayload(bodyText, func(data []byte) {
		if normalized, changed := normalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.done" {
			return
		}
		appendItem(gjson.GetBytes(data, "item"))
	})
	// done 事件未携带 compaction item 时再看 added：覆盖"其他 item 有 done、
	// compaction 只在 added 中"的混合形态；done 已含 compaction 时跳过，
	// 避免同一 item 在无 id 可去重时被收集两份（Codex 要求恰好一个）。
	if !hasCompactionItem {
		forEachOpenAISSEDataPayload(bodyText, func(data []byte) {
			if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.added" {
				return
			}
			item := gjson.GetBytes(data, "item")
			if !isResponsesCompactionItemType(item.Get("type").String()) {
				return
			}
			appendItem(item)
		})
	}
	if len(items) == 0 {
		return nil, false
	}
	outputJSON, err := json.Marshal(items)
	if err != nil {
		return nil, false
	}
	return outputJSON, true
}

// isResponsesCompactionItemType reports whether the item type is the Codex
// remote-compact result item ("compaction", upstream alias "compaction_summary").
func isResponsesCompactionItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

// supplementCompactionItemFromSSE 保证 compact 请求的终态 output 携带
// compaction item：终态 output 非空但缺失 compaction、而原始事件流的
// output_item.done（或 added）中存在时（上游不一致形态），以 raw JSON 补入。
// Codex remote compact v2 只从 output_item.done 收集 item 且要求恰好一个
// compaction item——纯流式透传（v0.1.146）下客户端直接读事件流天然拿得到，
// SSE→JSON 提取链路必须给出等价结果。非 compact 请求原样返回。
func supplementCompactionItemFromSSE(c *gin.Context, finalResponse []byte, bodyText string) []byte {
	if !isOpenAIResponsesCompactPath(c) {
		return finalResponse
	}
	if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
		// 空 output 由 reconstructResponseOutputFromSSE 整体修补，不在此处理。
		return finalResponse
	}
	if responsesOutputHasCompactionItem(finalResponse) {
		return finalResponse
	}
	item, found := findRawCompactionItemFromSSE(bodyText)
	if !found {
		return finalResponse
	}
	patched, err := sjson.SetRawBytes(finalResponse, "output.-1", item)
	if err != nil {
		return finalResponse
	}
	return patched
}

// responsesOutputHasCompactionItem reports whether the response JSON already
// carries a compaction item in its output array.
func responsesOutputHasCompactionItem(response []byte) bool {
	for _, item := range gjson.GetBytes(response, "output").Array() {
		if isResponsesCompactionItemType(item.Get("type").String()) {
			return true
		}
	}
	return false
}

// findRawCompactionItemFromSSE 从原始 SSE 事件流中提取第一个 compaction 类
// item 的 raw JSON：output_item.done 优先，output_item.added 兜底。
func findRawCompactionItemFromSSE(bodyText string) (json.RawMessage, bool) {
	var found json.RawMessage
	pick := func(eventType string) {
		forEachOpenAISSEDataPayload(bodyText, func(data []byte) {
			if found != nil {
				return
			}
			if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != eventType {
				return
			}
			item := gjson.GetBytes(data, "item")
			if !item.IsObject() || !isResponsesCompactionItemType(item.Get("type").String()) {
				return
			}
			found = json.RawMessage(item.Raw)
		})
	}
	pick("response.output_item.done")
	if found == nil {
		pick("response.output_item.added")
	}
	return found, found != nil
}

// reconstructResponseOutputFromSSE scans raw SSE body text and returns a
// JSON-encoded output array for a terminal event whose output is empty.
// Raw output_item.done items are preferred: per the Responses protocol they
// are the authoritative final form of each item. Delta accumulation only
// covers text/function_call/reasoning content and silently drops unknown
// item types such as compaction — Codex remote compact v2 then fails with
// "expected exactly one compaction output item, got 0" (#3887).
// Returns (nil, false) if nothing could be reconstructed.
func reconstructResponseOutputFromSSE(bodyText string) ([]byte, bool, error) {
	if outputJSON, ok := collectRawResponsesOutputItemsFromSSE(bodyText); ok {
		return outputJSON, true, nil
	}
	acc := apicompat.NewBufferedResponseAccumulator()
	imageOutputs := make([]json.RawMessage, 0, 1)
	seenImages := make(map[string]struct{})
	var retentionErr error
	forEachOpenAISSEDataPayload(bodyText, func(data []byte) {
		if retentionErr != nil {
			return
		}
		if imageOutput, ok := extractImageGenerationOutputFromSSEData(data, seenImages); ok {
			if err := acc.RetainExternalOutput(len(imageOutput), 1); err != nil {
				retentionErr = err
				return
			}
			imageOutputs = append(imageOutputs, imageOutput)
		}
		eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
		if responsesStreamEventMayContributeToOutput(eventType) {
			var event apicompat.ResponsesStreamEvent
			if err := json.Unmarshal(data, &event); err == nil {
				retentionErr = acc.ProcessEvent(&event)
			}
		}
	})
	if retentionErr != nil {
		return nil, false, retentionErr
	}
	output, ok := buildResponsesOutputJSON(acc, imageOutputs)
	return output, ok, nil
}

func buildResponsesOutputJSON(acc *apicompat.BufferedResponseAccumulator, imageOutputs []json.RawMessage) ([]byte, bool) {
	if (acc == nil || !acc.HasContent()) && len(imageOutputs) == 0 {
		return nil, false
	}
	var output []json.RawMessage
	if acc != nil && acc.HasContent() {
		outputJSON, err := json.Marshal(acc.BuildOutput())
		if err == nil {
			_ = json.Unmarshal(outputJSON, &output)
		}
	}
	output = append(output, imageOutputs...)
	if len(output) == 0 {
		return nil, false
	}

	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, false
	}
	return outputJSON, true
}

func extractImageGenerationOutputFromSSEData(data []byte, seen map[string]struct{}) (json.RawMessage, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return nil, false
	}
	if gjson.GetBytes(data, "type").String() != "response.output_item.done" {
		return nil, false
	}
	item := gjson.GetBytes(data, "item")
	if !item.Exists() || !item.IsObject() || item.Get("type").String() != "image_generation_call" {
		return nil, false
	}
	if strings.TrimSpace(item.Get("result").String()) == "" {
		return nil, false
	}
	key := strings.TrimSpace(item.Get("id").String())
	if len(key) > maxOpenAIResponsesRetainedStringBytes {
		key = ""
	}
	if key == "" && seen != nil {
		hasher := sha256.New()
		_, _ = io.WriteString(hasher, item.Get("result").String())
		key = fmt.Sprintf("sha256:%x", hasher.Sum(nil))
	}
	if key != "" && seen != nil {
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
	}
	return json.RawMessage(item.Raw), true
}

func (s *OpenAIGatewayService) parseSSEUsageFromBody(body string) *OpenAIUsage {
	usage := &OpenAIUsage{}
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		s.parseSSEUsageBytes(data, usage)
	})
	return usage
}

func (s *OpenAIGatewayService) replaceModelInSSEBody(body, fromModel, toModel string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if _, ok := extractOpenAISSEDataLine(line); !ok {
			continue
		}
		lines[i] = s.replaceModelInSSELine(line, fromModel, toModel)
	}
	return strings.Join(lines, "\n")
}
