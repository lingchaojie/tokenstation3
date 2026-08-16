package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
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
func (r *captureHealthRepoStub) ListLatestEventsBefore(context.Context, time.Time, []string, []string) ([]CaptureHealthEvent, error) {
	return nil, nil
}

func TestCaptureSettingsCannotEnableWhenSidecarInfrastructureIsNotReady(t *testing.T) {
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(&config.Config{}, settings, nil, &captureHealthRepoStub{}, nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true

	_, err := svc.Update(context.Background(), policy)
	require.ErrorIs(t, err, ErrCaptureInfrastructureNotReady)
}

func TestCaptureSettingsAllowsDisabledPolicyWithoutProvisioning(t *testing.T) {
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(&config.Config{}, settings, nil, &captureHealthRepoStub{}, nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Platforms.OpenAI = true

	got, err := svc.Update(context.Background(), policy)
	require.NoError(t, err)
	require.False(t, got.Policy.Enabled)
	require.True(t, got.Policy.Platforms.OpenAI)
	require.False(t, got.Provisioned)
	require.False(t, got.Ready)
}

func TestCaptureSettingsViewDoesNotExposeClickHouseAddressOrCredentials(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.ClickHouse.Addr = []string{
		"clickhouse://archive-user:super-secret@db.example.com:9440/llm?secure=true",
		"other-user:other-secret@10.0.0.8:9000",
	}
	cfg.Gateway.Capture.ClickHouse.Database = "llm_archive"
	cfg.Gateway.Capture.ClickHouse.Table = "model_call_archive"
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(cfg, settings, nil, &captureHealthRepoStub{}, nil)

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "llm_archive", got.Database)
	require.Equal(t, "model_call_archive", got.Table)
	require.NotContains(t, fmt.Sprintf("%+v", got), "super-secret")
	require.NotContains(t, fmt.Sprintf("%+v", got), "db.example.com")
}

func TestCaptureSettingsHistoryValidatesRangeAndSortsNewestFirst(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repo := &captureHealthRepoStub{events: []CaptureHealthEvent{
		{MinuteBucket: now.Add(-2 * time.Hour), Reason: "older"},
		{MinuteBucket: now.Add(-time.Hour), Reason: "newer"},
	}}
	svc := NewCaptureAdminService(&config.Config{}, NewSettingService(&capturePolicyRepoStub{}, nil), nil, repo, nil)
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
	svc := NewCaptureAdminService(&config.Config{}, NewSettingService(&capturePolicyRepoStub{}, nil), nil, repo, nil)
	svc.now = func() time.Time { return now }

	got, err := svc.History(context.Background(), "24h")
	require.NoError(t, err)
	require.Len(t, got.Events, 1)
	require.Equal(t, "ClickHouse batch send failed", got.Events[0].LastError)
	require.NotContains(t, got.Events[0].LastError, "super-secret")
	require.NotContains(t, got.Events[0].LastError, "db.internal")
}

type captureAdminStatusTransport struct {
	status model.Status
	err    error
	calls  int
}

func (*captureAdminStatusTransport) Begin(context.Context, model.Begin) (protocol.Attempt, error) {
	return nil, errors.New("unused")
}
func (t *captureAdminStatusTransport) Status(context.Context) (model.Status, error) {
	t.calls++
	return t.status, t.err
}
func (*captureAdminStatusTransport) Close() error { return nil }

func TestCaptureSettingsViewUsesLiveStatusBeforeCheckpointAndSeparatesDelivery(t *testing.T) {
	sourceID := uuid.New()
	live := model.Status{
		HealthSourceID: sourceID, SpoolReady: true, DeliveryReady: false,
		SpoolUsedBytes: 9 << 30, SpoolMaxBytes: 12 << 30, FilesystemFreeBytes: 10 << 30,
		ReadyRecords: 42, OldestReadyAgeSeconds: 90, CurrentBatchID: uuid.NewString(), UploadRetries: 7,
		DroppedRecords: 3, DroppedByReason: map[string]uint64{"spool_cap": 3},
	}
	transport := &captureAdminStatusTransport{status: live}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	supervisor := &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true, RestartCount: 2}}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.Spool.Dir = "/app/data/capture/spool"
	cfg.Gateway.Capture.Spool.MinFreeBytes = 8 << 30
	settings := NewSettingService(&capturePolicyRepoStub{}, nil)
	svc := NewCaptureAdminService(cfg, settings, pool, &captureHealthRepoStub{}, supervisor)
	svc.readStatusCheckpoint = func(string) (model.Status, bool, error) {
		return model.Status{SpoolReady: false, SpoolUsedBytes: 1}, true, nil
	}

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, transport.calls)
	require.True(t, got.Ready, "local acceptance does not depend on remote delivery")
	require.True(t, got.SidecarRunning)
	require.True(t, got.SpoolReady)
	require.False(t, got.DeliveryReady)
	require.EqualValues(t, 9<<30, got.SpoolUsedBytes)
	require.EqualValues(t, 12<<30, got.SpoolMaxBytes)
	require.EqualValues(t, 10<<30, got.FilesystemFreeBytes)
	require.EqualValues(t, 8<<30, got.SpoolMinFreeBytes)
	require.EqualValues(t, 42, got.ReadyRecords)
	require.EqualValues(t, 90, got.OldestReadyAgeSeconds)
	require.Equal(t, live.CurrentBatchID, got.CurrentBatchID)
	require.EqualValues(t, 2, got.SidecarRestartCount)
	require.EqualValues(t, 7, got.UploadRetries)
	require.EqualValues(t, 3, got.DroppedRecords)
	require.Equal(t, sourceID.String(), got.HealthSourceID)
}

func TestCaptureSettingsViewFallsBackToCheckpointWhenSupervisorIsDown(t *testing.T) {
	checkpoint := model.Status{
		HealthSourceID: uuid.New(), SpoolReady: true, DeliveryReady: true,
		SpoolUsedBytes: 1234, SpoolMaxBytes: 5678, ReadyRecords: 9, UploadRetries: 4,
	}
	transport := &captureAdminStatusTransport{status: model.Status{
		HealthSourceID: uuid.New(), SpoolReady: false, SpoolUsedBytes: 9999,
	}}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	supervisor := &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: false, RestartCount: 5, LastErrorClass: "exit_failed"}}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.Spool.Dir = "/app/data/capture/spool"
	svc := NewCaptureAdminService(cfg, NewSettingService(&capturePolicyRepoStub{}, nil), pool, &captureHealthRepoStub{}, supervisor)
	var checkpointPath string
	svc.readStatusCheckpoint = func(path string) (model.Status, bool, error) {
		checkpointPath = path
		return checkpoint, true, nil
	}

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.False(t, got.SidecarRunning)
	require.False(t, got.Ready)
	require.True(t, got.SpoolReady, "checkpoint gauges remain visible while the sidecar is down")
	require.EqualValues(t, 1234, got.SpoolUsedBytes)
	require.Zero(t, transport.calls, "a supervisor-down process cannot be a trusted live status source")
	require.EqualValues(t, 5, got.SidecarRestartCount)
	require.Equal(t, "/app/data/capture/status.json", checkpointPath)
	require.NotContains(t, got.InitializationError, "secret")
}

func TestCaptureSettingsViewUsesCheckpointReadinessWhenLiveStatusFailsButSupervisorRuns(t *testing.T) {
	checkpoint := model.Status{
		HealthSourceID: uuid.New(), SpoolReady: true, DeliveryReady: true,
		SpoolUsedBytes: 1234, SpoolMaxBytes: 5678,
	}
	transport := &captureAdminStatusTransport{err: errors.New("temporary status timeout")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	supervisor := &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: true}}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.Spool.Dir = "/app/data/capture/spool"
	svc := NewCaptureAdminService(cfg, NewSettingService(&capturePolicyRepoStub{}, nil), pool, &captureHealthRepoStub{}, supervisor)
	svc.readStatusCheckpoint = func(string) (model.Status, bool, error) { return checkpoint, true, nil }

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.True(t, got.SidecarRunning)
	require.True(t, got.SpoolReady)
	require.True(t, got.DeliveryReady)
	require.True(t, got.Ready)
	require.EqualValues(t, 1234, got.SpoolUsedBytes)
	require.Equal(t, 1, transport.calls)
}

func TestCaptureSettingsViewUsesZeroStatusForMissingOrCorruptCheckpoint(t *testing.T) {
	for name, read := range map[string]func(string) (model.Status, bool, error){
		"missing": func(string) (model.Status, bool, error) { return model.Status{}, false, nil },
		"corrupt": func(string) (model.Status, bool, error) {
			return model.Status{}, false, errors.New("invalid checkpoint raw-body=private")
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.Spool.Dir = "/app/data/capture/spool"
			svc := NewCaptureAdminService(cfg, NewSettingService(&capturePolicyRepoStub{}, nil), nil, &captureHealthRepoStub{}, &CaptureSidecarSupervisor{})
			svc.readStatusCheckpoint = read
			got, err := svc.Get(context.Background())
			require.NoError(t, err)
			require.False(t, got.Ready)
			require.False(t, got.SpoolReady)
			require.Zero(t, got.SpoolUsedBytes)
			require.NotContains(t, got.InitializationError, "private")
		})
	}
}
