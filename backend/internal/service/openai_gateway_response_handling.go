package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// openaiStreamingResult streaming response result
type openaiStreamingResult struct {
	usage            *OpenAIUsage
	firstTokenMs     *int
	clientDisconnect bool
	terminalObserved bool
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
	ttftMode := s.openAITTFTMode(ctx)
	firstOutputProgressObserved := false
	bufferedWriter := bufio.NewWriterSize(w, 4*1024)
	firstOutputStage := newDefaultOpenAIFirstOutputStage()
	defer func() {
		if err := firstOutputStage.Close(); err != nil {
			logger.LegacyPrintf("service.openai_gateway", "OpenAI first-output staging cleanup failed: account=%d model=%s error=%v", account.ID, originalModel, err)
		}
	}()
	writePendingString := func(value string) (int, error) {
		if firstOutputStage != nil && !firstOutputStage.closed {
			return firstOutputStage.WriteString(value)
		}
		return bufferedWriter.WriteString(value)
	}
	pendingBytes := func() int64 {
		if firstOutputStage != nil && !firstOutputStage.closed {
			return firstOutputStage.Buffered()
		}
		return int64(bufferedWriter.Buffered())
	}
	flushBuffered := func() error {
		if firstOutputStage != nil && !firstOutputStage.closed {
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
	sawFailedEvent := false
	sawSuccessfulTerminalEvent := false
	sawBareError := false
	sawResponseFailed := false
	terminalEventType := ""
	responsesSemanticOutputSeen := false
	capacityFailoverSuppressedLogged := false
	failedMessage := ""
	clientOutputStarted := false
	codexFailureTerminal := account != nil && account.IsOpenAIOAuthLike()
	upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	var streamEarlyErr error
	terminalFailurePending := false
	failureDelivered := false
	suppressCurrentEvent := false
	var bareErrorPayload []byte
	bareErrorAccountSideEffectsPending := false
	pendingSSEEventType := ""
	eventInProgress := false
	eventStartsClientOutput := false
	eventStartsTTFTOutput := false
	eventShouldFlush := false
	pendingProviderEventType := ""
	handlePendingWriteError := func(err error) {
		if firstOutputStage != nil && !firstOutputStage.closed {
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
		completedProgressEvent := eventStartsClientOutput
		completedTTFTEvent := eventStartsTTFTOutput
		shouldFlush := eventShouldFlush || (queueDrained && clientOutputStarted)
		eventInProgress = false
		if !clientDisconnected {
			if completedProgressEvent {
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
		if completedProgressEvent && !firstOutputProgressObserved {
			firstOutputScanGuard.Store(false)
			firstOutputProgressObserved = true
			stopFirstOutputTimer()
		}
		if completedTTFTEvent && firstTokenMs == nil {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		eventStartsClientOutput = false
		eventStartsTTFTOutput = false
		eventShouldFlush = false
	}
	sendErrorEvent := func(reason string) {
		if errorEventSent || clientDisconnected || failureDelivered {
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
	streamDoneItems := newResponsesStreamOutputItems()
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
			clientDisconnect: clientDisconnected,
			terminalObserved: sawSuccessfulTerminalEvent,
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
		if codexFailureTerminal && sawBareError && !sawResponseFailed && bareErrorAccountSideEffectsPending {
			s.handleOpenAIStreamTerminalAccountSideEffects(c, account, bareErrorPayload, failedMessage, resp.Header, mappedModel)
			bareErrorAccountSideEffectsPending = false
		}
		if codexFailureTerminal && sawBareError && !sawResponseFailed && !clientDisconnected {
			applyAttemptResponseHeaders()
			if _, err := writePendingString(buildOpenAIResponseFailedSSE(responseID, originalModel, bareErrorPayload, failedMessage)); err != nil {
				handlePendingWriteError(err)
			} else {
				failureDelivered = true
			}
		}
		if sawTerminalEvent && !sawFailedEvent {
			s.clearOpenAIProxyStreamDisconnect(account)
		}
		// Upstream providers may omit a terminal marker; preserve the bytes and usage.
		flushPending("Client disconnected during final flush, returning collected usage")
		if !sawTerminalEvent {
			return resultWithUsage(), nil
		}
		if sawFailedEvent {
			return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
		}
		logOpenAISuccessMissingUsage(ctx, c, account, resp, usage, terminalEventType, clientDisconnected)
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
			pendingSSEEventType = declared
			declared = strings.TrimSpace(declared)
			suppressCurrentEvent = codexFailureTerminal && (declared == "error" || (sawBareError && !sawResponseFailed && declared != "response.failed"))
			return
		}
		if strings.TrimSpace(line) == "" && pendingProviderEventType != "" {
			// An event-only frame has no data payload. Its header must not leak
			// into the next independent SSE event or downstream wire.
			pendingProviderEventType = ""
			pendingSSEEventType = ""
			return
		}
		// Extract data from SSE line (supports both "data: " and "data:" formats)
		if data, ok := extractOpenAISSEDataLine(line); ok {
			dataBytes := []byte(data)
			declaredEventType := pendingProviderEventType
			eventType := effectiveOpenAISSEEventType(dataBytes, pendingSSEEventType)
			pendingProviderEventType = ""
			pendingSSEEventType = ""
			if codexFailureTerminal && sawBareError && !sawResponseFailed &&
				(eventType == "response.completed" || eventType == "response.done") {
				// A later successful terminal is authoritative over a pending bare
				// error. Keep its usage and terminal visible to the client.
				sawBareError = false
				sawFailedEvent = false
				terminalFailurePending = false
				suppressCurrentEvent = false
				bareErrorPayload = nil
				bareErrorAccountSideEffectsPending = false
				failedMessage = ""
			}
			if codexFailureTerminal && sawBareError && !sawResponseFailed && eventType != "response.failed" {
				suppressCurrentEvent = true
			}
			observer.ObserveOpenAI(dataBytes, eventType)
			// 初始上游 data 的 type 只解析一次：原始值保持终止事件的精确匹配，规范化值供后续分支复用。
			if openAIStreamEventIsTerminalWithType(data, eventType) {
				sawTerminalEvent = true
				terminalEventType = eventType
				if strings.TrimSpace(data) == "[DONE]" {
					terminalEventType = "[DONE]"
				}
			}
			if responseID == "" {
				responseID = extractOpenAIResponseIDFromJSONBytes(dataBytes)
			}
			forceFlushFailedEvent := false
			if !capacityFailoverSuppressedLogged && account != nil && account.Platform == PlatformOpenAI &&
				(eventType == "error" || eventType == "response.failed") &&
				openAIStreamClientOutputStarted(c, clientOutputStarted) &&
				isOpenAIUpstreamCapacityShedEvent(dataBytes) {
				logOpenAICapacityFailoverSuppressed(ctx, account, "native_sse", upstreamRequestID, eventType)
				capacityFailoverSuppressedLogged = true
			}
			terminalFailed, terminalFailureStatus := openAIResponsesTerminalFailureStatus(dataBytes, eventType)
			switch eventType {
			case "response.completed", "response.done":
				if terminalFailed {
					sawFailedEvent = true
				} else {
					sawSuccessfulTerminalEvent = true
				}
			case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
				sawTerminalEvent = true
				sawFailedEvent = true
			}
			cyberHit := false
			if terminalFailed || eventType == "error" {
				if codexFailureTerminal && eventType == "error" {
					sawBareError = true
					bareErrorPayload = append(bareErrorPayload[:0], dataBytes...)
					suppressCurrentEvent = true
				} else if codexFailureTerminal && eventType == "response.failed" {
					sawResponseFailed = true
				}
				failedMessage = extractOpenAISSEErrorMessage(dataBytes)
				if failedMessage == "" {
					failedMessage = "upstream response ended with status " + terminalFailureStatus
					if terminalFailureStatus == "" {
						failedMessage = "Upstream response failed"
					}
				}
				// response.failed 自带上游已消耗的 usage（input token 通常已扣）；必须先解析
				// 再打 cyber 标记，否则 mark 记到的是解析前的 0，导致流式 cyber 按 0 token 计费
				// 而漏记真实用量。对齐 WS V2 / Chat 流式路径（均先解析 usage 再 Mark）。
				s.parseSSEUsageBytesWithType(dataBytes, eventType, usage)
				if hit, code, msg := detectOpenAICyberPolicy(dataBytes); hit {
					cyberHit = true
					MarkOpsCyberPolicy(c, CyberPolicyMark{
						Code:           code,
						Message:        msg,
						Body:           truncateString(string(dataBytes), 4096),
						UpstreamStatus: http.StatusOK,
						UpstreamInTok:  usage.InputTokens,
						UpstreamOutTok: usage.OutputTokens,
					})
				}
				outputStarted := openAIStreamClientOutputStarted(c, clientOutputStarted)
				if !outputStarted && !cyberHit {
					if compactErr := newOpenAICompactFallbackSignal(c, dataBytes, failedMessage); compactErr != nil {
						sawFailedEvent = true
						streamEarlyErr = compactErr
						return
					}
				}
				if outputStarted && !cyberHit {
					if codexFailureTerminal && eventType == "error" {
						// OpenAI commonly follows a bare error with response.failed.
						// Defer account health updates so the pair is applied once.
						bareErrorAccountSideEffectsPending = true
					} else {
						s.handleOpenAIStreamTerminalAccountSideEffects(c, account, dataBytes, failedMessage, resp.Header, mappedModel)
						bareErrorAccountSideEffectsPending = false
					}
				}
				if !outputStarted {
					shouldFailover := false
					if !cyberHit {
						if eventType == "error" {
							shouldFailover = openAIStreamErrorEventShouldFailover(dataBytes, failedMessage)
						} else {
							shouldFailover = openAIStreamFailedEventShouldFailover(dataBytes, failedMessage)
						}
					}
					if shouldFailover {
						sawFailedEvent = true
						streamEarlyErr = s.newOpenAIStreamFailoverErrorWithModel(c, account, false, upstreamRequestID, dataBytes, failedMessage, mappedModel, resp.Header)
						return
					}
					if !cyberHit && !sawBareError {
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
					}
				}
				if codexFailureTerminal && eventType != "response.failed" {
					dataBytes = []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":` + strconv.Quote(failedMessage) + `}}}`)
					data = string(dataBytes)
					line = "data: " + data
					eventType = "response.failed"
				}
				forceFlushFailedEvent = true
				sawFailedEvent = true
				terminalFailurePending = !codexFailureTerminal || eventType == "response.failed"
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
				eventType = effectiveOpenAISSEEventType(dataBytes, eventType)
			}
			if imageOutput, ok := extractImageGenerationOutputFromSSEData(dataBytes, streamSeenImages); ok {
				if retainErr := streamOutputAccumulator.RetainExternalOutput(len(imageOutput), 1); retainErr != nil {
					setOutputRetentionError(retainErr, dataBytes)
					return
				}
				streamImageOutputs = append(streamImageOutputs, imageOutput)
			}
			streamDoneItems.Observe(dataBytes)
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
			if normalizedData, normalized := normalizeResponsesStreamingTerminalOutput(dataBytes, streamOutputAccumulator, streamDoneItems, streamImageOutputs); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				line = "data: " + data
				eventType = effectiveOpenAISSEEventType(dataBytes, eventType)
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
			restoredData = restoreCodexToolNamesFromSSEContext(c, restoredData, eventType)
			if !bytes.Equal(restoredData, dataBytes) {
				dataBytes = restoredData
				data = string(restoredData)
				line = "data: " + data
				eventType = effectiveOpenAISSEEventType(dataBytes, eventType)
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
			startsClientOutput := forceFlushFailedEvent || openAIStreamDataStartsClientOutput(data, eventType)
			startsVisibleOutput := openAIStreamDataStartsVisibleOutput(data, eventType)
			captureWebChatStreamString(ctx, line+"\n")
			eventStartsClientOutput = eventStartsClientOutput || startsClientOutput
			startsTTFTOutput := openAIStreamDataStartsTTFT(data, eventType, forceFlushFailedEvent, ttftMode)
			eventStartsTTFTOutput = eventStartsTTFTOutput || startsTTFTOutput
			if startsClientOutput {
				firstOutputScanGuard.Store(false)
			}
			if startsClientOutput && !openAIStreamEventTypeIsTerminal(eventType) {
				responsesSemanticOutputSeen = true
			}
			// OpenAI Responses streams that terminate with an empty
			// response.completed (no output, no usage, no error, nothing sent
			// to the client) are silent upstream refusals: fail over instead of
			// recording a successful 0/0 usage turn (issue #5009).
			if account != nil && account.Platform == PlatformOpenAI &&
				(eventType == "response.completed" || eventType == "response.done") &&
				!sawFailedEvent && !responsesSemanticOutputSeen && !clientOutputStarted &&
				openAIResponsesCompletedEventIsEmpty(dataBytes, usage) {
				sawTerminalEvent = true
				streamEarlyErr = newOpenAIResponsesEmptyCompletedFailoverError(c, account, upstreamRequestID)
				return
			}

			// 写入客户端（客户端断开后继续 drain 上游）
			if !clientDisconnected && !failureDelivered && !suppressCurrentEvent {
				shouldFlush := queueDrained && (clientOutputStarted || startsClientOutput)
				if firstTokenMs == nil && startsVisibleOutput {
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

			// Record first token time
			if !guardFirstOutput && firstTokenMs == nil && startsTTFTOutput {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
				stopFirstOutputTimer()
			}
			s.parseSSEUsageBytesWithType(dataBytes, eventType, usage)
			return
		}

		// A blank line dispatches an event from the attempt-local stage. Staging is
		// unconditional; the configured timeout only controls its timer.
		if line == "" {
			pendingSSEEventType = ""
			if suppressCurrentEvent {
				suppressCurrentEvent = false
				terminalFailurePending = false
				eventInProgress = false
				eventStartsClientOutput = false
				eventStartsTTFTOutput = false
				eventShouldFlush = false
				return
			}
			if failureDelivered {
				terminalFailurePending = false
				eventInProgress = false
				eventStartsClientOutput = false
				eventStartsTTFTOutput = false
				eventShouldFlush = false
				return
			}
			if !clientDisconnected {
				if _, err := writePendingString("\n"); err != nil {
					handlePendingWriteError(err)
				}
			}
			if streamEarlyErr == nil {
				completeGuardedEvent(queueDrained)
			}
			if terminalFailurePending && streamEarlyErr == nil {
				terminalFailurePending = false
				failureDelivered = true
			}
			return
		}
		// A keepalive or queue-drain flush must never split an open SSE event.
		shouldFlush := false
		if line == "" {
			pendingSSEEventType = ""
			if suppressCurrentEvent {
				suppressCurrentEvent = false
				terminalFailurePending = false
				eventInProgress = false
				eventShouldFlush = false
				return
			}
			shouldFlush = eventShouldFlush || (queueDrained && clientOutputStarted)
			eventShouldFlush = false
			if failureDelivered {
				terminalFailurePending = false
			}
		}
		if !clientDisconnected && !failureDelivered && !suppressCurrentEvent {
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
			if line == "" && terminalFailurePending && streamEarlyErr == nil {
				terminalFailurePending = false
				failureDelivered = true
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
			if failureDelivered {
				return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
			}
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if codexFailureTerminal && sawBareError && !sawResponseFailed {
				_ = resp.Body.Close()
				return finalizeStream()
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
			if firstOutputProgressObserved {
				stopFirstOutputTimer()
				continue
			}
			if codexFailureTerminal && sawBareError && !sawResponseFailed && len(events) == 0 {
				markCaptureResponseTruncated(resp.Body)
				closeCaptureResponseUnderlying(resp)
				return finalizeStream()
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
			if clientDisconnected || failureDelivered {
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
	if strings.TrimSpace(gjson.Get(payload, "type").String()) != "" {
		return payload
	}
	patched, err := sjson.Set(payload, "type", eventType)
	if err != nil {
		return payload
	}
	return patched
}

func effectiveOpenAISSEEventType(payload []byte, eventType string) string {
	if payloadType := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); payloadType != "" {
		return payloadType
	}
	return strings.TrimSpace(eventType)
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
	s.parseSSEUsageBytesWithType(data, "", usage)
}

func (s *OpenAIGatewayService) parseSSEUsageBytesWithType(data []byte, eventType string, usage *OpenAIUsage) {
	if usage == nil || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return
	}
	// Usage is absent from nearly every delta event. Avoid full JSON validation
	// and four gjson path scans on that hot path while retaining progressive
	// usage from compatible upstreams on any event type.
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	parsedUsage, ok := extractOpenAIUsageFromJSONBytes(data)
	if !ok {
		return
	}
	if openAIStreamEventTypeIsTerminal(effectiveOpenAISSEEventType(data, eventType)) {
		if !openAIUsageHasTokens(&parsedUsage) && openAIUsageHasTokens(usage) {
			return
		}
		*usage = parsedUsage
		return
	}
	mergeOpenAIUsageNonZero(usage, parsedUsage)
}

// Compatible Responses upstreams may report usage before the terminal event.
// Retain those non-zero fields as a fallback. A terminal usage with any billed
// tokens is authoritative as a whole; an all-zero terminal does not erase a
// non-zero progressive observation from the same turn.
func mergeOpenAIUsageNonZero(dst *OpenAIUsage, src OpenAIUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.ImageInputTokens > 0 {
		dst.ImageInputTokens = src.ImageInputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

func openAIUsageHasTokens(usage *OpenAIUsage) bool {
	return usage != nil && (usage.InputTokens > 0 || usage.ImageInputTokens > 0 ||
		usage.OutputTokens > 0 || usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 || usage.ImageOutputTokens > 0)
}

const openAIMissingUsageLogInterval = time.Minute

type openAIMissingUsageLogSampler struct {
	total      atomic.Uint64
	suppressed atomic.Uint64
	lastLog    atomic.Int64
}

var openAIMissingUsageSampler openAIMissingUsageLogSampler

func (s *openAIMissingUsageLogSampler) sample(now time.Time) (logNow bool, total uint64, suppressed uint64) {
	total = s.total.Add(1)
	nowNanos := now.UnixNano()
	for {
		last := s.lastLog.Load()
		if last != 0 && nowNanos-last < int64(openAIMissingUsageLogInterval) {
			s.suppressed.Add(1)
			return false, total, 0
		}
		if s.lastLog.CompareAndSwap(last, nowNanos) {
			return true, total, s.suppressed.Swap(0)
		}
	}
}

func logOpenAISuccessMissingUsage(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, usage *OpenAIUsage, terminalEvent string, clientDisconnected bool) {
	if resp == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || openAIUsageHasTokens(usage) {
		return
	}
	terminalEvent = strings.TrimSpace(terminalEvent)
	if terminalEvent != "response.completed" && terminalEvent != "response.done" && terminalEvent != "[DONE]" && terminalEvent != "json" {
		return
	}
	logNow, total, suppressed := openAIMissingUsageSampler.sample(time.Now())
	if !logNow {
		return
	}
	accountID := int64(0)
	accountType := ""
	if account != nil {
		accountID = account.ID
		accountType = string(account.Type)
	}
	inboundEndpoint := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		inboundEndpoint = c.Request.URL.Path
	}
	logger.FromContext(ctx).With(
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_type", accountType),
		zap.String("inbound_endpoint", inboundEndpoint),
		zap.String("upstream_request_id", strings.TrimSpace(resp.Header.Get("x-request-id"))),
		zap.Int("upstream_status_code", resp.StatusCode),
		zap.String("terminal_event", terminalEvent),
		zap.Bool("client_disconnected", clientDisconnected),
		zap.Uint64("missing_usage_total", total),
		zap.Uint64("suppressed_since_last", suppressed),
	).Warn("openai_usage.success_missing_usage")
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

// openAIResponsesCompletedEventIsEmpty reports whether a response.completed /
// response.done event carries no accumulated usage, error, or output items.
func openAIResponsesCompletedEventIsEmpty(data []byte, usage *OpenAIUsage) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	if usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.ImageInputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0) {
		return false
	}
	if gjson.GetBytes(data, "usage").Exists() || gjson.GetBytes(data, "response.usage").Exists() {
		return false
	}
	if gjson.GetBytes(data, "error").Exists() || gjson.GetBytes(data, "response.error").Exists() {
		return false
	}
	if output := gjson.GetBytes(data, "response.output"); output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	return true
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

const openAIHTTPResponseOwnerContextKey = "openai_http_response_owner"

type openAIHTTPResponseOwner struct {
	userID   int64
	apiKeyID int64
}

// SetOpenAIHTTPResponseOwner marks the authenticated downstream owner whose
// successful Responses IDs may be used for later HTTP continuations.
func SetOpenAIHTTPResponseOwner(c *gin.Context, userID, apiKeyID int64) {
	if c == nil || userID <= 0 || apiKeyID <= 0 {
		return
	}
	c.Set(openAIHTTPResponseOwnerContextKey, openAIHTTPResponseOwner{userID: userID, apiKeyID: apiKeyID})
}

// ValidateOpenAIHTTPResponseOwner authorizes a continuation by downstream
// tenant. API key identity is retained in the binding, while keys owned by the
// same user remain interoperable.
func (s *OpenAIGatewayService) ValidateOpenAIHTTPResponseOwner(
	ctx context.Context,
	groupID int64,
	responseID string,
	userID, apiKeyID int64,
) (bool, error) {
	if s == nil || strings.TrimSpace(responseID) == "" || userID <= 0 || apiKeyID <= 0 {
		return false, nil
	}
	ownerUserID, ownerAPIKeyID, found, err := s.getOpenAIWSStateStore().GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil || !found {
		return false, err
	}
	return ownerUserID == userID || (ownerUserID <= 0 && ownerAPIKeyID == apiKeyID), nil
}

// BindOpenAIHTTPResponseOwner records an HTTP continuation owner independently
// from the upstream account selected for that response.
func (s *OpenAIGatewayService) BindOpenAIHTTPResponseOwner(
	ctx context.Context,
	groupID int64,
	responseID string,
	userID, apiKeyID int64,
) error {
	if s == nil {
		return nil
	}
	return s.getOpenAIWSStateStore().BindHTTPResponseOwner(
		ctx, groupID, responseID, userID, apiKeyID, s.openAIWSResponseStickyTTL(),
	)
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
	if rawOwner, ok := c.Get(openAIHTTPResponseOwnerContextKey); ok {
		if owner, ok := rawOwner.(openAIHTTPResponseOwner); ok && owner.userID > 0 && owner.apiKeyID > 0 {
			if err := s.BindOpenAIHTTPResponseOwner(ctx, groupID, responseID, owner.userID, owner.apiKeyID); err != nil {
				logger.L().Warn(
					"openai.http_bind_response_owner_failed",
					zap.Int64("group_id", groupID),
					zap.Int64("account_id", account.ID),
					zap.Int64("user_id", owner.userID),
					zap.Int64("api_key_id", owner.apiKeyID),
					zap.String("response_id", truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen)),
					zap.Error(err),
				)
			}
		}
	}
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
	// xAI reports visible output separately from reasoning_tokens; OpenAI
	// folds reasoning into completion/output. Use total_tokens to tell them apart.
	reasoningTokens := max(int(firstPositiveGJSONInt(
		value.Get("completion_tokens_details.reasoning_tokens"),
		value.Get("output_tokens_details.reasoning_tokens"),
	)), 0)
	if reasoningTokens > 0 {
		outputTokens = xai.IncludeIndependentReasoningTokens(
			inputTokens, outputTokens, value.Get("total_tokens").Int(), int64(reasoningTokens),
		)
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
	if !usageOK {
		if bodyLooksLikeSSE {
			return s.handleSSEToJSONWithWebChatCapture(ctx, resp, c, body, originalModel, mappedModel, stop)
		}
		stop()
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "invalid upstream Responses JSON response")
	}
	usage := &usageValue
	logOpenAISuccessMissingUsage(ctx, c, account, resp, usage, "json", false)

	// Replace model in response if needed
	if originalModel != mappedModel {
		body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = restoreGrokResponsesClientToolPayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore Grok Responses client tool response: %w", err)
	}
	body, err = restoreOpenAIResponsesClientToolPayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI Responses client tool response: %w", err)
	}
	body, err = restoreOpenAIResponsesNamespacePayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI namespace response: %w", err)
	}
	body = restoreCodexToolNamesFromContext(c, body)
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
	return s.handleSSEToJSONWithContext(ctx, resp, c, nil, body, originalModel, mappedModel, stopBeforeWrite...)
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

// handleSSEToJSON is the upstream-compatible entry point used by tests and
// compact callers. The account snapshot is used for Codex turn-state relay.
func (s *OpenAIGatewayService) handleSSEToJSON(resp *http.Response, c *gin.Context, args ...any) (*openaiNonStreamingResult, error) {
	var account *Account
	var body []byte
	var models []string
	for _, arg := range args {
		switch value := arg.(type) {
		case *Account:
			account = value
		case []byte:
			body = value
		case string:
			models = append(models, value)
		}
	}
	var originalModel, mappedModel string
	if len(models) > 0 {
		originalModel = models[0]
	}
	if len(models) > 1 {
		mappedModel = models[1]
	}
	return s.handleSSEToJSONWithOptions(context.Background(), resp, c, account, body, originalModel, mappedModel)
}

func (s *OpenAIGatewayService) handleSSEToJSONWithOptions(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, body []byte, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	return s.handleSSEToJSONWithContext(ctx, resp, c, account, body, originalModel, mappedModel, stopBeforeWrite...)
}

func (s *OpenAIGatewayService) handleSSEToJSONWithContext(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, body []byte, originalModel, mappedModel string, stopBeforeWrite ...func()) (*openaiNonStreamingResult, error) {
	stop := compactStopFunc(stopBeforeWrite...)
	bodyText := string(body)
	terminalType, terminalPayload, terminalOK := extractOpenAISSETerminalEvent(bodyText)
	if terminalOK && (terminalType == "response.failed" || terminalType == "error") {
		msg := extractOpenAISSEErrorMessage(terminalPayload)
		if msg == "" {
			msg = "Upstream compact response failed"
		}
		if compactErr := newOpenAICompactFallbackSignal(c, terminalPayload, msg); compactErr != nil {
			return nil, compactErr
		}
		if failoverErr := s.nonStreamingTerminalFailureFailover(c, resp, account, false, terminalType, terminalPayload, msg, mappedModel); failoverErr != nil {
			return nil, failoverErr
		}
		return nil, s.writeOpenAINonStreamingProtocolError(resp, c, msg)
	}
	finalResponse, ok := extractCodexFinalResponse(bodyText)

	usage := s.parseSSEUsageFromBody(bodyText)
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
		restoredBody, restoreErr = restoreOpenAIResponsesClientToolPayload(c, restoredBody)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI Responses client tool response: %w", restoreErr)
		}
		restoredBody, restoreErr = restoreOpenAIResponsesNamespacePayload(c, restoredBody)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI namespace response: %w", restoreErr)
		}
		restoredBody = restoreCodexToolNamesFromContext(c, restoredBody)
		body = restoredBody
	} else {
		stop()
		return nil, newOpenAIIncompleteChatStreamFailover(resp, "upstream Responses stream ended without a completed response")
	}

	stop()
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	logOpenAISuccessMissingUsage(c.Request.Context(), c, account, resp, usage, terminalType, false)
	s.relayOpenAICodexTurnState(c, account, resp.Header)

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

func extractOpenAISSETerminalEvent(body string) (string, []byte, bool) {
	var terminalType string
	var terminalPayload []byte
	forEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		switch eventType {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
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

func firstExistingOpenAIUsageCounter(usage gjson.Result, paths ...string) gjson.Result {
	for _, path := range paths {
		if value := usage.Get(path); value.Exists() {
			return value
		}
	}
	return gjson.Result{}
}

func buildOpenAIResponseFailedSSE(responseID, model string, source []byte, fallbackMessage string) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	errorType := strings.TrimSpace(gjson.GetBytes(source, "error.type").String())
	if errorType == "" {
		errorType = strings.TrimSpace(gjson.GetBytes(source, "response.error.type").String())
	}
	code := strings.TrimSpace(gjson.GetBytes(source, "error.code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(source, "response.error.code").String())
	}
	if code == "" {
		code = "upstream_error"
	}
	message := extractOpenAISSEErrorMessage(source)
	if message == "" {
		message = strings.TrimSpace(fallbackMessage)
	}
	if message == "" {
		message = "Upstream response failed"
	}
	errorBody := gin.H{"code": code, "message": message}
	if errorType != "" {
		errorBody["type"] = errorType
	}
	response := gin.H{
		"id":     responseID,
		"object": "response",
		"status": "failed",
		"output": []any{},
		"error":  errorBody,
	}
	if model = strings.TrimSpace(model); model != "" {
		response["model"] = model
	}
	payload, err := marshalOpenAIUpstreamJSON(gin.H{
		"type":     "response.failed",
		"response": response,
	})
	if err != nil {
		// All values above are JSON primitives, so this is only a defensive fallback.
		payload = []byte(`{"type":"response.failed","response":{"status":"failed","output":[],"error":{"code":"upstream_error","message":"Upstream response failed"}}}`)
	}
	return "event: response.failed\ndata: " + string(payload) + "\n\n"
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
	forEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		if finalResponse != nil {
			return
		}
		if normalized, changed := normalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		if eventType == "response.done" || eventType == "response.completed" {
			if response := gjson.GetBytes(data, "response"); response.IsObject() && response.Raw != "" {
				finalResponse = []byte(response.Raw)
			}
		}
	})
	if finalResponse != nil {
		return finalResponse, true
	}
	return nil, false
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

// responsesStreamOutputItems remembers the raw item carried by each
// response.output_item.done event, keyed by output_index.
//
// reconstructResponseOutputFromSSE already prefers the raw done items over
// delta accumulation when it rebuilds a buffered response, because the
// accumulator models only "one reasoning, one message, N function calls" and
// therefore cannot preserve item identity, per-item status/phase, ordering, or
// item types it does not know about. The streaming path had no equivalent
// because it never sees the whole body at once; this collector gives it one.
type responsesStreamOutputItems struct {
	items map[int]json.RawMessage
}

func newResponsesStreamOutputItems() *responsesStreamOutputItems {
	return &responsesStreamOutputItems{items: make(map[int]json.RawMessage)}
}

// Observe records the item of a response.output_item.done event verbatim. The
// raw JSON is kept byte for byte so vendor extensions and future fields survive
// the rebuild.
func (r *responsesStreamOutputItems) Observe(data []byte) {
	if r == nil || len(data) == 0 || !gjson.ValidBytes(data) {
		return
	}
	if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.done" {
		return
	}
	item := gjson.GetBytes(data, "item")
	if !item.Exists() || !item.IsObject() {
		return
	}
	index := int(gjson.GetBytes(data, "output_index").Int())
	r.items[index] = json.RawMessage(append([]byte(nil), item.Raw...))
}

func (r *responsesStreamOutputItems) HasItems() bool {
	return r != nil && len(r.items) > 0
}

// Count reports how many distinct output items the stream reported as done.
func (r *responsesStreamOutputItems) Count() int {
	if r == nil {
		return 0
	}
	return len(r.items)
}

// BuildOutput returns the remembered items ordered by output_index.
func (r *responsesStreamOutputItems) BuildOutput() ([]byte, bool) {
	if !r.HasItems() {
		return nil, false
	}
	indexes := make([]int, 0, len(r.items))
	for index := range r.items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	ordered := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		ordered = append(ordered, r.items[index])
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func normalizeResponsesStreamingTerminalOutput(data []byte, acc *apicompat.BufferedResponseAccumulator, doneItems *responsesStreamOutputItems, imageOutputs []json.RawMessage) ([]byte, bool) {
	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return data, false
	}

	output := gjson.GetBytes(data, "response.output")
	hasAccumulatedOutput := (acc != nil && acc.HasContent()) || len(imageOutputs) > 0 || doneItems.HasItems()
	if output.Exists() && output.IsArray() {
		terminalCount := len(output.Array())
		// A terminal output carrying at least as many items as the stream
		// reported is left untouched. Carrying fewer means the terminal
		// dropped items the stream already reported as done, and those
		// reported items are the authoritative record of the turn.
		if terminalCount > 0 && terminalCount >= doneItems.Count() {
			return data, false
		}
		if terminalCount == 0 && !hasAccumulatedOutput {
			return data, false
		}
	}

	outputJSON := []byte("[]")
	// Same precedence as reconstructResponseOutputFromSSE: the items the stream
	// actually reported win over anything rebuilt from deltas. Image generation
	// items arrive as done events too, so imageOutputs would duplicate them here.
	if reconstructed, ok := doneItems.BuildOutput(); ok {
		outputJSON = reconstructed
	} else if reconstructed, ok := buildResponsesOutputJSON(acc, imageOutputs); ok {
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
	forEachOpenAISSEFrame(bodyText, func(eventType string, data []byte) {
		data = []byte(openAICompatPayloadWithEventType(string(data), eventType))
		if normalized, changed := normalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		if eventType != "response.output_item.done" {
			return
		}
		appendItem(gjson.GetBytes(data, "item"))
	})
	// done 事件未携带 compaction item 时再看 added：覆盖"其他 item 有 done、
	// compaction 只在 added 中"的混合形态；done 已含 compaction 时跳过，
	// 避免同一 item 在无 id 可去重时被收集两份（Codex 要求恰好一个）。
	if !hasCompactionItem {
		forEachOpenAISSEFrame(bodyText, func(eventType string, data []byte) {
			if eventType != "response.output_item.added" {
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
		forEachOpenAISSEFrame(bodyText, func(effectiveType string, data []byte) {
			if found != nil {
				return
			}
			if effectiveType != eventType {
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
	forEachOpenAISSEFrame(bodyText, func(eventType string, data []byte) {
		if retentionErr != nil {
			return
		}
		data = []byte(openAICompatPayloadWithEventType(string(data), eventType))
		if imageOutput, ok := extractImageGenerationOutputFromSSEData(data, seenImages); ok {
			if err := acc.RetainExternalOutput(len(imageOutput), 1); err != nil {
				retentionErr = err
				return
			}
			imageOutputs = append(imageOutputs, imageOutput)
		}
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
	if len(key) > 1024 {
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
	forEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		s.parseSSEUsageBytesWithType(data, eventType, usage)
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
