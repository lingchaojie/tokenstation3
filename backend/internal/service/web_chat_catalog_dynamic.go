package service

import (
	"context"
	"sort"
	"strings"
)

type webChatDefaultGroupResolver interface {
	GetDefaultAPIKeyGroupID(ctx context.Context, keyType string) (*int64, error)
}

type webChatAccountLister interface {
	ListByGroup(ctx context.Context, groupID int64) ([]Account, error)
}

type webChatDefaultGroupSpec struct {
	Provider string
	KeyType  string
	Family   webChatModelFamily
}

// webChatDefaultGroupSpecs is the extensibility point: to add Gemini/GLM later,
// add a setting key (e.g. default_gemini_group_id) + append a spec here.
var webChatDefaultGroupSpecs = []webChatDefaultGroupSpec{
	{Provider: "anthropic", KeyType: APIKeyTypeAnthropic, Family: webChatFamilyClaude},
	{Provider: "openai", KeyType: APIKeyTypeOpenAI, Family: webChatFamilyGPT},
}

// resolveWebChatCatalog builds the WebChat model list from the two default
// groups' account model_mappings. Provider is decided by source group; a model
// whose family doesn't match the group's family is skipped (dedup) so e.g.
// cross-mapped claude-* on the OpenAI account isn't served via the OpenAI path.
// Metadata is enriched from the static catalog by normalized name; unmatched
// models fall back to per-family capability defaults so they remain usable.
func resolveWebChatCatalog(ctx context.Context, groups webChatDefaultGroupResolver, accounts webChatAccountLister) ([]WebChatModelCapability, error) {
	seen := map[string]struct{}{}
	out := make([]WebChatModelCapability, 0, 16)

	catalog := map[string]WebChatCatalogModel{}
	for _, m := range DefaultWebChatCatalogModels() {
		key := strings.ToLower(m.Provider) + "\x00" + normalizeWebChatModelName(m.ModelName)
		catalog[key] = m
	}

	for _, spec := range webChatDefaultGroupSpecs {
		gid, err := groups.GetDefaultAPIKeyGroupID(ctx, spec.KeyType)
		if err != nil {
			return nil, err
		}
		if gid == nil {
			continue
		}
		accts, err := accounts.ListByGroup(ctx, *gid)
		if err != nil {
			return nil, err
		}
		for i := range accts {
			if accts[i].Status != StatusActive {
				continue
			}
			for rawKey := range accts[i].GetModelMapping() {
				base := normalizeWebChatModelName(rawKey)
				if resolveWebChatModelFamily(base) != spec.Family {
					continue
				}
				if _, dup := seen[base]; dup {
					continue
				}
				seen[base] = struct{}{}
				out = append(out, buildWebChatCapability(spec.Provider, base, catalog))
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func buildWebChatCapability(provider, model string, catalog map[string]WebChatCatalogModel) WebChatModelCapability {
	var caps WebChatModelCapability
	key := provider + "\x00" + model
	if cm, ok := catalog[key]; ok {
		cm.Provider = provider
		cm.ModelName = model
		if built, ok := WebChatModelCapabilityFromCatalogModel(cm); ok {
			caps = built
		}
	}
	if caps.Model == "" {
		if route, ok := webChatProviderRoutes[provider]; ok {
			caps.Platform = route.Platform
			caps.KeyType = route.KeyType
		}
		caps.Provider = provider
		caps.Model = model
		caps.DisplayName = model
		caps.SupportsText = true
		caps.SupportsFileContext = true
		caps.PriceStatus = "unverified"
	}
	fam := ResolveWebChatModelCapability(provider, model)
	caps.SupportsThinking = fam.SupportsThinking
	caps.ThinkingEfforts = fam.ThinkingEfforts
	return caps
}
