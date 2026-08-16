package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type captureHealthRepoFake struct {
	mu                  sync.Mutex
	failUpserts         int
	commitFailedUpserts bool
	upserts             [][]CaptureHealthEvent
	latest              map[captureStatusEventKey]CaptureHealthEvent
	latestBeforeCalls   int
	cutoffs             []time.Time
}

type blockingCaptureHealthRepo struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	once    sync.Once
	upserts [][]CaptureHealthEvent
}

func (r *blockingCaptureHealthRepo) UpsertEvents(_ context.Context, events []CaptureHealthEvent) error {
	r.once.Do(func() { close(r.started) })
	<-r.release
	r.mu.Lock()
	r.upserts = append(r.upserts, append([]CaptureHealthEvent(nil), events...))
	r.mu.Unlock()
	return nil
}

func (*blockingCaptureHealthRepo) ListEvents(context.Context, time.Time, time.Time) ([]CaptureHealthEvent, error) {
	return nil, nil
}

func (*blockingCaptureHealthRepo) ListLatestEventsBefore(context.Context, time.Time, []string, []string) ([]CaptureHealthEvent, error) {
	return nil, nil
}

func (*blockingCaptureHealthRepo) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (r *captureHealthRepoFake) UpsertEvents(_ context.Context, events []CaptureHealthEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyEvents := append([]CaptureHealthEvent(nil), events...)
	r.upserts = append(r.upserts, copyEvents)
	if r.failUpserts > 0 {
		r.failUpserts--
		if r.commitFailedUpserts {
			r.persistLatestLocked(copyEvents)
		}
		return errors.New("postgres unavailable")
	}
	r.persistLatestLocked(copyEvents)
	return nil
}

func (r *captureHealthRepoFake) persistLatestLocked(events []CaptureHealthEvent) {
	if r.latest == nil {
		r.latest = make(map[captureStatusEventKey]CaptureHealthEvent)
	}
	for _, event := range events {
		key := captureStatusEventKey{minute: event.MinuteBucket, instance: event.InstanceID, reason: event.Reason}
		current := r.latest[key]
		current.MinuteBucket = event.MinuteBucket
		current.InstanceID = event.InstanceID
		current.Reason = event.Reason
		current.DroppedRecords = maxInt64(current.DroppedRecords, event.DroppedRecords)
		current.DroppedBytes = maxInt64(current.DroppedBytes, event.DroppedBytes)
		current.UploadRetries = maxInt64(current.UploadRetries, event.UploadRetries)
		current.SidecarRestarts = maxInt64(current.SidecarRestarts, event.SidecarRestarts)
		r.latest[key] = current
	}
}

func (r *captureHealthRepoFake) ListLatestEventsBefore(_ context.Context, before time.Time, instanceIDs, reasons []string) ([]CaptureHealthEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latestBeforeCalls++
	allowed := make(map[string]struct{}, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		allowed[instanceID] = struct{}{}
	}
	allowedReasons := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		allowedReasons[reason] = struct{}{}
	}
	latest := make(map[string]CaptureHealthEvent)
	for _, event := range r.latest {
		if !event.MinuteBucket.Before(before) {
			continue
		}
		if _, ok := allowed[event.InstanceID]; !ok {
			continue
		}
		if _, ok := allowedReasons[event.Reason]; !ok {
			continue
		}
		key := event.InstanceID + "\x00" + event.Reason
		if current, ok := latest[key]; !ok || event.MinuteBucket.After(current.MinuteBucket) {
			latest[key] = event
		}
	}
	result := make([]CaptureHealthEvent, 0, len(latest))
	for _, event := range latest {
		result = append(result, event)
	}
	return result, nil
}

func (r *captureHealthRepoFake) ListEvents(context.Context, time.Time, time.Time) ([]CaptureHealthEvent, error) {
	return nil, nil
}

func (r *captureHealthRepoFake) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cutoffs = append(r.cutoffs, cutoff)
	return 0, nil
}

func TestCaptureHealthReporterRetriesWithoutDoubleCounting(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	tracker := newCaptureHealthTracker("host-a", func() time.Time { return now })
	repo := &captureHealthRepoFake{failUpserts: 1}
	reporter := newCaptureHealthReporter(tracker, repo, captureHealthReporterOptions{now: func() time.Time { return now }})
	tracker.recordDrop(CaptureDropWriterQueueFull, 2, 300, nil)

	now = now.Add(time.Minute)
	require.Error(t, reporter.flushOnce(context.Background(), false))
	require.NoError(t, reporter.flushOnce(context.Background(), false))
	require.NoError(t, reporter.flushOnce(context.Background(), false))

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.upserts, 2, "third flush has no completed or pending buckets")
	require.Equal(t, int64(2), repo.upserts[0][0].DroppedRecords)
	require.Equal(t, int64(2), repo.upserts[1][0].DroppedRecords)
}

func TestCaptureHealthReporterCleanupUsesThirtyDayCutoff(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	repo := &captureHealthRepoFake{}
	reporter := newCaptureHealthReporter(newCaptureHealthTracker("host-a", func() time.Time { return now }), repo, captureHealthReporterOptions{
		now:       func() time.Time { return now },
		retention: 30 * 24 * time.Hour,
	})

	require.NoError(t, reporter.cleanupOnce(context.Background()))
	require.Equal(t, []time.Time{now.Add(-30 * 24 * time.Hour)}, repo.cutoffs)
}

func TestCaptureHealthReporterBoundsPendingRetryStateAndBatchSize(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	tracker := newCaptureHealthTracker("host-a", func() time.Time { return now })
	repo := &captureHealthRepoFake{failUpserts: 10}
	reporter := newCaptureHealthReporter(tracker, repo, captureHealthReporterOptions{
		now:             func() time.Time { return now },
		pendingCapacity: 2,
		maxBatchSize:    1,
	})

	for i := 0; i < 3; i++ {
		tracker.recordDrop(CaptureDropWriterQueueFull, 1, int64(i+1), nil)
		now = now.Add(time.Minute)
		require.Error(t, reporter.flushOnce(context.Background(), false))
	}

	reporter.mu.Lock()
	require.Len(t, reporter.pending, 2)
	reporter.mu.Unlock()
	require.Equal(t, uint64(1), tracker.snapshot().HistoryDroppedBuckets)

	repo.mu.Lock()
	for _, batch := range repo.upserts {
		require.LessOrEqual(t, len(batch), 1)
	}
	repo.mu.Unlock()
}

func TestBuildCaptureStatusEventsUsesSidecarIdentityAndCumulativeValues(t *testing.T) {
	minute := time.Date(2026, 8, 17, 1, 2, 0, 0, time.UTC)
	sourceID := uuid.New()
	status := model.Status{
		HealthSourceID:        sourceID,
		SpoolUsedBytes:        9 << 30,
		SpoolMaxBytes:         12 << 30,
		ReadyRecords:          42,
		OldestReadyAgeSeconds: 91,
		UploadRetries:         8,
		HealthBuckets: []model.HealthBucket{{
			Minute:         minute,
			DroppedRecords: map[string]uint64{"spool_cap": 3},
			DroppedBytes:   map[string]uint64{"spool_cap": 4096},
			UploadRetries:  8,
		}},
	}

	got := buildCaptureStatusEvents(status, CaptureSidecarSupervisorStatus{RestartCount: 2})

	require.Equal(t, []CaptureHealthEvent{
		{
			MinuteBucket: minute, InstanceID: sourceID.String(), Reason: captureHealthOperationsReason,
			SpoolUsedBytesPeak: 9 << 30, ReadyRecordsPeak: 42, OldestReadyAgeSecondsPeak: 91,
			UploadRetries: 8, SidecarRestarts: 2,
		},
		{
			MinuteBucket: minute, InstanceID: sourceID.String(), Reason: "spool_cap",
			DroppedRecords: 3, DroppedBytes: 4096, LastError: "capture spool reached physical cap",
		},
	}, got)
}

func TestBuildCaptureStatusEventsAttributesCurrentGaugesOnlyToCurrentBucket(t *testing.T) {
	previous := time.Date(2026, 8, 17, 1, 1, 0, 0, time.UTC)
	current := previous.Add(time.Minute)
	status := model.Status{
		HealthSourceID: uuid.New(), SpoolUsedBytes: 99, ReadyRecords: 8, OldestReadyAgeSeconds: 7,
		HealthBuckets: []model.HealthBucket{
			{Minute: previous, UploadRetries: 3},
			{Minute: current, UploadRetries: 4},
		},
	}

	events := buildCaptureStatusEvents(status, CaptureSidecarSupervisorStatus{RestartCount: 2})
	require.Len(t, events, 2)
	require.Equal(t, previous, events[0].MinuteBucket)
	require.Zero(t, events[0].SpoolUsedBytesPeak)
	require.Zero(t, events[0].ReadyRecordsPeak)
	require.Zero(t, events[0].OldestReadyAgeSecondsPeak)
	require.Zero(t, events[0].SidecarRestarts)
	require.EqualValues(t, 3, events[0].UploadRetries)
	require.Equal(t, current, events[1].MinuteBucket)
	require.EqualValues(t, 99, events[1].SpoolUsedBytesPeak)
	require.EqualValues(t, 8, events[1].ReadyRecordsPeak)
	require.EqualValues(t, 7, events[1].OldestReadyAgeSecondsPeak)
	require.EqualValues(t, 2, events[1].SidecarRestarts)
}

func TestBuildCaptureStatusEventsRejectsMissingSidecarIdentity(t *testing.T) {
	status := model.Status{HealthBuckets: []model.HealthBucket{{Minute: time.Now().UTC().Truncate(time.Minute)}}}
	require.Empty(t, buildCaptureStatusEvents(status, CaptureSidecarSupervisorStatus{}))
}

func TestSuccessfulLiveStatusPollStagesCumulativeBucketsForPersistence(t *testing.T) {
	minute := time.Date(2026, 8, 17, 1, 2, 0, 0, time.UTC)
	sourceID := uuid.New()
	transport := &captureAdminStatusTransport{status: model.Status{
		HealthSourceID: sourceID,
		HealthBuckets: []model.HealthBucket{{
			Minute:         minute,
			DroppedRecords: map[string]uint64{"spool_cap": 2},
		}},
	}}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	reporter := newCaptureStatusReporter(
		pool,
		&CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true}},
		&captureHealthRepoFake{},
		"/safe/status.json",
		nil,
	)
	pool.healthReporter = reporter

	status, err := pool.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, sourceID, status.HealthSourceID)

	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	require.Len(t, reporter.pending, 2)
	require.EqualValues(t, 2, reporter.pending[captureStatusEventKey{
		minute: minute, instance: sourceID.String(), reason: "spool_cap",
	}].DroppedRecords)
}

func TestLiveStatusMergesMainAndSidecarCumulativeLossesIdempotently(t *testing.T) {
	minute := time.Date(2026, 8, 17, 1, 2, 0, 0, time.UTC)
	sourceID := uuid.New()
	transport := &captureAdminStatusTransport{status: model.Status{
		HealthSourceID:  sourceID,
		DroppedRecords:  2,
		DroppedByReason: map[string]uint64{"spool_cap": 2},
		HealthBuckets: []model.HealthBucket{{
			Minute: minute, DroppedRecords: map[string]uint64{"spool_cap": 2},
		}},
	}}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	reporter := newCaptureStatusReporter(
		pool,
		&CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true}},
		&captureHealthRepoFake{},
		"/safe/status.json",
		nil,
	)
	pool.healthReporter = reporter
	pool.losses.installDurableOffset(sourceID, nil)
	pool.losses.record(CaptureDropIPCUnavailable)

	first, err := pool.Status(context.Background())
	require.NoError(t, err)
	second, err := pool.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, first.DroppedByReason, second.DroppedByReason)
	require.EqualValues(t, 3, second.DroppedRecords)

	pool.losses.record(CaptureDropIPCUnavailable)
	third, err := pool.Status(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, third.DroppedByReason["ipc_unavailable"])
	require.EqualValues(t, 2, third.DroppedByReason["spool_cap"])
	require.EqualValues(t, 4, third.DroppedRecords)

	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	require.EqualValues(t, 2, reporter.pending[captureStatusEventKey{
		minute: minute, instance: sourceID.String(), reason: "ipc_unavailable",
	}].DroppedRecords)
	require.EqualValues(t, 2, reporter.pending[captureStatusEventKey{
		minute: minute, instance: sourceID.String(), reason: "spool_cap",
	}].DroppedRecords)
}

func TestCaptureStatusReporterBoundsPendingStateAndRecoveryBatch(t *testing.T) {
	transport := &captureAdminStatusTransport{err: errors.New("status unavailable")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	repo := &captureHealthRepoFake{failUpserts: 1}
	reporter := newCaptureStatusReporter(pool, nil, repo, "/safe/status.json", nil)
	reporter.pendingCapacity = 2
	reporter.maxBatchSize = 1
	base := time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		reporter.merge([]CaptureHealthEvent{{
			MinuteBucket: base.Add(time.Duration(i) * time.Minute), InstanceID: "sidecar", Reason: "spool_cap",
			DroppedRecords: int64(i + 1),
		}})
	}

	reporter.mu.Lock()
	require.Len(t, reporter.pending, 2)
	_, oldestRetained := reporter.pending[captureStatusEventKey{minute: base, instance: "sidecar", reason: "spool_cap"}]
	reporter.mu.Unlock()
	require.False(t, oldestRetained)
	require.Error(t, reporter.flushOnce(context.Background()))
	require.NoError(t, reporter.flushOnce(context.Background()))
	require.NoError(t, reporter.flushOnce(context.Background()))

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.upserts, 3)
	for _, batch := range repo.upserts {
		require.LessOrEqual(t, len(batch), 1)
	}
	require.Equal(t, repo.upserts[0], repo.upserts[1], "failed cumulative batch must retry without mutation")
}

func TestCaptureStatusReporterConcurrentPollPreservesNewerCumulativeValue(t *testing.T) {
	minute := time.Date(2026, 8, 17, 1, 2, 0, 0, time.UTC)
	sourceID := uuid.New()
	pool := newConversationCapturePoolForTransport(&captureAdminStatusTransport{status: model.Status{
		HealthSourceID: sourceID, HealthBuckets: []model.HealthBucket{{Minute: minute}},
	}}, func() bool { return true })
	repo := &blockingCaptureHealthRepo{started: make(chan struct{}), release: make(chan struct{})}
	reporter := newCaptureStatusReporter(pool, nil, repo, "/safe/status.json", nil)
	pool.healthReporter = reporter
	pool.losses.installDurableOffset(sourceID, nil)
	pool.losses.record(CaptureDropIPCUnavailable)
	_, err := pool.Status(context.Background())
	require.NoError(t, err)

	flushDone := make(chan error, 1)
	go func() { flushDone <- reporter.flushOnce(context.Background()) }()
	<-repo.started
	pool.losses.record(CaptureDropIPCUnavailable)
	_, err = pool.Status(context.Background())
	require.NoError(t, err)
	close(repo.release)
	require.NoError(t, <-flushDone)

	reporter.mu.Lock()
	pending := reporter.pending[captureStatusEventKey{
		minute: minute, instance: sourceID.String(), reason: string(CaptureDropIPCUnavailable),
	}]
	reporter.mu.Unlock()
	require.EqualValues(t, 2, pending.DroppedRecords, "successful older flush must not delete a concurrent newer poll")
	require.NoError(t, reporter.flushOnce(context.Background()))

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.upserts, 2)
	var values []int64
	for _, batch := range repo.upserts {
		for _, event := range batch {
			if event.Reason == string(CaptureDropIPCUnavailable) {
				values = append(values, event.DroppedRecords)
			}
		}
	}
	require.Equal(t, []int64{1, 2}, values)
}

func TestCaptureStatusReporterUsesCheckpointWhenSupervisorIsDown(t *testing.T) {
	minute := time.Date(2026, 8, 17, 1, 2, 0, 0, time.UTC)
	liveTransport := &captureAdminStatusTransport{status: model.Status{
		HealthSourceID: uuid.New(),
		HealthBuckets:  []model.HealthBucket{{Minute: minute}},
	}}
	pool := newConversationCapturePoolForTransport(liveTransport, func() bool { return true })
	supervisor := &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: false, RestartCount: 3}}
	repo := &captureHealthRepoFake{}
	checkpointSource := uuid.New()
	readCalls := 0
	reporter := newCaptureStatusReporter(pool, supervisor, repo, "/safe/status.json", func(path string) (model.Status, bool, error) {
		readCalls++
		require.Equal(t, "/safe/status.json", path)
		return model.Status{
			HealthSourceID: checkpointSource,
			SpoolUsedBytes: 9 << 30,
			HealthBuckets:  []model.HealthBucket{{Minute: minute, UploadRetries: 4}},
		}, true, nil
	})

	require.NoError(t, reporter.flushOnce(context.Background()))
	require.Zero(t, liveTransport.calls)
	require.Equal(t, 1, readCalls)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.upserts, 1)
	require.Len(t, repo.upserts[0], 1)
	require.Equal(t, checkpointSource.String(), repo.upserts[0][0].InstanceID)
	require.EqualValues(t, 3, repo.upserts[0][0].SidecarRestarts)
	require.EqualValues(t, 4, repo.upserts[0][0].UploadRetries)
}

func TestCaptureStatusReporterAddsDurableOffsetAfterGatewayRestart(t *testing.T) {
	minute := time.Date(2026, 8, 17, 3, 4, 0, 0, time.UTC)
	sourceID := uuid.New()
	repo := &captureHealthRepoFake{latest: map[captureStatusEventKey]CaptureHealthEvent{
		{minute: minute.Add(-time.Hour), instance: sourceID.String(), reason: string(CaptureDropIPCUnavailable)}: {
			MinuteBucket: minute.Add(-time.Hour), InstanceID: sourceID.String(),
			Reason: string(CaptureDropIPCUnavailable), DroppedRecords: 5,
		},
	}}
	pool := newConversationCapturePoolForTransport(&captureAdminStatusTransport{status: model.Status{
		HealthSourceID: sourceID, HealthBuckets: []model.HealthBucket{{Minute: minute}},
	}}, func() bool { return true })
	pool.losses.record(CaptureDropIPCUnavailable)
	reporter := newCaptureStatusReporter(pool, nil, repo, "/safe/status.json", nil)
	reporter.now = func() time.Time { return minute.Add(30 * time.Second) }

	require.NoError(t, reporter.flushOnce(context.Background()))
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, 1, repo.latestBeforeCalls)
	var got int64
	for _, event := range repo.upserts[len(repo.upserts)-1] {
		if event.Reason == string(CaptureDropIPCUnavailable) {
			got = event.DroppedRecords
		}
	}
	require.EqualValues(t, 6, got, "persisted cumulative offset plus this-process delta")
}

func TestCaptureStatusReporterCommitResponseFailureAndRestartDoNotDuplicateOrReset(t *testing.T) {
	minute := time.Date(2026, 8, 17, 3, 4, 0, 0, time.UTC)
	sourceID := uuid.New()
	raw := model.Status{HealthSourceID: sourceID, HealthBuckets: []model.HealthBucket{{Minute: minute}}}
	repo := &captureHealthRepoFake{failUpserts: 1, commitFailedUpserts: true}
	firstProcess := newConversationCapturePoolForTransport(&captureAdminStatusTransport{status: raw}, func() bool { return true })
	firstProcess.losses.record(CaptureDropIPCUnavailable)
	firstReporter := newCaptureStatusReporter(firstProcess, nil, repo, "/safe/status.json", nil)
	firstReporter.now = func() time.Time { return minute.Add(30 * time.Second) }
	require.Error(t, firstReporter.flushOnce(context.Background()), "database committed but response was lost")

	secondReporterSameProcess := newCaptureStatusReporter(firstProcess, nil, repo, "/safe/status.json", nil)
	secondReporterSameProcess.now = firstReporter.now
	require.NoError(t, secondReporterSameProcess.flushOnce(context.Background()))

	secondProcess := newConversationCapturePoolForTransport(&captureAdminStatusTransport{status: raw}, func() bool { return true })
	secondProcess.losses.record(CaptureDropIPCUnavailable)
	secondProcessReporter := newCaptureStatusReporter(secondProcess, nil, repo, "/safe/status.json", nil)
	secondProcessReporter.now = firstReporter.now
	require.NoError(t, secondProcessReporter.flushOnce(context.Background()))
	require.NoError(t, secondProcessReporter.flushOnce(context.Background()))

	repo.mu.Lock()
	defer repo.mu.Unlock()
	var values []int64
	for _, batch := range repo.upserts {
		for _, event := range batch {
			if event.Reason == string(CaptureDropIPCUnavailable) {
				values = append(values, event.DroppedRecords)
			}
		}
	}
	require.NotEmpty(t, values)
	require.EqualValues(t, 2, values[len(values)-1])
	for _, value := range values {
		require.LessOrEqual(t, value, int64(2), "retry/reporter restart must not double count")
	}
}
