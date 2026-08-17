//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestTask8CrossClientModelMappingRequiresExplicitTrue(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"missing": "",
		"empty":   " ",
		"invalid": "enabled",
		"false":   "false",
		"zero":    "0",
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, parseGrokCrossClientModelMapEnabled(value))
		})
	}
	require.True(t, parseGrokCrossClientModelMapEnabled(" true "))
	require.True(t, parseGrokCrossClientModelMapEnabled("TRUE"))
}

func TestTask8PublishesRuntimeModelMappingWithOptInDefaults(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })

	for name, raw := range map[string]string{
		"missing": "",
		"empty":   " ",
		"invalid": "enabled",
		"false":   "false",
	} {
		t.Run(name, func(t *testing.T) {
			publishGrokRuntimeModelMapping("", parseGrokCrossClientModelMapEnabled(raw))
			opts := xai.RuntimeModelMappingOptions()
			require.Equal(t, xai.DefaultTextModel, opts.DefaultText)
			require.False(t, opts.EnableCrossClientMap)
			require.NotContains(t, xai.DefaultModelMapping(), "claude-*")
		})
	}

	publishGrokRuntimeModelMapping(" grok-build-0.1 ", true)
	opts := xai.RuntimeModelMappingOptions()
	require.Equal(t, "grok-build-0.1", opts.DefaultText)
	require.True(t, opts.EnableCrossClientMap)
	require.Equal(t, "grok-build-0.1", xai.DefaultModelMapping()["claude-*"])
}

func TestTask8RuntimeModelMappingPublishIsIdempotent(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })

	publishGrokRuntimeModelMapping(" grok-build-0.1 ", true)
	version := xai.RuntimeModelMappingVersion()
	publishGrokRuntimeModelMapping("grok-build-0.1", true)

	require.Equal(t, version, xai.RuntimeModelMappingVersion())
}
