package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageBillingRepository struct {
	db *sql.DB
}

func NewUsageBillingRepository(_ *dbent.Client, sqlDB *sql.DB) service.UsageBillingRepository {
	return &usageBillingRepository{db: sqlDB}
}

func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &service.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}

	result := &service.UsageBillingApplyResult{Applied: true, BillingType: cmd.BillingType}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	return r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
}

func (r *usageBillingRepository) claimUsageBillingRequest(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64, requestFingerprint string) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, requestID, apiKeyID, requestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, requestID, apiKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, requestID, apiKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) ReserveBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, reserveUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) CaptureBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, captureUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) ReleaseBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, releaseUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) applyBatchImageBalanceHold(
	ctx context.Context,
	cmd *service.BatchImageBalanceHoldCommand,
	apply func(context.Context, *sql.Tx, *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error),
) (_ *service.BatchImageBalanceHoldResult, err error) {
	if cmd == nil {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.BatchImageBalanceHoldResult{Applied: false}, nil
	}

	result, err := apply(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &service.BatchImageBalanceHoldResult{}
	}
	result.Applied = true

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	if funded, err := applyUsageBillingRewardLayer(ctx, tx, cmd, result); err != nil {
		return err
	} else if funded {
		return r.applyUsageBillingQuotas(ctx, tx, cmd, result)
	}

	if cmd.SubscriptionCost > 0 && cmd.SubscriptionID != nil {
		if cmd.SubscriptionSevenDayLimitUSD == nil {
			if cmd.AllowBalanceFallback && cmd.BalanceFallbackCost > 0 {
				newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceFallbackCost)
				if err != nil {
					return err
				}
				result.NewBalance = &newBalance
				result.BillingType = service.BillingTypeBalance
				result.FundingSource = service.UsageFundingAccount
				result.BalanceOverdrafted = !sufficient
				return r.applyUsageBillingQuotas(ctx, tx, cmd, result)
			}
			return service.ErrWeeklyLimitExceeded
		}
		var appliedSubscription bool
		var err error
		if cmd.AllowSubscriptionQuotaOverrun {
			appliedSubscription, err = incrementUsageBillingSubscription(ctx, tx, *cmd.SubscriptionID, cmd.SubscriptionCost, nil)
		} else {
			appliedSubscription, err = incrementUsageBillingSubscription(ctx, tx, *cmd.SubscriptionID, cmd.SubscriptionCost, cmd.SubscriptionSevenDayLimitUSD)
		}
		if err != nil {
			return err
		}
		if appliedSubscription {
			result.BillingType = service.BillingTypeSubscription
			result.FundingSource = service.UsageFundingSubscription
		} else if cmd.AllowBalanceFallback && cmd.BalanceFallbackCost > 0 {
			newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceFallbackCost)
			if err != nil {
				return err
			}
			result.NewBalance = &newBalance
			result.BillingType = service.BillingTypeBalance
			result.FundingSource = service.UsageFundingAccount
			result.BalanceOverdrafted = !sufficient
		} else {
			return service.ErrWeeklyLimitExceeded
		}
	}

	if cmd.BalanceCost > 0 {
		newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceCost)
		if err != nil {
			return err
		}
		result.NewBalance = &newBalance
		result.BillingType = service.BillingTypeBalance
		result.FundingSource = service.UsageFundingAccount
		result.BalanceOverdrafted = !sufficient
	}

	return r.applyUsageBillingQuotas(ctx, tx, cmd, result)
}

func (r *usageBillingRepository) applyUsageBillingQuotas(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {

	if cmd.APIKeyQuotaCost > 0 {
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, cmd.APIKeyQuotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, cmd.APIKeyRateLimitCost); err != nil {
			return err
		}
	}

	if cmd.AccountQuotaCost > 0 && shouldApplyUsageBillingAccountQuota(cmd) {
		quotaState, err := incrementUsageBillingAccountQuota(ctx, tx, cmd.AccountID, cmd.AccountQuotaCost)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

func applyUsageBillingRewardLayer(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) (bool, error) {
	if cmd == nil || result == nil {
		return false, nil
	}
	amount := cmd.BalanceCost
	if cmd.SubscriptionID != nil && cmd.SubscriptionCost > 0 {
		amount = cmd.SubscriptionCost
	}
	if amount <= 0 {
		return false, nil
	}

	now := time.Now().UTC()
	if _, err := expireRewardCreditsForUserTx(ctx, tx, cmd.UserID, now); err != nil {
		return false, err
	}

	newBalance, funded, err := tryConsumeRewardLayer(
		ctx,
		tx,
		cmd.UserID,
		[]service.RewardCreditType{service.RewardCreditDailyCheckIn},
		amount,
		cmd.RequestID,
		now,
	)
	if err != nil {
		return false, err
	}
	if funded {
		result.NewBalance = &newBalance
		result.BillingType = service.BillingTypeBalance
		result.FundingSource = service.UsageFundingDailyCheckIn
		return true, nil
	}

	newBalance, funded, err = tryConsumeRewardLayer(
		ctx,
		tx,
		cmd.UserID,
		[]service.RewardCreditType{service.RewardCreditAffiliateInviter, service.RewardCreditAffiliateInvitee},
		amount,
		cmd.RequestID,
		now,
	)
	if err != nil {
		return false, err
	}
	if funded {
		result.NewBalance = &newBalance
		result.BillingType = service.BillingTypeBalance
		result.FundingSource = service.UsageFundingAffiliate
		return true, nil
	}
	return false, nil
}

type usageRewardCreditLot struct {
	id        int64
	remaining float64
}

func tryConsumeRewardLayer(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	creditTypes []service.RewardCreditType,
	amount float64,
	eventKey string,
	now time.Time,
) (float64, bool, error) {
	if amount <= 0 || len(creditTypes) == 0 {
		return 0, false, nil
	}

	args := []any{userID, now}
	placeholders := make([]string, 0, len(creditTypes))
	for _, creditType := range creditTypes {
		args = append(args, creditType)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, remaining_amount::double precision
FROM user_reward_credits
WHERE user_id = $1
  AND expires_at > $2
  AND remaining_amount > 0
  AND expired_at IS NULL
  AND credit_type IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY expires_at ASC, id ASC
FOR UPDATE`, args...)
	if err != nil {
		return 0, false, err
	}
	lots := make([]usageRewardCreditLot, 0)
	total := 0.0
	for rows.Next() {
		var lot usageRewardCreditLot
		if err := rows.Scan(&lot.id, &lot.remaining); err != nil {
			_ = rows.Close()
			return 0, false, err
		}
		lots = append(lots, lot)
		total += lot.remaining
	}
	if err := rows.Close(); err != nil {
		return 0, false, err
	}
	if total+1e-9 < amount {
		return 0, false, nil
	}

	left := amount
	for _, lot := range lots {
		if left <= 1e-9 {
			break
		}
		consumed := lot.remaining
		if consumed > left {
			consumed = left
		}
		updated, err := tx.ExecContext(ctx, `
UPDATE user_reward_credits
SET remaining_amount = remaining_amount - $1,
    consumed_at = CASE WHEN remaining_amount - $1 <= 1e-9 THEN COALESCE(consumed_at, $2) ELSE consumed_at END,
    updated_at = $2
WHERE id = $3 AND remaining_amount + 1e-9 >= $1`, consumed, now, lot.id)
		if err != nil {
			return 0, false, err
		}
		affected, err := updated.RowsAffected()
		if err != nil {
			return 0, false, err
		}
		if affected != 1 {
			return 0, false, errors.New("reward credit changed while locked")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO user_reward_credit_events (
    credit_id, user_id, event_type, event_key, amount, request_id, created_at
)
VALUES ($1, $2, 'consume', $3, $4, $3, $5)`, lot.id, userID, eventKey, consumed, now); err != nil {
			return 0, false, err
		}
		left -= consumed
	}

	var newBalance float64
	err = tx.QueryRowContext(ctx, `
UPDATE users
SET balance = balance - $1, updated_at = $2
WHERE id = $3 AND deleted_at IS NULL
RETURNING balance`, amount, now, userID).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, service.ErrUserNotFound
	}
	if err != nil {
		return 0, false, err
	}
	return newBalance, true, nil
}

func shouldApplyUsageBillingAccountQuota(cmd *service.UsageBillingCommand) bool {
	if cmd == nil {
		return false
	}
	if strings.EqualFold(cmd.AccountType, service.AccountTypeAPIKey) || strings.EqualFold(cmd.AccountType, service.AccountTypeBedrock) {
		return true
	}
	return strings.EqualFold(cmd.AccountPlatform, service.PlatformKiro) && strings.EqualFold(cmd.AccountType, service.AccountTypeOAuth)
}

func incrementUsageBillingSubscription(ctx context.Context, tx *sql.Tx, subscriptionID int64, costUSD float64, sevenDayLimitUSD *float64) (bool, error) {
	if sevenDayLimitUSD != nil {
		const guardedUpdateSQL = `
			UPDATE user_subscriptions us
			SET
				daily_usage_usd = us.daily_usage_usd + $1,
				weekly_usage_usd = CASE
					WHEN us.weekly_window_start IS NOT NULL
						AND us.weekly_window_start + INTERVAL '7 days' <= NOW()
					THEN $1
					ELSE us.weekly_usage_usd + $1
				END,
				weekly_window_start = CASE
					WHEN us.weekly_window_start IS NOT NULL
						AND us.weekly_window_start + INTERVAL '7 days' <= NOW()
					THEN NOW()
					ELSE us.weekly_window_start
				END,
				monthly_usage_usd = us.monthly_usage_usd + $1,
				updated_at = NOW()
			WHERE us.id = $2
				AND us.deleted_at IS NULL
				AND (CASE
					WHEN us.weekly_window_start IS NOT NULL
						AND us.weekly_window_start + INTERVAL '7 days' <= NOW()
					THEN 0
					ELSE us.weekly_usage_usd
				END) + $1 <= $3 + 1e-9
		`
		res, err := tx.ExecContext(ctx, guardedUpdateSQL, costUSD, subscriptionID, *sevenDayLimitUSD)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected > 0 {
			return true, nil
		}
		exists, err := usageBillingSubscriptionExists(ctx, tx, subscriptionID)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, service.ErrSubscriptionNotFound
		}
		return false, nil
	}

	const updateSQL = `
		UPDATE user_subscriptions us
		SET
			daily_usage_usd = us.daily_usage_usd + $1,
			weekly_usage_usd = us.weekly_usage_usd + $1,
			monthly_usage_usd = us.monthly_usage_usd + $1,
			updated_at = NOW()
		WHERE us.id = $2
			AND us.deleted_at IS NULL
	`
	res, err := tx.ExecContext(ctx, updateSQL, costUSD, subscriptionID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		return true, nil
	}
	return false, service.ErrSubscriptionNotFound
}

func usageBillingSubscriptionExists(ctx context.Context, tx *sql.Tx, subscriptionID int64) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_subscriptions
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, subscriptionID).Scan(&exists)
	return exists, err
}

func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, bool, error) {
	var newBalance float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2
		  AND deleted_at IS NULL
		  AND balance - COALESCE((
			SELECT SUM(rc.remaining_amount + rc.reserved_amount)
			FROM user_reward_credits rc
			WHERE rc.user_id = users.id
			  AND rc.expired_at IS NULL
			  AND (rc.remaining_amount > 0 OR rc.reserved_amount > 0)
		  ), 0) >= $1
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if err == nil {
		return newBalance, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}

	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, service.ErrUserNotFound
	}
	if err != nil {
		return 0, false, err
	}
	return newBalance, false, nil
}

func reserveUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if cmd.BatchID != "" {
		now := time.Now().UTC()
		if _, err := expireRewardCreditsForUserTx(ctx, tx, cmd.UserID, now); err != nil {
			return nil, err
		}
		if result, funded, err := tryReserveBatchImageRewardLayer(ctx, tx, cmd,
			[]service.RewardCreditType{service.RewardCreditDailyCheckIn}, service.UsageFundingDailyCheckIn, now); err != nil {
			return nil, err
		} else if funded {
			return result, nil
		}
		if result, funded, err := tryReserveBatchImageRewardLayer(ctx, tx, cmd,
			[]service.RewardCreditType{service.RewardCreditAffiliateInviter, service.RewardCreditAffiliateInvitee}, service.UsageFundingAffiliate, now); err != nil {
			return nil, err
		} else if funded {
			return result, nil
		}
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2
		  AND deleted_at IS NULL
		  AND balance - COALESCE((
			SELECT SUM(rc.remaining_amount + rc.reserved_amount)
			FROM user_reward_credits rc
			WHERE rc.user_id = users.id
			  AND rc.expired_at IS NULL
			  AND (rc.remaining_amount > 0 OR rc.reserved_amount > 0)
		  ), 0) >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{FundingSource: service.UsageFundingAccount, NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, service.ErrBatchImageInsufficientBalance
}

func captureUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 && cmd.ActualAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if cmd.ActualAmount-cmd.HoldAmount > 0.00000001 {
		return nil, service.ErrBatchImageSettlementCostExceedsHold
	}
	if cmd.BatchID != "" {
		now := time.Now().UTC()
		if _, err := expireRewardCreditsForUserTx(ctx, tx, cmd.UserID, now); err != nil {
			return nil, err
		}
		allocations, err := loadBatchImageRewardAllocations(ctx, tx, cmd.UserID, service.BatchImageHoldRequestID(cmd.BatchID))
		if err != nil {
			return nil, err
		}
		if len(allocations) > 0 {
			return settleBatchImageRewardAllocations(ctx, tx, cmd, allocations, now)
		}
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance
				+ CASE WHEN $1 > $2 THEN $1 - $2 ELSE 0 END
				- CASE WHEN $2 > $1 THEN $2 - $1 ELSE 0 END,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.ActualAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{FundingSource: service.UsageFundingAccount, NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

func releaseUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	// 释放前校验该 job 确实预留过 hold（hold request id 已被 claim），
	// 防止从未成功冻结的 job 触发"幻影释放"，从其他用户的冻结资金池中凭空生成余额。
	held, heldErr := batchImageHoldClaimExists(ctx, tx, service.BatchImageHoldRequestID(cmd.BatchID), cmd.APIKeyID)
	if heldErr != nil {
		return nil, heldErr
	}
	if !held {
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] release skipped, hold was never reserved: batch=%s", cmd.BatchID)
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if cmd.BatchID != "" {
		now := time.Now().UTC()
		if _, err := expireRewardCreditsForUserTx(ctx, tx, cmd.UserID, now); err != nil {
			return nil, err
		}
		allocations, err := loadBatchImageRewardAllocations(ctx, tx, cmd.UserID, service.BatchImageHoldRequestID(cmd.BatchID))
		if err != nil {
			return nil, err
		}
		if len(allocations) > 0 {
			releaseCmd := *cmd
			releaseCmd.ActualAmount = 0
			return settleBatchImageRewardAllocations(ctx, tx, &releaseCmd, allocations, now)
		}
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{FundingSource: service.UsageFundingAccount, NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

func tryReserveBatchImageRewardLayer(
	ctx context.Context,
	tx *sql.Tx,
	cmd *service.BatchImageBalanceHoldCommand,
	creditTypes []service.RewardCreditType,
	fundingSource service.UsageFundingSource,
	now time.Time,
) (*service.BatchImageBalanceHoldResult, bool, error) {
	args := []any{cmd.UserID, now}
	placeholders := make([]string, 0, len(creditTypes))
	for _, creditType := range creditTypes {
		args = append(args, creditType)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, remaining_amount::double precision, expires_at
FROM user_reward_credits
WHERE user_id = $1
  AND expires_at > $2
  AND remaining_amount > 0
  AND expired_at IS NULL
  AND credit_type IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY expires_at ASC, id ASC
FOR UPDATE`, args...)
	if err != nil {
		return nil, false, err
	}
	type reservableLot struct {
		id        int64
		remaining float64
		expiresAt time.Time
	}
	lots := make([]reservableLot, 0)
	total := 0.0
	for rows.Next() {
		var lot reservableLot
		if err := rows.Scan(&lot.id, &lot.remaining, &lot.expiresAt); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		lots = append(lots, lot)
		total += lot.remaining
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if total+1e-9 < cmd.HoldAmount {
		return nil, false, nil
	}

	holdKey := service.BatchImageHoldRequestID(cmd.BatchID)
	left := cmd.HoldAmount
	for _, lot := range lots {
		if left <= 1e-9 {
			break
		}
		reserved := lot.remaining
		if reserved > left {
			reserved = left
		}
		updated, err := tx.ExecContext(ctx, `
UPDATE user_reward_credits
SET remaining_amount = remaining_amount - $1,
    reserved_amount = reserved_amount + $1,
    updated_at = $2
WHERE id = $3 AND remaining_amount + 1e-9 >= $1`, reserved, now, lot.id)
		if err != nil {
			return nil, false, err
		}
		affected, err := updated.RowsAffected()
		if err != nil {
			return nil, false, err
		}
		if affected != 1 {
			return nil, false, errors.New("reward credit changed while locked")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO batch_image_reward_allocations (
    hold_key, credit_id, user_id, reserved_amount, captured_amount,
    released_amount, expires_at_snapshot, created_at, updated_at
)
VALUES ($1, $2, $3, $4, 0, 0, $5, $6, $6)`, holdKey, lot.id, cmd.UserID, reserved, lot.expiresAt, now); err != nil {
			return nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO user_reward_credit_events (
    credit_id, user_id, event_type, event_key, amount, request_id, batch_id, created_at
)
VALUES ($1, $2, 'reserve', $3, $4, $3, $5, $6)`, lot.id, cmd.UserID, cmd.RequestID, reserved, cmd.BatchID, now); err != nil {
			return nil, false, err
		}
		left -= reserved
	}

	var balance, frozen float64
	err = tx.QueryRowContext(ctx, `
UPDATE users
SET balance = balance - $1,
    frozen_balance = COALESCE(frozen_balance, 0) + $1,
    updated_at = $2
WHERE id = $3 AND deleted_at IS NULL
RETURNING balance, frozen_balance`, cmd.HoldAmount, now, cmd.UserID).Scan(&balance, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrUserNotFound
	}
	if err != nil {
		return nil, false, err
	}
	return &service.BatchImageBalanceHoldResult{
		FundingSource: fundingSource,
		NewBalance:    &balance,
		FrozenBalance: &frozen,
	}, true, nil
}

type batchImageRewardAllocation struct {
	id                int64
	creditID          int64
	creditType        service.RewardCreditType
	reservedAmount    float64
	capturedAmount    float64
	releasedAmount    float64
	expiresAtSnapshot time.Time
}

func loadBatchImageRewardAllocations(ctx context.Context, tx *sql.Tx, userID int64, holdKey string) ([]batchImageRewardAllocation, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT a.id, a.credit_id, rc.credit_type,
       a.reserved_amount::double precision,
       a.captured_amount::double precision,
       a.released_amount::double precision,
       a.expires_at_snapshot
FROM batch_image_reward_allocations a
JOIN user_reward_credits rc ON rc.id = a.credit_id
WHERE a.hold_key = $1 AND a.user_id = $2
ORDER BY a.expires_at_snapshot ASC, a.id ASC
FOR UPDATE OF a, rc`, holdKey, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	allocations := make([]batchImageRewardAllocation, 0)
	for rows.Next() {
		var allocation batchImageRewardAllocation
		if err := rows.Scan(
			&allocation.id,
			&allocation.creditID,
			&allocation.creditType,
			&allocation.reservedAmount,
			&allocation.capturedAmount,
			&allocation.releasedAmount,
			&allocation.expiresAtSnapshot,
		); err != nil {
			return nil, err
		}
		allocations = append(allocations, allocation)
	}
	return allocations, rows.Err()
}

func settleBatchImageRewardAllocations(
	ctx context.Context,
	tx *sql.Tx,
	cmd *service.BatchImageBalanceHoldCommand,
	allocations []batchImageRewardAllocation,
	now time.Time,
) (*service.BatchImageBalanceHoldResult, error) {
	actualLeft := cmd.ActualAmount
	totalOutstanding := 0.0
	for _, allocation := range allocations {
		outstanding := allocation.reservedAmount - allocation.capturedAmount - allocation.releasedAmount
		if outstanding > 1e-9 {
			totalOutstanding += outstanding
		}
	}
	if actualLeft-totalOutstanding > 1e-9 {
		return nil, errors.New("batch image reward reservation is insufficient")
	}

	fundingSource := service.UsageFundingAffiliate
	if allocations[0].creditType == service.RewardCreditDailyCheckIn {
		fundingSource = service.UsageFundingDailyCheckIn
	}
	restoreTotal := 0.0
	for _, allocation := range allocations {
		outstanding := allocation.reservedAmount - allocation.capturedAmount - allocation.releasedAmount
		if outstanding <= 1e-9 {
			continue
		}
		captured := outstanding
		if captured > actualLeft {
			captured = actualLeft
		}
		actualLeft -= captured
		unused := outstanding - captured
		restored := unused
		expired := 0.0
		if !allocation.expiresAtSnapshot.After(now) {
			expired = unused
			restored = 0
		}

		creditUpdate, err := tx.ExecContext(ctx, `
UPDATE user_reward_credits
SET reserved_amount = reserved_amount - $1,
    remaining_amount = remaining_amount + $2,
    consumed_at = CASE
        WHEN $3 > 0 AND remaining_amount + $2 <= 1e-9 AND reserved_amount - $1 <= 1e-9
        THEN COALESCE(consumed_at, $4)
        ELSE consumed_at
    END,
    expired_at = CASE
        WHEN $5 > 0 AND remaining_amount + $2 <= 1e-9 AND reserved_amount - $1 <= 1e-9
        THEN COALESCE(expired_at, $4)
        ELSE expired_at
    END,
    updated_at = $4
WHERE id = $6 AND reserved_amount + 1e-9 >= $1`, outstanding, restored, captured, now, expired, allocation.creditID)
		if err != nil {
			return nil, err
		}
		creditRows, err := creditUpdate.RowsAffected()
		if err != nil {
			return nil, err
		}
		if creditRows != 1 {
			return nil, errors.New("batch image reward reservation changed while locked")
		}
		allocationUpdate, err := tx.ExecContext(ctx, `
UPDATE batch_image_reward_allocations
SET captured_amount = captured_amount + $1,
    released_amount = released_amount + $2,
    updated_at = $3
WHERE id = $4`, captured, unused, now, allocation.id)
		if err != nil {
			return nil, err
		}
		allocationRows, err := allocationUpdate.RowsAffected()
		if err != nil {
			return nil, err
		}
		if allocationRows != 1 {
			return nil, errors.New("batch image reward allocation changed while locked")
		}
		if captured > 1e-9 {
			if err := insertBatchImageRewardEvent(ctx, tx, allocation.creditID, cmd, "capture", captured, now); err != nil {
				return nil, err
			}
		}
		if restored > 1e-9 {
			if err := insertBatchImageRewardEvent(ctx, tx, allocation.creditID, cmd, "release", restored, now); err != nil {
				return nil, err
			}
			restoreTotal += restored
		}
		if expired > 1e-9 {
			if err := insertBatchImageRewardEvent(ctx, tx, allocation.creditID, cmd, "expire", expired, now); err != nil {
				return nil, err
			}
		}
	}

	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
UPDATE users
SET balance = balance + $1,
    frozen_balance = COALESCE(frozen_balance, 0) - $2,
    updated_at = $3
WHERE id = $4
  AND deleted_at IS NULL
  AND COALESCE(frozen_balance, 0) + 1e-9 >= $2
RETURNING balance, frozen_balance`, restoreTotal, totalOutstanding, now, cmd.UserID).Scan(&balance, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
			return nil, existsErr
		} else if !exists {
			return nil, service.ErrUserNotFound
		}
		return nil, errors.New("batch image frozen balance is insufficient")
	}
	if err != nil {
		return nil, err
	}
	return &service.BatchImageBalanceHoldResult{
		FundingSource: fundingSource,
		NewBalance:    &balance,
		FrozenBalance: &frozen,
	}, nil
}

func insertBatchImageRewardEvent(
	ctx context.Context,
	tx *sql.Tx,
	creditID int64,
	cmd *service.BatchImageBalanceHoldCommand,
	eventType string,
	amount float64,
	now time.Time,
) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO user_reward_credit_events (
    credit_id, user_id, event_type, event_key, amount, request_id, batch_id, created_at
)
VALUES ($1, $2, $3, $4, $5, $4, $6, $7)`, creditID, cmd.UserID, eventType, cmd.RequestID, amount, cmd.BatchID, now)
	return err
}

// batchImageHoldClaimExists 检查 hold request id 是否已在 dedup（或归档）表中被 claim，
// 即该 batch 的冻结操作确实成功提交过。
func batchImageHoldClaimExists(ctx context.Context, tx *sql.Tx, holdRequestID string, apiKeyID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func userExistsForBilling(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, cost, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func incrementUsageBillingAccountQuota(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	rows, err := tx.QueryContext(ctx,
		`UPDATE accounts SET extra = (
			COALESCE(extra, '{}'::jsonb)
			|| jsonb_build_object('quota_used', COALESCE((extra->>'quota_used')::numeric, 0) + $1)
			|| CASE WHEN COALESCE((extra->>'quota_daily_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_daily_used',
					CASE WHEN `+dailyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) + $1 END,
					'quota_daily_start',
					CASE WHEN `+dailyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_daily_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+dailyExpiredExpr+` AND `+nextDailyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_daily_reset_at', `+nextDailyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
			|| CASE WHEN COALESCE((extra->>'quota_weekly_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_weekly_used',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_weekly_used')::numeric, 0) + $1 END,
					'quota_weekly_start',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_weekly_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+weeklyExpiredExpr+` AND `+nextWeeklyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_weekly_reset_at', `+nextWeeklyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
		), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0)`,
		amount, accountID)
	if err != nil {
		return nil, err
	}

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 必须在执行下一条 SQL 前显式关闭 rows：pq 驱动在同一连接上
	// 不允许前一条查询的结果集未耗尽时启动新查询，否则会返回
	// "unexpected Parse response" 错误。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// 任意维度额度在本次递增中从"未超"跨越到"已超"时，必须刷新调度快照，
	// 否则 Redis 中缓存的 Account 仍显示旧的 used 值，后续请求会继续选中本账号，
	// 最终观察到 daily_used / weekly_used 大幅超过配置的 limit。
	// 对于日/周额度，即使本次触发了周期重置（pre=0、post=amount），
	// 判定式 (post-amount) < limit 同样成立，逻辑与总额度保持一致。
	crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit
	crossedDaily := state.DailyLimit > 0 && state.DailyUsed >= state.DailyLimit && (state.DailyUsed-amount) < state.DailyLimit
	crossedWeekly := state.WeeklyLimit > 0 && state.WeeklyUsed >= state.WeeklyLimit && (state.WeeklyUsed-amount) < state.WeeklyLimit
	if crossedTotal || crossedDaily || crossedWeekly {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}
