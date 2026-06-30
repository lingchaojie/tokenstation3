//go:build unit

package service

import (
	"context"
	"testing"
)

type groupCapacityAccountRepoStub struct {
	mockAccountRepoForPlatform
	accounts []Account
}

func (s *groupCapacityAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return s.accounts, nil
}

func TestGroupCapacityRPMUsesSupportedAccountsOnly(t *testing.T) {
	svc := NewGroupCapacityService(
		&groupCapacityAccountRepoStub{
			accounts: []Account{
				{
					ID:       1,
					Platform: PlatformAnthropic,
					Type:     AccountTypeOAuth,
					Extra:    map[string]any{"base_rpm": 10},
				},
				{
					ID:       2,
					Platform: PlatformKiro,
					Type:     AccountTypeOAuth,
					Extra:    map[string]any{"base_rpm": 20},
				},
				{
					ID:          3,
					Platform:    PlatformKiro,
					Type:        AccountTypeAPIKey,
					Credentials: map[string]any{"base_url": "https://relay.example.com"},
					Extra:       map[string]any{"base_rpm": 30},
				},
				{
					ID:       4,
					Platform: PlatformAnthropic,
					Type:     AccountTypeAPIKey,
					Extra:    map[string]any{"base_rpm": 40},
				},
			},
		},
		nil,
		NewConcurrencyService(nil),
		nil,
		&stubRPMCacheForAccountRPMTest{counts: map[int64]int{
			1: 3,
			2: 4,
			3: 5,
			4: 6,
		}},
	)

	summary, err := svc.getGroupCapacity(context.Background(), 1)
	if err != nil {
		t.Fatalf("getGroupCapacity returned error: %v", err)
	}
	if summary.RPMMax != 30 {
		t.Fatalf("RPMMax = %d, want 30", summary.RPMMax)
	}
	if summary.RPMUsed != 7 {
		t.Fatalf("RPMUsed = %d, want 7", summary.RPMUsed)
	}
}
