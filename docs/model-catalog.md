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
