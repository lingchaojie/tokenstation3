//go:build unit

package service

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSupportsModelInMapping(t *testing.T) {
	// 命中精确映射
	hit := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{
			"claude-sonnet-4-5-20250929": "claude-sonnet-4.5",
		},
	}}
	require.True(t, hit.SupportsModelInMapping("claude-sonnet-4-5-20250929"))

	// 不在映射 → false
	require.False(t, hit.SupportsModelInMapping("claude-sonnet-99"))

	// 空映射 → false（严格语义：空也拒）
	empty := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{}}
	require.False(t, empty.SupportsModelInMapping("claude-sonnet-4.5"))

	// 通配符命中
	wildcard := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{
			"claude-*": "claude-sonnet-4.5",
		},
	}}
	require.True(t, wildcard.SupportsModelInMapping("claude-anything"))

	// nil 账号 → false（不 panic）
	var nilAcc *Account
	require.False(t, nilAcc.SupportsModelInMapping("claude-sonnet-4.5"))
}

func TestIsModelSupportedByAccount_KiroStrict(t *testing.T) {
	s := &GatewayService{}

	// kiro 账号有映射，请求模型在映射里 → true
	hit := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-sonnet-4-5-20250929": "claude-sonnet-4.5"},
	}}
	require.True(t, s.isModelSupportedByAccount(hit, "claude-sonnet-4-5-20250929"))

	// kiro 账号有映射，模型不在映射 → false（即使老语义会放行）
	require.False(t, s.isModelSupportedByAccount(hit, "claude-future-99"))

	// kiro 账号空映射 → false（严格）
	empty := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{}}
	require.False(t, s.isModelSupportedByAccount(empty, "claude-sonnet-4.5"))

	// 非 kiro 账号（anthropic OAuth）不受影响（空映射→true，老行为）
	anthropic := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	require.True(t, s.isModelSupportedByAccount(anthropic, "claude-sonnet-4-5"))
}

func TestKiroForwardGuard_RejectsUnmappedModel(t *testing.T) {
	s := &GatewayService{}
	acc := &Account{
		ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"claude-sonnet-4.5": "claude-sonnet-4.5"},
		},
	}
	body := []byte(`{"model":"claude-future-99","messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
	require.NoError(t, err)

	// 不需要 gin.Context（守卫在最前），传 nil 让它在守卫处就报错
	_, err = s.forwardKiroMessages(nil, nil, acc, parsed, time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "model_mapping")
}

func TestKiroModelNotSupportedError_Type(t *testing.T) {
	err := kiroModelNotInMappingError(&Account{ID: 7}, "claude-future")
	var typed *KiroModelNotSupportedError
	require.True(t, errors.As(err, &typed))
	require.Equal(t, int64(7), typed.AccountID)
	require.Equal(t, "claude-future", typed.RequestedModel)
	require.Contains(t, err.Error(), "model_mapping")
}

func TestKiroDirectClaudeAliasesFollowExplicitMappingKeys(t *testing.T) {
	account := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{
			"claude-opus-6-1-2": "claude-opus-6.1.2",
			"claude-sonnet-5-5": "exact",
			"claude-sonnet-5.*": "wildcard",
			"claude-haiku-5-5":  "claude-haiku-5.5",
			"private-model":     "claude-opus-99.1",
		},
	}}
	for _, tc := range []struct{ requested, target string }{
		{"claude-opus-6.1.2", "claude-opus-6.1.2"},
		{"claude-sonnet-5-5", "exact"},
		{"claude-sonnet-5.5", "wildcard"},
	} {
		mapped, matched := account.ResolveMappedModel(tc.requested)
		require.True(t, matched, tc.requested)
		require.Equal(t, tc.target, mapped)
	}
	require.False(t, account.SupportsModelInMapping("claude-opus-99.1"))
	require.False(t, account.SupportsModelInMapping("claude-haiku-5.5-thinking"))

	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = "https://relay.example"
	require.False(t, account.SupportsModelInMapping("claude-opus-6.1.2"))
}
