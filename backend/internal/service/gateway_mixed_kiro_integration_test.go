//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMixedPool_AnthropicAccountNotKiroRouted(t *testing.T) {
	anthropic := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive}
	kiro := &Account{ID: 2, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive,
		Extra: map[string]any{"mixed_scheduling": true}}

	// 准入：anthropic 池里 mixed kiro 放行、anthropic 直放行；非混合时 kiro 被拒
	s := &GatewayService{}
	require.True(t, s.isAccountAllowedForPlatform(anthropic, PlatformAnthropic, true))
	require.True(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, true))
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, false))

	// 转发分流：anthropic 不进 kiro 路径，kiro 进
	require.False(t, isKiroDirectModeAccount(anthropic))
	require.True(t, isKiroDirectModeAccount(kiro))
}
