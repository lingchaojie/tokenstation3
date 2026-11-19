//go:build unit

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClaudeVersionUpdatePreservesLocalSettingsAndRefreshesRuntime(t *testing.T) {
	const syncedVersion = "99.0.1"
	const newVersion = "99.0.2"
	h, repo := newPartialPayloadTestHandler(t, map[string]string{
		service.SettingKeySiteName:                         "Local Gateway",
		service.SettingKeyClaudeCodeClientVersion:          claude.CLICurrentVersion,
		service.SettingKeyClaudeCodeClientVersionSynced:    syncedVersion,
		service.SettingKeyClaudeCodeVersionAutoSyncEnabled: "true",
	})
	require.Equal(t, claude.CLICurrentVersion, h.settingService.GetClaudeCodeClientVersion(context.Background()))
	rec := doPartialSettingsUpdate(t, h, map[string]any{
		"claude_code_client_version":            newVersion,
		"claude_code_version_auto_sync_enabled": false,
		"claude_code_client_version_synced":     "9.9.9",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Local Gateway", repo.values[service.SettingKeySiteName])
	require.Equal(t, syncedVersion, repo.values[service.SettingKeyClaudeCodeClientVersionSynced])
	require.Equal(t, "false", repo.values[service.SettingKeyClaudeCodeVersionAutoSyncEnabled])
	require.Equal(t, newVersion, h.settingService.GetClaudeCodeClientVersion(context.Background()))
	rec = doPartialSettingsUpdate(t, h, map[string]any{"claude_code_client_version": ""})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, syncedVersion, h.settingService.GetClaudeCodeClientVersion(context.Background()))
}

func TestClaudeVersionInvalidUpdateWritesNothing(t *testing.T) {
	h, repo := newPartialPayloadTestHandler(t, map[string]string{service.SettingKeySiteName: "Local Gateway"})
	rec := doPartialSettingsUpdate(t, h, map[string]any{"claude_code_client_version": "invalid\r\nheader"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Empty(t, repo.lastUpdates)
	require.Equal(t, "Local Gateway", repo.values[service.SettingKeySiteName])
}
