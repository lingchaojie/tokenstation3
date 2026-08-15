//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGrokErrorCaptureTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	return c, recorder
}

func grokErrorCaptureTestAccount() *Account {
	return &Account{
		ID: 9101, Name: "grok-error-capture", Platform: PlatformGrok,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-token", "base_url": "https://api.x.ai/v1"},
	}
}

func grokErrorCaptureTestConfig() *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
}

func TestForwardGrokResponsesFinalHTTPErrorCarriesFinalAttemptCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","input":"hi","stream":false}`)
	c, _ := newGrokErrorCaptureTestContext(t, body)
	errorBody := `{"error":{"message":"invalid request"}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "Xai-Request-Id": {"final-error"}},
		Body:       io.NopCloser(strings.NewReader(errorBody)),
	}}

	result, err := (&OpenAIGatewayService{cfg: grokErrorCaptureTestConfig(), httpUpstream: upstream}).Forward(
		context.Background(), c, grokErrorCaptureTestAccount(), body,
	)
	require.Error(t, err)
	require.NotNil(t, result, "final non-failover HTTP error must reach the handler capture sink")
	require.False(t, result.SucceededForScheduling(), "a final Grok HTTP error must not clear scheduler failure state")
	require.Equal(t, http.StatusBadRequest, result.UpstreamHTTPStatus)
	require.Equal(t, http.StatusBadRequest, result.HTTPStatusForCapture())
	require.Equal(t, upstream.lastBody, result.UpstreamRequest)
	require.Equal(t, errorBody, string(result.CaptureResponse))
}

func TestForwardGrokResponsesRetryableFinalHTTPErrorBuildsTerminalCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","input":"hi","stream":false}`)
	c, _ := newGrokErrorCaptureTestContext(t, body)
	errorBody := []byte(`{"error":{"message":"temporary unavailable"}}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/json"}, "Xai-Request-Id": {"final-retryable"}},
		Body:       io.NopCloser(bytes.NewReader(errorBody)),
	}}

	result, err := (&OpenAIGatewayService{cfg: grokErrorCaptureTestConfig(), httpUpstream: upstream}).Forward(
		context.Background(), c, grokErrorCaptureTestAccount(), body,
	)
	require.Error(t, err)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.HasUpstreamHTTPResponse)
	require.Equal(t, string(PlatformGrok), failure.Platform)
	record := BuildTerminalErrorCaptureRecord(c, failure.Platform, failure, 1<<20)
	require.NotNil(t, record)
	require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, upstream.lastBody, record.RawRequest)
}

func TestForwardGrokResponsesFinal2xxParseErrorCarriesConsumedCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","input":"hi","stream":false}`)
	c, _ := newGrokErrorCaptureTestContext(t, body)
	malformed := `{"id":"broken"`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}, "Xai-Request-Id": {"parse-error"}},
		Body:       io.NopCloser(strings.NewReader(malformed)),
	}}

	result, err := (&OpenAIGatewayService{cfg: grokErrorCaptureTestConfig(), httpUpstream: upstream}).forwardGrokResponses(
		context.Background(), c, grokErrorCaptureTestAccount(), body, "grok", false, time.Now(),
	)
	require.Error(t, err)
	require.NotNil(t, result, "committed final parse error must reach the handler capture sink")
	require.False(t, result.SucceededForScheduling(), "a consumed 2xx parse failure must be reported as a scheduler failure")
	require.Equal(t, http.StatusOK, result.UpstreamHTTPStatus)
	require.Equal(t, http.StatusOK, result.HTTPStatusForCapture())
	require.Equal(t, upstream.lastBody, result.UpstreamRequest)
	require.Equal(t, malformed, string(result.CaptureResponse))
}

func TestForwardGrokResponsesSuccessKeepsSchedulingAndCaptureStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","input":"hi","stream":false}`)
	c, _ := newGrokErrorCaptureTestContext(t, body)
	responseBody := `{"id":"resp-ok","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}, "Xai-Request-Id": {"success"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}

	result, err := (&OpenAIGatewayService{cfg: grokErrorCaptureTestConfig(), httpUpstream: upstream}).forwardGrokResponses(
		context.Background(), c, grokErrorCaptureTestAccount(), body, "grok", false, time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.SucceededForScheduling())
	require.Equal(t, http.StatusOK, result.UpstreamHTTPStatus)
	require.Equal(t, http.StatusOK, result.HTTPStatusForCapture())
	require.Equal(t, responseBody, string(result.CaptureResponse))
}

func TestForwardAsAnthropicGrokFinalInitial400CapturesBeyondFunctionalLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"grok","max_tokens":32,"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	c, _ := newGrokErrorCaptureTestContext(t, requestBody)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
	c.Set("api_key", &APIKey{ID: 9102})
	providerBody := []byte(`{"error":{"message":"ordinary bad request","padding":"` + strings.Repeat("g", 600<<10) + `"}}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "Xai-Request-Id": {"grok-initial-400"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
	}}
	records := make(chan *CaptureRecord, 2)
	pool := NewConversationCapturePoolForUnitTest(records)
	svc := &OpenAIGatewayService{cfg: grokErrorCaptureTestConfig(), httpUpstream: upstream, capturePool: pool}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, grokErrorCaptureTestAccount(), requestBody, "", "")
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()
	require.Len(t, records, 1, "the final Grok provider attempt must be archived exactly once")
	record := <-records
	require.Equal(t, providerBody, record.RawResponse)
	require.False(t, record.Truncated)
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, upstream.lastBody, record.RawRequest)
	require.Len(t, upstream.requests, 1, "ordinary 400 without encrypted reasoning must not retry")
}
