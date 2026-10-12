//go:build unit

package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
