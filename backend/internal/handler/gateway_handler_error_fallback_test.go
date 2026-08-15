package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayResponsesFailoverExhaustedAfterCompactHeartbeatWritesOneTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
	service.MarkOpenAICompactClientStream(c)
	stop := service.StartOpenAICompactSSEKeepalive(c, 5*time.Millisecond)
	defer stop()
	time.Sleep(20 * time.Millisecond)

	(&GatewayHandler{}).handleResponsesFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}, false)

	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
}

func TestGatewayResponsesLocalErrorAfterCompactHeartbeatWritesOneTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
	service.MarkOpenAICompactClientStream(c)
	stop := service.StartOpenAICompactSSEKeepalive(c, 5*time.Millisecond)
	defer stop()
	time.Sleep(20 * time.Millisecond)

	(&GatewayHandler{}).responsesErrorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts")

	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
	require.NotContains(t, rec.Body.String(), `{"error":{"code":"api_error"`)
}

func TestShouldStartResponsesCompactionKeepaliveIsKiroOnly(t *testing.T) {
	// The selected account, not the entry group's platform, owns this decision:
	// mixed scheduling may legally route an Anthropic group to a KIRO account.
	require.True(t, shouldStartResponsesCompactionKeepalive(&service.Account{Platform: service.PlatformKiro}, true, true))
	require.False(t, shouldStartResponsesCompactionKeepalive(&service.Account{Platform: service.PlatformAnthropic}, true, true))
	require.False(t, shouldStartResponsesCompactionKeepalive(nil, true, true))
	require.False(t, shouldStartResponsesCompactionKeepalive(&service.Account{Platform: service.PlatformKiro}, false, true))
	require.False(t, shouldStartResponsesCompactionKeepalive(&service.Account{Platform: service.PlatformKiro}, true, false))
}

func TestResponsesAccountSelectionContextLocksKiroAfterCompactionSelection(t *testing.T) {
	base := context.WithValue(context.Background(), ctxkey.ForcePlatform, service.PlatformAnthropic)

	require.Same(t, base, responsesAccountSelectionContext(base, false))
	locked := responsesAccountSelectionContext(base, true)
	require.Equal(t, service.PlatformAnthropic, locked.Value(ctxkey.ForcePlatform), "entry platform must stay mixed Anthropic")
	require.Equal(t, service.PlatformKiro, locked.Value(ctxkey.RequiredAccountPlatform))
}

func TestGatewayEnsureForwardErrorResponse_WritesFallbackWhenNotWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, false)

	require.True(t, wrote)
	require.Equal(t, http.StatusBadGateway, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "error", parsed["type"])
	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errorObj["type"])
	assert.Equal(t, "Upstream request failed", errorObj["message"])
}

// Writer 已写后 ensureForwardErrorResponse 必须把错误以 SSE 形式追加，
// 而不是 silent EOF。非 /responses 路径走 legacy data:{"type":"error"} 分支。
func TestGatewayEnsureForwardErrorResponse_AppendsSSEAfterWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.String(http.StatusTeapot, "already written")

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, false)

	require.True(t, wrote)
	require.Equal(t, http.StatusTeapot, w.Code)
	assert.Contains(t, w.Body.String(), "already written")
	assert.Contains(t, w.Body.String(), `data: {"type":"error"`)
}

func TestGatewayEnsureForwardErrorResponse_SkipsCommittedSSEError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
	c.Header("Content-Type", "text/event-stream")
	_, _ = c.Writer.WriteString("event: error\ndata: {\"type\":\"error\"}\n\n")
	service.MarkResponseCommitted(c)

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, true)

	require.False(t, wrote)
	require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
}

// case B 回归：Anthropic-backed /responses，Writer 已被写过时
// ensureForwardErrorResponse 仍要发 response.failed。
func TestGatewayEnsureForwardErrorResponse_ResponsesRouteAfterWrittenEmitsResponseFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
	_, _ = c.Writer.WriteString(":\n\n")

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, false)

	require.True(t, wrote)
	body := w.Body.String()
	assert.Contains(t, body, ":\n\n")
	assert.Contains(t, body, "event: response.failed\n")
	assert.Contains(t, body, `"type":"response.failed"`)
}

func TestGatewayForwardErrorAlreadyCommunicated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("json error already written", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		before := c.Writer.Size()
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Your Claude Code version (2.1.39) is below the minimum required version (2.1.81). Please update: npm update -g @anthropic-ai/claude-code",
			},
		})

		reported := gatewayForwardErrorAlreadyCommunicated(c, before, errors.New("upstream error: 400 message=version too low"))

		require.True(t, reported)
		body := w.Body.String()
		assert.NotContains(t, body, `data: {"type":"error"`)
	})

	t.Run("sse ping still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		c.Header("Content-Type", "text/event-stream")
		before := c.Writer.Size()
		_, _ = c.Writer.WriteString(":\n\n")

		reported := gatewayForwardErrorAlreadyCommunicated(c, before, errors.New("stream read error: unexpected EOF"))

		require.False(t, reported)
	})

	t.Run("no write still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)

		reported := gatewayForwardErrorAlreadyCommunicated(c, c.Writer.Size(), errors.New("upstream request failed"))

		require.False(t, reported)
	})

	// apikey 场景核心回归：复刻 GatewayService.handleErrorResponse 的 case 400 ——
	// 原样透传上游 JSON body 后返回 err。此时错误已经完整告知客户端，
	// handler 不得再追加 data:{"type":"error"} 帧，否则响应被污染成「JSON + 一行 data:」。
	t.Run("upstream 400 json passthrough via c.Data", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		before := c.Writer.Size()
		upstreamBody := []byte(`{"type":"error","error":{"type":"upstream_error","message":"Your Claude Code version (2.1.39) is below the minimum required version (2.1.81). Please update: npm update -g @anthropic-ai/claude-code"}}`)
		c.Data(http.StatusBadRequest, "application/json", upstreamBody)

		reported := gatewayForwardErrorAlreadyCommunicated(c, before, errors.New("upstream error: 400 message=version too low"))

		require.True(t, reported)
		body := w.Body.String()
		assert.NotContains(t, body, `data: {"type":"error"`)
		// 客户端只应收到上游那一份错误，没有被追加第二份。
		assert.Equal(t, 1, strings.Count(body, `"type":"error"`))
	})

	// 流式已开始（已 flush 真实 SSE 事件，不只是 ping）+ 上游中途 400：
	// HTTP 200 已固化，仍需 handler 补协议级终止帧，故不算「已完整告知」。
	t.Run("streaming 400 mid-stream still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		c.Header("Content-Type", "text/event-stream")
		before := c.Writer.Size()
		_, _ = c.Writer.WriteString("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")

		reported := gatewayForwardErrorAlreadyCommunicated(c, before, errors.New("upstream error: 400 message=version too low"))

		require.False(t, reported)
	})

	// 防御边界：err 为 nil 时永远不算「已告知」，避免在成功路径误吞兜底逻辑。
	t.Run("nil error never reports communicated", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		c.JSON(http.StatusOK, gin.H{"ok": true})

		reported := gatewayForwardErrorAlreadyCommunicated(c, 0, nil)

		require.False(t, reported)
	})
}
