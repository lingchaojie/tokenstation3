//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func inviteeUserIDs(users []*service.User) []int64 {
	ids := make([]int64, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	return ids
}

func TestAffiliateRepository_BindInviterAtLimitStillRewardsInvitee(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewAffiliateRepository(client, integrationDB)

	inviter := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-limit-inviter-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	invitee := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-limit-invitee-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	_, err := repo.EnsureUserAffiliate(txCtx, inviter.ID)
	require.NoError(t, err)
	_, err = repo.EnsureUserAffiliate(txCtx, invitee.ID)
	require.NoError(t, err)
	_, err = client.ExecContext(txCtx, "UPDATE user_affiliates SET inviter_reward_count = 3 WHERE user_id = $1", inviter.ID)
	require.NoError(t, err)

	now := time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC)
	result, err := repo.BindInviter(txCtx, service.AffiliateBindInput{
		InviteeUserID:      invitee.ID,
		InviterUserID:      inviter.ID,
		RewardMode:         service.AffiliateRewardModeImmediate,
		InviterReward:      10,
		InviteeReward:      5,
		ValidityDays:       7,
		InviterRewardLimit: 3,
		GrantedAt:          now,
	})

	require.NoError(t, err)
	require.True(t, result.Bound)
	require.True(t, result.Resolved)
	require.False(t, result.InviterRewarded)
	require.True(t, result.InviteeRewarded)
	require.InDelta(t, 0, querySingleFloat(t, txCtx, client, "SELECT balance::double precision FROM users WHERE id = $1", inviter.ID), 1e-9)
	require.InDelta(t, 5, querySingleFloat(t, txCtx, client, "SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.Equal(t, 3, querySingleInt(t, txCtx, client, "SELECT inviter_reward_count FROM user_affiliates WHERE user_id = $1", inviter.ID))
	require.Equal(t, 1, querySingleInt(t, txCtx, client, "SELECT COUNT(*) FROM user_reward_credits WHERE user_id = $1 AND credit_type = 'affiliate_invitee'", invitee.ID))
	rows, err := client.QueryContext(txCtx, `
SELECT granted_at, expires_at
FROM user_reward_credits
WHERE user_id = $1 AND credit_type = 'affiliate_invitee'`, invitee.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var grantedAt, expiresAt time.Time
	require.NoError(t, rows.Scan(&grantedAt, &expiresAt))
	require.NoError(t, rows.Close())
	require.Equal(t, now, grantedAt.UTC())
	require.Equal(t, now.Add(7*24*time.Hour), expiresAt.UTC())
}

func TestAffiliateRepository_ZeroInviterRewardDoesNotConsumeLimit(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewAffiliateRepository(client, integrationDB)

	inviter := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-zero-inviter-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	invitee := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-zero-invitee-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	_, err := repo.EnsureUserAffiliate(txCtx, inviter.ID)
	require.NoError(t, err)
	_, err = client.ExecContext(txCtx, "UPDATE user_affiliates SET inviter_reward_count = 2 WHERE user_id = $1", inviter.ID)
	require.NoError(t, err)

	result, err := repo.BindInviter(txCtx, service.AffiliateBindInput{
		InviteeUserID:      invitee.ID,
		InviterUserID:      inviter.ID,
		RewardMode:         service.AffiliateRewardModeImmediate,
		InviterReward:      0,
		InviteeReward:      5,
		ValidityDays:       7,
		InviterRewardLimit: 3,
		GrantedAt:          time.Now().UTC(),
	})

	require.NoError(t, err)
	require.True(t, result.Bound)
	require.False(t, result.InviterRewarded)
	require.True(t, result.InviteeRewarded)
	require.Equal(t, 2, querySingleInt(t, txCtx, client, "SELECT inviter_reward_count FROM user_affiliates WHERE user_id = $1", inviter.ID))
}

func TestAffiliateRepository_ResolveFirstRechargeBelowThresholdIsFinal(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewAffiliateRepository(client, integrationDB)

	inviter := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-resolve-inviter-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	invitee := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-resolve-invitee-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	now := time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC)
	bound, err := repo.BindInviter(txCtx, service.AffiliateBindInput{
		InviteeUserID: invitee.ID,
		InviterUserID: inviter.ID,
		RewardMode:    service.AffiliateRewardModeFirstRecharge,
		ValidityDays:  7,
		GrantedAt:     now,
	})
	require.NoError(t, err)
	require.True(t, bound.Bound)
	require.False(t, bound.Resolved)

	first, err := repo.ResolveFirstRecharge(txCtx, service.AffiliateSettlementInput{
		InviteeUserID: invitee.ID,
		Qualified:     false,
		InviterReward: 10,
		InviteeReward: 5,
		ValidityDays:  7,
		GrantedAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.True(t, first.Resolved)
	require.False(t, first.Qualified)

	second, err := repo.ResolveFirstRecharge(txCtx, service.AffiliateSettlementInput{
		InviteeUserID: invitee.ID,
		Qualified:     true,
		InviterReward: 10,
		InviteeReward: 5,
		ValidityDays:  7,
		GrantedAt:     now.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.False(t, second.Resolved)
	require.Equal(t, 0, querySingleInt(t, txCtx, client, "SELECT COUNT(*) FROM user_reward_credits WHERE user_id IN ($1, $2)", inviter.ID, invitee.ID))
}

func TestAffiliateRepository_ResolveFirstRechargeQualifiedRewardsBoth(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewAffiliateRepository(client, integrationDB)

	inviter := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-qualified-inviter-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	invitee := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("affiliate-qualified-invitee-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	now := time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC)
	_, err := repo.BindInviter(txCtx, service.AffiliateBindInput{
		InviteeUserID: invitee.ID,
		InviterUserID: inviter.ID,
		RewardMode:    service.AffiliateRewardModeFirstRecharge,
		ValidityDays:  7,
		GrantedAt:     now,
	})
	require.NoError(t, err)

	result, err := repo.ResolveFirstRecharge(txCtx, service.AffiliateSettlementInput{
		InviteeUserID:      invitee.ID,
		Qualified:          true,
		InviterReward:      10,
		InviteeReward:      5,
		ValidityDays:       7,
		InviterRewardLimit: 3,
		GrantedAt:          now.Add(time.Hour),
	})

	require.NoError(t, err)
	require.True(t, result.Resolved)
	require.True(t, result.Qualified)
	require.True(t, result.InviterRewarded)
	require.True(t, result.InviteeRewarded)
	require.InDelta(t, 10, querySingleFloat(t, txCtx, client, "SELECT balance::double precision FROM users WHERE id = $1", inviter.ID), 1e-9)
	require.InDelta(t, 5, querySingleFloat(t, txCtx, client, "SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.Equal(t, 1, querySingleInt(t, txCtx, client, "SELECT inviter_reward_count FROM user_affiliates WHERE user_id = $1", inviter.ID))
	require.Equal(t, 2, querySingleInt(t, txCtx, client, "SELECT COUNT(*) FROM user_reward_credits WHERE user_id IN ($1, $2)", inviter.ID, invitee.ID))
	require.Equal(t, 1, querySingleInt(t, txCtx, client, "SELECT COUNT(*) FROM user_affiliates WHERE user_id = $1 AND reward_status = 'resolved' AND inviter_rewarded AND invitee_rewarded", invitee.ID))
}

func TestAffiliateRepository_ConcurrentImmediateRewardsRespectInviterLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewAffiliateRepository(integrationEntClient, integrationDB)
	stamp := time.Now().UnixNano()
	inviter := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("affiliate-concurrent-inviter-%d@example.com", stamp), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	t.Cleanup(func() {
		_, _ = integrationEntClient.User.Delete().Where(user.IDEQ(inviter.ID)).Exec(context.Background())
	})

	const inviteeCount = 9
	invitees := make([]*service.User, 0, inviteeCount)
	for i := 0; i < inviteeCount; i++ {
		invitee := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("affiliate-concurrent-invitee-%d-%d@example.com", stamp, i), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
		invitees = append(invitees, invitee)
		t.Cleanup(func() {
			_, _ = integrationEntClient.User.Delete().Where(user.IDEQ(invitee.ID)).Exec(context.Background())
		})
	}

	start := make(chan struct{})
	results := make(chan service.AffiliateRewardResult, inviteeCount)
	errs := make(chan error, inviteeCount)
	var wg sync.WaitGroup
	for _, invitee := range invitees {
		invitee := invitee
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := repo.BindInviter(ctx, service.AffiliateBindInput{
				InviteeUserID:      invitee.ID,
				InviterUserID:      inviter.ID,
				RewardMode:         service.AffiliateRewardModeImmediate,
				InviterReward:      10,
				InviteeReward:      5,
				ValidityDays:       7,
				InviterRewardLimit: 3,
				GrantedAt:          time.Now().UTC(),
			})
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	boundCount, inviteeRewarded, inviterRewarded := 0, 0, 0
	for result := range results {
		if result.Bound {
			boundCount++
		}
		if result.InviteeRewarded {
			inviteeRewarded++
		}
		if result.InviterRewarded {
			inviterRewarded++
		}
	}
	require.Equal(t, inviteeCount, boundCount)
	require.Equal(t, inviteeCount, inviteeRewarded)
	require.Equal(t, 3, inviterRewarded)
	require.Equal(t, 3, querySingleInt(t, ctx, integrationEntClient, "SELECT inviter_reward_count FROM user_affiliates WHERE user_id = $1", inviter.ID))
	require.Equal(t, 3, querySingleInt(t, ctx, integrationEntClient, "SELECT COUNT(*) FROM user_reward_credits WHERE user_id = $1 AND credit_type = 'affiliate_inviter'", inviter.ID))
	require.Equal(t, inviteeCount, querySingleInt(t, ctx, integrationEntClient, "SELECT COUNT(*) FROM user_reward_credits WHERE credit_type = 'affiliate_invitee' AND user_id = ANY($1)", pq.Array(inviteeUserIDs(invitees))))
}

func querySingleFloat(t *testing.T, ctx context.Context, client *dbent.Client, query string, args ...any) float64 {
	t.Helper()
	rows, err := client.QueryContext(ctx, query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	require.True(t, rows.Next(), "expected one row")
	var value float64
	require.NoError(t, rows.Scan(&value))
	require.NoError(t, rows.Err())
	return value
}

func querySingleInt(t *testing.T, ctx context.Context, client *dbent.Client, query string, args ...any) int {
	t.Helper()
	rows, err := client.QueryContext(ctx, query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	require.True(t, rows.Next(), "expected one row")
	var value int
	require.NoError(t, rows.Scan(&value))
	require.NoError(t, rows.Err())
	return value
}

func TestAffiliateRepository_TransferQuotaToBalance_UsesClaimedQuotaBeforeClear(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-transfer-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Balance:      5.5,
		Concurrency:  5,
	})

	affCode := fmt.Sprintf("AFF%09d", time.Now().UnixNano()%1_000_000_000)
	_, err := client.ExecContext(txCtx, `
INSERT INTO user_affiliates (user_id, aff_code, aff_quota, aff_history_quota, created_at, updated_at)
VALUES ($1, $2, $3, $3, NOW(), NOW())`, u.ID, affCode, 12.34)
	require.NoError(t, err)

	transferred, balance, err := repo.TransferQuotaToBalance(txCtx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 12.34, transferred, 1e-9)
	require.InDelta(t, 17.84, balance, 1e-9)

	affQuota := querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", u.ID)
	require.InDelta(t, 0.0, affQuota, 1e-9)

	persistedBalance := querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", u.ID)
	require.InDelta(t, 17.84, persistedBalance, 1e-9)

	ledgerCount := querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_affiliate_ledger WHERE user_id = $1 AND action = 'transfer'", u.ID)
	require.Equal(t, 1, ledgerCount)

	rows, err := client.QueryContext(txCtx, `
SELECT amount::double precision,
       balance_after::double precision,
       aff_quota_after::double precision,
       aff_frozen_quota_after::double precision,
       aff_history_quota_after::double precision
FROM user_affiliate_ledger
WHERE user_id = $1 AND action = 'transfer'
LIMIT 1`, u.ID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next(), "expected transfer ledger")
	var amount, balanceAfter, quotaAfter, frozenAfter, historyAfter float64
	require.NoError(t, rows.Scan(&amount, &balanceAfter, &quotaAfter, &frozenAfter, &historyAfter))
	require.InDelta(t, 12.34, amount, 1e-9)
	require.InDelta(t, 17.84, balanceAfter, 1e-9)
	require.InDelta(t, 0.0, quotaAfter, 1e-9)
	require.InDelta(t, 0.0, frozenAfter, 1e-9)
	require.InDelta(t, 12.34, historyAfter, 1e-9)
}

// TestAffiliateRepository_BindInviter_ReusesOuterTransaction guards the
// cross-layer transaction propagation used by payment fulfillment.
func TestAffiliateRepository_BindInviter_ReusesOuterTransaction(t *testing.T) {
	ctx := context.Background()

	outerTx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err, "begin outer tx")
	// Defensive cleanup: if any require.* below fires before the explicit
	// Rollback, this prevents the tx from leaking until container teardown.
	// Rollback is idempotent at the driver level (extra rollback returns an
	// error we ignore).
	t.Cleanup(func() { _ = outerTx.Rollback() })
	client := outerTx.Client()
	txCtx := dbent.NewTxContext(ctx, outerTx)

	inviter := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-inviter-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})
	invitee := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-invitee-%d@example.com", time.Now().UnixNano()+1),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})

	repo := NewAffiliateRepository(client, integrationDB)
	_, err = repo.EnsureUserAffiliate(txCtx, inviter.ID)
	require.NoError(t, err)
	_, err = repo.EnsureUserAffiliate(txCtx, invitee.ID)
	require.NoError(t, err)

	bound, err := repo.BindInviter(txCtx, service.AffiliateBindInput{
		InviteeUserID: invitee.ID,
		InviterUserID: inviter.ID,
		RewardMode:    service.AffiliateRewardModeImmediate,
		InviterReward: 3.5,
		ValidityDays:  7,
		GrantedAt:     time.Now().UTC(),
	})
	require.NoError(t, err)
	require.True(t, bound.Bound, "invitee must bind to inviter")
	require.True(t, bound.InviterRewarded)

	// Visible inside the outer tx.
	innerBalance := querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", inviter.ID)
	require.InDelta(t, 3.5, innerBalance, 1e-9)

	// Roll back the outer tx; if AccrueQuota had opened its own inner tx and
	// committed it, the rows would still be visible to the global client.
	require.NoError(t, outerTx.Rollback())

	rows, err := integrationEntClient.QueryContext(ctx,
		"SELECT COUNT(*) FROM user_affiliates WHERE user_id IN ($1, $2)",
		inviter.ID, invitee.ID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
	var postRollbackCount int
	require.NoError(t, rows.Scan(&postRollbackCount))
	require.Equal(t, 0, postRollbackCount,
		"BindInviter must propagate the outer tx — found persisted rows after rollback")
}

func TestAffiliateRepository_TransferQuotaToBalance_EmptyQuota(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-empty-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Balance:      3.21,
		Concurrency:  5,
	})

	affCode := fmt.Sprintf("AFF%09d", time.Now().UnixNano()%1_000_000_000)
	_, err := client.ExecContext(txCtx, `
INSERT INTO user_affiliates (user_id, aff_code, aff_quota, aff_history_quota, created_at, updated_at)
VALUES ($1, $2, 0, 0, NOW(), NOW())`, u.ID, affCode)
	require.NoError(t, err)

	transferred, balance, err := repo.TransferQuotaToBalance(txCtx, u.ID)
	require.ErrorIs(t, err, service.ErrAffiliateQuotaEmpty)
	require.InDelta(t, 0.0, transferred, 1e-9)
	require.InDelta(t, 0.0, balance, 1e-9)

	persistedBalance := querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", u.ID)
	require.InDelta(t, 3.21, persistedBalance, 1e-9)
}

// TestAffiliateRepository_AdminCustomCode covers the success path of admin
// invite-code rewrite + reset within a shared test transaction:
// - UpdateUserAffCode replaces aff_code, sets aff_code_custom=true, lookup works
// - the old code can no longer be found
// - ResetUserAffCode reverts aff_code_custom and assigns a new system-format code
//
// The conflict path (duplicate code → ErrAffiliateCodeTaken) lives in its own
// test because a unique-violation aborts the surrounding Postgres tx, which
// would poison subsequent assertions in the same transaction.
func TestAffiliateRepository_AdminCustomCode(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-custom-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	})

	original, err := repo.EnsureUserAffiliate(txCtx, u.ID)
	require.NoError(t, err)
	require.False(t, original.AffCodeCustom, "system-generated codes start as non-custom")
	originalCode := original.AffCode

	// Rewrite to a custom code
	customCode := fmt.Sprintf("VIP%09d", time.Now().UnixNano()%1_000_000_000)
	require.NoError(t, repo.UpdateUserAffCode(txCtx, u.ID, customCode))

	updated, err := repo.EnsureUserAffiliate(txCtx, u.ID)
	require.NoError(t, err)
	require.Equal(t, customCode, updated.AffCode)
	require.True(t, updated.AffCodeCustom)

	// Lookup by new custom code finds the user
	byCode, err := repo.GetAffiliateByCode(txCtx, customCode)
	require.NoError(t, err)
	require.Equal(t, u.ID, byCode.UserID)

	// Old system code should no longer match
	_, err = repo.GetAffiliateByCode(txCtx, originalCode)
	require.ErrorIs(t, err, service.ErrAffiliateProfileNotFound)

	// Reset back to a fresh system code, clears custom flag
	newSysCode, err := repo.ResetUserAffCode(txCtx, u.ID)
	require.NoError(t, err)
	require.NotEqual(t, customCode, newSysCode)

	reset, err := repo.EnsureUserAffiliate(txCtx, u.ID)
	require.NoError(t, err)
	require.Equal(t, newSysCode, reset.AffCode)
	require.False(t, reset.AffCodeCustom)

	// The old custom code is now free again
	_, err = repo.GetAffiliateByCode(txCtx, customCode)
	require.ErrorIs(t, err, service.ErrAffiliateProfileNotFound)
}

// TestAffiliateRepository_AdminCustomCode_Conflict isolates the unique-violation
// path. PostgreSQL aborts the enclosing tx when a unique constraint fires, so
// this test must be the only assertion and run in its own tx — production
// callers each have their own outer tx, so this matches real behavior.
func TestAffiliateRepository_AdminCustomCode_Conflict(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	taker := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-conflict-taker-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser, Status: service.StatusActive,
	})
	requester := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-conflict-req-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser, Status: service.StatusActive,
	})

	takenCode := fmt.Sprintf("HOT%09d", time.Now().UnixNano()%1_000_000_000)
	require.NoError(t, repo.UpdateUserAffCode(txCtx, taker.ID, takenCode))

	// Now requester tries to grab the same code → conflict.
	err := repo.UpdateUserAffCode(txCtx, requester.ID, takenCode)
	require.ErrorIs(t, err, service.ErrAffiliateCodeTaken)
}

// TestAffiliateRepository_AdminRebateRate covers per-user exclusive rate
// set/clear and the Batch variant including NULL semantics.
func TestAffiliateRepository_AdminRebateRate(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u1 := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-rate-%d-a@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	})
	u2 := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-rate-%d-b@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	})

	// Set exclusive rate for u1
	rate := 42.5
	require.NoError(t, repo.SetUserRebateRate(txCtx, u1.ID, &rate))

	got, err := repo.EnsureUserAffiliate(txCtx, u1.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AffRebateRatePercent)
	require.InDelta(t, 42.5, *got.AffRebateRatePercent, 1e-9)

	// Clear exclusive rate
	require.NoError(t, repo.SetUserRebateRate(txCtx, u1.ID, nil))
	cleared, err := repo.EnsureUserAffiliate(txCtx, u1.ID)
	require.NoError(t, err)
	require.Nil(t, cleared.AffRebateRatePercent)

	// Batch set both users
	batchRate := 15.0
	require.NoError(t, repo.BatchSetUserRebateRate(txCtx, []int64{u1.ID, u2.ID}, &batchRate))

	for _, uid := range []int64{u1.ID, u2.ID} {
		v, err := repo.EnsureUserAffiliate(txCtx, uid)
		require.NoError(t, err)
		require.NotNil(t, v.AffRebateRatePercent)
		require.InDelta(t, 15.0, *v.AffRebateRatePercent, 1e-9)
	}

	// Batch clear
	require.NoError(t, repo.BatchSetUserRebateRate(txCtx, []int64{u1.ID, u2.ID}, nil))
	for _, uid := range []int64{u1.ID, u2.ID} {
		v, err := repo.EnsureUserAffiliate(txCtx, uid)
		require.NoError(t, err)
		require.Nil(t, v.AffRebateRatePercent)
	}
}

// TestAffiliateRepository_ListUsersWithCustomSettings verifies the admin list
// only includes users with at least one override applied.
func TestAffiliateRepository_ListUsersWithCustomSettings(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	// User without any custom config — should NOT appear in the list.
	plainEmail := fmt.Sprintf("affiliate-plain-%d@example.com", time.Now().UnixNano())
	uPlain := mustCreateUser(t, client, &service.User{
		Email: plainEmail, PasswordHash: "hash",
		Role: service.RoleUser, Status: service.StatusActive,
	})
	_, err := repo.EnsureUserAffiliate(txCtx, uPlain.ID)
	require.NoError(t, err)

	// User with a custom code — should appear.
	uCode := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-codeonly-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser, Status: service.StatusActive,
	})
	require.NoError(t, repo.UpdateUserAffCode(txCtx, uCode.ID, fmt.Sprintf("VIP%09d", time.Now().UnixNano()%1_000_000_000)))

	// User with only an exclusive rate — should appear.
	uRate := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("affiliate-rateonly-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser, Status: service.StatusActive,
	})
	r := 33.3
	require.NoError(t, repo.SetUserRebateRate(txCtx, uRate.ID, &r))

	entries, total, err := repo.ListUsersWithCustomSettings(txCtx, service.AffiliateAdminFilter{
		Page: 1, PageSize: 100,
	})
	require.NoError(t, err)

	// Build a quick lookup to assert per-user attributes (other tests may have
	// inserted custom rows in the same DB; we only care about our 3).
	byUserID := make(map[int64]service.AffiliateAdminEntry, len(entries))
	for _, e := range entries {
		byUserID[e.UserID] = e
	}

	require.NotContains(t, byUserID, uPlain.ID, "users without overrides must not appear")

	codeEntry, ok := byUserID[uCode.ID]
	require.True(t, ok, "custom-code user missing from list")
	require.True(t, codeEntry.AffCodeCustom)
	require.Nil(t, codeEntry.AffRebateRatePercent)

	rateEntry, ok := byUserID[uRate.ID]
	require.True(t, ok, "custom-rate user missing from list")
	require.False(t, rateEntry.AffCodeCustom)
	require.NotNil(t, rateEntry.AffRebateRatePercent)
	require.InDelta(t, 33.3, *rateEntry.AffRebateRatePercent, 1e-9)

	require.GreaterOrEqual(t, total, int64(2), "total must include at least our 2 custom rows")
}
