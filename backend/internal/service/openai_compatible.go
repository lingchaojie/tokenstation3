package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// DetectModelPlatform recognizes concrete provider-qualified or conventional
// model IDs without enabling the excluded Composite routing product.
func DetectModelPlatform(model string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return "", false
	}
	normalized = strings.TrimPrefix(normalized, "models/")
	if slash := strings.IndexByte(normalized, '/'); slash > 0 {
		provider := strings.TrimSpace(normalized[:slash])
		rest := strings.TrimSpace(normalized[slash+1:])
		switch provider {
		case "anthropic", "claude":
			return PlatformAnthropic, true
		case "openai", "chatgpt":
			return PlatformOpenAI, true
		case "google", "google-ai-studio", "gemini":
			return PlatformGemini, true
		case "xai", "x-ai", "grok":
			return PlatformGrok, true
		case "kimi", "moonshot":
			return PlatformKimi, true
		case "zhipu", "glm", "bigmodel":
			return PlatformZhipu, true
		case "deepseek":
			return PlatformDeepseek, true
		}
		if rest != "" {
			normalized = strings.TrimPrefix(rest, "models/")
		}
	}
	switch {
	case strings.HasPrefix(normalized, "anthropic.claude-"), strings.HasPrefix(normalized, "claude-"):
		return PlatformAnthropic, true
	case strings.HasPrefix(normalized, "gpt-"), strings.HasPrefix(normalized, "chatgpt-"), strings.HasPrefix(normalized, "codex-"), hasOpenAIModelSeriesPrefix(normalized):
		return PlatformOpenAI, true
	case strings.HasPrefix(normalized, "gemini-"), strings.HasPrefix(normalized, "learnlm-"):
		return PlatformGemini, true
	case normalized == "grok" || strings.HasPrefix(normalized, "grok-"):
		return PlatformGrok, true
	case normalized == "k3", normalized == "k3-256k", strings.HasPrefix(normalized, "kimi-"), strings.HasPrefix(normalized, "moonshot-"):
		return PlatformKimi, true
	case strings.HasPrefix(normalized, "glm-"):
		return PlatformZhipu, true
	case strings.HasPrefix(normalized, "deepseek-"):
		return PlatformDeepseek, true
	default:
		return "", false
	}
}

func hasOpenAIModelSeriesPrefix(model string) bool {
	for _, prefix := range []string{"o1", "o3", "o4", "o5"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			return true
		}
	}
	return false
}

func isConcreteRequestPlatform(platform string) bool {
	switch platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return true
	default:
		return false
	}
}

// IsOpenAICompatiblePlatform reports whether a platform can be reached through
// OpenAI-compatible gateway entry points.
func IsOpenAICompatiblePlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return true
	default:
		return false
	}
}

// NormalizeOpenAICompatiblePlatform returns the canonical OpenAI-compatible
// platform value used by account/group validation.
func NormalizeOpenAICompatiblePlatform(platform string) string {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return platform
	default:
		return PlatformOpenAI
	}
}

func WithOpenAICompatiblePlatform(ctx context.Context, platform string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !IsOpenAICompatiblePlatform(platform) {
		platform = PlatformOpenAI
	}
	return context.WithValue(ctx, ctxkey.ForcePlatform, platform)
}

func OpenAICompatiblePlatformFromContext(ctx context.Context) string {
	if ctx == nil {
		return PlatformOpenAI
	}
	if platform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && IsOpenAICompatiblePlatform(platform) {
		return platform
	}
	if platform, ok := ctx.Value(ctxkey.Platform).(string); ok && IsOpenAICompatiblePlatform(platform) {
		return platform
	}
	return PlatformOpenAI
}
