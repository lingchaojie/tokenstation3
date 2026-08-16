//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type retryCaptureCloseSignalBody struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *retryCaptureCloseSignalBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestGatewayBuiltInRetryAbortsTypedAttemptBeforeBackoffAndCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	closed := make(chan struct{})
	upstream := &anthropicHTTPUpstreamSequenceRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: &retryCaptureCloseSignalBody{
				Reader: bytes.NewReader([]byte(`{"error":{"message":"retry"}}`)),
				closed: closed,
			},
		},
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20},
	}}
	transport := &recordingCaptureTransport{}
	svc := &GatewayService{
		cfg:                  cfg,
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
		tlsFPProfileService:  &TLSFingerprintProfileService{},
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		capturePool:          newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	account := newAnthropicOAuthAccountForPartialUsageTest()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type forwardResult struct {
		result *ForwardResult
		err    error
	}
	done := make(chan forwardResult, 1)
	go func() {
		result, forwardErr := svc.Forward(ctx, c, account, parsed)
		done <- forwardResult{result: result, err: forwardErr}
	}()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("first retryable response was not consumed and closed")
	}
	require.Eventually(t, func() bool {
		attempts := transport.Attempts()
		return len(attempts) == 1 && len(attempts[0].TerminalStates()) == 1
	}, 150*time.Millisecond, 5*time.Millisecond, "typed attempt must terminate before the 300ms retry backoff completes")
	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[0].TerminalStates())

	cancel()
	select {
	case got := <-done:
		require.Nil(t, got.result)
		require.ErrorIs(t, got.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancel did not release retry backoff")
	}
	AbortCaptureAttempt(c) // Simulate the request-handler defer.
	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[0].TerminalStates(), "cancellation and handler cleanup must remain exactly once")
	require.Len(t, upstream.requests, 1, "cancellation must prevent the next retry request")
}
