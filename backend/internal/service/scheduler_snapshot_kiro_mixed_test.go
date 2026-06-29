//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mixed kiro 账号变更时需重建的平台桶集合：必须含 anthropic、不含 gemini。
func TestRebuildPlatformsForMixedAccount(t *testing.T) {
	kiro := &Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}}
	kiroResult := rebuildPlatformsForMixedAccount(kiro)
	require.Equal(t, []string{PlatformAnthropic}, kiroResult)
	require.NotContains(t, kiroResult, PlatformGemini) // 不变量：kiro 绝不混入 gemini

	anti := &Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}
	require.Equal(t, []string{PlatformAnthropic, PlatformGemini}, rebuildPlatformsForMixedAccount(anti))

	require.Nil(t, rebuildPlatformsForMixedAccount(&Account{Platform: PlatformKiro})) // 未开 mixed
	require.Nil(t, rebuildPlatformsForMixedAccount(&Account{Platform: PlatformOpenAI}))
	require.Nil(t, rebuildPlatformsForMixedAccount(&Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": false}}))
}
