//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Exercises the actual DDL without a Composite table. A stale platform list
// fails on the existing KIRO row; a non-idempotent migration fails on replay.
func TestUpstreamB8deMigrationUpgradeAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("b8de_upgrade"), tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `
CREATE TABLE payment_orders (id BIGINT PRIMARY KEY, amount DECIMAL(20,2) NOT NULL);
INSERT INTO payment_orders VALUES (1, 100);
CREATE TABLE user_platform_quotas (
    id BIGINT PRIMARY KEY, platform TEXT NOT NULL,
    daily_limit NUMERIC, weekly_limit NUMERIC, monthly_limit NUMERIC,
    CONSTRAINT user_platform_quotas_platform_check CHECK (platform IN ('kiro', 'openai'))
);
INSERT INTO user_platform_quotas (id, platform) VALUES (1, 'kiro');
`)
	require.NoError(t, err)
	for pass := 0; pass < 2; pass++ {
		for _, name := range []string{"246_add_payment_order_bonus_amount.sql", "247_add_typesafe_platform.sql"} {
			ddl, err := FS.ReadFile(name)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, string(ddl))
			require.NoError(t, err, "pass=%d migration=%s", pass, name)
		}
	}
	var oldBonus float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT bonus_amount FROM payment_orders WHERE id=1").Scan(&oldBonus))
	require.Zero(t, oldBonus)
	_, err = db.ExecContext(ctx, "INSERT INTO payment_orders (id, amount, bonus_amount) VALUES (2,110,10)")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "INSERT INTO payment_orders (id,amount,bonus_amount) VALUES (3,100,NULL)")
	require.Error(t, err, "bonus snapshot must remain non-null")
	for i, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "kiro", "grok", "kimi", "zhipu", "deepseek", "minimax", "opencode_go", "typesafe"} {
		_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas(id,platform) VALUES($1,$2)", i+2, platform)
		require.NoError(t, err, platform)
	}
	var nullQuotaRows int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM user_platform_quotas WHERE daily_limit IS NULL AND weekly_limit IS NULL AND monthly_limit IS NULL").Scan(&nullQuotaRows))
	require.Equal(t, 13, nullQuotaRows)
	_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas(id,platform) VALUES (99,'composite')")
	require.Error(t, err, "excluded platform must not become admissible")
}
