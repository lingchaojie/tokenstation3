package domain

import "strings"

// GroupModelsListConfig controls the optional custom /v1/models response list.
type GroupModelsListConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models,omitempty"`
}

// Allows reports whether a model belongs in the configured model-list response.
// This method is for response projection only; it must not be used for request admission.
func (c GroupModelsListConfig) Allows(model string) bool {
	if !c.Enabled {
		return true
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	for _, configured := range c.Models {
		configured = strings.TrimPrefix(strings.TrimSpace(configured), "models/")
		if configured == model {
			return true
		}
		if strings.HasSuffix(configured, "*") && strings.HasPrefix(model, strings.TrimSuffix(configured, "*")) {
			return true
		}
	}
	return false
}

// FilterForListing keeps configured models in configuration order when they are
// present in the discovered source catalog.
func (c GroupModelsListConfig) FilterForListing(source []string) []string {
	if !c.Enabled || len(c.Models) == 0 {
		return source
	}
	type availableModel struct{ key, original string }
	available := make([]availableModel, 0, len(source))
	for _, model := range source {
		key := strings.TrimPrefix(strings.TrimSpace(model), "models/")
		if key != "" {
			available = append(available, availableModel{key: key, original: model})
		}
	}
	seen := make(map[string]struct{}, len(c.Models))
	filtered := make([]string, 0, len(c.Models))
	for _, configured := range c.Models {
		key := strings.TrimPrefix(strings.TrimSpace(configured), "models/")
		wildcard := strings.HasSuffix(key, "*")
		prefix := strings.TrimSuffix(key, "*")
		for _, model := range available {
			if (!wildcard && model.key != key) || (wildcard && !strings.HasPrefix(model.key, prefix)) {
				continue
			}
			if _, ok := seen[model.key]; ok {
				continue
			}
			seen[model.key] = struct{}{}
			filtered = append(filtered, model.original)
			if !wildcard {
				break
			}
		}
	}
	return filtered
}
