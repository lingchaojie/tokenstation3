//go:build unit

package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func routeSet(router *gin.Engine) map[string]struct{} {
	routes := make(map[string]struct{}, len(router.Routes()))
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	return routes
}

func TestAuthenticationRolloutRoutesExcludePasskeyAndCaptchaStarts(t *testing.T) {
	authRouter := newAuthRoutesTestRouter(nil)
	authRoutes := routeSet(authRouter)

	for _, route := range []string{
		"POST /api/v1/auth/passkey/login/begin",
		"POST /api/v1/auth/passkey/login/finish",
		"POST /api/v1/auth/oauth/linuxdo/start",
		"POST /api/v1/auth/oauth/github/start",
		"POST /api/v1/auth/oauth/google/start",
		"POST /api/v1/auth/oauth/wechat/start",
		"POST /api/v1/auth/oauth/oidc/start",
		"POST /api/v1/auth/oauth/dingtalk/start",
	} {
		_, exposed := authRoutes[route]
		require.False(t, exposed, "deferred authentication route must not be registered: %s", route)
	}
	for _, route := range []string{
		"GET /api/v1/auth/oauth/linuxdo/start",
		"GET /api/v1/auth/oauth/github/start",
		"GET /api/v1/auth/oauth/google/start",
		"GET /api/v1/auth/oauth/wechat/start",
		"GET /api/v1/auth/oauth/oidc/start",
		"GET /api/v1/auth/oauth/dingtalk/start",
	} {
		_, exposed := authRoutes[route]
		require.True(t, exposed, "existing OAuth GET route must remain registered: %s", route)
	}

	userRouter := gin.New()
	RegisterUserRoutes(
		userRouter.Group("/api/v1"),
		&handler.Handlers{},
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		nil,
	)
	for key := range routeSet(userRouter) {
		require.NotContains(t, key, "/api/v1/user/passkeys", "authenticated passkey routes must remain hidden")
	}

	_, stepUpExposed := routeSet(userRouter)[http.MethodPost+" /api/v1/user/totp/step-up"]
	require.True(t, stepUpExposed, "dormant backend step-up route foundation must remain registered")
}
