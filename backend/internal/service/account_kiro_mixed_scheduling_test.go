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
}
