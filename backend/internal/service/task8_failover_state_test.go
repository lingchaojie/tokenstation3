package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func task8NonStreamingSSEContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func TestTask8NonStreamingCapacityFailureBeforeOutputRequestsFailover(t *testing.T) {
	c, rec := task8NonStreamingSSEContext(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte(strings.Join([]string{
		"event: response.failed",
		`data: {"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`,
		"",
		"data: [DONE]",
	}, "\n"))

	result, err := svc.handleSSEToJSON(resp, c, account, body, "model", "model")

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestTask8NonStreamingTerminalClientFailureDoesNotFailover(t *testing.T) {
	c, rec := task8NonStreamingSSEContext(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte("event: response.failed\n" +
		`data: {"type":"response.failed","error":{"type":"invalid_request_error","code":"invalid_request","message":"unknown parameter foo"}}` +
		"\n\ndata: [DONE]\n")

	result, err := svc.handleSSEToJSON(resp, c, account, body, "model", "model")

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "unknown parameter foo")
}
