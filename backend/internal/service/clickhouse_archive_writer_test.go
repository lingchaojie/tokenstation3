package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNoopArchiveWriterNeverErrors(t *testing.T) {
	var w ArchiveWriter = noopArchiveWriter{}
	completed := false
	item := newArchiveWriteItem(&CaptureRecord{}, 0, func(result archiveWriteResult) {
		completed = result.success
	})
	if err := w.Write(context.Background(), item); err != nil {
		t.Fatalf("noop write must not error: %v", err)
	}
	require.True(t, completed)
	w.Stop() // 不 panic
}

func TestArchiveWriterCompletesWholeBatchForEveryFlushOutcome(t *testing.T) {
	tests := []struct {
		name   string
		reason CaptureDropReason
		err    error
	}{
		{name: "prepare failure", reason: CaptureDropClickHousePrepareFailed, err: errors.New("prepare failed")},
		{name: "append failure", reason: CaptureDropClickHouseAppendFailed, err: errors.New("append failed")},
		{name: "send failure", reason: CaptureDropClickHouseSendFailed, err: errors.New("send failed")},
		{name: "success"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := newCaptureHealthTracker("host-a", time.Now)
			w := &clickHouseArchiveWriter{
				tracker: tracker,
				sendBatch: func([]*archiveWriteItem) (CaptureDropReason, error) {
					return tt.reason, tt.err
				},
			}
			items := []*archiveWriteItem{
				newTrackedArchiveWriteItem(tracker, []byte("one")),
				newTrackedArchiveWriteItem(tracker, []byte("two")),
			}
			w.flush(items)

			got := tracker.snapshot()
			if tt.err == nil {
				require.Equal(t, uint64(2), got.WrittenRecords)
				require.Zero(t, got.DroppedRecords)
				return
			}
			require.Equal(t, uint64(2), got.DroppedByReason[string(tt.reason)].Records)
			require.Equal(t, uint64(6), got.DroppedByReason[string(tt.reason)].Bytes)
		})
	}
}

func TestArchiveWriterReportsExactQueueDepthAndRejectsFullQueue(t *testing.T) {
	tracker := newCaptureHealthTracker("host-a", time.Now)
	w := &clickHouseArchiveWriter{
		batchCh: make(chan *archiveWriteItem, 1),
		tracker: tracker,
	}
	first := newArchiveWriteItem(&CaptureRecord{}, 1, nil)
	second := newArchiveWriteItem(&CaptureRecord{}, 1, nil)
	require.NoError(t, w.Write(context.Background(), first))
	require.ErrorIs(t, w.Write(context.Background(), second), errArchiveQueueFull)
	require.Equal(t, CaptureGaugeSnapshot{Current: 1, Peak: 1}, tracker.snapshot().WriterQueue)
	<-w.batchCh
	w.recordDequeued()
	require.Zero(t, tracker.snapshot().WriterQueue.Current)
}

func newTrackedArchiveWriteItem(tracker *captureHealthTracker, body []byte) *archiveWriteItem {
	bytes := int64(len(body))
	return newArchiveWriteItem(&CaptureRecord{RawResponse: body}, bytes, func(result archiveWriteResult) {
		if result.success {
			tracker.recordWritten(1)
			return
		}
		tracker.recordDrop(result.reason, 1, bytes, result.err)
	})
}

func TestCreateTableDDLContainsRawColumns(t *testing.T) {
	ddl := archiveCreateTableDDL("llm_archive", "model_call_archive")
	for _, must := range []string{
		"CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive",
		"raw_request        String CODEC(ZSTD(3))",
		"raw_response       String CODEC(ZSTD(3))",
		"session_id         String",
		"ORDER BY (session_id, captured_at, request_id)",
		"PARTITION BY toYYYYMM(captured_at)",
	} {
		if !strings.Contains(ddl, must) {
			t.Fatalf("DDL missing %q", must)
		}
	}
}
