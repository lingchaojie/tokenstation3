package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountKiloCredentialsPreferCliproxyPlusKeys(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformKilo,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":                "fallback-token",
			"organization_id":        "fallback-org",
			"kilocodeToken":          "plus-token",
			"kilocodeOrganizationId": "plus-org",
		},
	}

	require.True(t, account.IsKilo())
	require.Equal(t, "plus-token", account.GetKiloToken())
	require.Equal(t, "plus-org", account.GetKiloOrganizationID())
}

func TestNormalizeKiloBillingModel(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"anthropic/claude-sonnet-4.6":   "claude-sonnet-4.6",
		"openai/gpt-5":                  "gpt-5",
		"google/gemini-3.1-pro-preview": "gemini-3.1-pro-preview",
		"kilo-auto/frontier":            "kilo-auto/frontier",
		"free/qwen3-coder":              "free/qwen3-coder",
		"":                              "",
	}

	for input, want := range tests {
		input := input
		want := want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, NormalizeKiloBillingModel(input))
		})
	}
}

func TestKiloTokenCredentialIsSensitive(t *testing.T) {
	t.Parallel()

	require.True(t, IsSensitiveCredentialKey("kilocodeToken"))
}
