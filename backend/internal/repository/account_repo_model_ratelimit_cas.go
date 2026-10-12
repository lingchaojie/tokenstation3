package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ModelRateLimitCompareAndSetter = (*accountRepository)(nil)

// SetModelRateLimitIfUnchanged atomically replaces exactly the model scope the
// probe observed. Comparing the complete JSON includes reason, timestamps and
// opaque fields; SQL NULL represents a missing scope, distinct from JSON null.
// Other wallets and extra fields can change independently without invalidating
// this comparison, and are preserved by the partial JSON update.
func (r *accountRepository) SetModelRateLimitIfUnchanged(ctx context.Context, id int64, scope string, expected, replacement json.RawMessage) (bool, error) {
	if scope == "" {
		return false, nil
	}
	var expectedJSON any
	if expected != nil {
		expectedJSON = []byte(expected)
	}
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, `
		UPDATE accounts
		SET extra = jsonb_set(
			COALESCE(extra, '{}'::jsonb),
			'{model_rate_limits}'::text[],
			jsonb_set(
				CASE WHEN jsonb_typeof(extra->'model_rate_limits') = 'object'
					THEN extra->'model_rate_limits' ELSE '{}'::jsonb END,
				ARRAY[$1]::text[], $2::jsonb, true
			), true
		), updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
			AND (extra #> ARRAY['model_rate_limits', $1]::text[]) IS NOT DISTINCT FROM $4::jsonb`,
		scope, []byte(replacement), id, expectedJSON)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		// A request may have installed a newer restriction. Refresh from the row
		// rather than publishing the probe's detached scheduling snapshot.
		r.syncSchedulerAccountSnapshot(ctx, id)
		return false, nil
	}
	if err := enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		logger.LegacyPrintf("repository.account", "[SchedulerOutbox] enqueue model rate limit CAS failed: account=%d err=%v", id, err)
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}
