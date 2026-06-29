//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStickySessionTTLForAccountGroup(t *testing.T) {
	anthropicGroup := &Group{Platform: PlatformAnthropic}
	kiroGroup := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: 1800}
	mixedKiro := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Extra: map[string]any{
		"mixed_scheduling": true, "kiro_sticky_session_ttl_seconds": 1200,
	}}
	anthropicAcct := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	require.Equal(t, 1200*time.Second, stickySessionTTLForAccountGroup(mixedKiro, anthropicGroup))
	require.Equal(t, 1800*time.Second, stickySessionTTLForAccountGroup(mixedKiro, kiroGroup))
	require.Equal(t, stickySessionTTL, stickySessionTTLForAccountGroup(anthropicAcct, anthropicGroup))

	// nil 账号 → 安全回退默认 TTL（不 panic）
	require.Equal(t, stickySessionTTL, stickySessionTTLForAccountGroup(nil, anthropicGroup))
	require.Equal(t, stickySessionTTL, stickySessionTTLForAccountGroup(nil, nil))
}
