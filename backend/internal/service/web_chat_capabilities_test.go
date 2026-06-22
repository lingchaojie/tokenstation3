package service

import (
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
	require.Equal(t, "confirmed", caps.PriceStatus)
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
