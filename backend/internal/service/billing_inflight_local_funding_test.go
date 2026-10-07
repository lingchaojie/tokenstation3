//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type localFundingInflightCache struct {
	*memInflightCache
	snapshot     BillingBalanceSnapshot
	weeklyUsage  float64
	lastBalance  float64
	reserveCalls int
}

func (c *localFundingInflightCache) GetUserBalanceSnapshot(context.Context, int64) (BillingBalanceSnapshot, error) {
	return c.snapshot, nil
}
func (c *localFundingInflightCache) SetUserBalanceSnapshot(context.Context, int64, BillingBalanceSnapshot) error {
	return nil
}
func (c *localFundingInflightCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour), WeeklyUsage: c.weeklyUsage}, nil
}
func (c *localFundingInflightCache) ReserveInflightBalance(ctx context.Context, uid int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	c.lastBalance = balance
	c.reserveCalls++
	return c.memInflightCache.ReserveInflightBalance(ctx, uid, id, amount, balance, ttl)
}

func TestInflightLocalFunding(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		daily, affiliate, account, used          float64
		subscription, fallback, disabled, simple bool
		wantReserve                              bool
		wantBalance                              float64
		wantErr                                  error
	}{
		{name: "default off", account: 100, disabled: true},
		{name: "simple mode", account: 100, simple: true},
		{name: "subscription quota only", account: 100, subscription: true},
		{name: "rewards before subscription", daily: 4, affiliate: 7, account: 100, subscription: true, wantReserve: true, wantBalance: 7},
		{name: "rewards before exhausted subscription without fallback", affiliate: 7, subscription: true, used: 10, wantReserve: true, wantBalance: 7},
		{name: "weekly exhausted fallback on", account: 20, subscription: true, used: 10, fallback: true, wantReserve: true, wantBalance: 20},
		{name: "weekly exhausted fallback off", account: 20, subscription: true, used: 10, wantErr: ErrWeeklyLimitExceeded},
		{name: "balance layers never combine", daily: 4, affiliate: 7, account: 6, wantReserve: true, wantBalance: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &localFundingInflightCache{
				memInflightCache: newMemInflightCache(tc.daily + tc.affiliate + tc.account),
				snapshot:         BillingBalanceSnapshot{SchemaVersion: BillingBalanceSnapshotSchemaV1, TotalBalance: tc.daily + tc.affiliate + tc.account, DailyRewardBalance: tc.daily, AffiliateRewardBalance: tc.affiliate, AccountBalance: tc.account},
				weeklyUsage:      tc.used,
			}
			cfg := &config.Config{}
			cfg.Billing.InflightReservation.Enabled = !tc.disabled
			if tc.simple {
				cfg.RunMode = config.RunModeSimple
			}
			svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(svc.Stop)
			user := &User{ID: 1, SubscriptionBalanceFallbackEnabled: tc.fallback}
			var sub *UserSubscription
			limit := 10.0
			now := time.Now()
			group := &Group{ID: 9, SubscriptionType: SubscriptionTypeSubscription}
			if tc.subscription {
				sub = &UserSubscription{ID: 2, GroupID: group.ID, Status: SubscriptionStatusActive, SevenDayLimitUSD: &limit, WeeklyUsageUSD: tc.used, WeeklyWindowStart: &now, ExpiresAt: now.Add(time.Hour)}
			}
			res, err := svc.ReserveInflight(context.Background(), user, group, sub, 5)
			defer res.HandlerDone()
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, cache.reserveCalls)
				return
			}
			require.NoError(t, err)
			if !tc.wantReserve {
				require.Nil(t, res)
				require.Zero(t, cache.reserveCalls)
				return
			}
			require.NotNil(t, res)
			require.Equal(t, tc.wantBalance, cache.lastBalance)
			// The first request keeps upstream's admission rule. A second
			// request cannot spend the sum of mutually exclusive layers.
			if tc.wantBalance < 10 {
				second, err := svc.ReserveInflight(context.Background(), user, group, sub, 5)
				defer second.HandlerDone()
				require.ErrorIs(t, err, ErrInsufficientBalance)
			}
		})
	}
}
