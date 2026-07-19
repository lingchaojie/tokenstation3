package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type alvinRouteSettingRepo struct {
	service.SettingRepository
}

func (r *alvinRouteSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyAlvin {
		return "false", nil
	}
	return "", service.ErrSettingNotFound
}

func TestAuthRoutesExposeAlvinWithoutJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingService := service.NewSettingService(&alvinRouteSettingRepo{}, &config.Config{})
	settingHandler := handler.NewSettingHandler(settingService, "test-version")
	router := gin.New()
	v1 := router.Group("/api/v1")
	jwtCalls := 0
	jwtAuth := servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		jwtCalls++
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	RegisterAuthRoutes(v1, &handler.Handlers{
		Auth:    &handler.AuthHandler{},
		Setting: settingHandler,
	}, jwtAuth, nil, settingService)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Zero(t, jwtCalls)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"code":0,"message":"success","data":{"alvin":false}}`, recorder.Body.String())
}
