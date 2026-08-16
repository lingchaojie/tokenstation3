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
	attempt    captureAttemptToken
	resp       *http.Response
	limit      int
	mu         sync.Mutex
	buf        []byte
	observed   int64
	truncated  bool
	finishOnce sync.Once
	closeOnce  sync.Once
	closeErr   error
}

func (r *openAIHTTPCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.observed += int64(n)
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
	return r.closeUnderlying()
}

func (r *openAIHTTPCaptureReadCloser) closeUnderlying() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() { r.closeErr = r.inner.Close() })
	return r.closeErr
}

func (r *openAIHTTPCaptureReadCloser) closeCaptureUnderlying() error { return r.closeUnderlying() }
func (r *openAIHTTPCaptureReadCloser) joinCaptureReaders()           {}
func (r *openAIHTTPCaptureReadCloser) finishCapture()                { r.Finish() }
func (r *openAIHTTPCaptureReadCloser) captureResponseNeedsDrain() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observed <= int64(r.limit)
}
func (r *openAIHTTPCaptureReadCloser) captureResponseDrainRemaining() int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(r.limit) + 1 - r.observed
}
func (r *openAIHTTPCaptureReadCloser) markCaptureResponseTruncated() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.truncated = true
	r.mu.Unlock()
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
		setCaptureResultForAttempt(r.attempt, r.resp, body, truncated)
	})
}

func (s *OpenAIGatewayService) openAIHTTPCaptureEnabled(c *gin.Context, account *Account) bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && account != nil &&
		(openAIHTTPCaptureEndpointEligible(c) || isWebChatCaptureOwner(c)) &&
		CaptureMayApplyFor(c, string(account.Platform))
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
	if account.Platform == PlatformOpenAI && openAIHTTPCaptureEndpointEligible(c) {
		_, ok := beginCaptureAttemptForWireRequest(c.Request.Context(), c, s.capturePool, string(account.Platform), req, body, s.cfg.Gateway.Capture.MaxHeaderBytes)
		return ok
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
	if captureStreamingAttemptPath(c) {
		if _, exists := resp.Body.(*captureResponseReader); exists {
			return
		}
		attempt := captureAttemptForRequest(c)
		if attempt == nil {
			return
		}
		setCaptureAttemptResponseHTTPStatus(c, attempt, resp.StatusCode)
		attempt.WriteResponseHeaders(captureHeaderBytes(resp.Header, s.cfg.Gateway.Capture.MaxHeaderBytes))
		resp.Body = newCaptureResponseReader(resp.Body, attempt)
		return
	}
	resp.Body = &openAIHTTPCaptureReadCloser{
		inner:   resp.Body,
		c:       c,
		attempt: currentCaptureAttempt(c, true),
		resp:    resp,
		limit:   normalizeCaptureLimit(s.cfg.Gateway.Capture.MaxBodyBytes),
	}
}

func finishOpenAIHTTPCapture(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if capture, ok := resp.Body.(captureResponseLifecycle); ok {
		capture.finishCapture()
	}
}

func (s *OpenAIGatewayService) applyOpenAIHTTPSuccessCapture(c *gin.Context, account *Account, result *OpenAIForwardResult) {
	if result == nil || account == nil || s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled {
		return
	}
	outcome := CaptureOutcomeSuccess
	if result.UpstreamFailed || result.CaptureTerminalError {
		outcome = CaptureOutcomeTerminalError
	}
	content, enabled := CaptureDecisionFor(c, string(account.Platform), outcome)
	if !enabled {
		return
	}
	if captureStreamingAttemptPath(c) {
		// The streamed attempt stays request-owned until the handler's existing
		// usage/billing side-effect sink commits the client-visible outcome.
		return
	}
	bridge, ok := takeCaptureResult(c)
	if !ok || !bridge.ResponseObserved || bridge.RequestCaptureInvalid {
		return
	}
	result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
	result.UpstreamRequestHash = bridge.UpstreamRequestHash
	result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
	result.CaptureResponse = snapshotObservedBytes(bridge.Response)
	result.CaptureTruncated = bridge.Truncated
	result.CaptureRequestHeaders = snapshotBytes(bridge.RequestHeaders)
	result.CaptureResponseHeaders = snapshotBytes(bridge.ResponseHeaders)
	result.CaptureUpstreamEndpoint = bridge.UpstreamEndpoint
	result.CaptureHTTPStatus = bridge.HTTPStatus
	result.CaptureUpstreamModel = bridge.UpstreamModel
	result.CaptureStream = bridge.UpstreamStream
	result.CaptureStreamKnown = bridge.UpstreamStreamKnown
	if providerRequestID := captureProviderRequestIDBytes(bridge.ResponseHeaders); providerRequestID != "" {
		result.RequestID = providerRequestID
	}
	result.CaptureContentPolicy = &content
}

func (s *OpenAIGatewayService) submitOpenAIHTTPTerminalCapture(c *gin.Context, account *Account, resp *http.Response) {
	if s == nil || s.cfg == nil || s.capturePool == nil || account == nil || resp == nil {
		return
	}
	finishOpenAIHTTPCapture(resp)
	if captureStreamingAttemptPath(c) {
		// Typed OpenAI attempts remain request-owned until the handler's single
		// terminal-error side-effect sink classifies the final account outcome.
		return
	}
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
