package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureOpsAlertRulesMigrationSeedsDefaults(t *testing.T) {
	content, err := FS.ReadFile("192_capture_ops_alert_rules.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, fragment := range []string{
		"true, 'capture_ready', '<', 1.0, 1, 2, 'P0', true, 60",
		"true, 'capture_dropped_records', '>', 0.0, 5, 1, 'P1', true, 60",
		"true, 'capture_writer_failures', '>', 0.0, 5, 1, 'P0', true, 60",
	} {
		require.Contains(t, sql, fragment)
	}
	require.Equal(t, 3, strings.Count(sql, "INSERT INTO ops_alert_rules"))
	require.Equal(t, 3, strings.Count(sql, "ON CONFLICT (name) DO NOTHING"))
}
