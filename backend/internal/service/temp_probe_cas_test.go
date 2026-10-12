//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The stored row and loaded probe accounts are separate, as they are in PostgreSQL.
type tempProbeCASRepo struct {
	AccountRepository
	stored  *Account
	onExtra func(context.Context)
	casErr  error
	clears  int
}

func (r *tempProbeCASRepo) GetByID(context.Context, int64) (*Account, error) {
	if r.stored == nil {
		return nil, ErrAccountNotFound
	}
	copy := *r.stored
	if copy.TempUnschedulableUntil != nil {
		until := *copy.TempUnschedulableUntil
		copy.TempUnschedulableUntil = &until
	}
	return &copy, nil
}

func (r *tempProbeCASRepo) UpdateExtra(ctx context.Context, _ int64, _ map[string]any) error {
	if r.onExtra != nil {
		r.onExtra(ctx)
	}
	return nil
}

func (r *tempProbeCASRepo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	if r.stored.TempUnschedulableUntil == nil || r.stored.TempUnschedulableUntil.Before(until) {
		r.stored.TempUnschedulableUntil, r.stored.TempUnschedulableReason = &until, reason
	}
	return nil
}

func (r *tempProbeCASRepo) ClearTempUnschedulable(context.Context, int64) error {
	r.clears++
	r.stored.TempUnschedulableUntil, r.stored.TempUnschedulableReason = nil, ""
	return nil
}

func (r *tempProbeCASRepo) CompareAndSetTempUnschedulable(ctx context.Context, id int64, expectedUntil *time.Time, expectedReason string, until *time.Time, reason string) (bool, error) {
	if r.casErr != nil {
		return false, r.casErr
	}
	if r.stored == nil || r.stored.ID != id || r.stored.TempUnschedulableReason != expectedReason {
		return false, nil
	}
	actual := r.stored.TempUnschedulableUntil
	if (actual == nil) != (expectedUntil == nil) || (actual != nil && !actual.Equal(*expectedUntil)) {
		return false, nil
	}
	if until == nil {
		return true, r.ClearTempUnschedulable(ctx, id)
	}
	copy := *until
	r.stored.TempUnschedulableUntil, r.stored.TempUnschedulableReason = &copy, reason
	return true, nil
}

type tempProbeQuotaFunc func(context.Context, int64) (*CNProviderQuotaProbeResult, error)

func (f tempProbeQuotaFunc) QueryUsage(ctx context.Context, id int64) (*CNProviderQuotaProbeResult, error) {
	return f(ctx, id)
}

func tempProbeAccount(reason string, until *time.Time) *Account {
	account := commandCodeUsageAccount()
	account.Schedulable = true
	account.TempUnschedulableUntil, account.TempUnschedulableReason = until, reason
	return account
}

func injectTempProbeSpendCap(t *testing.T, ctx context.Context, repo *tempProbeCASRepo) {
	t.Helper()
	requestAccount, err := repo.GetByID(ctx, repo.stored.ID)
	require.NoError(t, err)
	NewRateLimitService(repo, nil, &config.Config{}, nil, nil).HandleUpstreamError(ctx, requestAccount,
		http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached the spending limit your organization set. It resets in 10 minutes."}}`))
	require.Equal(t, commandCodeSpendLimitReason, repo.stored.TempUnschedulableReason)
}

func TestCommandCodeTopUpProbeCASPreservesRequestRestriction(t *testing.T) {
	for _, mode := range []string{"quota", "balance"} {
		t.Run(mode, func(t *testing.T) {
			until := time.Now().Add(5 * time.Minute)
			repo := &tempProbeCASRepo{stored: tempProbeAccount(commandCodeUsageLimitReason+": old window", &until)}
			repo.onExtra = func(ctx context.Context) { injectTempProbeSpendCap(t, ctx, repo) }
			upstream := newCommandCodeAlphaUpstream(`{"org":null}`)
			upstream.responses["/alpha/billing/credits"] = commandCodeAlphaResponse{status: http.StatusOK, body: commandCodeCreditsBody(20)}
			if mode == "quota" {
				result, err := NewCNProviderQuotaService(repo, nil, upstream, &config.Config{}).QueryUsage(context.Background(), repo.stored.ID)
				require.NoError(t, err)
				require.True(t, result.Success)
			} else {
				result, err := NewCNProviderBalanceService(repo, nil, upstream, &config.Config{}).QueryBalance(context.Background(), repo.stored.ID)
				require.NoError(t, err)
				require.True(t, result.Success)
			}
			require.Equal(t, commandCodeSpendLimitReason, repo.stored.TempUnschedulableReason, "top-up cannot prove that an organization spend cap recovered")
			require.NotNil(t, repo.stored.TempUnschedulableUntil)
			require.Zero(t, repo.clears)
		})
	}
}

func TestCNBalanceProbeCASPreservesConcurrentRestriction(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		for _, change := range []string{"request_spend_cap", "same_reason_new_deadline", "opaque_rule"} {
			t.Run(map[bool]string{false: "low", true: "healthy"}[healthy]+"/"+change, func(t *testing.T) {
				until := time.Now().Add(-time.Minute)
				if healthy {
					until = time.Now().Add(5 * time.Minute)
				}
				repo := &tempProbeCASRepo{stored: tempProbeAccount(cnBalanceLowReason("previous probe"), &until)}
				snapshot, err := repo.GetByID(context.Background(), repo.stored.ID)
				require.NoError(t, err)
				balance := 0.2
				if healthy {
					balance = 20
				}
				var concurrentUntil time.Time
				var concurrentReason string
				prober := tempProbeQuotaFunc(func(ctx context.Context, _ int64) (*CNProviderQuotaProbeResult, error) {
					if change == "request_spend_cap" {
						injectTempProbeSpendCap(t, ctx, repo)
					} else {
						reason := repo.stored.TempUnschedulableReason
						if change == "opaque_rule" {
							reason = "administrator rule"
						}
						require.NoError(t, repo.SetTempUnschedulable(ctx, repo.stored.ID, time.Now().Add(10*time.Minute), reason))
					}
					concurrentUntil, concurrentReason = *repo.stored.TempUnschedulableUntil, repo.stored.TempUnschedulableReason
					return &CNProviderQuotaProbeResult{Success: true, Balance: &CNProviderBalanceResult{Success: true, Available: true, Balance: balance, Currency: "USD"}}, nil
				})
				svc := &CNProviderBalanceCheckService{accountRepo: repo, quotaService: prober, cfg: &config.Config{}}
				outcome := svc.checkCommandCode(context.Background(), snapshot, 0.5)
				require.Equal(t, cnBalanceNoChange, outcome, "a rejected probe does not count as a state change")
				require.Equal(t, concurrentReason, repo.stored.TempUnschedulableReason)
				require.True(t, concurrentUntil.Equal(*repo.stored.TempUnschedulableUntil))
				require.True(t, until.Equal(*snapshot.TempUnschedulableUntil), "rejected probe preserves detached snapshot")
				require.Equal(t, cnBalanceLowReason("previous probe"), snapshot.TempUnschedulableReason)
				require.Zero(t, repo.clears)
			})
		}
	}
}

func TestCNBalanceProbeCASAppliedAndFailClosed(t *testing.T) {
	for _, platform := range []string{PlatformCommandCode, PlatformKimi, PlatformDeepseek} {
		for _, tc := range []struct {
			name                            string
			healthy                         bool
			reason                          string
			expired, casError, noCapability bool
			want                            cnBalanceCheckOutcome
		}{
			{name: "set", want: cnBalancePaused},
			{name: "recover", healthy: true, reason: cnBalanceLowReason("previous"), want: cnBalanceCleared},
			{name: "expired_rearm", reason: cnBalanceLowReason("previous"), expired: true, want: cnBalancePaused},
			{name: "opaque_active", reason: "opaque rule"},
			{name: "healthy_spend_cap", healthy: true, reason: commandCodeSpendLimitReason},
			{name: "set_error", casError: true},
			{name: "clear_error", healthy: true, reason: cnBalanceLowReason("previous"), casError: true},
			{name: "set_missing_capability", noCapability: true},
			{name: "clear_missing_capability", healthy: true, reason: cnBalanceLowReason("previous"), noCapability: true},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				var until *time.Time
				if tc.reason != "" {
					value := time.Now().Add(time.Hour)
					if tc.expired {
						value = time.Now().Add(-time.Hour)
					}
					until = &value
				}
				repo := &tempProbeCASRepo{stored: tempProbeAccount(tc.reason, until)}
				repo.stored.Platform = platform
				if tc.casError {
					repo.casErr = errors.New("CAS unavailable")
				}
				snapshot, err := repo.GetByID(context.Background(), repo.stored.ID)
				require.NoError(t, err)
				var repository AccountRepository = repo
				if tc.noCapability {
					repository = &struct{ AccountRepository }{repo}
				}
				svc := &CNProviderBalanceCheckService{accountRepo: repository, cfg: &config.Config{}}
				balance := 0.2
				if tc.healthy {
					balance = 20
				}
				outcome := svc.applyBalanceResult(context.Background(), snapshot, &CNProviderBalanceResult{Success: true, Available: true, Balance: balance, Currency: "USD"}, 0.5)
				require.Equal(t, tc.want, outcome)
				if tc.want == cnBalanceCleared {
					require.Nil(t, repo.stored.TempUnschedulableUntil)
					require.Nil(t, snapshot.TempUnschedulableUntil)
					require.Empty(t, snapshot.TempUnschedulableReason)
				} else if tc.want == cnBalancePaused {
					require.NotNil(t, repo.stored.TempUnschedulableUntil)
					require.Contains(t, repo.stored.TempUnschedulableReason, cnBalanceLowReasonPrefix)
					require.Equal(t, repo.stored.TempUnschedulableReason, snapshot.TempUnschedulableReason)
					require.True(t, repo.stored.TempUnschedulableUntil.Equal(*snapshot.TempUnschedulableUntil))
					require.Zero(t, snapshot.TempUnschedulableUntil.Nanosecond()%1000, "replacement uses persisted microsecond precision")
				} else {
					require.Equal(t, tc.reason, repo.stored.TempUnschedulableReason)
					require.Equal(t, until, repo.stored.TempUnschedulableUntil)
					require.Equal(t, until, snapshot.TempUnschedulableUntil)
					require.Equal(t, tc.reason, snapshot.TempUnschedulableReason)
				}
			})
		}
	}
}

func TestCommandCodeTopUpProbeCASFailClosed(t *testing.T) {
	for _, tc := range []string{"error", "no_capability", "nil_account", "empty_account"} {
		t.Run(tc, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			repo := &tempProbeCASRepo{stored: tempProbeAccount(commandCodeUsageLimitReason, &until)}
			snapshot, err := repo.GetByID(context.Background(), repo.stored.ID)
			require.NoError(t, err)
			var repository AccountRepository = repo
			switch tc {
			case "error":
				repo.casErr = errors.New("CAS failed")
			case "no_capability":
				repository = &struct{ AccountRepository }{repo}
			case "nil_account":
				snapshot = nil
			case "empty_account":
				snapshot.ID = 0
			}
			usage := &commandCodeUsage{credits: commandCodeCreditsPayload{Credits: &commandCodeCredits{PurchasedCredits: 20}}}
			var applied bool
			require.NotPanics(t, func() {
				applied = clearCommandCodeWindowCooldown(context.Background(), repository, snapshot, usage, time.Now())
			})
			require.False(t, applied)
			require.Zero(t, repo.clears)
			require.Equal(t, commandCodeUsageLimitReason, repo.stored.TempUnschedulableReason)
			require.True(t, until.Equal(*repo.stored.TempUnschedulableUntil))
		})
	}
}
