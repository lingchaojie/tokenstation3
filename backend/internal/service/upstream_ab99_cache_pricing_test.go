//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// These cases catch stale 1h Priority prices and lost explicit-zero metadata
// when the new 1h override meets the local tier/provenance implementation.
func TestUpstreamAB99CacheWrite1hPreservesTierAndZeroPricing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		oneHour float64
		want    float64
	}{
		{"independent_one_hour_price", 5e-6, 0.0016},
		{"explicit_free_one_hour", 0, 0.0006},
	} {
		for _, interval := range []bool{false, true} {
			name := tc.name + "/flat"
			if interval {
				name = tc.name + "/interval"
			}
			t.Run(name, func(t *testing.T) {
				bs := &BillingService{}
				r := NewModelPricingResolver(nil, bs)
				generic := 3e-6
				ch := &ChannelModelPricing{CacheWritePrice: &generic, CacheWrite1hPrice: &tc.oneHour}
				if interval {
					ch = &ChannelModelPricing{Intervals: []PricingInterval{{MinTokens: 0, CacheWritePrice: &generic, CacheWrite1hPrice: &tc.oneHour}}}
				}
				resolved := &ResolvedPricing{
					Mode: BillingModeToken, Source: PricingSourceChannel,
					channelPricing: ch, longContextPricingEnabled: true,
					BasePricing: &ModelPricing{
						CacheCreationPricePerToken: 1e-6, CacheCreationPricePerTokenPriority: 2e-6,
						CacheCreation5mPrice: 1e-6, CacheCreation5mPricePriority: 2e-6,
						CacheCreation1hPrice: 2e-6, CacheCreation1hPricePriority: 4e-6,
						SupportsCacheBreakdown: true,
					},
				}
				r.applyTokenOverrides(ch, resolved)
				cost, err := bs.calculateTokenCost(resolved, CostInput{
					Model: "cache-price-fixture", Resolver: r, RateMultiplier: 1, ServiceTier: "priority",
					Tokens: UsageTokens{CacheCreationTokens: 200, CacheCreation5mTokens: 100, CacheCreation1hTokens: 100},
				})
				require.NoError(t, err)
				require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
				_, provenance := r.GetIntervalPricingWithProvenance(resolved, 200)
				require.Equal(t, PricingSourceChannel, provenance.CacheWrite1h)
				require.Equal(t, PricingSourceChannel, provenance.CacheWrite1hPriority)
			})
		}
	}
}

// A 1h-only override must not switch aggregate usage from the generic fallback
// rate to an unconfigured zero 5m field, including an unselected interval.
func TestUpstreamAB99OneHourOnlyOverridePreservesAggregateCachePrice(t *testing.T) {
	for _, mode := range []string{"legacy", "channel", "group", "interval", "unmatched_interval"} {
		for _, tier := range []struct {
			name string
			want float64
		}{{"", 0.00375}, {"priority", 0.0075}} {
			for _, oneHour := range []float64{6e-6, 0} {
				rateName := "paid_1h"
				if oneHour == 0 {
					rateName = "free_1h"
				}
				t.Run(mode+"/"+tier.name+"/"+rateName, func(t *testing.T) {
					bs := NewBillingService(&config.Config{}, nil)
					r := NewModelPricingResolver(nil, bs)
					card := &ChannelModelPricing{Models: []string{"claude-sonnet-4"}, CacheWrite1hPrice: &oneHour}
					if mode == "interval" || mode == "unmatched_interval" {
						minTokens := 0
						if mode == "unmatched_interval" {
							minTokens = 2000
						}
						card = &ChannelModelPricing{Intervals: []PricingInterval{{MinTokens: minTokens, CacheWrite1hPrice: &oneHour}}}
					}
					costFor := func(tokens UsageTokens) (*CostBreakdown, error) {
						if mode == "legacy" {
							return bs.calculateCostInternal("claude-sonnet-4", tokens, 1, tier.name, card)
						}
						resolved := r.resolveConfiguredPricing(card, "claude-sonnet-4", PricingSourceChannel)
						resolved.longContextPricingEnabled = true
						if mode == "group" {
							resolved = r.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: &Group{ModelPricing: []ChannelModelPricing{*card}}})
						}
						return bs.CalculateCostUnified(CostInput{
							Model: "claude-sonnet-4", Tokens: tokens, RateMultiplier: 1, ServiceTier: tier.name,
							Resolver: r, Resolved: resolved,
						})
					}
					cost, err := costFor(UsageTokens{CacheCreationTokens: 1000})
					require.NoError(t, err)
					require.InDelta(t, tier.want, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, tier.want, cost.TotalCost, 1e-12)
					// An actually reported 1h bucket keeps its independent price.
					oneHourCost, err := costFor(UsageTokens{CacheCreationTokens: 1000, CacheCreation1hTokens: 1000})
					if mode == "unmatched_interval" {
						require.ErrorIs(t, err, ErrModelPricingUnavailable)
						return
					}
					require.NoError(t, err)
					wantOneHour := 0.006
					if tier.name == "priority" {
						wantOneHour = 0.012
					}
					if oneHour == 0 {
						wantOneHour = 0
					}
					require.InDelta(t, wantOneHour, oneHourCost.TotalCost, 1e-12)
				})
			}
		}
	}
}

func TestUpstreamAB99AggregateCacheFallbackRespectsPricePresence(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		fiveMinute, priorityFiveMinute   float64
		fivePresent, priorityFivePresent bool
		genericFree, genericMissing      bool
		longContext                      bool
		wantStandard, wantPriority       float64
	}{
		{name: "missing 5m uses effective generic", wantStandard: 0.004, wantPriority: 0.008},
		{name: "missing 5m keeps long context multiplier", longContext: true, wantStandard: 0.008, wantPriority: 0.016},
		{name: "configured 5m remains preferred", fiveMinute: 2e-6, fivePresent: true, wantStandard: 0.002, wantPriority: 0.002},
		{name: "explicit free 5m remains free", fivePresent: true},
		{name: "explicit free priority 5m remains free", fiveMinute: 2e-6, fivePresent: true, priorityFivePresent: true, wantStandard: 0.002},
		{name: "explicit free generic remains free", genericFree: true},
		{name: "absent generic remains fail closed", genericMissing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs := NewBillingService(&config.Config{}, nil)
			p := &ModelPricing{
				CacheCreationPricePerToken: 4e-6, CacheCreationPricePerTokenPriority: 8e-6,
				cacheWritePriceConfigured: !tc.genericMissing, cacheWritePriorityConfigured: !tc.genericMissing,
				CacheCreation5mPrice: tc.fiveMinute, CacheCreation5mPricePriority: tc.priorityFiveMinute,
				cacheWrite5mPriceConfigured: tc.fivePresent, cacheWrite5mPriorityConfigured: tc.priorityFivePresent,
				tokenPricePresenceKnown: true,
			}
			if tc.genericFree || tc.genericMissing {
				p.CacheCreationPricePerToken, p.CacheCreationPricePerTokenPriority = 0, 0
			}
			if tc.longContext {
				p.LongContextInputThreshold = 500
				p.LongContextInputMultiplier = 2
			}
			bs.fallbackPrices["claude-sonnet-4"] = p
			oneHour := 6e-6
			card := &ChannelModelPricing{CacheWrite1hPrice: &oneHour}
			for _, tier := range []struct {
				name string
				want float64
			}{{"", tc.wantStandard}, {"priority", tc.wantPriority}} {
				cost, err := bs.calculateCostInternal("claude-sonnet-4", UsageTokens{CacheCreationTokens: 1000}, 1, tier.name, card)
				if tc.genericMissing {
					require.ErrorIs(t, err, ErrModelPricingUnavailable)
					continue
				}
				require.NoError(t, err)
				require.InDelta(t, tier.want, cost.TotalCost, 1e-12)
			}
			// Do not invent a missing explicitly reported TTL price.
			if !tc.fivePresent {
				_, err := bs.calculateCostInternal("claude-sonnet-4", UsageTokens{CacheCreationTokens: 1000, CacheCreation5mTokens: 1000}, 1, "", card)
				require.ErrorIs(t, err, ErrModelPricingUnavailable)
			}
		})
	}
}

func TestUpstreamAB99AccountStatsAcceptsOnlyExplicitFree1hPrice(t *testing.T) {
	zero := 0.0
	pricing := &ChannelModelPricing{CacheWrite1hPrice: &zero}
	require.True(t, configuredTokenPriceExists(pricing))
	cost := calculateTokenStatsCost(pricing, UsageTokens{CacheCreationTokens: 100, CacheCreation1hTokens: 100})
	require.NotNil(t, cost)
	require.Zero(t, *cost)
	// A used 5m bucket without a price must still fall through to another source.
	require.Nil(t, calculateTokenStatsCost(pricing, UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: 1, CacheCreation1hTokens: 99}))
}
