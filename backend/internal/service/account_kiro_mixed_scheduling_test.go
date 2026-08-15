//go:build unit

package service

import (
	"context"
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
	require.Equal(t, []string{"future", PlatformAntigravity}, mixedSchedulingPlatforms("future"))
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

func TestListSchedulableAccountsRequiredPlatformPreservesKiroMixedEligibility(t *testing.T) {
	repo := &mockAccountRepoForPlatform{accounts: []Account{
		{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 3, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": false}},
	}}
	svc := &GatewayService{cfg: testConfig(), accountRepo: repo}
	ctx := WithGatewayRequiredAccountPlatform(context.Background(), PlatformKiro)

	accounts, useMixed, err := svc.listSchedulableAccounts(ctx, nil, PlatformAnthropic, false)

	require.NoError(t, err)
	require.True(t, useMixed)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(2), accounts[0].ID, "required platform must not bypass KIRO mixed_scheduling eligibility")
}

func TestSelectAccountWithMixedSchedulingRequiredPlatformRejectsStickyAnthropic(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformKiro, Priority: 2, Status: StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}},
			{ID: 3, Platform: PlatformKiro, Priority: 1, Status: StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": false}},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{
		cfg:         testConfig(),
		accountRepo: repo,
		cache:       &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"compaction-session": 1}},
	}
	ctx := WithGatewayRequiredAccountPlatform(context.Background(), PlatformKiro)

	account, err := svc.selectAccountWithMixedScheduling(ctx, nil, "compaction-session", "", nil, PlatformAnthropic)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(2), account.ID, "sticky Anthropic and mixed-disabled KIRO must both be skipped")
}
