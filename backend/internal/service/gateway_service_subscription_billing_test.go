//go:build unit

package service

import (
	"testing"
)

// TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier locks in the fix
// that subscription-mode billing honours the group (and any user-specific) rate
// multiplier — i.e. cmd.SubscriptionCost tracks ActualCost (= TotalCost *
// RateMultiplier), not raw TotalCost.
func TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	sevenDayLimit := 100.0

	tests := []struct {
		name           string
		totalCost      float64
		actualCost     float64
		isSubscription bool
		wantSub        float64
		wantBalance    float64
	}{
		{
			name:           "subscription with 2x multiplier consumes 2x quota",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: true,
			wantSub:        2.0,
			wantBalance:    0,
		},
		{
			name:           "subscription with 0.5x multiplier consumes 0.5x quota",
			totalCost:      1.0,
			actualCost:     0.5,
			isSubscription: true,
			wantSub:        0.5,
			wantBalance:    0,
		},
		{
			name:           "free subscription (multiplier 0) consumes no quota",
			totalCost:      1.0,
			actualCost:     0,
			isSubscription: true,
			wantSub:        0,
			wantBalance:    0,
		},
		{
			name:           "balance billing keeps using ActualCost (regression)",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: false,
			wantSub:        0,
			wantBalance:    2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &postUsageBillingParams{
				Cost:               &CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
				User:               &User{ID: 1},
				APIKey:             &APIKey{ID: 2, GroupID: &groupID},
				Account:            &Account{ID: 3},
				Subscription:       &UserSubscription{ID: subID, SevenDayLimitUSD: &sevenDayLimit},
				IsSubscriptionBill: tt.isSubscription,
			}

			cmd := buildUsageBillingCommand("req-1", nil, p)
			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.SubscriptionCost != tt.wantSub {
				t.Errorf("SubscriptionCost = %v, want %v", cmd.SubscriptionCost, tt.wantSub)
			}
			if cmd.BalanceCost != tt.wantBalance {
				t.Errorf("BalanceCost = %v, want %v", cmd.BalanceCost, tt.wantBalance)
			}
		})
	}
}

func TestBuildUsageBillingCommand_SelectsLedgerFromActualSubscriptionQuota(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	limit := 5.0

	tests := []struct {
		name            string
		weeklyUsage     float64
		actualCost      float64
		wantSubCost     float64
		wantBalanceCost float64
		wantBillingType int8
	}{
		{
			name:            "subscription quota covers actual cost",
			weeklyUsage:     3,
			actualCost:      2,
			wantSubCost:     2,
			wantBalanceCost: 0,
			wantBillingType: BillingTypeSubscription,
		},
		{
			name:            "balance fallback when subscription quota is insufficient",
			weeklyUsage:     4,
			actualCost:      2,
			wantSubCost:     0,
			wantBalanceCost: 2,
			wantBillingType: BillingTypeBalance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			usageLog := &UsageLog{BillingType: BillingTypeSubscription}
			p := &postUsageBillingParams{
				Cost: &CostBreakdown{TotalCost: tt.actualCost, ActualCost: tt.actualCost},
				User: &User{ID: 1, Balance: 10},
				APIKey: &APIKey{
					ID:      2,
					GroupID: &groupID,
					Group: &Group{
						ID:               groupID,
						SubscriptionType: SubscriptionTypeSubscription,
					},
				},
				Account: &Account{ID: 3},
				Subscription: &UserSubscription{
					ID:               subID,
					SevenDayLimitUSD: &limit,
					WeeklyUsageUSD:   tt.weeklyUsage,
				},
				IsSubscriptionBill: true,
			}

			cmd := buildUsageBillingCommand("req-actual-quota", usageLog, p)
			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.SubscriptionCost != tt.wantSubCost {
				t.Errorf("SubscriptionCost = %v, want %v", cmd.SubscriptionCost, tt.wantSubCost)
			}
			if cmd.BalanceCost != tt.wantBalanceCost {
				t.Errorf("BalanceCost = %v, want %v", cmd.BalanceCost, tt.wantBalanceCost)
			}
			if cmd.BillingType != tt.wantBillingType {
				t.Errorf("cmd BillingType = %v, want %v", cmd.BillingType, tt.wantBillingType)
			}
			if usageLog.BillingType != tt.wantBillingType {
				t.Errorf("usageLog BillingType = %v, want %v", usageLog.BillingType, tt.wantBillingType)
			}
			if cmd.SubscriptionSevenDayLimitUSD == nil || *cmd.SubscriptionSevenDayLimitUSD != limit {
				t.Errorf("SubscriptionSevenDayLimitUSD = %v, want %v", cmd.SubscriptionSevenDayLimitUSD, limit)
			}
		})
	}
}

func TestBuildUsageBillingCommand_PassesGroupWeeklyFallbackLimitToRepositoryGuard(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	fallbackLimit := 5.0
	usageLog := &UsageLog{BillingType: BillingTypeSubscription}
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2},
		User: &User{ID: 1, Balance: 10},
		APIKey: &APIKey{
			ID:      2,
			GroupID: &groupID,
			Group: &Group{
				ID:               groupID,
				SubscriptionType: SubscriptionTypeSubscription,
				WeeklyLimitUSD:   &fallbackLimit,
			},
		},
		Account: &Account{ID: 3},
		Subscription: &UserSubscription{
			ID:             subID,
			WeeklyUsageUSD: 3,
		},
		IsSubscriptionBill: true,
	}

	cmd := buildUsageBillingCommand("req-group-fallback-quota", usageLog, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SubscriptionSevenDayLimitUSD == nil || *cmd.SubscriptionSevenDayLimitUSD != fallbackLimit {
		t.Fatalf("SubscriptionSevenDayLimitUSD = %v, want group fallback %v", cmd.SubscriptionSevenDayLimitUSD, fallbackLimit)
	}
	if cmd.SubscriptionCost != 2 {
		t.Fatalf("SubscriptionCost = %v, want 2", cmd.SubscriptionCost)
	}
	if cmd.BillingType != BillingTypeSubscription {
		t.Fatalf("BillingType = %v, want subscription", cmd.BillingType)
	}
}

func TestBuildUsageBillingCommand_AttemptedSubscriptionEnablesFallbackDespiteStaleLowBalance(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	limit := 5.0
	usageLog := &UsageLog{BillingType: BillingTypeSubscription}
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2},
		User: &User{ID: 1, Balance: 0.01},
		APIKey: &APIKey{
			ID:      2,
			GroupID: &groupID,
			Group: &Group{
				ID:               groupID,
				SubscriptionType: SubscriptionTypeSubscription,
			},
		},
		Account: &Account{ID: 3},
		Subscription: &UserSubscription{
			ID:               subID,
			SevenDayLimitUSD: &limit,
			WeeklyUsageUSD:   3,
		},
		IsSubscriptionBill: true,
	}

	cmd := buildUsageBillingCommand("req-stale-low-balance-fallback", usageLog, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SubscriptionCost != 2 {
		t.Fatalf("SubscriptionCost = %v, want 2", cmd.SubscriptionCost)
	}
	if !cmd.AllowBalanceFallback {
		t.Fatal("AllowBalanceFallback = false, want true so repository can atomically decide fallback from DB state")
	}
	if cmd.BalanceFallbackCost != 2 {
		t.Fatalf("BalanceFallbackCost = %v, want 2", cmd.BalanceFallbackCost)
	}
	if cmd.BalanceCost != 0 {
		t.Fatalf("BalanceCost = %v, want 0 for attempted subscription billing", cmd.BalanceCost)
	}
	if cmd.BillingType != BillingTypeSubscription || usageLog.BillingType != BillingTypeSubscription {
		t.Fatalf("billing types cmd=%v usageLog=%v, want subscription until repository reports actual ledger", cmd.BillingType, usageLog.BillingType)
	}
}

func TestBuildUsageBillingCommand_AbsentEffectiveSevenDayLimitFallsBackToCoveringBalance(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	usageLog := &UsageLog{BillingType: BillingTypeSubscription}
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2},
		User: &User{ID: 1, Balance: 10},
		APIKey: &APIKey{
			ID:      2,
			GroupID: &groupID,
			Group: &Group{
				ID:               groupID,
				SubscriptionType: SubscriptionTypeSubscription,
			},
		},
		Account:            &Account{ID: 3},
		Subscription:       &UserSubscription{ID: subID},
		IsSubscriptionBill: true,
	}

	cmd := buildUsageBillingCommand("req-absent-quota-balance", usageLog, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SubscriptionSevenDayLimitUSD != nil {
		t.Fatalf("SubscriptionSevenDayLimitUSD = %v, want nil", cmd.SubscriptionSevenDayLimitUSD)
	}
	if cmd.SubscriptionCost != 0 {
		t.Fatalf("SubscriptionCost = %v, want 0", cmd.SubscriptionCost)
	}
	if cmd.BalanceCost != 2 {
		t.Fatalf("BalanceCost = %v, want 2", cmd.BalanceCost)
	}
	if cmd.BillingType != BillingTypeBalance {
		t.Fatalf("BillingType = %v, want balance", cmd.BillingType)
	}
	if usageLog.BillingType != BillingTypeBalance {
		t.Fatalf("usageLog BillingType = %v, want balance", usageLog.BillingType)
	}
}

func TestBuildUsageBillingCommand_AbsentEffectiveSevenDayLimitWithZeroBalanceDoesNotBillSubscription(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)
	usageLog := &UsageLog{BillingType: BillingTypeSubscription}
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2},
		User: &User{ID: 1, Balance: 0},
		APIKey: &APIKey{
			ID:      2,
			GroupID: &groupID,
			Group: &Group{
				ID:               groupID,
				SubscriptionType: SubscriptionTypeSubscription,
			},
		},
		Account:            &Account{ID: 3},
		Subscription:       &UserSubscription{ID: subID},
		IsSubscriptionBill: true,
	}

	cmd := buildUsageBillingCommand("req-absent-quota-zero-balance", usageLog, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SubscriptionSevenDayLimitUSD != nil {
		t.Fatalf("SubscriptionSevenDayLimitUSD = %v, want nil", cmd.SubscriptionSevenDayLimitUSD)
	}
	if cmd.SubscriptionCost != 0 {
		t.Fatalf("SubscriptionCost = %v, want 0", cmd.SubscriptionCost)
	}
	if cmd.BalanceCost != 2 {
		t.Fatalf("BalanceCost = %v, want balance debit to be rejected by insufficient balance guard", cmd.BalanceCost)
	}
	if cmd.BillingType != BillingTypeBalance {
		t.Fatalf("BillingType = %v, want balance", cmd.BillingType)
	}
}
