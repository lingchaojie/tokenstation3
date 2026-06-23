# Web Chat

Web Chat adds an authenticated browser chat experience on top of the same model catalog, gateway dispatch, and billing pipeline used by API-key traffic.

## User Surface

- The public home page can link users to `/chat`, but the route requires an authenticated user session.
- The signed-in user sidebar includes a Web Chat entry.
- `/chat` presents a ChatGPT-style conversation workspace with:
  - conversation list and active thread view
  - provider and model switching based on the supported catalog subset
  - text, image, and file uploads
  - streaming assistant responses
  - attachment download links
- User-visible usage and cost details are shown through the existing Usage page, not inline on each chat bubble. Assistant messages store `usage_log_id` so a message can be tied back to the usage record.

## API Surface

The browser uses the authenticated `/api/v1/chat/*` API:

- `GET /chat/models` lists web-chat-capable models.
- `GET /chat/conversations` lists conversations owned by the current user.
- `POST /chat/conversations` creates a conversation with a default provider/model.
- `GET /chat/conversations/:id` loads a conversation and its messages.
- `PATCH /chat/conversations/:id` updates title, archive status, or default model.
- `DELETE /chat/conversations/:id` soft-deletes a conversation.
- `POST /chat/conversations/:id/messages` sends a user message and streams the assistant response.
- `POST /chat/conversations/:id/messages/:message_id/cancel` cancels a pending/streaming assistant message.
- `POST /chat/attachments` uploads an image or supported text file.
- `GET /chat/attachments/:id/download` downloads a user attachment.
- `GET /chat/artifacts/:id/download` downloads a stored model artifact.

## Model Scope

Web Chat reuses the public model catalog and filters it through `DefaultWebChatCatalogModels`.

Provider routing is intentionally narrow:

| Provider | Platform | Notes |
| --- | --- | --- |
| `anthropic` | `anthropic` | Routed through the main Anthropic gateway path. |
| `openai` | `openai` | Routed through the OpenAI gateway path. |
| `qwen` | `openai` | Treated as an OpenAI-compatible provider. |
| `gemini` | `gemini` | Routed through Gemini compatibility handling when possible. |

The selected provider/model is validated server-side before any conversation or message is accepted.

## Attachment Handling

- Upload size is capped at 20 MiB.
- Image uploads are read from web-chat storage and embedded into the Chat Completions payload as base64 data URLs when the selected model supports image input.
- Supported text-like files are included as bounded text previews appended to the user message.
- Unsupported MIME types and oversized files are rejected.
- Binary file context is not sent upstream unless it can be represented as an accepted image input.

## Billing

Web Chat uses the same billing semantics as API-key requests.

1. The service finds a user-accessible group for the selected model platform.
2. It creates or reuses a hidden API key named `Web Chat` with `key_type = web_chat`.
3. It resolves the active subscription for the routed group.
4. It runs the normal billing preflight:
   - subscription quota is checked first
   - balance fallback is allowed only when the user has enabled subscription balance fallback
   - balance-billed requests still apply user/platform quota checks
   - API key rate limits and RPM checks still apply
5. After upstream usage is known, `RecordUsage` calculates cost from the gateway pricing pipeline:
   - channel pricing when available
   - otherwise model pricing and fallback pricing
   - token, image, cache read/write, and multiplier handling are inherited from the gateway
6. `UsageBillingRepository.Apply` applies billing atomically with request de-duplication.
7. The usage log records `billing_type`:
   - `0` = balance
   - `1` = subscription

Hidden Web Chat API keys are not shown in the normal API key list. Usage records remain visible to the user in the Usage page, with the API key object hidden so the internal key secret is never exposed.

## Upstream Dispatch

Web Chat builds an OpenAI Chat Completions-shaped request internally, then dispatches by platform:

- Anthropic: selects a load-aware account from the routed group, converts Chat Completions to Responses and then Anthropic Messages, applies OAuth mimicry where required, sends the Anthropic upstream request, and converts the response back to Chat Completions format.
- OpenAI: selects a load-aware OpenAI account and uses the OpenAI gateway Chat Completions path. API-key accounts can be sent as raw Chat Completions when the account is configured or probed as not supporting Responses.
- Gemini: selects a load-aware Gemini account and uses the Gemini compatibility service when the selected account is a Gemini account.

Current Web Chat dispatch selects one account for a request. Unlike the normal gateway handlers, it does not yet wrap the request in the full multi-account failover loop.

## Current Limitations

- Conversation history can be sent to a different provider after the user switches models, but only if the target model supports the accumulated context shape.
- File uploads are sent as text previews or image inputs; arbitrary binary file understanding is not implemented.
- The artifact schema and download route exist, but automatic artifact extraction from Chat Completions responses is not enabled yet.
- Web Chat relies on correctly configured platform groups and active upstream accounts. If a user has no accessible group for a selected platform, sending is rejected before any upstream request is made.

## Verification

Recommended focused checks after Web Chat changes:

```bash
cd frontend
COREPACK_ENABLE_AUTO_PIN=0 pnpm test:run src/components/chat/__tests__/ChatView.spec.ts
COREPACK_ENABLE_AUTO_PIN=0 pnpm typecheck

cd ../backend
go test ./internal/service ./internal/handler ./internal/repository
```
