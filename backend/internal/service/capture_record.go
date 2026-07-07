package service

import (
	"bufio"
	"bytes"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// nonStreamingCaptureContextKey 是 gin.Context 上暂存非流式响应体采集结果的 key。
// handleNonStreamingResponse 只返回 (*ClaudeUsage, error)，真正的 *ForwardResult
// 由上层 Forward 组装；这里借用请求级 gin.Context 把采集字节从响应处理阶段
// 传递到 ForwardResult 组装阶段，避免改动既有函数签名影响调用方/测试。
const nonStreamingCaptureContextKey = "gateway_capture_nonstream_result"

// nonStreamingCaptureResult 是暂存在 gin.Context 上的采集结果。
type nonStreamingCaptureResult struct {
	Response  []byte
	Truncated bool
}

// setNonStreamingCaptureResult 在响应处理阶段写入采集结果。
func setNonStreamingCaptureResult(c *gin.Context, resp []byte, truncated bool) {
	if c == nil {
		return
	}
	c.Set(nonStreamingCaptureContextKey, &nonStreamingCaptureResult{Response: resp, Truncated: truncated})
}

// takeNonStreamingCaptureResult 在 ForwardResult 组装阶段读取并清理采集结果。
func takeNonStreamingCaptureResult(c *gin.Context) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(nonStreamingCaptureContextKey)
	if !ok {
		return nil, false
	}
	res, ok := v.(*nonStreamingCaptureResult)
	if !ok || res == nil {
		return nil, false
	}
	return res.Response, res.Truncated
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

// extractCaptureColumns 在 worker 内填充 rec 的抽取列，供归档写入前调用。
func extractCaptureColumns(rec *CaptureRecord) {
	rec.SessionID = extractCaptureSessionID(rec.RawRequest)

	rec.ThinkingEffort = ""
	if effort := NormalizeClaudeOutputEffort(gjson.GetBytes(rec.RawRequest, "output_config.effort").String()); effort != nil {
		rec.ThinkingEffort = *effort
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
