package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT6Astra_HardcodedFallbackUsesOfficialStandardPrices(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	pricing, err := svc.GetModelPricing("gpt-6-astra")

	require.NoError(t, err)
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadPricePerToken, 1e-12)
}

func TestGPT6Astra_HardcodedFallbackAppliesLongContextToWholeRequest(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	atBoundary, err := svc.CalculateCost("gpt-6-astra", UsageTokens{
		InputTokens:  272_000,
		OutputTokens: 10,
	}, 1)
	require.NoError(t, err)
	require.InDelta(t, 2.72, atBoundary.InputCost, 1e-12)
	require.InDelta(t, 0.0005, atBoundary.OutputCost, 1e-12)
	require.False(t, atBoundary.LongContextBillingApplied)

	aboveBoundary, err := svc.CalculateCost("gpt-6-astra", UsageTokens{
		InputTokens:  272_001,
		OutputTokens: 10,
	}, 1)
	require.NoError(t, err)
	require.InDelta(t, 5.44002, aboveBoundary.InputCost, 1e-12)
	require.InDelta(t, 0.00075, aboveBoundary.OutputCost, 1e-12)
	require.True(t, aboveBoundary.LongContextBillingApplied)
}

func TestGPT6Astra_PricingServiceUsesStaticFallbackWhenCatalogIsStale(t *testing.T) {
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}

	pricing := svc.GetModelPricing("gpt-6-astra")

	require.NotNil(t, pricing)
	require.InDelta(t, 10e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 20e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 100e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 25e-6, pricing.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 2e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.Equal(t, 272_000, pricing.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputCostMultiplier, 1e-12)
}

func TestDefaultPricingIncludesGPT6AstraServiceTiersAndLongContext(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingSvc := &PricingService{}
	pricingData, err := pricingSvc.parsePricingData(data)
	require.NoError(t, err)
	pricingSvc.pricingData = pricingData

	pricing := pricingSvc.GetCatalogModelPricing("gpt-6-astra")
	require.NotNil(t, pricing)
	require.InDelta(t, 10e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 20e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 100e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 25e-6, pricing.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 2e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.Equal(t, 272_000, pricing.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputCostMultiplier, 1e-12)

	billingSvc := NewBillingService(&config.Config{}, pricingSvc)
	tokens := UsageTokens{InputTokens: 100, OutputTokens: 100, CacheCreationTokens: 100, CacheReadTokens: 100}
	standard, err := billingSvc.CalculateCostWithServiceTier("gpt-6-astra", tokens, 1, "default")
	require.NoError(t, err)
	require.InDelta(t, 0.00735, standard.TotalCost, 1e-12)
	fast, err := billingSvc.CalculateCostWithServiceTier("gpt-6-astra", tokens, 1, "fast")
	require.NoError(t, err)
	require.InDelta(t, 0.0147, fast.TotalCost, 1e-12)
	flex, err := billingSvc.CalculateCostWithServiceTier("gpt-6-astra", tokens, 1, "flex")
	require.NoError(t, err)
	require.InDelta(t, 0.003675, flex.TotalCost, 1e-12)

	longContext, err := billingSvc.CalculateCost("gpt-6-astra", UsageTokens{
		InputTokens:         100_000,
		OutputTokens:        10,
		CacheCreationTokens: 100_000,
		CacheReadTokens:     72_001,
	}, 1)
	require.NoError(t, err)
	require.InDelta(t, 2.0, longContext.InputCost, 1e-12)
	require.InDelta(t, 2.5, longContext.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.144002, longContext.CacheReadCost, 1e-12)
	require.InDelta(t, 0.00075, longContext.OutputCost, 1e-12)
	require.True(t, longContext.LongContextBillingApplied)
}
