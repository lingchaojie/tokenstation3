//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGatewayVertexRegionalTokenPricing(t *testing.T) {
	const model = "gemini-3.7-flash"
	billing := newTestBillingService()
	at := time.Date(2026, time.November, 19, 0, 0, 0, 0, time.UTC)
	card := ChannelModelPricing{
		Platform: PlatformGemini, Models: []string{model},
		InputPrice: pricingMultiplier(4e-6), OutputPrice: pricingMultiplier(8e-6),
	}
	for _, tc := range []struct {
		name    string
		group   *Group
		channel []ChannelModelPricing
		want    float64
	}{
		{name: "catalog without group", want: 1.1},
		{name: "catalog with group", group: &Group{ID: 5, Platform: PlatformGemini}, want: 1.1},
		{name: "explicit group", group: &Group{ID: 5, Platform: PlatformGemini, ModelPricing: []ChannelModelPricing{card}}, want: 1},
		{name: "explicit channel", group: &Group{ID: 5, Platform: PlatformGemini}, channel: []ChannelModelPricing{card}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var channels *ChannelService
			if len(tc.channel) > 0 {
				channels = &ChannelService{}
				channels.cache.Store(populateChannelCache(
					[]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{5}, ModelPricing: tc.channel}},
					map[int64]string{5: PlatformGemini},
				))
			}
			gateway := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(channels, billing)}
			key := &APIKey{Group: tc.group}
			result := &ForwardResult{Usage: ClaudeUsage{InputTokens: 1_000, OutputTokens: 500}}
			global := gateway.calculateTokenCost(context.Background(), result, key, model, 1.5, at, nil)
			require.NotNil(t, global)
			result.UpstreamPriceMultiplier = vertexMultiRegionPriceMultiplier
			regional := gateway.calculateTokenCost(context.Background(), result, key, model, 1.5, at, nil)
			require.NotNil(t, regional)
			require.InDelta(t, global.InputCost*tc.want, regional.InputCost, 1e-12)
			require.InDelta(t, global.OutputCost*tc.want, regional.OutputCost, 1e-12)
			require.InDelta(t, global.TotalCost*tc.want, regional.TotalCost, 1e-12)
			require.InDelta(t, global.ActualCost*tc.want, regional.ActualCost, 1e-12)
		})
	}
}

func TestGatewayVertexRegionalPricingProtectsExplicitSiteFields(t *testing.T) {
	const model = "gemini-3.7-flash"
	catalog := newHotReloadPricingService(t, `{
		"gemini-3.7-flash": {
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000005,
			"output_cost_per_token_priority": 0.000009,
			"cache_read_input_token_cost": 0.0000001
		}
	}`, `{
		"gemini-3.7-flash": {
			"input_cost_per_token": 0.000003,
			"output_cost_per_token_priority": 0.000011
		}
	}`)
	billing := NewBillingService(catalog.cfg, catalog)
	gateway := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	at := time.Date(2026, time.November, 19, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		group       *Group
		serviceTier string
		opts        *recordUsageOpts
		outputRate  float64
	}{
		{name: "ungrouped standard", outputRate: 1.1},
		{name: "group default", group: &Group{ID: 5, Platform: PlatformGemini}, outputRate: 1.1},
		{name: "group priority with explicit output", group: &Group{ID: 5, Platform: PlatformGemini}, serviceTier: "priority", outputRate: 1},
		{name: "native Gemini context split", opts: &recordUsageOpts{LongContextThreshold: 500, LongContextMultiplier: 2}, outputRate: 1.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &ForwardResult{
				Usage:       ClaudeUsage{InputTokens: 1_000, OutputTokens: 500, CacheReadInputTokens: 100},
				ServiceTier: &tc.serviceTier,
			}
			key := &APIKey{Group: tc.group}
			global := gateway.calculateTokenCost(t.Context(), result, key, model, 1.5, at, tc.opts)
			require.NotNil(t, global)
			result.UpstreamPriceMultiplier = vertexMultiRegionPriceMultiplier
			regional := gateway.calculateTokenCost(t.Context(), result, key, model, 1.5, at, tc.opts)
			require.NotNil(t, regional)
			require.InDelta(t, global.InputCost, regional.InputCost, 1e-12)
			require.InDelta(t, global.OutputCost*tc.outputRate, regional.OutputCost, 1e-12)
			require.InDelta(t, global.CacheReadCost*1.1, regional.CacheReadCost, 1e-12)
			// The native split charges extra input above the threshold only in
			// ActualCost. The input bucket is an override and adds no surcharge.
			delta := regional.TotalCost - global.TotalCost
			require.InDelta(t, global.ActualCost+delta*1.5, regional.ActualCost, 1e-12)
		})
	}
	t.Run("default provider cost follows site bucket provenance", func(t *testing.T) {
		groupID := int64(5)
		channels := &ChannelService{}
		channels.cache.Store(populateChannelCache(
			[]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}}},
			map[int64]string{groupID: PlatformGemini},
		))
		cost := resolveAccountStatsCostWithUsage(
			t.Context(), channels, billing, 4, groupID, model,
			UsageTokens{InputTokens: 1_000, OutputTokens: 500, CacheReadTokens: 100},
			accountStatsCostUsage{requestCount: 1, upstreamPriceFactor: vertexMultiRegionPriceMultiplier},
			0, "", at,
		)
		require.NotNil(t, cost)
		require.InDelta(t, 1000*3e-6+(500*5e-6+100*1e-7)*1.1, *cost, 1e-12)
	})
}

func TestVertexRegionalPricingUsesEffectiveLongContextBucket(t *testing.T) {
	const model = "gemini-3.7-flash"
	catalog := newHotReloadPricingService(t, `{
		"gemini-3.7-flash": {
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000005,
			"input_cost_per_token_above_100k_tokens": 0.000006,
			"output_cost_per_token_above_100k_tokens": 0.000010,
			"cache_read_input_token_cost": 0.0000001,
			"cache_read_input_token_cost_above_100k_tokens": 0.0000004
		}
	}`, `{
		"gemini-3.7-flash": {
			"input_cost_per_token": 0.000003,
			"output_cost_per_token_above_100k_tokens": 0.000017,
			"cache_read_input_token_cost_above_100k_tokens": 0
		}
	}`)
	billing := NewBillingService(catalog.cfg, catalog)
	gateway := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	key := &APIKey{Group: &Group{ID: 5, Platform: PlatformGemini, LongContextPricingEnabled: true}}
	result := &ForwardResult{
		Usage:                   ClaudeUsage{InputTokens: 101_000, OutputTokens: 500, CacheReadInputTokens: 100},
		UpstreamPriceMultiplier: vertexMultiRegionPriceMultiplier,
	}
	cost := gateway.calculateTokenCost(t.Context(), result, key, model, 1, time.Time{}, nil)
	require.NotNil(t, cost)
	require.True(t, cost.LongContextBillingApplied)
	require.InDelta(t, 101_000*6e-6*1.1, cost.InputCost, 1e-12)
	require.InDelta(t, 500*17e-6, cost.OutputCost, 1e-12)
	require.Zero(t, cost.CacheReadCost)
}
