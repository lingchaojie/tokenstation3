//go:build unit

package service

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func parseFixRoundPricing(t *testing.T, catalog, override string) (*PricingService, *BillingService) {
	t.Helper()
	cfg := &config.Config{}
	if override != "" {
		path := filepath.Join(t.TempDir(), "override.json")
		require.NoError(t, os.WriteFile(path, []byte(override), 0o600))
		cfg.Pricing.OverrideFile = path
	}
	pricing := NewPricingService(cfg, nil)
	data, err := pricing.parsePricingData([]byte(catalog))
	require.NoError(t, err)
	pricing.pricingData = data
	return pricing, NewBillingService(cfg, pricing)
}

func provenanceField(t *testing.T, provenance PricingBucketProvenance, name string) string {
	t.Helper()
	field := reflect.ValueOf(provenance).FieldByName(name)
	require.True(t, field.IsValid(), "PricingBucketProvenance must expose %s independently", name)
	require.Equal(t, reflect.String, field.Kind())
	return field.String()
}

func TestCacheCreationTTL_ExplicitZeroAndAbsenceAreIndependent(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		tokens  UsageTokens
		want    float64
		wantErr bool
	}{
		{
			name:   "free 5m with priced 1h",
			entry:  `"cache_creation_input_token_cost":0,"cache_creation_input_token_cost_above_1hr":0.000004`,
			tokens: UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 10},
			want:   0,
		},
		{
			name:   "free 1h with priced 5m",
			entry:  `"cache_creation_input_token_cost":0.000001,"cache_creation_input_token_cost_above_1hr":0`,
			tokens: UsageTokens{CacheCreationTokens: 10, CacheCreation1hTokens: 10},
			want:   0,
		},
		{
			name:    "absent 1h fails closed instead of falling back to 5m",
			entry:   `"cache_creation_input_token_cost":0.000001`,
			tokens:  UsageTokens{CacheCreationTokens: 10, CacheCreation1hTokens: 10},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, billing := parseFixRoundPricing(t, `{"ttl-model":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,`+tt.entry+`}}`, "")
			cost, err := billing.CalculateCost("ttl-model", tt.tokens, 1)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrModelPricingUnavailable)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tt.want, cost.CacheCreationCost, 1e-12)
		})
	}
}

func TestCacheCreationTTL_OverrideAndPriorityProvenanceAreIndependent(t *testing.T) {
	pricing, billing := parseFixRoundPricing(t, `{"ttl-source-model":{
		"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
		"cache_creation_input_token_cost":0.000003,
		"cache_creation_input_token_cost_priority":0,
		"cache_creation_input_token_cost_above_1hr":0.000006,
		"cache_creation_input_token_cost_above_1hr_priority":0.000009,
		"supports_service_tier":true}}`, `{"ttl-source-model":{"cache_creation_input_token_cost_above_1hr":0.000007}}`)

	resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "ttl-source-model"})
	require.NotNil(t, resolved)
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "CacheWrite5m"))
	require.Equal(t, PricingSourceOverrideFile, provenanceField(t, resolved.Provenance, "CacheWrite1h"))
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "CacheWrite5mPriority"))
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "CacheWrite1hPriority"))

	standard, err := billing.CalculateCost("ttl-source-model", UsageTokens{CacheCreationTokens: 10, CacheCreation1hTokens: 10}, 1)
	require.NoError(t, err)
	require.InDelta(t, 10*0.000007, standard.CacheCreationCost, 1e-12)
	priority, err := billing.CalculateCostWithServiceTier("ttl-source-model", UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 10}, 1, "priority")
	require.NoError(t, err)
	require.Zero(t, priority.CacheCreationCost, "explicit free priority 5m price must not fall back or multiply standard price")
	require.True(t, pricing.pricingData["ttl-source-model"].CacheCreationInputTokenCostPriorityConfigured)
}

func TestLongContextComponents_PartialOverridePreservesIndependentSources(t *testing.T) {
	_, billing := parseFixRoundPricing(t, `{"long-source-model":{
		"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
		"long_context_input_token_threshold":200000,
		"long_context_input_cost_multiplier":2,
		"long_context_output_cost_multiplier":3}}`, `{"long-source-model":{"long_context_input_cost_multiplier":4}}`)
	resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "long-source-model"})
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)
	require.InDelta(t, 4, resolved.BasePricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 3, resolved.BasePricing.LongContextOutputMultiplier, 1e-12)
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextThreshold"))
	require.Equal(t, PricingSourceOverrideFile, provenanceField(t, resolved.Provenance, "LongContextInput"))
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextOutput"))
}

func TestLongContextComponents_NullRemovalClearsOnlyRemovedSource(t *testing.T) {
	_, billing := parseFixRoundPricing(t, `{"long-null-model":{
		"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
		"long_context_input_token_threshold":200000,
		"long_context_input_cost_multiplier":2,
		"long_context_output_cost_multiplier":3}}`, `{"long-null-model":{"long_context_output_cost_multiplier":null}}`)
	resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "long-null-model"})
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)
	require.InDelta(t, 2, resolved.BasePricing.LongContextInputMultiplier, 1e-12)
	require.Zero(t, resolved.BasePricing.LongContextOutputMultiplier)
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextThreshold"))
	require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextInput"))
	require.Empty(t, provenanceField(t, resolved.Provenance, "LongContextOutput"))
}

func TestLongContextComponents_AboveTierBaseSingleOverridePreservesDerivedSiblings(t *testing.T) {
	const catalog = `{"above-tier-model":{
		"input_cost_per_token":0.0000025,
		"output_cost_per_token":0.000015,
		"cache_read_input_token_cost":0.00000025,
		"input_cost_per_token_above_272k_tokens":0.000005,
		"output_cost_per_token_above_272k_tokens":0.0000225,
		"cache_read_input_token_cost_above_272k_tokens":0.0000005}}`
	tests := []struct {
		name                 string
		override             string
		inputTokens          int
		wantThreshold        int
		wantInputMultiplier  float64
		wantOutputMultiplier float64
		wantThresholdSource  string
		wantInputSource      string
		wantOutputSource     string
		wantTotal            float64
	}{
		{
			name:        "input multiplier override inherits threshold output and cache tier",
			override:    `{"above-tier-model":{"long_context_input_cost_multiplier":3}}`,
			inputTokens: 272001, wantThreshold: 272000,
			wantInputMultiplier: 3, wantOutputMultiplier: 1.5,
			wantThresholdSource: PricingSourceLiteLLM, wantInputSource: PricingSourceOverrideFile, wantOutputSource: PricingSourceLiteLLM,
			wantTotal: 2.0402425,
		},
		{
			name:        "explicit zero input multiplier preserves sibling ladder components",
			override:    `{"above-tier-model":{"long_context_input_cost_multiplier":0}}`,
			inputTokens: 272001, wantThreshold: 272000,
			wantInputMultiplier: 0, wantOutputMultiplier: 1.5,
			wantThresholdSource: PricingSourceLiteLLM, wantInputSource: PricingSourceOverrideFile, wantOutputSource: PricingSourceLiteLLM,
			wantTotal: 0.6802375,
		},
		{
			name:        "threshold override inherits input output and cache tier",
			override:    `{"above-tier-model":{"long_context_input_token_threshold":300000}}`,
			inputTokens: 300001, wantThreshold: 300000,
			wantInputMultiplier: 2, wantOutputMultiplier: 1.5,
			wantThresholdSource: PricingSourceOverrideFile, wantInputSource: PricingSourceLiteLLM, wantOutputSource: PricingSourceLiteLLM,
			wantTotal: 1.50024,
		},
		{
			name:        "output multiplier override inherits threshold input and cache tier",
			override:    `{"above-tier-model":{"long_context_output_cost_multiplier":4}}`,
			inputTokens: 272001, wantThreshold: 272000,
			wantInputMultiplier: 2, wantOutputMultiplier: 4,
			wantThresholdSource: PricingSourceLiteLLM, wantInputSource: PricingSourceLiteLLM, wantOutputSource: PricingSourceOverrideFile,
			wantTotal: 1.360615,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, billing := parseFixRoundPricing(t, catalog, tt.override)
			resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "above-tier-model"})
			require.NotNil(t, resolved)
			require.Equal(t, tt.wantThreshold, resolved.BasePricing.LongContextInputThreshold)
			require.InDelta(t, tt.wantInputMultiplier, resolved.BasePricing.LongContextInputMultiplier, 1e-12)
			require.InDelta(t, tt.wantOutputMultiplier, resolved.BasePricing.LongContextOutputMultiplier, 1e-12)
			require.Equal(t, tt.wantThresholdSource, provenanceField(t, resolved.Provenance, "LongContextThreshold"))
			require.Equal(t, tt.wantInputSource, provenanceField(t, resolved.Provenance, "LongContextInput"))
			require.Equal(t, tt.wantOutputSource, provenanceField(t, resolved.Provenance, "LongContextOutput"))
			require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextCacheRead"))

			cost, err := billing.CalculateCost("above-tier-model", UsageTokens{
				InputTokens: tt.inputTokens, OutputTokens: 10, CacheReadTokens: 20,
			}, 1)
			require.NoError(t, err)
			require.True(t, cost.LongContextBillingApplied)
			require.InDelta(t, tt.wantTotal, cost.TotalCost, 1e-12)
		})
	}
}

func TestLongContextComponents_AboveTierCacheBucketsRemainIndependent(t *testing.T) {
	const catalog = `{"above-cache-model":{
		"input_cost_per_token":0.0000025,"output_cost_per_token":0.000015,
		"cache_creation_input_token_cost":0.000001,
		"cache_creation_input_token_cost_above_1hr":0.000002,
		"cache_creation_input_token_cost_priority":0.000003,
		"cache_creation_input_token_cost_above_1hr_priority":0.000004,
		"cache_read_input_token_cost":0.00000025,
		"cache_read_input_token_cost_priority":0.0000005,
		"input_cost_per_token_above_272k_tokens":0.000005,
		"output_cost_per_token_above_272k_tokens":0.0000225,
		"cache_creation_input_token_cost_above_272k_tokens":0.000002,
		"cache_creation_input_token_cost_above_1hr_above_272k_tokens":0.000006,
		"cache_creation_input_token_cost_above_272k_tokens_priority":0.000012,
		"cache_creation_input_token_cost_above_1hr_above_272k_tokens_priority":0.000020,
		"cache_read_input_token_cost_above_272k_tokens":0.0000005,
		"cache_read_input_token_cost_above_272k_tokens_priority":0.000003,
		"supports_service_tier":true}}`
	_, billing := parseFixRoundPricing(t, catalog, `{"above-cache-model":{"long_context_input_cost_multiplier":3}}`)
	resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "above-cache-model"})
	for _, field := range []string{
		"LongContextCacheWrite5m", "LongContextCacheWrite1h",
		"LongContextCacheWrite5mPriority", "LongContextCacheWrite1hPriority",
		"LongContextCacheRead", "LongContextCacheReadPriority",
	} {
		require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, field), field)
	}

	tokens := UsageTokens{
		InputTokens: 272001, CacheCreationTokens: 20,
		CacheCreation5mTokens: 10, CacheCreation1hTokens: 10, CacheReadTokens: 10,
	}
	standard, err := billing.CalculateCost("above-cache-model", tokens, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.00008, standard.CacheCreationCost, 1e-12, "5m x2 and 1h x3 must stay independent of input override x3")
	require.InDelta(t, 0.000005, standard.CacheReadCost, 1e-12, "standard cache read keeps its catalog x2 ladder")

	priority, err := billing.CalculateCostWithServiceTier("above-cache-model", tokens, 1, "priority")
	require.NoError(t, err)
	require.InDelta(t, 0.00032, priority.CacheCreationCost, 1e-12, "priority 5m x4 and 1h x5 use their own above tiers")
	require.InDelta(t, 0.00003, priority.CacheReadCost, 1e-12, "priority cache read keeps its catalog x6 ladder")
}

func TestLongContextPriorityCacheAbove_UsesEffectiveTypedBaseAndFailsClosed(t *testing.T) {
	const catalog = `{"gemini-priority-fallback":{
		"input_cost_per_token":0.000001,"input_cost_per_token_priority":0.000002,
		"output_cost_per_token":0.000002,"output_cost_per_token_priority":0.000004,
		"cache_creation_input_token_cost":0.000004,
		"cache_creation_input_token_cost_above_1hr":0.000006,
		"cache_creation_input_token_cost_priority":0.000008,
		"cache_creation_input_token_cost_above_1hr_priority":0.000012,
		"cache_read_input_token_cost":0.0000002,
		"cache_read_input_token_cost_priority":0.0000004,
		"input_cost_per_token_above_272k_tokens":0.000002,
		"output_cost_per_token_above_272k_tokens":0.000004,
		"cache_creation_input_token_cost_above_272k_tokens_priority":0.0000072,
		"cache_creation_input_token_cost_above_1hr_above_272k_tokens_priority":0.000015,
		"cache_read_input_token_cost_above_272k_tokens_priority":0.00000072,
		"supports_service_tier":true}}`
	const removePriorityBases = `{"gemini-priority-fallback":{
		"cache_creation_input_token_cost_priority":null,
		"cache_creation_input_token_cost_above_1hr_priority":null,
		"cache_read_input_token_cost_priority":null}}`
	tokens := UsageTokens{
		InputTokens: 272001, CacheCreationTokens: 20,
		CacheCreation5mTokens: 10, CacheCreation1hTokens: 10, CacheReadTokens: 10,
	}

	t.Run("null removed priority bases fall back to matching standard bases", func(t *testing.T) {
		_, billing := parseFixRoundPricing(t, catalog, removePriorityBases)
		resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "gemini-priority-fallback"})
		cost, err := billing.CalculateCostWithServiceTier("gemini-priority-fallback", tokens, 1, "priority")
		require.NoError(t, err)
		require.InDelta(t, 0.000222, cost.CacheCreationCost, 1e-12,
			"5m 4e-6*1.8 and 1h 6e-6*2.5 must reproduce their priority above prices")
		require.InDelta(t, 0.0000072, cost.CacheReadCost, 1e-12,
			"priority read 7.2e-7 divided by effective standard base 2e-7 yields x3.6")
		require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextCacheWrite5mPriority"))
		require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextCacheWrite1hPriority"))
		require.Equal(t, PricingSourceLiteLLM, provenanceField(t, resolved.Provenance, "LongContextCacheReadPriority"))
	})

	t.Run("later channel cache override remains the authoritative effective base", func(t *testing.T) {
		_, billing := parseFixRoundPricing(t, catalog, removePriorityBases)
		pricing, err := billing.GetModelPricingWithChannel("gemini-priority-fallback", &ChannelModelPricing{
			CacheWritePrice: testPtrFloat64(0.000010),
			CacheReadPrice:  testPtrFloat64(0.000001),
		})
		require.NoError(t, err)
		cost := billing.computeTokenBreakdown(pricing, tokens, 1, "priority", true)
		require.InDelta(t, 0.00043, cost.CacheCreationCost, 1e-12,
			"channel 10e-6 base remains authoritative under inherited x1.8/x2.5 priority ratios")
		require.InDelta(t, 0.000036, cost.CacheReadCost, 1e-12,
			"channel 1e-6 read base remains authoritative under inherited x3.6 priority ratio")
		require.Equal(t, PricingSourceLiteLLM, pricing.provenance.LongContextCacheWrite5mPriority)
		require.Equal(t, PricingSourceLiteLLM, pricing.provenance.LongContextCacheWrite1hPriority)
		require.Equal(t, PricingSourceLiteLLM, pricing.provenance.LongContextCacheReadPriority)
	})

	t.Run("positive priority above price over explicit zero base fails closed", func(t *testing.T) {
		zeroPriorityBases := `{"gemini-priority-fallback":{
			"cache_creation_input_token_cost_priority":0,
			"cache_creation_input_token_cost_above_1hr_priority":0,
			"cache_read_input_token_cost_priority":0}}`
		_, billing := parseFixRoundPricing(t, catalog, zeroPriorityBases)
		resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "gemini-priority-fallback"})
		_, err := billing.CalculateCostWithServiceTier("gemini-priority-fallback", tokens, 1, "priority")
		require.ErrorIs(t, err, ErrModelPricingUnavailable)
		require.Equal(t, PricingSourceOverrideFile, resolved.Provenance.CacheWrite5mPriority)
		require.Equal(t, PricingSourceOverrideFile, resolved.Provenance.CacheWrite1hPriority)
		require.Equal(t, PricingSourceOverrideFile, resolved.Provenance.CacheReadPriority)
		require.Equal(t, PricingSourceLiteLLM, resolved.Provenance.LongContextCacheWrite5mPriority)
		require.Equal(t, PricingSourceLiteLLM, resolved.Provenance.LongContextCacheWrite1hPriority)
		require.Equal(t, PricingSourceLiteLLM, resolved.Provenance.LongContextCacheReadPriority)
	})
}

func TestLongContextInvalidCacheAbove_RespectsFinalApplicationPolicy(t *testing.T) {
	const catalog = `{"invalid-policy-model":{
		"input_cost_per_token":0.000001,"input_cost_per_token_priority":0.000002,
		"output_cost_per_token":0.000002,"output_cost_per_token_priority":0.000004,
		"cache_creation_input_token_cost":0.000004,
		"cache_creation_input_token_cost_above_1hr":0.000006,
		"cache_creation_input_token_cost_priority":0,
		"cache_creation_input_token_cost_above_1hr_priority":0,
		"cache_read_input_token_cost":0.0000002,
		"cache_read_input_token_cost_priority":0,
		"input_cost_per_token_above_272k_tokens":0.000002,
		"output_cost_per_token_above_272k_tokens":0.000004,
		"cache_creation_input_token_cost_above_272k_tokens_priority":0.0000072,
		"cache_creation_input_token_cost_above_1hr_above_272k_tokens_priority":0.000015,
		"cache_read_input_token_cost_above_272k_tokens_priority":0.00000072,
		"supports_service_tier":true}}`
	_, billing := parseFixRoundPricing(t, catalog, "")
	resolver := NewModelPricingResolver(nil, billing)
	baseResolved := resolver.Resolve(context.Background(), PricingInput{Model: "invalid-policy-model"})
	require.NotNil(t, baseResolved)
	above := UsageTokens{InputTokens: 272001, CacheReadTokens: 10}

	t.Run("unified enabled rejects active used priority bucket", func(t *testing.T) {
		_, err := billing.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "invalid-policy-model", Tokens: above,
			RateMultiplier: 1, ServiceTier: "priority", Resolver: resolver, Resolved: baseResolved,
		})
		require.ErrorIs(t, err, ErrModelPricingUnavailable)
	})

	t.Run("unified group off does not activate official ladder", func(t *testing.T) {
		resolved := *baseResolved
		resolved.longContextPricingEnabled = false
		cost, err := billing.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "invalid-policy-model", Tokens: above,
			RateMultiplier: 1, ServiceTier: "priority", Resolver: resolver, Resolved: &resolved,
		})
		require.NoError(t, err)
		require.False(t, cost.LongContextBillingApplied)
	})

	t.Run("unified interval takeover does not validate official ladder", func(t *testing.T) {
		resolved := *baseResolved
		resolved.Intervals = []PricingInterval{{MinTokens: 0, CacheReadPrice: testPtrFloat64(0.000001)}}
		cost, err := billing.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "invalid-policy-model", Tokens: above,
			RateMultiplier: 1, ServiceTier: "priority", Resolver: resolver, Resolved: &resolved,
		})
		require.NoError(t, err)
		require.False(t, cost.LongContextBillingApplied)
	})

	t.Run("legacy policy false does not validate official ladder", func(t *testing.T) {
		cost, err := billing.calculateCostWithServiceTierPolicy("invalid-policy-model", above, 1, "priority", false)
		require.NoError(t, err)
		require.False(t, cost.LongContextBillingApplied)
	})

	for _, tt := range []struct {
		name   string
		tokens UsageTokens
		tier   string
	}{
		{name: "below threshold", tokens: UsageTokens{InputTokens: 10, CacheReadTokens: 10}, tier: "priority"},
		{name: "standard tier mismatch", tokens: above, tier: "standard"},
		{name: "unused invalid bucket", tokens: UsageTokens{InputTokens: 272001, OutputTokens: 1}, tier: "priority"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := billing.CalculateCostWithServiceTier("invalid-policy-model", tt.tokens, 1, tt.tier)
			require.NoError(t, err)
			require.NotNil(t, cost)
		})
	}
}

func TestLongContextCacheAboveRatios_NonFiniteOrNonPositiveFailClosedPerBucket(t *testing.T) {
	const catalog = `{
		"standard-underflow":{
			"input_cost_per_token":0.000001,"input_cost_per_token_priority":0.000002,
			"output_cost_per_token":0.000002,"output_cost_per_token_priority":0.000004,
			"cache_creation_input_token_cost":1e308,"cache_creation_input_token_cost_above_1hr":1e308,
			"cache_creation_input_token_cost_priority":0.000002,"cache_creation_input_token_cost_above_1hr_priority":0.000002,
			"cache_read_input_token_cost":1e308,"cache_read_input_token_cost_priority":0.000002,
			"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_272k_tokens":5e-324,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens":5e-324,
			"cache_read_input_token_cost_above_272k_tokens":5e-324,
			"cache_creation_input_token_cost_above_272k_tokens_priority":0.000004,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens_priority":0.000004,
			"cache_read_input_token_cost_above_272k_tokens_priority":0.000004,
			"supports_service_tier":true},
		"priority-overflow":{
			"input_cost_per_token":0.000001,"input_cost_per_token_priority":0.000002,
			"output_cost_per_token":0.000002,"output_cost_per_token_priority":0.000004,
			"cache_creation_input_token_cost":0.000002,"cache_creation_input_token_cost_above_1hr":0.000002,
			"cache_creation_input_token_cost_priority":5e-324,"cache_creation_input_token_cost_above_1hr_priority":5e-324,
			"cache_read_input_token_cost":0.000002,"cache_read_input_token_cost_priority":5e-324,
			"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens":0.000004,
			"cache_read_input_token_cost_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_272k_tokens_priority":1e308,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens_priority":1e308,
			"cache_read_input_token_cost_above_272k_tokens_priority":1e308,
			"supports_service_tier":true},
		"zero-above-free":{
			"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
			"cache_creation_input_token_cost":0.000002,"cache_creation_input_token_cost_above_1hr":0.000002,
			"cache_read_input_token_cost":0.000002,
			"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_272k_tokens":0,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens":0,
			"cache_read_input_token_cost_above_272k_tokens":0},
		"zero-base-invalid":{
			"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
			"cache_creation_input_token_cost":0,"cache_creation_input_token_cost_above_1hr":0,
			"cache_read_input_token_cost":0,
			"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_272k_tokens":0.000004,
			"cache_creation_input_token_cost_above_1hr_above_272k_tokens":0.000004,
			"cache_read_input_token_cost_above_272k_tokens":0.000004},
		"absent-base-invalid":{
			"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,
			"input_cost_per_token_above_272k_tokens":0.000002,"output_cost_per_token_above_272k_tokens":0.000004,
			"cache_read_input_token_cost_above_272k_tokens":0.000004}}`
	_, billing := parseFixRoundPricing(t, catalog, "")
	standardUnderflow, err := billing.GetModelPricing("standard-underflow")
	require.NoError(t, err)
	require.True(t, standardUnderflow.longContextCacheReadInvalid)
	require.True(t, standardUnderflow.longContextCacheWrite5mInvalid)
	require.True(t, standardUnderflow.longContextCacheWrite1hInvalid)
	require.Equal(t, PricingSourceLiteLLM, standardUnderflow.provenance.LongContextCacheRead)
	require.Equal(t, PricingSourceLiteLLM, standardUnderflow.provenance.LongContextCacheWrite5m)
	require.Equal(t, PricingSourceLiteLLM, standardUnderflow.provenance.LongContextCacheWrite1h)
	priorityOverflow, err := billing.GetModelPricing("priority-overflow")
	require.NoError(t, err)
	require.True(t, priorityOverflow.longContextCacheReadPriorityInvalid)
	require.True(t, priorityOverflow.longContextCacheWrite5mPriorityInvalid)
	require.True(t, priorityOverflow.longContextCacheWrite1hPriorityInvalid)
	require.Equal(t, PricingSourceLiteLLM, priorityOverflow.provenance.LongContextCacheReadPriority)
	require.Equal(t, PricingSourceLiteLLM, priorityOverflow.provenance.LongContextCacheWrite5mPriority)
	require.Equal(t, PricingSourceLiteLLM, priorityOverflow.provenance.LongContextCacheWrite1hPriority)
	zeroAbove, err := billing.GetModelPricing("zero-above-free")
	require.NoError(t, err)
	require.True(t, zeroAbove.longContextCacheReadConfigured)
	require.True(t, zeroAbove.longContextCacheWrite5mConfigured)
	require.True(t, zeroAbove.longContextCacheWrite1hConfigured)
	require.Equal(t, PricingSourceLiteLLM, zeroAbove.provenance.LongContextCacheRead)
	require.Equal(t, PricingSourceLiteLLM, zeroAbove.provenance.LongContextCacheWrite5m)
	require.Equal(t, PricingSourceLiteLLM, zeroAbove.provenance.LongContextCacheWrite1h)

	bucketTokens := func(bucket string) UsageTokens {
		tokens := UsageTokens{InputTokens: 272001}
		switch bucket {
		case "read":
			tokens.CacheReadTokens = 1
		case "5m":
			tokens.CacheCreationTokens = 1
			tokens.CacheCreation5mTokens = 1
		case "1h":
			tokens.CacheCreationTokens = 1
			tokens.CacheCreation1hTokens = 1
		}
		return tokens
	}
	for _, tt := range []struct {
		name, model, tier, bucket string
	}{
		{name: "standard read underflow", model: "standard-underflow", tier: "standard", bucket: "read"},
		{name: "standard 5m underflow", model: "standard-underflow", tier: "standard", bucket: "5m"},
		{name: "standard 1h underflow", model: "standard-underflow", tier: "standard", bucket: "1h"},
		{name: "priority read overflow", model: "priority-overflow", tier: "priority", bucket: "read"},
		{name: "priority 5m overflow", model: "priority-overflow", tier: "priority", bucket: "5m"},
		{name: "priority 1h overflow", model: "priority-overflow", tier: "priority", bucket: "1h"},
		{name: "standard explicit zero base", model: "zero-base-invalid", tier: "standard", bucket: "read"},
		{name: "standard absent base", model: "absent-base-invalid", tier: "standard", bucket: "read"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := billing.CalculateCostWithServiceTier(tt.model, bucketTokens(tt.bucket), 1, tt.tier)
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
			require.Nil(t, cost, "an unrepresentable ratio must not leak a zero or infinite billed cost")
		})
	}

	for _, tt := range []struct {
		name, model, tier, bucket string
	}{
		{name: "standard invalid does not poison priority read", model: "standard-underflow", tier: "priority", bucket: "read"},
		{name: "priority invalid does not poison standard 5m", model: "priority-overflow", tier: "standard", bucket: "5m"},
		{name: "invalid buckets unused", model: "priority-overflow", tier: "priority", bucket: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := billing.CalculateCostWithServiceTier(tt.model, bucketTokens(tt.bucket), 1, tt.tier)
			require.NoError(t, err)
			require.NotNil(t, cost)
			require.False(t, math.IsNaN(cost.TotalCost))
			require.False(t, math.IsInf(cost.TotalCost, 0))
		})
	}

	for _, bucket := range []string{"read", "5m", "1h"} {
		t.Run("explicit zero above is configured free "+bucket, func(t *testing.T) {
			cost, err := billing.CalculateCostWithServiceTier("zero-above-free", bucketTokens(bucket), 1, "standard")
			require.NoError(t, err)
			if bucket == "read" {
				require.Zero(t, cost.CacheReadCost)
			} else {
				require.Zero(t, cost.CacheCreationCost)
			}
		})
	}
}

func TestPricingJSONDuplicateModelKeysAreRejected(t *testing.T) {
	svc := NewPricingService(&config.Config{}, nil)
	_, err := svc.parsePricingData([]byte(`{
		"duplicate-model":{"input_cost_per_token":0.000001},
		"duplicate-model":{"input_cost_per_token":0.000009}}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate model key")
}

func TestPricingOverrideDuplicateModelKeysRejectWholeOptionalFile(t *testing.T) {
	pricing, _ := parseFixRoundPricing(t,
		`{"duplicate-model":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}}`,
		`{"duplicate-model":{"input_cost_per_token":0.000009},"duplicate-model":{"input_cost_per_token":0.000008}}`)
	require.InDelta(t, 0.000001, pricing.pricingData["duplicate-model"].InputCostPerToken, 1e-12,
		"a duplicate-key optional override is rejected as a unit; map iteration must not silently choose a value")
}
