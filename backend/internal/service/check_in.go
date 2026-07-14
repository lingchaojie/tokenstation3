package service

import (
	"context"
	"fmt"
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
}

type CheckInRepository interface {
	FindClaim(ctx context.Context, userID int64, activityStartAt, checkInDate time.Time) (*DailyCheckInClaim, error)
	CreateClaim(ctx context.Context, input DailyCheckInClaimInput) (*DailyCheckInClaim, error)
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
