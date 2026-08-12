package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const captureHardMaxBodyBytes = 8 << 20

// CaptureRecord 是提交给归档通道的一条原始上游调用快照。
// 所有 []byte 字段在提交前已 deep-copy，worker 内安全读取。
type CaptureRecord struct {
	CapturedAt       time.Time
	Platform         string
	RequestID        string
	RequestedModel   string
	UpstreamModel    string
	UpstreamEndpoint string
	Stream           bool
	HTTPStatus       int
	RawRequest       []byte // 最终上游请求体逐字
	RawResponse      []byte // 流式=原始 SSE；非流式=完整 JSON
	RequestHeaders   []byte // 脱敏后 JSON
	ResponseHeaders  []byte // 脱敏后 JSON
	Truncated        bool
	ContentPolicy    *CaptureContentPolicy

	// 以下抽取列由 worker 调用 extractCaptureColumns 填充，提交时可留空。
	SessionID           string
	ThinkingEffort      string
	ThinkingType        string
	StopReason          string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	SignaturePresent    bool
}

// snapshotBytes 返回 src 的独立副本，避免 worker 读到被后续改写的底层数组。
func snapshotBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// captureWithLimit 返回最多 limit 字节的独立副本及是否被截断。limit<=0 或 src 为 nil 视为不采集。
func captureWithLimit(src []byte, limit int) ([]byte, bool) {
	limit = normalizeCaptureLimit(limit)
	if limit <= 0 || src == nil {
		return nil, false
	}
	if len(src) <= limit {
		return snapshotBytes(src), false
	}
	dst := make([]byte, limit)
	copy(dst, src[:limit])
	return dst, true
}

func normalizeCaptureLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > captureHardMaxBodyBytes {
		return captureHardMaxBodyBytes
	}
	return limit
}

// captureResponseIfEnabled 便于测试的薄封装。
func captureResponseIfEnabled(enabled bool, src []byte, limit int) []byte {
	if !enabled {
		return nil
	}
	b, _ := captureWithLimit(src, limit)
	return b
}

// sseTee 在上游 SSE 读取 goroutine 里按行累积原始字节（含 SSE 帧换行），
// 达到 limit 后停止累积并标记 truncated。mutex 保护：读 goroutine 写入、
// 主 goroutine 在返回时读取，二者可能并发，必须加锁。
type sseTee struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

// captureBodyReadCloser captures the exact bytes consumed from an upstream
// response without changing read/close behavior. It lets callers finalize the
// same capture on success and committed-error paths.
type captureBodyReadCloser struct {
	io.ReadCloser
	attempt    captureAttemptToken
	resp       *http.Response
	mu         sync.Mutex
	buf        []byte
	limit      int
	truncated  bool
	finishOnce sync.Once
}

func (r *captureBodyReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n <= 0 {
		return n, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.truncated {
		remain := r.limit - len(r.buf)
		if remain > n {
			remain = n
		}
		if remain > 0 {
			r.buf = append(r.buf, p[:remain]...)
		}
		if remain < n {
			r.truncated = true
		}
	}
	return n, err
}

func (r *captureBodyReadCloser) bytes() ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshotBytes(r.buf), r.truncated
}

func (r *captureBodyReadCloser) Close() error {
	r.Finish(r.resp)
	return r.ReadCloser.Close()
}

func (r *captureBodyReadCloser) Finish(resp *http.Response) {
	if r == nil {
		return
	}
	r.finishOnce.Do(func() {
		body, truncated := r.bytes()
		setCaptureResultForAttempt(r.attempt, resp, body, truncated)
	})
}

func beginCaptureResponse(c *gin.Context, resp *http.Response, enabled bool, limit int) func() {
	limit = normalizeCaptureLimit(limit)
	if !enabled || limit <= 0 || resp == nil || resp.Body == nil {
		return func() {}
	}
	reader := &captureBodyReadCloser{ReadCloser: resp.Body, attempt: currentCaptureAttempt(c, true), resp: resp, limit: limit}
	resp.Body = reader
	return func() {
		reader.Finish(resp)
	}
}

func finishCaptureResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if body, ok := resp.Body.(*captureBodyReadCloser); ok {
		body.Finish(resp)
	}
}

func newSSETee(limit int) *sseTee { return &sseTee{limit: normalizeCaptureLimit(limit)} }

func (t *sseTee) appendLine(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.truncated {
		return
	}
	chunk := line + "\n" // 还原 scanner 去掉的换行；事件间空行 -> "\n\n"
	if len(t.buf)+len(chunk) > t.limit {
		if remain := t.limit - len(t.buf); remain > 0 {
			t.buf = append(t.buf, chunk[:remain]...)
		}
		t.truncated = true
		return
	}
	t.buf = append(t.buf, chunk...)
}

func (t *sseTee) bytes() ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf == nil {
		return nil, t.truncated
	}
	out := make([]byte, len(t.buf))
	copy(out, t.buf)
	return out, t.truncated
}

// captureResultContextKey 是 gin.Context 上暂存响应体采集结果的 key。
// handleNonStreamingResponse / handleStreamingResponse 都不直接返回 *ForwardResult，
// 真正的 *ForwardResult 由上层 Forward 组装；这里借用请求级 gin.Context 把采集字节
// 从响应处理阶段传递到 ForwardResult 组装阶段，避免改动既有函数签名影响调用方/测试。
// 流式与非流式互斥，同一请求只有一条路径会写入。
const captureResultContextKey = "gateway_capture_result"

// captureResultBridge 是暂存在 gin.Context 上的采集结果。
// 只保存“上游相关”数据：上游请求头/响应头（脱敏后）与响应体，不含任何客户端侧字段。
type captureResultBridge struct {
	UpstreamRequest     []byte
	UpstreamRequestHash string
	Response            []byte
	Truncated           bool
	RequestTruncated    bool
	ResponseTruncated   bool
	RequestHeaders      []byte // 上游请求头(脱敏)JSON —— 真正发给厂商的头
	ResponseHeaders     []byte // 上游响应头(脱敏)JSON —— 厂商返回的头
	UpstreamEndpoint    string
	HTTPStatus          int
	Platform            string
}

// captureResultSlot is shared by all forwarding attempts that reuse one Gin
// context. Forwarding attempts are sequential, while response finalization may
// race with result assembly; the slot makes publish/take/reset atomic so a take
// can never clear a bridge published after it.
type captureResultSlot struct {
	mu         sync.Mutex
	generation uint64
	bridge     *captureResultBridge
}

type captureAttemptToken struct {
	slot       *captureResultSlot
	generation uint64
}

func existingCaptureSlot(c *gin.Context) *captureResultSlot {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(captureResultContextKey); ok {
		if slot, ok := v.(*captureResultSlot); ok && slot != nil {
			return slot
		}
	}
	return nil
}

func captureSlot(c *gin.Context) *captureResultSlot {
	if slot := existingCaptureSlot(c); slot != nil {
		return slot
	}
	if c == nil {
		return nil
	}
	slot := &captureResultSlot{}
	c.Set(captureResultContextKey, slot)
	return slot
}

func currentCaptureAttempt(c *gin.Context, create bool) captureAttemptToken {
	slot := existingCaptureSlot(c)
	if slot == nil && create {
		slot = captureSlot(c)
	}
	if slot == nil {
		return captureAttemptToken{}
	}
	slot.mu.Lock()
	if slot.generation == 0 {
		slot.generation = 1
	}
	if create && slot.bridge == nil {
		slot.bridge = &captureResultBridge{}
	}
	token := captureAttemptToken{slot: slot, generation: slot.generation}
	slot.mu.Unlock()
	return token
}

func startCaptureAttempt(c *gin.Context) captureAttemptToken {
	slot := captureSlot(c)
	if slot == nil {
		return captureAttemptToken{}
	}
	slot.mu.Lock()
	platform := ""
	if slot.bridge != nil {
		platform = slot.bridge.Platform
	}
	slot.generation++
	if slot.generation == 0 {
		slot.generation = 1
	}
	slot.bridge = &captureResultBridge{Platform: platform}
	token := captureAttemptToken{slot: slot, generation: slot.generation}
	slot.mu.Unlock()
	return token
}

func withCaptureAttempt(token captureAttemptToken, fn func(*captureResultBridge)) bool {
	if token.slot == nil || fn == nil {
		return false
	}
	token.slot.mu.Lock()
	defer token.slot.mu.Unlock()
	if token.slot.generation != token.generation {
		return false
	}
	if token.slot.bridge == nil {
		token.slot.bridge = &captureResultBridge{}
	}
	fn(token.slot.bridge)
	return true
}

// beginCaptureAttempt isolates a new account attempt from any result left by a
// prior nil-result/failover path. Handlers invoke service attempts serially for
// a request; response publishing and result taking remain synchronized below.
func beginCaptureAttempt(c *gin.Context) {
	slot := existingCaptureSlot(c)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.generation++
	slot.bridge = nil
	slot.mu.Unlock()
}

// ResetCaptureExchange clears snapshots from an intermediate failover attempt
// without replacing the synchronized slot shared by response finalization.
func ResetCaptureExchange(c *gin.Context) {
	if existingCaptureSlot(c) == nil {
		return
	}
	beginCaptureAttempt(c)
	if c != nil {
		c.Set(kiroCaptureHeadersContextKey, nil)
	}
}

// SetCaptureOutboundRequest snapshots the actual post-mapping request sent to
// the provider. Callers must guard this with CaptureMayApplyFor.
func SetCaptureOutboundRequest(c *gin.Context, req *http.Request, body []byte, limit int) {
	token := startCaptureAttempt(c)
	if token.slot == nil {
		return
	}
	request, requestTruncated := captureWithLimit(body, limit)
	requestHash := HashUsageRequestPayload(body)
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		bridge.UpstreamRequest = request
		bridge.UpstreamRequestHash = requestHash
		bridge.RequestTruncated = requestTruncated
		bridge.Truncated = requestTruncated
		if req != nil {
			bridge.RequestHeaders = redactHTTPHeader(req.Header)
			if req.URL != nil {
				bridge.UpstreamEndpoint = req.URL.String()
			}
		}
	})
}

func setCapturePlatform(c *gin.Context, platform string) {
	slot := captureSlot(c)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.bridge == nil {
		slot.bridge = &captureResultBridge{}
	}
	slot.bridge.Platform = strings.ToLower(strings.TrimSpace(platform))
	slot.mu.Unlock()
}

// setCaptureResult 在响应处理阶段写入采集结果（流式与非流式共用）。
// resp 是上游 http.Response —— 从中取“真正发给厂商的请求头”(resp.Request.Header)
// 与“厂商返回的响应头”(resp.Header)，脱敏后随桥暂存；均为上游相关，不含客户端头。
func setCaptureResult(c *gin.Context, resp *http.Response, body []byte, truncated bool) {
	setCaptureResultForAttempt(currentCaptureAttempt(c, true), resp, body, truncated)
}

func setCaptureResultForAttempt(token captureAttemptToken, resp *http.Response, body []byte, truncated bool) {
	body, hardTruncated := captureWithLimit(body, captureHardMaxBodyBytes)
	truncated = truncated || hardTruncated
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		bridge.Response = snapshotBytes(body)
		bridge.ResponseTruncated = truncated
		bridge.Truncated = bridge.RequestTruncated || truncated
		if resp != nil {
			bridge.HTTPStatus = resp.StatusCode
			if resp.Request != nil && len(bridge.RequestHeaders) == 0 {
				bridge.RequestHeaders = redactHTTPHeader(resp.Request.Header)
				if resp.Request.URL != nil && bridge.UpstreamEndpoint == "" {
					bridge.UpstreamEndpoint = resp.Request.URL.String()
				}
			}
			bridge.ResponseHeaders = redactHTTPHeader(resp.Header)
		}
	})
}

func markCaptureResultTruncated(c *gin.Context) {
	withCaptureAttempt(currentCaptureAttempt(c, false), func(bridge *captureResultBridge) {
		bridge.ResponseTruncated = true
		bridge.Truncated = true
	})
}

type boundedCaptureWriter struct {
	buf       []byte
	limit     int
	total     int64
	truncated bool
}

func (w *boundedCaptureWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if remain := w.limit - len(w.buf); remain > 0 {
		if remain > len(p) {
			remain = len(p)
		}
		w.buf = append(w.buf, p[:remain]...)
	}
	if w.total > int64(w.limit) {
		w.truncated = true
	}
	return len(p), nil
}

type replayPrefixReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *replayPrefixReadCloser) Close() error { return r.closer.Close() }

func snapshotHTTPRequestBodyForCapture(req *http.Request, limit int) ([]byte, bool, string) {
	limit = normalizeCaptureLimit(limit)
	if req == nil || limit <= 0 {
		return nil, false, ""
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer func() { _ = body.Close() }()
			hasher := sha256.New()
			writer := &boundedCaptureWriter{limit: limit}
			if _, copyErr := io.Copy(io.MultiWriter(hasher, writer), body); copyErr == nil {
				return snapshotBytes(writer.buf), writer.truncated, hex.EncodeToString(hasher.Sum(nil))
			}
		}
	}
	if req.Body == nil {
		return nil, false, ""
	}
	original := req.Body
	prefix, err := io.ReadAll(io.LimitReader(original, int64(limit)+1))
	if err != nil {
		return nil, false, ""
	}
	req.Body = &replayPrefixReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), original), closer: original}
	captured, truncated := captureWithLimit(prefix, limit)
	requestHash := ""
	if !truncated {
		requestHash = HashUsageRequestPayload(prefix)
	}
	return captured, truncated, requestHash
}

func snapshotHTTPRequestBody(req *http.Request) []byte {
	if req == nil {
		return nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer func() { _ = body.Close() }()
			if data, readErr := io.ReadAll(body); readErr == nil {
				return snapshotBytes(data)
			}
		}
	}
	if req.Body == nil {
		return nil
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	return snapshotBytes(data)
}

// setCaptureUpstreamRequest records the body immediately before the real Do
// call. A later retry replaces it, so the bridge always represents the final
// attempted provider request rather than the inbound client payload.
func setCaptureUpstreamRequest(c *gin.Context, req *http.Request, limit int) {
	token := startCaptureAttempt(c)
	if token.slot == nil {
		return
	}
	body, truncated, requestHash := snapshotHTTPRequestBodyForCapture(req, limit)
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		bridge.UpstreamRequest = body
		bridge.UpstreamRequestHash = requestHash
		bridge.RequestTruncated = truncated
		bridge.Truncated = truncated
		if req != nil {
			bridge.RequestHeaders = redactHTTPHeader(req.Header)
			if req.URL != nil {
				bridge.UpstreamEndpoint = req.URL.String()
			}
		}
	})
}

func setCaptureUpstreamResponse(c *gin.Context, resp *http.Response) {
	if resp == nil {
		return
	}
	withCaptureAttempt(currentCaptureAttempt(c, false), func(bridge *captureResultBridge) {
		if len(bridge.RequestHeaders) == 0 && resp.Request != nil {
			bridge.RequestHeaders = redactHTTPHeader(resp.Request.Header)
		}
		bridge.ResponseHeaders = redactHTTPHeader(resp.Header)
		bridge.HTTPStatus = resp.StatusCode
		if bridge.UpstreamEndpoint == "" && resp.Request != nil && resp.Request.URL != nil {
			bridge.UpstreamEndpoint = resp.Request.URL.String()
		}
	})
}

type captureUpstreamRequestContextKey struct{}

type captureUpstreamRequestContext struct {
	c     *gin.Context
	limit int
}

func withCaptureUpstreamRequestContext(ctx context.Context, c *gin.Context, limit int) context.Context {
	if ctx == nil || c == nil {
		return ctx
	}
	return context.WithValue(ctx, captureUpstreamRequestContextKey{}, captureUpstreamRequestContext{c: c, limit: normalizeCaptureLimit(limit)})
}

func setCaptureUpstreamRequestFromContext(ctx context.Context, req *http.Request) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	setCaptureUpstreamRequest(captureCtx.c, req, captureCtx.limit)
}

func setCaptureUpstreamResponseFromContext(ctx context.Context, resp *http.Response) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	setCaptureUpstreamResponse(captureCtx.c, resp)
}

func markCaptureResultTruncatedFromContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	markCaptureResultTruncated(captureCtx.c)
}

// takeCaptureResult 在 ForwardResult 组装阶段读取采集结果（流式与非流式共用）。
func takeCaptureResult(c *gin.Context) (*captureResultBridge, bool) {
	if c == nil {
		return nil, false
	}
	slot := existingCaptureSlot(c)
	if slot == nil {
		return nil, false
	}
	slot.mu.Lock()
	res := slot.bridge
	slot.bridge = nil
	slot.generation++
	slot.mu.Unlock()
	return res, res != nil
}

// attachCaptureToForwardResult transfers the attempt-local capture bridge to
// the result returned to the handler. It is shared by complete and committed
// partial responses so both paths submit the same real upstream bytes once.
func attachCaptureToForwardResult(c *gin.Context, result *ForwardResult) *ForwardResult {
	if result == nil {
		return nil
	}
	if bridge, ok := takeCaptureResult(c); ok {
		result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
		result.UpstreamRequestHash = bridge.UpstreamRequestHash
		result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
		result.CaptureRequestHeaders = bridge.RequestHeaders
		result.CaptureResponseHeaders = bridge.ResponseHeaders
		result.CaptureUpstreamEndpoint = bridge.UpstreamEndpoint
		result.CaptureHTTPStatus = bridge.HTTPStatus
		if bridge.Response != nil {
			result.CaptureResponse = bridge.Response
			result.CaptureTruncated = bridge.Truncated
		}
		if platform := firstNonEmpty(bridge.Platform, platformFromCaptureEndpoint(bridge.UpstreamEndpoint)); platform != "" {
			if content, enabled := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); enabled {
				result.CaptureContentPolicy = &content
			}
		}
	}
	return result
}

func finalizeForwardResult(c *gin.Context, result *ForwardResult) *ForwardResult {
	return attachCaptureToForwardResult(c, result)
}

func attachCaptureToOpenAIForwardResult(c *gin.Context, result *OpenAIForwardResult) *OpenAIForwardResult {
	if result == nil {
		return nil
	}
	if bridge, ok := takeCaptureResult(c); ok && bridge.Response != nil {
		result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
		result.UpstreamRequestHash = bridge.UpstreamRequestHash
		result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
		result.CaptureResponse = bridge.Response
		result.CaptureTruncated = bridge.Truncated
		result.CaptureRequestHeaders = bridge.RequestHeaders
		result.CaptureResponseHeaders = bridge.ResponseHeaders
		result.CaptureUpstreamEndpoint = bridge.UpstreamEndpoint
		result.CaptureHTTPStatus = bridge.HTTPStatus
		if platform := firstNonEmpty(bridge.Platform, platformFromCaptureEndpoint(bridge.UpstreamEndpoint)); platform != "" {
			if content, enabled := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); enabled {
				result.CaptureContentPolicy = &content
			}
		}
	}
	return result
}

func finalizeOpenAIForwardResult(c *gin.Context, result *OpenAIForwardResult, upstreamRequest []byte) *OpenAIForwardResult {
	if result == nil {
		return nil
	}
	result.UpstreamRequest, _ = captureWithLimit(upstreamRequest, captureHardMaxBodyBytes)
	result.UpstreamRequestHash = HashUsageRequestPayload(upstreamRequest)
	result.CaptureRequest = snapshotBytes(result.UpstreamRequest)
	return attachCaptureToOpenAIForwardResult(c, result)
}

func platformFromCaptureEndpoint(endpoint string) string {
	host := strings.ToLower(endpoint)
	switch {
	case strings.Contains(host, "api.openai.com") || strings.Contains(host, "/backend-api/codex"):
		return PlatformOpenAI
	case strings.Contains(host, "anthropic"):
		return PlatformAnthropic
	case strings.Contains(host, "generativelanguage") || strings.Contains(host, "googleapis"):
		return PlatformGemini
	case strings.Contains(host, "x.ai"):
		return PlatformGrok
	default:
		return ""
	}
}

// responseColumns 是从原始上游响应体（流式 SSE 或非流式 JSON）轻扫描抽取出的
// 可查询列，供 extractCaptureColumns 写回 CaptureRecord。
type responseColumns struct {
	StopReason          string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	SignaturePresent    bool
}

// extractCaptureSessionID 优先从上游 body 的 metadata.user_id 解出 session_id，
// 无则 fallback 到 body 内 session 提示字段（prompt_cache_key/conversation_id/...）。
func extractCaptureSessionID(body []byte) string {
	if uid := gjson.GetBytes(body, "metadata.user_id").String(); uid != "" {
		if parsed := ParseMetadataUserID(uid); parsed != nil && parsed.SessionID != "" {
			return parsed.SessionID
		}
	}
	return extractBodySessionID(string(body))
}

// extractResponseColumns 轻扫描响应，取 stop_reason/usage/signature 抽取列。
// 流式=按 SSE 行累积（后到覆盖先到）；非流式=单个 JSON。不做完整组装。
func extractResponseColumns(resp []byte, stream bool) responseColumns {
	var cols responseColumns
	apply := func(js string) {
		if sr := gjson.Get(js, "stop_reason").String(); sr != "" {
			cols.StopReason = sr
		}
		if sr := gjson.Get(js, "delta.stop_reason").String(); sr != "" {
			cols.StopReason = sr
		}
		if v := gjson.Get(js, "usage.input_tokens"); v.Exists() {
			cols.InputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "message.usage.input_tokens"); v.Exists() {
			cols.InputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.output_tokens"); v.Exists() {
			cols.OutputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.cache_read_input_tokens"); v.Exists() {
			cols.CacheReadTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.cache_creation_input_tokens"); v.Exists() {
			cols.CacheCreationTokens = int(v.Int())
		}
		// 流式 message_start 事件把 usage（含 cache 明细）挂在 message.usage 下。
		if v := gjson.Get(js, "message.usage.cache_read_input_tokens"); v.Exists() {
			cols.CacheReadTokens = int(v.Int())
		}
		if v := gjson.Get(js, "message.usage.cache_creation_input_tokens"); v.Exists() {
			cols.CacheCreationTokens = int(v.Int())
		}
		if v := gjson.Get(js, "message.usage.output_tokens"); v.Exists() {
			cols.OutputTokens = int(v.Int())
		}
		// fast-path guard: 先做字符串命中再走 content ForEach 深扫，避免逐块解析开销。
		if strings.Contains(js, "\"signature\"") || strings.Contains(js, "signature_delta") {
			if gjson.Get(js, "signature").Exists() ||
				gjson.Get(js, "delta.signature").Exists() ||
				gjson.Get(js, "delta.type").String() == "signature_delta" {
				cols.SignaturePresent = true
			}
			gjson.Get(js, "content").ForEach(func(_, b gjson.Result) bool {
				if b.Get("signature").String() != "" {
					cols.SignaturePresent = true
					return false
				}
				return true
			})
		}
	}
	if !stream {
		apply(string(resp))
		return cols
	}
	sc := bufio.NewScanner(bytes.NewReader(resp))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload != "" && payload != "[DONE]" {
				apply(payload)
			}
		}
	}
	return cols
}

// redactedHeaderKeys 需剥离的凭证类 header（小写匹配）。
var redactedHeaderKeys = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"api-key":             {}, // Azure-style bare api-key header
	"cookie":              {},
	"set-cookie":          {},
	"proxy-authorization": {},
	"x-goog-api-key":      {},
}

// redactHeadersJSON 剥离凭证类头后序列化为 JSON。保留影响模型行为/诊断的头
// （anthropic-version、anthropic-beta、x-request-id、限流头等）。nil/空 -> nil。
func redactHeadersJSON(h map[string][]string) []byte {
	if len(h) == 0 {
		return nil
	}
	clean := make(map[string][]string, len(h))
	for k, v := range h {
		if _, bad := redactedHeaderKeys[strings.ToLower(k)]; bad {
			continue
		}
		clean[k] = v
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return nil
	}
	return b
}

// redactHTTPHeader 是 http.Header 的适配。
func redactHTTPHeader(h http.Header) []byte { return redactHeadersJSON(map[string][]string(h)) }

// SnapshotForCapture 返回 src 的受限独立副本（<= limit 字节），供 handler 采集请求体。
func SnapshotForCapture(src []byte, limit int) []byte { b, _ := captureWithLimit(src, limit); return b }

// SnapshotForCaptureWithFlag 与 SnapshotForCapture 相同，但额外返回是否被截断，
// 供调用方把请求截断并入 CaptureRecord.Truncated。
func SnapshotForCaptureWithFlag(src []byte, limit int) ([]byte, bool) {
	return captureWithLimit(src, limit)
}

// CaptureRequestID 返回上游 request_id；为空时兜底生成 UUID，
// 仅用于归档记录（不影响返回客户端）。满足 PDF「全局唯一 request_id」。
func CaptureRequestID(upstream string) string {
	if s := strings.TrimSpace(upstream); s != "" {
		return s
	}
	return "cap_" + uuid.NewString()
}

// buildErrorCaptureRecord 组装一条“上游错误响应”归档记录。请求/响应体均受 limit
// 截断并独立拷贝；头部从上游 http.Response 取（脱敏）。所有字段只反映上游相关信息。
// 返回 nil 表示无需归档（reqBody 与 respBody 都为空）。
func buildErrorCaptureRecord(resp *http.Response, platform, requestedModel, upstreamModel, upstreamEndpoint string, stream bool, reqBody, respBody []byte, limit int) *CaptureRecord {
	if len(reqBody) == 0 && len(respBody) == 0 {
		return nil
	}
	rawReq, requestTruncated := captureWithLimit(reqBody, limit)
	rawResp, responseTruncated := captureWithLimit(respBody, limit)
	rec := &CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         platform,
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: upstreamEndpoint,
		Stream:           stream,
		RawRequest:       rawReq,
		RawResponse:      rawResp,
		Truncated:        requestTruncated || responseTruncated,
	}
	if resp != nil {
		rec.HTTPStatus = resp.StatusCode
		rec.RequestID = resp.Header.Get("x-request-id")
		if resp.Request != nil {
			rec.RequestHeaders = redactHTTPHeader(resp.Request.Header)
		}
		rec.ResponseHeaders = redactHTTPHeader(resp.Header)
	}
	return rec
}

func captureEndpointFromResponse(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.String()
}

func captureRequestHeadersFromResponse(resp *http.Response) http.Header {
	if resp == nil || resp.Request == nil {
		return nil
	}
	return resp.Request.Header.Clone()
}

// BuildTerminalErrorCaptureRecord materializes only a final upstream HTTP
// failure. Callers must explicitly mark that an actual error-status response
// was received; transport, local scheduling, and errors embedded in an HTTP 200
// stream are deliberately excluded even when they reuse HTTP-like status codes.
func BuildTerminalErrorCaptureRecord(c *gin.Context, platform string, failure *UpstreamFailoverError, limit int) *CaptureRecord {
	if c == nil || failure == nil || failure.StatusCode <= 0 || !failure.HasUpstreamHTTPResponse {
		return nil
	}
	content, enabled := CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
	if !enabled {
		return nil
	}
	bridge, hasBridge := takeCaptureResult(c)
	if !hasBridge && strings.TrimSpace(failure.UpstreamEndpoint) == "" {
		return nil
	}

	requestedModel := captureRequestedModel(c)
	var upstreamModel string
	var stream bool
	var fallbackRequest []byte
	if value, ok := c.Get("parsed_request"); ok {
		if parsed, valid := value.(*ParsedRequest); valid && parsed != nil {
			if requestedModel == "" {
				requestedModel = parsed.Model
			}
			stream = parsed.Stream
			if parsed.Body != nil {
				fallbackRequest = parsed.Body.Bytes()
			}
		}
	}

	rawRequest, requestTruncated := captureWithLimit(fallbackRequest, limit)
	endpoint := strings.TrimSpace(failure.UpstreamEndpoint)
	requestHeaders := redactHTTPHeader(failure.RequestHeaders)
	if hasBridge {
		if bridge.UpstreamRequest != nil {
			var additionallyTruncated bool
			rawRequest, additionallyTruncated = captureWithLimit(bridge.UpstreamRequest, limit)
			requestTruncated = bridge.RequestTruncated || additionallyTruncated
		}
		requestHeaders = snapshotBytes(bridge.RequestHeaders)
		if endpoint == "" {
			endpoint = bridge.UpstreamEndpoint
		}
	}
	rawResponse, responseTruncated := captureWithLimit(failure.ResponseBody, limit)
	if hasBridge && bridge.Response != nil {
		rawResponse = snapshotBytes(bridge.Response)
		responseTruncated = bridge.ResponseTruncated
	}
	if model, outboundStream, _ := extractOpenAIRequestMetaFromBody(rawRequest); model != "" {
		upstreamModel = model
		stream = outboundStream
	}
	if requestedModel == "" {
		requestedModel = upstreamModel
	}
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	rec := &CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         platform,
		RequestID:        failure.ResponseHeaders.Get("x-request-id"),
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: endpoint,
		Stream:           stream,
		HTTPStatus:       failure.StatusCode,
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   requestHeaders,
		ResponseHeaders:  redactHTTPHeader(failure.ResponseHeaders),
		Truncated:        requestTruncated || responseTruncated,
		ContentPolicy:    &content,
	}
	if rec.RequestID == "" {
		rec.RequestID = CaptureRequestID("")
	}
	return rec
}

// extractCaptureColumns 在 worker 内填充 rec 的抽取列，供归档写入前调用。
func extractCaptureColumns(rec *CaptureRecord) {
	rec.SessionID = extractCaptureSessionID(rec.RawRequest)

	// 仅当 submit 侧未预填时，才从 raw_request 回退抽取。
	// Bedrock/Kiro 等 body 里 output_config 可能已被剥离/翻译，故 submit 侧优先用
	// ParsedRequest.OutputEffort 预填；此处不覆盖已有值。
	if rec.ThinkingEffort == "" {
		if effort := NormalizeClaudeOutputEffort(gjson.GetBytes(rec.RawRequest, "output_config.effort").String()); effort != nil {
			rec.ThinkingEffort = *effort
		}
	}
	rec.ThinkingType = gjson.GetBytes(rec.RawRequest, "thinking.type").String()

	cols := extractResponseColumns(rec.RawResponse, rec.Stream)
	rec.StopReason = cols.StopReason
	rec.InputTokens = cols.InputTokens
	rec.OutputTokens = cols.OutputTokens
	rec.CacheReadTokens = cols.CacheReadTokens
	rec.CacheCreationTokens = cols.CacheCreationTokens
	rec.SignaturePresent = cols.SignaturePresent
}

// ApplyCaptureContentPolicy clears disabled persistence fields. Call it only
// after extractCaptureColumns so searchable metadata survives body suppression.
func ApplyCaptureContentPolicy(rec *CaptureRecord, policy CaptureContentPolicy) {
	if rec == nil {
		return
	}
	if !policy.RawRequest {
		rec.RawRequest = nil
	}
	if !policy.RawResponse {
		rec.RawResponse = nil
	}
	if !policy.RequestHeaders {
		rec.RequestHeaders = nil
	}
	if !policy.ResponseHeaders {
		rec.ResponseHeaders = nil
	}
}
