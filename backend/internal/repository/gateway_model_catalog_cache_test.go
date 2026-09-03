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

func newGatewayModelCatalogCacheTest(t *testing.T) (*miniredis.Miniredis, service.ModelCatalogCache) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewGatewayCache(client).(service.ModelCatalogCache)
	require.True(t, ok)
	return mr, cache
}

func TestGatewayModelCatalogCache_PersistsEmptyCatalogAndIsolatesKeys(t *testing.T) {
	_, cache := newGatewayModelCatalogCacheTest(t)
	ctx := context.Background()

	_, err := cache.GetModelCatalog(ctx, 3, service.PlatformAnthropic)
	require.ErrorIs(t, err, service.ErrModelCatalogCacheMiss)

	require.NoError(t, cache.SetModelCatalog(ctx, 3, service.PlatformAnthropic, []string{}, 10*time.Minute))
	empty, err := cache.GetModelCatalog(ctx, 3, service.PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, []string{}, empty)

	require.NoError(t, cache.SetModelCatalog(ctx, 6, service.PlatformOpenAI, []string{"gpt-5.5"}, 10*time.Minute))
	openAIModels, err := cache.GetModelCatalog(ctx, 6, service.PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.5"}, openAIModels)

	_, err = cache.GetModelCatalog(ctx, 3, service.PlatformOpenAI)
	require.ErrorIs(t, err, service.ErrModelCatalogCacheMiss)
	stillEmpty, err := cache.GetModelCatalog(ctx, 3, service.PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, []string{}, stillEmpty)
}

func TestGatewayModelCatalogCache_UsesVersionedGroupPlatformKeyAndTTL(t *testing.T) {
	mr, cache := newGatewayModelCatalogCacheTest(t)
	ctx := context.Background()

	require.NoError(t, cache.SetModelCatalog(ctx, 3, " anthropic ", []string{"claude-public"}, 10*time.Minute))

	key := buildModelCatalogKey(3, service.PlatformAnthropic)
	require.Equal(t, "gateway:model_catalog:v1:group:3:platform:anthropic", key)
	raw, err := mr.Get(key)
	require.NoError(t, err)
	require.Equal(t, `["claude-public"]`, raw)
	require.Equal(t, 10*time.Minute, mr.TTL(key))
}

func TestGatewayModelCatalogCache_ReportsMalformedJSON(t *testing.T) {
	mr, cache := newGatewayModelCatalogCacheTest(t)
	key := buildModelCatalogKey(3, service.PlatformAnthropic)
	mr.Set(key, `{not-json`)

	models, err := cache.GetModelCatalog(context.Background(), 3, service.PlatformAnthropic)

	require.Nil(t, models)
	require.ErrorContains(t, err, "decode model catalog")
}
