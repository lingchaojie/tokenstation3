//go:build unit

package handler

import (
	"bytes"
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

type geminiNativeCaptureUpstream struct {
	mu       sync.Mutex
	lastBody []byte
	status   int
	response []byte
}

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
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"rid-gemini-native"}},
		Body:       io.NopCloser(bytes.NewReader(u.response)),
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
		upstreamStatus     int
		upstreamResponse   []byte
		expectedHTTPStatus int
	}{
		{
			name:               "success",
			upstreamStatus:     http.StatusOK,
			upstreamResponse:   []byte(`{"candidates":[{"content":{"parts":[{"text":"Done."}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3},"modelVersion":"gemini-test-upstream"}`),
			expectedHTTPStatus: http.StatusOK,
		},
		{
			name:               "terminal_provider_error",
			upstreamStatus:     http.StatusUnauthorized,
			upstreamResponse:   []byte(`{"error":{"code":401,"message":"` + strings.Repeat("x", 600<<10) + `"}}`),
			expectedHTTPStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			upstream := &geminiNativeCaptureUpstream{status: tt.upstreamStatus, response: tt.upstreamResponse}
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
			captureRecords := make(chan *service.CaptureRecord, 4)
			capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
			gateway := service.NewGatewayService(
				nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, scheduler, nil,
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
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent", bytes.NewReader(requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-test:generateContent"}}
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 9733, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
				Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

			h.GeminiV1BetaModels(c)
			capturePool.Stop()
			require.Equal(t, tt.expectedHTTPStatus, recorder.Code)
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
