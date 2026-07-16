# GPT-5.6 Model Marketplace Design

## Goal

Add the three GPT-5.6 variants already supported by the gateway to the public
model marketplace. Both `/models` and `/dashboard/models` consume the same
backend catalog, so the change must appear consistently in both views without
frontend-only fallback data.

## Scope

The catalog will add these OpenAI models:

| Model ID | Display name | Release date | Context window | Input / cache read / output per 1M tokens |
|---|---|---|---:|---:|
| `gpt-5.6-sol` | GPT-5.6 Sol | 2026-07-09 | 1,050,000 | $5 / $0.5 / $30 |
| `gpt-5.6-terra` | GPT-5.6 Terra | 2026-07-09 | 1,050,000 | $2.5 / $0.25 / $15 |
| `gpt-5.6-luna` | GPT-5.6 Luna | 2026-07-09 | 1,050,000 | $1 / $0.1 / $6 |

The release date was confirmed for this task. Pricing and context values come
from the repository's existing GPT-5.6 billing and model-pricing data.

## Catalog Representation

Each entry will use the existing OpenAI catalog conventions:

- Provider: OpenAI.
- Modality: text, matching the other OpenAI text-output models in the catalog.
- Features: chat, reasoning, vision input, tool use, and prompt caching.
- Price and release status: confirmed.
- Pricing source: the existing OpenAI source URL.
- Context source: the existing OpenAI model-documentation URL.

Descriptions will distinguish the variants without making unsupported
performance claims: Sol is the highest-capability tier, Terra is the balanced
tier, and Luna is the lower-cost tier.

`PublicModelCatalogUpdatedAt` will advance to `2026-07-15`, the date the catalog
data is changed.

## Ordering

The backend already sorts catalog rows by provider rank, then `released_at`
descending, then display name. The frontend applies the same ordering for its
default sort. The three GPT-5.6 entries therefore appear in the OpenAI section
ahead of GPT-5.5 because `2026-07-09` is newer than `2026-06-21`.

Because the three variants share a release date, the existing tie-breaker makes
their rendered order GPT-5.6 Luna, GPT-5.6 Sol, then GPT-5.6 Terra. No sorting
implementation change is needed.

## Implementation

Only `backend/internal/service/public_model_catalog.go` needs production-code
changes:

1. Register release metadata for the three model IDs.
2. Add the three catalog entries in the OpenAI group before the older GPT-5.5
   entry in source order.
3. Advance the catalog update timestamp.

The frontend remains API-driven and requires no production change.

## Testing

Development will follow red-green-refactor:

1. Add a backend catalog test that initially fails because the three entries
   are absent.
2. Assert model IDs, display names, release metadata, context window, features,
   source metadata, and all three pricing values.
3. Assert the sorted OpenAI result places all GPT-5.6 variants before GPT-5.5
   and preserves the established same-date name tie-breaker.
4. Add only the catalog data needed to pass the test.
5. Run the focused backend tests and the existing frontend catalog utility and
   component tests.
6. Run broader repository checks appropriate to the changed backend package.
7. Run `graphify update .` after the code change to refresh the knowledge graph.

## Out of Scope

- Changing gateway routing, aliases, billing, or model availability.
- Deriving marketplace entries dynamically from the pricing JSON.
- Changing frontend filters, cards, or sorting behavior.
- Adding the generic `gpt-5.6` alias as a fourth marketplace model.
