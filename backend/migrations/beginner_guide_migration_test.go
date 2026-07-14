package migrations

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBeginnerGuideMigrationBackfillsBeforeDefault(t *testing.T) {
	sqlBytes, err := os.ReadFile("183_add_beginner_guide_state.sql")
	require.NoError(t, err)
	sql := string(sqlBytes)

	addAt := strings.Index(sql, "ADD COLUMN IF NOT EXISTS beginner_guide_prompt_state")
	backfillAt := strings.Index(sql, "SET beginner_guide_prompt_state = 'suppressed'")
	defaultAt := strings.Index(sql, "SET DEFAULT 'eligible'")
	require.Greater(t, addAt, -1)
	require.Greater(t, backfillAt, addAt)
	require.Greater(t, defaultAt, backfillAt)
	require.Contains(t, sql, "beginner_guide_progress JSONB")
	require.Contains(t, sql, "beginner_guide_completed_at TIMESTAMPTZ")
	require.Contains(t, sql, "CHECK (beginner_guide_prompt_state IN ('eligible', 'suppressed', 'completed'))")
}
