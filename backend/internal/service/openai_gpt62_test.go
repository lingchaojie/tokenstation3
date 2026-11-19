//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestGPT62Identity(t *testing.T) {
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, suffix := range []string{"", "-low", "-medium", "-high", "-xhigh", "-max", "-openai-compact"} {
			model := base + suffix
			require.Equal(t, base, normalizeKnownOpenAICodexModel(model), model)
			require.Equal(t, base, NormalizeOpenAICompatRequestedModel(model), model)
			require.Equal(t, base, normalizeCodexModel(model), model)
			require.True(t, shouldAutoInjectPromptCacheKeyForCompat(model), model)
		}
		require.Equal(t, base, normalizeKnownOpenAICodexModel("openai/"+strings.ToUpper(strings.ReplaceAll(base, "-", "_"))))
	}
	for _, unknown := range []string{"gpt-6.2", "gpt-6.2-foo", "gpt-6.2-sol-preview", "gpt-6.2-astra-law"} {
		require.Empty(t, normalizeKnownOpenAICodexModel(unknown), unknown)
		_, err := newTestBillingService().GetModelPricing(unknown)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, unknown)
	}
}

func TestGPT62FixedTierPricing(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(data)
	require.NoError(t, err)
	for source, svc := range map[string]*BillingService{
		"builtin":  newTestBillingService(),
		"fallback": NewBillingService(&config.Config{}, &PricingService{}),
		"catalog":  NewBillingService(&config.Config{}, catalog),
	} {
		for _, tc := range []struct {
			model                      string
			input, output, write, read float64
		}{
			{"gpt-6.2-sol", 0.002, 0.001, 0.0005, 0.00004},
			{"gpt-6.2-astra", 0.008, 0.004, 0.002, 0.00016},
		} {
			for _, at := range []time.Time{time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 2, 0, 0, 0, 0, time.UTC)} {
				for _, tier := range []struct {
					name       string
					multiplier float64
				}{{"", 1}, {"priority", 2}, {"ultrafast", 6}} {
					t.Run(source+"/"+tc.model+"/"+at.Format("2006-01-02")+"/"+tier.name, func(t *testing.T) {
						for _, model := range []string{tc.model, tc.model + "-max"} {
							cost, err := svc.CalculateCostUnified(CostInput{
								Model: model, Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 100, CacheCreationTokens: 200, CacheReadTokens: 400},
								RateMultiplier: 1, ServiceTier: tier.name, PricingAt: at,
							})
							require.NoError(t, err)
							require.InDelta(t, tc.input*tier.multiplier, cost.InputCost, 1e-12)
							require.InDelta(t, tc.output*tier.multiplier, cost.OutputCost, 1e-12)
							require.InDelta(t, tc.write*tier.multiplier, cost.CacheCreationCost, 1e-12)
							require.InDelta(t, tc.read*tier.multiplier, cost.CacheReadCost, 1e-12)
						}
					})
				}
			}
		}
	}

}

func TestGPT62SubscriptionLongContext(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			account := &Account{Platform: PlatformOpenAI, Type: kind}
			exempt := openAISubscriptionLongContextExempt(account, &OpenAIForwardResult{Model: "public", UpstreamModel: model})
			require.Equal(t, kind != AccountTypeAPIKey, exempt)
			require.False(t, openAISubscriptionLongContextExempt(account, &OpenAIForwardResult{Model: model, UpstreamModel: "gpt-6-astra"}))
			require.False(t, openAISubscriptionLongContextExempt(account, &OpenAIForwardResult{Model: "gpt-6-astra", BillingModel: model}))
		}
		for _, n := range []int{272000, 272001} {
			for _, exempt := range []bool{false, true} {
				enabled := true
				cost, err := newTestBillingService().CalculateCostUnified(CostInput{
					Model: model, Tokens: UsageTokens{InputTokens: n, OutputTokens: 100}, RateMultiplier: 1,
					LongContextBillingEnabled: &enabled, DisableModelLongContextPricing: exempt,
				})
				require.NoError(t, err)
				unit := 2e-6
				if model == "gpt-6.2-astra" {
					unit = 8e-6
				}
				inputFactor, outputFactor := 1.0, 1.0
				if n == 272001 && !exempt {
					inputFactor, outputFactor = 2, 1.5
				}
				require.InDelta(t, float64(n)*unit*inputFactor, cost.InputCost, 1e-12)
				require.InDelta(t, 100*unit*5*outputFactor, cost.OutputCost, 1e-12)
			}
		}
	}
}

func TestGPT62CompatibilityPreservesReasoning(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		req := &apicompat.AnthropicRequest{Model: model + "-max"}
		applyOpenAICompatModelNormalization(req)
		require.Equal(t, model, req.Model)
		require.NotNil(t, req.OutputConfig)
		require.Equal(t, "max", req.OutputConfig.Effort)
		converted, err := apicompat.AnthropicToResponses(req)
		require.NoError(t, err)
		require.Equal(t, "max", converted.Reasoning.Effort)
		for _, effort := range []string{"none", "minimal"} {
			require.Error(t, validateGPT61SolCompatRequest([]byte(`{"reasoning":{"effort":"`+effort+`"}}`), model))
		}
		body, changed, err := normalizeGPT6ResponsesSampling([]byte(`{"temperature":0.7,"top_p":0.8,"input":"hi"}`), model)
		require.NoError(t, err)
		require.True(t, changed)
		require.JSONEq(t, `{"input":"hi"}`, string(body))
	}
}

func TestGPT62CodexCapabilities(t *testing.T) {
	for _, tc := range []struct {
		model  string
		window int
	}{{"gpt-6.2-sol", 500000}, {"gpt-6.2-astra", 272000}} {
		desc := newConfiguredCodexModelDescriptor(tc.model)
		require.EqualValues(t, tc.window, desc.ContextWindow)
		require.Equal(t, "low", *desc.DefaultReasoningLevel)
		require.NotEmpty(t, desc.officialMetadata)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		body, err := completeAPIKeyCodexModelsManifestMetadata([]byte(`{"models":[{"slug":"`+tc.model+`"}]}`), true, account)
		require.NoError(t, err)
		var manifest struct {
			Models []map[string]any `json:"models"`
		}
		require.NoError(t, json.Unmarshal(body, &manifest))
		require.Len(t, manifest.Models, 1)
		require.Equal(t, false, manifest.Models[0]["use_responses_lite"])
		require.EqualValues(t, tc.window, manifest.Models[0]["context_window"])
		require.Contains(t, string(body), "6x")
		for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			acc := &Account{Platform: PlatformOpenAI, Type: kind}
			caps := accountCodexToolCapabilities(acc, tc.model)
			require.JSONEq(t, "true", string(caps["supports_search_tool"]))
			if kind == AccountTypeAPIKey {
				require.JSONEq(t, "false", string(caps["use_responses_lite"]))
				var tiers []configuredCodexServiceTier
				require.NoError(t, json.Unmarshal(caps["service_tiers"], &tiers))
				require.Contains(t, string(caps["service_tiers"]), "ultrafast")
				require.Contains(t, string(caps["service_tiers"]), "6x")
			} else {
				require.NotContains(t, string(caps["service_tiers"]), "ultrafast")
			}
		}
	}
}

// Outbound identity must move all paired headers to CLI while retaining the
// administrator's effective version and platform fingerprint.
func TestGPT62CodexHeadersNormalizeLegacyTUI(t *testing.T) {
	previousEnforcement := codexIdentityEnforcement.Load()
	codexCanonicalUAMu.RLock()
	previousResolver := codexCanonicalUAResolver
	codexCanonicalUAMu.RUnlock()
	t.Cleanup(func() {
		SetCodexIdentityEnforcementEnabled(previousEnforcement)
		SetCodexCanonicalUserAgentResolver(previousResolver)
	})
	SetCodexIdentityEnforcementEnabled(true)
	SetCodexCanonicalUserAgentResolver(func() string { return "codex-tui/0.169.0 (Linux; x86_64) xterm" })
	h := make(http.Header)
	h.Set("originator", "codex-tui")
	enforceCodexIdentityHeadersWithUA(h, "codex-tui/0.140.0 (Mac OS; arm64) ghostty (codex-tui; 0.140.0)")
	require.Equal(t, "codex_cli_rs", h.Get("originator"))
	require.Equal(t, "codex_cli_rs/0.169.0 (Mac OS; arm64) ghostty", h.Get("User-Agent"))
	require.Equal(t, "0.169.0", h.Get("version"))
	auth := make(http.Header)
	ApplyCodexCanonicalAuthIdentity(auth)
	require.Equal(t, "codex_cli_rs", auth.Get("originator"))
	require.True(t, strings.HasPrefix(auth.Get("User-Agent"), "codex_cli_rs/0.169.0 "))
	require.Empty(t, auth.Get("version"))
}

func TestGPT62PlanEntitlements(t *testing.T) {
	for _, tc := range []struct {
		plan, model string
		want        bool
	}{
		{"pro", "gpt-6.2-sol", true}, {"chatgpt_pro", "gpt-6.2-sol", true},
		{"pro", "gpt-6.2-astra", false}, {"pro-max", "gpt-6.2-astra", true},
		{"promax", "gpt-6.2-sol", true}, {"business", "gpt-6.2-sol", false},
		{"", "gpt-6.2-sol", false}, {"promax", "gpt-6-astra", false},
	} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			acc := &Account{Platform: PlatformOpenAI, Type: kind, Credentials: map[string]any{"plan_type": tc.plan}}
			caps := accountCodexToolCapabilities(acc, tc.model)
			want := tc.want
			if kind == AccountTypeAPIKey {
				want = true
			}
			require.Equal(t, want, strings.Contains(string(caps["service_tiers"]), "ultrafast"), "%s/%s/%s", tc.plan, tc.model, kind)
		}
	}
	acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "promax", "base_url": "https://proxy.example"}}
	require.Empty(t, accountCodexToolCapabilities(acc, "gpt-6.2-sol")["service_tiers"])
	acc.Credentials["base_url"] = "https://chatgpt.com"
	acc.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"gpt-6.2-sol": {ID: "gpt-6.2-sol", CodexToolCapabilities: map[string]json.RawMessage{
			"service_tiers": json.RawMessage(`[{"id":"priority","provider_field":"preserved"}]`),
		}},
	}})
	caps := accountCodexToolCapabilities(acc, "gpt-6.2-sol")
	require.Contains(t, string(caps["service_tiers"]), "provider_field")
	require.Contains(t, string(caps["service_tiers"]), "ultrafast")
}

func TestGPT62RecordUsageKeepsCredentialLongContextPolicy(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(logs, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			tier := "ultrafast"
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "resp_62", Model: "public", BillingModel: model, UpstreamModel: model, ServiceTier: &tier, Usage: OpenAIUsage{InputTokens: 300000, OutputTokens: 100}, Duration: time.Second},
				APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1, true), User: &User{ID: 2},
				Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: kind, Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
			})
			require.NoError(t, err)
			require.NotNil(t, logs.lastLog)
			want := 3.606
			if kind == AccountTypeAPIKey {
				want = 7.209
			}
			if model == "gpt-6.2-astra" {
				want *= 4
			}
			require.InDelta(t, want, logs.lastLog.TotalCost, 1e-10, "%s/%s", model, kind)
		}
	}
}
