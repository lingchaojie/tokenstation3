//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

const testMonitorProviderOpenCodeGo = "opencode_go"

func TestOpenCodeGoMonitorCreateAndUpdateValidation(t *testing.T) {
	create := ChannelMonitorCreateParams{
		Provider:        testMonitorProviderOpenCodeGo,
		CheckMode:       MonitorCheckModeProbe,
		Endpoint:        "https://8.8.8.8/zen/go/v1",
		APIKey:          "sk-opencode",
		PrimaryModel:    "glm-4.6",
		IntervalSeconds: 60,
	}
	require.NoError(t, validateCreateParams(create))

	existing := &ChannelMonitor{
		Provider:        MonitorProviderKimi,
		CheckMode:       MonitorCheckModeProbe,
		APIMode:         MonitorAPIModeChatCompletions,
		Endpoint:        "https://8.8.8.8/zen/go/v1",
		APIKey:          "encrypted",
		PrimaryModel:    "glm-4.6",
		IntervalSeconds: 60,
	}
	provider := testMonitorProviderOpenCodeGo
	require.NoError(t, applyMonitorUpdate(existing, ChannelMonitorUpdateParams{Provider: &provider}))
	require.Equal(t, testMonitorProviderOpenCodeGo, existing.Provider)
}

func TestOpenCodeGoMonitorQuotaValidationAndFetch(t *testing.T) {
	account := &Account{ID: 27, Platform: domain.PlatformOpenCodeGo}
	require.NoError(t, monitorAccountQuotaCapability(account))

	fetcher, usage, cnQuota, cnBalance, accounts := newQuotaFetcherTestSetup(t)
	accounts.accounts[account.ID] = account
	cnQuota.result = &CNProviderQuotaProbeResult{
		Success:         true,
		CredentialValid: true,
		Tiers:           []CNQuotaTier{{Window: "weekly", UsedPercent: 25}},
	}

	snapshot := fetcher.Fetch(context.Background(), account.ID)
	require.True(t, snapshot.Success)
	require.Equal(t, "cn_quota", snapshot.Source)
	require.Equal(t, 1, cnQuota.calls)
	require.Equal(t, 0, cnBalance.calls)
	require.Equal(t, 0, usage.getCalls())

	svc := NewChannelMonitorService(nil, nil)
	svc.SetQuotaFetcher(fetcher)
	require.NoError(t, svc.validateLinkedAccount(context.Background(), testMonitorProviderOpenCodeGo, &account.ID))

	zen := &Account{
		ID:          28,
		Platform:    domain.PlatformOpenCodeGo,
		Credentials: map[string]any{"account_mode": AccountModeZen},
	}
	accounts.accounts[zen.ID] = zen
	require.ErrorIs(
		t,
		svc.validateLinkedAccount(context.Background(), testMonitorProviderOpenCodeGo, &zen.ID),
		ErrChannelMonitorAccountNotSupportable,
	)
}

func TestOpenCodeGoMonitorProbeUsesChatCompletions(t *testing.T) {
	h := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, h) + "/zen/go/v1"

	result := runCheckForModel(
		context.Background(),
		testMonitorProviderOpenCodeGo,
		endpoint,
		"sk-opencode",
		"glm-4.6",
		nil,
	)

	require.Equal(t, MonitorStatusOperational, result.Status, result.Message)
	require.Equal(t, "/zen/go/v1/chat/completions", h.lastPath)
	require.Equal(t, "glm-4.6", h.lastBody["model"])
	require.NotEmpty(t, h.lastBody["messages"])
	require.Equal(t, false, h.lastBody["stream"])
	require.Equal(t, "Bearer sk-opencode", h.lastHeaders.Get("Authorization"))
}
