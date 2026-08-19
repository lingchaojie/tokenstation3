package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ForwardUpstream 使用 base_url + /v1/messages + 双 header 认证透传上游 Claude 请求
func (s *AntigravityGatewayService) ForwardUpstream(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	beginCaptureAttempt(c)
	beginUpstreamResponseModelObservation(c)
	startTime := time.Now()
	sessionID := getSessionID(c)
	prefix := logPrefix(sessionID, account.Name)

	// 获取上游配置
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("upstream account missing base_url or api_key")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	// 解析请求获取模型信息
	var claudeReq antigravity.ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, fmt.Errorf("parse claude request: %w", err)
	}
	if strings.TrimSpace(claudeReq.Model) == "" {
		return nil, fmt.Errorf("missing model")
	}
	originalModel := claudeReq.Model

	// 构建上游请求 URL
	upstreamURL := baseURL + "/v1/messages"

	// 能力维度 sanitize：Anthropic-compatible 上游透传路径也需要保证 body↔beta header
	// 对称。客户端 anthropic-beta header 不含 context-management-2025-06-27 但 body 带
	// context_management 时 strip，与 Anthropic 直连 / Bedrock / Vertex 路径保持一致。
	clientBeta := c.GetHeader("anthropic-beta")
	if sanitized, changed := sanitizeAnthropicBodyForBetaTokens(body, clientBeta); changed {
		body = sanitized
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey) // Claude API 兼容

	// 透传 Claude 相关 headers
	if v := c.GetHeader("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}
	if v := clientBeta; v != "" {
		req.Header.Set("anthropic-beta", v)
	}

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 发送请求
	captureLimit := 0
	if s.settingService != nil && s.settingService.cfg != nil {
		captureLimit = s.settingService.cfg.Gateway.Capture.MaxBodyBytes
	}
	captureEnabled := s.settingService != nil && s.settingService.cfg != nil &&
		s.settingService.cfg.Gateway.Capture.Enabled && CaptureMayApplyFor(c, string(account.Platform))
	if captureEnabled {
		if s.capturePool != nil {
			beginCaptureAttemptForWireRequest(ctx, c, s.capturePool, string(account.Platform), req, body, s.settingService.cfg.Gateway.Capture.MaxHeaderBytes)
		} else {
			setCapturePlatform(c, string(account.Platform))
			setCaptureUpstreamRequest(c, req, captureLimit)
		}
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if s.capturePool != nil {
			AbortCaptureAttempt(c)
		}
		logger.LegacyPrintf("service.antigravity_gateway", "%s upstream request failed: %v", prefix, err)
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	_ = beginCaptureResponse(c, resp, captureEnabled, captureLimit)
	defer func() { _ = resp.Body.Close() }()

	// 处理错误响应
	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)

		// 429 错误时标记账号限流
		if resp.StatusCode == http.StatusTooManyRequests {
			s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody, originalModel, 0, "", false)
		}

		// 透传上游错误
		c.Header("Content-Type", resp.Header.Get("Content-Type"))
		c.Status(resp.StatusCode)
		_, _ = c.Writer.Write(respBody)

		finishCaptureResponse(resp)
		MarkResponseCommitted(c)
		return nil, newTerminalProviderHTTPError(account, resp, respBody)
	}

	// 处理成功响应（流式/非流式）
	var usage *ClaudeUsage
	var firstTokenMs *int
	var clientDisconnect bool

	if claudeReq.Stream {
		// 流式响应：透传
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		streamRes, streamErr := s.streamUpstreamResponse(c, resp, startTime)
		if streamErr != nil {
			if streamRes == nil {
				return failedForwardResultForError(c, resp, originalModel, originalModel, true, startTime, streamErr), streamErr
			}
			return streamErrorForwardResult(c, resp, originalModel, originalModel, startTime, streamRes.usage, streamRes.firstTokenMs, streamRes.clientDisconnect, streamRes.semanticOutput, streamErr), streamErr
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
		clientDisconnect = streamRes.clientDisconnect
	} else {
		// 非流式响应：直接透传
		var cfg *config.Config
		if s != nil && s.settingService != nil {
			cfg = s.settingService.cfg
		}
		respBody, err := ReadUpstreamResponseBody(resp.Body, cfg, c, nil)
		if err != nil {
			return nil, newInvalidProviderResponseFailover(resp, fmt.Sprintf("read antigravity upstream response: %v", err))
		}

		// 提取 usage
		upstreamResponseModelObserverFromContext(c).ObserveAnthropic(respBody)
		usage = s.extractClaudeUsage(respBody)

		c.Header("Content-Type", resp.Header.Get("Content-Type"))
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write(respBody)
	}

	// 构建计费结果
	duration := time.Since(startTime)
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=success duration_ms=%d", prefix, duration.Milliseconds())

	finishCaptureResponse(resp)
	return finalizeForwardResult(c, &ForwardResult{
		Model:                         originalModel,
		UpstreamModel:                 originalModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Stream:                        claudeReq.Stream,
		Duration:                      duration,
		FirstTokenMs:                  firstTokenMs,
		ClientDisconnect:              clientDisconnect,
		Usage: ClaudeUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
		},
	}), nil
}

// streamUpstreamResponse 透传上游 SSE 流并提取 Claude usage
func (s *AntigravityGatewayService) streamUpstreamResponse(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravityStreamResult, error) {
	usage := &ClaudeUsage{}
	var firstTokenMs *int
	semanticOutput := false
	terminalObserved := false
	providerPayloadObserved := false
	providerPhase := anthropicProviderAwaitingStart
	declaredEventType := ""

	readActivity := newProviderBodyReadActivity(resp.Body)
	scanner := bufio.NewScanner(readActivity)
	maxLineSize := defaultMaxLineSize
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, openAIDefaultStreamQueueSize)
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
	go func() {
		defer close(scanDone)
		defer close(events)
		for scanner.Scan() {
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	providerScanFinished := false
	defer func() {
		if !providerScanFinished {
			drainCaptureScannerOnParserFailure(ginRequestContext(c), resp, events, scanDone, &readActivity.lastRead, 0, nil, func() {
				close(done)
			})
			return
		}
		close(done)
		closeCaptureResponseAndJoinScanner(resp, scanDone)
	}()

	streamInterval := time.Duration(0)
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.settingService.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
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

	// 下游 keepalive：防止代理/Cloudflare Tunnel 因连接空闲而断开
	keepaliveInterval := time.Duration(0)
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.settingService.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}
	lastDataAt := time.Now()

	flusher, _ := c.Writer.(http.Flusher)
	cw := newAntigravityClientWriter(c.Writer, flusher, "antigravity upstream")
	var staged stagedConvertedStream
	var stagedErr error
	defer func() { _ = staged.close() }()
	writeStaged := func(payload string, commit bool) bool {
		if cw.Disconnected() {
			return false
		}
		if err := staged.write(c, func() { c.Status(resp.StatusCode) }, payload, commit); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				stagedErr = err
			}
			cw.markDisconnected()
			return false
		}
		return true
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				providerScanFinished = true
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: true}, nil
			}
			if ev.err != nil {
				if terminalObserved {
					result := &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: true}
					if staged.committed || cw.Disconnected() || semanticOutput {
						return result, fmt.Errorf("antigravity upstream stream read error after terminal event: %w", ev.err)
					}
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity upstream stream read failed after an uncommitted terminal event")
				}
				if disconnect, handled := handleStreamReadError(ev.err, cw.Disconnected(), "antigravity upstream"); handled {
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: disconnect, semanticOutput: semanticOutput}, fmt.Errorf("stream read error: %w", ev.err)
				}
				if !staged.committed && !cw.Disconnected() {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity upstream stream read failed before semantic output")
				}
				logger.LegacyPrintf("service.antigravity_gateway", "Stream read error (antigravity upstream): %v", ev.err)
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream read error: %w", ev.err)
			}

			lastDataAt = time.Now()

			line := ev.line
			if eventType, ok := parseAnthropicSSEField(line, "event"); ok {
				declaredEventType = eventType
			}
			if data, ok := extractAnthropicSSEDataLine(line); ok {
				data = strings.TrimSpace(data)
				if data == "[DONE]" {
					if providerPhase.state != anthropicProviderStarted.state || providerPhase.hasActive || !providerPhase.finalDelta {
						return nil, newIncompleteProviderStreamFailover(resp, "antigravity upstream [DONE] arrived before a valid message_start")
					}
					providerPhase.state = anthropicProviderTerminated.state
				} else if gjson.Valid(data) {
					decodedType := gjson.Get(data, "type").String()
					if err := validateAnthropicProviderEvent(&providerPhase, declaredEventType, []byte(data), decodedType); err != nil {
						if !staged.committed && !cw.Disconnected() {
							return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(err))
						}
						return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, err
					}
					if decodedType == "message_start" {
						providerPayloadObserved = true
					}
				} else {
					invalidEventErr := fmt.Errorf("invalid JSON for Anthropic event %q", declaredEventType)
					if !staged.committed && !cw.Disconnected() {
						return nil, newIncompleteProviderStreamFailover(resp, invalidEventErr.Error())
					}
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, invalidEventErr
				}
				upstreamResponseModelObserverFromContext(c).ObserveAnthropic([]byte(data))
				if anthropicStreamEventIsTerminal("", data) {
					terminalObserved = true
				}
				if anthropicSSEEventHasSemanticOutput(data) {
					semanticOutput = true
				}
				declaredEventType = ""
			}

			// 记录首 token 时间
			if firstTokenMs == nil && len(line) > 0 {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}

			// 尝试从 message_delta 或 message_stop 事件提取 usage
			s.extractSSEUsage(line, usage)

			// 透传行
			writeStaged(line+"\n", semanticOutput || (terminalObserved && providerPayloadObserved))
			if stagedErr != nil {
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity upstream pre-output stage exceeded limit")
				}
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: true}, stagedErr
			}

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if cw.Disconnected() {
				logger.LegacyPrintf("service.antigravity_gateway", "Upstream timeout after client disconnect (antigravity upstream), returning terminal partial usage")
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: true, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout after client disconnect")
			}
			if !staged.committed && !cw.Disconnected() {
				return nil, newIncompleteProviderStreamFailover(resp, "antigravity upstream stream timed out before semantic output")
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity upstream)")
			return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if cw.Disconnected() {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// SSE ping 事件：Anthropic 原生格式，客户端会正确处理，
			// 同时保持连接活跃防止 Cloudflare Tunnel 等代理断开
			if !staged.committed || !writeStaged("event: ping\ndata: {\"type\": \"ping\"}\n\n", true) {
				logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during keepalive ping (antigravity upstream), continuing to drain upstream for billing")
				continue
			}
		}
	}
}

// extractSSEUsage 从 SSE data 行中提取 Claude usage（用于流式透传场景）
//
// Anthropic streaming 的 usage 字段分布在两类事件中：
//   - message_start：嵌套在 event.message.usage（input_tokens、cache_creation_input_tokens、
//     cache_read_input_tokens 等输入侧字段）
//   - message_delta：位于顶层 event.usage（流结束时的最终 output_tokens）
//
// 仅读取顶层 event.usage 会漏掉 message_start 的输入侧字段，导致流式透传请求落库的
// usage_logs 记录 input_tokens=0。
func (s *AntigravityGatewayService) extractSSEUsage(line string, usage *ClaudeUsage) {
	dataStr, ok := extractAnthropicSSEDataLine(line)
	if !ok {
		return
	}
	event := gjson.Parse(dataStr)
	if !event.IsObject() {
		return
	}
	u := event.Get("usage")
	if event.Get("type").String() == "message_start" {
		u = event.Get("message.usage")
	}
	if !u.IsObject() {
		return
	}
	if value := int(u.Get("input_tokens").Int()); value > 0 {
		usage.InputTokens = value
	}
	if value := int(u.Get("output_tokens").Int()); value > 0 {
		usage.OutputTokens = value
	}
	if value := int(u.Get("cache_read_input_tokens").Int()); value > 0 {
		usage.CacheReadInputTokens = value
	}
	if value := int(u.Get("cache_creation_input_tokens").Int()); value > 0 {
		usage.CacheCreationInputTokens = value
	}
	usage.CacheCreation5mTokens = int(u.Get("cache_creation.ephemeral_5m_input_tokens").Int())
	usage.CacheCreation1hTokens = int(u.Get("cache_creation.ephemeral_1h_input_tokens").Int())
}

// extractClaudeUsage 从非流式 Claude 响应提取 usage
func (s *AntigravityGatewayService) extractClaudeUsage(body []byte) *ClaudeUsage {
	usage := &ClaudeUsage{}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return usage
	}
	u := root.Get("usage")
	if !u.IsObject() {
		return usage
	}
	usage.InputTokens = int(u.Get("input_tokens").Int())
	usage.OutputTokens = int(u.Get("output_tokens").Int())
	usage.CacheReadInputTokens = int(u.Get("cache_read_input_tokens").Int())
	usage.CacheCreationInputTokens = int(u.Get("cache_creation_input_tokens").Int())
	usage.CacheCreation5mTokens = int(u.Get("cache_creation.ephemeral_5m_input_tokens").Int())
	usage.CacheCreation1hTokens = int(u.Get("cache_creation.ephemeral_1h_input_tokens").Int())
	return usage
}
