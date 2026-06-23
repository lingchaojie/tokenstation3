package service

import (
	"context"
	"strings"
)

const (
	KiloDefaultOpenRouterBaseURL = "https://api.kilo.ai/api/openrouter"
	KiloDefaultAPIBaseURL        = "https://api.kilo.ai/api"
	KiloDefaultUserAgent         = "cli-proxy-kilo"
)

type openAICompatiblePlatformCtxKey struct{}

func IsOpenAICompatiblePlatform(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformKilo
}

func NormalizeOpenAICompatiblePlatform(platform string) string {
	if platform == PlatformKilo {
		return PlatformKilo
	}
	return PlatformOpenAI
}

func WithOpenAICompatiblePlatform(ctx context.Context, platform string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAICompatiblePlatformCtxKey{}, NormalizeOpenAICompatiblePlatform(platform))
}

func OpenAICompatiblePlatformFromContext(ctx context.Context) string {
	if ctx == nil {
		return PlatformOpenAI
	}
	platform, _ := ctx.Value(openAICompatiblePlatformCtxKey{}).(string)
	if !IsOpenAICompatiblePlatform(platform) {
		return PlatformOpenAI
	}
	return platform
}

func buildKiloChatCompletionsURL(base string) string {
	return buildKiloOpenRouterEndpointURL(base, "/chat/completions")
}

func buildKiloModelsURL(base string) string {
	return buildKiloOpenRouterEndpointURL(base, "/models")
}

func buildKiloBalanceURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if normalized == "" {
		normalized = KiloDefaultAPIBaseURL
	}
	endpoint := "/profile/balance"
	if strings.HasSuffix(normalized, endpoint) {
		return normalized
	}
	return normalized + endpoint
}

func buildKiloOpenRouterEndpointURL(base string, endpoint string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if normalized == "" {
		normalized = KiloDefaultOpenRouterBaseURL
	}
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(normalized, endpoint) {
		return normalized
	}
	return normalized + endpoint
}

func applyKiloHeaders(reqHeaderSetter interface{ Set(string, string) }, account *Account) {
	if account == nil {
		return
	}
	if orgID := account.GetKiloOrganizationID(); orgID != "" {
		reqHeaderSetter.Set("X-Kilocode-OrganizationID", orgID)
	}
	if userAgent := account.GetKiloUserAgent(); userAgent != "" {
		reqHeaderSetter.Set("User-Agent", userAgent)
	}
}

func NormalizeKiloBillingModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return model
	}
	switch strings.ToLower(strings.TrimSpace(parts[0])) {
	case "anthropic", "openai", "google":
		if trimmed := strings.TrimSpace(parts[1]); trimmed != "" {
			return trimmed
		}
	}
	return model
}
