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

func TestOpenAIGatewayServiceNativeCommittedPartialCarriesCaptureAndFinalRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.4","stream":true,"input":"hello"}`)
	responseBody := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-native-partial"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 64 * 1024
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:           cfg,
		httpUpstream:  upstream,
		toolCorrector: NewCodexToolCorrector(),
		capturePool:   newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	account := &Account{
		ID: 10, Name: "native", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "chatgpt_account_id": "acct"}, Status: StatusActive, Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
	require.Equal(t, []byte(responseBody), attempts[0].ResponseBytes())
	require.Equal(t, captureHeaderBytes(upstream.lastReq.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].RequestHeaderBytes())
	require.Equal(t, redactHTTPHeader(upstream.resp.Header), attempts[0].ResponseHeaderBytes())
	require.Empty(t, attempts[0].TerminalStates(), "the handler-side partial-result sink owns commit")
	AbortCaptureAttempt(c)
}
