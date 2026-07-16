//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetAffiliateRewardConfig_Defaults(t *testing.T) {
	repo := &settingGetAllRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetAffiliateRewardConfig(context.Background())

	require.NoError(t, err)
	require.Equal(t, AffiliateRewardConfig{
		FirstRechargeThreshold: 0,
		InviterReward:          10,
		InviteeReward:          5,
		ValidityDays:           7,
		InviterRewardLimit:     0,
	}, got)
}

func TestSettingService_GetAffiliateRewardConfig_Overrides(t *testing.T) {
	repo := &settingGetAllRepoStub{values: map[string]string{
		SettingKeyAffiliateFirstRechargeThreshold: "25.5",
		SettingKeyAffiliateInviterReward:          "12.25",
		SettingKeyAffiliateInviteeReward:          "6.5",
		SettingKeyAffiliateRewardValidityDays:     "14",
		SettingKeyAffiliateInviterRewardLimit:     "3",
	}}
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetAffiliateRewardConfig(context.Background())

	require.NoError(t, err)
	require.Equal(t, AffiliateRewardConfig{
		FirstRechargeThreshold: 25.5,
		InviterReward:          12.25,
		InviteeReward:          6.5,
		ValidityDays:           14,
		InviterRewardLimit:     3,
	}, got)
}

func TestSettingService_UpdateSettings_PersistsAffiliateRewardConfig(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		AffiliateFirstRechargeThreshold: 0,
		AffiliateInviterReward:          10,
		AffiliateInviteeReward:          5,
		AffiliateRewardValidityDays:     14,
		AffiliateInviterRewardLimit:     3,
	})

	require.NoError(t, err)
	require.Equal(t, "0.00000000", repo.updates[SettingKeyAffiliateFirstRechargeThreshold])
	require.Equal(t, "10.00000000", repo.updates[SettingKeyAffiliateInviterReward])
	require.Equal(t, "5.00000000", repo.updates[SettingKeyAffiliateInviteeReward])
	require.Equal(t, "14", repo.updates[SettingKeyAffiliateRewardValidityDays])
	require.Equal(t, "3", repo.updates[SettingKeyAffiliateInviterRewardLimit])
}

func TestSettingService_UpdateSettings_RejectsInvalidAffiliateRewardConfig(t *testing.T) {
	tests := []struct {
		name     string
		settings SystemSettings
	}{
		{
			name: "more than eight decimal places",
			settings: SystemSettings{
				AffiliateFirstRechargeThreshold: 1.123456789,
				AffiliateInviterReward:          10,
				AffiliateInviteeReward:          5,
				AffiliateRewardValidityDays:     7,
			},
		},
		{
			name: "negative reward limit",
			settings: SystemSettings{
				AffiliateInviterReward:      10,
				AffiliateInviteeReward:      5,
				AffiliateRewardValidityDays: 7,
				AffiliateInviterRewardLimit: -1,
			},
		},
		{
			name: "validity exceeds maximum",
			settings: SystemSettings{
				AffiliateInviterReward:      10,
				AffiliateInviteeReward:      5,
				AffiliateRewardValidityDays: AffiliateRewardValidityDaysMax + 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &settingUpdateRepoStub{}
			svc := NewSettingService(repo, &config.Config{})

			err := svc.UpdateSettings(context.Background(), &tt.settings)

			require.Error(t, err)
			require.Equal(t, "INVALID_AFFILIATE_REWARD_CONFIG", infraerrors.Reason(err))
			require.Nil(t, repo.updates)
		})
	}
}

func TestSettingService_GetAllSettings_ParsesAffiliateRewardConfig(t *testing.T) {
	repo := &settingGetAllRepoStub{values: map[string]string{
		SettingKeyAffiliateFirstRechargeThreshold: "30",
		SettingKeyAffiliateInviterReward:          "13",
		SettingKeyAffiliateInviteeReward:          "7",
		SettingKeyAffiliateRewardValidityDays:     "21",
		SettingKeyAffiliateInviterRewardLimit:     "4",
	}}
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetAllSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, 30.0, got.AffiliateFirstRechargeThreshold)
	require.Equal(t, 13.0, got.AffiliateInviterReward)
	require.Equal(t, 7.0, got.AffiliateInviteeReward)
	require.Equal(t, 21, got.AffiliateRewardValidityDays)
	require.Equal(t, 4, got.AffiliateInviterRewardLimit)
}
