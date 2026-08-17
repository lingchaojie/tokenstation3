package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var errProviderStreamIdleTimeout = errors.New("upstream stream data interval timeout")

const providerTerminalTailDrainGrace = 25 * time.Millisecond

type providerLineScanEvent struct {
	line string
	err  error
}

type providerLineReader struct {
	resp     *http.Response
	events   <-chan providerLineScanEvent
	stop     chan struct{}
	done     <-chan struct{}
	stopOnce sync.Once
	timer    *time.Timer
	timeout  time.Duration
	timerCh  <-chan time.Time
	lastRead *atomic.Int64
}

func newBufferedProviderSSEScanner(reader io.Reader, cfg *config.Config) *bufio.Scanner {
	limit := resolveUpstreamResponseReadLimit(cfg)
	if reader == nil {
		reader = http.NoBody
	}
	reader = io.LimitReader(reader, limit+1)
	maxLineSize := resolveBufferedProviderSSELineLimit(cfg)
	initialSize := 64 * 1024
	if maxLineSize < initialSize {
		initialSize = maxLineSize
	}
	scannerBufferSize := maxLineSize + 2 // one-byte probe plus a possible CRLF delimiter
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, initialSize), scannerBufferSize)
	scanner.Split(boundedTotalStreamScanLines(maxLineSize, limit, ErrUpstreamResponseBodyTooLarge))
	return scanner
}

func boundedTotalStreamScanLines(lineLimit int, totalLimit int64, limitErr error) bufio.SplitFunc {
	if lineLimit < 1 {
		lineLimit = 1
	}
	if limitErr == nil {
		limitErr = bufio.ErrTooLong
	}
	var observed int64
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		advance, token, err = bufio.ScanLines(data, atEOF)
		if err != nil {
			return advance, token, err
		}
		if token != nil && len(token) > lineLimit {
			return 0, nil, limitErr
		}
		// Unlike the generic streaming guard, this scanner owns a total-limit
		// reader with one probe byte. Let an exact-limit unterminated line ask
		// for that probe so N bytes remain valid and N+1 fails deterministically.
		if token == nil && len(data) > lineLimit {
			return 0, nil, limitErr
		}
		if advance <= 0 || totalLimit <= 0 {
			return advance, token, nil
		}
		if int64(advance) > totalLimit-observed {
			return 0, nil, limitErr
		}
		observed += int64(advance)
		return advance, token, nil
	}
}

func newProviderLineReader(
	resp *http.Response,
	cfg *config.Config,
	newScanner func(io.Reader) *bufio.Scanner,
) *providerLineReader {
	body := io.Reader(http.NoBody)
	if resp != nil && resp.Body != nil {
		body = resp.Body
	}
	activity := newProviderBodyReadActivity(body)
	if newScanner == nil {
		newScanner = bufio.NewScanner
	}
	scanner := newScanner(activity)
	events := make(chan providerLineScanEvent, openAIFirstOutputGuardQueueSize)
	stop := make(chan struct{})
	done := make(chan struct{})
	send := func(event providerLineScanEvent) bool {
		select {
		case events <- event:
			return true
		case <-stop:
			return false
		}
	}
	reader := &providerLineReader{
		resp: resp, events: events, stop: stop, done: done,
		lastRead: &activity.lastRead,
	}
	go func() {
		defer close(done)
		defer close(events)
		for scanner.Scan() {
			if !send(providerLineScanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = send(providerLineScanEvent{err: err})
		}
	}()

	if cfg != nil && cfg.Gateway.StreamDataIntervalTimeout > 0 {
		reader.timeout = time.Duration(cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		reader.timer = time.NewTimer(reader.timeout)
		reader.timerCh = reader.timer.C
	}
	return reader
}

func (r *providerLineReader) Next() (string, bool, error) {
	return r.NextContext(context.Background())
}

func (r *providerLineReader) NextContext(ctx context.Context) (string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case event, ok := <-r.events:
			return r.consumeEvent(event, ok)
		default:
		}
		select {
		case event, ok := <-r.events:
			return r.consumeEvent(event, ok)
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-r.timerCh:
			select {
			case event, ok := <-r.events:
				return r.consumeEvent(event, ok)
			default:
			}
			remaining := r.timeout - time.Since(time.Unix(0, r.lastRead.Load()))
			if remaining > 0 {
				r.resetTimerAfter(remaining)
				continue
			}
			return "", false, errProviderStreamIdleTimeout
		}
	}
}

func (r *providerLineReader) NextBefore(deadline time.Time) (line string, ok bool, timedOut bool, err error) {
	if r == nil {
		return "", false, false, nil
	}
	select {
	case event, channelOpen := <-r.events:
		line, ok, err = r.consumeEvent(event, channelOpen)
		return line, ok, false, err
	default:
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", false, true, nil
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case event, channelOpen := <-r.events:
		line, ok, err = r.consumeEvent(event, channelOpen)
		return line, ok, false, err
	case <-timer.C:
		// Prefer a provider event that became ready at the deadline boundary.
		select {
		case event, channelOpen := <-r.events:
			line, ok, err = r.consumeEvent(event, channelOpen)
			return line, ok, false, err
		default:
			return "", false, true, nil
		}
	}
}

func (r *providerLineReader) consumeEvent(event providerLineScanEvent, ok bool) (string, bool, error) {
	if !ok {
		return "", false, nil
	}
	if event.err != nil {
		r.waitScannerAndDrainCapture()
		return "", false, event.err
	}
	return event.line, true, nil
}

func (r *providerLineReader) waitScannerAndDrainCapture() {
	if r == nil || r.resp == nil || r.resp.Body == nil {
		return
	}
	scannerDone := false
	for captureResponseNeedsDrain(r.resp.Body) {
		select {
		case event, ok := <-r.events:
			if !ok {
				scannerDone = true
			}
			if event.err != nil {
				<-r.done
				scannerDone = true
			}
		case <-r.timerCh:
			return
		}
		if scannerDone {
			break
		}
	}
	if scannerDone && captureResponseNeedsDrain(r.resp.Body) {
		r.drainCaptureAfterScannerDone()
	}
}

func (r *providerLineReader) drainCaptureAfterScannerDone() {
	if r == nil || r.resp == nil || r.resp.Body == nil || !captureResponseNeedsDrain(r.resp.Body) {
		return
	}
	timeout := r.timeout
	if timeout <= 0 || timeout > captureOverflowDrainTimeout {
		timeout = captureOverflowDrainTimeout
	}
	drainCaptureResponseRemainderBounded(context.Background(), r.resp.Body, timeout)
}

func (r *providerLineReader) Close() {
	if r == nil {
		return
	}
	if r.resp != nil && r.resp.Body != nil && captureResponseNeedsDrain(r.resp.Body) {
		select {
		case <-r.done:
			// The scanner may have stopped on a token/line error before the
			// provider body reached EOF. It is now safe for this goroutine to
			// drain any unread finite tail before publishing capture.
			r.drainCaptureAfterScannerDone()
		default:
			r.DrainCaptureOnParserFailure(context.Background())
			return
		}
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.stopOnce.Do(func() {
		close(r.stop)
		closeCaptureResponseAndJoinScanner(r.resp, r.done)
	})
}

// DrainCaptureOnParserFailure preserves the provider-native response after a
// protocol/parser failure. The scanner remains the sole body reader while it
// drains a finite tail to EOF (or the capture ceiling). If the provider stalls
// or the request is cancelled, the snapshot is explicitly marked truncated
// before the transport is closed, so an unread tail is never published as an
// exact response.
func (r *providerLineReader) DrainCaptureOnParserFailure(ctx context.Context) {
	if r == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.stopOnce.Do(func() {
		drainCaptureScannerOnParserFailure(ctx, r.resp, r.events, r.done, r.lastRead, r.timeout, nil, func() {
			close(r.stop)
		})
	})
}

func drainCaptureScannerOnParserFailure[T any](
	ctx context.Context,
	resp *http.Response,
	events <-chan T,
	done <-chan struct{},
	lastRead *atomic.Int64,
	idle time.Duration,
	onEvent func(T),
	stopScanner func(),
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if stopScanner == nil {
		stopScanner = func() {}
	}
	if resp == nil || resp.Body == nil || !captureResponseNeedsDrain(resp.Body) {
		stopScanner()
		closeCaptureResponseAndJoinScanner(resp, done)
		return
	}
	if idle <= 0 || idle > captureOverflowDrainTimeout {
		idle = captureOverflowDrainTimeout
	}
	lastReadAt := time.Now()
	if lastRead != nil {
		lastReadAt = time.Unix(0, lastRead.Load())
	}
	remaining := idle - time.Since(lastReadAt)
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()

	scannerDone := false
	aborted := false
	for !scannerDone && captureResponseNeedsDrain(resp.Body) {
		select {
		case event, ok := <-events:
			if !ok {
				scannerDone = true
			} else if onEvent != nil {
				onEvent(event)
			}
		case <-ctx.Done():
			markCaptureResponseTruncated(resp.Body)
			closeCaptureResponseUnderlying(resp)
			aborted = true
		case <-timer.C:
			lastReadAt = time.Now()
			if lastRead != nil {
				lastReadAt = time.Unix(0, lastRead.Load())
			}
			remaining = idle - time.Since(lastReadAt)
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			markCaptureResponseTruncated(resp.Body)
			closeCaptureResponseUnderlying(resp)
			aborted = true
		}
		if aborted {
			for event := range events {
				if onEvent != nil {
					onEvent(event)
				}
			}
			scannerDone = true
		}
	}

	// Reaching the capture limit still requires interrupting and joining the
	// scanner; a natural EOF has already closed the event channel.
	if scannerDone && !aborted && captureResponseNeedsDrain(resp.Body) {
		drainCaptureResponseRemainderBounded(ctx, resp.Body, idle)
	}
	closeCaptureResponseUnderlying(resp)
	if !scannerDone {
		for event := range events {
			if onEvent != nil {
				onEvent(event)
			}
		}
	}
	if done != nil {
		<-done
	}
	if lifecycle, ok := resp.Body.(captureResponseLifecycle); ok {
		lifecycle.joinCaptureReaders()
		lifecycle.finishCapture()
	}
	stopScanner()
}

// CloseAndCollectBufferedTail closes an intentionally open provider stream at
// the terminal-tail grace boundary, then lets bufio.Scanner dispatch any bytes
// it had already buffered but could not expose before EOF. This preserves the
// prompt terminal-return contract without silently accepting an unterminated
// provider frame that was already on the wire.
func (r *providerLineReader) CloseAndCollectBufferedTail() ([]string, error) {
	if r == nil {
		return nil, nil
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	var lines []string
	var scanErr error
	r.stopOnce.Do(func() {
		closeCaptureResponseUnderlying(r.resp)
		for event := range r.events {
			if event.err != nil {
				if !providerScannerCloseError(event.err) && scanErr == nil {
					scanErr = event.err
				}
				continue
			}
			lines = append(lines, event.line)
		}
		if r.done != nil {
			<-r.done
		}
		if r.resp != nil && r.resp.Body != nil {
			if lifecycle, ok := r.resp.Body.(captureResponseLifecycle); ok {
				lifecycle.joinCaptureReaders()
				lifecycle.finishCapture()
			}
		}
		close(r.stop)
	})
	return lines, scanErr
}

func providerScannerCloseError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, http.ErrBodyReadAfterClose)
}

// closeAndCollectScannerBufferedTail transfers sole ownership of scanner to a
// goroutine after an application terminal has been observed. It waits for a
// short period of provider-byte inactivity, closes the intentionally open
// response body, and then collects tokens that bufio.Scanner could expose only
// after EOF (notably an unterminated post-terminal line).
func closeAndCollectScannerBufferedTail(
	resp *http.Response,
	scanner *bufio.Scanner,
	activity *providerBodyReadActivity,
	idle time.Duration,
) ([]string, error) {
	if scanner == nil {
		return nil, nil
	}
	if idle <= 0 {
		idle = providerTerminalTailDrainGrace
	}
	events := make(chan providerLineScanEvent, openAIFirstOutputGuardQueueSize)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		for scanner.Scan() {
			events <- providerLineScanEvent{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			events <- providerLineScanEvent{err: err}
		}
	}()

	lastRead := time.Now()
	if activity != nil {
		lastRead = activity.LastReadTime()
	}
	initialWait := idle - time.Since(lastRead)
	if initialWait <= 0 {
		initialWait = time.Nanosecond
	}
	timer := time.NewTimer(initialWait)
	defer timer.Stop()
	var lines []string
	var scanErr error
	closedForIdle := false
	for {
		select {
		case event, ok := <-events:
			if !ok {
				<-done
				return lines, scanErr
			}
			if event.err != nil {
				if (!closedForIdle || !providerScannerCloseError(event.err)) && scanErr == nil {
					scanErr = event.err
				}
				continue
			}
			lines = append(lines, event.line)
		case <-timer.C:
			if activity != nil {
				remaining := idle - time.Since(activity.LastReadTime())
				if remaining > 0 {
					timer.Reset(remaining)
					continue
				}
			}
			closedForIdle = true
			closeCaptureResponseUnderlying(resp)
			timer.Reset(time.Hour)
		}
	}
}

func (r *providerLineReader) resetTimerAfter(delay time.Duration) {
	if r.timer == nil {
		return
	}
	if !r.timer.Stop() {
		select {
		case <-r.timer.C:
		default:
		}
	}
	r.timer.Reset(delay)
}
