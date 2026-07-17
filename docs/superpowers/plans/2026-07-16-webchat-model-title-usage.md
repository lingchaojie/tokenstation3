# Web Chat Model Title Usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve Web Chat model ordering, inline conversation rename, automatic title generation, and usage-record values.

**Architecture:** Reuse existing Web Chat service, repository, DTO, and Pinia store boundaries. Add metadata and normalization at backend service/DTO edges, then keep frontend changes scoped to the chat store and conversation rail.

**Tech Stack:** Go backend with Ent repositories and service unit tests; Vue 3/Pinia frontend with Vitest component/store tests.

## Global Constraints

- Do not change production directly.
- Preserve existing dirty worktree changes outside files touched by this plan.
- Do not add usage-table columns for this task.
- Do not expose hidden Web Chat API key secret values.
- Auto-title generation must use the current conversation model and ordinary Web Chat usage accounting.

---

### Task 1: Backend Model Release Metadata and Sorting

**Files:**
- Modify: `backend/internal/service/web_chat_capabilities.go`
- Modify: `backend/internal/service/web_chat_catalog_dynamic.go`
- Test: `backend/internal/service/web_chat_capabilities_test.go`
- Test: `backend/internal/service/web_chat_catalog_dynamic_test.go`

**Interfaces:**
- Produces: `WebChatModelCapability.ReleasedAt string json:"released_at,omitempty"`
- Produces: Web Chat model lists sorted provider asc, known release date desc, display/model asc.

- [ ] Add failing service tests proving release dates flow from the public catalog into Web Chat capabilities and dynamic catalog sorting places newer known models before older/unknown models within a provider.
- [ ] Run `cd backend && go test ./internal/service -run 'Test(WebChatModelCapabilities|DynamicWebChatCatalog)'` and confirm the new tests fail for missing `ReleasedAt` or wrong order.
- [ ] Add `ReleasedAt` to `WebChatCatalogModel` and `WebChatModelCapability`; populate it from `PublicModelCatalogModelsForWebChat()`.
- [ ] Update dynamic sorting to compare provider, known `ReleasedAt` desc, display name, model name.
- [ ] Re-run the focused backend service tests and confirm they pass.

### Task 2: Usage DTO and Web Chat Reasoning Effort Values

**Files:**
- Modify: `backend/internal/handler/dto/mappers.go`
- Modify: `backend/internal/handler/dto/mappers_test.go`
- Modify: `backend/internal/service/web_chat_dispatch.go`
- Modify: `backend/internal/service/web_chat_service_test.go`

**Interfaces:**
- Produces: sanitized usage DTO API key summary for `APIKeyTypeWebChat` with `Name: "Web Chat"` and no secret key.
- Produces: Web Chat usage result reasoning-effort normalization before `RecordUsage`.

- [ ] Add failing DTO mapper tests showing Web Chat usage logs return an API key object named `Web Chat` without exposing `Key`.
- [ ] Add failing Web Chat service tests for usage reasoning effort: default `medium` when thinking-capable but disabled, highest actual effort when enabled, empty when unsupported.
- [ ] Run focused backend tests and confirm failures.
- [ ] Implement sanitized Web Chat API key DTO helper.
- [ ] Implement Web Chat usage reasoning-effort normalization before calling OpenAI/gateway usage recording.
- [ ] Re-run focused backend tests and confirm they pass.

### Task 3: Auto Title Endpoint and Store Trigger

**Files:**
- Modify: `backend/internal/service/web_chat_service.go`
- Modify: `backend/internal/repository/web_chat_repo.go`
- Modify: `backend/internal/handler/chat_handler.go`
- Modify: `backend/internal/router/router.go`
- Modify: `frontend/src/api/chat.ts`
- Modify: `frontend/src/stores/chat.ts`
- Test: existing backend Web Chat service/handler tests
- Test: existing frontend chat store tests if present, otherwise add focused tests beside store tests.

**Interfaces:**
- Produces: `POST /api/v1/chat/conversations/:id/title/generate`
- Consumes: current conversation model/provider and existing first-message fallback title.
- Produces: updated conversation title only when manual rename has not happened.

- [ ] Add failing backend tests for generating a title after first assistant completion and skipping overwrite after manual rename.
- [ ] Add failing frontend store test that triggers title generation once after the first completed assistant response.
- [ ] Run focused backend/frontend tests and confirm failures.
- [ ] Implement backend service method using the current model/provider and existing Web Chat accounting path.
- [ ] Add route/handler and frontend API wrapper.
- [ ] Trigger once from `sendMessage()` after assistant stream completion; ignore generation failures.
- [ ] Re-run focused tests and confirm they pass.

### Task 4: Left Conversation Rail Inline Rename

**Files:**
- Modify: `frontend/src/components/chat/ConversationRail.vue`
- Modify: `frontend/src/i18n/locales/en/webchat.ts`
- Modify: `frontend/src/i18n/locales/zh/webchat.ts`
- Test: frontend component tests for `ConversationRail` if present, otherwise add focused test.

**Interfaces:**
- Consumes: existing `chatStore.renameConversation(conversationId, title)`.
- Produces: inline input with check save button, cancel button, Enter save, Escape cancel.

- [ ] Add failing component tests for edit click showing input/check/cancel and save updating the store.
- [ ] Run the focused frontend test and confirm failure.
- [ ] Replace `window.prompt` flow with row-local edit state and buttons.
- [ ] Keep edit entry only in the left rail; rely on store title update for the top title.
- [ ] Re-run focused frontend tests and confirm they pass.

### Task 5: Frontend Model Sorting

**Files:**
- Modify: `frontend/src/api/chat.ts`
- Modify: `frontend/src/components/chat/Composer.vue`
- Test: frontend tests for model sorting if present, otherwise add utility/component test.

**Interfaces:**
- Consumes: `WebChatModel.released_at?: string`
- Produces: selected-provider model options sorted known release date desc, then display/model asc.

- [ ] Add failing frontend test for model options sorting by `released_at`.
- [ ] Run focused frontend test and confirm failure.
- [ ] Add `released_at` to the frontend type and sort model options defensively.
- [ ] Re-run focused frontend test and confirm it passes.

### Task 6: Final Verification

**Files:**
- No new files unless test commands reveal required fixes.

- [ ] Run backend focused tests covering modified Web Chat, DTO, and handler code.
- [ ] Run frontend focused tests covering chat store/components.
- [ ] Run broader backend/frontend test or build commands if focused tests reveal integration risk.
- [ ] Inspect `git diff --stat` and `git diff` to confirm no unrelated changes were included.
