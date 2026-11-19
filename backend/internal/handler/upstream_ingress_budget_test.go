//go:build unit

package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIngressModelPrereadRejectsOversizedBodyBeforeAuth(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/responses"} {
		t.Run(path, func(t *testing.T) {
			engine := gin.New()
			called := false
			engine.POST(path, middleware.RequestBodyLimit(24), InboundEndpointMiddleware(), func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"claude-fable","input":"x"}`))
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
			require.False(t, called)
		})
	}
}

func TestIngressModelPrereadReusesBudgetedBody(t *testing.T) {
	const body = `{"model":"claude-fable","input":"xyz"}`
	engine := gin.New()
	engine.POST("/v1/responses", middleware.RequestBodyLimit(1024, httputil.NewBodyMemoryBudget(int64(len(body)+1))), InboundEndpointMiddleware(), func(c *gin.Context) {
		require.Equal(t, "claude-fable", c.Request.Context().Value(ctxkey.IngressModel))
		_, ok := c.Request.Body.(*httputil.PrereadBody)
		require.True(t, ok)
		first, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, body, string(first))
		second, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(second))
		c.Status(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestIngressModelPrereadReturnsCapacityBeforeAuth(t *testing.T) {
	engine := gin.New()
	called := false
	engine.POST("/responses", middleware.RequestBodyLimit(1024, httputil.NewBodyMemoryBudget(4)), InboundEndpointMiddleware(), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"claude-fable"}`)))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	require.False(t, called)
}
