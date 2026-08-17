//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var upstreamSyncMigrationFilenames = []string{
	"191_capture_health_events.sql",
	"192_capture_ops_alert_rules.sql",
	"193_usage_log_image_input_tokens.sql",
	"194_audit_logs.sql",
	"195_group_duplicate_operation_id.sql",
	"198_ops_ingress_reject_aggregates.sql",
	"199_auth_cache_invalidation_outbox.sql",
	"200_group_reasoning_effort_policy.sql",
	"202_group_auth_cache_image_generation.sql",
	"203_add_usage_log_session_id.sql",
	"204_allow_live_usage_request_type.sql",
	"205_add_group_allow_live.sql",
	"206_add_users_email_alias_dedup_index_notx.sql",
	"207_passkey_credentials.sql",
	"208_group_profit_control.sql",
	"209_group_profit_control_auth_cache_invalidation.sql",
	"210_add_usage_log_upstream_response_model.sql",
	"211_channel_monitor_v2.sql",
	"212_add_usage_log_upstream_model_mismatch_index_notx.sql",
	"213_channel_monitor_mode.sql",
	"214_channel_monitor_v2_ignored_error_categories.sql",
	"215_channel_monitor_v2_seed_popular_models.sql",
	"216_channel_monitor_v2_health_thresholds.sql",
	"217_channel_monitor_v2_fixed_rollups.sql",
	"218_channel_monitor_v2_rollup_permissions.sql",
	"219_channel_monitor_v2_refresh_5m.sql",
	"220_channel_monitor_v2_full_table_permissions.sql",
	"221_channel_monitor_v2_default_ignore_and_cache.sql",
	"222_channel_monitor_hide_throughput.sql",
	"223_channel_monitor_v2_reset_factory_cache_thresholds.sql",
	"224_channel_monitor_v2_privacy_defaults.sql",
	"225_group_video_model_prices.sql",
	"228_clear_non_grok_video_generation_config.sql",
	"229_capture_spool_alert_rules.sql",
}

func TestUpstreamSyncMigrationSequenceStartsAfterLocal190(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)

	var got []string
	for _, name := range files {
		if name >= "191_" {
			got = append(got, name)
		}
	}
	sort.Strings(got)

	require.Equal(t, upstreamSyncMigrationFilenames, got)
}

func TestUpstreamSyncMigrationSequenceIntentionallyLeavesPromptAuditGap(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	for _, name := range files {
		require.False(t, strings.HasPrefix(name, "196_"), name)
		require.False(t, strings.HasPrefix(name, "197_"), name)
	}
	require.Contains(t, files, "195_group_duplicate_operation_id.sql")
	require.Contains(t, files, "198_ops_ingress_reject_aggregates.sql")
}

func TestUpstreamSyncMigrationSequenceIncludesCaptureHealthAndOpsRules(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	require.Contains(t, files, "191_capture_health_events.sql")
	require.Contains(t, files, "192_capture_ops_alert_rules.sql")
	require.Contains(t, files, "193_usage_log_image_input_tokens.sql")
}

func TestUpstreamSyncMigrationSequencePreservesNonTransactionalFiles(t *testing.T) {
	for _, name := range []string{
		"206_add_users_email_alias_dedup_index_notx.sql",
		"212_add_usage_log_upstream_model_mismatch_index_notx.sql",
	} {
		content, err := fs.ReadFile(migrations.FS, name)
		require.NoError(t, err, name)

		nonTransactional, err := validateMigrationExecutionMode(name, string(content))
		require.NoError(t, err, name)
		require.True(t, nonTransactional, name)
	}
}

func TestUpstreamSyncMigrationRunnerUsesRemappedFilenames(t *testing.T) {
	require.Equal(t, "182_add_usage_logs_api_key_latest_ip_index_notx.sql", latestAPIKeyIPIndexMigration)
	require.Equal(t, "212_add_usage_log_upstream_model_mismatch_index_notx.sql", usageLogsUpstreamModelMismatchIndexMigration)
	for _, name := range []string{
		"213_channel_monitor_mode.sql",
		"228_clear_non_grok_video_generation_config.sql",
	} {
		content, err := fs.ReadFile(migrations.FS, name)
		require.NoError(t, err, name)
		sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
		currentChecksum := hex.EncodeToString(sum[:])

		rule, ok := migrationChecksumCompatibilityRules[name]
		require.True(t, ok, name)
		require.Equal(t, currentChecksum, rule.fileChecksum, name)
		for historicalChecksum := range rule.acceptedDBChecksum {
			require.True(t, isMigrationChecksumCompatible(name, historicalChecksum, currentChecksum), name)
		}
	}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs(usageLogsUpstreamModelMismatchIndex).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	require.NoError(t, prepareNonTransactionalMigration(
		context.Background(),
		db,
		"212_add_usage_log_upstream_model_mismatch_index_notx.sql",
	))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSyncMigration220Policy(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("migration_policy_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	_, err = tx.ExecContext(ctx, `
SET LOCAL search_path TO pg_temp, public;
CREATE TEMP TABLE groups (
    id BIGINT PRIMARY KEY,
    platform TEXT,
    video_price_480p NUMERIC,
    video_price_720p NUMERIC,
    video_price_1080p NUMERIC,
    video_model_prices JSONB
);
INSERT INTO groups (id, platform, video_price_480p, video_model_prices) VALUES
    (1, 'openai', 1.25, '{"model": 2.5}'),
    (2, 'grok', 2.25, '{"model": 3.5}'),
    (3, 'composite', 3.25, '{"model": 4.5}');
`)
	require.NoError(t, err)

	content, err := fs.ReadFile(migrations.FS, "228_clear_non_grok_video_generation_config.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err)

	var backupPrice string
	var backupModels string
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT video_price_480p::text, video_model_prices::text
FROM groups_video_price_backup_220
WHERE group_id = 1
`).Scan(&backupPrice, &backupModels))
	require.Equal(t, "1.25", backupPrice)
	require.JSONEq(t, `{"model": 2.5}`, backupModels)
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT video_price_480p::text, video_model_prices::text
FROM groups_video_price_backup_220
WHERE group_id = 3
`).Scan(&backupPrice, &backupModels))
	require.Equal(t, "3.25", backupPrice)
	require.JSONEq(t, `{"model": 4.5}`, backupModels)

	rows, err := tx.QueryContext(ctx, `
SELECT id, video_price_480p::text, video_model_prices::text
FROM groups
ORDER BY id
`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	type groupPricing struct {
		id     int64
		price  *string
		models *string
	}
	var got []groupPricing
	for rows.Next() {
		var row groupPricing
		require.NoError(t, rows.Scan(&row.id, &row.price, &row.models))
		got = append(got, row)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []groupPricing{
		{id: 1},
		{id: 2, price: stringPointer("2.25"), models: stringPointer(`{"model": 3.5}`)},
		{id: 3},
	}, got)
}

func stringPointer(value string) *string {
	return &value
}
