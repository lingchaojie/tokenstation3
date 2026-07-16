# Web Chat Model Title Usage Design

## Goal

Improve Web Chat model ordering, conversation rename UX, automatic title generation, and usage-record display values without changing unrelated table layouts or exposing hidden Web Chat API key secrets.

## Scope

- Model dropdown should show newer models before older models within each provider.
- Conversation rename editing should happen inline only in the left conversation list.
- Saved rename changes should update the shared conversation title so the top title changes too.
- Automatic title generation should run once after the first completed assistant response, using the current conversation model.
- Title generation should be charged and recorded as ordinary Web Chat usage, without a special "title generation" label.
- Web Chat usage records should populate existing fields where the value is known, especially API Key and reasoning effort.

## Model Ordering

The backend Web Chat model capability DTO will include release-date metadata sourced from the existing public model catalog. Dynamic account-derived models that match catalog entries inherit the catalog release date. Models without a known release date sort after dated models within the same provider and then by display name/model name for deterministic output.

Provider grouping remains unchanged in the UI. The model selector receives backend-sorted models and also applies the same stable sort defensively for the selected provider.

## Inline Rename UX

Only the left conversation list exposes the edit control. Clicking edit turns that row title into a compact input with a confirm check button and a cancel button. Keyboard behavior remains efficient: Enter saves and Escape cancels. Blur saves only when focus leaves the entire edit control, so clicking the check button does not race with blur.

The store continues using the existing conversation update endpoint. Because the current conversation and list rows share store state, saving the new title updates both the left list and top title.

## Automatic Title

When a new conversation is created from the first user message, the existing initial title remains the trimmed first-message fallback. After the first assistant response reaches `completed`, the frontend requests server-side auto-title generation once.

The server uses the same model/provider selected for that conversation. It sends a short title-generation prompt through the existing Web Chat dispatch/billing path using the hidden Web Chat API key, so usage and balance behavior match normal chat calls. Usage records must not include a special title-generation description or new UI field.

Manual titles are protected. The backend updates the title only when the current title still equals the initial first-message fallback or is blank; if the user has renamed the conversation before the title response returns, the generated title is discarded.

If title generation fails, the current title remains unchanged and the chat response is not affected.

## Usage Records

No new usage-table columns are added. Existing DTO values are improved:

- For Web Chat hidden API keys, usage-log DTOs return a sanitized API key summary with name `Web Chat` and without the secret key value, so existing API Key cells do not show `-`.
- For Web Chat requests whose model supports thinking but deep thinking is off, usage logging records the default visible effort `medium` without sending that value upstream.
- For Web Chat requests with deep thinking on, usage logging records the actual normalized effort sent upstream, which is already the highest supported tier.
- For on/off-only thinking models, enabled thinking records `high`, matching existing gateway semantics.
- For models that do not support thinking, `reasoning_effort` remains empty.

## Testing

- Backend unit tests cover release-date propagation/sorting, sanitized Web Chat API key usage DTOs, and Web Chat usage reasoning-effort normalization.
- Frontend tests cover left-list inline rename controls and provider model sorting.
- Auto-title tests cover first-response trigger, manual-title protection, and failure leaving the fallback title unchanged.
