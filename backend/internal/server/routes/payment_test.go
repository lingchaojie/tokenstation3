//go:build unit

package routes

import (
	"testing"

	dbhandler "github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterPaymentRoutesDoesNotExposeUserSelfServiceRefund(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	v1 := router.Group("/api/v1")
	noopAuth := func(c *gin.Context) {}

	RegisterPaymentRoutes(
		v1,
		dbhandler.NewPaymentHandler(nil, nil),
		&dbhandler.PaymentWebhookHandler{},
		adminhandler.NewPaymentHandler(nil, nil),
		middleware.JWTAuthMiddleware(noopAuth),
		middleware.AdminAuthMiddleware(noopAuth),
		nil,
	)

	var adminRefundRouteFound bool
	for _, route := range router.Routes() {
		require.NotEqual(t, "/api/v1/payment/orders/:id/refund-request", route.Path)
		require.NotEqual(t, "/api/v1/payment/orders/refund-eligible-providers", route.Path)
		if route.Path == "/api/v1/admin/payment/orders/:id/refund" {
			adminRefundRouteFound = true
		}
	}
	require.True(t, adminRefundRouteFound)
}
