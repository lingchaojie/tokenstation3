package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelCatalogGroupResolver interface {
	ResolveModelCatalogGroups(context.Context, *service.APIKey) ([]*service.Group, error)
}

func (h *GatewayHandler) writeUnifiedModelCatalog(c *gin.Context, apiKey *service.APIKey) {
	if h.modelCatalogGroupResolver == nil {
		h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to resolve model catalog groups")
		return
	}
	if h.gatewayService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to load model catalog")
		return
	}

	groups, err := h.modelCatalogGroupResolver.ResolveModelCatalogGroups(c.Request.Context(), apiKey)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to resolve model catalog groups")
		return
	}

	set := make(map[string]struct{})
	for _, group := range groups {
		models, err := h.gatewayService.GetConfiguredModelCatalog(c.Request.Context(), group)
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "internal_error", "failed to load model catalog")
			return
		}
		for _, id := range models {
			set[id] = struct{}{}
		}
	}

	models := make([]string, 0, len(set))
	for id := range set {
		models = append(models, id)
	}
	sort.Strings(models)

	if c.Query("client_version") != "" {
		manifest := make([]gin.H, 0, len(models))
		for _, id := range models {
			manifest = append(manifest, gin.H{"slug": id})
		}
		c.JSON(http.StatusOK, gin.H{"models": manifest})
		return
	}

	writeOpenAIModelsList(c, models)
}
