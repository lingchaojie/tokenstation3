package antigravity

import "testing"

func TestModelInfoMap_ContainsLatestClaudeModels(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-fable-5":  "Claude Fable 5",
		"claude-mythos-5": "Claude Mythos 5",
	}

	for model, displayName := range cases {
		info, ok := getModelInfo(model)
		if !ok {
			t.Fatalf("expected model info for %q to exist", model)
		}
		if info.DisplayName != displayName {
			t.Fatalf("unexpected display name for %q: got %q want %q", model, info.DisplayName, displayName)
		}
	}
}

func TestDefaultModels_ContainsNewAndLegacyImageModels(t *testing.T) {
	t.Parallel()

	models := DefaultModels()
	byID := make(map[string]ClaudeModel, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	requiredIDs := []string{
		"claude-fable-5",
		"claude-mythos-5",
		"claude-opus-4-8",
		"claude-opus-4-6-thinking",
		"gemini-2.5-flash-image",
		"gemini-2.5-flash-image-preview",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview",
		"gemini-3-pro-image", // legacy compatibility
	}

	for _, id := range requiredIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected model %q to be exposed in DefaultModels", id)
		}
	}
}
