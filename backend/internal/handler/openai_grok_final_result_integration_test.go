//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokFinalHandlerScheduleReport struct {
	accountID int64
	success   bool
}

type grokFinalHandlerScheduler struct {
	account *service.Account

	mu       sync.Mutex
	reports  []grokFinalHandlerScheduleReport
	switches int
}

func (s *grokFinalHandlerScheduler) Select(_ context.Context, req service.OpenAIAccountScheduleRequest) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
	if s == nil || s.account == nil {
		return nil, service.OpenAIAccountScheduleDecision{}, service.ErrNoAvailableAccounts
	}
	if _, excluded := req.ExcludedIDs[s.account.ID]; excluded {
		return nil, service.OpenAIAccountScheduleDecision{}, service.ErrNoAvailableAccounts
	}
	return &service.AccountSelectionResult{
		Account:     s.account,
		Acquired:    true,
		ReleaseFunc: func() {},
	}, service.OpenAIAccountScheduleDecision{SelectedAccountID: s.account.ID}, nil
}

func (s *grokFinalHandlerScheduler) ReportResult(accountID int64, success bool, _ *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, grokFinalHandlerScheduleReport{accountID: accountID, success: success})
}

func (s *grokFinalHandlerScheduler) ReportSwitch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switches++
}

func (*grokFinalHandlerScheduler) SnapshotMetrics() service.OpenAIAccountSchedulerMetricsSnapshot {
	return service.OpenAIAccountSchedulerMetricsSnapshot{}
}

func (s *grokFinalHandlerScheduler) snapshot() ([]grokFinalHandlerScheduleReport, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]grokFinalHandlerScheduleReport(nil), s.reports...), s.switches
}

type grokFinalHandlerUpstream struct {
	mu        sync.Mutex
	responses []*http.Response
	err       error
	bodies    [][]byte
}

func (u *grokFinalHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.bodies = append(u.bodies, append([]byte(nil), body...))
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) == 0 {
		return nil, errors.New("unexpected Grok upstream call")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	resp.Request = req
	return resp, nil
}

func (u *grokFinalHandlerUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *grokFinalHandlerUpstream) requestBodies() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([][]byte, len(u.bodies))
	for i := range u.bodies {
		out[i] = append([]byte(nil), u.bodies[i]...)
	}
	return out
}

type grokFinalHandlerRun struct {
	recorder *httptest.ResponseRecorder
	reports  []grokFinalHandlerScheduleReport
	switches int
	captures []*service.CaptureRecord
	bodies   [][]byte
	usage    <-chan *service.UsageLog
}

type grokFinalHandlerUsageRepo struct {
	service.UsageLogRepository
	records chan<- *service.UsageLog
}

func (r *grokFinalHandlerUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	if r != nil && r.records != nil {
		r.records <- log
	}
	return true, nil
}

func grokFinalHandlerResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func runGrokFinalHandler(t *testing.T, requestBody string, upstream *grokFinalHandlerUpstream) grokFinalHandlerRun {
	t.Helper()
	gin.SetMode(gin.TestMode)

	groupID := int64(9400)
	account := &service.Account{
		ID: 9401, Name: "grok-final", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.x.ai/v1"},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)

	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billingCache.Stop()
	captureRecords := make(chan *service.CaptureRecord, 8)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	usageRecords := make(chan *service.UsageLog, 8)
	usageRepo := &grokFinalHandlerUsageRepo{records: usageRecords}
	scheduler := &grokFinalHandlerScheduler{account: account}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{*account}}, usageRepo, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, settingService, nil, capturePool,
	)
	resetScheduler := gateway.InstallOpenAIAccountSchedulerForUnitTest(scheduler)
	defer resetScheduler()
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx := service.WithOpenAICompatiblePlatform(context.Background(), service.PlatformGrok)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(requestBody)).WithContext(reqCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9402, GroupID: &groupID, User: &service.User{ID: 9403, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9403, Concurrency: 0})

	h.Responses(c)
	capturePool.Stop()

	var captures []*service.CaptureRecord
	for {
		select {
		case capture := <-captureRecords:
			captures = append(captures, capture)
		default:
			reports, switches := scheduler.snapshot()
			return grokFinalHandlerRun{
				recorder: recorder,
				reports:  reports,
				switches: switches,
				captures: captures,
				bodies:   upstream.requestBodies(),
				usage:    usageRecords,
			}
		}
	}
}

func TestGrokFinalResultsDriveRealHandlerSchedulingAndCapture(t *testing.T) {
	successBody := `{"id":"resp-ok","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
	tests := []struct {
		name              string
		requestBody       string
		upstream          *grokFinalHandlerUpstream
		wantSuccess       bool
		wantCapture       bool
		wantCaptureStatus int
		wantStatus        int
		wantCalls         int
		wantReports       int
		wantCyberUsage    bool
	}{
		{
			name:        "final HTTP 400",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusBadRequest, `{"error":{"message":"invalid request"}}`),
			}},
			wantSuccess: false, wantCapture: true, wantCaptureStatus: http.StatusBadRequest, wantStatus: http.StatusBadGateway, wantCalls: 1, wantReports: 1,
		},
		{
			name:        "final cyber error remains exact once",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusBadRequest, `{"error":{"code":"cyber_policy","message":"blocked"}}`),
			}},
			wantSuccess: false, wantCapture: true, wantCaptureStatus: http.StatusBadRequest, wantStatus: http.StatusBadRequest, wantCalls: 1, wantReports: 1, wantCyberUsage: true,
		},
		{
			name:        "final 2xx parse failure",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusOK, `{"id":"broken"`),
			}},
			wantSuccess: false, wantCapture: true, wantCaptureStatus: http.StatusOK, wantStatus: http.StatusBadGateway, wantCalls: 1, wantReports: 1,
		},
		{
			name:        "success",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusOK, successBody),
			}},
			wantSuccess: true, wantCapture: true, wantCaptureStatus: http.StatusOK, wantStatus: http.StatusOK, wantCalls: 1, wantReports: 1,
		},
		{
			name:        "retryable final response",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusServiceUnavailable, `{"error":{"message":"temporary unavailable"}}`),
			}},
			wantSuccess: false, wantCapture: true, wantCaptureStatus: http.StatusServiceUnavailable, wantStatus: http.StatusBadGateway, wantCalls: 1, wantReports: -1,
		},
		{
			name:        "transport failure",
			requestBody: `{"model":"grok","input":"hi","stream":false}`,
			upstream:    &grokFinalHandlerUpstream{err: errors.New("transport down")},
			wantSuccess: false, wantCapture: false, wantStatus: http.StatusBadGateway, wantCalls: 1, wantReports: -1,
		},
		{
			name:        "encrypted content retry captures only final attempt",
			requestBody: `{"model":"grok","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"keep"}],"encrypted_content":"cipher"},{"type":"message","role":"user","content":"hi"}],"stream":false}`,
			upstream: &grokFinalHandlerUpstream{responses: []*http.Response{
				grokFinalHandlerResponse(http.StatusBadRequest, `{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}`),
				grokFinalHandlerResponse(http.StatusOK, successBody),
			}},
			wantSuccess: true, wantCapture: true, wantCaptureStatus: http.StatusOK, wantStatus: http.StatusOK, wantCalls: 2, wantReports: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runGrokFinalHandler(t, tt.requestBody, tt.upstream)
			if tt.wantReports >= 0 {
				require.Len(t, got.reports, tt.wantReports, "the final handler outcome must be reported exactly once")
				require.Equal(t, int64(9401), got.reports[0].accountID)
				require.Equal(t, tt.wantSuccess, got.reports[0].success)
			} else {
				for _, report := range got.reports {
					require.False(t, report.success, "retryable/transport attempts must never be reported as success")
				}
			}
			require.Equal(t, tt.wantCalls, len(got.bodies))
			require.Equal(t, tt.wantStatus, got.recorder.Code)

			if !tt.wantCapture {
				require.Empty(t, got.captures, "retryable/transport results must not create a final capture")
				return
			}
			require.Len(t, got.captures, 1, "only the final attempt may be captured")
			capture := got.captures[0]
			require.Equal(t, tt.wantCaptureStatus, capture.HTTPStatus)
			require.Equal(t, got.bodies[len(got.bodies)-1], capture.RawRequest)
			require.Equal(t, service.HashUsageRequestPayload(got.bodies[len(got.bodies)-1]), hashFinalOpenAIUpstreamRequest(&service.OpenAIForwardResult{UpstreamRequest: capture.RawRequest}, []byte(tt.requestBody)))
			if tt.wantCalls == 2 {
				require.Contains(t, string(got.bodies[0]), "encrypted_content")
				require.NotContains(t, string(capture.RawRequest), "encrypted_content")
			}
			if tt.wantCyberUsage {
				select {
				case usage := <-got.usage:
					require.Equal(t, service.RequestTypeCyberBlocked, usage.RequestType)
				case <-time.After(time.Second):
					t.Fatal("cyber usage record was not submitted")
				}
				select {
				case duplicate := <-got.usage:
					t.Fatalf("cyber result produced duplicate usage record: request_type=%s", duplicate.RequestType.String())
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}
