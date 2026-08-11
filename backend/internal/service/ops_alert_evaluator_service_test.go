//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type opsCaptureHealthRepoStub struct {
	events []CaptureHealthEvent
	err    error
	start  time.Time
	end    time.Time
}

func (r *opsCaptureHealthRepoStub) UpsertEvents(context.Context, []CaptureHealthEvent) error {
	return nil
}

func (r *opsCaptureHealthRepoStub) ListEvents(_ context.Context, start, end time.Time) ([]CaptureHealthEvent, error) {
	r.start = start
	r.end = end
	return append([]CaptureHealthEvent(nil), r.events...), r.err
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

	status := &mutableArchiveWriterStatus{initError: "dial failed"}
	svc.capturePool = newOpsCaptureMetricPool(t, status)
	value, ok = svc.computeRuleMetric(context.Background(), rule, nil, now.Add(-time.Minute), now, "", nil)
	require.True(t, ok)
	require.Zero(t, value)

	status.set(true, "")
	value, ok = svc.computeRuleMetric(context.Background(), rule, nil, now.Add(-time.Minute), now, "", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)
}

func TestComputeRuleMetricCaptureDroppedRecords(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	repo := &opsCaptureHealthRepoStub{events: []CaptureHealthEvent{
		{InstanceID: "app-1", Reason: string(CaptureDropWorkerQueueFull), DroppedRecords: 2},
		{InstanceID: "app-2", Reason: string(CaptureDropWriterUnavailable), DroppedRecords: 3},
		{InstanceID: "app-2", Reason: string(CaptureDropClickHouseSendFailed), DroppedRecords: 5},
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
	require.Equal(t, 10.0, value)
	require.Equal(t, start, repo.start)
	require.Equal(t, end, repo.end)
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

	require.True(t, ok)
	require.Equal(t, 17.0, value)
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
		Reason:         string(CaptureDropWorkerQueueFull),
		DroppedRecords: 4,
	}}}
	pool := newOpsCaptureMetricPool(t, staticArchiveWriterStatus{ready: true})
	svc := NewOpsAlertEvaluatorService(nil, &stubOpsRepo{}, nil, nil, nil, nil, pool, repo)

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
