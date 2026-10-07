package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resetRouteUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *resetRouteUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

func (r *resetRouteUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func TestB8deClaudeResetRoutesRejectNonAdminWithRealAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "synthetic-reset-route-secret", ExpireHour: 1}}
	user := &service.User{ID: 91, Email: "user@example.com", Role: service.RoleUser, Status: service.StatusActive}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	users := service.NewUserService(&resetRouteUserRepo{user: user}, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	adminAuth := servermiddleware.NewAdminAuthMiddleware(auth, users, nil, nil)
	noop := func(c *gin.Context) { c.Next() }
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, OpenAIOAuth: &adminhandler.OpenAIOAuthHandler{}}}
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, servermiddleware.AuditLogMiddleware(noop), servermiddleware.StepUpAuthMiddleware(noop), nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/accounts/1/claude/reset-credits"},
		{http.MethodPost, "/api/v1/admin/accounts/1/claude/reset-credits/redeem"},
	} {
		for _, authorization := range []string{"", "Bearer " + token} {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if authorization == "" {
				require.Equal(t, http.StatusUnauthorized, w.Code)
			} else {
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Body.String(), "Admin access required")
			}
		}
	}
}

// The Claude redeem route consumes an irreversible credit just like the Codex
// reset-quota route, so it must sit behind exactly the same middleware chain.
func TestClaudeResetRedeemRouteMatchesCodexResetProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, OpenAIOAuth: &adminhandler.OpenAIOAuthHandler{}}}
	chains := map[string][]string{}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		names := c.HandlerNames()
		chains[c.FullPath()] = names[:len(names)-1]
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil)

	codex := "/api/v1/admin/openai/accounts/1/reset-quota"
	claude := "/api/v1/admin/accounts/1/claude/reset-credits/redeem"
	for _, path := range []string{codex, claude} {
		for _, auth := range []string{"", "Bearer user-token"} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, nil)
			if auth != "" {
				request.Header.Set("Authorization", auth)
			}
			router.ServeHTTP(recorder, request)
			if auth == "" {
				require.Equal(t, http.StatusUnauthorized, recorder.Code, path)
			} else {
				require.Equal(t, http.StatusForbidden, recorder.Code, path)
			}
		}
	}
	codexChain := chains["/api/v1/admin/openai/accounts/:id/reset-quota"]
	claudeChain := chains["/api/v1/admin/accounts/:id/claude/reset-credits/redeem"]
	require.NotEmpty(t, codexChain)
	require.Equal(t, strings.Join(codexChain, "\n"), strings.Join(claudeChain, "\n"))
}
