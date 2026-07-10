package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

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

func newSSETee(limit int) *sseTee { return &sseTee{limit: limit} }

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
	Response        []byte
	Truncated       bool
	RequestHeaders  []byte // 上游请求头(脱敏)JSON —— 真正发给厂商的头
	ResponseHeaders []byte // 上游响应头(脱敏)JSON —— 厂商返回的头
}

// setCaptureResult 在响应处理阶段写入采集结果（流式与非流式共用）。
// resp 是上游 http.Response —— 从中取“真正发给厂商的请求头”(resp.Request.Header)
// 与“厂商返回的响应头”(resp.Header)，脱敏后随桥暂存；均为上游相关，不含客户端头。
func setCaptureResult(c *gin.Context, resp *http.Response, body []byte, truncated bool) {
	if c == nil {
		return
	}
	bridge := &captureResultBridge{Response: body, Truncated: truncated}
	if resp != nil {
		if resp.Request != nil {
			bridge.RequestHeaders = redactHTTPHeader(resp.Request.Header)
		}
		bridge.ResponseHeaders = redactHTTPHeader(resp.Header)
	}
	c.Set(captureResultContextKey, bridge)
}

// takeCaptureResult 在 ForwardResult 组装阶段读取采集结果（流式与非流式共用）。
func takeCaptureResult(c *gin.Context) (*captureResultBridge, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(captureResultContextKey)
	if !ok {
		return nil, false
	}
	res, ok := v.(*captureResultBridge)
	if !ok || res == nil {
		return nil, false
	}
	return res, true
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
	rawReq, _ := captureWithLimit(reqBody, limit)
	rawResp, truncated := captureWithLimit(respBody, limit)
	rec := &CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         platform,
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: upstreamEndpoint,
		Stream:           stream,
		RawRequest:       rawReq,
		RawResponse:      rawResp,
		Truncated:        truncated,
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
