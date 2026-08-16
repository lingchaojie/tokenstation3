package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/stretchr/testify/require"
)

func TestCaptureHealthTrackerCountsLossByReasonAndCapsIncidents(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	tracker := newCaptureHealthTracker("host-a", func() time.Time { return now })
	for i := 0; i < 105; i++ {
		tracker.recordDrop(CaptureDropWriterQueueFull, 1, int64(i+1), fmt.Errorf("password=secret-%d", i))
		now = now.Add(time.Second)
	}

	got := tracker.snapshot()
	require.Equal(t, uint64(105), got.DroppedRecords)
	require.Equal(t, uint64(5565), got.DroppedBytes)
	require.Equal(t, uint64(105), got.DroppedByReason[string(CaptureDropWriterQueueFull)].Records)
	require.Len(t, got.RecentIncidents, 100)
	require.Equal(t, int64(6), got.RecentIncidents[0].Bytes)
	require.Equal(t, int64(105), got.RecentIncidents[99].Bytes)
	require.NotContains(t, got.LastError, "secret-104")
	require.LessOrEqual(t, len(got.LastError), captureHealthErrorMaxBytes)
}

func TestCaptureHealthTrackerAggregatesMinuteBuckets(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	tracker := newCaptureHealthTracker("host-a", func() time.Time { return now })
	tracker.workerQueue.set(7)
	tracker.writerQueue.set(8)
	tracker.inFlightBytes.set(900)
	tracker.recordDrop(CaptureDropByteBudgetExceeded, 2, 300, nil)

	events := tracker.takeBucketsBefore(now.Add(time.Minute).Truncate(time.Minute), false)
	require.Equal(t, []CaptureHealthEvent{{
		MinuteBucket: now.Truncate(time.Minute), InstanceID: "host-a", Reason: string(CaptureDropByteBudgetExceeded),
		DroppedRecords: 2, DroppedBytes: 300, WorkerQueuePeak: 7, WriterQueuePeak: 8, InFlightBytePeak: 900,
	}}, events)
	require.Empty(t, tracker.takeBucketsBefore(now.Add(2*time.Minute), false))
}

type deferredLifecycleWriter struct {
	items chan *archiveWriteItem
	err   error
}

func newDeferredLifecycleWriter() *deferredLifecycleWriter {
	return &deferredLifecycleWriter{items: make(chan *archiveWriteItem, 8)}
}

func (w *deferredLifecycleWriter) Write(_ context.Context, item *archiveWriteItem) error {
	if w.err != nil {
		return w.err
	}
	w.items <- item
	return nil
}
func (w *deferredLifecycleWriter) Stop() {}

func (w *deferredLifecycleWriter) take(t *testing.T) *archiveWriteItem {
	t.Helper()
	select {
	case item := <-w.items:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for archive item")
		return nil
	}
}

func TestCapturePoolKeepsBytesUntilWriterCompletes(t *testing.T) {
	writer := newDeferredLifecycleWriter()
	pool := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 8, OverflowPolicy: "drop", MaxQueueBytes: 1024,
	}, writer)
	defer pool.Stop()

	pool.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
	item := writer.take(t)
	require.Equal(t, int64(60), pool.Health().InFlightBytes.Current)
	require.True(t, item.completeSuccess())
	require.False(t, item.completeSuccess(), "completion must be exactly once")
	require.Eventually(t, func() bool { return pool.Health().InFlightBytes.Current == 0 }, time.Second, time.Millisecond)
	require.Equal(t, uint64(1), pool.Health().WrittenRecords)
}

func TestCapturePoolSynchronousAdmissionFailureHasNoQueueState(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: errors.New("sidecar unavailable")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	attempt, ok := pool.Begin(context.Background(), model.Begin{})

	require.False(t, ok)
	require.Nil(t, attempt)
	require.Equal(t, 1, transport.Begins(), "admission must synchronously issue exactly one IPC Begin")
	health := pool.Health()
	require.Zero(t, health.WorkerQueue.Current)
	require.Zero(t, health.WriterQueue.Current)
	require.Zero(t, health.InFlightBytes.Current)
	require.Empty(t, health.DroppedByReason, "gateway no longer reports retired queue or byte-budget loss")
}

func TestCapturePoolSynchronousAttemptHasExactlyOneTerminal(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), model.Begin{Policy: model.ContentPolicy{StoreResponseBody: true}})
	require.True(t, ok)
	require.True(t, attempt.WriteResponse([]byte("response")))
	final := model.Final{HTTPStatus: 200, StopReason: "stop", ResponseComplete: true}
	require.True(t, attempt.Finalize(final))
	require.True(t, attempt.Commit())
	require.False(t, attempt.Commit(), "a synchronous attempt may commit only once")
	attempt.Abort()

	recording := transport.Attempts()[0]
	require.Equal(t, []captureTerminalState{captureCommitted}, recording.TerminalStates())
	require.Equal(t, []model.Final{final}, recording.Finals())
	health := pool.Health()
	require.Zero(t, health.WorkerQueue.Current)
	require.Zero(t, health.WriterQueue.Current)
	require.Zero(t, health.InFlightBytes.Current)
}

func TestCapturePoolReportsWriterQueueLoss(t *testing.T) {
	writer := newDeferredLifecycleWriter()
	writer.err = errArchiveQueueFull
	pool := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 8, OverflowPolicy: "drop", MaxQueueBytes: 1024,
	}, writer)
	defer pool.Stop()

	pool.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
	require.Eventually(t, func() bool {
		return pool.Health().DroppedByReason[string(CaptureDropWriterQueueFull)].Records == 1
	}, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		return pool.Health().InFlightBytes.Current == 0
	}, time.Second, time.Millisecond)
}

func TestCapturePoolReportsUnavailableWriter(t *testing.T) {
	writer := newDeferredLifecycleWriter()
	writer.err = errArchiveWriterUnavailable
	pool := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 8, OverflowPolicy: "drop", MaxQueueBytes: 1024,
	}, writer)
	defer pool.Stop()

	pool.Submit(&CaptureRecord{RawResponse: []byte("body")})
	require.Eventually(t, func() bool {
		return pool.Health().DroppedByReason[string(CaptureDropWriterUnavailable)].Records == 1
	}, time.Second, time.Millisecond)
}

func TestCaptureHealthErrorNeverContainsRecordBody(t *testing.T) {
	tracker := newCaptureHealthTracker("host-a", time.Now)
	recordBody := "private-user-prompt"
	tracker.recordDrop(CaptureDropClickHouseSendFailed, 1, int64(len(recordBody)), errors.New("send failed"))

	encoded := fmt.Sprintf("%+v", tracker.snapshot())
	require.False(t, strings.Contains(encoded, recordBody))
}

func TestCaptureHealthErrorUsesSafeCategoryInsteadOfDriverDetails(t *testing.T) {
	tracker := newCaptureHealthTracker("host-a", time.Now)
	tracker.recordDrop(
		CaptureDropClickHouseSendFailed,
		1,
		42,
		errors.New("dial clickhouse://archive:super-secret@db.internal:9000 failed"),
	)

	got := tracker.snapshot()
	require.Equal(t, "ClickHouse batch send failed", got.LastError)
	require.NotContains(t, got.LastError, "super-secret")
	require.NotContains(t, got.LastError, "db.internal")
	require.Equal(t, got.LastError, got.RecentIncidents[0].Error)
}
