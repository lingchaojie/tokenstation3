package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionFromService_IncludesPlanSnapshotSevenDayBalance(t *testing.T) {
	t.Parallel()

	planID := int64(42)
	planName := "Plus weekly"
	limit := 110.0
	windowStart := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	sub := &service.UserSubscription{
		ID:                1001,
		UserID:            2002,
		GroupID:           3003,
		PlanID:            &planID,
		PlanName:          &planName,
		SevenDayLimitUSD:  &limit,
		WeeklyWindowStart: &windowStart,
		WeeklyUsageUSD:    35.25,
		DailyUsageUSD:     4.5,
		MonthlyUsageUSD:   50.75,
	}

	got := UserSubscriptionFromService(sub)

	require.NotNil(t, got)
	require.NotNil(t, got.PlanID)
	require.Equal(t, planID, *got.PlanID)
	require.NotNil(t, got.PlanName)
	require.Equal(t, planName, *got.PlanName)
	require.NotNil(t, got.SevenDayLimitUSD)
	require.InDelta(t, 110.0, *got.SevenDayLimitUSD, 1e-9)
	require.InDelta(t, 35.25, got.SevenDayUsageUSD, 1e-9)
	require.NotNil(t, got.SevenDayRemainingUSD)
	require.InDelta(t, 74.75, *got.SevenDayRemainingUSD, 1e-9)
	require.NotNil(t, got.SevenDayResetAt)
	require.Equal(t, windowStart.Add(7*24*time.Hour), *got.SevenDayResetAt)

	// Existing usage fields must remain intact for current consumers.
	require.InDelta(t, 35.25, got.WeeklyUsageUSD, 1e-9)
	require.InDelta(t, 4.5, got.DailyUsageUSD, 1e-9)
	require.InDelta(t, 50.75, got.MonthlyUsageUSD, 1e-9)
}

func TestUserSubscriptionFromServiceWithGroup_UsesLegacyWeeklyLimitFallback(t *testing.T) {
	t.Parallel()

	legacyLimit := 80.0
	windowStart := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	sub := &service.UserSubscription{
		ID:                1002,
		UserID:            2003,
		GroupID:           3004,
		WeeklyWindowStart: &windowStart,
		WeeklyUsageUSD:    20.5,
	}
	group := &service.Group{WeeklyLimitUSD: &legacyLimit}

	got := UserSubscriptionFromServiceWithGroup(sub, group)

	require.NotNil(t, got)
	require.Nil(t, got.PlanID)
	require.Nil(t, got.PlanName)
	require.NotNil(t, got.SevenDayLimitUSD)
	require.InDelta(t, 80.0, *got.SevenDayLimitUSD, 1e-9)
	require.InDelta(t, 20.5, got.SevenDayUsageUSD, 1e-9)
	require.NotNil(t, got.SevenDayRemainingUSD)
	require.InDelta(t, 59.5, *got.SevenDayRemainingUSD, 1e-9)
	require.NotNil(t, got.SevenDayResetAt)
	require.Equal(t, windowStart.Add(7*24*time.Hour), *got.SevenDayResetAt)
}

func TestUserSubscriptionFromServiceWithGroup_DoesNotFallbackForPlanBackedNilQuota(t *testing.T) {
	t.Parallel()

	planID := int64(43)
	planName := "Plan without quota snapshot"
	legacyLimit := 80.0
	sub := &service.UserSubscription{
		ID:             1003,
		UserID:         2004,
		GroupID:        3005,
		PlanID:         &planID,
		PlanName:       &planName,
		WeeklyUsageUSD: 20.5,
	}
	group := &service.Group{WeeklyLimitUSD: &legacyLimit}

	got := UserSubscriptionFromServiceWithGroup(sub, group)

	require.NotNil(t, got)
	require.NotNil(t, got.PlanID)
	require.Equal(t, planID, *got.PlanID)
	require.NotNil(t, got.PlanName)
	require.Equal(t, planName, *got.PlanName)
	require.Nil(t, got.SevenDayLimitUSD)
	require.Nil(t, got.SevenDayRemainingUSD)
	require.InDelta(t, 20.5, got.SevenDayUsageUSD, 1e-9)
}
