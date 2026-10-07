package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestB8deTypeSafeModelDetectionDoesNotEnableChatCompatibility(t *testing.T) {
	for _, model := range []string{"jev-latest", "jev-1", "typesafe/jev-latest", "jev/jev-latest"} {
		platform, ok := DetectModelPlatform(model)
		require.True(t, ok, model)
		require.Equal(t, PlatformTypeSafe, platform)
		require.True(t, isConcreteRequestPlatform(platform))
		require.False(t, IsOpenAICompatiblePlatform(platform))
	}
}
