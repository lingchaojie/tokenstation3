package service

import (
	"sort"
	"strings"
)

const PublicModelCatalogUpdatedAt = "2026-07-15"

type PublicModelCatalogResponse struct {
	UpdatedAt string                       `json:"updated_at"`
	Providers []PublicModelCatalogProvider `json:"providers"`
	Models    []PublicModelCatalogModel    `json:"models"`
}

type PublicModelCatalogProvider struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	AccentColor string `json:"accent_color"`
	ModelCount  int    `json:"model_count"`
}

type PublicModelCatalogModel struct {
	Provider         string                    `json:"provider"`
	ProviderName     string                    `json:"provider_name"`
	ModelName        string                    `json:"model_name"`
	DisplayName      string                    `json:"display_name"`
	Modalities       []string                  `json:"modalities"`
	Description      string                    `json:"description"`
	ContextWindow    int                       `json:"context_window,omitempty"`
	ContextSourceURL string                    `json:"context_source_url,omitempty"`
	Features         []string                  `json:"features"`
	Pricing          PublicModelCatalogPricing `json:"pricing"`
	PriceStatus      string                    `json:"price_status"`
	ReleasedAt       string                    `json:"released_at"`
	ReleaseStatus    string                    `json:"release_status"`
	SourceURL        string                    `json:"source_url,omitempty"`
	UpdatedAt        string                    `json:"updated_at"`
}

type PublicModelCatalogPricing struct {
	Currency            string                        `json:"currency"`
	Unit                string                        `json:"unit"`
	InputPerMillion     *float64                      `json:"input_per_million,omitempty"`
	OutputPerMillion    *float64                      `json:"output_per_million,omitempty"`
	CacheReadPerMillion *float64                      `json:"cache_read_per_million,omitempty"`
	PriceLines          []PublicModelCatalogPriceLine `json:"price_lines,omitempty"`
	Note                string                        `json:"note,omitempty"`
}

type PublicModelCatalogPriceLine struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

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

const (
	contextSourceAnthropic = "https://docs.anthropic.com/en/docs/build-with-claude/context-windows"
	contextSourceOpenAI    = "https://developers.openai.com/api/docs/models"
	contextSourceGemini    = "https://ai.google.dev/gemini-api/docs/models"
	contextSourceQwen      = "https://www.alibabacloud.com/help/en/model-studio/models"
	contextSourceGLM47     = "https://docs.z.ai/guides/llm/glm-4.7"
	contextSourceGLM5      = "https://docs.z.ai/guides/llm/glm-5"
	contextSourceGLM51     = "https://docs.z.ai/guides/llm/glm-5.1"
	contextSourceGLM52     = "https://docs.z.ai/guides/llm/glm-5.2"
	contextSourceDeepSeek  = "https://api-docs.deepseek.com/quick_start/pricing"
	contextSourceMiniMax   = "https://platform.minimax.io/docs/guides/text-generation"
	contextSourceKimi      = "https://platform.kimi.ai/docs/pricing/chat"
)

var publicModelCatalogProviderMeta = []PublicModelCatalogProvider{
	{Key: "anthropic", Name: "Anthropic", AccentColor: "#d97745"},
	{Key: "openai", Name: "OpenAI", AccentColor: "#27a644"},
	{Key: "gemini", Name: "Gemini", AccentColor: "#4f8df5"},
	{Key: "qwen", Name: "Alibaba Cloud", AccentColor: "#7c6df2"},
	{Key: "glm", Name: "GLM", AccentColor: "#25a98e"},
	{Key: "deepseek", Name: "DeepSeek", AccentColor: "#4b6bfb"},
	{Key: "minimax", Name: "MiniMax", AccentColor: "#f59e0b"},
	{Key: "kimi", Name: "Kimi", AccentColor: "#8b5cf6"},
}

func usd(input, output float64) PublicModelCatalogPricing {
	return PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens", InputPerMillion: floatPtr(input), OutputPerMillion: floatPtr(output)}
}

func usdWithCache(input, output, cacheRead float64) PublicModelCatalogPricing {
	pricing := usd(input, output)
	pricing.CacheReadPerMillion = floatPtr(cacheRead)
	return pricing
}

func pendingPricing() PublicModelCatalogPricing {
	return PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens", Note: "Pending confirmation"}
}

func withPriceLines(pricing PublicModelCatalogPricing, lines ...PublicModelCatalogPriceLine) PublicModelCatalogPricing {
	pricing.PriceLines = lines
	return pricing
}

func priceLine(label string, amount float64, unit string) PublicModelCatalogPriceLine {
	return PublicModelCatalogPriceLine{Label: label, Amount: amount, Unit: unit}
}

func floatPtr(value float64) *float64 { return &value }

type modelReleaseInfo struct {
	ReleasedAt    string
	ReleaseStatus string
}

var publicModelReleaseInfoByModel = map[string]modelReleaseInfo{
	"claude-opus-4-8":          {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"claude-opus-4-7":          {ReleasedAt: "2026-05-01", ReleaseStatus: "unverified"},
	"claude-opus-4-6":          {ReleasedAt: "2026-04-01", ReleaseStatus: "unverified"},
	"claude-sonnet-5":          {ReleasedAt: "2026-06-30", ReleaseStatus: "confirmed"},
	"claude-sonnet-4-6":        {ReleasedAt: "2026-04-01", ReleaseStatus: "unverified"},
	"claude-opus-4-5":          {ReleasedAt: "2026-03-01", ReleaseStatus: "unverified"},
	"claude-sonnet-4-5":        {ReleasedAt: "2025-09-29", ReleaseStatus: "confirmed"},
	"claude-sonnet-4-20250514": {ReleasedAt: "2025-05-14", ReleaseStatus: "confirmed"},
	"gpt-5.6-sol":              {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
	"gpt-5.6-terra":            {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
	"gpt-5.6-luna":             {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
	"gpt-5.5":                  {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"gpt-image-2":              {ReleasedAt: "2026-06-15", ReleaseStatus: "unverified"},
	"gpt-5.4":                  {ReleasedAt: "2026-05-01", ReleaseStatus: "unverified"},
	"gpt-5.4-mini":             {ReleasedAt: "2026-05-01", ReleaseStatus: "unverified"},
	"gpt-5.3-codex":            {ReleasedAt: "2026-03-01", ReleaseStatus: "unverified"},
	"gemini-3.5-flash":         {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"gemini-3.1-pro-preview":   {ReleasedAt: "2026-03-01", ReleaseStatus: "unverified"},
	"gemini-3.1-flash-image":   {ReleasedAt: "2026-03-01", ReleaseStatus: "unverified"},
	"gemini-3-pro-image":       {ReleasedAt: "2025-12-01", ReleaseStatus: "unverified"},
	"gemini-2.5-flash-image":   {ReleasedAt: "2025-06-01", ReleaseStatus: "unverified"},
	"qwen3.6-plus":             {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"qwen3.5-plus":             {ReleasedAt: "2026-01-01", ReleaseStatus: "unverified"},
	"glm-5.2":                  {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"glm-5.1":                  {ReleasedAt: "2026-05-01", ReleaseStatus: "unverified"},
	"glm-5":                    {ReleasedAt: "2026-04-01", ReleaseStatus: "unverified"},
	"glm-4.7":                  {ReleasedAt: "2026-02-01", ReleaseStatus: "unverified"},
	"DeepSeek-V4-Pro":          {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"DeepSeek-V4-Flash":        {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"deepseek-v3.2":            {ReleasedAt: "2025-12-01", ReleaseStatus: "unverified"},
	"MiniMax-M3":               {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"MiniMax-M2.7":             {ReleasedAt: "2026-03-01", ReleaseStatus: "unverified"},
	"MiniMax-M2.5":             {ReleasedAt: "2026-01-01", ReleaseStatus: "unverified"},
	"Kimi-k2.6":                {ReleasedAt: "2026-06-21", ReleaseStatus: "unverified"},
	"Kimi-k2.5":                {ReleasedAt: "2026-04-01", ReleaseStatus: "unverified"},
}

func releaseInfoForModel(modelName string) modelReleaseInfo {
	if info, ok := publicModelReleaseInfoByModel[modelName]; ok {
		return info
	}
	return modelReleaseInfo{ReleasedAt: PublicModelCatalogUpdatedAt, ReleaseStatus: "unverified"}
}

func catalogModel(provider, providerName, modelName, displayName string, modalities []string, description string, contextWindow int, contextSourceURL string, features []string, pricing PublicModelCatalogPricing, priceStatus string, sourceURL string) PublicModelCatalogModel {
	release := releaseInfoForModel(modelName)
	return PublicModelCatalogModel{Provider: provider, ProviderName: providerName, ModelName: modelName, DisplayName: displayName, Modalities: modalities, Description: description, ContextWindow: contextWindow, ContextSourceURL: contextSourceURL, Features: features, Pricing: pricing, PriceStatus: priceStatus, ReleasedAt: release.ReleasedAt, ReleaseStatus: release.ReleaseStatus, SourceURL: sourceURL, UpdatedAt: PublicModelCatalogUpdatedAt}
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

var publicModelCatalogModels = []PublicModelCatalogModel{
	catalogModel("anthropic", "Anthropic", "claude-opus-4-8", "Claude Opus 4.8", textModalities(), "Highest-capability Claude model for complex reasoning, coding, and long-context work.", 1000000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-7", "Claude Opus 4.7", textModalities(), "Claude Opus model for complex reasoning, writing, and engineering workflows.", 1000000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-6", "Claude Opus 4.6", textModalities(), "Claude Opus model for advanced reasoning and long-running tasks.", 1000000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-5", "Claude Opus 4.5", textModalities(), "Claude Opus model for high-accuracy reasoning, coding, and analysis.", 200000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-20250514", "Claude Sonnet 4", textModalities(), "Balanced Claude Sonnet model for coding, writing, and production chat workloads.", 200000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-5", "Claude Sonnet 4.5", textModalities(), "Balanced Claude Sonnet model with strong coding and agent performance.", 200000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-5", "Claude Sonnet 5", textModalities(), "Best combination of speed and intelligence in the Claude model family, with a 1M-token context window.", 1000000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(2, 10, 0.2), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6", textModalities(), "Balanced Claude Sonnet model for production coding and agent workflows.", 1000000, contextSourceAnthropic, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("openai", "OpenAI", "gpt-5.6-sol", "GPT-5.6 Sol", textModalities(), "Highest-capability GPT-5.6 tier for complex reasoning, coding, and long-context agent workflows.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(5, 30, 0.5), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.6-terra", "GPT-5.6 Terra", textModalities(), "Balanced GPT-5.6 tier for production reasoning, coding, and agent workloads.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(2.5, 15, 0.25), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.6-luna", "GPT-5.6 Luna", textModalities(), "Lower-cost GPT-5.6 tier for efficient reasoning, coding, and high-throughput agent workloads.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(1, 6, 0.1), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.5", "GPT-5.5", textModalities(), "OpenAI frontier text model for complex reasoning and agentic work.", 1050000, contextSourceOpenAI, textFeatures("tool use", "prompt caching"), usdWithCache(5, 30, 0.5), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4", "GPT-5.4", textModalities(), "OpenAI flagship text model for advanced reasoning, coding, and multimodal-adjacent workflows.", 1050000, contextSourceOpenAI, textFeatures("tool use", "prompt caching"), usdWithCache(2.5, 15, 0.25), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4-mini", "GPT-5.4 Mini", textModalities(), "Lower-cost OpenAI model for fast production text and agent tasks.", 400000, contextSourceOpenAI, textFeatures("tool use", "prompt caching"), usdWithCache(0.75, 4.5, 0.075), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.3-codex", "GPT-5.3 Codex", textModalities(), "OpenAI Codex-focused model for repository-scale coding workflows.", 400000, contextSourceOpenAI, textFeatures("coding", "tool use", "prompt caching"), usdWithCache(1.75, 14, 0.175), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2", "GPT-Image-2", imageModalities(), "OpenAI image generation model for production visual generation workflows.", 0, "", imageFeatures("multi-resolution"), withPriceLines(usdWithCache(2.5, 5, 1.25), priceLine("1K image", 0.21, "image"), priceLine("2K image", 0.85, "image"), priceLine("4K image", 3.4, "image")), "confirmed", sourceOpenAI),
	catalogModel("gemini", "Gemini", "gemini-3.5-flash", "Gemini 3.5 Flash", textModalities(), "Fast Gemini model for high-throughput text, coding, and agent tasks.", 1048576, contextSourceGemini, textFeatures("tool use", "prompt caching"), usdWithCache(1.5, 9, 0.15), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", textModalities(), "Gemini Pro preview model for higher-capability reasoning and multimodal-adjacent tasks.", 1048576, contextSourceGemini, textFeatures("tool use", "prompt caching"), usdWithCache(2, 12, 0.2), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-2.5-flash-image", "Gemini 2.5 Flash Image", imageModalities(), "Gemini image generation model with multi-resolution output pricing.", 65536, contextSourceGemini, imageFeatures("multi-resolution"), withPriceLines(usdWithCache(0.3, 2.5, 0.03), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image", "Gemini 3.1 Flash Image", imageModalities(), "Gemini Flash image generation model with multi-resolution pricing.", 131072, contextSourceGemini, imageFeatures("multi-resolution"), withPriceLines(usd(0.3, 2.5), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image", "Gemini 3 Pro Image", imageModalities(), "Gemini Pro image generation model with multi-resolution count pricing.", 65536, contextSourceGemini, imageFeatures("multi-resolution"), withPriceLines(usd(0.5, 1.5), priceLine("Standard image", 0.4, "image"), priceLine("HD image", 0.4, "image"), priceLine("4K image", 0.5, "image")), "confirmed", sourceGemini),
	catalogModel("qwen", "Alibaba Cloud", "qwen3.5-plus", "Qwen3.5 Plus", textModalities(), "Qwen Plus model with official tiered pricing by request context size.", 1000000, contextSourceQwen, textFeatures("vision input", "video input", "agentic coding"), withPriceLines(PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens"}, priceLine("0-256K input", 0.4, "1M tokens"), priceLine("0-256K output", 2.4, "1M tokens"), priceLine("256K-1M input", 0.5, "1M tokens"), priceLine("256K-1M output", 3, "1M tokens")), "confirmed", sourceQwen),
	catalogModel("qwen", "Alibaba Cloud", "qwen3.6-plus", "Qwen3.6 Plus", textModalities(), "Qwen Plus model listed by the reference catalog; official per-model price is not published in the checked pricing table.", 1000000, contextSourceQwen, textFeatures("agentic coding"), pendingPricing(), "unverified", ""),
	catalogModel("glm", "GLM", "glm-4.7", "GLM-4.7", textModalities(), "GLM model for general reasoning, chat, and coding workloads.", 200000, contextSourceGLM47, textFeatures("tool use"), usd(0.441, 2.06), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5", "GLM-5", textModalities(), "GLM model for higher-capability reasoning and production chat.", 200000, contextSourceGLM5, textFeatures("tool use"), usd(0.882, 3.24), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.1", "GLM-5.1", textModalities(), "GLM model for advanced reasoning and coding workflows.", 200000, contextSourceGLM51, textFeatures("tool use"), usd(1.18, 4.12), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.2", "GLM-5.2", textModalities(), "GLM model for frontier reasoning, coding, and agent workflows.", 1000000, contextSourceGLM52, textFeatures("tool use", "prompt caching"), usdWithCache(1.4, 4.4, 0.28), "confirmed", sourceGLM),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Pro", "DeepSeek V4 Pro", textModalities(), "DeepSeek V4 Pro for high-capability reasoning, coding, and long-context agent work.", 1000000, contextSourceDeepSeek, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.435, 0.87, 0.003625), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Flash", "DeepSeek V4 Flash", textModalities(), "DeepSeek V4 Flash for efficient long-context reasoning and production chat.", 1000000, contextSourceDeepSeek, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "deepseek-v3.2", "DeepSeek V3.2", textModalities(), "DeepSeek V3.2 model retained from the reference catalog for compatibility.", 0, "", textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("minimax", "MiniMax", "MiniMax-M3", "MiniMax M3", textModalities(), "MiniMax text model for general chat, coding, and agent workflows.", 1000000, contextSourceMiniMax, textFeatures("tool use", "prompt caching"), usdWithCache(0.62, 2.47, 0.124), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.5", "MiniMax M2.5", textModalities(), "MiniMax text model for efficient production chat and coding tasks.", 204800, contextSourceMiniMax, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.031), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.7", "MiniMax M2.7", textModalities(), "MiniMax text model for balanced reasoning and throughput.", 204800, contextSourceMiniMax, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.061), "confirmed", sourceMiniMax),
	catalogModel("kimi", "Kimi", "Kimi-k2.5", "Kimi K2.5", textModalities(), "Kimi model for long-context chat, reasoning, and coding workflows.", 262144, contextSourceKimi, textFeatures("long context"), usd(0.062, 1.85), "confirmed", sourceKimi),
	catalogModel("kimi", "Kimi", "Kimi-k2.6", "Kimi K2.6", textModalities(), "Kimi model for upgraded long-context reasoning and agent work.", 262144, contextSourceKimi, textFeatures("long context"), usd(0.097, 2.38), "confirmed", sourceKimi),
}

func publicModelCatalogModelsSnapshot() []PublicModelCatalogModel {
	models := make([]PublicModelCatalogModel, 0, len(publicModelCatalogModels))
	for _, model := range publicModelCatalogModels {
		if shouldExcludePublicCatalogModel(model) {
			continue
		}
		model.Modalities = append([]string(nil), model.Modalities...)
		model.Features = append([]string(nil), model.Features...)
		model.Pricing.PriceLines = append([]PublicModelCatalogPriceLine(nil), model.Pricing.PriceLines...)
		models = append(models, model)
	}
	return models
}

func PublicModelCatalogModelsForWebChat() []PublicModelCatalogModel {
	models := publicModelCatalogModelsSnapshot()
	sortPublicModelCatalog(models)
	return models
}

func shouldExcludePublicCatalogModel(model PublicModelCatalogModel) bool {
	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	providerName := strings.ToLower(strings.TrimSpace(model.ProviderName))
	name := strings.ToLower(strings.TrimSpace(model.ModelName))
	display := strings.ToLower(strings.TrimSpace(model.DisplayName))
	return strings.Contains(provider, "agnes") || strings.Contains(provider, "doubao") || strings.Contains(providerName, "agnes") || strings.Contains(providerName, "doubao") || strings.Contains(name, "agnes") || strings.Contains(name, "doubao") || strings.Contains(display, "agnes") || strings.Contains(display, "doubao")
}

func PublicModelCatalogProviders(models []PublicModelCatalogModel) []PublicModelCatalogProvider {
	counts := make(map[string]int, len(publicModelCatalogProviderMeta))
	for _, model := range models {
		counts[model.Provider]++
	}
	providers := make([]PublicModelCatalogProvider, 0, len(publicModelCatalogProviderMeta))
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

func sortPublicModelCatalog(models []PublicModelCatalogModel) {
	providerRank := make(map[string]int, len(publicModelCatalogProviderMeta))
	for idx, provider := range publicModelCatalogProviderMeta {
		providerRank[provider.Key] = idx
	}
	sort.SliceStable(models, func(i, j int) bool {
		leftRank := publicModelCatalogProviderRank(providerRank, models[i].Provider)
		rightRank := publicModelCatalogProviderRank(providerRank, models[j].Provider)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if models[i].ReleasedAt != models[j].ReleasedAt {
			return models[i].ReleasedAt > models[j].ReleasedAt
		}
		return strings.ToLower(models[i].DisplayName) < strings.ToLower(models[j].DisplayName)
	})
}

func publicModelCatalogProviderRank(providerRank map[string]int, provider string) int {
	rank, ok := providerRank[provider]
	if !ok {
		return len(publicModelCatalogProviderMeta)
	}
	return rank
}
