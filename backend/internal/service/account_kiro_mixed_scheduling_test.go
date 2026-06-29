//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsKiroMixedSchedulingEnabled(t *testing.T) {
	kiroOn := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	kiroOff := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": false}}
	kiroNone := &Account{Platform: PlatformKiro}
	anthropic := &Account{Platform: PlatformAnthropic, Extra: map[string]any{"mixed_scheduling": true}}

	require.True(t, kiroOn.IsKiroMixedSchedulingEnabled())
	require.False(t, kiroOff.IsKiroMixedSchedulingEnabled())
	require.False(t, kiroNone.IsKiroMixedSchedulingEnabled())
	require.False(t, anthropic.IsKiroMixedSchedulingEnabled())
}

func TestAccountEligibleForMixedPlatform(t *testing.T) {
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	anti := &Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}

	require.True(t, accountEligibleForMixedPlatform(kiro, PlatformAnthropic))
	require.False(t, accountEligibleForMixedPlatform(kiro, PlatformGemini)) // kiro 绝不进 gemini
	require.True(t, accountEligibleForMixedPlatform(anti, PlatformAnthropic))
	require.True(t, accountEligibleForMixedPlatform(anti, PlatformGemini))
	require.False(t, accountEligibleForMixedPlatform(&Account{Platform: PlatformOpenAI}, PlatformAnthropic))

	// flag-gating regression guards
	require.False(t, accountEligibleForMixedPlatform(nil, PlatformAnthropic))
	require.False(t, accountEligibleForMixedPlatform(&Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": false}}, PlatformAnthropic))
	require.False(t, accountEligibleForMixedPlatform(&Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": false}}, PlatformAnthropic))
}

func TestMixedSchedulingPlatforms(t *testing.T) {
	require.Equal(t, []string{PlatformAnthropic, PlatformAntigravity, PlatformKiro}, mixedSchedulingPlatforms(PlatformAnthropic))
	require.Equal(t, []string{PlatformGemini, PlatformAntigravity}, mixedSchedulingPlatforms(PlatformGemini))
}

func TestIsAccountAllowedForPlatform_Kiro(t *testing.T) {
	s := &GatewayService{}
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	// useMixed=true, anthropic 池：放行
	require.True(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, true))
	// gemini 池：拒绝
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformGemini, true))
	// 非混合（强制平台）：拒绝
	require.False(t, s.isAccountAllowedForPlatform(kiro, PlatformAnthropic, false))
	// 未开 mixed 的 kiro：拒绝
	kiroOff := &Account{Platform: PlatformKiro}
	require.False(t, s.isAccountAllowedForPlatform(kiroOff, PlatformAnthropic, true))
}
