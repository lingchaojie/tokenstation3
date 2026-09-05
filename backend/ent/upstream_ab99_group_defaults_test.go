package ent_test

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/group"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/stretchr/testify/require"
)

// Runtime descriptor indexes must follow the combined schema, including the
// local KIRO fields between the upstream manifest and reasoning-policy fields.
func TestUpstreamAb99GroupDefaultsPreserveKiroAndOptInPolicies(t *testing.T) {
	require.False(t, group.DefaultForceOpenaiFast)
	require.False(t, group.DefaultFreeOpenaiFast)
	require.Empty(t, group.DefaultMaxReasoningEffort)
	require.Equal(t, "downgrade", group.DefaultMaxReasoningEffortOverLimit)
	require.Empty(t, group.DefaultReasoningEffortMappings)
	require.False(t, group.DefaultCodexModelsManifestConfig.Enabled)
	require.Empty(t, group.DefaultCodexModelsManifestConfig.AccountIDs)
	require.False(t, group.DefaultCodexModelsManifestConfig.FallbackToScheduler)
	require.False(t, group.DefaultKiroCacheEmulationEnabled)
	require.True(t, group.DefaultKiroAutoStickyEnabled)
	require.Equal(t, 3600, group.DefaultKiroStickySessionTTLSeconds)
	require.Equal(t, 1.0, group.DefaultKiroCacheEmulationRatio)
	require.Equal(t, "q", group.DefaultKiroEndpointMode)
}
