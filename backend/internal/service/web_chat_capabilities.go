package service

import (
	"fmt"
	"strings"
)

type WebChatModelCapability struct {
	Provider               string `json:"provider"`
	Platform               string `json:"platform"`
	KeyType                string `json:"key_type"`
	Model                  string `json:"model"`
	DisplayName            string `json:"display_name"`
	SupportsText           bool   `json:"supports_text"`
	SupportsImageInput     bool   `json:"supports_image_input"`
	SupportsFileContext    bool   `json:"supports_file_context"`
	SupportsArtifactOutput bool   `json:"supports_artifact_output"`
	PriceStatus            string `json:"price_status"`
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
	Modalities  []string
	Features    []string
	PriceStatus string
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

func WebChatModelCapabilityFromCatalogModel(model WebChatCatalogModel) (WebChatModelCapability, bool) {
	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	route, ok := webChatProviderRoutes[provider]
	if !ok {
		return WebChatModelCapability{}, false
	}
	hasImageModality := containsFold(model.Modalities, "image")
	supportsImageInput := hasImageModality || containsFold(model.Features, "vision input")

	return WebChatModelCapability{
		Provider:               provider,
		Platform:               route.Platform,
		KeyType:                route.KeyType,
		Model:                  model.ModelName,
		DisplayName:            model.DisplayName,
		SupportsText:           true,
		SupportsImageInput:     supportsImageInput,
		SupportsFileContext:    true,
		SupportsArtifactOutput: hasImageModality || containsFold(model.Features, "image generation"),
		PriceStatus:            model.PriceStatus,
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
