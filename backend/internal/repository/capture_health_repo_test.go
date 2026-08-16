package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
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
		WithArgs(bucket, "sidecar-source", "spool_cap", int64(3), int64(900), int64(9<<30), int64(42), int64(91), int64(8), int64(2), "capture spool reached physical cap").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewCaptureHealthRepository(db)
	err = repo.UpsertEvents(context.Background(), []service.CaptureHealthEvent{{
		MinuteBucket:              bucket,
		InstanceID:                "sidecar-source",
		Reason:                    "spool_cap",
		DroppedRecords:            3,
		DroppedBytes:              900,
		SpoolUsedBytesPeak:        9 << 30,
		ReadyRecordsPeak:          42,
		OldestReadyAgeSecondsPeak: 91,
		UploadRetries:             8,
		SidecarRestarts:           2,
		LastError:                 "capture spool reached physical cap",
	}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, captureHealthUpsertSQL, "dropped_records = GREATEST")
	require.Contains(t, captureHealthUpsertSQL, "upload_retries = GREATEST")
	require.Contains(t, captureHealthUpsertSQL, "sidecar_restarts = GREATEST")
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
			"spool_used_bytes_peak", "ready_records_peak", "oldest_ready_age_seconds_peak", "upload_retries", "sidecar_restarts", "last_error",
		}).AddRow(bucket, "sidecar-source", "spool_cap", int64(2), int64(200), int64(9<<30), int64(42), int64(91), int64(8), int64(2), ""))

	repo := NewCaptureHealthRepository(db)
	got, err := repo.ListEvents(context.Background(), start, end)
	require.NoError(t, err)
	require.Equal(t, []service.CaptureHealthEvent{{
		MinuteBucket: bucket, InstanceID: "sidecar-source", Reason: "spool_cap",
		DroppedRecords: 2, DroppedBytes: 200, SpoolUsedBytesPeak: 9 << 30,
		ReadyRecordsPeak: 42, OldestReadyAgeSecondsPeak: 91, UploadRetries: 8, SidecarRestarts: 2,
	}}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureHealthRepositoryListsLatestCumulativeValuesBeforeWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	before := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	bucket := before.Add(-6 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(captureHealthListLatestBeforeSQL)).
		WithArgs(before, pq.Array([]string{"sidecar-a", "sidecar-b"}), pq.Array([]string{"ipc_unavailable"})).
		WillReturnRows(sqlmock.NewRows([]string{
			"minute_bucket", "instance_id", "reason", "dropped_records", "dropped_bytes",
			"spool_used_bytes_peak", "ready_records_peak", "oldest_ready_age_seconds_peak", "upload_retries", "sidecar_restarts", "last_error",
		}).AddRow(bucket, "sidecar-a", "ipc_unavailable", int64(9), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), ""))

	repo := NewCaptureHealthRepository(db)
	got, err := repo.ListLatestEventsBefore(context.Background(), before, []string{"sidecar-a", "sidecar-b"}, []string{"ipc_unavailable"})
	require.NoError(t, err)
	require.Equal(t, []service.CaptureHealthEvent{{
		MinuteBucket: bucket, InstanceID: "sidecar-a", Reason: "ipc_unavailable", DroppedRecords: 9,
	}}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureHealthRepositoryBoundsLatestCumulativeLookupInputs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewCaptureHealthRepository(db)

	_, err = repo.ListLatestEventsBefore(
		context.Background(), time.Now(), make([]string, captureHealthLatestSourceLimit+1), []string{"spool_cap"},
	)
	require.Error(t, err)
	_, err = repo.ListLatestEventsBefore(
		context.Background(), time.Now(), []string{"sidecar"}, make([]string, captureHealthLatestReasonLimit+1),
	)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "bounded rejection must not query PostgreSQL")
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
