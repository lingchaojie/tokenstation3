package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const captureHealthRepositoryTimeout = 5 * time.Second
const captureHealthPendingCapacity = 4096
const captureHealthRetryBatchSize = 256

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
