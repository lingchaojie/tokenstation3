package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettingHandler_BuildSystemSettingsPayloadIncludesAffiliateRewardControls(t *testing.T) {
	settingService := service.NewSettingService(&settingHandlerRepoStub{}, &config.Config{})
	handler := NewSettingHandler(settingService, nil, nil, nil, nil, nil, nil)

	got := handler.buildSystemSettingsPayload(&service.SystemSettings{
		AffiliateFirstRechargeThreshold: 0,
		AffiliateInviterReward:          10,
		AffiliateInviteeReward:          5,
		AffiliateRewardValidityDays:     14,
		AffiliateInviterRewardLimit:     3,
	}, nil, nil, false)

	require.Equal(t, 14, got.AffiliateRewardValidityDays)
	require.Equal(t, 3, got.AffiliateInviterRewardLimit)
}

func TestUpdateSettingsRequest_AffiliateRewardControlsPreserveExplicitZero(t *testing.T) {
	var req UpdateSettingsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"affiliate_first_recharge_threshold": 0,
		"affiliate_inviter_reward_limit": 0
	}`), &req))

	require.NotNil(t, req.AffiliateFirstRechargeThreshold)
	require.Zero(t, *req.AffiliateFirstRechargeThreshold)
	require.NotNil(t, req.AffiliateInviterRewardLimit)
	require.Zero(t, *req.AffiliateInviterRewardLimit)
}
