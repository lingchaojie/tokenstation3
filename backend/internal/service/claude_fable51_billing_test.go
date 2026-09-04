package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const (
	fableInputPricePerToken           = 10e-6
	fableOutputPricePerToken          = 50e-6
	fableCacheCreation5mPricePerToken = 12.5e-6
	fableCacheCreation1hPricePerToken = 20e-6
	fable5CacheReadPricePerToken      = 1e-6
	fable51CacheReadPricePerToken     = 0.25e-6
)

func TestClaudeFable51_HardcodedFallbackUsesExactCacheRates(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	tests := []struct {
		model     string
		cacheRead float64
	}{
		{model: "claude-fable-5-1", cacheRead: fable51CacheReadPricePerToken},
		{model: "us.anthropic.claude-fable-5-1-v1", cacheRead: fable51CacheReadPricePerToken},
		{model: "claude-fable-5", cacheRead: fable5CacheReadPricePerToken},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.InDelta(t, fableInputPricePerToken, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, fableOutputPricePerToken, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, fableCacheCreation5mPricePerToken, pricing.CacheCreationPricePerToken, 1e-12)
			require.InDelta(t, fableCacheCreation5mPricePerToken, pricing.CacheCreation5mPrice, 1e-12)
			require.InDelta(t, fableCacheCreation1hPricePerToken, pricing.CacheCreation1hPrice, 1e-12)
			require.InDelta(t, tt.cacheRead, pricing.CacheReadPricePerToken, 1e-12)
			require.True(t, pricing.SupportsCacheBreakdown)
		})
	}
}

func TestClaudeFable51_BillingSeparatesFiveMinuteAndOneHourCacheWrites(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	tokens := UsageTokens{
		InputTokens:           1_000_000,
		OutputTokens:          1_000_000,
		CacheCreation5mTokens: 1_000_000,
		CacheCreation1hTokens: 1_000_000,
		CacheReadTokens:       1_000_000,
	}

	cost, err := svc.CalculateCost("claude-fable-5-1", tokens, 1)

	require.NoError(t, err)
	require.InDelta(t, 10.0, cost.InputCost, 1e-9)
	require.InDelta(t, 50.0, cost.OutputCost, 1e-9)
	require.InDelta(t, 32.5, cost.CacheCreationCost, 1e-9)
	require.InDelta(t, 0.25, cost.CacheReadCost, 1e-9)
	require.InDelta(t, 92.75, cost.TotalCost, 1e-9)
}

func TestDefaultPricingIncludesClaudeFable51Rates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingSvc := &PricingService{}
	pricingData, err := pricingSvc.parsePricingData(data)
	require.NoError(t, err)
	pricingSvc.pricingData = pricingData
	billingSvc := NewBillingService(&config.Config{}, pricingSvc)

	pricing, err := billingSvc.GetModelPricing("claude-fable-5-1")
	require.NoError(t, err)
	require.InDelta(t, fableInputPricePerToken, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, fableOutputPricePerToken, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, fableCacheCreation5mPricePerToken, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, fableCacheCreation1hPricePerToken, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, fable51CacheReadPricePerToken, pricing.CacheReadPricePerToken, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
}
