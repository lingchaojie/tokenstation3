package migrations

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReasoningMigrationRunsAfterLegacyColumnAndExcludesOfflineWithdrawal(t *testing.T) {
	files, err := fs.Glob(FS, "*.sql")
	require.NoError(t, err)
	require.Contains(t, files, "240_channel_max_reasoning_effort_multiplier.sql")
	require.Contains(t, files, "244_content_moderation_engine_meta.sql")
	require.Contains(t, files, "245_channel_reasoning_effort_multipliers.sql")
	for _, name := range files {
		require.NotEqual(t, "239_channel_reasoning_effort_multipliers.sql", name)
		require.NotEqual(t, "238b_content_moderation_engine_meta.sql", name)
		require.NotEqual(t, "240_affiliate_ledger_operation_id.sql", name)
	}
}

func TestChannelReasoningEffortMultipliersMigration(t *testing.T) {
	content, err := FS.ReadFile("245_channel_reasoning_effort_multipliers.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE channel_model_pricing ADD COLUMN IF NOT EXISTS reasoning_effort_multipliers JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "ALTER TABLE channel_account_stats_model_pricing ADD COLUMN IF NOT EXISTS reasoning_effort_multipliers JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "WHERE max_reasoning_effort_multiplier IS NOT NULL")
	require.Contains(t, sql, "jsonb_build_object('max', max_reasoning_effort_multiplier)")
	require.Contains(t, sql, "attrelid = 'channel_model_pricing'::regclass AND attname = 'reasoning_effort_multipliers' AND NOT attisdropped")
	require.Less(t, strings.Index(sql, "UPDATE channel_model_pricing"), strings.Index(sql, "END IF;"))
	require.Contains(t, sql, "UPDATE groups AS g SET model_pricing")
	require.Contains(t, sql, "NOT (entry ? 'reasoning_effort_multipliers')")
	require.Contains(t, sql, "entry - 'max_reasoning_effort_multiplier'")
	require.Contains(t, sql, "END ORDER BY ordinal")
	require.NotContains(t, strings.ToLower(sql), "fable")
}
