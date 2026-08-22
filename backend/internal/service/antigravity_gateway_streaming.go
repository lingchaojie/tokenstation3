package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type antigravityStreamResult struct {
	usage            *ClaudeUsage
	firstTokenMs     *int
	clientDisconnect bool // 客户端是否在流式传输过程中断开
	semanticOutput   bool
	terminalObserved bool
}

func (s *AntigravityGatewayService) observeAntigravityGeminiSSELine(c *gin.Context, line string) {
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	// Observe the original payload: ObserveGemini supports both the v1internal
	// wrapper and direct Gemini response shapes. The main stream handler will
	// unwrap the same line for business processing, so unwrapping here would be
	// duplicate work on every SSE event.
	observer.ObserveGeminiString(payload)
}

// antigravityClientWriter 封装流式响应的客户端写入，自动检测断开并标记。
// 断开后所有写入操作变为 no-op，调用方通过 Disconnected() 判断是否继续 drain 上游。
type antigravityClientWriter struct {
	w                gin.ResponseWriter
	flusher          http.Flusher
	disconnected     bool
	prefix           string // 日志前缀，标识来源方法
	beforeFirstWrite func()
}

func newAntigravityClientWriter(w gin.ResponseWriter, flusher http.Flusher, prefix string) *antigravityClientWriter {
	return &antigravityClientWriter{w: w, flusher: flusher, prefix: prefix}
}

// Write 写入数据到客户端，写入失败时标记断开并返回 false
func (cw *antigravityClientWriter) Write(p []byte) bool {
	if cw.disconnected {
		return false
	}
	cw.prepareFirstWrite()
	if _, err := cw.w.Write(p); err != nil {
		cw.markDisconnected()
		return false
	}
	cw.flusher.Flush()
	return true
}

// Fprintf 格式化写入数据到客户端，写入失败时标记断开并返回 false
func (cw *antigravityClientWriter) Fprintf(format string, args ...any) bool {
	if cw.disconnected {
		return false
	}
	cw.prepareFirstWrite()
	if _, err := fmt.Fprintf(cw.w, format, args...); err != nil {
		cw.markDisconnected()
		return false
	}
	cw.flusher.Flush()
	return true
}

func (cw *antigravityClientWriter) Disconnected() bool { return cw.disconnected }

func (cw *antigravityClientWriter) prepareFirstWrite() {
	if cw.beforeFirstWrite == nil {
		return
	}
	prepare := cw.beforeFirstWrite
	cw.beforeFirstWrite = nil
	prepare()
}

func (cw *antigravityClientWriter) markDisconnected() {
	cw.disconnected = true
	logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during streaming (%s), continuing to drain upstream for billing", cw.prefix)
}

// handleStreamReadError 处理上游读取错误的通用逻辑。
// 返回 (clientDisconnect, handled)：handled=true 表示错误已处理，调用方应返回已收集的 usage。
func handleStreamReadError(err error, clientDisconnected bool, prefix string) (disconnect bool, handled bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		logger.LegacyPrintf("service.antigravity_gateway", "Context canceled during streaming (%s), returning collected usage", prefix)
		return true, true
	}
	if clientDisconnected {
		logger.LegacyPrintf("service.antigravity_gateway", "Upstream read error after client disconnect (%s): %v, returning collected usage", prefix, err)
		return true, true
	}
	return false, false
}

func (s *AntigravityGatewayService) handleGeminiStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravityStreamResult, error) {
	if upstreamResponseModelObserverFromContext(c) == nil {
		beginUpstreamResponseModelObservation(c)
	}
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/event-stream; charset=utf-8"
	}
	c.Header("Content-Type", contentType)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	// 使用 Scanner 并限制单行大小，避免 ReadString 无上限导致 OOM
	readActivity := newProviderBodyReadActivity(resp.Body)
	scanner := bufio.NewScanner(readActivity)
	maxLineSize := defaultMaxLineSize
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	usage := &ClaudeUsage{}
	var firstTokenMs *int
	semanticOutput := false
	terminalObserved := false
	validProviderPayloadObserved := false

	type scanEvent struct {
		line string
		err  error
	}
	// 独立 goroutine 读取上游，避免读取阻塞影响超时处理
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
	go func(scanBuf *sseScannerBuf64K) {
		defer close(scanDone)
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for scanner.Scan() {
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
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

	// 上游数据间隔超时保护（防止上游挂起长期占用连接）
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

	cw := newAntigravityClientWriter(c.Writer, flusher, "antigravity gemini")
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

	// 仅发送一次错误事件，避免多次写入导致协议混乱
	errorEventSent := false
	sendErrorEvent := func(reason string) {
		if errorEventSent || cw.Disconnected() || !semanticOutput {
			return
		}
		errorEventSent = true
		writeStaged(fmt.Sprintf("event: error\ndata: {\"error\":\"%s\"}\n\n", reason), true)
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				providerScanFinished = true
				if stagedErr != nil && !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini pre-output stage exceeded limit")
				}
				if !staged.committed && !cw.Disconnected() {
					if !writeStaged("", true) {
						return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: true, semanticOutput: semanticOutput}, nil
					}
				}
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: terminalObserved}, nil
			}
			if ev.err != nil {
				if terminalObserved {
					result := &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: true}
					if staged.committed || cw.Disconnected() || semanticOutput {
						return result, fmt.Errorf("antigravity gemini stream read error after terminal event: %w", ev.err)
					}
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini stream read failed after an uncommitted terminal event")
				}
				if disconnect, handled := handleStreamReadError(ev.err, cw.Disconnected(), "antigravity gemini"); handled {
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: disconnect, semanticOutput: semanticOutput}, fmt.Errorf("stream read error: %w", ev.err)
				}
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini stream read failed before client output")
				}
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (antigravity): max_size=%d error=%v", maxLineSize, ev.err)
					sendErrorEvent("response_too_large")
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, ev.err
				}
				sendErrorEvent("stream_read_error")
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, ev.err
			}

			lastDataAt = time.Now()

			line := ev.line
			s.observeAntigravityGeminiSSELine(c, line)
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload == "" {
					writeStaged(line+"\n", terminalObserved && validProviderPayloadObserved)
					if stagedErr != nil {
						return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini pre-output stage exceeded limit")
					}
					continue
				}
				if payload == "[DONE]" {
					terminalObserved = true
					writeStaged(line+"\n", validProviderPayloadObserved)
					continue
				}

				// 解包 v1internal 响应
				inner, parseErr := s.unwrapV1InternalResponse([]byte(payload))
				if parseErr != nil || inner == nil {
					if !staged.committed {
						return nil, newIncompleteProviderStreamFailover(resp, "invalid wrapped Antigravity Gemini payload")
					}
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, errors.New("invalid wrapped Antigravity Gemini payload")
				}
				payload = string(inner)

				// 解析 usage
				if u := extractGeminiUsage(inner); u != nil {
					usage = u
				}
				validProviderPayloadObserved = gjson.ValidBytes(inner)
				if strings.TrimSpace(gjson.GetBytes(inner, "candidates.0.finishReason").String()) != "" {
					terminalObserved = true
				}
				if geminiPayloadHasSemanticOutput(inner) {
					semanticOutput = true
				}
				// Shape validation has already completed. Read only the two fields
				// needed by diagnostics instead of materializing the full provider
				// envelope (which may legitimately contain a large opaque sibling).
				if gjson.GetBytes(inner, "candidates.0.finishReason").String() == "MALFORMED_FUNCTION_CALL" {
					logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] MALFORMED_FUNCTION_CALL detected in forward stream")
					if content := gjson.GetBytes(inner, "candidates.0.content"); content.Exists() {
						logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] Malformed content: %s", content.Raw)
					}
				}

				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}

				writeStaged(fmt.Sprintf("data: %s\n\n", payload), semanticOutput || (terminalObserved && validProviderPayloadObserved))
				if stagedErr != nil {
					if !staged.committed {
						return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini pre-output stage exceeded limit")
					}
					return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: true}, stagedErr
				}
				continue
			}

			writeStaged(line+"\n", semanticOutput || (terminalObserved && validProviderPayloadObserved))
			if stagedErr != nil {
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini pre-output stage exceeded limit")
				}
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: true}, stagedErr
			}

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if cw.Disconnected() {
				logger.LegacyPrintf("service.antigravity_gateway", "Upstream timeout after client disconnect (antigravity gemini), returning terminal partial usage")
				return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: true, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout after client disconnect")
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity)")
			if !staged.committed {
				return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini stream timed out before client output")
			}
			sendErrorEvent("stream_timeout")
			return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if cw.Disconnected() {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// SSE ping/keepalive：保持连接活跃防止 Cloudflare Tunnel 等代理断开
			if !staged.committed || !writeStaged(":\n\n", true) {
				logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during keepalive ping (antigravity gemini), continuing to drain upstream for billing")
				continue
			}
		}
	}
}

// handleGeminiStreamToNonStreaming 读取上游流式响应，合并为非流式响应返回给客户端
// Gemini 流式响应是增量的，需要累积所有 chunk 的内容
func (s *AntigravityGatewayService) handleGeminiStreamToNonStreaming(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravityStreamResult, error) {
	if upstreamResponseModelObserverFromContext(c) == nil {
		beginUpstreamResponseModelObservation(c)
	}
	lineReader := newProviderLineReader(resp, s.settingService.cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, s.settingService.cfg)
	})
	defer lineReader.Close()

	usage := &ClaudeUsage{}
	var firstTokenMs *int
	var last map[string]any
	var lastWithParts map[string]any
	var collectedImageParts []map[string]any // 收集所有包含图片的 parts
	var collectedTextParts []string          // 收集所有文本片段

	for {
		line, ok, err := lineReader.Next()
		if err != nil {
			if errors.Is(err, errProviderStreamIdleTimeout) {
				logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity non-stream)")
				return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini aggregate stream timed out before terminal event")
			}
			return nil, newIncompleteProviderStreamFailover(resp, "antigravity gemini aggregate stream read failed before terminal event: "+sanitizeStreamError(err))
		}
		if !ok {
			goto returnResponse
		}
		s.observeAntigravityGeminiSSELine(c, line)
		trimmed := strings.TrimRight(line, "\r\n")

		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			continue
		}
		if payload == "" {
			continue
		}

		// 解包 v1internal 响应
		inner, parseErr := s.unwrapV1InternalResponse([]byte(payload))
		if parseErr != nil || inner == nil {
			return nil, newIncompleteProviderStreamFailover(resp, "invalid wrapped Antigravity Gemini payload")
		}
		parsed, err := decodeGeminiCompatResponse(inner)
		if err != nil {
			return nil, newIncompleteProviderStreamFailover(resp, "invalid Antigravity Gemini JSON payload")
		}
		// 记录首 token 时间
		if firstTokenMs == nil {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		last = parsed
		// 提取 usage
		if u := extractGeminiUsage(inner); u != nil {
			usage = u
		}

		if gjson.GetBytes(inner, "candidates.0.finishReason").String() == "MALFORMED_FUNCTION_CALL" {
			logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] MALFORMED_FUNCTION_CALL detected in forward non-stream collect")
			if content := gjson.GetBytes(inner, "candidates.0.content"); content.Exists() {
				logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] Malformed content: %s", content.Raw)
			}
		}

		// 保留最后一个有 parts 的响应
		if parts := extractGeminiParts(parsed); len(parts) > 0 {
			lastWithParts = parsed
			// 收集包含图片和文本的 parts
			for _, part := range parts {
				if inlineData, ok := part["inlineData"].(map[string]any); ok {
					collectedImageParts = append(collectedImageParts, part)
					_ = inlineData // 避免 unused 警告
				}
				if text, ok := part["text"].(string); ok && text != "" {
					collectedTextParts = append(collectedTextParts, text)
				}
			}
		}

	}

returnResponse:
	// 选择最后一个有效响应
	finalResponse := pickGeminiCollectResult(last, lastWithParts)

	// 处理空响应情况 — 触发同账号重试 + failover 切换账号
	if last == nil && lastWithParts == nil {
		logger.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] warning: empty stream response (gemini non-stream), triggering failover")
		return nil, newIncompleteProviderStreamFailover(resp, "empty stream response from upstream")
	}

	// 如果收集到了图片 parts，需要合并到最终响应中
	if len(collectedImageParts) > 0 {
		finalResponse = mergeImagePartsToResponse(finalResponse, collectedImageParts)
	}

	// 如果收集到了文本，需要合并到最终响应中
	if len(collectedTextParts) > 0 {
		finalResponse = mergeTextPartsToResponse(finalResponse, collectedTextParts)
	}

	respBody, err := json.Marshal(finalResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}
	c.Data(http.StatusOK, "application/json", respBody)

	return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

// getOrCreateGeminiParts 获取 Gemini 响应的 parts 结构，返回深拷贝和更新回调
func getOrCreateGeminiParts(response map[string]any) (result map[string]any, existingParts []any, setParts func([]any)) {
	// 深拷贝 response
	result = make(map[string]any)
	for k, v := range response {
		result[k] = v
	}

	// 获取或创建 candidates
	candidates, ok := result["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		candidates = []any{map[string]any{}}
	}

	// 获取第一个 candidate
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		candidate = make(map[string]any)
		candidates[0] = candidate
	}

	// 获取或创建 content
	content, ok := candidate["content"].(map[string]any)
	if !ok {
		content = map[string]any{"role": "model"}
		candidate["content"] = content
	}

	// 获取现有 parts
	existingParts, ok = content["parts"].([]any)
	if !ok {
		existingParts = []any{}
	}

	// 返回更新回调
	setParts = func(newParts []any) {
		content["parts"] = newParts
		result["candidates"] = candidates
	}

	return result, existingParts, setParts
}

// mergeCollectedPartsToResponse 将收集的所有 parts 合并到 Gemini 响应中
// 这个函数会合并所有类型的 parts：text、thinking、functionCall、inlineData 等
// 保持原始顺序，只合并连续的普通 text parts
func mergeCollectedPartsToResponse(response map[string]any, collectedParts []map[string]any) map[string]any {
	if len(collectedParts) == 0 {
		return response
	}

	result, _, setParts := getOrCreateGeminiParts(response)

	// 合并策略：
	// 1. 保持原始顺序
	// 2. 连续的普通 text parts 合并为一个
	// 3. thinking、functionCall、inlineData 等保持原样
	var mergedParts []any
	var textBuffer strings.Builder

	flushTextBuffer := func() {
		if textBuffer.Len() > 0 {
			mergedParts = append(mergedParts, map[string]any{
				"text": textBuffer.String(),
			})
			textBuffer.Reset()
		}
	}

	for _, part := range collectedParts {
		// 检查是否是普通 text part
		if text, ok := part["text"].(string); ok {
			// 检查是否有 thought 标记
			if thought, _ := part["thought"].(bool); thought {
				// thinking part，先刷新 text buffer，然后保留原样
				flushTextBuffer()
				mergedParts = append(mergedParts, part)
			} else {
				// 普通 text，累积到 buffer
				_, _ = textBuffer.WriteString(text)
			}
		} else {
			// 非 text part（functionCall、inlineData 等），先刷新 text buffer，然后保留原样
			flushTextBuffer()
			mergedParts = append(mergedParts, part)
		}
	}

	// 刷新剩余的 text
	flushTextBuffer()

	setParts(mergedParts)
	return result
}

// mergeImagePartsToResponse 将收集到的图片 parts 合并到 Gemini 响应中
func mergeImagePartsToResponse(response map[string]any, imageParts []map[string]any) map[string]any {
	if len(imageParts) == 0 {
		return response
	}

	result, existingParts, setParts := getOrCreateGeminiParts(response)

	// 检查现有 parts 中是否已经有图片
	for _, p := range existingParts {
		if pm, ok := p.(map[string]any); ok {
			if _, hasInline := pm["inlineData"]; hasInline {
				return result // 已有图片，不重复添加
			}
		}
	}

	// 添加收集到的图片 parts
	for _, imgPart := range imageParts {
		existingParts = append(existingParts, imgPart)
	}
	setParts(existingParts)
	return result
}

// mergeTextPartsToResponse 将收集到的文本合并到 Gemini 响应中
func mergeTextPartsToResponse(response map[string]any, textParts []string) map[string]any {
	if len(textParts) == 0 {
		return response
	}

	mergedText := strings.Join(textParts, "")
	result, existingParts, setParts := getOrCreateGeminiParts(response)

	// 查找并更新第一个 text part，或创建新的
	newParts := make([]any, 0, len(existingParts)+1)
	textUpdated := false

	for _, p := range existingParts {
		pm, ok := p.(map[string]any)
		if !ok {
			newParts = append(newParts, p)
			continue
		}
		if _, hasText := pm["text"]; hasText && !textUpdated {
			// 用累积的文本替换
			newPart := make(map[string]any)
			for k, v := range pm {
				newPart[k] = v
			}
			newPart["text"] = mergedText
			newParts = append(newParts, newPart)
			textUpdated = true
		} else {
			newParts = append(newParts, pm)
		}
	}

	if !textUpdated {
		newParts = append([]any{map[string]any{"text": mergedText}}, newParts...)
	}

	setParts(newParts)
	return result
}

func (s *AntigravityGatewayService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

// WriteMappedClaudeError 导出版本，供 handler 层使用（如 fallback 错误处理）
func (s *AntigravityGatewayService) WriteMappedClaudeError(c *gin.Context, account *Account, upstreamStatus int, upstreamRequestID string, body []byte) error {
	return s.writeMappedClaudeError(c, account, upstreamStatus, upstreamRequestID, body)
}

func (s *AntigravityGatewayService) writeMappedClaudeError(c *gin.Context, account *Account, upstreamStatus int, upstreamRequestID string, body []byte) error {
	MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	logBody, maxBytes := s.getLogConfig()
	upstreamDetail := s.getUpstreamErrorDetail(body)
	setOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// 记录上游错误详情便于排障（可选：由配置控制；不回显到客户端）
	if logBody {
		logger.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] upstream_error status=%d body=%s", upstreamStatus, truncateForLog(body, maxBytes))
	}

	// 检查错误透传规则
	if ptStatus, ptErrType, ptErrMsg, matched := applyErrorPassthroughRule(
		c, account.Platform, upstreamStatus, body,
		0, "", "",
	); matched {
		c.JSON(ptStatus, gin.H{
			"type":  "error",
			"error": gin.H{"type": ptErrType, "message": ptErrMsg},
		})
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	switch upstreamStatus {
	case 400:
		statusCode = http.StatusBadRequest
		errType = "invalid_request_error"
		errMsg = getPassthroughOrDefault(upstreamMsg, "Invalid request")
	case 401:
		statusCode = http.StatusBadGateway
		errType = "authentication_error"
		errMsg = "Upstream authentication failed"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "permission_error"
		errMsg = "Upstream access forbidden"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	c.JSON(statusCode, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": errMsg},
	})
	if upstreamMsg == "" {
		return fmt.Errorf("upstream error: %d", upstreamStatus)
	}
	return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
}

func (s *AntigravityGatewayService) writeGoogleError(c *gin.Context, status int, message string) error {
	MarkResponseCommitted(c)
	statusStr := "UNKNOWN"
	switch status {
	case 400:
		statusStr = "INVALID_ARGUMENT"
	case 404:
		statusStr = "NOT_FOUND"
	case 429:
		statusStr = "RESOURCE_EXHAUSTED"
	case 500:
		statusStr = "INTERNAL"
	case 502, 503:
		statusStr = "UNAVAILABLE"
	}

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  statusStr,
		},
	})
	return fmt.Errorf("%s", message)
}

// collectClaudeStreamResponse 收集上游流式响应，转换为 Claude 非流式格式返回
// 用于处理客户端非流式请求但上游只支持流式的情况
func (s *AntigravityGatewayService) collectClaudeStreamResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) ([]byte, *antigravityStreamResult, error) {
	if upstreamResponseModelObserverFromContext(c) == nil {
		beginUpstreamResponseModelObservation(c)
	}
	lineReader := newProviderLineReader(resp, s.settingService.cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, s.settingService.cfg)
	})
	defer lineReader.Close()

	var firstTokenMs *int
	var last map[string]any
	var lastWithParts map[string]any
	var collectedParts []map[string]any // 收集所有 parts（包括 text、thinking、functionCall、inlineData 等）
	var meaningfulResponse bool

	for {
		line, ok, err := lineReader.Next()
		if err != nil {
			if errors.Is(err, errProviderStreamIdleTimeout) {
				logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity claude non-stream)")
				return nil, nil, newIncompleteProviderStreamFailover(resp, "antigravity claude aggregate stream timed out before terminal event")
			}
			return nil, nil, newIncompleteProviderStreamFailover(resp, "antigravity claude aggregate stream read failed before terminal event: "+sanitizeStreamError(err))
		}
		if !ok {
			goto returnResponse
		}
		trimmed := strings.TrimRight(line, "\r\n")

		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			continue
		}
		if payload == "" {
			continue
		}

		// 解包 v1internal 响应
		inner, parseErr := s.unwrapV1InternalResponse([]byte(payload))
		if parseErr != nil || inner == nil {
			return nil, nil, newIncompleteProviderStreamFailover(resp, "invalid wrapped Antigravity Gemini payload")
		}
		upstreamResponseModelObserverFromContext(c).ObserveGemini(inner)

		parsed, err := decodeGeminiCompatResponse(inner)
		if err != nil {
			return nil, nil, newIncompleteProviderStreamFailover(resp, "invalid Antigravity Gemini JSON payload")
		}
		last = parsed

		// 保留最后一个有 parts 的响应，并收集所有 parts
		parts := extractGeminiParts(parsed)
		if len(parts) > 0 {
			lastWithParts = parsed

			// 收集所有 parts（text、thinking、functionCall、inlineData 等）
			collectedParts = append(collectedParts, parts...)
		}
		if len(parts) > 0 || strings.TrimSpace(extractGeminiFinishReason(parsed)) != "" ||
			strings.TrimSpace(gjson.GetBytes(inner, "promptFeedback.blockReason").String()) != "" {
			meaningfulResponse = true
			if firstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}
		}

	}

returnResponse:
	// 处理空响应情况 — 触发同账号重试 + failover 切换账号
	if !meaningfulResponse {
		logger.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] warning: empty stream response (claude non-stream), triggering failover")
		return nil, nil, newIncompleteProviderStreamFailover(resp, "empty stream response from upstream")
	}

	// 选择最后一个有效响应
	finalResponse := pickGeminiCollectResult(last, lastWithParts)

	// 将收集的所有 parts 合并到最终响应中
	if len(collectedParts) > 0 {
		finalResponse = mergeCollectedPartsToResponse(finalResponse, collectedParts)
	}

	// 序列化为 JSON（Gemini 格式）
	geminiBody, err := json.Marshal(finalResponse)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal gemini response: %w", err)
	}

	// 转换 Gemini 响应为 Claude 格式
	claudeResp, agUsage, err := antigravity.TransformGeminiToClaude(geminiBody, originalModel)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] transform_error error=%v body=%s", err, string(geminiBody))
		return nil, nil, fmt.Errorf("failed to parse upstream response: %w", err)
	}

	// 转换为 service.ClaudeUsage
	usage := &ClaudeUsage{
		InputTokens:              agUsage.InputTokens,
		OutputTokens:             agUsage.OutputTokens,
		CacheCreationInputTokens: agUsage.CacheCreationInputTokens,
		CacheReadInputTokens:     agUsage.CacheReadInputTokens,
		ImageOutputTokens:        agUsage.ImageOutputTokens,
	}

	return claudeResp, &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

// handleClaudeStreamToNonStreaming 收集上游流式响应，转换为 Claude 非流式格式返回
// 用于处理客户端非流式请求但上游只支持流式的情况
func (s *AntigravityGatewayService) handleClaudeStreamToNonStreaming(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*antigravityStreamResult, error) {
	claudeResp, streamRes, err := s.collectClaudeStreamResponse(c, resp, startTime, originalModel)
	if err != nil {
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			return nil, err
		}

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		errMsg := "Failed to parse upstream response"
		errType := "upstream_error"
		if strings.Contains(err.Error(), "stream data interval timeout") {
			errMsg = "Upstream stream data interval timeout"
			errType = "upstream_timeout"
		} else if errors.Is(err, bufio.ErrTooLong) {
			errMsg = "Upstream response line too long"
			errType = "response_too_large"
		}

		return nil, s.writeClaudeError(c, http.StatusBadGateway, errType, errMsg)
	}
	c.Data(http.StatusOK, "application/json", claudeResp)
	return streamRes, nil
}

// handleClaudeStreamingResponse 处理 Claude 流式响应（Gemini SSE → Claude SSE 转换）
func (s *AntigravityGatewayService) handleClaudeStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*antigravityStreamResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	processor := antigravity.NewStreamingProcessor(originalModel)
	var firstTokenMs *int
	semanticOutput := false
	terminalObserved := false
	validProviderPayloadObserved := false
	// 使用 Scanner 并限制单行大小，避免 ReadString 无上限导致 OOM
	readActivity := newProviderBodyReadActivity(resp.Body)
	scanner := bufio.NewScanner(readActivity)
	maxLineSize := defaultMaxLineSize
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

	// 辅助函数：转换 antigravity.ClaudeUsage 到 service.ClaudeUsage
	convertUsage := func(agUsage *antigravity.ClaudeUsage) *ClaudeUsage {
		if agUsage == nil {
			return &ClaudeUsage{}
		}
		return &ClaudeUsage{
			InputTokens:              agUsage.InputTokens,
			OutputTokens:             agUsage.OutputTokens,
			CacheCreationInputTokens: agUsage.CacheCreationInputTokens,
			CacheReadInputTokens:     agUsage.CacheReadInputTokens,
		}
	}

	type scanEvent struct {
		line string
		err  error
	}
	// 独立 goroutine 读取上游，避免读取阻塞影响超时处理
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
	go func(scanBuf *sseScannerBuf64K) {
		defer close(scanDone)
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for scanner.Scan() {
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
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

	cw := newAntigravityClientWriter(c.Writer, flusher, "antigravity claude")
	var staged stagedConvertedStream
	var stagedErr error
	defer func() { _ = staged.close() }()
	writeStaged := func(payload []byte, commit bool) bool {
		if cw.Disconnected() {
			return false
		}
		if err := staged.write(c, func() { c.Status(http.StatusOK) }, string(payload), commit); err != nil {
			var clientWriteErr *stagedConvertedClientWriteError
			if !errors.As(err, &clientWriteErr) {
				stagedErr = err
			}
			cw.markDisconnected()
			return false
		}
		return true
	}

	// 仅发送一次错误事件，避免多次写入导致协议混乱
	errorEventSent := false
	sendErrorEvent := func(reason string) {
		if errorEventSent || cw.Disconnected() || !semanticOutput {
			return
		}
		errorEventSent = true
		writeStaged([]byte(fmt.Sprintf("event: error\ndata: {\"error\":\"%s\"}\n\n", reason)), true)
	}

	// finishUsage 是获取 processor 最终 usage 的辅助函数
	finishUsage := func() *ClaudeUsage {
		_, agUsage := processor.Finish()
		return convertUsage(agUsage)
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				providerScanFinished = true
				if stagedErr != nil && !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude pre-output stage exceeded limit")
				}
				// 上游完成，发送结束事件
				finalEvents, agUsage := processor.Finish()
				convertedUsage := convertUsage(agUsage)
				if !semanticOutput && len(finalEvents) == 0 && convertedUsage.InputTokens == 0 && convertedUsage.OutputTokens == 0 && convertedUsage.CacheCreationInputTokens == 0 && convertedUsage.CacheReadInputTokens == 0 {
					return nil, newIncompleteProviderStreamFailover(resp, "empty Antigravity Claude stream response")
				}
				if len(finalEvents) > 0 {
					writeStaged(finalEvents, true)
					if stagedErr != nil {
						if !staged.committed {
							return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude pre-output stage exceeded limit")
						}
						return &antigravityStreamResult{usage: convertedUsage, firstTokenMs: firstTokenMs, semanticOutput: true}, stagedErr
					}
				}
				return &antigravityStreamResult{usage: convertedUsage, firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: terminalObserved}, nil
			}
			if ev.err != nil {
				if terminalObserved {
					result := &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, clientDisconnect: cw.Disconnected(), semanticOutput: semanticOutput, terminalObserved: true}
					if staged.committed || cw.Disconnected() || semanticOutput {
						return result, fmt.Errorf("antigravity claude stream read error after terminal event: %w", ev.err)
					}
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude stream read failed after an uncommitted terminal event")
				}
				if disconnect, handled := handleStreamReadError(ev.err, cw.Disconnected(), "antigravity claude"); handled {
					return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, clientDisconnect: disconnect, semanticOutput: semanticOutput}, fmt.Errorf("stream read error: %w", ev.err)
				}
				if !staged.committed {
					return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude stream read failed before client output")
				}
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (antigravity): max_size=%d error=%v", maxLineSize, ev.err)
					sendErrorEvent("response_too_large")
					return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, ev.err
				}
				sendErrorEvent("stream_read_error")
				return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream read error: %w", ev.err)
			}

			lastDataAt = time.Now()
			s.observeAntigravityGeminiSSELine(c, ev.line)
			trimmed := strings.TrimSpace(ev.line)
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				switch payload {
				case "":
				case "[DONE]":
					terminalObserved = true
				default:
					inner, unwrapErr := s.unwrapV1InternalResponse([]byte(payload))
					if unwrapErr != nil || inner == nil {
						if !staged.committed {
							return nil, newIncompleteProviderStreamFailover(resp, "invalid wrapped Antigravity Gemini payload")
						}
						return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, errors.New("invalid wrapped Antigravity Gemini payload")
					}
					if gjson.ValidBytes(inner) && (geminiPayloadHasSemanticOutput(inner) || strings.TrimSpace(gjson.GetBytes(inner, "promptFeedback.blockReason").String()) != "") {
						validProviderPayloadObserved = true
					}
					if strings.TrimSpace(gjson.GetBytes(inner, "candidates.0.finishReason").String()) != "" {
						terminalObserved = true
					}
				}
			}

			// 处理 SSE 行，转换为 Claude 格式
			claudeEvents := processor.ProcessLine(strings.TrimRight(ev.line, "\r\n"))
			if len(claudeEvents) > 0 {
				if anthropicSSEBytesHaveSemanticOutput(claudeEvents) {
					semanticOutput = true
				}
				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}
				writeStaged(claudeEvents, semanticOutput || (terminalObserved && validProviderPayloadObserved))
				if stagedErr != nil {
					if !staged.committed {
						return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude pre-output stage exceeded limit")
					}
					return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, semanticOutput: true}, stagedErr
				}
			}

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if cw.Disconnected() {
				logger.LegacyPrintf("service.antigravity_gateway", "Upstream timeout after client disconnect (antigravity claude), returning terminal partial usage")
				return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, clientDisconnect: true, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout after client disconnect")
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity)")
			if !staged.committed {
				return nil, newIncompleteProviderStreamFailover(resp, "antigravity claude stream timed out before client output")
			}
			sendErrorEvent("stream_timeout")
			return &antigravityStreamResult{usage: finishUsage(), firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if cw.Disconnected() {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// SSE ping 事件：Anthropic 原生格式，客户端会正确处理，
			// 同时保持连接活跃防止 Cloudflare Tunnel 等代理断开
			if !staged.committed || !writeStaged([]byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"), true) {
				logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during keepalive ping (antigravity claude), continuing to drain upstream for billing")
				continue
			}
		}
	}
}

func (s *AntigravityGatewayService) extractImageInputSize(body []byte) string {
	var req antigravity.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ImageConfig != nil {
		return strings.TrimSpace(req.GenerationConfig.ImageConfig.ImageSize)
	}

	return ""
}

// isImageGenerationModel 判断模型是否为图片生成模型
// 支持的模型：gemini-3.1-flash-image, gemini-3-pro-image, gemini-2.5-flash-image 等
func isImageGenerationModel(model string) bool {
	modelLower := strings.ToLower(model)
	// 移除 models/ 前缀
	modelLower = strings.TrimPrefix(modelLower, "models/")

	// 精确匹配或前缀匹配
	return modelLower == "gemini-3.1-flash-image" ||
		modelLower == "gemini-3.1-flash-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-3.1-flash-image-") ||
		modelLower == "gemini-3-pro-image" ||
		modelLower == "gemini-3-pro-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-3-pro-image-") ||
		modelLower == "gemini-2.5-flash-image" ||
		modelLower == "gemini-2.5-flash-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-2.5-flash-image-")
}
