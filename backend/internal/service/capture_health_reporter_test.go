package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type captureHealthRepoFake struct {
	mu          sync.Mutex
	failUpserts int
	upserts     [][]CaptureHealthEvent
	cutoffs     []time.Time
}

func (r *captureHealthRepoFake) UpsertEvents(_ context.Context, events []CaptureHealthEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyEvents := append([]CaptureHealthEvent(nil), events...)
	r.upserts = append(r.upserts, copyEvents)
	if r.failUpserts > 0 {
		r.failUpserts--
		return errors.New("postgres unavailable")
	}
	return nil
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
