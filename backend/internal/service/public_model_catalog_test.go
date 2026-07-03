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
