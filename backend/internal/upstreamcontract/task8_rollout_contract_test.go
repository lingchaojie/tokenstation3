package upstreamcontract

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTask8DeferredGrokProductSurfacesAreAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		"backend/internal/handler/gateway_web_search.go",
		"backend/internal/handler/grok_audio.go",
		"backend/internal/service/grok_audio.go",
		"backend/internal/service/grok_search_count.go",
		"backend/internal/service/openai_gateway_search_surcharge_test.go",
		"backend/internal/service/openai_gateway_grok_search_billing_test.go",
		"backend/migrations/226_group_audio_voice_pricing.sql",
		"backend/migrations/227_group_search_price_per_1k.sql",
	} {
		if pathHasFiles(t, filepath.Join(root, path)) {
			t.Errorf("deferred Grok Voice/Search path must be absent: %s", path)
		}
	}

	for path, forbidden := range map[string][]string{
		"backend/internal/server/routes/gateway.go": {
			`"/tts"`, `"/stt"`, `"/realtime"`, `"/custom-voices"`, `"/custom-voices/:voice_id"`, `"/web_search"`,
		},
		"backend/ent/schema/group.go": {
			`"search_price_per_1k"`, `"audio_realtime_price_per_min"`, `"audio_tts_price_per_million_chars"`, `"audio_stt_price_per_hour"`,
		},
		"backend/internal/service/openai_gateway_service.go":        {"SearchCount", "AudioUsage"},
		"backend/internal/service/gateway_service.go":               {"SearchCount", "AudioUsage", "DoGrokNativeResponsesJSON", "/v1/web_search"},
		"backend/internal/handler/admin/account_handler.go":         {"AudioDataURL", `json:"audio_data_url"`},
		"backend/internal/server/routes/gateway_test.go":            {"custom-voices", "grokCustomVoiceEndpoint"},
		"backend/internal/handler/usage_record_submit_task_test.go": {"SearchCount"},
		"backend/internal/service/response_model_billing_test.go": {
			"SearchCount", "AudioUsage", "CalculateSearchCost", "CalculateAudioCost",
		},
		"backend/internal/service/group.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/handler/dto/types.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTtsPricePerMillionChars", "AudioSttPricePerHour",
		},
		"backend/internal/handler/dto/mappers.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTtsPricePerMillionChars", "AudioSttPricePerHour",
		},
		"backend/internal/service/admin_service.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/handler/admin/group_handler.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/admin_group.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/repository/group_repo.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/repository/api_key_repo.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/admin_group_duplicate.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/api_key_auth_cache.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/api_key_auth_cache_impl.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/openai_gateway_usage.go": {
			"SearchPricePer1k", "AudioRealtimePricePerMin", "AudioTTSPricePerMillionChars", "AudioSTTPricePerHour",
		},
		"backend/internal/service/grok_upstream_url.go": {"buildGrokVoiceURL"},
		"backend/internal/service/account_test_service.go": {
			"AccountTestModeGrokSearch", "AccountTestModeGrokTTS", "AccountTestModeGrokSTT", "AccountTestModeGrokRealtime",
			"testGrokSearch", "testGrokTTS", "testGrokSTT", "testGrokRealtime", "grokWSDialer",
		},
		"backend/internal/service/gateway_usage_billing.go": {
			"StableGrokAudioBillingRequestID", "StableGrokRealtimeBillingRequestID",
		},
		"backend/internal/repository/migrations_runner.go":                   {"226_group_audio_voice_pricing.sql", "227_group_search_price_per_1k.sql"},
		"backend/internal/repository/upstream_sync_migration_policy_test.go": {"226_group_audio_voice_pricing.sql", "227_group_search_price_per_1k.sql"},
		"backend/internal/repository/migrations_schema_integration_test.go": {
			"audio_realtime_price_per_min", "audio_tts_price_per_million_chars", "audio_stt_price_per_hour", "search_price_per_1k",
		},
		"backend/internal/server/api_contract_test.go": {
			"audio_realtime_price_per_min", "audio_tts_price_per_million_chars", "audio_stt_price_per_hour", "search_price_per_1k",
		},
		"frontend/src/types/index.ts": {
			"search_price_per_1k", "audio_realtime_price_per_min", "audio_tts_price_per_million_chars", "audio_stt_price_per_hour",
		},
		"frontend/src/components/admin/account/AccountTestModal.vue": {
			"testModeSearch", "testModeTTS", "testModeSTT", "testModeRealtime",
		},
		"frontend/src/views/admin/GroupsView.vue": {
			"search_price_per_1k", "audio_realtime_price_per_min", "audio_tts_price_per_million_chars", "audio_stt_price_per_hour",
		},
		"frontend/src/i18n/locales/en/admin/accounts.ts": {
			"testModeSearch", "testModeTTS", "testModeSTT", "testModeRealtime", "sendingSearchRequest", "audioUploadLabel",
		},
		"frontend/src/i18n/locales/zh/admin/accounts.ts": {
			"testModeSearch", "testModeTTS", "testModeSTT", "testModeRealtime", "sendingSearchRequest", "audioUploadLabel",
		},
		"frontend/src/i18n/locales/en/admin/overview.ts": {"explicitPricing", "voicePricing", "searchPricePer1k"},
		"frontend/src/i18n/locales/zh/admin/overview.ts": {"explicitPricing", "voicePricing", "searchPricePer1k"},
	} {
		body := readRepoFile(t, path)
		for _, token := range forbidden {
			if strings.Contains(body, token) {
				t.Errorf("deferred Grok Voice/Search surface %q remains in %s", token, path)
			}
		}
	}
}

func TestTask8ApprovedVideoPricingMigrationRemains(t *testing.T) {
	root := repoRoot(t)
	if !pathHasFiles(t, filepath.Join(root, "backend/migrations/225_group_video_model_prices.sql")) {
		t.Fatal("approved per-model video pricing migration 225 is missing")
	}

	for path, required := range map[string][]string{
		"backend/ent/schema/group.go":             {`"video_model_prices"`},
		"backend/internal/service/group.go":       {"VideoModelPrices", "GetVideoPriceForModel"},
		"frontend/src/types/index.ts":             {"VideoModelPrices", "video_model_prices"},
		"frontend/src/views/admin/GroupsView.vue": {"video_model_prices"},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("approved video pricing surface %q missing from %s", token, path)
			}
		}
	}
}

func TestTask8GeneratedGroupSchemaMatchesApprovedPricingSurface(t *testing.T) {
	generatedGroupPaths := []string{
		"backend/ent/group.go",
		"backend/ent/group/group.go",
		"backend/ent/group/where.go",
		"backend/ent/group_create.go",
		"backend/ent/group_update.go",
		"backend/ent/migrate/schema.go",
		"backend/ent/mutation.go",
		"backend/ent/runtime/runtime.go",
	}
	for _, path := range generatedGroupPaths {
		body := readRepoFile(t, path)
		for _, rejected := range []string{
			"search_price_per_1k",
			"audio_realtime_price_per_min",
			"audio_tts_price_per_million_chars",
			"audio_stt_price_per_hour",
		} {
			if strings.Contains(strings.ToLower(body), rejected) {
				t.Errorf("generated Ent surface %q remains in %s after its schema/migration was excluded", rejected, path)
			}
		}
	}

	for path, required := range map[string][]string{
		"backend/ent/group.go":          {"VideoModelPrices"},
		"backend/ent/group/group.go":    {`FieldVideoModelPrices = "video_model_prices"`},
		"backend/ent/group_create.go":   {"SetVideoModelPrices"},
		"backend/ent/group_update.go":   {"SetVideoModelPrices"},
		"backend/ent/migrate/schema.go": {`{Name: "video_model_prices"`},
		"backend/ent/mutation.go":       {"SetVideoModelPrices"},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("approved generated Ent surface %q missing from %s", token, path)
			}
		}
	}

	// Regeneration must continue to include local schema families introduced before
	// this upstream sync; otherwise a clean Group fix could silently drop local Ent.
	migrationSchema := readRepoFile(t, "backend/ent/migrate/schema.go")
	for _, localTable := range []string{
		`"daily_check_in_claims"`,
		`"user_api_key_routes"`,
		`"web_chat_artifacts"`,
		`"web_chat_attachments"`,
		`"web_chat_conversations"`,
		`"web_chat_messages"`,
	} {
		if !strings.Contains(migrationSchema, localTable) {
			t.Errorf("local generated Ent table %q disappeared during Task8 regeneration", localTable)
		}
	}
}

func TestTask8GrokRolloutDefaultsStayOptIn(t *testing.T) {
	for path, required := range map[string][]string{
		"backend/internal/config/config.go": {
			`viper.SetDefault("gateway.grok.free_quota_soft_gate_enabled", false)`,
		},
		"deploy/config.example.yaml": {"free_quota_soft_gate_enabled: false"},
		"backend/internal/service/setting_service.go": {
			`SettingKeyGrokCrossClientModelMapEnabled:       "false"`,
			"parseGrokCrossClientModelMapEnabled(settings[SettingKeyGrokCrossClientModelMapEnabled])",
			`publishGrokRuntimeModelMapping("", false)`,
			"publishGrokRuntimeModelMapping(result.GrokDefaultTextModel, result.GrokCrossClientModelMapEnabled)",
			"publishGrokRuntimeModelMapping(settings.GrokDefaultTextModel, settings.GrokCrossClientModelMapEnabled)",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("Task8 opt-in default contract %q missing from %s", token, path)
			}
		}
	}
}

func TestTask8GrokRuntimeMappingLoadsDuringStartup(t *testing.T) {
	serviceWire := readRepoFile(t, "backend/internal/service/wire.go")
	providerStart := strings.Index(serviceWire, "func ProvideSettingService(")
	if providerStart < 0 {
		t.Fatal("locate ProvideSettingService source")
	}
	providerEndOffset := strings.Index(serviceWire[providerStart:], "\n}")
	if providerEndOffset < 0 {
		t.Fatal("locate ProvideSettingService end")
	}
	provider := serviceWire[providerStart : providerStart+providerEndOffset]

	initialize := strings.Index(provider, "svc.InitializeDefaultSettings(context.Background())")
	loadGrok := strings.Index(provider, "LoadGrokRuntimeModelMappingSettings(context.Background(), settingRepo)")
	loadForwardedIP := strings.Index(provider, "svc.LoadForwardedClientIPSettings(context.Background())")
	if initialize < 0 || loadGrok < 0 || loadForwardedIP < 0 {
		t.Fatalf("ProvideSettingService must initialize defaults, load Grok runtime mapping, then load forwarded-IP settings")
	}
	if initialize >= loadGrok || loadGrok >= loadForwardedIP {
		t.Fatalf("Grok runtime mapping load must run immediately after default initialization")
	}
}

func TestTask8VideoPricingFallbackOrderAndLocalMediaInvariants(t *testing.T) {
	billing := readRepoFile(t, "backend/internal/service/billing_service.go")
	modelPrice := strings.Index(billing, "LookupVideoModelPrice(groupConfig.ModelPrices, model, resolution)")
	legacyFlat := strings.Index(billing, "case VideoBillingResolution480P:")
	codeFallback := strings.Index(billing, "return s.getDefaultVideoPrice(model, resolution)")
	if modelPrice < 0 || legacyFlat < 0 || codeFallback < 0 || modelPrice >= legacyFlat || legacyFlat >= codeFallback {
		t.Fatalf("video pricing must prefer model map, then legacy flat tier, then code fallback")
	}

	for path, required := range map[string][]string{
		"backend/internal/handler/grok_media.go": {
			"ResolveGrokMediaVideoRequestAccount", "boundLookupAccountID", "GrokMediaEndpointVideoContent",
		},
		"backend/internal/service/grok_media.go": {
			"IsGrokVideoStatusBillable", `"video.url"`, `"done"`, "forwardGrokMediaVideoContent",
		},
		"backend/internal/service/openai_gateway_upstream_errors.go": {
			"case 401, 402, 403, 405, 429, 529:",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("Task8 local media invariant %q missing from %s", token, path)
			}
		}
	}

	// The reverted account-wide 405 eviction was implemented in this Grok path;
	// 405 now belongs only to the generic failover classifier above.
	grokForward := readRepoFile(t, "backend/internal/service/openai_gateway_grok.go")
	if strings.Contains(grokForward, "StatusMethodNotAllowed") || strings.Contains(grokForward, "statusCode == 405") {
		t.Fatal("Grok 405 must not reintroduce account-wide eviction side effects")
	}
}

func TestTask8GrokSecurityAndRepairChainRemains(t *testing.T) {
	for path, required := range map[string][]string{
		"backend/internal/pkg/xai/oauth.go": {
			"TryConsumeSession", "TryConsume", "oauthEndpointAllowedHosts", "ValidateTrustedBaseURL",
		},
		"backend/internal/service/grok_oauth_service.go": {
			"GROK_OAUTH_CLIENT_NOT_CONFIGURED", "TryConsumeSession", "GROK_OAUTH_SESSION_ALREADY_USED",
		},
		"backend/internal/handler/admin/grok_oauth_handler.go": {
			"grokSSOImportConcurrency", "normalizeSSOImportTokens",
		},
		"backend/internal/handler/admin/grok_import_probe.go": {
			"grokImportProbeQueueLimit", "pending", "inFlight", "grokImportProbeConcurrency",
		},
		"backend/internal/service/grok_upstream_url.go": {
			"grokOperatorPolicyValidator", "IsOfficialBaseURL", "ValidateTrustedBaseURL",
		},
		"backend/internal/service/grok_quota_service.go": {
			"grokBillingMaxAttempts", "isRetryableGrokBillingStatus", `"billing returned %d"`,
		},
		"backend/internal/service/openai_account_runtime_block_fastpath.go": {
			"recordOpenAIAccountModelTransientFailure", `"block_scope", "account_model"`,
		},
		"backend/internal/service/openai_gateway_scheduling.go": {
			"account.GrokMediaGenerationEligibility()",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("Task8 Grok security/repair invariant %q missing from %s", token, path)
			}
		}
	}

	billing := readRepoFile(t, "backend/internal/service/grok_quota_service.go")
	for _, leaked := range []string{`"status", statusCode, "body"`, `"billing returned %d: %s"`} {
		if strings.Contains(billing, leaked) {
			t.Errorf("billing error-body redaction regressed: %q remains", leaked)
		}
	}
	quotaTests := readRepoFile(t, "backend/internal/service/grok_quota_service_test.go")
	if strings.Contains(quotaTests, `require.Contains(t, infraerrors.Message(err), "billing returned 502: cloudflare failure")`) {
		t.Error("upstream retry test still requires the rejected billing response body to leak into the admin error")
	}
	for _, testName := range []string{
		"TestGrokQuotaServiceFetchBillingRetriesTransientStatusesThenSucceeds",
		"TestGrokQuotaServiceFetchBillingRetriesTransportErrorThenSucceeds",
		"TestGrokQuotaServiceProbeBillingRedactsUpstreamErrorBodyFromErrorAndLogs",
		"TestGrokQuotaServicePartialBilling403PersistsMediaEligibilitySignal",
	} {
		if !strings.Contains(quotaTests, testName) {
			t.Errorf("Task8 quota retry/redaction regression test missing: %s", testName)
		}
	}

	grokRequest := readRepoFile(t, "backend/internal/service/openai_gateway_grok_test.go")
	for _, testName := range []string{
		"TestBuildGrokResponsesRequestAppliesHeaderOverridesLast",
		"TestBuildGrokResponsesRequestIgnoresBlockedHeaderOverrides",
	} {
		if !strings.Contains(grokRequest, testName) {
			t.Errorf("Grok header safety regression test missing: %s", testName)
		}
	}
}
