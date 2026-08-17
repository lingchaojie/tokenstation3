//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroTerminalCaptureUpstream struct {
	body   []byte
	seen   []byte
	status int
}

type kiroTerminalCaptureCooldownStore struct{}

func (kiroTerminalCaptureCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}
func (kiroTerminalCaptureCooldownStore) MarkSuccess(context.Context, string) error { return nil }
func (kiroTerminalCaptureCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	return 0, nil
}
func (kiroTerminalCaptureCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}
func (kiroTerminalCaptureCooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	return nil, nil
}
func (kiroTerminalCaptureCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

type kiroWebSearchTerminalCaptureUpstream struct {
	mu            sync.Mutex
	runtimeBodies [][]byte
	terminalBody  []byte
}

func (u *kiroWebSearchTerminalCaptureUpstream) response(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Request: req}
	switch {
	case bytes.Contains(body, []byte(`"method":"tools/list"`)):
		response.Body = io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":"tools_list","result":{"tools":[{"name":"web_search","description":"Search the web"}]}}`))
	case bytes.Contains(body, []byte(`"method":"tools/call"`)):
		response.Body = io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`))
	default:
		u.mu.Lock()
		u.runtimeBodies = append(u.runtimeBodies, append([]byte(nil), body...))
		u.mu.Unlock()
		response.StatusCode = http.StatusServiceUnavailable
		response.Header = http.Header{
			"Content-Type":     {"application/json"},
			"X-Amzn-Requestid": {"rid-kiro-websearch-final-503"},
		}
		response.Body = io.NopCloser(bytes.NewReader(u.terminalBody))
	}
	return response, nil
}

func (u *kiroWebSearchTerminalCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.response(req)
}

func (u *kiroWebSearchTerminalCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(req)
}

func newTerminalOnlyCaptureSettingService(t *testing.T, cfg *config.Config) *service.SettingService {
	t.Helper()
	settings := service.NewSettingService(&handlerCaptureSettingRepo{}, cfg)
	policy := service.DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = true
	// These integration tests exercise terminal capture mechanics across model
	// families; production defaults continue to limit Anthropic and Kiro.
	policy.ModelAllowlists.Anthropic = []string{}
	policy.ModelAllowlists.Kiro = []string{}
	_, err := settings.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)
	return settings
}

func (u *kiroTerminalCaptureUpstream) response(req *http.Request) (*http.Response, error) {
	requestBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.seen = append([]byte(nil), requestBody...)
	status := u.status
	if status == 0 {
		status = http.StatusBadRequest
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":     {"application/json"},
			"X-Amzn-Requestid": {"rid-kiro-native-400"},
		},
		Body: io.NopCloser(bytes.NewReader(u.body)), Request: req,
	}, nil
}

func TestKiroMessagesRouterArchivesMalformedProvider200AsTerminalWithoutBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, accountID, userID = int64(9830), int64(9831), int64(9832)
	rawProviderBody := append(
		buildHandlerKiroRawEventStreamFrame(t, "assistantResponseEvent", []byte(`{"assistantResponseEvent":{}} {}`)),
		buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
	)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformKiro, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "kiro-native-malformed", Platform: service.PlatformKiro,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key": "kiro-provider-secret", "api_region": "us-east-1",
			"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &kiroTerminalCaptureUpstream{body: rawProviderBody, status: http.StatusOK}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 8 << 20
	settingService := newTerminalOnlyCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, kiroTerminalCaptureCooldownStore{}, nil, nil, nil, settingService, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
	)
	h := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9833, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.Messages(c)
	capturePool.Stop()

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.True(t, json.Valid(recorder.Body.Bytes()))
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, service.PlatformKiro, record.Platform)
	require.Equal(t, http.StatusOK, record.HTTPStatus)
	require.Equal(t, rawProviderBody, record.RawResponse)
	require.Equal(t, upstream.seen, record.RawRequest)
}

func (u *kiroTerminalCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.response(req)
}

func (u *kiroTerminalCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(req)
}

func TestKiroMessagesRouterArchivesNativeTerminalProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, accountID, userID = int64(9810), int64(9811), int64(9812)
	errorBody := []byte(`{"message":"kiro rejected request","reason":"invalid_request","padding":"` + strings.Repeat("x", 3<<20) + `"}`)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformKiro, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "kiro-native-terminal", Platform: service.PlatformKiro,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key": "kiro-provider-secret", "api_region": "us-east-1",
			"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &kiroTerminalCaptureUpstream{body: errorBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 8 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, kiroTerminalCaptureCooldownStore{}, nil, nil, nil, settingService, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
	)
	h := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9813, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.Messages(c)
	capturePool.Stop()

	require.NotEmpty(t, upstream.seen, "request must reach the native KIRO provider; response=%s", recorder.Body.String())
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.True(t, json.Valid(recorder.Body.Bytes()), "KIRO JSON error must not be followed by a generic SSE frame: %q", recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "data: {")
	require.Len(t, captureRecords, 1, "native KIRO terminal provider attempt must be archived exactly once")
	record := <-captureRecords
	require.Equal(t, service.PlatformKiro, record.Platform)
	require.Equal(t, "rid-kiro-native-400", record.RequestID)
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, errorBody, record.RawResponse)
	require.False(t, record.Truncated)
	require.Equal(t, upstream.seen, record.RawRequest)
	require.NotEqual(t, requestBody, record.RawRequest)
	require.NotContains(t, string(record.RequestHeaders), "kiro-provider-secret")
}

func TestKiroCompatibilityRoutersArchiveFinalNativeHTTPFailureWithoutBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		path        string
		requestBody string
		handle      func(*GatewayHandler, *gin.Context)
	}{
		{
			name:        "chat_completions",
			path:        "/v1/chat/completions",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(9840 + index*10)
			accountID := groupID + 1
			userID := groupID + 2
			group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformKiro, Status: service.StatusActive, RateMultiplier: 1}
			account := &service.Account{
				ID: accountID, Name: "kiro-compat-terminal", Platform: service.PlatformKiro,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
				Credentials: map[string]any{
					"api_key": "kiro-provider-secret", "api_region": "us-west-2",
					"model_mapping": map[string]any{"claude-sonnet-4-6": "kiro-mapped-model"},
				},
				AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
			}
			errorBody := []byte(`{"message":"KIRO payment required","reason":"subscription_required"}`)
			upstream := &kiroTerminalCaptureUpstream{body: errorBody, status: http.StatusPaymentRequired}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitches = 1
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			settingService := newTerminalOnlyCaptureSettingService(t, cfg)
			scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			captureRecords := make(chan *service.CaptureRecord, 4)
			capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
			usageRecords := make(chan *service.UsageLog, 2)
			usageRepo := &rawCCHandlerUsageRepo{records: usageRecords}
			gateway := service.NewGatewayService(
				&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
				nil, nil, kiroTerminalCaptureCooldownStore{}, nil, nil, nil, settingService, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
			)
			h := NewGatewayHandler(
				gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
			)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: groupID + 3, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
				Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

			tt.handle(h, c)
			capturePool.Stop()

			require.NotEmpty(t, upstream.seen, "compatibility handler must reach the native KIRO runtime")
			require.Equal(t, http.StatusPaymentRequired, recorder.Code)
			require.Len(t, captureRecords, 1, "final KIRO provider attempt must be archived exactly once")
			record := <-captureRecords
			require.Equal(t, service.PlatformKiro, record.Platform)
			require.Equal(t, "rid-kiro-native-400", record.RequestID)
			require.Equal(t, http.StatusPaymentRequired, record.HTTPStatus)
			require.Equal(t, upstream.seen, record.RawRequest)
			require.Equal(t, errorBody, record.RawResponse)
			require.NotContains(t, string(record.RequestHeaders), "kiro-provider-secret")
			require.Contains(t, string(record.ResponseHeaders), "X-Amzn-Requestid")
			require.Empty(t, usageRecords, "terminal KIRO compatibility failure must not create usage")
		})
	}
}

func buildHandlerKiroEventStreamFrame(t *testing.T, eventType string, payload map[string]any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	return buildHandlerKiroRawEventStreamFrame(t, eventType, payloadBytes)
}

func buildHandlerKiroRawEventStreamFrame(t *testing.T, eventType string, payloadBytes []byte) []byte {
	t.Helper()
	headers := bytes.NewBuffer(nil)
	require.NoError(t, headers.WriteByte(byte(len(":event-type"))))
	_, _ = headers.WriteString(":event-type")
	require.NoError(t, headers.WriteByte(7))
	require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(eventType))))
	_, _ = headers.WriteString(eventType)
	totalLength := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, totalLength))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func TestKiroRoutersFailOverEmptyOrUsageOnlyEventStreamWithoutCapturingOrBillingFirstAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		path        string
		requestBody string
		firstBody   func(*testing.T) []byte
		handle      func(*GatewayHandler, *gin.Context)
	}{
		{
			name:        "direct_nonstream_empty",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody:   func(*testing.T) []byte { return nil },
			handle:      func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_usage_only",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return buildHandlerKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
					"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"inputTokens": 2, "outputTokens": 0}},
				})
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_empty",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody:   func(*testing.T) []byte { return nil },
			handle:      func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses_usage_only",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return buildHandlerKiroEventStreamFrame(t, "usageEvent", map[string]any{
					"usageEvent": map[string]any{"inputTokens": 2, "outputTokens": 0},
				})
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_nonstream_malformed_then_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroRawEventStreamFrame(t, "assistantResponseEvent", []byte(`{"assistantResponseEvent":{}} {}`)),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_malformed_then_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroRawEventStreamFrame(t, "assistantResponseEvent", []byte(`{not-json}`)),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_malformed_then_stop",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroRawEventStreamFrame(t, "assistantResponseEvent", []byte(`{not-json}`)),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses_malformed_then_stop",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroRawEventStreamFrame(t, "assistantResponseEvent", []byte(`{"assistantResponseEvent":{}} {}`)),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_nonstream_malformed_known_content_then_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": 123}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_malformed_known_reasoning_then_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{"reasoningContentEvent": map[string]any{"text": 123}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_malformed_known_tool_then_stop",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": 123, "name": "tool", "input": `{}`}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "direct_nonstream_malformed_aggregated_tool",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "input": `{"path":`}}),
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "stop": true}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_malformed_aggregated_tool",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "input": `{"path":`}}),
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "stop": true}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_malformed_aggregated_tool",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "input": `{"path":`}}),
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "stop": true}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses_malformed_aggregated_tool",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "input": `{"path":`}}),
					buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{"toolUseEvent": map[string]any{"toolUseId": "toolu_invalid", "name": "lookup", "stop": true}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_nonstream_aggregate_tool_missing_required_field",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"toolUses": []map[string]any{{"toolUseId": "toolu_missing_path", "name": "write", "input": map[string]any{"content": "x"}}}}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "tool_use"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_aggregate_tool_missing_required_field",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"toolUses": []map[string]any{{"toolUseId": "toolu_missing_path", "name": "write", "input": map[string]any{"content": "x"}}}}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "tool_use"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_aggregate_tool_missing_required_field",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"toolUses": []map[string]any{{"toolUseId": "toolu_missing_path", "name": "write", "input": map[string]any{"content": "x"}}}}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "tool_use"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses_aggregate_tool_missing_required_field",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"toolUses": []map[string]any{{"toolUseId": "toolu_missing_path", "name": "write", "input": map[string]any{"content": "x"}}}}}),
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "tool_use"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_nonstream_semantic_after_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}}),
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "first-leak"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_semantic_after_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}}),
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "first-leak"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "responses_semantic_after_stop",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}}),
					buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "first-leak"}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_nonstream_usage_after_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}}),
					buildHandlerKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"inputTokens": 999, "outputTokens": 999}}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "direct_stream_metering_after_stop",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return append(
					buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}}),
					buildHandlerKiroEventStreamFrame(t, "meteringEvent", map[string]any{"meteringEvent": map[string]any{"usage": 999.0}})...,
				)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "chat_completions_orphan_tool_stop",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				return buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{
					"toolUseEvent": map[string]any{"toolUseId": "toolu_orphan", "stop": true},
				})
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:        "responses_orphan_tool_stop",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				return buildHandlerKiroEventStreamFrame(t, "toolUseEvent", map[string]any{
					"toolUseEvent": map[string]any{"toolUseId": "toolu_orphan", "stop": true},
				})
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "direct_stream_corrupt_crc_then_valid_frame",
			path:        EndpointMessages,
			requestBody: `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				corrupt := buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "must-not-leak"}})
				corrupt[len(corrupt)-1] ^= 0xff
				return append(corrupt, buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name:        "responses_malformed_header_then_valid_frame",
			path:        "/v1/responses",
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`,
			firstBody: func(t *testing.T) []byte {
				malformed := buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "must-not-leak"}})
				headerTypeOffset := 12 + 1 + len(":event-type")
				malformed[headerTypeOffset] = 0xff
				binary.BigEndian.PutUint32(malformed[len(malformed)-4:], crc32.ChecksumIEEE(malformed[:len(malformed)-4]))
				return append(malformed, buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name:        "chat_completions_invalid_utf8_header_then_valid_frame",
			path:        EndpointChatCompletions,
			requestBody: `{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			firstBody: func(t *testing.T) []byte {
				malformed := buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "must-not-leak"}})
				valueAt := bytes.Index(malformed[12:], []byte("assistantResponseEvent")) + 12
				require.GreaterOrEqual(t, valueAt, 12)
				malformed[valueAt] = 0xff
				binary.BigEndian.PutUint32(malformed[len(malformed)-4:], crc32.ChecksumIEEE(malformed[:len(malformed)-4]))
				return append(malformed, buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"messageStopEvent": map[string]any{"stopReason": "end_turn"}})...)
			},
			handle: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(9860 + index*10)
			firstAccountID, secondAccountID, userID := groupID+1, groupID+2, groupID+3
			group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformKiro, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true}
			newAccount := func(id int64, priority int) *service.Account {
				return &service.Account{
					ID: id, Name: "kiro-empty-failover", Platform: service.PlatformKiro,
					Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
					Credentials: map[string]any{
						"api_key": "kiro-provider-secret", "api_region": "us-west-2",
						"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
					},
					AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
				}
			}
			firstAccount, secondAccount := newAccount(firstAccountID, 1), newAccount(secondAccountID, 2)
			var second bytes.Buffer
			second.Write(buildHandlerKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "recovered"},
			}))
			second.Write(buildHandlerKiroEventStreamFrame(t, "messageStopEvent", map[string]any{
				"messageStopEvent": map[string]any{"stopReason": "end_turn"},
			}))
			secondBody := second.Bytes()
			upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: tt.firstBody(t), second: secondBody}
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
			terminals := make(chan string, 32)
			capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(captureRecords, terminals)
			usageRepo := &gatewayAnthropicUsageRepo{}
			gateway := service.NewGatewayService(
				&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
				nil, nil, kiroTerminalCaptureCooldownStore{}, nil, nil, nil, settingService, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
			)
			h := NewGatewayHandler(
				gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
			)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: groupID + 4, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
				Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

			tt.handle(h, c)
			capturePool.Stop()

			upstream.mu.Lock()
			calls := append([]int64(nil), upstream.calls...)
			secondRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
			upstream.mu.Unlock()
			require.GreaterOrEqual(t, len(calls), 2)
			require.Equal(t, secondAccountID, calls[len(calls)-1])
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "recovered")
			require.Len(t, captureRecords, 1, "only the final successful provider attempt is captured")
			record := <-captureRecords
			require.Equal(t, secondBody, record.RawResponse)
			require.Len(t, secondRequests, 1)
			require.Equal(t, secondRequests[0], record.RawRequest)
			require.Len(t, terminals, len(calls), "each real KIRO attempt must reach exactly one terminal state")
			terminalStates := make([]string, 0, len(calls))
			for range calls {
				terminalStates = append(terminalStates, <-terminals)
			}
			expectedTerminals := make([]string, len(calls))
			for i := range expectedTerminals[:len(expectedTerminals)-1] {
				expectedTerminals[i] = "abort"
			}
			expectedTerminals[len(expectedTerminals)-1] = "commit"
			require.Equal(t, expectedTerminals, terminalStates)
			require.Len(t, usageRepo.snapshot(), 1, "only the second account is billed")
			require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
		})
	}
}

func TestKiroMessagesRouterOnlyWebSearchFinal503IsTerminalAndArchivedExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, accountID, userID = int64(9820), int64(9821), int64(9822)
	terminalBody := []byte(`{"message":"final KIRO web-search overload"}`)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformKiro, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "kiro-websearch-terminal", Platform: service.PlatformKiro,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key": "kiro-provider-secret", "api_region": "us-east-1",
			"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &kiroWebSearchTerminalCaptureUpstream{terminalBody: terminalBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 8 << 20
	settingService := newTerminalOnlyCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, kiroTerminalCaptureCooldownStore{}, nil, nil, nil, settingService, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
	)
	h := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"latest news"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9823, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.Messages(c)
	capturePool.Stop()

	upstream.mu.Lock()
	runtimeBodies := append([][]byte(nil), upstream.runtimeBodies...)
	upstream.mu.Unlock()
	require.GreaterOrEqual(t, len(runtimeBodies), 3, "KIRO must perform its bounded AWS runtime retry sequence")
	require.Zero(t, len(runtimeBodies)%3, "each selected account attempt must use the three-request KIRO retry budget")
	require.NotEqual(t, http.StatusOK, recorder.Code, "the synthetic message_start must not turn a final AWS 503 into success")
	require.NotContains(t, recorder.Body.String(), "message_start", "pre-semantic staging must discard the synthetic preamble on terminal failure")
	require.Len(t, captureRecords, 1, "terminal-only policy must archive the final AWS attempt exactly once")
	record := <-captureRecords
	require.Equal(t, service.PlatformKiro, record.Platform)
	require.Equal(t, "rid-kiro-websearch-final-503", record.RequestID)
	require.Equal(t, http.StatusServiceUnavailable, record.HTTPStatus)
	require.Equal(t, runtimeBodies[len(runtimeBodies)-1], record.RawRequest)
	require.Equal(t, terminalBody, record.RawResponse)
	require.False(t, record.Truncated)
}
