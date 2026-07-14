package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type checkInRepositoryFake struct {
	findClaim   *DailyCheckInClaim
	findErr     error
	createErr   error
	createCalls int
	inputs      []DailyCheckInClaimInput
}

func (f *checkInRepositoryFake) FindClaim(context.Context, int64, time.Time, time.Time) (*DailyCheckInClaim, error) {
	return f.findClaim, f.findErr
}

func (f *checkInRepositoryFake) CreateClaim(_ context.Context, input DailyCheckInClaimInput) (*DailyCheckInClaim, error) {
	f.createCalls++
	f.inputs = append(f.inputs, input)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &DailyCheckInClaim{
		ID:              int64(f.createCalls),
		UserID:          input.UserID,
		ActivityStartAt: input.ActivityStartAt,
		CheckInDate:     input.CheckInDate,
		RewardAmount:    input.RewardAmount,
		BalanceAfter:    15,
		ClaimedAt:       input.ClaimedAt,
	}, nil
}

type checkInAuthCacheFake struct {
	APIKeyAuthCacheInvalidator
	userIDs []int64
}

func (f *checkInAuthCacheFake) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	f.userIDs = append(f.userIDs, userID)
}

type checkInBillingCacheFake struct {
	BillingCache
	userIDs []int64
	err     error
}

func (f *checkInBillingCacheFake) InvalidateUserBalance(_ context.Context, userID int64) error {
	f.userIDs = append(f.userIDs, userID)
	return f.err
}

func activeCheckInConfig() DailyCheckInConfig {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	return DailyCheckInConfig{Enabled: true, StartAt: &start, DurationDays: 7, RewardAmount: 10}
}

func newCheckInServiceFixture(
	t *testing.T,
	now time.Time,
	cfg DailyCheckInConfig,
) (*CheckInService, *checkInRepositoryFake, *checkInAuthCacheFake, *checkInBillingCacheFake, *SettingService) {
	t.Helper()
	settingsRepo := newDailyCheckInSettingRepo(nil)
	settings := NewSettingService(settingsRepo, &config.Config{})
	require.NoError(t, settings.UpdateDailyCheckInConfig(context.Background(), cfg))
	repo := &checkInRepositoryFake{}
	authCache := &checkInAuthCacheFake{}
	billingCache := &checkInBillingCacheFake{}
	svc := NewCheckInService(repo, settings, authCache, billingCache)
	svc.now = func() time.Time { return now }
	return svc, repo, authCache, billingCache, settings
}

func TestCheckInService_Status_UsesUTC8CalendarDay(t *testing.T) {
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	svc, _, _, _, _ := newCheckInServiceFixture(t, now, activeCheckInConfig())

	got, err := svc.GetStatus(context.Background(), 42)

	require.NoError(t, err)
	require.Equal(t, DailyCheckInStateActive, got.State)
	require.Equal(t, "2026-07-21", got.CheckInDate)
	require.Equal(t, "2026-07-22T00:00:00+08:00", got.NextResetAt.Format(time.RFC3339))
}

func TestCheckInService_Status_ActivityBoundaries(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		enabled bool
		now     time.Time
		want    DailyCheckInActivityState
	}{
		{name: "disabled", enabled: false, now: start, want: DailyCheckInStateDisabled},
		{name: "upcoming", enabled: true, now: start.Add(-time.Nanosecond), want: DailyCheckInStateUpcoming},
		{name: "start inclusive", enabled: true, now: start, want: DailyCheckInStateActive},
		{name: "end exclusive", enabled: true, now: start.Add(7 * 24 * time.Hour), want: DailyCheckInStateEnded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := activeCheckInConfig()
			cfg.Enabled = tt.enabled
			svc, repo, _, _, _ := newCheckInServiceFixture(t, tt.now, cfg)
			got, err := svc.GetStatus(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.State)
			require.Zero(t, repo.createCalls)
		})
	}
}

func TestCheckInService_Status_ReturnsSameDayClaim(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 30, 0, 0, time.UTC)
	svc, repo, _, _, _ := newCheckInServiceFixture(t, now, activeCheckInConfig())
	repo.findClaim = &DailyCheckInClaim{ID: 9, UserID: 42, RewardAmount: 10, BalanceAfter: 25, ClaimedAt: now.Add(-time.Hour)}

	got, err := svc.GetStatus(context.Background(), 42)

	require.NoError(t, err)
	require.True(t, got.ClaimedToday)
	require.Equal(t, int64(9), got.Claim.ID)
	require.InDelta(t, 25, got.Claim.BalanceAfter, 1e-9)
}

func TestCheckInService_Claim_RejectsAdmin(t *testing.T) {
	svc, repo, _, _, _ := newCheckInServiceFixture(t, time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC), activeCheckInConfig())

	_, err := svc.Claim(context.Background(), 42, RoleAdmin)

	require.ErrorIs(t, err, ErrDailyCheckInUserOnly)
	require.Zero(t, repo.createCalls)
}

func TestCheckInService_Claim_InactiveDoesNotWrite(t *testing.T) {
	cfg := activeCheckInConfig()
	cfg.Enabled = false
	svc, repo, _, _, _ := newCheckInServiceFixture(t, time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC), cfg)

	_, err := svc.Claim(context.Background(), 42, RoleUser)

	require.ErrorIs(t, err, ErrDailyCheckInInactive)
	require.Zero(t, repo.createCalls)
}

func TestCheckInService_Claim_InvalidatesCachesAfterSuccess(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	svc, repo, authCache, billingCache, _ := newCheckInServiceFixture(t, now, activeCheckInConfig())
	billingCache.err = errors.New("redis unavailable")

	got, err := svc.Claim(context.Background(), 42, RoleUser)

	require.NoError(t, err)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, []int64{42}, authCache.userIDs)
	require.Equal(t, []int64{42}, billingCache.userIDs)
	require.InDelta(t, 10, got.RewardAmount, 1e-9)
	require.InDelta(t, 15, got.BalanceAfter, 1e-9)
}

func TestCheckInService_Claim_UsesLatestReward(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	svc, repo, _, _, settings := newCheckInServiceFixture(t, now, activeCheckInConfig())
	cfg := activeCheckInConfig()
	cfg.RewardAmount = 12.5
	require.NoError(t, settings.UpdateDailyCheckInConfig(context.Background(), cfg))

	_, err := svc.Claim(context.Background(), 42, RoleUser)

	require.NoError(t, err)
	require.InDelta(t, 12.5, repo.inputs[0].RewardAmount, 1e-9)
}

func TestCheckInService_Claim_NewStartCreatesNewActivityIdentity(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	svc, repo, _, _, settings := newCheckInServiceFixture(t, now, activeCheckInConfig())
	_, err := svc.Claim(context.Background(), 42, RoleUser)
	require.NoError(t, err)

	newStart := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	cfg := activeCheckInConfig()
	cfg.StartAt = &newStart
	require.NoError(t, settings.UpdateDailyCheckInConfig(context.Background(), cfg))
	_, err = svc.Claim(context.Background(), 42, RoleUser)

	require.NoError(t, err)
	require.Len(t, repo.inputs, 2)
	require.NotEqual(t, repo.inputs[0].ActivityStartAt, repo.inputs[1].ActivityStartAt)
}

func TestCheckInService_AdminConfig_RoundTripsNormalizedValue(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	svc, _, _, _, _ := newCheckInServiceFixture(t, now, activeCheckInConfig())
	start := time.Date(2026, 7, 23, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))

	got, state, endAt, err := svc.UpdateAdminConfig(context.Background(), DailyCheckInConfig{
		Enabled: true, StartAt: &start, DurationDays: 3, RewardAmount: 8,
	})

	require.NoError(t, err)
	require.Equal(t, DailyCheckInStateUpcoming, state)
	require.Equal(t, "2026-07-22T16:00:00Z", got.StartAt.Format(time.RFC3339))
	require.Equal(t, "2026-07-25T16:00:00Z", endAt.Format(time.RFC3339))
}
