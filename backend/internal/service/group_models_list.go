package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// DomainGroupModelsListConfig converts the display-only service configuration
// to the domain value persisted by ent.
func DomainGroupModelsListConfig(cfg GroupModelsListConfig) domain.GroupModelsListConfig {
	return domain.GroupModelsListConfig(cfg)
}

// GroupModelsListConfigFromDomain converts the persisted display configuration
// to the service representation.
func GroupModelsListConfigFromDomain(cfg domain.GroupModelsListConfig) GroupModelsListConfig {
	return GroupModelsListConfig(cfg)
}

func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{Enabled: cfg.Enabled}
	if len(cfg.Models) == 0 {
		return out
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	out.Models = make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	return out
}

func (g *Group) CustomModelsListEnabled() bool {
	return g != nil && g.ModelsListConfig.Enabled && len(g.ModelsListConfig.Models) > 0
}
