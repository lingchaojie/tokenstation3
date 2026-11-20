package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// runCompatStreamToEnd drives the compat stream handler with an upstream that
// sends nothing and closes after closeAfter.
func runCompatStreamToEnd(t *testing.T, keepaliveInterval, maxWait, closeAfter time.Duration) error {
	t.Helper()
	svc := newAntigravityCompatService(config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize, StreamDataIntervalTimeout: 30, StreamKeepaliveInterval: 0,
	}, nil)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1/responses", nil)
	reader, pipeWriter := io.Pipe()
	defer func() { _ = reader.Close() }()
	resp := &http.Response{StatusCode: http.StatusOK, Body: reader}
	done := make(chan error, 1)
	go func() {
		_, err := svc.handleAntigravityCompatStreamWithKeepaliveInterval(
			c, resp, time.Now(), "gemini-4-hydrogen-high",
			newAntigravityResponsesStreamAdapter("gemini-4-hydrogen-high"), "test", keepaliveInterval, maxWait,
		)
		done <- err
	}()
	time.Sleep(closeAfter)
	_ = pipeWriter.Close()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("compat stream handler did not return")
		return nil
	}
}

func TestAntigravityCompatEmptyStreamAfterKeepaliveIsCommittedNoContent(t *testing.T) {
	err := runCompatStreamToEnd(t, 10*time.Millisecond, time.Second, 50*time.Millisecond)
	require.Error(t, err)
	require.True(t, IsAntigravityCompatCommittedNoContent(err), "got %v", err)
}

func TestAntigravityCompatPreContentTimeoutAfterKeepaliveIsCommittedNoContent(t *testing.T) {
	err := runCompatStreamToEnd(t, 10*time.Millisecond, 40*time.Millisecond, 200*time.Millisecond)
	require.Error(t, err)
	require.ErrorContains(t, err, "pre-content stream timeout")
	require.True(t, IsAntigravityCompatCommittedNoContent(err), "got %v", err)
}

func TestAntigravityCompatEmptyStreamBeforeKeepaliveStaysFailover(t *testing.T) {
	err := runCompatStreamToEnd(t, time.Second, 2*time.Second, 10*time.Millisecond)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "got %v", err)
	require.False(t, IsAntigravityCompatCommittedNoContent(err))
}

type antigravityStickyReleaseCache struct {
	stubGatewayCache
	rebind func()
}

func (c *antigravityStickyReleaseCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	accountID, err := c.stubGatewayCache.GetSessionAccountID(ctx, groupID, sessionHash)
	if c.rebind != nil {
		c.rebind()
	}
	return accountID, err
}

func (c *antigravityStickyReleaseCache) CompareAndDeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string, expectedAccountID int64) (bool, error) {
	if c.rebind != nil {
		c.rebind()
	}
	if c.sessionBindings[sessionHash] != expectedAccountID {
		return false, nil
	}
	return true, c.DeleteSessionAccountID(ctx, groupID, sessionHash)
}

func TestReleaseStickySessionAfterCommittedNoContent(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	committed := &antigravityCompatCommittedNoContentError{msg: "empty Antigravity compatibility stream after keepalive"}
	newCache := func(accountID int64) *antigravityStickyReleaseCache {
		return &antigravityStickyReleaseCache{
			stubGatewayCache: stubGatewayCache{sessionBindings: map[string]int64{"sess": accountID}},
		}
	}

	t.Run("unbinds the session from the account that failed", func(t *testing.T) {
		cache := newCache(5)
		svc := &GatewayService{cache: cache}
		require.True(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		_, bound := cache.sessionBindings["sess"]
		require.False(t, bound)
	})

	t.Run("keeps a binding that already moved to another account", func(t *testing.T) {
		cache := newCache(6)
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		require.Equal(t, int64(6), cache.sessionBindings["sess"])
	})

	t.Run("keeps a binding replaced concurrently during cleanup", func(t *testing.T) {
		cache := newCache(5)
		cache.rebind = func() { cache.sessionBindings["sess"] = 6 }
		svc := &GatewayService{cache: cache}

		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		require.Equal(t, int64(6), cache.sessionBindings["sess"])
	})

	t.Run("keeps the binding when the cache cannot delete conditionally", func(t *testing.T) {
		cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess": 5}}
		svc := &GatewayService{cache: cache}

		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		require.Equal(t, int64(5), cache.sessionBindings["sess"])
	})

	t.Run("ignores other errors", func(t *testing.T) {
		cache := newCache(5)
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, errors.New("stream data interval timeout")))
		require.Equal(t, int64(5), cache.sessionBindings["sess"])
	})

	t.Run("no session hash is a no-op", func(t *testing.T) {
		cache := newCache(5)
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "", 5, committed))
		require.Equal(t, int64(5), cache.sessionBindings["sess"])
	})
}
