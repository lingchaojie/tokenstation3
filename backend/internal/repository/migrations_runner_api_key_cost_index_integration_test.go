//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestMigration251UpgradesPartitionedUsageLogsFrom248(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase("migration251"), tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, applyMigrationsFS(ctx, db, embeddedMigrationsBefore(t, "249_")))
	// The 248 runner leaves partitioning to the operator (migration 035).
	// Recreate the supported layout after applying the real 248 history.
	_, err = db.ExecContext(ctx, `
		ALTER TABLE usage_logs RENAME TO usage_logs_before_partitioning;
		CREATE TABLE usage_logs (LIKE usage_logs_before_partitioning INCLUDING DEFAULTS)
			PARTITION BY RANGE (created_at);
		CREATE TABLE usage_logs_legacy PARTITION OF usage_logs
			FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
		CREATE TABLE usage_logs_current PARTITION OF usage_logs
			FOR VALUES FROM ('2026-01-01') TO ('2027-01-01') PARTITION BY RANGE (created_at);
		CREATE SCHEMA usage_archive;
		CREATE TABLE usage_archive."logs-2026" PARTITION OF usage_logs_current
			FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
		INSERT INTO usage_logs (user_id, api_key_id, account_id, model, created_at, actual_cost)
			VALUES (11, 11, 11, 'model', '2026-01-02', 0.2),
			       (11, 11, 11, 'model', '2026-01-02', 0.3);
		CREATE INDEX idx_usage_logs_api_key_created_cost
			ON ONLY usage_logs (api_key_id, created_at) INCLUDE (actual_cost);
		CREATE INDEX usage_logs_legacy_covering
			ON usage_logs_legacy (api_key_id, created_at) INCLUDE (actual_cost);
		ALTER INDEX idx_usage_logs_api_key_created_cost ATTACH PARTITION usage_logs_legacy_covering;
	`)
	require.NoError(t, err)

	// Model an interrupted concurrent build in the nested partition by
	// deliberately failing PostgreSQL's build on duplicate fixture rows.
	var archiveOID uint32
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT 'usage_archive."logs-2026"'::regclass::oid
	`).Scan(&archiveOID))
	archiveIndex := fmt.Sprintf("idx_usage_logs_api_key_created_cost_p%d", archiveOID)
	_, err = db.ExecContext(ctx, "CREATE UNIQUE INDEX CONCURRENTLY "+archiveIndex+
		` ON usage_archive."logs-2026" (api_key_id, created_at) INCLUDE (actual_cost)`)
	var buildErr *pq.Error
	require.ErrorAs(t, err, &buildErr)
	require.Equal(t, "23505", string(buildErr.Code))
	var valid bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT indisvalid FROM pg_index WHERE indexrelid = $1::regclass
	`, "usage_archive."+archiveIndex).Scan(&valid))
	require.False(t, valid, "interrupted build must leave an invalid child index")

	for i := 0; i < 2; i++ {
		require.NoError(t, ApplyMigrations(ctx, db), "startup %d", i+1)
	}
	var cost float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT sum(actual_cost) FROM usage_logs WHERE api_key_id = 11").Scan(&cost))
	require.Equal(t, 0.5, cost)
	var legacyIndexes int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_index WHERE indrelid = 'usage_logs_legacy'::regclass
	`).Scan(&legacyIndexes))
	require.Equal(t, 1, legacyIndexes, "keep the already attached index on the legacy partition")
	// New partitions also inherit the indexed covering columns.
	_, err = db.ExecContext(ctx, `
		CREATE TABLE usage_logs_future PARTITION OF usage_logs
			FOR VALUES FROM ('2027-01-01') TO ('2028-01-01');
	`)
	require.NoError(t, err)
	var indexedTables []string
	rows, err := db.QueryContext(ctx, `
		SELECT j.indrelid::regclass::text
		FROM pg_partition_tree('public.idx_usage_logs_api_key_created_cost') p
		JOIN pg_index j ON j.indexrelid = p.relid
		WHERE j.indisvalid AND j.indisready AND j.indnkeyatts = 2 AND j.indnatts = 3
			AND pg_get_indexdef(j.indexrelid, 1, false) = 'api_key_id'
			AND pg_get_indexdef(j.indexrelid, 2, false) = 'created_at'
			AND pg_get_indexdef(j.indexrelid, 3, false) = 'actual_cost'
	`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		indexedTables = append(indexedTables, table)
	}
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, []string{
		"usage_logs", "usage_logs_legacy", "usage_logs_current", `usage_archive."logs-2026"`, "usage_logs_future",
	}, indexedTables)

	t.Run("empty partitioned root resolved through search_path", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = conn.ExecContext(ctx, `
			CREATE SCHEMA empty_partition_layout;
			SET search_path TO empty_partition_layout, public;
			CREATE TABLE empty_partition_layout.usage_logs
				(api_key_id bigint, created_at timestamptz, actual_cost numeric)
				PARTITION BY RANGE (created_at);
		`)
		require.NoError(t, err)
		ddl, err := migrations.FS.ReadFile(usageLogsAPIKeyCostIndexMigration)
		require.NoError(t, err)
		require.NoError(t, applyNonTransactionalMigration(ctx, conn, usageLogsAPIKeyCostIndexMigration, string(ddl)))

		var valid bool
		require.NoError(t, conn.QueryRowContext(ctx, `
			SELECT indisvalid FROM pg_index
			WHERE indexrelid = 'empty_partition_layout.idx_usage_logs_api_key_created_cost'::regclass
		`).Scan(&valid))
		require.True(t, valid)
		_, err = conn.ExecContext(ctx, `
			CREATE TABLE usage_logs_new PARTITION OF usage_logs DEFAULT;
		`)
		require.NoError(t, err)
		var attached int
		require.NoError(t, conn.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_partition_tree('empty_partition_layout.idx_usage_logs_api_key_created_cost')
			WHERE isleaf
		`).Scan(&attached))
		require.Equal(t, 1, attached)
	})
}

func TestMigration251RetriesInvalidIndexOnOrdinaryTableInCurrentSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const schema = "migration_251_ordinary_table"
	execGroupUsageRollupSQL(t, ctx, integrationDB, "CREATE SCHEMA "+schema)
	t.Cleanup(func() { _, _ = integrationDB.Exec("DROP SCHEMA " + schema + " CASCADE") })
	db := openGroupUsageRollupSchemaDB(t, schema, "UTC")
	execGroupUsageRollupSQL(t, ctx, db, atlasSchemaRevisionsTableDDL)
	execGroupUsageRollupSQL(t, ctx, db, `
		CREATE TABLE usage_logs (api_key_id BIGINT, created_at TIMESTAMPTZ, actual_cost NUMERIC);
		INSERT INTO usage_logs VALUES (1, '2026-01-01', 0.2), (1, '2026-01-01', 0.3);
	`)

	// PostgreSQL keeps an invalid index when a concurrent build fails.
	_, err := db.ExecContext(ctx, "CREATE UNIQUE INDEX CONCURRENTLY "+usageLogsAPIKeyCostIndex+
		" ON usage_logs (api_key_id, created_at) INCLUDE (actual_cost)")
	var buildErr *pq.Error
	require.ErrorAs(t, err, &buildErr)
	require.Equal(t, "23505", string(buildErr.Code))

	readIndex := func() (valid, unique bool) {
		t.Helper()
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT indisvalid, indisunique
			FROM pg_index
			WHERE indrelid = to_regclass('usage_logs') AND indexrelid = to_regclass($1)
		`, usageLogsAPIKeyCostIndex).Scan(&valid, &unique))
		return valid, unique
	}
	valid, unique := readIndex()
	require.False(t, valid)
	require.True(t, unique)

	content, err := migrations.FS.ReadFile(usageLogsAPIKeyCostIndexMigration)
	require.NoError(t, err)
	fsys := fstest.MapFS{usageLogsAPIKeyCostIndexMigration: &fstest.MapFile{Data: content}}
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
	valid, unique = readIndex()
	require.True(t, valid)
	require.False(t, unique)
	var applied int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE filename = $1", usageLogsAPIKeyCostIndexMigration).Scan(&applied))
	require.Equal(t, 1, applied)
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
}
