//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheSnapshotMemoKiroRevisionScopesInvalidation(t *testing.T) {
	ctx := context.Background()
	cache, _, recorder := newSchedulerCacheMemoUnit(t, defaultSchedulerSnapshotWriteChunkSize)
	kiro := memoTestAccountOn(service.PlatformKiro, 231, "kiro")
	kiro.Extra = map[string]any{"mixed_scheduling": true}
	anthropic := memoTestAccount(232, "anthropic")
	gemini := memoTestAccountOn(service.PlatformGemini, 233, "gemini")

	anthropicMixed := service.SchedulerBucket{GroupID: 202, Platform: service.PlatformAnthropic, Mode: service.SchedulerModeMixed}
	anthropicSingle := service.SchedulerBucket{GroupID: 202, Platform: service.PlatformAnthropic, Mode: service.SchedulerModeSingle}
	geminiMixed := service.SchedulerBucket{GroupID: 202, Platform: service.PlatformGemini, Mode: service.SchedulerModeMixed}
	memoPublish(t, cache, anthropicMixed, anthropic, kiro)
	memoPublish(t, cache, anthropicSingle, anthropic)
	memoPublish(t, cache, geminiMixed, gemini)
	for _, bucket := range []service.SchedulerBucket{anthropicMixed, anthropicSingle, geminiMixed} {
		memoRead(t, cache, bucket)
	}

	kiro.Name = "kiro-renamed"
	require.NoError(t, cache.SetAccount(ctx, &kiro))

	got, reloaded := memoReadExpectHit(t, cache, recorder, anthropicMixed)
	require.True(t, reloaded)
	require.Equal(t, "kiro-renamed", got[1].Name)
	_, reloaded = memoReadExpectHit(t, cache, recorder, anthropicSingle)
	require.False(t, reloaded)
	_, reloaded = memoReadExpectHit(t, cache, recorder, geminiMixed)
	require.False(t, reloaded)
}
