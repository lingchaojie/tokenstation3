// Package securityaudit retains the generic gateway security-audit adapter.
// Prompt Audit is intentionally excluded; this package only delegates to the
// existing local content-moderation service.
package securityaudit

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type DecisionKind string

const (
	DecisionAllow        DecisionKind = "allow"
	DecisionBlock        DecisionKind = "block"
	ErrorCodeUnavailable              = "content_policy_unavailable"
)

type Request struct {
	RequestID, Username, UserEmail, APIKeyName, GroupName string
	Provider, Endpoint, Protocol, Model, Stage            string
	UserID, APIKeyID                                      int64
	GroupID                                               *int64
	Body                                                  []byte
}

type LegacyDecision struct {
	Allowed, Blocked, Flagged  bool
	Message, ErrorCode, Action string
	StatusCode                 int
}

type Decision struct {
	Kind           DecisionKind
	HTTPStatus     int
	ErrorCode      string
	ClientMessage  string
	Legacy         *LegacyDecision
	AllowNextStage bool
}

type LegacyEngine interface {
	Check(context.Context, Request) (*LegacyDecision, error)
}

type Coordinator struct{ legacy LegacyEngine }

func NewCoordinator(legacy LegacyEngine, _ any) *Coordinator { return &Coordinator{legacy: legacy} }

func (c *Coordinator) Check(ctx context.Context, req Request) Decision {
	decision := Decision{Kind: DecisionAllow, HTTPStatus: http.StatusOK, AllowNextStage: true}
	if c == nil || c.legacy == nil {
		return decision
	}
	legacy, err := c.legacy.Check(ctx, req)
	if err != nil || legacy == nil {
		return decision
	}
	decision.Legacy = legacy
	if legacy.Blocked {
		decision.Kind = DecisionBlock
		decision.HTTPStatus = legacy.StatusCode
		if decision.HTTPStatus < 400 || decision.HTTPStatus > 599 {
			decision.HTTPStatus = http.StatusForbidden
		}
		decision.ErrorCode = "content_policy_violation"
		decision.ClientMessage = legacy.Message
		decision.AllowNextStage = false
	}
	return decision
}

type LegacyModerationAdapter struct {
	service *service.ContentModerationService
}

func NewLegacyModerationAdapter(svc *service.ContentModerationService) LegacyEngine {
	return &LegacyModerationAdapter{service: svc}
}

func (a *LegacyModerationAdapter) Check(ctx context.Context, req Request) (*LegacyDecision, error) {
	if a == nil || a.service == nil {
		return nil, nil
	}
	decision, err := a.service.Check(ctx, service.ContentModerationCheckInput{
		RequestID: req.RequestID, UserID: req.UserID, UserEmail: req.UserEmail,
		APIKeyID: req.APIKeyID, APIKeyName: req.APIKeyName, GroupID: cloneInt64Ptr(req.GroupID),
		GroupName: req.GroupName, Endpoint: req.Endpoint, Provider: req.Provider,
		Model: req.Model, Protocol: req.Protocol, Body: req.Body,
	})
	if err != nil || decision == nil {
		return nil, err
	}
	return &LegacyDecision{
		Allowed: decision.Allowed, Blocked: decision.Blocked, Flagged: decision.Flagged,
		Message: decision.Message, StatusCode: decision.StatusCode,
		ErrorCode: "content_policy_violation", Action: decision.Action,
	}, nil
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
