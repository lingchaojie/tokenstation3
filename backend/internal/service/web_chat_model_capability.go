package service

import (
	"regexp"
	"strings"
)

// webChatModelFamily classifies a model id into a capability family.
type webChatModelFamily int

const (
	webChatFamilyUnknown webChatModelFamily = iota
	webChatFamilyClaude
	webChatFamilyGPT
	webChatFamilyGemini
	webChatFamilyGLM
)

var webChatDateSuffixRe = regexp.MustCompile(`-\d{8}$`)

// normalizeWebChatModelName strips the "-thinking" pseudo-suffix and a trailing
// -YYYYMMDD date so a mapping key like "claude-opus-4-5-20251101-thinking"
// resolves to catalog/capability entries keyed by "claude-opus-4-5".
func normalizeWebChatModelName(model string) string {
	m := strings.TrimSpace(model)
	m = strings.TrimSuffix(m, "-thinking")
	m = webChatDateSuffixRe.ReplaceAllString(m, "")
	return m
}

func resolveWebChatModelFamily(model string) webChatModelFamily {
	id := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(id, "claude-"), strings.HasPrefix(id, "opus-"),
		strings.HasPrefix(id, "sonnet-"), strings.HasPrefix(id, "haiku-"):
		return webChatFamilyClaude
	case strings.HasPrefix(id, "glm-"):
		return webChatFamilyGLM
	case strings.HasPrefix(id, "gemini-"):
		return webChatFamilyGemini
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "o1"),
		strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"):
		return webChatFamilyGPT
	default:
		return webChatFamilyUnknown
	}
}

// ResolveWebChatModelCapability returns per-model thinking capability, keyed by
// model family (prefix) rather than coarse provider. Mirrors the classifier
// pattern of ResolveThinkingProtocol. Only fills thinking-related fields here;
// modality/image fields are still derived from the catalog by the caller.
func ResolveWebChatModelCapability(provider, model string) WebChatModelCapability {
	caps := WebChatModelCapability{Provider: strings.ToLower(strings.TrimSpace(provider)), Model: model}
	switch resolveWebChatModelFamily(model) {
	case webChatFamilyClaude:
		caps.SupportsThinking = true
		caps.ThinkingEfforts = []string{"medium", "high", "xhigh"}
	case webChatFamilyGPT:
		caps.SupportsThinking = true
		caps.ThinkingEfforts = []string{"low", "medium", "high", "xhigh"}
	case webChatFamilyGemini:
		caps.SupportsThinking = true
		caps.ThinkingEfforts = []string{"low", "high"}
	case webChatFamilyGLM:
		// GLM native scale is high/max; expose a plain on/off toggle (no efforts).
		// Downstream NormalizeGLMOpenAIReasoningEffort translates the emitted value.
		caps.SupportsThinking = true
		caps.ThinkingEfforts = nil
	default:
		caps.SupportsThinking = false
		caps.ThinkingEfforts = nil
	}
	return caps
}
