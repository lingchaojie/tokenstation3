package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type captureManagerTestWriter struct {
	writes atomic.Int32
	stops  atomic.Int32
}

func (w *captureManagerTestWriter) Write(_ context.Context, item *archiveWriteItem) error {
	w.writes.Add(1)
	if item != nil {
		item.completeSuccess()
	}
	return nil
}

func (w *captureManagerTestWriter) Stop() {
	w.stops.Add(1)
}

type controlledCaptureRetryWait struct {
	delays   chan time.Duration
	releases chan struct{}
}

func newControlledCaptureRetryWait() *controlledCaptureRetryWait {
	return &controlledCaptureRetryWait{
		delays:   make(chan time.Duration),
		releases: make(chan struct{}),
	}
}

func (w *controlledCaptureRetryWait) wait(ctx context.Context, delay time.Duration) bool {
	select {
	case w.delays <- delay:
	case <-ctx.Done():
		return false
	}
	select {
	case <-w.releases:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *controlledCaptureRetryWait) release() {
	w.releases <- struct{}{}
}

func newCaptureManagerTestItem() *archiveWriteItem {
	return newArchiveWriteItem(&CaptureRecord{}, 0, nil)
}

func TestCaptureArchiveWriterManagerRecoversAfterInitializationFailures(t *testing.T) {
	t.Parallel()

	waits := newControlledCaptureRetryWait()
	recoveredWriter := &captureManagerTestWriter{}
	initErr := errors.New("clickhouse unavailable")
	var attempts atomic.Int32

	manager := newCaptureArchiveWriterManager(
		config.GatewayCaptureConfig{},
		newCaptureHealthTracker("test", time.Now),
		captureWriterRetryOptions{
			InitialDelay: 2 * time.Second,
			MaxDelay:     60 * time.Second,
			Jitter:       func(delay time.Duration) time.Duration { return delay },
			Wait:         waits.wait,
			Factory: func(context.Context, config.GatewayCaptureConfig, *captureHealthTracker) (ArchiveWriter, error) {
				if attempts.Add(1) < 3 {
					return nil, initErr
				}
				return recoveredWriter, nil
			},
		},
	)
	t.Cleanup(manager.Stop)

	require.False(t, manager.Ready())
	require.ErrorIs(t, manager.Write(context.Background(), newCaptureManagerTestItem()), errArchiveWriterUnavailable)
	require.Contains(t, manager.InitializationError(), initErr.Error())

	require.Equal(t, 2*time.Second, <-waits.delays)
	waits.release()
	require.Equal(t, 4*time.Second, <-waits.delays)
	waits.release()

	require.Eventually(t, manager.Ready, time.Second, time.Millisecond)
	require.Empty(t, manager.InitializationError())
	require.NoError(t, manager.Write(context.Background(), newCaptureManagerTestItem()))
	require.Equal(t, int32(1), recoveredWriter.writes.Load())
	require.Equal(t, int32(3), attempts.Load())
}

func TestCaptureArchiveWriterManagerBackoffDoublesAndCaps(t *testing.T) {
	t.Parallel()

	waits := newControlledCaptureRetryWait()
	manager := newCaptureArchiveWriterManager(
		config.GatewayCaptureConfig{},
		newCaptureHealthTracker("test", time.Now),
		captureWriterRetryOptions{
			InitialDelay: 2 * time.Second,
			MaxDelay:     10 * time.Second,
			Jitter:       func(delay time.Duration) time.Duration { return delay },
			Wait:         waits.wait,
			Factory: func(context.Context, config.GatewayCaptureConfig, *captureHealthTracker) (ArchiveWriter, error) {
				return nil, errors.New("still unavailable")
			},
		},
	)

	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, expected := range want {
		require.Equal(t, expected, <-waits.delays)
		if i < len(want)-1 {
			waits.release()
		}
	}
	manager.Stop()
}

func TestCaptureArchiveWriterManagerStopCancelsPendingRetry(t *testing.T) {
	t.Parallel()

	waits := newControlledCaptureRetryWait()
	var attempts atomic.Int32
	manager := newCaptureArchiveWriterManager(
		config.GatewayCaptureConfig{},
		newCaptureHealthTracker("test", time.Now),
		captureWriterRetryOptions{
			InitialDelay: time.Second,
			MaxDelay:     time.Second,
			Jitter:       func(delay time.Duration) time.Duration { return delay },
			Wait:         waits.wait,
			Factory: func(context.Context, config.GatewayCaptureConfig, *captureHealthTracker) (ArchiveWriter, error) {
				attempts.Add(1)
				return nil, errors.New("unavailable")
			},
		},
	)

	require.Equal(t, time.Second, <-waits.delays)
	stopped := make(chan struct{})
	go func() {
		manager.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the pending retry wait")
	}
	require.Equal(t, int32(1), attempts.Load())
}

func TestCaptureArchiveWriterManagerStopWaitsForInFlightFactory(t *testing.T) {
	t.Parallel()

	waits := newControlledCaptureRetryWait()
	retryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	lateWriter := &captureManagerTestWriter{}
	var attempts atomic.Int32

	manager := newCaptureArchiveWriterManager(
		config.GatewayCaptureConfig{},
		newCaptureHealthTracker("test", time.Now),
		captureWriterRetryOptions{
			InitialDelay: time.Second,
			MaxDelay:     time.Second,
			Jitter:       func(delay time.Duration) time.Duration { return delay },
			Wait:         waits.wait,
			Factory: func(context.Context, config.GatewayCaptureConfig, *captureHealthTracker) (ArchiveWriter, error) {
				if attempts.Add(1) == 1 {
					return nil, errors.New("initial failure")
				}
				close(retryEntered)
				<-releaseFactory
				return lateWriter, nil
			},
		},
	)

	require.Equal(t, time.Second, <-waits.delays)
	waits.release()
	<-retryEntered

	stopped := make(chan struct{})
	go func() {
		manager.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned before the in-flight factory completed")
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseFactory)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the in-flight factory completed")
	}

	require.False(t, manager.Ready())
	require.Equal(t, int32(1), lateWriter.stops.Load())
}

func TestCaptureArchiveWriterManagerStopsRecoveredWriterExactlyOnce(t *testing.T) {
	t.Parallel()

	waits := newControlledCaptureRetryWait()
	recoveredWriter := &captureManagerTestWriter{}
	var attempts atomic.Int32
	manager := newCaptureArchiveWriterManager(
		config.GatewayCaptureConfig{},
		newCaptureHealthTracker("test", time.Now),
		captureWriterRetryOptions{
			InitialDelay: time.Second,
			MaxDelay:     time.Second,
			Jitter:       func(delay time.Duration) time.Duration { return delay },
			Wait:         waits.wait,
			Factory: func(context.Context, config.GatewayCaptureConfig, *captureHealthTracker) (ArchiveWriter, error) {
				if attempts.Add(1) == 1 {
					return nil, errors.New("initial failure")
				}
				return recoveredWriter, nil
			},
		},
	)

	require.Equal(t, time.Second, <-waits.delays)
	waits.release()
	require.Eventually(t, manager.Ready, time.Second, time.Millisecond)

	manager.Stop()
	manager.Stop()
	require.Equal(t, int32(1), recoveredWriter.stops.Load())
}
