//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

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
	status  int
	newBody func() io.ReadCloser
}

type gatewayRetryDelayCaptureUpstream struct {
	calls chan struct{}
}

func (u *gatewayRetryDelayCaptureUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *gatewayRetryDelayCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls <- struct{}{}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"incomplete\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\n",
		)),
		Request: req,
	}, nil
}

func TestGatewayChatCompletionsAbortsTypedAttemptBeforeSameAccountRetryDelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, accountID, userID = int64(9620), int64(9621), int64(9622)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic,
		Status: service.StatusActive, RateMultiplier: 1,
	}
	account := &service.Account{
		ID: accountID, Name: "anthropic-retry-delay", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key":                      "test-anthropic-key",
			"pool_mode":                    true,
			"pool_mode_retry_count":        float64(1),
			"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	terminals := make(chan string, 4)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(make(chan *service.CaptureRecord, 1), terminals)
	t.Cleanup(capturePool.Stop)
	upstream := &gatewayRetryDelayCaptureUpstream{calls: make(chan struct{}, 2)}
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, &gatewayAnthropicUsageRepo{}, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billing, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, capturePool,
	)
	handler := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billing, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settings, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, strings.NewReader(
		`{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
	)).WithContext(requestCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9623, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	done := make(chan struct{})
	go func() {
		handler.ChatCompletions(c)
		close(done)
	}()
	select {
	case <-upstream.calls:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("first upstream call did not start")
	}
	select {
	case terminal := <-terminals:
		require.Equal(t, "abort", terminal)
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("failed typed attempt remained open during the same-account retry delay")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after request cancellation")
	}
	require.Empty(t, terminals, "request defer must not emit a duplicate terminal")
}

type gatewayAnthropicTwoAccountUpstream struct {
	mu       sync.Mutex
	calls    []int64
	requests map[int64][][]byte
	firstID  int64
	first    []byte
	second   []byte
}

func (u *gatewayAnthropicTwoAccountUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *gatewayAnthropicTwoAccountUpstream) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	if u.requests == nil {
		u.requests = make(map[int64][][]byte)
	}
	u.calls = append(u.calls, accountID)
	u.requests[accountID] = append(u.requests[accountID], append([]byte(nil), body...))
	providerBody := u.second
	if accountID == u.firstID {
		providerBody = u.first
	}
	u.mu.Unlock()
	contentType := "application/json"
	if bytes.Contains(providerBody, []byte("event:")) || bytes.Contains(providerBody, []byte("data:")) {
		contentType = "text/event-stream"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {contentType},
			"X-Request-Id": {"anthropic-account-" + strconv.FormatInt(accountID, 10)},
		},
		Body: io.NopCloser(bytes.NewReader(providerBody)), Request: req,
	}, nil
}

func (u *gatewayAnthropicAPIKeyStreamUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *gatewayAnthropicAPIKeyStreamUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	status := u.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
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
	return runGatewayAnthropicCompatHandler(
		t,
		EndpointMessages,
		`{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
		newBody,
		func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
	)
}

func runGatewayAnthropicCompatHandler(
	t *testing.T,
	endpoint string,
	requestBody string,
	newBody func() io.ReadCloser,
	handle func(*GatewayHandler, *gin.Context),
) gatewayAnthropicHandlerRunResult {
	return runGatewayAnthropicCompatHandlerWithStatus(t, endpoint, requestBody, http.StatusOK, newBody, handle)
}

func runGatewayAnthropicCompatHandlerWithStatus(
	t *testing.T,
	endpoint string,
	requestBody string,
	status int,
	newBody func() io.ReadCloser,
	handle func(*GatewayHandler, *gin.Context),
) gatewayAnthropicHandlerRunResult {
	return runGatewayAnthropicHandlerWithStatusAndPassthrough(t, endpoint, requestBody, status, newBody, handle, true)
}

func runGatewayAnthropicHandlerWithStatusAndPassthrough(
	t *testing.T,
	endpoint string,
	requestBody string,
	status int,
	newBody func() io.ReadCloser,
	handle func(*GatewayHandler, *gin.Context),
	passthrough bool,
	accountMutators ...func(*service.Account),
) gatewayAnthropicHandlerRunResult {
	return runGatewayAnthropicHandlerWithConfig(
		t, endpoint, requestBody, status, newBody, handle, passthrough, nil, accountMutators...,
	)
}

func runGatewayAnthropicHandlerWithConfig(
	t *testing.T,
	endpoint string,
	requestBody string,
	status int,
	newBody func() io.ReadCloser,
	handle func(*GatewayHandler, *gin.Context),
	passthrough bool,
	configure func(*config.Config),
	accountMutators ...func(*service.Account),
) gatewayAnthropicHandlerRunResult {
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
		Extra:         map[string]any{"anthropic_passthrough": passthrough},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	for _, mutate := range accountMutators {
		if mutate != nil {
			mutate(account)
		}
	}
	schedulerCache := &fakeSchedulerCache{accounts: []*service.Account{account}}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	if configure != nil {
		configure(cfg)
	}
	settingService := newEnabledCaptureSettingService(t, cfg)

	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	t.Cleanup(capturePool.Stop)
	usageRepo := &gatewayAnthropicUsageRepo{}
	upstream := &gatewayAnthropicAPIKeyStreamUpstream{status: status, newBody: newBody}
	gateway := service.NewGatewayService(
		&antigravityCaptureAccountRepo{}, // accountRepo
		&fakeGroupRepo{group: group},     // groupRepo
		usageRepo,                        // usageLogRepo
		nil,                              // usageBillingRepo
		nil,                              // userRepo
		nil,                              // userSubRepo
		nil,                              // userGroupRateRepo
		nil,                              // cache
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
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9633, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(),
		Status: service.StatusActive, Group: group,
		User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	handle(h, c)
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

func TestGatewayResponsesKiroCompactionRestoresLazyKeepaliveWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const accountID, userID = int64(9641), int64(9642)
	groupID := int64(9640)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformKiro,
		Status: service.StatusActive, RateMultiplier: 1,
	}
	account := &service.Account{
		ID: accountID, Name: "kiro-compaction-handler", Platform: service.PlatformKiro,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key":  "test-kiro-relay-key",
			"base_url": "https://relay.example.com",
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	require.True(t, shouldStartResponsesCompactionKeepalive(account, true, true))

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.StreamKeepaliveInterval = 1
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	upstream := &gatewayAnthropicAPIKeyStreamUpstream{
		status: http.StatusUnprocessableEntity,
		newBody: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"rejected"}}`))
		},
	}
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, nil, nil,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, strings.NewReader(
		`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"message","role":"user","content":"continue"},{"type":"compaction_trigger"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9643, UserID: userID, GroupID: &groupID, Status: service.StatusActive, Group: group,
		User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})
	originalWriter := c.Writer

	handler.Responses(c)

	require.Equal(t, 1, upstream.callCount())
	require.Same(t, originalWriter, c.Writer, "handler return must unwrap the lazily installed keepalive writer")
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.True(t, json.Valid(recorder.Body.Bytes()))
}

func TestGatewayNativeMessagesCapturePreservesProviderBytes(t *testing.T) {
	t.Run("stream_crlf_and_unterminated_tail", func(t *testing.T) {
		providerSSE := "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_native\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"usage\":{\"input_tokens\":2}}}\r\n\r\n" +
			"event: content_block_start\r\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\r\n\r\n" +
			"event: content_block_delta\r\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\r\n\r\n" +
			"event: content_block_stop\r\ndata: {\"type\":\"content_block_stop\",\"index\":0}\r\n\r\n" +
			"event: message_delta\r\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\r\n\r\n" +
			"event: message_stop\r\ndata: {\"type\":\"message_stop\"}"
		got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
			t, EndpointMessages,
			`{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			http.StatusOK, func() io.ReadCloser { return io.NopCloser(strings.NewReader(providerSSE)) },
			func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false,
		)
		require.Len(t, got.captures, 1)
		require.Equal(t, providerSSE, string(got.captures[0].RawResponse))
	})

	t.Run("terminal_error_above_legacy_reader_limit", func(t *testing.T) {
		errorBody := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"` + strings.Repeat("z", 600<<10) + `"}}`)
		got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
			t, EndpointMessages,
			`{"model":"claude-test","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			http.StatusUnprocessableEntity, func() io.ReadCloser { return io.NopCloser(bytes.NewReader(errorBody)) },
			func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false,
		)
		require.Len(t, got.captures, 1)
		require.Equal(t, errorBody[:512<<10], got.captures[0].RawResponse, "typed capture must contain only bytes consumed by the provider-error reader")
		require.True(t, got.captures[0].Truncated, "reaching the functional read ceiling leaves response completeness unknown")
		require.Equal(t, http.StatusUnprocessableEntity, got.captures[0].HTTPStatus)
	})
}

func TestGatewayCompatibilityFinalIncompleteAttemptIsTruncated(t *testing.T) {
	providerSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-incomplete\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2}}}\n\n"
	routes := []struct {
		name, endpoint, requestBody string
		handle                      func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "messages", endpoint: EndpointMessages,
			requestBody: `{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name: "chat_completions", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":true,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
				t, route.endpoint, route.requestBody, http.StatusOK,
				func() io.ReadCloser { return io.NopCloser(strings.NewReader(providerSSE)) },
				route.handle, false, func(account *service.Account) {
					account.Credentials["pool_mode"] = true
					account.Credentials["pool_mode_retry_count"] = float64(0)
				},
			)
			require.Equal(t, 1, got.calls)
			require.Len(t, got.captures, 1)
			require.Equal(t, providerSSE, string(got.captures[0].RawResponse))
			require.True(t, got.captures[0].Truncated, "an incomplete provider stream must finalize with response_complete=false")
			require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
		})
	}
}

func TestGatewayCompatibilityCapturePreservesSuccessfulUpstreamHTTPStatus(t *testing.T) {
	providerSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-status\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"usage\":{\"input_tokens\":2}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	routes := []struct {
		name, endpoint, requestBody string
		handle                      func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "messages", endpoint: EndpointMessages,
			requestBody: `{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name: "chat_completions", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":true,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
				t, route.endpoint, route.requestBody, http.StatusCreated,
				func() io.ReadCloser { return io.NopCloser(strings.NewReader(providerSSE)) },
				route.handle, false,
			)
			require.Equal(t, 1, got.calls)
			require.Len(t, got.captures, 1)
			require.Equal(t, http.StatusCreated, got.captures[0].HTTPStatus)
		})
	}
}

func TestGatewayCompatibilityBoundedHTTPFailoverCaptureIsIncomplete(t *testing.T) {
	const functionalErrorLimit = 512 << 10
	providerBody := `{"type":"error","error":{"type":"api_error","message":"` + strings.Repeat("x", functionalErrorLimit) + `"}}`
	routes := []struct {
		name, endpoint, requestBody string
		handle                      func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "messages", endpoint: EndpointMessages,
			requestBody: `{"model":"claude-test","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name: "chat_completions", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":false,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
				t, route.endpoint, route.requestBody, http.StatusServiceUnavailable,
				func() io.ReadCloser { return io.NopCloser(strings.NewReader(providerBody)) },
				route.handle, false, func(account *service.Account) {
					account.Credentials["pool_mode"] = true
					account.Credentials["pool_mode_retry_count"] = float64(0)
				},
			)
			require.Equal(t, 2, got.calls, "the fixture exhausts its configured initial attempt plus one account switch")
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(providerBody[:functionalErrorLimit]), got.captures[0].RawResponse)
			require.True(t, got.captures[0].Truncated, "a bounded provider-error prefix cannot be finalized complete")
			require.Equal(t, http.StatusServiceUnavailable, got.captures[0].HTTPStatus)
		})
	}
}

func TestGatewayMessagesShortHTTPErrorReadCaptureIsIncomplete(t *testing.T) {
	providerBody := `{"type":"error","error":{"type":"invalid_request_error","message":"short"}}`
	got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
		t, EndpointMessages,
		`{"model":"claude-test","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}]}`,
		http.StatusBadRequest,
		func() io.ReadCloser {
			return io.NopCloser(io.MultiReader(strings.NewReader(providerBody), iotest.ErrReader(io.ErrUnexpectedEOF)))
		},
		func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false,
	)
	require.Equal(t, 1, got.calls)
	require.Len(t, got.captures, 1)
	require.Equal(t, []byte(providerBody), got.captures[0].RawResponse)
	require.True(t, got.captures[0].Truncated, "a short failed provider read must not be replayed as complete")
	require.Equal(t, http.StatusBadRequest, got.captures[0].HTTPStatus)
}

func TestGatewayMessagesBoundedFailoverOn400CaptureIsIncomplete(t *testing.T) {
	const functionalErrorLimit = 512 << 10
	providerBody := `{"type":"error","error":{"type":"invalid_request_error","message":"requires beta"}}` + strings.Repeat(" ", functionalErrorLimit)
	got := runGatewayAnthropicHandlerWithConfig(
		t, EndpointMessages,
		`{"model":"claude-test","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}]}`,
		http.StatusBadRequest,
		func() io.ReadCloser { return io.NopCloser(strings.NewReader(providerBody)) },
		func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false,
		func(cfg *config.Config) { cfg.Gateway.FailoverOn400 = true },
	)
	require.Equal(t, 1, got.calls)
	require.Len(t, got.captures, 1)
	require.Equal(t, []byte(providerBody[:functionalErrorLimit]), got.captures[0].RawResponse)
	require.True(t, got.captures[0].Truncated, "a bounded 400 failover prefix cannot be finalized complete")
	require.Equal(t, http.StatusBadRequest, got.captures[0].HTTPStatus)
}

func TestGatewayAnthropicTwoAccountMalformedProviderResponseArchivesOnlyFinalAttempt(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "ordinary"
		if passthrough {
			name = "api_key_passthrough"
		}
		validStreamBody := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		for _, scenario := range []struct {
			name        string
			requestBody []byte
			firstBody   []byte
			finalBody   []byte
		}{
			{
				name:        "invalid_nonstream_json",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_role_and_content",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"user","content":[{}],"usage":{"input_tokens":9,"output_tokens":0}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_tool_missing_id",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"tool_use","name":"lookup","input":{}}],"usage":{"input_tokens":9,"output_tokens":0}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_nonstring_block_type",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":123}],"usage":{"input_tokens":9,"output_tokens":0}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_known_siblings",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":123,"type":"message","role":"assistant","model":123,"content":[{"type":"text","text":"first-leak"}],"stop_reason":[],"usage":"bad"}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_missing_stop_reason",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"usage":{"input_tokens":9,"output_tokens":1}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_null_stop_reason",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":null,"usage":{"input_tokens":9,"output_tokens":1}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "invalid_nonstream_empty_stop_reason",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"","usage":{"input_tokens":9,"output_tokens":1}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "declared_known_event_missing_payload_type",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "nonstring_top_level_event_type",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("data: {\"type\":123}\n\n" +
					"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "malformed_message_start_usage",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":\"bad\"}}\n\n"),
				finalBody:   validStreamBody,
			},
			{
				name:        "malformed_message_delta_usage",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":\"bad\"}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "malformed_message_delta_cached_tokens",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1,\"cached_tokens\":\"bad\"}}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "malformed_nonstream_cached_tokens",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":1,"cached_tokens":"bad"}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "malformed_nonstream_cache_creation_breakdown",
				requestBody: []byte(`{"model":"claude-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte(`{"id":"msg-first","type":"message","role":"assistant","content":[{"type":"text","text":"first-leak"}],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":1,"cache_creation_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":100}}}`),
				finalBody:   []byte(`{"id":"msg-final","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`),
			},
			{
				name:        "malformed_stream_cache_creation_breakdown",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody:   []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"cache_creation_input_tokens\":1,\"cache_creation\":{\"ephemeral_5m_input_tokens\":100,\"ephemeral_1h_input_tokens\":100}}}}\n\n"),
				finalBody:   validStreamBody,
			},
			{
				name:        "message_stop_without_terminal_delta",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "duplicate_content_block_index",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "fractional_content_block_index",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0.5,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0.5,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\n"),
				finalBody: validStreamBody,
			},
			{
				name:        "empty_message_start_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "invalid_message_start_content_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{}],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "nonempty_message_start_content_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"invalid-in-start\"}],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "stop_before_message_start_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n" +
					"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "malformed_declared_event_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
					"event: message_delta\ndata: {not-json}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "malformed_data_only_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
					"data: {not-json}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "invalid_content_block_shape_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "invalid_content_block_start_shape_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\"}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
			{
				name:        "semantic_delta_before_message_start_stream",
				requestBody: []byte(`{"model":"claude-test","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				firstBody: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
				finalBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-final\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
			},
		} {
			t.Run(name+"/"+scenario.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				const groupID, firstAccountID, secondAccountID, userID = int64(9650), int64(9651), int64(9652), int64(9653)
				group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true}
				newAccount := func(id int64, priority int) *service.Account {
					return &service.Account{
						ID: id, Name: "anthropic-two-account", Platform: service.PlatformAnthropic,
						Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
						Credentials:   map[string]any{"api_key": "test-anthropic-key"},
						Extra:         map[string]any{"anthropic_passthrough": passthrough},
						AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
					}
				}
				firstAccount := newAccount(firstAccountID, 1)
				secondAccount := newAccount(secondAccountID, 2)
				upstream := &gatewayAnthropicTwoAccountUpstream{firstID: firstAccountID, first: scenario.firstBody, second: scenario.finalBody}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Gateway.MaxAccountSwitches = 2
				cfg.Gateway.Capture.Enabled = true
				cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
				settingService := newEnabledCaptureSettingService(t, cfg)
				scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{firstAccount, secondAccount}}, nil, nil, nil, nil)
				billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				captureRecords := make(chan *service.CaptureRecord, 4)
				capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
				usageRepo := &gatewayAnthropicUsageRepo{}
				gateway := service.NewGatewayService(
					&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
					service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
					nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
				)
				h := NewGatewayHandler(
					gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
					service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
				)

				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewReader(scenario.requestBody))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
					ID: 9654, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
					Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
				})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

				h.Messages(c)
				capturePool.Stop()
				require.Equal(t, http.StatusOK, recorder.Code)
				require.NotContains(t, recorder.Body.String(), "first-leak")
				require.NotContains(t, recorder.Body.String(), "{not-json}")
				require.Contains(t, recorder.Body.String(), "recovered")
				upstream.mu.Lock()
				calls := append([]int64(nil), upstream.calls...)
				finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
				upstream.mu.Unlock()
				require.NotEmpty(t, calls)
				require.Equal(t, secondAccountID, calls[len(calls)-1])
				require.NotContains(t, calls[:len(calls)-1], secondAccountID)
				require.Len(t, captureRecords, 1)
				record := <-captureRecords
				require.Equal(t, scenario.finalBody, record.RawResponse)
				require.Len(t, finalRequests, 1)
				require.Equal(t, finalRequests[0], record.RawRequest)
				require.Len(t, usageRepo.snapshot(), 1)
				require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
			})
		}
	}
}

func TestGatewayCompatibilityHandlersRejectInvalidAnthropicStreamBeforeCommit(t *testing.T) {
	malformedAttempts := []struct {
		name string
		body []byte
	}{
		{
			name: "malformed_message_start",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "malformed_declared_event",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
				"event: message_delta\ndata: {not-json}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "invalid_content_block_shape",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "invalid_content_block_start_shape",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "semantic_before_message_start",
			body: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "event_type_mismatch",
			body: []byte("event: future_event\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "error_payload_without_event_header",
			body: []byte("data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"retry me\"}}\n\n" +
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"),
		},
		{
			name: "delta_without_content_block_start",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-leak\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "message_stop_with_open_content_block",
			body: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
	}
	finalBody := []byte(strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg-final","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":2,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"recovered"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n")
	routes := []struct {
		name, endpoint, requestBody string
		handle                      func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "chat_completions_buffered", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses_buffered", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":false,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name: "chat_completions_stream", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses_stream", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":true,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}
	for index, route := range routes {
		for _, malformed := range malformedAttempts {
			t.Run(route.name+"/"+malformed.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				groupID := int64(9670 + index*10)
				firstAccountID, secondAccountID, userID := groupID+1, groupID+2, groupID+3
				group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
				newAccount := func(id int64, priority int) *service.Account {
					return &service.Account{
						ID: id, Name: "anthropic-compat-buffered", Platform: service.PlatformAnthropic,
						Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
						Credentials:   map[string]any{"api_key": "test-anthropic-key"},
						AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
					}
				}
				firstAccount, secondAccount := newAccount(firstAccountID, 1), newAccount(secondAccountID, 2)
				upstream := &gatewayAnthropicTwoAccountUpstream{firstID: firstAccountID, first: malformed.body, second: finalBody}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Gateway.MaxAccountSwitches = 2
				cfg.Gateway.Capture.Enabled = true
				cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
				settings := newEnabledCaptureSettingService(t, cfg)
				scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{firstAccount, secondAccount}}, nil, nil, nil, nil)
				billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				captureRecords := make(chan *service.CaptureRecord, 4)
				capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
				usageRepo := &gatewayAnthropicUsageRepo{}
				gateway := service.NewGatewayService(
					&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
					service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
					nil, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, capturePool,
				)
				handler := NewGatewayHandler(
					gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
					service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settings, capturePool,
				)

				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, route.endpoint, strings.NewReader(route.requestBody))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
					ID: groupID + 4, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
					Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
				})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

				route.handle(handler, c)
				capturePool.Stop()
				upstream.mu.Lock()
				calls := append([]int64(nil), upstream.calls...)
				finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
				upstream.mu.Unlock()
				require.NotEmpty(t, calls)
				require.Equal(t, secondAccountID, calls[len(calls)-1])
				require.NotContains(t, recorder.Body.String(), "first-leak")
				require.NotContains(t, recorder.Body.String(), "{not-json}")
				require.Contains(t, recorder.Body.String(), "recovered")
				require.Len(t, captureRecords, 1)
				record := <-captureRecords
				require.Equal(t, finalBody, record.RawResponse)
				require.Len(t, finalRequests, 1)
				require.Equal(t, finalRequests[0], record.RawRequest)
				require.Len(t, usageRepo.snapshot(), 1)
				require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
			})
		}
	}
}

func TestGatewayNativeMessagesRetryExhaustedCustomStatusArchivesExactlyOnce(t *testing.T) {
	errorBody := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"retry exhausted 422"}}`)
	got := runGatewayAnthropicHandlerWithStatusAndPassthrough(
		t, EndpointMessages,
		`{"model":"claude-test","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}]}`,
		http.StatusUnprocessableEntity, func() io.ReadCloser { return io.NopCloser(bytes.NewReader(errorBody)) },
		func(h *GatewayHandler, c *gin.Context) { h.Messages(c) }, false,
		func(account *service.Account) {
			account.Credentials["custom_error_codes_enabled"] = true
			account.Credentials["custom_error_codes"] = []any{float64(http.StatusTooManyRequests)}
		},
	)
	require.Equal(t, 5, got.calls)
	require.Len(t, got.captures, 1)
	require.Equal(t, errorBody, got.captures[0].RawResponse)
	require.Equal(t, http.StatusUnprocessableEntity, got.captures[0].HTTPStatus)
}

func TestGatewayCompatibilityHandlersArchiveProviderAttemptExactlyOnce(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_compat","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	tests := []struct {
		name, endpoint, requestBody string
		handle                      func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "chat_completions", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":true,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runGatewayAnthropicCompatHandler(t, tt.endpoint, tt.requestBody, func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(upstreamSSE))
			}, tt.handle)

			require.Equal(t, 1, got.calls)
			require.Len(t, got.usages, 1)
			require.Len(t, got.captures, 1, "the compatibility handler must submit its final provider exchange exactly once")
			require.Equal(t, upstreamSSE, string(got.captures[0].RawResponse))
			require.Contains(t, string(got.captures[0].RawRequest), `"model":"claude-test"`)
		})
	}
}

func TestGatewayCompatibilityHandlersArchiveTerminalFailoverAttemptExactlyOnce(t *testing.T) {
	tests := []struct {
		name, endpoint, requestBody, errorBody string
		status                                 int
		handle                                 func(*GatewayHandler, *gin.Context)
	}{
		{
			name: "chat_completions", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			errorBody:   `{"type":"error","error":{"type":"overloaded_error","message":"final provider overload"}}`, status: http.StatusServiceUnavailable,
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":false,"input":"hello"}`,
			errorBody:   `{"type":"error","error":{"type":"overloaded_error","message":"final provider overload"}}`, status: http.StatusServiceUnavailable,
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name: "chat_completions_non_failover", endpoint: EndpointChatCompletions,
			requestBody: `{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			errorBody:   `{"type":"error","error":{"type":"invalid_request_error","message":"provider rejected request"}}`, status: http.StatusUnprocessableEntity,
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name: "responses_non_failover", endpoint: EndpointResponses,
			requestBody: `{"model":"claude-test","stream":false,"input":"hello"}`,
			errorBody:   `{"type":"error","error":{"type":"invalid_request_error","message":"provider rejected request"}}`, status: http.StatusUnprocessableEntity,
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runGatewayAnthropicCompatHandlerWithStatus(t, tt.endpoint, tt.requestBody, tt.status, func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(tt.errorBody))
			}, tt.handle)

			require.GreaterOrEqual(t, got.calls, 1)
			require.Empty(t, got.usages)
			require.Len(t, got.captures, 1, "the exhausted handler must archive the final provider HTTP attempt")
			require.Equal(t, service.PlatformAnthropic, got.captures[0].Platform)
			require.Equal(t, tt.status, got.captures[0].HTTPStatus)
			require.Equal(t, tt.errorBody, string(got.captures[0].RawResponse))
			require.Contains(t, string(got.captures[0].RawRequest), `"model":"claude-test"`)
			if strings.Contains(tt.name, "non_failover") {
				require.Equal(t, tt.status, got.recorder.Code)
				require.True(t, json.Valid(got.recorder.Body.Bytes()), "provider JSON error must not be followed by an SSE frame: %q", got.recorder.Body.String())
				require.NotContains(t, got.recorder.Body.String(), "data: {")
			}
		})
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
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"type":"message","role":"assistant","content":[],"usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: error` + "\n" + `data: {"type":"error","error":{"type":"overloaded_error","message":"boom"}}`,
	}, "\n\n") + "\n\n"
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(upstreamSSE))
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 5, "the handler must not append a second generic error event")
	require.Equal(t, `event: error`+"\n"+`data: {"type":"error","error":{"type":"overloaded_error","message":"boom"}}`, events[4])
	require.NotContains(t, got.recorder.Body.String(), "Upstream request failed")
	require.Equal(t, 1, got.calls, "postsemantic output must never be replayed")
	require.True(t, service.IsResponseCommitted(got.context), "a flushed complete upstream error event must be marked communicated")
	require.Len(t, got.usages, 1)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Zero(t, got.usages[0].OutputTokens)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, upstreamSSE, string(got.captures[0].RawResponse))
}

func TestGatewayHandlerAnthropicAPIKeyPostsemanticMissingTerminalDeltaDoesNotReplay(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"type":"message","role":"assistant","content":[],"usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{},"usage":{"output_tokens":99}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(upstreamSSE))
	})

	require.Contains(t, got.recorder.Body.String(), `"text":"hello"`)
	require.Contains(t, got.recorder.Body.String(), `"type":"error"`)
	require.Equal(t, 1, got.calls, "postsemantic protocol errors must never replay on another account")
	require.Len(t, got.usages, 1)
	require.Equal(t, 2, got.usages[0].InputTokens)
	require.Zero(t, got.usages[0].OutputTokens, "invalid terminal usage must not overwrite trusted usage")
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, upstreamSSE, string(got.captures[0].RawResponse))
}

func TestGatewayHandlerAnthropicAPIKeyReadErrorDiscardsIncompletePendingEventBeforeFallback(t *testing.T) {
	completePrefix := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"type":"message","role":"assistant","content":[],"usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
	}, "\n\n") + "\n\n"
	incomplete := `event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"dangling"}}`
	rawUpstream := completePrefix + incomplete
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return &gatewayAnthropicReadErrorBody{reader: bytes.NewReader([]byte(rawUpstream)), err: errors.New("forced upstream read failure")}
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 4, "fallback must be its own event after the three complete upstream events")
	require.Contains(t, events[3], `"type":"error"`)
	require.Contains(t, events[3], "Upstream request failed")
	require.NotContains(t, got.recorder.Body.String(), "dangling", "an incomplete upstream event must never be exposed")
	require.Equal(t, 1, got.calls, "postsemantic output must never be replayed")
	require.Len(t, got.usages, 1)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, rawUpstream, string(got.captures[0].RawResponse), "capture retains the provider-native upstream bytes, including the incomplete tail")
}

func TestGatewayHandlerAnthropicAPIKeyCleanEOFFinalizesPendingDoneEvent(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"type":"message","role":"assistant","content":[],"usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
	}, "\n\n") + "\n\n" + `data: [DONE]`
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(upstreamSSE))
	})

	events := parseCompleteSSEEvents(t, got.recorder.Body.String())
	require.Len(t, events, 6)
	require.Equal(t, "data: [DONE]", events[5])
	require.Equal(t, 1, got.calls)
	require.Len(t, got.usages, 1)
	require.Len(t, got.captures, 1)
	require.Equal(t, http.StatusOK, got.captures[0].HTTPStatus)
	require.Equal(t, upstreamSSE, string(got.captures[0].RawResponse), "capture preserves the provider-native unterminated final line")
}

func TestGatewayHandlerAnthropicAPIKeyCleanEOFRejectsProviderTailAfterTerminal(t *testing.T) {
	complete := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"type":"message","role":"assistant","content":[],"usage":{"input_tokens":2}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	tail := `event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"dangling"}}`
	rawUpstream := complete + tail
	got := runGatewayAnthropicAPIKeyStream(t, func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(rawUpstream))
	})

	require.Equal(t, 1, got.calls, "committed output must not replay after a malformed provider tail")
	require.Contains(t, got.recorder.Body.String(), `"text":"hello"`)
	require.NotContains(t, got.recorder.Body.String(), "dangling")
	require.Contains(t, got.recorder.Body.String(), `"type":"error"`)
	require.Len(t, got.usages, 1)
	require.Len(t, got.captures, 1)
	require.Equal(t, rawUpstream, string(got.captures[0].RawResponse))
}
