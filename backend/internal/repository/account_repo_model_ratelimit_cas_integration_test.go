//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The runtime assertion lets the RED test exercise the absent capability before
// its production implementation exists, without a compilation-only failure.
type modelRateLimitCASForTest interface {
	SetModelRateLimitIfUnchanged(context.Context, int64, string, json.RawMessage, json.RawMessage) (bool, error)
}

func TestAccountRepositoryModelRateLimitCAS(t *testing.T) {
	ctx := context.Background()
	old := map[string]any{
		"rate_limited_at": "2026-10-12T00:00:00Z", "rate_limit_reset_at": "2099-01-01T00:00:00Z",
		"reason": "cline_credits_exhausted: insufficient credits", "metadata": map[string]any{"source": "request"},
	}
	oldJSON := json.RawMessage(`{"metadata":{"source":"request"},"rate_limit_reset_at":"2099-01-01T00:00:00Z","rate_limited_at":"2026-10-12T00:00:00Z","reason":"cline_credits_exhausted: insufficient credits"}`)
	for _, scope := range []string{"cline:credits", "cline:pass"} {
		for _, clear := range []bool{false, true} {
			for _, tc := range []struct {
				name         string
				stored       any
				present      bool
				parentNull   bool
				expected     json.RawMessage
				changedField string
				wantApplied  bool
			}{
				{name: "full_scope_matches", stored: old, present: true, expected: oldJSON, wantApplied: true},
				{name: "reason_changed", stored: old, present: true, expected: oldJSON, changedField: "reason"},
				{name: "same_reason_reset_changed", stored: old, present: true, expected: oldJSON, changedField: "rate_limit_reset_at"},
				{name: "same_reason_limited_at_changed", stored: old, present: true, expected: oldJSON, changedField: "rate_limited_at"},
				{name: "same_reason_metadata_changed", stored: old, present: true, expected: oldJSON, changedField: "metadata"},
				{name: "missing_matches", wantApplied: true},
				{name: "null_matches", stored: nil, present: true, expected: json.RawMessage("null"), wantApplied: true},
				{name: "missing_does_not_match_null", expected: json.RawMessage("null")},
				{name: "null_does_not_match_missing", stored: nil, present: true},
				{name: "null_parent_missing_scope", parentNull: true, wantApplied: true},
			} {
				t.Run(fmt.Sprintf("%s/clear_%t/%s", scope, clear, tc.name), func(t *testing.T) {
					tx := testEntTx(t)
					cache := &schedulerCacheRecorder{}
					repo := newAccountRepositoryWithSQL(tx.Client(), tx, cache)
					setter, ok := any(repo).(modelRateLimitCASForTest)
					require.True(t, ok, "the production repository must provide atomic model scope compare-and-set")
					otherScope := "cline:pass"
					if scope == otherScope {
						otherScope = "cline:credits"
					}
					other := map[string]any{"reason": "opaque other wallet", "rate_limit_reset_at": "2099-02-01T00:00:00Z"}
					limits := map[string]any{otherScope: other}
					if tc.present {
						limits[scope] = tc.stored
					}
					extra := map[string]any{"model_rate_limits": limits, "cline_balance": 5.0, "unrelated": map[string]any{"keep": true}}
					if tc.parentNull {
						extra["model_rate_limits"] = nil
					}
					account := mustCreateAccount(t, tx.Client(), &service.Account{
						Name: "cline-cas-" + tc.name, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey,
						Credentials: map[string]any{"api_key": "cline-local-test"}, Extra: extra,
					})
					if tc.changedField != "" {
						_, err := tx.ExecContext(ctx, `UPDATE accounts SET extra = jsonb_set(extra, ARRAY['model_rate_limits', $1, $2]::text[], '"concurrent restriction"'::jsonb) WHERE id = $3`, scope, tc.changedField, account.ID)
						require.NoError(t, err)
					}
					before, err := repo.GetByID(ctx, account.ID)
					require.NoError(t, err)
					beforeJSON, err := json.Marshal(before.Extra)
					require.NoError(t, err)
					require.NoError(t, cache.SetAccount(ctx, account))
					cache.setAccounts = nil
					var beforeEvents int
					require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", []any{account.ID}, &beforeEvents))
					until := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
					reason := "cline_credits_low: balance below threshold"
					if clear {
						until = until.Add(-time.Hour)
						reason = ""
					}
					payload := map[string]string{"rate_limited_at": "2026-10-12T02:00:00Z", "rate_limit_reset_at": until.Format(time.RFC3339)}
					if reason != "" {
						payload["reason"] = reason
					}
					replacement, err := json.Marshal(payload)
					require.NoError(t, err)
					applied, err := setter.SetModelRateLimitIfUnchanged(ctx, account.ID, scope, tc.expected, replacement)
					require.NoError(t, err)
					require.Equal(t, tc.wantApplied, applied)
					after, err := repo.GetByID(ctx, account.ID)
					require.NoError(t, err)
					afterJSON, err := json.Marshal(after.Extra)
					require.NoError(t, err)
					var afterEvents int
					require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", []any{account.ID}, &afterEvents))
					if tc.wantApplied {
						entry := after.Extra["model_rate_limits"].(map[string]any)[scope].(map[string]any)
						require.Equal(t, until.Format(time.RFC3339), entry["rate_limit_reset_at"])
						require.Equal(t, "2026-10-12T02:00:00Z", entry["rate_limited_at"], "persist exactly the probe's generation")
						if clear {
							require.NotContains(t, entry, "reason")
						} else {
							require.Equal(t, reason, entry["reason"])
						}
						if !tc.parentNull {
							require.Equal(t, other, after.Extra["model_rate_limits"].(map[string]any)[otherScope])
						}
						require.Equal(t, 5.0, after.Extra["cline_balance"])
						require.Equal(t, map[string]any{"keep": true}, after.Extra["unrelated"])
						require.Equal(t, beforeEvents+1, afterEvents, "an applied change enqueues scheduler propagation")
					} else {
						require.JSONEq(t, string(beforeJSON), string(afterJSON), "mismatch leaves all database extras intact")
						require.Equal(t, beforeEvents, afterEvents, "rejected changes do not enqueue a mutation")
					}
					cached, err := cache.GetAccount(ctx, account.ID)
					require.NoError(t, err)
					cachedJSON, err := json.Marshal(cached.Extra)
					require.NoError(t, err)
					require.JSONEq(t, string(afterJSON), string(cachedJSON), "cache must carry authoritative restrictions even after mismatch")
				})
			}
		}
	}
}
