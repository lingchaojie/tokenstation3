package middleware

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AbortWithRequestBodyReadFailure writes the shared response for a failed body
// read in middleware that inspects the body before the handler. It applies the
// same classification and payload-free diagnostics as the gateway handlers.
func AbortWithRequestBodyReadFailure(c *gin.Context, component string, err error) {
	failure := httputil.DescribeRequestBodyReadFailure(c.Request, err)
	if failure.Kind != httputil.RequestBodyReadErrorMaxBytes {
		logRequestBodyReadFailure(c, component, failure)
	}
	gatewayRequestBodyErrorWriter(c)(c, failure.Status, failure.Message)
	c.Abort()
}

func logRequestBodyReadFailure(c *gin.Context, component string, failure httputil.RequestBodyReadFailure) {
	fields := append([]zap.Field{zap.String("component", component)}, failure.LogFields()...)
	logger.FromContext(c.Request.Context()).Warn("read request body failed", fields...)
}

// Body-read failures follow the protocol associated with the ingress route,
// independently of the provider account selected later.
func gatewayRequestBodyErrorWriter(c *gin.Context) GatewayErrorWriter {
	path := c.Request.URL.Path
	switch {
	case strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/antigravity/v1beta"):
		return GoogleErrorWriter
	case strings.Contains(path, "/messages"):
		return AnthropicErrorWriter
	default:
		return func(c *gin.Context, status int, message string) {
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
		}
	}
}
