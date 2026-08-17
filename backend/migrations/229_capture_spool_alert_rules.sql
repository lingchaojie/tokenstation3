-- Replace retired in-process queue health with durable spool and delivery
-- operations while retaining the old columns and alert history for rollback.

ALTER TABLE capture_health_events
  ADD COLUMN IF NOT EXISTS spool_used_bytes_peak BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS ready_records_peak BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS oldest_ready_age_seconds_peak BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS upload_retries BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS sidecar_restarts BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_capture_health_events_source_reason_latest
  ON capture_health_events (instance_id, reason, minute_bucket DESC, id DESC);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'capture_health_events_spool_used_bytes_peak_nonnegative' AND conrelid = to_regclass('public.capture_health_events')) THEN
    ALTER TABLE capture_health_events ADD CONSTRAINT capture_health_events_spool_used_bytes_peak_nonnegative CHECK (spool_used_bytes_peak >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'capture_health_events_ready_records_peak_nonnegative' AND conrelid = to_regclass('public.capture_health_events')) THEN
    ALTER TABLE capture_health_events ADD CONSTRAINT capture_health_events_ready_records_peak_nonnegative CHECK (ready_records_peak >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'capture_health_events_oldest_ready_age_seconds_peak_nonnegative' AND conrelid = to_regclass('public.capture_health_events')) THEN
    ALTER TABLE capture_health_events ADD CONSTRAINT capture_health_events_oldest_ready_age_seconds_peak_nonnegative CHECK (oldest_ready_age_seconds_peak >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'capture_health_events_upload_retries_nonnegative' AND conrelid = to_regclass('public.capture_health_events')) THEN
    ALTER TABLE capture_health_events ADD CONSTRAINT capture_health_events_upload_retries_nonnegative CHECK (upload_retries >= 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'capture_health_events_sidecar_restarts_nonnegative' AND conrelid = to_regclass('public.capture_health_events')) THEN
    ALTER TABLE capture_health_events ADD CONSTRAINT capture_health_events_sidecar_restarts_nonnegative CHECK (sidecar_restarts >= 0);
  END IF;
END $$;

UPDATE ops_alert_events AS event
SET status = 'resolved',
    resolved_at = COALESCE(event.resolved_at, NOW())
FROM ops_alert_rules AS rule
WHERE event.rule_id = rule.id
  AND rule.metric_type = 'capture_writer_failures'
  AND event.status = 'firing';

UPDATE ops_alert_rules
SET enabled = false, updated_at = NOW()
WHERE metric_type IN ('capture_spool_usage_percent', 'capture_delivery_ready', 'capture_writer_failures');

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES
  ('转存 Spool 使用率达到 70%', 'Capture spool 使用率达到 70%；检查积压增长与远端传输状态。', true, 'capture_spool_usage_percent', '>=', 70.0, 1, 1, 'P2', true, 60, NOW(), NOW()),
  ('转存 Spool 使用率达到 85%', 'Capture spool 使用率达到 85%；尽快恢复远端传输。', true, 'capture_spool_usage_percent', '>=', 85.0, 1, 1, 'P1', true, 30, NOW(), NOW()),
  ('转存 Spool 使用率达到 95%', 'Capture spool 使用率达到 95%；接近硬容量上限，新转存可能丢失。', true, 'capture_spool_usage_percent', '>=', 95.0, 1, 1, 'P0', true, 15, NOW(), NOW())
ON CONFLICT (name) DO UPDATE SET
  description = EXCLUDED.description,
  enabled = EXCLUDED.enabled,
  metric_type = EXCLUDED.metric_type,
  operator = EXCLUDED.operator,
  threshold = EXCLUDED.threshold,
  window_minutes = EXCLUDED.window_minutes,
  sustained_minutes = EXCLUDED.sustained_minutes,
  severity = EXCLUDED.severity,
  notify_email = EXCLUDED.notify_email,
  cooldown_minutes = EXCLUDED.cooldown_minutes,
  updated_at = NOW();

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '转存远端传输不可用',
    'Capture sidecar 本地 Spool 仍可接收数据，但远端 ClickHouse 传输未就绪；积压会自动重试。',
    true, 'capture_delivery_ready', '<', 1.0, 1, 2, 'P1', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO UPDATE SET
  description = EXCLUDED.description,
  enabled = EXCLUDED.enabled,
  metric_type = EXCLUDED.metric_type,
  operator = EXCLUDED.operator,
  threshold = EXCLUDED.threshold,
  window_minutes = EXCLUDED.window_minutes,
  sustained_minutes = EXCLUDED.sustained_minutes,
  severity = EXCLUDED.severity,
  notify_email = EXCLUDED.notify_email,
  cooldown_minutes = EXCLUDED.cooldown_minutes,
  updated_at = NOW();

UPDATE ops_alert_rules
SET description = 'Capture local acceptance readiness: 1 only when the supervised sidecar is running and its local spool is ready; remote delivery is evaluated separately.',
    updated_at = NOW()
WHERE name = '转存基础设施未就绪'
  AND metric_type = 'capture_ready';
