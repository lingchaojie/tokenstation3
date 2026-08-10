package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const captureHealthUpsertSQL = `
INSERT INTO capture_health_events (
  minute_bucket, instance_id, reason, dropped_records, dropped_bytes,
  worker_queue_peak, writer_queue_peak, in_flight_bytes_peak, last_error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (minute_bucket, instance_id, reason) DO UPDATE SET
  dropped_records = capture_health_events.dropped_records + EXCLUDED.dropped_records,
  dropped_bytes = capture_health_events.dropped_bytes + EXCLUDED.dropped_bytes,
  worker_queue_peak = GREATEST(capture_health_events.worker_queue_peak, EXCLUDED.worker_queue_peak),
  writer_queue_peak = GREATEST(capture_health_events.writer_queue_peak, EXCLUDED.writer_queue_peak),
  in_flight_bytes_peak = GREATEST(capture_health_events.in_flight_bytes_peak, EXCLUDED.in_flight_bytes_peak),
  last_error = CASE WHEN EXCLUDED.last_error <> '' THEN EXCLUDED.last_error ELSE capture_health_events.last_error END,
  updated_at = NOW()`

const captureHealthListSQL = `
SELECT minute_bucket, instance_id, reason, dropped_records, dropped_bytes,
       worker_queue_peak, writer_queue_peak, in_flight_bytes_peak, last_error
FROM capture_health_events
WHERE minute_bucket >= $1 AND minute_bucket < $2
ORDER BY minute_bucket DESC, id DESC`

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
			event.WorkerQueuePeak,
			event.WriterQueuePeak,
			event.InFlightBytePeak,
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
			&event.WorkerQueuePeak,
			&event.WriterQueuePeak,
			&event.InFlightBytePeak,
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
