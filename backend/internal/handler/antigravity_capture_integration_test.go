//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type antigravityCaptureTokenCache struct{}

func (*antigravityCaptureTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "antigravity-provider-secret", nil
}
func (*antigravityCaptureTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (*antigravityCaptureTokenCache) DeleteAccessToken(context.Context, string) error { return nil }
func (*antigravityCaptureTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (*antigravityCaptureTokenCache) ReleaseRefreshLock(context.Context, string) error { return nil }

type antigravityCaptureUpstream struct {
	mu       sync.Mutex
	requests [][]byte
	body     []byte
}

func (u *antigravityCaptureUpstream) responseFor(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requests = append(u.requests, append([]byte(nil), body...))
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {"rid-antigravity-terminal"},
		},
		Body:    io.NopCloser(bytes.NewReader(u.body)),
		Request: req,
	}, nil
}

func (u *antigravityCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.responseFor(req)
}

func (u *antigravityCaptureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.responseFor(req)
}

func TestAntigravityCompatibilityRouterArchivesTerminalProviderAttemptExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		groupID   = int64(9780)
		accountID = int64(9781)
		userID    = int64(9782)
	)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAntigravity, Status: service.StatusActive, RateMultiplier: 1}
	account := &service.Account{
		ID: accountID, Name: "antigravity-terminal", Platform: service.PlatformAntigravity,
		Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{
			"access_token": "stale-antigravity-secret", "project_id": "project-capture",
			"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	errorBody := []byte(`{"error":{"code":401,"message":"invalid antigravity bearer"}}`)
	upstream := &antigravityCaptureUpstream{body: errorBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
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
	tokenProvider := service.NewAntigravityTokenProvider(nil, &antigravityCaptureTokenCache{}, nil)
	antigravityService := service.NewAntigravityGatewayService(nil, nil, scheduler, tokenProvider, nil, upstream, settingService, nil)
	h := NewGatewayHandler(
		gateway, nil, nil, antigravityService, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settingService, capturePool,
	)

	requestBody := []byte(`{"model":"claude-sonnet-4-5","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9783, UserID: userID, GroupID: func() *int64 { id := groupID; return &id }(), Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})

	h.ChatCompletions(c)
	capturePool.Stop()
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Len(t, captureRecords, 1, "the terminal Antigravity provider attempt must be archived once")
	archived := <-captureRecords
	upstream.mu.Lock()
	require.NotEmpty(t, upstream.requests)
	finalRequest := append([]byte(nil), upstream.requests[len(upstream.requests)-1]...)
	upstream.mu.Unlock()
	require.Equal(t, finalRequest, archived.RawRequest)
	require.Equal(t, errorBody, archived.RawResponse)
	require.Equal(t, http.StatusUnauthorized, archived.HTTPStatus)
	require.Equal(t, service.PlatformAntigravity, archived.Platform)
	require.NotContains(t, string(archived.RequestHeaders), "antigravity-provider-secret")
}
