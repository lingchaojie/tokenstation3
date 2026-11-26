//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositoryKiroOveragePrecedenceAndAuthoritativeStatus(t *testing.T) {
	ctx := context.Background()
	const quotaReason = "OVERAGE_REQUEST_LIMIT_EXCEEDED"
	const authReason = "kiro auth failure (401): expired"

	for _, tc := range []struct {
		name         string
		platform     string
		overageFirst bool
		wantReason   string
	}{
		{name: "overage claims an existing longer cooldown", platform: service.PlatformKiro, wantReason: quotaReason},
		{name: "longer ordinary cooldown retains active overage reason", platform: service.PlatformKiro, overageFirst: true, wantReason: quotaReason},
		{name: "another platform keeps its normal cooldown ordering", platform: service.PlatformGemini, overageFirst: true, wantReason: authReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testEntTx(t)
			repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
			reader, ok := any(repo).(interface {
				GetActiveKiroOveragePark(context.Context, int64) (*time.Time, error)
			})
			require.True(t, ok, "Kiro quota park authority must read the database row")
			account := mustCreateAccount(t, tx.Client(), &service.Account{
				Name: "kiro-quota-authority", Platform: tc.platform, Type: service.AccountTypeOAuth,
			})
			now := time.Now().UTC().Truncate(time.Microsecond)
			monthlyUntil, authUntil := now.Add(2*time.Minute), now.Add(10*time.Minute)
			if tc.overageFirst {
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, monthlyUntil, quotaReason))
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, authUntil, authReason))
			} else {
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, authUntil, authReason))
				require.NoError(t, repo.SetTempUnschedulable(ctx, account.ID, monthlyUntil, quotaReason))
			}
			stored, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, &authUntil, stored.TempUnschedulableUntil)
			require.Equal(t, tc.wantReason, stored.TempUnschedulableReason)

			active, err := reader.GetActiveKiroOveragePark(ctx, account.ID)
			require.NoError(t, err)
			if tc.platform != service.PlatformKiro {
				require.Nil(t, active)
				return
			}
			require.Equal(t, &authUntil, active)

			cache := NewTempUnschedCache(testRedis(t))
			require.NoError(t, cache.SetTempUnsched(ctx, account.ID, &service.TempUnschedState{
				UntilUnix: authUntil.Add(time.Minute).Unix(), ErrorMessage: authReason,
			}))
			rate := service.NewRateLimitService(repo, nil, nil, nil, cache)
			status, err := rate.GetTempUnschedStatus(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, &service.TempUnschedState{UntilUnix: authUntil.Unix(), ErrorMessage: quotaReason}, status)

			require.NoError(t, rate.ClearTempUnschedulable(ctx, account.ID))
			require.NoError(t, cache.SetTempUnsched(ctx, account.ID, &service.TempUnschedState{
				UntilUnix: authUntil.Unix(), ErrorMessage: quotaReason,
			}))
			status, err = rate.GetTempUnschedStatus(ctx, account.ID)
			require.NoError(t, err)
			require.Nil(t, status)
		})
	}
}
