//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCheckInRepository_ConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewCheckInRepository(client)
	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("check-in-concurrent-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	})
	input := service.DailyCheckInClaimInput{
		UserID:          user.ID,
		ActivityStartAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		CheckInDate:     time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		RewardAmount:    10,
		ClaimedAt:       time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC),
		ExpiresAt:       time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC),
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.CreateClaim(ctx, input)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		if err == nil {
			successes++
		}
		if errors.Is(err, service.ErrDailyCheckInAlreadyClaimed) {
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	persisted, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 10, persisted.Balance, 1e-9)
}

func TestCheckInRepository_CreateClaimCreatesRewardCreditAtomically(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	repo := NewCheckInRepository(client)
	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("check-in-credit-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	})
	input := service.DailyCheckInClaimInput{
		UserID:          user.ID,
		ActivityStartAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		CheckInDate:     time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		RewardAmount:    10,
		ClaimedAt:       time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC),
		ExpiresAt:       time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC),
	}

	claim, err := repo.CreateClaim(txCtx, input)

	require.NoError(t, err)
	require.InDelta(t, 10, claim.BalanceAfter, 1e-9)
	rows, err := client.QueryContext(txCtx, `
SELECT credit_type, source_key, original_amount::double precision, granted_at, expires_at
FROM user_reward_credits
WHERE user_id = $1`, user.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var creditType, sourceKey string
	var amount float64
	var grantedAt, expiresAt time.Time
	require.NoError(t, rows.Scan(&creditType, &sourceKey, &amount, &grantedAt, &expiresAt))
	require.NoError(t, rows.Close())
	require.Equal(t, string(service.RewardCreditDailyCheckIn), creditType)
	require.Equal(t, fmt.Sprintf("daily-check-in:%d", claim.ID), sourceKey)
	require.InDelta(t, 10, amount, 1e-9)
	require.Equal(t, input.ClaimedAt, grantedAt.UTC())
	require.Equal(t, input.ExpiresAt, expiresAt.UTC())
	require.Equal(t, 1, querySingleInt(t, txCtx, client, "SELECT COUNT(*) FROM user_reward_credit_events WHERE user_id = $1 AND event_type = 'grant'", user.ID))
}

func TestCheckInRepository_RewardCreditFailureRollsBackClaimAndBalance(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewCheckInRepository(client)
	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("check-in-rollback-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})
	claimedAt := time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC)

	_, err := repo.CreateClaim(ctx, service.DailyCheckInClaimInput{
		UserID:          user.ID,
		ActivityStartAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		CheckInDate:     time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		RewardAmount:    10,
		ClaimedAt:       claimedAt,
		ExpiresAt:       claimedAt,
	})

	require.Error(t, err)
	require.Equal(t, 0, querySingleInt(t, ctx, client, "SELECT COUNT(*) FROM daily_check_in_claims WHERE user_id = $1", user.ID))
	require.Equal(t, 0, querySingleInt(t, ctx, client, "SELECT COUNT(*) FROM user_reward_credits WHERE user_id = $1", user.ID))
	require.InDelta(t, 0, querySingleFloat(t, ctx, client, "SELECT balance::double precision FROM users WHERE id = $1", user.ID), 1e-9)
}
