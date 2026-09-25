//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupUsageRollupReadSnapshotConcurrentHistoricalInsert(t *testing.T) {
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	for _, protected := range []bool{false, true} {
		name := "upstream_split_reads_reproduce_mixed_snapshot"
		if protected {
			name = "repeatable_read_preserves_original_single_statement_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
			seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			_, err := seed.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id,user_id,group_id,actual_cost,created_at)
				VALUES (1,1,10,2,TIMESTAMPTZ '2026-08-13 12:00:00+08');
				INSERT INTO usage_group_daily_rollups (bucket_date,group_id,actual_cost,computed_at)
				VALUES (DATE '2026-08-13',10,2,NOW());
				UPDATE usage_group_rollup_state SET closed_before=DATE '2026-08-14',
				retained_from=TIMESTAMPTZ '2026-08-13 00:00:00+08',timezone_name='Asia/Shanghai' WHERE id=1;
			`)
			require.NoError(t, err)
			require.NoError(t, seed.Commit())
			conn, err := integrationDB.Conn(ctx)
			require.NoError(t, err)
			defer conn.Close()
			_, err = conn.ExecContext(ctx, "SET search_path TO "+pq.QuoteIdentifier(schema))
			require.NoError(t, err)
			defer func() { _, _ = conn.ExecContext(context.Background(), "RESET search_path") }()
			today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
			read := func(tx *sql.Tx) ([]usagestats.GroupUsageSummary, error) {
				repo := &usageLogRepository{sql: tx}
				state, err := repo.readGroupUsageRollupSnapshot(ctx, service.GroupUsageTimezoneName(), service.GroupUsageDate(today))
				if err != nil {
					return nil, err
				}
				writer := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
				_, err = writer.ExecContext(ctx, `INSERT INTO usage_logs (id,user_id,group_id,actual_cost,created_at) VALUES
					(2,1,10,3,TIMESTAMPTZ '2026-08-13 13:00:00+08'),
					(3,1,10,4,TIMESTAMPTZ '2026-08-14 12:00:00+08')`)
				require.NoError(t, err)
				require.NoError(t, writer.Commit())
				return repo.queryGroupUsageRollupSnapshot(ctx, today, state)
			}
			var result []usagestats.GroupUsageSummary
			if protected {
				result, err = withGroupUsageRollupReadSnapshot(ctx, conn, read)
			} else {
				tx, beginErr := conn.BeginTx(ctx, nil)
				require.NoError(t, beginErr)
				defer tx.Rollback()
				result, err = read(tx)
			}
			require.NoError(t, err)
			require.Len(t, result, 1)
			if protected {
				require.Equal(t, float64(2), result[0].TotalCost)
			} else {
				// Neither the pre-commit total (2) nor the post-commit total (9).
				require.Equal(t, float64(6), result[0].TotalCost)
			}
		})
	}
}
