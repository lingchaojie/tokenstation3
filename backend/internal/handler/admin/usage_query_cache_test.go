package admin

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageStatsCacheKey_StableAndDistinct(t *testing.T) {
	start := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	base := usagestats.UsageLogFilters{StartTime: &start, EndTime: &end, Model: "claude-3"}

	k1 := usageStatsCacheKey(base)
	k2 := usageStatsCacheKey(base)
	require.NotEmpty(t, k1)
	require.Equal(t, k1, k2, "same filters must produce same key")

	other := base
	other.Model = "gpt-4o"
	require.NotEqual(t, k1, usageStatsCacheKey(other), "different model must change key")

	withUser := base
	withUser.UserID = 7
	require.NotEqual(t, k1, usageStatsCacheKey(withUser), "different user must change key")

	withExcluded := base
	withExcluded.ExcludedUserIDs = []int64{9, 3}
	sameSet := base
	sameSet.ExcludedUserIDs = []int64{3, 9, 3}
	require.NotEqual(t, usageStatsCacheKey(base), usageStatsCacheKey(withExcluded))
	require.Equal(t, usageStatsCacheKey(withExcluded), usageStatsCacheKey(sameSet))
}

func TestUsageStatsCacheKey_PreservesRawModelFilterSource(t *testing.T) {
	raw := usagestats.UsageLogFilters{
		Model:           "claude-opus-4-6",
		ExcludedUserIDs: []int64{9, 3},
	}
	requested := raw
	requested.ModelFilterSource = usagestats.ModelSourceRequested
	requested.ExcludedUserIDs = []int64{3, 9, 3}

	require.NotEqual(t, usageStatsCacheKey(raw), usageStatsCacheKey(requested))
}
