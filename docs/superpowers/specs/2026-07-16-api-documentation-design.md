# LINX2 API Documentation Design

**Date:** 2026-07-16

**Status:** Approved

**Branch:** `feat/api-docs`

**Base:** `dev` at `b58e9f197`

## Summary

Add a first-party, public API documentation experience to the existing LINX2
frontend. The documentation follows a classic three-column developer-docs
layout inspired by the ZenMux quickstart while remaining visually and
behaviorally consistent with the current product.

The documentation combines developer guides and an API reference. It exposes
only interfaces and capabilities confirmed by the current code and production
usage, keeps client setup instructions consistent with the existing API-key
modal and beginner guide, and deliberately omits unsupported or internal
surfaces.

The canonical entry is `/docs`. Permanent entry points are added to the public
homepage header and the shared self-service sidebar used by regular users and
the administrator's personal-account area.

## Context and Evidence

### Reference product

The ZenMux quickstart establishes the desired information-density pattern:

- A persistent product/docs header.
- A grouped left navigation.
- A focused center article.
- A right page table of contents.
- Protocol and capability labels near the beginning of the page.
- Copyable request examples and explicit authentication guidance.

LINX2 does not copy ZenMux content, product claims, supported providers, or API
surface. Only the documentation interaction pattern is used as a reference.

### Local implementation

The current repository confirms these public gateway interfaces:

- Anthropic Messages and token counting.
- OpenAI Responses and Chat Completions compatibility.
- Key-aware model listing.
- OpenAI-compatible image generation and editing.
- Streaming, tool calling, structured output, reasoning controls, and prompt
  caching where supported by the selected protocol and model.
- API-key expiry, quota, IP/CIDR restrictions, subscription limits, and balance
  enforcement.
- Request correlation headers.

The project already has three important content sources:

- `frontend/src/components/keys/UseKeyModal.vue` presents existing client and
  SDK instructions.
- `frontend/src/components/keys/clientConfigFiles.ts` is the shared pure
  generator already consumed by the key modal and beginner guide for several
  clients.
- `frontend/src/components/getting-started/curriculum.ts` owns verified client
  installation commands and source metadata.

### Production verification

A read-only production inspection confirmed that the deployed gateway receives
real traffic on Messages, Responses, Chat Completions, image generation, and
image editing. Route registration was checked without changing production or
calling upstream providers.

Route registration alone is not treated as product support. In particular,
Gemini, Embeddings, video routes, internal aliases, and alpha endpoints are not
included merely because a handler is registered.

## Goals

1. Give developers a concise path from API key to a successful request.
2. Document the stable, currently supported gateway interfaces.
3. Explain LINX2-specific capabilities without exposing internal scheduling or
   provider credentials.
4. Keep Base URLs, commands, model examples, and client configuration identical
   to existing product guidance.
5. Make the documentation permanently discoverable from public and authenticated
   navigation.
6. Provide Chinese and English content using the existing locale and theme
   systems.
7. Make every article directly linkable, searchable, responsive, and keyboard
   accessible.
8. Document actual error envelopes and stream termination behavior rather than
   inventing one synthetic format.

## Non-Goals

- Advertising Gemini or native `/v1beta/*` support.
- Advertising Embeddings or video generation.
- Documenting internal aliases such as `/responses` or
  `/backend-api/codex/*`.
- Documenting `/v1/alpha/search`.
- Documenting administrator, JWT user-management, batch-image management, or
  other private control-plane APIs.
- Advertising retry, failover, scheduler, provider-pool, or other stability
  implementation details.
- Exposing KIRO cache emulation or presenting it as provider-native caching.
- Adding an interactive request runner that handles real user secrets.
- Building a separate documentation service or adopting a new Markdown runtime.
- Changing production or making upstream provider calls as part of documentation
  discovery.
- Deciding whether to add `/v1/balance` or change the public positioning of
  `/v1/usage`; that decision is explicitly deferred.

## Chosen Product Shape

Use a combined developer-guide and API-reference experience.

This is preferred over a pure endpoint catalog because LINX2 users need Base
URL, API-key, protocol, and client-configuration guidance before individual
parameter tables are useful. It is preferred over a broad management-platform
reference because the current request concerns the public inference gateway,
not the authenticated dashboard or administrator control plane.

The chosen visual direction is the classic documentation layout:

- Sticky documentation header.
- Grouped left navigation.
- Center article column.
- Sticky right page table of contents.
- Compact top capability tags.
- Existing LINX2 colors, typography, theme behavior, and controls.

## Entry Points and Route Policy

### Canonical routes

```text
/docs
/docs/guide/quickstart
/docs/guide/authentication
/docs/guide/client-integration
/docs/api-reference/messages
/docs/api-reference/responses
/docs/api-reference/chat-completions
/docs/api-reference/models
/docs/api-reference/images
/docs/platform/errors
/docs/platform/request-id
```

`/docs` renders the quickstart rather than requiring an extra redirect.

The existing authenticated `/docs/batch-image` alias remains authoritative and
must not be shadowed by a new catch-all. Documentation routes therefore use
explicit section prefixes rather than a broad `/docs/:slug` matcher.

In normal public mode, API documentation is anonymous. Backend-only mode keeps
the existing public allowlist semantics and does not gain a new anonymous
surface; authenticated users can still access the route.

### Navigation

- Add an always-visible internal “API Docs” / “API 文档” link to the built-in
  homepage header.
- Preserve the administrator-configured external `doc_url` as a separate
  external documentation entry. The first-party API docs do not overwrite this
  setting.
- Add `/docs` to `buildSelfNavItems` next to Beginner Guide and API Keys. This
  automatically covers regular users and the administrator's personal-account
  navigation.
- Keep the API documentation entry visible in simple mode.
- Preserve custom homepage HTML behavior. Do not inject built-in navigation into
  administrator-supplied full-page HTML or iframe content.

## Information Architecture

### Top capability tags

The quickstart header presents compact, non-interactive capability tags:

- Messages
- Responses
- Chat Completions
- Images
- Tools
- Streaming

These are descriptive labels, not provider availability guarantees. Gemini,
Embeddings, video, and stability features are absent.

### Left navigation

#### Quickstart

- Authentication and Base URL
- Unified API Key
- First request
- Retrieve available models

#### Client integration

- Claude Code
- Codex CLI
- OpenCode
- CC Switch
- Python SDK

#### API Reference

- Anthropic Messages
- OpenAI Responses
- Chat Completions
- Models
- Images

#### Advanced capabilities

- Streaming responses
- Tool calling
- Structured outputs
- Reasoning
- Prompt cache

#### Platform

- Error codes
- Request IDs and troubleshooting
- API-key limits and security settings

### Right table of contents

The right column is generated from stable article section metadata. Each heading
has a shareable hash anchor. The active item follows scrolling without changing
the route.

## Public API Reference Scope

### Documented endpoints

| Protocol | Method | Path | Positioning |
| --- | --- | --- | --- |
| Anthropic | POST | `/v1/messages` | Messages, streaming, and tool use |
| Anthropic | POST | `/v1/messages/count_tokens` | Preflight token estimation |
| OpenAI | POST | `/v1/responses` | Preferred OpenAI-compatible interface |
| OpenAI | POST | `/v1/chat/completions` | Compatibility for existing clients |
| Common | GET | `/v1/models` | Models available to the current key |
| OpenAI Images | POST | `/v1/images/generations` | Image generation |
| OpenAI Images | POST | `/v1/images/edits` | Image editing |

### Deferred endpoint

`GET /v1/usage` currently exposes API-key-authenticated usage, quota, remaining
balance/subscription information, rate windows, daily usage, and model
statistics. It intentionally skips billing enforcement so an expired or
quota-exhausted key can inspect its own state.

The product decision between documenting this interface as-is and adding a
separate balance interface is deferred. The first implementation must not add,
rename, or prominently document a balance API without a later explicit decision.

### Explicit exclusions

- `/v1beta/*`
- `/v1/embeddings`
- `/v1/videos/*`
- `/v1/alpha/search`
- `/responses` and `/responses/*`
- `/backend-api/codex/*`
- Batch image job-management routes
- JWT dashboard APIs
- Administrator APIs

## Content Ownership and Consistency

Documentation must not maintain another copy of client commands, Base URL
normalization, SDK examples, or sample model choices.

### Canonical sources

1. Runtime Base URL comes from `appStore.apiBaseUrl` / public
   `api_base_url`, falling back to `window.location.origin` exactly as the
   existing key and beginner-guide experiences do.
2. Bare-root, `/v1`, and full-endpoint joining rules live in one exported pure
   helper rather than being repeated in Vue components.
3. `clientConfigFiles.ts` remains the shared pure client-configuration module
   and is expanded as necessary.
4. SDK, WorkBuddy, and richer OpenCode builders currently embedded in
   `UseKeyModal.vue` are extracted into pure shared functions before being used
   by documentation.
5. Installation commands and official-source metadata remain in
   `getting-started/curriculum.ts`.
6. Default example model identifiers become shared structured constants used by
   the existing key instructions and documentation.
7. Labels and explanatory prose use shared i18n keys when the same concept
   already exists. Documentation-only explanations receive dedicated Chinese
   and English keys.

### Consumption rules

- `UseKeyModal`, `GettingStartedView`, and API docs call the same builders with
  their own display inputs.
- Public docs always pass a placeholder such as `$LINX2_API_KEY`; they never
  fetch, inject, persist, or log a real key.
- The documentation does not maintain a static exhaustive model list. It tells
  developers to call `GET /v1/models` with their key and uses only shared model
  constants for runnable examples.
- Reuse does not imply displaying every historical platform branch. Gemini and
  other excluded platforms remain filtered out of the documentation catalog.

## Endpoint Page Contract

Every endpoint article uses the same presentation order:

1. Method, path, and protocol label.
2. Concise behavior and compatibility statement.
3. Authentication headers and Base URL.
4. Request parameter table with required/optional, type, and description.
5. cURL example.
6. Python example where an existing supported SDK path applies.
7. Non-streaming success response.
8. Streaming event behavior where supported.
9. Endpoint-specific errors.
10. Request-correlation guidance.

Examples are copyable but never executable in the browser.

Claims use bounded language such as “compatible with the documented fields”
rather than “100% compatible.” Feature behavior may be conditioned on the
selected model and routed group where that is true in the implementation.

## Error Documentation

The gateway has multiple real error envelopes. The docs must preserve this
distinction.

### Gateway authentication and billing envelope

Authentication and billing middleware can return this structure before the
protocol handler runs:

```json
{
  "code": "INVALID_API_KEY",
  "message": "Invalid API key"
}
```

Document these actionable gateway codes:

| HTTP | Code | Recommended action |
| --- | --- | --- |
| 400 | `api_key_in_query_deprecated` | Move the key into a request header |
| 401 | `API_KEY_REQUIRED` | Add Bearer or `x-api-key` authentication |
| 401 | `INVALID_API_KEY` | Verify or recreate the key |
| 401 | `API_KEY_DISABLED` | Enable or replace the key |
| 401 | `USER_NOT_FOUND` | Contact the administrator about key ownership |
| 401 | `USER_INACTIVE` | Restore the user account |
| 403 | `ACCESS_DENIED` | Check the key's IP/CIDR rules |
| 403 | `API_KEY_EXPIRED` | Extend or replace the key |
| 403 | `GROUP_DELETED` | Bind the key to an available group |
| 403 | `GROUP_DISABLED` | Bind the key to an active group |
| 403 | `GROUP_NOT_ALLOWED` | Request access or bind another group |
| 403 | `INSUFFICIENT_BALANCE` | Add balance or check subscription coverage |
| 403 | `SUBSCRIPTION_INVALID` | Check subscription state |
| 429 | `API_KEY_QUOTA_EXHAUSTED` | Increase or reset the key quota |
| 429 | `USAGE_LIMIT_EXCEEDED` | Wait for reset or change the configured limit |
| 500 | `INTERNAL_ERROR` | Report the request IDs |
| 500 | `SUBSCRIPTION_MAINTENANCE_FAILED` | Report the request IDs |

### Anthropic envelope

Messages validation and protocol errors use:

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "model is required"
  }
}
```

### OpenAI envelope

Responses, Chat Completions, and Images validation errors use:

```json
{
  "error": {
    "type": "invalid_request_error",
    "message": "model is required"
  }
}
```

The docs explicitly warn that an OpenAI endpoint can still return the gateway
authentication/billing envelope because middleware runs before protocol
handling.

### Streaming failures

After streaming begins, the HTTP status may already be `200`:

- Messages emits an Anthropic `event: error` frame.
- Responses emits a terminal Responses event such as `response.failed`.
- Chat Completions follows its existing stream-error representation.

Client examples must inspect both the initial HTTP response and terminal stream
events.

## Request Correlation

Every endpoint page includes a compact troubleshooting note:

- Capture the response `X-Request-ID`.
- Capture the response `X-Client-Request-ID` when present.
- Include both values, endpoint, timestamp, and sanitized error response when
  contacting support.
- Never include an API key or provider credential in an error report.

This section describes correlation only. It does not advertise internal
monitoring or stability mechanisms.

## Frontend Architecture

Suggested boundaries:

```text
frontend/src/views/public/ApiDocsView.vue
frontend/src/components/api-docs/ApiDocsShell.vue
frontend/src/components/api-docs/ApiDocsHeader.vue
frontend/src/components/api-docs/ApiDocsSidebar.vue
frontend/src/components/api-docs/ApiDocsToc.vue
frontend/src/components/api-docs/ApiDocsSearch.vue
frontend/src/components/api-docs/ApiDocsCodeBlock.vue
frontend/src/components/api-docs/ApiEndpointPage.vue
frontend/src/components/api-docs/ApiGuidePage.vue
frontend/src/components/api-docs/catalog.ts
```

Responsibilities:

- `ApiDocsView` resolves the current page from the route and coordinates search,
  sidebar state, and hash navigation.
- `ApiDocsShell` owns the responsive three-column frame and common header.
- `ApiDocsSidebar` renders grouped catalog entries and the mobile drawer.
- `ApiDocsToc` renders article headings and active-scroll state.
- `ApiDocsSearch` searches the current locale's in-memory catalog.
- `ApiDocsCodeBlock` owns copy state, keyboard behavior, and horizontal overflow.
- `ApiEndpointPage` renders structured endpoint metadata.
- `ApiGuidePage` renders guide-specific structured sections.
- `catalog.ts` owns stable page IDs, routes, groups, keywords, capability tags,
  and endpoint metadata. It does not own client command strings.

No new backend documentation endpoint is required.

## Search and Navigation Behavior

- Build a small in-memory index from the selected locale's page titles,
  descriptions, endpoint paths, error codes, and keywords.
- Open search from the header button, `/`, or `Cmd/Ctrl+K`.
- Use an accessible dialog with focus management and Escape-to-close.
- Results link to a page and optional heading hash.
- Switching locale rebuilds the labels and search index without changing the
  current route.
- Unknown documentation routes render a documentation-scoped not-found state
  with links to Quickstart and search, not the global application 404.

## Responsive and Accessibility Behavior

### Desktop

- Left navigation approximately 17–18rem.
- Center article uses the remaining bounded width.
- Right page table of contents approximately 12–14rem.
- Header, left navigation, and right table of contents remain visible while the
  article scrolls.

### Tablet

- Keep the left navigation.
- Move the right table of contents into a compact article-level disclosure.

### Mobile

- Replace the fixed left navigation with an accessible drawer.
- Allow top capability tags to scroll horizontally.
- Keep the article full-width.
- Scroll inside code blocks without introducing page-level horizontal overflow.

All controls have visible keyboard focus, semantic labels, and reduced-motion
behavior consistent with the existing UI. The existing locale switcher and
theme controls are reused.

## Localization

- Add dedicated Chinese and English API-doc locale modules.
- Stable page IDs, route slugs, endpoint paths, JSON fields, code, model IDs,
  environment variables, and error codes are language-independent.
- Explanatory prose, navigation labels, parameter descriptions, and recommended
  actions are localized.
- Missing locale keys fail tests rather than silently falling back to mixed
  language content.

## Security and Privacy

- Never fetch real API keys for the public docs.
- Never place a secret in a URL, route query, hash, local storage, session
  storage, analytics event, console output, or error example.
- Use `$LINX2_API_KEY` or an equally obvious placeholder in every example.
- Link authenticated users to `/keys` for real key creation and to the existing
  “Use Key” modal for personalized configuration.
- Do not expose provider credentials, group scheduling rules, account-pool
  details, proxy configuration, or production topology.
- Sanitize any configured branding URL using existing helpers.
- External official-source links use `noopener noreferrer`.

## Testing Strategy

Implementation follows test-driven development.

### Shared content tests

- Base URL normalization handles bare origins, trailing slashes, and existing
  `/v1` suffixes exactly once.
- The same input produces identical Claude Code, Codex, OpenCode, CC Switch, and
  supported SDK output for the key modal, beginner guide, and docs.
- Documentation builders always receive a placeholder key.
- Shared example model constants are used instead of copied literals.

### Catalog and content tests

- The documented endpoint set exactly matches the approved seven endpoints.
- Gemini, `/v1beta`, Embeddings, video, alpha search, internal aliases, and
  stability features do not appear in page content, navigation, tags, or search.
- Every page ID, route, heading ID, and search entry is unique.
- Every Chinese key has an English counterpart and vice versa.
- Error-code tables preserve the documented HTTP/code mapping.
- Anthropic, OpenAI, and streaming error examples preserve their distinct
  envelopes.

### Route and navigation tests

- `/docs` and approved subroutes are registered before the global catch-all.
- Normal public mode permits anonymous access.
- Backend-only mode preserves the existing anonymous allowlist.
- `/docs/batch-image` continues to resolve to the existing authenticated guide.
- Homepage, regular-user sidebar, administrator personal sidebar, and simple
  mode expose the internal API-doc entry.
- Configured external `doc_url` remains available and is not rewritten to
  `/docs`.

### Component tests

- Desktop, tablet, and mobile navigation states render correctly.
- The mobile drawer traps/restores focus and closes with Escape.
- Search opens from mouse and keyboard, filters localized content, and navigates
  to route/hash targets.
- Copy buttons report success without mutating the example.
- Active table-of-contents state follows headings.
- Long paths and code blocks do not create page-level horizontal overflow.
- Light/dark theme and locale switching preserve the current article.

### Regression verification

- Focused API-doc and shared-generator Vitest suites.
- Existing `UseKeyModal`, `clientConfigFiles`, and beginner-guide suites.
- Full frontend unit suite.
- Type checking.
- Production frontend build.
- Full backend `go test ./...` because documentation depends on gateway
  contracts even though no backend behavior should change.
- `graphify update .` after implementation.

## Rollout and Compatibility

- The feature is additive and does not migrate user data.
- No production configuration change is required.
- Existing custom homepage behavior, external documentation URL, key modal,
  beginner guide, and batch-image guide remain available.
- No existing gateway endpoint changes semantics as part of this work.
- If a shared-generator extraction reveals an existing discrepancy between the
  key modal and beginner guide, preserve the currently approved displayed output
  and cover the resolution with explicit regression tests.

## Deferred Decisions

The following require separate product approval and are not implementation
blockers for the rest of the docs:

1. Whether `GET /v1/usage` is documented in the first public release.
2. Whether a separate `/v1/balance` API should exist.
3. Whether the docs later gain an authenticated, opt-in request playground.
4. Whether additional providers or endpoint families become publicly supported.

Until decided, none of these are inferred from registered routes or internal
implementation capability.
