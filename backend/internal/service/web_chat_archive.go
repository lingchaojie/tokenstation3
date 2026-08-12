package service

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type webChatFinalGatewayErrorCaptureSubmitter interface {
	submitWebChatFinalGatewayErrorCapture(
		ctx context.Context,
		c *gin.Context,
		account *Account,
		requestedModel string,
		upstreamModel string,
		upstreamEndpoint string,
		stream bool,
		resp *http.Response,
		responseBody []byte,
	)
}

type webChatFinalGatewayErrorCaptureContextKey struct{}

const webChatCaptureOwnerContextKey = "web_chat_capture_owner"

func markWebChatCaptureOwner(c *gin.Context) {
	if c != nil {
		c.Set(webChatCaptureOwnerContextKey, true)
	}
}

func isWebChatCaptureOwner(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get(webChatCaptureOwnerContextKey)
	owned, _ := value.(bool)
	return exists && owned
}

func hasWebChatFinalGatewayErrorCaptureSubmitter(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	submitter, _ := ctx.Value(webChatFinalGatewayErrorCaptureContextKey{}).(webChatFinalGatewayErrorCaptureSubmitter)
	return submitter != nil
}

func ownsWebChatFinalGatewayErrorCapture(ctx context.Context) bool {
	return hasWebChatStreamCapture(ctx) && hasWebChatFinalGatewayErrorCaptureSubmitter(ctx)
}

func withWebChatFinalGatewayErrorCaptureSubmitter(ctx context.Context, submitter webChatFinalGatewayErrorCaptureSubmitter) context.Context {
	if ctx == nil || submitter == nil {
		return ctx
	}
	return context.WithValue(ctx, webChatFinalGatewayErrorCaptureContextKey{}, submitter)
}

func submitWebChatFinalGatewayErrorCaptureFromContext(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestedModel string,
	upstreamModel string,
	upstreamEndpoint string,
	stream bool,
	resp *http.Response,
	responseBody []byte,
) {
	if ctx == nil {
		return
	}
	submitter, _ := ctx.Value(webChatFinalGatewayErrorCaptureContextKey{}).(webChatFinalGatewayErrorCaptureSubmitter)
	if submitter == nil {
		return
	}
	submitter.submitWebChatFinalGatewayErrorCapture(
		ctx, c, account, requestedModel, upstreamModel, upstreamEndpoint, stream, resp, responseBody,
	)
}

// submitWebChatFinalGatewayErrorCapture owns terminal HTTP-error archival for
// direct WebChat service callers. Normal gateway handlers have their own sink
// and do not carry a WebChat stream-capture token, so this cannot duplicate
// handler archival. A final provider request snapshot is mandatory: new
// producers must never archive the inbound compatibility payload as upstream.
func (s *GatewayService) submitWebChatFinalGatewayErrorCapture(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestedModel string,
	upstreamModel string,
	upstreamEndpoint string,
	stream bool,
	resp *http.Response,
	responseBody []byte,
) {
	if s == nil || s.capturePool == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled ||
		!ownsWebChatFinalGatewayErrorCapture(ctx) || account == nil || resp == nil {
		return
	}
	content, enabled := CaptureDecisionFor(c, string(account.Platform), CaptureOutcomeTerminalError)
	if !enabled {
		return
	}
	bridge, ok := takeCaptureResult(c)
	if !ok || len(bridge.UpstreamRequest) == 0 {
		return
	}
	limit := s.cfg.Gateway.Capture.MaxBodyBytes
	rawRequest, requestTruncated := SnapshotForCaptureWithFlag(bridge.UpstreamRequest, limit)
	rawResponse, responseTruncated := SnapshotForCaptureWithFlag(responseBody, limit)
	requestHeaders := bridge.RequestHeaders
	if len(requestHeaders) == 0 && resp.Request != nil {
		requestHeaders = redactHTTPHeader(resp.Request.Header)
	}
	responseHeaders := bridge.ResponseHeaders
	if len(responseHeaders) == 0 {
		responseHeaders = redactHTTPHeader(resp.Header)
	}
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         string(account.Platform),
		RequestID:        CaptureRequestID(buildKiroRequestID(resp)),
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: upstreamEndpoint,
		Stream:           stream,
		HTTPStatus:       resp.StatusCode,
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   requestHeaders,
		ResponseHeaders:  responseHeaders,
		Truncated:        requestTruncated || responseTruncated || bridge.Truncated,
		ContentPolicy:    &content,
	})
}

// SubmitWebChatCapture archives a gateway result for the WebChat caller, which
// invokes GatewayService directly and therefore does not pass through the
// normal HTTP handler capture sink.
func (s *GatewayService) SubmitWebChatCapture(result *ForwardResult, account *Account, requestBody []byte, upstreamEndpoint string) {
	if s == nil || s.capturePool == nil || result == nil || account == nil || result.CaptureResponse == nil || result.CaptureContentPolicy == nil {
		return
	}
	limit := 0
	if s.cfg != nil {
		limit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	finalRequest := result.UpstreamRequest
	if finalRequest == nil {
		finalRequest = requestBody
	}
	rawRequest, requestTruncated := SnapshotForCaptureWithFlag(finalRequest, limit)
	rawResponse, responseTruncated := SnapshotForCaptureWithFlag(result.CaptureResponse, limit)
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         string(account.Platform),
		RequestID:        CaptureRequestID(result.RequestID),
		RequestedModel:   result.Model,
		UpstreamModel:    result.UpstreamModel,
		UpstreamEndpoint: firstNonEmpty(result.CaptureUpstreamEndpoint, upstreamEndpoint),
		Stream:           result.Stream,
		HTTPStatus:       result.HTTPStatusForCapture(),
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   result.CaptureRequestHeaders,
		ResponseHeaders:  result.CaptureResponseHeaders,
		Truncated:        requestTruncated || responseTruncated || result.CaptureTruncated,
		ContentPolicy:    result.CaptureContentPolicy,
	})
}

// SubmitWebChatCapture is the OpenAI equivalent. The result carries the exact
// final attempt body after provider-specific rewriting; requestBody is only a
// compatibility fallback for result producers that predate that snapshot.
func (s *OpenAIGatewayService) SubmitWebChatCapture(result *OpenAIForwardResult, account *Account, requestBody []byte, upstreamEndpoint string) {
	if s == nil || s.capturePool == nil || result == nil || account == nil || result.CaptureResponse == nil || result.CaptureContentPolicy == nil {
		return
	}
	limit := 0
	if s.cfg != nil {
		limit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	finalRequest := result.UpstreamRequest
	if finalRequest == nil {
		finalRequest = requestBody
	}
	rawRequest, requestTruncated := SnapshotForCaptureWithFlag(finalRequest, limit)
	rawResponse, responseTruncated := SnapshotForCaptureWithFlag(result.CaptureResponse, limit)
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         string(account.Platform),
		RequestID:        CaptureRequestID(result.RequestID),
		RequestedModel:   result.Model,
		UpstreamModel:    result.UpstreamModel,
		UpstreamEndpoint: firstNonEmpty(result.CaptureUpstreamEndpoint, upstreamEndpoint),
		Stream:           result.Stream,
		HTTPStatus:       result.HTTPStatusForCapture(),
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   result.CaptureRequestHeaders,
		ResponseHeaders:  result.CaptureResponseHeaders,
		Truncated:        requestTruncated || responseTruncated || result.CaptureTruncated,
		ContentPolicy:    result.CaptureContentPolicy,
	})
}
