package handler

import (
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

const publicModelCatalogUpdatedAt = "2026-06-21"

const (
	sourceAnthropic = "https://docs.anthropic.com/en/docs/about-claude/pricing"
	sourceOpenAI    = "https://openai.com/api/pricing/"
	sourceGemini    = "https://ai.google.dev/gemini-api/docs/pricing"
	sourceQwen      = "https://www.alibabacloud.com/help/en/model-studio/model-pricing"
	sourceGLM       = "https://docs.z.ai/guides/overview/pricing"
	sourceDeepSeek  = "https://api-docs.deepseek.com/quick_start/pricing"
	sourceMiniMax   = "https://platform.minimax.io/docs/guides/pricing-paygo"
	sourceKimi      = "https://platform.kimi.ai/docs/pricing/chat"
)

var publicModelCatalogProviderMeta = []dto.PublicModelCatalogProvider{
	{Key: "anthropic", Name: "Anthropic", AccentColor: "#d97745"},
	{Key: "openai", Name: "OpenAI", AccentColor: "#27a644"},
	{Key: "gemini", Name: "Gemini", AccentColor: "#4f8df5"},
	{Key: "qwen", Name: "Qwen", AccentColor: "#7c6df2"},
	{Key: "glm", Name: "GLM", AccentColor: "#25a98e"},
	{Key: "deepseek", Name: "DeepSeek", AccentColor: "#4b6bfb"},
	{Key: "minimax", Name: "MiniMax", AccentColor: "#f59e0b"},
	{Key: "kimi", Name: "Kimi", AccentColor: "#8b5cf6"},
}

func usd(input, output float64) dto.PublicModelCatalogPricing {
	return dto.PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens", InputPerMillion: floatPtr(input), OutputPerMillion: floatPtr(output)}
}

func usdWithCache(input, output, cacheRead float64) dto.PublicModelCatalogPricing {
	pricing := usd(input, output)
	pricing.CacheReadPerMillion = floatPtr(cacheRead)
	return pricing
}

func pendingPricing() dto.PublicModelCatalogPricing {
	return dto.PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens", Note: "Pending confirmation"}
}

func withPriceLines(pricing dto.PublicModelCatalogPricing, lines ...dto.PublicModelCatalogPriceLine) dto.PublicModelCatalogPricing {
	pricing.PriceLines = lines
	return pricing
}

func priceLine(label string, amount float64, unit string) dto.PublicModelCatalogPriceLine {
	return dto.PublicModelCatalogPriceLine{Label: label, Amount: amount, Unit: unit}
}

func floatPtr(value float64) *float64 { return &value }

func catalogModel(provider, providerName, modelName, displayName string, modalities []string, description string, contextWindow int, features []string, pricing dto.PublicModelCatalogPricing, priceStatus string, sourceURL string) dto.PublicModelCatalogModel {
	return dto.PublicModelCatalogModel{Provider: provider, ProviderName: providerName, ModelName: modelName, DisplayName: displayName, Modalities: modalities, Description: description, ContextWindow: contextWindow, Features: features, Pricing: pricing, PriceStatus: priceStatus, SourceURL: sourceURL, UpdatedAt: publicModelCatalogUpdatedAt}
}

func textModalities() []string  { return []string{"text"} }
func imageModalities() []string { return []string{"image"} }

func textFeatures(extra ...string) []string {
	features := []string{"chat", "reasoning"}
	return append(features, extra...)
}

func imageFeatures(extra ...string) []string {
	features := []string{"image generation"}
	return append(features, extra...)
}

var publicModelCatalogModels = []dto.PublicModelCatalogModel{
	catalogModel("anthropic", "Anthropic", "claude-opus-4-8", "Claude Opus 4.8", textModalities(), "Highest-capability Claude model for complex reasoning, coding, and long-context work.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-7", "Claude Opus 4.7", textModalities(), "Claude Opus model for complex reasoning, writing, and engineering workflows.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-7-max", "Claude Opus 4.7 Max", textModalities(), "High-capacity Claude Opus routing option for demanding agent and coding workloads.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-6", "Claude Opus 4.6", textModalities(), "Claude Opus model for advanced reasoning and long-running tasks.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-6-thinking", "Claude Opus 4.6 Thinking", textModalities(), "Claude Opus reasoning route tuned for explicit thinking workloads.", 200000, textFeatures("tool use", "prompt caching", "thinking"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-5", "Claude Opus 4.5", textModalities(), "Claude Opus model for high-accuracy reasoning, coding, and analysis.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-20250514", "Claude Sonnet 4", textModalities(), "Balanced Claude Sonnet model for coding, writing, and production chat workloads.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-5", "Claude Sonnet 4.5", textModalities(), "Balanced Claude Sonnet model with strong coding and agent performance.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 20250929", textModalities(), "Versioned Claude Sonnet 4.5 model for stable production routing.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6", textModalities(), "Balanced Claude Sonnet model for production coding and agent workflows.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("openai", "OpenAI", "gpt-5.5", "GPT-5.5", textModalities(), "OpenAI frontier text model for complex reasoning and agentic work.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 30, 0.5), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4", "GPT-5.4", textModalities(), "OpenAI flagship text model for advanced reasoning, coding, and multimodal-adjacent workflows.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(2.5, 15, 0.25), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4-mini", "GPT-5.4 Mini", textModalities(), "Lower-cost OpenAI model for fast production text and agent tasks.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(0.75, 4.5, 0.075), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.3-codex", "GPT-5.3 Codex", textModalities(), "OpenAI Codex-focused model for repository-scale coding workflows.", 400000, textFeatures("coding", "tool use", "prompt caching"), usdWithCache(1.75, 14, 0.175), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2", "GPT-Image-2", imageModalities(), "OpenAI image generation model for production visual generation workflows.", 0, imageFeatures("multi-resolution"), withPriceLines(usdWithCache(2.5, 5, 1.25), priceLine("1K image", 0.21, "image"), priceLine("2K image", 0.85, "image"), priceLine("4K image", 3.4, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-count", "GPT-Image-2 Count", imageModalities(), "OpenAI image generation count route for standard-resolution output.", 0, imageFeatures("standard image"), withPriceLines(usd(3, 6), priceLine("Standard image", 0.2, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-hd-count", "GPT-Image-2 HD Count", imageModalities(), "OpenAI image generation count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(2, 5), priceLine("HD image", 0.4, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-4k-count", "GPT-Image-2 4K Count", imageModalities(), "OpenAI image generation count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(5, 6), priceLine("4K image", 0.8, "image")), "confirmed", sourceOpenAI),
	catalogModel("gemini", "Gemini", "gemini-3.5-flash", "Gemini 3.5 Flash", textModalities(), "Fast Gemini model for high-throughput text, coding, and agent tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(1.5, 9, 0.15), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", textModalities(), "Gemini Pro preview model for higher-capability reasoning and multimodal-adjacent tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(2, 12, 0.2), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-2.5-flash-image", "Gemini 2.5 Flash Image", imageModalities(), "Gemini image generation model with multi-resolution output pricing.", 0, imageFeatures("multi-resolution"), withPriceLines(usdWithCache(0.3, 2.5, 0.03), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-2.5-flash-image-count", "Gemini 2.5 Flash Image Count", imageModalities(), "Gemini image count route for fixed-price image generation.", 0, imageFeatures("image count"), withPriceLines(usd(0.5, 1.5), priceLine("1K image", 0.1, "image"), priceLine("2K image", 0.1, "image"), priceLine("4K image", 0.1, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image", "Gemini 3.1 Flash Image", imageModalities(), "Gemini Flash image generation model with multi-resolution pricing.", 0, imageFeatures("multi-resolution"), withPriceLines(usd(0.3, 2.5), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-count", "Gemini 3.1 Flash Image Count", imageModalities(), "Gemini Flash image count route for standard output.", 0, imageFeatures("standard image"), withPriceLines(usd(0.5, 1.5), priceLine("Standard image", 0.3, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-hd-count", "Gemini 3.1 Flash Image HD Count", imageModalities(), "Gemini Flash image count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(0.5, 1.5), priceLine("HD image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-4k-count", "Gemini 3.1 Flash Image 4K Count", imageModalities(), "Gemini Flash image count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(0.5, 1.5), priceLine("4K image", 0.55, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-count", "Gemini 3 Pro Image Count", imageModalities(), "Gemini Pro image count route for standard output.", 0, imageFeatures("standard image"), withPriceLines(usd(0.5, 1.5), priceLine("Standard image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-hd-count", "Gemini 3 Pro Image HD Count", imageModalities(), "Gemini Pro image count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(0.5, 1.5), priceLine("HD image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-4k-count", "Gemini 3 Pro Image 4K Count", imageModalities(), "Gemini Pro image count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(0.5, 1.5), priceLine("4K image", 0.5, "image")), "confirmed", sourceGemini),
	catalogModel("qwen", "Qwen", "qwen3.5-plus", "Qwen3.5 Plus", textModalities(), "Qwen Plus model with official tiered pricing by request context size.", 1000000, textFeatures("vision input", "video input", "agentic coding"), withPriceLines(dto.PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens"}, priceLine("0-256K input", 0.4, "1M tokens"), priceLine("0-256K output", 2.4, "1M tokens"), priceLine("256K-1M input", 0.5, "1M tokens"), priceLine("256K-1M output", 3, "1M tokens")), "confirmed", sourceQwen),
	catalogModel("qwen", "Qwen", "qwen3.6-plus", "Qwen3.6 Plus", textModalities(), "Qwen Plus model listed by the reference catalog; official per-model price is not published in the checked pricing table.", 1000000, textFeatures("agentic coding"), pendingPricing(), "unverified", ""),
	catalogModel("glm", "GLM", "glm-4.7", "GLM-4.7", textModalities(), "GLM model for general reasoning, chat, and coding workloads.", 128000, textFeatures("tool use"), usd(0.441, 2.06), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5", "GLM-5", textModalities(), "GLM model for higher-capability reasoning and production chat.", 128000, textFeatures("tool use"), usd(0.882, 3.24), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.1", "GLM-5.1", textModalities(), "GLM model for advanced reasoning and coding workflows.", 128000, textFeatures("tool use"), usd(1.18, 4.12), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.2", "GLM-5.2", textModalities(), "GLM model for frontier reasoning, coding, and agent workflows.", 128000, textFeatures("tool use", "prompt caching"), usdWithCache(1.4, 4.4, 0.28), "confirmed", sourceGLM),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Pro", "DeepSeek V4 Pro", textModalities(), "DeepSeek V4 Pro for high-capability reasoning, coding, and long-context agent work.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.435, 0.87, 0.003625), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Flash", "DeepSeek V4 Flash", textModalities(), "DeepSeek V4 Flash for efficient long-context reasoning and production chat.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "deepseek-v3.2", "DeepSeek V3.2", textModalities(), "DeepSeek V3.2 model retained from the reference catalog for compatibility.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("minimax", "MiniMax", "MiniMax-M3", "MiniMax M3", textModalities(), "MiniMax text model for general chat, coding, and agent workflows.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.62, 2.47, 0.124), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.5", "MiniMax M2.5", textModalities(), "MiniMax text model for efficient production chat and coding tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.031), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.7", "MiniMax M2.7", textModalities(), "MiniMax text model for balanced reasoning and throughput.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.061), "confirmed", sourceMiniMax),
	catalogModel("kimi", "Kimi", "Kimi-k2.5", "Kimi K2.5", textModalities(), "Kimi model for long-context chat, reasoning, and coding workflows.", 200000, textFeatures("long context"), usd(0.062, 1.85), "confirmed", sourceKimi),
	catalogModel("kimi", "Kimi", "Kimi-k2.6", "Kimi K2.6", textModalities(), "Kimi model for upgraded long-context reasoning and agent work.", 200000, textFeatures("long context"), usd(0.097, 2.38), "confirmed", sourceKimi),
}

func GetPublicModelCatalogModels() []dto.PublicModelCatalogModel {
	models := make([]dto.PublicModelCatalogModel, 0, len(publicModelCatalogModels))
	for _, model := range publicModelCatalogModels {
		if shouldExcludePublicCatalogModel(model) {
			continue
		}
		models = append(models, model)
	}
	return models
}

func shouldExcludePublicCatalogModel(model dto.PublicModelCatalogModel) bool {
	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	providerName := strings.ToLower(strings.TrimSpace(model.ProviderName))
	name := strings.ToLower(strings.TrimSpace(model.ModelName))
	display := strings.ToLower(strings.TrimSpace(model.DisplayName))
	return strings.Contains(provider, "agnes") || strings.Contains(provider, "doubao") || strings.Contains(providerName, "agnes") || strings.Contains(providerName, "doubao") || strings.Contains(name, "agnes") || strings.Contains(name, "doubao") || strings.Contains(display, "agnes") || strings.Contains(display, "doubao")
}

func publicModelCatalogProviders(models []dto.PublicModelCatalogModel) []dto.PublicModelCatalogProvider {
	counts := make(map[string]int, len(publicModelCatalogProviderMeta))
	for _, model := range models {
		counts[model.Provider]++
	}
	providers := make([]dto.PublicModelCatalogProvider, 0, len(publicModelCatalogProviderMeta))
	for _, provider := range publicModelCatalogProviderMeta {
		count := counts[provider.Key]
		if count == 0 {
			continue
		}
		provider.ModelCount = count
		providers = append(providers, provider)
	}
	return providers
}

func sortPublicModelCatalog(models []dto.PublicModelCatalogModel) {
	providerRank := make(map[string]int, len(publicModelCatalogProviderMeta))
	for idx, provider := range publicModelCatalogProviderMeta {
		providerRank[provider.Key] = idx
	}
	sort.SliceStable(models, func(i, j int) bool {
		leftRank := providerRank[models[i].Provider]
		rightRank := providerRank[models[j].Provider]
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(models[i].DisplayName) < strings.ToLower(models[j].DisplayName)
	})
}

// GetPublicModelCatalog returns the public model marketplace catalog.
// GET /api/v1/settings/model-catalog
func (h *SettingHandler) GetPublicModelCatalog(c *gin.Context) {
	models := GetPublicModelCatalogModels()
	sortPublicModelCatalog(models)
	response.Success(c, dto.PublicModelCatalogResponse{UpdatedAt: publicModelCatalogUpdatedAt, Providers: publicModelCatalogProviders(models), Models: models})
}
