package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const cursorDeliveryCaptureContextKey = "cursor_delivery_capture_active"

// beginCursorDeliveryCapture establishes Cursor's caller-protocol capture
// boundary. It deliberately snapshots the inbound JSON and never the Connect
// request synthesized for Cursor's Agent endpoint.
func (s *OpenAIGatewayService) beginCursorDeliveryCapture(
	c *gin.Context,
	account *Account,
	body []byte,
	upstreamModel string,
	stream bool,
) {
	if c == nil || c.Request == nil || account == nil {
		return
	}
	c.Set(cursorDeliveryCaptureContextKey, false)

	// Every valid Cursor forwarding attempt takes typed ownership before any
	// policy or admission guard. A retry therefore retires its predecessor and
	// cannot fall back to the legacy whole-body bridge.
	transitionCaptureAttemptOwner(c, captureAttemptOwnerTyped)
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled || s.capturePool == nil ||
		!CaptureMayApplyFor(c, PlatformCursor) {
		return
	}
	content, enabled := captureContentPolicyForAttempt(c, PlatformCursor)
	if !enabled {
		return
	}

	format := model.PayloadJSON
	if stream {
		format = model.PayloadSSE
	}
	begin := model.Begin{
		CaptureID:        uuid.New(),
		CapturedAt:       time.Now().UTC(),
		RequestID:        CaptureRequestID(""),
		SessionID:        captureSessionID(c),
		Platform:         PlatformCursor,
		RequestedModel:   captureRequestedModel(c),
		UpstreamModel:    strings.TrimSpace(upstreamModel),
		UpstreamEndpoint: cursorAgentEndpoint,
		Stream:           stream,
		Format:           format,
		Policy:           captureModelContentPolicy(content),
	}
	attempt, ok := s.capturePool.Begin(c.Request.Context(), begin)
	if !ok {
		return
	}
	attempt.headerLimit = s.cfg.Gateway.Capture.MaxHeaderBytes
	replaceCaptureAttemptForRequest(c, attempt)
	c.Set(cursorDeliveryCaptureContextKey, true)
	setCaptureAttemptStreamGeometry(c, attempt, stream, true)
	attempt.WriteRequestHeaders(captureHeaderBytes(c.Request.Header, attempt.headerLimit))
	attempt.WriteRequest(body)
}

// CursorDeliveryCaptureActive reports whether the current Cursor request owns
// an admitted typed attempt. Handler-generated terminal bytes use the same
// successful-write sink as normal Cursor delivery only while this is true.
func CursorDeliveryCaptureActive(c *gin.Context) bool {
	if c == nil || captureAttemptForRequest(c) == nil {
		return false
	}
	value, ok := c.Get(cursorDeliveryCaptureContextKey)
	active, valid := value.(bool)
	return ok && valid && active
}

func markCursorDeliveryResponse(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	markCaptureAttemptResponse(c, c.Writer.Status(), c.Writer.Header())
}
