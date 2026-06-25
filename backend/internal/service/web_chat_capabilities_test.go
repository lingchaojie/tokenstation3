package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebChatCapabilities_BlocksImageWhenTargetDoesNotSupportImage(t *testing.T) {
	caps := WebChatModelCapability{
		Provider:            "anthropic",
		Model:               "claude-text-only",
		SupportsText:        true,
		SupportsImageInput:  false,
		SupportsFileContext: false,
	}
	err := ValidateWebChatContextForModel(caps, WebChatContextSummary{ImageAttachmentCount: 1})
	require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
	require.Contains(t, err.Error(), "image")
}

func TestWebChatCapabilities_AllowsTextAcrossProviders(t *testing.T) {
	caps := WebChatModelCapability{Provider: "openai", Model: "gpt-5", SupportsText: true}
	err := ValidateWebChatContextForModel(caps, WebChatContextSummary{TextMessageCount: 12})
	require.NoError(t, err)
}

func TestWebChatCapabilities_DerivesCapabilitiesFromCatalogModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "qwen",
		ModelName:   "qwen3.5-plus",
		DisplayName: "Qwen3.5 Plus",
		Modalities:  []string{"text"},
		Features:    []string{"Vision Input", "agentic coding"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.Equal(t, "qwen", caps.Provider)
	require.Equal(t, PlatformOpenAI, caps.Platform)
	require.Equal(t, APIKeyTypeOpenAI, caps.KeyType)
	require.Equal(t, "qwen3.5-plus", caps.Model)
	require.Equal(t, "Qwen3.5 Plus", caps.DisplayName)
	require.True(t, caps.SupportsText)
	require.True(t, caps.SupportsImageInput)
	require.True(t, caps.SupportsFileContext)
	require.False(t, caps.SupportsArtifactOutput)
	require.True(t, caps.SupportsThinking)
	require.False(t, caps.SupportsWebSearch)
	require.Equal(t, []string{"low", "medium", "high", "xhigh"}, caps.ThinkingEfforts)
	require.Equal(t, "confirmed", caps.PriceStatus)
}

func TestWebChatCapabilities_DerivesOpenAIWebSearchForTextModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "openai",
		ModelName:   "gpt-5.5",
		DisplayName: "GPT-5.5",
		Modalities:  []string{"text"},
		Features:    []string{"reasoning"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, caps.Platform)
	require.True(t, caps.SupportsText)
	require.True(t, caps.SupportsThinking)
	require.True(t, caps.SupportsWebSearch)
	require.False(t, caps.SupportsImageGeneration)
}

func TestWebChatCapabilities_DerivesAnthropicWebSearchForTextModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "anthropic",
		ModelName:   "claude-sonnet-4",
		DisplayName: "Claude Sonnet 4",
		Modalities:  []string{"text"},
		Features:    []string{"reasoning"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.Equal(t, PlatformAnthropic, caps.Platform)
	require.True(t, caps.SupportsText)
	require.True(t, caps.SupportsThinking)
	require.True(t, caps.SupportsWebSearch)
	require.False(t, caps.SupportsImageGeneration)
}

func TestWebChatCapabilities_DerivesOpenAIImageGenerationOptionsFromCatalogModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "openai",
		ModelName:   "gpt-image-2",
		DisplayName: "GPT Image 2",
		Modalities:  []string{"image"},
		Features:    []string{"Image Generation", "multi-resolution"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, caps.Platform)
	require.True(t, caps.SupportsImageGeneration)
	require.True(t, caps.SupportsArtifactOutput)
	require.False(t, caps.SupportsWebSearch)
	require.Equal(t, []string{"1024x1024", "1536x1024", "1024x1536"}, caps.ImageGenerationSizes)
	require.Equal(t, []string{"1:1", "3:2", "2:3"}, caps.ImageGenerationAspectRatios)
	require.Equal(t, []string{"medium", "high"}, caps.ImageGenerationQualities)
	require.Equal(t, []string{"png", "jpeg", "webp"}, caps.ImageGenerationOutputFormats)
	require.Equal(t, []string{"opaque", "transparent"}, caps.ImageGenerationBackgrounds)
}

func TestWebChatCapabilities_DerivesGeminiImageGenerationOptionsFromCatalogModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "gemini",
		ModelName:   "gemini-3.1-flash-image",
		DisplayName: "Gemini 3.1 Flash Image",
		Modalities:  []string{"image"},
		Features:    []string{"Image Generation", "multi-resolution"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.Equal(t, PlatformGemini, caps.Platform)
	require.True(t, caps.SupportsImageGeneration)
	require.True(t, caps.SupportsArtifactOutput)
	require.Equal(t, []string{"1K", "2K", "4K"}, caps.ImageGenerationSizes)
	require.Equal(t, []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}, caps.ImageGenerationAspectRatios)
	require.Empty(t, caps.ImageGenerationQualities)
	require.Empty(t, caps.ImageGenerationOutputFormats)
	require.Empty(t, caps.ImageGenerationBackgrounds)
}

func TestWebChatCapabilities_SkipsUnsupportedCatalogProviders(t *testing.T) {
	caps := WebChatModelCapabilitiesFromCatalog([]WebChatCatalogModel{
		{
			Provider:    "glm",
			ModelName:   "glm-5.2",
			DisplayName: "GLM-5.2",
			Modalities:  []string{"text"},
		},
		{
			Provider:    " OpenAI ",
			ModelName:   "gpt-5",
			DisplayName: "GPT-5",
			Modalities:  []string{"text"},
		},
	})

	require.Len(t, caps, 1)
	require.Equal(t, "openai", caps[0].Provider)
	require.Equal(t, PlatformOpenAI, caps[0].Platform)
	require.Equal(t, APIKeyTypeOpenAI, caps[0].KeyType)
}

func TestWebChatModelDefaultCapabilityResolverResolvesCatalogBackedModel(t *testing.T) {
	svc := NewWebChatService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	caps, err := svc.resolveWebChatSendCapability("anthropic", "claude-sonnet-4-20250514")

	require.NoError(t, err)
	require.Equal(t, "anthropic", caps.Provider)
	require.Equal(t, PlatformAnthropic, caps.Platform)
	require.Equal(t, APIKeyTypeAnthropic, caps.KeyType)
	require.Equal(t, "claude-sonnet-4-20250514", caps.Model)
	require.True(t, caps.SupportsText)

	caps, err = svc.resolveWebChatSendCapability("openai", "gpt-5.5")
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, caps.Platform)
	require.Equal(t, "gpt-5.5", caps.Model)
	require.True(t, caps.SupportsWebSearch)
}

func TestWebChatModelDefaultCapabilityResolverRejectsUnsupportedCatalogEntries(t *testing.T) {
	resolver := NewWebChatCatalogCapabilityResolver([]WebChatCatalogModel{
		{Provider: "glm", ModelName: "glm-5.2", Modalities: []string{"text"}},
		{Provider: "anthropic", ModelName: "claude-sonnet-4-20250514", Modalities: []string{"text"}},
	})

	_, err := resolver.ResolveWebChatCapability("glm", "glm-5.2")
	require.ErrorIs(t, err, ErrWebChatInvalidModel)

	_, err = resolver.ResolveWebChatCapability("anthropic", "missing-model")
	require.ErrorIs(t, err, ErrWebChatInvalidModel)

	svc := NewWebChatService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err = svc.resolveWebChatSendCapability("anthropic", "claude-sonnet-4")
	require.ErrorIs(t, err, ErrWebChatInvalidModel)
}

func TestWebChatCapabilities_DefaultCatalogDerivedFromPublicRoutableModels(t *testing.T) {
	publicModels := PublicModelCatalogModelsForWebChat()
	want := make(map[string]PublicModelCatalogModel)
	for _, model := range publicModels {
		provider := strings.ToLower(strings.TrimSpace(model.Provider))
		if _, ok := webChatProviderRoutes[provider]; !ok {
			continue
		}
		want[webChatCapabilityKey(provider, model.ModelName)] = model
	}

	got := DefaultWebChatCatalogModels()

	require.NotEmpty(t, want)
	require.Len(t, got, len(want))
	for _, model := range got {
		provider := strings.ToLower(strings.TrimSpace(model.Provider))
		publicModel, ok := want[webChatCapabilityKey(provider, model.ModelName)]
		require.Truef(t, ok, "web chat catalog model %s/%s is not in public catalog", model.Provider, model.ModelName)
		require.Equal(t, publicModel.DisplayName, model.DisplayName)
		require.Equal(t, publicModel.Modalities, model.Modalities)
		require.Equal(t, publicModel.Features, model.Features)
		require.Equal(t, publicModel.PriceStatus, model.PriceStatus)
	}
}
