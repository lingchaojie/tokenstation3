//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
