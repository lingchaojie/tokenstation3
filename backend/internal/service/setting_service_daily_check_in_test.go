package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type dailyCheckInSettingRepo struct {
	values map[string]string
}

func newDailyCheckInSettingRepo(values map[string]string) *dailyCheckInSettingRepo {
	if values == nil {
		values = map[string]string{}
	}
	return &dailyCheckInSettingRepo{values: values}
}

func (r *dailyCheckInSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *dailyCheckInSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *dailyCheckInSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *dailyCheckInSettingRepo) SetIfAbsent(_ context.Context, key, value string) error {
	if _, exists := r.values[key]; !exists {
		r.values[key] = value
	}
	return nil
}

func (r *dailyCheckInSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *dailyCheckInSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *dailyCheckInSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	values := make(map[string]string, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return values, nil
}

func (r *dailyCheckInSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestGetDailyCheckInConfig_UsesSafeDefaults(t *testing.T) {
	svc := NewSettingService(newDailyCheckInSettingRepo(nil), &config.Config{})

	got, err := svc.GetDailyCheckInConfig(context.Background())

	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Nil(t, got.StartAt)
	require.Zero(t, got.DurationDays)
	require.Equal(t, 10.0, got.RewardAmount)
}

func TestUpdateDailyCheckInConfig_NormalizesUTCAndPersists(t *testing.T) {
	repo := newDailyCheckInSettingRepo(nil)
	svc := NewSettingService(repo, &config.Config{})
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))

	err := svc.UpdateDailyCheckInConfig(context.Background(), DailyCheckInConfig{
		Enabled: true, StartAt: &start, DurationDays: 7, RewardAmount: 10,
	})

	require.NoError(t, err)
	require.Equal(t, "true", repo.values[SettingKeyDailyCheckInEnabled])
	require.Equal(t, "2026-07-19T16:00:00Z", repo.values[SettingKeyDailyCheckInStartAt])
	require.Equal(t, "7", repo.values[SettingKeyDailyCheckInDurationDays])
	require.Equal(t, "10.00000000", repo.values[SettingKeyDailyCheckInRewardAmount])
}

func TestUpdateDailyCheckInConfig_RejectsInvalidReward(t *testing.T) {
	tests := []struct {
		name   string
		reward float64
	}{
		{name: "zero", reward: 0},
		{name: "not a number", reward: math.NaN()},
		{name: "infinite", reward: math.Inf(1)},
		{name: "more than eight decimals", reward: 1.123456789},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			svc := NewSettingService(newDailyCheckInSettingRepo(nil), &config.Config{})
			err := svc.UpdateDailyCheckInConfig(context.Background(), DailyCheckInConfig{
				Enabled: true, StartAt: &start, DurationDays: 1, RewardAmount: tt.reward,
			})
			require.ErrorIs(t, err, ErrDailyCheckInConfigInvalid)
		})
	}
}

func TestGetPublicSettings_IncludesDailyCheckInWindow(t *testing.T) {
	repo := newDailyCheckInSettingRepo(map[string]string{
		SettingKeyDailyCheckInEnabled:      "true",
		SettingKeyDailyCheckInStartAt:      "2026-07-19T16:00:00Z",
		SettingKeyDailyCheckInDurationDays: "7",
		SettingKeyDailyCheckInRewardAmount: "10.00000000",
	})
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetPublicSettings(context.Background())

	require.NoError(t, err)
	require.True(t, got.DailyCheckInEnabled)
	require.Equal(t, "2026-07-19T16:00:00Z", got.DailyCheckInStartAt)
	require.Equal(t, "2026-07-26T16:00:00Z", got.DailyCheckInEndAt)
}

func TestGetPublicSettings_InvalidDailyCheckInRewardFailsClosed(t *testing.T) {
	repo := newDailyCheckInSettingRepo(map[string]string{
		SettingKeyDailyCheckInEnabled:      "true",
		SettingKeyDailyCheckInStartAt:      "2026-07-19T16:00:00Z",
		SettingKeyDailyCheckInDurationDays: "7",
		SettingKeyDailyCheckInRewardAmount: "not-a-number",
	})
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetPublicSettings(context.Background())

	require.NoError(t, err)
	require.False(t, got.DailyCheckInEnabled)
}

func TestInitializeDefaultSettings_PersistsDailyCheckInDefaults(t *testing.T) {
	repo := newDailyCheckInSettingRepo(nil)
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "false", repo.values[SettingKeyDailyCheckInEnabled])
	require.Equal(t, "", repo.values[SettingKeyDailyCheckInStartAt])
	require.Equal(t, "0", repo.values[SettingKeyDailyCheckInDurationDays])
	require.Equal(t, "10.00000000", repo.values[SettingKeyDailyCheckInRewardAmount])
}
