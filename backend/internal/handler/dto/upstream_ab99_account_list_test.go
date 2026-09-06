package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Dropping KIRO runtime fields from the compact projection would hide cooldown
// and quota state after the account page switches to always using lite=1.
func TestAccountListItemPreservesKiroRuntimeState(t *testing.T) {
	reset := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	item := AccountListItemFromAccount(&Account{
		ID: 42, Platform: "kiro", KiroQuotaState: "exhausted",
		KiroQuotaReason: "monthly quota", KiroQuotaResetAt: &reset,
		KiroRuntimeState: "cooldown", KiroRuntimeReason: "rate limited", KiroRuntimeResetAt: &reset,
		GroupIDs: []int64{7}, Groups: []*Group{{ID: 7}},
	})
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "exhausted", decoded["kiro_quota_state"])
	require.Equal(t, "monthly quota", decoded["kiro_quota_reason"])
	require.Equal(t, "2026-09-06T00:00:00Z", decoded["kiro_quota_reset_at"])
	require.Equal(t, "cooldown", decoded["kiro_runtime_state"])
	require.Equal(t, "rate limited", decoded["kiro_runtime_reason"])
	require.Equal(t, "2026-09-06T00:00:00Z", decoded["kiro_runtime_reset_at"])
	require.Equal(t, []any{float64(7)}, decoded["group_ids"])
	require.NotContains(t, decoded, "groups")
	require.NotContains(t, decoded, "account_groups")
}
