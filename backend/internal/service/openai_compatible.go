package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// IsOpenAICompatiblePlatform reports whether a platform can be reached through
// OpenAI-compatible gateway entry points.
func IsOpenAICompatiblePlatform(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformGrok
}

// NormalizeOpenAICompatiblePlatform returns the canonical OpenAI-compatible
// platform value used by account/group validation.
func NormalizeOpenAICompatiblePlatform(platform string) string {
	switch platform {
	case PlatformOpenAI, PlatformGrok:
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
