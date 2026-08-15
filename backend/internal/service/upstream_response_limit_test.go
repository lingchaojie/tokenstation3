package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countingCaptureResponseReader struct {
	reader io.Reader
	read   int
}

// These provider readers are shared by unit and integration-tagged service
// tests, so keep them in this tag-independent test file.
type providerPrefixThenBlockReader struct {
	prefix    []byte
	offset    int
	closed    chan struct{}
	closeOnce sync.Once
}

type providerSlowChunksReader struct {
	chunks   [][]byte
	interval time.Duration
	index    int
}

func (r *providerSlowChunksReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	time.Sleep(r.interval)
	n := copy(p, r.chunks[r.index])
	if n != len(r.chunks[r.index]) {
		return n, io.ErrShortBuffer
	}
	r.index++
	return n, nil
}

func (*providerSlowChunksReader) Close() error { return nil }

func (r *providerPrefixThenBlockReader) Read(p []byte) (int, error) {
	if r.offset < len(r.prefix) {
		n := copy(p, r.prefix[r.offset:])
		r.offset += n
		return n, nil
	}
	<-r.closed
	return 0, io.EOF
}

func (r *providerPrefixThenBlockReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type slowCaptureTailReader struct {
	chunks    [][]byte
	delay     time.Duration
	index     int
	closed    chan struct{}
	closeOnce sync.Once
}

func (r *slowCaptureTailReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	if r.index > 0 && r.delay > 0 {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.closed:
			return 0, io.EOF
		}
	}
	n := copy(p, r.chunks[r.index])
	r.index++
	return n, nil
}

func (r *slowCaptureTailReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func (r *countingCaptureResponseReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func (*countingCaptureResponseReader) Close() error { return nil }

func TestCaptureDrainTimeoutTracksProviderByteActivity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil)
	setCaptureUpstreamRequest(c, request, 1<<20)
	raw := &slowCaptureTailReader{
		chunks: [][]byte{[]byte("tool"), []byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")},
		delay:  10 * time.Millisecond,
		closed: make(chan struct{}),
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: request}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	prefix := make([]byte, 4)
	_, err := io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	require.Equal(t, []byte("tool"), prefix)

	drainCaptureResponseRemainderBounded(context.Background(), resp.Body, 15*time.Millisecond)
	finish()

	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("toolabcde"), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

func TestReadUpstreamResponseBodyDrainsCapturePastSmallerFunctionalLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	setCapturePlatform(c, PlatformAnthropic)
	req := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", bytes.NewReader([]byte(`{"model":"claude"}`)))
	setCaptureUpstreamRequest(c, req, 1<<20)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-functional-smaller"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("toolong"))),
		Request:    req,
	}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	body, err := ReadUpstreamResponseBody(resp.Body, cfg, c, nil)
	require.Nil(t, body)
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	finish()
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("toolong"), bridge.Response)
	require.False(t, bridge.ResponseTruncated)
}

func TestReadUpstreamResponseBodyDrainsOnlyToCaptureLimitAndProbe(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	req := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", bytes.NewReader([]byte(`{"model":"claude"}`)))
	setCaptureUpstreamRequest(c, req, 10)
	source := &countingCaptureResponseReader{reader: bytes.NewReader([]byte("0123456789abcdef"))}
	resp := &http.Response{StatusCode: http.StatusOK, Body: source, Request: req}
	finish := beginCaptureResponse(c, resp, true, 10)
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	body, err := ReadUpstreamResponseBody(resp.Body, cfg, c, nil)
	require.Nil(t, body)
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	finish()
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, 11, source.read, "capture may consume only its ten-byte ceiling plus one truncation probe")
	require.Equal(t, []byte("0123456789"), bridge.Response)
	require.True(t, bridge.ResponseTruncated)
}

func TestReadUpstreamResponseBodyCaptureDrainStopsAtContextDeadline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	beginCaptureAttempt(c)
	setCaptureUpstreamRequest(c, c.Request, 1<<20)
	raw := &providerPrefixThenBlockReader{prefix: []byte("tool"), closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: c.Request}
	finish := beginCaptureResponse(c, resp, true, 1<<20)
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	type readResult struct {
		body []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		body, err := ReadUpstreamResponseBody(resp.Body, cfg, c, nil)
		resultCh <- readResult{body: body, err: err}
	}()

	select {
	case result := <-resultCh:
		require.Nil(t, result.body)
		require.ErrorIs(t, result.err, ErrUpstreamResponseBodyTooLarge)
	case <-time.After(500 * time.Millisecond):
		_ = raw.Close()
		<-resultCh
		t.Fatal("capture drain remained blocked after the request context deadline")
	}
	finish()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("tool"), capture.Response)
	require.True(t, capture.ResponseTruncated, "a context-aborted capture drain is incomplete")
}

func TestReadUpstreamResponseBodyStalledBodyReturnsAtContextDeadline(t *testing.T) {
	for _, prefix := range []string{"to", "too"} {
		for _, captureEnabled := range []bool{false, true} {
			name := prefix
			if captureEnabled {
				name += "/capture"
			} else {
				name += "/no_capture"
			}
			t.Run(name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
				beginCaptureAttempt(c)
				setCaptureUpstreamRequest(c, c.Request, 1<<20)
				raw := &providerPrefixThenBlockReader{prefix: []byte(prefix), closed: make(chan struct{})}
				resp := &http.Response{StatusCode: http.StatusOK, Body: raw, Request: c.Request}
				finish := beginCaptureResponse(c, resp, captureEnabled, 1<<20)
				cfg := &config.Config{}
				cfg.Gateway.UpstreamResponseReadMaxBytes = 3

				type readResult struct {
					body []byte
					err  error
				}
				resultCh := make(chan readResult, 1)
				go func() {
					body, err := ReadUpstreamResponseBody(resp.Body, cfg, c, nil)
					resultCh <- readResult{body: body, err: err}
				}()

				select {
				case result := <-resultCh:
					require.Nil(t, result.body)
					require.ErrorIs(t, result.err, context.DeadlineExceeded)
				case <-time.After(500 * time.Millisecond):
					_ = raw.Close()
					<-resultCh
					t.Fatal("stalled non-streaming body ignored the request context deadline")
				}
				finish()
				capture, ok := takeCaptureResult(c)
				if captureEnabled {
					require.True(t, ok)
					require.Equal(t, []byte(prefix), capture.Response)
					require.True(t, capture.ResponseTruncated, "a stalled body closed at the context deadline is incomplete")
				} else {
					require.True(t, ok)
					require.Empty(t, capture.Response)
				}
			})
		}
	}
}

func TestReadAllWithProviderIdleStopsStallWhileContextRemainsActive(t *testing.T) {
	raw := &providerPrefixThenBlockReader{prefix: []byte("partial"), closed: make(chan struct{})}
	started := time.Now()
	body, err := readAllWithProviderIdle(
		context.Background(), raw, 50*time.Millisecond,
		func(reader io.Reader) ([]byte, error) { return io.ReadAll(reader) },
	)
	require.Equal(t, []byte("partial"), body)
	require.ErrorIs(t, err, errProviderStreamIdleTimeout)
	require.Less(t, time.Since(started), 500*time.Millisecond)
	select {
	case <-raw.closed:
	default:
		t.Fatal("provider idle timeout did not close the underlying response body")
	}
}

func TestReadAllWithProviderIdleZeroDisablesInternalIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	raw := &providerPrefixThenBlockReader{prefix: []byte("partial"), closed: make(chan struct{})}
	type readResult struct {
		body []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		body, err := readAllWithProviderIdle(
			ctx, raw, 0,
			func(reader io.Reader) ([]byte, error) { return io.ReadAll(reader) },
		)
		resultCh <- readResult{body: body, err: err}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("zero idle timeout returned early: body=%q err=%v", result.body, result.err)
	case <-time.After(75 * time.Millisecond):
	}
	cancel()
	result := <-resultCh
	require.Equal(t, []byte("partial"), result.body)
	require.ErrorIs(t, result.err, context.Canceled)
}

func TestReadUpstreamBodyWithCeilingStopsShortAndExactLimitStalls(t *testing.T) {
	for _, prefix := range []string{"to", "too"} {
		t.Run(prefix, func(t *testing.T) {
			raw := &providerPrefixThenBlockReader{prefix: []byte(prefix), closed: make(chan struct{})}
			resp := &http.Response{Body: raw}
			started := time.Now()

			body, truncated, err := readUpstreamBodyWithCeiling(
				context.Background(), resp, 3, 50*time.Millisecond,
			)

			require.Equal(t, []byte(prefix), body)
			require.False(t, truncated)
			require.ErrorIs(t, err, errProviderStreamIdleTimeout)
			require.Less(t, time.Since(started), 500*time.Millisecond)
			select {
			case <-raw.closed:
			default:
				t.Fatal("bounded ceiling read did not close the stalled provider body")
			}
		})
	}
}

func TestCaptureAwareErrorBodyDrainStopsAtRequestDeadline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil).WithContext(ctx)
	beginCaptureAttempt(c)
	setCaptureUpstreamRequest(c, request, 1<<20)
	raw := &providerPrefixThenBlockReader{prefix: []byte("tool"), closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: raw, Request: request}
	finish := beginCaptureResponse(c, resp, true, 1<<20)

	type readResult struct {
		body []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		body, err := readCaptureAwareUpstreamErrorBody(resp, 3)
		resultCh <- readResult{body: body, err: err}
	}()

	select {
	case result := <-resultCh:
		require.Equal(t, []byte("too"), result.body)
		require.NoError(t, result.err)
	case <-time.After(500 * time.Millisecond):
		_ = raw.Close()
		<-resultCh
		t.Fatal("capture-aware error-body read remained blocked after the request deadline")
	}
	finish()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("tool"), capture.Response)
	require.True(t, capture.ResponseTruncated, "a context-aborted error-body drain is incomplete")
}

func TestCaptureAwareErrorBodyExactFunctionalLimitDoesNotWaitForProbe(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil).WithContext(ctx)
	beginCaptureAttempt(c)
	setCaptureUpstreamRequest(c, request, 1<<20)
	raw := &providerPrefixThenBlockReader{prefix: []byte("too"), closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: raw, Request: request}
	finish := beginCaptureResponse(c, resp, true, 1<<20)

	type readResult struct {
		body []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		body, err := readCaptureAwareUpstreamErrorBody(resp, 3)
		resultCh <- readResult{body: body, err: err}
	}()

	select {
	case result := <-resultCh:
		require.Equal(t, []byte("too"), result.body)
		require.NoError(t, result.err)
	case <-time.After(500 * time.Millisecond):
		_ = raw.Close()
		<-resultCh
		t.Fatal("capture-aware error-body read waited indefinitely for an overflow probe")
	}
	finish()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("too"), capture.Response)
	require.True(t, capture.ResponseTruncated, "an exact functional prefix without provider EOF is incomplete")
}

func TestCaptureAwareErrorBodyShortPrefixStopsAtRequestDeadline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/messages", nil).WithContext(ctx)
	beginCaptureAttempt(c)
	setCaptureUpstreamRequest(c, request, 1<<20)
	raw := &providerPrefixThenBlockReader{prefix: []byte("to"), closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: raw, Request: request}
	finish := beginCaptureResponse(c, resp, true, 1<<20)

	type readResult struct {
		body []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		body, err := readCaptureAwareUpstreamErrorBody(resp, 3)
		resultCh <- readResult{body: body, err: err}
	}()

	select {
	case result := <-resultCh:
		require.Equal(t, []byte("to"), result.body)
		require.ErrorIs(t, result.err, context.DeadlineExceeded)
	case <-time.After(500 * time.Millisecond):
		_ = raw.Close()
		<-resultCh
		t.Fatal("capture-aware error-body read ignored the request deadline for a short prefix")
	}
	finish()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("to"), capture.Response)
	require.True(t, capture.ResponseTruncated, "a short provider prefix closed at the deadline is incomplete")
}

func TestResolveUpstreamResponseReadLimit(t *testing.T) {
	t.Run("use default when config missing", func(t *testing.T) {
		require.Equal(t, defaultUpstreamResponseReadMaxBytes, resolveUpstreamResponseReadLimit(nil))
	})

	t.Run("use configured value", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseReadMaxBytes = 1234
		require.Equal(t, int64(1234), resolveUpstreamResponseReadLimit(cfg))
	})
}

func TestReadUpstreamResponseBodyLimited(t *testing.T) {
	t.Run("within limit", func(t *testing.T) {
		body, err := readUpstreamResponseBodyLimited(bytes.NewReader([]byte("ok")), 2)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), body)
	})

	t.Run("exceeds limit", func(t *testing.T) {
		body, err := readUpstreamResponseBodyLimited(bytes.NewReader([]byte("toolong")), 3)
		require.Nil(t, body)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
	})
}

func TestReadUpstreamResponseBody(t *testing.T) {
	t.Run("within limit", func(t *testing.T) {
		body, err := ReadUpstreamResponseBody(bytes.NewReader([]byte("ok")), nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), body)
	})

	t.Run("exceeds limit calls onTooLarge", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseReadMaxBytes = 3

		called := false
		onTooLarge := func(_ *gin.Context) { called = true }

		body, err := ReadUpstreamResponseBody(bytes.NewReader([]byte("toolong")), cfg, nil, onTooLarge)
		require.Nil(t, body)
		require.True(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
		require.True(t, called)
	})

	t.Run("nil onTooLarge does not panic", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseReadMaxBytes = 3

		body, err := ReadUpstreamResponseBody(bytes.NewReader([]byte("toolong")), cfg, nil, nil)
		require.Nil(t, body)
		require.True(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
	})

	t.Run("io error does not call onTooLarge", func(t *testing.T) {
		called := false
		onTooLarge := func(_ *gin.Context) { called = true }

		body, err := ReadUpstreamResponseBody(iotest.ErrReader(errors.New("disk failure")), nil, nil, onTooLarge)
		require.Nil(t, body)
		require.Error(t, err)
		require.False(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
		require.False(t, called)
	})
}
