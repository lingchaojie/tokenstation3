//go:build integration

package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	appmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestCaptureSpoolMigrationAppliesSchemaRulesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))

	wantColumns := []string{
		"oldest_ready_age_seconds_peak",
		"ready_records_peak",
		"sidecar_restarts",
		"spool_used_bytes_peak",
		"upload_retries",
	}
	require.Equal(t, wantColumns, queryCaptureSpoolHealthColumns(t))
	for _, column := range wantColumns {
		assertCaptureSpoolColumn(t, column)
	}
	for _, retained := range []string{"worker_queue_peak", "writer_queue_peak", "in_flight_bytes_peak"} {
		require.Truef(t, captureHealthColumnExists(t, retained), "migration must retain historical column %s", retained)
	}
	assertCaptureLatestOffsetIndex(t)

	require.Equal(t, []float64{70, 85, 95}, queryEnabledCaptureRuleThresholds(t, "capture_spool_usage_percent"))
	require.Equal(t, []string{">=", ">=", ">="}, queryEnabledCaptureRuleOperators(t, "capture_spool_usage_percent"))
	require.Equal(t, 1, queryEnabledCaptureRuleCount(t, "capture_delivery_ready"))
	require.Equal(t, 1, queryEnabledCaptureRuleShapeCount(t, "capture_delivery_ready", "<", 1))
	require.Equal(t, 0, queryEnabledCaptureRuleCount(t, "capture_writer_failures"))
	var readyDescription string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT description FROM ops_alert_rules WHERE metric_type = 'capture_ready' ORDER BY id LIMIT 1`).Scan(&readyDescription))
	require.Contains(t, strings.ToLower(readyDescription), "sidecar")
	require.Contains(t, strings.ToLower(readyDescription), "spool")

	var writerRuleID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT id FROM ops_alert_rules WHERE metric_type = 'capture_writer_failures' ORDER BY id LIMIT 1`).Scan(&writerRuleID))
	var resolvedEventID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO ops_alert_events (rule_id, severity, status, title, description)
VALUES ($1, 'P0', 'resolved', 'task11 history preservation', 'must survive migration replay')
RETURNING id`, writerRuleID).Scan(&resolvedEventID))
	var firingEventID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO ops_alert_events (rule_id, severity, status, title, description)
VALUES ($1, 'P0', 'firing', 'task11 stale writer event', 'must be resolved, not deleted')
RETURNING id`, writerRuleID).Scan(&firingEventID))
	customReadyName := "task11 custom capture readiness " + uuid.NewString()
	customReadyDescription := "administrator-owned description must remain unchanged"
	var customReadyRuleID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO ops_alert_rules (
  name, description, enabled, metric_type, operator, threshold,
  window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes
) VALUES ($1, $2, true, 'capture_ready', '<', 1, 1, 1, 'P2', false, 60)
RETURNING id`, customReadyName, customReadyDescription).Scan(&customReadyRuleID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM ops_alert_events WHERE id IN ($1, $2)`, resolvedEventID, firingEventID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM ops_alert_rules WHERE id = $1`, customReadyRuleID)
	})

	migrationSQL, err := appmigrations.FS.ReadFile("229_capture_spool_alert_rules.sql")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
CREATE TABLE task11_constraint_collision (value BIGINT NOT NULL DEFAULT 0);
ALTER TABLE task11_constraint_collision
  ADD CONSTRAINT capture_health_events_upload_retries_nonnegative CHECK (value >= 0);
ALTER TABLE capture_health_events
  DROP CONSTRAINT capture_health_events_upload_retries_nonnegative`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DROP TABLE IF EXISTS task11_constraint_collision`)
	})
	_, err = integrationDB.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	assertCaptureSpoolColumn(t, "upload_retries")
	assertCaptureLatestOffsetIndex(t)

	require.Equal(t, []float64{70, 85, 95}, queryEnabledCaptureRuleThresholds(t, "capture_spool_usage_percent"))
	require.Equal(t, []string{">=", ">=", ">="}, queryEnabledCaptureRuleOperators(t, "capture_spool_usage_percent"))
	require.Equal(t, 1, queryEnabledCaptureRuleCount(t, "capture_delivery_ready"))
	require.Equal(t, 1, queryEnabledCaptureRuleShapeCount(t, "capture_delivery_ready", "<", 1))
	require.Equal(t, 0, queryEnabledCaptureRuleCount(t, "capture_writer_failures"))
	var resolvedStatus string
	var resolvedAt *time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT status, resolved_at FROM ops_alert_events WHERE id = $1`, firingEventID).Scan(&resolvedStatus, &resolvedAt))
	require.Equal(t, "resolved", resolvedStatus)
	require.NotNil(t, resolvedAt)
	var historyCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM ops_alert_events WHERE id IN ($1, $2)`, resolvedEventID, firingEventID).Scan(&historyCount))
	require.Equal(t, 2, historyCount)
	var gotCustomDescription string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT description FROM ops_alert_rules WHERE id = $1`, customReadyRuleID).Scan(&gotCustomDescription))
	require.Equal(t, customReadyDescription, gotCustomDescription)
	var appliedCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM schema_migrations WHERE filename = '229_capture_spool_alert_rules.sql'`).Scan(&appliedCount))
	require.Equal(t, 1, appliedCount)
}

func TestCaptureHealthLatestOffsetQueryUsesSourceOrderedIndex(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	sourceID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM capture_health_events WHERE instance_id = $1`, sourceID)
	})
	_, err := integrationDB.ExecContext(ctx, `
INSERT INTO capture_health_events (minute_bucket, instance_id, reason, dropped_records)
SELECT NOW() - make_interval(mins => n), $1,
       CASE WHEN n % 2 = 0 THEN 'ipc_unavailable' ELSE 'pre_commit_disconnect' END,
       n
FROM generate_series(1, 120) AS n`, sourceID)
	require.NoError(t, err)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`)
	require.NoError(t, err)
	rows, err := tx.QueryContext(ctx, `
EXPLAIN (COSTS OFF)
SELECT latest.minute_bucket, latest.instance_id, latest.reason, latest.dropped_records
FROM unnest($2::text[]) AS requested_source(instance_id)
CROSS JOIN unnest($3::text[]) AS requested_reason(reason)
CROSS JOIN LATERAL (
  SELECT event.minute_bucket, event.instance_id, event.reason, event.dropped_records
  FROM capture_health_events AS event
  WHERE event.instance_id = requested_source.instance_id
    AND event.reason = requested_reason.reason
    AND event.minute_bucket < $1
  ORDER BY event.minute_bucket DESC, event.id DESC
  LIMIT 1
) AS latest`, time.Now().UTC().Add(time.Minute), pq.Array([]string{sourceID}), pq.Array([]string{
		"ipc_unavailable", "pre_commit_disconnect",
	}))
	require.NoError(t, err)
	defer rows.Close()
	var planLines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		planLines = append(planLines, line)
	}
	require.NoError(t, rows.Err())
	plan := strings.Join(planLines, "\n")
	require.Contains(t, plan, "idx_capture_health_events_source_reason_latest")
	require.NotContains(t, plan, "Sort", "latest lookup must use bounded index seeks rather than sorting source history")
}

func assertCaptureLatestOffsetIndex(t *testing.T) {
	t.Helper()
	var definition string
	require.NoError(t, integrationDB.QueryRow(`
SELECT indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename = 'capture_health_events'
  AND indexname = 'idx_capture_health_events_source_reason_latest'`).Scan(&definition))
	require.Contains(t, definition, "(instance_id, reason, minute_bucket DESC, id DESC)")
}

func TestCaptureHealthRepositoryRepeatedStatusPollIsIdempotent(t *testing.T) {
	ctx := context.Background()
	sourceID := uuid.NewString()
	minute := time.Date(2026, 8, 17, 2, 3, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM capture_health_events WHERE instance_id = $1`, sourceID)
	})
	event := service.CaptureHealthEvent{
		MinuteBucket: minute, InstanceID: sourceID, Reason: "spool_cap",
		DroppedRecords: 3, DroppedBytes: 4096, SpoolUsedBytesPeak: 9 << 30,
		ReadyRecordsPeak: 42, OldestReadyAgeSecondsPeak: 91, UploadRetries: 8, SidecarRestarts: 2,
	}
	repo := NewCaptureHealthRepository(integrationDB)
	require.NoError(t, repo.UpsertEvents(ctx, []service.CaptureHealthEvent{event}))
	require.NoError(t, repo.UpsertEvents(ctx, []service.CaptureHealthEvent{event}))

	var droppedRecords, droppedBytes, spoolBytes, readyRecords, oldestAge, retries, restarts int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT dropped_records, dropped_bytes, spool_used_bytes_peak, ready_records_peak,
       oldest_ready_age_seconds_peak, upload_retries, sidecar_restarts
FROM capture_health_events
WHERE minute_bucket = $1 AND instance_id = $2 AND reason = $3`, minute, sourceID, event.Reason).Scan(
		&droppedRecords, &droppedBytes, &spoolBytes, &readyRecords, &oldestAge, &retries, &restarts,
	))
	require.Equal(t, []int64{3, 4096, 9 << 30, 42, 91, 8, 2}, []int64{
		droppedRecords, droppedBytes, spoolBytes, readyRecords, oldestAge, retries, restarts,
	})
	latest, err := repo.ListLatestEventsBefore(ctx, minute.Add(24*time.Hour), []string{sourceID}, []string{"spool_cap"})
	require.NoError(t, err)
	require.Len(t, latest, 1)
	require.Equal(t, event, latest[0])
}

func queryCaptureSpoolHealthColumns(t *testing.T) []string {
	t.Helper()
	rows, err := integrationDB.Query(`
SELECT column_name
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'capture_health_events'
  AND column_name IN ('spool_used_bytes_peak', 'ready_records_peak', 'oldest_ready_age_seconds_peak', 'upload_retries', 'sidecar_restarts')`)
	require.NoError(t, err)
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		columns = append(columns, column)
	}
	require.NoError(t, rows.Err())
	sort.Strings(columns)
	return columns
}

func assertCaptureSpoolColumn(t *testing.T, column string) {
	t.Helper()
	var dataType, nullable, defaultValue string
	require.NoError(t, integrationDB.QueryRow(`
SELECT data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'capture_health_events' AND column_name = $1`, column).
		Scan(&dataType, &nullable, &defaultValue))
	require.Equal(t, "bigint", dataType)
	require.Equal(t, "NO", nullable)
	require.Equal(t, "0", defaultValue)

	var checks int
	require.NoError(t, integrationDB.QueryRow(`
SELECT COUNT(*)
FROM pg_constraint c
JOIN pg_class rel ON rel.oid = c.conrelid
JOIN pg_namespace n ON n.oid = rel.relnamespace
WHERE n.nspname = 'public' AND rel.relname = 'capture_health_events' AND c.contype = 'c'
  AND pg_get_constraintdef(c.oid) ILIKE $1`, fmt.Sprintf("%%%s >= 0%%", column)).Scan(&checks))
	require.Equalf(t, 1, checks, "%s must have one nonnegative check", column)
}

func captureHealthColumnExists(t *testing.T, column string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, integrationDB.QueryRow(`
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_schema = 'public' AND table_name = 'capture_health_events' AND column_name = $1
)`, column).Scan(&exists))
	return exists
}

func queryEnabledCaptureRuleThresholds(t *testing.T, metric string) []float64 {
	t.Helper()
	rows, err := integrationDB.Query(`
SELECT threshold FROM ops_alert_rules WHERE metric_type = $1 AND enabled = true ORDER BY threshold`, metric)
	require.NoError(t, err)
	defer rows.Close()
	var thresholds []float64
	for rows.Next() {
		var threshold float64
		require.NoError(t, rows.Scan(&threshold))
		thresholds = append(thresholds, threshold)
	}
	require.NoError(t, rows.Err())
	return thresholds
}

func queryEnabledCaptureRuleCount(t *testing.T, metric string) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRow(`
SELECT COUNT(*) FROM ops_alert_rules WHERE metric_type = $1 AND enabled = true`, metric).Scan(&count))
	return count
}

func queryEnabledCaptureRuleOperators(t *testing.T, metric string) []string {
	t.Helper()
	rows, err := integrationDB.Query(`
SELECT operator FROM ops_alert_rules WHERE metric_type = $1 AND enabled = true ORDER BY threshold`, metric)
	require.NoError(t, err)
	defer rows.Close()
	var operators []string
	for rows.Next() {
		var operator string
		require.NoError(t, rows.Scan(&operator))
		operators = append(operators, operator)
	}
	require.NoError(t, rows.Err())
	return operators
}

func queryEnabledCaptureRuleShapeCount(t *testing.T, metric, operator string, threshold float64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRow(`
SELECT COUNT(*) FROM ops_alert_rules
WHERE metric_type = $1 AND enabled = true AND operator = $2 AND threshold = $3`, metric, operator, threshold).Scan(&count))
	return count
}
