//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositoryClearTempUnschedulableUnlessActiveReason(t *testing.T) {
	ctx := context.Background()
	const reason = "OVERAGE_REQUEST_LIMIT_EXCEEDED"
	for _, tc := range []struct {
		name          string
		initialReason string
		expired       bool
		replaceReason bool
		wantCleared   bool
	}{
		{name: "auth cooldown", initialReason: "kiro auth failure (401): expired", wantCleared: true},
		{name: "echoed code in an auth error", initialReason: "kiro auth failure (401): " + reason, wantCleared: true},
		{name: "active monthly park", initialReason: reason},
		{name: "monthly park installed after the auth snapshot", initialReason: "kiro auth failure (401): expired", replaceReason: true},
		{name: "expired monthly park", initialReason: reason, expired: true, wantCleared: true},
		{name: "no cooldown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testEntTx(t)
			cache := &schedulerCacheRecorder{}
			repo := newAccountRepositoryWithSQL(tx.Client(), tx, cache)
			setter, ok := any(repo).(interface {
				ClearTempUnschedulableUnlessActiveReason(context.Context, int64, string) (bool, error)
			})
			require.True(t, ok, "automated recovery requires an atomic live-row guard")
			account := mustCreateAccount(t, tx.Client(), &service.Account{
				Name: "kiro-overage-recovery", Platform: service.PlatformKiro, Type: service.AccountTypeOAuth,
			})
			now := time.Now().UTC().Truncate(time.Microsecond)
			until := now.Add(time.Hour)
			if tc.expired {
				until = now.Add(-time.Hour)
			}
			if tc.initialReason != "" {
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, until, tc.initialReason))
			}
			if tc.replaceReason {
				snapshot, err := repo.GetByID(ctx, account.ID)
				require.NoError(t, err)
				require.Equal(t, tc.initialReason, snapshot.TempUnschedulableReason)
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, until.Add(time.Hour), reason))
			}
			before, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			cache.setAccounts = nil
			var eventsBefore int
			require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", []any{account.ID}, &eventsBefore))

			applied, err := setter.ClearTempUnschedulableUnlessActiveReason(ctx, account.ID, reason)
			require.NoError(t, err)
			require.Equal(t, tc.wantCleared, applied)

			after, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			var eventsAfter int
			require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", []any{account.ID}, &eventsAfter))
			if tc.wantCleared {
				require.Nil(t, after.TempUnschedulableUntil)
				require.Empty(t, after.TempUnschedulableReason)
				require.Equal(t, eventsBefore+1, eventsAfter)
				require.Len(t, cache.setAccounts, 1)
				require.Nil(t, cache.setAccounts[0].TempUnschedulableUntil)
				require.Empty(t, cache.setAccounts[0].TempUnschedulableReason)
			} else {
				require.Equal(t, before.TempUnschedulableUntil, after.TempUnschedulableUntil)
				require.Equal(t, before.TempUnschedulableReason, after.TempUnschedulableReason)
				require.Equal(t, before.UpdatedAt, after.UpdatedAt)
				require.Equal(t, eventsBefore, eventsAfter)
				require.Empty(t, cache.setAccounts)
			}
		})
	}
}
