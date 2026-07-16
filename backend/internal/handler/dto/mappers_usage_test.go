package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFromService_IncludesOpenAIWSMode(t *testing.T) {
	t.Parallel()

	wsLog := &service.UsageLog{
		RequestID:    "req_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: true,
	}
	httpLog := &service.UsageLog{
		RequestID:    "resp_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: false,
	}

	require.True(t, UsageLogFromService(wsLog).OpenAIWSMode)
	require.False(t, UsageLogFromService(httpLog).OpenAIWSMode)
	require.True(t, UsageLogFromServiceAdmin(wsLog).OpenAIWSMode)
	require.False(t, UsageLogFromServiceAdmin(httpLog).OpenAIWSMode)
}

func TestUsageLogFromService_PrefersRequestTypeForLegacyFields(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:    "req_2",
		Model:        "gpt-5.3-codex",
		RequestType:  service.RequestTypeWSV2,
		Stream:       false,
		OpenAIWSMode: false,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "ws_v2", userDTO.RequestType)
	require.True(t, userDTO.Stream)
	require.True(t, userDTO.OpenAIWSMode)
	require.Equal(t, "ws_v2", adminDTO.RequestType)
	require.True(t, adminDTO.Stream)
	require.True(t, adminDTO.OpenAIWSMode)
}

func TestUsageLogFromService_ReturnsSanitizedWebChatAPIKey(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		ID:       10,
		APIKeyID: 20,
		APIKey: &service.APIKey{
			ID:      20,
			UserID:  30,
			Key:     "wc_secret",
			Name:    "Web Chat",
			KeyType: service.APIKeyTypeWebChat,
		},
	}

	out := UsageLogFromService(log)
	require.Equal(t, int64(20), out.APIKeyID)
	require.NotNil(t, out.APIKey)
	require.Equal(t, int64(20), out.APIKey.ID)
	require.Equal(t, "Web Chat", out.APIKey.Name)
	require.Equal(t, service.APIKeyTypeWebChat, out.APIKey.KeyType)
	require.Empty(t, out.APIKey.Key)

	body, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), "wc_secret")
}

func TestUsageLogFromServiceAdmin_ReturnsSanitizedWebChatAPIKey(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		ID:       11,
		APIKeyID: 21,
		APIKey: &service.APIKey{
			ID:      21,
			UserID:  31,
			Key:     "wc_secret",
			Name:    "Web Chat",
			KeyType: service.APIKeyTypeWebChat,
		},
	}

	out := UsageLogFromServiceAdmin(log)
	require.Equal(t, int64(21), out.APIKeyID)
	require.NotNil(t, out.APIKey)
	require.Equal(t, int64(21), out.APIKey.ID)
	require.Equal(t, "Web Chat", out.APIKey.Name)
	require.Equal(t, service.APIKeyTypeWebChat, out.APIKey.KeyType)
	require.Empty(t, out.APIKey.Key)

	body, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), "wc_secret")
}

func TestUsageCleanupTaskFromService_RequestTypeMapping(t *testing.T) {
	t.Parallel()

	requestType := int16(service.RequestTypeStream)
	task := &service.UsageCleanupTask{
		ID:     1,
		Status: service.UsageCleanupStatusPending,
		Filters: service.UsageCleanupFilters{
			RequestType: &requestType,
		},
	}

	dtoTask := UsageCleanupTaskFromService(task)
	require.NotNil(t, dtoTask)
	require.NotNil(t, dtoTask.Filters.RequestType)
	require.Equal(t, "stream", *dtoTask.Filters.RequestType)
}

func TestRequestTypeStringPtrNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, requestTypeStringPtr(nil))
}

func TestUsageLogFromService_IncludesServiceTierForUserAndAdmin(t *testing.T) {
	t.Parallel()

	serviceTier := "priority"
	inboundEndpoint := "/v1/chat/completions"
	upstreamEndpoint := "/v1/responses"
	log := &service.UsageLog{
		RequestID:             "req_3",
		Model:                 "gpt-5.4",
		ServiceTier:           &serviceTier,
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		AccountRateMultiplier: f64Ptr(1.5),
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.NotNil(t, userDTO.ServiceTier)
	require.Equal(t, serviceTier, *userDTO.ServiceTier)
	require.NotNil(t, userDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *userDTO.InboundEndpoint)
	require.Nil(t, userDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.ServiceTier)
	require.Equal(t, serviceTier, *adminDTO.ServiceTier)
	require.NotNil(t, adminDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *adminDTO.InboundEndpoint)
	require.NotNil(t, adminDTO.UpstreamEndpoint)
	require.Equal(t, upstreamEndpoint, *adminDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.AccountRateMultiplier)
	require.InDelta(t, 1.5, *adminDTO.AccountRateMultiplier, 1e-12)
}

func TestUsageLogFromService_NormalizesBillingForRegularUsers(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:         "req_billing",
		Model:             "gpt-5.4",
		InputCost:         0.10,
		OutputCost:        0.20,
		CacheCreationCost: 0.05,
		CacheReadCost:     0.15,
		ImageOutputCost:   0.50,
		TotalCost:         1.00,
		ActualCost:        1.50,
		RateMultiplier:    1.50,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.InDelta(t, 1.50, userDTO.TotalCost, 1e-12)
	require.InDelta(t, 1.50, userDTO.ActualCost, 1e-12)
	require.InDelta(t, 1.0, userDTO.RateMultiplier, 1e-12)
	require.InDelta(t, 0.15, userDTO.InputCost, 1e-12)
	require.InDelta(t, 0.30, userDTO.OutputCost, 1e-12)
	require.InDelta(t, 0.075, userDTO.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.225, userDTO.CacheReadCost, 1e-12)
	require.InDelta(t, 0.75, userDTO.ImageOutputCost, 1e-12)

	require.InDelta(t, 1.00, adminDTO.TotalCost, 1e-12)
	require.InDelta(t, 1.50, adminDTO.ActualCost, 1e-12)
	require.InDelta(t, 1.50, adminDTO.RateMultiplier, 1e-12)
	require.InDelta(t, 0.10, adminDTO.InputCost, 1e-12)
}

func TestUsageLogFromService_UsesRequestedModelAndKeepsUpstreamAdminOnly(t *testing.T) {
	t.Parallel()

	upstreamModel := "claude-sonnet-4-20250514"
	log := &service.UsageLog{
		RequestID:      "req_4",
		Model:          upstreamModel,
		RequestedModel: "claude-sonnet-4",
		UpstreamModel:  &upstreamModel,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "claude-sonnet-4", userDTO.Model)
	require.Equal(t, "claude-sonnet-4", adminDTO.Model)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "upstream_model")

	adminJSON, err := json.Marshal(adminDTO)
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"upstream_model":"claude-sonnet-4-20250514"`)
}

func TestUsageLogFromService_KeepsUserBillingAndIPWithoutAdminCostFields(t *testing.T) {
	t.Parallel()

	ipAddress := "203.0.113.10"
	accountRateMultiplier := 1.5
	accountStatsCost := 0.21
	log := &service.UsageLog{
		RequestID:             "req_user_visible_billing",
		Model:                 "gpt-5.4",
		InputCost:             0.01,
		OutputCost:            0.02,
		CacheCreationCost:     0.03,
		CacheReadCost:         0.04,
		TotalCost:             0.10,
		ActualCost:            0.08,
		RateMultiplier:        0.8,
		IPAddress:             &ipAddress,
		AccountRateMultiplier: &accountRateMultiplier,
		AccountStatsCost:      &accountStatsCost,
	}

	userDTO := UsageLogFromService(log)
	// DEV masking: regular users see actual cost as total_cost with rate_multiplier
	// normalized to 1 (markup hidden). Exact per-field scaling is covered by
	// TestUsageLogFromService_NormalizesBillingForRegularUsers.
	require.InDelta(t, 0.08, userDTO.TotalCost, 1e-12)
	require.InDelta(t, 0.08, userDTO.ActualCost, 1e-12)
	require.InDelta(t, 1.0, userDTO.RateMultiplier, 1e-12)
	require.NotNil(t, userDTO.IPAddress)
	require.Equal(t, ipAddress, *userDTO.IPAddress)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "account_rate_multiplier")
	require.NotContains(t, string(userJSON), "account_stats_cost")
	require.NotContains(t, string(userJSON), "account_cost")
}

func TestUsageLogFromService_FallsBackToLegacyModelWhenRequestedModelMissing(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID: "req_legacy",
		Model:     "claude-3",
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "claude-3", userDTO.Model)
	require.Equal(t, "claude-3", adminDTO.Model)
}

func TestUsageLogFromService_IncludesImageBillingMetadataForUserAndAdmin(t *testing.T) {
	t.Parallel()

	imageSize := "4K"
	inputSize := "1024x1024"
	outputSize := "3840x2160"
	source := "output"
	log := &service.UsageLog{
		RequestID:          "req_image_metadata",
		Model:              "gpt-image-2",
		ImageCount:         2,
		ImageSize:          &imageSize,
		ImageInputSize:     &inputSize,
		ImageOutputSize:    &outputSize,
		ImageSizeSource:    &source,
		ImageSizeBreakdown: map[string]int{"4K": 2},
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	for _, got := range []*UsageLog{userDTO, &adminDTO.UsageLog} {
		require.Equal(t, 2, got.ImageCount)
		require.NotNil(t, got.ImageSize)
		require.Equal(t, imageSize, *got.ImageSize)
		require.NotNil(t, got.ImageInputSize)
		require.Equal(t, inputSize, *got.ImageInputSize)
		require.NotNil(t, got.ImageOutputSize)
		require.Equal(t, outputSize, *got.ImageOutputSize)
		require.NotNil(t, got.ImageSizeSource)
		require.Equal(t, source, *got.ImageSizeSource)
		require.Equal(t, map[string]int{"4K": 2}, got.ImageSizeBreakdown)
	}
}

func TestUsageLogFromService_PreservesHistoricalMissingImageSize(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:  "req_legacy_image_missing_size",
		Model:      "gpt-image-2",
		ImageCount: 1,
		ImageSize:  nil,
	}

	dto := UsageLogFromService(log)
	require.Equal(t, 1, dto.ImageCount)
	require.Nil(t, dto.ImageSize)
	require.Nil(t, dto.ImageInputSize)
	require.Nil(t, dto.ImageOutputSize)
	require.Nil(t, dto.ImageSizeSource)
	require.Nil(t, dto.ImageSizeBreakdown)

	body, err := json.Marshal(dto)
	require.NoError(t, err)
	require.Contains(t, string(body), `"image_size":null`)
	require.NotContains(t, string(body), `"image_size":"2K"`)
}

func TestUsageLogFromService_NestedLegacySubscriptionUsesLogGroupFallback(t *testing.T) {
	t.Parallel()

	legacyLimit := 95.0
	log := &service.UsageLog{
		RequestID: "req_legacy_sub_group",
		Model:     "claude-sonnet-4",
		Group: &service.Group{
			WeeklyLimitUSD: &legacyLimit,
		},
		Subscription: &service.UserSubscription{
			ID:             1004,
			UserID:         2005,
			GroupID:        3006,
			WeeklyUsageUSD: 35.0,
		},
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	for _, got := range []*UserSubscription{userDTO.Subscription, adminDTO.Subscription} {
		require.NotNil(t, got)
		require.NotNil(t, got.SevenDayLimitUSD)
		require.InDelta(t, 95.0, *got.SevenDayLimitUSD, 1e-9)
		require.NotNil(t, got.SevenDayRemainingUSD)
		require.InDelta(t, 60.0, *got.SevenDayRemainingUSD, 1e-9)
	}
}

func f64Ptr(value float64) *float64 {
	return &value
}
