package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type captureHealthRepoStub struct {
	events []CaptureHealthEvent
	err    error
	start  time.Time
	end    time.Time
}

func (r *captureHealthRepoStub) UpsertEvents(context.Context, []CaptureHealthEvent) error { return nil }
func (r *captureHealthRepoStub) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (r *captureHealthRepoStub) ListEvents(_ context.Context, start, end time.Time) ([]CaptureHealthEvent, error) {
	r.start, r.end = start, end
	return append([]CaptureHealthEvent(nil), r.events...), r.err
}

func TestCaptureSettingsCannotEnableWhenWriterIsNotReady(t *testing.T) {
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(&config.Config{}, settings, nil, &captureHealthRepoStub{})
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true

	_, err := svc.Update(context.Background(), policy)
	require.ErrorIs(t, err, ErrCaptureInfrastructureNotReady)
}

func TestCaptureSettingsAllowsDisabledPolicyWithoutProvisioning(t *testing.T) {
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(&config.Config{}, settings, nil, &captureHealthRepoStub{})
	policy := DefaultCaptureRuntimePolicy()
	policy.Platforms.OpenAI = true

	got, err := svc.Update(context.Background(), policy)
	require.NoError(t, err)
	require.False(t, got.Policy.Enabled)
	require.True(t, got.Policy.Platforms.OpenAI)
	require.False(t, got.Provisioned)
	require.False(t, got.Ready)
}

func TestCaptureSettingsViewRedactsClickHouseCredentials(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.ClickHouse.Addr = []string{
		"clickhouse://archive-user:super-secret@db.example.com:9440/llm?secure=true",
		"other-user:other-secret@10.0.0.8:9000",
	}
	cfg.Gateway.Capture.ClickHouse.Database = "llm_archive"
	cfg.Gateway.Capture.ClickHouse.Table = "model_call_archive"
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(cfg, settings, nil, &captureHealthRepoStub{})

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"clickhouse://db.example.com:9440", "10.0.0.8:9000"}, got.Addresses)
	require.Equal(t, "llm_archive", got.Database)
	require.Equal(t, "model_call_archive", got.Table)
}

func TestCaptureSettingsViewDoesNotExposeInitializationErrorDetails(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	pool := newConversationCapturePoolWithState(
		conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 1, WriterQueueSize: 1},
		unavailableArchiveWriter{},
		newCaptureHealthTracker("test", time.Now),
		nil,
		false,
		"dial clickhouse://archive:super-secret@db.example.com:9000 failed",
	)
	t.Cleanup(pool.Stop)
	svc := NewCaptureAdminService(cfg, NewSettingService(&capturePolicyRepoStub{}, nil), pool, &captureHealthRepoStub{})

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.True(t, got.Provisioned)
	require.False(t, got.Ready)
	require.Equal(t, "ClickHouse initialization failed; check server logs", got.InitializationError)
}

func TestCaptureSettingsHistoryValidatesRangeAndSortsNewestFirst(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repo := &captureHealthRepoStub{events: []CaptureHealthEvent{
		{MinuteBucket: now.Add(-2 * time.Hour), Reason: "older"},
		{MinuteBucket: now.Add(-time.Hour), Reason: "newer"},
	}}
	svc := NewCaptureAdminService(&config.Config{}, NewSettingService(&capturePolicyRepoStub{}, nil), nil, repo)
	svc.now = func() time.Time { return now }

	got, err := svc.History(context.Background(), "24h")
	require.NoError(t, err)
	require.Equal(t, "24h", got.Range)
	require.Equal(t, now.Add(-24*time.Hour), repo.start)
	require.Equal(t, now, repo.end)
	require.Equal(t, []string{"newer", "older"}, []string{got.Events[0].Reason, got.Events[1].Reason})

	_, err = svc.History(context.Background(), "1h")
	require.ErrorIs(t, err, ErrInvalidCaptureHistoryRange)
	require.False(t, errors.Is(err, ErrCaptureInfrastructureNotReady))
}

func TestCaptureSettingsHistoryReclassifiesStoredErrorBeforeReturningIt(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repo := &captureHealthRepoStub{events: []CaptureHealthEvent{{
		MinuteBucket: now.Add(-time.Minute),
		Reason:       string(CaptureDropClickHouseSendFailed),
		LastError:    "dial clickhouse://archive:super-secret@db.internal:9000 failed",
	}}}
	svc := NewCaptureAdminService(&config.Config{}, NewSettingService(&capturePolicyRepoStub{}, nil), nil, repo)
	svc.now = func() time.Time { return now }

	got, err := svc.History(context.Background(), "24h")
	require.NoError(t, err)
	require.Len(t, got.Events, 1)
	require.Equal(t, "ClickHouse batch send failed", got.Events[0].LastError)
	require.NotContains(t, got.Events[0].LastError, "super-secret")
	require.NotContains(t, got.Events[0].LastError, "db.internal")
}
