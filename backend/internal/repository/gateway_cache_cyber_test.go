package repository

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type cyberRedisCommandHook struct {
	mu            sync.Mutex
	mgetKeyCounts []int
	setBatchSizes []int
}

func (h *cyberRedisCommandHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *cyberRedisCommandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "mget" {
			h.mu.Lock()
			h.mgetKeyCounts = append(h.mgetKeyCounts, len(cmd.Args())-1)
			h.mu.Unlock()
		}
		return next(ctx, cmd)
	}
}

func (h *cyberRedisCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		setCount := 0
		for _, cmd := range cmds {
			if cmd.Name() == "set" {
				setCount++
			}
		}
		if setCount > 0 {
			h.mu.Lock()
			h.setBatchSizes = append(h.setBatchSizes, setCount)
			h.mu.Unlock()
		}
		return next(ctx, cmds)
	}
}

func TestGatewayCacheCyberBlockWritesScopeLengthsAndExactKeysTogether(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewGatewayCache(client).(service.CyberSessionBlockStore)
	require.True(t, ok)

	ctx := context.Background()
	lookup, err := store.CyberSessionTranscriptLookup(ctx, 7, "scope-1")
	require.NoError(t, err)
	require.Empty(t, lookup)

	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
		APIKeyID: 7, ScopeKey: "scope-1", Keys: []string{"block-1", "block-2"}, TranscriptLengths: []int{3, 2},
	}, time.Minute))
	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
		APIKeyID: 7, ScopeKey: "scope-2", Keys: []string{"block-3"}, TranscriptLengths: []int{3, 40},
	}, time.Minute))
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "scope-1")
	require.NoError(t, err)
	require.ElementsMatch(t, []int{2, 3, 40}, lookup.Lengths, "lengths are indexed per API key, like the block keys")
	require.False(t, lookup.LegacyScopeActive)
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "inactive-scope")
	require.NoError(t, err)
	require.Empty(t, lookup)
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 8, "scope-1")
	require.NoError(t, err)
	require.Empty(t, lookup)

	matched, err := store.FindCyberSessionBlocked(ctx, []string{"missing", "block-1", "block-2"})
	require.NoError(t, err)
	require.Equal(t, "block-1", matched)
	require.Greater(t, server.TTL(cyberSessionScopePrefix+"scope-1"), time.Duration(0))
	require.Greater(t, server.TTL(cyberSessionTranscriptLengthsKey(7)), time.Duration(0))
	require.Equal(t, server.TTL(cyberSessionBlockPrefix+"block-1"), server.TTL(cyberSessionBlockPrefix+"block-2"))
}

func TestGatewayCacheCyberBlockWithoutScopeSkipsTranscriptIndex(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewGatewayCache(client).(service.CyberSessionBlockStore)
	require.True(t, ok)

	ctx := context.Background()
	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{APIKeyID: 7, Keys: []string{"explicit"}, TranscriptLengths: []int{3}}, time.Minute))
	require.False(t, server.Exists(cyberSessionTranscriptLengthsKey(7)))
	matched, err := store.FindCyberSessionBlocked(ctx, []string{"explicit"})
	require.NoError(t, err)
	require.Equal(t, "explicit", matched)
}

func TestGatewayCacheCyberBlockShorterTTLKeepsOlderScopesIndexed(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewGatewayCache(client).(service.CyberSessionBlockStore)
	require.True(t, ok)
	ctx := context.Background()

	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
		APIKeyID: 7, ScopeKey: "old-scope", Keys: []string{"long-lived"}, TranscriptLengths: []int{7},
	}, time.Hour))
	for _, scope := range []string{"new-scope", "old-scope"} {
		require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
			APIKeyID: 7, ScopeKey: scope, Keys: []string{"short-lived"}, TranscriptLengths: []int{19},
		}, time.Minute))
	}
	server.FastForward(2 * time.Minute)

	lookup, err := store.CyberSessionTranscriptLookup(ctx, 7, "old-scope")
	require.NoError(t, err)
	require.Contains(t, lookup.Lengths, 7, "an older scope must still consider its live block")
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "new-scope")
	require.NoError(t, err)
	require.Empty(t, lookup, "a newly recorded scope expires at its own deadline")
	matched, err := store.FindCyberSessionBlocked(ctx, []string{"short-lived", "long-lived"})
	require.NoError(t, err)
	require.Equal(t, "long-lived", matched)
}

func TestGatewayCacheCyberBlockPreservesOriginalLegacyScopeDeadline(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewGatewayCache(client).(service.CyberSessionBlockStore)
	require.True(t, ok)
	ctx := context.Background()

	require.NoError(t, client.Set(ctx, cyberSessionScopePrefix+"legacy-scope", "1", 5*time.Minute).Err())
	lookup, err := store.CyberSessionTranscriptLookup(ctx, 7, "legacy-scope")
	require.NoError(t, err)
	require.True(t, lookup.LegacyScopeActive)
	require.Empty(t, lookup.Lengths)

	// Indexing another scope of the API key must not hide legacy blocks.
	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
		APIKeyID: 7, ScopeKey: "other-scope", Keys: []string{"other"}, TranscriptLengths: []int{1},
	}, time.Minute))
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "legacy-scope")
	require.NoError(t, err)
	require.True(t, lookup.LegacyScopeActive)
	require.Equal(t, []int{1}, lookup.Lengths)

	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{
		APIKeyID: 7, ScopeKey: "legacy-scope", Keys: []string{"new"}, TranscriptLengths: []int{2},
	}, time.Hour))
	server.FastForward(4 * time.Minute)
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "legacy-scope")
	require.NoError(t, err)
	require.True(t, lookup.LegacyScopeActive, "new writes retain the existing fallback window")

	server.FastForward(2 * time.Minute)
	lookup, err = store.CyberSessionTranscriptLookup(ctx, 7, "legacy-scope")
	require.NoError(t, err)
	require.False(t, lookup.LegacyScopeActive, "new writes may not extend the legacy fallback window")
	require.Contains(t, lookup.Lengths, 2)
}

func TestGatewayCacheCyberBlockCommandsAreBoundedAndLookupShortCircuits(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	hook := &cyberRedisCommandHook{}
	client.AddHook(hook)
	store, ok := NewGatewayCache(client).(service.CyberSessionBlockStore)
	require.True(t, ok)

	keys := make([]string, cyberSessionRedisCommandMaxKeys*2+44)
	for i := range keys {
		keys[i] = "block-" + strconv.Itoa(i)
	}
	ctx := context.Background()
	require.NoError(t, store.SetCyberSessionBlocked(ctx, service.CyberSessionBlock{APIKeyID: 7, ScopeKey: "large-scope", Keys: keys}, time.Minute))
	require.Equal(t, []int{cyberSessionRedisCommandMaxKeys, cyberSessionRedisCommandMaxKeys, 44}, hook.setBatchSizes)

	lookup := make([]string, len(keys))
	for i := range lookup {
		lookup[i] = "missing-" + strconv.Itoa(i)
	}
	lookup[cyberSessionRedisCommandMaxKeys+3] = keys[cyberSessionRedisCommandMaxKeys+3]
	matched, err := store.FindCyberSessionBlocked(ctx, lookup)
	require.NoError(t, err)
	require.Equal(t, keys[cyberSessionRedisCommandMaxKeys+3], matched)
	require.Equal(t, []int{cyberSessionRedisCommandMaxKeys, cyberSessionRedisCommandMaxKeys}, hook.mgetKeyCounts)
}
