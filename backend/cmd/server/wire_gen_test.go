package main

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProvideServiceBuildInfo(t *testing.T) {
	in := handler.BuildInfo{
		Version:   "v-test",
		BuildType: "release",
	}
	out := provideServiceBuildInfo(in)
	require.Equal(t, in.Version, out.Version)
	require.Equal(t, in.BuildType, out.BuildType)
}

func TestProvideCleanup_WithMinimalDependencies_NoPanic(t *testing.T) {
	cleanup := provideCleanupWithMinimalDependenciesForTest(t, nil, nil)
	require.NotPanics(t, cleanup)
}

// Removing the concrete supervisor cleanup step leaves the child lifecycle
// active after application cleanup. Repeating Stop afterward also proves the
// supervisor path remains idempotent.
func TestProvideCleanupStopsCaptureSidecarSupervisor(t *testing.T) {
	supervisor := &service.CaptureSidecarSupervisor{}
	cleanup := provideCleanupWithMinimalDependenciesForTest(t, supervisor, nil)
	require.NotPanics(t, cleanup)
	require.True(t, reflect.ValueOf(supervisor).Elem().FieldByName("stopping").Bool())
	require.NotPanics(t, func() {
		supervisor.Stop()
		supervisor.Stop()
	})
}

func TestApplicationOwnsCursorObservedModelsLifecycle(t *testing.T) {
	repo := &cursorObservedModelsLifecycleRepo{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	upstream := &cursorObservedModelsNoCallUpstream{}
	observedModels := service.NewCursorObservedModelsService(repo, nil, upstream, 6*time.Hour)
	app := &Application{
		CursorObservedModels: observedModels,
		Cleanup:              provideCleanupWithMinimalDependenciesForTest(t, nil, observedModels),
	}

	startReturned := make(chan struct{})
	go func() {
		app.Start()
		close(startReturned)
	}()
	requireChannelClosed(t, startReturned, "Application.Start blocked on the initial refresh")
	requireChannelClosed(t, repo.started, "initial observed-model refresh did not start")

	app.Start()
	require.Never(t, func() bool { return repo.calls.Load() != 1 }, 25*time.Millisecond, time.Millisecond,
		"Application.Start launched the initial refresh loop more than once")
	require.Zero(t, upstream.calls.Load(), "empty fake account results must not make an upstream call")

	cleanupReturned := make(chan struct{})
	go func() {
		app.Cleanup()
		close(cleanupReturned)
	}()
	requireChannelClosed(t, repo.canceled, "cleanup did not cancel the observed-model refresh")
	requireChannelClosed(t, cleanupReturned, "cleanup did not join the observed-model refresh")
}

func provideCleanupWithMinimalDependenciesForTest(
	t *testing.T,
	captureSidecarSupervisor *service.CaptureSidecarSupervisor,
	cursorObservedModels *service.CursorObservedModelsService,
) func() {
	t.Helper()
	cfg := &config.Config{}

	oauthSvc := service.NewOAuthService(nil, nil)
	openAIOAuthSvc := service.NewOpenAIOAuthService(nil, nil)
	geminiOAuthSvc := service.NewGeminiOAuthService(nil, nil, nil, nil, cfg)
	antigravityOAuthSvc := service.NewAntigravityOAuthService(nil)
	kiroOAuthSvc := service.NewKiroOAuthService(nil)

	tokenRefreshSvc := service.NewTokenRefreshService(
		nil,
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		kiroOAuthSvc,
		nil,
		nil,
		cfg,
		nil,
	)
	accountExpirySvc := service.NewAccountExpiryService(nil, time.Second)
	codexVersionSyncSvc := service.NewOpenAICodexVersionSyncService(nil, nil, nil, time.Second)
	proxyExpirySvc := service.NewProxyExpiryService(nil, time.Second)
	subscriptionExpirySvc := service.NewSubscriptionExpiryService(nil, time.Second)
	pricingSvc := service.NewPricingService(cfg, nil)
	emailQueueSvc := service.NewEmailQueueService(nil, 1)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	idempotencyCleanupSvc := service.NewIdempotencyCleanupService(nil, cfg)
	schedulerSnapshotSvc := service.NewSchedulerSnapshotService(nil, nil, nil, nil, cfg)
	opsSystemLogSinkSvc := service.NewOpsSystemLogSink(nil)
	rewardCreditExpirySvc := service.NewRewardCreditExpiryService(nil, nil, nil)

	cleanup := provideCleanup(
		nil, // entClient
		nil, // redis
		&service.OpsMetricsCollector{},
		&service.OpsAggregationService{},
		&service.OpsAlertEvaluatorService{},
		&service.OpsCleanupService{},
		&service.OpsScheduledReportService{},
		opsSystemLogSinkSvc,
		nil, // opsService
		nil, // opsIngressRejectAggregator
		nil, // apiKeyService
		nil, // authCacheInvalidationWorker
		schedulerSnapshotSvc,
		tokenRefreshSvc,
		accountExpirySvc,
		nil, // cnProviderBalanceCheck
		codexVersionSyncSvc,
		proxyExpirySvc,
		subscriptionExpirySvc,
		&service.UsageCleanupService{},
		idempotencyCleanupSvc,
		&service.BatchImageCleanupService{},
		nil, // batchImageWorker
		pricingSvc,
		emailQueueSvc,
		billingCacheSvc,
		&service.UsageRecordWorkerPool{},
		nil, // conversationCapturePool
		captureSidecarSupervisor,
		&service.SubscriptionService{},
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		kiroOAuthSvc,
		nil, // grokOAuth
		nil, // openAIGateway
		nil, // scheduledTestRunner
		nil, // backupSvc
		nil, // paymentOrderExpiry
		nil, // channelMonitorRunner
		nil, // channelMonitorV2Aggregator
		nil, // quotaFlusher
		rewardCreditExpirySvc,
		nil, // upstreamBillingProbe
		nil, // ollamaCloudUsage
		nil, // auditLog
		cursorObservedModels,
	)

	return cleanup
}

func requireChannelClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

type cursorObservedModelsLifecycleRepo struct {
	service.AccountRepository

	calls        atomic.Int64
	started      chan struct{}
	canceled     chan struct{}
	startedOnce  sync.Once
	canceledOnce sync.Once
}

func (r *cursorObservedModelsLifecycleRepo) ListSchedulableByPlatform(ctx context.Context, _ string) ([]service.Account, error) {
	r.calls.Add(1)
	r.startedOnce.Do(func() { close(r.started) })
	<-ctx.Done()
	r.canceledOnce.Do(func() { close(r.canceled) })
	return nil, ctx.Err()
}

type cursorObservedModelsNoCallUpstream struct {
	calls atomic.Int64
}

func (u *cursorObservedModelsNoCallUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls.Add(1)
	return nil, context.Canceled
}

func (u *cursorObservedModelsNoCallUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	accountID int64,
	concurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}
