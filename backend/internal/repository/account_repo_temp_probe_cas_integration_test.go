//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Runtime assertion lets RED fail on missing behavior rather than compilation.
type tempProbeCASForTest interface {
	CompareAndSetTempUnschedulable(context.Context, int64, *time.Time, string, *time.Time, string) (bool, error)
}

func TestAccountRepositoryTempProbeCAS(t *testing.T) {
	ctx := context.Background()
	for _, clear := range []bool{false, true} {
		for _, tc := range []struct {
			name                                                                                                               string
			nilUntil, nullReason, emptyReason, expired, changeReason, changeDeadline, changeExtra, nilExpected, wrongNilReason bool
			wantApplied                                                                                                        bool
		}{
			{name: "match", wantApplied: true},
			{name: "expired_match", expired: true, wantApplied: true},
			{name: "changed_reason", changeReason: true},
			{name: "same_reason_changed_deadline", changeDeadline: true},
			{name: "updated_extra_independent", changeExtra: true, wantApplied: true},
			{name: "nil_pair", nilUntil: true, nullReason: true, wantApplied: true},
			{name: "nil_deadline_with_reason", nilUntil: true, wantApplied: true},
			{name: "null_reason", nullReason: true, wantApplied: true},
			{name: "empty_reason", emptyReason: true, wantApplied: true},
			{name: "nil_expected_does_not_match_set", nilExpected: true},
			{name: "nil_deadline_reason_mismatch", nilUntil: true, wrongNilReason: true},
		} {
			t.Run(fmt.Sprintf("clear_%t/%s", clear, tc.name), func(t *testing.T) {
				tx := testEntTx(t)
				cache := &schedulerCacheRecorder{}
				repo := newAccountRepositoryWithSQL(tx.Client(), tx, cache)
				setter, ok := any(repo).(tempProbeCASForTest)
				require.True(t, ok, "production repository must compare the exact temp deadline and reason atomically")
				limitedAt := time.Now().UTC().Truncate(time.Microsecond)
				resetAt := limitedAt.Add(time.Hour)
				account := mustCreateAccount(t, tx.Client(), &service.Account{
					Name: "temp-probe-cas", Platform: service.PlatformCommandCode, Type: service.AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "local-fixture"}, Extra: map[string]any{"unrelated": map[string]any{"keep": true}},
					RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt, OverloadUntil: &resetAt,
				})
				// Persist a microsecond timestamp and reload it: PostgreSQL determines
				// the exact timestamp precision used by the CAS expectation.
				var storedUntil any = time.Now().UTC().Truncate(time.Microsecond).Add(5 * time.Minute)
				if tc.expired {
					storedUntil = time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
				}
				if tc.nilUntil {
					storedUntil = nil
				}
				var storedReason any = "balance_low: old probe"
				if tc.nullReason {
					storedReason = nil
				} else if tc.emptyReason {
					storedReason = ""
				}
				_, err := tx.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_until=$1,temp_unschedulable_reason=$2,updated_at=NOW()-INTERVAL '1 hour' WHERE id=$3`, storedUntil, storedReason, account.ID)
				require.NoError(t, err)
				observed, err := repo.GetByID(ctx, account.ID)
				require.NoError(t, err)
				require.NoError(t, cache.SetAccount(ctx, observed))
				if tc.changeReason {
					_, err = tx.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_reason='opaque cap' WHERE id=$1`, account.ID)
					require.NoError(t, err)
				}
				if tc.changeDeadline {
					_, err = tx.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_until=temp_unschedulable_until+INTERVAL '1 microsecond' WHERE id=$1`, account.ID)
					require.NoError(t, err)
				}
				if tc.changeExtra {
					require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{"probe_balance": 20.0}))
				}
				before, err := repo.GetByID(ctx, account.ID)
				require.NoError(t, err)
				if tc.changeExtra {
					require.NotEqual(t, observed.UpdatedAt, before.UpdatedAt, "probe persistence really changes the row timestamp")
				}
				var beforeEvents int
				require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", []any{account.ID}, &beforeEvents))
				expectedUntil, expectedReason := observed.TempUnschedulableUntil, observed.TempUnschedulableReason
				if tc.nilExpected {
					expectedUntil = nil
				}
				if tc.wrongNilReason {
					expectedReason = ""
				}
				var replacement *time.Time
				reason := ""
				if !clear {
					until := time.Now().UTC().Truncate(time.Microsecond).Add(20 * time.Minute)
					replacement = &until
					reason = "balance_low: replacement"
				}
				applied, err := setter.CompareAndSetTempUnschedulable(ctx, account.ID, expectedUntil, expectedReason, replacement, reason)
				require.NoError(t, err)
				require.Equal(t, tc.wantApplied, applied)
				after, err := repo.GetByID(ctx, account.ID)
				require.NoError(t, err)
				var afterEvents int
				require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", []any{account.ID}, &afterEvents))
				if tc.wantApplied {
					require.Equal(t, replacement, after.TempUnschedulableUntil)
					require.Equal(t, reason, after.TempUnschedulableReason)
					require.Equal(t, beforeEvents+1, afterEvents)
				} else {
					require.Equal(t, before.TempUnschedulableUntil, after.TempUnschedulableUntil)
					require.Equal(t, before.TempUnschedulableReason, after.TempUnschedulableReason)
					require.Equal(t, before.UpdatedAt, after.UpdatedAt)
					require.Equal(t, beforeEvents, afterEvents)
				}
				require.Equal(t, before.Extra, after.Extra)
				require.Equal(t, before.Credentials, after.Credentials)
				require.Equal(t, before.Schedulable, after.Schedulable)
				require.Equal(t, before.Status, after.Status)
				require.Equal(t, before.RateLimitedAt, after.RateLimitedAt)
				require.Equal(t, before.RateLimitResetAt, after.RateLimitResetAt)
				require.Equal(t, before.OverloadUntil, after.OverloadUntil)
				cached, err := cache.GetAccount(ctx, account.ID)
				require.NoError(t, err)
				require.NotNil(t, cached)
				require.Equal(t, after.TempUnschedulableUntil, cached.TempUnschedulableUntil)
				require.Equal(t, after.TempUnschedulableReason, cached.TempUnschedulableReason)
			})
		}
	}
}

func TestAccountRepositoryTempProbeCASMissingAndDeleted(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"zero", "missing", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			tx := testEntTx(t)
			cache := &schedulerCacheRecorder{}
			repo := newAccountRepositoryWithSQL(tx.Client(), tx, cache)
			setter, ok := any(repo).(tempProbeCASForTest)
			require.True(t, ok, "production CAS capability must exist")
			id := int64(0)
			if kind != "zero" {
				account := mustCreateAccount(t, tx.Client(), &service.Account{Name: "temp-cas-absent"})
				id = account.ID
				require.NoError(t, cache.SetAccount(ctx, account))
				query := `DELETE FROM accounts WHERE id=$1`
				if kind == "deleted" {
					query = `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`
				}
				_, err := tx.ExecContext(ctx, query, id)
				require.NoError(t, err)
			}
			until := time.Now().Add(time.Hour)
			applied, err := setter.CompareAndSetTempUnschedulable(ctx, id, nil, "", &until, "balance_low: stale")
			require.NoError(t, err)
			require.False(t, applied)
			var events int
			require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", []any{id}, &events))
			require.Zero(t, events)
			cached, err := cache.GetAccount(ctx, id)
			require.NoError(t, err)
			require.Nil(t, cached, "a missing row cannot remain schedulable in cache")
		})
	}
}

func TestAccountRepositoryTempProbeCASOutboxFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	cache := &schedulerCacheRecorder{}
	repo := newAccountRepositoryWithSQL(client, &failAtomicSchedulerOutboxSQLExecutor{sqlExecutor: integrationDB}, cache)
	setter, ok := any(repo).(tempProbeCASForTest)
	require.True(t, ok, "production CAS capability must exist")
	account := mustCreateAccount(t, client, &service.Account{Name: "temp-cas-outbox-failure"})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, err)
	})
	before, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NoError(t, cache.SetAccount(ctx, before))
	cache.setAccounts = nil
	until := time.Now().Add(time.Hour)
	applied, err := setter.CompareAndSetTempUnschedulable(ctx, account.ID, nil, "", &until, "balance_low: new")
	require.Error(t, err)
	require.False(t, applied)
	after, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, after.TempUnschedulableUntil)
	require.Empty(t, after.TempUnschedulableReason)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)
	require.Empty(t, cache.setAccounts, "a rejected database mutation must not publish a new scheduling state")
	var events int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", account.ID).Scan(&events))
	require.Zero(t, events)
}
