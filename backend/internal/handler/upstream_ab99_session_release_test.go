//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Models the external Redis set; real selection, forwarding, capture and handler
// cleanup run unchanged. An unrelated new session is the observable admission probe.
type upstreamAB99SessionCache struct {
	service.SessionLimitCache
	mu            sync.Mutex
	sessions      map[string]bool
	registrations int
}

func (s *upstreamAB99SessionCache) RegisterSession(_ context.Context, _ int64, id string, limit int, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[id] || len(s.sessions) < limit {
		s.sessions[id] = true
		s.registrations++
		return true, nil
	}
	return false, nil
}

func (s *upstreamAB99SessionCache) UnregisterSession(_ context.Context, _ int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

func (s *upstreamAB99SessionCache) RefreshSession(context.Context, int64, string, time.Duration) error {
	return nil
}

func TestGatewayMessagesSessionReleasePreservesCaptureAndUsage(t *testing.T) {
	const prefix = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-session\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"usage\":{\"input_tokens\":2}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"
	const ending = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, tc := range []struct {
		name           string
		status         int
		body           string
		wantNewSession bool
		wantUsage      int
	}{
		{"capture-only rejection releases", http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"bad input"}}`, true, 0},
		{"success retains", http.StatusOK, prefix + ending, false, 1},
		{"partial metered output retains", http.StatusOK, prefix, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &upstreamAB99SessionCache{sessions: make(map[string]bool)}
			got := runGatewayAnthropicHandlerWithSessionCache(t, EndpointMessages,
				`{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				tc.status, func() io.ReadCloser { return io.NopCloser(strings.NewReader(tc.body)) },
				func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false, nil, cache,
				func(a *service.Account) {
					a.Type = service.AccountTypeSetupToken
					a.Credentials = map[string]any{"access_token": "test-setup-token"}
					a.Extra["max_sessions"] = 1
				})
			require.Equal(t, 1, got.calls)
			require.Len(t, got.captures, 1, "cleanup must not bypass or duplicate capture")
			require.Len(t, got.usages, tc.wantUsage)
			require.Positive(t, cache.registrations, "the real scheduler must have registered a session")
			allowed, err := cache.RegisterSession(context.Background(), 9631, "unrelated-new-session", 1, time.Minute)
			require.NoError(t, err)
			require.Equal(t, tc.wantNewSession, allowed)
		})
	}
}
