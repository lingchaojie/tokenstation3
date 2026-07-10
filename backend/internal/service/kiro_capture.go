package service

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Kiro 走独立的 forwardKiroMessages，其流式响应经 io.Pipe 包装后返回的
// *http.Response 只带合成头（wrappedHeaders，含网关自造的 x-request-id）。
// 为满足「归档只存真实上游头」的决策，在真实上游响应仍可见的
// openKiroAnthropicStreamResponse 内把脱敏后的真实上游请求头/响应头暂存到
// gin.Context，供 forwardKiroMessages 组装 CaptureRecord 时取回。
const kiroCaptureHeadersContextKey = "gateway_kiro_capture_headers"

type kiroCaptureHeaders struct {
	RequestHeaders  []byte // 真实上游请求头(脱敏)JSON
	ResponseHeaders []byte // 真实上游响应头(脱敏)JSON
}

// buildKiroCaptureHeaders 从真实上游响应抽取脱敏后的上游请求头/响应头。
// resp.Request 为出站到 CodeWhisperer 的请求（httpUpstream.Do 设置），
// resp.Header 为厂商返回头。凭证类头由 redactHTTPHeader 剥离。
func buildKiroCaptureHeaders(resp *http.Response) kiroCaptureHeaders {
	var h kiroCaptureHeaders
	if resp == nil {
		return h
	}
	if resp.Request != nil {
		h.RequestHeaders = redactHTTPHeader(resp.Request.Header)
	}
	h.ResponseHeaders = redactHTTPHeader(resp.Header)
	return h
}

// stashKiroCaptureHeaders 把真实上游头暂存到 gin.Context（capture 开启时调用）。
func stashKiroCaptureHeaders(c *gin.Context, resp *http.Response) {
	if c == nil || resp == nil {
		return
	}
	c.Set(kiroCaptureHeadersContextKey, buildKiroCaptureHeaders(resp))
}

// takeKiroCaptureHeaders 取回暂存的真实上游头；未暂存时返回 nil,nil。
func takeKiroCaptureHeaders(c *gin.Context) ([]byte, []byte) {
	if c == nil {
		return nil, nil
	}
	v, ok := c.Get(kiroCaptureHeadersContextKey)
	if !ok {
		return nil, nil
	}
	h, ok := v.(kiroCaptureHeaders)
	if !ok {
		return nil, nil
	}
	return h.RequestHeaders, h.ResponseHeaders
}
