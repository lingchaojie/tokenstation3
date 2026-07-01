package service

import (
	"reflect"
	"testing"
)

func TestResolveWebChatModelThinkingEfforts(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		efforts  []string
		thinking bool
	}{
		{"anthropic", "claude-opus-4-8", []string{"medium", "high", "xhigh"}, true},
		{"anthropic", "claude-opus-4-8-thinking", []string{"medium", "high", "xhigh"}, true},
		{"openai", "gpt-5.5", []string{"low", "medium", "high", "xhigh"}, true},
		{"gemini", "gemini-3.1-pro-preview", []string{"low", "high"}, true},
		{"openai", "glm-4.7", []string{}, true},          // glm: on/off only → empty efforts
		{"openai", "mystery-model-x", []string{}, false}, // unknown → no thinking
	}
	for _, tc := range cases {
		caps := ResolveWebChatModelCapability(tc.provider, tc.model)
		if caps.SupportsThinking != tc.thinking {
			t.Fatalf("%s/%s: SupportsThinking=%v want %v", tc.provider, tc.model, caps.SupportsThinking, tc.thinking)
		}
		got := caps.ThinkingEfforts
		if len(got) == 0 && len(tc.efforts) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.efforts) {
			t.Fatalf("%s/%s: efforts=%v want %v", tc.provider, tc.model, got, tc.efforts)
		}
	}
}

func TestNormalizeWebChatModelName(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8-thinking":            "claude-opus-4-8",
		"claude-opus-4-5-20251101":            "claude-opus-4-5",
		"claude-sonnet-4-5-20250929-thinking": "claude-sonnet-4-5",
		"gpt-5.5":                             "gpt-5.5",
	}
	for in, want := range cases {
		if got := normalizeWebChatModelName(in); got != want {
			t.Fatalf("normalize(%q)=%q want %q", in, got, want)
		}
	}
}
