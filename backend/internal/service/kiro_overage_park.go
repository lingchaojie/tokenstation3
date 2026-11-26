package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// KiroOverageLimitCode is the reason persisted for a native Kiro monthly overage park.
const KiroOverageLimitCode = kiroOverageLimitCode

// KiroOverageParkReader reads active monthly overage parks from the authoritative account row.
type KiroOverageParkReader interface {
	GetActiveKiroOveragePark(ctx context.Context, id int64) (*time.Time, error)
}

func readActiveKiroOveragePark(ctx context.Context, repo AccountRepository, id int64) (*time.Time, error) {
	reader, ok := repo.(KiroOverageParkReader)
	if !ok || id <= 0 {
		return nil, nil
	}
	return reader.GetActiveKiroOveragePark(ctx, id)
}

const kiroOverageLookupFailed GatewayFailureReason = "kiro_overage_park_lookup_failed"

func (s *GatewayService) checkKiroOveragePark(ctx context.Context, account *Account) error {
	if !isKiroDirectModeAccount(account) {
		return nil
	}
	until, err := readActiveKiroOveragePark(ctx, s.accountRepo, account.ID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		slog.WarnContext(ctx, "kiro.overage_park_lookup_failed", "account_id", account.ID, "error", err)
		return &UpstreamFailoverError{
			StatusCode: http.StatusServiceUnavailable, Platform: PlatformKiro,
			Stage: GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
			Reason: kiroOverageLookupFailed, NextAccountAction: NextAccountRetry,
			ClientMessage: "Kiro quota status is temporarily unavailable",
		}
	}
	if until == nil {
		return nil
	}
	return &UpstreamFailoverError{
		StatusCode: http.StatusPaymentRequired, Platform: PlatformKiro,
		Stage: GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
		Reason: kiroOverageLimitCode, NextAccountAction: NextAccountRetry,
		ClientMessage: "Kiro monthly overage quota is exhausted",
	}
}

func isKiroOverageGateFailure(err error) bool {
	var failure *UpstreamFailoverError
	return errors.As(err, &failure) &&
		(failure.Reason == kiroOverageLimitCode || failure.Reason == kiroOverageLookupFailed)
}
