package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicModelCatalog_QwenUsesAlibabaCloudDisplayProvider(t *testing.T) {
	models := PublicModelCatalogModelsForWebChat()
	var qwenPlus *PublicModelCatalogModel
	for idx := range models {
		if models[idx].Provider == "qwen" && models[idx].ModelName == "qwen3.5-plus" {
			qwenPlus = &models[idx]
			break
		}
	}

	require.NotNil(t, qwenPlus)
	require.Equal(t, "qwen", qwenPlus.Provider)
	require.Equal(t, "Alibaba Cloud", qwenPlus.ProviderName)

	providers := PublicModelCatalogProviders(models)
	var qwenProvider *PublicModelCatalogProvider
	for idx := range providers {
		if providers[idx].Key == "qwen" {
			qwenProvider = &providers[idx]
			break
		}
	}

	require.NotNil(t, qwenProvider)
	require.Equal(t, "Alibaba Cloud", qwenProvider.Name)
	require.Greater(t, qwenProvider.ModelCount, 0)
}

func TestPublicModelCatalog_IncludesClaudeSonnet5(t *testing.T) {
	models := PublicModelCatalogModelsForWebChat()
	var sonnet5 *PublicModelCatalogModel
	for idx := range models {
		if models[idx].Provider == "anthropic" && models[idx].ModelName == "claude-sonnet-5" {
			sonnet5 = &models[idx]
			break
		}
	}

	require.NotNil(t, sonnet5)
	require.Equal(t, "Claude Sonnet 5", sonnet5.DisplayName)
	require.Equal(t, "confirmed", sonnet5.PriceStatus)
	require.Equal(t, "confirmed", sonnet5.ReleaseStatus)
	require.Equal(t, "2026-06-30", sonnet5.ReleasedAt)
	require.Equal(t, 1_000_000, sonnet5.ContextWindow)
	require.Equal(t, sourceAnthropic, sonnet5.SourceURL)
	require.Equal(t, contextSourceAnthropic, sonnet5.ContextSourceURL)
	require.NotNil(t, sonnet5.Pricing.InputPerMillion)
	require.NotNil(t, sonnet5.Pricing.OutputPerMillion)
	require.NotNil(t, sonnet5.Pricing.CacheReadPerMillion)
	require.Equal(t, 2.0, *sonnet5.Pricing.InputPerMillion)
	require.Equal(t, 10.0, *sonnet5.Pricing.OutputPerMillion)
	require.Equal(t, 0.2, *sonnet5.Pricing.CacheReadPerMillion)
}

func TestPublicModelCatalog_IncludesGPT56VariantsInReleaseOrder(t *testing.T) {
	type expectation struct {
		displayName string
		input       float64
		cacheRead   float64
		output      float64
	}

	expected := map[string]expectation{
		"gpt-5.6-sol":   {displayName: "GPT-5.6 Sol", input: 5, cacheRead: 0.5, output: 30},
		"gpt-5.6-terra": {displayName: "GPT-5.6 Terra", input: 2.5, cacheRead: 0.25, output: 15},
		"gpt-5.6-luna":  {displayName: "GPT-5.6 Luna", input: 1, cacheRead: 0.1, output: 6},
	}

	models := PublicModelCatalogModelsForWebChat()
	found := make(map[string]struct{}, len(expected))
	openAIModelNames := make([]string, 0)
	for idx := range models {
		model := &models[idx]
		if model.Provider != "openai" {
			continue
		}
		openAIModelNames = append(openAIModelNames, model.ModelName)
		want, ok := expected[model.ModelName]
		if !ok {
			continue
		}

		found[model.ModelName] = struct{}{}
		require.Equal(t, "OpenAI", model.ProviderName)
		require.Equal(t, want.displayName, model.DisplayName)
		require.Equal(t, []string{"text"}, model.Modalities)
		require.ElementsMatch(t, []string{"chat", "reasoning", "vision input", "tool use", "prompt caching"}, model.Features)
		require.Equal(t, "2026-07-09", model.ReleasedAt)
		require.Equal(t, "confirmed", model.ReleaseStatus)
		require.Equal(t, "2026-07-15", model.UpdatedAt)
		require.Equal(t, 1_050_000, model.ContextWindow)
		require.Equal(t, sourceOpenAI, model.SourceURL)
		require.Equal(t, contextSourceOpenAI, model.ContextSourceURL)
		require.Equal(t, "confirmed", model.PriceStatus)
		require.NotNil(t, model.Pricing.InputPerMillion)
		require.NotNil(t, model.Pricing.CacheReadPerMillion)
		require.NotNil(t, model.Pricing.OutputPerMillion)
		require.Equal(t, want.input, *model.Pricing.InputPerMillion)
		require.Equal(t, want.cacheRead, *model.Pricing.CacheReadPerMillion)
		require.Equal(t, want.output, *model.Pricing.OutputPerMillion)
	}

	require.Len(t, found, len(expected))
	require.GreaterOrEqual(t, len(openAIModelNames), 4)
	require.Equal(t, []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.5"}, openAIModelNames[:4])
}
