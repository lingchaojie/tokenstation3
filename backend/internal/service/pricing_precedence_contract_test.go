//go:build unit

package service

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// setPricingOverrideFileByContract keeps this regression fixture compilable on
// the clean first parent, where the approved configuration field did not yet
// exist. In that tree the override cannot take effect and the numeric
// assertions below provide the genuine behavioral RED.
func setPricingOverrideFileByContract(cfg *config.Config, path string) {
	pricing := reflect.ValueOf(&cfg.Pricing).Elem()
	field := pricing.FieldByName("OverrideFile")
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
		field.SetString(path)
	}
}

func TestPricingOverrideFilePrecedence_ExplicitZeroBeatsBuiltInCatalog(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "pricing-overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"precedence-model": {
			"input_cost_per_token": 0.000009,
			"output_cost_per_token": 0
		}
	}`), 0o600))

	cfg := &config.Config{}
	setPricingOverrideFileByContract(cfg, overridePath)
	svc := &PricingService{cfg: cfg}
	data, err := svc.parsePricingData([]byte(`{
		"precedence-model": {
			"litellm_provider": "approved",
			"mode": "chat",
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000002
		}
	}`))
	require.NoError(t, err)

	got := data["precedence-model"]
	require.NotNil(t, got)
	require.InDelta(t, 0.000009, got.InputCostPerToken, 1e-12,
		"override_file must beat the approved built-in catalog")
}

func TestPricingOverrideFilePrecedence_ExplicitZeroRetainsOutputBucketPresence(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "pricing-overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"free-output-model": {"output_cost_per_token": 0}
	}`), 0o600))

	cfg := &config.Config{}
	setPricingOverrideFileByContract(cfg, overridePath)
	svc := &PricingService{cfg: cfg}
	data, err := svc.parsePricingData([]byte(`{
		"free-output-model": {
			"litellm_provider": "approved",
			"mode": "chat",
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000002
		}
	}`))
	require.NoError(t, err)

	got := data["free-output-model"]
	require.NotNil(t, got)
	require.Zero(t, got.OutputCostPerToken,
		"an explicit numeric zero is a real free price, not a missing price")
	require.True(t, got.OutputCostPerTokenConfigured,
		"explicit zero must retain bucket presence independently of input")
}

func TestPricingCatalogTierPrecedence_AboveTierFieldsProduceLongContextPricing(t *testing.T) {
	svc := &PricingService{cfg: &config.Config{}}
	data, err := svc.parsePricingData([]byte(`{
		"tiered-model": {
			"litellm_provider": "approved",
			"mode": "chat",
			"input_cost_per_token": 0.000001,
			"output_cost_per_token": 0.000002,
			"input_cost_per_token_above_200k_tokens": 0.000003,
			"output_cost_per_token_above_200k_tokens": 0.000008
		}
	}`))
	require.NoError(t, err)

	got := data["tiered-model"]
	require.NotNil(t, got)
	require.Equal(t, 200000, got.LongContextInputTokenThreshold)
	require.InDelta(t, 3.0, got.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 4.0, got.LongContextOutputCostMultiplier, 1e-12)
}
