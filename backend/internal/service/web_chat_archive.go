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
		responseComplete ...bool,
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
	responseComplete ...bool,
) {
	if ctx == nil {
		return
	}
	submitter, _ := ctx.Value(webChatFinalGatewayErrorCaptureContextKey{}).(webChatFinalGatewayErrorCaptureSubmitter)
	if submitter == nil {
		return
	}
	submitter.submitWebChatFinalGatewayErrorCapture(
		ctx, c, account, requestedModel, upstreamModel, upstreamEndpoint, stream, resp, responseBody, responseComplete...,
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
	responseCompleteOverride ...bool,
) {
	if s == nil || s.capturePool == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled ||
		!ownsWebChatFinalGatewayErrorCapture(ctx) || account == nil || resp == nil {
		return
	}
	if captureStreamingAttemptPath(c) {
		finishCaptureResponse(resp)
		responseComplete := boundedUpstreamErrorResponseComplete(responseBody, nil, s.upstreamErrorBodyReadLimit())
		if len(responseCompleteOverride) > 0 {
			responseComplete = responseCompleteOverride[0]
		}
		CommitTerminalErrorCaptureAttemptWithCompleteness(c, string(account.Platform), resp.StatusCode, responseComplete)
		return
	}
	content, enabled := CaptureDecisionFor(c, string(account.Platform), CaptureOutcomeTerminalError)
	if !enabled {
		return
	}
	// Some compatibility services submit synchronously from the HTTP error
	// branch while their outer defer has not published the response wrapper yet.
	// Finish first so redirect/fallback request ownership and response metadata
	// are resolved before the bridge is taken.
	finishCaptureResponse(resp)
	bridge, ok := takeCaptureResult(c)
	if !ok || bridge.RequestCaptureInvalid || bridge.UpstreamRequest == nil {
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
	if bridge.ResponseObserved {
		rawResponse, responseTruncated = SnapshotForCaptureWithFlag(bridge.Response, limit)
		responseTruncated = responseTruncated || bridge.ResponseTruncated
	}
	captureModel := firstNonEmpty(bridge.UpstreamModel, upstreamModel)
	captureStream := stream
	if bridge.UpstreamStreamKnown {
		captureStream = bridge.UpstreamStream
	}
	httpStatus := resp.StatusCode
	if bridge.ResponseObserved && bridge.HTTPStatus > 0 {
		httpStatus = bridge.HTTPStatus
	}
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         string(account.Platform),
		RequestID:        CaptureRequestID(captureProviderRequestIDBytes(responseHeaders)),
		RequestedModel:   requestedModel,
		UpstreamModel:    captureModel,
		UpstreamEndpoint: redactCaptureEndpoint(firstNonEmpty(bridge.UpstreamEndpoint, upstreamEndpoint)),
		Stream:           captureStream,
		HTTPStatus:       httpStatus,
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   requestHeaders,
		ResponseHeaders:  responseHeaders,
		Truncated:        requestTruncated || responseTruncated || bridge.Truncated,
		ContentPolicy:    &content,
	})
}

// submitWebChatTerminalCapture owns typed final HTTP failures that are
// returned before a compatibility forwarder has an *http.Response to pass to
// submitWebChatFinalGatewayErrorCapture. Only direct WebChat calls carry the
// ownership token; normal gateway handlers keep their existing terminal sink.
func (s *GatewayService) submitWebChatTerminalCapture(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	failure *UpstreamFailoverError,
) {
	if s == nil || s.capturePool == nil || s.cfg == nil || account == nil || failure == nil ||
		!ownsWebChatFinalGatewayErrorCapture(ctx) {
		return
	}
	platform := firstNonEmpty(failure.Platform, string(account.Platform))
	if captureStreamingAttemptPath(c) {
		if failure.HasUpstreamHTTPResponse {
			CommitTerminalErrorCaptureAttemptWithCompleteness(c, platform, failure.HTTPStatusForCapture(), !failure.CaptureResponseIncomplete)
		} else {
			AbortCaptureAttempt(c)
		}
		return
	}
	if record := BuildTerminalErrorCaptureRecord(c, platform, failure, s.cfg.Gateway.Capture.MaxBodyBytes); record != nil {
		s.capturePool.Submit(record)
	}
}

// SubmitWebChatCapture archives a gateway result for the WebChat caller, which
// invokes GatewayService directly and therefore does not pass through the
// normal HTTP handler capture sink.
func (s *GatewayService) SubmitWebChatCapture(result *ForwardResult, account *Account, requestBody []byte, upstreamEndpoint string) {
	if s == nil || s.capturePool == nil || result == nil || account == nil || result.UpstreamRequest == nil || result.CaptureResponse == nil || result.CaptureContentPolicy == nil {
		return
	}
	limit := 0
	if s.cfg != nil {
		limit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	rawRequest, requestTruncated := SnapshotForCaptureWithFlag(result.UpstreamRequest, limit)
	rawResponse, responseTruncated := SnapshotForCaptureWithFlag(result.CaptureResponse, limit)
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:          time.Now().UTC(),
		Platform:            string(account.Platform),
		RequestID:           CaptureRequestID(result.RequestID),
		RequestedModel:      result.Model,
		UpstreamModel:       result.UpstreamModelForCapture(),
		UpstreamEndpoint:    redactCaptureEndpoint(firstNonEmpty(result.CaptureUpstreamEndpoint, upstreamEndpoint)),
		Stream:              result.StreamForCapture(),
		HTTPStatus:          result.HTTPStatusForCapture(),
		InputTokens:         result.Usage.InputTokens,
		OutputTokens:        result.Usage.OutputTokens,
		CacheReadTokens:     result.Usage.CacheReadInputTokens,
		CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		RawRequest:          rawRequest,
		RawResponse:         rawResponse,
		RequestHeaders:      result.CaptureRequestHeaders,
		ResponseHeaders:     result.CaptureResponseHeaders,
		Truncated:           requestTruncated || responseTruncated || result.CaptureTruncated,
		ContentPolicy:       result.CaptureContentPolicy,
	})
}

// SubmitWebChatCapture is the OpenAI equivalent. The result carries the exact
// final attempt body after provider-specific rewriting; requestBody is only a
// compatibility fallback for result producers that predate that snapshot.
func (s *OpenAIGatewayService) SubmitWebChatCapture(result *OpenAIForwardResult, account *Account, requestBody []byte, upstreamEndpoint string) {
	if s == nil || s.capturePool == nil || result == nil || account == nil || result.UpstreamRequest == nil || result.CaptureResponse == nil || result.CaptureContentPolicy == nil {
		return
	}
	limit := 0
	if s.cfg != nil {
		limit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	rawRequest, requestTruncated := SnapshotForCaptureWithFlag(result.UpstreamRequest, limit)
	rawResponse, responseTruncated := SnapshotForCaptureWithFlag(result.CaptureResponse, limit)
	s.capturePool.Submit(&CaptureRecord{
		CapturedAt:          time.Now().UTC(),
		Platform:            string(account.Platform),
		RequestID:           CaptureRequestID(result.RequestID),
		RequestedModel:      result.Model,
		UpstreamModel:       result.UpstreamModelForCapture(),
		UpstreamEndpoint:    redactCaptureEndpoint(firstNonEmpty(result.CaptureUpstreamEndpoint, upstreamEndpoint)),
		Stream:              result.StreamForCapture(),
		HTTPStatus:          result.HTTPStatusForCapture(),
		InputTokens:         result.Usage.InputTokens,
		OutputTokens:        result.Usage.OutputTokens,
		CacheReadTokens:     result.Usage.CacheReadInputTokens,
		CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		RawRequest:          rawRequest,
		RawResponse:         rawResponse,
		RequestHeaders:      result.CaptureRequestHeaders,
		ResponseHeaders:     result.CaptureResponseHeaders,
		Truncated:           requestTruncated || responseTruncated || result.CaptureTruncated,
		ContentPolicy:       result.CaptureContentPolicy,
	})
}

// SubmitWebChatTerminalCapture owns the final typed HTTP failure for direct
// WebChat callers, which do not pass through the normal OpenAI handler sink.
func (s *OpenAIGatewayService) SubmitWebChatTerminalCapture(c *gin.Context, failure *UpstreamFailoverError) {
	if s == nil || s.capturePool == nil || s.cfg == nil || failure == nil {
		return
	}
	platform := firstNonEmpty(failure.Platform, PlatformOpenAI)
	if captureStreamingAttemptPath(c) {
		if failure.HasUpstreamHTTPResponse {
			CommitTerminalErrorCaptureAttemptWithCompleteness(c, platform, failure.HTTPStatusForCapture(), !failure.CaptureResponseIncomplete)
		} else {
			AbortCaptureAttempt(c)
		}
		return
	}
	if record := BuildTerminalErrorCaptureRecord(c, platform, failure, s.cfg.Gateway.Capture.MaxBodyBytes); record != nil {
		s.capturePool.Submit(record)
	}
}
