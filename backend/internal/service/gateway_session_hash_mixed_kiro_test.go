//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func mustParseMixedKiroRequest(t *testing.T, userMsg string, hasMixedKiro bool) *ParsedRequest {
	t.Helper()
	body := anthropicSessionBody("You are helpful", []any{msg("user", userMsg)}, "")
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformAnthropic, HasMixedKiroAutoStickyAccount: hasMixedKiro}
	parsed.SessionContext = &SessionContext{APIKeyID: 7}
	return parsed
}

// 含 mixed kiro auto-sticky 账号的 anthropic 组 + 裸客户端 → system-prompt 稳定 hash（跨轮一致）。
func TestGenerateSessionHash_MixedKiroStableHash(t *testing.T) {
	s := &GatewayService{}
	h1 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn one", true))
	h2 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn two", true))
	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2)
}

// 纯 anthropic 组（无 mixed kiro）裸客户端 → 档位 3 fallback（含全部 messages，逐轮变化，行为不变）。
func TestGenerateSessionHash_PlainAnthropicUnchanged(t *testing.T) {
	s := &GatewayService{}
	h1 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn one", false))
	h2 := s.GenerateSessionHash(mustParseMixedKiroRequest(t, "turn two different", false))
	require.NotEmpty(t, h1)
	require.NotEqual(t, h1, h2)
}

func mustParseNativeKiroRequest(t *testing.T, userMsg string, autoSticky bool) *ParsedRequest {
	t.Helper()
	body := anthropicSessionBody("You are helpful", []any{msg("user", userMsg)}, "")
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro, KiroAutoStickyEnabled: autoSticky}
	parsed.SessionContext = &SessionContext{APIKeyID: 7}
	return parsed
}

// 原生 kiro 组 + auto-sticky 开 → 跨轮稳定 hash（回归保护：重构未改变原生行为）
func TestGenerateSessionHash_NativeKiroAutoStickyOn(t *testing.T) {
	s := &GatewayService{}
	h1 := s.GenerateSessionHash(mustParseNativeKiroRequest(t, "turn one", true))
	h2 := s.GenerateSessionHash(mustParseNativeKiroRequest(t, "turn two", true))
	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2)
}

// 原生 kiro 组 + auto-sticky 关 → 返回 ""（不落档位 3）
func TestGenerateSessionHash_NativeKiroAutoStickyOff(t *testing.T) {
	s := &GatewayService{}
	require.Empty(t, s.GenerateSessionHash(mustParseNativeKiroRequest(t, "turn one", false)))
}

// 混合 kiro 组 + 有 first-user-message seed → 两轮均非空（验证不返回 "" 也不 panic）。
// 注：两轮 user 文本不同，seed 也不同，档位 2.5 各自命中稳定 hash，结果不一定相等；
// 此处只断言非空，保证混合组不会意外走 native-kiro 的 "" 返回路径。
func TestGenerateSessionHash_MixedKiroNoSeedFallsThrough(t *testing.T) {
	s := &GatewayService{}
	mk := func(userMsg string) *ParsedRequest {
		body := anthropicSessionBody("", []any{msg("user", userMsg)}, "")
		parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
		require.NoError(t, err)
		parsed.Group = &Group{Platform: PlatformAnthropic, HasMixedKiroAutoStickyAccount: true}
		parsed.SessionContext = &SessionContext{APIKeyID: 7}
		return parsed
	}
	r1 := s.GenerateSessionHash(mk("turn one"))
	r2 := s.GenerateSessionHash(mk("turn two different"))
	require.NotEmpty(t, r1)
	require.NotEmpty(t, r2)
}
