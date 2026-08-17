package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const captureHealthUpsertSQL = `
INSERT INTO capture_health_events (
  minute_bucket, instance_id, reason, dropped_records, dropped_bytes,
  spool_used_bytes_peak, ready_records_peak, oldest_ready_age_seconds_peak,
  upload_retries, sidecar_restarts, last_error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (minute_bucket, instance_id, reason) DO UPDATE SET
  dropped_records = GREATEST(capture_health_events.dropped_records, EXCLUDED.dropped_records),
  dropped_bytes = GREATEST(capture_health_events.dropped_bytes, EXCLUDED.dropped_bytes),
  spool_used_bytes_peak = GREATEST(capture_health_events.spool_used_bytes_peak, EXCLUDED.spool_used_bytes_peak),
  ready_records_peak = GREATEST(capture_health_events.ready_records_peak, EXCLUDED.ready_records_peak),
  oldest_ready_age_seconds_peak = GREATEST(capture_health_events.oldest_ready_age_seconds_peak, EXCLUDED.oldest_ready_age_seconds_peak),
  upload_retries = GREATEST(capture_health_events.upload_retries, EXCLUDED.upload_retries),
  sidecar_restarts = GREATEST(capture_health_events.sidecar_restarts, EXCLUDED.sidecar_restarts),
  last_error = CASE WHEN EXCLUDED.last_error <> '' THEN EXCLUDED.last_error ELSE capture_health_events.last_error END,
  updated_at = NOW()`

const captureHealthListSQL = `
SELECT minute_bucket, instance_id, reason, dropped_records, dropped_bytes,
       spool_used_bytes_peak, ready_records_peak, oldest_ready_age_seconds_peak,
       upload_retries, sidecar_restarts, last_error
FROM capture_health_events
WHERE minute_bucket >= $1 AND minute_bucket < $2
ORDER BY minute_bucket DESC, id DESC`

const captureHealthListLatestBeforeSQL = `
SELECT latest.minute_bucket, latest.instance_id, latest.reason,
       latest.dropped_records, latest.dropped_bytes,
       latest.spool_used_bytes_peak, latest.ready_records_peak,
       latest.oldest_ready_age_seconds_peak, latest.upload_retries,
       latest.sidecar_restarts, latest.last_error
FROM unnest($2::text[]) AS requested_source(instance_id)
CROSS JOIN unnest($3::text[]) AS requested_reason(reason)
CROSS JOIN LATERAL (
  SELECT event.minute_bucket, event.instance_id, event.reason,
         event.dropped_records, event.dropped_bytes,
         event.spool_used_bytes_peak, event.ready_records_peak,
         event.oldest_ready_age_seconds_peak, event.upload_retries,
         event.sidecar_restarts, event.last_error
  FROM capture_health_events AS event
  WHERE event.instance_id = requested_source.instance_id
    AND event.reason = requested_reason.reason
    AND event.minute_bucket < $1
  ORDER BY event.minute_bucket DESC, event.id DESC
  LIMIT 1
) AS latest
ORDER BY requested_source.instance_id, requested_reason.reason`

const (
	captureHealthLatestSourceLimit = 256
	captureHealthLatestReasonLimit = 7
)

const captureHealthDeleteSQL = `DELETE FROM capture_health_events WHERE minute_bucket < $1`

type captureHealthRepository struct {
	db *sql.DB
}

func NewCaptureHealthRepository(db *sql.DB) service.CaptureHealthRepository {
	return &captureHealthRepository{db: db}
}

func (r *captureHealthRepository) UpsertEvents(ctx context.Context, events []service.CaptureHealthEvent) (err error) {
	if r == nil || r.db == nil {
		return errors.New("nil capture health repository")
	}
	if len(events) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin capture health upsert: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, captureHealthUpsertSQL)
	if err != nil {
		return fmt.Errorf("prepare capture health upsert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, event := range events {
		if _, err = stmt.ExecContext(
			ctx,
			event.MinuteBucket.UTC(),
			event.InstanceID,
			event.Reason,
			event.DroppedRecords,
			event.DroppedBytes,
			event.SpoolUsedBytesPeak,
			event.ReadyRecordsPeak,
			event.OldestReadyAgeSecondsPeak,
			event.UploadRetries,
			event.SidecarRestarts,
			event.LastError,
		); err != nil {
			return fmt.Errorf("upsert capture health event: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit capture health upsert: %w", err)
	}
	return nil
}

func (r *captureHealthRepository) ListEvents(ctx context.Context, start, end time.Time) ([]service.CaptureHealthEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("nil capture health repository")
	}
	rows, err := r.db.QueryContext(ctx, captureHealthListSQL, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("list capture health events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]service.CaptureHealthEvent, 0)
	for rows.Next() {
		var event service.CaptureHealthEvent
		if err := rows.Scan(
			&event.MinuteBucket,
			&event.InstanceID,
			&event.Reason,
			&event.DroppedRecords,
			&event.DroppedBytes,
			&event.SpoolUsedBytesPeak,
			&event.ReadyRecordsPeak,
			&event.OldestReadyAgeSecondsPeak,
			&event.UploadRetries,
			&event.SidecarRestarts,
			&event.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan capture health event: %w", err)
		}
		event.MinuteBucket = event.MinuteBucket.UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capture health events: %w", err)
	}
	return events, nil
}

func (r *captureHealthRepository) ListLatestEventsBefore(ctx context.Context, before time.Time, instanceIDs, reasons []string) ([]service.CaptureHealthEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("nil capture health repository")
	}
	if len(instanceIDs) == 0 || len(reasons) == 0 {
		return nil, nil
	}
	if len(instanceIDs) > captureHealthLatestSourceLimit || len(reasons) > captureHealthLatestReasonLimit {
		return nil, errors.New("capture health latest query exceeds bounded inputs")
	}
	rows, err := r.db.QueryContext(ctx, captureHealthListLatestBeforeSQL, before.UTC(), pq.Array(instanceIDs), pq.Array(reasons))
	if err != nil {
		return nil, fmt.Errorf("list latest capture health events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events, err := scanCaptureHealthEvents(rows)
	if err != nil {
		return nil, err
	}
	return events, nil
}

type captureHealthRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanCaptureHealthEvents(rows captureHealthRows) ([]service.CaptureHealthEvent, error) {
	events := make([]service.CaptureHealthEvent, 0)
	for rows.Next() {
		var event service.CaptureHealthEvent
		if err := rows.Scan(
			&event.MinuteBucket,
			&event.InstanceID,
			&event.Reason,
			&event.DroppedRecords,
			&event.DroppedBytes,
			&event.SpoolUsedBytesPeak,
			&event.ReadyRecordsPeak,
			&event.OldestReadyAgeSecondsPeak,
			&event.UploadRetries,
			&event.SidecarRestarts,
			&event.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan capture health event: %w", err)
		}
		event.MinuteBucket = event.MinuteBucket.UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capture health events: %w", err)
	}
	return events, nil
}

func (r *captureHealthRepository) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("nil capture health repository")
	}
	result, err := r.db.ExecContext(ctx, captureHealthDeleteSQL, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete capture health events: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("capture health deleted rows: %w", err)
	}
	return deleted, nil
}
