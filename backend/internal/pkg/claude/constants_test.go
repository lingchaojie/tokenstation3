package claude

import "testing"

func TestDefaultModelsContainsLatestClaudeModels(t *testing.T) {
	t.Parallel()

	ids := DefaultModelIDs()
	byID := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		byID[id] = struct{}{}
	}

	for _, id := range []string{"claude-fable-5", "claude-mythos-5"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected model %q to be exposed in DefaultModels", id)
		}
	}
}
