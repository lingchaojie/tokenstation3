//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type task8GrokRuntimeSettingReader struct {
	values map[string]string
	err    error
	keys   []string
}

func (r *task8GrokRuntimeSettingReader) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.keys = append([]string(nil), keys...)
	if r.err != nil {
		return nil, r.err
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func TestLoadGrokRuntimeModelMappingSettingsPublishesStoredOptIn(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})

	reader := &task8GrokRuntimeSettingReader{values: map[string]string{
		"grok_default_text_model":             " grok-build-0.1 ",
		"grok_cross_client_model_map_enabled": "true",
	}}
	beforeVersion := xai.RuntimeModelMappingVersion()

	require.NoError(t, LoadGrokRuntimeModelMappingSettings(context.Background(), reader))
	require.Equal(t, []string{
		"grok_default_text_model",
		"grok_cross_client_model_map_enabled",
	}, reader.keys)
	require.Equal(t, xai.ModelMappingOptions{
		DefaultText:          "grok-build-0.1",
		EnableCrossClientMap: true,
	}, xai.RuntimeModelMappingOptions())
	require.Equal(t, "grok-build-0.1", xai.DefaultModelMapping()["claude-*"])
	require.Equal(t, beforeVersion+1, xai.RuntimeModelMappingVersion())
}

func TestLoadGrokRuntimeModelMappingSettingsFailsClosed(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })

	for name, values := range map[string]map[string]string{
		"missing": nil,
		"empty": {
			"grok_default_text_model":             " ",
			"grok_cross_client_model_map_enabled": " ",
		},
		"invalid": {
			"grok_cross_client_model_map_enabled": "enabled",
		},
		"explicit false": {
			"grok_default_text_model":             "grok-4.3",
			"grok_cross_client_model_map_enabled": "false",
		},
	} {
		t.Run(name, func(t *testing.T) {
			xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
				DefaultText:          "stale-model",
				EnableCrossClientMap: true,
			})

			require.NoError(t, LoadGrokRuntimeModelMappingSettings(context.Background(), &task8GrokRuntimeSettingReader{values: values}))
			opts := xai.RuntimeModelMappingOptions()
			require.False(t, opts.EnableCrossClientMap)
			require.NotContains(t, xai.DefaultModelMapping(), "claude-*")
			if name == "explicit false" {
				require.Equal(t, "grok-4.3", opts.DefaultText)
			} else {
				require.Equal(t, xai.DefaultTextModel, opts.DefaultText)
			}
		})
	}
}

func TestLoadGrokRuntimeModelMappingSettingsReadErrorFailsClosedAndReturnsError(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
		DefaultText:          "stale-model",
		EnableCrossClientMap: true,
	})

	readErr := errors.New("settings unavailable")
	err := LoadGrokRuntimeModelMappingSettings(context.Background(), &task8GrokRuntimeSettingReader{err: readErr})

	require.ErrorIs(t, err, readErr)
	require.Equal(t, xai.ModelMappingOptions{
		DefaultText:          xai.DefaultTextModel,
		EnableCrossClientMap: false,
	}, xai.RuntimeModelMappingOptions())
	require.NotContains(t, xai.DefaultModelMapping(), "claude-*")
}
