package service

// 本文件由 gateway_service.go 纯移动拆分而来：Anthropic APIKey 直通
// （passthrough）转发路径及其流式/非流式响应与 usage 解析。仅做代码搬迁，
// 无任何行为变更。

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
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
)

type anthropicPassthroughForwardInput struct {
	Body          []byte
	Parsed        *ParsedRequest
	RequestModel  string
	OriginalModel string
	RequestStream bool
	StartTime     time.Time
}

func (s *GatewayService) forwardAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	reqModel string,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*ForwardResult, error) {
	return s.forwardAnthropicAPIKeyPassthroughWithInput(ctx, c, account, anthropicPassthroughForwardInput{
		Body:          body,
		RequestModel:  reqModel,
		OriginalModel: originalModel,
		RequestStream: reqStream,
		StartTime:     startTime,
	})
}

func (s *GatewayService) forwardAnthropicAPIKeyPassthroughWithInput(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	input anthropicPassthroughForwardInput,
) (*ForwardResult, error) {
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if tokenType != "apikey" {
		return nil, fmt.Errorf("anthropic api key passthrough requires apikey token, got: %s", tokenType)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	logger.LegacyPrintf("service.gateway", "[Anthropic 自动透传] 命中 API Key 透传分支: account=%d name=%s model=%s stream=%v",
		account.ID, account.Name, input.RequestModel, input.RequestStream)

	if c != nil {
		c.Set("anthropic_passthrough", true)
	}
	// Pre-filter: strip empty text blocks (including nested in tool_result) to prevent upstream 400.
	input.Body = StripEmptyTextBlocks(input.Body)
	// Pre-filter: strip web-search history blocks the upstream cannot accept
	// (emulation-synthesized ones always; genuine ones additionally for
	// passback-required third-party upstreams such as GLM/Kimi/DeepSeek,
	// which reject server_tool_use with 400). input.RequestModel 已是映射后的模型 ID。
	input.Body = FilterWebSearchHistoryBlocks(input.Body, input.RequestModel)
	if input.Parsed != nil {
		// 透传分支也会改写实际 wire body，成功 usage hash 依赖这里同步当前 body。
		if err := input.Parsed.ReplaceBody(input.Body); err != nil {
			return nil, err
		}
	}

	var resp *http.Response
	retryStart := time.Now()
	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, input.RequestStream)
		upstreamReq, wireBody, err := s.buildUpstreamRequestAnthropicAPIKeyPassthrough(upstreamCtx, c, account, input.Body, token)
		releaseUpstreamCtx()
		if err != nil {
			return nil, err
		}
		if input.Parsed != nil && !bytes.Equal(wireBody, input.Body) {
			// build 阶段会按 beta 能力清理 body，发送前同步到 ParsedRequest 当前视图。
			if err := input.Parsed.ReplaceBody(wireBody); err != nil {
				return nil, err
			}
			input.Body = input.Parsed.Body.Bytes()
		}

		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if !errors.Is(err, context.Canceled) {
				scheduleOllamaCloudUsageActivity(s.deferredService, account)
			}
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Passthrough:        true,
				Kind:               "request_error",
				Message:            safeErr,
			})
			c.JSON(http.StatusBadGateway, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "upstream_error",
					"message": "Upstream request failed",
				},
			})
			return nil, fmt.Errorf("upstream request failed: %s", safeErr)
		}

		// 透传分支禁止 400 请求体降级重试（该重试会改写请求体）
		if resp.StatusCode >= 400 && resp.StatusCode != 400 && s.shouldRetryUpstreamError(account, resp.StatusCode) {
			if attempt < maxRetryAttempts {
				elapsed := time.Since(retryStart)
				if elapsed >= maxRetryElapsed {
					break
				}

				delay := retryBackoffDelay(attempt)
				remaining := maxRetryElapsed - elapsed
				if delay > remaining {
					delay = remaining
				}
				if delay <= 0 {
					break
				}

				respBody, _ := s.readUpstreamErrorBody(resp)
				_ = resp.Body.Close()
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
					Passthrough:        true,
					Kind:               "retry",
					Message:            extractUpstreamErrorMessage(respBody),
					Detail: func() string {
						if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
							return truncateString(string(respBody), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
						}
						return ""
					}(),
				})
				logger.LegacyPrintf("service.gateway", "Anthropic passthrough account %d: upstream error %d, retry %d/%d after %v (elapsed=%v/%v)",
					account.ID, resp.StatusCode, attempt, maxRetryAttempts, delay, elapsed, maxRetryElapsed)
				if err := sleepWithContext(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
			break
		}

		break
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("upstream request failed: empty response")
	}
	streamOwnsResponseBody := false
	defer func() {
		if !streamOwnsResponseBody {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 && s.shouldRetryUpstreamError(account, resp.StatusCode) {
		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			respBody, _ := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))

			logger.LegacyPrintf("service.gateway", "[Anthropic Passthrough] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
				account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(respBody), 1000))

			s.handleRetryExhaustedSideEffects(ctx, resp, account)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Passthrough:        true,
				Kind:               "retry_exhausted_failover",
				Message:            extractUpstreamErrorMessage(respBody),
				Detail: func() string {
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						return truncateString(string(respBody), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
					}
					return ""
				}(),
			})
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return s.handleRetryExhaustedError(ctx, resp, c, account)
	}

	if resp.StatusCode >= 400 && s.shouldFailoverUpstreamError(resp.StatusCode) {
		respBody, _ := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		logger.LegacyPrintf("service.gateway", "[Anthropic Passthrough] Upstream error (failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
			account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(respBody), 1000))

		s.handleFailoverSideEffects(ctx, resp, account, input.RequestModel)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Passthrough:        true,
			Kind:               "failover",
			Message:            extractUpstreamErrorMessage(respBody),
			Detail: func() string {
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					return truncateString(string(respBody), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
				}
				return ""
			}(),
		})
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           respBody,
			RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
		}
	}

	if resp.StatusCode >= 400 {
		return s.handleErrorResponse(ctx, resp, c, account, input.RequestModel)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	var clientDisconnect bool
	if input.RequestStream {
		// The streaming reader must close the body itself to interrupt a blocked
		// Scanner and join its goroutine. Transfer ownership before entering it so
		// this caller does not close the same body a second time.
		streamOwnsResponseBody = true
		streamResult, err := s.handleStreamingResponseAnthropicAPIKeyPassthrough(ctx, resp, c, account, input.StartTime, input.RequestModel)
		if err != nil {
			// 流中断时保留已观测到的 usage 与错误一起返回，避免上游已计量的请求
			// 完全漏记漏计费（issue #5148）。
			if partial := partialStreamUsageResult(c, resp, streamResult, input.OriginalModel, input.RequestModel, input.StartTime, err); partial != nil {
				return partial, err
			}
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
		clientDisconnect = streamResult.clientDisconnect
	} else {
		usage, err = s.handleNonStreamingResponseAnthropicAPIKeyPassthrough(ctx, resp, c, account)
		if err != nil {
			return nil, err
		}
	}
	if usage == nil {
		usage = &ClaudeUsage{}
	}

	return attachCaptureToForwardResult(c, &ForwardResult{
		RequestID:                     resp.Header.Get("x-request-id"),
		Usage:                         *usage,
		Model:                         input.OriginalModel,
		UpstreamModel:                 input.RequestModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Stream:                        input.RequestStream,
		Duration:                      time.Since(input.StartTime),
		FirstTokenMs:                  firstTokenMs,
		ClientDisconnect:              clientDisconnect,
	}), nil
}

func (s *GatewayService) buildUpstreamRequestAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, []byte, error) {
	targetURL := claudeAPIURL
	baseURL := account.GetBaseURL()
	if baseURL != "" {
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, nil, err
		}
		targetURL = validatedURL + "/v1/messages?beta=true"
	}

	// 能力维度 body sanitize：透传路径上 anthropic-beta header 原样透传客户端值，
	// 依此决定是否保留 body 中的 context_management。避免“客户端 body 带字段但
	// header 忘记带 beta token”的客户端 bug 在透传场景下让上游 400。
	clientBeta := ""
	if c != nil && c.Request != nil {
		clientBeta = getHeaderRaw(c.Request.Header, "anthropic-beta")
	}
	// 账号覆写了 anthropic-beta 时，覆写值即最终上游值：净化以覆写值为准
	if beta, ok := account.HeaderOverrideValue("anthropic-beta"); ok {
		clientBeta = beta
	}
	if sanitized, changed := sanitizeAnthropicBodyForBetaTokens(body, clientBeta); changed {
		body = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	if c != nil && c.Request != nil {
		for key, values := range c.Request.Header {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if !allowedHeaders[lowerKey] {
				continue
			}
			wireKey := resolveWireCasing(key)
			for _, v := range values {
				addHeaderRaw(req.Header, wireKey, v)
			}
		}
	}

	// 覆盖入站鉴权残留，并注入上游认证
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	req.Header.Del("cookie")
	setAnthropicAPIKeyAuthHeader(req.Header, account, token)

	if getHeaderRaw(req.Header, "content-type") == "" {
		setHeaderRaw(req.Header, "content-type", "application/json")
	}
	if getHeaderRaw(req.Header, "anthropic-version") == "" {
		setHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	}

	// 旧版账号级自定义请求头（晚于默认/透传头；header_overrides 仍可最终覆盖）
	applyAccountCustomHeaders(req, account)
	// 账号级请求头覆写（最终生效，覆盖上面所有来源的同名头）
	account.ApplyHeaderOverrides(req.Header)

	s.recordUpstreamUserAgent(ctx, account, req)
	return req, body, nil
}

func (s *GatewayService) handleStreamingResponseAnthropicAPIKeyPassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	model string,
) (*streamingResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bodyOwnedByScanner := false
	defer func() {
		if !bodyOwnedByScanner && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	if s.rateLimitService != nil {
		s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)
	}

	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Header("Content-Type", contentType)
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "no-cache")
	}
	if c.Writer.Header().Get("Connection") == "" {
		c.Header("Connection", "keep-alive")
	}
	c.Header("X-Accel-Buffering", "no")
	if v := resp.Header.Get("x-request-id"); v != "" {
		c.Header("x-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &ClaudeUsage{}
	var firstTokenMs *int
	clientDisconnected := false
	semanticOutput := false
	sawTerminalEvent := false

	var tee *sseTee
	if s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
		account != nil && CaptureMayApplyFor(c, string(account.Platform)) {
		tee = newSSETee(s.cfg.Gateway.Capture.MaxBodyBytes)
		defer func() {
			if semanticOutput || sawTerminalEvent {
				captured, truncated := tee.bytes()
				setCaptureResult(c, resp, captured, truncated)
			}
		}()
	}

	stagedOutput := newDefaultOpenAIFirstOutputStage()
	defer func() { _ = stagedOutput.Close() }()
	stageCommitted := false
	pendingEventOutput := newDefaultOpenAIFirstOutputStage()
	defer func() { _ = pendingEventOutput.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	scanner.Split(boundedStreamScanLines(openAIFirstOutputStageMaxBytes, errStreamScannerTokenLimit))

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	scanDone := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	go func(scanBuf *sseScannerBuf64K) {
		defer close(scanDone)
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			line := scanner.Text()
			if tee != nil {
				tee.appendLine(line)
			}
			if !sendEvent(scanEvent{line: line}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
	bodyOwnedByScanner = true
	defer func() {
		close(done)
		// Closing the owned body is the only way to interrupt Scanner.Read. Join
		// before returning so a canceled/overflowed attempt cannot leak into the
		// next account; the outer caller has transferred close ownership here.
		_ = resp.Body.Close()
		<-scanDone
	}()

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
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
	var keepaliveTimer *time.Timer
	if keepaliveInterval > 0 {
		keepaliveTimer = time.NewTimer(keepaliveInterval)
		defer keepaliveTimer.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTimer != nil {
		keepaliveCh = keepaliveTimer.C
	}
	lastDataAt := time.Now()
	resetKeepaliveTimer := func() {
		if keepaliveTimer == nil {
			return
		}
		if !keepaliveTimer.Stop() {
			select {
			case <-keepaliveTimer.C:
			default:
			}
		}
		keepaliveTimer.Reset(keepaliveInterval)
	}

	streamResult := func() *streamingResult {
		return &streamingResult{
			usage:            usage,
			firstTokenMs:     firstTokenMs,
			clientDisconnect: clientDisconnected,
			semanticOutput:   semanticOutput,
		}
	}
	preOutputFailover := func(message string, retryable bool, responseBody []byte) error {
		if len(responseBody) == 0 {
			responseBody, _ = json.Marshal(map[string]any{
				"type": "error",
				"error": map[string]string{
					"type":    "upstream_disconnected",
					"message": message,
				},
			})
		}
		return &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           responseBody,
			RetryableOnSameAccount: retryable,
		}
	}
	transformLine := func(line string) string {
		restored := string(reverseToolNamesIfPresent(c, []byte(line)))
		return stripSub2apiInternalUsageFields(restored)
	}
	commitStageDelivery := func(stage *openAIFirstOutputStage, dst io.Writer, scope string) (int64, error) {
		delivered, commitErr := stage.CommitTo(dst)
		deliveryErr, cleanupErr := splitOpenAIFirstOutputCommitError(commitErr)
		if cleanupErr != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] %s staging cleanup failed after commit: account=%d error=%v", scope, account.ID, cleanupErr)
		}
		return delivered, deliveryErr
	}
	commitStage := func() {
		if stageCommitted {
			return
		}
		stageCommitted = true
		if clientDisconnected {
			return
		}
		if _, err := commitStageDelivery(stagedOutput, w, "first-output"); err != nil {
			clientDisconnected = true
			logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] Client disconnected while committing staged output, continue draining upstream for usage: account=%d error=%v", account.ID, err)
		}
	}

	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	inPartialEvent := false
	pendingEventName := ""
	pendingEventData := ""
	pendingEventBytes := 0
	takePendingEvent := func() *openAIFirstOutputStage {
		stage := pendingEventOutput
		pendingEventOutput = newDefaultOpenAIFirstOutputStage()
		pendingEventData = ""
		pendingEventName = ""
		pendingEventBytes = 0
		inPartialEvent = false
		return stage
	}
	discardPendingEvent := func() error {
		return takePendingEvent().Close()
	}
	processPendingEvent := func(finalizeAtCleanEOF bool) error {
		data := pendingEventData
		eventName := pendingEventName
		if finalizeAtCleanEOF {
			if _, err := pendingEventOutput.WriteString("\n"); err != nil {
				return fmt.Errorf("finalize Anthropic passthrough event: %w", err)
			}
		}
		eventStage := takePendingEvent()

		if data != "" {
			observer.ObserveAnthropic([]byte(data))
			s.parseSSEUsagePassthrough(data, usage)
		}
		if strings.EqualFold(eventName, "error") {
			eventErr := &sseStreamErrorEventError{RawData: data}
			if !semanticOutput {
				return errors.Join(eventErr, eventStage.Close())
			}
			if clientDisconnected {
				return errors.Join(eventErr, eventStage.Close())
			}
			expected := eventStage.Buffered()
			delivered, deliverErr := eventStage.CommitTo(w)
			if delivered == expected {
				flusher.Flush()
				lastDataAt = time.Now()
				resetKeepaliveTimer()
				MarkResponseCommitted(c)
			} else if deliverErr != nil {
				clientDisconnected = true
			}
			return errors.Join(eventErr, deliverErr)
		}

		eventHasSemanticOutput := anthropicSSEEventHasSemanticOutput(data)
		terminal := anthropicStreamEventIsTerminal(eventName, data)
		wasSemanticOutput := semanticOutput
		if !wasSemanticOutput {
			if _, err := commitStageDelivery(eventStage, stagedOutput, "event"); err != nil {
				return fmt.Errorf("stage complete Anthropic passthrough event: %w", err)
			}
		} else if clientDisconnected {
			if err := eventStage.Close(); err != nil {
				return fmt.Errorf("discard Anthropic passthrough event after disconnect: %w", err)
			}
		} else {
			if _, err := commitStageDelivery(eventStage, w, "event"); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] Client disconnected while committing complete event, continue draining upstream for usage: account=%d error=%v", account.ID, err)
			}
		}
		if eventHasSemanticOutput && !semanticOutput {
			semanticOutput = true
			if firstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}
		}
		if terminal {
			sawTerminalEvent = true
		}
		if eventHasSemanticOutput || terminal {
			commitStage()
		}
		if (wasSemanticOutput || eventHasSemanticOutput || terminal) && !clientDisconnected {
			flusher.Flush()
			lastDataAt = time.Now()
			resetKeepaliveTimer()
		}
		return nil
	}
	handlePendingEventError := func(eventErr error) (*streamingResult, error) {
		var sseErr *sseStreamErrorEventError
		if !semanticOutput {
			responseBody := []byte(nil)
			if errors.As(eventErr, &sseErr) {
				responseBody = []byte(sseErr.RawData)
			}
			return nil, preOutputFailover("upstream returned an SSE error before semantic output", true, responseBody)
		}
		return streamResult(), eventErr
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// A clean EOF may complete a recognized terminal/error event when the
				// provider omitted the optional trailing blank line. Other partial
				// events are discarded so a handler fallback cannot become another
				// data line in an unterminated upstream event.
				if inPartialEvent {
					finalizeAtEOF := anthropicStreamEventIsTerminal(pendingEventName, pendingEventData) ||
						(strings.EqualFold(pendingEventName, "error") && pendingEventData != "")
					if finalizeAtEOF {
						if eventErr := processPendingEvent(true); eventErr != nil {
							return handlePendingEventError(eventErr)
						}
					} else if cleanupErr := discardPendingEvent(); cleanupErr != nil {
						if !semanticOutput {
							return nil, errors.Join(
								preOutputFailover("upstream stream ended before semantic output", true, nil),
								cleanupErr,
							)
						}
						return streamResult(), fmt.Errorf("discard incomplete Anthropic passthrough event: %w", cleanupErr)
					}
				}
				if !sawTerminalEvent {
					if !semanticOutput {
						return nil, preOutputFailover("upstream stream ended before semantic output", true, nil)
					}
					return streamResult(), fmt.Errorf("stream usage incomplete: missing terminal event")
				}
				return streamResult(), nil
			}
			if ev.err != nil {
				cleanupErr := discardPendingEvent()
				if sawTerminalEvent {
					if cleanupErr != nil {
						return streamResult(), fmt.Errorf("discard incomplete Anthropic passthrough event after terminal event: %w", cleanupErr)
					}
					return streamResult(), nil
				}
				if !semanticOutput {
					return nil, errors.Join(
						preOutputFailover("upstream stream disconnected: "+sanitizeStreamError(ev.err), true, nil),
						cleanupErr,
					)
				}
				if errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return streamResult(), errors.Join(fmt.Errorf("stream usage incomplete: %w", ev.err), cleanupErr)
				}
				if errors.Is(ev.err, errStreamScannerTokenLimit) {
					logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] SSE line exceeded bounded staging limit: account=%d max_size=%d error=%v", account.ID, openAIFirstOutputStageMaxBytes, ev.err)
					return streamResult(), errors.Join(ev.err, cleanupErr)
				}
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] SSE line too long: account=%d max_size=%d error=%v", account.ID, maxLineSize, ev.err)
					return streamResult(), errors.Join(ev.err, cleanupErr)
				}
				if clientDisconnected {
					return streamResult(), errors.Join(fmt.Errorf("stream usage incomplete after disconnect: %w", ev.err), cleanupErr)
				}
				return streamResult(), errors.Join(fmt.Errorf("stream read error: %w", ev.err), cleanupErr)
			}

			line := ev.line
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				lineBytes := len(line) + 1
				if lineBytes > openAIFirstOutputStageMaxBytes-pendingEventBytes {
					limitErr := fmt.Errorf("anthropic SSE event exceeded %d bytes", openAIFirstOutputStageMaxBytes)
					cleanupErr := discardPendingEvent()
					if !semanticOutput {
						return nil, errors.Join(preOutputFailover(limitErr.Error(), true, nil), cleanupErr)
					}
					return streamResult(), errors.Join(limitErr, cleanupErr)
				}
				pendingEventBytes += lineBytes
			}

			outputLine := transformLine(line)
			if _, err := pendingEventOutput.WriteString(outputLine + "\n"); err != nil {
				cleanupErr := discardPendingEvent()
				if !semanticOutput {
					return nil, errors.Join(preOutputFailover(sanitizeStreamError(err), true, nil), cleanupErr)
				}
				return streamResult(), errors.Join(err, cleanupErr)
			}

			if trimmed != "" {
				if strings.HasPrefix(strings.TrimSpace(outputLine), "event:") {
					pendingEventName = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(outputLine), "event:"))
				}
				if pendingEventData == "" {
					if data, ok := extractAnthropicSSEDataLine(outputLine); ok {
						pendingEventData = strings.TrimSpace(data)
					}
				}
				inPartialEvent = true
				continue
			}

			if eventErr := processPendingEvent(false); eventErr != nil {
				return handlePendingEventError(eventErr)
			}

		case <-ctxDone:
			cancelErr := ctx.Err()
			if cancelErr == nil {
				cancelErr = context.Canceled
			}
			cleanupErr := discardPendingEvent()
			clientDisconnected = true
			if !semanticOutput {
				return nil, errors.Join(
					preOutputFailover("upstream stream canceled: "+sanitizeStreamError(cancelErr), false, nil),
					cleanupErr,
				)
			}
			return streamResult(), errors.Join(fmt.Errorf("stream usage incomplete: %w", cancelErr), cleanupErr)

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] Stream data interval timeout: account=%d model=%s interval=%s", account.ID, model, streamInterval)
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, model)
			}
			cleanupErr := discardPendingEvent()
			if !semanticOutput {
				return nil, errors.Join(
					preOutputFailover(fmt.Sprintf("upstream stream idle for %s", streamInterval), true, nil),
					cleanupErr,
				)
			}
			if clientDisconnected {
				return streamResult(), errors.Join(errors.New("stream usage incomplete after timeout"), cleanupErr)
			}
			return streamResult(), errors.Join(errors.New("stream data interval timeout"), cleanupErr)

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if !semanticOutput {
				resetKeepaliveTimer()
				continue
			}
			if inPartialEvent {
				resetKeepaliveTimer()
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				resetKeepaliveTimer()
				continue
			}
			if _, err := fmt.Fprint(w, "event: ping\ndata: {\"type\": \"ping\"}\n\n"); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "[Anthropic passthrough] Client disconnected during keepalive ping, continue draining upstream for usage: account=%d", account.ID)
				continue
			}
			flusher.Flush()
			lastDataAt = time.Now()
			resetKeepaliveTimer()
		}
	}
}

func extractAnthropicSSEDataLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	start := len("data:")
	for start < len(line) {
		if line[start] != ' ' && line[start] != '\t' {
			break
		}
		start++
	}
	return line[start:], true
}

func stripSub2apiInternalUsageFields(line string) string {
	if !strings.Contains(line, "_sub2api_kiro_credits") {
		return line
	}
	data, ok := extractAnthropicSSEDataLine(line)
	if !ok {
		return line
	}
	cleaned, err := sjson.Delete(data, "usage._sub2api_kiro_credits")
	if err != nil {
		return line
	}
	return line[:len(line)-len(data)] + cleaned
}

func (s *GatewayService) parseSSEUsagePassthrough(data string, usage *ClaudeUsage) {
	if usage == nil || data == "" || data == "[DONE]" {
		return
	}

	parsed := gjson.Parse(data)
	switch parsed.Get("type").String() {
	case "message_start":
		msgUsage := parsed.Get("message.usage")
		if msgUsage.Exists() {
			usage.InputTokens = int(msgUsage.Get("input_tokens").Int())
			usage.CacheCreationInputTokens = int(msgUsage.Get("cache_creation_input_tokens").Int())
			usage.CacheReadInputTokens = int(msgUsage.Get("cache_read_input_tokens").Int())

			// 保持与通用解析一致：message_start 允许覆盖 5m/1h 明细（包括 0）。
			cc5m := msgUsage.Get("cache_creation.ephemeral_5m_input_tokens")
			cc1h := msgUsage.Get("cache_creation.ephemeral_1h_input_tokens")
			if cc5m.Exists() || cc1h.Exists() {
				usage.CacheCreation5mTokens = int(cc5m.Int())
				usage.CacheCreation1hTokens = int(cc1h.Int())
			}
		}
	case "message_delta":
		deltaUsage := parsed.Get("usage")
		if deltaUsage.Exists() {
			if v := deltaUsage.Get("input_tokens").Int(); v > 0 {
				usage.InputTokens = int(v)
			}
			if v := deltaUsage.Get("output_tokens").Int(); v > 0 {
				usage.OutputTokens = int(v)
			}
			if v := deltaUsage.Get("cache_creation_input_tokens").Int(); v > 0 {
				usage.CacheCreationInputTokens = int(v)
			}
			if v := deltaUsage.Get("cache_read_input_tokens").Int(); v > 0 {
				usage.CacheReadInputTokens = int(v)
			}

			cc5m := deltaUsage.Get("cache_creation.ephemeral_5m_input_tokens")
			cc1h := deltaUsage.Get("cache_creation.ephemeral_1h_input_tokens")
			if cc5m.Exists() && cc5m.Int() > 0 {
				usage.CacheCreation5mTokens = int(cc5m.Int())
			}
			if cc1h.Exists() && cc1h.Int() > 0 {
				usage.CacheCreation1hTokens = int(cc1h.Int())
			}
			if v := deltaUsage.Get("_sub2api_kiro_credits"); v.Exists() && v.Float() > 0 {
				usage.KiroCredits = v.Float()
			}
		}
	}

	if usage.CacheReadInputTokens == 0 {
		if cached := parsed.Get("message.usage.cached_tokens").Int(); cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
		if cached := parsed.Get("usage.cached_tokens").Int(); usage.CacheReadInputTokens == 0 && cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
	}
	if usage.CacheCreationInputTokens == 0 {
		cc5m := parsed.Get("message.usage.cache_creation.ephemeral_5m_input_tokens").Int()
		cc1h := parsed.Get("message.usage.cache_creation.ephemeral_1h_input_tokens").Int()
		if cc5m == 0 && cc1h == 0 {
			cc5m = parsed.Get("usage.cache_creation.ephemeral_5m_input_tokens").Int()
			cc1h = parsed.Get("usage.cache_creation.ephemeral_1h_input_tokens").Int()
		}
		total := cc5m + cc1h
		if total > 0 {
			usage.CacheCreationInputTokens = int(total)
		}
	}
}

func parseClaudeUsageFromResponseBody(body []byte) *ClaudeUsage {
	usage := &ClaudeUsage{}
	if len(body) == 0 {
		return usage
	}

	parsed := gjson.ParseBytes(body)
	usageNode := parsed.Get("usage")
	if !usageNode.Exists() {
		return usage
	}

	usage.InputTokens = int(usageNode.Get("input_tokens").Int())
	usage.OutputTokens = int(usageNode.Get("output_tokens").Int())
	usage.CacheCreationInputTokens = int(usageNode.Get("cache_creation_input_tokens").Int())
	usage.CacheReadInputTokens = int(usageNode.Get("cache_read_input_tokens").Int())

	cc5m := usageNode.Get("cache_creation.ephemeral_5m_input_tokens").Int()
	cc1h := usageNode.Get("cache_creation.ephemeral_1h_input_tokens").Int()
	if cc5m > 0 || cc1h > 0 {
		usage.CacheCreation5mTokens = int(cc5m)
		usage.CacheCreation1hTokens = int(cc1h)
	}
	if usage.CacheCreationInputTokens == 0 && (cc5m > 0 || cc1h > 0) {
		usage.CacheCreationInputTokens = int(cc5m + cc1h)
	}
	if usage.CacheReadInputTokens == 0 {
		if cached := usageNode.Get("cached_tokens").Int(); cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
	}
	return usage
}

func (s *GatewayService) invalidNonStreamingJSONFailoverError(
	ctx context.Context,
	resp *http.Response,
	account *Account,
	body []byte,
	parseErr error,
	requestedModel ...string,
) error {
	const statusCode = http.StatusBadGateway

	accountID := int64(0)
	accountName := ""
	retryableOnSameAccount := false
	if account != nil {
		accountID = account.ID
		accountName = account.Name
		retryableOnSameAccount = account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)
	}

	logger.LegacyPrintf(
		"service.gateway",
		"Account %d(%s): upstream returned non-JSON 2xx response, attempting failover: status=%d request_id=%s error=%v",
		accountID,
		accountName,
		resp.StatusCode,
		resp.Header.Get("x-request-id"),
		parseErr,
	)

	if s.rateLimitService != nil && account != nil {
		if len(requestedModel) > 0 {
			s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, resp.Header, body, requestedModel[0])
		} else {
			s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, resp.Header, body)
		}
	}

	return &UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           body,
		ResponseHeaders:        resp.Header,
		RetryableOnSameAccount: retryableOnSameAccount,
	}
}

func (s *GatewayService) handleNonStreamingResponseAnthropicAPIKeyPassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
) (*ClaudeUsage, error) {
	if s.rateLimitService != nil {
		s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)
	}

	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, anthropicTooLargeError)
	if err != nil {
		return nil, err
	}
	if s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
		account != nil && CaptureMayApplyFor(c, string(account.Platform)) {
		captured, truncated := captureWithLimit(body, s.cfg.Gateway.Capture.MaxBodyBytes)
		setCaptureResult(c, resp, captured, truncated)
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveAnthropic(body)

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, s.invalidNonStreamingJSONFailoverError(ctx, resp, account, body, err)
		}
	}

	usage := parseClaudeUsageFromResponseBody(body)
	if IsForceCacheBilling(ctx) && usage.InputTokens > 0 {
		body, err = classifyAnthropicResponseInputAsCacheRead(body, usage)
		if err != nil {
			return nil, err
		}
	}

	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	body = reverseToolNamesIfPresent(c, body)
	c.Data(resp.StatusCode, contentType, body)
	return usage, nil
}

func classifyAnthropicResponseInputAsCacheRead(body []byte, usage *ClaudeUsage) ([]byte, error) {
	classified, err := sjson.SetBytes(body, "usage.input_tokens", 0)
	if err != nil {
		return nil, fmt.Errorf("classify forced cache billing input tokens: %w", err)
	}
	classified, err = sjson.SetBytes(classified, "usage.cache_read_input_tokens", usage.CacheReadInputTokens+usage.InputTokens)
	if err != nil {
		return nil, fmt.Errorf("classify forced cache billing cache read tokens: %w", err)
	}
	return classified, nil
}

func writeAnthropicPassthroughResponseHeaders(dst http.Header, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		responseheaders.WriteFilteredHeaders(dst, src, filter)
		return
	}
	if v := strings.TrimSpace(src.Get("Content-Type")); v != "" {
		dst.Set("Content-Type", v)
	}
	if v := strings.TrimSpace(src.Get("x-request-id")); v != "" {
		dst.Set("x-request-id", v)
	}
}
