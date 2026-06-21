//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type settingHandlerPublicRepoStub struct {
	values map[string]string
}

func (s *settingHandlerPublicRepoStub) Get(ctx context.Context, key string) (*service.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingHandlerPublicRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *settingHandlerPublicRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingHandlerPublicRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingHandlerPublicRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingHandlerPublicRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingHandlerPublicRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestSettingHandler_GetPublicSettings_ExposesForceEmailOnThirdPartySignup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			ForceEmailOnThirdPartySignup bool `json:"force_email_on_third_party_signup"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.ForceEmailOnThirdPartySignup)
}

func TestSettingHandler_GetPublicSettings_ExposesWeChatOAuthModeCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyWeChatConnectEnabled:             "true",
			service.SettingKeyWeChatConnectAppID:               "wx-mp-app",
			service.SettingKeyWeChatConnectAppSecret:           "wx-mp-secret",
			service.SettingKeyWeChatConnectMode:                "mp",
			service.SettingKeyWeChatConnectScopes:              "snsapi_base",
			service.SettingKeyWeChatConnectOpenEnabled:         "true",
			service.SettingKeyWeChatConnectMPEnabled:           "true",
			service.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			service.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			WeChatOAuthEnabled     bool `json:"wechat_oauth_enabled"`
			WeChatOAuthOpenEnabled bool `json:"wechat_oauth_open_enabled"`
			WeChatOAuthMPEnabled   bool `json:"wechat_oauth_mp_enabled"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.WeChatOAuthEnabled)
	require.True(t, resp.Data.WeChatOAuthOpenEnabled)
	require.True(t, resp.Data.WeChatOAuthMPEnabled)
}

func TestSettingHandler_GetPublicModelPricing_ReturnsCuratedPricingFromFallbackData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pricingService := service.NewPricingService(&config.Config{
		Pricing: config.PricingConfig{
			DataDir:      t.TempDir(),
			FallbackFile: filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"),
		},
	}, nil)
	require.NoError(t, pricingService.Initialize())
	defer pricingService.Stop()

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{}}, &config.Config{}), "test-version")
	h.SetPricingService(pricingService)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-pricing", nil)

	h.GetPublicModelPricing(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Providers []struct {
				Provider    string `json:"provider"`
				AccentColor string `json:"accent_color"`
				Models      []struct {
					Name                string  `json:"name"`
					Model               string  `json:"model"`
					InputPerMillion     float64 `json:"input_per_million"`
					OutputPerMillion    float64 `json:"output_per_million"`
					CacheReadPerMillion float64 `json:"cache_read_per_million"`
				} `json:"models"`
			} `json:"providers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Providers, 2)

	anthropic := resp.Data.Providers[0]
	require.Equal(t, "Anthropic", anthropic.Provider)
	require.Equal(t, "#d97745", anthropic.AccentColor)
	require.NotEmpty(t, anthropic.Models)
	require.Equal(t, "Claude Opus 4.8", anthropic.Models[0].Name)
	require.Equal(t, "claude-opus-4-8", anthropic.Models[0].Model)
	require.InDelta(t, 5.0, anthropic.Models[0].InputPerMillion, 0.001)
	require.InDelta(t, 25.0, anthropic.Models[0].OutputPerMillion, 0.001)
	require.InDelta(t, 0.5, anthropic.Models[0].CacheReadPerMillion, 0.001)
	for _, model := range anthropic.Models {
		require.NotEqual(t, "Claude Mythos 5", model.Name)
	}

	openai := resp.Data.Providers[1]
	require.Equal(t, "OpenAI", openai.Provider)
	require.Equal(t, "gpt-5.5", openai.Models[0].Model)
}

func TestSettingHandler_GetPublicModelPricing_OmitsMissingCuratedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fallbackFile := filepath.Join(t.TempDir(), "model_pricing.json")
	require.NoError(t, os.WriteFile(fallbackFile, []byte(`{
		"claude-opus-4-8": {
			"input_cost_per_token": 0.000005,
			"output_cost_per_token": 0.000025,
			"cache_read_input_token_cost": 0.0000005,
			"litellm_provider": "anthropic",
			"mode": "chat"
		}
	}`), 0o644))

	pricingService := service.NewPricingService(&config.Config{
		Pricing: config.PricingConfig{
			DataDir:      t.TempDir(),
			FallbackFile: fallbackFile,
		},
	}, nil)
	require.NoError(t, pricingService.Initialize())
	defer pricingService.Stop()

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{}}, &config.Config{}), "test-version")
	h.SetPricingService(pricingService)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-pricing", nil)

	h.GetPublicModelPricing(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Providers []struct {
				Provider string `json:"provider"`
				Models   []struct {
					Model string `json:"model"`
				} `json:"models"`
			} `json:"providers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Providers, 1)
	require.Equal(t, "Anthropic", resp.Data.Providers[0].Provider)
	require.Len(t, resp.Data.Providers[0].Models, 1)
	require.Equal(t, "claude-opus-4-8", resp.Data.Providers[0].Models[0].Model)
}

func TestSettingHandler_GetPublicModelCatalog_ReturnsCompleteCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{}}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-catalog", nil)

	h.GetPublicModelCatalog(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			UpdatedAt string `json:"updated_at"`
			Providers []struct {
				Key        string `json:"key"`
				Name       string `json:"name"`
				ModelCount int    `json:"model_count"`
			} `json:"providers"`
			Models []struct {
				Provider     string   `json:"provider"`
				ProviderName string   `json:"provider_name"`
				ModelName    string   `json:"model_name"`
				DisplayName  string   `json:"display_name"`
				Modalities   []string `json:"modalities"`
				PriceStatus  string   `json:"price_status"`
				SourceURL    string   `json:"source_url"`
				Pricing      struct {
					Currency            string  `json:"currency"`
					Unit                string  `json:"unit"`
					InputPerMillion     float64 `json:"input_per_million"`
					OutputPerMillion    float64 `json:"output_per_million"`
					CacheReadPerMillion float64 `json:"cache_read_per_million"`
					Note                string  `json:"note"`
					PriceLines          []struct {
						Label  string  `json:"label"`
						Amount float64 `json:"amount"`
						Unit   string  `json:"unit"`
					} `json:"price_lines"`
				} `json:"pricing"`
			} `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "2026-06-21", resp.Data.UpdatedAt)
	require.Len(t, resp.Data.Models, 43)
	require.Len(t, resp.Data.Providers, 8)
	require.Equal(t, "anthropic", resp.Data.Providers[0].Key)
	require.Equal(t, "Anthropic", resp.Data.Providers[0].Name)
	require.Equal(t, 10, resp.Data.Providers[0].ModelCount)

	for _, provider := range resp.Data.Providers {
		key := strings.ToLower(provider.Key)
		name := strings.ToLower(provider.Name)
		require.NotContains(t, key, "agnes")
		require.NotContains(t, key, "doubao")
		require.NotContains(t, name, "agnes")
		require.NotContains(t, name, "doubao")
	}

	for _, model := range resp.Data.Models {
		provider := strings.ToLower(model.Provider)
		providerName := strings.ToLower(model.ProviderName)
		modelName := strings.ToLower(model.ModelName)
		displayName := strings.ToLower(model.DisplayName)
		require.NotContains(t, provider, "agnes")
		require.NotContains(t, provider, "doubao")
		require.NotContains(t, providerName, "agnes")
		require.NotContains(t, providerName, "doubao")
		require.NotContains(t, modelName, "agnes")
		require.NotContains(t, modelName, "doubao")
		require.NotContains(t, displayName, "agnes")
		require.NotContains(t, displayName, "doubao")
		require.NotEmpty(t, model.ProviderName)
		require.NotEmpty(t, model.DisplayName)
		require.NotEmpty(t, model.Modalities)
	}
}

func TestSettingHandler_GetPublicModelCatalog_ExposesConfirmedAndUnverifiedPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{}}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/model-catalog", nil)

	h.GetPublicModelCatalog(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				ModelName   string `json:"model_name"`
				PriceStatus string `json:"price_status"`
				SourceURL   string `json:"source_url"`
				Pricing     struct {
					InputPerMillion     *float64 `json:"input_per_million"`
					OutputPerMillion    *float64 `json:"output_per_million"`
					CacheReadPerMillion *float64 `json:"cache_read_per_million"`
					Note                string   `json:"note"`
					PriceLines          []struct {
						Label  string  `json:"label"`
						Amount float64 `json:"amount"`
						Unit   string  `json:"unit"`
					} `json:"price_lines"`
				} `json:"pricing"`
			} `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))

	byModel := map[string]struct {
		PriceStatus string
		SourceURL   string
		Input       *float64
		Output      *float64
		CacheRead   *float64
		Note        string
		PriceLines  []struct {
			Label  string  `json:"label"`
			Amount float64 `json:"amount"`
			Unit   string  `json:"unit"`
		}
	}{}
	for _, model := range resp.Data.Models {
		byModel[model.ModelName] = struct {
			PriceStatus string
			SourceURL   string
			Input       *float64
			Output      *float64
			CacheRead   *float64
			Note        string
			PriceLines  []struct {
				Label  string  `json:"label"`
				Amount float64 `json:"amount"`
				Unit   string  `json:"unit"`
			}
		}{
			PriceStatus: model.PriceStatus,
			SourceURL:   model.SourceURL,
			Input:       model.Pricing.InputPerMillion,
			Output:      model.Pricing.OutputPerMillion,
			CacheRead:   model.Pricing.CacheReadPerMillion,
			Note:        model.Pricing.Note,
			PriceLines:  model.Pricing.PriceLines,
		}
	}

	opus, ok := byModel["claude-opus-4-8"]
	require.True(t, ok, "expected claude-opus-4-8 in public model catalog")
	require.Equal(t, "confirmed", opus.PriceStatus)
	require.Equal(t, "https://docs.anthropic.com/en/docs/about-claude/pricing", opus.SourceURL)
	require.NotNil(t, opus.Input)
	require.NotNil(t, opus.Output)
	require.NotNil(t, opus.CacheRead)
	require.InDelta(t, 5, *opus.Input, 0.001)
	require.InDelta(t, 25, *opus.Output, 0.001)
	require.InDelta(t, 0.5, *opus.CacheRead, 0.001)

	qwen, ok := byModel["qwen3.6-plus"]
	require.True(t, ok, "expected qwen3.6-plus in public model catalog")
	require.Equal(t, "unverified", qwen.PriceStatus)
	require.Empty(t, qwen.SourceURL)
	require.Nil(t, qwen.Input)
	require.Nil(t, qwen.Output)
	require.Nil(t, qwen.CacheRead)
	require.Equal(t, "Pending confirmation", qwen.Note)

	image, ok := byModel["gpt-image-2"]
	require.True(t, ok, "expected gpt-image-2 in public model catalog")
	require.Equal(t, "confirmed", image.PriceStatus)
	require.Len(t, image.PriceLines, 3)
	require.Equal(t, "1K image", image.PriceLines[0].Label)
	require.InDelta(t, 0.21, image.PriceLines[0].Amount, 0.001)
	require.Equal(t, "image", image.PriceLines[0].Unit)
	require.Equal(t, "2K image", image.PriceLines[1].Label)
	require.InDelta(t, 0.85, image.PriceLines[1].Amount, 0.001)
	require.Equal(t, "image", image.PriceLines[1].Unit)
	require.Equal(t, "4K image", image.PriceLines[2].Label)
	require.InDelta(t, 3.4, image.PriceLines[2].Amount, 0.001)
	require.Equal(t, "image", image.PriceLines[2].Unit)
}
