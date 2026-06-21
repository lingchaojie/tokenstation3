# Model Marketplace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build public and authenticated LINX2.AI model marketplace pages backed by a public catalog API, using the OpenLLM model list as the reference set while excluding Agnes and Doubao.

**Architecture:** Add a new backend `GET /api/v1/settings/model-catalog` endpoint that returns a static curated public catalog with official upstream pricing where confirmed and `unverified` pricing status where an exact official price cannot be matched. Add a shared Vue `ModelCatalog` component that fetches this endpoint and handles search, provider filter, modality filter, sorting, loading, empty, and error states. Mount the shared component from `/models` in the public landing shell and `/dashboard/models` in the authenticated app shell.

**Tech Stack:** Go, Gin, existing backend `response.Success` DTO pattern, Vue 3 Composition API, Vite, Tailwind, Pinia auth/app stores, vue-i18n, Vitest, Vue Test Utils.

---

## File Structure

Backend:

- Modify: `backend/internal/handler/dto/settings.go`
  - Add public catalog DTOs next to `PublicModelPricingResponse`.
- Create: `backend/internal/handler/model_catalog.go`
  - Own the catalog constants, provider metadata, defensive Agnes/Doubao exclusion, and `GetPublicModelCatalog`.
- Modify: `backend/internal/server/routes/auth.go`
  - Register `GET /settings/model-catalog` in the existing public settings group.
- Test: `backend/internal/handler/setting_handler_public_test.go`
  - Add unit tests for catalog count, exclusions, confirmed rows, and unverified rows.

Frontend:

- Modify: `frontend/src/api/settings.ts`
  - Add catalog response interfaces and `getPublicModelCatalog()`.
- Create: `frontend/src/utils/modelCatalog.ts`
  - Add pure filtering, sorting, option-building, and price formatting helpers.
- Test: `frontend/src/utils/__tests__/modelCatalog.spec.ts`
  - Cover search, provider filter, modality filter, sort order, and price formatting.
- Create: `frontend/src/components/models/ModelCatalog.vue`
  - Shared marketplace UI that fetches catalog data and renders controls/cards.
- Test: `frontend/src/components/models/__tests__/ModelCatalog.spec.ts`
  - Mount the component with a mocked API and assert controls, cards, prices, pending labels, empty, and error states.
- Create: `frontend/src/views/public/ModelsView.vue`
  - Public landing shell for `/models`.
- Create: `frontend/src/views/user/ModelCatalogView.vue`
  - Authenticated app-shell page for `/dashboard/models`.
- Modify: `frontend/src/router/index.ts`
  - Add `/models` public route and `/dashboard/models` authenticated non-admin route.
- Modify: `frontend/src/router/__tests__/guards.spec.ts`
  - Confirm `/models` is public and `/dashboard/models` requires auth but not admin.
- Modify: `frontend/src/views/HomeView.vue`
  - Add a public header nav link to `/models`.
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`
  - Assert the Models nav entry appears in the public header.
- Modify: `frontend/src/components/layout/AppSidebar.vue`
  - Add a sidebar item for `/dashboard/models`.
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
  - Assert the sidebar source includes the catalog route and i18n key.
- Modify: `frontend/src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts`
  - Include `ModelCatalogView.vue` in the Linear source contract.
- Modify: `frontend/src/i18n/locales/zh.ts`
  - Add Chinese nav and model catalog strings.
- Modify: `frontend/src/i18n/locales/en.ts`
  - Add English nav and model catalog strings.

## Catalog Data Contract

Backend DTO names and JSON keys:

```go
type PublicModelCatalogResponse struct {
	UpdatedAt string                       `json:"updated_at"`
	Providers []PublicModelCatalogProvider `json:"providers"`
	Models    []PublicModelCatalogModel    `json:"models"`
}

type PublicModelCatalogProvider struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	AccentColor string `json:"accent_color"`
	ModelCount  int    `json:"model_count"`
}

type PublicModelCatalogModel struct {
	Provider      string                    `json:"provider"`
	ProviderName  string                    `json:"provider_name"`
	ModelName     string                    `json:"model_name"`
	DisplayName   string                    `json:"display_name"`
	Modalities    []string                  `json:"modalities"`
	Description   string                    `json:"description"`
	ContextWindow int                       `json:"context_window,omitempty"`
	Features      []string                  `json:"features"`
	Pricing       PublicModelCatalogPricing `json:"pricing"`
	PriceStatus   string                    `json:"price_status"`
	SourceURL     string                    `json:"source_url,omitempty"`
	UpdatedAt     string                    `json:"updated_at"`
}

type PublicModelCatalogPricing struct {
	Currency            string                        `json:"currency"`
	Unit                string                        `json:"unit"`
	InputPerMillion     *float64                      `json:"input_per_million,omitempty"`
	OutputPerMillion    *float64                      `json:"output_per_million,omitempty"`
	CacheReadPerMillion *float64                      `json:"cache_read_per_million,omitempty"`
	PriceLines          []PublicModelCatalogPriceLine `json:"price_lines,omitempty"`
	Note                string                        `json:"note,omitempty"`
}

type PublicModelCatalogPriceLine struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}
```

Provider order:

```text
anthropic, openai, gemini, qwen, glm, deepseek, minimax, kimi
```

Official source URLs to store on confirmed rows:

```text
Anthropic: https://docs.anthropic.com/en/docs/about-claude/pricing
OpenAI: https://openai.com/api/pricing/
Gemini: https://ai.google.dev/gemini-api/docs/pricing
Qwen: https://www.alibabacloud.com/help/en/model-studio/model-pricing
GLM: https://docs.z.ai/guides/overview/pricing
DeepSeek: https://api-docs.deepseek.com/quick_start/pricing
MiniMax: https://platform.minimax.io/docs/guides/pricing-paygo
Kimi: https://platform.kimi.ai/docs/pricing/chat
```

Complete model set to encode in `backend/internal/handler/model_catalog.go`:

| Provider | Model name | Display name | Modalities | Price status | Pricing to show |
| --- | --- | --- | --- | --- | --- |
| anthropic | claude-opus-4-8 | Claude Opus 4.8 | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-opus-4-7 | Claude Opus 4.7 | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-opus-4-7-max | Claude Opus 4.7 Max | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-opus-4-6 | Claude Opus 4.6 | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-opus-4-6-thinking | Claude Opus 4.6 Thinking | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-opus-4-5 | Claude Opus 4.5 | text | confirmed | input 5, output 25, cache read 0.5 USD / 1M tokens |
| anthropic | claude-sonnet-4-20250514 | Claude Sonnet 4 | text | confirmed | input 3, output 15, cache read 0.3 USD / 1M tokens |
| anthropic | claude-sonnet-4-5 | Claude Sonnet 4.5 | text | confirmed | input 3, output 15, cache read 0.3 USD / 1M tokens |
| anthropic | claude-sonnet-4-5-20250929 | Claude Sonnet 4.5 20250929 | text | confirmed | input 3, output 15, cache read 0.3 USD / 1M tokens |
| anthropic | claude-sonnet-4-6 | Claude Sonnet 4.6 | text | confirmed | input 3, output 15, cache read 0.3 USD / 1M tokens |
| openai | gpt-5.5 | GPT-5.5 | text | confirmed | input 5, output 30, cache read 0.5 USD / 1M tokens |
| openai | gpt-5.4 | GPT-5.4 | text | confirmed | input 2.5, output 15, cache read 0.25 USD / 1M tokens |
| openai | gpt-5.4-mini | GPT-5.4 Mini | text | confirmed | input 0.75, output 4.5, cache read 0.075 USD / 1M tokens |
| openai | gpt-5.3-codex | GPT-5.3 Codex | text | confirmed | input 1.75, output 14, cache read 0.175 USD / 1M tokens |
| openai | gpt-image-2 | GPT-Image-2 | image | confirmed | text/image input 2.5, output 5, cache read 1.25 USD / 1M tokens; image lines: 1K 0.21, 2K 0.85, 4K 3.4 USD / image |
| openai | gpt-image-2-count | GPT-Image-2 Count | image | confirmed | input 3, output 6 USD / 1M tokens; image line: standard 0.2 USD / image |
| openai | gpt-image-2-hd-count | GPT-Image-2 HD Count | image | confirmed | input 2, output 5 USD / 1M tokens; image line: HD 0.4 USD / image |
| openai | gpt-image-2-4k-count | GPT-Image-2 4K Count | image | confirmed | input 5, output 6 USD / 1M tokens; image line: 4K 0.8 USD / image |
| gemini | gemini-3.5-flash | Gemini 3.5 Flash | text | confirmed | input 1.5, output 9, cache read 0.15 USD / 1M tokens |
| gemini | gemini-3.1-pro-preview | Gemini 3.1 Pro Preview | text | confirmed | input 2, output 12, cache read 0.2 USD / 1M tokens |
| gemini | gemini-2.5-flash-image | Gemini 2.5 Flash Image | image | confirmed | input 0.3, output 2.5, cache read 0.03 USD / 1M tokens; image lines: 1K 0.2, 2K 0.4, 4K 0.8 USD / image |
| gemini | gemini-2.5-flash-image-count | Gemini 2.5 Flash Image Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image lines: 1K 0.1, 2K 0.1, 4K 0.1 USD / image |
| gemini | gemini-3.1-flash-image | Gemini 3.1 Flash Image | image | confirmed | input 0.3, output 2.5 USD / 1M tokens; image lines: 1K 0.2, 2K 0.4, 4K 0.8 USD / image |
| gemini | gemini-3.1-flash-image-count | Gemini 3.1 Flash Image Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: standard 0.3 USD / image |
| gemini | gemini-3.1-flash-image-hd-count | Gemini 3.1 Flash Image HD Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: HD 0.4 USD / image |
| gemini | gemini-3.1-flash-image-4k-count | Gemini 3.1 Flash Image 4K Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: 4K 0.55 USD / image |
| gemini | gemini-3-pro-image-count | Gemini 3 Pro Image Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: standard 0.4 USD / image |
| gemini | gemini-3-pro-image-hd-count | Gemini 3 Pro Image HD Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: HD 0.4 USD / image |
| gemini | gemini-3-pro-image-4k-count | Gemini 3 Pro Image 4K Count | image | confirmed | input 0.5, output 1.5 USD / 1M tokens; image line: 4K 0.5 USD / image |
| qwen | qwen3.5-plus | Qwen3.5 Plus | text | confirmed | price lines: 0-256K input 0.4, output 2.4 USD / 1M tokens; 256K-1M input 0.5, output 3 USD / 1M tokens |
| qwen | qwen3.6-plus | Qwen3.6 Plus | text | unverified | Pending confirmation |
| glm | glm-4.7 | GLM-4.7 | text | confirmed | input 0.441, output 2.06 USD / 1M tokens |
| glm | glm-5 | GLM-5 | text | confirmed | input 0.882, output 3.24 USD / 1M tokens |
| glm | glm-5.1 | GLM-5.1 | text | confirmed | input 1.18, output 4.12 USD / 1M tokens |
| glm | glm-5.2 | GLM-5.2 | text | confirmed | input 1.4, output 4.4, cache read 0.28 USD / 1M tokens |
| deepseek | DeepSeek-V4-Pro | DeepSeek V4 Pro | text | confirmed | input 0.435, output 0.87, cache hit 0.003625 USD / 1M tokens |
| deepseek | DeepSeek-V4-Flash | DeepSeek V4 Flash | text | confirmed | input 0.14, output 0.28, cache hit 0.0028 USD / 1M tokens |
| deepseek | deepseek-v3.2 | DeepSeek V3.2 | text | confirmed | input 0.14, output 0.28, cache hit 0.0028 USD / 1M tokens |
| minimax | MiniMax-M3 | MiniMax M3 | text | confirmed | input 0.62, output 2.47, cache read 0.124 USD / 1M tokens |
| minimax | MiniMax-M2.5 | MiniMax M2.5 | text | confirmed | input 0.309, output 1.24, cache read 0.031 USD / 1M tokens |
| minimax | MiniMax-M2.7 | MiniMax M2.7 | text | confirmed | input 0.309, output 1.24, cache read 0.061 USD / 1M tokens |
| kimi | Kimi-k2.5 | Kimi K2.5 | text | confirmed | input 0.062, output 1.85 USD / 1M tokens |
| kimi | Kimi-k2.6 | Kimi K2.6 | text | confirmed | input 0.097, output 2.38 USD / 1M tokens |

Expected catalog size: 43 models.

## Task 1: Backend DTOs and Catalog Tests

**Files:**
- Modify: `backend/internal/handler/dto/settings.go`
- Modify: `backend/internal/handler/setting_handler_public_test.go`

- [ ] **Step 1: Add failing backend tests**

Append these tests to `backend/internal/handler/setting_handler_public_test.go`:

```go
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
				Provider    string   `json:"provider"`
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

	for _, model := range resp.Data.Models {
		name := strings.ToLower(model.ModelName)
		provider := strings.ToLower(model.Provider)
		require.NotContains(t, provider, "agnes")
		require.NotContains(t, provider, "doubao")
		require.NotContains(t, name, "agnes")
		require.NotContains(t, name, "doubao")
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

	opus := byModel["claude-opus-4-8"]
	require.Equal(t, "confirmed", opus.PriceStatus)
	require.Contains(t, opus.SourceURL, "docs.anthropic.com")
	require.NotNil(t, opus.Input)
	require.NotNil(t, opus.Output)
	require.NotNil(t, opus.CacheRead)
	require.InDelta(t, 5, *opus.Input, 0.001)
	require.InDelta(t, 25, *opus.Output, 0.001)
	require.InDelta(t, 0.5, *opus.CacheRead, 0.001)

	qwen := byModel["qwen3.6-plus"]
	require.Equal(t, "unverified", qwen.PriceStatus)
	require.Empty(t, qwen.SourceURL)
	require.Nil(t, qwen.Input)
	require.Nil(t, qwen.Output)
	require.Equal(t, "Pending confirmation", qwen.Note)

	image := byModel["gpt-image-2"]
	require.Equal(t, "confirmed", image.PriceStatus)
	require.Len(t, image.PriceLines, 3)
	require.Equal(t, "1K image", image.PriceLines[0].Label)
	require.InDelta(t, 0.21, image.PriceLines[0].Amount, 0.001)
}
```

Update the test import list to include `strings`:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run the backend tests and verify they fail for missing API**

Run from `backend/`:

```bash
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetPublicModelCatalog' -count=1
```

Expected: FAIL with a compile error that `h.GetPublicModelCatalog` is undefined.

- [ ] **Step 3: Add DTO types**

Add the DTO block from "Catalog Data Contract" immediately after `PublicModelPricingModel` in `backend/internal/handler/dto/settings.go`.

- [ ] **Step 4: Run the backend tests and verify the handler is still missing**

Run from `backend/`:

```bash
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetPublicModelCatalog' -count=1
```

Expected: FAIL with only the missing `GetPublicModelCatalog` handler error.

- [ ] **Step 5: Commit the failing tests and DTO contract**

```bash
git add backend/internal/handler/dto/settings.go backend/internal/handler/setting_handler_public_test.go
git commit -m "test: add public model catalog backend contract"
```

## Task 2: Backend Catalog Handler and Route

**Files:**
- Create: `backend/internal/handler/model_catalog.go`
- Modify: `backend/internal/server/routes/auth.go`
- Test: `backend/internal/handler/setting_handler_public_test.go`

- [ ] **Step 1: Implement catalog helpers and handler**

Create `backend/internal/handler/model_catalog.go` with this structure and encode every model from the catalog table:

```go
package handler

import (
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

const publicModelCatalogUpdatedAt = "2026-06-21"

const (
	sourceAnthropic = "https://docs.anthropic.com/en/docs/about-claude/pricing"
	sourceOpenAI    = "https://openai.com/api/pricing/"
	sourceGemini    = "https://ai.google.dev/gemini-api/docs/pricing"
	sourceQwen      = "https://www.alibabacloud.com/help/en/model-studio/model-pricing"
	sourceGLM       = "https://docs.z.ai/guides/overview/pricing"
	sourceDeepSeek  = "https://api-docs.deepseek.com/quick_start/pricing"
	sourceMiniMax   = "https://platform.minimax.io/docs/guides/pricing-paygo"
	sourceKimi      = "https://platform.kimi.ai/docs/pricing/chat"
)

var publicModelCatalogProviderMeta = []dto.PublicModelCatalogProvider{
	{Key: "anthropic", Name: "Anthropic", AccentColor: "#d97745"},
	{Key: "openai", Name: "OpenAI", AccentColor: "#27a644"},
	{Key: "gemini", Name: "Gemini", AccentColor: "#4f8df5"},
	{Key: "qwen", Name: "Qwen", AccentColor: "#7c6df2"},
	{Key: "glm", Name: "GLM", AccentColor: "#25a98e"},
	{Key: "deepseek", Name: "DeepSeek", AccentColor: "#4b6bfb"},
	{Key: "minimax", Name: "MiniMax", AccentColor: "#f59e0b"},
	{Key: "kimi", Name: "Kimi", AccentColor: "#8b5cf6"},
}

func usd(input, output float64) dto.PublicModelCatalogPricing {
	return dto.PublicModelCatalogPricing{
		Currency:         "USD",
		Unit:             "1M tokens",
		InputPerMillion:  floatPtr(input),
		OutputPerMillion: floatPtr(output),
	}
}

func usdWithCache(input, output, cacheRead float64) dto.PublicModelCatalogPricing {
	pricing := usd(input, output)
	pricing.CacheReadPerMillion = floatPtr(cacheRead)
	return pricing
}

func pendingPricing() dto.PublicModelCatalogPricing {
	return dto.PublicModelCatalogPricing{
		Currency: "USD",
		Unit:     "1M tokens",
		Note:     "Pending confirmation",
	}
}

func withPriceLines(pricing dto.PublicModelCatalogPricing, lines ...dto.PublicModelCatalogPriceLine) dto.PublicModelCatalogPricing {
	pricing.PriceLines = lines
	return pricing
}

func priceLine(label string, amount float64, unit string) dto.PublicModelCatalogPriceLine {
	return dto.PublicModelCatalogPriceLine{Label: label, Amount: amount, Unit: unit}
}

func floatPtr(value float64) *float64 {
	return &value
}

func catalogModel(provider, providerName, modelName, displayName string, modalities []string, description string, contextWindow int, features []string, pricing dto.PublicModelCatalogPricing, priceStatus string, sourceURL string) dto.PublicModelCatalogModel {
	return dto.PublicModelCatalogModel{
		Provider:      provider,
		ProviderName:  providerName,
		ModelName:     modelName,
		DisplayName:   displayName,
		Modalities:    modalities,
		Description:   description,
		ContextWindow: contextWindow,
		Features:      features,
		Pricing:       pricing,
		PriceStatus:   priceStatus,
		SourceURL:     sourceURL,
		UpdatedAt:     publicModelCatalogUpdatedAt,
	}
}

func textModalities() []string {
	return []string{"text"}
}

func imageModalities() []string {
	return []string{"image"}
}

func textFeatures(extra ...string) []string {
	features := []string{"chat", "reasoning"}
	return append(features, extra...)
}

func imageFeatures(extra ...string) []string {
	features := []string{"image generation"}
	return append(features, extra...)
}

var publicModelCatalogModels = []dto.PublicModelCatalogModel{
	catalogModel("anthropic", "Anthropic", "claude-opus-4-8", "Claude Opus 4.8", textModalities(), "Highest-capability Claude model for complex reasoning, coding, and long-context work.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-7", "Claude Opus 4.7", textModalities(), "Claude Opus model for complex reasoning, writing, and engineering workflows.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-7-max", "Claude Opus 4.7 Max", textModalities(), "High-capacity Claude Opus routing option for demanding agent and coding workloads.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-6", "Claude Opus 4.6", textModalities(), "Claude Opus model for advanced reasoning and long-running tasks.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-6-thinking", "Claude Opus 4.6 Thinking", textModalities(), "Claude Opus reasoning route tuned for explicit thinking workloads.", 200000, textFeatures("tool use", "prompt caching", "thinking"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-opus-4-5", "Claude Opus 4.5", textModalities(), "Claude Opus model for high-accuracy reasoning, coding, and analysis.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 25, 0.5), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-20250514", "Claude Sonnet 4", textModalities(), "Balanced Claude Sonnet model for coding, writing, and production chat workloads.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-5", "Claude Sonnet 4.5", textModalities(), "Balanced Claude Sonnet model with strong coding and agent performance.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 20250929", textModalities(), "Versioned Claude Sonnet 4.5 model for stable production routing.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("anthropic", "Anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6", textModalities(), "Balanced Claude Sonnet model for production coding and agent workflows.", 200000, textFeatures("tool use", "prompt caching"), usdWithCache(3, 15, 0.3), "confirmed", sourceAnthropic),
	catalogModel("openai", "OpenAI", "gpt-5.5", "GPT-5.5", textModalities(), "OpenAI frontier text model for complex reasoning and agentic work.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(5, 30, 0.5), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4", "GPT-5.4", textModalities(), "OpenAI flagship text model for advanced reasoning, coding, and multimodal-adjacent workflows.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(2.5, 15, 0.25), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.4-mini", "GPT-5.4 Mini", textModalities(), "Lower-cost OpenAI model for fast production text and agent tasks.", 400000, textFeatures("tool use", "prompt caching"), usdWithCache(0.75, 4.5, 0.075), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.3-codex", "GPT-5.3 Codex", textModalities(), "OpenAI Codex-focused model for repository-scale coding workflows.", 400000, textFeatures("coding", "tool use", "prompt caching"), usdWithCache(1.75, 14, 0.175), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2", "GPT-Image-2", imageModalities(), "OpenAI image generation model for production visual generation workflows.", 0, imageFeatures("multi-resolution"), withPriceLines(usdWithCache(2.5, 5, 1.25), priceLine("1K image", 0.21, "image"), priceLine("2K image", 0.85, "image"), priceLine("4K image", 3.4, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-count", "GPT-Image-2 Count", imageModalities(), "OpenAI image generation count route for standard-resolution output.", 0, imageFeatures("standard image"), withPriceLines(usd(3, 6), priceLine("Standard image", 0.2, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-hd-count", "GPT-Image-2 HD Count", imageModalities(), "OpenAI image generation count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(2, 5), priceLine("HD image", 0.4, "image")), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-image-2-4k-count", "GPT-Image-2 4K Count", imageModalities(), "OpenAI image generation count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(5, 6), priceLine("4K image", 0.8, "image")), "confirmed", sourceOpenAI),
	catalogModel("gemini", "Gemini", "gemini-3.5-flash", "Gemini 3.5 Flash", textModalities(), "Fast Gemini model for high-throughput text, coding, and agent tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(1.5, 9, 0.15), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", textModalities(), "Gemini Pro preview model for higher-capability reasoning and multimodal-adjacent tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(2, 12, 0.2), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-2.5-flash-image", "Gemini 2.5 Flash Image", imageModalities(), "Gemini image generation model with multi-resolution output pricing.", 0, imageFeatures("multi-resolution"), withPriceLines(usdWithCache(0.3, 2.5, 0.03), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-2.5-flash-image-count", "Gemini 2.5 Flash Image Count", imageModalities(), "Gemini image count route for fixed-price image generation.", 0, imageFeatures("image count"), withPriceLines(usd(0.5, 1.5), priceLine("1K image", 0.1, "image"), priceLine("2K image", 0.1, "image"), priceLine("4K image", 0.1, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image", "Gemini 3.1 Flash Image", imageModalities(), "Gemini Flash image generation model with multi-resolution pricing.", 0, imageFeatures("multi-resolution"), withPriceLines(usd(0.3, 2.5), priceLine("1K image", 0.2, "image"), priceLine("2K image", 0.4, "image"), priceLine("4K image", 0.8, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-count", "Gemini 3.1 Flash Image Count", imageModalities(), "Gemini Flash image count route for standard output.", 0, imageFeatures("standard image"), withPriceLines(usd(0.5, 1.5), priceLine("Standard image", 0.3, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-hd-count", "Gemini 3.1 Flash Image HD Count", imageModalities(), "Gemini Flash image count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(0.5, 1.5), priceLine("HD image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3.1-flash-image-4k-count", "Gemini 3.1 Flash Image 4K Count", imageModalities(), "Gemini Flash image count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(0.5, 1.5), priceLine("4K image", 0.55, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-count", "Gemini 3 Pro Image Count", imageModalities(), "Gemini Pro image count route for standard output.", 0, imageFeatures("standard image"), withPriceLines(usd(0.5, 1.5), priceLine("Standard image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-hd-count", "Gemini 3 Pro Image HD Count", imageModalities(), "Gemini Pro image count route for HD output.", 0, imageFeatures("HD image"), withPriceLines(usd(0.5, 1.5), priceLine("HD image", 0.4, "image")), "confirmed", sourceGemini),
	catalogModel("gemini", "Gemini", "gemini-3-pro-image-4k-count", "Gemini 3 Pro Image 4K Count", imageModalities(), "Gemini Pro image count route for 4K output.", 0, imageFeatures("4K image"), withPriceLines(usd(0.5, 1.5), priceLine("4K image", 0.5, "image")), "confirmed", sourceGemini),
	catalogModel("qwen", "Qwen", "qwen3.5-plus", "Qwen3.5 Plus", textModalities(), "Qwen Plus model with official tiered pricing by request context size.", 1000000, textFeatures("vision input", "video input", "agentic coding"), withPriceLines(dto.PublicModelCatalogPricing{Currency: "USD", Unit: "1M tokens"}, priceLine("0-256K input", 0.4, "1M tokens"), priceLine("0-256K output", 2.4, "1M tokens"), priceLine("256K-1M input", 0.5, "1M tokens"), priceLine("256K-1M output", 3, "1M tokens")), "confirmed", sourceQwen),
	catalogModel("qwen", "Qwen", "qwen3.6-plus", "Qwen3.6 Plus", textModalities(), "Qwen Plus model listed by the reference catalog; official per-model price is not published in the checked pricing table.", 1000000, textFeatures("agentic coding"), pendingPricing(), "unverified", ""),
	catalogModel("glm", "GLM", "glm-4.7", "GLM-4.7", textModalities(), "GLM model for general reasoning, chat, and coding workloads.", 128000, textFeatures("tool use"), usd(0.441, 2.06), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5", "GLM-5", textModalities(), "GLM model for higher-capability reasoning and production chat.", 128000, textFeatures("tool use"), usd(0.882, 3.24), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.1", "GLM-5.1", textModalities(), "GLM model for advanced reasoning and coding workflows.", 128000, textFeatures("tool use"), usd(1.18, 4.12), "confirmed", sourceGLM),
	catalogModel("glm", "GLM", "glm-5.2", "GLM-5.2", textModalities(), "GLM model for frontier reasoning, coding, and agent workflows.", 128000, textFeatures("tool use", "prompt caching"), usdWithCache(1.4, 4.4, 0.28), "confirmed", sourceGLM),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Pro", "DeepSeek V4 Pro", textModalities(), "DeepSeek V4 Pro for high-capability reasoning, coding, and long-context agent work.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.435, 0.87, 0.003625), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "DeepSeek-V4-Flash", "DeepSeek V4 Flash", textModalities(), "DeepSeek V4 Flash for efficient long-context reasoning and production chat.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("deepseek", "DeepSeek", "deepseek-v3.2", "DeepSeek V3.2", textModalities(), "DeepSeek V3.2 model retained from the reference catalog for compatibility.", 1000000, textFeatures("thinking", "tool use", "context caching"), usdWithCache(0.14, 0.28, 0.0028), "confirmed", sourceDeepSeek),
	catalogModel("minimax", "MiniMax", "MiniMax-M3", "MiniMax M3", textModalities(), "MiniMax text model for general chat, coding, and agent workflows.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.62, 2.47, 0.124), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.5", "MiniMax M2.5", textModalities(), "MiniMax text model for efficient production chat and coding tasks.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.031), "confirmed", sourceMiniMax),
	catalogModel("minimax", "MiniMax", "MiniMax-M2.7", "MiniMax M2.7", textModalities(), "MiniMax text model for balanced reasoning and throughput.", 1000000, textFeatures("tool use", "prompt caching"), usdWithCache(0.309, 1.24, 0.061), "confirmed", sourceMiniMax),
	catalogModel("kimi", "Kimi", "Kimi-k2.5", "Kimi K2.5", textModalities(), "Kimi model for long-context chat, reasoning, and coding workflows.", 200000, textFeatures("long context"), usd(0.062, 1.85), "confirmed", sourceKimi),
	catalogModel("kimi", "Kimi", "Kimi-k2.6", "Kimi K2.6", textModalities(), "Kimi model for upgraded long-context reasoning and agent work.", 200000, textFeatures("long context"), usd(0.097, 2.38), "confirmed", sourceKimi),
}

func GetPublicModelCatalogModels() []dto.PublicModelCatalogModel {
	models := make([]dto.PublicModelCatalogModel, 0, len(publicModelCatalogModels))
	for _, model := range publicModelCatalogModels {
		if shouldExcludePublicCatalogModel(model) {
			continue
		}
		models = append(models, model)
	}
	return models
}

func shouldExcludePublicCatalogModel(model dto.PublicModelCatalogModel) bool {
	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	name := strings.ToLower(strings.TrimSpace(model.ModelName))
	display := strings.ToLower(strings.TrimSpace(model.DisplayName))
	return strings.Contains(provider, "agnes") ||
		strings.Contains(provider, "doubao") ||
		strings.Contains(name, "agnes") ||
		strings.Contains(name, "doubao") ||
		strings.Contains(display, "agnes") ||
		strings.Contains(display, "doubao")
}

func publicModelCatalogProviders(models []dto.PublicModelCatalogModel) []dto.PublicModelCatalogProvider {
	counts := make(map[string]int, len(publicModelCatalogProviderMeta))
	for _, model := range models {
		counts[model.Provider]++
	}
	providers := make([]dto.PublicModelCatalogProvider, 0, len(publicModelCatalogProviderMeta))
	for _, provider := range publicModelCatalogProviderMeta {
		count := counts[provider.Key]
		if count == 0 {
			continue
		}
		provider.ModelCount = count
		providers = append(providers, provider)
	}
	return providers
}

func sortPublicModelCatalog(models []dto.PublicModelCatalogModel) {
	providerRank := make(map[string]int, len(publicModelCatalogProviderMeta))
	for idx, provider := range publicModelCatalogProviderMeta {
		providerRank[provider.Key] = idx
	}
	sort.SliceStable(models, func(i, j int) bool {
		leftRank := providerRank[models[i].Provider]
		rightRank := providerRank[models[j].Provider]
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(models[i].DisplayName) < strings.ToLower(models[j].DisplayName)
	})
}

// GetPublicModelCatalog returns the public model marketplace catalog.
// GET /api/v1/settings/model-catalog
func (h *SettingHandler) GetPublicModelCatalog(c *gin.Context) {
	models := GetPublicModelCatalogModels()
	sortPublicModelCatalog(models)
	response.Success(c, dto.PublicModelCatalogResponse{
		UpdatedAt: publicModelCatalogUpdatedAt,
		Providers: publicModelCatalogProviders(models),
		Models:    models,
	})
}
```

- [ ] **Step 2: Register the public route**

Modify the public settings group in `backend/internal/server/routes/auth.go`:

```go
	settings := v1.Group("/settings")
	{
		settings.GET("/public", h.Setting.GetPublicSettings)
		settings.GET("/model-pricing", h.Setting.GetPublicModelPricing)
		settings.GET("/model-catalog", h.Setting.GetPublicModelCatalog)
		settings.GET("/email-unsubscribe", h.Setting.UnsubscribeNotificationEmail)
	}
```

- [ ] **Step 3: Run focused backend tests**

Run from `backend/`:

```bash
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetPublicModelCatalog|TestSettingHandler_GetPublicModelPricing' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run handler package unit tests**

Run from `backend/`:

```bash
go test -tags=unit ./internal/handler -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit backend catalog implementation**

```bash
git add backend/internal/handler/model_catalog.go backend/internal/server/routes/auth.go backend/internal/handler/setting_handler_public_test.go
git commit -m "feat: add public model catalog API"
```

## Task 3: Frontend API Types and Catalog Utilities

**Files:**
- Modify: `frontend/src/api/settings.ts`
- Create: `frontend/src/utils/modelCatalog.ts`
- Create: `frontend/src/utils/__tests__/modelCatalog.spec.ts`

- [ ] **Step 1: Write failing utility tests**

Create `frontend/src/utils/__tests__/modelCatalog.spec.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { PublicModelCatalogModel } from '@/api/settings'
import {
  buildModelCatalogProviderOptions,
  filterModelCatalog,
  formatModelCatalogAmount,
  sortModelCatalog,
} from '../modelCatalog'

const models: PublicModelCatalogModel[] = [
  {
    provider: 'anthropic',
    provider_name: 'Anthropic',
    model_name: 'claude-opus-4-8',
    display_name: 'Claude Opus 4.8',
    modalities: ['text'],
    description: 'Complex reasoning and coding',
    context_window: 200000,
    features: ['reasoning', 'prompt caching'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 5,
      output_per_million: 25,
      cache_read_per_million: 0.5,
    },
    price_status: 'confirmed',
    source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
    updated_at: '2026-06-21',
  },
  {
    provider: 'openai',
    provider_name: 'OpenAI',
    model_name: 'gpt-image-2',
    display_name: 'GPT-Image-2',
    modalities: ['image'],
    description: 'Image generation',
    context_window: 0,
    features: ['image generation'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 2.5,
      output_per_million: 5,
      price_lines: [{ label: '1K image', amount: 0.21, unit: 'image' }],
    },
    price_status: 'confirmed',
    source_url: 'https://openai.com/api/pricing/',
    updated_at: '2026-06-21',
  },
  {
    provider: 'qwen',
    provider_name: 'Qwen',
    model_name: 'qwen3.6-plus',
    display_name: 'Qwen3.6 Plus',
    modalities: ['text'],
    description: 'Agentic coding',
    context_window: 1000000,
    features: ['reasoning'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      note: 'Pending confirmation',
    },
    price_status: 'unverified',
    source_url: '',
    updated_at: '2026-06-21',
  },
]

describe('model catalog utilities', () => {
  it('formats compact USD amounts without noisy decimals', () => {
    expect(formatModelCatalogAmount(5)).toBe('$5')
    expect(formatModelCatalogAmount(0.075)).toBe('$0.075')
    expect(formatModelCatalogAmount(0.003625)).toBe('$0.003625')
  })

  it('filters by keyword, provider, and modality', () => {
    expect(filterModelCatalog(models, { query: 'coding', provider: 'all', modality: 'all' }).map((model) => model.model_name)).toEqual([
      'claude-opus-4-8',
      'qwen3.6-plus',
    ])
    expect(filterModelCatalog(models, { query: '', provider: 'openai', modality: 'image' }).map((model) => model.model_name)).toEqual([
      'gpt-image-2',
    ])
  })

  it('sorts by provider name and confirmation status', () => {
    expect(sortModelCatalog(models, 'provider').map((model) => model.provider_name)).toEqual(['Anthropic', 'OpenAI', 'Qwen'])
    expect(sortModelCatalog(models, 'status').map((model) => model.price_status)).toEqual(['confirmed', 'confirmed', 'unverified'])
  })

  it('builds provider options with stable counts', () => {
    expect(buildModelCatalogProviderOptions(models)).toEqual([
      { value: 'all', label: 'All providers', count: 3 },
      { value: 'anthropic', label: 'Anthropic', count: 1 },
      { value: 'openai', label: 'OpenAI', count: 1 },
      { value: 'qwen', label: 'Qwen', count: 1 },
    ])
  })
})
```

- [ ] **Step 2: Run the utility tests and verify they fail**

Run from `frontend/`:

```bash
pnpm test:run src/utils/__tests__/modelCatalog.spec.ts
```

Expected: FAIL because `src/utils/modelCatalog.ts` does not exist.

- [ ] **Step 3: Add API types and client function**

Append to `frontend/src/api/settings.ts`:

```ts
export interface PublicModelCatalogProvider {
  key: string
  name: string
  accent_color: string
  model_count: number
}

export interface PublicModelCatalogPriceLine {
  label: string
  amount: number
  unit: string
}

export interface PublicModelCatalogPricing {
  currency: string
  unit: string
  input_per_million?: number
  output_per_million?: number
  cache_read_per_million?: number
  price_lines?: PublicModelCatalogPriceLine[]
  note?: string
}

export interface PublicModelCatalogModel {
  provider: string
  provider_name: string
  model_name: string
  display_name: string
  modalities: string[]
  description: string
  context_window?: number
  features: string[]
  pricing: PublicModelCatalogPricing
  price_status: 'confirmed' | 'unverified'
  source_url?: string
  updated_at: string
}

export interface PublicModelCatalogResponse {
  updated_at: string
  providers: PublicModelCatalogProvider[]
  models: PublicModelCatalogModel[]
}

export async function getPublicModelCatalog(): Promise<PublicModelCatalogResponse> {
  const { data } = await apiClient.get<PublicModelCatalogResponse>('/settings/model-catalog')
  return data
}
```

- [ ] **Step 4: Add pure utilities**

Create `frontend/src/utils/modelCatalog.ts`:

```ts
import type { PublicModelCatalogModel } from '@/api/settings'

export type ModelCatalogSortKey = 'default' | 'newest' | 'provider' | 'status'

export interface ModelCatalogFilters {
  query: string
  provider: string
  modality: string
}

export interface ModelCatalogProviderOption {
  value: string
  label: string
  count: number
}

const MODEL_PROVIDER_ORDER = ['anthropic', 'openai', 'gemini', 'qwen', 'glm', 'deepseek', 'minimax', 'kimi']

function providerRank(provider: string): number {
  const rank = MODEL_PROVIDER_ORDER.indexOf(provider)
  return rank === -1 ? MODEL_PROVIDER_ORDER.length : rank
}

export function formatModelCatalogAmount(value: number): string {
  return `$${Number(value.toFixed(6)).toString()}`
}

export function formatContextWindow(value?: number): string {
  if (!value || value <= 0) return ''
  if (value >= 1000000) return `${Number((value / 1000000).toFixed(1)).toString()}M`
  if (value >= 1000) return `${Number((value / 1000).toFixed(0)).toString()}K`
  return value.toString()
}

export function filterModelCatalog(
  models: PublicModelCatalogModel[],
  filters: ModelCatalogFilters,
): PublicModelCatalogModel[] {
  const query = filters.query.trim().toLowerCase()
  return models.filter((model) => {
    const providerMatch = filters.provider === 'all' || model.provider === filters.provider
    const modalityMatch = filters.modality === 'all' || model.modalities.includes(filters.modality)
    if (!providerMatch || !modalityMatch) return false
    if (!query) return true
    const searchText = [
      model.provider_name,
      model.model_name,
      model.display_name,
      model.description,
      ...model.features,
      ...model.modalities,
    ]
      .join(' ')
      .toLowerCase()
    return searchText.includes(query)
  })
}

export function sortModelCatalog(
  models: PublicModelCatalogModel[],
  sortKey: ModelCatalogSortKey,
): PublicModelCatalogModel[] {
  const copy = [...models]
  if (sortKey === 'newest') {
    return copy.sort((a, b) => b.updated_at.localeCompare(a.updated_at) || a.display_name.localeCompare(b.display_name))
  }
  if (sortKey === 'provider') {
    return copy.sort((a, b) => providerRank(a.provider) - providerRank(b.provider) || a.display_name.localeCompare(b.display_name))
  }
  if (sortKey === 'status') {
    return copy.sort((a, b) => a.price_status.localeCompare(b.price_status) || a.display_name.localeCompare(b.display_name))
  }
  return copy
}

export function buildModelCatalogProviderOptions(models: PublicModelCatalogModel[]): ModelCatalogProviderOption[] {
  const counts = new Map<string, { label: string; count: number }>()
  for (const model of models) {
    const current = counts.get(model.provider) ?? { label: model.provider_name, count: 0 }
    current.count += 1
    counts.set(model.provider, current)
  }
  return [
    { value: 'all', label: 'All providers', count: models.length },
    ...Array.from(counts.entries())
      .map(([value, item]) => ({ value, label: item.label, count: item.count }))
      .sort((a, b) => providerRank(a.value) - providerRank(b.value) || a.label.localeCompare(b.label)),
  ]
}
```

- [ ] **Step 5: Run utility tests**

Run from `frontend/`:

```bash
pnpm test:run src/utils/__tests__/modelCatalog.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit frontend API and utilities**

```bash
git add frontend/src/api/settings.ts frontend/src/utils/modelCatalog.ts frontend/src/utils/__tests__/modelCatalog.spec.ts
git commit -m "feat: add model catalog frontend utilities"
```

## Task 4: Shared ModelCatalog Component

**Files:**
- Create: `frontend/src/components/models/ModelCatalog.vue`
- Create: `frontend/src/components/models/__tests__/ModelCatalog.spec.ts`

- [ ] **Step 1: Write failing component tests**

Create `frontend/src/components/models/__tests__/ModelCatalog.spec.ts`:

```ts
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'

import ModelCatalog from '../ModelCatalog.vue'

const { getPublicModelCatalogMock } = vi.hoisted(() => ({
  getPublicModelCatalogMock: vi.fn(),
}))

vi.mock('@/api/settings', () => ({
  getPublicModelCatalog: getPublicModelCatalogMock,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'modelCatalog.searchPlaceholder': 'Search models',
    'modelCatalog.providerFilter': 'Provider',
    'modelCatalog.modalityFilter': 'Modality',
    'modelCatalog.sort': 'Sort',
    'modelCatalog.allProviders': 'All providers',
    'modelCatalog.allModalities': 'All modalities',
    'modelCatalog.modalities.text': 'Text',
    'modelCatalog.modalities.image': 'Image',
    'modelCatalog.sortOptions.default': 'Recommended',
    'modelCatalog.sortOptions.newest': 'Newest',
    'modelCatalog.sortOptions.provider': 'Provider',
    'modelCatalog.sortOptions.status': 'Price status',
    'modelCatalog.confirmed': 'Official price',
    'modelCatalog.pending': 'Pending confirmation',
    'modelCatalog.input': 'Input',
    'modelCatalog.output': 'Output',
    'modelCatalog.cacheRead': 'Cache read',
    'modelCatalog.perMillionTokens': 'per 1M tokens',
    'modelCatalog.source': 'Source',
    'modelCatalog.empty': 'No models found',
    'modelCatalog.loadError': 'Failed to load model catalog',
    'common.retry': 'Retry',
  }
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => messages[key] ?? key }),
  }
})

const catalogFixture = {
  updated_at: '2026-06-21',
  providers: [
    { key: 'anthropic', name: 'Anthropic', accent_color: '#d97745', model_count: 1 },
    { key: 'openai', name: 'OpenAI', accent_color: '#27a644', model_count: 1 },
    { key: 'qwen', name: 'Qwen', accent_color: '#7c6df2', model_count: 1 },
  ],
  models: [
    {
      provider: 'anthropic',
      provider_name: 'Anthropic',
      model_name: 'claude-opus-4-8',
      display_name: 'Claude Opus 4.8',
      modalities: ['text'],
      description: 'Complex reasoning and coding',
      context_window: 200000,
      features: ['reasoning', 'prompt caching'],
      pricing: {
        currency: 'USD',
        unit: '1M tokens',
        input_per_million: 5,
        output_per_million: 25,
        cache_read_per_million: 0.5,
      },
      price_status: 'confirmed',
      source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
      updated_at: '2026-06-21',
    },
    {
      provider: 'openai',
      provider_name: 'OpenAI',
      model_name: 'gpt-image-2',
      display_name: 'GPT-Image-2',
      modalities: ['image'],
      description: 'Image generation',
      context_window: 0,
      features: ['image generation'],
      pricing: {
        currency: 'USD',
        unit: '1M tokens',
        input_per_million: 2.5,
        output_per_million: 5,
        price_lines: [{ label: '1K image', amount: 0.21, unit: 'image' }],
      },
      price_status: 'confirmed',
      source_url: 'https://openai.com/api/pricing/',
      updated_at: '2026-06-21',
    },
    {
      provider: 'qwen',
      provider_name: 'Qwen',
      model_name: 'qwen3.6-plus',
      display_name: 'Qwen3.6 Plus',
      modalities: ['text'],
      description: 'Agentic coding',
      context_window: 1000000,
      features: ['reasoning'],
      pricing: { currency: 'USD', unit: '1M tokens', note: 'Pending confirmation' },
      price_status: 'unverified',
      source_url: '',
      updated_at: '2026-06-21',
    },
  ],
}

function mountCatalog() {
  return mount(ModelCatalog, {
    global: {
      stubs: {
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
}

describe('ModelCatalog', () => {
  beforeEach(() => {
    getPublicModelCatalogMock.mockReset()
    getPublicModelCatalogMock.mockResolvedValue(catalogFixture)
  })

  it('renders confirmed and pending model cards', async () => {
    const wrapper = mountCatalog()
    await flushPromises()

    expect(wrapper.text()).toContain('Claude Opus 4.8')
    expect(wrapper.text()).toContain('$5')
    expect(wrapper.text()).toContain('$25')
    expect(wrapper.text()).toContain('$0.5')
    expect(wrapper.text()).toContain('GPT-Image-2')
    expect(wrapper.text()).toContain('1K image')
    expect(wrapper.text()).toContain('$0.21')
    expect(wrapper.text()).toContain('Qwen3.6 Plus')
    expect(wrapper.text()).toContain('Pending confirmation')
  })

  it('filters by search and provider', async () => {
    const wrapper = mountCatalog()
    await flushPromises()

    await wrapper.get('[data-testid="model-catalog-search"]').setValue('image')
    expect(wrapper.text()).toContain('GPT-Image-2')
    expect(wrapper.text()).not.toContain('Claude Opus 4.8')

    await wrapper.get('[data-testid="model-catalog-search"]').setValue('')
    await wrapper.get('[data-testid="model-catalog-provider"]').setValue('qwen')
    expect(wrapper.text()).toContain('Qwen3.6 Plus')
    expect(wrapper.text()).not.toContain('GPT-Image-2')
  })

  it('shows an error panel and retries loading', async () => {
    getPublicModelCatalogMock.mockRejectedValueOnce(new Error('network failed'))
    getPublicModelCatalogMock.mockResolvedValueOnce(catalogFixture)

    const wrapper = mountCatalog()
    await flushPromises()

    expect(wrapper.text()).toContain('Failed to load model catalog')
    await wrapper.get('[data-testid="model-catalog-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Claude Opus 4.8')
  })
})
```

- [ ] **Step 2: Run component tests and verify they fail**

Run from `frontend/`:

```bash
pnpm test:run src/components/models/__tests__/ModelCatalog.spec.ts
```

Expected: FAIL because `ModelCatalog.vue` does not exist.

- [ ] **Step 3: Implement shared component**

Create `frontend/src/components/models/ModelCatalog.vue` with these sections:

```vue
<template>
  <section class="space-y-5" data-testid="model-catalog">
    <div class="linx-panel-strong p-4 sm:p-5">
      <div class="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p class="linx-section-kicker">{{ t('modelCatalog.kicker') }}</p>
          <h1 class="mt-3 text-2xl font-semibold tracking-[-0.04em] text-linear-ink sm:text-3xl">
            {{ t('modelCatalog.title') }}
          </h1>
          <p class="mt-2 max-w-3xl text-sm leading-6 text-linear-ink-subtle">
            {{ t('modelCatalog.description') }}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4 xl:min-w-[28rem]">
          <div class="linx-panel p-3">
            <p class="text-lg font-semibold text-linear-ink">{{ models.length }}</p>
            <p class="text-xs text-linear-ink-tertiary">{{ t('modelCatalog.stats.models') }}</p>
          </div>
          <div class="linx-panel p-3">
            <p class="text-lg font-semibold text-linear-ink">{{ providers.length }}</p>
            <p class="text-xs text-linear-ink-tertiary">{{ t('modelCatalog.stats.providers') }}</p>
          </div>
          <div class="linx-panel p-3">
            <p class="text-lg font-semibold text-linear-ink">{{ confirmedCount }}</p>
            <p class="text-xs text-linear-ink-tertiary">{{ t('modelCatalog.stats.confirmed') }}</p>
          </div>
          <div class="linx-panel p-3">
            <p class="text-lg font-semibold text-linear-ink">{{ imageCount }}</p>
            <p class="text-xs text-linear-ink-tertiary">{{ t('modelCatalog.stats.image') }}</p>
          </div>
        </div>
      </div>
    </div>

    <div class="linx-panel p-4">
      <div class="grid gap-3 lg:grid-cols-[1.2fr_0.8fr_0.7fr_0.7fr]">
        <label class="relative block">
          <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-linear-ink-tertiary" />
          <input
            v-model="query"
            data-testid="model-catalog-search"
            class="input pl-10"
            type="search"
            :placeholder="t('modelCatalog.searchPlaceholder')"
          />
        </label>
        <select v-model="selectedProvider" data-testid="model-catalog-provider" class="input">
          <option v-for="option in providerOptions" :key="option.value" :value="option.value">
            {{ option.label }} ({{ option.count }})
          </option>
        </select>
        <select v-model="selectedModality" data-testid="model-catalog-modality" class="input">
          <option value="all">{{ t('modelCatalog.allModalities') }}</option>
          <option value="text">{{ t('modelCatalog.modalities.text') }}</option>
          <option value="image">{{ t('modelCatalog.modalities.image') }}</option>
          <option value="audio">{{ t('modelCatalog.modalities.audio') }}</option>
          <option value="video">{{ t('modelCatalog.modalities.video') }}</option>
        </select>
        <select v-model="sortKey" data-testid="model-catalog-sort" class="input">
          <option value="default">{{ t('modelCatalog.sortOptions.default') }}</option>
          <option value="newest">{{ t('modelCatalog.sortOptions.newest') }}</option>
          <option value="provider">{{ t('modelCatalog.sortOptions.provider') }}</option>
          <option value="status">{{ t('modelCatalog.sortOptions.status') }}</option>
        </select>
      </div>
    </div>

    <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <div v-for="index in 6" :key="index" class="linx-panel h-64 animate-pulse bg-linear-surface-1"></div>
    </div>

    <div v-else-if="errorMessage" class="linx-panel p-5" data-testid="model-catalog-error">
      <p class="text-sm font-semibold text-linear-ink">{{ t('modelCatalog.loadError') }}</p>
      <p class="mt-1 text-sm text-linear-ink-subtle">{{ errorMessage }}</p>
      <button data-testid="model-catalog-retry" class="btn btn-secondary mt-4" @click="loadCatalog">
        {{ t('common.retry') }}
      </button>
    </div>

    <div v-else-if="filteredModels.length === 0" class="linx-panel p-8 text-center" data-testid="model-catalog-empty">
      <p class="text-sm font-semibold text-linear-ink">{{ t('modelCatalog.empty') }}</p>
    </div>

    <div v-else class="grid gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="model-catalog-grid">
      <article
        v-for="model in filteredModels"
        :key="`${model.provider}:${model.model_name}`"
        class="linx-panel flex min-h-[22rem] flex-col p-5 transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
      >
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-xs font-medium uppercase tracking-[0.18em] text-primary-400">{{ model.provider_name }}</p>
            <h2 class="mt-2 break-words text-lg font-semibold tracking-[-0.03em] text-linear-ink">{{ model.display_name }}</h2>
            <p class="font-mono-brand mt-1 break-all text-xs text-linear-ink-tertiary">{{ model.model_name }}</p>
          </div>
          <span
            class="rounded-full border px-2.5 py-1 text-xs font-medium"
            :class="model.price_status === 'confirmed' ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-300' : 'border-amber-400/30 bg-amber-500/10 text-amber-300'"
          >
            {{ model.price_status === 'confirmed' ? t('modelCatalog.confirmed') : t('modelCatalog.pending') }}
          </span>
        </div>

        <p class="mt-4 text-sm leading-6 text-linear-ink-subtle">{{ model.description }}</p>

        <div class="mt-4 flex flex-wrap gap-2">
          <span v-for="modality in model.modalities" :key="modality" class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-muted">
            {{ t(`modelCatalog.modalities.${modality}`) }}
          </span>
          <span v-if="formatContextWindow(model.context_window)" class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-muted">
            {{ formatContextWindow(model.context_window) }}
          </span>
        </div>

        <div class="mt-4 flex flex-wrap gap-2">
          <span v-for="feature in model.features" :key="feature" class="text-xs text-linear-ink-tertiary">{{ feature }}</span>
        </div>

        <div class="mt-auto pt-5">
          <div v-if="model.price_status === 'unverified'" class="rounded-lg border border-linear-hairline bg-linear-surface-1 p-3 text-sm text-linear-ink-subtle">
            {{ t('modelCatalog.pending') }}
          </div>
          <div v-else class="space-y-2 rounded-lg border border-linear-hairline bg-linear-surface-1 p-3">
            <div v-if="model.pricing.input_per_million !== undefined" class="flex items-center justify-between gap-3 text-sm">
              <span class="text-linear-ink-subtle">{{ t('modelCatalog.input') }}</span>
              <span class="font-mono-brand font-semibold text-linear-ink">{{ formatModelCatalogAmount(model.pricing.input_per_million) }} / {{ t('modelCatalog.perMillionTokens') }}</span>
            </div>
            <div v-if="model.pricing.output_per_million !== undefined" class="flex items-center justify-between gap-3 text-sm">
              <span class="text-linear-ink-subtle">{{ t('modelCatalog.output') }}</span>
              <span class="font-mono-brand font-semibold text-linear-ink">{{ formatModelCatalogAmount(model.pricing.output_per_million) }} / {{ t('modelCatalog.perMillionTokens') }}</span>
            </div>
            <div v-if="model.pricing.cache_read_per_million !== undefined" class="flex items-center justify-between gap-3 text-sm">
              <span class="text-linear-ink-subtle">{{ t('modelCatalog.cacheRead') }}</span>
              <span class="font-mono-brand font-semibold text-linear-ink">{{ formatModelCatalogAmount(model.pricing.cache_read_per_million) }} / {{ t('modelCatalog.perMillionTokens') }}</span>
            </div>
            <div v-for="line in model.pricing.price_lines || []" :key="line.label" class="flex items-center justify-between gap-3 text-sm">
              <span class="text-linear-ink-subtle">{{ line.label }}</span>
              <span class="font-mono-brand font-semibold text-linear-ink">{{ formatModelCatalogAmount(line.amount) }} / {{ line.unit }}</span>
            </div>
          </div>

          <a
            v-if="model.source_url"
            class="mt-3 inline-flex text-xs font-medium text-primary-300 transition-colors hover:text-primary-200"
            :href="model.source_url"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ t('modelCatalog.source') }}
          </a>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getPublicModelCatalog, type PublicModelCatalogModel, type PublicModelCatalogProvider } from '@/api/settings'
import {
  buildModelCatalogProviderOptions,
  filterModelCatalog,
  formatContextWindow,
  formatModelCatalogAmount,
  sortModelCatalog,
  type ModelCatalogSortKey,
} from '@/utils/modelCatalog'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const loading = ref(false)
const errorMessage = ref('')
const models = ref<PublicModelCatalogModel[]>([])
const providers = ref<PublicModelCatalogProvider[]>([])
const query = ref('')
const selectedProvider = ref('all')
const selectedModality = ref('all')
const sortKey = ref<ModelCatalogSortKey>('default')

const providerOptions = computed(() => {
  const options = buildModelCatalogProviderOptions(models.value)
  return options.map((option) => option.value === 'all' ? { ...option, label: t('modelCatalog.allProviders') } : option)
})

const filteredModels = computed(() => sortModelCatalog(filterModelCatalog(models.value, {
  query: query.value,
  provider: selectedProvider.value,
  modality: selectedModality.value,
}), sortKey.value))

const confirmedCount = computed(() => models.value.filter((model) => model.price_status === 'confirmed').length)
const imageCount = computed(() => models.value.filter((model) => model.modalities.includes('image')).length)

async function loadCatalog() {
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await getPublicModelCatalog()
    models.value = data.models || []
    providers.value = data.providers || []
  } catch (err: unknown) {
    errorMessage.value = extractApiErrorMessage(err, t('modelCatalog.loadError'))
  } finally {
    loading.value = false
  }
}

onMounted(loadCatalog)
</script>
```

- [ ] **Step 4: Run component tests**

Run from `frontend/`:

```bash
pnpm test:run src/components/models/__tests__/ModelCatalog.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Commit shared component**

```bash
git add frontend/src/components/models/ModelCatalog.vue frontend/src/components/models/__tests__/ModelCatalog.spec.ts
git commit -m "feat: add shared model catalog component"
```

## Task 5: Public and Authenticated Routes

**Files:**
- Create: `frontend/src/views/public/ModelsView.vue`
- Create: `frontend/src/views/user/ModelCatalogView.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/router/__tests__/guards.spec.ts`
- Modify: `frontend/src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts`

- [ ] **Step 1: Add failing route guard tests**

Add these cases to `frontend/src/router/__tests__/guards.spec.ts`:

```ts
it('访问 /models 公开页面允许通过', () => {
  const authState: MockAuthState = {
    isAuthenticated: false,
    isAdmin: false,
    isSimpleMode: false,
    backendModeEnabled: false,
    hasPendingAuthSession: false,
  }
  const redirect = simulateGuard('/models', { requiresAuth: false }, authState)
  expect(redirect).toBeNull()
})

it('普通用户访问 /dashboard/models 允许通过', () => {
  const authState: MockAuthState = {
    isAuthenticated: true,
    isAdmin: false,
    isSimpleMode: false,
    backendModeEnabled: false,
    hasPendingAuthSession: false,
  }
  const redirect = simulateGuard('/dashboard/models', { requiresAuth: true, requiresAdmin: false }, authState)
  expect(redirect).toBeNull()
})

it('未认证用户访问 /dashboard/models 重定向到 /login', () => {
  const authState: MockAuthState = {
    isAuthenticated: false,
    isAdmin: false,
    isSimpleMode: false,
    backendModeEnabled: false,
    hasPendingAuthSession: false,
  }
  const redirect = simulateGuard('/dashboard/models', { requiresAuth: true, requiresAdmin: false }, authState)
  expect(redirect).toBe('/login')
})
```

- [ ] **Step 2: Run guard tests and verify the source route is missing**

Run from `frontend/`:

```bash
pnpm test:run src/router/__tests__/guards.spec.ts
```

Expected: the new pure guard tests pass, but route source coverage is not present yet. Continue with route creation.

- [ ] **Step 3: Create public page**

Create `frontend/src/views/public/ModelsView.vue`:

```vue
<template>
  <div class="linear-landing min-h-screen bg-linear-canvas text-linear-ink selection:bg-primary-500/30 selection:text-primary-900 dark:selection:text-primary-100">
    <header class="sticky top-0 z-20 border-b border-linear-hairline bg-linear-canvas/90 backdrop-blur-xl">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-6 px-4 py-3 sm:px-6 lg:px-8">
        <router-link to="/home" class="group flex items-center gap-3" :aria-label="siteName">
          <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-linear-hairline transition-colors group-hover:ring-linear-hairline-strong">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span class="leading-tight">
            <span class="block text-sm font-semibold text-linear-ink">
              <LinxWordmark v-if="usesDefaultBrand" />
              <span v-else>{{ siteName }}</span>
            </span>
            <span class="block text-[10px] font-medium uppercase tracking-[0.22em] text-linear-ink-tertiary">{{ siteSubtitle }}</span>
          </span>
        </router-link>

        <div class="ml-auto flex items-center gap-2 sm:gap-3">
          <div class="hidden items-center gap-6 text-sm font-medium text-linear-ink-subtle md:flex">
            <router-link to="/models" class="text-linear-ink">{{ t('nav.modelMarketplace') }}</router-link>
            <router-link to="/home#pricing" class="transition-colors hover:text-linear-ink">{{ t('modelCatalog.pricingNav') }}</router-link>
            <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="transition-colors hover:text-linear-ink">{{ t('home.docs') }}</a>
          </div>
          <LocaleSwitcher />
          <button class="ui-theme-toggle" :title="isDark ? t('home.switchToLight') : t('home.switchToDark')" @click="toggleTheme">
            <Icon v-if="isDark" name="sun" size="md" class="ui-theme-icon-accent" />
            <Icon v-else name="moon" size="md" class="ui-theme-icon-accent" />
          </button>
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="inline-flex h-10 items-center justify-center rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-400">
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <ModelCatalog />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import LinxWordmark from '@/components/common/LinxWordmark.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelCatalog from '@/components/models/ModelCatalog.vue'
import { useAppStore, useAuthStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))
const DEFAULT_SITE_NAME = 'LINX2.AI'
const brandIconUrl = '/linx2-icon.png'

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || DEFAULT_SITE_NAME)
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI Gateway Platform')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const brandLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || brandIconUrl)
const usesDefaultBrand = computed(() => siteName.value.trim().toUpperCase() === DEFAULT_SITE_NAME)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')

function toggleTheme() {
  document.documentElement.classList.toggle('dark')
  isDark.value = document.documentElement.classList.contains('dark')
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

onMounted(() => {
  if (!appStore.publicSettingsLoaded) {
    void appStore.fetchPublicSettings()
  }
  if (!authStore.user) {
    void authStore.checkAuth()
  }
})
</script>
```

- [ ] **Step 4: Create authenticated user page**

Create `frontend/src/views/user/ModelCatalogView.vue`:

```vue
<template>
  <AppLayout>
    <div class="linear-model-catalog-page space-y-5">
      <ModelCatalog />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import ModelCatalog from '@/components/models/ModelCatalog.vue'
</script>
```

- [ ] **Step 5: Register routes**

Add the public route after `/home` in `frontend/src/router/index.ts`:

```ts
  {
    path: '/models',
    name: 'PublicModels',
    component: () => import('@/views/public/ModelsView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Models',
      titleKey: 'modelCatalog.title'
    }
  },
```

Add the authenticated route after `/dashboard`:

```ts
  {
    path: '/dashboard/models',
    name: 'ModelMarketplace',
    component: () => import('@/views/user/ModelCatalogView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Model Marketplace',
      titleKey: 'modelCatalog.title',
      descriptionKey: 'modelCatalog.description'
    }
  },
```

- [ ] **Step 6: Extend Linear source contract**

Modify `frontend/src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts`:

```ts
const modelCatalogSource = readFileSync(resolve(userDir, 'ModelCatalogView.vue'), 'utf8')
```

Add the assertion in the first test:

```ts
expect(modelCatalogSource).toContain('linear-model-catalog-page')
```

- [ ] **Step 7: Run route and source tests**

Run from `frontend/`:

```bash
pnpm test:run src/router/__tests__/guards.spec.ts src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts
```

Expected: PASS.

- [ ] **Step 8: Commit routes and pages**

```bash
git add frontend/src/views/public/ModelsView.vue frontend/src/views/user/ModelCatalogView.vue frontend/src/router/index.ts frontend/src/router/__tests__/guards.spec.ts frontend/src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts
git commit -m "feat: add model marketplace routes"
```

## Task 6: Navigation and I18n

**Files:**
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`

- [ ] **Step 1: Add failing navigation source assertions**

Add to `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`:

```ts
describe('AppSidebar model marketplace navigation', () => {
  it('exposes the authenticated model marketplace route to regular user navigation', () => {
    expect(componentSource).toContain("path: '/dashboard/models'")
    expect(componentSource).toContain("t('nav.modelMarketplace')")
  })
})
```

Add to `frontend/src/views/__tests__/HomeView.spec.ts` in the default landing test:

```ts
expect(text).toContain('模型广场')
```

Add the mocked message used by the new header link near the existing `messages` object entries:

```ts
'nav.modelMarketplace': '模型广场',
```

- [ ] **Step 2: Run targeted frontend tests and verify they fail**

Run from `frontend/`:

```bash
pnpm test:run src/components/layout/__tests__/AppSidebar.spec.ts src/views/__tests__/HomeView.spec.ts
```

Expected: FAIL because the navigation labels are not wired yet.

- [ ] **Step 3: Add i18n keys**

In `frontend/src/i18n/locales/zh.ts`, add under `nav`:

```ts
modelMarketplace: '模型广场',
```

Add a top-level `modelCatalog` object:

```ts
modelCatalog: {
  title: '模型广场',
  kicker: '官方模型目录',
  pricingNav: '定价',
  description: '按服务商、模态和价格确认状态浏览可用模型。价格仅展示官方上游公开价格，未确认的模型保留展示并标记为待确认。',
  searchPlaceholder: '搜索模型、服务商或能力',
  providerFilter: '服务商',
  modalityFilter: '模态',
  sort: '排序',
  allProviders: '全部服务商',
  allModalities: '全部模态',
  confirmed: '官方价格',
  pending: '待确认',
  input: '输入',
  output: '输出',
  cacheRead: '缓存读取',
  perMillionTokens: '每百万 Token',
  source: '官方来源',
  empty: '没有匹配的模型',
  loadError: '模型目录加载失败',
  stats: {
    models: '模型',
    providers: '服务商',
    confirmed: '已确认价格',
    image: '图像模型',
  },
  modalities: {
    text: '文本',
    image: '图像',
    audio: '音频',
    video: '视频',
  },
  sortOptions: {
    default: '推荐顺序',
    newest: '最近更新',
    provider: '服务商',
    status: '价格状态',
  },
},
```

In `frontend/src/i18n/locales/en.ts`, add under `nav`:

```ts
modelMarketplace: 'Models',
```

Add a matching top-level `modelCatalog` object:

```ts
modelCatalog: {
  title: 'Model Marketplace',
  kicker: 'Official model catalog',
  pricingNav: 'Pricing',
  description: 'Browse available models by provider, modality, and price confirmation status. Prices show official upstream public pricing only; unmatched rows remain visible as pending confirmation.',
  searchPlaceholder: 'Search models, providers, or capabilities',
  providerFilter: 'Provider',
  modalityFilter: 'Modality',
  sort: 'Sort',
  allProviders: 'All providers',
  allModalities: 'All modalities',
  confirmed: 'Official price',
  pending: 'Pending confirmation',
  input: 'Input',
  output: 'Output',
  cacheRead: 'Cache read',
  perMillionTokens: 'per 1M tokens',
  source: 'Official source',
  empty: 'No models found',
  loadError: 'Failed to load model catalog',
  stats: {
    models: 'Models',
    providers: 'Providers',
    confirmed: 'Confirmed prices',
    image: 'Image models',
  },
  modalities: {
    text: 'Text',
    image: 'Image',
    audio: 'Audio',
    video: 'Video',
  },
  sortOptions: {
    default: 'Recommended',
    newest: 'Newest',
    provider: 'Provider',
    status: 'Price status',
  },
},
```

- [ ] **Step 4: Add public header link**

Modify the desktop nav block in `frontend/src/views/HomeView.vue`:

```vue
<div class="hidden items-center gap-6 text-sm font-medium text-linear-ink-subtle md:flex">
  <a href="#capabilities" class="transition-colors hover:text-linear-ink">{{ copy.nav.capabilities }}</a>
  <router-link to="/models" class="transition-colors hover:text-linear-ink">{{ t('nav.modelMarketplace') }}</router-link>
  <a href="#pricing" class="transition-colors hover:text-linear-ink">{{ copy.nav.pricing }}</a>
  <a
    v-if="docUrl"
    :href="docUrl"
    target="_blank"
    rel="noopener noreferrer"
    class="transition-colors hover:text-linear-ink"
  >
    {{ t('home.docs') }}
  </a>
</div>
```

- [ ] **Step 5: Add authenticated sidebar item**

In `frontend/src/components/layout/AppSidebar.vue`, add a local icon near the other icon declarations:

```ts
const ModelCatalogIcon = {
  render: () =>
    h(
      'svg',
      { fill: 'none', viewBox: '0 0 24 24', stroke: 'currentColor', 'stroke-width': '1.5' },
      [
        h('path', {
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          d: 'M3.75 5.25h6v6h-6v-6zM14.25 5.25h6v6h-6v-6zM3.75 14.25h6v6h-6v-6zM14.25 14.25h6v6h-6v-6z',
        }),
      ],
    ),
}
```

Add the nav item in `buildSelfNavItems`, directly after the dashboard item and before `/keys`:

```ts
{ path: '/dashboard/models', label: t('nav.modelMarketplace'), icon: ModelCatalogIcon },
```

- [ ] **Step 6: Run navigation tests**

Run from `frontend/`:

```bash
pnpm test:run src/components/layout/__tests__/AppSidebar.spec.ts src/views/__tests__/HomeView.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit navigation and i18n**

```bash
git add frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts
git commit -m "feat: add model marketplace navigation"
```

## Task 7: Full Verification and Visual QA

**Files:**
- No planned source edits. Only verification output and fixes if a command reveals a defect.

- [ ] **Step 1: Run backend handler tests**

Run from `backend/`:

```bash
go test -tags=unit ./internal/handler -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted frontend tests**

Run from `frontend/`:

```bash
pnpm test:run src/utils/__tests__/modelCatalog.spec.ts src/components/models/__tests__/ModelCatalog.spec.ts src/router/__tests__/guards.spec.ts src/views/user/__tests__/SecondaryUserPagesLinearSource.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/views/__tests__/HomeView.spec.ts
```

Expected: PASS.

- [ ] **Step 3: Run frontend typecheck**

Run from `frontend/`:

```bash
pnpm typecheck
```

Expected: PASS.

- [ ] **Step 4: Run frontend production build**

Run from `frontend/`:

```bash
pnpm build
```

Expected: PASS.

- [ ] **Step 5: Start frontend server for browser QA**

Run from `frontend/`:

```bash
pnpm dev -- --host 0.0.0.0 --port 49622
```

Expected: Vite reports a local URL and a network URL. Use the network URL for Windows browser testing when `localhost` is not reachable from Windows.

- [ ] **Step 6: Verify public page in browser**

Open:

```text
http://127.0.0.1:49622/models
```

If Windows is outside the WSL network namespace, open the Vite network URL printed by Step 5.

Check:

```text
The page renders the public LINX2 shell.
The catalog grid is visible.
Search narrows cards.
Provider and modality selects narrow cards.
Official prices and pending confirmation labels render.
No horizontal overflow appears at mobile width.
```

- [ ] **Step 7: Verify authenticated page in browser**

Open after logging in:

```text
http://127.0.0.1:49622/dashboard/models
```

Check:

```text
The page renders inside AppLayout.
The sidebar includes Models / 模型广场.
The route is available to a normal authenticated user.
The route does not require admin permissions.
Catalog interactions match the public page.
```

- [ ] **Step 8: Stop the Vite server**

Use `Ctrl-C` in the terminal running Vite.

## Final Acceptance Criteria

- `GET /api/v1/settings/model-catalog` returns 43 non-Agnes, non-Doubao models.
- The backend response includes provider summaries, model metadata, official pricing where confirmed, and `price_status: "unverified"` for rows that must show pending confirmation.
- `/models` is public and renders the shared catalog in a public LINX2 landing shell.
- `/dashboard/models` is authenticated, non-admin, and renders the same shared catalog inside `AppLayout`.
- The homepage header links to `/models`.
- The authenticated sidebar links to `/dashboard/models`.
- Search, provider filter, modality filter, and sort controls work on representative data.
- Frontend tests, frontend typecheck, frontend build, and backend handler tests pass.
