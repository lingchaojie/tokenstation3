package repository

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
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type lifecycleUsageLogRepo struct {
	service.UsageLogRepository
	logs []*service.UsageLog
}

func (r *lifecycleUsageLogRepo) Create(ctx context.Context, log *service.UsageLog) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.logs = append(r.logs, log)
	return true, nil
}

type lifecycleBillingRepo struct {
	service.UsageBillingRepository
	commands []*service.UsageBillingCommand
}

func (r *lifecycleBillingRepo) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.commands = append(r.commands, cmd)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type lifecycleDisconnectedWriter struct {
	gin.ResponseWriter
	disconnect context.CancelFunc
}

func (w *lifecycleDisconnectedWriter) Write([]byte) (int, error) {
	w.disconnect()
	return 0, errors.New("client disconnected")
}

// Exercise the real forwarding and RecordUsage services with the real HTTP
// upstream. Only the persistence boundary is replaced: closing an attempt must
// not cancel the detached stream before its final usage reaches billing.
func TestHTTPUpstreamForwardDrainsUsageAfterClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	clientCtx, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	releaseUsage := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUsage) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "lifecycle-billing")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-releaseUsage:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lifecycle\",\"status\":\"completed\",\"usage\":{\"input_tokens\":17,\"output_tokens\":8,\"total_tokens\":25}}}\n\n")
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	defer release()

	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.StreamKeepaliveInterval = 1
	upstream := NewHTTPUpstream(cfg)
	usageRepo := &lifecycleUsageLogRepo{}
	billingRepo := &lifecycleBillingRepo{}
	svc := service.NewOpenAIGatewayService(
		nil, usageRepo, billingRepo, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, nil, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	account := &service.Account{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": srv.URL, "api_key": "test-key"},
	}
	body := []byte(`{"model":"gpt-5.1","stream":true,"input":"hello"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(clientCtx)
	c.Writer = &lifecycleDisconnectedWriter{ResponseWriter: c.Writer, disconnect: disconnect}

	type forwardResult struct {
		result *service.OpenAIForwardResult
		err    error
	}
	done := make(chan forwardResult, 1)
	go func() {
		result, err := svc.Forward(clientCtx, c, account, body)
		done <- forwardResult{result, err}
	}()
	select {
	case <-clientCtx.Done():
	case got := <-done:
		t.Fatalf("forward returned before downstream disconnect: %v", got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("downstream did not receive the first event")
	}
	release()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 17, got.result.Usage.InputTokens)
		require.Equal(t, 8, got.result.Usage.OutputTokens)
		// RecordUsage must still work with the cancelled downstream context.
		require.NoError(t, svc.RecordUsage(clientCtx, &service.OpenAIRecordUsageInput{
			Result: got.result, APIKey: &service.APIKey{ID: 2},
			User: &service.User{ID: 3}, Account: account,
		}))
	case <-time.After(5 * time.Second):
		t.Fatal("forward did not finish draining usage")
	}
	require.Len(t, billingRepo.commands, 1)
	require.Len(t, usageRepo.logs, 1)
	require.Equal(t, 17, usageRepo.logs[0].InputTokens)
	require.Equal(t, 8, usageRepo.logs[0].OutputTokens)
	require.Greater(t, usageRepo.logs[0].ActualCost, 0.0)
	s, ok := upstream.(*httpUpstreamService)
	require.True(t, ok)
	requireNoUpstreamInFlight(t, s)
	for _, entry := range s.clients {
		entry.client.CloseIdleConnections()
	}
}

func TestHTTPUpstreamForwardDisconnectBudgetStopsActiveProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	providerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(providerDone)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_ = http.NewResponseController(w).Flush()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = io.WriteString(w, ": ping\n\n")
				_ = http.NewResponseController(w).Flush()
			}
		}
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.StreamDataIntervalTimeout = 30
	upstream, ok := NewHTTPUpstream(cfg).(*httpUpstreamService)
	require.True(t, ok)
	require.Zero(t, upstream.upstreamDrainTimeout(), "a configured SSE idle interval must not enable a total drain cap")
	cfg.Gateway.UpstreamDrainTimeoutSeconds = 1
	svc := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, nil, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	account := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"base_url": srv.URL, "api_key": "test-key"},
	}
	body := []byte(`{"model":"gpt-5.1","stream":true,"input":"hello"}`)
	clientCtx, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(clientCtx)
	c.Writer = &lifecycleDisconnectedWriter{ResponseWriter: c.Writer, disconnect: disconnect}
	forwardDone := make(chan error, 1)
	go func() {
		_, err := svc.Forward(clientCtx, c, account, body)
		forwardDone <- err
	}()
	select {
	case <-clientCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("downstream did not receive the first event")
	}
	select {
	case <-providerDone:
		t.Fatal("provider was canceled before the drain interval")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-providerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("continuous provider heartbeats outlived the disconnect budget")
	}
	select {
	case err := <-forwardDone:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("forward did not release the canceled attempt")
	}
	requireNoUpstreamInFlight(t, upstream)
}
