//go:build integration

package repository

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var task3ApprovedUpgradeMigrations = []string{
	"231_add_usage_log_native_compaction_v2.sql",
	"232_add_usage_log_requested_reasoning_effort.sql",
	"233_user_restrict_public_groups.sql",
}

func TestTask3MigrationsUpgradePersisted230Fixture(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("task3_upgrade_test"),
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

	// Establish a real persisted pre-upgrade database by running the same
	// migration engine over the embedded migration set through local version 230.
	require.NoError(t, applyMigrationsFS(ctx, db, task3MigrationsThrough230(t)))
	task3RequirePersisted230Boundary(t, db)

	fixture := task3InsertPersisted230Fixture(t, db)
	task3InstallMigrationOrderAudit(t, db)

	// Apply the complete current embedded migration set through the production
	// entrypoint. The audit trigger records the runner's actual insertion order.
	require.NoError(t, ApplyMigrations(ctx, db))
	task3RequireApprovedUpgradeState(t, db, fixture)

	// Startup reapplication must skip all three migrations without adding an
	// audit row or mutating any persisted fixture row.
	require.NoError(t, ApplyMigrations(ctx, db))
	task3RequireApprovedUpgradeState(t, db, fixture)
}

type task3Persisted230Fixture struct {
	defaultGroupID          int64
	defaultGroupDescription sql.NullString
	userID                  int64
	apiKeyID                int64
	accountID               int64
	usageLogID              int64
	email                   string
	apiKey                  string
}

type task3ColumnMetadata struct {
	dataType   string
	nullable   string
	maxLength  sql.NullInt64
	defaultSQL sql.NullString
}

func task3MigrationsThrough230(t *testing.T) fs.FS {
	t.Helper()

	names, err := fs.Glob(dbmigrations.FS, "*.sql")
	require.NoError(t, err)
	filtered := fstest.MapFS{}
	for _, name := range names {
		if name >= "231_" {
			continue
		}
		content, readErr := fs.ReadFile(dbmigrations.FS, name)
		require.NoError(t, readErr, name)
		filtered[name] = &fstest.MapFile{Data: content, Mode: 0o444}
	}
	require.NotEmpty(t, filtered)
	return filtered
}

func task3RequirePersisted230Boundary(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()

	var newest string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT filename
FROM schema_migrations
ORDER BY filename DESC
LIMIT 1`).Scan(&newest))
	require.Equal(t, "230_channel_image_input_price.sql", newest)

	for _, target := range []struct {
		table  string
		column string
	}{
		{table: "usage_logs", column: "native_compaction_v2"},
		{table: "usage_logs", column: "requested_reasoning_effort"},
		{table: "users", column: "restrict_public_groups"},
	} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM information_schema.columns
	WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
)`, target.table, target.column).Scan(&exists))
		require.Falsef(t, exists, "pre-231 fixture unexpectedly contains %s.%s", target.table, target.column)
	}
}

func task3InsertPersisted230Fixture(t *testing.T, db *sql.DB) task3Persisted230Fixture {
	t.Helper()
	ctx := context.Background()
	fixture := task3Persisted230Fixture{
		email:  "task3-upgrade-sentinel@example.invalid",
		apiKey: "sk-task3-upgrade-sentinel",
	}

	require.NoError(t, db.QueryRowContext(ctx, `
UPDATE groups
SET description = 'task3-upgrade-default-group-sentinel'
WHERE name = 'default'
RETURNING id, description`).Scan(&fixture.defaultGroupID, &fixture.defaultGroupDescription))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status, balance, concurrency)
VALUES ($1, 'task3-sentinel-hash', 'user', 'active', 17.25, 9)
RETURNING id`, fixture.email).Scan(&fixture.userID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO api_keys (user_id, key, name, group_id, status)
VALUES ($1, $2, 'task3-upgrade-sentinel', $3, 'active')
RETURNING id`, fixture.userID, fixture.apiKey, fixture.defaultGroupID).Scan(&fixture.apiKeyID))
	_, err := db.ExecContext(ctx, `
INSERT INTO user_allowed_groups (user_id, group_id)
VALUES ($1, $2)`, fixture.userID, fixture.defaultGroupID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra, status)
VALUES ('task3-upgrade-sentinel', 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb, 'active')
RETURNING id`).Scan(&fixture.accountID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO usage_logs (user_id, api_key_id, account_id, request_id, model)
VALUES ($1, $2, $3, 'task3-upgrade-sentinel', 'task3-model')
RETURNING id`, fixture.userID, fixture.apiKeyID, fixture.accountID).Scan(&fixture.usageLogID))

	return fixture
}

func task3InstallMigrationOrderAudit(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE task3_migration_apply_audit (
	sequence BIGSERIAL PRIMARY KEY,
	filename TEXT NOT NULL
);
CREATE FUNCTION task3_record_migration_apply() RETURNS TRIGGER AS $$
BEGIN
	IF NEW.filename >= '231_' THEN
		INSERT INTO task3_migration_apply_audit (filename) VALUES (NEW.filename);
	END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER task3_record_migration_apply
AFTER INSERT ON schema_migrations
FOR EACH ROW EXECUTE FUNCTION task3_record_migration_apply();`)
	require.NoError(t, err)
}

func task3RequireApprovedUpgradeState(t *testing.T, db *sql.DB, fixture task3Persisted230Fixture) {
	t.Helper()
	ctx := context.Background()

	native := task3ReadColumnMetadata(t, db, "usage_logs", "native_compaction_v2")
	require.Equal(t, "boolean", native.dataType)
	require.Equal(t, "NO", native.nullable)
	require.False(t, native.maxLength.Valid)
	require.Equal(t, sql.NullString{String: "false", Valid: true}, native.defaultSQL)

	reasoning := task3ReadColumnMetadata(t, db, "usage_logs", "requested_reasoning_effort")
	require.Equal(t, "character varying", reasoning.dataType)
	require.Equal(t, "YES", reasoning.nullable)
	require.Equal(t, sql.NullInt64{Int64: 20, Valid: true}, reasoning.maxLength)
	require.False(t, reasoning.defaultSQL.Valid)
	task3RequireColumnUnindexed(t, db, "usage_logs", "native_compaction_v2")
	task3RequireColumnUnindexed(t, db, "usage_logs", "requested_reasoning_effort")

	restrictPublic := task3ReadColumnMetadata(t, db, "users", "restrict_public_groups")
	require.Equal(t, "boolean", restrictPublic.dataType)
	require.Equal(t, "NO", restrictPublic.nullable)
	require.False(t, restrictPublic.maxLength.Valid)
	require.Equal(t, sql.NullString{String: "false", Valid: true}, restrictPublic.defaultSQL)

	var nativeComment sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT col_description('usage_logs'::regclass, a.attnum)
FROM pg_attribute AS a
WHERE a.attrelid = 'usage_logs'::regclass
  AND a.attname = 'native_compaction_v2'
  AND NOT a.attisdropped`).Scan(&nativeComment))
	require.Equal(t, sql.NullString{
		String: "True only when the request was identified at runtime as native OpenAI remote compaction v2",
		Valid:  true,
	}, nativeComment)

	var gotDefaultGroupDescription sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT description
FROM groups
WHERE id = $1 AND name = 'default'`, fixture.defaultGroupID).Scan(&gotDefaultGroupDescription))
	require.Equal(t, fixture.defaultGroupDescription, gotDefaultGroupDescription)

	var gotEmail string
	var restrictValue bool
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT email, restrict_public_groups
FROM users
WHERE id = $1`, fixture.userID).Scan(&gotEmail, &restrictValue))
	require.Equal(t, fixture.email, gotEmail)
	require.False(t, restrictValue)

	var gotAPIKey string
	var gotAPIKeyUserID, gotAPIKeyGroupID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT key, user_id, group_id
FROM api_keys
WHERE id = $1`, fixture.apiKeyID).Scan(&gotAPIKey, &gotAPIKeyUserID, &gotAPIKeyGroupID))
	require.Equal(t, fixture.apiKey, gotAPIKey)
	require.Equal(t, fixture.userID, gotAPIKeyUserID)
	require.Equal(t, fixture.defaultGroupID, gotAPIKeyGroupID)

	var allowedGroupRows int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM user_allowed_groups
WHERE user_id = $1 AND group_id = $2`, fixture.userID, fixture.defaultGroupID).Scan(&allowedGroupRows))
	require.Equal(t, 1, allowedGroupRows)

	var gotRequestID string
	var nativeValue bool
	var requestedEffort sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT request_id, native_compaction_v2, requested_reasoning_effort
FROM usage_logs
WHERE id = $1`, fixture.usageLogID).Scan(&gotRequestID, &nativeValue, &requestedEffort))
	require.Equal(t, "task3-upgrade-sentinel", gotRequestID)
	require.False(t, nativeValue)
	require.False(t, requestedEffort.Valid)

	var pluginTables int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = 'public'
  AND table_name LIKE 'sub2api_plugin_%'`).Scan(&pluginTables))
	require.Zero(t, pluginTables)

	rows, err := db.QueryContext(ctx, `
SELECT filename
FROM task3_migration_apply_audit
ORDER BY sequence`)
	require.NoError(t, err)
	defer rows.Close()
	var appliedOrder []string
	for rows.Next() {
		var filename string
		require.NoError(t, rows.Scan(&filename))
		appliedOrder = append(appliedOrder, filename)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, task3ApprovedUpgradeMigrations, appliedOrder)

	for _, filename := range task3ApprovedUpgradeMigrations {
		var appliedCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM schema_migrations
WHERE filename = $1`, filename).Scan(&appliedCount))
		require.Equalf(t, 1, appliedCount, "%s must be recorded once", filename)
	}
}

func task3ReadColumnMetadata(t *testing.T, db *sql.DB, table, column string) task3ColumnMetadata {
	t.Helper()
	var metadata task3ColumnMetadata
	require.NoError(t, db.QueryRowContext(context.Background(), `
SELECT data_type, is_nullable, character_maximum_length, column_default
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column,
	).Scan(&metadata.dataType, &metadata.nullable, &metadata.maxLength, &metadata.defaultSQL))
	return metadata
}

func task3RequireColumnUnindexed(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	var indexCount int
	require.NoError(t, db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM pg_indexes
WHERE schemaname = 'public'
  AND tablename = $1
  AND indexdef ILIKE '%' || $2 || '%'`, table, column).Scan(&indexCount))
	require.Zero(t, indexCount, "%s.%s must not have an index", table, column)
}
