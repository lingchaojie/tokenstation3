//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type opsCaptureHealthRepoStub struct {
	events            []CaptureHealthEvent
	latest            []CaptureHealthEvent
	err               error
	start             time.Time
	end               time.Time
	latestBefore      time.Time
	latestInstanceIDs []string
	latestReasons     []string
	latestCalls       int
}

func (r *opsCaptureHealthRepoStub) UpsertEvents(context.Context, []CaptureHealthEvent) error {
	return nil
}

func (r *opsCaptureHealthRepoStub) ListEvents(_ context.Context, start, end time.Time) ([]CaptureHealthEvent, error) {
	r.start = start
	r.end = end
	return append([]CaptureHealthEvent(nil), r.events...), r.err
}

func (r *opsCaptureHealthRepoStub) ListLatestEventsBefore(_ context.Context, before time.Time, instanceIDs, reasons []string) ([]CaptureHealthEvent, error) {
	r.latestCalls++
	r.latestBefore = before
	r.latestInstanceIDs = append([]string(nil), instanceIDs...)
	r.latestReasons = append([]string(nil), reasons...)
	return append([]CaptureHealthEvent(nil), r.latest...), r.err
}

func TestComputeRuleMetricCaptureDroppedRecordsBoundsBaselineSources(t *testing.T) {
	start := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	repo := &opsCaptureHealthRepoStub{}
	for i := 0; i <= captureHealthBaselineSourceLimit; i++ {
		repo.events = append(repo.events, CaptureHealthEvent{
			MinuteBucket: start, InstanceID: fmt.Sprintf("sidecar-%03d", i),
			Reason: string(CaptureDropSpoolCap), DroppedRecords: 1,
		})
	}
	svc := &OpsAlertEvaluatorService{opsRepo: &stubOpsRepo{}, captureHealthRepo: repo}

	_, ok := svc.computeRuleMetric(
		context.Background(), &OpsAlertRule{MetricType: "capture_dropped_records"}, nil,
		start, start.Add(time.Minute), "", nil,
	)

	require.False(t, ok)
	require.Zero(t, repo.latestCalls, "oversized source sets must not reach the baseline query")
}

func (r *opsCaptureHealthRepoStub) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newOpsCaptureMetricPool(t *testing.T, status archiveWriterStatus) *ConversationCapturePool {
	t.Helper()
	pool := newConversationCapturePoolWithStatus(
		conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 1, WriterQueueSize: 1},
		noopArchiveWriter{},
		status,
		newCaptureHealthTracker("ops-test", time.Now),
		nil,
	)
	t.Cleanup(pool.Stop)
	return pool
}

var _ OpsRepository = (*stubOpsRepo)(nil)

type stubOpsRepo struct {
	OpsRepository
	overview *OpsDashboardOverview
	err      error
}

type opsAlertEvaluatorRepoStub struct {
	OpsRepository
	rules             []*OpsAlertRule
	activeEvent       *OpsAlertEvent
	resolveErr        error
	resolutionApplied bool
	resolvedStatus    string
	resolvedAt        *time.Time
	heartbeatResult   string
}

func (s *opsAlertEvaluatorRepoStub) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	return s.rules, nil
}

func (s *opsAlertEvaluatorRepoStub) GetLatestSystemMetrics(context.Context, int) (*OpsSystemMetricsSnapshot, error) {
	return &OpsSystemMetricsSnapshot{}, nil
}

func (s *opsAlertEvaluatorRepoStub) GetActiveAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	return s.activeEvent, nil
}

func (s *opsAlertEvaluatorRepoStub) UpdateAlertEventStatus(_ context.Context, _ int64, status string, resolvedAt *time.Time) error {
	if s.resolveErr != nil {
		return s.resolveErr
	}
	s.resolvedStatus = status
	if resolvedAt != nil {
		copied := *resolvedAt
		s.resolvedAt = &copied
		s.resolutionApplied = true
	}
	return nil
}

func (s *opsAlertEvaluatorRepoStub) UpsertJobHeartbeat(_ context.Context, input *OpsUpsertJobHeartbeatInput) error {
	if input != nil && input.LastResult != nil {
		s.heartbeatResult = *input.LastResult
	}
	return nil
}

func (s *stubOpsRepo) GetDashboardOverview(ctx context.Context, filter *OpsDashboardFilter) (*OpsDashboardOverview, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.overview != nil {
		return s.overview, nil
	}
	return &OpsDashboardOverview{}, nil
}

func TestComputeGroupAvailableRatio(t *testing.T) {
	t.Parallel()

	t.Run("正常情况: 10个账号, 8个可用 = 80%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  10,
			AvailableCount: 8,
		})
		require.InDelta(t, 80.0, got, 0.0001)
	})

	t.Run("边界情况: TotalAccounts = 0 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  0,
			AvailableCount: 8,
		})
		require.Equal(t, 0.0, got)
	})

	t.Run("边界情况: AvailableCount = 0 应返回 0%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  10,
			AvailableCount: 0,
		})
		require.Equal(t, 0.0, got)
	})
}

func TestCountAccountsByCondition(t *testing.T) {
	t.Parallel()

	t.Run("测试限流账号统计: acc.IsRateLimited", func(t *testing.T) {
		t.Parallel()

		accounts := map[int64]*AccountAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: false},
			3: {IsRateLimited: true},
		}

		got := countAccountsByCondition(accounts, func(acc *AccountAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(2), got)
	})

	t.Run("测试错误账号统计（排除临时不可调度）: acc.HasError && acc.TempUnschedulableUntil == nil", func(t *testing.T) {
		t.Parallel()

		until := time.Now().UTC().Add(5 * time.Minute)
		accounts := map[int64]*AccountAvailability{
			1: {HasError: true},
			2: {HasError: true, TempUnschedulableUntil: &until},
			3: {HasError: false},
		}

		got := countAccountsByCondition(accounts, func(acc *AccountAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})
		require.Equal(t, int64(1), got)
	})

	t.Run("边界情况: 空 map 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := countAccountsByCondition(map[int64]*AccountAvailability{}, func(acc *AccountAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(0), got)
	})
}

// TestComputeRuleMetric_AccountTempUnscheduledCount verifies the new
// account_temp_unscheduled_count metric counts accounts currently in the
// temp-unscheduled window and ignores those whose window has expired or
// were never temp-unscheduled.
func TestComputeRuleMetric_AccountTempUnscheduledCount(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	futureUntil := now.Add(5 * time.Minute)
	pastUntil := now.Add(-1 * time.Minute)

	availability := &OpsAccountAvailability{
		Accounts: map[int64]*AccountAvailability{
			// currently temp-unscheduled (window active)
			1: {TempUnschedulableUntil: &futureUntil},
			2: {TempUnschedulableUntil: &futureUntil},
			// temp-unsched window already expired → should NOT count
			3: {TempUnschedulableUntil: &pastUntil},
			// never temp-unscheduled
			4: {HasError: true},
			5: {IsRateLimited: true},
		},
	}

	opsService := &OpsService{
		getAccountAvailability: func(_ context.Context, _ string, _ *int64) (*OpsAccountAvailability, error) {
			return availability, nil
		},
	}
	svc := &OpsAlertEvaluatorService{
		opsService: opsService,
		opsRepo:    &stubOpsRepo{},
	}

	rule := &OpsAlertRule{MetricType: "account_temp_unscheduled_count"}
	val, ok := svc.computeRuleMetric(context.Background(), rule, nil,
		now.Add(-5*time.Minute), now, "", nil)

	require.True(t, ok)
	require.InDelta(t, 2.0, val, 0.0001, "only 2 accounts have an active temp-unsched window")
}

func TestComputeRuleMetricNewIndicators(t *testing.T) {
	t.Parallel()

	groupID := int64(101)
	platform := "openai"

	availability := &OpsAccountAvailability{
		Group: &GroupAvailability{
			GroupID:        groupID,
			TotalAccounts:  10,
			AvailableCount: 8,
		},
		Accounts: map[int64]*AccountAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: true},
			3: {HasError: true},
			4: {HasError: true, TempUnschedulableUntil: timePtr(time.Now().UTC().Add(2 * time.Minute))},
			5: {HasError: false, IsRateLimited: false},
		},
	}

	opsService := &OpsService{
		getAccountAvailability: func(_ context.Context, _ string, _ *int64) (*OpsAccountAvailability, error) {
			return availability, nil
		},
	}

	svc := &OpsAlertEvaluatorService{
		opsService: opsService,
		opsRepo:    &stubOpsRepo{overview: &OpsDashboardOverview{}},
	}

	start := time.Now().UTC().Add(-5 * time.Minute)
	end := time.Now().UTC()
	ctx := context.Background()

	tests := []struct {
		name       string
		metricType string
		groupID    *int64
		wantValue  float64
		wantOK     bool
	}{
		{
			name:       "group_available_accounts",
			metricType: "group_available_accounts",
			groupID:    &groupID,
			wantValue:  8,
			wantOK:     true,
		},
		{
			name:       "group_available_ratio",
			metricType: "group_available_ratio",
			groupID:    &groupID,
			wantValue:  80.0,
			wantOK:     true,
		},
		{
			name:       "account_rate_limited_count",
			metricType: "account_rate_limited_count",
			groupID:    nil,
			wantValue:  2,
			wantOK:     true,
		},
		{
			name:       "account_error_count",
			metricType: "account_error_count",
			groupID:    nil,
			wantValue:  1,
			wantOK:     true,
		},
		{
			name:       "group_available_accounts without group_id returns false",
			metricType: "group_available_accounts",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
		{
			name:       "group_available_ratio without group_id returns false",
			metricType: "group_available_ratio",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := &OpsAlertRule{
				MetricType: tt.metricType,
			}
			gotValue, gotOK := svc.computeRuleMetric(ctx, rule, nil, start, end, platform, tt.groupID)
			require.Equal(t, tt.wantOK, gotOK)
			if !tt.wantOK {
				return
			}
			require.InDelta(t, tt.wantValue, gotValue, 0.0001)
		})
	}
}

func TestComputeRuleMetricCaptureReady(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	rule := &OpsAlertRule{MetricType: "capture_ready"}
	svc := &OpsAlertEvaluatorService{opsRepo: &stubOpsRepo{}}

	value, ok := svc.computeRuleMetric(context.Background(), rule, nil, now.Add(-time.Minute), now, "", nil)
	require.False(t, ok)
	require.Zero(t, value)

	transport := &captureAdminStatusTransport{status: model.Status{SpoolReady: true, DeliveryReady: false}}
	svc.capturePool = newConversationCapturePoolForTransport(transport, func() bool { return true })
	svc.captureSupervisor = &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: false}}
	value, ok = svc.computeRuleMetric(context.Background(), rule, nil, now.Add(-time.Minute), now, "", nil)
	require.True(t, ok)
	require.Zero(t, value)

	svc.captureSupervisor.status.Running = true
	value, ok = svc.computeRuleMetric(context.Background(), rule, nil, now.Add(-time.Minute), now, "", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)
}

func TestComputeRuleMetricCaptureDeliveryAndSpoolUsage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	transport := &captureAdminStatusTransport{status: model.Status{
		SpoolReady: true, DeliveryReady: false, SpoolUsedBytes: 9 << 30, SpoolMaxBytes: 12 << 30,
	}}
	svc := &OpsAlertEvaluatorService{
		opsRepo: &stubOpsRepo{}, capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
		captureSupervisor: &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true}},
	}

	delivery, deliveryOK := svc.computeRuleMetric(context.Background(), &OpsAlertRule{MetricType: "capture_delivery_ready"}, nil, now.Add(-time.Minute), now, "", nil)
	usage, usageOK := svc.computeRuleMetric(context.Background(), &OpsAlertRule{MetricType: "capture_spool_usage_percent"}, nil, now.Add(-time.Minute), now, "", nil)

	require.True(t, deliveryOK)
	require.Zero(t, delivery)
	require.True(t, usageOK)
	require.Equal(t, 75.0, usage)
}

func TestCaptureMetricsUseCheckpointWhenLiveStatusFailsButSupervisorRuns(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.Spool.Dir = "/app/data/capture/spool"
	transport := &captureAdminStatusTransport{err: errors.New("temporary status timeout")}
	svc := &OpsAlertEvaluatorService{
		opsRepo: &stubOpsRepo{}, cfg: cfg,
		capturePool:       newConversationCapturePoolForTransport(transport, func() bool { return true }),
		captureSupervisor: &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true}},
		readCaptureStatusCheckpoint: func(string) (model.Status, bool, error) {
			return model.Status{SpoolReady: true, DeliveryReady: true, SpoolUsedBytes: 9, SpoolMaxBytes: 12}, true, nil
		},
	}

	for metric, want := range map[string]float64{
		"capture_ready": 1, "capture_delivery_ready": 1, "capture_spool_usage_percent": 75,
	} {
		value, ok := svc.computeRuleMetric(
			context.Background(), &OpsAlertRule{MetricType: metric}, nil, now.Add(-time.Minute), now, "", nil,
		)
		require.True(t, ok, metric)
		require.Equal(t, want, value, metric)
	}
}

func TestComputeRuleMetricCaptureDroppedRecords(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	repo := &opsCaptureHealthRepoStub{events: []CaptureHealthEvent{
		{MinuteBucket: start, InstanceID: "sidecar-1", Reason: string(CaptureDropIPCUnavailable), DroppedRecords: 2},
		{MinuteBucket: start, InstanceID: "sidecar-2", Reason: string(CaptureDropSpoolCap), DroppedRecords: 3},
		{MinuteBucket: start, InstanceID: "sidecar-2", Reason: string(CaptureDropPreCommitDisconnect), DroppedRecords: 5},
		{MinuteBucket: start, InstanceID: "legacy-app", Reason: string(CaptureDropClickHouseSendFailed), DroppedRecords: 7},
		{MinuteBucket: start, InstanceID: "sidecar-2", Reason: captureHealthOperationsReason, UploadRetries: 11},
	}}
	svc := &OpsAlertEvaluatorService{opsRepo: &stubOpsRepo{}, captureHealthRepo: repo}

	value, ok := svc.computeRuleMetric(
		context.Background(),
		&OpsAlertRule{MetricType: "capture_dropped_records"},
		nil,
		start,
		end,
		"",
		nil,
	)

	require.True(t, ok)
	require.Equal(t, 10.0, value, "delivery retries and legacy writer failures are not dropped records")
	require.Equal(t, start, repo.start)
	require.Equal(t, end, repo.end)
	require.Equal(t, start, repo.latestBefore)
}

func TestComputeRuleMetricCaptureDroppedRecordsUsesCumulativeDeltas(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	repo := &opsCaptureHealthRepoStub{
		latest: []CaptureHealthEvent{
			{MinuteBucket: start.Add(-6 * time.Hour), InstanceID: "sidecar-1", Reason: "spool_cap", DroppedRecords: 10},
		},
		events: []CaptureHealthEvent{
			{MinuteBucket: start, InstanceID: "sidecar-1", Reason: "spool_cap", DroppedRecords: 10},
			{MinuteBucket: start.Add(time.Minute), InstanceID: "sidecar-1", Reason: "spool_cap", DroppedRecords: 12},
			{MinuteBucket: start.Add(2 * time.Minute), InstanceID: "sidecar-1", Reason: "spool_cap", DroppedRecords: 12},
		}}
	svc := &OpsAlertEvaluatorService{opsRepo: &stubOpsRepo{}, captureHealthRepo: repo}

	value, ok := svc.computeRuleMetric(
		context.Background(), &OpsAlertRule{MetricType: "capture_dropped_records"}, nil, start, end, "", nil,
	)

	require.True(t, ok)
	require.Equal(t, 2.0, value, "unchanged cumulative rows must not be counted again each minute")
	require.Equal(t, start, repo.start)
	require.Equal(t, start, repo.latestBefore)
	require.Equal(t, []string{"sidecar-1"}, repo.latestInstanceIDs)
	require.ElementsMatch(t, captureOperationalDropReasonNames, repo.latestReasons)
}

func TestComputeRuleMetricCaptureWriterFailures(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	repo := &opsCaptureHealthRepoStub{events: []CaptureHealthEvent{
		{Reason: string(CaptureDropWriterUnavailable), DroppedRecords: 2},
		{Reason: string(CaptureDropClickHousePrepareFailed), DroppedRecords: 3},
		{Reason: string(CaptureDropClickHouseAppendFailed), DroppedRecords: 5},
		{Reason: string(CaptureDropClickHouseSendFailed), DroppedRecords: 7},
		{Reason: string(CaptureDropWorkerQueueFull), DroppedRecords: 11},
		{Reason: string(CaptureDropWriterQueueFull), DroppedRecords: 13},
		{Reason: string(CaptureDropByteBudgetExceeded), DroppedRecords: 17},
	}}
	svc := &OpsAlertEvaluatorService{opsRepo: &stubOpsRepo{}, captureHealthRepo: repo}

	value, ok := svc.computeRuleMetric(
		context.Background(),
		&OpsAlertRule{MetricType: "capture_writer_failures"},
		nil,
		start,
		end,
		"",
		nil,
	)

	require.False(t, ok, "the retired writer metric must not remain active in the evaluator")
	require.Zero(t, value)
}

func TestComputeRuleMetricCaptureRepositoryErrorIsUnavailable(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc := &OpsAlertEvaluatorService{
		opsRepo:           &stubOpsRepo{},
		captureHealthRepo: &opsCaptureHealthRepoStub{err: errors.New("postgres unavailable")},
	}

	value, ok := svc.computeRuleMetric(
		context.Background(),
		&OpsAlertRule{MetricType: "capture_dropped_records"},
		nil,
		now.Add(-5*time.Minute),
		now,
		"",
		nil,
	)

	require.False(t, ok)
	require.Zero(t, value)
}

func TestNewOpsAlertEvaluatorServiceProvidesCaptureMetrics(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	repo := &opsCaptureHealthRepoStub{events: []CaptureHealthEvent{{
		MinuteBucket:   now.Add(-30 * time.Second),
		InstanceID:     "sidecar-source",
		Reason:         string(CaptureDropSpoolCap),
		DroppedRecords: 4,
	}}}
	pool := newOpsCaptureMetricPool(t, staticArchiveWriterStatus{ready: true})
	svc := NewOpsAlertEvaluatorService(nil, &stubOpsRepo{}, nil, nil, nil, nil, pool, nil, repo)

	ready, readyOK := svc.computeRuleMetric(
		context.Background(),
		&OpsAlertRule{MetricType: "capture_ready"},
		nil,
		now.Add(-time.Minute),
		now,
		"",
		nil,
	)
	dropped, droppedOK := svc.computeRuleMetric(
		context.Background(),
		&OpsAlertRule{MetricType: "capture_dropped_records"},
		nil,
		now.Add(-time.Minute),
		now,
		"",
		nil,
	)

	require.True(t, readyOK)
	require.Equal(t, 1.0, ready)
	require.True(t, droppedOK)
	require.Equal(t, 4.0, dropped)
}

func TestOpsAlertEvaluatorSendsRecoveryEmailAfterResolution(t *testing.T) {
	rule := &OpsAlertRule{
		ID:          91,
		Name:        "Capture writer readiness",
		Enabled:     true,
		Severity:    "P0",
		MetricType:  "capture_ready",
		Operator:    "<",
		Threshold:   1,
		NotifyEmail: true,
	}
	firedAt := time.Now().UTC().Add(-5 * time.Minute)
	repo := &opsAlertEvaluatorRepoStub{
		rules: []*OpsAlertRule{rule},
		activeEvent: &OpsAlertEvent{
			ID:        501,
			RuleID:    rule.ID,
			Severity:  rule.Severity,
			Status:    OpsAlertStatusFiring,
			FiredAt:   firedAt,
			EmailSent: true,
		},
	}
	pool := newOpsCaptureMetricPool(t, staticArchiveWriterStatus{ready: true})
	svc := NewOpsAlertEvaluatorService(nil, repo, nil, nil, nil, nil, pool, nil, nil)

	var sentKind opsAlertEmailKind
	var sentEvent *OpsAlertEvent
	svc.sendAlertEmail = func(_ context.Context, _ *OpsAlertRuntimeSettings, _ *OpsAlertRule, event *OpsAlertEvent, kind opsAlertEmailKind) bool {
		require.True(t, repo.resolutionApplied, "the resolved state must be durable before recovery email dispatch")
		sentKind = kind
		sentEvent = event
		return true
	}

	svc.evaluateOnce(time.Minute)

	require.Equal(t, OpsAlertStatusResolved, repo.resolvedStatus)
	require.NotNil(t, repo.resolvedAt)
	require.Equal(t, opsAlertEmailRecovery, sentKind)
	require.NotNil(t, sentEvent)
	require.Equal(t, OpsAlertStatusResolved, sentEvent.Status)
	require.NotNil(t, sentEvent.ResolvedAt)
	require.True(t, sentEvent.EmailSent, "a prior firing email must not suppress recovery notification")
	require.True(t, strings.Contains(repo.heartbeatResult, "resolved=1"), repo.heartbeatResult)
	require.True(t, strings.Contains(repo.heartbeatResult, "emails_sent=1"), repo.heartbeatResult)
}

func TestOpsAlertEvaluatorDoesNotSendRecoveryEmailWhenResolutionFails(t *testing.T) {
	rule := &OpsAlertRule{
		ID:         92,
		Name:       "Capture writer readiness",
		Enabled:    true,
		Severity:   "P0",
		MetricType: "capture_ready",
		Operator:   "<",
		Threshold:  1,
	}
	repo := &opsAlertEvaluatorRepoStub{
		rules:       []*OpsAlertRule{rule},
		resolveErr:  errors.New("database unavailable"),
		activeEvent: &OpsAlertEvent{ID: 502, RuleID: rule.ID, Status: OpsAlertStatusFiring},
	}
	pool := newOpsCaptureMetricPool(t, staticArchiveWriterStatus{ready: true})
	svc := NewOpsAlertEvaluatorService(nil, repo, nil, nil, nil, nil, pool, nil, nil)
	svc.sendAlertEmail = func(context.Context, *OpsAlertRuntimeSettings, *OpsAlertRule, *OpsAlertEvent, opsAlertEmailKind) bool {
		t.Fatal("recovery email must not be sent before resolution is persisted")
		return false
	}

	svc.evaluateOnce(time.Minute)

	require.False(t, repo.resolutionApplied)
	require.True(t, strings.Contains(repo.heartbeatResult, "resolved=0"), repo.heartbeatResult)
	require.True(t, strings.Contains(repo.heartbeatResult, "emails_sent=0"), repo.heartbeatResult)
}

func TestShouldSkipOpsAlertEmailTreatsEmailSentAsFiringOnly(t *testing.T) {
	event := &OpsAlertEvent{EmailSent: true}

	require.True(t, shouldSkipOpsAlertEmail(opsAlertEmailFiring, event))
	require.False(t, shouldSkipOpsAlertEmail(opsAlertEmailRecovery, event))
}
