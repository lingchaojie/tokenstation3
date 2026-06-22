# Web Chat Design

Date: 2026-06-22
Branch/worktree: `worktree-model-marketplace` at `.claude/worktrees/model-marketplace`

## Objective

Build a logged-in web chat product for LINX2.AI so users can talk to supported models directly from the browser, upload files and images, download model-generated files, and be billed with the same subscription-first logic used by API Key traffic.

## Product Entry Points

The feature has two user-facing entry points:

- Homepage entry: the public homepage should add a prominent chat entry inspired by ChatGPT and Doubao web experiences. It is visible to visitors, but using it requires login. Unauthenticated users are redirected to login with a return path back to chat.
- Authenticated app entry: the user sidebar should add a `Chat` / `对话` item near the model marketplace entry. The authenticated route is `/chat`.

The chat page is a product surface, not a marketing page. After login, `/chat` should show the usable conversation interface immediately.

## Scope

In scope:

- Persistent conversation list and message history.
- Create, rename, delete, and continue conversations.
- Streamed assistant responses.
- Model selection from currently supported marketplace models.
- Mid-conversation model switching with capability checks.
- Text messages.
- Image uploads for models that support image input.
- General file uploads with platform-neutral storage.
- Model-generated file artifacts with authenticated download links.
- Usage logging and billing through the existing gateway billing path.
- User-visible error states for insufficient balance, quota exhaustion, unsupported attachment types, and upstream failures.

Out of scope for the first implementation:

- Team-shared conversations.
- Public share links.
- Admin prompt templates for Web Chat.
- Browser-side API Key entry.
- Full document parsing for every file format.
- Vendor-native file IDs as a durable cross-model abstraction.
- Voice input or audio output.

## Architecture

Add a Web Chat layer above the existing gateway services.

The Web Chat layer owns browser UX, conversation persistence, attachments, artifact downloads, model capability validation, and conversion from stored conversation history into provider-specific request payloads. It should not implement a separate billing ledger.

Each send request follows this shape:

1. The user sends a message to `POST /api/v1/chat/conversations/:id/messages`.
2. Web Chat persists the user message and attached file metadata.
3. Web Chat validates the selected model against the current conversation content.
4. Web Chat resolves the user, route group, subscription, and account selection inputs that the existing gateway path needs.
5. Web Chat converts the unified conversation history into the selected provider protocol.
6. Web Chat forwards the request through existing gateway service methods.
7. Web Chat persists the assistant message and any generated artifacts.
8. Existing `RecordUsage` logic writes `usage_logs` and applies billing.

This keeps account selection, channel pricing, group rate multipliers, subscription-first billing, balance fallback, API Key quota semantics, and usage log persistence aligned with current API Key behavior.

## Data Model

Add SQL migrations and Ent schemas for these tables.

`web_chat_conversations`:

- `id`
- `user_id`
- `title`
- `default_model`
- `default_provider`
- `last_model`
- `last_provider`
- `status`: `active`, `archived`, `deleted`
- `message_count`
- `last_message_at`
- `created_at`
- `updated_at`

`web_chat_messages`:

- `id`
- `conversation_id`
- `user_id`
- `role`: `user`, `assistant`, `system`
- `model`
- `provider`
- `content_text`
- `content_json`: provider-neutral message parts and display metadata
- `status`: `pending`, `streaming`, `completed`, `failed`, `canceled`
- `error_code`
- `error_message`
- `usage_log_id`
- `created_at`
- `updated_at`

`web_chat_attachments`:

- `id`
- `message_id`
- `conversation_id`
- `user_id`
- `kind`: `image`, `file`
- `filename`
- `content_type`
- `size_bytes`
- `storage_key`
- `sha256`
- `text_preview`
- `status`: `uploaded`, `processed`, `unsupported`, `deleted`
- `created_at`

`web_chat_artifacts`:

- `id`
- `message_id`
- `conversation_id`
- `user_id`
- `filename`
- `content_type`
- `size_bytes`
- `storage_key`
- `sha256`
- `source`: `model_output`, `image_output`, `generated_file`
- `created_at`

Storage should be abstracted behind a small service interface. The first implementation can store files under the configured data directory. The interface should support a future S3-backed implementation because the project already has S3 backup concepts.

## API Design

All chat APIs require JWT authentication and the standard user guard.

Conversation APIs:

- `GET /api/v1/chat/conversations`
- `POST /api/v1/chat/conversations`
- `GET /api/v1/chat/conversations/:id`
- `PATCH /api/v1/chat/conversations/:id`
- `DELETE /api/v1/chat/conversations/:id`

Message APIs:

- `POST /api/v1/chat/conversations/:id/messages`
- `POST /api/v1/chat/conversations/:id/messages/:message_id/cancel`

Attachment APIs:

- `POST /api/v1/chat/attachments`
- `GET /api/v1/chat/attachments/:id/download`
- `GET /api/v1/chat/artifacts/:id/download`

Capability API:

- `GET /api/v1/chat/models`

`GET /api/v1/chat/models` should derive its list from the same model catalog concepts used by the marketplace, but it must include Web Chat capability flags:

- `supports_text`
- `supports_image_input`
- `supports_file_context`
- `supports_artifact_output`
- `provider`
- `model`
- `display_name`
- `price_status`

## Billing Design

Web Chat must bill exactly like API Key traffic.

Rules:

- A Web Chat send request is billable only after the selected upstream request produces usage data or a billable image/file output event.
- Billing uses the selected model for that message turn.
- If the user has an active applicable subscription, subscription quota is attempted first.
- Existing balance fallback behavior applies unchanged.
- Existing channel model pricing, account rate multiplier, user group rate multiplier, image pricing, and long-context pricing apply unchanged.
- `usage_logs.inbound_endpoint` should be set to the Web Chat send endpoint, such as `/api/v1/chat/messages`.
- `usage_logs.upstream_endpoint` should still record the normalized upstream endpoint.
- `usage_logs.api_key_id` currently requires an API Key. The implementation will create hidden internal Web Chat API Key rows for attribution instead of making `usage_logs.api_key_id` nullable.

Internal Web Chat keys:

- Are created on demand per `user_id + group_id` so each billable turn has a concrete routed group.
- Use a reserved key type such as `web_chat`.
- Use unlimited API Key quota and rate windows because user-facing Web Chat limits are enforced through the same user, subscription, balance, group, and account controls as gateway traffic.
- Are never returned by user API Key list/detail APIs.
- Are never accepted by API Key authentication middleware. If a reserved `web_chat` key string is presented as a public credential, authentication must reject it.
- Are generated and managed server-side only.

The preferred implementation path is to avoid duplicating billing code while keeping usage log foreign keys valid.

## Model Switching

Model switching is allowed inside one conversation, but it is not vendor-native seamless context transfer.

The product behavior is:

- The conversation stores provider-neutral message history.
- Each new turn is sent to the currently selected model.
- Text history can be carried across providers.
- Image history can be carried only when the target model supports image input.
- General files are stored in LINX2 storage first. They are not represented as durable vendor-native `file_id` values.
- If the selected model cannot handle the current conversation context, the send action is blocked before billing and the UI explains the required action.

Unsupported cases should offer clear user choices:

- Continue with text-only context.
- Remove unsupported attachments from the next request.
- Start a new conversation with the selected model.

The first implementation should not silently drop attachments.

## Provider Adaptation

Web Chat stores one internal message representation:

- role
- text parts
- image attachment references
- file attachment references
- artifact references

Provider adapters convert this representation into protocol-specific payloads:

- OpenAI-compatible chat/responses payloads for OpenAI-compatible groups.
- Anthropic Messages payloads for Anthropic-compatible groups.
- Gemini GenerateContent payloads for Gemini-compatible groups.

The adapter layer also returns capability validation errors before dispatch. This keeps the Vue page simple and prevents frontend-only validation from becoming a security boundary.

## Frontend Design

The `/chat` page should follow a ChatGPT/Doubao-style app layout while fitting the existing LINX2 visual system:

- Left conversation rail with new chat, search, recent conversations, rename, and delete.
- Main conversation pane with messages, streamed assistant output, attachments, and artifact download chips.
- Top model selector with provider/model display and capability hints.
- Composer with multiline text input, image upload, file upload, send, stop, and disabled states.
- Empty state that immediately invites the user to start a conversation.

The homepage should add a chat-oriented first-viewport or near-first-viewport entry:

- Logged-out CTA: opens login and returns to `/chat`.
- Logged-in CTA: goes directly to `/chat`.
- Copy should describe browser chat as a logged-in product, not a public playground.

The sidebar should add `Chat` / `对话` before or next to `Models`.

## File And Image Handling

Uploads:

- Enforce max file size and allowed content types server-side.
- Store original filename, content type, size, and hash.
- Images can be converted to data URLs or provider-native image parts when needed.
- Text files can expose a bounded `text_preview` for prompt context.
- Unsupported files remain downloadable and visible but are not sent to the model as context.

Downloads:

- Attachment and artifact downloads require the owner user.
- Download handlers must not expose raw storage paths.
- Generated artifacts are tied to the assistant message that produced them.
- External vendor URLs should be fetched and stored server-side before exposing a user download, when feasible.

## Security And Abuse Controls

Required safeguards:

- JWT auth on every Web Chat endpoint.
- User ownership checks on every conversation, message, attachment, and artifact.
- Server-side file type and size validation.
- No browser-provided storage paths.
- No public download URLs in the first implementation.
- Reuse existing content moderation hooks where the gateway path already applies them.
- Pre-billing validation for unsupported model capabilities.
- Rate/concurrency enforcement using existing user and account concurrency controls.

## Error Handling

User-facing errors:

- Not logged in: redirect to login with return path.
- No available model/account: show retryable service-unavailable message.
- Insufficient balance or quota: show billing error and link to purchase/usage pages.
- Unsupported attachments for selected model: block send with a specific message.
- Upload rejected: show max size or file type reason.
- Upstream stream interrupted: persist the assistant message as failed or partial failed, and allow retry.

Errors before upstream dispatch should not create usage logs or bill the user.

## Testing

Backend tests:

- Conversation CRUD enforces user ownership.
- Message send persists user and assistant messages.
- Model capability validation blocks unsupported image/file context before billing.
- Web Chat usage records preserve subscription-first billing behavior.
- Web Chat usage logs include the Web Chat inbound endpoint.
- Artifact download rejects non-owner access.
- Attachment upload enforces content type and size.

Frontend tests:

- `/chat` requires authentication.
- Sidebar includes the Chat entry.
- Homepage CTA routes logged-out users through login and logged-in users to `/chat`.
- Conversation list renders persisted conversations.
- Model selector updates selected model and displays capability warnings.
- Composer disables send while streaming and supports cancel.
- Attachment chips and artifact download actions render correctly.

Verification:

- Run relevant Go unit tests.
- Run frontend unit tests and typecheck.
- Start the local frontend dev server and verify desktop/mobile layout for `/chat` and homepage entry.
- Verify streaming output does not cause layout overflow.

## Open Decisions Resolved

- Conversations are persistent in the first implementation.
- Web Chat is a logged-in feature.
- The primary authenticated route is `/chat`.
- The homepage should promote the feature but not allow anonymous use.
- Users may switch models mid-conversation.
- Cross-provider switching uses provider-neutral stored history plus per-request adapter conversion.
- Unsupported context is blocked or explicitly reduced by user choice; it is not silently dropped.
- Billing must reuse existing subscription-first gateway billing behavior.
