package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDailyCheckInStatusFromService_DerivesActiveAndClaim(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	claimedAt := start.Add(25 * time.Hour)

	got := DailyCheckInStatusFromService(service.DailyCheckInStatus{
		State:        service.DailyCheckInStateActive,
		StartAt:      &start,
		EndAt:        &end,
		RewardAmount: 10,
		CheckInDate:  "2026-07-21",
		ClaimedToday: true,
		Claim: &service.DailyCheckInClaim{
			RewardAmount: 10,
			BalanceAfter: 15,
			ClaimedAt:    claimedAt,
		},
		NextResetAt: time.Date(2026, 7, 22, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
	})

	require.True(t, got.Active)
	require.Equal(t, "active", got.State)
	require.True(t, got.ClaimedToday)
	require.NotNil(t, got.Claim)
	require.InDelta(t, 15, got.Claim.BalanceAfter, 1e-9)
}

func TestDailyCheckInConfigFromService_IncludesDerivedStateAndEnd(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)

	got := DailyCheckInConfigFromService(
		service.DailyCheckInConfig{Enabled: true, StartAt: &start, DurationDays: 7, RewardAmount: 10},
		service.DailyCheckInStateUpcoming,
		&end,
	)

	require.True(t, got.Enabled)
	require.Equal(t, 7, got.DurationDays)
	require.Equal(t, "upcoming", got.State)
	require.Equal(t, end, *got.EndAt)
}
