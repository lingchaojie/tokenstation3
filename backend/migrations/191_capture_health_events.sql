-- Persist minute-level archive loss aggregates so incidents remain visible
-- after an application restart without writing once per dropped record.
CREATE TABLE IF NOT EXISTS capture_health_events (
  id BIGSERIAL PRIMARY KEY,
  minute_bucket TIMESTAMPTZ NOT NULL,
  instance_id VARCHAR(255) NOT NULL,
  reason VARCHAR(64) NOT NULL,
  dropped_records BIGINT NOT NULL DEFAULT 0,
  dropped_bytes BIGINT NOT NULL DEFAULT 0,
  worker_queue_peak BIGINT NOT NULL DEFAULT 0,
  writer_queue_peak BIGINT NOT NULL DEFAULT 0,
  in_flight_bytes_peak BIGINT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (minute_bucket, instance_id, reason)
);

CREATE INDEX IF NOT EXISTS idx_capture_health_events_bucket
  ON capture_health_events (minute_bucket DESC, id DESC);
