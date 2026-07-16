package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterRewardCreditRoutesRegistersAuthenticatedUserContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{User: &handler.UserHandler{}}

	registerRewardCreditRoutes(router.Group("/api/v1/user"), handlers)

	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes[http.MethodGet+" /api/v1/user/reward-credits"])
}
