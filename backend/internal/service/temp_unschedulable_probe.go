package service

import (
	"context"
	"time"
)

// TempUnschedulableCompareAndSetter is the narrow mutation capability for
// asynchronous probes. The expected deadline and reason must still match the
// stored pair; unrelated account updates do not invalidate the comparison.
// A nil replacement deadline clears both fields. Empty expected reason matches
// both SQL NULL and empty string, which Account represents identically.
type TempUnschedulableCompareAndSetter interface {
	CompareAndSetTempUnschedulable(ctx context.Context, id int64, expectedUntil *time.Time, expectedReason string, until *time.Time, reason string) (bool, error)
}

// replaceProbeTempUnschedulable fails closed when the repository cannot compare
// the observed restriction. A rejected probe must not publish its detached state.
func replaceProbeTempUnschedulable(ctx context.Context, repo AccountRepository, account *Account, until *time.Time, reason string) (bool, error) {
	setter, ok := repo.(TempUnschedulableCompareAndSetter)
	if !ok || account == nil || account.ID <= 0 {
		return false, nil
	}
	if until == nil {
		reason = ""
	} else {
		// PostgreSQL stores microseconds. Normalize only the replacement, never
		// the loaded expectation, so the successful local pair is persisted exactly.
		value := until.UTC().Truncate(time.Microsecond)
		until = &value
	}
	applied, err := setter.CompareAndSetTempUnschedulable(ctx, account.ID,
		account.TempUnschedulableUntil, account.TempUnschedulableReason, until, reason)
	if err != nil || !applied {
		return false, err
	}
	account.TempUnschedulableUntil, account.TempUnschedulableReason = until, reason
	return true, nil
}
