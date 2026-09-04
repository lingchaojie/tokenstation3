# Unified Model Catalog and Root Gateway Endpoints Design

## Goal

Make one unified API key return a stable, deduplicated catalog of every model
that the key is currently authorized to call through its effective default
groups, while allowing all supported client examples to use
`https://www.linx2.ai` without an added `/v1` suffix.

## Confirmed scope

This is display and compatibility work only. It must not change model-family
selection, account selection, scheduling, failover, request translation,
billing, or upstream forwarding behavior.

The change covers:

- `GET /models` and `GET /v1/models` for unified (`auto`) API keys;
- the Codex `client_version` model-manifest response for unified keys;
- a ten-minute Redis cache for the display catalog;
- bare-path aliases needed by examples that no longer contain `/v1`;
- key-usage modal, Codex, OpenCode, CC Switch, SDK, WorkBuddy, and Grok Build
  generated examples.

The change does not cover:

- adding DeepSeek, Zhipu/GLM, or Kimi to unified-key routing;
- changing POST routing when two providers expose the same public model ID;
- changing standalone typed-key model-list behavior;
- removing existing `/v1` routes;
- removing Gemini's required `/v1beta` prefix;
- rewriting admin `/api/v1`, internal APIs, or configured upstream provider
  base URLs.

DEV already supports configuring Kimi, Zhipu/GLM, and DeepSeek accounts and
groups, including API keys, base URLs, and model mappings. Unified keys
currently resolve only Anthropic and OpenAI effective default groups. Under the
confirmed display-only scope, models that exist exclusively in standalone
Chinese-provider groups are not advertised by a unified key, because that key
cannot route to those groups yet. DeepSeek or GLM public IDs do appear when
they are configured on accounts that are genuinely callable through one of the
unified key's effective groups.

## Approaches considered

### 1. Probe every upstream provider

Fetch each provider's model endpoint and merge the live results. This can
discover provider-native names, but it makes `GET /models` depend on proxy,
credentials, provider availability, and incompatible response formats. A
temporary upstream incident would also make the public catalog unstable. This
approach is rejected.

### 2. Aggregate configured models from effective groups

Resolve the unified key's effective Anthropic and OpenAI groups using the same
user override and global-default rules already used by authentication. Read
the persistently eligible accounts in those groups, collect their configured
public mapping keys, then normalize, deduplicate, and sort them. This is the
selected approach because it produces a stable, permission-aware catalog
without entering the forwarding path.

### 3. Aggregate every active group in the system

Scan all active groups regardless of the key's effective routes. This would
make Claude, GPT, DeepSeek, GLM, and Kimi names easy to display, but could
advertise models the key cannot call and leak private or exclusive group
configuration. This approach is rejected.

## Catalog semantics

### Effective groups

For a unified key, the catalog resolver independently resolves the key owner's
Anthropic and OpenAI effective groups. Resolution preserves the existing order
of precedence:

1. the user's provider-specific route override;
2. the administrator's provider default group;
3. existing active-group and user-access checks.

A provider with no valid or authorized effective group contributes nothing.
Unexpected repository or settings failures fail the request instead of
returning a misleading partial catalog. Duplicate group IDs are processed
once. Resolving the catalog must not mutate the authenticated key or influence
subsequent requests.

Static and `default_follow` keys retain their current single-group model-list
behavior.

### Account eligibility

The catalog uses the existing persistent model-availability query rather than
the live scheduler snapshot. Accounts must be enabled and schedulable in
persistent configuration, but temporary rate limits, overloads, cooldowns,
expiry windows, and runtime unscheduling do not remove their models from the
display catalog.

Only accounts that the current scheduling rules can genuinely select for the
effective group may contribute. Existing mixed-platform eligibility remains in
force, including the per-account Antigravity and Kiro mixed-scheduling flags.
The catalog does not broaden any mixed-platform pool.

### Public model IDs

The source is each eligible account's effective `model_mapping`:

- mapping keys are the public model IDs returned to clients;
- mapping values are upstream model IDs and are not returned;
- identity mappings act as explicit whitelists;
- built-in effective mappings used by supported account types count as
  configured mappings;
- blank keys and wildcard keys such as `gpt-*` are not concrete display
  models and are omitted;
- OpenAI passthrough accounts have no finite enumerable catalog and therefore
  contribute no IDs of their own;
- exact public IDs are trimmed, deduplicated across accounts and groups, and
  sorted for deterministic output.

An empty configured result is valid and returns an empty list. The unified
catalog never adds hard-coded fallback models. Existing per-group custom model
list configuration remains a final display-only intersection and cannot add a
model that no eligible account exposes.

The cache and response store only model IDs. Provider or group candidates are
not stored because this catalog is not used for routing.

## Redis cache

Each effective group catalog is cached under a versioned key containing the
group ID and platform. The JSON value is a list of public model IDs, including
an explicit empty list when no configured models exist.

The cache uses a ten-minute TTL with cache-aside refresh:

1. a request reads Redis first;
2. a cache miss recomputes from persistent configuration and writes Redis for
   ten minutes;
3. later requests read the cached list until expiry;
4. a Redis read or write failure falls back to the database and is logged;
5. a database failure returns an internal error rather than a fabricated
   catalog.

The cache is an optimization for `GET /models` only. It is not consulted by
POST routing or scheduling.

## HTTP responses

`GET /models` and `GET /v1/models` continue to share one handler and return the
same result for a unified key.

Ordinary clients receive an OpenAI-compatible list:

```json
{
  "object": "list",
  "data": [
    {"id": "claude-opus-4-6", "object": "model"},
    {"id": "gpt-5.5", "object": "model"}
  ]
}
```

Codex requests containing `client_version` receive the minimal Codex manifest
shape generated from the exact same sorted IDs:

```json
{
  "models": [
    {"slug": "claude-opus-4-6"},
    {"slug": "gpt-5.5"}
  ]
}
```

The separate shape is required because Codex's model picker consumes manifest
entries keyed by `slug`, while generic OpenAI-compatible clients consume
`object` and `data[].id`. Unified-key Codex model discovery is synthesized
locally and performs no upstream model probe.

## Bare gateway paths and examples

Existing `/v1` endpoints remain backward compatible. The server adds only the
missing aliases needed for bare client base URLs, delegating to the exact same
middleware and handlers as their `/v1` equivalents:

- `POST /messages` aliases `POST /v1/messages`;
- `POST /antigravity/messages` aliases
  `POST /antigravity/v1/messages`;
- `POST /antigravity/messages/count_tokens` aliases
  `POST /antigravity/v1/messages/count_tokens`.

Other required bare endpoints, including `/models`, `/responses`,
`/chat/completions`, `/embeddings`, image endpoints, and
`/messages/count_tokens`, already exist and are retained.

Generated client configurations use the normalized bare origin:

- Codex `base_url` and CC Switch Codex endpoint use
  `https://www.linx2.ai`;
- OpenCode Anthropic and OpenAI providers use the bare origin;
- Antigravity Claude uses `https://www.linx2.ai/antigravity`;
- OpenAI SDK, image SDK, WorkBuddy, Grok Build, and Grok CC Switch examples use
  the bare origin or a bare full endpoint such as `/chat/completions`;
- Claude Code and Anthropic SDK remain bare;
- Gemini and Antigravity Gemini retain `/v1beta` wherever the client requires
  it;
- the `ccswitch://v1/import` URI remains unchanged because `v1` is the CC
  Switch deeplink protocol version, not a gateway API suffix.

## Error handling and security

- The catalog is derived only after normal API-key authentication.
- Exclusive-group access checks remain authoritative.
- Missing one provider default is treated as no contribution from that
  provider, so a valid Anthropic-only or OpenAI-only unified catalog can still
  be returned.
- Infrastructure failures return the existing gateway internal-error shape and
  do not expose group IDs, account IDs, credentials, or upstream model values.
- No production server, production data, or provider account is modified or
  contacted as part of implementation or verification without separate user
  approval.

## Testing strategy

Implementation follows red-green-refactor TDD.

Backend tests cover:

- unified-key resolution of both effective groups, permission filtering,
  missing-provider handling, and group deduplication;
- stable candidate selection despite transient account state;
- mixed-platform eligibility flags;
- public mapping keys versus upstream values, wildcard omission, passthrough
  omission, deterministic deduplication, and valid empty catalogs;
- Redis hit, miss, explicit empty value, ten-minute TTL, and Redis-failure
  database fallback;
- identical unified results from `/models` and `/v1/models`;
- ordinary OpenAI list shape and Codex manifest shape from the same IDs;
- unchanged static-key behavior;
- registration of the three missing bare aliases.

Frontend tests cover every changed generated configuration and assert that
gateway API examples no longer add `/v1`, while Gemini `/v1beta` and the CC
Switch deeplink scheme remain unchanged.

Final verification includes focused red-green tests, affected backend and
frontend packages, Go formatting and lint, frontend lint/typecheck, builds,
and a review of the final diff against this scope.
