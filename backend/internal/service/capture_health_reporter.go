package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const captureHealthRepositoryTimeout = 5 * time.Second
const captureHealthPendingCapacity = 4096
const captureHealthRetryBatchSize = 256

const captureHealthOperationsReason = "operations"

var captureOperationalDropReasonNames = []string{
	string(CaptureDropIPCUnavailable),
	string(CaptureDropIPCBackpressure),
	string(CaptureDropSidecarDown),
	string(CaptureDropSpoolCap),
	string(CaptureDropSpoolFreeReserve),
	string(CaptureDropSpoolCorrupt),
	string(CaptureDropPreCommitDisconnect),
}

var captureMainProcessDropReasonNames = []string{
	string(CaptureDropIPCUnavailable),
	string(CaptureDropIPCBackpressure),
	string(CaptureDropSidecarDown),
	string(CaptureDropPreCommitDisconnect),
}

func buildCaptureStatusEvents(status model.Status, supervisor CaptureSidecarSupervisorStatus) []CaptureHealthEvent {
	if status.HealthSourceID == uuid.Nil {
		return nil
	}
	instanceID := status.HealthSourceID.String()
	events := make([]CaptureHealthEvent, 0, len(status.HealthBuckets)*(len(captureDropReasons)+1))
	for bucketIndex, bucket := range status.HealthBuckets {
		if bucket.Minute.IsZero() {
			continue
		}
		operations := CaptureHealthEvent{
			MinuteBucket:  bucket.Minute.UTC().Truncate(time.Minute),
			InstanceID:    instanceID,
			Reason:        captureHealthOperationsReason,
			UploadRetries: boundedUint64ToInt64(bucket.UploadRetries),
		}
		if bucketIndex == len(status.HealthBuckets)-1 {
			operations.SpoolUsedBytesPeak = nonnegativeInt64(status.SpoolUsedBytes)
			operations.ReadyRecordsPeak = nonnegativeInt64(status.ReadyRecords)
			operations.OldestReadyAgeSecondsPeak = nonnegativeInt64(status.OldestReadyAgeSeconds)
			operations.SidecarRestarts = boundedUint64ToInt64(supervisor.RestartCount)
		}
		events = append(events, operations)
		reasons := make([]string, 0, len(bucket.DroppedRecords)+len(bucket.DroppedBytes))
		seen := make(map[string]struct{}, len(bucket.DroppedRecords)+len(bucket.DroppedBytes))
		for reason := range bucket.DroppedRecords {
			seen[reason] = struct{}{}
			reasons = append(reasons, reason)
		}
		for reason := range bucket.DroppedBytes {
			if _, ok := seen[reason]; !ok {
				reasons = append(reasons, reason)
			}
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			if !isCaptureOperationalDropReason(reason) {
				continue
			}
			events = append(events, CaptureHealthEvent{
				MinuteBucket:   bucket.Minute.UTC().Truncate(time.Minute),
				InstanceID:     instanceID,
				Reason:         reason,
				DroppedRecords: boundedUint64ToInt64(bucket.DroppedRecords[reason]),
				DroppedBytes:   boundedUint64ToInt64(bucket.DroppedBytes[reason]),
				LastError:      captureHealthErrorCategory(CaptureDropReason(reason)),
			})
		}
	}
	return events
}

func isCaptureOperationalDropReason(reason string) bool {
	switch CaptureDropReason(reason) {
	case CaptureDropIPCUnavailable,
		CaptureDropIPCBackpressure,
		CaptureDropSidecarDown,
		CaptureDropSpoolCap,
		CaptureDropSpoolFreeReserve,
		CaptureDropSpoolCorrupt,
		CaptureDropPreCommitDisconnect:
		return true
	default:
		return false
	}
}

func boundedUint64ToInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}

func nonnegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

type captureHealthReporterOptions struct {
	now             func() time.Time
	flushInterval   time.Duration
	cleanupInterval time.Duration
	retention       time.Duration
	pendingCapacity int
	maxBatchSize    int
}

type captureHealthReporter struct {
	tracker *captureHealthTracker
	repo    CaptureHealthRepository
	now     func() time.Time

	flushInterval   time.Duration
	cleanupInterval time.Duration
	retention       time.Duration
	pendingCapacity int
	maxBatchSize    int

	ctx       context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup

	mu      sync.Mutex
	pending map[captureHealthBucketKey]CaptureHealthEvent
}

type captureStatusEventKey struct {
	minute   time.Time
	instance string
	reason   string
}

type captureStatusReporter struct {
	pool            *ConversationCapturePool
	supervisor      *CaptureSidecarSupervisor
	repo            CaptureHealthRepository
	statusPath      string
	read            func(string) (model.Status, bool, error)
	now             func() time.Time
	pendingCapacity int
	maxBatchSize    int

	ctx       context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup

	mu      sync.Mutex
	pending map[captureStatusEventKey]CaptureHealthEvent
}

func newCaptureStatusReporter(
	pool *ConversationCapturePool,
	supervisor *CaptureSidecarSupervisor,
	repo CaptureHealthRepository,
	statusPath string,
	read func(string) (model.Status, bool, error),
) *captureStatusReporter {
	ctx, cancel := context.WithCancel(context.Background())
	return &captureStatusReporter{
		pool: pool, supervisor: supervisor, repo: repo, statusPath: statusPath, read: read,
		now: time.Now, pendingCapacity: captureHealthPendingCapacity, maxBatchSize: captureHealthRetryBatchSize,
		ctx: ctx, cancel: cancel, pending: make(map[captureStatusEventKey]CaptureHealthEvent),
	}
}

func (r *captureStatusReporter) Start() {
	if r == nil || r.pool == nil || r.repo == nil {
		return
	}
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			if err := r.flushOnce(r.ctx); err != nil {
				logger.L().Warn("capture.status_initial_flush_failed", zap.Error(err))
			}
			flushTicker := time.NewTicker(time.Minute)
			cleanupTicker := time.NewTicker(time.Hour)
			defer flushTicker.Stop()
			defer cleanupTicker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-flushTicker.C:
					if err := r.flushOnce(r.ctx); err != nil {
						logger.L().Warn("capture.status_flush_failed", zap.Error(err))
					}
				case <-cleanupTicker.C:
					ctx, cancel := captureHealthDBContext(r.ctx)
					_, err := r.repo.DeleteBefore(ctx, r.now().UTC().Add(-30*24*time.Hour))
					cancel()
					if err != nil {
						logger.L().Warn("capture.status_cleanup_failed", zap.Error(err))
					}
				}
			}
		}()
	})
}

func (r *captureStatusReporter) flushOnce(ctx context.Context) error {
	if r == nil || r.pool == nil || r.repo == nil {
		return nil
	}
	status := model.Status{}
	found := false
	if r.supervisor == nil || r.supervisor.Status().Running {
		var err error
		status, err = r.pool.rawStatus(ctx)
		found = err == nil
	}
	var err error
	if !found && r.read != nil {
		status, found, err = r.read(r.statusPath)
		if err != nil {
			found = false
		}
	}
	if found {
		if err := r.ensureDurableOffset(ctx, status); err != nil {
			return err
		}
		status = r.pool.withObservedLosses(status)
		supervisor := CaptureSidecarSupervisorStatus{}
		if r.supervisor != nil {
			supervisor = r.supervisor.Status()
		}
		r.merge(buildCaptureStatusEvents(status, supervisor))
	}

	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return nil
	}
	keys := make([]captureStatusEventKey, 0, len(r.pending))
	for key := range r.pending {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if !keys[i].minute.Equal(keys[j].minute) {
			return keys[i].minute.Before(keys[j].minute)
		}
		if keys[i].instance != keys[j].instance {
			return keys[i].instance < keys[j].instance
		}
		return keys[i].reason < keys[j].reason
	})
	batch := make([]CaptureHealthEvent, 0, len(keys))
	if r.maxBatchSize > 0 && len(keys) > r.maxBatchSize {
		keys = keys[:r.maxBatchSize]
	}
	for _, key := range keys {
		batch = append(batch, r.pending[key])
	}
	r.mu.Unlock()

	dbCtx, cancel := captureHealthDBContext(ctx)
	defer cancel()
	if err := r.repo.UpsertEvents(dbCtx, batch); err != nil {
		return fmt.Errorf("upsert capture status events: %w", err)
	}
	r.mu.Lock()
	for index, key := range keys {
		if current, ok := r.pending[key]; ok && current == batch[index] {
			delete(r.pending, key)
		}
	}
	r.mu.Unlock()
	return nil
}

func (r *captureStatusReporter) ensureDurableOffset(ctx context.Context, status model.Status) error {
	if r == nil || r.pool == nil || r.pool.losses == nil || status.HealthSourceID == uuid.Nil {
		return nil
	}
	if r.pool.losses.hasDurableOffset(status.HealthSourceID) {
		return nil
	}
	dbCtx, cancel := captureHealthDBContext(ctx)
	defer cancel()
	events, err := r.repo.ListLatestEventsBefore(
		dbCtx, r.now().UTC().Add(time.Minute), []string{status.HealthSourceID.String()}, captureMainProcessDropReasonNames,
	)
	if err != nil {
		return fmt.Errorf("load capture status durable offset: %w", err)
	}
	persisted := make(map[string]uint64, 4)
	for _, event := range events {
		if !isMainProcessCaptureDropReason(event.Reason) || event.DroppedRecords <= 0 {
			continue
		}
		value := uint64(event.DroppedRecords)
		if value > persisted[event.Reason] {
			persisted[event.Reason] = value
		}
	}
	r.pool.losses.installDurableOffset(status.HealthSourceID, persisted)
	return nil
}

func isMainProcessCaptureDropReason(reason string) bool {
	switch CaptureDropReason(reason) {
	case CaptureDropIPCUnavailable, CaptureDropIPCBackpressure, CaptureDropSidecarDown, CaptureDropPreCommitDisconnect:
		return true
	default:
		return false
	}
}

func (r *captureStatusReporter) merge(events []CaptureHealthEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range events {
		key := captureStatusEventKey{minute: event.MinuteBucket, instance: event.InstanceID, reason: event.Reason}
		if _, exists := r.pending[key]; !exists && r.pendingCapacity > 0 && len(r.pending) >= r.pendingCapacity {
			delete(r.pending, oldestCaptureStatusEventKey(r.pending))
		}
		pending := r.pending[key]
		if pending.MinuteBucket.IsZero() {
			pending.MinuteBucket = event.MinuteBucket
			pending.InstanceID = event.InstanceID
			pending.Reason = event.Reason
		}
		pending.DroppedRecords = maxInt64(pending.DroppedRecords, event.DroppedRecords)
		pending.DroppedBytes = maxInt64(pending.DroppedBytes, event.DroppedBytes)
		pending.SpoolUsedBytesPeak = maxInt64(pending.SpoolUsedBytesPeak, event.SpoolUsedBytesPeak)
		pending.ReadyRecordsPeak = maxInt64(pending.ReadyRecordsPeak, event.ReadyRecordsPeak)
		pending.OldestReadyAgeSecondsPeak = maxInt64(pending.OldestReadyAgeSecondsPeak, event.OldestReadyAgeSecondsPeak)
		pending.UploadRetries = maxInt64(pending.UploadRetries, event.UploadRetries)
		pending.SidecarRestarts = maxInt64(pending.SidecarRestarts, event.SidecarRestarts)
		if event.LastError != "" {
			pending.LastError = event.LastError
		}
		r.pending[key] = pending
	}
}

func oldestCaptureStatusEventKey(pending map[captureStatusEventKey]CaptureHealthEvent) captureStatusEventKey {
	var oldest captureStatusEventKey
	first := true
	for key := range pending {
		if first || key.minute.Before(oldest.minute) ||
			(key.minute.Equal(oldest.minute) && (key.instance < oldest.instance ||
				(key.instance == oldest.instance && key.reason < oldest.reason))) {
			oldest = key
			first = false
		}
	}
	return oldest
}

// observe stages every successfully received live status before another
// protocol poll can acknowledge and rotate its cumulative health buckets.
// Repository GREATEST upserts make observing the same poll more than once safe.
func (r *captureStatusReporter) observe(status model.Status) {
	if r == nil || r.repo == nil {
		return
	}
	supervisor := CaptureSidecarSupervisorStatus{}
	if r.supervisor != nil {
		supervisor = r.supervisor.Status()
	}
	if r.pool != nil && r.pool.losses != nil && r.pool.losses.hasDurableOffset(status.HealthSourceID) {
		status = r.pool.withObservedLosses(status)
	}
	r.merge(buildCaptureStatusEvents(status, supervisor))
}

func (r *captureStatusReporter) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.cancel()
		r.wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), captureHealthRepositoryTimeout)
		defer cancel()
		if err := r.flushOnce(ctx); err != nil {
			logger.L().Warn("capture.status_final_flush_failed", zap.Error(err))
		}
	})
}

func newCaptureHealthReporter(tracker *captureHealthTracker, repo CaptureHealthRepository, opts captureHealthReporterOptions) *captureHealthReporter {
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.flushInterval <= 0 {
		opts.flushInterval = time.Minute
	}
	if opts.cleanupInterval <= 0 {
		opts.cleanupInterval = time.Hour
	}
	if opts.retention <= 0 {
		opts.retention = 30 * 24 * time.Hour
	}
	if opts.pendingCapacity <= 0 {
		opts.pendingCapacity = captureHealthPendingCapacity
	}
	if opts.maxBatchSize <= 0 {
		opts.maxBatchSize = captureHealthRetryBatchSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &captureHealthReporter{
		tracker: tracker, repo: repo, now: opts.now,
		flushInterval: opts.flushInterval, cleanupInterval: opts.cleanupInterval, retention: opts.retention,
		pendingCapacity: opts.pendingCapacity, maxBatchSize: opts.maxBatchSize,
		ctx: ctx, cancel: cancel, pending: make(map[captureHealthBucketKey]CaptureHealthEvent),
	}
}

func (r *captureHealthReporter) Start() {
	if r == nil || r.repo == nil || r.tracker == nil {
		return
	}
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go r.run()
	})
}

func (r *captureHealthReporter) run() {
	defer r.wg.Done()
	flushTicker := time.NewTicker(r.flushInterval)
	cleanupTicker := time.NewTicker(r.cleanupInterval)
	defer flushTicker.Stop()
	defer cleanupTicker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-flushTicker.C:
			if err := r.flushOnce(r.ctx, false); err != nil {
				logger.L().Warn("capture.health_flush_failed", zap.Error(err))
			}
		case <-cleanupTicker.C:
			if err := r.cleanupOnce(r.ctx); err != nil {
				logger.L().Warn("capture.health_cleanup_failed", zap.Error(err))
			}
		}
	}
}

func (r *captureHealthReporter) flushOnce(ctx context.Context, includeCurrent bool) error {
	if r == nil || r.repo == nil || r.tracker == nil {
		return nil
	}
	events := r.tracker.takeBucketsBefore(r.now().UTC().Truncate(time.Minute), includeCurrent)
	r.mu.Lock()
	evicted := uint64(0)
	for _, event := range events {
		key := captureHealthBucketKey{minute: event.MinuteBucket, reason: CaptureDropReason(event.Reason)}
		if _, exists := r.pending[key]; !exists && len(r.pending) >= r.pendingCapacity {
			if oldest, ok := oldestCaptureHealthBucketKey(r.pending); ok {
				delete(r.pending, oldest)
				evicted++
			}
		}
		pending := r.pending[key]
		pending.MinuteBucket = event.MinuteBucket
		pending.InstanceID = event.InstanceID
		pending.Reason = event.Reason
		pending.DroppedRecords += event.DroppedRecords
		pending.DroppedBytes += event.DroppedBytes
		pending.WorkerQueuePeak = maxInt64(pending.WorkerQueuePeak, event.WorkerQueuePeak)
		pending.WriterQueuePeak = maxInt64(pending.WriterQueuePeak, event.WriterQueuePeak)
		pending.InFlightBytePeak = maxInt64(pending.InFlightBytePeak, event.InFlightBytePeak)
		if event.LastError != "" {
			pending.LastError = event.LastError
		}
		r.pending[key] = pending
	}
	if evicted > 0 {
		r.tracker.recordHistoryBucketsDropped(evicted)
	}
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return nil
	}
	keys := make([]captureHealthBucketKey, 0, len(r.pending))
	for key := range r.pending {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].minute.Equal(keys[j].minute) {
			return keys[i].reason < keys[j].reason
		}
		return keys[i].minute.Before(keys[j].minute)
	})
	if len(keys) > r.maxBatchSize {
		keys = keys[:r.maxBatchSize]
	}
	batch := make([]CaptureHealthEvent, 0, len(keys))
	for _, key := range keys {
		batch = append(batch, r.pending[key])
	}
	r.mu.Unlock()
	if evicted > 0 {
		logger.L().Warn("capture.health_history_pending_overflow", zap.Uint64("evicted_buckets", evicted))
	}

	dbCtx, cancel := captureHealthDBContext(ctx)
	defer cancel()
	if err := r.repo.UpsertEvents(dbCtx, batch); err != nil {
		return fmt.Errorf("upsert capture health events: %w", err)
	}
	r.mu.Lock()
	for _, event := range batch {
		delete(r.pending, captureHealthBucketKey{minute: event.MinuteBucket, reason: CaptureDropReason(event.Reason)})
	}
	r.mu.Unlock()
	return nil
}

func oldestCaptureHealthBucketKey(pending map[captureHealthBucketKey]CaptureHealthEvent) (captureHealthBucketKey, bool) {
	var oldest captureHealthBucketKey
	found := false
	for key := range pending {
		if !found || key.minute.Before(oldest.minute) || (key.minute.Equal(oldest.minute) && key.reason < oldest.reason) {
			oldest = key
			found = true
		}
	}
	return oldest, found
}

func (r *captureHealthReporter) cleanupOnce(ctx context.Context) error {
	if r == nil || r.repo == nil {
		return nil
	}
	dbCtx, cancel := captureHealthDBContext(ctx)
	defer cancel()
	_, err := r.repo.DeleteBefore(dbCtx, r.now().UTC().Add(-r.retention))
	if err != nil {
		return fmt.Errorf("cleanup capture health events: %w", err)
	}
	return nil
}

func captureHealthDBContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(ctx, captureHealthRepositoryTimeout)
}

func (r *captureHealthReporter) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.cancel()
		r.wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), captureHealthRepositoryTimeout)
		defer cancel()
		if err := r.flushOnce(ctx, true); err != nil {
			logger.L().Warn("capture.health_final_flush_failed", zap.Error(err))
		}
	})
}
