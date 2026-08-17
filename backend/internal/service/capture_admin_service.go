package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/sidecar"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

var (
	ErrCaptureInfrastructureNotReady = errors.New("capture infrastructure is not ready")
	ErrInvalidCaptureHistoryRange    = errors.New("capture history range must be 24h, 7d, or 30d")
	ErrInvalidCapturePolicy          = errors.New("invalid capture policy")
)

type CaptureSettingsView struct {
	Policy                CaptureRuntimePolicy `json:"policy"`
	Provisioned           bool                 `json:"provisioned"`
	Ready                 bool                 `json:"ready"`
	SidecarRunning        bool                 `json:"sidecar_running"`
	SpoolReady            bool                 `json:"spool_ready"`
	DeliveryReady         bool                 `json:"delivery_ready"`
	SpoolUsedBytes        int64                `json:"spool_used_bytes"`
	SpoolMaxBytes         int64                `json:"spool_max_bytes"`
	SpoolMinFreeBytes     int64                `json:"spool_min_free_bytes"`
	FilesystemFreeBytes   int64                `json:"filesystem_free_bytes"`
	ReadyRecords          int64                `json:"ready_records"`
	OldestReadyAgeSeconds int64                `json:"oldest_ready_age_seconds"`
	CurrentBatchID        string               `json:"current_batch_id"`
	SidecarRestartCount   uint64               `json:"sidecar_restart_count"`
	UploadRetries         uint64               `json:"upload_retries"`
	LastUploadAt          *time.Time           `json:"last_upload_at"`
	HealthSourceID        string               `json:"health_source_id"`
	DroppedRecords        uint64               `json:"dropped_records"`
	DroppedByReason       map[string]uint64    `json:"dropped_by_reason"`
	InitializationError   string               `json:"initialization_error,omitempty"`
	Database              string               `json:"database"`
	Table                 string               `json:"table"`
}

type CaptureHealthHistory struct {
	Range  string               `json:"range"`
	Start  time.Time            `json:"start"`
	End    time.Time            `json:"end"`
	Events []CaptureHealthEvent `json:"events"`
}

type CaptureAdminService struct {
	cfg                  *config.Config
	settingService       *SettingService
	capturePool          *ConversationCapturePool
	healthRepo           CaptureHealthRepository
	supervisor           *CaptureSidecarSupervisor
	readStatusCheckpoint func(string) (model.Status, bool, error)
	now                  func() time.Time
}

func NewCaptureAdminService(
	cfg *config.Config,
	settingService *SettingService,
	capturePool *ConversationCapturePool,
	healthRepo CaptureHealthRepository,
	supervisor *CaptureSidecarSupervisor,
) *CaptureAdminService {
	return &CaptureAdminService{
		cfg: cfg, settingService: settingService, capturePool: capturePool, healthRepo: healthRepo,
		supervisor: supervisor, readStatusCheckpoint: sidecar.ReadStatusCheckpoint, now: time.Now,
	}
}

func (s *CaptureAdminService) Get(ctx context.Context) (*CaptureSettingsView, error) {
	if s == nil || s.settingService == nil {
		return nil, errors.New("capture settings service is unavailable")
	}
	policy, err := s.settingService.GetCaptureRuntimePolicy(ctx)
	if err != nil {
		return nil, err
	}
	return s.buildView(ctx, policy), nil
}

func (s *CaptureAdminService) Update(ctx context.Context, policy CaptureRuntimePolicy) (*CaptureSettingsView, error) {
	if s == nil || s.settingService == nil {
		return nil, errors.New("capture settings service is unavailable")
	}
	normalized, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCapturePolicy, err)
	}
	if normalized.Enabled && !s.infrastructureReady(ctx) {
		return nil, ErrCaptureInfrastructureNotReady
	}
	updated, err := s.settingService.UpdateCaptureRuntimePolicy(ctx, normalized)
	if err != nil {
		return nil, err
	}
	return s.buildView(ctx, updated), nil
}

func (s *CaptureAdminService) History(ctx context.Context, selectedRange string) (*CaptureHealthHistory, error) {
	duration, ok := captureHistoryDuration(selectedRange)
	if !ok {
		return nil, ErrInvalidCaptureHistoryRange
	}
	if s == nil || s.healthRepo == nil {
		return nil, errors.New("capture health repository is unavailable")
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	end := now().UTC()
	start := end.Add(-duration)
	events, err := s.healthRepo.ListEvents(ctx, start, end)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].LastError = safeStoredCaptureHealthError(CaptureDropReason(events[i].Reason), events[i].LastError)
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].MinuteBucket.Equal(events[j].MinuteBucket) {
			if events[i].InstanceID == events[j].InstanceID {
				return events[i].Reason < events[j].Reason
			}
			return events[i].InstanceID < events[j].InstanceID
		}
		return events[i].MinuteBucket.After(events[j].MinuteBucket)
	})
	if events == nil {
		events = []CaptureHealthEvent{}
	}
	return &CaptureHealthHistory{Range: selectedRange, Start: start, End: end, Events: events}, nil
}

func captureHistoryDuration(selectedRange string) (time.Duration, bool) {
	switch selectedRange {
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func (s *CaptureAdminService) infrastructureReady(ctx context.Context) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled {
		return false
	}
	status, found := s.captureStatus(ctx)
	return found && s.supervisorRunning() && status.SpoolReady
}

func (s *CaptureAdminService) buildView(ctx context.Context, policy CaptureRuntimePolicy) *CaptureSettingsView {
	view := &CaptureSettingsView{
		Policy:          policy,
		DroppedByReason: map[string]uint64{},
		Provisioned:     s != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled,
	}
	if s == nil {
		return view
	}
	if s.cfg == nil {
		return view
	}
	cc := s.cfg.Gateway.Capture
	view.Database = cc.ClickHouse.Database
	view.Table = cc.ClickHouse.Table
	view.SpoolMinFreeBytes = nonnegativeInt64(cc.Spool.MinFreeBytes)
	status, found := s.captureStatus(ctx)
	view.SidecarRunning = s.supervisorRunning()
	view.SpoolReady = status.SpoolReady
	view.DeliveryReady = found && view.SidecarRunning && status.DeliveryReady
	view.SpoolUsedBytes = nonnegativeInt64(status.SpoolUsedBytes)
	view.SpoolMaxBytes = nonnegativeInt64(status.SpoolMaxBytes)
	view.FilesystemFreeBytes = nonnegativeInt64(status.FilesystemFreeBytes)
	view.ReadyRecords = nonnegativeInt64(status.ReadyRecords)
	view.OldestReadyAgeSeconds = nonnegativeInt64(status.OldestReadyAgeSeconds)
	view.CurrentBatchID = status.CurrentBatchID
	view.SidecarRestartCount = s.supervisorStatus().RestartCount
	view.UploadRetries = status.UploadRetries
	view.LastUploadAt = status.LastUploadAt
	if status.HealthSourceID != uuid.Nil {
		view.HealthSourceID = status.HealthSourceID.String()
	}
	view.DroppedRecords = status.DroppedRecords
	for reason, count := range status.DroppedByReason {
		if isCaptureOperationalDropReason(reason) {
			view.DroppedByReason[reason] = count
		}
	}
	view.Ready = found && view.SidecarRunning && view.SpoolReady
	if view.Provisioned && (!view.SidecarRunning || !found) {
		view.InitializationError = "Capture sidecar is unavailable; check server logs"
	}
	return view
}

func (s *CaptureAdminService) captureStatus(ctx context.Context) (model.Status, bool) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Capture.Enabled {
		return model.Status{}, false
	}
	if s.capturePool != nil && (s.supervisor == nil || s.supervisor.Status().Running) {
		if status, err := s.capturePool.Status(ctx); err == nil {
			return status, true
		}
	}
	if s.readStatusCheckpoint != nil {
		path := sidecar.StatusCheckpointPath(s.cfg.Gateway.Capture.Spool.Dir)
		if status, found, err := s.readStatusCheckpoint(path); err == nil && found {
			if s.capturePool != nil {
				status = s.capturePool.withObservedLosses(status)
			}
			return status, true
		}
	}
	return model.Status{}, false
}

func (s *CaptureAdminService) supervisorStatus() CaptureSidecarSupervisorStatus {
	if s == nil || s.supervisor == nil {
		return CaptureSidecarSupervisorStatus{}
	}
	return s.supervisor.Status()
}

func (s *CaptureAdminService) supervisorRunning() bool {
	if s == nil {
		return false
	}
	if s.supervisor != nil {
		return s.supervisor.Status().Running
	}
	return s.capturePool != nil && s.capturePool.Ready()
}
