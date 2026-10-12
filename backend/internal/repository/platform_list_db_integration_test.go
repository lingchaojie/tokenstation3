//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func platformConstraintExists(t *testing.T, ctx context.Context, client *dbent.Client, table, constraint string) bool {
	t.Helper()
	rows, err := client.QueryContext(ctx, `
SELECT 1
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public' AND tbl.relname = $1 AND c.conname = $2`, table, constraint)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	exists := rows.Next()
	require.NoError(t, rows.Err())
	return exists
}

// 迁移 248 删除平台白名单 CHECK 约束：旧库（约束仍在）上可重复执行，执行后
// 新登记的平台无需再做数据库迁移即可写入。
func TestMigration248DropsPlatformCheckConstraints(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	// 还原本地迁移 247 之后、248 之前的约束状态。
	for _, stmt := range []string{
		`ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check`,
		`ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
			CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'grok',
			                    'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe'))`,
	} {
		_, err := client.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	require.True(t, platformConstraintExists(t, ctx, client, "user_platform_quotas", "user_platform_quotas_platform_check"))

	migrationSQL, err := dbmigrations.FS.ReadFile("248_drop_platform_check_constraints.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = client.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)
	}
	require.False(t, platformConstraintExists(t, ctx, client, "user_platform_quotas", "user_platform_quotas_platform_check"))
	// 渠道监控的 provider 约束表示探测能力，保留。
	require.True(t, platformConstraintExists(t, ctx, client, "channel_monitors", "channel_monitors_provider_check"))
	require.True(t, platformConstraintExists(t, ctx, client, "channel_monitor_request_templates", "channel_monitor_request_templates_provider_check"))

	// 数据库层不再限制平台取值：模拟未来新登记的平台。
	userID := mustCreateUserForQuota(t, client)
	_, err = client.ExecContext(ctx, `
INSERT INTO user_platform_quotas (user_id, platform, daily_limit_usd, daily_usage_usd, weekly_usage_usd, monthly_usage_usd, created_at, updated_at)
VALUES ($1, 'future_platform', 1, 0, 0, 0, NOW(), NOW())`, userID)
	require.NoError(t, err)
}

// 删除 CHECK 约束后，平台合法性由应用层保证：原生 SQL 写入前的 repository 校验。
func TestUserPlatformQuotaRepository_RejectsUnregisteredPlatform(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	userID := mustCreateUserForQuota(t, client)
	repo := NewUserPlatformQuotaRepository(client)
	daily := 5.0
	require.NoError(t, repo.UpsertForUser(txCtx, userID, []UserPlatformQuotaRecord{
		{UserID: userID, Platform: service.PlatformOpenCodeGo, DailyLimitUSD: &daily},
	}))

	for _, platform := range []string{"bogus", "moonshot", "Kimi", "composite"} {
		err := repo.BulkInsertInitial(txCtx, []UserPlatformQuotaRecord{
			{UserID: userID, Platform: service.PlatformAnthropic, DailyLimitUSD: &daily},
			{UserID: userID, Platform: platform, DailyLimitUSD: &daily},
		})
		require.ErrorContains(t, err, "is not allowed", platform)
		err = repo.UpsertForUser(txCtx, userID, []UserPlatformQuotaRecord{
			{UserID: userID, Platform: platform, DailyLimitUSD: &daily},
		})
		require.ErrorContains(t, err, "is not allowed", platform)
		require.ErrorContains(t, repo.IncrementUsageWithReset(txCtx, userID, platform, 1, time.Now()), "is not allowed", platform)
		require.ErrorContains(t, repo.BatchSnapshotUsage(txCtx, []UserPlatformQuotaSnapshot{
			{UserID: userID, Platform: service.PlatformAnthropic},
			{UserID: userID, Platform: platform},
		}, time.Now()), "is not allowed", platform)
	}

	// 被拒绝的写入不产生任何副作用：既有记录保留、未新增任何行。
	got, err := repo.ListByUser(txCtx, userID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, service.PlatformOpenCodeGo, got[0].Platform)

	// Unlimited rows remain explicit configuration and still require a valid platform.
	require.ErrorContains(t, repo.BulkInsertInitial(txCtx, []UserPlatformQuotaRecord{{UserID: userID, Platform: "bogus"}}), "is not allowed")
	require.ErrorContains(t, repo.UpsertForUser(txCtx, userID, []UserPlatformQuotaRecord{{UserID: userID, Platform: "bogus"}}), "is not allowed")
}

func TestUserPlatformQuotaRepository_CatalogSupportsKiroAndNewUnlimitedRows(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	userID := mustCreateUserForQuota(t, client)
	repo := NewUserPlatformQuotaRepository(client)
	records := []UserPlatformQuotaRecord{
		{UserID: userID, Platform: service.PlatformKiro},
		{UserID: userID, Platform: service.PlatformCommandCode},
		{UserID: userID, Platform: service.PlatformCline},
	}
	require.NoError(t, repo.BulkInsertInitial(txCtx, records))
	require.NoError(t, repo.IncrementUsageWithReset(txCtx, userID, service.PlatformKiro, 2, time.Now()))
	require.NoError(t, repo.UpsertForUser(txCtx, userID, records))
	got, err := repo.ListByUser(txCtx, userID)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, record := range got {
		require.Nil(t, record.DailyLimitUSD)
		require.Nil(t, record.WeeklyLimitUSD)
		require.Nil(t, record.MonthlyLimitUSD)
		if record.Platform == service.PlatformKiro {
			require.Equal(t, 2.0, record.DailyUsageUSD)
			require.NotNil(t, record.DailyWindowStart)
		}
	}
}
