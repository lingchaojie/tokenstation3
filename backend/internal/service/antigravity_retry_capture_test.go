//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type antigravityCloseErrorBody struct {
	io.Reader
	err error
}

func (b *antigravityCloseErrorBody) Close() error { return b.err }

func antigravityShortRetryBody(model string) []byte {
	return []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.ErrorInfo","metadata":{"model":"` + model + `"},"reason":"RATE_LIMIT_EXCEEDED"},` +
		`{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.1s"}]}}`)
}

func newAntigravityRetryCaptureParams(t *testing.T, c *gin.Context, upstream HTTPUpstream) antigravityRetryLoopParams {
	t.Helper()
	cfg := &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}
	return antigravityRetryLoopParams{
		ctx: context.Background(), c: c, prefix: "[capture-test]",
		account:     &Account{ID: 801, Name: "antigravity-retry", Type: AccountTypeOAuth, Platform: PlatformAntigravity, Concurrency: 1},
		accessToken: "secret", action: "generateContent", body: []byte(`{"model":"gemini-test"}`),
		httpUpstream: upstream, accountRepo: &stubAntigravityAccountRepo{},
		settingService: NewSettingService(&antigravitySettingRepoStub{}, cfg),
		handleError: func(context.Context, string, *Account, int, http.Header, []byte, string, int64, string, bool) *handleModelRateLimitResult {
			return nil
		},
	}
}

func TestHandleSmartRetryCaptureKeepsOnlyFinalHTTPAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	initialBody := antigravityShortRetryBody("gemini-test")
	finalBody := []byte(`{"error":{"status":"UNAVAILABLE","message":"` + strings.Repeat("z", 64<<10) + `"}}`)
	upstream := &mockSmartRetryUpstream{responses: []*http.Response{{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"final-503"}},
		Body:       io.NopCloser(bytes.NewReader(finalBody)),
	}}, errors: []error{nil}}
	params := newAntigravityRetryCaptureParams(t, c, upstream)
	initialResp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"X-Request-Id": {"initial-429"}}, Body: io.NopCloser(bytes.NewReader(initialBody))}
	captureTransport := &recordingCaptureTransport{}
	svc := &AntigravityGatewayService{capturePool: newConversationCapturePoolForTransport(captureTransport, func() bool { return true })}

	result := svc.handleSmartRetry(params, initialResp, initialBody, "https://ag.test", 0, []string{"https://ag.test"})
	require.NotNil(t, result)
	require.NotNil(t, result.switchError, "multi-account retry exhaustion keeps its existing switch behavior")
	require.True(t, result.switchError.Failure.CaptureResponseIncomplete)
	require.True(t, CommitTerminalErrorCaptureAttemptWithCompleteness(c, PlatformAntigravity, http.StatusServiceUnavailable, !result.switchError.Failure.CaptureResponseIncomplete))
	require.Len(t, captureTransport.Attempts(), 1)
	attempt := captureTransport.Attempts()[0]
	require.Equal(t, upstream.requestBodies[0], attempt.RequestBytes())
	require.Equal(t, finalBody[:8<<10], attempt.ResponseBytes(), "capture must contain exactly the bytes naturally consumed by the bounded classifier")
	require.Contains(t, string(attempt.ResponseHeaderBytes()), "final-503")
	require.NotContains(t, string(attempt.ResponseHeaderBytes()), "initial-429")
	require.Equal(t, []captureTerminalState{captureCommitted}, attempt.TerminalStates())
	require.Len(t, attempt.Finals(), 1)
	require.False(t, attempt.Finals()[0].ResponseComplete)
}

func TestReadAntigravityRetryResponseKeepsCompleteHTTPExchangeWhenCloseFails(t *testing.T) {
	body := []byte(`{"error":{"status":"UNAVAILABLE","message":"provider unavailable"}}`)
	closeErr := errors.New("forced close failure")
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"X-Request-Id": {"provider-503"}},
		Body:       &antigravityCloseErrorBody{Reader: bytes.NewReader(body), err: closeErr},
	}

	detached, gotBody, err := readAntigravityRetryResponse(resp, 8<<10)

	require.NoError(t, err, "a close-only cleanup failure must not erase an already observed HTTP response")
	require.NotNil(t, detached)
	require.Equal(t, http.StatusServiceUnavailable, detached.StatusCode)
	require.Equal(t, "provider-503", detached.Header.Get("X-Request-Id"))
	require.Equal(t, body, gotBody)
}

func TestHandleSmartRetryFinalTransportDoesNotPairInitialHTTPResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	initialBody := antigravityShortRetryBody("gemini-test")
	transportErr := errors.New("final retry transport failed")
	upstream := &mockSmartRetryUpstream{responses: []*http.Response{nil}, errors: []error{transportErr}}
	params := newAntigravityRetryCaptureParams(t, c, upstream)
	initialResp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"X-Request-Id": {"initial-429"}}, Body: io.NopCloser(bytes.NewReader(initialBody))}

	result := (&AntigravityGatewayService{}).handleSmartRetry(params, initialResp, initialBody, "https://ag.test", 0, []string{"https://ag.test"})
	require.NotNil(t, result)
	require.NotNil(t, result.switchError)
	_, ok := takeCaptureResult(c)
	require.False(t, ok, "a request-only transport retry must not be combined with the initial HTTP response")
}

func TestCreditsOveragesCaptureKeepsOnlyFinalHTTPAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	finalBody := []byte(`{"error":{"status":"UNAVAILABLE","message":"` + strings.Repeat("c", 96<<10) + `"}}`)
	upstream := &mockSmartRetryUpstream{responses: []*http.Response{{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"credits-final-503"}},
		Body:       io.NopCloser(bytes.NewReader(finalBody)),
	}}, errors: []error{nil}}
	params := newAntigravityRetryCaptureParams(t, c, upstream)

	result := (&AntigravityGatewayService{}).attemptCreditsOveragesRetry(
		params, "https://ag.test", "gemini-test", 0, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"QUOTA_EXHAUSTED"}}`),
	)
	require.True(t, result.handled)
	require.Nil(t, result.resp)
	require.NotNil(t, result.failure)
	require.True(t, result.failure.HasUpstreamHTTPResponse)
	require.Equal(t, http.StatusServiceUnavailable, result.failure.StatusCode)
	require.Len(t, upstream.requestBodies, 1)
	require.Contains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, finalBody, bridge.Response)
	require.Equal(t, http.StatusServiceUnavailable, bridge.HTTPStatus)
	require.Contains(t, string(bridge.ResponseHeaders), "credits-final-503")
	require.Equal(t, upstream.requestBodies[0], bridge.UpstreamRequest)
}

func TestCreditsOveragesTransportDoesNotPairInitialHTTPResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	transportErr := errors.New("credits retry transport failed")
	upstream := &mockSmartRetryUpstream{responses: []*http.Response{nil}, errors: []error{transportErr}}
	params := newAntigravityRetryCaptureParams(t, c, upstream)

	result := (&AntigravityGatewayService{}).attemptCreditsOveragesRetry(
		params, "https://ag.test", "gemini-test", 0, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"QUOTA_EXHAUSTED"}}`),
	)
	require.True(t, result.handled)
	require.Nil(t, result.resp)
	require.NotNil(t, result.failure)
	require.False(t, result.failure.HasUpstreamHTTPResponse)
	require.Contains(t, result.failure.ClientMessage, transportErr.Error())
	_, ok := takeCaptureResult(c)
	require.False(t, ok, "a request-only credits retry must not be paired with the initial quota response")
}
