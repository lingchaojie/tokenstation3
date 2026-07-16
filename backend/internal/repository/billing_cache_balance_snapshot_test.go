//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBillingBalanceSnapshotCache_RoundTripsLayersAndLegacyTotalReader(t *testing.T) {
	cache, mr := newMiniRedisCache(t)
	ctx := context.Background()
	nextExpiry := time.Now().Add(2 * time.Minute).UTC()
	want := service.BillingBalanceSnapshot{
		TotalBalance:           35,
		DailyRewardBalance:     5,
		AffiliateRewardBalance: 10,
		AccountBalance:         20,
		NextRewardExpiresAt:    &nextExpiry,
	}

	require.NoError(t, cache.SetUserBalanceSnapshot(ctx, 42, want))
	got, err := cache.GetUserBalanceSnapshot(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, service.BillingBalanceSnapshotSchemaV1, got.SchemaVersion)
	require.InDelta(t, want.TotalBalance, got.TotalBalance, 0.000001)
	require.InDelta(t, want.DailyRewardBalance, got.DailyRewardBalance, 0.000001)
	require.InDelta(t, want.AffiliateRewardBalance, got.AffiliateRewardBalance, 0.000001)
	require.InDelta(t, want.AccountBalance, got.AccountBalance, 0.000001)

	total, err := cache.GetUserBalance(ctx, 42)
	require.NoError(t, err)
	require.InDelta(t, 35, total, 0.000001)
	require.LessOrEqual(t, mr.TTL(billingBalanceKey(42)), 2*time.Minute)
}

func TestBillingBalanceSnapshotCache_LegacyPayloadIsSnapshotMiss(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetUserBalance(ctx, 42, 10))

	_, err := cache.GetUserBalanceSnapshot(ctx, 42)
	require.ErrorIs(t, err, redis.Nil)
}

func TestBillingBalanceSnapshotCache_ExpiredSnapshotCannotBeRead(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	expired := time.Now().Add(-time.Second).UTC()
	require.NoError(t, cache.SetUserBalanceSnapshot(ctx, 42, service.BillingBalanceSnapshot{
		TotalBalance:        5,
		DailyRewardBalance:  5,
		NextRewardExpiresAt: &expired,
	}))

	_, err := cache.GetUserBalanceSnapshot(ctx, 42)
	require.ErrorIs(t, err, redis.Nil)
}

func TestBillingBalanceSnapshotCache_LegacyDeductInvalidatesStructuredPayload(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetUserBalanceSnapshot(ctx, 42, service.BillingBalanceSnapshot{
		TotalBalance:   10,
		AccountBalance: 10,
	}))

	require.NoError(t, cache.DeductUserBalance(ctx, 42, 1))
	_, err := cache.GetUserBalanceSnapshot(ctx, 42)
	require.ErrorIs(t, err, redis.Nil)
}
