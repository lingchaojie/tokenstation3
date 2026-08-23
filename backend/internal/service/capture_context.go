package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
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

type captureRequestScope struct {
	policy    CompiledCapturePolicy
	userID    int64
	groupID   int64
	hasGroup  bool
	sessionID string
}

type captureAttemptRequestSlot struct {
	mu                 sync.Mutex
	attempt            *CaptureAttempt
	owner              captureAttemptOwner
	responseHTTPStatus int
	responseMarked     bool
	stream             bool
	streamKnown        bool
}

type captureAttemptOwner uint8

const (
	captureAttemptOwnerNone captureAttemptOwner = iota
	captureAttemptOwnerTyped
	captureAttemptOwnerLegacy
)

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

func captureSessionID(c *gin.Context) string {
	scope, ok := captureScopeFrom(c)
	if !ok {
		return ""
	}
	return strings.TrimSpace(scope.sessionID)
}

// SetCaptureSessionID attaches a privacy-safe, stable conversation identity to
// subsequent wire attempts. Per-turn routing fallbacks are deliberately
// excluded because they change as the message history grows.
func SetCaptureSessionID(c *gin.Context, parsed *ParsedRequest, sessionHash string) {
	scope, ok := captureScopeFrom(c)
	if !ok {
		return
	}
	scope.sessionID = ""
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" || !captureSessionHashIsStable(parsed) {
		return
	}
	payload := "capture-session-v1\x00" + strconv.FormatInt(scope.userID, 10) + "\x00" + sessionHash
	digest := sha256.Sum256([]byte(payload))
	scope.sessionID = "cap_session_" + hex.EncodeToString(digest[:])
}

func captureSessionHashIsStable(parsed *ParsedRequest) bool {
	if parsed == nil {
		return false
	}
	if strings.TrimSpace(parsed.ExplicitSessionID) != "" || strings.TrimSpace(parsed.BodySessionID) != "" {
		return true
	}
	if metadata := ParseMetadataUserID(parsed.MetadataUserID); metadata != nil && strings.TrimSpace(metadata.SessionID) != "" {
		return true
	}
	group := parsed.Group
	kiroAutoSticky := group != nil && (group.HasMixedKiroAutoStickyAccount || group.EffectiveKiroAutoStickyEnabled())
	if !kiroAutoSticky {
		return false
	}
	return extractTextFromSystemRaw(parsed.SystemRaw()) != "" || extractFirstUserMessageTextFromRaw(parsed.MessagesRaw()) != ""
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
	normalizedPlatform := strings.ToLower(strings.TrimSpace(platform))
	requestedModel := captureRequestedModel(c)
	if requestedModel == "" {
		return scope.policy.Decide(normalizedPlatform, outcome, scope.userID, groupID)
	}
	return scope.policy.DecideForModel(normalizedPlatform, requestedModel, outcome, scope.userID, groupID)
}

// CaptureMayApplyFor is the allocation guard used before an upstream result is
// known. It is true when a configured terminal outcome or possible client
// disconnect matches.
func CaptureMayApplyFor(c *gin.Context, platform string) bool {
	if _, ok := CaptureDecisionFor(c, platform, captureOutcomeClientDisconnect); ok {
		return true
	}
	if _, ok := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); ok {
		return true
	}
	_, ok := CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
	return ok
}

func captureContentPolicyForAttempt(c *gin.Context, platform string) (CaptureContentPolicy, bool) {
	if content, ok := CaptureDecisionFor(c, platform, captureOutcomeClientDisconnect); ok {
		return content, true
	}
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

func captureStreamingAttemptPath(c *gin.Context) bool {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.owner == captureAttemptOwnerTyped
}

// CaptureUsesStreamingAttempt reports whether the current provider owner
// attempted typed admission. It remains true after admission failure so the
// same owner cannot fall back to a whole-body bridge.
func CaptureUsesStreamingAttempt(c *gin.Context) bool {
	return captureStreamingAttemptPath(c)
}

func transitionCaptureAttemptOwner(c *gin.Context, owner captureAttemptOwner) {
	slot := captureAttemptSlotForRequest(c, owner != captureAttemptOwnerNone)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	previous := slot.attempt
	slot.attempt = nil
	slot.owner = owner
	slot.responseHTTPStatus = 0
	slot.responseMarked = false
	slot.stream = false
	slot.streamKnown = false
	slot.mu.Unlock()
	if previous != nil {
		previous.Abort()
	}
}

func markCaptureLegacyOwner(c *gin.Context) {
	if captureStreamingAttemptPath(c) {
		transitionCaptureAttemptOwner(c, captureAttemptOwnerLegacy)
		return
	}
	slot := captureAttemptSlotForRequest(c, true)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.owner = captureAttemptOwnerLegacy
	slot.mu.Unlock()
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

func captureAttemptUsableForRequest(c *gin.Context) bool {
	attempt := captureAttemptForRequest(c)
	return attempt != nil && attempt.usable()
}

func replaceCaptureAttemptForRequest(c *gin.Context, next *CaptureAttempt) {
	slot := captureAttemptSlotForRequest(c, next != nil)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	previous := slot.attempt
	slot.attempt = next
	if previous != next {
		slot.responseHTTPStatus = 0
		slot.responseMarked = false
		slot.stream = false
		slot.streamKnown = false
	}
	slot.mu.Unlock()
	if previous != nil && previous != next {
		previous.Abort()
	}
}

func takeCaptureAttemptForRequest(c *gin.Context) *CaptureAttempt {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	attempt := slot.attempt
	slot.attempt = nil
	slot.owner = captureAttemptOwnerNone
	slot.responseHTTPStatus = 0
	slot.responseMarked = false
	slot.stream = false
	slot.streamKnown = false
	slot.mu.Unlock()
	return attempt
}

func setCaptureAttemptResponseHTTPStatus(c *gin.Context, attempt *CaptureAttempt, status int) {
	if attempt == nil || status < 100 || status > 599 {
		return
	}
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.owner == captureAttemptOwnerTyped && slot.attempt == attempt {
		slot.responseHTTPStatus = status
	}
	slot.mu.Unlock()
}

// markCaptureAttemptResponse atomically gives the first caller response write
// ownership of the delivered HTTP metadata. Later writes and concurrent
// terminal paths cannot replace that first observed status/header snapshot.
func markCaptureAttemptResponse(c *gin.Context, status int, headers http.Header) bool {
	if status < 100 || status > 599 {
		return false
	}
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.owner != captureAttemptOwnerTyped || slot.attempt == nil || slot.responseMarked {
		return false
	}
	slot.responseMarked = true
	slot.responseHTTPStatus = status
	return slot.attempt.WriteResponseHeaders(captureHeaderBytes(headers, slot.attempt.headerLimit))
}

func captureAttemptResponseHTTPStatus(c *gin.Context) int {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return 0
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.owner != captureAttemptOwnerTyped || slot.attempt == nil {
		return 0
	}
	return slot.responseHTTPStatus
}

func setCaptureAttemptStreamGeometry(c *gin.Context, attempt *CaptureAttempt, stream, known bool) {
	if attempt == nil || !known {
		return
	}
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.owner == captureAttemptOwnerTyped && slot.attempt == attempt {
		slot.stream = stream
		slot.streamKnown = true
	}
	slot.mu.Unlock()
}

func captureAttemptStreamGeometry(c *gin.Context) (bool, bool) {
	slot := captureAttemptSlotForRequest(c, false)
	if slot == nil {
		return false, false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.owner != captureAttemptOwnerTyped || slot.attempt == nil || !slot.streamKnown {
		return false, false
	}
	return slot.stream, true
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

func captureTerminalOutcome(upstreamFailed, terminalError, clientDisconnect bool) CaptureOutcome {
	if upstreamFailed || terminalError {
		return CaptureOutcomeTerminalError
	}
	if clientDisconnect {
		return captureOutcomeClientDisconnect
	}
	return CaptureOutcomeSuccess
}

func captureFinalResponseComplete(stream, upstreamFailed, terminalError, clientDisconnect, explicitlyComplete bool) bool {
	if explicitlyComplete {
		return true
	}
	if stream || upstreamFailed || terminalError || clientDisconnect {
		return false
	}
	// Non-streaming success is returned only after the provider body reader has
	// reached the verified full-body EOF boundary.
	return true
}

// CommitTerminalErrorCaptureAttempt commits only an observed final provider
// HTTP response. Callers must reject local/synthetic failures before invoking
// it.
func CommitTerminalErrorCaptureAttempt(c *gin.Context, platform string, httpStatus int) bool {
	return CommitTerminalErrorCaptureAttemptWithCompleteness(c, platform, httpStatus, true)
}

// CommitTerminalErrorCaptureAttemptWithCompleteness preserves whether the
// provider response reached its terminal boundary before the failure.
func CommitTerminalErrorCaptureAttemptWithCompleteness(c *gin.Context, platform string, httpStatus int, responseComplete bool) bool {
	return CommitCaptureAttempt(c, platform, CaptureOutcomeTerminalError, model.Final{
		HTTPStatus:       boundedCaptureUint16(httpStatus),
		ResponseComplete: responseComplete,
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
	httpStatus := result.HTTPStatusForCapture()
	if observed := captureAttemptResponseHTTPStatus(c); observed != 0 {
		httpStatus = observed
	}
	stream, streamKnown := captureAttemptStreamGeometry(c)
	if !streamKnown {
		stream = result.StreamForCapture()
	}
	final := model.Final{
		HTTPStatus:          boundedCaptureUint16(httpStatus),
		InputTokens:         boundedCaptureUint32(result.Usage.InputTokens),
		OutputTokens:        boundedCaptureUint32(result.Usage.OutputTokens),
		CacheReadTokens:     boundedCaptureUint32(result.Usage.CacheReadInputTokens),
		CacheCreationTokens: boundedCaptureUint32(result.Usage.CacheCreationInputTokens),
		ResponseComplete: captureFinalResponseComplete(
			stream,
			result.UpstreamFailed,
			result.CaptureTerminalError,
			result.ClientDisconnect,
			result.CaptureResponseComplete,
		),
	}
	outcome := captureTerminalOutcome(result.UpstreamFailed, result.CaptureTerminalError, result.ClientDisconnect)
	return CommitCaptureAttempt(c, platform, outcome, final)
}

// CommitOpenAIForwardCaptureAttempt is the OpenAI-family equivalent of the
// gateway sink above.
func CommitOpenAIForwardCaptureAttempt(c *gin.Context, platform string, result *OpenAIForwardResult) bool {
	if result == nil {
		AbortCaptureAttempt(c)
		return false
	}
	httpStatus := result.HTTPStatusForCapture()
	if observed := captureAttemptResponseHTTPStatus(c); observed != 0 {
		httpStatus = observed
	}
	stream, streamKnown := captureAttemptStreamGeometry(c)
	if !streamKnown {
		stream = result.StreamForCapture()
	}
	responseComplete := captureFinalResponseComplete(
		stream,
		result.UpstreamFailed,
		result.CaptureTerminalError,
		result.ClientDisconnect,
		result.CaptureResponseComplete,
	)
	if strings.EqualFold(strings.TrimSpace(platform), PlatformCursor) {
		responseComplete = result.CaptureResponseComplete
	}
	final := model.Final{
		HTTPStatus:          boundedCaptureUint16(httpStatus),
		InputTokens:         boundedCaptureUint32(result.Usage.InputTokens),
		OutputTokens:        boundedCaptureUint32(result.Usage.OutputTokens),
		CacheReadTokens:     boundedCaptureUint32(result.Usage.CacheReadInputTokens),
		CacheCreationTokens: boundedCaptureUint32(result.Usage.CacheCreationInputTokens),
		ResponseComplete:    responseComplete,
	}
	outcome := captureTerminalOutcome(result.UpstreamFailed, result.CaptureTerminalError, result.ClientDisconnect)
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
	encoded := redactHTTPHeader(header)
	if limit > 0 && len(encoded) > limit {
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
	transitionCaptureAttemptOwner(c, captureAttemptOwnerTyped)
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
	format := captureWirePayloadFormat(platform, endpoint, stream, streamKnown)
	begin := model.Begin{
		CaptureID:        uuid.New(),
		CapturedAt:       time.Now().UTC(),
		RequestID:        CaptureRequestID(""),
		SessionID:        captureSessionID(c),
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
	setCaptureAttemptStreamGeometry(c, attempt, stream, streamKnown)
	attempt.WriteRequestHeaders(captureHeaderBytes(req.Header, headerLimit))
	attempt.WriteRequest(body)
	return attempt, true
}

func captureWirePayloadFormat(platform, endpoint string, stream, streamKnown bool) model.PayloadFormat {
	if strings.EqualFold(strings.TrimSpace(platform), PlatformKiro) && isKiroNativeEventStreamEndpoint(endpoint) {
		return model.PayloadAWSEventStream
	}
	if !streamKnown || !stream {
		return model.PayloadJSON
	}
	if isBedrockStreamingCaptureEndpoint(endpoint) {
		return model.PayloadAWSEventStream
	}
	return model.PayloadSSE
}

func (s *GatewayService) captureOutboundRequest(c *gin.Context, account *Account, req *http.Request, body []byte) {
	// Every wire attempt owns a fresh capture boundary, including retries whose
	// platform or current runtime policy makes capture ineligible. Retire any
	// prior typed owner before an admission guard can return.
	transitionCaptureAttemptOwner(c, captureAttemptOwnerNone)
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled || account == nil {
		return
	}
	if !CaptureMayApplyFor(c, string(account.Platform)) {
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
	attempt := captureAttemptForRequest(c)
	if attempt == nil || resp == nil {
		return func() {}
	}
	setCaptureAttemptResponseHTTPStatus(c, attempt, resp.StatusCode)
	attempt.WriteResponseHeaders(captureHeaderBytes(resp.Header, s.cfg.Gateway.Capture.MaxHeaderBytes))
	if resp.Body != nil {
		resp.Body = newCaptureResponseReader(resp.Body, attempt)
	}
	return func() {}
}
