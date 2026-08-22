package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
)

// isClaudeCodeClient 判断请求是否来自真正的 Claude Code 客户端。
// 判定条件：
//  1. User-Agent 匹配 claude-cli/X.Y.Z（大小写不敏感）
//  2. metadata.user_id 符合 Claude Code 格式（legacy 或 JSON 格式）
//
// 只检查 metadata.user_id 非空不够严格：第三方工具（opencode 等）可能伪造 UA
// 并附带任意 metadata.user_id 字符串，从而绕过 mimicry。必须通过 ParseMetadataUserID
// 验证格式才能确认是真正的 Claude Code 客户端。
func isClaudeCodeClient(userAgent string, metadataUserID string) bool {
	if !claudeCliUserAgentRe.MatchString(userAgent) {
		return false
	}
	return ParseMetadataUserID(metadataUserID) != nil
}

func shouldUseClaudeCodeNoopDeltaKeepalive(userAgent string) bool {
	version := ExtractCLIVersion(userAgent)
	if version == "" {
		return false
	}
	return CompareVersions(version, claudeCodeNoopDeltaKeepaliveMinVersion) >= 0
}

func claudeCodeKeepaliveDeltaTypeForContentBlock(blockType string) string {
	switch blockType {
	case "text":
		return "text_delta"
	case "tool_use":
		return "input_json_delta"
	case "thinking":
		return "thinking_delta"
	default:
		return ""
	}
}

func claudeCodeKeepaliveFieldForDeltaType(deltaType string) string {
	switch deltaType {
	case "text_delta":
		return "text"
	case "input_json_delta":
		return "partial_json"
	case "thinking_delta":
		return "thinking"
	default:
		return ""
	}
}

func buildClaudeCodeNoopDeltaKeepalive(index int, deltaType string) (string, bool) {
	fieldName := claudeCodeKeepaliveFieldForDeltaType(deltaType)
	if fieldName == "" {
		return "", false
	}
	return fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"%s\",\"%s\":\"\"}}\n\n", index, deltaType, fieldName), true
}

// shouldRectifySignatureError 统一判断是否应触发签名整流（strip thinking blocks 并重试）。
// 根据账号类型检查对应的开关和匹配模式。
//
// mappedModel 用于按 thinking 协议族分流：passback-required (DeepSeek/Kimi/GLM 等) 上游
// 的 400 不是签名缺失问题，retry 任何 thinking 变形都会破坏「原样回传」契约——直接透传
// 错误给客户端。详见 thinking_protocol.go。
func (s *GatewayService) shouldRectifySignatureError(ctx context.Context, account *Account, respBody []byte, mappedModel string) bool {
	if !ShouldRectifyThinkingSignatureError(mappedModel) {
		return false
	}
	if account.Type == AccountTypeAPIKey {
		// API Key 账号：独立开关，一次读取配置
		settings, err := s.settingService.GetRectifierSettings(ctx)
		if err != nil || !settings.Enabled || !settings.APIKeySignatureEnabled {
			return false
		}
		// 先检查内置模式（同 OAuth），再检查自定义关键词
		if s.isThinkingBlockSignatureError(respBody) {
			return true
		}
		return matchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	// OAuth/SetupToken/Upstream/Bedrock 等：保持原有行为（内置模式 + 原开关）
	return s.isThinkingBlockSignatureError(respBody) && s.settingService.IsSignatureRectifierEnabled(ctx)
}

// isSignatureErrorPattern 仅做模式匹配，不检查开关。
// 用于已进入重试流程后的二阶段检测（此时开关已在首次调用时验证过）。
func (s *GatewayService) isSignatureErrorPattern(ctx context.Context, account *Account, respBody []byte) bool {
	if s.isThinkingBlockSignatureError(respBody) {
		return true
	}
	if account.Type == AccountTypeAPIKey {
		settings, err := s.settingService.GetRectifierSettings(ctx)
		if err != nil {
			return false
		}
		return matchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	return false
}

// matchSignaturePatterns 检查响应体是否匹配自定义关键词列表（不区分大小写）。
func matchSignaturePatterns(respBody []byte, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	bodyLower := strings.ToLower(string(respBody))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(bodyLower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// isThinkingBlockSignatureError 检测是否是thinking block相关错误
// 这类错误可以通过过滤thinking blocks并重试来解决
func (s *GatewayService) isThinkingBlockSignatureError(respBody []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
	if msg == "" {
		return false
	}

	// 检测signature相关的错误（更宽松的匹配）
	// 例如: "Invalid `signature` in `thinking` block", "***.signature" 等
	if strings.Contains(msg, "signature") {
		return true
	}

	// 检测 thinking block 顺序/类型错误
	// 例如: "Expected `thinking` or `redacted_thinking`, but found `text`"
	if strings.Contains(msg, "expected") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "redacted_thinking")) {
		logger.LegacyPrintf("service.gateway", "[SignatureCheck] Detected thinking block type error")
		return true
	}

	// 检测 thinking block 被修改的错误
	// 例如: "thinking or redacted_thinking blocks in the latest assistant message cannot be modified"
	if strings.Contains(msg, "cannot be modified") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "redacted_thinking")) {
		logger.LegacyPrintf("service.gateway", "[SignatureCheck] Detected thinking block modification error")
		return true
	}

	// 检测空消息内容错误（可能是过滤 thinking blocks 后导致的，或客户端发送了空 text block）
	// 例如: "all messages must have non-empty content"
	//       "messages: text content blocks must be non-empty"
	if strings.Contains(msg, "non-empty content") || strings.Contains(msg, "empty content") ||
		strings.Contains(msg, "content blocks must be non-empty") {
		logger.LegacyPrintf("service.gateway", "[SignatureCheck] Detected empty content error")
		return true
	}

	// 检测 thinking block 缺少 thinking 字段的错误（跨模型切换时常见：
	// 其他模型回过的 assistant 历史里有 type=thinking 但没有 thinking 文本，
	// 喂给开启 extended thinking 的 claude 时会被拒）
	// 例如: "messages.1.content.0.thinking: each thinking block must contain thinking"
	if strings.Contains(msg, "thinking block must contain") {
		logger.LegacyPrintf("service.gateway", "[SignatureCheck] Detected thinking block missing content error")
		return true
	}

	return false
}

func (s *GatewayService) shouldFailoverOn400(respBody []byte) bool {
	// 只对"可能是兼容性差异导致"的 400 允许切换，避免无意义重试。
	// 默认保守：无法识别则不切换。
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
	if msg == "" {
		return false
	}

	// 缺少/错误的 beta header：换账号/链路可能成功（尤其是混合调度时）。
	// 更精确匹配 beta 相关的兼容性问题，避免误触发切换。
	if strings.Contains(msg, "anthropic-beta") ||
		strings.Contains(msg, "beta feature") ||
		strings.Contains(msg, "requires beta") {
		return true
	}

	// thinking/tool streaming 等兼容性约束（常见于中间转换链路）
	if strings.Contains(msg, "thinking") || strings.Contains(msg, "thought_signature") || strings.Contains(msg, "signature") {
		return true
	}
	if strings.Contains(msg, "tool_use") || strings.Contains(msg, "tool_result") || strings.Contains(msg, "tools") {
		return true
	}

	return false
}

// sanitizeStreamError 返回不含网络地址的客户端可见错误描述。
// 默认 (*net.OpError).Error() 会拼接 Source/Addr 字段，泄露内部 IP/端口与上游
// 服务器地址（例如 "read tcp 10.0.0.1:54321->52.1.2.3:443: read: connection
// reset by peer"）。该函数只保留可识别的错误类别，原始 err 仍在调用点写入日志。
func sanitizeStreamError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected EOF"
	case errors.Is(err, io.EOF):
		return "EOF"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset by peer"
	case errors.Is(err, syscall.ECONNABORTED):
		return "connection aborted"
	case errors.Is(err, syscall.ETIMEDOUT):
		return "connection timed out"
	case errors.Is(err, syscall.EPIPE):
		return "broken pipe"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			if netErr.Op != "" {
				return netErr.Op + " timeout"
			}
			return "i/o timeout"
		}
		if netErr.Op != "" {
			return netErr.Op + " network error"
		}
	}
	return "upstream connection error"
}

// ExtractUpstreamErrorMessage 从上游响应体中提取错误消息
// 支持 Claude 风格的错误格式：{"type":"error","error":{"type":"...","message":"..."}}
func ExtractUpstreamErrorMessage(body []byte) string {
	return extractUpstreamErrorMessage(body)
}

func extractUpstreamErrorMessage(body []byte) string {
	// Claude 风格：{"type":"error","error":{"type":"...","message":"..."}}
	if m := gjson.GetBytes(body, "error.message").String(); strings.TrimSpace(m) != "" {
		inner := strings.TrimSpace(m)
		// 有些上游会把完整 JSON 作为字符串塞进 message
		if strings.HasPrefix(inner, "{") {
			if innerMsg := gjson.Get(inner, "error.message").String(); strings.TrimSpace(innerMsg) != "" {
				return innerMsg
			}
		}
		return m
	}

	// ChatGPT 内部 API 风格：{"detail":"..."}
	if d := gjson.GetBytes(body, "detail").String(); strings.TrimSpace(d) != "" {
		return d
	}

	// 兜底：尝试顶层 message
	return gjson.GetBytes(body, "message").String()
}

func extractUpstreamErrorCode(body []byte) string {
	if code := strings.TrimSpace(gjson.GetBytes(body, "error.code").String()); code != "" {
		return code
	}

	inner := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if !strings.HasPrefix(inner, "{") {
		return ""
	}

	if code := strings.TrimSpace(gjson.Get(inner, "error.code").String()); code != "" {
		return code
	}

	if lastBrace := strings.LastIndex(inner, "}"); lastBrace >= 0 {
		if code := strings.TrimSpace(gjson.Get(inner[:lastBrace+1], "error.code").String()); code != "" {
			return code
		}
	}

	return ""
}

func isCountTokensUnsupported404(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "/v1/messages/count_tokens") {
		return true
	}
	return strings.Contains(msg, "count_tokens") && strings.Contains(msg, "not found")
}

func (s *GatewayService) readUpstreamErrorBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	limit := s.upstreamErrorBodyReadLimit()
	body, err := readCaptureAwareUpstreamErrorBody(resp, limit)
	// The capture-aware reader may consume beyond the functional error prefix.
	// Publish those raw bytes before terminal classification constructs/submits a
	// record; waiting for a later Close would make the record fall back to the
	// smaller business body.
	finishCaptureResponse(resp)
	return body, err
}

func (s *GatewayService) upstreamErrorBodyReadLimit() int64 {
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody && s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func boundedUpstreamErrorResponseComplete(body []byte, readErr error, limit int64) bool {
	return readErr == nil && limit > 0 && int64(len(body)) < limit
}

func captureAwareUpstreamErrorResponseComplete(resp *http.Response, body []byte, readErr error, limit int64) bool {
	if readErr != nil {
		return false
	}
	// Legacy WebChat ownership deliberately lets the capture wrapper finish its
	// existing bounded drain beyond the smaller business/logging prefix. In that
	// case the wrapper's archive-ceiling state, rather than the returned prefix
	// length, is the authoritative completeness signal.
	if resp != nil {
		if captureReader, ok := resp.Body.(*captureBodyReadCloser); ok {
			_, truncated := captureReader.bytes()
			return !truncated
		}
	}
	return boundedUpstreamErrorResponseComplete(body, nil, limit)
}

type replayedGatewayUpstreamErrorBody struct {
	*bytes.Reader
	terminalErr error
}

func (r *replayedGatewayUpstreamErrorBody) Read(p []byte) (int, error) {
	if r == nil || r.Reader == nil {
		return 0, io.EOF
	}
	if r.Len() > 0 {
		return r.Reader.Read(p)
	}
	if r.terminalErr != nil {
		return 0, r.terminalErr
	}
	return 0, io.EOF
}

func (r *replayedGatewayUpstreamErrorBody) Close() error { return nil }

func replayGatewayUpstreamErrorBody(body []byte, terminalErr error) io.ReadCloser {
	return &replayedGatewayUpstreamErrorBody{Reader: bytes.NewReader(body), terminalErr: terminalErr}
}

// readWebChatUpstreamErrorBody preserves the normal 512 KiB safety bound for
// handlers while allowing the explicitly-tokened one-account WebChat boundary
// to archive the terminal provider body up to its configured capture ceiling.
func (s *GatewayService) readWebChatUpstreamErrorBody(ctx context.Context, resp *http.Response) ([]byte, bool, bool, error) {
	captureLimit, captureApproved := captureUpstreamRequestLimitFromContext(ctx)
	if !ownsWebChatFinalGatewayErrorCapture(ctx) || !captureApproved {
		body, err := s.readUpstreamErrorBody(resp)
		return body, false, captureAwareUpstreamErrorResponseComplete(resp, body, err, s.upstreamErrorBodyReadLimit()), err
	}
	body, truncated, err := readUpstreamBodyWithCeiling(ctx, resp, captureLimit, resolveProviderBodyIdleTimeout(s.cfg))
	return body, truncated, err == nil && !truncated, err
}

func readUpstreamBodyWithCeiling(ctx context.Context, resp *http.Response, limit int, idleTimeout time.Duration) ([]byte, bool, error) {
	limit = normalizeCaptureLimit(limit)
	if resp == nil || resp.Body == nil || limit < 0 {
		return nil, false, nil
	}
	if limit == 0 {
		body, err := readAllWithProviderIdle(ctx, resp.Body, idleTimeout, func(reader io.Reader) ([]byte, error) {
			return io.ReadAll(reader)
		})
		return body, err != nil, err
	}
	body, err := readAllWithProviderIdle(ctx, resp.Body, idleTimeout, func(reader io.Reader) ([]byte, error) {
		return io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	})
	if len(body) <= limit {
		return body, false, err
	}
	return body[:limit], true, err
}

func (s *GatewayService) handleErrorResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, requestedModel ...string) (*ForwardResult, error) {
	startedAt := time.Now()
	// Upstream returned a non-success HTTP status; count Ollama Cloud activity.
	scheduleOllamaCloudUsageActivity(s.deferredService, account)
	body, readErr := s.readUpstreamErrorBody(resp)
	if readErr != nil {
		// 读取失败时 body 可能被截断，错误分类会基于不完整数据；记录日志以便排查，
		// 避免静默吞掉导致误判。
		logger.LegacyPrintf("service.gateway", "[Forward] Failed to fully read upstream error body: Account=%d(%s) Status=%d err=%v",
			account.ID, account.Name, resp.StatusCode, readErr)
	}

	// 调试日志：打印上游错误响应
	logger.LegacyPrintf("service.gateway", "[Forward] Upstream error (non-retryable): Account=%d(%s) Status=%d RequestID=%s Body=%s",
		account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(body), 1000))

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

	// Print a compact upstream request fingerprint when we hit the Claude Code OAuth
	// credential scope error. This avoids requiring env-var tweaks in a fixed deploy.
	if isClaudeCodeCredentialScopeError(upstreamMsg) && c != nil {
		if v, ok := c.Get(claudeMimicDebugInfoKey); ok {
			if line, ok := v.(string); ok && strings.TrimSpace(line) != "" {
				logger.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s",
					resp.StatusCode,
					resp.Header.Get("x-request-id"),
					line,
				)
			}
		}
	}

	// Enrich Ops error logs with upstream status + message, and optionally a truncated body snippet.
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// 处理上游错误，标记账号状态
	shouldDisable := false
	if s.rateLimitService != nil {
		if len(requestedModel) > 0 {
			shouldDisable = s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel[0])
		} else {
			shouldDisable = s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
		}
	}
	if shouldDisable {
		responseComplete := boundedUpstreamErrorResponseComplete(body, readErr, s.upstreamErrorBodyReadLimit())
		return nil, &UpstreamFailoverError{
			StatusCode:                resp.StatusCode,
			ResponseBody:              body,
			RequestHeaders:            captureRequestHeadersFromResponse(resp),
			ResponseHeaders:           resp.Header.Clone(),
			UpstreamEndpoint:          captureEndpointFromResponse(resp),
			HasUpstreamHTTPResponse:   true,
			CaptureResponseIncomplete: !responseComplete,
		}
	}

	MarkResponseCommitted(c)
	model := ""
	if len(requestedModel) > 0 {
		model = requestedModel[0]
	}
	typedCaptureResult := terminalHTTPErrorForwardResult(c, resp, model, model, false, startedAt, boundedUpstreamErrorResponseComplete(body, readErr, s.upstreamErrorBodyReadLimit()))

	// 归档采集（错误响应）：仅在此终态提交——failover 重试路径在上面 shouldDisable 分支已返回，
	// 不会走到这里，故不会归档中间重试。drop-safe，绝不影响转发。
	if s.capturePool != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && !captureStreamingAttemptPath(c) {
		failure := &UpstreamFailoverError{
			StatusCode:              resp.StatusCode,
			ResponseBody:            body,
			RequestHeaders:          captureRequestHeadersFromResponse(resp),
			ResponseHeaders:         resp.Header.Clone(),
			UpstreamEndpoint:        captureEndpointFromResponse(resp),
			Platform:                string(account.Platform),
			HasUpstreamHTTPResponse: true,
		}
		if rec := BuildTerminalErrorCaptureRecord(c, string(account.Platform), failure, s.cfg.Gateway.Capture.MaxBodyBytes); rec != nil {
			s.capturePool.Submit(rec)
		}
	}

	// 记录上游错误响应体摘要便于排障（可选：由配置控制；不回显到客户端）
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.gateway",
			"Upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	// 非 failover 错误也支持错误透传规则匹配。
	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return typedCaptureResult, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return typedCaptureResult, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, summary)
	}

	// 根据状态码返回适当的自定义错误响应（不透传上游详细信息）
	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case 400:
		c.Data(http.StatusBadRequest, "application/json", body)
		summary := upstreamMsg
		if summary == "" {
			summary = truncateForLog(body, 512)
		}
		if summary == "" {
			return typedCaptureResult, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return typedCaptureResult, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, summary)
	case 401:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded, please retry later"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream service temporarily unavailable"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	// 返回自定义错误响应
	c.JSON(statusCode, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": errMsg,
		},
	})

	if upstreamMsg == "" {
		return typedCaptureResult, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return typedCaptureResult, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

func (s *GatewayService) handleRetryExhaustedSideEffects(ctx context.Context, resp *http.Response, account *Account) {
	body, _ := s.readUpstreamErrorBody(resp)
	statusCode := resp.StatusCode

	// OAuth/Setup Token 账号的 403：标记账号异常
	if account.IsOAuth() && statusCode == 403 {
		s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, resp.Header, body)
		logger.LegacyPrintf("service.gateway", "Account %d: marked as error after %d retries for status %d", account.ID, maxRetryAttempts, statusCode)
	} else {
		// API Key 未配置错误码：不标记账号状态
		logger.LegacyPrintf("service.gateway", "Account %d: upstream error %d after %d retries (not marking account)", account.ID, statusCode, maxRetryAttempts)
	}
}

func (s *GatewayService) handleFailoverSideEffects(ctx context.Context, resp *http.Response, account *Account, requestedModel ...string) {
	body, _ := s.readUpstreamErrorBody(resp)
	if len(requestedModel) > 0 {
		s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel[0])
		return
	}
	s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
}

// handleRetryExhaustedError 处理重试耗尽后的错误
// OAuth 403：标记账号异常
// API Key 未配置错误码：仅返回错误，不标记账号
func (s *GatewayService) handleRetryExhaustedError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*ForwardResult, error) {
	startedAt := time.Now()
	MarkResponseCommitted(c)
	// Capture upstream error body before side-effects consume the stream.
	respBody, readErr := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	typedCaptureResult := terminalHTTPErrorForwardResult(c, resp, "", "", false, startedAt, boundedUpstreamErrorResponseComplete(respBody, readErr, s.upstreamErrorBodyReadLimit()))

	s.handleRetryExhaustedSideEffects(ctx, resp, account)

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

	if isClaudeCodeCredentialScopeError(upstreamMsg) && c != nil {
		if v, ok := c.Get(claudeMimicDebugInfoKey); ok {
			if line, ok := v.(string); ok && strings.TrimSpace(line) != "" {
				logger.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s",
					resp.StatusCode,
					resp.Header.Get("x-request-id"),
					line,
				)
			}
		}
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(respBody), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "retry_exhausted",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// This path owns a final provider HTTP response even though the account's
	// custom error-code policy classified it as retryable and all retries were
	// exhausted. It returns a normal communicated error rather than a typed
	// failover, so the handler has no later terminal sink; archive it here once.
	if s.capturePool != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && !captureStreamingAttemptPath(c) {
		failure := &UpstreamFailoverError{
			StatusCode:              resp.StatusCode,
			ResponseBody:            respBody,
			RequestHeaders:          captureRequestHeadersFromResponse(resp),
			ResponseHeaders:         resp.Header.Clone(),
			UpstreamEndpoint:        captureEndpointFromResponse(resp),
			Platform:                string(account.Platform),
			HasUpstreamHTTPResponse: true,
		}
		if rec := BuildTerminalErrorCaptureRecord(c, string(account.Platform), failure, s.cfg.Gateway.Capture.MaxBodyBytes); rec != nil {
			s.capturePool.Submit(rec)
		}
	}

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.gateway",
			"Upstream error %d retries_exhausted (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(respBody, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		respBody,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed after retries",
	); matched {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return typedCaptureResult, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched)", resp.StatusCode)
		}
		return typedCaptureResult, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched) message=%s", resp.StatusCode, summary)
	}

	// 返回统一的重试耗尽错误响应
	c.JSON(http.StatusBadGateway, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream request failed after retries",
		},
	})

	if upstreamMsg == "" {
		return typedCaptureResult, fmt.Errorf("upstream error: %d (retries exhausted)", resp.StatusCode)
	}
	return typedCaptureResult, fmt.Errorf("upstream error: %d (retries exhausted) message=%s", resp.StatusCode, upstreamMsg)
}

// streamingResult 流式响应结果
type streamingResult struct {
	usage            *ClaudeUsage
	firstTokenMs     *int
	clientDisconnect bool // 客户端是否在流式传输过程中断开
	semanticOutput   bool // 是否已向客户端提交实际文本/工具等语义输出
	responseComplete bool // 是否观测到上游官方 terminal 事件
}

// partialStreamUsageResult 在流式转发中途出错时，把已经提交语义输出的部分结果包装为
// ForwardResult（与错误一起返回给 handler 记录）。语义输出可能先于 usage 到达，因此
// 即使 token 仍为零也必须保留一次 capture；反之仅有 usage/preamble 仍可安全 failover。
//
// 不变式：UpstreamFailoverError 必须保持 result=nil——failover 重试成功后按成功请求
// 计费，若同时返回部分 usage 会造成双重计费，此处显式拦截兜底。
func partialStreamUsageResult(ctx context.Context, c *gin.Context, resp *http.Response, streamResult *streamingResult, model, upstreamModel string, startTime time.Time, err error) *ForwardResult {
	if streamResult == nil {
		return nil
	}
	clientCancellation := isClientCausalCancellation(ctx, err, streamResult.clientDisconnect)
	if !streamResult.semanticOutput && !clientCancellation {
		return nil
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return nil
	}
	return attachCaptureToForwardResult(c, &ForwardResult{
		RequestID:                     resp.Header.Get("x-request-id"),
		Usage:                         *streamResult.usage,
		Model:                         model,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Stream:                        true,
		Duration:                      time.Since(startTime),
		FirstTokenMs:                  streamResult.firstTokenMs,
		ClientDisconnect:              streamResult.clientDisconnect,
		CaptureTerminalError:          !clientCancellation,
		CaptureResponseComplete:       streamResult.responseComplete,
	})
}

func isClientCausalCancellation(ctx context.Context, err error, clientDisconnect bool) bool {
	return clientDisconnect && ctx != nil && errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled)
}

// anthropicSSEEventHasSemanticOutput reports whether an Anthropic SSE event
// exposes model output that the caller can act on. Protocol preambles,
// keepalive frames and usage-only events deliberately do not cross the retry
// boundary: they remain staged until semantic output or terminal success.
func anthropicSSEEventHasSemanticOutput(data string) bool {
	if data == "" || data == "[DONE]" || !gjson.Valid(data) {
		return false
	}
	event := gjson.Parse(data)
	eventType := event.Get("type").String()
	switch eventType {
	case "content_block_start":
		block := event.Get("content_block")
		blockType := block.Get("type").String()
		switch blockType {
		case "tool_use", "server_tool_use":
			// A non-empty tool name is visible semantic output before its JSON
			// arguments arrive. A generated/id-only empty tool element is not.
			return block.Get("name").Type == gjson.String && block.Get("name").String() != ""
		case "text":
			return block.Get("text").Type == gjson.String && block.Get("text").String() != ""
		case "thinking":
			return block.Get("thinking").Type == gjson.String && block.Get("thinking").String() != ""
		case "redacted_thinking":
			return block.Get("data").Type == gjson.String && block.Get("data").String() != ""
		}
	case "content_block_delta":
		delta := event.Get("delta")
		deltaType := delta.Get("type").String()
		switch deltaType {
		case "text_delta":
			return delta.Get("text").Type == gjson.String && delta.Get("text").String() != ""
		case "thinking_delta":
			return delta.Get("thinking").Type == gjson.String && delta.Get("thinking").String() != ""
		case "input_json_delta":
			return delta.Get("partial_json").Type == gjson.String && delta.Get("partial_json").String() != ""
		}
	}
	return false
}

func anthropicSSEBytesHaveSemanticOutput(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		payload, ok := parseAnthropicSSEField(strings.TrimSpace(line), "data")
		if ok && anthropicSSEEventHasSemanticOutput(payload) {
			return true
		}
	}
	return false
}

func (s *GatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel string, mimicClaudeCode bool) (*streamingResult, error) {
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
	// 更新5h窗口状态
	s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}

	// 设置SSE响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 透传其他响应头
	if v := resp.Header.Get("x-request-id"); v != "" {
		c.Header("x-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	// 归档采集：仅 gateway.capture.enabled=true 时在上游 SSE 读取 goroutine 里逐行累积原始字节。
	// tee 加锁保护；此 defer 在函数返回时读取已到达的字节写入 gin.Context 桥，供上层 Forward 组装。
	var tee *sseTee
	semanticOutput := false
	sawTerminalEvent := false
	providerPayloadObserved := false
	kiroTranslatedBody, providerNativeCapture := resp.Body.(*kiroTranslatedStreamBody)
	_, legacyRawProviderCapture := resp.Body.(*captureBodyReadCloser)
	_, streamingAttemptCapture := resp.Body.(*captureResponseReader)
	rawProviderCapture := legacyRawProviderCapture || streamingAttemptCapture || captureStreamingAttemptPath(c)
	stageSyntheticKiroWebSearchEvents := providerNativeCapture && kiroTranslatedBody.stageSyntheticWebSearchEvents
	stagedKiroWebSearchBlockIndexes := make(map[int]struct{})
	if !rawProviderCapture && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil && CaptureMayApplyFor(c, string(account.Platform)) {
		setCapturePlatform(c, string(account.Platform))
		tee = newSSETee(s.cfg.Gateway.Capture.MaxBodyBytes)
		defer func() {
			if semanticOutput || sawTerminalEvent {
				b, tr := tee.bytes()
				if !providerNativeCapture {
					setCaptureResult(c, resp, b, tr)
				}
			}
		}()
	}

	usage := &ClaudeUsage{}
	var firstTokenMs *int
	providerReader, readActivity := providerBodyReaderWithActivity(resp.Body)
	scanner := bufio.NewScanner(providerReader)
	// 设置更大的buffer以处理长行
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
	// 独立 goroutine 读取上游，避免读取阻塞导致超时/keepalive无法处理
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
	providerScanFinished := false
	defer func() {
		if !providerScanFinished {
			drainCaptureScannerOnParserFailure(ctx, resp, events, scanDone, &readActivity.lastRead, 0, nil, func() {
				close(done)
			})
			return
		}
		close(done)
		// Scanner has no context-aware Read API. Closing the owned response body
		// is what interrupts a blocked network read; joining the goroutine keeps a
		// canceled/overflowed attempt from leaking into the next account attempt.
		closeCaptureResponseAndJoinScanner(resp, scanDone)
	}()

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	// 仅监控上游数据间隔超时，避免下游写入阻塞导致误判
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
	keepaliveInterval := s.streamKeepaliveIntervalForAccount(account)
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

	// 仅发送一次错误事件，避免多次写入导致协议混乱（写失败时尽力通知客户端）。
	// 事件格式遵循 Anthropic SSE 标准：{"type":"error","error":{"type":<reason>,"message":<message>}}
	// 这样 Anthropic SDK / Claude Code 等客户端能按标准 error 类型解析，UI 能显示具体错误文案，
	// 服务端 ExtractUpstreamErrorMessage 也能从透传的 body 中提取 message。
	errorEventSent := false
	sendErrorEvent := func(reason, message string) {
		if errorEventSent {
			return
		}
		errorEventSent = true
		if message == "" {
			message = reason
		}
		body, err := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    reason,
				"message": message,
			},
		})
		if err != nil {
			// json.Marshal 不可能在已知 string-only 输入上失败，保守 fallback
			body = []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, reason, message))
		}
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", body)
		flusher.Flush()
	}

	needModelReplace := originalModel != mappedModel
	clientDisconnected := false // 客户端断开标志，断开后继续读取上游以获取完整usage
	useNoopDeltaKeepalive := c != nil && c.Request != nil && shouldUseClaudeCodeNoopDeltaKeepalive(c.GetHeader("User-Agent"))
	noopDeltaKeepaliveBlockIndex := -1
	noopDeltaKeepaliveDeltaType := ""

	pendingEventLines := make([]string, 0, 4)
	pendingEventBytes := 0

	processSSEEvent := func(lines []string) ([]string, string, *sseUsagePatch, bool, error) {
		if len(lines) == 0 {
			return nil, "", nil, false, nil
		}

		eventName := ""
		dataLine := ""
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
				continue
			}
			if dataLine == "" && sseDataRe.MatchString(trimmed) {
				dataLine = sseDataRe.ReplaceAllString(trimmed, "")
			}
		}

		if eventName == "error" {
			return nil, dataLine, nil, false, &sseStreamErrorEventError{RawData: dataLine}
		}

		if dataLine == "" {
			return []string{strings.Join(lines, "\n") + "\n\n"}, "", nil, false, nil
		}

		if dataLine == "[DONE]" {
			sawTerminalEvent = true
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, nil, false, nil
		}

		event := gjson.Parse(dataLine)
		eventType := event.Get("type").String()
		eventHasSemanticOutput := anthropicSSEEventHasSemanticOutput(dataLine)
		eventIndex := event.Get("index")
		hasEventIndex := eventIndex.Exists() && nonNegativeIntegerGJSON(eventIndex)
		if stageSyntheticKiroWebSearchEvents {
			if hasEventIndex {
				index := int(eventIndex.Int())
				switch eventType {
				case "content_block_start":
					blockType := event.Get("content_block.type").String()
					blockName := event.Get("content_block.name").String()
					if blockType == "web_search_tool_result" ||
						(blockType == "server_tool_use" && strings.EqualFold(strings.TrimSpace(blockName), "web_search")) {
						stagedKiroWebSearchBlockIndexes[index] = struct{}{}
					}
				case "content_block_stop":
					defer delete(stagedKiroWebSearchBlockIndexes, index)
				}
				if _, staged := stagedKiroWebSearchBlockIndexes[index]; staged {
					eventHasSemanticOutput = false
				}
			}
		}
		observer.ObserveAnthropic([]byte(dataLine))
		if eventName == "" {
			eventName = eventType
		}
		if useNoopDeltaKeepalive {
			switch eventType {
			case "content_block_start":
				if hasEventIndex {
					idx := int(eventIndex.Int())
					noopDeltaKeepaliveBlockIndex = -1
					noopDeltaKeepaliveDeltaType = ""
					if deltaType := claudeCodeKeepaliveDeltaTypeForContentBlock(event.Get("content_block.type").String()); deltaType != "" {
						noopDeltaKeepaliveBlockIndex = idx
						noopDeltaKeepaliveDeltaType = deltaType
					}
				}
			case "content_block_delta":
				if hasEventIndex {
					idx := int(eventIndex.Int())
					deltaType := event.Get("delta.type").String()
					if claudeCodeKeepaliveFieldForDeltaType(deltaType) != "" {
						noopDeltaKeepaliveBlockIndex = idx
						noopDeltaKeepaliveDeltaType = deltaType
					}
				}
			case "content_block_stop":
				if hasEventIndex && int(eventIndex.Int()) == noopDeltaKeepaliveBlockIndex {
					noopDeltaKeepaliveBlockIndex = -1
					noopDeltaKeepaliveDeltaType = ""
				}
			case "message_stop":
				noopDeltaKeepaliveBlockIndex = -1
				noopDeltaKeepaliveDeltaType = ""
			}
		}

		updatedData := []byte(dataLine)
		eventChanged := false
		setJSONValue := func(path string, value any) {
			if next, err := sjson.SetBytes(updatedData, path, value); err == nil {
				updatedData = next
				eventChanged = true
			}
		}
		usagePath := "usage"
		if eventType == "message_start" {
			providerPayloadObserved = true
			usagePath = "message.usage"
		}
		// 兼容 Kimi cached_tokens → cache_read_input_tokens.
		cacheRead := event.Get(usagePath + ".cache_read_input_tokens")
		cached := event.Get(usagePath + ".cached_tokens")
		if cacheRead.Int() <= 0 && cached.Int() > 0 {
			setJSONValue(usagePath+".cache_read_input_tokens", cached.Int())
		}

		// Cache TTL Override: 重写 SSE 事件中的 cache_creation 分类。
		// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
		if overrideTarget, ok := s.resolveCacheTTLUsageOverrideTarget(ctx, account); ok {
			fiveMinute := event.Get(usagePath + ".cache_creation.ephemeral_5m_input_tokens").Int()
			oneHour := event.Get(usagePath + ".cache_creation.ephemeral_1h_input_tokens").Int()
			total := fiveMinute + oneHour
			if total > 0 && ((overrideTarget == "1h" && oneHour != total) || (overrideTarget != "1h" && fiveMinute != total)) {
				if overrideTarget == "1h" {
					setJSONValue(usagePath+".cache_creation.ephemeral_1h_input_tokens", total)
					setJSONValue(usagePath+".cache_creation.ephemeral_5m_input_tokens", 0)
				} else {
					setJSONValue(usagePath+".cache_creation.ephemeral_5m_input_tokens", total)
					setJSONValue(usagePath+".cache_creation.ephemeral_1h_input_tokens", 0)
				}
			}
		}

		if needModelReplace && event.Get("message.model").String() == mappedModel {
			setJSONValue("message.model", originalModel)
		}

		usagePatch := extractSSEUsagePatchFromGJSON(gjson.ParseBytes(updatedData))
		for _, path := range []string{usagePath + "._sub2api_kiro_credits", usagePath + "." + kiroFinalUsageSSEField} {
			if gjson.GetBytes(updatedData, path).Exists() {
				if next, err := sjson.DeleteBytes(updatedData, path); err == nil {
					updatedData = next
					eventChanged = true
				}
			}
		}
		if anthropicStreamEventIsTerminal(eventName, dataLine) {
			sawTerminalEvent = true
		}
		if !eventChanged {
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, usagePatch, eventHasSemanticOutput, nil
		}

		block := ""
		if eventName != "" {
			block = "event: " + eventName + "\n"
		}
		block += "data: " + string(updatedData) + "\n\n"
		emittedData := string(updatedData)
		return []string{block}, emittedData, usagePatch, eventHasSemanticOutput, nil
	}

	// Reuse the established OpenAI first-output spool: 64 KiB stays in memory,
	// larger preambles spill to an unlinked temp file, and the total attempt-local
	// stage is capped at openAIFirstOutputStageMaxBytes (8 MiB).
	stagedOutput := newDefaultOpenAIFirstOutputStage()
	defer func() { _ = stagedOutput.Close() }()
	outputCommitted := false
	writeOutput := func(block string, commitsSemanticOutput, terminal bool) error {
		if clientDisconnected {
			return nil
		}
		restored := reverseToolNamesIfPresent(c, []byte(block))
		if !semanticOutput {
			if _, err := stagedOutput.Write(restored); err != nil {
				return fmt.Errorf("stage Anthropic output: %w", err)
			}
			if commitsSemanticOutput {
				semanticOutput = true
				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}
			}
			if !semanticOutput && (!terminal || !providerPayloadObserved) {
				return nil
			}
			_, commitErr := stagedOutput.CommitTo(w)
			outputCommitted = true
			deliveryErr, cleanupErr := splitOpenAIFirstOutputCommitError(commitErr)
			if cleanupErr != nil {
				logger.LegacyPrintf("service.gateway", "Anthropic first-output staging cleanup failed after commit: account=%d error=%v", account.ID, cleanupErr)
			}
			if deliveryErr != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "Client disconnected during streaming, continuing to drain upstream for billing: error=%v", deliveryErr)
				return nil
			}
		} else {
			if _, werr := w.Write(restored); werr != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "Client disconnected during streaming, continuing to drain upstream for billing")
				return nil
			}
		}
		flusher.Flush()
		lastDataAt = time.Now()
		resetKeepaliveTimer()
		return nil
	}

	streamResult := func() *streamingResult {
		return &streamingResult{
			usage:            usage,
			firstTokenMs:     firstTokenMs,
			clientDisconnect: clientDisconnected,
			semanticOutput:   semanticOutput,
			responseComplete: sawTerminalEvent,
		}
	}
	preOutputFailover := func(message string, retryable bool) error {
		failure := newIncompleteProviderStreamFailover(resp, message)
		failure.RetryableOnSameAccount = retryable
		return failure
	}
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				providerScanFinished = true
				pendingEventLines = nil
				// 上游完成，返回结果
				return streamResult(), nil
			}
			if ev.err != nil {
				requestCanceled := ctx != nil && errors.Is(ctx.Err(), context.Canceled)
				if isClientCausalCancellation(ctx, ev.err, clientDisconnected || requestCanceled) {
					clientDisconnected = true
					if !semanticOutput && captureAttemptForRequest(c) == nil {
						return nil, preOutputFailover("upstream stream canceled: "+sanitizeStreamError(ev.err), false)
					}
					return streamResult(), fmt.Errorf("stream usage incomplete: %w", ev.err)
				}
				if !outputCommitted && !semanticOutput {
					// Adapter-owned pipe bodies may carry a fully classified terminal
					// provider HTTP failure (for example KIRO only-WebSearch after the
					// final AWS request). Preserve that typed error and its final-attempt
					// metadata instead of collapsing it into a generic retryable stream
					// disconnect, which would lose status/headers/capture ownership.
					var providerHTTPFailure *UpstreamFailoverError
					if errors.As(ev.err, &providerHTTPFailure) && providerHTTPFailure.HasUpstreamHTTPResponse {
						return nil, providerHTTPFailure
					}
					disconnectMsg := "upstream stream disconnected: " + sanitizeStreamError(ev.err)
					logger.LegacyPrintf("service.gateway", "Upstream stream read error before semantic output (account=%d), failing over: %v", account.ID, ev.err)
					return nil, preOutputFailover(disconnectMsg, true)
				}
				// 检测 context 取消（客户端断开会导致 context 取消，进而影响上游读取）
				if errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return streamResult(), fmt.Errorf("stream usage incomplete: %w", ev.err)
				}
				// 客户端已通过写入失败检测到断开，上游也出错了，返回已收集的 usage
				if clientDisconnected {
					return streamResult(), fmt.Errorf("stream usage incomplete after disconnect: %w", ev.err)
				}
				// 客户端未断开，正常的错误处理
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.gateway", "SSE line too long: account=%d max_size=%d error=%v", account.ID, maxLineSize, ev.err)
					sendErrorEvent("response_too_large", fmt.Sprintf("upstream SSE line exceeded %d bytes", maxLineSize))
					return streamResult(), ev.err
				}
				// 上游中途读错误（unexpected EOF / connection reset 等，常见于 HTTP/2 GOAWAY）：
				// 若尚未向客户端写过任何字节，包成 UpstreamFailoverError 让 handler 层走 failover/重试。
				// 已经开始写流时 SSE 协议无 resume，只能透传错误事件给客户端。
				// 注意:面向客户端的 disconnectMsg 必须用 sanitizeStreamError 剥离地址,
				// 默认 *net.OpError 的 Error() 会泄露内部 IP/端口和上游地址。完整 ev.err
				// 仅在下方 LegacyPrintf 内部日志中保留供运维诊断。
				disconnectMsg := "upstream stream disconnected: " + sanitizeStreamError(ev.err)
				sendErrorEvent("stream_read_error", disconnectMsg)
				return streamResult(), fmt.Errorf("stream read error: %w", ev.err)
			}
			line := ev.line
			trimmed := strings.TrimSpace(line)

			if trimmed == "" {
				if len(pendingEventLines) == 0 {
					continue
				}

				outputBlocks, data, usagePatch, eventHasSemanticOutput, err := processSSEEvent(pendingEventLines)
				pendingEventLines = pendingEventLines[:0]
				pendingEventBytes = 0
				if err != nil {
					if semanticOutput {
						return streamResult(), err
					}
					var sseErr *sseStreamErrorEventError
					if errors.As(err, &sseErr) {
						// Match upstream's commit boundary: protocol/preamble bytes that
						// preceded event:error are already part of the downstream stream.
						// The caller classifies overload as 529 only when nothing was sent.
						if !outputCommitted && !clientDisconnected && stagedOutput.Buffered() > 0 {
							_, commitErr := stagedOutput.CommitTo(w)
							outputCommitted = true
							deliveryErr, cleanupErr := splitOpenAIFirstOutputCommitError(commitErr)
							if cleanupErr != nil {
								logger.LegacyPrintf("service.gateway", "Anthropic first-output staging cleanup failed before SSE error: account=%d error=%v", account.ID, cleanupErr)
							}
							if deliveryErr != nil {
								clientDisconnected = true
								return streamResult(), errors.Join(err, deliveryErr)
							}
							flusher.Flush()
							lastDataAt = time.Now()
							resetKeepaliveTimer()
						}
						return nil, sseErr
					}
					return nil, preOutputFailover(sanitizeStreamError(err), true)
				}

				for _, block := range outputBlocks {
					if writeErr := writeOutput(block, eventHasSemanticOutput, sawTerminalEvent); writeErr != nil {
						if !semanticOutput {
							return nil, preOutputFailover(sanitizeStreamError(writeErr), true)
						}
						return streamResult(), writeErr
					}
				}
				if data != "" && usagePatch != nil {
					mergeSSEUsagePatch(usage, usagePatch)
				}
				continue
			}

			lineBytes := len(line) + 1 // Scanner removes the newline delimiter.
			if lineBytes > openAIFirstOutputStageMaxBytes-pendingEventBytes {
				limitErr := fmt.Errorf("anthropic SSE event exceeded %d bytes", openAIFirstOutputStageMaxBytes)
				if !semanticOutput {
					return nil, preOutputFailover(limitErr.Error(), true)
				}
				return streamResult(), limitErr
			}
			pendingEventLines = append(pendingEventLines, line)
			pendingEventBytes += lineBytes

		case <-ctxDone:
			cancelErr := ctx.Err()
			if cancelErr == nil {
				cancelErr = context.Canceled
			}
			clientDisconnected = true
			if !semanticOutput && captureAttemptForRequest(c) == nil {
				return nil, preOutputFailover("upstream stream canceled: "+sanitizeStreamError(cancelErr), false)
			}
			return streamResult(), fmt.Errorf("stream usage incomplete: %w", cancelErr)

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return streamResult(), fmt.Errorf("stream usage incomplete after timeout")
			}
			logger.LegacyPrintf("service.gateway", "Stream data interval timeout: account=%d model=%s interval=%s", account.ID, originalModel, streamInterval)
			// 处理流超时，可能标记账户为临时不可调度或错误状态
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, originalModel)
			}
			if !semanticOutput {
				return nil, preOutputFailover(fmt.Sprintf("upstream stream idle for %s", streamInterval), true)
			}
			sendErrorEvent("stream_timeout", fmt.Sprintf("upstream stream idle for %s", streamInterval))
			return streamResult(), fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if !semanticOutput {
				resetKeepaliveTimer()
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				resetKeepaliveTimer()
				continue
			}
			keepaliveBlock := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
			if useNoopDeltaKeepalive && noopDeltaKeepaliveBlockIndex >= 0 {
				if block, ok := buildClaudeCodeNoopDeltaKeepalive(noopDeltaKeepaliveBlockIndex, noopDeltaKeepaliveDeltaType); ok {
					keepaliveBlock = block
				}
			}
			if _, werr := fmt.Fprint(w, keepaliveBlock); werr != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "Client disconnected during keepalive ping, continuing to drain upstream for billing")
				continue
			}
			flusher.Flush()
			lastDataAt = time.Now()
			resetKeepaliveTimer()
		}
	}

}

func nonNegativeIntegerGJSON(value gjson.Result) bool {
	if value.Type != gjson.Number {
		return false
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value.Raw), 10, strconv.IntSize)
	return err == nil && parsed >= 0
}

func checkedAddNonNegativeGJSON(left, right gjson.Result) (int64, bool) {
	if left.Exists() && !nonNegativeIntegerGJSON(left) {
		return 0, false
	}
	if right.Exists() && !nonNegativeIntegerGJSON(right) {
		return 0, false
	}
	leftValue, rightValue := left.Int(), right.Int()
	if leftValue > int64(^uint(0)>>1)-rightValue {
		return 0, false
	}
	return leftValue + rightValue, true
}

func (s *GatewayService) parseSSEUsage(data string, usage *ClaudeUsage) {
	if usage == nil {
		return
	}
	event := gjson.Parse(data)
	if !event.IsObject() {
		return
	}
	if patch := extractSSEUsagePatchFromGJSON(event); patch != nil {
		mergeSSEUsagePatch(usage, patch)
	}
}

type sseUsagePatch struct {
	inputTokens              int
	hasInputTokens           bool
	outputTokens             int
	hasOutputTokens          bool
	cacheCreationInputTokens int
	hasCacheCreationInput    bool
	cacheReadInputTokens     int
	hasCacheReadInput        bool
	cacheCreation5mTokens    int
	hasCacheCreation5m       bool
	cacheCreation1hTokens    int
	hasCacheCreation1h       bool
	kiroCredits              float64
	hasKiroCredits           bool
}

const kiroFinalUsageSSEField = "_sub2api_kiro_final_usage"

func extractSSEUsagePatchFromGJSON(event gjson.Result) *sseUsagePatch {
	eventType := strings.TrimSpace(event.Get("type").String())
	usage := event.Get("usage")
	if eventType == "message_start" {
		usage = event.Get("message.usage")
	}
	if !usage.IsObject() || !gjsonCollectionHasValues(usage) {
		return nil
	}
	patch := &sseUsagePatch{}
	setInt := func(path string, value *int, present *bool, allowZero bool) {
		field := usage.Get(path)
		if !field.Exists() {
			return
		}
		parsed := int(field.Int())
		if parsed > 0 || allowZero {
			*value = parsed
			*present = true
		}
	}
	finalUsage := usage.Get(kiroFinalUsageSSEField).Bool()
	switch eventType {
	case "message_start":
		// Preserve the established message_start behavior: a present usage
		// object authoritatively initializes the three input-side counters,
		// including their zero values.
		patch.hasInputTokens = true
		patch.hasCacheCreationInput = true
		patch.hasCacheReadInput = true
		patch.inputTokens = int(usage.Get("input_tokens").Int())
		patch.cacheCreationInputTokens = int(usage.Get("cache_creation_input_tokens").Int())
		patch.cacheReadInputTokens = int(usage.Get("cache_read_input_tokens").Int())
	case "message_delta":
		setInt("input_tokens", &patch.inputTokens, &patch.hasInputTokens, finalUsage)
		setInt("output_tokens", &patch.outputTokens, &patch.hasOutputTokens, finalUsage)
		setInt("cache_creation_input_tokens", &patch.cacheCreationInputTokens, &patch.hasCacheCreationInput, finalUsage)
		setInt("cache_read_input_tokens", &patch.cacheReadInputTokens, &patch.hasCacheReadInput, finalUsage)
		if finalUsage {
			patch.hasCacheCreation5m = true
			patch.hasCacheCreation1h = true
		}
	default:
		return nil
	}
	setInt("cache_creation.ephemeral_5m_input_tokens", &patch.cacheCreation5mTokens, &patch.hasCacheCreation5m, eventType == "message_start" || finalUsage)
	setInt("cache_creation.ephemeral_1h_input_tokens", &patch.cacheCreation1hTokens, &patch.hasCacheCreation1h, eventType == "message_start" || finalUsage)
	if credits := usage.Get("_sub2api_kiro_credits"); credits.Exists() && credits.Float() > 0 {
		patch.kiroCredits = credits.Float()
		patch.hasKiroCredits = true
	}
	return patch
}

func mergeSSEUsagePatch(usage *ClaudeUsage, patch *sseUsagePatch) {
	if usage == nil || patch == nil {
		return
	}

	if patch.hasInputTokens {
		usage.InputTokens = patch.inputTokens
	}
	if patch.hasCacheCreationInput {
		usage.CacheCreationInputTokens = patch.cacheCreationInputTokens
	}
	if patch.hasCacheReadInput {
		usage.CacheReadInputTokens = patch.cacheReadInputTokens
	}
	if patch.hasOutputTokens {
		usage.OutputTokens = patch.outputTokens
	}
	if patch.hasCacheCreation5m {
		usage.CacheCreation5mTokens = patch.cacheCreation5mTokens
	}
	if patch.hasCacheCreation1h {
		usage.CacheCreation1hTokens = patch.cacheCreation1hTokens
	}
	if patch.hasKiroCredits {
		usage.KiroCredits = patch.kiroCredits
	}
}

// applyCacheTTLOverride 将所有 cache creation tokens 归入指定的 TTL 类型。
// target 为 "5m" 或 "1h"。返回 true 表示发生了变更。
func applyCacheTTLOverride(usage *ClaudeUsage, target string) bool {
	// Fallback: 如果只有聚合字段但无 5m/1h 明细，将聚合字段归入 5m 默认类别
	if usage.CacheCreation5mTokens == 0 && usage.CacheCreation1hTokens == 0 && usage.CacheCreationInputTokens > 0 {
		usage.CacheCreation5mTokens = usage.CacheCreationInputTokens
	}

	total := usage.CacheCreation5mTokens + usage.CacheCreation1hTokens
	if total == 0 {
		return false
	}
	switch target {
	case "1h":
		if usage.CacheCreation1hTokens == total {
			return false // 已经全是 1h
		}
		usage.CacheCreation1hTokens = total
		usage.CacheCreation5mTokens = 0
	default: // "5m"
		if usage.CacheCreation5mTokens == total {
			return false // 已经全是 5m
		}
		usage.CacheCreation5mTokens = total
		usage.CacheCreation1hTokens = 0
	}
	return true
}

func (s *GatewayService) resolveCacheTTLUsageOverrideTarget(ctx context.Context, account *Account) (string, bool) {
	if account == nil {
		return "", false
	}
	if account.IsCacheTTLOverrideEnabled() {
		return account.GetCacheTTLOverrideTarget(), true
	}
	if account.IsAnthropicOAuthOrSetupToken() && s != nil && s.settingService != nil && s.settingService.IsAnthropicCacheTTL1hInjectionEnabled(ctx) {
		return cacheTTLTarget5m, true
	}
	return "", false
}

func (s *GatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, originalModel, mappedModel string) (*ClaudeUsage, error) {
	// 更新5h窗口状态
	s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)

	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if err != nil {
		return nil, errors.Join(newInvalidProviderResponseFailover(resp, "failed to read upstream Anthropic response"), err)
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveAnthropic(body)

	// 归档采集：在任何改写（model/tool 名还原、Kimi/cache-TTL usage 规整）之前，
	// 快照上游原始响应体，保证与流式 tee 一样是"逐字上游原文"（零成本：关闭时不分配）。
	if !captureStreamingAttemptPath(c) && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil && CaptureMayApplyFor(c, string(account.Platform)) {
		capturedResp, truncated := captureWithLimit(body, s.cfg.Gateway.Capture.MaxBodyBytes)
		setCaptureResult(c, resp, capturedResp, truncated)
	}

	// 解析usage
	var response struct {
		Usage ClaudeUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return nil, invalidNonStreamingJSONFailoverError(ctx, s.rateLimitService, resp, account, body, err, mappedModel)
		}
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// 解析嵌套的 cache_creation 对象中的 5m/1h 明细
	cc5m := gjson.GetBytes(body, "usage.cache_creation.ephemeral_5m_input_tokens")
	cc1h := gjson.GetBytes(body, "usage.cache_creation.ephemeral_1h_input_tokens")
	if cc5m.Exists() || cc1h.Exists() {
		response.Usage.CacheCreation5mTokens = int(cc5m.Int())
		response.Usage.CacheCreation1hTokens = int(cc1h.Int())
	}

	// 兼容 Kimi cached_tokens → cache_read_input_tokens
	if response.Usage.CacheReadInputTokens == 0 {
		cachedTokens := gjson.GetBytes(body, "usage.cached_tokens").Int()
		if cachedTokens > 0 {
			response.Usage.CacheReadInputTokens = int(cachedTokens)
			if newBody, err := sjson.SetBytes(body, "usage.cache_read_input_tokens", cachedTokens); err == nil {
				body = newBody
			}
		}
	}

	// Cache TTL Override: 重写 non-streaming 响应中的 cache_creation 分类。
	// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
	if overrideTarget, ok := s.resolveCacheTTLUsageOverrideTarget(ctx, account); ok {
		if applyCacheTTLOverride(&response.Usage, overrideTarget) {
			// 同步更新 body JSON 中的嵌套 cache_creation 对象
			if newBody, err := sjson.SetBytes(body, "usage.cache_creation.ephemeral_5m_input_tokens", response.Usage.CacheCreation5mTokens); err == nil {
				body = newBody
			}
			if newBody, err := sjson.SetBytes(body, "usage.cache_creation.ephemeral_1h_input_tokens", response.Usage.CacheCreation1hTokens); err == nil {
				body = newBody
			}
		}
	}

	// 如果有模型映射，替换响应中的model字段
	if originalModel != mappedModel {
		body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
	}

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := "application/json"
	if s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}

	body = reverseToolNamesIfPresent(c, body)

	// 写入响应
	c.Data(resp.StatusCode, contentType, body)

	return &response.Usage, nil
}

// replaceModelInResponseBody 替换响应体中的model字段
// 使用 gjson/sjson 精确替换，避免全量 JSON 反序列化
func (s *GatewayService) replaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	if m := gjson.GetBytes(body, "model"); m.Exists() && m.Str == fromModel {
		newBody, err := sjson.SetBytes(body, "model", toModel)
		if err != nil {
			return body
		}
		return newBody
	}
	return body
}

// reconcileCachedTokens 兼容 Kimi 等上游：
// 将 OpenAI 风格的 cached_tokens 映射到 Claude 标准的 cache_read_input_tokens
func reconcileCachedTokens(usage map[string]any) bool {
	if usage == nil {
		return false
	}
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if cacheRead > 0 {
		return false // 已有标准字段，无需处理
	}
	cached, _ := usage["cached_tokens"].(float64)
	if cached <= 0 {
		return false
	}
	usage["cache_read_input_tokens"] = cached
	return true
}

func (s *GatewayService) streamKeepaliveIntervalForAccount(account *Account) time.Duration {
	if account != nil && account.Platform == PlatformKiro {
		if s != nil && s.cfg != nil && s.cfg.Gateway.KiroStreamKeepaliveInterval > 0 {
			return time.Duration(s.cfg.Gateway.KiroStreamKeepaliveInterval) * time.Second
		}
		return defaultKiroStreamKeepalive
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		return time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	return 0
}
