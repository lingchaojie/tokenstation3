# Model Marketplace Design

Date: 2026-06-21
Branch/worktree: `worktree-model-marketplace` at `.claude/worktrees/model-marketplace`

## Objective

Build a model marketplace for LINX2.AI with two entry points:

- Public page: `/models`, accessible without login.
- Logged-in user page: `/dashboard/models`, visible to all authenticated users.

Both pages must show the same model catalog, filters, sorting, and model card content. The only difference is the shell: public landing-page chrome for `/models`, and app/sidebar chrome for `/dashboard/models`.

## Scope

The catalog is based on `https://openllm.shop/models` as a reference model list, excluding Agnes and Doubao. The OpenLLM reference endpoint currently returns 49 models; after excluding Agnes and Doubao, the expected catalog size is about 43 models.

Prices must show official upstream prices only. Platform discount prices, OpenLLM discounted prices, and internal route multipliers must not be displayed. If a model cannot be matched to an official pricing source, keep the model in the catalog and display the price as `待确认` / `Pending confirmation`.

Official pricing sources checked during design:

- OpenAI API Pricing: https://openai.com/api/pricing/
- Anthropic Claude API Pricing: https://docs.anthropic.com/en/docs/about-claude/pricing
- Google Gemini API Pricing: https://ai.google.dev/gemini-api/docs/pricing
- Z.AI Pricing: https://docs.z.ai/guides/overview/pricing
- MiniMax Pay-as-you-go Pricing: https://platform.minimax.io/docs/guides/pricing-paygo
- Kimi Model Pricing: https://platform.kimi.ai/docs/pricing/chat

## Architecture

Add a backend public model catalog API, separate from the homepage's compact pricing table:

`GET /api/v1/settings/model-catalog`

The handler should return a curated model catalog DTO. It can reuse existing pricing service data where exact official-price matches are already present, but it should not expose internal platform discounts or channel-specific pricing.

The catalog data should include:

- `provider`: normalized provider key and display label.
- `model_name`: model identifier displayed to users.
- `display_name`: optional friendly name if different.
- `modalities`: text, image, audio, video.
- `description`: short user-facing description.
- `context_window`: max context when known.
- `features`: capability tags such as vision, reasoning, function calling, prompt caching.
- `pricing`: official input/output/cache/image price data when confirmed.
- `price_status`: `confirmed` or `unverified`.
- `source_url`: official pricing URL when confirmed.
- `updated_at`: catalog row update timestamp or source data timestamp.

The response should not include Agnes or Doubao models. Filtering should be defensive: remove rows whose provider key is `agnes` or `doubao`, and rows whose model name starts with or contains those excluded product names.

## Frontend

Create a shared `ModelCatalog` component and supporting data utilities. Both routes should render this component:

- `src/views/public/ModelsView.vue` for `/models`.
- `src/views/user/ModelCatalogView.vue` for `/dashboard/models`.
- Shared component under `src/components/models/` or similar.
- API wrapper in `src/api/settings.ts` or a new `src/api/modelCatalog.ts`, following existing frontend API patterns.

Public `/models` should reuse the current `HomeView.vue` visual language:

- `linear-landing`, `bg-linear-canvas`, `linx-panel`, `linx-panel-strong`.
- Public header with brand, locale switcher, theme toggle, login/dashboard CTA.
- Header navigation should include `模型广场` / `Models`.

Logged-in `/dashboard/models` should use `AppLayout` and the existing sidebar navigation. It should be available to all authenticated users, not only admins.

## Interaction Design

The shared catalog supports:

- Keyword search across model name, provider, and description.
- Provider filter: Anthropic, OpenAI, Gemini, Qwen, GLM, DeepSeek, MiniMax, Kimi.
- Modality filter: all, text, image, audio, video.
- Sorting: popular/default order, newest, provider, price confirmation status.
- Responsive cards: one column on mobile, two or three columns on desktop.

Each model card should show:

- Provider and model name.
- Modality chips.
- Capability tags.
- Context window when known.
- Description.
- Official price rows:
  - Text models: input, output, cache read when confirmed.
  - Image/per-use models: per-use or per-resolution rows when confirmed.
  - Unverified models: `待确认` / `Pending confirmation`.
- Source label or link for confirmed official prices.

Cards should fit existing LINX2/Linear UI. Do not copy OpenLLM's blue-green palette or card styling.

## Data Flow

1. Both pages request `GET /api/v1/settings/model-catalog`.
2. Frontend stores response in local component state; no global store is required unless the implementation finds meaningful reuse.
3. Search/filter/sort are frontend-local computed operations.
4. Empty, loading, and error states use existing `linx-panel` styling.

## Error Handling

If the model catalog API fails:

- Public page should show a non-blocking catalog error panel and keep the rest of the page usable.
- Logged-in page should show an error panel with a retry action.
- The error must not redirect users or require admin permissions.

If a single model row lacks pricing data:

- Keep the row visible.
- Show `待确认` / `Pending confirmation`.
- Do not infer prices from OpenLLM discounted fields.

## Testing

Follow test-first implementation.

Backend tests:

- The catalog endpoint returns a successful response with provider/model rows.
- Agnes and Doubao are excluded.
- Confirmed official-price rows expose official prices and source URLs.
- Unverified rows remain present and return `price_status = unverified`.

Frontend tests:

- `/models` is public and does not require auth.
- `/dashboard/models` requires authentication but not admin role.
- Shared catalog renders confirmed prices and `待确认` rows correctly.
- Search, provider filter, modality filter, and sorting work on representative fixture data.
- Public page includes the Models nav entry.
- User sidebar includes the model marketplace entry.

Verification:

- Run frontend unit tests and typecheck.
- Run relevant backend unit tests.
- Start the Vite dev server and verify public and logged-in routes render without blank screens or mobile overflow.

## Out of Scope

- Admin management UI for editing the model catalog.
- User-specific channel availability, group eligibility, balance, or quota overlays on model cards.
- Displaying platform discount prices or OpenLLM discounted prices.
- Live dependency on `openllm.shop` from the LINX2 frontend at runtime.
- Adding Agnes or Doubao in any entry point.

## Open Decisions Resolved

- Both public and logged-in pages are required.
- Logged-in page is visible to all authenticated users.
- Prices display official upstream prices only.
- Unofficial or unconfirmed prices display as `待确认`.
- Public route is `/models`; logged-in route is `/dashboard/models`.
