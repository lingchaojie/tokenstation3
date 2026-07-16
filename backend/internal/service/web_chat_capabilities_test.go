package service

import (
	"context"
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
	require.True(t, caps.SupportsText)
	require.True(t, caps.SupportsImageInput)
	require.True(t, caps.SupportsFileContext)
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
	require.False(t, caps.SupportsThinking)
	require.Empty(t, caps.ThinkingEfforts)
	require.False(t, caps.SupportsWebSearch)
	require.Equal(t, []string{"1024x1024", "1536x1024", "1024x1536"}, caps.ImageGenerationSizes)
	require.Equal(t, []string{"1:1", "3:2", "2:3"}, caps.ImageGenerationAspectRatios)
	require.Equal(t, []string{"low", "medium", "high"}, caps.ImageGenerationQualities)
	require.Equal(t, []string{"png", "jpeg", "webp"}, caps.ImageGenerationOutputFormats)
	require.Equal(t, []string{"opaque", "auto"}, caps.ImageGenerationBackgrounds)
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
	svc := NewWebChatService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.defaultGroups = stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 1, APIKeyTypeOpenAI: 2}}
	svc.accountLister = stubAccountLister{byGroup: map[int64][]Account{
		1: {acctWithMapping(PlatformAnthropic, "claude-sonnet-4-20250514")},
		2: {acctWithMapping(PlatformOpenAI, "gpt-5.5")},
	}}

	caps, err := svc.resolveWebChatSendCapability(context.Background(), "anthropic", "claude-sonnet-4-20250514")

	require.NoError(t, err)
	require.Equal(t, "anthropic", caps.Provider)
	require.Equal(t, PlatformAnthropic, caps.Platform)
	require.Equal(t, APIKeyTypeAnthropic, caps.KeyType)
	require.Equal(t, "claude-sonnet-4-20250514", caps.Model)
	require.True(t, caps.SupportsText)

	caps, err = svc.resolveWebChatSendCapability(context.Background(), "openai", "gpt-5.5")
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, caps.Platform)
	require.Equal(t, "gpt-5.5", caps.Model)
	require.True(t, caps.SupportsWebSearch)
}

func TestResolveWebChatCatalog_OpenAIImageModelDoesNotInheritGPTThinking(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeOpenAI: 2}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		2: {acctWithMapping(PlatformOpenAI, "gpt-image-2")},
	}}

	got, err := resolveWebChatCatalog(context.Background(), gr, al)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "openai", got[0].Provider)
	require.Equal(t, "gpt-image-2", got[0].Model)
	require.True(t, got[0].SupportsImageGeneration)
	require.False(t, got[0].SupportsThinking)
	require.Empty(t, got[0].ThinkingEfforts)
	require.False(t, got[0].SupportsWebSearch)
	require.Equal(t, []string{"low", "medium", "high"}, got[0].ImageGenerationQualities)
	require.Equal(t, []string{"opaque", "auto"}, got[0].ImageGenerationBackgrounds)
}

func TestResolveWebChatCatalog_OpenAIGPTFallbackGetsNativeInputsAndSearch(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeOpenAI: 2}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		2: {acctWithMapping(PlatformOpenAI, "gpt-5.6-custom")},
	}}

	got, err := resolveWebChatCatalog(context.Background(), gr, al)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].SupportsImageInput)
	require.True(t, got[0].SupportsFileContext)
	require.True(t, got[0].SupportsWebSearch)
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

	svc := NewWebChatService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.defaultGroups = stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 1}}
	svc.accountLister = stubAccountLister{byGroup: map[int64][]Account{
		1: {acctWithMapping(PlatformAnthropic, "claude-sonnet-4")},
	}}
	// A model not present in any default group is rejected.
	_, err = svc.resolveWebChatSendCapability(context.Background(), "anthropic", "claude-opus-4-8")
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
