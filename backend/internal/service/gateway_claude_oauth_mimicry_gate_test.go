//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldMimicClaudeCodeForAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		account            *Account
		isClaudeCodeClient bool
		want               bool
	}{
		{name: "Anthropic OAuth non Claude Code client", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, want: true},
		{name: "Anthropic OAuth Claude Code client", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, isClaudeCodeClient: true, want: false},
		{name: "KIRO OAuth", account: &Account{Platform: PlatformKiro, Type: AccountTypeOAuth}, want: false},
		{name: "KIRO setup token", account: &Account{Platform: PlatformKiro, Type: AccountTypeSetupToken}, want: false},
		{name: "Anthropic API key", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, want: false},
		{name: "nil account", account: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, shouldMimicClaudeCodeForAccount(tt.account, tt.isClaudeCodeClient))
		})
	}
}
