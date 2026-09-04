# sub2api upstream `a2fb09260` semantic integration design

## Status

- Date: 2026-09-01
- Design status: approved in chat; written-spec review pending
- Local branch at design time: `dev`
- `DEV_BASE`: `f768645be81754a170eaa48b8dd889692ef40473`
- `LAST_UPSTREAM` / merge base: `2bc139ab527b4a687546d145dc7bb9063cf14510`
- `UPSTREAM_PIN`: `a2fb09260a955676f99cdc92f05469febee82a08`
- Upstream description: `v0.1.185-1-ga2fb09260`

## Objective

Semantically integrate the complete missing Wei-Shaw/sub2api history through
`UPSTREAM_PIN` into the latest local `dev`, preserving a normal two-parent merge
commit and the local product behavior documented below.

The integration must bring in the upstream pricing/catalog improvements,
per-user public-group access controls, Codex model catalog, OpenAI WebSocket and
429 stability fixes, usage observability, and cross-provider compatibility and
failover fixes. It must not reintroduce product areas that this fork has already
excluded.

## Evidence and scale

At design time:

- `dev == origin/dev == f768645be81754a170eaa48b8dd889692ef40473`.
- The upstream gap is 420 commits, including 261 non-merge commits.
- `LAST_UPSTREAM..UPSTREAM_PIN` changes 733 files, with 64,332 insertions and
  4,881 deletions.
- A read-only `git merge-tree --write-tree dev upstream/main` simulation reports
  153 explicit conflict paths.
- 48 files changed by local post-sync capture/forwarding work also change in the
  new upstream range.

These facts rule out a file-copy, conflict-marker union, or selective
cherry-pick strategy. The implementation must review both explicit conflicts
and Git's automatic merge regions.

## Chosen integration strategy

Use a normal merge with the fixed upstream commit:

```text
local dev       DEV_BASE ─────────────┐
                                      ├─ MERGE_COMMIT
upstream        UPSTREAM_PIN ─────────┘
```

The merge is started with `--no-ff --no-commit`. `DEV_BASE` is the first parent
and `UPSTREAM_PIN` is the second parent. The final merge tree is a semantic
implementation, not the textual union of both parents.

This strategy is selected because it:

- preserves the complete upstream ancestry for future syncs;
- keeps the local branch history and local extensions intact;
- permits non-force rollback through `git revert -m 1`;
- makes the exact integrated upstream endpoint reproducible.

Selective cherry-picking is rejected because the requested areas share schema,
generated code, settings, protocol conversion, scheduling, pricing, and usage
logging dependencies. Rebuilding the fork on top of the upstream tree is also
rejected because it would obscure or rewrite local history and greatly increase
the risk of losing local behavior.

## In scope

### Build and runtime baseline

- Upgrade the repository's Go baseline from 1.26.6 to 1.27.0 where required by
  the upstream code.
- Align CI, Dockerfiles, lint configuration, and JSON/runtime compatibility with
  that baseline without dropping stricter local checks.
- Preserve all local deployment, security, capture sidecar, and production
  configuration behavior unless a concrete upstream compatibility change
  requires a documented adjustment.

### Pricing and billing

- Data-driven long-context price tiers from the model-price catalog.
- `pricing.override_file` JSON patch support.
- Channel time-pricing weekday rules.
- Fast/priority/flex service-tier propagation and pricing based on the observed
  upstream tier, with no unauthorized tier upgrade in billing.
- DeepSeek peak/off-peak pricing and upstream account-stat pricing-policy
  alignment.
- Model Plaza display of context tiers and time-pricing information.

The following local billing behavior remains authoritative:

- upstream usage supplies quantities; this site supplies prices;
- a completed billable request with missing usage fails closed;
- missing pricing for a used bucket fails closed;
- an explicitly configured numeric zero remains a valid free price;
- image and video pricing, per-bucket provenance, group/channel overrides,
  account-stat cost, and alias fallback retain the locally verified behavior;
- KIRO direct keeps its approved tokenizer/request fallback and Anthropic price
  mapping; KIRO relay requires real token usage;
- upstream billing rates must not overwrite local account pricing.

### Public-group access control

Add an opt-in `restrict_public_groups` user setting:

- the persisted default is `false`;
- existing and new users remain able to bind every public group by default;
- exclusive groups retain the existing `allowed_groups` requirement;
- only users explicitly switched to restricted mode must have a public group in
  `allowed_groups` before they can see or bind it;
- subscription-group availability continues to require an active subscription.

The existing simple-mode defaults are protected invariants:

- `openai-default` and `anthropic-default` continue to be created when missing;
- they remain active, standard, non-exclusive groups with rate multiplier 1.0;
- existing API keys bound to either default group continue to authenticate and
  route after upgrade;
- the new access-control migration does not rewrite existing user/group edges.

### Codex and OpenAI

- Routed Codex model catalog and capability intersection based on actual routes.
- Stable catalog behavior when accounts are temporarily or persistently
  unschedulable.
- API-key and OAuth catalog isolation, upstream model synchronization, aliases,
  image-input capability preservation, and priority tier advertisement.
- OpenAI reset-credit use based on configured usage thresholds.
- OAuth quota-exhaustion 429 scheduling, model-scoped Spark limits, transient
  429 retry behavior, and WebSocket semantic-rate-limit isolation.
- Codex session affinity and model-capacity spillover behavior.
- Configurable image-tool cooldown.
- WebSocket request isolation, oversized HTTP bridging, stale idle connection
  recycling, capacity-shed error mapping, client-close attribution, and TTFT
  mode settings.
- API-key instruction, client-tool replay, Responses Lite, service-tier,
  reasoning replay, delegation bootstrap, and terminal-event compatibility
  fixes in the pinned range.

### Cross-provider compatibility and failover

Integrate applicable fixes for:

- OpenAI Responses, Chat Completions, WebSocket, images, tools, and API-key
  forwarding;
- Anthropic native/API-key and compatible paths;
- Bedrock transport failures;
- Gemini and Antigravity protocol conversion, tool schema, token limits, and
  endpoint selection;
- Kimi, Zhipu, and DeepSeek adaptive/native protocol paths, account tests,
  balance/quota probing, usage normalization, and tool conversion;
- Grok text, x_search, supported image/video behavior, error classification,
  cooldown, and retry handling;
- Ollama Cloud usage windows attached to supported Chinese-provider accounts;
- Zhipu team GLM Coding Plan usage queries.

Provider-specific behavior must feed the shared scheduling, error,
usage-pricing, and capture boundaries instead of bypassing them.

### Usage and operations observability

- Persist the client-requested reasoning effort separately from the effective
  mapped effort, while hiding policy-internal mapped values from ordinary users
  as intended upstream.
- Persist and filter native OpenAI remote-compaction-v2 requests.
- Add the configurable TTFT interpretation mode and related API/UI contracts.
- Integrate applicable Ops error detail/list navigation and upstream endpoint
  observability without weakening local capture or error-request reporting.

## Explicitly out of scope

The final tree must not add or enable:

- the experimental `.s2plugin` upload/runtime/UI system;
- plugin database tables, plugin package API, plugin process execution, or
  plugin deployment volumes/configuration;
- Composite backend, schema, admin UI, routes, or product documentation;
- Grok Voice, TTS, STT, Realtime, custom voices, or audio billing;
- an independent `/x_search` product surface;
- upstream billing-rate writeback into local account configuration.

The fork's unified key remains the cross-platform entry point. Local WebChat,
payment/rewards, branding, capture, KIRO, and other unrelated extensions remain
in place.

## Data and migration design

The local tree already uses migration numbers 229 and 230 for capture spool
alerts and channel image-input pricing. Upstream uses 229 and 230 for the
excluded plugin subsystem and contains three files numbered 231.

The integration will:

1. omit upstream plugin migrations 229 and 230 together with the plugin code;
2. assign unique, monotonically ordered local filenames to the three included
   schema changes:
   - `usage_logs.native_compaction_v2` (`BOOLEAN NOT NULL DEFAULT FALSE`);
   - `usage_logs.requested_reasoning_effort` (nullable text, no backfill);
   - `users.restrict_public_groups` (`BOOLEAN NOT NULL DEFAULT FALSE`);
3. update the repository's migration sequence/schema contract tests;
4. regenerate Ent only from the final intended schema;
5. verify an empty database and an upgraded pre-merge database against real
   PostgreSQL;
6. require migrations to be idempotent and metadata-only where the upstream SQL
   intends that behavior.

No production database is accessed or modified during implementation.

## Forwarding state machine

The integrated request flow remains:

```text
ingress
  -> authentication and group authorization
  -> request normalization and model mapping
  -> account scheduling
  -> provider-specific transformation
  -> upstream transport
  -> provider/transport/error classification
  -> pre-output failover when allowed
  -> committed response handling
  -> usage extraction and pricing preflight
  -> guarded final side-effect submission
       (idempotent billing/usage logging + capture of the real final attempt)
```

Required error semantics:

- A real provider or transport failure before output may trigger failover.
- Client cancellation/disconnect must not punish or disable the provider
  account.
- Once output is committed, the service preserves the real bytes already sent;
  it does not replace them with a synthetic full-response error.
- A committed partial/terminal result still supplies its real terminal state to
  capture and billing logic.
- Missing billable usage or missing relevant pricing fails closed and is
  reported through the stable Ops error path.
- Provider failure classification must not overwrite a more specific committed
  result or client-disconnect cause.
- 429 handling distinguishes quota exhaustion, transient rate limiting,
  model-scoped rate limiting, and WebSocket semantic limits; pause/cooldown
  scope follows that classification.

## Idempotent settlement and capture semantics

This design deliberately avoids claiming that all distributed side effects are
mathematically exactly-once.

There are three separate guarantees:

1. The local handler submitter uses `sync.Once` so usage/capture submission for
   one final upstream attempt is invoked at most once. A pre-output failed
   attempt that will fail over submits no final side effects.
2. Billing claims `(request_id, api_key_id)` in a narrow dedup table and checks a
   request fingerprint. Balance, subscription, quota, and billing-dedup effects
   are committed in one database transaction. A duplicate is a no-op; a reused
   key with a different fingerprint is an error.
3. Usage-log persistence is deduplicated best-effort with bounded asynchronous
   batching and a synchronous fallback. It is not in the same database
   transaction as billing, so the design does not describe billing plus usage
   logging as one atomic exactly-once action.

The handler-level coupling is a local semantic integration absent from current
`upstream/main`; it protects the local capture system and must remain. The
database billing dedup foundation originates upstream and is shared by both
trees.

## Capture invariants

- Capture observes the final real provider attempt and never decides whether a
  provider response is acceptable.
- Existing redaction, size/spool, service-model allowlist, terminal-error, and
  exact final-attempt rules remain.
- Client disconnect classification, provider-terminal causality, retryable
  sidecar upload behavior, and stable session identity added after the last
  upstream sync remain.
- No new provider is silently added to capture recording merely because its
  forwarding path is integrated.

## Frontend and public API contracts

- Admin user editing gains the opt-in public-group restriction control.
- Existing users deserialize missing `restrict_public_groups` as false.
- Usage views and filters gain requested/effective reasoning and native
  compaction behavior without exposing policy-internal data to ordinary users.
- Model Plaza displays the new catalog/context/time-pricing data without losing
  local model aliases or group visibility behavior.
- Account, group, channel, and settings forms preserve every local field through
  API-to-form-to-API round trips.
- Existing local navigation, announcement, WebChat, payment, branding, and key
  instructions remain.

## Implementation sequence

1. Refresh and pin `origin/dev` and `upstream/main`; stop on drift.
2. Create a backup branch at `DEV_BASE`, a dedicated merge branch, and an
   isolated worktree.
3. Start the ordinary no-commit merge of `UPSTREAM_PIN`.
4. Build a complete conflict/automatic-merge ledger.
5. Resolve build baseline, migrations, schema, Ent, and Wire.
6. Integrate public-group and usage-observability contracts.
7. Integrate pricing and account-stat behavior.
8. Integrate shared forwarding/error/failover behavior.
9. Integrate provider-specific behavior through the shared boundaries.
10. Integrate frontend and deployment contracts.
11. Enforce exclusion and local-invariant tests.
12. Audit every changed production region and its caller/callee chain.
13. Create the synchronization archive, then run the full verification matrix
    on the final uncommitted merge tree.
14. Create the two-parent merge commit containing the verified code and archive.
15. Obtain a fresh independent full-range review.
16. Re-fetch and stop for user authorization before any push.

If a test-driven fix is made after a verification command, the affected command
and any invalidated broader suite are rerun.

## Verification design

### Targeted behavior tests

- Billing dedup: duplicate request, fingerprint conflict, transaction rollback,
  explicit free pricing, missing bucket price, and usage-log fallback.
- Default groups: simple-mode creation, migration upgrade, existing API-key
  authorization, new-user defaults, and opt-in restriction.
- Pricing: catalog override, context tiers, weekday periods, observed service
  tier, DeepSeek peak/off-peak, group/channel overrides, images/videos, account
  stats, KIRO, and alias fallback.
- Forwarding: pre-output failover, committed partials, malformed upstream
  events, tool/reasoning transforms, usage extraction, and provider-specific
  errors.
- 429: quota reset, transient retry, model scope, account scope, and WebSocket
  semantic isolation.
- Capture: final attempt, client disconnect, provider terminal errors, billing
  submission once, sidecar retries, and no capture-based response rejection.
- Exclusions: no plugin runtime/routes/schema/UI, no Composite surface, and no
  Grok audio/Realtime surface or defaults.

### Repository-wide gates

Commands are selected from the final Makefile and CI workflows, and include:

- generated-code consistency;
- backend build;
- backend unit and integration suites;
- required race-focused tests;
- backend lint/static checks;
- frontend lint, typecheck, targeted tests, full tests, and production build;
- migration/schema integration tests on PostgreSQL;
- deployment shell/Compose/Caddy/container contract tests required by CI;
- conflict-marker, whitespace, unexpected-file, and generated-drift checks.

Baseline failures are only classified as pre-existing after the identical
command reproduces at `DEV_BASE` in a separate worktree.

## Review, publication, and rollback

After the final tree and archive pass local verification, a fresh independent
reviewer receives raw coordinates and test results and reviews the complete
`DEV_BASE..HEAD` integration, including explicit conflicts and automatic merge
regions. The review must end with `SAFE TO PUSH`; any actionable issue triggers
a fix, relevant retest, and a new independent review.

No push is performed merely because implementation and local testing finish.
Before publication:

- fetch `origin` and `upstream` again;
- stop if `origin/dev`, `origin/main`, or `upstream/main` drifted;
- present the final SHA, parents, tests, review result, migration impact, and
  rollback branch;
- obtain explicit user authorization;
- push without force and wait for every required check on the exact pushed SHA.

No production deployment or production-environment mutation is in scope.

Rollback uses a normal revert of the merge commit with first-parent mainline,
followed by the same review and test discipline. The backup branch is retained
until the user confirms the integration is stable.

## Decision log

- The user approved a complete-history semantic merge through `a2fb09260`.
- The user approved excluding the plugin system, Composite, and Grok
  Voice/TTS/STT/Realtime while retaining local KIRO, capture, unified key, and
  site-controlled billing.
- The user confirmed that the original OpenAI and Anthropic default-group
  design must remain unchanged.
- The billing guarantee is documented as transactional idempotent settlement;
  usage logging is deduplicated best-effort with fallback, not part of a single
  exactly-once transaction.
- Push and production operations require separate explicit authorization.
