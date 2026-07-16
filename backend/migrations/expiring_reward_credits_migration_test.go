package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const expiringRewardCreditsMigration = "184_expiring_reward_credits.sql"

func readExpiringRewardCreditsMigration(t *testing.T) string {
	t.Helper()
	content, err := FS.ReadFile(expiringRewardCreditsMigration)
	require.NoError(t, err)
	return string(content)
}

func TestExpiringRewardCreditsMigrationCreatesConstrainedLedgers(t *testing.T) {
	sqlText := readExpiringRewardCreditsMigration(t)
	normalized := strings.Join(strings.Fields(sqlText), " ")

	require.Contains(t, normalized, "CREATE TABLE IF NOT EXISTS user_reward_credits")
	require.Contains(t, normalized, "CREATE TABLE IF NOT EXISTS user_reward_credit_events")
	require.Contains(t, normalized, "CREATE TABLE IF NOT EXISTS batch_image_reward_allocations")
	require.Contains(t, normalized, "UNIQUE (user_id, credit_type, source_key)")
	require.Contains(t, normalized, "CHECK (credit_type IN ('daily_check_in', 'affiliate_inviter', 'affiliate_invitee'))")
	require.Contains(t, normalized, "CHECK (remaining_amount + reserved_amount <= original_amount)")
	require.Contains(t, normalized, "CHECK (event_type IN ('grant', 'consume', 'reserve', 'capture', 'release', 'expire'))")
	require.Contains(t, normalized, "UNIQUE (credit_id, event_type, event_key)")
	require.Contains(t, normalized, "UNIQUE (hold_key, credit_id)")
	require.Contains(t, normalized, "WHERE remaining_amount > 0")
	require.Contains(t, normalized, "WHERE reserved_amount > 0")
}

func TestExpiringRewardCreditsMigrationBackfillsAffiliateRewardState(t *testing.T) {
	sqlText := readExpiringRewardCreditsMigration(t)
	normalized := strings.Join(strings.Fields(sqlText), " ")

	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS inviter_reward_count INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS reward_mode VARCHAR(32)")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS reward_status VARCHAR(32)")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS reward_resolved_at TIMESTAMPTZ")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS reward_source_order_id BIGINT")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS inviter_rewarded BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, normalized, "ADD COLUMN IF NOT EXISTS invitee_rewarded BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, normalized, "COUNT(DISTINCT source_user_id)")
	require.Contains(t, normalized, "reward_mode = 'first_recharge'")
	require.Contains(t, normalized, "reward_status = CASE")
	require.Contains(t, normalized, "THEN 'resolved' ELSE 'pending' END")
	require.Contains(t, normalized, "AFFILIATE_REBATE_APPLIED")
	require.Contains(t, normalized, "AFFILIATE_REBATE_SKIPPED")
	require.Contains(t, normalized, "CHECK (reward_mode IS NULL OR reward_mode IN ('immediate', 'first_recharge'))")
	require.Contains(t, normalized, "CHECK (reward_status IS NULL OR reward_status IN ('pending', 'resolved'))")
}

func TestExpiringRewardCreditsMigrationMovesLegacyWalletBeforeZeroingIt(t *testing.T) {
	sqlText := readExpiringRewardCreditsMigration(t)
	normalized := strings.Join(strings.Fields(sqlText), " ")

	creditBalanceAt := strings.Index(normalized, "UPDATE users u SET balance = u.balance + legacy.total_amount")
	zeroWalletAt := strings.Index(normalized, "SET aff_quota = 0, aff_frozen_quota = 0")
	require.GreaterOrEqual(t, creditBalanceAt, 0)
	require.Greater(t, zeroWalletAt, creditBalanceAt)
	require.Contains(t, normalized, "ua.aff_quota + ua.aff_frozen_quota AS total_amount")
	require.NotContains(t, normalized, "aff_history_quota = 0")

	require.Contains(t, normalized, "key = 'affiliate_first_recharge_threshold'")
	require.Contains(t, normalized, "value::numeric = 20")
	require.Contains(t, normalized, "key = 'affiliate_inviter_reward'")
	require.Contains(t, normalized, "value::numeric = 5")
	require.Contains(t, normalized, "('affiliate_reward_validity_days', '7',")
	require.Contains(t, normalized, "('affiliate_inviter_reward_limit', '0',")
}

func TestExpiringRewardCreditsMigrationUsesExistingSettingsColumns(t *testing.T) {
	sqlText := readExpiringRewardCreditsMigration(t)
	normalized := strings.Join(strings.Fields(sqlText), " ")

	require.Contains(t, normalized, "INSERT INTO settings (key, value, updated_at)")
	require.NotContains(t, normalized, "INSERT INTO settings (key, value, created_at, updated_at)")
}
