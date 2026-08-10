package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const captureScopeContextKey = "gateway_capture_scope"
const captureRequestedModelContextKey = "gateway_capture_requested_model"

type captureRequestScope struct {
	policy   CompiledCapturePolicy
	userID   int64
	groupID  int64
	hasGroup bool
}

// PrepareCaptureScope loads one immutable runtime-policy snapshot for the
// authenticated request. Missing services and policy read failures fail closed.
func PrepareCaptureScope(ctx context.Context, c *gin.Context, settings *SettingService, userID int64, groupID *int64) {
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

func (s *GatewayService) captureOutboundRequest(c *gin.Context, account *Account, req *http.Request, body []byte) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled || account == nil {
		return
	}
	if !CaptureMayApplyFor(c, string(account.Platform)) {
		return
	}
	SetCaptureOutboundRequest(c, req, body, s.cfg.Gateway.Capture.MaxBodyBytes)
}
