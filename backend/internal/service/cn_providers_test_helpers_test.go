//go:build unit

package service

import "context"

// cnBalanceProbeRepo records the snapshot writes used by provider account probes.
// The local balance tests use cnBalanceStateRepo for pause/clear state instead;
// imported Command Code/Cline tests need only account loading and extra writes.
type cnBalanceProbeRepo struct {
	AccountRepository
	account     *Account
	extraWrites []map[string]any
}

func (r *cnBalanceProbeRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *cnBalanceProbeRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.extraWrites = append(r.extraWrites, updates)
	return nil
}
