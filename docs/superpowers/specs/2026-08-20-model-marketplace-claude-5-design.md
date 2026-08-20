# Claude 5 Model Marketplace Design

## Goal

Add Claude Opus 5 and Claude Fable 5 to the backend-owned model marketplace,
keep the Anthropic cards ordered by release date, and make the Web Chat model
selector display Fable 5 consistently with other Claude models.

Both `/models` and `/dashboard/models` consume the same backend catalog. Web
Chat enriches its dynamically available models from that catalog, then applies
a frontend formatter to the selected model and its options.

## Confirmed Metadata

| Model ID | Display name | Release date | Context window | Input / cache read / output per 1M tokens |
|---|---|---|---:|---:|
| `claude-opus-5` | Claude Opus 5 | 2026-07-24 | 1,000,000 | $5 / $0.50 / $25 |
| `claude-fable-5` | Claude Fable 5 | 2026-06-09 | 1,000,000 | $10 / $1 / $50 |

The release dates and input/output prices come from Anthropic's launch pages.
The context windows and 128K maximum output limits are confirmed by the AWS
Bedrock model cards. Cache-read prices match the repository's existing billing
data and Anthropic's standard 10% cache-read relationship for these models.

Sources:

- https://www.anthropic.com/news/claude-opus-5
- https://www.anthropic.com/news/claude-fable-5-mythos-5
- https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5.html
- https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5.html

## Catalog Representation

Add both models to `backend/internal/service/public_model_catalog.go` using the
existing Anthropic card conventions:

- Provider: Anthropic.
- Modality: text.
- Features: chat, reasoning, vision input, tool use, and prompt caching.
- Price and release status: confirmed.
- Context source: the model-specific AWS Bedrock model card.
- Pricing source: the corresponding Anthropic launch page.

Descriptions will stay factual and concise. Opus 5 is the newest Opus model for
long-running agent, coding, and professional work. Fable 5 is the higher-tier
model for complex knowledge work, coding, and sustained autonomous tasks.

Advance `PublicModelCatalogUpdatedAt` to `2026-08-20` because the static catalog
data changes on that date.

## Ordering

Do not change the sorting algorithm. The backend already sorts by provider,
then `released_at` descending, then display name. The frontend marketplace and
Web Chat apply the same release-date-first semantics.

Within Anthropic, the relevant order will therefore place Opus 5 first, while
Fable 5 will appear according to its June 9 release date rather than being
manually promoted based on capability tier.

## Fable 5 Display Fix

The current mismatch has two causes:

1. Fable 5 is absent from the static public catalog, so the dynamic Web Chat
   catalog cannot enrich it and falls back to `display_name = claude-fable-5`.
2. `formatWebChatModelName` recognizes only the Claude Opus, Sonnet, and Haiku
   families. It therefore returns the machine-like fallback unchanged for
   Fable.

Adding the catalog row supplies `Claude Fable 5` in the normal path. Expanding
the formatter's Claude family matcher to include `fable` makes the display
robust when catalog enrichment is unavailable or stale. Routing model IDs stay
unchanged.

## Testing

Follow red-green-refactor:

1. Add backend catalog tests for both cards, all confirmed metadata, and their
   release-date positions in the Anthropic model list.
2. Add a dynamic Web Chat catalog test proving Fable 5 receives the human
   display name and release metadata from the public catalog.
3. Add frontend formatter coverage proving `claude-fable-5` and its thinking
   variant render as `Claude Fable 5`, including a machine-like fallback
   display name.
4. Implement only the catalog rows, model-specific source constants, and
   formatter family expansion needed to pass those tests.
5. Run focused backend and frontend tests, then the broader affected package
   checks and repository diff checks.

## Out of Scope

- Changing gateway routing, account model mappings, billing behavior, or model
  availability.
- Adding Claude Mythos 5 to the marketplace.
- Changing the marketplace card layout or general sorting behavior.
- Displaying maximum output tokens on cards; the current catalog schema has no
  such field.
