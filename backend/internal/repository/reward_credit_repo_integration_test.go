//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newRewardCreditTestUser(t *testing.T, client *dbent.Client, balance float64) *service.User {
	t.Helper()
	return mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("reward-credit-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Balance:      balance,
		Concurrency:  1,
	})
}

func rewardGrant(userID int64, creditType service.RewardCreditType, source string, amount float64, grantedAt, expiresAt time.Time) service.RewardCreditGrant {
	return service.RewardCreditGrant{
		UserID:     userID,
		CreditType: creditType,
		SourceKey:  source,
		Amount:     amount,
		GrantedAt:  grantedAt,
		ExpiresAt:  expiresAt,
	}
}

func TestRewardCreditRepository_GrantIsIdempotentAndUpdatesBalance(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewRewardCreditRepository(client, integrationDB)
	user := newRewardCreditTestUser(t, client, 3)
	now := time.Date(2026, 7, 16, 1, 30, 0, 0, time.UTC)
	grant := rewardGrant(user.ID, service.RewardCreditDailyCheckIn, "daily-check-in:42", 5, now, now.Add(12*time.Hour))

	first, err := repo.Grant(txCtx, grant)
	require.NoError(t, err)
	require.True(t, first.Applied)
	require.NotZero(t, first.CreditID)
	require.InDelta(t, 8, first.BalanceAfter, 1e-8)

	second, err := repo.Grant(txCtx, grant)
	require.NoError(t, err)
	require.False(t, second.Applied)
	require.Equal(t, first.CreditID, second.CreditID)
	require.InDelta(t, 8, second.BalanceAfter, 1e-8)

	require.InDelta(t, 8, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", user.ID), 1e-8)
	require.Equal(t, 1, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_reward_credits WHERE user_id = $1", user.ID))
	require.Equal(t, 1, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_reward_credit_events WHERE user_id = $1 AND event_type = 'grant'", user.ID))
}

func TestRewardCreditRepository_GetSummaryExpiresStaleCredits(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewRewardCreditRepository(client, integrationDB)
	user := newRewardCreditTestUser(t, client, 10)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)

	_, err := repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditDailyCheckIn, "daily-check-in:1", 4, now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditDailyCheckIn, "daily-check-in:2", 5, now, now.Add(8*time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditAffiliateInvitee, "affiliate-binding:2:invitee", 6, now, now.Add(48*time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditAffiliateInviter, "affiliate-binding:3:inviter", 7, now, now.Add(24*time.Hour)))
	require.NoError(t, err)

	summary, err := repo.GetSummary(txCtx, user.ID, now)
	require.NoError(t, err)
	require.InDelta(t, 5, summary.DailyCheckIn.Amount, 1e-8)
	require.NotNil(t, summary.DailyCheckIn.ExpiresAt)
	require.Equal(t, now.Add(8*time.Hour), summary.DailyCheckIn.ExpiresAt.UTC())
	require.InDelta(t, 13, summary.Affiliate.Amount, 1e-8)
	require.Equal(t, 2, summary.Affiliate.CreditCount)
	require.NotNil(t, summary.Affiliate.EarliestExpiresAt)
	require.Equal(t, now.Add(24*time.Hour), summary.Affiliate.EarliestExpiresAt.UTC())

	// The expired lot was originally added, then lazily removed by GetSummary.
	require.InDelta(t, 28, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", user.ID), 1e-8)
	require.Equal(t, 1, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_reward_credit_events WHERE user_id = $1 AND event_type = 'expire'", user.ID))
}

func TestRewardCreditRepository_ListCreditsFiltersAndSortsByExpiry(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewRewardCreditRepository(client, integrationDB)
	user := newRewardCreditTestUser(t, client, 0)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)

	_, err := repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditAffiliateInvitee, "affiliate-binding:8:invitee", 5, now, now.Add(72*time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditAffiliateInviter, "affiliate-binding:7:inviter", 10, now, now.Add(24*time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditDailyCheckIn, "daily-check-in:9", 3, now, now.Add(8*time.Hour)))
	require.NoError(t, err)

	items, total, err := repo.ListCredits(txCtx, service.RewardCreditListFilter{
		UserID: user.ID,
		CreditTypes: []service.RewardCreditType{
			service.RewardCreditAffiliateInviter,
			service.RewardCreditAffiliateInvitee,
		},
		Status:   service.RewardCreditStatusActive,
		Page:     1,
		PageSize: 20,
		Now:      now,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	require.Equal(t, service.RewardCreditAffiliateInviter, items[0].CreditType)
	require.Equal(t, service.RewardCreditRoleInviter, items[0].RoleLabel)
	require.Equal(t, service.RewardCreditAffiliateInvitee, items[1].CreditType)
	require.Equal(t, service.RewardCreditRoleInvitee, items[1].RoleLabel)
}

func TestRewardCreditRepository_ExpireUserIsIdempotent(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewRewardCreditRepository(client, integrationDB)
	user := newRewardCreditTestUser(t, client, 2)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)

	_, err := repo.Grant(txCtx, rewardGrant(user.ID, service.RewardCreditDailyCheckIn, "daily-check-in:11", 4, now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)

	expired, err := repo.ExpireUser(txCtx, user.ID, now)
	require.NoError(t, err)
	require.InDelta(t, 4, expired, 1e-8)
	expired, err = repo.ExpireUser(txCtx, user.ID, now.Add(time.Minute))
	require.NoError(t, err)
	require.Zero(t, expired)
	require.InDelta(t, 2, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", user.ID), 1e-8)
}

func TestRewardCreditRepository_ExpireBatchGroupsAffectedUsers(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewRewardCreditRepository(client, integrationDB)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	firstUser := newRewardCreditTestUser(t, client, 1)
	secondUser := newRewardCreditTestUser(t, client, 2)

	_, err := repo.Grant(txCtx, rewardGrant(firstUser.ID, service.RewardCreditDailyCheckIn, "daily-check-in:21", 3, now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(firstUser.ID, service.RewardCreditAffiliateInvitee, "affiliate-binding:21:invitee", 4, now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)
	_, err = repo.Grant(txCtx, rewardGrant(secondUser.ID, service.RewardCreditAffiliateInviter, "affiliate-binding:22:inviter", 5, now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)

	results, err := repo.ExpireBatch(txCtx, now, 100)
	require.NoError(t, err)
	require.ElementsMatch(t, []service.RewardCreditExpiryResult{
		{UserID: firstUser.ID, ExpiredAmount: 7},
		{UserID: secondUser.ID, ExpiredAmount: 5},
	}, results)
	require.InDelta(t, 1, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", firstUser.ID), 1e-8)
	require.InDelta(t, 2, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", secondUser.ID), 1e-8)
}

func TestRewardCreditRepository_ConcurrentDuplicateGrantAppliesOnce(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewRewardCreditRepository(client, integrationDB)
	user := newRewardCreditTestUser(t, client, 0)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	grant := rewardGrant(user.ID, service.RewardCreditAffiliateInvitee, "affiliate-binding:99:invitee", 5, now, now.Add(7*24*time.Hour))

	var wg sync.WaitGroup
	results := make(chan service.RewardCreditGrantResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := repo.Grant(ctx, grant)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	applied := 0
	for result := range results {
		if result.Applied {
			applied++
		}
	}
	require.Equal(t, 1, applied)
	require.InDelta(t, 5, querySingleFloat(t, ctx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", user.ID), 1e-8)
}
