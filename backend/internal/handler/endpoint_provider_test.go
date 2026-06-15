package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestInboundProviderFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Anthropic SDK / default /v1 surfaces
		{"/v1/messages", service.PlatformAnthropic},
		{"/v1/messages/count_tokens", service.PlatformAnthropic},
		{"/v1/models", service.PlatformAnthropic},
		{"/v1/usage", service.PlatformAnthropic},
		{"/antigravity/v1/messages", service.PlatformAnthropic},
		// OpenAI SDK surfaces
		{"/v1/chat/completions", service.PlatformOpenAI},
		{"/chat/completions", service.PlatformOpenAI},
		{"/v1/responses", service.PlatformOpenAI},
		{"/v1/responses/compact", service.PlatformOpenAI},
		{"/responses", service.PlatformOpenAI},
		{"/backend-api/codex/responses", service.PlatformOpenAI},
		{"/v1/embeddings", service.PlatformOpenAI},
		{"/v1/images/generations", service.PlatformOpenAI},
		{"/v1/images/edits", service.PlatformOpenAI},
		// Gemini — out of unified scope
		{"/v1beta/models", ""},
		{"/v1beta/models/gemini-2.5-pro:generateContent", ""},
	}
	for _, c := range cases {
		if got := InboundProviderFromPath(c.path); got != c.want {
			t.Errorf("InboundProviderFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
