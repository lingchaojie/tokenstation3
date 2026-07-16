package admin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexImportEntryAcceptsAgentIdentityAuthJSON(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	privateKeyBase64 := base64.StdEncoding.EncodeToString(der)

	item, err := normalizeCodexImportEntry(codexImportEntry{
		Index: 1,
		Value: map[string]any{
			"auth_mode": "agentIdentity",
			"agent_identity": map[string]any{
				"agent_runtime_id":           "runtime-import",
				"agent_private_key":          privateKeyBase64,
				"account_id":                 "account-import",
				"chatgpt_user_id":            "user-import",
				"email":                      "agent@example.invalid",
				"plan_type":                  "pro",
				"chatgpt_account_is_fedramp": false,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.True(t, item.IsAgentIdentity)
	require.Equal(t, service.OpenAIAuthModeAgentIdentity, item.Credentials["auth_mode"])
	require.Equal(t, "runtime-import", item.Credentials["agent_runtime_id"])
	require.Equal(t, privateKeyBase64, item.Credentials["agent_private_key"])
	require.Equal(t, "account-import", item.Credentials["chatgpt_account_id"])
	require.Equal(t, "user-import", item.Credentials["chatgpt_user_id"])
	require.NotContains(t, item.Credentials, "access_token")
	require.NotContains(t, item.Credentials, "refresh_token")
	require.NotEmpty(t, item.WarningTexts)
}

func TestImportCodexSessionsCreatesAgentIdentityWithoutOAuthExpiry(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	svc := newCodexImportMemoryAdminService(nil)
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	result, err := handler.importCodexSessions(context.Background(), CodexSessionImportRequest{
		SkipDefaultGroupBind: boolPtr(true),
	}, []codexImportEntry{{
		Index: 1,
		Value: map[string]any{
			"auth_mode": "agentIdentity",
			"agent_identity": map[string]any{
				"agent_runtime_id":  "runtime-import",
				"agent_private_key": base64.StdEncoding.EncodeToString(der),
				"task_id":           "task-import",
				"account_id":        "account-import",
				"chatgpt_user_id":   "user-import",
			},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	require.Zero(t, result.Failed)
	require.Len(t, svc.createdAccounts, 1)
	created := svc.createdAccounts[0]
	require.Nil(t, created.ExpiresAt)
	require.Nil(t, created.AutoPauseOnExpired)
	require.Equal(t, service.OpenAIAuthModeAgentIdentity, created.Credentials["auth_mode"])
	require.NotContains(t, created.Credentials, "access_token")
	require.NotContains(t, created.Credentials, "refresh_token")
}

func TestImportCodexSessionsConvertsExistingOAuthAccountToAgentIdentity(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	expiresAt := time.Now().Add(time.Hour)
	svc := newCodexImportMemoryAdminService([]service.Account{{
		ID:                 17,
		Name:               "existing-oauth",
		Platform:           service.PlatformOpenAI,
		Type:               service.AccountTypeOAuth,
		Status:             service.StatusActive,
		ExpiresAt:          &expiresAt,
		AutoPauseOnExpired: true,
		Credentials: map[string]any{
			"auth_mode":          "oauth",
			"openai_auth_mode":   "oauth",
			"access_token":       "old-access-token",
			"refresh_token":      "old-refresh-token",
			"id_token":           "old-id-token",
			"expires_at":         expiresAt.Format(time.RFC3339),
			"expires_in":         3600,
			"client_id":          "old-client-id",
			"token_type":         "Bearer",
			"chatgpt_account_id": "account-import",
			"chatgpt_user_id":    "user-import",
			"model_mapping":      map[string]any{"gpt-5": "gpt-5-codex"},
		},
	}})
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	result, err := handler.importCodexSessions(context.Background(), CodexSessionImportRequest{
		SkipDefaultGroupBind: boolPtr(true),
	}, []codexImportEntry{{
		Index: 1,
		Value: map[string]any{
			"auth_mode": "agentIdentity",
			"agent_identity": map[string]any{
				"agent_runtime_id":  "runtime-import",
				"agent_private_key": base64.StdEncoding.EncodeToString(der),
				"task_id":           "task-import",
				"account_id":        "account-import",
				"chatgpt_user_id":   "user-import",
			},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Updated)
	require.Zero(t, result.Created)
	require.Zero(t, result.Failed)
	require.Len(t, svc.updatedAccounts, 1)

	update := svc.updatedAccounts[0].input
	require.NotNil(t, update.ExpiresAt)
	require.Zero(t, *update.ExpiresAt, "Agent Identity must clear the old OAuth account expiry")
	require.NotNil(t, update.AutoPauseOnExpired)
	require.False(t, *update.AutoPauseOnExpired, "Agent Identity must not inherit OAuth auto-pause")
	require.Equal(t, service.OpenAIAuthModeAgentIdentity, update.Credentials["auth_mode"])
	require.Equal(t, "runtime-import", update.Credentials["agent_runtime_id"])
	require.Contains(t, update.Credentials, "model_mapping", "local routing metadata must be preserved")
	for _, key := range []string{
		"openai_auth_mode",
		"access_token",
		"refresh_token",
		"id_token",
		"expires_at",
		"expires_in",
		"client_id",
		"token_type",
	} {
		require.NotContains(t, update.Credentials, key, "OAuth-only credential %q must be removed", key)
	}
}
