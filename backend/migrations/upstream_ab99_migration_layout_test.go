package migrations

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Applied migrations are keyed by complete filename. The upstream additions
// must follow the deployed local sequence without renaming historical files.
func TestUpstreamAB99MigrationLayout(t *testing.T) {
	newFiles := []string{
		"234_add_usage_log_upstream_request_id.sql",
		"235_channel_cache_write_1h_pricing.sql",
		"236_group_force_openai_fast.sql",
		"237_group_reasoning_effort_over_limit.sql",
		"238_add_usage_log_upstream_request_id_index_notx.sql",
		"239_group_free_openai_fast.sql",
		"240_channel_max_reasoning_effort_multiplier.sql",
		"241_group_codex_models_manifest_config.sql",
	}
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)
	for i, name := range newFiles {
		_, err := FS.ReadFile(name)
		require.NoError(t, err, name)
		var matches []string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), fmt.Sprintf("%03d_", 234+i)) {
				matches = append(matches, entry.Name())
			}
		}
		require.Equal(t, []string{name}, matches)
	}
	column, err := FS.ReadFile(newFiles[0])
	require.NoError(t, err)
	require.Contains(t, string(column), "ADD COLUMN IF NOT EXISTS upstream_request_id VARCHAR(128)")
	index, err := FS.ReadFile(newFiles[4])
	require.NoError(t, err)
	require.Contains(t, string(index), "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id")
	require.Contains(t, string(index), "WHERE upstream_request_id IS NOT NULL")
}
