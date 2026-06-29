//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveKiroEndpointMode(t *testing.T) {
	kiroGroup := &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_endpoint_mode": "krs",
	}}
	anthropicAcct := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	require.Equal(t, KiroEndpointModeKRS, resolveKiroEndpointMode(mixedKiroAcct, kiroGroup))
	require.Equal(t, KiroEndpointModeKRS, resolveKiroEndpointMode(mixedKiroAcct, anthropicGroup))
	require.Equal(t, KiroEndpointModeQ, resolveKiroEndpointMode(anthropicAcct, anthropicGroup))
}

func TestResolveKiroCacheEmulation(t *testing.T) {
	kiroGroup := &Group{Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": 0.5,
	}}

	enabled, ratio := resolveKiroCacheEmulation(mixedKiroAcct, kiroGroup)
	require.True(t, enabled)
	require.Equal(t, float64(1), ratio)

	enabled, ratio = resolveKiroCacheEmulation(mixedKiroAcct, anthropicGroup)
	require.True(t, enabled)
	require.Equal(t, 0.5, ratio)

	enabled, _ = resolveKiroCacheEmulation(&Account{Platform: PlatformAnthropic}, anthropicGroup)
	require.False(t, enabled)
}

func TestResolveKiroStickyTTL(t *testing.T) {
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	kiroGroup := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: 1800}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_sticky_session_ttl_seconds": 1200,
	}}
	require.Equal(t, 1200, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, anthropicGroup))
	require.Equal(t, 1800, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, kiroGroup))
	require.Equal(t, 0, resolveKiroStickySessionTTLSeconds(&Account{Platform: PlatformAnthropic}, anthropicGroup))
}
