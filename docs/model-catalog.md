# Model Catalog

The model catalog is the user-facing model marketplace. It should present models at the level users choose from, not every backend route or parameter variant.

## Display Model Scope

- Show one card for each base model that users can understand as a distinct product.
- Do not show separate cards for parameter or capability variants of the same base model.
- Examples:
  - `claude-opus-4-7-max` is represented by `claude-opus-4-7`.
  - `claude-opus-4-6-thinking` is represented by `claude-opus-4-6`.
  - `claude-sonnet-4-5-20250929` is represented by `claude-sonnet-4-5`.
  - `gpt-image-2-count`, `gpt-image-2-hd-count`, and `gpt-image-2-4k-count` are represented by `gpt-image-2`.
  - `gemini-3.1-flash-image-count`, `gemini-3.1-flash-image-hd-count`, and `gemini-3.1-flash-image-4k-count` are represented by `gemini-3.1-flash-image`.
  - `gemini-3-pro-image-count`, `gemini-3-pro-image-hd-count`, and `gemini-3-pro-image-4k-count` are represented by `gemini-3-pro-image`.
  - `gemini-2.5-flash-image-count` is represented by `gemini-2.5-flash-image`.
- Variant-specific support, such as max routing, thinking mode, versioned routing, image size/count routes, special request parameters, or other backend-only capabilities, belongs in backend capability mapping rather than the public catalog card list.

## Data Quality

- Prices should use official provider pricing sources when available.
- Context window values must come from official provider documentation.
- If an official context window cannot be verified, omit `context_window` for that model instead of guessing.
- Release dates are used only for backend/default sorting and are not displayed to users.

## User Experience

- The public `/models` page and authenticated `/dashboard/models` page share the same user-facing catalog semantics.
- Users should not need to understand route-level variants before choosing a model.
- Web Chat reuses this catalog and applies a narrower provider/platform routing subset. See [Web Chat](web-chat.md) for the authenticated chat surface and billing behavior.

## October 22, 2026 Refresh

The static snapshot now includes Claude Opus 5.5, Sonnet 5.5, Haiku 5.5, GPT-6.1 Sol, GPT-6 Sol, GPT-6 Luna, GPT-Image-2.5 Flare, and GPT-Image-2.5 Sunburst.

- Claude metadata and cache pricing come from the official [Opus 5.5](https://platform.claude.com/docs/en/models/opus-5-5/overview), [Sonnet 5.5](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), and [Haiku 5.5](https://platform.claude.com/docs/en/models/haiku-5-5/overview) pages. Haiku's base rates apply through 100,000 input tokens; larger requests use the higher tier for the entire request.
- GPT context windows come from the official [GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol), [GPT-6 Sol](https://developers.openai.com/api/docs/models/gpt-6-sol), and [GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) pages. The [standard pricing table](https://developers.openai.com/api/docs/pricing) supplies token and cache rates. Above 272,000 input tokens, the entire request uses double input/cache rates and 1.5 times output rates.
- The [Flare](https://developers.openai.com/api/docs/models/gpt-image-2.5-flare) and [Sunburst](https://developers.openai.com/api/docs/models/gpt-image-2.5-sunburst) pages describe generation and editing. Their cards distinguish text input/cache, image input/cache, and image output token prices. No context window or fixed per-image price is assumed.
- OpenAI release dates are recorded in the official [API changelog](https://developers.openai.com/api/docs/changelog).

`/api/v1/settings/model-catalog` serves this versioned static snapshot; changing account model whitelists does not refresh it. The gateway's `/v1/models` list is separate and depends on the requesting key and available accounts. A catalog card describes a model, not a guarantee that every group can route to it.

### November 19, 2026 refresh

Added separate GPT-6.2 Sol and GPT-6.2 Astra cards and account mapping presets. Official API context is 1,050,000 tokens for both. Standard input/output/cache-read rates per million are $2/$10/$0.10 for Sol and $8/$40/$0.40 for Astra; generic cache writes are $2.50 and $10. TokenStation applies Fast at 2x and Ultra Fast at 6x on all dates, as approved for this rollout, rather than the upstream promotion. Existing models keep their prices.

API-key requests above 272,000 input tokens use 2x input/cache and 1.5x output rates. Actual ChatGPT credentials using these upstream models retain the subscription exemption. Existing administrator prices and the funding pipeline keep their precedence. No 30m/12h cache accounting was imported.

Sources: [Sol](https://developers.openai.com/api/docs/models/gpt-6.2-sol), [Astra](https://developers.openai.com/api/docs/models/gpt-6.2-astra), [pricing](https://developers.openai.com/api/docs/pricing). The November 17 release timestamp comes from the pinned upstream model list and remains marked unverified until independently confirmed.
