package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	grokDefaultTextModelSettingKey    = "grok_default_text_model"
	grokCrossClientModelMapSettingKey = "grok_cross_client_model_map_enabled"
)

type grokRuntimeModelMappingSettingReader interface {
	GetMultiple(ctx context.Context, keys []string) (map[string]string, error)
}

// parseGrokCrossClientModelMapEnabled keeps cross-client model aliases opt-in.
// Missing, empty, malformed, and explicit false values all remain disabled.
func parseGrokCrossClientModelMapEnabled(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

// publishGrokRuntimeModelMapping updates the process-wide fallback used only
// by Grok accounts whose credentials do not define model_mapping. Foreign
// client aliases remain opt-in and the built-in Grok text model is the safe
// fallback for missing/empty settings.
func publishGrokRuntimeModelMapping(defaultText string, crossClientEnabled bool) {
	defaultText = strings.TrimSpace(defaultText)
	if defaultText == "" {
		defaultText = xai.DefaultTextModel
	}
	opts := xai.ModelMappingOptions{
		DefaultText:          defaultText,
		EnableCrossClientMap: crossClientEnabled,
	}
	if xai.RuntimeModelMappingOptions() == opts {
		return
	}
	xai.SetRuntimeModelMappingOptions(opts)
}

// LoadGrokRuntimeModelMappingSettings restores persisted Grok mapping options
// into the process-wide xAI runtime during startup. A settings read failure
// always revokes any stale cross-client mapping before returning the error.
func LoadGrokRuntimeModelMappingSettings(ctx context.Context, reader grokRuntimeModelMappingSettingReader) error {
	settings, err := reader.GetMultiple(ctx, []string{
		grokDefaultTextModelSettingKey,
		grokCrossClientModelMapSettingKey,
	})
	if err != nil {
		publishGrokRuntimeModelMapping("", false)
		return fmt.Errorf("get grok runtime model mapping settings: %w", err)
	}

	publishGrokRuntimeModelMapping(
		settings[grokDefaultTextModelSettingKey],
		parseGrokCrossClientModelMapEnabled(settings[grokCrossClientModelMapSettingKey]),
	)
	return nil
}
