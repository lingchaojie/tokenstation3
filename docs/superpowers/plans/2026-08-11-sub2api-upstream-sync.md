# Sub2API Upstream Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the complete `eb2b8632ded6..0b3fe95afd20` upstream range into the approved local dev line without regressing KIRO, WebChat, capture, payment, rewards, public APIs, settings, branding or deployment behavior.

**Architecture:** Create one isolated merge worktree from the committed plan tip whose parent is the approved design commit `abc6c839727ff1af6b86533b82cb159f3fbc83a3`, preserve the upstream topology with one ordinary two-parent merge, and resolve the merge by functional domain. Each domain ends in an explicitly staged, reviewable checkpoint with targeted evidence; the final merge commit is created only after all domain checkpoints and semantic decisions are complete.

**Tech Stack:** Git merge/worktree, Go 1.26.5, Ent, Wire, PostgreSQL migrations, Redis, Gin, Vue 3, TypeScript, pnpm 9, Vitest, Docker/Caddy, GitHub Actions.

## Global Constraints

- Fixed pre-plan design tip: `abc6c839727ff1af6b86533b82cb159f3fbc83a3`.
- Effective `DEV_BASE`: the execution-time dev tip whose parent is the fixed design tip and whose only change is this implementation plan; record its concrete SHA in `/tmp/tokenstation3-upstream-sync-dev-base.txt` before creating refs.
- Fixed last upstream: `eb2b8632ded614bf991d7d36abfa38b513ad8c2d`.
- Fixed upstream pin: `0b3fe95afd20aba77ee7649b37febb8255fb57a5`.
- Preserve the complete missing history with `git merge --no-ff --no-commit`; do not replace the sync with cherry-picks or a squash.
- Preserve every invariant in `docs/superpowers/specs/2026-08-11-sub2api-upstream-sync-design.md`.
- Never resolve a semantic conflict without the user's decision; present local behavior, upstream behavior, conflict, impact, options and recommendation first.
- Treat Git auto-merged shared production paths as requiring the same semantic review as explicit conflicts.
- Never resolve generated Ent, Wire or pnpm lockfiles by text union; regenerate from approved sources.
- Stage explicit paths only; never use `git add -A`.
- Do not access or change production accounts, databases, code or provider APIs without separate confirmation under `AGENTS.md`.
- Do not call upstream provider APIs outside the repository's configured proxy and service flow.
- While `MERGE_HEAD` exists, domain tasks end with staged checkpoints rather than commits; only Task 14 creates the required two-parent merge commit.

---

### Task 1: Create the isolated merge workspace and prove the baseline

**Files:**
- Read: `AGENTS.md`
- Read: `skills/sync-sub2api-upstream/SKILL.md`
- Read: `docs/upstream-sync/README.md`
- Read: `docs/upstream-sync/2026-07-16-sub2api-0.1.156-eb2b.md`
- Read: `docs/superpowers/specs/2026-08-11-sub2api-upstream-sync-design.md`
- Read: `.github/workflows/backend-ci.yml`
- Read: `Makefile`
- No repository file modifications

**Interfaces:**
- Consumes: committed implementation-plan tip directly above `abc6c839727ff1af6b86533b82cb159f3fbc83a3`
- Produces: backup ref, isolated merge branch/worktree, dependency setup and baseline evidence

- [ ] **Step 1: Re-read all governing instructions and confirm the current workspace remains untouched**

Run:

```bash
git status --short --branch
git worktree list --porcelain
git remote -v
git ls-files --others --exclude-standard -z | sort -z | xargs -0 -r sha256sum > /tmp/tokenstation3-preexisting-untracked.sha256
```

Expected: the original worktree remains on `dev`; the previously recorded untracked documents remain unmodified.

- [ ] **Step 2: Refresh refs and enforce the fixed coordinates**

Run:

```bash
git fetch --prune --tags origin
git fetch --prune --tags upstream
test "$(git rev-parse HEAD^)" = abc6c839727ff1af6b86533b82cb159f3fbc83a3
test "$(git diff-tree --no-commit-id --name-only -r HEAD)" = docs/superpowers/plans/2026-08-11-sub2api-upstream-sync.md
test "$(git merge-base HEAD upstream/main)" = eb2b8632ded614bf991d7d36abfa38b513ad8c2d
test "$(git rev-parse upstream/main)" = 0b3fe95afd20aba77ee7649b37febb8255fb57a5
git rev-parse HEAD > /tmp/tokenstation3-upstream-sync-dev-base.txt
```

Expected: all tests exit zero. If any ref moved, stop for the Runbook drift decision.

- [ ] **Step 3: Create the rollback point and isolated worktree**

Run:

```bash
git check-ignore -q .worktrees
test ! -e .worktrees/upstream-sub2api-20260811-0b3f
if git show-ref --verify --quiet refs/heads/merge/upstream-sub2api-20260811-0b3f; then exit 1; fi
sync_dev_base=$(cat /tmp/tokenstation3-upstream-sync-dev-base.txt)
if git show-ref --verify --quiet refs/heads/backup/dev-before-upstream-sync-20260811-plan; then
  test "$(git rev-parse backup/dev-before-upstream-sync-20260811-plan)" = "$sync_dev_base"
else
  git branch backup/dev-before-upstream-sync-20260811-plan "$sync_dev_base"
fi
git worktree add .worktrees/upstream-sub2api-20260811-0b3f -b merge/upstream-sub2api-20260811-0b3f "$sync_dev_base"
```

If the backup ref already exists, verify it resolves to the fixed dev tip and
reuse it; if it resolves elsewhere, stop instead of moving it. Expected: the
backup ref and merge branch both resolve to the fixed dev tip; the original
worktree is unchanged.

- [ ] **Step 4: Install dependencies inside the isolated worktree**

Run from `.worktrees/upstream-sub2api-20260811-0b3f`:

```bash
go -C backend mod download
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend install --frozen-lockfile
```

Expected: both commands exit zero without modifying tracked dependency files.

- [ ] **Step 5: Run the merge baseline checks**

Run sequentially:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=integration -p 1 ./...
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run test:run
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend
GOMAXPROCS=2 make build-backend
COREPACK_ENABLE_PROJECT_SPEC=0 make build-frontend
GOMAXPROCS=2 make check-generate
```

Expected: all checks pass and `git status --short` contains no tracked changes. Any failure is reported before the merge and requires a proceed-or-investigate decision.

### Task 2: Start the full merge and build the coverage inventory

**Files:**
- Modify: Git index and working tree through the merge
- Create later: `docs/upstream-sync/2026-08-11-sub2api-0.1.173-0b3f.md`
- No source resolution in this task

**Interfaces:**
- Consumes: clean isolated baseline and fixed upstream pin
- Produces: `MERGE_HEAD`, complete explicit-conflict list, shared auto-merge list, upstream-only production list and domain ownership map

- [ ] **Step 1: Start the ordinary merge without committing**

Run:

```bash
git merge --no-ff --no-commit 0b3fe95afd20aba77ee7649b37febb8255fb57a5
```

Expected: merge stops with conflicts, `MERGE_HEAD` equals the upstream pin, and no merge commit exists yet.

- [ ] **Step 2: Capture the exact merge topology and conflict evidence outside the repository**

Run:

```bash
git rev-parse HEAD MERGE_HEAD > /tmp/tokenstation3-upstream-sync-parents.txt
git diff --name-only --diff-filter=U | sort -u > /tmp/tokenstation3-upstream-sync-conflicts.txt
git diff --name-only eb2b8632ded614bf991d7d36abfa38b513ad8c2d..HEAD | sort -u > /tmp/tokenstation3-upstream-sync-ours.txt
git diff --name-only eb2b8632ded614bf991d7d36abfa38b513ad8c2d..MERGE_HEAD | sort -u > /tmp/tokenstation3-upstream-sync-theirs.txt
comm -12 /tmp/tokenstation3-upstream-sync-ours.txt /tmp/tokenstation3-upstream-sync-theirs.txt > /tmp/tokenstation3-upstream-sync-shared.txt
```

Expected: every explicit conflict is present in the shared/structural inventory, and both parents match the approved coordinates.

- [ ] **Step 3: Assign every changed production path to exactly one functional domain**

Use these concrete domain prefixes:

```text
migrations-schema-generated
auth-security-settings
gateway-transport-openai
kiro-scheduler
grok-composite-profit
usage-billing
subscription-payment-reward
webchat-capture-public-api
frontend-branding-deploy
cross-domain-auto-merge-audit
```

Expected: counts across domains equal the union of explicit conflicts, shared auto-merged production paths and upstream-only production paths. Tests and generated files attach to their owning production domain.

### Task 3: Decide and integrate migrations, schema and generated code

**Files:**
- Modify: `backend/migrations/*.sql`
- Modify: `backend/internal/repository/migrations_runner.go`
- Modify: `backend/internal/repository/migrations_schema_integration_test.go`
- Modify: `backend/migrations/*_migration_test.go`
- Modify: `backend/ent/schema/group.go`
- Modify: `backend/ent/schema/usage_log.go`
- Modify: other changed `backend/ent/schema/*.go` files identified by the inventory
- Regenerate: `backend/ent/**`
- Regenerate: `backend/cmd/server/wire_gen.go`

**Interfaces:**
- Consumes: all upstream migrations in the fixed range and the local 001-190 history
- Produces: approved monotonic migration sequence, final schemas and regenerated Ent/Wire artifacts

- [ ] **Step 1: Present the migration decision before editing**

Present the complete proposed mapping `upstream 172/178..220 -> local 191..228`, including duplicate source numbers, `_notx` migrations, runner constants, checksum rules and the data-clearing behavior of upstream migration 220. Ask separately whether any deployed environment has already recorded an original upstream filename.

Expected: explicit user approval of the mapping and a separate approve/defer decision for the migration-220 cleanup.

- [ ] **Step 2: Add regression tests for the approved migration policy**

Add tests with these exact responsibilities:

```text
TestUpstreamSyncMigrationSequenceStartsAfterLocal190
TestUpstreamSyncMigrationSequencePreservesNonTransactionalFiles
TestUpstreamSyncMigrationRunnerUsesRemappedFilenames
TestUpstreamSyncMigration220Policy
```

The migration-220 test asserts either preserved non-Grok values when deferred or backup-before-clear behavior when approved.

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=integration -p 1 ./internal/repository -run 'TestUpstreamSyncMigration|TestMigration'
```

Expected before implementation: at least the new mapping assertions fail because the upstream filenames are not integrated.

- [ ] **Step 3: Implement the approved migration order and runner references**

Rename the full upstream migration sequence monotonically after local 190, preserve lexical dependency order and `_notx` suffixes, then update `latestAPIKeyIPIndexMigration`, non-transactional dispatch, checksum compatibility rules, fixtures, comments and hard-coded test names.

Run the command from Step 2 again.

Expected: all targeted migration tests pass.

- [ ] **Step 4: Merge source schemas as a semantic union**

The group schema must retain KIRO, local payment/reward/subscription fields and add only approved upstream Composite, reasoning, profit, media and monitor fields. The usage schema must retain `kiro_credits` and add `image_input_tokens`, `image_input_cost`, `session_id`, `upstream_response_model` and `upstream_model_mismatch`.

- [ ] **Step 5: Regenerate rather than merge generated files**

Run:

```bash
make -C backend generate
make -C backend check-generate
```

Expected: Ent and Wire are generated from the resolved source schemas with no conflict markers or manual generated-file edits.

- [ ] **Step 6: Stage the migration/schema checkpoint explicitly**

Run `git add` with the exact resolved migration, schema and generated paths printed by `git status --short`; do not include files owned by later domains.

Expected: no unmerged migration/schema/generated entry remains, while unrelated domain conflicts remain unstaged.

### Task 4: Integrate authentication and security foundations

**Files:**
- Modify: `backend/internal/handler/auth_oauth_pending_flow.go`
- Modify: `backend/internal/handler/auth_oauth_pending_flow_test.go`
- Modify: `backend/internal/server/routes/auth.go`
- Modify: `backend/internal/server/middleware/security_headers.go`
- Modify: `backend/internal/pkg/ip/ip.go`
- Modify: `backend/internal/pkg/oauth/oauth.go`
- Modify: affected authentication handler/service tests from the inventory

**Interfaces:**
- Consumes: existing local OAuth pending flow, Yundu rules, KIRO External IdP and upstream security fixes
- Produces: ownership-safe OAuth completion, approved client-IP trust policy, approved Passkey/captcha surface and preserved local OAuth behavior

- [ ] **Step 1: Add the OAuth account-takeover regression first**

Port the upstream test `TestExchangePendingOAuthCompletionChoiceStateDoesNotBindIdentity` and assert that a non-terminal choice session cannot bind an attacker's provider identity to the target user or mutate that user's profile.

Run:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/handler -run TestExchangePendingOAuthCompletionChoiceStateDoesNotBindIdentity
```

Expected before the guard: FAIL. After applying the `02e50cc22d03` terminal-session guard in the local flow: PASS.

- [ ] **Step 2: Integrate URL/path and client-IP security without bypassing local routes**

Add regression coverage for Gemini path segments, OpenAI Responses subpaths, Grok media identifiers, KIRO relay/custom endpoints, trusted proxy lists and custom IP headers. Invalid client-controlled path segments must fail before URL construction; valid KIRO relay routes must remain accepted.

Run:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/server/routes ./internal/service ./internal/pkg/ip -run 'PathGuard|ClientIP|TrustedProxy|Kiro'
```

Expected: security cases and local KIRO compatibility pass together.

- [ ] **Step 3: Present authentication rollout decisions**

Present Passkey, Tencent captcha, Alibaba captcha, session binding and step-up 2FA as separate externally visible controls. Preserve the upstream final default-off state for session binding/step-up unless the user selects another default. Do not expose routes or UI for a deferred capability.

- [ ] **Step 4: Implement only the approved authentication surfaces and stage the checkpoint**

Run targeted handler, middleware and route tests, then explicitly stage only authentication/security paths.

Expected: local Yundu, WeChat/LinuxDo/OIDC pending flows and KIRO External IdP tests remain green; no unmerged auth/security entries remain.

### Task 5: Integrate settings, prompt audit and panel rate limiting

**Files:**
- Modify: `backend/internal/handler/admin/setting_handler.go`
- Modify: `backend/internal/handler/admin/setting_handler_update.go`
- Modify: `backend/internal/handler/dto/settings.go`
- Modify: `backend/internal/service/setting_service.go`
- Modify: `backend/internal/service/setting_update.go`
- Modify: `backend/internal/service/settings_view.go`
- Modify: `backend/internal/securityaudit/**`
- Modify: `backend/internal/server/routes/admin.go`
- Test: existing settings, prompt-audit and rate-limit tests identified by the inventory

**Interfaces:**
- Consumes: local consolidated settings and upstream omitted-field/security-audit/rate-limit behavior
- Produces: atomic partial updates that preserve every local field and only the approved new settings/defaults

- [ ] **Step 1: Write omitted-field preservation tests covering local settings**

Add a table-driven test named `TestUpdateSettingsOmittingPreservesLocalExtensions` covering KIRO, public plans, IkunPay, reward, affiliate, check-in, capture, announcement and `alvin` keys.

Run:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/service ./internal/handler/admin -run 'SettingsOmitting|Settings.*Preserve|Alvin|Affiliate|CheckIn|Capture'
```

Expected before integration: the new omission-contract test fails on whichever upstream update path would clear local fields.

- [ ] **Step 2: Fold upstream partial-PUT semantics into the consolidated local service**

Keep the local consolidated API surface. Record omitted fields in the handler, apply `UpdateSettingsOmitting` semantics in the service, reload the cache from persisted storage, and preserve secret masking/configured flags and audit keys.

- [ ] **Step 3: Present prompt-audit and rate-limit decisions**

For prompt audit, present gateway/WebChat/title coverage, full-prompt storage, retention, fail-open/fail-closed behavior and duplication with ClickHouse capture. For panel limiting, present the upstream default-on rates and the exact WebChat/payment/check-in/public/admin route coverage. Implement the selected scope and defaults only after approval.

- [ ] **Step 4: Test and stage settings/security-audit/rate-limit paths**

Run default and unit tests for settings, security audit, middleware and affected routes. Explicitly stage resolved paths.

Expected: all local setting DTO/public API contracts are preserved and no split-settings file shadows the consolidated implementation.

### Task 6: Integrate shared gateway, transports and OpenAI compatibility

**Files:**
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/repository/http_upstream.go`
- Modify: `backend/internal/pkg/proxyutil/dialer.go`
- Modify: `backend/internal/pkg/apicompat/**`
- Modify: `backend/internal/service/gateway_*.go`
- Modify: `backend/internal/service/openai_*.go`
- Modify: `backend/internal/service/openai_ws_v2/**`

**Interfaces:**
- Consumes: approved auth/settings decisions, local capture hooks and upstream OpenAI/Responses/WS/failover repairs
- Produces: one request lifecycle with compatible conversion, bounded retry/failover, exact-once usage/capture and preserved local headers/proxy behavior

- [ ] **Step 1: Add combination tests before resolving the shared control flow**

Cover these exact cases:

```text
Responses Lite reasoning normalization
Anthropic-to-Responses content-part and full-output events
pre-output failover versus in-stream error
client cancellation and first-output timeout
same-account retry without duplicate cache billing
custom header order: defaults -> legacy custom headers -> header_overrides
upstream user-agent history recording
TCP/TLS/SOCKS5 connect timeout through the configured transport
```

Run targeted packages with `GOMAXPROCS=2` and `-p 1`.

Expected: tests expose missing upstream fixes or lost local side effects before the final control flow is staged.

- [ ] **Step 2: Resolve entry-to-cleanup order explicitly**

The final order must document validation, authentication, model mapping, header/body rewrite, capture request snapshot, scheduling, external call, retry/failover, response capture, usage/billing and cleanup. Preserve local legacy headers, structured overrides, capture fields and proxy selection while adding approved upstream compatibility and timeout behavior.

- [ ] **Step 3: Test HTTP, SSE and WS paths and stage the checkpoint**

Run handler/service/apicompat/WS tests, including no-cache reruns for changed paths. Explicitly stage gateway/OpenAI/transport paths.

Expected: no duplicate charge/capture, no response after committed output failover, and no unmerged shared gateway entry remains.

### Task 7: Preserve KIRO and integrate scheduler changes

**Files:**
- Modify: `backend/internal/pkg/kiro/**`
- Modify: `backend/internal/pkg/kirocooldown/**`
- Modify: `backend/internal/service/scheduler_snapshot_service.go`
- Modify: `backend/internal/service/openai_account_scheduler.go`
- Modify: `backend/internal/repository/scheduler_cache.go`
- Modify: `backend/internal/domain/constants.go`
- Modify: KIRO account/group/quota handlers and DTOs identified by the inventory

**Interfaces:**
- Consumes: local KIRO runtime and upstream scheduler/cache optimizations
- Produces: six-platform scheduler behavior with KIRO direct/relay/mixed buckets and upstream performance/reliability fixes

- [ ] **Step 1: Write KIRO invariant tests before accepting scheduler changes**

Add or retain tests asserting:

```text
schedulerSnapshotPlatforms includes KIRO
AllowedQuotaPlatforms includes KIRO
mixed KIRO groups build KIRO and Anthropic-compatible buckets
group lifecycle, full rebuild and retirement include KIRO
model-scoped cooldown does not evict unrelated KIRO models
quota metadata and LastUsedAt cache writes survive snapshot publication
direct/relay selection preserves profile ARN and machine identity
```

- [ ] **Step 2: Present KIRO-specific product decisions**

Ask whether new Composite groups may route to KIRO and whether profit control/scheduling thresholds apply to KIRO. Recommend explicit Composite routes only, no automatic Claude-to-KIRO inference, and keep profit control off for KIRO until a `KiroCredits` cost conversion is defined.

- [ ] **Step 3: Integrate approved behavior and upstream scheduler improvements**

Port model-scoped cooldown, scoped batch rebuild, snapshot payload reuse, allocation reduction, quota metadata preservation, LastUsedAt isolation, cancellation and exclusion diagnostics without adopting upstream's legacy-unsupported KIRO branches.

- [ ] **Step 4: Run KIRO and scheduler tests and stage the checkpoint**

Run:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/pkg/kiro ./internal/pkg/kirocooldown ./internal/service ./internal/repository -run 'Kiro|KIRO|Scheduler|Snapshot|Quota|Cooldown|LastUsed'
```

Expected: direct, relay, mixed scheduling and all cache/lifecycle tests pass.

### Task 8: Integrate Grok, Composite groups and provider feature gates

**Files:**
- Modify: `backend/internal/pkg/xai/**`
- Modify: `backend/internal/service/grok_*.go`
- Modify: `backend/internal/service/openai_gateway_grok*.go`
- Modify: `backend/internal/service/composite_*.go`
- Modify: `backend/internal/service/group*.go`
- Modify: `backend/internal/handler/grok_media.go`
- Modify: `backend/internal/server/routes/gateway.go`

**Interfaces:**
- Consumes: existing local Grok host split/custom URL/redaction policy and approved Composite/KIRO decisions
- Produces: final Grok OAuth/SSO/media/voice/search behavior and Composite routing without weakening local security or billing rules

- [ ] **Step 1: Lock current local Grok invariants with tests**

Assert default OAuth text versus media host selection, custom-base takeover of approved endpoints, fixed official auth endpoints, header denylist, redacted billing errors, bounded SSO workers, scheduler-cache media eligibility and no panic on missing OAuth clients.

- [ ] **Step 2: Present provider-surface and default decisions**

Ask which of Grok voice, TTS/STT, realtime, custom voices, web search and expanded video pricing are enabled. Present the mismatch between the v0.1.173 release note and pin code for cross-client model-mapping default, and ask for the desired default. Do not expose a deferred route or pricing field.

- [ ] **Step 3: Integrate the final upstream repair chains only**

Exclude the reverted initial async-image implementation, Grok account-wide 405 eviction and reverted global risk-control fail-closed behavior. Integrate only final SSO/session isolation, model-level cooldown, media ownership, retry, free-gate and billing behavior approved by the user.

- [ ] **Step 4: Test and stage Grok/Composite paths**

Run Grok SSO/OAuth/custom URL/header/media/search/voice tests, Composite route/alias/platform tests and group lifecycle tests. Explicitly stage resolved paths.

Expected: local host/redaction behavior is preserved; final defaults match the user's decisions.

### Task 9: Build the combined usage and billing contract

**Files:**
- Modify: `backend/internal/repository/usage_log_repo_insert.go`
- Modify: `backend/internal/repository/usage_log_repo_query.go`
- Modify: `backend/internal/pkg/usagestats/usage_log_types.go`
- Modify: `backend/internal/service/billing_service.go`
- Modify: `backend/internal/service/gateway_usage_billing.go`
- Modify: `backend/internal/service/openai_gateway_usage.go`
- Modify: `backend/internal/service/pricing_service.go`
- Modify: usage DTO/mappers and tests identified by the inventory

**Interfaces:**
- Consumes: final schema, local KIRO/long-context/reward billing and upstream image/session/response-model fields
- Produces: ordered 60-column usage inserts and approved response-model pricing semantics

- [ ] **Step 1: Add an exact usage-column contract test**

Create `TestUsageLogInsertColumnContractIncludesLocalAndUpstreamFields`. It must compare the full ordered column list, argument-type count, continuous placeholders and single/batch/prepared paths. It must include `kiro_credits`, image input token/cost, session ID and upstream response model/mismatch fields.

Run:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/repository -run 'UsageLogInsertColumnContract|UsageLog.*Placeholder'
GOMAXPROCS=2 go -C backend test -tags=integration -p 1 ./internal/repository -run 'UsageLog'
```

Expected before integration: the 60-column assertion fails; after integration all real PostgreSQL insert variants pass.

- [ ] **Step 2: Present response-model billing and multiplier decisions**

Separate observation from billing. Present requested, mapped, forwarded and upstream-response models, mismatch handling, opt-in channel rules, inability to increase an existing fee, KIRO/WebChat/capture implications and automatic upstream multiplier writeback. Ask for explicit choices before changing charge selection or account multipliers.

- [ ] **Step 3: Implement the approved cost precedence and exact-once behavior**

Preserve local long-context account snapshot, provider actual cost, KIRO credits, reward/account-stat allocation and billing-failure usage records. Add approved image-input and response-model behavior with monetary quantization. Ensure retry/failover cannot duplicate usage or charging.

- [ ] **Step 4: Test and stage usage/billing paths**

Run default/unit/integration usage, billing, long-context, KIRO credits, response-model, image-input and account-stat tests. Explicitly stage paths.

Expected: the final insert contract is 60 columns and billing behavior matches the recorded user decision.

### Task 10: Integrate subscriptions, payments and rewards

**Files:**
- Modify: `backend/internal/server/middleware/api_key_auth.go`
- Modify: `backend/internal/service/subscription*.go`
- Modify: `backend/internal/repository/user_subscription_repo.go`
- Modify: `backend/internal/service/payment*.go`
- Modify: `backend/internal/payment/provider/stripe.go`
- Modify: `backend/internal/payment/provider/easypay.go`
- Preserve: local IkunPay provider files and routes
- Modify: reward/affiliate integration points identified by the inventory

**Interfaces:**
- Consumes: local universal subscription and reward ledger plus upstream renewal/refund/idempotency fixes
- Produces: one atomic subscription/payment/reward flow with unchanged local public contracts unless explicitly approved

- [ ] **Step 1: Add local invariant tests before resolving shared payment code**

Cover universal routed-group subscription lookup, virtual seats, reward consumption order, hold/release, first-recharge idempotency, public plans minimal fields, IkunPay GET/POST webhooks and absence of user self-service refund routes.

- [ ] **Step 2: Present payment decisions**

Ask whether to restore upstream user refund-request routes, adopt require-force insufficient-balance refund semantics and enable Alipay mobile deep links. Stripe refund idempotency and EasyPay UTF-8 preservation may be integrated independently but still receive payment-focused review.

- [ ] **Step 3: Merge subscription and payment fixes without reverting local architecture**

Retain `ResolveActiveSubscriptionForRoutedGroup`; do not restore conditional legacy subscription lookup. Integrate approved expiry-window, midnight reset, row-lock renewal, Stripe idempotency, EasyPay encoding and refund behavior while preserving public plans, IkunPay and local fulfillment concurrency.

- [ ] **Step 4: Test and stage subscription/payment/reward paths**

Run default/unit/integration tests for middleware, subscription repositories/services, reward credits, affiliate, check-in, payment providers, public plans and routes.

Expected: no local route or reward ordering disappears; approved monetary transitions are transactional and idempotent.

### Task 11: Preserve WebChat, capture and public API behavior

**Files:**
- Modify: `backend/internal/server/routes/user.go`
- Modify: `backend/internal/handler/gateway_handler*.go`
- Modify: WebChat handlers/services/repositories identified by `rg -l 'WebChat|webchat' backend`
- Modify: capture handlers/services/repositories identified by `rg -l 'Capture|capture' backend`
- Modify: public settings/payment/model handlers and tests

**Interfaces:**
- Consumes: resolved gateway/auth/settings/billing behavior
- Produces: WebChat and capture paths that observe the same request lifecycle without exposing keys or duplicating provider work

- [ ] **Step 1: Add cross-domain WebChat and capture tests**

Cover native Responses streaming, attachments/artifacts, cancellation, title generation, hidden API keys, retry/switch capture, non-stream/error capture, redaction, truncation, bounded queue loss and KIRO credits.

- [ ] **Step 2: Apply the approved prompt-audit decision to WebChat**

If WebChat/title audit is enabled, route it through the same approved audit contract without duplicate upstream dispatch. If deferred, assert prompt audit does not intercept those routes. Preserve ClickHouse capture independently.

- [ ] **Step 3: Preserve public API minimization and cache invalidation**

Keep unauthenticated public plans and model/pricing endpoints limited to public fields, retain cache invalidation after plan/group updates and preserve `alvin` public setting behavior.

- [ ] **Step 4: Test and stage WebChat/capture/public paths**

Run backend WebChat/capture/public API tests and the frontend WebChat suite:

```bash
GOMAXPROCS=2 go -C backend test -p 1 ./internal/handler ./internal/service ./internal/repository -run 'WebChat|Capture|PublicPlan|Public.*Setting|Alvin'
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend-webchat
```

Expected: all local contracts pass with the resolved shared gateway.

### Task 12: Integrate frontend, i18n, branding and deployment

**Files:**
- Modify: `frontend/src/api/**`
- Modify: `frontend/src/components/**`
- Modify: `frontend/src/views/**`
- Modify: `frontend/src/router/**`
- Modify: `frontend/src/i18n/**`
- Modify: `frontend/src/types/**`
- Modify: `frontend/package.json`
- Regenerate: `frontend/pnpm-lock.yaml`
- Modify: `frontend/index.html`
- Modify: `deploy/Caddyfile`
- Modify: `deploy/docker-compose*.yml`
- Modify: `deploy/config.example.yaml`
- Modify: `deploy/install.sh`
- Modify: `Dockerfile`

**Interfaces:**
- Consumes: all approved backend DTO/routes/settings and local visual/deployment invariants
- Produces: one typed frontend contract and local deployment defaults with approved upstream features

- [ ] **Step 1: Merge API/types before UI surfaces**

Preserve KIRO in account/group/platform unions, WebChat/getting-started routes, public plans, local payment types and local settings. Add only backend-approved new fields and endpoints.

- [ ] **Step 2: Merge locale modules as an explicit union**

Keep local `gettingStarted` and `webchat` modules and add approved upstream modules such as `batchImage` and `channelMonitorV2`. Run locale compilation tests after every locale-index edit.

- [ ] **Step 3: Apply approved feature visibility and preserve branding**

Do not expose deferred Passkey/captcha/Live/Grok/monitor/payment controls. Preserve LINX2 title, favicon, logo, sidebar/home behavior and sanitize runtime branding URLs.

- [ ] **Step 4: Reconcile dependencies and regenerate the lockfile**

Keep the approved direct dependency versions, add required overrides, then run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend install --lockfile-only
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend install --frozen-lockfile
```

Expected: lockfile is generated, not text-merged, and install is reproducible.

- [ ] **Step 5: Merge deployment hardening without replacing local defaults**

Integrate approved cross-build, SSE buffering, request-size, no-new-privileges, Redis and install hardening while retaining `127.0.0.1:8080`, local Caddy domains/tests, `ghcr.io/lingchaojie/sub2api`, `SUB2API_IMAGE`, `UPDATE_GITHUB_REPO`, KIRO/capture/proxy environment variables and local repository URLs.

- [ ] **Step 6: Test and stage frontend/deploy paths**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run test:run
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend
COREPACK_ENABLE_PROJECT_SPEC=0 make build-frontend
/bin/bash -n deploy/apple-container.sh
/bin/bash deploy/tests/apple-container-test.sh
./deploy/test-caddyfile-cache.sh
```

Expected: frontend and deployment contracts pass, with no upstream branding/default replacement.

### Task 13: Audit all auto-merged and upstream-only production regions

**Files:**
- Review: every path in `/tmp/tokenstation3-upstream-sync-shared.txt`
- Review: every upstream-only production path from the Task 2 domain map
- Modify: only paths where the audit finds a semantic defect

**Interfaces:**
- Consumes: all staged functional checkpoints and the complete inventory
- Produces: written coverage evidence that every production region was traced through callers, callees, state, side effects and failures

- [ ] **Step 1: Four-way review every shared path**

For each path compare merge base, ours, theirs and current result. Record entry point, parameter/config source, state reads/writes, external calls, response consumers, error/retry/cancel/cleanup and local invariant coverage.

- [ ] **Step 2: Review upstream-only production paths for reachability and policy**

Confirm every new route/service/worker is wired only when its product decision is approved, has bounded concurrency/timeouts, uses configured proxy policy, redacts secrets and has matching frontend/config/tests.

- [ ] **Step 3: Repair findings test-first and restage exact paths**

For each defect, add a regression that fails against the current result, make the minimal semantic correction, rerun the owning domain tests and explicitly stage the corrected paths.

- [ ] **Step 4: Prove inventory closure**

Expected: every inventory path has one completed review record; `git diff --name-only --diff-filter=U` is empty; no unstaged production file is unexplained.

### Task 14: Run full verification, archive and create the merge commit

**Files:**
- Create: `docs/upstream-sync/2026-08-11-sub2api-0.1.173-0b3f.md`
- Modify: `docs/upstream-sync/README.md`
- Modify: any source/test file required by verified findings

**Interfaces:**
- Consumes: conflict-free staged merge result and all domain evidence
- Produces: complete local verification, synchronization archive and one two-parent merge commit

- [ ] **Step 1: Audit the pending tree before tests**

Run:

```bash
test -z "$(git diff --name-only --diff-filter=U)"
git diff --check
git diff --cached --check
if rg -n '^(<<<<<<<|=======|>>>>>>>)' --glob '!docs/superpowers/**' .; then exit 1; fi
git diff --summary HEAD
```

Expected: no unmerged entries, whitespace errors, conflict markers or unexplained mode changes.

- [ ] **Step 2: Run the complete local verification matrix sequentially**

Run:

```bash
GOMAXPROCS=2 make check-generate
GOMAXPROCS=2 make build-backend
GOMAXPROCS=2 go -C backend test -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=integration -p 1 ./...
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run test:run
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend
COREPACK_ENABLE_PROJECT_SPEC=0 make build-frontend
GOMAXPROCS=2 go -C backend test -tags=embed -p 1 ./internal/web
./deploy/test-caddyfile-cache.sh
```

Expected: every command exits zero. A failure is fixed or reproduced with the identical command on the fixed dev base; it is never waived by inspection alone.

- [ ] **Step 3: Write the synchronization archive from actual evidence**

Record dev base, backup ref, last upstream, merge base, pin/tag, commit/file counts, migration mapping and decisions, explicit/auto-merge coverage, exact test commands/results and pending review/push/CI status. Update the archive index.

- [ ] **Step 4: Stage only the archive paths and verify the complete index**

Run:

```bash
git add docs/upstream-sync/2026-08-11-sub2api-0.1.173-0b3f.md docs/upstream-sync/README.md
git diff --cached --check
git status --short
```

Expected: all intended merge files are staged; pre-existing user files and temporary audit output are absent.

- [ ] **Step 5: Create the required merge commit and prove its topology**

Run:

```bash
git commit -m "merge: sync sub2api upstream through 0b3fe95af"
git show -s --format='%H %P %s' HEAD
```

Expected: the commit has exactly two parents: the concrete effective `DEV_BASE` recorded in `/tmp/tokenstation3-upstream-sync-dev-base.txt` and `0b3fe95afd20aba77ee7649b37febb8255fb57a5`.

### Task 15: Independent full review and remediation loop

**Files:**
- Review: complete effective `DEV_BASE..HEAD`, with `DEV_BASE` read from `/tmp/tokenstation3-upstream-sync-dev-base.txt`
- Modify: only files required to close reviewer findings

**Interfaces:**
- Consumes: exact merge commit, parents, inventories and raw test results
- Produces: independent `SAFE TO PUSH` conclusion with complete conflict and auto-merge coverage

- [ ] **Step 1: Dispatch a fresh reviewer with raw coordinates only**

Provide worktree path, dev base, upstream pin, final HEAD, both merge parents, full changed range, explicit conflict list, auto-merged production list and raw verification results. Do not provide a safety conclusion.

- [ ] **Step 2: Require complete review output**

The reviewer must cover every production path, KIRO/WebChat/capture/payment/reward/settings/migration/security/billing/route invariants and tests, list findings by severity with file/line/evidence/impact/fix, and end with `SAFE TO PUSH` or `NOT SAFE TO PUSH`.

- [ ] **Step 3: Close every finding**

For each actionable issue, add a failing regression, implement the minimal fix, rerun affected and full shared-path tests, commit the fix, and dispatch a different fresh reviewer over the new exact range. Repeat until a reviewer reports no actionable issues and `SAFE TO PUSH`.

### Task 16: Recheck drift, advance local branches, publish without force and wait for exact-SHA CI

**Files:**
- No source modification unless CI finds a defect
- Final report records remote SHA, CI links and rollback point

**Interfaces:**
- Consumes: verified and independently approved merge tip
- Produces: remote dev/main state with successful required checks for the exact pushed dev SHA

- [ ] **Step 1: Refresh and apply every drift gate**

Run:

```bash
git fetch --prune --tags origin
git fetch --prune --tags upstream
```

Confirm origin/dev has not moved beyond the integrated base, upstream/main remains the fixed pin or ask whether to expand the range, and local main can fast-forward to origin/main. Any change requires user direction before publication.

- [ ] **Step 2: Show the exact refs that would be updated**

Display local/remote dev and main SHAs, the backup ref and the exact reviewed merge tip. Confirm publication does not require a force update. If pushing main can trigger a production deployment, apply the `AGENTS.md` production confirmation gate before the push.

- [ ] **Step 3: Fast-forward the checked-out local dev and local main safely**

First verify the original worktree still has exactly the pre-existing untracked
documents and no tracked edits. Verify the reviewed merge tip descends from
local dev, verify local main is an ancestor of `origin/main`, and then run:

```bash
git -C /home/alvin/tokenstation3 diff --quiet
git -C /home/alvin/tokenstation3 diff --cached --quiet
(cd /home/alvin/tokenstation3 && sha256sum -c /tmp/tokenstation3-preexisting-untracked.sha256)
git -C /home/alvin/tokenstation3 merge --ff-only merge/upstream-sub2api-20260811-0b3f
git branch -f main origin/main
(cd /home/alvin/tokenstation3 && sha256sum -c /tmp/tokenstation3-preexisting-untracked.sha256)
```

Expected: local `dev` equals the reviewed merge tip, local `main` equals
`origin/main`, and all pre-existing untracked documents remain byte-for-byte
untouched. If an untracked path would be overwritten, stop and ask rather than
moving or deleting it.

- [ ] **Step 4: Push dev and main without force**

Use a normal non-force push, preferably atomic when supported:

```bash
git push --atomic origin dev main
```

Expected: `origin/dev` resolves to the reviewed SHA and main is a fast-forward or no-op.

- [ ] **Step 5: Wait for all required checks on the exact pushed SHA**

Query GitHub checks for the pushed dev SHA until every required CI and security check reaches success. A failure starts a test-first repair, local revalidation, new independent review, new push and new exact-SHA wait.

- [ ] **Step 6: Report completion without mutating the green tip**

Report upstream pin, pushed dev SHA, main state, backup ref, local tests, independent reviewer conclusion and CI links. Do not add a post-CI archive commit unless the user explicitly requests it and accepts another full review/push/CI cycle.
