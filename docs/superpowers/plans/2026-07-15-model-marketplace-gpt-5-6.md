# GPT-5.6 Model Marketplace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three supported GPT-5.6 variants to the public model marketplace with confirmed metadata and release-date ordering.

**Architecture:** Extend the existing backend-owned static catalog; both marketplace routes already consume this API and need no frontend production changes. Preserve the current provider/date/name sorting function and prove the new rows sort before GPT-5.5 through a backend regression test.

**Tech Stack:** Go 1.24, Testify, Vue 3, TypeScript 5.6, Vitest, graphify.

## Global Constraints

- Work only in `/home/alvin/tokenstation3/.worktrees/model-marketplace-gpt-5-6` on `feature/model-marketplace-gpt-5-6`, which is based on `dev` at `3ef9d9c8c`.
- Add exactly `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna`; do not add the generic `gpt-5.6` alias.
- Use `2026-07-09` as the confirmed release date for all three variants.
- Use a 1,050,000-token context window for all three variants.
- Keep the existing provider rank, release-date descending, display-name tie-break sorting behavior unchanged.
- Do not change gateway routing, aliases, billing, model availability, frontend filters, cards, or frontend sorting.
- Use the existing OpenAI pricing and context source constants.
- Follow red-green-refactor and preserve the observed failing-test output before changing production code.

---

### Task 1: Add and Verify the GPT-5.6 Catalog Entries

**Files:**
- Modify: `backend/internal/service/public_model_catalog_test.go:62`
- Modify: `backend/internal/service/public_model_catalog.go:8,123-156,183-196`

**Interfaces:**
- Consumes: `PublicModelCatalogModelsForWebChat() []PublicModelCatalogModel`, `catalogModel(...) PublicModelCatalogModel`, `textModalities() []string`, `textFeatures(...string) []string`, and `usdWithCache(input, output, cacheRead float64) PublicModelCatalogPricing`.
- Produces: three additional `PublicModelCatalogModel` rows returned by `PublicModelCatalogModelsForWebChat()`; no new exported API or type.

- [ ] **Step 1: Write the failing backend catalog test**

Append this test to `backend/internal/service/public_model_catalog_test.go`:

```go
func TestPublicModelCatalog_IncludesGPT56VariantsInReleaseOrder(t *testing.T) {
	type expectation struct {
		displayName string
		input       float64
		cacheRead   float64
		output      float64
	}

	expected := map[string]expectation{
		"gpt-5.6-sol":   {displayName: "GPT-5.6 Sol", input: 5, cacheRead: 0.5, output: 30},
		"gpt-5.6-terra": {displayName: "GPT-5.6 Terra", input: 2.5, cacheRead: 0.25, output: 15},
		"gpt-5.6-luna":  {displayName: "GPT-5.6 Luna", input: 1, cacheRead: 0.1, output: 6},
	}

	models := PublicModelCatalogModelsForWebChat()
	found := make(map[string]struct{}, len(expected))
	openAIModelNames := make([]string, 0)
	for idx := range models {
		model := &models[idx]
		if model.Provider != "openai" {
			continue
		}
		openAIModelNames = append(openAIModelNames, model.ModelName)
		want, ok := expected[model.ModelName]
		if !ok {
			continue
		}

		found[model.ModelName] = struct{}{}
		require.Equal(t, "OpenAI", model.ProviderName)
		require.Equal(t, want.displayName, model.DisplayName)
		require.Equal(t, []string{"text"}, model.Modalities)
		require.ElementsMatch(t, []string{"chat", "reasoning", "vision input", "tool use", "prompt caching"}, model.Features)
		require.Equal(t, "2026-07-09", model.ReleasedAt)
		require.Equal(t, "confirmed", model.ReleaseStatus)
		require.Equal(t, "2026-07-15", model.UpdatedAt)
		require.Equal(t, 1_050_000, model.ContextWindow)
		require.Equal(t, sourceOpenAI, model.SourceURL)
		require.Equal(t, contextSourceOpenAI, model.ContextSourceURL)
		require.Equal(t, "confirmed", model.PriceStatus)
		require.NotNil(t, model.Pricing.InputPerMillion)
		require.NotNil(t, model.Pricing.CacheReadPerMillion)
		require.NotNil(t, model.Pricing.OutputPerMillion)
		require.Equal(t, want.input, *model.Pricing.InputPerMillion)
		require.Equal(t, want.cacheRead, *model.Pricing.CacheReadPerMillion)
		require.Equal(t, want.output, *model.Pricing.OutputPerMillion)
	}

	require.Len(t, found, len(expected))
	require.GreaterOrEqual(t, len(openAIModelNames), 4)
	require.Equal(t, []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.5"}, openAIModelNames[:4])
}
```

- [ ] **Step 2: Run the new test and verify RED**

Run:

```bash
cd backend
go test ./internal/service -run '^TestPublicModelCatalog_IncludesGPT56VariantsInReleaseOrder$' -count=1
```

Expected: FAIL at `require.Len(t, found, len(expected))` because the catalog contains zero of the three expected GPT-5.6 model IDs. Save this output before continuing.

- [ ] **Step 3: Add the minimal release metadata and catalog rows**

In `backend/internal/service/public_model_catalog.go`, change the catalog timestamp to:

```go
const PublicModelCatalogUpdatedAt = "2026-07-15"
```

Insert these rows immediately before the existing `gpt-5.5` release metadata:

```go
	"gpt-5.6-sol":             {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
	"gpt-5.6-terra":           {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
	"gpt-5.6-luna":            {ReleasedAt: "2026-07-09", ReleaseStatus: "confirmed"},
```

Insert these catalog rows immediately before the existing `gpt-5.5` row:

```go
	catalogModel("openai", "OpenAI", "gpt-5.6-sol", "GPT-5.6 Sol", textModalities(), "Highest-capability GPT-5.6 tier for complex reasoning, coding, and long-context agent workflows.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(5, 30, 0.5), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.6-terra", "GPT-5.6 Terra", textModalities(), "Balanced GPT-5.6 tier for production reasoning, coding, and agent workloads.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(2.5, 15, 0.25), "confirmed", sourceOpenAI),
	catalogModel("openai", "OpenAI", "gpt-5.6-luna", "GPT-5.6 Luna", textModalities(), "Lower-cost GPT-5.6 tier for efficient reasoning, coding, and high-throughput agent workloads.", 1050000, contextSourceOpenAI, textFeatures("vision input", "tool use", "prompt caching"), usdWithCache(1, 6, 0.1), "confirmed", sourceOpenAI),
```

Format both changed Go files:

```bash
gofmt -w backend/internal/service/public_model_catalog.go backend/internal/service/public_model_catalog_test.go
```

- [ ] **Step 4: Run the new test and verify GREEN**

Run:

```bash
cd backend
go test ./internal/service -run '^TestPublicModelCatalog_IncludesGPT56VariantsInReleaseOrder$' -count=1
```

Expected: PASS with `ok github.com/Wei-Shaw/sub2api/internal/service`.

- [ ] **Step 5: Run the complete backend catalog test group**

Run:

```bash
cd backend
go test ./internal/service -run '^TestPublicModelCatalog_' -count=1
```

Expected: PASS with all existing and new public catalog tests green.

- [ ] **Step 6: Verify the unchanged frontend catalog consumers**

Run:

```bash
cd frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/utils/__tests__/modelCatalog.spec.ts src/components/models/__tests__/ModelCatalog.spec.ts
```

Expected: 2 test files and 11 tests pass. The existing stale Browserslist-data notice is acceptable; no test failure is acceptable.

- [ ] **Step 7: Run the broader changed backend package**

Run:

```bash
cd backend
go test ./internal/service -count=1
```

Expected: PASS with `ok github.com/Wei-Shaw/sub2api/internal/service`.

- [ ] **Step 8: Review the implementation diff**

Run:

```bash
git diff --check
git diff -- backend/internal/service/public_model_catalog.go backend/internal/service/public_model_catalog_test.go
git status --short
```

Expected: no whitespace errors; only the two intended backend files are modified beyond the already committed plan/spec documentation.

- [ ] **Step 9: Commit the tested catalog change**

Run:

```bash
git add backend/internal/service/public_model_catalog.go backend/internal/service/public_model_catalog_test.go
git commit -m "feat: add GPT-5.6 models to marketplace"
```

Expected: one commit containing only the catalog data and its regression test.

### Task 2: Refresh the Knowledge Graph and Run Final Verification

**Files:**
- Modify: `graphify-out/graph.json`
- Modify: other tracked `graphify-out/` outputs selected by `graphify update .`

**Interfaces:**
- Consumes: the committed Go catalog and test from Task 1.
- Produces: refreshed graphify outputs that represent the new GPT-5.6 marketplace nodes and relationships; no runtime API changes.

- [ ] **Step 1: Refresh graphify after the code change**

From the worktree root, run:

```bash
graphify update .
```

Expected: the incremental update completes successfully and refreshes tracked files under `graphify-out/`.

- [ ] **Step 2: Review and commit only the graph refresh**

Run:

```bash
git status --short
git diff --check
git diff --stat -- graphify-out
git add graphify-out
git commit -m "chore: refresh graph for GPT-5.6 marketplace"
```

Expected: the commit contains only generated graphify outputs; production and test files remain in the Task 1 commit.

- [ ] **Step 3: Run fresh final verification**

Run:

```bash
cd backend
go test ./internal/service -run '^TestPublicModelCatalog_' -count=1
cd ../frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/utils/__tests__/modelCatalog.spec.ts src/components/models/__tests__/ModelCatalog.spec.ts
cd ..
git diff --check
git status --short --branch
git log --oneline dev..HEAD
```

Expected: backend catalog tests pass; 2 frontend files and 11 tests pass; the worktree is clean on `feature/model-marketplace-gpt-5-6`; the log shows the design, plan, implementation, and graph refresh commits.
