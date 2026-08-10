package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCaptureSettingsRoutesUseProtectedAdminGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authCalls := 0
	admin := router.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		authCalls++
		c.Next()
	})
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Capture: adminhandler.NewCaptureHandler(nil)}}
	registerCaptureSettingsRoutes(admin, handlers)

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/capture-settings"},
		{http.MethodPut, "/api/v1/admin/capture-settings"},
		{http.MethodGet, "/api/v1/admin/capture-settings/history?range=24h"},
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(test.method, test.path, nil)
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusInternalServerError, recorder.Code)
	}
	require.Equal(t, 3, authCalls)
}
