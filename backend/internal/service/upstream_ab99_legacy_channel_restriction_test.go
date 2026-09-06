//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestLegacySelectionEnforcesChannelRestrictionAcrossPriorityPaths(t *testing.T) {
	for _, mode := range []string{"single", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				sticky     bool
				routed     bool
				restrict   bool
				allBlocked bool
				wantID     int64
			}{
				{name: "ordinary", restrict: true, wantID: 2},
				{name: "sticky", sticky: true, restrict: true, wantID: 2},
				{name: "routed", routed: true, restrict: true, wantID: 2},
				{name: "routed sticky", routed: true, sticky: true, restrict: true, wantID: 2},
				{name: "all blocked", routed: true, sticky: true, restrict: true, allBlocked: true},
				{name: "disabled sticky", sticky: true, wantID: 1},
				{name: "disabled routed", routed: true, wantID: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					bindings := map[string]int64{}
					session := ""
					if tc.sticky {
						session = "sticky"
						bindings[session] = 1
					}
					var routing map[string][]int64
					if tc.routed {
						routing = map[string][]int64{"claude-fable-5-1": {1}}
					}
					f := newLoadAwareRestrictionFixture(t, tc.restrict, bindings, routing)
					f.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = false
					repo := f.svc.accountRepo.(*mockAccountRepoForPlatform)
					for i := range repo.accounts {
						repo.accounts[i].AccountGroups = []AccountGroup{{GroupID: f.groupID}}
					}
					if tc.allBlocked {
						repo.accounts[1].Credentials = map[string]any{
							"model_mapping": map[string]any{"claude-fable-5-1": "claude-fable-5-1"},
						}
					}
					ctx := f.ctx
					if mode == "single" {
						ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformAnthropic)
					}
					result, err := f.svc.SelectAccountWithLoadAwareness(ctx, &f.groupID, session, "claude-fable-5-1", nil, "", 0)
					if tc.wantID == 0 {
						require.ErrorIs(t, err, ErrNoAvailableAccounts)
						require.Nil(t, result)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, tc.wantID, result.Account.ID)
					require.Zero(t, f.concurrencyCache.loadBatchCalls, "must exercise the legacy selector")
					if tc.sticky {
						require.Equal(t, tc.wantID, f.cache.sessionBindings[session])
					}
				})
			}
		})
	}
}
