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

func TestPublicModelCatalog_IncludesClaude5ModelsInReleaseOrder(t *testing.T) {
	type expectation struct {
		displayName      string
		releasedAt       string
		input            float64
		cacheRead        float64
		cacheWrite5m     float64
		cacheWrite1h     float64
		output           float64
		sourceURL        string
		contextSourceURL string
	}

	expected := map[string]expectation{
		"claude-fable-5-1": {
			displayName:      "Claude Fable 5.1",
			releasedAt:       "2026-09-01",
			input:            10,
			cacheRead:        0.25,
			cacheWrite5m:     12.5,
			cacheWrite1h:     20,
			output:           50,
			sourceURL:        "https://platform.claude.com/docs/en/models/fable-5-1/overview",
			contextSourceURL: "https://platform.claude.com/docs/en/models/fable-5-1/overview",
		},
		"claude-opus-5": {
			displayName:      "Claude Opus 5",
			releasedAt:       "2026-07-24",
			input:            5,
			cacheRead:        0.5,
			output:           25,
			sourceURL:        "https://www.anthropic.com/news/claude-opus-5",
			contextSourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5.html",
		},
		"claude-fable-5": {
			displayName:      "Claude Fable 5",
			releasedAt:       "2026-06-09",
			input:            10,
			cacheRead:        1,
			cacheWrite5m:     12.5,
			cacheWrite1h:     20,
			output:           50,
			sourceURL:        "https://www.anthropic.com/news/claude-fable-5-mythos-5",
			contextSourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5.html",
		},
	}

	models := PublicModelCatalogModelsForWebChat()
	found := make(map[string]struct{}, len(expected))
	anthropicModelNames := make([]string, 0)
	for idx := range models {
		model := &models[idx]
		if model.Provider != "anthropic" {
			continue
		}
		anthropicModelNames = append(anthropicModelNames, model.ModelName)
		want, ok := expected[model.ModelName]
		if !ok {
			continue
		}

		found[model.ModelName] = struct{}{}
		require.Equal(t, "Anthropic", model.ProviderName)
		require.Equal(t, want.displayName, model.DisplayName)
		require.Equal(t, []string{"text"}, model.Modalities)
		require.ElementsMatch(t, []string{"chat", "reasoning", "vision input", "tool use", "prompt caching"}, model.Features)
		require.Equal(t, want.releasedAt, model.ReleasedAt)
		require.Equal(t, "confirmed", model.ReleaseStatus)
		require.Equal(t, "2026-09-03", model.UpdatedAt)
		require.Equal(t, 1_000_000, model.ContextWindow)
		require.Equal(t, want.sourceURL, model.SourceURL)
		require.Equal(t, want.contextSourceURL, model.ContextSourceURL)
		require.Equal(t, "confirmed", model.PriceStatus)
		require.NotNil(t, model.Pricing.InputPerMillion)
		require.NotNil(t, model.Pricing.CacheReadPerMillion)
		require.NotNil(t, model.Pricing.OutputPerMillion)
		require.Equal(t, want.input, *model.Pricing.InputPerMillion)
		require.Equal(t, want.cacheRead, *model.Pricing.CacheReadPerMillion)
		require.Equal(t, want.output, *model.Pricing.OutputPerMillion)
		if want.cacheWrite5m > 0 || want.cacheWrite1h > 0 {
			priceLines := make(map[string]PublicModelCatalogPriceLine, len(model.Pricing.PriceLines))
			for _, line := range model.Pricing.PriceLines {
				priceLines[line.Label] = line
			}
			require.Equal(t, PublicModelCatalogPriceLine{Label: "5m cache write", Amount: want.cacheWrite5m, Unit: "1M tokens"}, priceLines["5m cache write"])
			require.Equal(t, PublicModelCatalogPriceLine{Label: "1h cache write", Amount: want.cacheWrite1h, Unit: "1M tokens"}, priceLines["1h cache write"])
		}
	}

	require.Len(t, found, len(expected))
	require.GreaterOrEqual(t, len(anthropicModelNames), 5)
	require.Equal(t, []string{"claude-fable-5-1", "claude-opus-5", "claude-sonnet-5", "claude-opus-4-8", "claude-fable-5"}, anthropicModelNames[:5])
}

func TestPublicModelCatalog_IncludesGPT56VariantsInReleaseOrder(t *testing.T) {
	type expectation struct {
		displayName string
		input       float64
		cacheRead   float64
		output      float64
	}

	expected := map[string]expectation{
		"gpt-5.6-sol":   {displayName: "GPT-5.6 Sol", input: 4, cacheRead: 0.4, output: 20},
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
		require.Equal(t, "2026-09-03", model.UpdatedAt)
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
