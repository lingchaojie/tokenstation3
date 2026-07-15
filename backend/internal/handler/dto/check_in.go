package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type DailyCheckInClaim struct {
	RewardAmount float64   `json:"reward_amount"`
	BalanceAfter float64   `json:"balance_after"`
	ClaimedAt    time.Time `json:"claimed_at"`
}

type DailyCheckInStatus struct {
	State        string             `json:"state"`
	Active       bool               `json:"active"`
	StartAt      *time.Time         `json:"start_at"`
	EndAt        *time.Time         `json:"end_at"`
	RewardAmount float64            `json:"reward_amount"`
	CheckInDate  string             `json:"check_in_date"`
	ClaimedToday bool               `json:"claimed_today"`
	Claim        *DailyCheckInClaim `json:"claim,omitempty"`
	NextResetAt  time.Time          `json:"next_reset_at"`
}

type DailyCheckInClaimResult struct {
	RewardAmount float64   `json:"reward_amount"`
	BalanceAfter float64   `json:"balance_after"`
	CheckInDate  string    `json:"check_in_date"`
	ClaimedAt    time.Time `json:"claimed_at"`
}

type DailyCheckInConfig struct {
	Enabled      bool       `json:"enabled"`
	StartAt      *time.Time `json:"start_at"`
	DurationDays int        `json:"duration_days"`
	RewardAmount float64    `json:"reward_amount"`
	EndAt        *time.Time `json:"end_at"`
	State        string     `json:"state"`
}

func DailyCheckInStatusFromService(input service.DailyCheckInStatus) DailyCheckInStatus {
	result := DailyCheckInStatus{
		State:        string(input.State),
		Active:       input.State == service.DailyCheckInStateActive,
		StartAt:      input.StartAt,
		EndAt:        input.EndAt,
		RewardAmount: input.RewardAmount,
		CheckInDate:  input.CheckInDate,
		ClaimedToday: input.ClaimedToday,
		NextResetAt:  input.NextResetAt,
	}
	if input.Claim != nil {
		result.Claim = &DailyCheckInClaim{
			RewardAmount: input.Claim.RewardAmount,
			BalanceAfter: input.Claim.BalanceAfter,
			ClaimedAt:    input.Claim.ClaimedAt,
		}
	}
	return result
}

func DailyCheckInClaimResultFromService(input service.DailyCheckInClaimResult) DailyCheckInClaimResult {
	return DailyCheckInClaimResult{
		RewardAmount: input.RewardAmount,
		BalanceAfter: input.BalanceAfter,
		CheckInDate:  input.CheckInDate,
		ClaimedAt:    input.ClaimedAt,
	}
}

func DailyCheckInConfigFromService(
	config service.DailyCheckInConfig,
	state service.DailyCheckInActivityState,
	endAt *time.Time,
) DailyCheckInConfig {
	return DailyCheckInConfig{
		Enabled:      config.Enabled,
		StartAt:      config.StartAt,
		DurationDays: config.DurationDays,
		RewardAmount: config.RewardAmount,
		EndAt:        endAt,
		State:        string(state),
	}
}
