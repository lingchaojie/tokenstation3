package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursorPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("231_add_cursor_platform.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "'deepseek', 'cursor'")
	require.NotContains(t, sql, "channel_monitors_provider_check")
}
