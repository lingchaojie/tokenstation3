//go:build unit

package service

import (
	"testing"

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
