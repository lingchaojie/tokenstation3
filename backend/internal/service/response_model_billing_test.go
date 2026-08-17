//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	anthropicCheapFixtureModel  = "claude-sonnet-4"
	anthropicPriceyFixtureModel = "claude-opus-4.8"
	openAICheapFixtureModel     = "gpt-5.4-nano"
	openAIPriceyFixtureModel    = "gpt-5.5"
)

func orderedObservationOnlyModels(t *testing.T, svc *BillingService, tokens UsageTokens, a, b string) (cheaper, pricier string, baselineCost *CostBreakdown) {
	t.Helper()
	costA, err := svc.CalculateCost(a, tokens, 1.1)
	require.NoError(t, err)
	costB, err := svc.CalculateCost(b, tokens, 1.1)
	require.NoError(t, err)
	require.NotEqual(t, costA.TotalCost, costB.TotalCost, "fixture prices must differ")
	if costA.TotalCost < costB.TotalCost {
		return a, b, costB
	}
	return b, a, costA
}

// A response model is audit data only. Even a stale persisted value from an
// earlier build must not let a provider declaration change the charge basis.
func TestGatewayServiceRecordUsage_ApprovedObservationOnlyNeverReprices(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	tokens := UsageTokens{InputTokens: 100, OutputTokens: 50}
	cheaper, pricier, baselineCost := orderedObservationOnlyModels(
		t, svc.billingService, tokens, anthropicCheapFixtureModel, anthropicPriceyFixtureModel,
	)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:             "gateway_response_model_observation_only",
			Usage:                 ClaudeUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens},
			Model:                 pricier,
			UpstreamResponseModel: cheaper,
			Duration:              time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
		ChannelUsageFields: ChannelUsageFields{
			ChannelID:          9,
			OriginalModel:      pricier,
			ChannelMappedModel: pricier,
			// Prove stale data cannot reactivate the retired mode.
			BillingModelSource: "response_model",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, baselineCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, baselineCost.ActualCost, userRepo.lastAmount, 1e-12)
	require.NotNil(t, usageRepo.lastLog.UpstreamResponseModel)
	require.Equal(t, cheaper, *usageRepo.lastLog.UpstreamResponseModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModelMismatch)
	require.True(t, *usageRepo.lastLog.UpstreamModelMismatch)
}

func TestOpenAIGatewayServiceRecordUsage_ApprovedObservationOnlyNeverReprices(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	tokens := UsageTokens{InputTokens: 20, OutputTokens: 10}
	cheaper, pricier, baselineCost := orderedObservationOnlyModels(
		t, svc.billingService, tokens, openAICheapFixtureModel, openAIPriceyFixtureModel,
	)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:             "openai_response_model_observation_only",
			Model:                 pricier,
			UpstreamModel:         pricier,
			UpstreamResponseModel: cheaper,
			Usage:                 OpenAIUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens},
			Duration:              time.Second,
		},
		APIKey:  &APIKey{ID: 10},
		User:    &User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: ChannelUsageFields{
			ChannelID:          9,
			OriginalModel:      pricier,
			ChannelMappedModel: pricier,
			BillingModelSource: "response_model",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, baselineCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, baselineCost.ActualCost, userRepo.lastAmount, 1e-12)
	require.NotNil(t, usageRepo.lastLog.UpstreamResponseModel)
	require.Equal(t, cheaper, *usageRepo.lastLog.UpstreamResponseModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModelMismatch)
	require.True(t, *usageRepo.lastLog.UpstreamModelMismatch)
}

func TestChannelNormalizeBillingModelSource_RetiredResponseModelUsesExistingDefault(t *testing.T) {
	channel := &Channel{BillingModelSource: "response_model"}
	channel.normalizeBillingModelSource()
	require.Equal(t, BillingModelSourceChannelMapped, channel.BillingModelSource)
}
