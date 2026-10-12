//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// UpdateExtra is a deterministic boundary after the HTTP calls but before the
// wallet write. GetByID returns a detached snapshot, just like the real repo.
type clineConcurrentWalletRepo struct {
	*clineAccountRepo
	beforeWalletWrite func(*Account)
}

func (r *clineConcurrentWalletRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	if err := r.clineAccountRepo.UpdateExtra(ctx, id, updates); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeWalletWrite(&r.accounts[0])
	return nil
}

func TestClineUsageProbePreservesConcurrentWalletRestrictions(t *testing.T) {
	for _, tc := range []struct{ name, scope, reason string }{
		{"credits_spend", clineCreditsRateLimitKey, "cline_spend_limit: organization limit"},
		{"credits_inference", clineCreditsRateLimitKey, "cline_inference_cap: inference limit"},
		{"pass_inference", clinePassRateLimitKey, "cline_inference_cap: inference limit"},
		{"pass_entitlement", clinePassRateLimitKey, "cline_pass_unavailable: organization accounts cannot subscribe"},
	} {
		for _, healthy := range []bool{false, true} {
			for _, observed := range []string{"probe_owned", "expired", "missing", "null"} {
				if healthy && observed != "probe_owned" {
					continue // A healthy probe only clears an active probe-owned block.
				}
				t.Run(fmt.Sprintf("%s/healthy_%t/%s", tc.name, healthy, observed), func(t *testing.T) {
					now := time.Now().UTC().Truncate(time.Second)
					account := clineTestAccount(91)
					oldReason := "cline_credits_exhausted: insufficient credits"
					if tc.scope == clinePassRateLimitKey {
						oldReason = "cline_pass_limit: weekly window used up"
					}
					if observed == "probe_owned" || observed == "expired" {
						reset := now.Add(time.Hour)
						if observed == "expired" {
							reset = now.Add(-time.Minute)
							oldReason = "opaque expired restriction"
						}
						setAccountModelRateLimitSnapshot(account, tc.scope, reset, oldReason, now.Add(-time.Hour))
					} else if observed == "null" {
						account.Extra = map[string]any{"model_rate_limits": map[string]any{tc.scope: nil}}
					}
					if observed == "missing" || observed == "null" {
						oldReason = ""
					}
					base := &clineAccountRepo{accounts: []Account{*account}}
					concurrentReset := now.Add(2 * time.Hour)
					repo := &clineConcurrentWalletRepo{clineAccountRepo: base, beforeWalletWrite: func(stored *Account) {
						setAccountModelRateLimitSnapshot(stored, tc.scope, concurrentReset, tc.reason, now)
					}}
					upstream := clineWalletShortageUpstream(tc.scope, false, now)
					if healthy {
						upstream = newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
					}
					result, err := NewCNProviderQuotaService(repo, nil, upstream, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
					require.NoError(t, err)
					require.True(t, result.Success)
					require.True(t, result.Persisted, "quota/balance persistence is independent of a rejected wallet write")
					require.Equal(t, tc.reason, base.accounts[0].modelRateLimitReason(tc.scope))
					require.Equal(t, concurrentReset, *base.accounts[0].modelRateLimitResetAt(tc.scope))
					require.Equal(t, oldReason, base.lastSnapshot.modelRateLimitReason(tc.scope), "a rejected CAS must not mutate the detached snapshot")
					if observed == "missing" || observed == "null" {
						require.Nil(t, base.lastSnapshot.modelRateLimitResetAt(tc.scope))
					}
				})
			}
		}
	}
}

func TestClineUsageProbePreservesConcurrentSameReasonGeneration(t *testing.T) {
	for _, scope := range []string{clineCreditsRateLimitKey, clinePassRateLimitKey} {
		for _, field := range []string{"rate_limit_reset_at", "rate_limited_at", "opaque_metadata"} {
			t.Run(scope+"/"+field, func(t *testing.T) {
				now := time.Now().UTC().Truncate(time.Second)
				account := clineTestAccount(92)
				reason := "cline_credits_exhausted: insufficient credits"
				if scope == clinePassRateLimitKey {
					reason = "cline_pass_limit: weekly window used up"
				}
				setAccountModelRateLimitSnapshot(account, scope, now.Add(time.Hour), reason, now.Add(-time.Hour))
				base := &clineAccountRepo{accounts: []Account{*account}}
				var concurrent []byte
				repo := &clineConcurrentWalletRepo{clineAccountRepo: base, beforeWalletWrite: func(stored *Account) {
					entry := stored.Extra["model_rate_limits"].(map[string]any)[scope].(map[string]any)
					entry[field] = "2099-01-01T00:00:00Z"
					concurrent, _ = json.Marshal(entry)
				}}
				upstream := newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
				_, err := NewCNProviderQuotaService(repo, nil, upstream, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
				require.NoError(t, err)
				stored, err := json.Marshal(base.accounts[0].Extra["model_rate_limits"].(map[string]any)[scope])
				require.NoError(t, err)
				require.JSONEq(t, string(concurrent), string(stored), "the entire scope generation must match")
			})
		}
	}
}

// This adapter intentionally exposes only AccountRepository, so the optional
// CAS capability is unavailable even though the wrapped test repo supports it.
type clineWalletRepoWithoutCAS struct{ AccountRepository }

func TestClineUsageProbeSuccessfulWriteKeepsSnapshotGeneration(t *testing.T) {
	for _, scope := range []string{clineCreditsRateLimitKey, clinePassRateLimitKey} {
		t.Run(scope, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			repo := &clineAccountRepo{accounts: []Account{*clineTestAccount(94)}}
			snapshot, err := repo.GetByID(context.Background(), 94)
			require.NoError(t, err)
			svc := NewCNProviderQuotaService(repo, nil, nil, clineUsageTestConfig())
			tiers := []CNQuotaTier{{Window: "weekly", UsedPercent: 10}}
			balance := &CNProviderBalanceResult{Balance: 5}
			if scope == clinePassRateLimitKey {
				tiers[0].UsedPercent = 100
			} else {
				balance.Balance = 0.2
			}
			svc.applyClineWallets(context.Background(), snapshot, true, tiers, balance, now)
			storedJSON, err := json.Marshal(repo.accounts[0].Extra["model_rate_limits"])
			require.NoError(t, err)
			snapshotJSON, err := json.Marshal(snapshot.Extra["model_rate_limits"])
			require.NoError(t, err)
			require.JSONEq(t, string(storedJSON), string(snapshotJSON), "successful DB and detached snapshot writes use one generation")
			tiers[0].UsedPercent = 10
			balance.Balance = 5
			svc.applyClineWallets(context.Background(), snapshot, true, tiers, balance, now.Add(time.Second))
			require.False(t, repo.accounts[0].isRateLimitActiveForKey(scope), "the same detached account can recover after its own applied write")
		})
	}
}

func TestClineUsageProbeWalletWriteFailurePreservesSnapshots(t *testing.T) {
	for _, scope := range []string{clineCreditsRateLimitKey, clinePassRateLimitKey} {
		for _, healthy := range []bool{false, true} {
			for _, capability := range []string{"error", "unavailable"} {
				t.Run(fmt.Sprintf("%s/healthy_%t/%s", scope, healthy, capability), func(t *testing.T) {
					now := time.Now().UTC().Truncate(time.Second)
					account := clineTestAccount(93)
					reason := "cline_credits_exhausted: insufficient credits"
					if scope == clinePassRateLimitKey {
						reason = "cline_pass_limit: weekly window used up"
					}
					setAccountModelRateLimitSnapshot(account, scope, now.Add(time.Hour), reason, now)
					original, err := json.Marshal(account.Extra)
					require.NoError(t, err)
					base := &clineAccountRepo{accounts: []Account{*account}}
					var repo AccountRepository = base
					if capability == "error" {
						base.casError = errors.New("CAS unavailable")
					} else {
						repo = &clineWalletRepoWithoutCAS{AccountRepository: base}
					}
					upstream := clineWalletShortageUpstream(scope, false, now)
					if healthy {
						upstream = newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
					}
					_, err = NewCNProviderQuotaService(repo, nil, upstream, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
					require.NoError(t, err)
					snapshot, err := json.Marshal(base.lastSnapshot.Extra)
					require.NoError(t, err)
					require.JSONEq(t, string(original), string(snapshot))
					require.Equal(t, reason, base.accounts[0].modelRateLimitReason(scope))
					require.Equal(t, now.Add(time.Hour), *base.accounts[0].modelRateLimitResetAt(scope))
				})
			}
		}
	}
}

// Probe responses can prove balance/window recovery, but cannot prove that an
// organization spend cap, inference cap or entitlement restriction recovered.
// A shortage followed by a healthy probe must preserve those active blocks.
func TestClineUsageProbeShortageThenHealthyPreservesOpaqueWalletLimits(t *testing.T) {
	for _, tc := range []struct {
		name, scope, model, reason string
		unsubscribed               bool
	}{
		{"credits_spend", clineCreditsRateLimitKey, "deepseek/deepseek-v4-flash", "cline_spend_limit: organization limit", false},
		{"credits_inference", clineCreditsRateLimitKey, "deepseek/deepseek-v4-flash", "cline_inference_cap: inference limit", false},
		{"credits_unknown", clineCreditsRateLimitKey, "deepseek/deepseek-v4-flash", "opaque_credits_limit", false},
		{"pass_inference", clinePassRateLimitKey, "cline-pass/glm-5.3", "cline_inference_cap: inference limit", false},
		{"pass_entitlement", clinePassRateLimitKey, "cline-pass/glm-5.3", "cline_pass_unavailable: organization accounts cannot subscribe", true},
		{"pass_unknown", clinePassRateLimitKey, "cline-pass/glm-5.3", "opaque_pass_limit", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			originalReset := now.Add(time.Hour)
			account := clineTestAccount(81)
			setAccountModelRateLimitSnapshot(account, tc.scope, originalReset, tc.reason, now)
			repo := &clineAccountRepo{accounts: []Account{*account}}
			stored := &repo.accounts[0]
			shortage := clineWalletShortageUpstream(tc.scope, tc.unsubscribed, now)
			result, err := NewCNProviderQuotaService(repo, nil, shortage, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.True(t, result.Success)
			shortageReason := stored.modelRateLimitReason(tc.scope)
			shortageReset := stored.modelRateLimitResetAt(tc.scope)

			healthy := newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
			result, err = NewCNProviderQuotaService(repo, nil, healthy, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.True(t, result.Success)

			require.Equal(t, tc.reason, shortageReason, "shortage must not replace an unproven restriction")
			require.NotNil(t, shortageReset)
			require.Equal(t, originalReset, *shortageReset)
			require.Equal(t, tc.reason, stored.modelRateLimitReason(tc.scope), "a healthy probe does not prove this restriction recovered")
			require.Equal(t, originalReset, *stored.modelRateLimitResetAt(tc.scope))
			require.True(t, stored.isModelRateLimitedWithContext(context.Background(), tc.model))
		})
	}
}

func TestClineUsageProbeShortageReplacesExpiredOpaqueWalletLimits(t *testing.T) {
	for _, scope := range []string{clinePassRateLimitKey, clineCreditsRateLimitKey} {
		t.Run(scope, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			account := clineTestAccount(82)
			setAccountModelRateLimitSnapshot(account, scope, now.Add(-time.Minute), "cline_inference_cap: expired", now.Add(-time.Hour))
			repo := &clineAccountRepo{accounts: []Account{*account}}
			stored := &repo.accounts[0]
			shortage := clineWalletShortageUpstream(scope, false, now)
			_, err := NewCNProviderQuotaService(repo, nil, shortage, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			wantPrefix := "cline_pass_limit:"
			if scope == clineCreditsRateLimitKey {
				wantPrefix = "cline_credits_low:"
			}
			require.True(t, strings.HasPrefix(stored.modelRateLimitReason(scope), wantPrefix))
			require.True(t, stored.isRateLimitActiveForKey(scope))

			healthy := newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
			_, err = NewCNProviderQuotaService(repo, nil, healthy, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.False(t, stored.isRateLimitActiveForKey(scope), "a fresh probe-owned shortage can recover normally")
		})
	}
}

func TestClineUsageProbeShortageThenHealthyRecoversProvableWalletLimits(t *testing.T) {
	for _, tc := range []struct {
		name, scope, reason string
		unsubscribed        bool
	}{
		{"pass_window", clinePassRateLimitKey, "cline_pass_limit: weekly window used up", false},
		{"pass_subscription", clinePassRateLimitKey, "cline_pass_unavailable: no ClinePass subscription", true},
		{"credits_exhausted", clineCreditsRateLimitKey, "cline_credits_exhausted: insufficient credits", false},
		{"credits_low", clineCreditsRateLimitKey, "cline_credits_low: balance below threshold", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			account := clineTestAccount(83)
			setAccountModelRateLimitSnapshot(account, tc.scope, now.Add(time.Hour), tc.reason, now)
			repo := &clineAccountRepo{accounts: []Account{*account}}
			shortage := clineWalletShortageUpstream(tc.scope, tc.unsubscribed, now)
			_, err := NewCNProviderQuotaService(repo, nil, shortage, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.True(t, repo.accounts[0].isRateLimitActiveForKey(tc.scope))

			healthy := newClineAccountUpstream(commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}, `{"balance":5000000}`)
			_, err = NewCNProviderQuotaService(repo, nil, healthy, clineUsageTestConfig()).QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.False(t, repo.accounts[0].isRateLimitActiveForKey(tc.scope))
		})
	}
}

func clineWalletShortageUpstream(scope string, unsubscribed bool, now time.Time) *commandCodeAlphaUpstream {
	usage := commandCodeAlphaResponse{status: http.StatusOK, body: clineUsageLimitsBody}
	balance := `{"balance":5000000}`
	if scope == clinePassRateLimitKey {
		usage.body = `{"limits":[{"type":"weekly","percentUsed":100,"resetsAt":"` + now.Add(30*time.Minute).Format(time.RFC3339) + `"}]}`
		if unsubscribed {
			usage = commandCodeAlphaResponse{status: http.StatusNotFound, body: `{"error":"no plan history found for user"}`}
		}
	} else {
		balance = `{"balance":200000}` // $0.20, below the configured $0.50 threshold.
	}
	return newClineAccountUpstream(usage, balance)
}
