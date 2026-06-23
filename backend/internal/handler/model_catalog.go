package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

func publicModelCatalogModelsSnapshot() []dto.PublicModelCatalogModel {
	return publicModelCatalogDTOModels(service.PublicModelCatalogModelsForWebChat())
}

func PublicModelCatalogModelsForWebChat() []dto.PublicModelCatalogModel {
	return publicModelCatalogModelsSnapshot()
}

func publicModelCatalogProviders(_ []dto.PublicModelCatalogModel) []dto.PublicModelCatalogProvider {
	return publicModelCatalogDTOProviders(service.PublicModelCatalogProviders(service.PublicModelCatalogModelsForWebChat()))
}

func publicModelCatalogDTOModels(models []service.PublicModelCatalogModel) []dto.PublicModelCatalogModel {
	out := make([]dto.PublicModelCatalogModel, 0, len(models))
	for _, model := range models {
		out = append(out, dto.PublicModelCatalogModel{
			Provider:         model.Provider,
			ProviderName:     model.ProviderName,
			ModelName:        model.ModelName,
			DisplayName:      model.DisplayName,
			Modalities:       append([]string(nil), model.Modalities...),
			Description:      model.Description,
			ContextWindow:    model.ContextWindow,
			ContextSourceURL: model.ContextSourceURL,
			Features:         append([]string(nil), model.Features...),
			Pricing:          publicModelCatalogDTOPricing(model.Pricing),
			PriceStatus:      model.PriceStatus,
			ReleasedAt:       model.ReleasedAt,
			ReleaseStatus:    model.ReleaseStatus,
			SourceURL:        model.SourceURL,
			UpdatedAt:        model.UpdatedAt,
		})
	}
	return out
}

func publicModelCatalogDTOPricing(pricing service.PublicModelCatalogPricing) dto.PublicModelCatalogPricing {
	lines := make([]dto.PublicModelCatalogPriceLine, 0, len(pricing.PriceLines))
	for _, line := range pricing.PriceLines {
		lines = append(lines, dto.PublicModelCatalogPriceLine{
			Label:  line.Label,
			Amount: line.Amount,
			Unit:   line.Unit,
		})
	}
	return dto.PublicModelCatalogPricing{
		Currency:            pricing.Currency,
		Unit:                pricing.Unit,
		InputPerMillion:     cloneFloat64Ptr(pricing.InputPerMillion),
		OutputPerMillion:    cloneFloat64Ptr(pricing.OutputPerMillion),
		CacheReadPerMillion: cloneFloat64Ptr(pricing.CacheReadPerMillion),
		PriceLines:          lines,
		Note:                pricing.Note,
	}
}

func publicModelCatalogDTOProviders(providers []service.PublicModelCatalogProvider) []dto.PublicModelCatalogProvider {
	out := make([]dto.PublicModelCatalogProvider, 0, len(providers))
	for _, provider := range providers {
		out = append(out, dto.PublicModelCatalogProvider{
			Key:         provider.Key,
			Name:        provider.Name,
			AccentColor: provider.AccentColor,
			ModelCount:  provider.ModelCount,
		})
	}
	return out
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// GetPublicModelCatalog returns the admin-only model marketplace catalog.
// GET /api/v1/admin/settings/model-catalog
func (h *SettingHandler) GetPublicModelCatalog(c *gin.Context) {
	models := publicModelCatalogModelsSnapshot()
	response.Success(c, dto.PublicModelCatalogResponse{
		UpdatedAt: service.PublicModelCatalogUpdatedAt,
		Providers: publicModelCatalogProviders(models),
		Models:    models,
	})
}
