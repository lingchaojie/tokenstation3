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
