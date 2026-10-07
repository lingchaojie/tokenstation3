//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamB8dePricingPreservation(t *testing.T) {
	require.Equal(t, 3.0, modelReasoningEffortBillingMultiplier("claude-fable-5-1", "max", nil))
	require.Equal(t, 1.0, modelReasoningEffortBillingMultiplier("claude-fable-5-1", "max", map[string]float64{"max": 1}))
	svc := newInflightEstimateGateway(t, nil)
	group := &Group{ID: 5, RateMultiplier: 1}
	key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	req := InflightEstimateRequest{Model: "claude-fable-5-1", BodyBytes: 4000, MaxTokens: 1000}
	standard, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	req.ReasoningEffort = "max"
	maximum, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	require.InDelta(t, standard*3, maximum, 1e-12)
	group.ModelPricing = []ChannelModelPricing{{Models: []string{req.Model}, BillingMode: BillingModeToken, ReasoningEffortMultipliers: map[string]float64{"max": 1}}}
	explicit, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	require.InDelta(t, standard, explicit, 1e-12)
	group.ModelPricing = nil
	req.Model, req.ReasoningEffort = "gpt-6-astra", ""
	standard, ok = svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	req.ServiceTier = "ultrafast"
	ultrafast, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	require.InDelta(t, standard*6, ultrafast, 1e-12)
}

func TestInflightLocalPricing_AlphaSearchUsesOnlyPerCallPrice(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	group := &Group{ID: 51, Platform: PlatformOpenAI, RateMultiplier: 2,
		ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.6-sol"}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(99)}},
	}
	key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	req := InflightEstimateRequest{Model: "gpt-5.6-sol", Kind: InflightEstimatePerRequest, SearchCalls: 1, BodyBytes: 9999, MaxTokens: 9999}
	for _, tc := range []struct {
		price *float64
		want  float64
	}{
		{nil, 0.02}, {testPtrFloat64(0.005), 0.01}, {testPtrFloat64(0), 0},
	} {
		group.WebSearchPricePerCall = tc.price
		cost, priced := svc.EstimateInflightReservation(context.Background(), key, req)
		require.True(t, priced)
		require.InDelta(t, tc.want, cost, 1e-12, "model card and token estimates must not override alpha/search per-call billing")
	}
}

func TestInflightLocalPricing_ExplicitZeroIsPricedMissingIsNot(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo} {
		t.Run(string(mode), func(t *testing.T) {
			svc := newInflightEstimateGateway(t, nil)
			group := &Group{ID: 1, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{
				Models: []string{"local-free-model"}, BillingMode: mode,
				InputPrice: testPtrFloat64(0), OutputPrice: testPtrFloat64(0), PerRequestPrice: testPtrFloat64(0),
			}}}
			key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
			req := InflightEstimateRequest{Model: "local-free-model", BodyBytes: 4000, MaxTokens: 1000}
			if mode == BillingModeImage {
				req.Kind = InflightEstimateImage
			}
			if mode == BillingModeVideo {
				req.Kind = InflightEstimateVideo
			}
			cost, priced := svc.EstimateInflightReservation(context.Background(), key, req)
			require.True(t, priced, "configured zero must not trigger fail-closed unpriced policy")
			require.Zero(t, cost)
			group.ModelPricing[0].OutputPrice = nil
			group.ModelPricing[0].PerRequestPrice = nil
			_, priced = svc.EstimateInflightReservation(context.Background(), key, req)
			require.False(t, priced, "a sparse card must not fabricate the missing bucket")
		})
	}
}

func TestInflightLocalPricing_LongContextFollowsGroupPolicy(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	svc.cfg.Billing.InflightReservation.MaxInputTokens = 0
	group := &Group{ID: 1, RateMultiplier: 1}
	key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	req := InflightEstimateRequest{Model: "gpt-6.1-sol", BodyBytes: 300000 * 4, MaxTokens: 1000}
	base, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	require.InDelta(t, 300000*2e-6+1000*10e-6, base, 1e-12)
	group.LongContextPricingEnabled = true
	long, ok := svc.EstimateInflightReservation(context.Background(), key, req)
	require.True(t, ok)
	require.InDelta(t, 300000*4e-6+1000*15e-6, long, 1e-12)
}
