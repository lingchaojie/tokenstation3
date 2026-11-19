//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62ReviewUnknownPricingWithLoadedCatalog(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(data)
	require.NoError(t, err)
	for _, model := range []string{"gpt-6.2", "gpt-6.2-foo", "gpt-6.2-sol-preview", "gpt-6.2-astra-law", "gpt-6.2-sol-2026-11-17", "openai/GPT_6.2_ASTRA_PREVIEW"} {
		t.Run(model, func(t *testing.T) {
			for _, pricing := range []*PricingService{catalog, {}} {
				_, err := NewBillingService(&config.Config{}, pricing).GetModelPricing(model)
				require.ErrorIs(t, err, ErrModelPricingUnavailable)
				require.Nil(t, pricing.GetIdentifiedModelPricing(model))
			}
		})
	}
	// An explicit operator entry remains authoritative, including a free price.
	custom := &LiteLLMModelPricing{}
	catalog.pricingData["gpt-6.2-sol-preview"] = custom
	require.Same(t, custom, catalog.GetModelPricing("gpt-6.2-sol-preview"))
	require.Same(t, custom, catalog.GetIdentifiedModelPricing("gpt-6.2-sol-preview"))
}

func TestGPT62ReviewAPIManifestAliasTransport(t *testing.T) {
	for _, slug := range []string{"gpt-6.2-sol", "gpt-6.2-sol-max", "gpt-6.2-astra-openai-compact", "openai/GPT_6.2_ASTRA_HIGH", "public"} {
		t.Run(slug, func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"public": "gpt-6.2-astra-max"}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
				"gpt-6.2-astra-max": {ID: "gpt-6.2-astra-max", CodexToolCapabilities: map[string]json.RawMessage{"use_responses_lite": json.RawMessage("true")}},
			}})
			manifest := []byte(`{"models":[{"slug":"` + slug + `","use_responses_lite":true}]}`)
			body, err := completeAPIKeyCodexModelsManifestMetadata(manifest, true, account)
			require.NoError(t, err)
			body, err = adjustAPIKeyCodexModelsManifest(body, account)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(body, "models.0.use_responses_lite").Bool(), slug)
			caps := accountCodexToolCapabilities(account, "gpt-6.2-astra-max")
			require.JSONEq(t, "false", string(caps["use_responses_lite"]))
		})
	}
}

func TestGPT62ReviewNativeResponsesRejectDisabledReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, route := range []string{"mapped", "compact-map", "compact-fallback", "passthrough"} {
			for _, field := range []string{`"reasoning":{"effort":"none"}`, `"reasoning_effort":"minimal"`, `"thinking":{"type":"disabled"}`} {
				t.Run(model+"/"+route+"/"+field, func(t *testing.T) {
					account := rawChatCompletionsTestAccount()
					account.Extra = map[string]any{"openai_responses_supported": true}
					account.Credentials["model_mapping"] = map[string]any{"public": model}
					cfg := rawChatCompletionsTestConfig()
					path, requested := "/v1/responses", "public"
					switch route {
					case "compact-map":
						path = "/v1/responses/compact"
						account.Credentials["model_mapping"] = map[string]any{"public": "gpt-5.4"}
						account.Credentials["compact_model_mapping"] = map[string]any{"public": model}
					case "compact-fallback":
						path = "/v1/responses/compact"
						account.Credentials["model_mapping"] = map[string]any{"public": "gpt-5.4"}
						cfg.Gateway.OpenAICompactModel = model
					case "passthrough":
						requested = model
						account.Extra["openai_passthrough"] = true
					}
					body := []byte(`{"model":"` + requested + `","input":"hi",` + field + `}`)
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_62","status":"completed","output":[]}`))}}
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
					_, err := svc.Forward(context.Background(), c, account, body)
					require.Error(t, err)
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Nil(t, upstream.lastReq, "invalid requests must never reach the provider")
				})
			}
		}
	}
}

func TestGPT62ReviewWSReasoningUsesResolvedModel(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"public": "gpt-6.2-sol"}
	for _, effort := range []string{"none", "minimal"} {
		_, err := prepareOpenAIWSHTTPBridgeBody(account, []byte(`{"type":"response.create","model":"public","reasoning":{"effort":"`+effort+`"}}`))
		require.Error(t, err)
		// A later WS turn may omit model and inherit the already resolved model.
		_, _, err = (&OpenAIGatewayService{}).applyOpenAIFastPolicyToWSResponseCreate(context.Background(), account, "gpt-6.2-sol", []byte(`{"type":"response.create","reasoning":{"effort":"`+effort+`"}}`))
		require.Error(t, err)
	}
	for _, model := range []string{"gpt-6.2-sol-max", "gpt-6.2-astra-high"} {
		body, _, err := (&OpenAIGatewayService{}).applyOpenAIFastPolicyToWSResponseCreate(context.Background(), account, model, []byte(`{"type":"response.create"}`))
		require.NoError(t, err)
		require.Equal(t, deriveOpenAIReasoningEffortFromModel(model), gjson.GetBytes(body, "reasoning.effort").String())
	}
	account.Credentials["model_mapping"] = map[string]any{"gpt-6.2-sol": "company-model"}
	body, err := prepareOpenAIWSHTTPBridgeBody(account, []byte(`{"type":"response.create","model":"gpt-6.2-sol","reasoning":{"effort":"none"}}`))
	require.NoError(t, err, "qualification must use the mapped provider model")
	require.False(t, gjson.GetBytes(body, "reasoning").Exists())
}

func TestGPT62ReviewOAuthAliasEffortPreserved(t *testing.T) {
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, ingress := range []string{"responses", "chat", "responses-shape"} {
			for _, tc := range []struct{ requested, mapped, explicit, want string }{
				{base + "-max", "", "", "max"},
				{"openai/" + strings.ToUpper(strings.ReplaceAll(base, "-", "_")) + "_HIGH", "", "", "high"},
				{base + "-max", "", "low", "low"},
				{"public", base + "-max", "", "max"},
				{"public-max", base, "", ""},
				{base + "-max", base + "-high", "", "high"},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s/%s", base, ingress, tc.requested, tc.mapped+tc.explicit), func(t *testing.T) {
					s := newAstraOAuthSetup(t, false)
					s.upstream.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_62","model":"` + base + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)))}
					if tc.mapped != "" {
						s.account.Credentials["model_mapping"] = map[string]any{tc.requested: tc.mapped}
					}
					body := astraRequestBody(tc.requested, false, "", tc.explicit)
					var err error
					var result *OpenAIForwardResult
					if ingress == "chat" {
						body = []byte(`{"model":"` + tc.requested + `","reasoning_effort":"` + tc.explicit + `","messages":[{"role":"user","content":"hi"}]}`)
						result, err = s.svc.ForwardAsChatCompletions(context.Background(), s.c, s.account, body, "", "")
					} else if ingress == "responses-shape" {
						result, err = s.svc.ForwardAsChatCompletions(context.Background(), s.c, s.account, body, "", "")
					} else {
						result, err = s.svc.Forward(context.Background(), s.c, s.account, body)
					}
					require.NoError(t, err)
					require.Equal(t, base, gjson.GetBytes(s.upstream.lastBody, "model").String())
					require.Equal(t, tc.want, gjson.GetBytes(s.upstream.lastBody, "reasoning.effort").String())
					if tc.want == "" {
						require.Nil(t, result.ReasoningEffort, "a public alias must not invent an effort for usage billing")
					} else {
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, tc.want, *result.ReasoningEffort)
					}
				})
			}
		}
	}
}

func TestGPT62ReviewMessagesMappedAliasEffort(t *testing.T) {
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, raw := range []bool{false, true} {
			for _, tc := range []struct{ requested, mapped, explicit, want string }{
				{"public", base + "-max", "", "max"},
				{"public", base + "-max", "low", "low"},
				{base + "-max", base + "-high", "", "high"},
			} {
				t.Run(fmt.Sprintf("%s/raw=%v/%s/%s", base, raw, tc.requested, tc.explicit), func(t *testing.T) {
					s := newAstraOAuthSetup(t, false)
					s.upstream.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_62","model":"` + base + `","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`)))}
					if raw {
						s.account = forceChatMessagesFallbackAccount()
						s.svc.cfg = rawChatCompletionsTestConfig()
						s.upstream.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_62","model":"` + base + `","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}
					}
					s.account.Credentials["model_mapping"] = map[string]any{tc.requested: tc.mapped}
					body := []byte(`{"model":"` + tc.requested + `","max_tokens":8,"output_config":{"effort":"` + tc.explicit + `"},"messages":[{"role":"user","content":"hi"}]}`)
					_, err := s.svc.ForwardAsAnthropic(context.Background(), s.c, s.account, body, "", "")
					require.NoError(t, err)
					path := "reasoning.effort"
					if raw {
						path = "reasoning_effort"
					}
					require.Equal(t, tc.want, gjson.GetBytes(s.upstream.lastBody, path).String())
				})
			}
		}
	}
}
