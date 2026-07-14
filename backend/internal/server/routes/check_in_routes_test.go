package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterCheckInRoutes_RegistersUserAndAdminContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{
		CheckIn: &handler.CheckInHandler{},
		Admin: &handler.AdminHandlers{
			CheckIn: &adminhandler.CheckInHandler{},
		},
	}

	registerCheckInRoutes(router.Group("/api/v1/user"), handlers)
	registerAdminCheckInRoutes(router.Group("/api/v1/admin"), handlers)

	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes[http.MethodGet+" /api/v1/user/check-in/status"])
	require.True(t, routes[http.MethodPost+" /api/v1/user/check-in/claim"])
	require.True(t, routes[http.MethodGet+" /api/v1/admin/check-in/config"])
	require.True(t, routes[http.MethodPut+" /api/v1/admin/check-in/config"])
}
