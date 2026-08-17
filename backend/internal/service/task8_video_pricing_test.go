//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTask8VideoModelPricingLookupSignalsLegacyFallback(t *testing.T) {
	t.Parallel()
	prices := NormalizeVideoModelPrices(map[string]map[string]float64{
		"grok-imagine-video-1.5-preview": {
			VideoBillingResolution720P: 0.14,
		},
	})

	modelPrice := LookupVideoModelPrice(prices, "grok-imagine-video-1.5", "720p")
	require.NotNil(t, modelPrice)
	require.InDelta(t, 0.14, *modelPrice, 1e-9)

	// A missing model/tier intentionally returns nil: the production caller then
	// uses the legacy flat resolution column before the code rate-card fallback.
	require.Nil(t, LookupVideoModelPrice(prices, "grok-imagine-video", "720p"))
	require.Nil(t, LookupVideoModelPrice(prices, "grok-imagine-video-1.5", "1080p"))
}
