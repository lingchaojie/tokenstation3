//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountKiroConfigReaders(t *testing.T) {
	acc := &Account{Platform: PlatformKiro, Extra: map[string]any{
		"kiro_endpoint_mode":              "krs",
		"kiro_cache_emulation_enabled":    true,
		"kiro_cache_emulation_ratio":      json.Number("0.5"),
		"kiro_auto_sticky_enabled":        true,
		"kiro_sticky_session_ttl_seconds": json.Number("1800"),
	}}
	require.Equal(t, KiroEndpointModeKRS, acc.KiroEndpointMode())
	require.True(t, acc.KiroCacheEmulationEnabled())
	require.Equal(t, 0.5, acc.KiroCacheEmulationRatio())
	require.True(t, acc.KiroAutoStickyEnabled())
	require.Equal(t, 1800, acc.KiroStickySessionTTLSeconds())
}

func TestAccountKiroConfigDefaults(t *testing.T) {
	acc := &Account{Platform: PlatformKiro} // Extra nil
	require.Equal(t, KiroEndpointModeQ, acc.KiroEndpointMode())
	require.False(t, acc.KiroCacheEmulationEnabled())
	require.Equal(t, float64(0), acc.KiroCacheEmulationRatio())
	require.False(t, acc.KiroAutoStickyEnabled())
	require.Equal(t, DefaultKiroStickySessionTTLSeconds, acc.KiroStickySessionTTLSeconds())

	bad := &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_endpoint_mode": "xyz"}}
	require.Equal(t, KiroEndpointModeQ, bad.KiroEndpointMode())
}
