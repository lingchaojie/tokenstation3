package repository

import (
	"encoding/json/jsontext"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityPreservesModelPricingAndLongContextPolicy(t *testing.T) {
	entity := &dbent.Group{LongContextPricingEnabled: true, ModelPricing: jsontext.Value(`[{"models":["claude-fable-5-1"],"input_price":0,"reasoning_effort_multipliers":{"max":1.5}}]`)}
	got := groupEntityToService(entity)
	require.True(t, got.LongContextPricingEnabled)
	require.Len(t, got.ModelPricing, 1)
	require.NotNil(t, got.ModelPricing[0].InputPrice)
	require.Zero(t, *got.ModelPricing[0].InputPrice)
	require.Equal(t, map[string]float64{"max": 1.5}, got.ModelPricing[0].ReasoningEffortMultipliers)
}
