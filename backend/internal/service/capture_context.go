package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const captureScopeContextKey = "gateway_capture_scope"
const captureRequestedModelContextKey = "gateway_capture_requested_model"
const captureAttemptRequestContextKey = "gateway_capture_attempt"
const captureStreamingAttemptPathContextKey = "gateway_capture_streaming_attempt_path"

type captureRequestScope struct {
	policy   CompiledCapturePolicy
	userID   int64
	groupID  int64
	hasGroup bool
}

type captureAttemptRequestSlot struct {
	mu      sync.Mutex
	attempt *CaptureAttempt
}

type captureAttemptContextKey struct{}

// PrepareCaptureScope performs one synchronous transport admission and stores
// the resulting attempt in the returned standard context. A failed admission
// is fail-open and returns the original context unchanged.
func PrepareCaptureScope(ctx context.Context, transport protocol.Transport, begin model.Begin) (context.Context, *CaptureAttempt) {
	if ctx == nil {
		ctx = context.Background()
	}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(ctx, begin)
	if !ok {
		return ctx, nil
	}
	return context.WithValue(ctx, captureAttemptContextKey{}, attempt), attempt
}

func captureAttemptFromContext(ctx context.Context) *CaptureAttempt {
	if ctx == nil {
		return nil
	}
	attempt, _ := ctx.Value(captureAttemptContextKey{}).(*CaptureAttempt)
	return attempt
}

// PrepareCapturePolicyScope loads one immutable runtime-policy snapshot for the
// authenticated request. Missing services and policy read failures fail closed.
func PrepareCapturePolicyScope(ctx context.Context, c *gin.Context, settings *SettingService, userID int64, groupID *int64) {
	if c == nil || settings == nil || userID <= 0 {
		return
	}
	// Static provisioning off is the default and must be true zero-cost: do not
	// touch PostgreSQL or start a policy refresh when no capture pool can exist.
	if settings.cfg == nil || !settings.cfg.Gateway.Capture.Enabled {
		return
	}
	compiled := settings.GetCompiledCaptureRuntimePolicyHot()
	if !compiled.Enabled() {
		return
	}
	setCompiledCaptureScopeForTest(c, compiled, userID, groupID)
}

func setCompiledCaptureScopeForTest(c *gin.Context, policy CompiledCapturePolicy, userID int64, groupID *int64) {
	if c == nil {
		return
	}
	scope := &captureRequestScope{policy: policy, userID: userID}
	if groupID != nil {
		scope.groupID = *groupID
		scope.hasGroup = true
	}
	c.Set(captureScopeContextKey, scope)
}

func captureScopeFrom(c *gin.Context) (*captureRequestScope, bool) {
	if c == nil {
		return nil, false
	}
	value, exists := c.Get(captureScopeContextKey)
	if !exists {
		return nil, false
	}
	scope, ok := value.(*captureRequestScope)
	return scope, ok && scope != nil
}

// SetCaptureRequestedModel preserves the client-visible model independently
// from per-attempt provider mapping. ResetCaptureExchange deliberately does not
// clear it because failover attempts belong to the same inbound request.
func SetCaptureRequestedModel(c *gin.Context, model string) {
	if c == nil {
		return
	}
	if model = strings.TrimSpace(model); model != "" {
		c.Set(captureRequestedModelContextKey, model)
	}
}

func captureRequestedModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(captureRequestedModelContextKey)
	model, _ := value.(string)
	return strings.TrimSpace(model)
}

// CaptureDecisionFor makes the final platform/outcome decision without any DB
// access. Callers should only allocate capture buffers after this returns true.
func CaptureDecisionFor(c *gin.Context, platform string, outcome CaptureOutcome) (CaptureContentPolicy, bool) {
	scope, ok := captureScopeFrom(c)
	if !ok {
		return CaptureContentPolicy{}, false
	}
	var groupID *int64
	if scope.hasGroup {
		groupID = &scope.groupID
	}
	return scope.policy.Decide(strings.ToLower(strings.TrimSpace(platform)), outcome, scope.userID, groupID)
}

// CaptureMayApplyFor is the allocation guard used before an upstream result is
// known. It is true only when at least one configured terminal outcome matches.
func CaptureMayApplyFor(c *gin.Context, platform string) bool {
	if _, ok := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); ok {
		return true
	}
	_, ok := CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
	return ok
}

func captureContentPolicyForAttempt(c *gin.Context, platform string) (CaptureContentPolicy, bool) {
	if content, ok := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); ok {
		return content, true
	}
	return CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
}

func captureAttemptSlotForRequest(c *gin.Context, create bool) *captureAttemptRequestSlot {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(captureAttemptRequestContextKey); ok {
		if slot, valid := value.(*captureAttemptRequestSlot); valid && slot != nil {
			return slot
		}
	}
	if !create {
		return nil
	}
	slot := &captureAttemptRequestSlot{}
	c.Set(captureAttemptRequestContextKey, slot)
	return slot
}

func markCaptureStreamingAttemptPath(c *gin.Context) {
	if c != nil {
		c.Set(captureStreamingAttemptPathContextKey, true)
	}
}

func captureStreamingAttemptPath(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get(captureStreamingAttemptPathContextKey)
	enabled, _ := value.(bool)
	return exists && enabled
}

func captureAttemptForRequest(c *gin.Context) *CaptureAttempt {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.attempt
}

func replaceCaptureAttemptForRequest(c *gin.Context, next *CaptureAttempt) {
	slot := captureAttemptSlotForRequest(c, next != nil)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	previous := slot.attempt
	slot.attempt = next
	slot.mu.Unlock()
	if previous != nil && previous != next {
		previous.Abort()
	}
}

func abortCaptureAttemptForRequest(c *gin.Context) {
	replaceCaptureAttemptForRequest(c, nil)
}

func takeCaptureAttemptForRequest(c *gin.Context) *CaptureAttempt {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	attempt := slot.attempt
	slot.attempt = nil
	slot.mu.Unlock()
	return attempt
}

// AbortCaptureAttempt transfers and terminates the request's current attempt.
// Repeated terminal paths are inert because the slot is cleared first.
func AbortCaptureAttempt(c *gin.Context) {
	if attempt := takeCaptureAttemptForRequest(c); attempt != nil {
		attempt.Abort()
	}
}

// CommitCaptureAttempt is the sole outcome-aware terminal sink. Outcome-off
// policy aborts the attempt without finalizing or committing a sidecar record.
func CommitCaptureAttempt(c *gin.Context, platform string, outcome CaptureOutcome, final model.Final) bool {
	attempt := takeCaptureAttemptForRequest(c)
	if attempt == nil {
		return false
	}
	if _, enabled := CaptureDecisionFor(c, platform, outcome); !enabled {
		attempt.Abort()
		return false
	}
	if !attempt.Finalize(final) {
		attempt.Abort()
		return false
	}
	return attempt.Commit()
}

// CommitCapturePreCommitDisconnect preserves the naturally observed partial
// provider exchange while identifying that no client-visible commit occurred.
func CommitCapturePreCommitDisconnect(c *gin.Context, platform string, final model.Final) bool {
	final.StopReason = "pre_commit_disconnect"
	final.ResponseComplete = false
	return CommitCaptureAttempt(c, platform, CaptureOutcomeTerminalError, final)
}

// CommitTerminalErrorCaptureAttempt commits only an observed final provider
// HTTP response. Callers must reject local/synthetic failures before invoking
// it.
func CommitTerminalErrorCaptureAttempt(c *gin.Context, platform string, httpStatus int) bool {
	return CommitCaptureAttempt(c, platform, CaptureOutcomeTerminalError, model.Final{
		HTTPStatus:       boundedCaptureUint16(httpStatus),
		ResponseComplete: true,
	})
}

func boundedCaptureUint16(value int) uint16 {
	if value <= 0 {
		return 0
	}
	if value > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(value)
}

func boundedCaptureUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}
	if uint64(value) > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(value)
}

// CommitForwardCaptureAttempt maps the existing final usage/billing result to
// the sidecar terminal contract. It is called only from that exact-once sink.
func CommitForwardCaptureAttempt(c *gin.Context, platform string, result *ForwardResult) bool {
	if result == nil {
		AbortCaptureAttempt(c)
		return false
	}
	final := model.Final{
		HTTPStatus:          boundedCaptureUint16(result.HTTPStatusForCapture()),
		InputTokens:         boundedCaptureUint32(result.Usage.InputTokens),
		OutputTokens:        boundedCaptureUint32(result.Usage.OutputTokens),
		CacheReadTokens:     boundedCaptureUint32(result.Usage.CacheReadInputTokens),
		CacheCreationTokens: boundedCaptureUint32(result.Usage.CacheCreationInputTokens),
		ResponseComplete:    !result.UpstreamFailed && !result.ClientDisconnect,
	}
	if result.ClientDisconnect {
		return CommitCapturePreCommitDisconnect(c, platform, final)
	}
	outcome := CaptureOutcomeSuccess
	if result.UpstreamFailed || result.CaptureTerminalError {
		outcome = CaptureOutcomeTerminalError
	}
	return CommitCaptureAttempt(c, platform, outcome, final)
}

// CommitOpenAIForwardCaptureAttempt is the OpenAI-family equivalent of the
// gateway sink above.
func CommitOpenAIForwardCaptureAttempt(c *gin.Context, platform string, result *OpenAIForwardResult) bool {
	if result == nil {
		AbortCaptureAttempt(c)
		return false
	}
	final := model.Final{
		HTTPStatus:          boundedCaptureUint16(result.HTTPStatusForCapture()),
		InputTokens:         boundedCaptureUint32(result.Usage.InputTokens),
		OutputTokens:        boundedCaptureUint32(result.Usage.OutputTokens),
		CacheReadTokens:     boundedCaptureUint32(result.Usage.CacheReadInputTokens),
		CacheCreationTokens: boundedCaptureUint32(result.Usage.CacheCreationInputTokens),
		ResponseComplete:    !result.UpstreamFailed && !result.ClientDisconnect,
	}
	if result.ClientDisconnect {
		return CommitCapturePreCommitDisconnect(c, platform, final)
	}
	outcome := CaptureOutcomeSuccess
	if result.UpstreamFailed || result.CaptureTerminalError {
		outcome = CaptureOutcomeTerminalError
	}
	return CommitCaptureAttempt(c, platform, outcome, final)
}

func captureModelContentPolicy(policy CaptureContentPolicy) model.ContentPolicy {
	return model.ContentPolicy{
		StoreRequestBody:     policy.RawRequest,
		StoreResponseBody:    policy.RawResponse,
		StoreRequestHeaders:  policy.RequestHeaders,
		StoreResponseHeaders: policy.ResponseHeaders,
	}
}

func captureHeaderBytes(header http.Header, limit int) []byte {
	if limit <= 0 || limit > 1<<20 {
		limit = 1 << 20
	}
	encoded := redactHTTPHeader(header)
	if len(encoded) > limit {
		encoded = encoded[:limit]
	}
	return encoded
}

func beginCaptureAttemptForWireRequest(
	ctx context.Context,
	c *gin.Context,
	pool *ConversationCapturePool,
	platform string,
	req *http.Request,
	body []byte,
	headerLimit int,
) (*CaptureAttempt, bool) {
	abortCaptureAttemptForRequest(c)
	markCaptureStreamingAttemptPath(c)
	if pool == nil || req == nil {
		return nil, false
	}
	content, enabled := captureContentPolicyForAttempt(c, platform)
	if !enabled {
		return nil, false
	}
	endpoint := ""
	if req.URL != nil {
		endpoint = redactCaptureURL(req.URL)
	}
	upstreamModel, stream, streamKnown := extractCaptureProviderRequestMeta(platform, body, endpoint)
	format := model.PayloadJSON
	if streamKnown && stream {
		format = model.PayloadSSE
	}
	begin := model.Begin{
		CaptureID:        uuid.New(),
		CapturedAt:       time.Now().UTC(),
		RequestID:        CaptureRequestID(""),
		Platform:         strings.ToLower(strings.TrimSpace(platform)),
		RequestedModel:   captureRequestedModel(c),
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: endpoint,
		Stream:           stream,
		Format:           format,
		Policy:           captureModelContentPolicy(content),
	}
	attempt, ok := pool.Begin(ctx, begin)
	if !ok {
		return nil, false
	}
	attempt.headerLimit = headerLimit
	replaceCaptureAttemptForRequest(c, attempt)
	attempt.WriteRequestHeaders(captureHeaderBytes(req.Header, headerLimit))
	attempt.WriteRequest(body)
	return attempt, true
}

func (s *GatewayService) captureOutboundRequest(c *gin.Context, account *Account, req *http.Request, body []byte) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled || account == nil {
		return
	}
	if !CaptureMayApplyFor(c, string(account.Platform)) {
		return
	}
	// KIRO keeps its existing forwarding/capture contract until its dedicated
	// upstream-tracked migration. This task must not alter KIRO semantics.
	if account.Platform == PlatformKiro || isWebChatCaptureOwner(c) {
		setCapturePlatform(c, string(account.Platform))
		SetCaptureOutboundRequest(c, req, body, s.cfg.Gateway.Capture.MaxBodyBytes)
		return
	}
	beginCaptureAttemptForWireRequest(c.Request.Context(), c, s.capturePool, string(account.Platform), req, body, s.cfg.Gateway.Capture.MaxHeaderBytes)
}

// beginGatewayCaptureResponse attaches the raw provider-response tee for the
// same policy-approved attempt created by captureOutboundRequest. Keeping this
// guard next to the request snapshot prevents response buffering when runtime
// capture policy cannot match the request.
func (s *GatewayService) beginGatewayCaptureResponse(c *gin.Context, account *Account, resp *http.Response) func() {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled || account == nil ||
		!CaptureMayApplyFor(c, string(account.Platform)) {
		return func() {}
	}
	if account.Platform == PlatformKiro {
		return beginCaptureResponse(c, resp, true, s.cfg.Gateway.Capture.MaxBodyBytes)
	}
	attempt := captureAttemptForRequest(c)
	if attempt == nil || resp == nil || resp.Body == nil {
		return func() {}
	}
	attempt.WriteResponseHeaders(captureHeaderBytes(resp.Header, s.cfg.Gateway.Capture.MaxHeaderBytes))
	resp.Body = newCaptureResponseReader(resp.Body, attempt)
	return func() {}
}
