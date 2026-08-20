//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type geminiNativeCaptureUpstream struct {
	mu       sync.Mutex
	lastBody []byte
	status   int
	response []byte
	readErr  error
	calls    chan struct{}
}

type geminiNativeDataErrorBody struct {
	data []byte
	err  error
	done bool
}

func (b *geminiNativeDataErrorBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	b.done = true
	return copy(p, b.data), b.err
}

func (*geminiNativeDataErrorBody) Close() error { return nil }

func (u *geminiNativeCaptureUpstream) responseFor(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.lastBody = append([]byte(nil), body...)
	u.mu.Unlock()
	if u.calls != nil {
		u.calls <- struct{}{}
	}
	status := u.status
	if status == 0 {
		status = http.StatusOK
	}
	responseBody := io.ReadCloser(io.NopCloser(bytes.NewReader(u.response)))
	if u.readErr != nil {
		responseBody = &geminiNativeDataErrorBody{data: u.response, err: u.readErr}
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"rid-gemini-native"}},
		Body:       responseBody,
		Request:    req,
	}, nil
}

func TestGeminiNativeRouterAbortsAttemptBeforeSameAccountRetryDelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		groupID   = int64(9737)
		accountID = int64(9738)
		userID    = int64(9739)
	)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "gemini-native-retry-delay", Platform: service.PlatformGemini,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
			"pool_mode": true, "pool_mode_retry_count": float64(1),
			"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
			"model_mapping":                map[string]any{"gemini-test": "gemini-test-upstream"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &geminiNativeCaptureUpstream{
		status: http.StatusBadGateway, response: []byte(`{"error":{"code":502,"message":"temporary"}}`), calls: make(chan struct{}, 2),
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitchesGemini = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	terminals := make(chan string, 4)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(make(chan *service.CaptureRecord, 1), terminals)
	t.Cleanup(capturePool.Stop)
	accountRepo := &antigravityCaptureAccountRepo{}
	rateLimits := service.NewRateLimitService(accountRepo, nil, cfg, nil, nil)
	gateway := service.NewGatewayService(
		accountRepo, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
	)
	gemini := service.NewGeminiMessagesCompatService(accountRepo, &fakeGroupRepo{group: group}, nil, scheduler, nil, rateLimits, upstream, nil, cfg, capturePool)
	h := NewGatewayHandler(
		gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)).WithContext(requestCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:generateContent"}}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9740, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	done := make(chan struct{})
	go func() {
		h.GeminiV1BetaModels(c)
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

func (u *geminiNativeCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.responseFor(req)
}

func (u *geminiNativeCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.responseFor(req)
}

func TestGeminiNativeRouterArchivesProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name               string
		action             string
		skipCapture        bool
		terminalOnly       bool
		upstreamReadErr    error
		upstreamStatus     int
		upstreamResponse   []byte
		expectedHTTPStatus int
	}{
		{
			name:               "count_tokens_probe_is_not_a_conversation",
			action:             "countTokens",
			skipCapture:        true,
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte(`{"totalTokens":3}`),
			expectedHTTPStatus: http.StatusOK,
		},
		{
			name:               "success",
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte(`{"candidates":[{"content":{"parts":[{"text":"Done."}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3},"modelVersion":"gemini-test-upstream"}`),
			expectedHTTPStatus: http.StatusOK,
		},
		{
			name:               "buffered_missing_usage_is_terminal",
			terminalOnly:       true,
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte(`{"candidates":[{"content":{"parts":[{"text":"Done without usage."}],"role":"model"},"finishReason":"STOP"}],"modelVersion":"gemini-test-upstream"}`),
			expectedHTTPStatus: http.StatusOK,
		},
		{
			name:               "streaming_missing_usage_is_terminal",
			action:             "streamGenerateContent",
			terminalOnly:       true,
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Done without usage.\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"modelVersion\":\"gemini-test-upstream\"}\n\n"),
			expectedHTTPStatus: http.StatusOK,
		},
		{
			name:               "provider_200_read_failure_is_terminal",
			terminalOnly:       true,
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte(`{"partial":"provider bytes"}`),
			upstreamReadErr:    errors.New("forced provider body read failure"),
			expectedHTTPStatus: http.StatusBadGateway,
		},
		{
			name:               "terminal_provider_error",
			upstreamStatus:     http.StatusUnauthorized,
			upstreamResponse:   []byte(`{"error":{"code":401,"message":"` + strings.Repeat("x", 600<<10) + `"}}`),
			expectedHTTPStatus: http.StatusBadGateway,
		},
		{
			name:               "terminal_non_failover_provider_error",
			upstreamStatus:     http.StatusUnprocessableEntity,
			upstreamResponse:   []byte(`{"error":{"code":422,"message":"` + strings.Repeat("y", 600<<10) + `"}}`),
			expectedHTTPStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := tt.action
			if action == "" {
				action = "generateContent"
			}
			const (
				groupID   = int64(9730)
				accountID = int64(9731)
				userID    = int64(9732)
			)
			group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
			account := &service.Account{
				ID: accountID, Name: "gemini-native-capture", Platform: service.PlatformGemini,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
				Credentials: map[string]any{
					"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
					"model_mapping": map[string]any{"gemini-test": "gemini-test-upstream"},
				},
				AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
			}
			upstream := &geminiNativeCaptureUpstream{status: tt.upstreamStatus, response: tt.upstreamResponse, readErr: tt.upstreamReadErr}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitchesGemini = 1
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			settingService := newEnabledCaptureSettingService(t, cfg)
			if tt.terminalOnly {
				settingService = newTerminalOnlyCaptureSettingService(t, cfg)
			}
			scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			captureRecords := make(chan *service.CaptureRecord, 4)
			capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
			accountRepo := &antigravityCaptureAccountRepo{}
			gateway := service.NewGatewayService(
				accountRepo, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
			)
			gemini := service.NewGeminiMessagesCompatService(accountRepo, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg, capturePool)
			h := NewGatewayHandler(
				gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
			)

			requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:"+action, bytes.NewReader(requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:" + action}}
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 9733, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
				Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

			h.GeminiV1BetaModels(c)
			capturePool.Stop()
			require.Equal(t, tt.expectedHTTPStatus, recorder.Code)
			if tt.skipCapture {
				require.Empty(t, captureRecords, "countTokens is an operational probe, not a conversation")
				return
			}
			if tt.upstreamStatus == http.StatusUnprocessableEntity {
				if !json.Valid(recorder.Body.Bytes()) {
					body := recorder.Body.Bytes()
					prefix, suffix := body, body
					if len(prefix) > 256 {
						prefix = prefix[:256]
					}
					if len(suffix) > 256 {
						suffix = suffix[len(suffix)-256:]
					}
					t.Fatalf("mapped provider JSON error must remain one JSON document: len=%d prefix=%q suffix=%q", len(body), prefix, suffix)
				}
				require.NotContains(t, recorder.Body.String(), "data: {")
			}
			require.Len(t, captureRecords, 1, "the native Gemini router must archive one provider exchange")
			archived := <-captureRecords
			upstream.mu.Lock()
			actualRequest := append([]byte(nil), upstream.lastBody...)
			upstream.mu.Unlock()
			require.Equal(t, actualRequest, archived.RawRequest)
			wantResponse := tt.upstreamResponse
			wantTruncated := tt.upstreamReadErr != nil
			if tt.upstreamStatus >= http.StatusBadRequest && len(wantResponse) > 512<<10 {
				wantResponse = wantResponse[:512<<10]
				wantTruncated = true
			}
			require.Len(t, archived.RawResponse, len(wantResponse))
			require.True(t, bytes.Equal(wantResponse, archived.RawResponse), "capture must contain exactly the bytes naturally consumed by the Gemini response parser")
			require.Equal(t, wantTruncated, archived.Truncated)
			require.Equal(t, tt.upstreamStatus, archived.HTTPStatus)
			require.NotContains(t, string(archived.RequestHeaders), "gemini-secret")
		})
	}
}

func TestGeminiMessagesNonStreamingLargeProviderResponseIsNotLimitedByCaptureCeiling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		groupID   = int64(9734)
		accountID = int64(9735)
		userID    = int64(9736)
	)
	largeText := strings.Repeat("x", config.GatewayCaptureMaxBodyBytes+1024) + "tail-marker"
	providerResponse := []byte(`{"candidates":[{"content":{"parts":[{"text":"` + largeText + `"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3},"modelVersion":"gemini-3.1-pro"}`)
	require.Greater(t, len(providerResponse), config.GatewayCaptureMaxBodyBytes)

	for _, captureEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture_off", true: "capture_on"}[captureEnabled], func(t *testing.T) {
			group := &service.Group{
				ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive,
				RateMultiplier: 1, AllowMessagesDispatch: true,
			}
			account := &service.Account{
				ID: accountID, Name: "gemini-large-nonstream", Platform: service.PlatformGemini,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
				Credentials: map[string]any{
					"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
					"model_mapping": map[string]any{"gemini-3.1-pro": "gemini-3.1-pro"},
				},
				AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
			}
			upstream := &geminiNativeCaptureUpstream{status: http.StatusOK, response: providerResponse}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitchesGemini = 1
			cfg.Gateway.Capture.Enabled = captureEnabled
			cfg.Gateway.Capture.MaxBodyBytes = config.GatewayCaptureMaxBodyBytes
			cfg.Gateway.Capture.MaxQueueBytes = 2 * config.GatewayCaptureMaxBodyBytes
			settingService := service.NewSettingService(&handlerCaptureSettingRepo{}, cfg)
			if captureEnabled {
				settingService = newEnabledCaptureSettingService(t, cfg)
			}
			scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			captureRecords := make(chan *service.CaptureRecord, 2)
			capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
			usageRepo := &gatewayAnthropicUsageRepo{}
			accountRepo := &antigravityCaptureAccountRepo{}
			gateway := service.NewGatewayService(
				accountRepo, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, settingService, nil, nil, nil, nil, nil, capturePool,
			)
			gemini := service.NewGeminiMessagesCompatService(accountRepo, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg, capturePool)
			h := NewGatewayHandler(
				gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
			)

			requestBody := []byte(`{"model":"gemini-3.1-pro","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 9737, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
				Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

			h.Messages(c)
			capturePool.Stop()
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, len(largeText), len(gjson.Get(recorder.Body.String(), "content.0.text").String()))
			require.Len(t, usageRepo.snapshot(), 1, "the successful provider attempt must be billed exactly once")
			if !captureEnabled {
				require.Empty(t, captureRecords, "capture-off requests must not retain the large provider body")
				return
			}
			require.Len(t, captureRecords, 1, "capture-on requests must archive the successful attempt exactly once")
			archived := <-captureRecords
			require.Len(t, archived.RawResponse, config.GatewayCaptureMaxBodyBytes)
			require.Equal(t, providerResponse[:config.GatewayCaptureMaxBodyBytes], archived.RawResponse)
			require.True(t, archived.Truncated)
			require.Equal(t, http.StatusOK, archived.HTTPStatus)
			require.NotContains(t, string(archived.RequestHeaders), "gemini-secret")
		})
	}
}

func testAntigravityGeminiNativeRouterFailover(t *testing.T, firstBody []byte) {
	gin.SetMode(gin.TestMode)
	const groupID, firstAccountID, secondAccountID, userID = int64(9740), int64(9741), int64(9742), int64(9743)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "antigravity-gemini-two-account", Platform: service.PlatformAntigravity,
			Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{
				"access_token": "stale-secret", "project_id": "project-capture",
				"model_mapping": map[string]any{"gemini-test": "gemini-test"},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount := newAccount(firstAccountID, 1)
	secondAccount := newAccount(secondAccountID, 2)
	secondBody := []byte("data: {\"response\":{\"responseId\":\"second-success\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"recovered\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}}\n\n")
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitchesGemini = 2
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
	tokenProvider := service.NewAntigravityTokenProvider(nil, &antigravityCaptureTokenCache{}, nil)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil, capturePool)
	h := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:streamGenerateContent", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, service.PlatformAntigravity))
	c.Set(string(middleware.ContextKeyForcePlatform), service.PlatformAntigravity)
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:streamGenerateContent"}}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9744, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.GeminiV1BetaModels(c)
	capturePool.Stop()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "recovered")
	require.NotContains(t, recorder.Body.String(), "first-leak")
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	require.GreaterOrEqual(t, len(calls), 2)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	for _, accountID := range calls[:len(calls)-1] {
		require.Equal(t, firstAccountID, accountID)
	}
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Len(t, finalRequests, 1)
	require.Equal(t, finalRequests[0], record.RawRequest)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
}

func TestGeminiNativeMultiCandidateStreamAllowsLaterCandidateAfterEarlierFinish(t *testing.T) {
	body := []byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"primary\"}]},\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"candidates\":[{\"index\":1,\"content\":{\"parts\":[{\"text\":\"accepted-first\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}\n\ndata: [DONE]\n\n")
	testGeminiNativeStreamPresemanticFailover(t, body, true)
}

func testGeminiNativeStreamPresemanticFailover(t *testing.T, firstBody []byte, expectFirstTerminal ...bool) {
	gin.SetMode(gin.TestMode)
	const groupID, firstAccountID, secondAccountID, userID = int64(9750), int64(9751), int64(9752), int64(9753)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "gemini-native-two-account", Platform: service.PlatformGemini,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{
				"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
				"model_mapping": map[string]any{"gemini-3.1-pro": "gemini-3.1-pro"},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount := newAccount(firstAccountID, 1)
	secondAccount := newAccount(secondAccountID, 2)
	secondBody := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"recovered\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}\n\ndata: [DONE]\n\n")
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitchesGemini = 2
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg, capturePool)
	h := NewGatewayHandler(
		gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.1-pro:streamGenerateContent", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-3.1-pro:streamGenerateContent"}}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9754, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.GeminiV1BetaModels(c)
	capturePool.Stop()
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	firstRequests := append([][]byte(nil), upstream.requests[firstAccountID]...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	if len(expectFirstTerminal) > 0 && expectFirstTerminal[0] {
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.True(t, strings.Contains(recorder.Body.String(), "blocked-by-provider") || strings.Contains(recorder.Body.String(), "accepted-first"), recorder.Body.String())
		require.NotEmpty(t, calls)
		for _, accountID := range calls {
			require.Equal(t, firstAccountID, accountID)
		}
		require.Len(t, captureRecords, 1)
		record := <-captureRecords
		require.Equal(t, firstBody, record.RawResponse)
		require.NotEmpty(t, firstRequests)
		require.Equal(t, firstRequests[len(firstRequests)-1], record.RawRequest)
		require.Len(t, usageRepo.snapshot(), 1)
		require.Equal(t, firstAccountID, usageRepo.snapshot()[0].AccountID)
		return
	}
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "recovered")
	require.NotContains(t, recorder.Body.String(), "first-leak")
	require.NotContains(t, recorder.Body.String(), "data: [DONE]\n\ndata:", "the DONE-only first attempt must stay staged")
	require.GreaterOrEqual(t, len(calls), 2)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Len(t, finalRequests, 1)
	require.Equal(t, finalRequests[0], record.RawRequest)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
}

func testGeminiNativeGenerateContentPresemanticFailover(t *testing.T, firstBody []byte) {
	gin.SetMode(gin.TestMode)
	const groupID, firstAccountID, secondAccountID, userID = int64(9770), int64(9771), int64(9772), int64(9773)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "gemini-native-nonstream-two-account", Platform: service.PlatformGemini,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{
				"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
				"model_mapping": map[string]any{"gemini-test": "gemini-test-upstream"},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount := newAccount(firstAccountID, 1)
	secondAccount := newAccount(secondAccountID, 2)
	secondBody := []byte(`{"candidates":[{"content":{"parts":[{"text":"recovered"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`)
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitchesGemini = 2
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg, capturePool)
	h := NewGatewayHandler(
		gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent", bytes.NewReader([]byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:generateContent"}}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9774, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.GeminiV1BetaModels(c)
	capturePool.Stop()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "recovered")
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	require.GreaterOrEqual(t, len(calls), 2)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Len(t, finalRequests, 1)
	require.Equal(t, finalRequests[0], record.RawRequest)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
}

func testGeminiCompatibilityDONEOnlyAccountFailover(t *testing.T, endpoint string) {
	testGeminiCompatibilityPresemanticFailover(t, endpoint, []byte("data: [DONE]\n\n"))
}

func testGeminiCompatibilityPresemanticFailover(t *testing.T, endpoint string, firstBody []byte, expectFirstTerminal ...bool) {
	gin.SetMode(gin.TestMode)
	clientStream := !strings.HasSuffix(endpoint, "_buffered")
	endpoint = strings.TrimSuffix(endpoint, "_buffered")
	const groupID, firstAccountID, secondAccountID, userID = int64(9755), int64(9756), int64(9757), int64(9758)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true}
	newAccount := func(id int64, priority int) *service.Account {
		return &service.Account{
			ID: id, Name: "gemini-compat-two-account", Platform: service.PlatformGemini,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{
				"api_key": "gemini-secret", "base_url": "https://generativelanguage.googleapis.com",
				"model_mapping": map[string]any{"gemini-test": "gemini-test-upstream"},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
	}
	firstAccount := newAccount(firstAccountID, 1)
	secondAccount := newAccount(secondAccountID, 2)
	secondBody := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"recovered\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3}}\n\ndata: [DONE]\n\n")
	if !clientStream {
		secondBody = []byte(`{"candidates":[{"content":{"parts":[{"text":"recovered"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`)
	}
	upstream := &antigravityTwoAccountCaptureUpstream{firstID: firstAccountID, first: firstBody, second: secondBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.MaxAccountSwitchesGemini = 2
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg, capturePool)
	h := NewGatewayHandler(
		gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(fmt.Sprintf(`{"model":"gemini-test","stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, clientStream))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	path := "/v1/messages"
	if endpoint == "chat_completions" {
		path = EndpointChatCompletions
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9759, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})
	if endpoint == "chat_completions" {
		h.ChatCompletions(c)
	} else {
		h.Messages(c)
	}
	capturePool.Stop()
	upstream.mu.Lock()
	calls := append([]int64(nil), upstream.calls...)
	firstRequests := append([][]byte(nil), upstream.requests[firstAccountID]...)
	finalRequests := append([][]byte(nil), upstream.requests[secondAccountID]...)
	upstream.mu.Unlock()
	t.Logf("endpoint=%s status=%d calls=%v captures=%d usages=%d body=%q", endpoint, recorder.Code, calls, len(captureRecords), len(usageRepo.snapshot()), recorder.Body.String())
	if len(expectFirstTerminal) > 0 && expectFirstTerminal[0] {
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.NotEmpty(t, calls)
		for _, accountID := range calls {
			require.Equal(t, firstAccountID, accountID)
		}
		require.Len(t, captureRecords, 1)
		record := <-captureRecords
		require.Equal(t, firstBody, record.RawResponse)
		require.NotEmpty(t, firstRequests)
		require.Equal(t, firstRequests[len(firstRequests)-1], record.RawRequest)
		require.Len(t, usageRepo.snapshot(), 1)
		require.Equal(t, firstAccountID, usageRepo.snapshot()[0].AccountID)
		return
	}
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "recovered")
	require.NotContains(t, recorder.Body.String(), "first-leak")
	require.GreaterOrEqual(t, len(calls), 2)
	require.Equal(t, secondAccountID, calls[len(calls)-1])
	require.Len(t, captureRecords, 1)
	record := <-captureRecords
	require.Equal(t, secondBody, record.RawResponse)
	require.Len(t, finalRequests, 1)
	require.Equal(t, finalRequests[0], record.RawRequest)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, secondAccountID, usageRepo.snapshot()[0].AccountID)
}
