package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheCompareAndDeleteSessionAccountID(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	store, ok := cache.(interface {
		CompareAndDeleteSessionAccountID(context.Context, int64, string, int64) (bool, error)
	})
	require.True(t, ok, "the production cache must support conditional removal")

	ctx := context.Background()
	const sessionHash = "shared-session"
	require.NoError(t, cache.SetSessionAccountID(ctx, 10, sessionHash, 5, time.Minute))
	require.NoError(t, cache.SetSessionAccountID(ctx, 11, sessionHash, 6, time.Minute))
	require.NoError(t, cache.SetSessionAccountID(ctx, 10, sessionHash, 6, time.Minute))

	deleted, err := store.CompareAndDeleteSessionAccountID(ctx, 10, sessionHash, 5)
	require.NoError(t, err)
	require.False(t, deleted)
	rebound, err := cache.GetSessionAccountID(ctx, 10, sessionHash)
	require.NoError(t, err)
	require.Equal(t, int64(6), rebound)

	deleted, err = store.CompareAndDeleteSessionAccountID(ctx, 10, sessionHash, 6)
	require.NoError(t, err)
	require.True(t, deleted)
	_, err = cache.GetSessionAccountID(ctx, 10, sessionHash)
	require.ErrorIs(t, err, service.ErrStickySessionNotFound)
	otherGroup, err := cache.GetSessionAccountID(ctx, 11, sessionHash)
	require.NoError(t, err)
	require.Equal(t, int64(6), otherGroup)

	deleted, err = store.CompareAndDeleteSessionAccountID(ctx, 10, sessionHash, 6)
	require.NoError(t, err)
	require.False(t, deleted)
}
