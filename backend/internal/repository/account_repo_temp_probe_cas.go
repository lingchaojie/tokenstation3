package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.TempUnschedulableCompareAndSetter = (*accountRepository)(nil)

// CompareAndSetTempUnschedulable replaces only the restriction pair observed by
// a probe. Timestamp equality uses the persisted precision, and NULL reason is
// equivalent to the empty reason exposed by Account. Balance/extra persistence
// can change updated_at independently without invalidating this comparison.
func (r *accountRepository) CompareAndSetTempUnschedulable(ctx context.Context, id int64, expectedUntil *time.Time, expectedReason string, until *time.Time, reason string) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	var replacementReason any
	if until != nil {
		replacementReason = reason
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts
			SET temp_unschedulable_until = $1,
				temp_unschedulable_reason = $2,
				updated_at = NOW()
			WHERE id = $3 AND deleted_at IS NULL
				AND temp_unschedulable_until IS NOT DISTINCT FROM $4::timestamptz
				AND COALESCE(temp_unschedulable_reason, '') = $5
			RETURNING id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $6, updated.id, NULL, NULL FROM updated
	`, until, replacementReason, id, expectedUntil, expectedReason, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	// A mismatch means a request or administrator may have installed a newer
	// restriction. Propagate the authoritative row even when our write was rejected.
	r.syncTempProbeSchedulerSnapshot(ctx, id)
	return affected > 0, nil
}

func (r *accountRepository) syncTempProbeSchedulerSnapshot(ctx context.Context, id int64) {
	if r.schedulerCache == nil {
		return
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	propagationCtx, cancel := context.WithTimeout(base, 2*time.Second)
	defer cancel()
	account, err := r.GetByID(propagationCtx, id)
	if errors.Is(err, service.ErrAccountNotFound) {
		r.deleteSchedulerAccountSnapshot(propagationCtx, id)
		return
	}
	if err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] sync temp probe snapshot read failed: id=%d err=%v", id, err)
		return
	}
	if err := r.schedulerCache.SetAccount(propagationCtx, account); err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] sync temp probe snapshot write failed: id=%d err=%v", id, err)
	}
}
