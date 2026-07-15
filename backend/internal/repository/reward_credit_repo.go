package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type rewardCreditQueryExecer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type rewardCreditRepository struct {
	client *dbent.Client
	db     *sql.DB
}

func NewRewardCreditRepository(client *dbent.Client, db *sql.DB) service.RewardCreditRepository {
	return &rewardCreditRepository{client: client, db: db}
}

func (r *rewardCreditRepository) Grant(ctx context.Context, input service.RewardCreditGrant) (result service.RewardCreditGrantResult, err error) {
	if err := validateRewardCreditGrant(input); err != nil {
		return result, err
	}
	err = r.withTx(ctx, func(txCtx context.Context, q rewardCreditQueryExecer) error {
		var grantErr error
		result, grantErr = grantRewardCreditTx(txCtx, q, input)
		return grantErr
	})
	return result, err
}

func (r *rewardCreditRepository) GetSummary(ctx context.Context, userID int64, now time.Time) (summary service.RewardBalanceSummary, err error) {
	if userID <= 0 {
		return summary, service.ErrUserNotFound
	}
	now = normalizedRewardCreditNow(now)
	err = r.withTx(ctx, func(txCtx context.Context, q rewardCreditQueryExecer) error {
		if _, expireErr := expireRewardCreditsForUserTx(txCtx, q, userID, now); expireErr != nil {
			return expireErr
		}

		rows, queryErr := q.QueryContext(txCtx, `
SELECT
    COALESCE(SUM(remaining_amount) FILTER (WHERE credit_type = 'daily_check_in'), 0)::double precision,
    MIN(expires_at) FILTER (WHERE credit_type = 'daily_check_in'),
    COALESCE(SUM(remaining_amount) FILTER (WHERE credit_type IN ('affiliate_inviter', 'affiliate_invitee')), 0)::double precision,
    MIN(expires_at) FILTER (WHERE credit_type IN ('affiliate_inviter', 'affiliate_invitee')),
    COUNT(*) FILTER (WHERE credit_type IN ('affiliate_inviter', 'affiliate_invitee'))::integer
FROM user_reward_credits
WHERE user_id = $1
  AND remaining_amount > 0
  AND expired_at IS NULL
  AND expires_at > $2`, userID, now)
		if queryErr != nil {
			return queryErr
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return rows.Err()
		}

		var dailyExpiry sql.NullTime
		var affiliateExpiry sql.NullTime
		if scanErr := rows.Scan(
			&summary.DailyCheckIn.Amount,
			&dailyExpiry,
			&summary.Affiliate.Amount,
			&affiliateExpiry,
			&summary.Affiliate.CreditCount,
		); scanErr != nil {
			return scanErr
		}
		if dailyExpiry.Valid {
			expiresAt := dailyExpiry.Time
			summary.DailyCheckIn.ExpiresAt = &expiresAt
		}
		if affiliateExpiry.Valid {
			expiresAt := affiliateExpiry.Time
			summary.Affiliate.EarliestExpiresAt = &expiresAt
		}
		return rows.Err()
	})
	return summary, err
}

func (r *rewardCreditRepository) ListCredits(ctx context.Context, filter service.RewardCreditListFilter) (items []service.RewardCredit, total int64, err error) {
	if filter.UserID <= 0 {
		return nil, 0, service.ErrUserNotFound
	}
	filter = normalizeRewardCreditListFilter(filter)
	if err := validateRewardCreditListFilter(filter); err != nil {
		return nil, 0, err
	}

	err = r.withTx(ctx, func(txCtx context.Context, q rewardCreditQueryExecer) error {
		if _, expireErr := expireRewardCreditsForUserTx(txCtx, q, filter.UserID, filter.Now); expireErr != nil {
			return expireErr
		}
		whereSQL, args := rewardCreditListWhere(filter)

		countRows, queryErr := q.QueryContext(txCtx, "SELECT COUNT(*) FROM user_reward_credits "+whereSQL, args...)
		if queryErr != nil {
			return queryErr
		}
		if !countRows.Next() {
			_ = countRows.Close()
			return countRows.Err()
		}
		if scanErr := countRows.Scan(&total); scanErr != nil {
			_ = countRows.Close()
			return scanErr
		}
		if closeErr := countRows.Close(); closeErr != nil {
			return closeErr
		}

		limitPos := len(args) + 1
		offsetPos := len(args) + 2
		args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
		query := fmt.Sprintf(`
SELECT id, user_id, credit_type, source_key,
       original_amount::double precision,
       remaining_amount::double precision,
       reserved_amount::double precision,
       granted_at, expires_at, consumed_at, expired_at
FROM user_reward_credits
%s
ORDER BY expires_at ASC, id ASC
LIMIT $%d OFFSET $%d`, whereSQL, limitPos, offsetPos)
		rows, queryErr := q.QueryContext(txCtx, query, args...)
		if queryErr != nil {
			return queryErr
		}
		defer func() { _ = rows.Close() }()
		items = make([]service.RewardCredit, 0, filter.PageSize)
		for rows.Next() {
			var item service.RewardCredit
			var consumedAt sql.NullTime
			var expiredAt sql.NullTime
			if scanErr := rows.Scan(
				&item.ID,
				&item.UserID,
				&item.CreditType,
				&item.SourceKey,
				&item.OriginalAmount,
				&item.RemainingAmount,
				&item.ReservedAmount,
				&item.GrantedAt,
				&item.ExpiresAt,
				&consumedAt,
				&expiredAt,
			); scanErr != nil {
				return scanErr
			}
			item.RoleLabel = service.RewardCreditRoleForType(item.CreditType)
			if consumedAt.Valid {
				value := consumedAt.Time
				item.ConsumedAt = &value
			}
			if expiredAt.Valid {
				value := expiredAt.Time
				item.ExpiredAt = &value
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, total, err
}

func (r *rewardCreditRepository) ExpireUser(ctx context.Context, userID int64, now time.Time) (expired float64, err error) {
	if userID <= 0 {
		return 0, service.ErrUserNotFound
	}
	now = normalizedRewardCreditNow(now)
	err = r.withTx(ctx, func(txCtx context.Context, q rewardCreditQueryExecer) error {
		var expireErr error
		expired, expireErr = expireRewardCreditsForUserTx(txCtx, q, userID, now)
		return expireErr
	})
	return expired, err
}

func (r *rewardCreditRepository) ExpireBatch(ctx context.Context, now time.Time, limit int) (results []service.RewardCreditExpiryResult, err error) {
	now = normalizedRewardCreditNow(now)
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	err = r.withTx(ctx, func(txCtx context.Context, q rewardCreditQueryExecer) error {
		credits, queryErr := selectExpiredRewardCreditsTx(txCtx, q, 0, now, limit)
		if queryErr != nil {
			return queryErr
		}
		if len(credits) == 0 {
			results = []service.RewardCreditExpiryResult{}
			return nil
		}

		byUser := make(map[int64]float64)
		for _, credit := range credits {
			if applyErr := expireRewardCreditTx(txCtx, q, credit, now); applyErr != nil {
				return applyErr
			}
			byUser[credit.userID] += credit.amount
		}
		results = make([]service.RewardCreditExpiryResult, 0, len(byUser))
		for userID, amount := range byUser {
			if updateErr := subtractExpiredRewardBalanceTx(txCtx, q, userID, amount); updateErr != nil {
				return updateErr
			}
			results = append(results, service.RewardCreditExpiryResult{UserID: userID, ExpiredAmount: amount})
		}
		return nil
	})
	return results, err
}

func (r *rewardCreditRepository) withTx(ctx context.Context, fn func(context.Context, rewardCreditQueryExecer) error) (err error) {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	if r == nil || r.db == nil {
		return errors.New("reward credit repository db is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func grantRewardCreditTx(ctx context.Context, q rewardCreditQueryExecer, input service.RewardCreditGrant) (service.RewardCreditGrantResult, error) {
	var result service.RewardCreditGrantResult
	rows, err := q.QueryContext(ctx, `
INSERT INTO user_reward_credits (
    user_id, credit_type, source_key, original_amount, remaining_amount,
    reserved_amount, granted_at, expires_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $4, 0, $5, $6, $5, $5)
ON CONFLICT (user_id, credit_type, source_key) DO NOTHING
RETURNING id`, input.UserID, input.CreditType, input.SourceKey, input.Amount, input.GrantedAt, input.ExpiresAt)
	if err != nil {
		return result, err
	}
	if rows.Next() {
		if err := rows.Scan(&result.CreditID); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Applied = true
	}
	if err := rows.Close(); err != nil {
		return result, err
	}

	if !result.Applied {
		return existingRewardCreditGrantResult(ctx, q, input)
	}

	balanceRows, err := q.QueryContext(ctx, `
UPDATE users
SET balance = balance + $1, updated_at = $2
WHERE id = $3 AND deleted_at IS NULL
RETURNING balance`, input.Amount, input.GrantedAt, input.UserID)
	if err != nil {
		return result, err
	}
	if !balanceRows.Next() {
		_ = balanceRows.Close()
		return result, service.ErrUserNotFound
	}
	if err := balanceRows.Scan(&result.BalanceAfter); err != nil {
		_ = balanceRows.Close()
		return result, err
	}
	if err := balanceRows.Close(); err != nil {
		return result, err
	}

	if _, err := q.ExecContext(ctx, `
INSERT INTO user_reward_credit_events (
    credit_id, user_id, event_type, event_key, amount, created_at
)
VALUES ($1, $2, 'grant', $3, $4, $5)`, result.CreditID, input.UserID, input.SourceKey, input.Amount, input.GrantedAt); err != nil {
		return result, err
	}
	return result, nil
}

func existingRewardCreditGrantResult(ctx context.Context, q rewardCreditQueryExecer, input service.RewardCreditGrant) (service.RewardCreditGrantResult, error) {
	var result service.RewardCreditGrantResult
	rows, err := q.QueryContext(ctx, `
SELECT rc.id, u.balance
FROM user_reward_credits rc
JOIN users u ON u.id = rc.user_id
WHERE rc.user_id = $1 AND rc.credit_type = $2 AND rc.source_key = $3`, input.UserID, input.CreditType, input.SourceKey)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return result, errors.New("reward credit conflict row not found")
	}
	if err := rows.Scan(&result.CreditID, &result.BalanceAfter); err != nil {
		return result, err
	}
	return result, rows.Err()
}

type expiredRewardCredit struct {
	id     int64
	userID int64
	amount float64
}

func expireRewardCreditsForUserTx(ctx context.Context, q rewardCreditQueryExecer, userID int64, now time.Time) (float64, error) {
	credits, err := selectExpiredRewardCreditsTx(ctx, q, userID, now, 0)
	if err != nil || len(credits) == 0 {
		return 0, err
	}
	total := 0.0
	for _, credit := range credits {
		if err := expireRewardCreditTx(ctx, q, credit, now); err != nil {
			return 0, err
		}
		total += credit.amount
	}
	if err := subtractExpiredRewardBalanceTx(ctx, q, userID, total); err != nil {
		return 0, err
	}
	return total, nil
}

func selectExpiredRewardCreditsTx(ctx context.Context, q rewardCreditQueryExecer, userID int64, now time.Time, limit int) ([]expiredRewardCredit, error) {
	args := []any{now}
	userWhere := ""
	if userID > 0 {
		args = append(args, userID)
		userWhere = " AND user_id = $2"
	}
	limitSQL := ""
	if limit > 0 {
		args = append(args, limit)
		limitSQL = fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := q.QueryContext(ctx, `
SELECT id, user_id, remaining_amount::double precision
FROM user_reward_credits
WHERE expires_at <= $1
  AND remaining_amount > 0
  AND expired_at IS NULL`+userWhere+`
ORDER BY expires_at ASC, id ASC`+limitSQL+`
FOR UPDATE SKIP LOCKED`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	credits := make([]expiredRewardCredit, 0)
	for rows.Next() {
		var credit expiredRewardCredit
		if err := rows.Scan(&credit.id, &credit.userID, &credit.amount); err != nil {
			return nil, err
		}
		credits = append(credits, credit)
	}
	return credits, rows.Err()
}

func expireRewardCreditTx(ctx context.Context, q rewardCreditQueryExecer, credit expiredRewardCredit, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
UPDATE user_reward_credits
SET remaining_amount = 0,
    expired_at = COALESCE(expired_at, $1),
    updated_at = $1
WHERE id = $2 AND remaining_amount > 0`, now, credit.id); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
INSERT INTO user_reward_credit_events (
    credit_id, user_id, event_type, event_key, amount, created_at
)
VALUES ($1, $2, 'expire', 'auto-expiry', $3, $4)
ON CONFLICT (credit_id, event_type, event_key) DO NOTHING`, credit.id, credit.userID, credit.amount, now)
	return err
}

func subtractExpiredRewardBalanceTx(ctx context.Context, q rewardCreditQueryExecer, userID int64, amount float64) error {
	result, err := q.ExecContext(ctx, `
UPDATE users
SET balance = balance - $1, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL`, amount, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrUserNotFound
	}
	return nil
}

func validateRewardCreditGrant(input service.RewardCreditGrant) error {
	if input.UserID <= 0 || strings.TrimSpace(input.SourceKey) == "" || len(input.SourceKey) > 191 {
		return errors.New("invalid reward credit grant")
	}
	if !validRewardCreditType(input.CreditType) {
		return errors.New("invalid reward credit type")
	}
	if input.Amount <= 0 || math.IsNaN(input.Amount) || math.IsInf(input.Amount, 0) {
		return errors.New("invalid reward credit amount")
	}
	if input.GrantedAt.IsZero() || input.ExpiresAt.IsZero() || !input.ExpiresAt.After(input.GrantedAt) {
		return errors.New("invalid reward credit expiry")
	}
	return nil
}

func validRewardCreditType(creditType service.RewardCreditType) bool {
	switch creditType {
	case service.RewardCreditDailyCheckIn, service.RewardCreditAffiliateInviter, service.RewardCreditAffiliateInvitee:
		return true
	default:
		return false
	}
}

func normalizeRewardCreditListFilter(filter service.RewardCreditListFilter) service.RewardCreditListFilter {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	if strings.TrimSpace(filter.Status) == "" {
		filter.Status = service.RewardCreditStatusActive
	}
	filter.Now = normalizedRewardCreditNow(filter.Now)
	return filter
}

func validateRewardCreditListFilter(filter service.RewardCreditListFilter) error {
	for _, creditType := range filter.CreditTypes {
		if !validRewardCreditType(creditType) {
			return errors.New("invalid reward credit type filter")
		}
	}
	switch filter.Status {
	case service.RewardCreditStatusActive, service.RewardCreditStatusExpired, service.RewardCreditStatusConsumed, service.RewardCreditStatusAll:
		return nil
	default:
		return errors.New("invalid reward credit status")
	}
}

func rewardCreditListWhere(filter service.RewardCreditListFilter) (string, []any) {
	args := []any{filter.UserID}
	clauses := []string{"user_id = $1"}
	if len(filter.CreditTypes) > 0 {
		placeholders := make([]string, 0, len(filter.CreditTypes))
		for _, creditType := range filter.CreditTypes {
			args = append(args, creditType)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "credit_type IN ("+strings.Join(placeholders, ", ")+")")
	}
	switch filter.Status {
	case service.RewardCreditStatusActive:
		args = append(args, filter.Now)
		clauses = append(clauses, fmt.Sprintf("remaining_amount > 0 AND expired_at IS NULL AND expires_at > $%d", len(args)))
	case service.RewardCreditStatusExpired:
		clauses = append(clauses, "expired_at IS NOT NULL")
	case service.RewardCreditStatusConsumed:
		clauses = append(clauses, "consumed_at IS NOT NULL")
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func normalizedRewardCreditNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
