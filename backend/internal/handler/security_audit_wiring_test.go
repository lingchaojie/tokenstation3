package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type task6LegacyAuditEngine struct{ calls int }

func (e *task6LegacyAuditEngine) Check(context.Context, securityaudit.Request) (*securityaudit.LegacyDecision, error) {
	e.calls++
	return &securityaudit.LegacyDecision{Blocked: true, Message: "blocked", StatusCode: http.StatusForbidden}, nil
}

func TestGatewayHandlerProvidersInjectLegacyOnlyCoordinatorAndCompileCaptureConstructors(t *testing.T) {
	coordinator := securityaudit.NewCoordinator(nil, nil)

	gateway := ProvideGatewayHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, coordinator, nil)
	require.Same(t, coordinator, gateway.securityAuditCoordinator)

	openAI := ProvideOpenAIGatewayHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, coordinator, nil)
	require.Same(t, coordinator, openAI.securityAuditCoordinator)
}

func TestRunSecurityAuditHTTPUsesInjectedLegacyCoordinator(t *testing.T) {
	engine := &task6LegacyAuditEngine{}
	coordinator := securityaudit.NewCoordinator(engine, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	decision := runSecurityAudit(c, nil, coordinator, nil, &service.APIKey{ID: 7}, middleware.AuthSubject{UserID: 8}, service.ContentModerationProtocolAnthropicMessages, "claude", []byte(`{"messages":[]}`), "http")

	require.NotNil(t, decision)
	require.False(t, decision.AllowNextStage)
	require.Equal(t, securityaudit.DecisionBlock, decision.Kind)
	require.Equal(t, 1, engine.calls)
}
