//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUsageBillingRepositoryApply_DeduplicatesBalanceBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-" + uuid.NewString(),
		Name:   "billing",
		Quota:  1,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "usage-billing-account-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
	})

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:           requestID,
		APIKeyID:            apiKey.ID,
		UserID:              user.ID,
		AccountID:           account.ID,
		AccountType:         service.AccountTypeAPIKey,
		BalanceCost:         1.25,
		APIKeyQuotaCost:     1.25,
		APIKeyRateLimitCost: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result1)
	require.True(t, result1.Applied)
	require.True(t, result1.APIKeyQuotaExhausted)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result2)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id = $1", apiKey.ID).Scan(&quotaUsed))
	require.InDelta(t, 1.25, quotaUsed, 0.000001)

	var usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT usage_5h FROM api_keys WHERE id = $1", apiKey.ID).Scan(&usage5h))
	require.InDelta(t, 1.25, usage5h, 0.000001)

	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT status FROM api_keys WHERE id = $1", apiKey.ID).Scan(&status))
	require.Equal(t, service.StatusAPIKeyQuotaExhausted, status)

	var dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, apiKey.ID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}

func TestUsageBillingRepositoryApply_AllowsPostUsageBalanceOverdraft(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-overdraft-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      0.01,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-overdraft-" + uuid.NewString(),
		Name:   "billing-overdraft",
	})

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   uuid.NewString(),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 0.05,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -0.04, *result.NewBalance, 0.000001)
	require.Equal(t, service.BillingTypeBalance, result.BillingType)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, -0.04, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_DeduplicatesSubscriptionBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-sub-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-sub-" + uuid.NewString(),
		Name:    "billing-sub",
	})
	limit := 10.0
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:           user.ID,
		GroupID:          group.ID,
		SevenDayLimitUSD: &limit,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1 WHERE id = $2", limit, subscription.ID)
	require.NoError(t, err)

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:                    requestID,
		APIKeyID:                     apiKey.ID,
		UserID:                       user.ID,
		AccountID:                    0,
		SubscriptionID:               &subscription.ID,
		SubscriptionCost:             2.5,
		SubscriptionSevenDayLimitUSD: &limit,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 2.5, dailyUsage, 0.000001)
}

func TestUsageBillingRepositoryApply_ResetBoundaryBillsSubscriptionBeforeBalanceFallback(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-reset-boundary-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-reset-boundary-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-reset-boundary-" + uuid.NewString(),
		Name:    "billing-reset-boundary",
	})
	limit := 5.0
	windowStart := time.Now().Add(-7 * 24 * time.Hour)
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:            user.ID,
		GroupID:           group.ID,
		SevenDayLimitUSD:  &limit,
		WeeklyWindowStart: &windowStart,
		WeeklyUsageUSD:    limit,
	})
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE user_subscriptions
		SET seven_day_limit_usd = $1,
			weekly_window_start = $2,
			weekly_usage_usd = $3
		WHERE id = $4
	`, limit, windowStart, limit, subscription.ID)
	require.NoError(t, err)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:                    uuid.NewString(),
		APIKeyID:                     apiKey.ID,
		UserID:                       user.ID,
		SubscriptionID:               &subscription.ID,
		SubscriptionCost:             0.5,
		BalanceFallbackCost:          0.5,
		AllowBalanceFallback:         true,
		SubscriptionSevenDayLimitUSD: &limit,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Nil(t, result.NewBalance)
	require.Equal(t, service.BillingTypeSubscription, result.BillingType)

	var weeklyUsage float64
	var resetStart time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd, weekly_window_start FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage, &resetStart))
	require.InDelta(t, 0.5, weeklyUsage, 0.000001)
	require.True(t, resetStart.After(windowStart), "weekly window should reset before increment")

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 100, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_RejectsGuardedSubscriptionQuotaInsufficientWithoutFallback(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-no-fallback-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-no-fallback-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-no-fallback-" + uuid.NewString(),
		Name:    "billing-no-fallback",
	})
	limit := 5.0
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:           user.ID,
		GroupID:          group.ID,
		SevenDayLimitUSD: &limit,
		WeeklyUsageUSD:   4,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1 WHERE id = $2", limit, subscription.ID)
	require.NoError(t, err)

	_, err = repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:                    uuid.NewString(),
		APIKeyID:                     apiKey.ID,
		UserID:                       user.ID,
		SubscriptionID:               &subscription.ID,
		SubscriptionCost:             2,
		SubscriptionSevenDayLimitUSD: &limit,
	})
	require.ErrorIs(t, err, service.ErrWeeklyLimitExceeded)

	var weeklyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
	require.InDelta(t, 4, weeklyUsage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 100, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_RecordsNoFallbackPostUsageQuotaCrossing(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-post-usage-cross-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-post-usage-cross-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-post-usage-cross-" + uuid.NewString(),
		Name:    "billing-post-usage-cross",
	})
	limit := 5.0
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:           user.ID,
		GroupID:          group.ID,
		SevenDayLimitUSD: &limit,
		WeeklyUsageUSD:   4,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1 WHERE id = $2", limit, subscription.ID)
	require.NoError(t, err)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:                     uuid.NewString(),
		APIKeyID:                      apiKey.ID,
		UserID:                        user.ID,
		SubscriptionID:                &subscription.ID,
		SubscriptionCost:              2,
		SubscriptionSevenDayLimitUSD:  &limit,
		AllowSubscriptionQuotaOverrun: true,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, service.BillingTypeSubscription, result.BillingType)

	var weeklyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
	require.InDelta(t, 6, weeklyUsage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 100, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_FallsBackToBalanceWhenGuardedSubscriptionQuotaInsufficient(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-fallback-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-fallback-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-fallback-" + uuid.NewString(),
		Name:    "billing-fallback",
	})
	limit := 5.0
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:           user.ID,
		GroupID:          group.ID,
		SevenDayLimitUSD: &limit,
		WeeklyUsageUSD:   4,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1 WHERE id = $2", limit, subscription.ID)
	require.NoError(t, err)

	cmd := &service.UsageBillingCommand{
		RequestID:                    uuid.NewString(),
		APIKeyID:                     apiKey.ID,
		UserID:                       user.ID,
		SubscriptionID:               &subscription.ID,
		SubscriptionCost:             2,
		BalanceFallbackCost:          2,
		AllowBalanceFallback:         true,
		SubscriptionSevenDayLimitUSD: &limit,
	}

	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 98, *result.NewBalance, 0.000001)
	require.Equal(t, service.BillingTypeBalance, result.BillingType)

	var weeklyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
	require.InDelta(t, 4, weeklyUsage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_FallbackBalanceCanOverdraftAfterSubscriptionGuardMiss(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-fallback-overdraft-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      0.01,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-fallback-overdraft-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-fallback-overdraft-" + uuid.NewString(),
		Name:    "billing-fallback-overdraft",
	})
	limit := 5.0
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:           user.ID,
		GroupID:          group.ID,
		SevenDayLimitUSD: &limit,
		WeeklyUsageUSD:   4,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1 WHERE id = $2", limit, subscription.ID)
	require.NoError(t, err)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:                    uuid.NewString(),
		APIKeyID:                     apiKey.ID,
		UserID:                       user.ID,
		SubscriptionID:               &subscription.ID,
		SubscriptionCost:             2,
		BalanceFallbackCost:          2,
		AllowBalanceFallback:         true,
		SubscriptionSevenDayLimitUSD: &limit,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -1.99, *result.NewBalance, 0.000001)
	require.Equal(t, service.BillingTypeBalance, result.BillingType)

	var weeklyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
	require.InDelta(t, 4, weeklyUsage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, -1.99, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_GuardsWithGroupWeeklyFallbackLimitWhenSnapshotLimitNil(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	newFixture := func(t *testing.T, weeklyUsage float64) (*service.User, *service.APIKey, *service.UserSubscription, float64) {
		t.Helper()
		user := mustCreateUser(t, client, &service.User{
			Email:        fmt.Sprintf("usage-billing-group-fallback-user-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()),
			PasswordHash: "hash",
			Balance:      100,
		})
		weeklyLimit := 5.0
		group := mustCreateGroup(t, client, &service.Group{
			Name:             "usage-billing-group-fallback-" + uuid.NewString(),
			Platform:         service.PlatformAnthropic,
			SubscriptionType: service.SubscriptionTypeSubscription,
			WeeklyLimitUSD:   &weeklyLimit,
		})
		apiKey := mustCreateApiKey(t, client, &service.APIKey{
			UserID:  user.ID,
			GroupID: &group.ID,
			Key:     "sk-usage-billing-group-fallback-" + uuid.NewString(),
			Name:    "billing-group-fallback",
		})
		subscription := mustCreateSubscription(t, client, &service.UserSubscription{
			UserID:         user.ID,
			GroupID:        group.ID,
			WeeklyUsageUSD: weeklyUsage,
		})
		return user, apiKey, subscription, weeklyLimit
	}

	t.Run("within effective group limit increments subscription even when snapshot limit is nil", func(t *testing.T) {
		user, apiKey, subscription, weeklyLimit := newFixture(t, 3)
		cmd := &service.UsageBillingCommand{
			RequestID:                    uuid.NewString(),
			APIKeyID:                     apiKey.ID,
			UserID:                       user.ID,
			SubscriptionID:               &subscription.ID,
			SubscriptionCost:             2,
			BalanceFallbackCost:          2,
			AllowBalanceFallback:         true,
			SubscriptionSevenDayLimitUSD: &weeklyLimit,
		}

		result, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, result.Applied)
		require.Nil(t, result.NewBalance)
		require.Equal(t, service.BillingTypeSubscription, result.BillingType)

		var weeklyUsage float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
		require.InDelta(t, 5, weeklyUsage, 0.000001)

		var balance float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
		require.InDelta(t, 100, balance, 0.000001)
	})

	t.Run("over effective group limit falls back to balance", func(t *testing.T) {
		user, apiKey, subscription, weeklyLimit := newFixture(t, 4)
		cmd := &service.UsageBillingCommand{
			RequestID:                    uuid.NewString(),
			APIKeyID:                     apiKey.ID,
			UserID:                       user.ID,
			SubscriptionID:               &subscription.ID,
			SubscriptionCost:             2,
			BalanceFallbackCost:          2,
			AllowBalanceFallback:         true,
			SubscriptionSevenDayLimitUSD: &weeklyLimit,
		}

		result, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, result.Applied)
		require.NotNil(t, result.NewBalance)
		require.InDelta(t, 98, *result.NewBalance, 0.000001)
		require.Equal(t, service.BillingTypeBalance, result.BillingType)

		var weeklyUsage float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
		require.InDelta(t, 4, weeklyUsage, 0.000001)

		var balance float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
		require.InDelta(t, 98, balance, 0.000001)
	})
}

func TestUsageBillingRepositoryApply_RejectsUnguardedSubscriptionBillingWithoutEffectiveLimit(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-no-limit-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-no-limit-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-no-limit-" + uuid.NewString(),
		Name:    "billing-no-limit",
	})
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:  user.ID,
		GroupID: group.ID,
	})

	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:        uuid.NewString(),
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		SubscriptionID:   &subscription.ID,
		SubscriptionCost: 2,
	})
	require.ErrorIs(t, err, service.ErrWeeklyLimitExceeded)

	var weeklyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
	require.InDelta(t, 0, weeklyUsage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 100, balance, 0.000001)
}

func TestUsageBillingRepositoryApply_RequestFingerprintConflict(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-conflict-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-conflict-" + uuid.NewString(),
		Name:   "billing-conflict",
	})

	requestID := uuid.NewString()
	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 1.25,
	})
	require.NoError(t, err)

	_, err = repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 2.50,
	})
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
}

func TestUsageBillingRepositoryApply_RewardFundingUsesFirstCompleteLayer(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	usageRepo := NewUsageBillingRepository(client, integrationDB)
	rewardRepo := NewRewardCreditRepository(client, integrationDB)
	now := time.Now().UTC()

	newUserAndKey := func(t *testing.T, ownBalance float64) (*service.User, *service.APIKey) {
		t.Helper()
		user := mustCreateUser(t, client, &service.User{
			Email:        fmt.Sprintf("usage-reward-layer-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()),
			PasswordHash: "hash",
			Balance:      ownBalance,
		})
		apiKey := mustCreateApiKey(t, client, &service.APIKey{
			UserID: user.ID,
			Key:    "sk-usage-reward-layer-" + uuid.NewString(),
			Name:   "reward-layer",
		})
		return user, apiKey
	}

	grant := func(t *testing.T, userID int64, creditType service.RewardCreditType, source string, amount float64, expiresAt time.Time) int64 {
		t.Helper()
		result, err := rewardRepo.Grant(ctx, service.RewardCreditGrant{
			UserID:     userID,
			CreditType: creditType,
			SourceKey:  source,
			Amount:     amount,
			GrantedAt:  now,
			ExpiresAt:  expiresAt,
		})
		require.NoError(t, err)
		return result.CreditID
	}

	remaining := func(t *testing.T, creditID int64) float64 {
		t.Helper()
		var amount float64
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT remaining_amount FROM user_reward_credits WHERE id = $1", creditID,
		).Scan(&amount))
		return amount
	}

	t.Run("daily check-in fully covers and lower layers remain untouched", func(t *testing.T) {
		user, apiKey := newUserAndKey(t, 100)
		dailyID := grant(t, user.ID, service.RewardCreditDailyCheckIn, "daily:"+uuid.NewString(), 10, now.Add(time.Hour))
		affiliateID := grant(t, user.ID, service.RewardCreditAffiliateInviter, "affiliate:"+uuid.NewString(), 20, now.Add(24*time.Hour))

		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID, BalanceCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingDailyCheckIn, result.FundingSource)
		require.InDelta(t, 2, remaining(t, dailyID), 0.000001)
		require.InDelta(t, 20, remaining(t, affiliateID), 0.000001)
	})

	t.Run("insufficient daily stays whole while affiliate lots combine FIFO", func(t *testing.T) {
		user, apiKey := newUserAndKey(t, 100)
		dailyID := grant(t, user.ID, service.RewardCreditDailyCheckIn, "daily:"+uuid.NewString(), 3, now.Add(time.Hour))
		firstAffiliateID := grant(t, user.ID, service.RewardCreditAffiliateInvitee, "affiliate:"+uuid.NewString(), 5, now.Add(2*time.Hour))
		secondAffiliateID := grant(t, user.ID, service.RewardCreditAffiliateInviter, "affiliate:"+uuid.NewString(), 5, now.Add(3*time.Hour))

		requestID := uuid.NewString()
		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: requestID, APIKeyID: apiKey.ID, UserID: user.ID, BalanceCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingAffiliate, result.FundingSource)
		require.InDelta(t, 3, remaining(t, dailyID), 0.000001)
		require.InDelta(t, 0, remaining(t, firstAffiliateID), 0.000001)
		require.InDelta(t, 2, remaining(t, secondAffiliateID), 0.000001)

		var consumed float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM user_reward_credit_events
			WHERE request_id = $1 AND event_type = 'consume'`, requestID).Scan(&consumed))
		require.InDelta(t, 8, consumed, 0.000001)

		replay, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: requestID, APIKeyID: apiKey.ID, UserID: user.ID, BalanceCost: 8,
		})
		require.NoError(t, err)
		require.False(t, replay.Applied)
		require.InDelta(t, 2, remaining(t, secondAffiliateID), 0.000001)
	})

	t.Run("insufficient reward layers do not combine and account records debt", func(t *testing.T) {
		user, apiKey := newUserAndKey(t, 0)
		dailyID := grant(t, user.ID, service.RewardCreditDailyCheckIn, "daily:"+uuid.NewString(), 5, now.Add(time.Hour))
		affiliateID := grant(t, user.ID, service.RewardCreditAffiliateInvitee, "affiliate:"+uuid.NewString(), 5, now.Add(2*time.Hour))

		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID, BalanceCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingAccount, result.FundingSource)
		require.True(t, result.BalanceOverdrafted)
		require.InDelta(t, 5, remaining(t, dailyID), 0.000001)
		require.InDelta(t, 5, remaining(t, affiliateID), 0.000001)
		require.NotNil(t, result.NewBalance)
		require.InDelta(t, 2, *result.NewBalance, 0.000001)
	})

	t.Run("expired reward is lazily cleared and cannot fund the request", func(t *testing.T) {
		user, apiKey := newUserAndKey(t, 100)
		expiredGrant, err := rewardRepo.Grant(ctx, service.RewardCreditGrant{
			UserID: user.ID, CreditType: service.RewardCreditDailyCheckIn,
			SourceKey: "expired:" + uuid.NewString(), Amount: 10,
			GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
		})
		require.NoError(t, err)

		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID, BalanceCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingAccount, result.FundingSource)
		require.InDelta(t, 0, remaining(t, expiredGrant.CreditID), 0.000001)
		require.NotNil(t, result.NewBalance)
		require.InDelta(t, 92, *result.NewBalance, 0.000001)
	})
}

func TestUsageBillingRepositoryApply_RewardsPrecedeSubscriptionAndFallbackHonorsSwitch(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	usageRepo := NewUsageBillingRepository(client, integrationDB)
	rewardRepo := NewRewardCreditRepository(client, integrationDB)
	now := time.Now().UTC()
	limit := 5.0

	newFixture := func(t *testing.T, weeklyUsage float64, ownBalance float64) (*service.User, *service.APIKey, *service.UserSubscription) {
		t.Helper()
		user := mustCreateUser(t, client, &service.User{
			Email: fmt.Sprintf("usage-reward-sub-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()), PasswordHash: "hash", Balance: ownBalance,
		})
		group := mustCreateGroup(t, client, &service.Group{
			Name: "usage-reward-sub-" + uuid.NewString(), Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeSubscription,
		})
		apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-usage-reward-sub-" + uuid.NewString(), Name: "reward-sub"})
		subscription := mustCreateSubscription(t, client, &service.UserSubscription{UserID: user.ID, GroupID: group.ID, SevenDayLimitUSD: &limit, WeeklyUsageUSD: weeklyUsage})
		_, err := integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET seven_day_limit_usd = $1, weekly_usage_usd = $2 WHERE id = $3", limit, weeklyUsage, subscription.ID)
		require.NoError(t, err)
		return user, apiKey, subscription
	}

	grantRewards := func(t *testing.T, userID int64, daily, affiliate float64) {
		t.Helper()
		for _, item := range []struct {
			creditType service.RewardCreditType
			amount     float64
			expiresAt  time.Time
		}{
			{service.RewardCreditDailyCheckIn, daily, now.Add(time.Hour)},
			{service.RewardCreditAffiliateInviter, affiliate, now.Add(24 * time.Hour)},
		} {
			if item.amount <= 0 {
				continue
			}
			_, err := rewardRepo.Grant(ctx, service.RewardCreditGrant{
				UserID: userID, CreditType: item.creditType, SourceKey: "reward:" + uuid.NewString(), Amount: item.amount, GrantedAt: now, ExpiresAt: item.expiresAt,
			})
			require.NoError(t, err)
		}
	}

	t.Run("subscription is used only after both reward layers are insufficient", func(t *testing.T) {
		user, apiKey, subscription := newFixture(t, 0, 100)
		grantRewards(t, user.ID, 1, 1)
		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID,
			SubscriptionID: &subscription.ID, SubscriptionCost: 4, SubscriptionSevenDayLimitUSD: &limit,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingSubscription, result.FundingSource)

		var weeklyUsage float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT weekly_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&weeklyUsage))
		require.InDelta(t, 4, weeklyUsage, 0.000001)
	})

	t.Run("subscription insufficiency does not use account when fallback is off", func(t *testing.T) {
		user, apiKey, subscription := newFixture(t, 4, 100)
		grantRewards(t, user.ID, 1, 1)
		_, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID,
			SubscriptionID: &subscription.ID, SubscriptionCost: 2, SubscriptionSevenDayLimitUSD: &limit,
		})
		require.ErrorIs(t, err, service.ErrWeeklyLimitExceeded)

		var balance float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
		require.InDelta(t, 102, balance, 0.000001)
	})

	t.Run("subscription insufficiency uses account when fallback is on", func(t *testing.T) {
		user, apiKey, subscription := newFixture(t, 4, 100)
		grantRewards(t, user.ID, 1, 1)
		result, err := usageRepo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKey.ID, UserID: user.ID,
			SubscriptionID: &subscription.ID, SubscriptionCost: 2, BalanceFallbackCost: 2,
			AllowBalanceFallback: true, SubscriptionSevenDayLimitUSD: &limit,
		})
		require.NoError(t, err)
		require.Equal(t, service.UsageFundingAccount, result.FundingSource)
		require.NotNil(t, result.NewBalance)
		require.InDelta(t, 100, *result.NewBalance, 0.000001)
	})
}

func TestUsageBillingRepositoryApply_UpdatesAccountQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-account-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-account-" + uuid.NewString(),
		Name:   "billing-account",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "usage-billing-account-quota-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
		Extra: map[string]any{
			"quota_limit": 100.0,
		},
	})

	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:        uuid.NewString(),
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		AccountID:        account.ID,
		AccountType:      service.AccountTypeAPIKey,
		AccountQuotaCost: 3.5,
	})
	require.NoError(t, err)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", account.ID).Scan(&quotaUsed))
	require.InDelta(t, 3.5, quotaUsed, 0.000001)
}

func TestUsageBillingRepositoryApply_UpdatesKiroOAuthAccountQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-kiro-account-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-kiro-account-" + uuid.NewString(),
		Name:   "billing-kiro-account",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name:     "usage-billing-kiro-account-quota-" + uuid.NewString(),
		Platform: service.PlatformKiro,
		Type:     service.AccountTypeOAuth,
		Extra: map[string]any{
			"quota_limit": 100.0,
		},
	})

	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:        uuid.NewString(),
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		AccountID:        account.ID,
		AccountPlatform:  service.PlatformKiro,
		AccountType:      service.AccountTypeOAuth,
		AccountQuotaCost: 4.25,
	})
	require.NoError(t, err)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", account.ID).Scan(&quotaUsed))
	require.InDelta(t, 4.25, quotaUsed, 0.000001)
}

func TestUsageBillingRepositoryApply_EnqueuesSchedulerOutboxOnQuotaCrossing(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	newFixture := func(t *testing.T, extra map[string]any) (int64, int64) {
		t.Helper()
		user := mustCreateUser(t, client, &service.User{
			Email:        fmt.Sprintf("usage-billing-outbox-user-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()),
			PasswordHash: "hash",
		})
		apiKey := mustCreateApiKey(t, client, &service.APIKey{
			UserID: user.ID,
			Key:    "sk-usage-billing-outbox-" + uuid.NewString(),
			Name:   "billing-outbox",
		})
		account := mustCreateAccount(t, client, &service.Account{
			Name:  "usage-billing-outbox-" + uuid.NewString(),
			Type:  service.AccountTypeAPIKey,
			Extra: extra,
		})
		return apiKey.ID, account.ID
	}

	outboxCountFor := func(t *testing.T, accountID int64) int {
		t.Helper()
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
			service.SchedulerOutboxEventAccountChanged, accountID,
		).Scan(&count))
		return count
	}

	t.Run("daily_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, accountID := newFixture(t, map[string]any{
			"quota_daily_limit": 10.0,
		})
		// 第一次低于日限额：不应入队 outbox
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 4,
		})
		require.NoError(t, err)
		require.Equal(t, 0, outboxCountFor(t, accountID), "below limit should not enqueue")

		// 第二次跨越日限额：应入队一次 outbox
		_, err = repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "crossing daily limit should enqueue once")

		// 再次递增（已超）：不应重复入队
		_, err = repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 2,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "subsequent increments beyond limit should not re-enqueue")
	})

	t.Run("weekly_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, accountID := newFixture(t, map[string]any{
			"quota_weekly_limit": 10.0,
		})
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 15, // 单次即跨越
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "single-shot crossing weekly limit should enqueue once")
	})
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedup_BatchDeletesOldRows(t *testing.T) {
	ctx := context.Background()
	repo := newDashboardAggregationRepositoryWithSQL(integrationDB)

	oldRequestID := "dedup-old-" + uuid.NewString()
	newRequestID := "dedup-new-" + uuid.NewString()
	oldCreatedAt := time.Now().UTC().AddDate(0, 0, -400)
	newCreatedAt := time.Now().UTC().Add(-time.Hour)

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint, created_at)
		VALUES ($1, 1, $2, $3), ($4, 1, $5, $6)
	`,
		oldRequestID, strings.Repeat("a", 64), oldCreatedAt,
		newRequestID, strings.Repeat("b", 64), newCreatedAt,
	)
	require.NoError(t, err)

	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	var oldCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", oldRequestID).Scan(&oldCount))
	require.Equal(t, 0, oldCount)

	var newCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", newRequestID).Scan(&newCount))
	require.Equal(t, 1, newCount)

	var archivedCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup_archive WHERE request_id = $1", oldRequestID).Scan(&archivedCount))
	require.Equal(t, 1, archivedCount)
}

func TestUsageBillingRepositoryApply_DeduplicatesAgainstArchivedKey(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)
	aggRepo := newDashboardAggregationRepositoryWithSQL(integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-archive-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-archive-" + uuid.NewString(),
		Name:   "billing-archive",
	})

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE usage_billing_dedup
		SET created_at = $1
		WHERE request_id = $2 AND api_key_id = $3
	`, time.Now().UTC().AddDate(0, 0, -400), requestID, apiKey.ID)
	require.NoError(t, err)
	require.NoError(t, aggRepo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)
}
