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

func TestAccountKiroEndpointModeAuto(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Extra:    map[string]any{"kiro_endpoint_mode": "auto"},
	}

	require.Equal(t, "auto", account.KiroEndpointMode())
}

func TestAccountKiroConfigNonKiroReturnsDefaults(t *testing.T) {
	// 非 kiro 账号即使 Extra 里带了 kiro 配置，也必须返回默认值（与 group.go 语义一致）
	acc := &Account{Platform: PlatformAnthropic, Extra: map[string]any{
		"kiro_endpoint_mode":              "krs",
		"kiro_cache_emulation_enabled":    true,
		"kiro_cache_emulation_ratio":      0.5,
		"kiro_auto_sticky_enabled":        true,
		"kiro_sticky_session_ttl_seconds": 1800,
	}}
	require.Equal(t, KiroEndpointModeQ, acc.KiroEndpointMode())
	require.False(t, acc.KiroCacheEmulationEnabled())
	require.Equal(t, float64(0), acc.KiroCacheEmulationRatio())
	require.False(t, acc.KiroAutoStickyEnabled())
	require.Equal(t, 0, acc.KiroStickySessionTTLSeconds()) // 非 kiro → 0，不是 3600
}

func TestAccountKiroExtraFloatTypes(t *testing.T) {
	// kiroExtraFloat 兼容 float64 / int / 字符串数字
	f := &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": float64(0.25)}}
	require.Equal(t, 0.25, f.KiroCacheEmulationRatio())
	s := &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": "0.75"}}
	require.Equal(t, 0.75, s.KiroCacheEmulationRatio())
}
