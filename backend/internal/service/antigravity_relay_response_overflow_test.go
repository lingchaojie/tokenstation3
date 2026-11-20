package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAntigravityRelayResponseOverflowLeavesFailoverAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	const responseLimit = 256
	goodBody := `{"id":"msg_retry","type":"message","role":"assistant","content":[{"type":"text","text":"second relay"}],"usage":{"input_tokens":8,"output_tokens":2},"stop_reason":"end_turn"}`
	accepted := goodBody + strings.Repeat(" ", responseLimit-len(goodBody))
	response := func(body string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{
		response(accepted + " "),
		response(accepted),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{UpstreamResponseReadMaxBytes: responseLimit}}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		httpUpstream:   upstream,
	}
	account := func(id int64) *Account {
		return &Account{
			ID: id, Platform: PlatformAntigravity, Type: AccountTypeUpstream, Status: StatusActive, Concurrency: 1,
			Credentials: map[string]any{"base_url": "https://relay.example", "api_key": "test-token"},
		}
	}

	result, err := svc.Forward(context.Background(), c, account(81), body, false)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Nil(t, result)
	require.False(t, failure.RetryableOnSameAccount)
	require.True(t, failure.HasUpstreamHTTPResponse)
	require.False(t, c.Writer.Written(), "the first failed relay must leave the response replayable")
	require.Empty(t, rec.Body.String())

	result, err = svc.Forward(context.Background(), c, account(82), body, false)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, accepted, rec.Body.String())
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.True(t, result.CaptureResponseComplete)
}
