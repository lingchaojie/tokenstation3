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
