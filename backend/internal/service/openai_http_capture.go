package service

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type openAIHTTPCaptureReadCloser struct {
	inner      io.ReadCloser
	c          *gin.Context
	resp       *http.Response
	limit      int
	mu         sync.Mutex
	buf        []byte
	truncated  bool
	finishOnce sync.Once
}

func (r *openAIHTTPCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.mu.Lock()
		remaining := r.limit - len(r.buf)
		if remaining > 0 {
			copyN := n
			if copyN > remaining {
				copyN = remaining
			}
			r.buf = append(r.buf, p[:copyN]...)
		}
		if n > remaining {
			r.truncated = true
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *openAIHTTPCaptureReadCloser) Close() error {
	r.Finish()
	return r.inner.Close()
}

func (r *openAIHTTPCaptureReadCloser) Finish() {
	if r == nil {
		return
	}
	r.finishOnce.Do(func() {
		r.mu.Lock()
		body := snapshotBytes(r.buf)
		truncated := r.truncated
		r.mu.Unlock()
		setCaptureResult(r.c, r.resp, body, truncated)
	})
}

func (s *OpenAIGatewayService) openAIHTTPCaptureEnabled(c *gin.Context, account *Account) bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil &&
		openAIHTTPCaptureEndpointEligible(c) && CaptureMayApplyFor(c, string(account.Platform))
}

func openAIHTTPCaptureEndpointEligible(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil || c.Request.Method != http.MethodPost {
		return false
	}
	if imageIntent, known := getOpenAIImageIntentHint(c); known && imageIntent {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	for _, suffix := range []string{
		"/v1/responses",
		"/backend-api/codex/responses",
		"/v1/chat/completions",
		"/v1/messages",
	} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return path == "/responses" || path == "/chat/completions"
}

// prepareOpenAIHTTPCaptureAttempt snapshots the final post-conversion request
// immediately before HTTP send. A later retry overwrites this attempt.
func (s *OpenAIGatewayService) prepareOpenAIHTTPCaptureAttempt(c *gin.Context, account *Account, req *http.Request, body []byte) bool {
	if !s.openAIHTTPCaptureEnabled(c, account) {
		return false
	}
	setCapturePlatform(c, string(account.Platform))
	SetCaptureOutboundRequest(c, req, body, s.cfg.Gateway.Capture.MaxBodyBytes)
	return true
}

// wrapOpenAIHTTPCaptureResponse records bytes as the existing response parser
// consumes them, preserving raw JSON/SSE before any client-protocol conversion.
func (s *OpenAIGatewayService) wrapOpenAIHTTPCaptureResponse(c *gin.Context, account *Account, resp *http.Response) {
	if !s.openAIHTTPCaptureEnabled(c, account) || resp == nil || resp.Body == nil {
		return
	}
	if _, exists := resp.Body.(*openAIHTTPCaptureReadCloser); exists {
		return
	}
	resp.Body = &openAIHTTPCaptureReadCloser{
		inner: resp.Body,
		c:     c,
		resp:  resp,
		limit: s.cfg.Gateway.Capture.MaxBodyBytes,
	}
}

func finishOpenAIHTTPCapture(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if capture, ok := resp.Body.(*openAIHTTPCaptureReadCloser); ok {
		capture.Finish()
	}
}

func (s *OpenAIGatewayService) applyOpenAIHTTPSuccessCapture(c *gin.Context, account *Account, result *OpenAIForwardResult) {
	if result == nil || account == nil || s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled {
		return
	}
	content, enabled := CaptureDecisionFor(c, string(account.Platform), CaptureOutcomeSuccess)
	if !enabled {
		return
	}
	bridge, ok := takeCaptureResult(c)
	if !ok || bridge.Response == nil {
		return
	}
	result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
	result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
	result.CaptureResponse = snapshotBytes(bridge.Response)
	result.CaptureTruncated = bridge.Truncated
	result.CaptureRequestHeaders = snapshotBytes(bridge.RequestHeaders)
	result.CaptureResponseHeaders = snapshotBytes(bridge.ResponseHeaders)
	result.CaptureUpstreamEndpoint = bridge.UpstreamEndpoint
	result.CaptureHTTPStatus = bridge.HTTPStatus
	result.CaptureContentPolicy = &content
}

func (s *OpenAIGatewayService) submitOpenAIHTTPTerminalCapture(c *gin.Context, account *Account, resp *http.Response) {
	if s == nil || s.cfg == nil || s.capturePool == nil || account == nil || resp == nil {
		return
	}
	finishOpenAIHTTPCapture(resp)
	failure := &UpstreamFailoverError{
		StatusCode:              resp.StatusCode,
		RequestHeaders:          captureRequestHeadersFromResponse(resp),
		ResponseHeaders:         resp.Header.Clone(),
		UpstreamEndpoint:        captureEndpointFromResponse(resp),
		HasUpstreamHTTPResponse: true,
		Platform:                string(account.Platform),
	}
	if rec := BuildTerminalErrorCaptureRecord(c, string(account.Platform), failure, s.cfg.Gateway.Capture.MaxBodyBytes); rec != nil {
		s.capturePool.Submit(rec)
	}
}
