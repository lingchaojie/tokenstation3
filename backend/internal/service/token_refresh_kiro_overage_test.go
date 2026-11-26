//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type kiroOverageRecoveryRepo struct {
	tokenRefreshAccountRepo
	schedulerCache       *tokenRefreshSchedulerCache
	afterCredentialWrite func()
	beforeRateLimitClear func()
}

func (r *kiroOverageRecoveryRepo) publish(ctx context.Context, id int64) error {
	if r.schedulerCache == nil {
		return nil
	}
	return r.schedulerCache.SetAccount(ctx, r.accountsByID[id])
}

func (r *kiroOverageRecoveryRepo) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	if err := r.tokenRefreshAccountRepo.UpdateCredentials(ctx, id, credentials); err != nil {
		return err
	}
	if r.afterCredentialWrite != nil {
		r.afterCredentialWrite()
	}
	return r.publish(ctx, id)
}

func (r *kiroOverageRecoveryRepo) ClearTempUnschedulable(ctx context.Context, id int64) error {
	account := r.accountsByID[id]
	account.TempUnschedulableUntil = nil
	account.TempUnschedulableReason = ""
	return r.publish(ctx, id)
}

func (r *kiroOverageRecoveryRepo) ClearTempUnschedulableUnlessActiveReason(ctx context.Context, id int64, reason string) (bool, error) {
	account := r.accountsByID[id]
	until := account.TempUnschedulableUntil
	if until == nil || (account.TempUnschedulableReason == reason && time.Now().Before(*until)) {
		return false, nil
	}
	return true, r.ClearTempUnschedulable(ctx, id)
}

func (r *kiroOverageRecoveryRepo) ClearRateLimit(_ context.Context, id int64) error {
	if r.beforeRateLimitClear != nil {
		r.beforeRateLimitClear()
	}
	r.accountsByID[id].RateLimitResetAt = nil
	return nil
}

func TestTokenRefreshService_KiroOverageRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                string
		observedOverage     bool
		observedNoCooldown  bool
		concurrentOverage   bool
		wantPark            bool
		wantClear           int
		wantSchedulerWrites int
	}{
		{name: "monthly park already observed", observedOverage: true, wantPark: true, wantSchedulerWrites: 1},
		{name: "monthly park installed during credential persistence", concurrentOverage: true, wantPark: true, wantSchedulerWrites: 1},
		{name: "monthly park installed during a routine refresh", observedNoCooldown: true, concurrentOverage: true, wantPark: true, wantSchedulerWrites: 1},
		{name: "auth cooldown recovered", wantClear: 1, wantSchedulerWrites: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			authUntil, parkUntil := now.Add(10*time.Minute), now.Add(72*time.Hour)
			const authReason = "kiro auth failure (401): expired"
			const parkReason = "OVERAGE_REQUEST_LIMIT_EXCEEDED"
			stored := &Account{
				ID: 15, Platform: PlatformKiro, Type: AccountTypeOAuth,
				TempUnschedulableUntil: &authUntil, TempUnschedulableReason: authReason,
			}
			if tc.observedOverage {
				stored.TempUnschedulableUntil, stored.TempUnschedulableReason = &parkUntil, parkReason
			} else if tc.observedNoCooldown {
				stored.TempUnschedulableUntil, stored.TempUnschedulableReason = nil, ""
			}
			snapshot := snapshotOAuthRefreshAccount(stored)
			scheduler := &tokenRefreshSchedulerCache{}
			repo := &kiroOverageRecoveryRepo{schedulerCache: scheduler}
			repo.accountsByID = map[int64]*Account{stored.ID: stored}
			if tc.concurrentOverage {
				repo.afterCredentialWrite = func() {
					stored.TempUnschedulableUntil, stored.TempUnschedulableReason = &parkUntil, parkReason
				}
			}
			invalidator := &tokenCacheInvalidatorStub{}
			tempCache := &tempUnschedCacheStub{}
			blocker := &tokenRefreshRuntimeBlocker{}
			cfg := &config.Config{TokenRefresh: config.TokenRefreshConfig{MaxRetries: 1}}
			svc := NewTokenRefreshService(repo, nil, nil, nil, nil, nil, invalidator, scheduler, cfg, tempCache)
			svc.SetAccountRuntimeBlocker(blocker)
			refresher := &tokenRefresherStub{credentials: map[string]any{
				"access_token": "new-local-token",
				"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/local-fixture",
			}}

			require.NoError(t, svc.refreshWithRetry(ctx, snapshot, refresher, refresher, time.Hour))
			require.NotNil(t, scheduler.lastAccount)
			for _, account := range []*Account{stored, scheduler.lastAccount} {
				require.Equal(t, "new-local-token", account.GetCredential("access_token"))
				if tc.wantPark {
					require.Equal(t, &parkUntil, account.TempUnschedulableUntil)
					require.Equal(t, parkReason, account.TempUnschedulableReason)
				} else {
					require.Nil(t, account.TempUnschedulableUntil)
					require.Empty(t, account.TempUnschedulableReason)
				}
			}
			require.Equal(t, tc.wantClear, tempCache.deleteCalls)
			require.Equal(t, tc.wantClear, blocker.clearCalls)
			require.Equal(t, tc.wantSchedulerWrites, scheduler.setAccountCalls)
			require.Equal(t, 1, invalidator.calls)
		})
	}
}

func TestScheduledTestRunner_KiroOverageRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		auto     bool
		racePark bool
		wantPark bool
	}{
		{name: "auto recovery preserves an existing park", auto: true, wantPark: true},
		{name: "auto recovery preserves a newly persisted park", auto: true, racePark: true, wantPark: true},
		{name: "manual test recovery clears a park"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			authUntil, parkUntil := now.Add(10*time.Minute), now.Add(72*time.Hour)
			const parkReason = "OVERAGE_REQUEST_LIMIT_EXCEEDED"
			stored := &Account{
				ID: 15, Platform: PlatformKiro, Type: AccountTypeAPIKey, Status: StatusActive,
				TempUnschedulableUntil: &parkUntil, TempUnschedulableReason: parkReason,
				RateLimitResetAt: &authUntil,
			}
			repo := &kiroOverageRecoveryRepo{}
			repo.accountsByID = map[int64]*Account{stored.ID: stored}
			repo.snapshotReads = true
			if tc.racePark {
				stored.TempUnschedulableUntil, stored.TempUnschedulableReason = &authUntil, "kiro auth failure (401): expired"
				repo.beforeRateLimitClear = func() {
					stored.TempUnschedulableUntil, stored.TempUnschedulableReason = &parkUntil, parkReason
				}
			}
			tempCache := &tempUnschedCacheStub{}
			blocker := &tokenRefreshRuntimeBlocker{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, tempCache)
			svc.SetAccountRuntimeBlocker(blocker)

			if tc.auto {
				runner := &ScheduledTestRunnerService{rateLimitSvc: svc}
				runner.tryRecoverAccount(ctx, stored.ID, 1)
			} else {
				result, err := svc.RecoverAccountAfterSuccessfulTest(ctx, stored.ID)
				require.NoError(t, err)
				require.True(t, result.ClearedRateLimit)
			}

			require.Nil(t, stored.RateLimitResetAt)
			if tc.wantPark {
				require.Equal(t, &parkUntil, stored.TempUnschedulableUntil)
				require.Equal(t, parkReason, stored.TempUnschedulableReason)
				require.Zero(t, tempCache.deleteCalls)
				require.Zero(t, blocker.clearCalls)
			} else {
				require.Nil(t, stored.TempUnschedulableUntil)
				require.Empty(t, stored.TempUnschedulableReason)
				require.Equal(t, 1, tempCache.deleteCalls)
				require.Equal(t, 1, blocker.clearCalls)
			}
		})
	}
}
