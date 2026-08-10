package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCaptureHealthRepositoryUpsertAddsAggregate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	bucket := time.Date(2026, 8, 11, 1, 2, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectPrepare(regexp.QuoteMeta(captureHealthUpsertSQL))
	mock.ExpectExec(regexp.QuoteMeta(captureHealthUpsertSQL)).
		WithArgs(bucket, "host-a", "writer_queue_full", int64(3), int64(900), int64(7), int64(8), int64(1024), "queue full").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewCaptureHealthRepository(db)
	err = repo.UpsertEvents(context.Background(), []service.CaptureHealthEvent{{
		MinuteBucket:     bucket,
		InstanceID:       "host-a",
		Reason:           "writer_queue_full",
		DroppedRecords:   3,
		DroppedBytes:     900,
		WorkerQueuePeak:  7,
		WriterQueuePeak:  8,
		InFlightBytePeak: 1024,
		LastError:        "queue full",
	}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureHealthRepositoryListsNewestFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	bucket := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(captureHealthListSQL)).WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"minute_bucket", "instance_id", "reason", "dropped_records", "dropped_bytes",
			"worker_queue_peak", "writer_queue_peak", "in_flight_bytes_peak", "last_error",
		}).AddRow(bucket, "host-a", "byte_budget_exceeded", int64(2), int64(200), int64(4), int64(5), int64(600), ""))

	repo := NewCaptureHealthRepository(db)
	got, err := repo.ListEvents(context.Background(), start, end)
	require.NoError(t, err)
	require.Equal(t, []service.CaptureHealthEvent{{
		MinuteBucket: bucket, InstanceID: "host-a", Reason: "byte_budget_exceeded",
		DroppedRecords: 2, DroppedBytes: 200, WorkerQueuePeak: 4, WriterQueuePeak: 5, InFlightBytePeak: 600,
	}}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureHealthRepositoryDeletesBeforeCutoff(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cutoff := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(captureHealthDeleteSQL)).WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 9))

	repo := NewCaptureHealthRepository(db)
	deleted, err := repo.DeleteBefore(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(9), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
