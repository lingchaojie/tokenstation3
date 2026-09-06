package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamAB99FastSettingsAcceptUltrafast(t *testing.T) {
	svc := &SettingService{}
	settings := &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: " ULTRAFAST ", Scope: BetaPolicyScopeAll, Action: BetaPolicyActionPass,
	}}}
	require.NoError(t, svc.ValidateOpenAIFastPolicySettings(settings))
	require.Equal(t, OpenAIFastTierUltrafast, settings.Rules[0].ServiceTier)
}
