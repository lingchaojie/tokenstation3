package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const modelCatalogCacheTTL = 10 * time.Minute

var ErrModelCatalogCacheMiss = errors.New("model catalog cache miss")

type ModelCatalogCache interface {
	GetModelCatalog(ctx context.Context, groupID int64, platform string) ([]string, error)
	SetModelCatalog(ctx context.Context, groupID int64, platform string, models []string, ttl time.Duration) error
}

func (s *GatewayService) GetConfiguredModelCatalog(ctx context.Context, group *Group) ([]string, error) {
	if s == nil || s.accountRepo == nil || group == nil || group.ID <= 0 {
		return []string{}, nil
	}

	models, err := s.loadConfiguredModelCatalog(ctx, group)
	if err != nil {
		return nil, err
	}
	return filterConfiguredCatalogByCustomList(models, group), nil
}

func (s *GatewayService) loadConfiguredModelCatalog(ctx context.Context, group *Group) ([]string, error) {
	platform := strings.TrimSpace(group.Platform)
	if s.modelCatalogCache != nil {
		models, err := s.modelCatalogCache.GetModelCatalog(ctx, group.ID, platform)
		if err == nil {
			return models, nil
		}
		if !errors.Is(err, ErrModelCatalogCacheMiss) {
			slog.Warn("model_catalog_cache_read_failed", "group_id", group.ID, "platform", platform, "error", err)
		}
	}

	candidatePlatforms := []string{platform}
	if platform == PlatformAnthropic || platform == PlatformGemini {
		candidatePlatforms = mixedSchedulingPlatforms(platform)
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		&group.ID,
		candidatePlatforms,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("list model catalog candidates: %w", err)
	}

	set := make(map[string]struct{})
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform && !accountEligibleForMixedPlatform(account, platform) {
			continue
		}
		if platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
			continue
		}
		for rawID := range account.GetModelMapping() {
			id := strings.TrimSpace(rawID)
			if id == "" || strings.Contains(id, "*") {
				continue
			}
			set[id] = struct{}{}
		}
	}

	models := make([]string, 0, len(set))
	for id := range set {
		models = append(models, id)
	}
	sort.Strings(models)
	if s.modelCatalogCache != nil {
		if err := s.modelCatalogCache.SetModelCatalog(ctx, group.ID, platform, models, modelCatalogCacheTTL); err != nil {
			slog.Warn("model_catalog_cache_write_failed", "group_id", group.ID, "platform", platform, "error", err)
		}
	}
	return models, nil
}

func filterConfiguredCatalogByCustomList(models []string, group *Group) []string {
	if group == nil || !group.CustomModelsListEnabled() {
		return append([]string{}, models...)
	}
	available := make(map[string]struct{}, len(models))
	for _, id := range models {
		available[id] = struct{}{}
	}
	selected := make(map[string]struct{}, len(group.ModelsListConfig.Models))
	for _, rawID := range group.ModelsListConfig.Models {
		if id := strings.TrimSpace(rawID); id != "" {
			if _, ok := available[id]; ok {
				selected[id] = struct{}{}
			}
		}
	}
	filtered := make([]string, 0, len(selected))
	for id := range selected {
		filtered = append(filtered, id)
	}
	sort.Strings(filtered)
	return filtered
}
