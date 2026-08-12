package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var (
	ErrCaptureInfrastructureNotReady = errors.New("capture infrastructure is not ready")
	ErrInvalidCaptureHistoryRange    = errors.New("capture history range must be 24h, 7d, or 30d")
	ErrInvalidCapturePolicy          = errors.New("invalid capture policy")
)

type CaptureCapacitySettings struct {
	MaxBodyBytes          int    `json:"max_body_bytes"`
	MaxQueueBytes         int64  `json:"max_queue_bytes"`
	QueueSize             int    `json:"queue_size"`
	WorkerCount           int    `json:"worker_count"`
	WriterQueueSize       int    `json:"writer_queue_size"`
	OverflowPolicy        string `json:"overflow_policy"`
	OverflowSamplePercent int    `json:"overflow_sample_percent"`
	BatchMaxSize          int    `json:"batch_max_size"`
	BatchMaxIntervalMs    int    `json:"batch_max_interval_ms"`
}

type CaptureSettingsView struct {
	Policy              CaptureRuntimePolicy    `json:"policy"`
	Provisioned         bool                    `json:"provisioned"`
	Ready               bool                    `json:"ready"`
	InitializationError string                  `json:"initialization_error,omitempty"`
	Addresses           []string                `json:"addresses"`
	Database            string                  `json:"database"`
	Table               string                  `json:"table"`
	Capacity            CaptureCapacitySettings `json:"capacity"`
	Health              CaptureHealthSnapshot   `json:"health"`
}

type CaptureHealthHistory struct {
	Range  string               `json:"range"`
	Start  time.Time            `json:"start"`
	End    time.Time            `json:"end"`
	Events []CaptureHealthEvent `json:"events"`
}

type CaptureAdminService struct {
	cfg            *config.Config
	settingService *SettingService
	capturePool    *ConversationCapturePool
	healthRepo     CaptureHealthRepository
	now            func() time.Time
}

func NewCaptureAdminService(
	cfg *config.Config,
	settingService *SettingService,
	capturePool *ConversationCapturePool,
	healthRepo CaptureHealthRepository,
) *CaptureAdminService {
	return &CaptureAdminService{
		cfg: cfg, settingService: settingService, capturePool: capturePool, healthRepo: healthRepo, now: time.Now,
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
	return s.buildView(policy), nil
}

func (s *CaptureAdminService) Update(ctx context.Context, policy CaptureRuntimePolicy) (*CaptureSettingsView, error) {
	if s == nil || s.settingService == nil {
		return nil, errors.New("capture settings service is unavailable")
	}
	normalized, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCapturePolicy, err)
	}
	if normalized.Enabled && !s.infrastructureReady() {
		return nil, ErrCaptureInfrastructureNotReady
	}
	updated, err := s.settingService.UpdateCaptureRuntimePolicy(ctx, normalized)
	if err != nil {
		return nil, err
	}
	return s.buildView(updated), nil
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

func (s *CaptureAdminService) infrastructureReady() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && s.capturePool != nil && s.capturePool.Ready()
}

func (s *CaptureAdminService) buildView(policy CaptureRuntimePolicy) *CaptureSettingsView {
	view := &CaptureSettingsView{
		Policy:      policy,
		Addresses:   []string{},
		Health:      CaptureHealthSnapshot{DroppedByReason: map[string]CaptureReasonStats{}, RecentIncidents: []CaptureLossIncident{}},
		Provisioned: s != nil && s.cfg != nil && s.cfg.Gateway.Capture.Enabled && s.capturePool != nil,
		Ready:       s.infrastructureReady(),
	}
	if s == nil {
		return view
	}
	if s.capturePool != nil {
		if s.capturePool.InitializationError() != "" {
			view.InitializationError = "ClickHouse initialization failed; check server logs"
		}
		view.Health = s.capturePool.Health()
	}
	if s.cfg == nil {
		return view
	}
	cc := s.cfg.Gateway.Capture
	view.Database = cc.ClickHouse.Database
	view.Table = cc.ClickHouse.Table
	view.Addresses = redactCaptureAddresses(cc.ClickHouse.Addr)
	view.Capacity = CaptureCapacitySettings{
		MaxBodyBytes: cc.MaxBodyBytes, MaxQueueBytes: cc.MaxQueueBytes, QueueSize: cc.QueueSize,
		WorkerCount: cc.WorkerCount, WriterQueueSize: cc.WriterQueueSize,
		OverflowPolicy: cc.OverflowPolicy, OverflowSamplePercent: cc.OverflowSamplePercent,
		BatchMaxSize: cc.BatchMaxSize, BatchMaxIntervalMs: cc.BatchMaxIntervalMs,
	}
	return view
}

func redactCaptureAddresses(addresses []string) []string {
	redacted := make([]string, 0, len(addresses))
	for _, raw := range addresses {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		withScheme := strings.Contains(raw, "://")
		candidate := raw
		if !withScheme {
			candidate = "//" + raw
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			redacted = append(redacted, "<redacted>")
			continue
		}
		if withScheme {
			redacted = append(redacted, parsed.Scheme+"://"+parsed.Host)
		} else {
			redacted = append(redacted, parsed.Host)
		}
	}
	return redacted
}
