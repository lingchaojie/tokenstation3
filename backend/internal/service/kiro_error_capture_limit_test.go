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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroPrefixErrorBody struct {
	payload []byte
	err     error
	done    bool
	closed  bool
}

func (r *kiroPrefixErrorBody) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.payload), r.err
}

func (r *kiroPrefixErrorBody) Close() error {
	r.closed = true
	return nil
}

func TestDumpKiro429DebugPassesThroughDataAndReadError(t *testing.T) {
	sentinel := errors.New("forced provider read failure")
	original := &kiroPrefixErrorBody{payload: []byte(`{"message":"rate limited"}`), err: sentinel}
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Body: original}

	dumpKiro429ResponseForDebug(resp, 9, "https://kiro.example/invoke", "invoke")

	observed, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, original.payload, observed)
	require.False(t, original.closed, "debug observation must leave the provider body owned by the functional reader")
	require.NoError(t, resp.Body.Close())
	require.True(t, original.closed)
}

func TestDumpKiro429DebugDoesNotReadOrWaitForExactLimitBody(t *testing.T) {
	raw := &providerPrefixThenBlockReader{
		prefix: bytes.Repeat([]byte("x"), 2048),
		closed: make(chan struct{}),
	}
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Body: raw}
	started := time.Now()

	dumpKiro429ResponseForDebug(resp, 9, "https://kiro.example/invoke", "invoke")

	require.Less(t, time.Since(started), 50*time.Millisecond)
	require.Zero(t, raw.offset, "debug observer must not consume provider bytes ahead of the functional classifier")
	require.NoError(t, resp.Body.Close())
}

func TestKiroErrorCaptureLimitDoesNotChangeFunctionalClassificationBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"message":"invalid model requested","reason":"invalid_model","padding":"` + strings.Repeat("x", (3<<20)) + `"}`)

	read := func(captureLimit int) ([]byte, *captureResultBridge) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx := context.Background()
		if captureLimit > 0 {
			enableCaptureForTest(t, c)
			beginCaptureAttempt(c)
			ctx = withCaptureUpstreamRequestContext(ctx, c, captureLimit)
		}
		resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewReader(body))}
		got, _, err := (&GatewayService{}).readKiroUpstreamErrorBody(ctx, resp)
		require.NoError(t, err)
		if captureLimit <= 0 {
			return got, nil
		}
		bridge, ok := takeCaptureResult(c)
		require.True(t, ok)
		return got, bridge
	}

	withoutCapture, _ := read(0)
	require.Len(t, withoutCapture, 2<<20)

	smallCapture, smallBridge := read(64)
	require.Equal(t, withoutCapture, smallCapture, "small capture limits must not shorten the functional KIRO error body")
	require.Equal(t, body[:64], smallBridge.Response)
	require.True(t, smallBridge.Truncated)

	largeCapture, largeBridge := read(8 << 20)
	require.Equal(t, withoutCapture, largeCapture, "large capture limits must not expose additional bytes to KIRO classification")
	require.Equal(t, body, largeBridge.Response, "archive may retain bytes beyond the stable functional 2 MiB ceiling")
	require.False(t, largeBridge.Truncated)
}
