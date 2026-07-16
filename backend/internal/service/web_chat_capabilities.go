package service

import (
	"fmt"
	"strings"
)

type WebChatModelCapability struct {
	Provider                     string   `json:"provider"`
	Platform                     string   `json:"platform"`
	KeyType                      string   `json:"key_type"`
	Model                        string   `json:"model"`
	DisplayName                  string   `json:"display_name"`
	ReleasedAt                   string   `json:"released_at,omitempty"`
	SupportsText                 bool     `json:"supports_text"`
	SupportsImageInput           bool     `json:"supports_image_input"`
	SupportsFileContext          bool     `json:"supports_file_context"`
	SupportsArtifactOutput       bool     `json:"supports_artifact_output"`
	SupportsThinking             bool     `json:"supports_thinking"`
	ThinkingEfforts              []string `json:"thinking_efforts,omitempty"`
	SupportsWebSearch            bool     `json:"supports_web_search"`
	SupportsImageGeneration      bool     `json:"supports_image_generation"`
	ImageGenerationSizes         []string `json:"image_generation_sizes,omitempty"`
	ImageGenerationAspectRatios  []string `json:"image_generation_aspect_ratios,omitempty"`
	ImageGenerationQualities     []string `json:"image_generation_qualities,omitempty"`
	ImageGenerationOutputFormats []string `json:"image_generation_output_formats,omitempty"`
	ImageGenerationBackgrounds   []string `json:"image_generation_backgrounds,omitempty"`
	PriceStatus                  string   `json:"price_status"`
}

type WebChatContextSummary struct {
	TextMessageCount     int
	ImageAttachmentCount int
	FileAttachmentCount  int
}

type WebChatCatalogModel struct {
	Provider    string
	ModelName   string
	DisplayName string
	ReleasedAt  string
	Modalities  []string
	Features    []string
	PriceStatus string
}

type WebChatCatalogCapabilityResolver struct {
	byProviderModel map[string]WebChatModelCapability
}

var webChatProviderRoutes = map[string]struct {
	Platform string
	KeyType  string
}{
	"anthropic": {Platform: PlatformAnthropic, KeyType: APIKeyTypeAnthropic},
	"openai":    {Platform: PlatformOpenAI, KeyType: APIKeyTypeOpenAI},
	"qwen":      {Platform: PlatformOpenAI, KeyType: APIKeyTypeOpenAI},
	"gemini":    {Platform: PlatformGemini, KeyType: ""},
}

func DefaultWebChatCatalogModels() []WebChatCatalogModel {
	publicModels := PublicModelCatalogModelsForWebChat()
	models := make([]WebChatCatalogModel, 0, len(publicModels))
	for _, model := range publicModels {
		provider := strings.ToLower(strings.TrimSpace(model.Provider))
		if _, ok := webChatProviderRoutes[provider]; !ok {
			continue
		}
		models = append(models, WebChatCatalogModel{
			Provider:    provider,
			ModelName:   model.ModelName,
			DisplayName: model.DisplayName,
			ReleasedAt:  model.ReleasedAt,
			Modalities:  append([]string(nil), model.Modalities...),
			Features:    append([]string(nil), model.Features...),
			PriceStatus: model.PriceStatus,
		})
	}
	return models
}

func NewWebChatCatalogCapabilityResolver(models []WebChatCatalogModel) *WebChatCatalogCapabilityResolver {
	resolver := &WebChatCatalogCapabilityResolver{byProviderModel: make(map[string]WebChatModelCapability)}
	for _, capability := range WebChatModelCapabilitiesFromCatalog(models) {
		provider := strings.ToLower(strings.TrimSpace(capability.Provider))
		model := strings.TrimSpace(capability.Model)
		if provider == "" || model == "" {
			continue
		}
		resolver.byProviderModel[webChatCapabilityKey(provider, model)] = capability
	}
	return resolver
}

func (r *WebChatCatalogCapabilityResolver) ResolveWebChatCapability(provider, model string) (WebChatModelCapability, error) {
	if r == nil {
		return WebChatModelCapability{}, ErrWebChatInvalidModel
	}
	key := webChatCapabilityKey(strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(model))
	capability, ok := r.byProviderModel[key]
	if !ok {
		return WebChatModelCapability{}, ErrWebChatInvalidModel
	}
	return capability, nil
}

func WebChatModelCapabilityFromCatalogModel(model WebChatCatalogModel) (WebChatModelCapability, bool) {
	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	route, ok := webChatProviderRoutes[provider]
	if !ok {
		return WebChatModelCapability{}, false
	}
	hasImageModality := containsFold(model.Modalities, "image")
	hasTextModality := len(model.Modalities) == 0 || containsFold(model.Modalities, "text")
	supportsImageGeneration := hasImageModality || containsFold(model.Features, "image generation")
	supportsImageInput := hasImageModality || containsFold(model.Features, "vision input")
	if isOpenAIWebChatGPTTextModel(provider, model.ModelName, supportsImageGeneration) {
		supportsImageInput = true
	}
	supportsThinking := hasTextModality && webChatProviderSupportsThinking(provider)
	supportsWebSearch := hasTextModality && !supportsImageGeneration && webChatProviderSupportsWebSearch(provider)

	return WebChatModelCapability{
		Provider:                     provider,
		Platform:                     route.Platform,
		KeyType:                      route.KeyType,
		Model:                        model.ModelName,
		DisplayName:                  model.DisplayName,
		ReleasedAt:                   model.ReleasedAt,
		SupportsText:                 true,
		SupportsImageInput:           supportsImageInput,
		SupportsFileContext:          true,
		SupportsArtifactOutput:       supportsImageGeneration,
		SupportsThinking:             supportsThinking,
		ThinkingEfforts:              webChatThinkingEffortsForProvider(provider, supportsThinking),
		SupportsWebSearch:            supportsWebSearch,
		SupportsImageGeneration:      supportsImageGeneration,
		ImageGenerationSizes:         webChatImageGenerationSizesForProvider(provider, supportsImageGeneration),
		ImageGenerationAspectRatios:  webChatImageGenerationAspectRatiosForProvider(provider, supportsImageGeneration),
		ImageGenerationQualities:     webChatImageGenerationQualitiesForProvider(provider, supportsImageGeneration),
		ImageGenerationOutputFormats: webChatImageGenerationOutputFormatsForProvider(provider, supportsImageGeneration),
		ImageGenerationBackgrounds:   webChatImageGenerationBackgroundsForProvider(provider, supportsImageGeneration),
		PriceStatus:                  model.PriceStatus,
	}, true
}

func WebChatModelCapabilitiesFromCatalog(models []WebChatCatalogModel) []WebChatModelCapability {
	caps := make([]WebChatModelCapability, 0, len(models))
	for _, model := range models {
		capability, ok := WebChatModelCapabilityFromCatalogModel(model)
		if !ok {
			continue
		}
		caps = append(caps, capability)
	}
	return caps
}

func ValidateWebChatContextForModel(caps WebChatModelCapability, summary WebChatContextSummary) error {
	if !caps.SupportsText && summary.TextMessageCount > 0 {
		return fmt.Errorf("%w: text context is not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
	}
	if !caps.SupportsImageInput && summary.ImageAttachmentCount > 0 {
		return fmt.Errorf("%w: image attachments are not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
	}
	if !caps.SupportsFileContext && summary.FileAttachmentCount > 0 {
		return fmt.Errorf("%w: file context is not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
	}
	return nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func isOpenAIWebChatGPTTextModel(provider, model string, supportsImageGeneration bool) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "openai") &&
		resolveWebChatModelFamily(model) == webChatFamilyGPT &&
		!supportsImageGeneration
}

func webChatProviderSupportsThinking(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic", "openai", "qwen", "gemini":
		return true
	default:
		return false
	}
}

func webChatProviderSupportsWebSearch(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "anthropic":
		return true
	default:
		return false
	}
}

func webChatThinkingEffortsForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return []string{"medium", "high", "xhigh"}
	case "gemini":
		return []string{"low", "medium", "high"}
	default:
		return []string{"low", "medium", "high", "xhigh"}
	}
}

func webChatImageGenerationSizesForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini":
		return []string{"1K", "2K", "4K"}
	case "openai":
		return []string{"1024x1024", "1536x1024", "1024x1536"}
	default:
		return nil
	}
}

func webChatImageGenerationAspectRatiosForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini":
		return []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}
	case "openai":
		return []string{"1:1", "3:2", "2:3"}
	default:
		return nil
	}
}

func webChatImageGenerationQualitiesForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return []string{"low", "medium", "high"}
	default:
		return nil
	}
}

func webChatImageGenerationOutputFormatsForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return []string{"png", "jpeg", "webp"}
	default:
		return nil
	}
}

func webChatImageGenerationBackgroundsForProvider(provider string, enabled bool) []string {
	if !enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return []string{"opaque", "auto"}
	default:
		return nil
	}
}

func webChatCapabilityKey(provider, model string) string {
	return provider + "\x00" + model
}
