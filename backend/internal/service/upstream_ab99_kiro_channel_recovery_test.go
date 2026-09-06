//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/stretchr/testify/require"
)

// Passing the unfiltered pool to recovery would clear a restricted account's
// cooldown even though it can never serve this request. Removing recovery would
// instead make the mixed-pool case unavailable despite an eligible 429 account.
func TestLoadAwareKiroRecoveryRespectsChannelAdmission(t *testing.T) {
	for _, tc := range []struct {
		name           string
		legacy         bool
		restrict       bool
		allowSecond    bool
		suspended      bool
		alreadyRetried bool
		wantID         int64
	}{
		{name: "all restricted", restrict: true},
		{name: "only eligible account recovered", restrict: true, allowSecond: true, wantID: 42},
		{name: "restriction disabled", wantID: 41},
		{name: "suspended not recovered", restrict: true, allowSecond: true, suspended: true},
		{name: "recovery only once", restrict: true, allowSecond: true, alreadyRetried: true},
		{name: "legacy all restricted", legacy: true, restrict: true},
		{name: "legacy only eligible recovered", legacy: true, restrict: true, allowSecond: true, wantID: 42},
		{name: "legacy restriction disabled", legacy: true, wantID: 41},
		{name: "legacy suspended", legacy: true, restrict: true, allowSecond: true, suspended: true},
		{name: "legacy one attempt", legacy: true, restrict: true, allowSecond: true, alreadyRetried: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(10)
			group := &Group{ID: groupID, Platform: PlatformKiro, Status: StatusActive, Hydrated: true}
			accounts := []Account{
				{ID: 41, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-fable-5-1"}}},
				{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 2,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-fable-5-1"}}},
			}
			if tc.allowSecond {
				accounts[1].Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"}
			}
			repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{41: &accounts[0], 42: &accounts[1]}}
			channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, RestrictModels: tc.restrict,
				BillingModelSource: BillingModelSourceUpstream,
				ModelPricing:       []ChannelModelPricing{{Platform: PlatformKiro, Models: []string{"claude-sonnet-4-6"}}},
			}
			store := &stubKiroCooldownStore{state: &kirocooldown.State{Active: true, Reason: kirocooldown.CooldownReason429,
				CooldownUntil: time.Now().Add(time.Minute), Remaining: time.Minute}, clearResult: true}
			if tc.suspended {
				store.state.Reason = kirocooldown.CooldownReasonSuspended
			}
			cfg := testConfig()
			cfg.Gateway.Scheduling.LoadBatchEnabled = !tc.legacy
			svc := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}},
				channelService:     newTestChannelService(makeStandardRepo(channel, map[int64]string{groupID: PlatformKiro})),
				concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}), cfg: cfg, kiroCooldownStore: store,
			}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			if tc.alreadyRetried {
				ctx = context.WithValue(ctx, kiroCooldownRecoveryAttemptedKey, true)
			}
			excluded := map[int64]struct{}{99: {}}
			result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-6", excluded, "", 0)
			require.Equal(t, map[int64]struct{}{99: {}}, excluded, "recovery must not modify the caller's exclusion set")
			if tc.wantID == 0 {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Nil(t, result)
				require.False(t, store.clearCalled, "must not clear a restricted, suspended, or already-retried cooldown")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.wantID, result.Account.ID)
			require.True(t, store.clearCalled)
			if tc.restrict {
				require.Equal(t, []string{kiroRuntimeKey(&accounts[1])}, store.clearKeys, "only the admitted account may reach cooldown storage")
			}
		})
	}
}
