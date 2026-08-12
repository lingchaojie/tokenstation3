//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayAnthropicAPIKeyStreamUpstream struct {
	mu      sync.Mutex
	calls   int
	newBody func() io.ReadCloser
}

func (u *gatewayAnthropicAPIKeyStreamUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *gatewayAnthropicAPIKeyStreamUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"text/event-stream"},
			"x-request-id": {"req-handler-apikey-stream"},
		},
		Body:    u.newBody(),
		Request: req,
	}, nil
}

func (u *gatewayAnthropicAPIKeyStreamUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

type gatewayAnthropicReadErrorBody struct {
	reader *bytes.Reader
	err    error
}

func (r *gatewayAnthropicReadErrorBody) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

func (*gatewayAnthropicReadErrorBody) Close() error { return nil }

type gatewayAnthropicUsageRepo struct {
	service.UsageLogRepository
	mu      sync.Mutex
	records []*service.UsageLog
}

func (r *gatewayAnthropicUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	if log != nil {
		cloned := *log
		r.mu.Lock()
		r.records = append(r.records, &cloned)
		r.mu.Unlock()
	}
	return true, nil
}

func (r *gatewayAnthropicUsageRepo) snapshot() []*service.UsageLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageLog(nil), r.records...)
}

type gatewayAnthropicHandlerRunResult struct {
	recorder *httptest.ResponseRecorder
	context  *gin.Context
	usages   []*service.UsageLog
	captures []*service.CaptureRecord
	calls    int
}

func runGatewayAnthropicAPIKeyStream(t *testing.T, newBody func() io.ReadCloser) gatewayAnthropicHandlerRunResult {
	t.Helper()
	gin.SetMode(gin.TestMode)

	const (
		groupID   = int64(9630)
		accountID = int64(9631)
		userID    = int64(9632)
	)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic,
		Status: service.StatusActive, RateMultiplier: 1,
	}
	account := &service.Account{
		ID: accountID, Name: "anthropic-apikey-handler", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 1, Priority: 1,
		Credentials:   map[string]any{"api_key": "test-anthropic-key"},
		Extra:         map[string]any{"anthropic_passthrough": true},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	schedulerCache := &fakeSchedulerCache{accounts: []*service.Account{account}}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)

	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	t.Cleanup(capturePool.Stop)
	usageRepo := &gatewayAnthropicUsageRepo{}
	upstream := &gatewayAnthropicAPIKeyStreamUpstream{newBody: newBody}
	gateway := service.NewGatewayService(
		nil,                          // accountRepo
		&fakeGroupRepo{group: group}, // groupRepo
		usageRepo,                    // usageLogRepo
		nil,                          // usageBillingRepo
		nil,                          // userRepo
		nil,                          // userSubRepo
		nil,                          // userGroupRateRepo
		nil,                          // cache
		cfg,
		schedulerSnapshot,
		nil, // concurrencyService: scheduler snapshot acquires directly
		service.NewBillingService(cfg, nil),
		nil, // rateLimitService
		billingCache,
		nil, // identityService
		upstream,
		&service.DeferredService{},
		nil, // claudeTokenProvider
		nil, // kiroTokenProvider
		nil, // kiroCooldownStore
		nil, // sessionLimitCache
		nil, // rpmCache
		nil, // digestStore
		settingService,
		nil, // tlsFPProfileService
		nil, // channelService
		nil, // resolver
		nil, // balanceNotifyService
		nil, // userPlatformQuotaRepo
		capturePool,
	)
	concurrencyService := service.NewConcurrencyService(&fakeConcurrencyCache{})
	apiKeyService := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg)
	h := NewGatewayHandler(
		gateway, nil, nil, nil, nil, concurrencyService, billingCache, nil, apiKeyService,
		nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, strings.NewReader(
		`{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9633, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(),
		Status: service.StatusActive, Group: group,
		User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.Messages(c)
	capturePool.Stop()

	var captures []*service.CaptureRecord
	for {
		select {
		case capture := <-captureRecords:
			captures = append(captures, capture)
		default:
			return gatewayAnthropicHandlerRunResult{
				recorder: recorder,
				context:  c,
				usages:   usageRepo.snapshot(),
				captures: captures,
				calls:    upstream.callCount(),
			}
		}
	}
}

func parseCompleteSSEEvents(t *testing.T, wire string) []string {
	t.Helper()
	normalized := strings.ReplaceAll(wire, "\r\n", "\n")
	require.True(t, strings.HasSuffix(normalized, "\n\n"), "stream must end on a complete SSE event boundary: %q", normalized)
	parts := strings.Split(normalized, "\n\n")
	require.Empty(t, parts[len(parts)-1])
	events := make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		if part != "" {
			events = append(events, part)
		}
	}
	return events
}

func TestGatewayHandlerAnthropicAPIKeyPostsemanticErrorEventIsCommunicatedOnce(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":2}}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","usage":{"output_tokens":1}}`,
		`event: error` + "\n" + `data: {"type":"error","error":{"type":"overloaded_error","message":"boom"}}`,
	}, "\n\n") + "\n\n"
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(upstreamSSE))
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 4, "the handler must not append a second generic error event")
	require.Equal(t, `event: error`+"\n"+`data: {"type":"error","error":{"type":"overloaded_error","message":"boom"}}`, events[3])
	require.NotContains(t, got.recorder.Body.String(), "Upstream request failed")
	require.Equal(t, 1, got.calls, "postsemantic output must never be replayed")
	require.True(t, service.IsResponseCommitted(got.context), "a flushed complete upstream error event must be marked communicated")
	require.Len(t, got.usages, 1)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Equal(t, 1, got.usages[0].OutputTokens)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, upstreamSSE, string(got.captures[0].RawResponse))
}

func TestGatewayHandlerAnthropicAPIKeyReadErrorDiscardsIncompletePendingEventBeforeFallback(t *testing.T) {
	completePrefix := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":2}}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
	}, "\n\n") + "\n\n"
	incomplete := `event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"dangling"}}`
	rawUpstream := completePrefix + incomplete
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return &gatewayAnthropicReadErrorBody{reader: bytes.NewReader([]byte(rawUpstream)), err: errors.New("forced upstream read failure")}
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 3, "fallback must be its own event after the two complete upstream events")
	require.Contains(t, events[2], `"type":"error"`)
	require.Contains(t, events[2], "Upstream request failed")
	require.NotContains(t, got.recorder.Body.String(), "dangling", "an incomplete upstream event must never be exposed")
	require.Equal(t, 1, got.calls, "postsemantic output must never be replayed")
	require.Len(t, got.usages, 1)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, rawUpstream+"\n", string(got.captures[0].RawResponse), "capture retains the normalized upstream bytes, including the incomplete tail")
}

func TestGatewayHandlerAnthropicAPIKeyCleanEOFFinalizesPendingDoneEvent(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":2}}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","usage":{"output_tokens":1}}`,
	}, "\n\n") + "\n\n" + `data: [DONE]`
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(upstreamSSE))
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 4)
	require.Equal(t, "data: [DONE]", events[3])
	require.Equal(t, 1, got.calls)
	require.Len(t, got.usages, 1)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, upstreamSSE+"\n", string(got.captures[0].RawResponse), "unterminated upstream line normalization is preserved in capture")
}
