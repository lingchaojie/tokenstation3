package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const DailyCheckInRewardDefault = 10.0

var ErrDailyCheckInConfigInvalid = infraerrors.BadRequest(
	"DAILY_CHECK_IN_CONFIG_INVALID",
	"daily check-in config is invalid",
)

var ErrDailyCheckInAlreadyClaimed = infraerrors.Conflict(
	"DAILY_CHECK_IN_ALREADY_CLAIMED",
	"daily check-in reward already claimed",
)

var (
	ErrDailyCheckInInactive = infraerrors.Conflict(
		"DAILY_CHECK_IN_INACTIVE",
		"daily check-in activity is not active",
	)
	ErrDailyCheckInUserOnly = infraerrors.Forbidden(
		"DAILY_CHECK_IN_USER_ONLY",
		"daily check-in is only available to ordinary users",
	)
)

type DailyCheckInActivityState string

const (
	DailyCheckInStateDisabled DailyCheckInActivityState = "disabled"
	DailyCheckInStateUpcoming DailyCheckInActivityState = "upcoming"
	DailyCheckInStateActive   DailyCheckInActivityState = "active"
	DailyCheckInStateEnded    DailyCheckInActivityState = "ended"
)

type DailyCheckInConfig struct {
	Enabled      bool       `json:"enabled"`
	StartAt      *time.Time `json:"start_at"`
	DurationDays int        `json:"duration_days"`
	RewardAmount float64    `json:"reward_amount"`
}

type DailyCheckInClaim struct {
	ID              int64
	UserID          int64
	ActivityStartAt time.Time
	CheckInDate     time.Time
	RewardAmount    float64
	BalanceAfter    float64
	ClaimedAt       time.Time
}

type DailyCheckInClaimInput struct {
	UserID          int64
	ActivityStartAt time.Time
	CheckInDate     time.Time
	RewardAmount    float64
	ClaimedAt       time.Time
	ExpiresAt       time.Time
}

type CheckInRepository interface {
	FindClaim(ctx context.Context, userID int64, activityStartAt, checkInDate time.Time) (*DailyCheckInClaim, error)
	CreateClaim(ctx context.Context, input DailyCheckInClaimInput) (*DailyCheckInClaim, error)
}

type DailyCheckInStatus struct {
	State        DailyCheckInActivityState
	StartAt      *time.Time
	EndAt        *time.Time
	RewardAmount float64
	CheckInDate  string
	ClaimedToday bool
	Claim        *DailyCheckInClaim
	NextResetAt  time.Time
}

type DailyCheckInClaimResult struct {
	RewardAmount float64
	BalanceAfter float64
	CheckInDate  string
	ClaimedAt    time.Time
}

type CheckInService struct {
	repo                 CheckInRepository
	settings             *SettingService
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCache         BillingCache
	now                  func() time.Time
}

func NewCheckInService(
	repo CheckInRepository,
	settings *SettingService,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache BillingCache,
) *CheckInService {
	return &CheckInService{
		repo:                 repo,
		settings:             settings,
		authCacheInvalidator: authCacheInvalidator,
		billingCache:         billingCache,
		now:                  time.Now,
	}
}

func (c DailyCheckInConfig) EndAt() *time.Time {
	if c.StartAt == nil || c.DurationDays <= 0 {
		return nil
	}
	end := c.StartAt.UTC().Add(time.Duration(c.DurationDays) * 24 * time.Hour)
	return &end
}

func ValidateDailyCheckInConfig(c DailyCheckInConfig) error {
	if c.DurationDays < 0 || c.RewardAmount <= 0 || math.IsNaN(c.RewardAmount) || math.IsInf(c.RewardAmount, 0) {
		return ErrDailyCheckInConfigInvalid
	}
	if decimal.NewFromFloat(c.RewardAmount).Exponent() < -8 {
		return ErrDailyCheckInConfigInvalid
	}
	if c.Enabled && (c.StartAt == nil || c.DurationDays <= 0) {
		return ErrDailyCheckInConfigInvalid
	}
	return nil
}

func (s *SettingService) GetDailyCheckInConfig(ctx context.Context) (DailyCheckInConfig, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyDailyCheckInEnabled,
		SettingKeyDailyCheckInStartAt,
		SettingKeyDailyCheckInDurationDays,
		SettingKeyDailyCheckInRewardAmount,
	})
	if err != nil {
		return DailyCheckInConfig{}, fmt.Errorf("get daily check-in config: %w", err)
	}

	cfg := DailyCheckInConfig{RewardAmount: DailyCheckInRewardDefault}
	valid := true
	if raw, ok := values[SettingKeyDailyCheckInEnabled]; ok {
		cfg.Enabled, err = strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			cfg.Enabled = false
			valid = false
		}
	}
	if raw := strings.TrimSpace(values[SettingKeyDailyCheckInStartAt]); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			valid = false
		} else {
			parsed = parsed.UTC()
			cfg.StartAt = &parsed
		}
	}
	if raw := strings.TrimSpace(values[SettingKeyDailyCheckInDurationDays]); raw != "" {
		cfg.DurationDays, err = strconv.Atoi(raw)
		if err != nil {
			cfg.DurationDays = 0
			valid = false
		}
	}
	if raw := strings.TrimSpace(values[SettingKeyDailyCheckInRewardAmount]); raw != "" {
		cfg.RewardAmount, err = strconv.ParseFloat(raw, 64)
		if err != nil {
			cfg.RewardAmount = DailyCheckInRewardDefault
			valid = false
		}
	}
	if !valid || ValidateDailyCheckInConfig(cfg) != nil {
		cfg.Enabled = false
	}
	return cfg, nil
}

func (s *SettingService) UpdateDailyCheckInConfig(ctx context.Context, cfg DailyCheckInConfig) error {
	if err := ValidateDailyCheckInConfig(cfg); err != nil {
		return err
	}

	startAt := ""
	if cfg.StartAt != nil {
		startAt = cfg.StartAt.UTC().Format(time.RFC3339)
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyDailyCheckInEnabled:      strconv.FormatBool(cfg.Enabled),
		SettingKeyDailyCheckInStartAt:      startAt,
		SettingKeyDailyCheckInDurationDays: strconv.Itoa(cfg.DurationDays),
		SettingKeyDailyCheckInRewardAmount: strconv.FormatFloat(cfg.RewardAmount, 'f', 8, 64),
	}); err != nil {
		return fmt.Errorf("update daily check-in config: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

var utcPlus8 = time.FixedZone("UTC+8", 8*60*60)

func evaluateDailyCheckInActivity(
	cfg DailyCheckInConfig,
	now time.Time,
) (DailyCheckInActivityState, *time.Time) {
	if !cfg.Enabled {
		return DailyCheckInStateDisabled, cfg.EndAt()
	}
	if ValidateDailyCheckInConfig(cfg) != nil {
		return DailyCheckInStateDisabled, nil
	}
	endAt := cfg.EndAt()
	if now.Before(cfg.StartAt.UTC()) {
		return DailyCheckInStateUpcoming, endAt
	}
	if !now.Before(endAt.UTC()) {
		return DailyCheckInStateEnded, endAt
	}
	return DailyCheckInStateActive, endAt
}

func dailyCheckInDate(now time.Time) (time.Time, string, time.Time) {
	local := now.In(utcPlus8)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	nextResetAt := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, utcPlus8)
	return date, local.Format("2006-01-02"), nextResetAt
}

func (s *CheckInService) GetStatus(ctx context.Context, userID int64) (DailyCheckInStatus, error) {
	cfg, err := s.settings.GetDailyCheckInConfig(ctx)
	if err != nil {
		return DailyCheckInStatus{}, err
	}
	now := s.now()
	state, endAt := evaluateDailyCheckInActivity(cfg, now)
	checkInDate, checkInDateText, nextResetAt := dailyCheckInDate(now)
	status := DailyCheckInStatus{
		State:        state,
		StartAt:      cfg.StartAt,
		EndAt:        endAt,
		RewardAmount: cfg.RewardAmount,
		CheckInDate:  checkInDateText,
		NextResetAt:  nextResetAt,
	}
	if state != DailyCheckInStateActive || cfg.StartAt == nil {
		return status, nil
	}
	claim, err := s.repo.FindClaim(ctx, userID, cfg.StartAt.UTC(), checkInDate)
	if err != nil {
		return DailyCheckInStatus{}, err
	}
	status.Claim = claim
	status.ClaimedToday = claim != nil
	return status, nil
}

func (s *CheckInService) Claim(ctx context.Context, userID int64, role string) (DailyCheckInClaimResult, error) {
	if role != RoleUser {
		return DailyCheckInClaimResult{}, ErrDailyCheckInUserOnly
	}
	cfg, err := s.settings.GetDailyCheckInConfig(ctx)
	if err != nil {
		return DailyCheckInClaimResult{}, err
	}
	now := s.now()
	state, _ := evaluateDailyCheckInActivity(cfg, now)
	if state != DailyCheckInStateActive || cfg.StartAt == nil {
		return DailyCheckInClaimResult{}, ErrDailyCheckInInactive
	}
	checkInDate, checkInDateText, nextResetAt := dailyCheckInDate(now)
	claim, err := s.repo.CreateClaim(ctx, DailyCheckInClaimInput{
		UserID:          userID,
		ActivityStartAt: cfg.StartAt.UTC(),
		CheckInDate:     checkInDate,
		RewardAmount:    cfg.RewardAmount,
		ClaimedAt:       now.UTC(),
		ExpiresAt:       nextResetAt.UTC(),
	})
	if err != nil {
		return DailyCheckInClaimResult{}, err
	}

	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		if invalidateErr := s.billingCache.InvalidateUserBalance(ctx, userID); invalidateErr != nil {
			slog.WarnContext(ctx, "failed to invalidate balance cache after daily check-in", "user_id", userID, "error", invalidateErr)
		}
	}
	return DailyCheckInClaimResult{
		RewardAmount: claim.RewardAmount,
		BalanceAfter: claim.BalanceAfter,
		CheckInDate:  checkInDateText,
		ClaimedAt:    claim.ClaimedAt,
	}, nil
}

func (s *CheckInService) GetAdminConfig(
	ctx context.Context,
) (DailyCheckInConfig, DailyCheckInActivityState, *time.Time, error) {
	cfg, err := s.settings.GetDailyCheckInConfig(ctx)
	if err != nil {
		return DailyCheckInConfig{}, "", nil, err
	}
	state, endAt := evaluateDailyCheckInActivity(cfg, s.now())
	return cfg, state, endAt, nil
}

func (s *CheckInService) UpdateAdminConfig(
	ctx context.Context,
	cfg DailyCheckInConfig,
) (DailyCheckInConfig, DailyCheckInActivityState, *time.Time, error) {
	if err := s.settings.UpdateDailyCheckInConfig(ctx, cfg); err != nil {
		return DailyCheckInConfig{}, "", nil, err
	}
	return s.GetAdminConfig(ctx)
}
