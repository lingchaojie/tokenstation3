package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

var ErrUpstreamResponseBodyTooLarge = errors.New("upstream response body too large")

// defaultUpstreamResponseReadMaxBytes 源自 config.DefaultUpstreamResponseReadMaxBytes，
// 仅在 cfg 为 nil 时作为兜底（测试或极端场景）。
const defaultUpstreamResponseReadMaxBytes = config.DefaultUpstreamResponseReadMaxBytes

const captureOverflowDrainTimeout = time.Second

func resolveUpstreamResponseReadLimit(cfg *config.Config) int64 {
	if cfg != nil && cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
		return cfg.Gateway.UpstreamResponseReadMaxBytes
	}
	return defaultUpstreamResponseReadMaxBytes
}

func readUpstreamResponseBodyLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("response body is nil")
	}
	if maxBytes <= 0 {
		maxBytes = defaultUpstreamResponseReadMaxBytes
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: limit=%d", ErrUpstreamResponseBodyTooLarge, maxBytes)
	}
	return body, nil
}

type providerBodyReadActivity struct {
	reader   io.Reader
	lastRead atomic.Int64
}

type providerBodyReadActivityCarrier interface {
	providerReadActivity() *providerBodyReadActivity
}

func newProviderBodyReadActivity(reader io.Reader) *providerBodyReadActivity {
	if reader == nil {
		reader = http.NoBody
	}
	activity := &providerBodyReadActivity{reader: reader}
	activity.lastRead.Store(time.Now().UnixNano())
	return activity
}

func (r *providerBodyReadActivity) LastReadTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return time.Unix(0, r.lastRead.Load())
}

func (r *providerBodyReadActivity) providerReadActivity() *providerBodyReadActivity { return r }

func (r *providerBodyReadActivity) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.lastRead.Store(time.Now().UnixNano())
	}
	return n, err
}

func (r *providerBodyReadActivity) Close() error {
	if closer, ok := r.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (r *providerBodyReadActivity) closeCaptureUnderlying() error {
	if lifecycle, ok := r.reader.(captureResponseLifecycle); ok {
		return lifecycle.closeCaptureUnderlying()
	}
	return r.Close()
}

func (r *providerBodyReadActivity) joinCaptureReaders() {
	if lifecycle, ok := r.reader.(captureResponseLifecycle); ok {
		lifecycle.joinCaptureReaders()
	}
}

func (r *providerBodyReadActivity) finishCapture() {
	if lifecycle, ok := r.reader.(captureResponseLifecycle); ok {
		lifecycle.finishCapture()
	}
}

func (r *providerBodyReadActivity) captureResponseNeedsDrain() bool {
	if lifecycle, ok := r.reader.(captureResponseDrainLifecycle); ok {
		return lifecycle.captureResponseNeedsDrain()
	}
	return false
}

func (r *providerBodyReadActivity) captureResponseDrainRemaining() int64 {
	if lifecycle, ok := r.reader.(captureResponseDrainLifecycle); ok {
		return lifecycle.captureResponseDrainRemaining()
	}
	return 0
}

func (r *providerBodyReadActivity) markCaptureResponseTruncated() {
	if lifecycle, ok := r.reader.(captureResponseDrainLifecycle); ok {
		lifecycle.markCaptureResponseTruncated()
	}
}

func providerBodyReaderWithActivity(reader io.Reader) (io.Reader, *providerBodyReadActivity) {
	if carrier, ok := reader.(providerBodyReadActivityCarrier); ok {
		if activity := carrier.providerReadActivity(); activity != nil {
			return reader, activity
		}
	}
	activity := newProviderBodyReadActivity(reader)
	return activity, activity
}

type providerBodyReadResult struct {
	body []byte
	err  error
}

func readAllWithProviderIdle(
	ctx context.Context,
	reader io.Reader,
	timeout time.Duration,
	read func(io.Reader) ([]byte, error),
) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("response body is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	activity := newProviderBodyReadActivity(reader)
	resultCh := make(chan providerBodyReadResult, 1)
	var terminalState atomic.Int32 // 0=reading, 1=completed, 2=aborted by context/idle
	go func() {
		body, err := read(activity)
		terminalState.CompareAndSwap(0, 1)
		resultCh <- providerBodyReadResult{body: body, err: err}
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		timerCh = timer.C
		defer timer.Stop()
	}
	for {
		select {
		case result := <-resultCh:
			return result.body, result.err
		default:
		}
		select {
		case result := <-resultCh:
			return result.body, result.err
		case <-ctx.Done():
			if !terminalState.CompareAndSwap(0, 2) {
				result := <-resultCh
				return result.body, result.err
			}
			markCaptureResponseTruncated(reader)
			closeCaptureReaderUnderlying(reader)
			result := <-resultCh
			return result.body, ctx.Err()
		case <-timerCh:
			remaining := timeout - time.Since(time.Unix(0, activity.lastRead.Load()))
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			if !terminalState.CompareAndSwap(0, 2) {
				result := <-resultCh
				return result.body, result.err
			}
			markCaptureResponseTruncated(reader)
			closeCaptureReaderUnderlying(reader)
			result := <-resultCh
			return result.body, errProviderStreamIdleTimeout
		}
	}
}

func resolveProviderBodyIdleTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.Gateway.StreamDataIntervalTimeout > 0 {
		return time.Duration(cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	return 0
}

func readUpstreamResponseBodyLimitedWithIdle(
	ctx context.Context,
	reader io.Reader,
	maxBytes int64,
	timeout time.Duration,
) ([]byte, error) {
	return readAllWithProviderIdle(ctx, reader, timeout, func(activeReader io.Reader) ([]byte, error) {
		return readUpstreamResponseBodyLimited(activeReader, maxBytes)
	})
}

// TooLargeWriter 在响应超限时向客户端写格式化的错误响应。
type TooLargeWriter func(c *gin.Context)

// ReadUpstreamResponseBody 读取上游非流式响应体。
// 超限时自动记录 ops error 并调用 onTooLarge 向客户端写错误。
func ReadUpstreamResponseBody(reader io.Reader, cfg *config.Config, c *gin.Context, onTooLarge TooLargeWriter) ([]byte, error) {
	return ReadUpstreamResponseBodyWithContext(ginRequestContext(c), reader, cfg, c, onTooLarge)
}

// ReadUpstreamResponseBodyWithContext is the context-explicit form used by
// gateways whose upstream request lifetime is deliberately detached from the
// downstream client request. The reader and its idle timer must follow that
// upstream lifetime instead of an already-cancelled Gin request context.
func ReadUpstreamResponseBodyWithContext(ctx context.Context, reader io.Reader, cfg *config.Config, c *gin.Context, onTooLarge TooLargeWriter) ([]byte, error) {
	maxBytes := resolveUpstreamResponseReadLimit(cfg)
	body, err := readUpstreamResponseBodyLimitedWithIdle(
		ctx, reader, maxBytes, resolveProviderBodyIdleTimeout(cfg),
	)
	if err != nil {
		if errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			// The functional reader deliberately stops after maxBytes+1, which may
			// be lower than the policy-approved capture ceiling. Continue draining
			// only capture wrappers so their bounded tee can observe the same final
			// provider response accurately; business parsing and client behavior
			// remain governed by maxBytes.
			drainCaptureResponseRemainderBounded(ctx, reader, captureOverflowDrainTimeout)
			setOpsUpstreamError(c, http.StatusBadGateway, "upstream response too large", "")
			if onTooLarge != nil {
				onTooLarge(c)
			}
		}
		return nil, err
	}
	return body, nil
}

func ginRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func drainCaptureResponseRemainder(reader io.Reader) {
	lifecycle, ok := reader.(captureResponseDrainLifecycle)
	if !ok {
		return
	}
	remaining := lifecycle.captureResponseDrainRemaining()
	if remaining <= 0 {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, remaining))
}

// drainCaptureResponseRemainderBounded gives a capture wrapper a short,
// bounded opportunity to observe bytes beyond the functional parsing limit.
// The drain owns the reader after the functional reader has stopped. On
// cancellation or idle expiry it closes only the underlying transport, waits
// for the sole reader to exit, and leaves final capture publication to the
// normal attempt lifecycle.
func drainCaptureResponseRemainderBounded(ctx context.Context, reader io.Reader, timeout time.Duration) {
	if !captureResponseNeedsDrain(reader) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = captureOverflowDrainTimeout
	}
	activeReader, activity := providerBodyReaderWithActivity(reader)
	done := make(chan struct{})
	go func() {
		defer close(done)
		drainCaptureResponseRemainder(activeReader)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		default:
		}
		select {
		case <-done:
			return
		case <-ctx.Done():
			markCaptureResponseTruncated(reader)
			closeCaptureReaderUnderlying(reader)
			<-done
			return
		case <-timer.C:
			remaining := timeout - time.Since(activity.LastReadTime())
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			markCaptureResponseTruncated(reader)
			closeCaptureReaderUnderlying(reader)
			<-done
			return
		}
	}
}

func closeCaptureReaderUnderlying(reader io.Reader) {
	if lifecycle, ok := reader.(captureResponseLifecycle); ok {
		_ = lifecycle.closeCaptureUnderlying()
		return
	}
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
}

func captureResponseNeedsDrain(reader io.Reader) bool {
	if lifecycle, ok := reader.(captureResponseDrainLifecycle); ok {
		return lifecycle.captureResponseNeedsDrain()
	}
	return false
}

func markCaptureResponseTruncated(reader io.Reader) {
	if lifecycle, ok := reader.(captureResponseDrainLifecycle); ok {
		lifecycle.markCaptureResponseTruncated()
	}
}

// openAITooLargeError 以 OpenAI / Gemini 格式写入超限错误。
func openAITooLargeError(c *gin.Context) {
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream response too large",
		},
	})
}
