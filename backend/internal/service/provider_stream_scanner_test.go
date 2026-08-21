//go:build unit

package service

import (
	"bufio"
	"bytes"
	"errors"
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

func TestProviderLineReaderDeliversQueuedProviderDataBeforeIdleTimeout(t *testing.T) {
	for iteration := 0; iteration < 64; iteration++ {
		events := make(chan providerLineScanEvent, 1)
		events <- providerLineScanEvent{line: "second"}
		timer := time.NewTimer(0)
		reader := &providerLineReader{events: events, timer: timer, timerCh: timer.C}

		line, ok, err := reader.Next()
		timer.Stop()
		require.NoError(t, err, "queued upstream data must win over an expired consumer-side timer")
		require.True(t, ok)
		require.Equal(t, "second", line)
	}
}

func TestProviderLineReaderTreatsPartialLineBytesAsProviderActivity(t *testing.T) {
	raw := &providerSlowChunksReader{
		chunks: [][]byte{
			[]byte("data: {"),
			[]byte(`"value":1}`),
			[]byte("\n"),
		},
		interval: 15 * time.Millisecond,
	}
	resp := &http.Response{Body: raw}
	reader := newProviderLineReader(resp, nil, bufio.NewScanner)
	reader.timeout = 25 * time.Millisecond
	reader.timer = time.NewTimer(reader.timeout)
	reader.timerCh = reader.timer.C
	defer reader.Close()

	line, ok, err := reader.Next()
	require.NoError(t, err, "provider bytes arriving within the idle interval must keep a partial SSE line alive")
	require.True(t, ok)
	require.Equal(t, `data: {"value":1}`, line)
}

func TestAnthropicBufferedCollectorDrainsCapturePastSmallerFunctionalLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	line := ": " + strings.Repeat("x", 1022) + "\n"
	providerBody := []byte(strings.Repeat(line, 200))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", bytes.NewReader([]byte(`{"model":"claude"}`)))
	setCaptureUpstreamRequest(c, request, 1<<20)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-buffer-limit"}},
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
		Request:    request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  2 * 1024 * 1024,
		UpstreamResponseReadMaxBytes: 1024,
	}}
	svc := &GatewayService{cfg: cfg}

	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "claude", "claude", nil, time.Now(), false)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, capture.Response, len(providerBody))
	require.True(t, bytes.Equal(providerBody, capture.Response), "capture must preserve the finite provider body exactly")
	require.False(t, capture.ResponseTruncated)
}

func TestAnthropicBufferedCollectorDrainsOnlyToCaptureLimitAndProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	line := ": " + strings.Repeat("x", 1022) + "\n"
	providerBody := []byte(strings.Repeat(line, 200))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", bytes.NewReader([]byte(`{"model":"claude"}`)))
	setCaptureUpstreamRequest(c, request, 32<<10)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(providerBody)), Request: request}
	finishCapture := beginCaptureResponse(c, resp, true, 32<<10)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  2 * 1024 * 1024,
		UpstreamResponseReadMaxBytes: 1024,
	}}

	result, err := (&GatewayService{cfg: cfg}).handleCCBufferedFromAnthropic(resp, c, "claude", "claude", nil, time.Now(), false)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, capture.Response, 32<<10)
	require.True(t, capture.ResponseTruncated)
}

func TestProviderLineReaderCaptureDrainStopsAtIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil)
	setCaptureUpstreamRequest(c, request, 1<<20)
	raw := &providerPrefixThenBlockReader{
		prefix: []byte(strings.Repeat("x", 1025)),
		closed: make(chan struct{}),
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: request}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  2 * 1024 * 1024,
		UpstreamResponseReadMaxBytes: 1024,
		StreamDataIntervalTimeout:    1,
	}}
	reader := newProviderLineReader(resp, cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, cfg)
	})
	reader.timeout = 50 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		_, _, err := reader.Next()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	case <-time.After(500 * time.Millisecond):
		_ = raw.Close()
		err := <-errCh
		t.Fatalf("capture drain remained blocked after the configured idle timeout: %v", err)
	}
	reader.Close()
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, raw.prefix, capture.Response)
	require.True(t, capture.ResponseTruncated, "an idle-aborted provider body is not an exact final response")
}

func TestBufferedProviderSSEScannerCountsExactCRLFWireBytes(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  1024,
		UpstreamResponseReadMaxBytes: 10,
	}}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(": x\r\n: x\r\n: x\r\n"))}
	reader := newProviderLineReader(resp, cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, cfg)
	})
	defer reader.Close()

	var gotErr error
	for {
		_, ok, err := reader.Next()
		if err != nil {
			gotErr = err
			break
		}
		if !ok {
			break
		}
	}
	require.ErrorIs(t, gotErr, ErrUpstreamResponseBodyTooLarge,
		"CRLF delimiters must count toward the functional wire-byte ceiling")
}

func TestBufferedProviderSSEScannerAllowsExactFunctionalLimitWithoutDelimiter(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  1024,
		UpstreamResponseReadMaxBytes: 10,
	}}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("0123456789"))}
	reader := newProviderLineReader(resp, cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, cfg)
	})
	defer reader.Close()

	line, ok, err := reader.Next()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0123456789", line)
	_, ok, err = reader.Next()
	require.NoError(t, err)
	require.False(t, ok)
}

func TestBufferedProviderSSEScannerLargeLineDrainsOnlyCaptureLimitAndProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil)
	setCaptureUpstreamRequest(c, request, 32<<10)
	source := &countingCaptureResponseReader{reader: &repeatedCaptureByteReader{remaining: 4 << 20, value: 'x'}}
	resp := &http.Response{StatusCode: http.StatusOK, Body: source, Request: request}
	finishCapture := beginCaptureResponse(c, resp, true, 32<<10)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  2 * 1024 * 1024,
		UpstreamResponseReadMaxBytes: 1024,
	}}
	reader := newProviderLineReader(resp, cfg, func(r io.Reader) *bufio.Scanner {
		return newBufferedProviderSSEScanner(r, cfg)
	})

	_, _, gotErr := reader.Next()
	require.True(t, errors.Is(gotErr, ErrUpstreamResponseBodyTooLarge), "got %v", gotErr)
	reader.Close()
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.LessOrEqual(t, source.read, (32<<10)+1,
		"a single large token must not be read up to MaxLineSize merely to drain capture")
	require.Len(t, capture.Response, 32<<10)
	require.True(t, capture.ResponseTruncated)
}

func TestProviderLineReaderScannerLimitDrainsUnreadFiniteTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/chat/completions", nil)
	setCaptureUpstreamRequest(c, request, 3<<20)
	providerBody := bytes.Repeat([]byte{'x'}, (2<<20)+6)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(providerBody)),
		Request:    request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 3<<20)
	reader := newProviderLineReader(resp, nil, func(r io.Reader) *bufio.Scanner {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		return scanner
	})

	_, _, err := reader.Next()
	require.ErrorIs(t, err, bufio.ErrTooLong)
	reader.Close()
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, providerBody, capture.Response)
	require.False(t, capture.ResponseTruncated)
}
