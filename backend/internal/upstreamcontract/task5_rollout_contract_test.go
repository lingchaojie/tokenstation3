package upstreamcontract

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func pathHasFiles(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		return true
	}
	hasFiles := false
	if err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current != path && !entry.IsDir() {
			hasFiles = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", path, err)
	}
	return hasFiles
}

func TestTask5PromptAuditDedicatedSurfaceIsAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		"backend/internal/securityaudit/prompt_config.go",
		"backend/internal/securityaudit/prompt_repository.go",
		"backend/internal/securityaudit/prompt_payload_store.go",
		"backend/internal/securityaudit/prompt_worker.go",
		"backend/migrations/196_prompt_audit.sql",
		"backend/migrations/197_prompt_audit_full_prompt.sql",
		"frontend/src/features/prompt-audit",
		"openspec/changes/add-openai-compatible-prompt-audit",
	} {
		if pathHasFiles(t, filepath.Join(root, path)) {
			t.Errorf("Prompt Audit dedicated path must contain no files: %s", path)
		}
	}
	legacyBody := readRepoFile(t, "backend/internal/securityaudit/legacy.go")
	for _, forbidden := range []string{"prompt_audit", "prompt_guard", "full_prompt", "PayloadStore", "PromptService"} {
		if strings.Contains(legacyBody, forbidden) {
			t.Errorf("generic security audit adapter still depends on %q", forbidden)
		}
	}

	for _, path := range []string{
		"backend/internal/server/router.go",
		"backend/internal/server/routes/admin.go",
		"frontend/src/router/index.ts",
		"frontend/src/components/layout/AppSidebar.vue",
	} {
		body := readRepoFile(t, path)
		if strings.Contains(body, "prompt-audit") || strings.Contains(body, "registerPromptAuditRoutes") {
			t.Errorf("Prompt Audit route/UI surface remains in %s", path)
		}
	}
}

func TestTask5PanelRateLimitFoundationIsDormantDefaultOff(t *testing.T) {
	serviceBody := readRepoFile(t, "backend/internal/service/setting_panel_rate_limit.go")
	if !strings.Contains(serviceBody, "Enabled:     false") {
		t.Error("panel limiter missing/invalid/error default must be enabled=false")
	}
	if strings.Contains(serviceBody, "fallback = prior.settings") {
		t.Error("panel limiter DB errors must not revive a previously enabled cached value")
	}

	for _, path := range []string{
		"backend/internal/server/routes/admin.go",
		"backend/internal/server/routes/auth.go",
		"backend/internal/server/routes/user.go",
		"backend/internal/server/routes/payment.go",
		"backend/internal/server/routes/model_plaza.go",
	} {
		body := readRepoFile(t, path)
		for _, forbidden := range []string{"NewPanelRateLimiter", "PanelRateLimiter", "panelRateLimiter", "/panel-rate-limit"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("panel limiter runtime mount %q remains in %s", forbidden, path)
			}
		}
	}

	for _, path := range []string{
		"frontend/src/api/admin/settings.ts",
		"frontend/src/views/admin/SettingsView.vue",
		"frontend/src/views/admin/__tests__/SettingsView.spec.ts",
		"frontend/src/i18n/locales/en/admin/settings.ts",
		"frontend/src/i18n/locales/zh/admin/settings.ts",
	} {
		if body := readRepoFile(t, path); strings.Contains(body, "panelRateLimit") || strings.Contains(body, "panel-rate-limit") {
			t.Errorf("panel limiter frontend setting remains in %s", path)
		}
	}
}

func TestTask5NonRolloutSettingsHaveConsolidatedRoundTripChain(t *testing.T) {
	serviceBody := readRepoFile(t, "backend/internal/service/setting_service.go")
	handlerBody := readRepoFile(t, "backend/internal/handler/admin/setting_handler.go")
	dtoBody := readRepoFile(t, "backend/internal/handler/dto/settings.go")

	type settingContract struct {
		field, jsonKey, settingKey string
	}
	contracts := []settingContract{
		{"RegistrationEmailDomainQuotaEnabled", "registration_email_domain_quota_enabled", "SettingKeyRegistrationEmailDomainQuotaEnabled"},
		{"AuditLogRetentionDays", "audit_log_retention_days", "SettingKeyAuditLogRetentionDays"},
		{"ForwardedClientIPHeaders", "forwarded_client_ip_headers", "SettingKeyForwardedClientIPHeaders"},
		{"CompactHomeEnabled", "compact_home_enabled", "SettingKeyCompactHomeEnabled"},
		{"ChannelMonitorMode", "channel_monitor_mode", "SettingKeyChannelMonitorMode"},
		{"ChannelMonitorHideThroughput", "channel_monitor_hide_throughput", "SettingKeyChannelMonitorHideThroughput"},
		{"GrokDefaultTextModel", "grok_default_text_model", "SettingKeyGrokDefaultTextModel"},
		{"GrokCrossClientModelMapEnabled", "grok_cross_client_model_map_enabled", "SettingKeyGrokCrossClientModelMapEnabled"},
		{"GrokDefaultBaseURLMode", "grok_default_base_url_mode", "SettingKeyGrokDefaultBaseURLMode"},
		{"ModelPlazaEnabled", "model_plaza_enabled", "SettingKeyModelPlazaEnabled"},
		{"ModelPlazaRequireAuth", "model_plaza_require_auth", "SettingKeyModelPlazaRequireAuth"},
		{"ModelPlazaDescription", "model_plaza_description", "SettingKeyModelPlazaDescription"},
		{"OpenAICodexClientVersion", "openai_codex_client_version", "SettingKeyOpenAICodexClientVersion"},
		{"OpenAICodexVersionAutoSyncEnabled", "openai_codex_version_auto_sync_enabled", "SettingKeyOpenAICodexVersionAutoSyncEnabled"},
		{"OpenAILowUpstreamRatePriorityEnabled", "openai_low_upstream_rate_priority_enabled", "SettingKeyOpenAILowUpstreamRatePriorityEnabled"},
		{"OpenAIOAuthSchedulingRateMultiplier", "openai_oauth_scheduling_rate_multiplier", "SettingKeyOpenAIOAuthSchedulingRateMultiplier"},
		{"OpenAIAdvancedSchedulerWeightUpstreamCost", "openai_advanced_scheduler_weight_upstream_cost", "SettingKeyOpenAIAdvancedSchedulerWeightUpstreamCost"},
		{"AccountSchedulingThresholds", "account_scheduling_thresholds", "SettingKeyAccountSchedulingThresholds"},
	}
	for _, contract := range contracts {
		if !strings.Contains(handlerBody, `json:"`+contract.jsonKey+`"`) {
			t.Errorf("%s missing consolidated PUT request field", contract.jsonKey)
		}
		if strings.Count(handlerBody, contract.field+":") < 2 {
			t.Errorf("%s missing request-to-SystemSettings or admin response mapping", contract.jsonKey)
		}
		if !strings.Contains(serviceBody, "updates["+contract.settingKey+"]") {
			t.Errorf("%s missing persistence mapping", contract.jsonKey)
		}
		if !strings.Contains(serviceBody, contract.field+":") && !strings.Contains(serviceBody, "result."+contract.field) {
			t.Errorf("%s missing reload/parse mapping", contract.jsonKey)
		}
		if !strings.Contains(dtoBody, `json:"`+contract.jsonKey) {
			t.Errorf("%s missing admin DTO", contract.jsonKey)
		}
	}
	for _, cacheHook := range []string{
		"SetForwardedClientIPSettings(settings.APIKeyACLTrustForwardedIP, settings.ForwardedClientIPHeaders)",
		"lowUpstreamRatePriorityEnabled: settings.OpenAILowUpstreamRatePriorityEnabled",
		"oauthSchedulingRateMultiplier:  settings.OpenAIOAuthSchedulingRateMultiplier",
		"SettingKeyOpenAIAdvancedSchedulerWeightUpstreamCost:     settings.OpenAIAdvancedSchedulerWeightUpstreamCost",
		"s.openAICodexVersionCache.Store",
		"accountSchedulingThresholdsCache.Store",
		"s.notifyChannelMonitorRuntimeListeners()",
	} {
		if !strings.Contains(serviceBody, cacheHook) {
			t.Errorf("non-rollout settings cache/runtime chain missing %q", cacheHook)
		}
	}

}

func TestTask5SourceProviderGraphIsReproducible(t *testing.T) {
	handlerWire := readRepoFile(t, "backend/internal/handler/wire.go")
	for _, required := range []string{
		"func ProvideSecurityAuditCoordinator(",
		"securityaudit.NewLegacyModerationAdapter(contentModerationService)",
		"securityaudit.NewCoordinator(legacy, nil)",
		"ProvideSecurityAuditCoordinator,",
	} {
		if !strings.Contains(handlerWire, required) {
			t.Errorf("generic moderation provider graph missing %q", required)
		}
	}

	serviceWire := readRepoFile(t, "backend/internal/service/wire.go")
	start := strings.Index(serviceWire, "func ProvideSettingService(")
	if start < 0 {
		t.Fatal("locate ProvideSettingService source")
	}
	end := strings.Index(serviceWire[start:], "\n}\n")
	if end < 0 {
		t.Fatal("locate ProvideSettingService source")
	}
	provider := serviceWire[start : start+end]
	for _, required := range []string{
		"svc.InitializeDefaultSettings(context.Background())",
		"return nil, fmt.Errorf(\"initialize default settings: %w\", err)",
		"svc.LoadForwardedClientIPSettings(context.Background())",
		"SetCodexCanonicalUserAgentResolver(func() string",
		"return svc, nil",
	} {
		if !strings.Contains(provider, required) {
			t.Errorf("ProvideSettingService missing %q", required)
		}
	}
	if strings.Contains(provider, "LoadAPIKeyACLTrustForwardedIPSetting") {
		t.Error("ProvideSettingService retains obsolete forwarded-IP loader")
	}
}

func TestTask5SettingsCorrectnessRegressions(t *testing.T) {
	serviceBody := readRepoFile(t, "backend/internal/service/setting_service.go")
	for _, required := range []string{
		"HideThroughput: true",
		"HideThroughput:         !isFalseSettingValue(vals[SettingKeyChannelMonitorHideThroughput])",
		"validateOpenAIOAuthSchedulingRateMultiplier(settings.OpenAIOAuthSchedulingRateMultiplier)",
	} {
		if !strings.Contains(serviceBody, required) {
			t.Errorf("settings regression guard missing %q", required)
		}
	}

	handlerBody := readRepoFile(t, "backend/internal/handler/admin/setting_handler.go")
	for _, required := range []string{
		"mergeSMTPPartialUpdate(&req, previousSettings, sentFields)",
		"h.auditSettingsUpdate(c, previousSettings, updatedSettings, previousAuthSourceDefaults, updatedAuthSourceDefaults, req, sentFields)",
	} {
		if !strings.Contains(handlerBody, required) {
			t.Errorf("handler regression guard missing %q", required)
		}
	}
	load := strings.Index(handlerBody, "updatedSettings, err := h.settingService.GetAllSettings")
	audit := strings.Index(handlerBody, "h.auditSettingsUpdate(c, previousSettings, updatedSettings")
	if load < 0 || audit < load {
		t.Error("operation audit must use the persisted/reloaded settings snapshot")
	}
}

func TestTask5LegacyUnmoderatedEndpointsHaveZeroModerationCalls(t *testing.T) {
	for _, path := range []string{
		"backend/internal/handler/batch_image_handler.go",
		"backend/internal/handler/openai_alpha_search.go",
		"backend/internal/handler/openai_embeddings.go",
	} {
		body := readRepoFile(t, path)
		if count := strings.Count(body, "checkSecurityAudit"); count != 0 {
			t.Errorf("%s moderation call count=%d, want 0 to preserve local HEAD coverage", path, count)
		}
	}
}
