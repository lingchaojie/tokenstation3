# Sub2API Cursor Fork Sync Design

**Date:** 2026-08-23  
**Status:** Approved  
**Implementation branch:** `sub2api-cursor-fork-sync`

## Baselines

- Local integration base: `origin/dev@f768645be81754a170eaa48b8dd889692ef40473`
- Cursor behavior source: `SJwen0/cursor--@3709f0f6c83ed84b62c2a0f7f8e1ff63d6cfb7d4`
- Integration strategy: semantic port of the fork's final state, not a commit-by-commit cherry-pick
- Existing `cursor-channel` and `feat/cursor-channel` worktrees are experimental and are not implementation inputs

The fork's final Cursor behavior is the normative reference. Local deviations are allowed only where required to fit the latest DEV architecture, preserve local capture behavior, or retain stronger local security and lifecycle guarantees.

## Goals

1. Port the fork's complete Cursor forwarding runtime into the latest DEV.
2. Port the full Cursor account and credential lifecycle needed to operate that runtime.
3. Support the existing Chat Completions, Responses, and Anthropic Messages entry points.
4. Preserve the caller's requested API protocol at the delivery and capture boundaries.
5. Integrate Cursor with current scheduling, accounting, proxy, audit, and admin UI conventions.
6. Cover the fork behavior and the local adaptations with deterministic tests.

## Non-goals

- Reusing or completing the existing official-SDK Cursor experiments.
- Replacing the fork's private Connect/Protobuf transport with the official Cursor SDK.
- Persisting raw Connect/Protobuf frames as the capture delivery format.
- Building a long-lived, stateful Cursor Agent session coordinator; tool continuations remain stateless as in the fork.
- Porting the fork's standalone E2E CLI probe into the production runtime.
- Claiming Cursor channel-monitor support. The fork permits the platform in some monitor data paths but does not implement a Cursor-specific monitor.
- Modifying or probing production. Any production change or real production-account call still requires explicit user confirmation under `AGENTS.md`.

## Architecture

The implementation has five layers:

1. **Cursor wire package** — Connect envelope framing, Protobuf encoding and decoding, HTTP/2 stream management, upstream headers, heartbeats, timeouts, and event parsing.
2. **Credential/control plane** — login and credential import, token exchange and refresh, model discovery, account testing, persistence, and secret redaction.
3. **Gateway runtime** — request translation, account selection, retry classification, proxy-aware transport reuse, usage finalization, and billing integration.
4. **Protocol delivery and capture** — conversion back to the caller's original API protocol and capture of that delivered JSON or SSE representation.
5. **Admin surface** — platform types, API contracts, account forms, model presentation, status display, and localization.

The runtime data flow is:

```text
caller request
  -> existing Chat / Responses / Messages entry point
  -> Cursor request translation
  -> Connect/Protobuf HTTP/2 stream
  -> Cursor event decoding
  -> caller-protocol response encoding
  -> client delivery + local capture
```

## Cursor Wire Compatibility

The port preserves the fork's final wire behavior, including:

- `AvailableModels` through the Cursor api2 unary Protobuf endpoint.
- Agent `Run` through the Cursor api5 Connect streaming endpoint.
- The pinned Cursor CLI client-version identity and the fork's exact header contract.
- The initial `RunRequest`, environment context, exec close, KV acknowledgement sequence, pacing, and subsequent heartbeat behavior.
- Explicit end-envelope handling, Connect trailers, gzip envelopes, and a 64 MiB decompression limit.
- A 30-second idle watchdog and correct first-byte/stream termination handling.
- Decoding of text, thinking, tool calls, token deltas, usage, and provider errors.
- A 400 ms drain window for sibling parallel tool calls.
- Final enum and deep-link redaction fixes from the pinned fork commit.

Wire-level code should be kept independent of service-layer types. Service hooks may observe decoded events and terminal state, but capture concerns must not be embedded into the framing parser.

## Protocol Translation and Delivery

### Chat Completions

Chat Completions requests are translated to Cursor input and Cursor events are assembled into valid OpenAI Chat Completions JSON or SSE. Text, reasoning, tool calls, usage, finish reasons, and `max_tokens` termination must follow the fork's behavior.

### Responses API

Responses requests use the existing DEV compatibility path to reach the Cursor chat-shaped runtime. Cursor output is converted back through the current Responses encoder so the caller receives Responses JSON or SSE rather than Chat Completions chunks.

### Anthropic Messages

Messages requests use the existing DEV compatibility path to reach the Cursor runtime. Output is converted back to Anthropic Messages JSON or SSE, including valid content-block and tool-use event ordering.

### Tools and images

- MCP-style tool declarations are passed to Cursor using the fork's schema representation.
- Parallel tool calls are retained and delivered in the caller's protocol.
- Tool-result continuation is stateless: the complete conversation is flattened into the next Cursor request.
- Inline data-URI images are supported as in the fork. Remote image URLs are not fetched by the gateway.
- Built-in Cursor agent tools are not serviced by the gateway unless represented as caller-declared tools.

## Capture Semantics

Cursor's Connect/Protobuf stream is an internal upstream transport detail. No `connect_proto` capture format is introduced.

Capture records remain uniform with the rest of DEV:

- The captured request is the caller's original API request representation.
- A non-stream response is captured as the final caller-protocol JSON.
- A stream response is captured as the caller-protocol SSE bytes produced by the final encoder.
- Chat Completions, Responses, and Messages therefore retain their own requested delivery formats.
- Connect acknowledgements, heartbeats, envelope headers, and other internal control frames never appear in the capture payload.
- Parsed usage, upstream failure, response completeness, and client disconnect are supplied as structured capture terminal metadata.
- Finish/stop reason is extracted from the delivered JSON/SSE bytes by the current capture extractor. Cursor must not populate the legacy `Final.StopReason` field.

Cursor integrates with the current typed capture-attempt ownership model rather than creating a parallel capture lifecycle. Retry replacement, exact-once commit, client-disconnect causality, and preservation of provider terminal truth must match the latest DEV behavior at the integration baseline.

If the client disconnects after a usable response prefix, capture keeps the exact delivered prefix and the latest DEV terminal metadata rules decide whether the authoritative outcome is upstream completion, provider failure, or client disconnect.

## Credential and Account Lifecycle

The complete fork lifecycle is included:

- Deep-link OAuth login with PKCE initiation and polling.
- Exchange of a `crsr_` Cursor User API Key for a session token.
- Import and upgrade of a `WorkosCursorSessionToken` cookie.
- Access-token caching, refresh, invalidation, rejected-token fingerprinting, and distributed refresh locking.
- Proxy-aware login, exchange, refresh, model discovery, testing, and forwarding.
- Fail-closed behavior when an account has a configured proxy that cannot be resolved or used.

Cursor credentials are stored through the existing encrypted account credential model. Cursor accounts are OAuth/session accounts; `cursor + apikey` is rejected because a `crsr_` key is an exchange credential, not an upstream bearer token.

Account testing calls `AvailableModels` rather than a billed chat endpoint. Observed models are stored with the account and become authoritative when present; the fork fallback model list is used only when model discovery has not produced a usable result.

Secrets in Authorization, Cookie, session-token, refresh-token, and deep-link fields are redacted from logs, audit records, errors, and capture headers.

## Scheduling, Retry, and Accounting

- Cursor participates in the existing platform scheduler, group routing, model whitelist, composite routing, quota, and profit-control paths.
- Transient transport failures receive the fork's same-account retry before normal account failover.
- Authentication rejection invalidates cached access tokens and uses the fork's refresh/retry classification.
- Rate limiting permits account switching without applying an unsupported long-lived quarantine.
- Cursor client-version rejection is treated as a provider-scoped stop condition to avoid futile account rotation.
- Once caller-visible streaming bytes have been emitted, the gateway does not replay the request and risk duplicate text or tool calls; it emits the appropriate protocol terminal outcome.
- A Cursor-configured proxy never silently falls back to direct egress.

Upstream `TurnEnded` usage is authoritative when present. When absent, the fork's fallback estimation for text, tool schemas, and images is used. Cursor has no reliable account-balance endpoint, so local usage logs remain the accounting source.

## Data Model, API, and UI Integration

The semantic port includes all platform-enum and validation sites required for Cursor to behave as a first-class account platform. This includes backend DTOs, repositories, services, routing, admin handlers, frontend API types, account forms, badges, model selectors, status rendering, and localization.

The fork's old migration number is not copied. Implementation allocates the next valid migration number from the then-current DEV head and adds migration tests following current repository conventions. Generated Ent and Wire artifacts must remain reproducible under `make check-generate`.

The UI exposes only supported behavior. It must not present API-key bearer mode, a balance refresh action, or Cursor monitor support that the runtime cannot fulfill.

## Error and Security Behavior

- Non-2xx Connect responses and Connect trailer errors are mapped to existing gateway error types without leaking upstream bodies or credentials.
- Host validation and SSRF protections are retained for upstream and proxy-derived URLs.
- Mid-stream failure preserves already delivered output and records the provider terminal cause in capture.
- Unknown or newly observed Cursor enum values do not panic the process; compatibility gaps are handled defensively and tested.
- Request logging uses IDs and bounded metadata, never raw credentials, image payloads, or full private conversation bodies.

## Testing Strategy

Implementation follows a fork-parity checklist tied to `3709f0f6c`. Each final fork behavior must be either represented by local code and a deterministic test or explicitly documented as an approved non-goal.

Required test groups:

1. **Wire tests:** envelope encode/decode, gzip, trailers, end flags, header contract, initial frame sequence, heartbeats, idle timeout, response-size limit, and parallel tool drain.
2. **Credential tests:** all three onboarding methods, refresh locking, invalidation, proxy fail-closed behavior, secret redaction, and observed-model persistence.
3. **Gateway tests:** Chat, Responses, and Messages in streaming and buffered modes; text, thinking, tools, parallel tools, images, `max_tokens`, usage, and model mapping.
4. **Retry tests:** same-account transient retry, account failover, auth rejection, rate limit, client-version rejection, pre-output retry, and post-output terminal handling.
5. **Capture tests:** original request format, exact delivered JSON/SSE, usage and stop metadata, upstream error truth, retry ownership, exact-once commit, and client disconnect.
6. **Control-plane tests:** platform validation, account DTOs, scheduler eligibility, account testing, migration, generated code, admin forms, API contracts, and localization.

No live Cursor or production-account request is required for automated acceptance. Any later live probe must use the application's real configured proxy path and requires explicit approval if it touches production configuration or credentials.

## Acceptance Criteria

- The branch remains based on the refreshed latest DEV baseline and contains no old experimental Cursor implementation.
- All in-scope runtime and control-plane behavior from the pinned fork final state is present.
- All three caller protocols receive valid native-format JSON/SSE responses.
- Capture stores caller-facing JSON/SSE, not Connect/Protobuf, and passes the latest DEV disconnect/terminal-state regression suite.
- Cursor credentials, proxies, model discovery, scheduling, retry, and accounting behave as specified.
- Backend tests, frontend lint/typecheck/tests, generated-code checks, migration tests, and focused Cursor parity tests pass.
- No unsupported Cursor monitor or API-key bearer behavior is exposed.
