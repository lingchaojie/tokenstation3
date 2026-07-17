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

	// group-first 不变量：原生 kiro 组配置覆盖账号配置
	qGroup := &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}
	require.Equal(t, KiroEndpointModeQ, resolveKiroEndpointMode(mixedKiroAcct, qGroup))
}

func TestResolveKiroEndpointModeAutoForMixedScheduling(t *testing.T) {
	autoGroup := &Group{Platform: PlatformKiro, KiroEndpointMode: "auto"}
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_endpoint_mode": "auto",
	}}

	require.Equal(t, "auto", resolveKiroEndpointMode(mixedKiroAcct, autoGroup))
	require.Equal(t, "auto", resolveKiroEndpointMode(mixedKiroAcct, anthropicGroup))
}

func TestResolveKiroEndpointModeAPIKeyForcesQ(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_region": "eu-west-1",
			"api_key":    "ksk_test",
		},
		Extra: map[string]any{"kiro_endpoint_mode": "auto"},
	}
	parsed := &ParsedRequest{Group: &Group{Platform: PlatformKiro, KiroEndpointMode: "auto"}}

	require.Equal(t, KiroEndpointModeQ, kiroEndpointModeForRequest(account, parsed))
	endpoints := buildKiroEndpoints(account, kiroEndpointModeForRequest(account, parsed))
	require.Len(t, endpoints, 1)
	require.Equal(t, "https://q.eu-west-1.amazonaws.com/generateAssistantResponse", endpoints[0].URL)
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

	// nil group + kiro direct 账号 → 走账号配置（生产中 parsed.Group 可能为 nil）
	enabled, ratio = resolveKiroCacheEmulation(mixedKiroAcct, nil)
	require.True(t, enabled)
	require.Equal(t, 0.5, ratio)
	// nil group + 非 kiro 账号 → 安全默认
	enabled, _ = resolveKiroCacheEmulation(&Account{Platform: PlatformAnthropic}, nil)
	require.False(t, enabled)
}

func TestResolveKiroStickySessionTTLSeconds(t *testing.T) {
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	kiroGroup := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: 1800}
	mixedKiroAcct := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_sticky_session_ttl_seconds": 1200,
	}}
	require.Equal(t, 1200, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, anthropicGroup))
	require.Equal(t, 1800, resolveKiroStickySessionTTLSeconds(mixedKiroAcct, kiroGroup))
	require.Equal(t, 0, resolveKiroStickySessionTTLSeconds(&Account{Platform: PlatformAnthropic}, anthropicGroup))
}
