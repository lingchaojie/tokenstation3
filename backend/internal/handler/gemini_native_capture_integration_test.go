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
			gemini := service.NewGeminiMessagesCompatService(accountRepo, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg)
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
			require.Equal(t, tt.upstreamResponse, archived.RawResponse)
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
	providerResponse := []byte(`{"candidates":[{"content":{"parts":[{"text":"` + largeText + `"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3},"modelVersion":"gemini-test-upstream"}`)
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
					"model_mapping": map[string]any{"gemini-test": "gemini-test-upstream"},
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
			gemini := service.NewGeminiMessagesCompatService(accountRepo, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg)
			h := NewGatewayHandler(
				gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
			)

			requestBody := []byte(`{"model":"gemini-test","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
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

func TestAntigravityGeminiNativeRouterFailoverArchivesOnlyFinalSemanticAccount(t *testing.T) {
	firstBodies := map[string][]byte{
		"done_only": []byte("data: [DONE]\n\n"),
		"malformed_then_valid": []byte("data: {not-json}\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"empty_candidates_then_valid": []byte("data: {\"response\":{\"candidates\":[]}}\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
		"done_then_valid": []byte("data: [DONE]\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}}\n\n"),
	}
	for name, firstBody := range firstBodies {
		t.Run(name, func(t *testing.T) {
			testAntigravityGeminiNativeRouterFailover(t, firstBody)
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
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil)
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

func TestGeminiNativeStreamDONEOnlyAccountFailsOverWithoutCaptureOrBilling(t *testing.T) {
	testGeminiNativeStreamPresemanticFailover(t, []byte("data: [DONE]\n\n"))
}

func TestGeminiNativeBlockedPromptFeedbackIsTerminalWithoutAccountReplay(t *testing.T) {
	blocked := []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"blockReasonMessage\":\"blocked-by-provider\"}}\n\n")
	testGeminiNativeStreamPresemanticFailover(t, blocked, true)
}

func TestGeminiNativeAncillaryUsageBeforeValidTerminalDoesNotReplay(t *testing.T) {
	body := []byte("data: {\"usageMetadata\":{\"promptTokenCount\":8},\"modelVersion\":\"gemini-provider\"}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"accepted-first\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n")
	testGeminiNativeStreamPresemanticFailover(t, body, true)
}

func TestGeminiNativeMultiCandidateStreamAllowsLaterCandidateAfterEarlierFinish(t *testing.T) {
	body := []byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"primary\"}]},\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"candidates\":[{\"index\":1,\"content\":{\"parts\":[{\"text\":\"accepted-first\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n")
	testGeminiNativeStreamPresemanticFailover(t, body, true)
}

func TestGeminiCommittedContentWithoutFinishDoesNotReplayAfterDONE(t *testing.T) {
	body := []byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"accepted-first\"}]}}]}\n\ndata: [DONE]\n\n")
	t.Run("native", func(t *testing.T) {
		testGeminiNativeStreamPresemanticFailover(t, body, true)
	})
	for _, endpoint := range []string{"messages", "chat_completions"} {
		t.Run(endpoint, func(t *testing.T) {
			testGeminiCompatibilityPresemanticFailover(t, endpoint, body, true)
		})
	}
}

func TestGeminiNativeStreamRejectsMalformedProviderPayloadsBeforeCommit(t *testing.T) {
	firstBodies := map[string][]byte{
		"empty_candidates":           []byte("data: {\"candidates\":[]}\n\ndata: [DONE]\n\n"),
		"empty_candidate":            []byte("data: {\"candidates\":[{}]}\n\ndata: [DONE]\n\n"),
		"finish_only":                []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"usage_only":                 []byte("data: {\"usageMetadata\":{\"promptTokenCount\":8}}\n\ndata: [DONE]\n\n"),
		"total_tokens_only":          []byte("data: {\"totalTokens\":8}\n\ndata: [DONE]\n\n"),
		"nonstring_finish_reason":    []byte("data: {\"candidates\":[{\"finishReason\":123}]}\n\ndata: [DONE]\n\n"),
		"malformed_first_candidate":  []byte("data: {\"candidates\":[{}, {\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"malformed_later_candidate":  []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}},{\"finishReason\":123}]}\n\ndata: [DONE]\n\n"),
		"fractional_candidate_index": []byte("data: {\"candidates\":[{\"index\":0.5,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"mixed_duplicate_index":      []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}},{\"index\":0,\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"nonstring_part_text":        []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":123}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"nonstring_function_name":    []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":123,\"args\":{}}}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"oversized_function_name":    []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"" + strings.Repeat("x", 1025) + "\",\"args\":{}}}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"scalar_candidate":           []byte("data: {\"candidates\":[123]}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_feedback":    []byte("data: {\"promptFeedback\":{\"foo\":\"bar\"}}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_rating":      []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"safetyRatings\":[123]}}\n\n"),
		"invalid_candidate_ratings":  []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"safetyRatings\":\"bad\"}]}\n\n"),
		"invalid_grounding_metadata": []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"groundingMetadata\":{\"groundingChunks\":[123]}}]}\n\n"),
		"invalid_finish_message":     []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"finishMessage\":123}]}\n\n"),
		"blocked_with_candidate":     []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"},\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}}]}\n\ndata: [DONE]\n\n"),
		"nonstring_model_ancillary":  []byte("data: {\"modelVersion\":123}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"invalid_usage_ancillary":    []byte("data: {\"usageMetadata\":{\"promptTokenCount\":\"bad\"}}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"invalid_usage_details":      []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":-7}]}}\n\n"),
		"cached_exceeds_prompt":      []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"cachedContentTokenCount\":3}}\n\n"),
		"usage_sum_overflow":         []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":9223372036854775807,\"thoughtsTokenCount\":1}}\n\n"),
		"image_exceeds_output":       []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":3}]}}\n\n"),
		"malformed_then_valid": []byte("data: {not-json}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"empty_candidates_then_valid": []byte("data: {\"candidates\":[]}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"done_then_valid": []byte("data: [DONE]\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"too_many_candidates": []byte("data: {\"candidates\":[" +
			strings.Repeat(`{"content":{"role":"model","parts":[{"text":""}]}},`, 1024) +
			`{"content":{"role":"model","parts":[{"text":""}]}}]}` + "\n\n"),
	}
	for name, firstBody := range firstBodies {
		t.Run(name, func(t *testing.T) {
			testGeminiNativeStreamPresemanticFailover(t, firstBody)
		})
	}
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
				"model_mapping": map[string]any{"gemini-test": "gemini-test-upstream"},
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg)
	h := NewGatewayHandler(
		gateway, nil, gemini, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:streamGenerateContent", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:streamGenerateContent"}}
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

func TestGeminiNativeGenerateContentRejectsMalformedProviderPayloadsBeforeCommit(t *testing.T) {
	firstBodies := map[string][]byte{
		"empty_candidates":           []byte(`{"candidates":[]}`),
		"empty_candidate":            []byte(`{"candidates":[{}]}`),
		"finish_only":                []byte(`{"candidates":[{"finishReason":"STOP"}]}`),
		"usage_only":                 []byte(`{"usageMetadata":{"promptTokenCount":8}}`),
		"total_tokens_only":          []byte(`{"totalTokens":8}`),
		"nonstring_finish_reason":    []byte(`{"candidates":[{"finishReason":123}]}`),
		"malformed_first_candidate":  []byte(`{"candidates":[{}, {"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP"}]}`),
		"malformed_later_candidate":  []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]}},{"finishReason":123}]}`),
		"content_without_finish":     []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]}}]}`),
		"fractional_candidate_index": []byte(`{"candidates":[{"index":0.5,"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP"}]}`),
		"mixed_duplicate_index":      []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]}},{"index":0,"finishReason":"STOP"}]}`),
		"nonstring_part_text":        []byte(`{"candidates":[{"content":{"parts":[{"text":123}]},"finishReason":"STOP"}]}`),
		"nonstring_function_name":    []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":123,"args":{}}}]},"finishReason":"STOP"}]}`),
		"oversized_function_name":    []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"` + strings.Repeat("x", 1025) + `","args":{}}}]},"finishReason":"STOP"}]}`),
		"scalar_candidate":           []byte(`{"candidates":[123]}`),
		"invalid_prompt_feedback":    []byte(`{"promptFeedback":{"foo":"bar"}}`),
		"invalid_prompt_rating":      []byte(`{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[123]}}`),
		"invalid_candidate_ratings":  []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP","safetyRatings":"bad"}]}`),
		"invalid_grounding_metadata": []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP","groundingMetadata":{"groundingChunks":[123]}}]}`),
		"invalid_finish_message":     []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP","finishMessage":123}]}`),
		"blocked_with_candidate":     []byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"index":0,"content":{"parts":[{"text":"first-leak"}]}}]}`),
		"nonstring_model_sibling":    []byte(`{"modelVersion":123,"candidates":[{"finishReason":"STOP"}]}`),
		"invalid_usage_sibling":      []byte(`{"usageMetadata":{"promptTokenCount":"bad"},"candidates":[{"finishReason":"STOP"}]}`),
		"invalid_usage_details":      []byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":-7}]},"candidates":[{"content":{"parts":[{"text":"first-leak"}]}}, {"finishReason":"STOP"}]}`),
		"cached_exceeds_prompt":      []byte(`{"usageMetadata":{"promptTokenCount":2,"cachedContentTokenCount":3},"candidates":[{"finishReason":"STOP"}]}`),
		"usage_sum_overflow":         []byte(`{"usageMetadata":{"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1},"candidates":[{"finishReason":"STOP"}]}`),
		"image_exceeds_output":       []byte(`{"usageMetadata":{"candidatesTokenCount":2,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":3}]},"candidates":[{"finishReason":"STOP"}]}`),
		"too_many_candidates": []byte(`{"candidates":[` +
			strings.Repeat(`{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP"},`, 1024) +
			`{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP"}]}`),
	}
	for name, firstBody := range firstBodies {
		t.Run(name, func(t *testing.T) {
			testGeminiNativeGenerateContentPresemanticFailover(t, firstBody)
		})
	}
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg)
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

func TestGeminiCompatibilityStreamsDONEOnlyAccountFailsOverWithoutCaptureOrBilling(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat_completions"} {
		t.Run(endpoint, func(t *testing.T) {
			testGeminiCompatibilityDONEOnlyAccountFailover(t, endpoint)
		})
	}
}

func TestGeminiCompatibilityBlockedPromptFeedbackIsTerminalWithoutAccountReplay(t *testing.T) {
	blocked := []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"blockReasonMessage\":\"blocked-by-provider\"}}\n\n")
	for _, endpoint := range []string{"messages", "chat_completions"} {
		t.Run(endpoint, func(t *testing.T) {
			testGeminiCompatibilityPresemanticFailover(t, endpoint, blocked, true)
		})
	}
}

func TestGeminiCompatibilityAncillaryUsageBeforeValidTerminalDoesNotReplay(t *testing.T) {
	body := []byte("data: {\"usageMetadata\":{\"promptTokenCount\":8},\"modelVersion\":\"gemini-provider\"}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"accepted-first\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n")
	for _, endpoint := range []string{"messages", "chat_completions"} {
		t.Run(endpoint, func(t *testing.T) {
			testGeminiCompatibilityPresemanticFailover(t, endpoint, body, true)
		})
	}
}

func TestGeminiCompatibilityStreamsEmptyFunctionCallFailsOverWithoutCaptureOrBilling(t *testing.T) {
	firstBody := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{}}]}}]}\n\n")
	for _, endpoint := range []string{"messages", "chat_completions"} {
		t.Run(endpoint, func(t *testing.T) {
			testGeminiCompatibilityPresemanticFailover(t, endpoint, firstBody)
		})
	}
}

func TestGeminiCompatibilityStreamsRejectMalformedProviderPayloadsBeforeCommit(t *testing.T) {
	firstBodies := map[string][]byte{
		"empty_candidates":           []byte("data: {\"candidates\":[]}\n\ndata: [DONE]\n\n"),
		"empty_candidate":            []byte("data: {\"candidates\":[{}]}\n\ndata: [DONE]\n\n"),
		"finish_only":                []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"usage_only":                 []byte("data: {\"usageMetadata\":{\"promptTokenCount\":8}}\n\ndata: [DONE]\n\n"),
		"total_tokens_only":          []byte("data: {\"totalTokens\":8}\n\ndata: [DONE]\n\n"),
		"nonstring_finish_reason":    []byte("data: {\"candidates\":[{\"finishReason\":123}]}\n\ndata: [DONE]\n\n"),
		"malformed_first_candidate":  []byte("data: {\"candidates\":[{}, {\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"mixed_duplicate_index":      []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}},{\"index\":0,\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"nonstring_part_text":        []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":123}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"nonstring_function_name":    []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":123,\"args\":{}}}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"oversized_function_name":    []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"" + strings.Repeat("x", 1025) + "\",\"args\":{}}}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"scalar_candidate":           []byte("data: {\"candidates\":[123]}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_feedback":    []byte("data: {\"promptFeedback\":{\"foo\":\"bar\"}}\n\ndata: [DONE]\n\n"),
		"invalid_prompt_rating":      []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\",\"safetyRatings\":[123]}}\n\n"),
		"invalid_candidate_ratings":  []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"safetyRatings\":\"bad\"}]}\n\n"),
		"invalid_grounding_metadata": []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"groundingMetadata\":{\"groundingChunks\":[123]}}]}\n\n"),
		"invalid_finish_message":     []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\",\"finishMessage\":123}]}\n\n"),
		"blocked_with_candidate":     []byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"},\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"first-leak\"}]}}]}\n\ndata: [DONE]\n\n"),
		"nonstring_model_ancillary":  []byte("data: {\"modelVersion\":123}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"invalid_usage_ancillary":    []byte("data: {\"usageMetadata\":{\"promptTokenCount\":\"bad\"}}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"invalid_usage_details":      []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":-7}]}}\n\n"),
		"cached_exceeds_prompt":      []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"cachedContentTokenCount\":3}}\n\n"),
		"image_exceeds_output":       []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":2,\"candidatesTokensDetails\":[{\"modality\":\"IMAGE\",\"tokenCount\":3}]}}\n\n"),
		"malformed_then_valid": []byte("data: {not-json}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"empty_candidates_then_valid": []byte("data: {\"candidates\":[]}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: [DONE]\n\n"),
		"done_then_valid": []byte("data: [DONE]\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first-leak\"}]},\"finishReason\":\"STOP\"}]}\n\n"),
		"too_many_candidates": []byte("data: {\"candidates\":[" +
			strings.Repeat(`{"content":{"role":"model","parts":[{"text":""}]}},`, 1024) +
			`{"content":{"role":"model","parts":[{"text":""}]}}]}` + "\n\n"),
	}
	for _, endpoint := range []string{"messages", "chat_completions"} {
		for name, firstBody := range firstBodies {
			t.Run(endpoint+"/"+name, func(t *testing.T) {
				testGeminiCompatibilityPresemanticFailover(t, endpoint, firstBody)
			})
		}
	}
}

func TestGeminiCompatibilityBufferedResponsesRequireApplicationTerminal(t *testing.T) {
	firstBodies := map[string][]byte{
		"content_without_finish":  []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`),
		"malformed_sibling":       []byte(`{"modelVersion":123,"candidates":[{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP"}]}`),
		"blocked_with_candidate":  []byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"index":0,"content":{"parts":[{"text":"first-leak"}]}}]}`),
		"invalid_grounding":       []byte(`{"candidates":[{"content":{"parts":[{"text":"first-leak"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":[123]}}]}`),
		"oversized_function_name": []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"` + strings.Repeat("x", 1025) + `","args":{}}}]},"finishReason":"STOP"}]}`),
	}
	for _, endpoint := range []string{"messages_buffered", "chat_completions_buffered"} {
		for name, firstBody := range firstBodies {
			t.Run(endpoint+"/"+name, func(t *testing.T) {
				testGeminiCompatibilityPresemanticFailover(t, endpoint, firstBody)
			})
		}
	}
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
	gemini := service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: group}, nil, scheduler, nil, nil, upstream, nil, cfg)
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
