//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayRecordUsage_AccountStatsReceivesCacheTTLBreakdown(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(
		usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{},
	)
	pricingSvc, billing := parseFixRoundPricing(t, `{"ttl-parity-model":{
		"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
		"cache_creation_input_token_cost":0.000003,
		"cache_creation_input_token_cost_above_1hr":0.000009}}`, "")
	_ = pricingSvc
	channel := &Channel{ID: 1, Status: StatusActive}
	svc.channelService = newTestChannelServiceForStats(t, channel, 10, "anthropic")
	svc.billingService = billing
	svc.resolver = NewModelPricingResolver(svc.channelService, billing)
	groupID := int64(10)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway-account-stats-ttl-parity",
			Model:     "ttl-parity-model",
			Usage: ClaudeUsage{
				InputTokens: 4, OutputTokens: 3,
				CacheCreationInputTokens: 30,
				CacheCreation5mTokens:    10,
				CacheCreation1hTokens:    20,
			},
		},
		APIKey: &APIKey{ID: 101, GroupID: &groupID, Group: &Group{ID: groupID}},
		User:   &User{ID: 201}, Account: &Account{ID: 301, Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
	require.InDelta(t, usageRepo.lastLog.TotalCost, *usageRepo.lastLog.AccountStatsCost, 1e-12)
	require.InDelta(t, 4e-6+3*2e-6+10*3e-6+20*9e-6, *usageRepo.lastLog.AccountStatsCost, 1e-12)
}

func TestBillingAndAccountStatsModelPricingParityMatrix(t *testing.T) {
	_, billing := parseFixRoundPricing(t, `{"parity-model":{
		"input_cost_per_token":0.000001,"input_cost_per_token_priority":0.000002,
		"output_cost_per_token":0.000003,"output_cost_per_token_priority":0.000006,
		"input_cost_per_image_token":0.000004,"output_cost_per_image_token":0.000005,
		"cache_creation_input_token_cost":0.000007,
		"cache_creation_input_token_cost_priority":0.000014,
		"cache_creation_input_token_cost_above_1hr":0.000021,
		"cache_creation_input_token_cost_above_1hr_priority":0.000042,
		"cache_read_input_token_cost":0.0000005,
		"cache_read_input_token_cost_priority":0.000001,
		"long_context_input_token_threshold":100,
		"long_context_input_cost_multiplier":2,
		"long_context_output_cost_multiplier":3,
		"supports_service_tier":true},
		"free-parity-model":{
		"input_cost_per_token":0,"output_cost_per_token":0,
		"input_cost_per_image_token":0,"output_cost_per_image_token":0,
		"cache_creation_input_token_cost":0,
		"cache_creation_input_token_cost_above_1hr":0,
		"cache_read_input_token_cost":0}}`, "")
	tests := []struct {
		name   string
		model  string
		tokens UsageTokens
		tier   string
	}{
		{name: "image input output", model: "parity-model", tokens: UsageTokens{InputTokens: 12, ImageInputTokens: 2, OutputTokens: 5, ImageOutputTokens: 1}},
		{name: "cache 5m 1h", model: "parity-model", tokens: UsageTokens{CacheCreationTokens: 12, CacheCreation5mTokens: 5, CacheCreation1hTokens: 7, CacheReadTokens: 3}},
		{name: "priority", model: "parity-model", tokens: UsageTokens{InputTokens: 10, OutputTokens: 4, CacheCreationTokens: 3, CacheCreation5mTokens: 3}, tier: "priority"},
		{name: "flex", model: "parity-model", tokens: UsageTokens{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3}, tier: "flex"},
		{name: "long context", model: "parity-model", tokens: UsageTokens{InputTokens: 101, OutputTokens: 4, CacheReadTokens: 3}},
		{name: "explicit free", model: "free-parity-model", tokens: UsageTokens{InputTokens: 10, ImageInputTokens: 2, OutputTokens: 4, ImageOutputTokens: 1, CacheCreationTokens: 3, CacheCreation5mTokens: 2, CacheCreation1hTokens: 1, CacheReadTokens: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			billed, err := billing.CalculateCostWithServiceTier(tt.model, tt.tokens, 1, tt.tier)
			require.NoError(t, err)
			stats := tryModelFilePricing(billing, tt.model, tt.tokens, tt.tier)
			require.NotNil(t, stats, "an explicitly resolved free price remains a non-nil zero")
			require.InDelta(t, billed.TotalCost, *stats, 1e-12)
		})
	}
}

func TestResolveAccountStatsCost_ApplyPricingPreservesResolvedFreeZero(t *testing.T) {
	channel := &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: true}
	cs := newTestChannelServiceForStats(t, channel, 10, "anthropic")
	got := resolveAccountStatsCostWithUsage(
		context.Background(), cs, nil, 1, 10, "explicit-free-model",
		UsageTokens{InputTokens: 1}, accountStatsCostUsage{requestCount: 1}, 0, "",
	)
	require.NotNil(t, got)
	require.Zero(t, *got)
}
