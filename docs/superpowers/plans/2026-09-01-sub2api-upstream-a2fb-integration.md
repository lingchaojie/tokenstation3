# sub2api Upstream a2fb Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task by task. Use `superpowers:test-driven-development` for every behavior change, `superpowers:systematic-debugging` for unexpected failures, and `superpowers:verification-before-completion` before any completion claim. The sync workflow additionally requires `sync-sub2api-upstream`; its independent-review and CI gates are mandatory.

**Goal:** Semantically integrate upstream `Wei-Shaw/sub2api` from local base `2bc139ab527b4a687546d145dc7bb9063cf14510` through pinned upstream commit `a2fb09260a955676f99cdc92f05469febee82a08`, delivering the approved catalog, ACL, observability, compatibility, and failover improvements while preserving TokenStation's local product contracts.

**Architecture:** Create a real two-parent merge in an isolated worktree. Resolve every changed path by comparing merge-base, local, upstream, and final result; retain the shared gateway state machine and local finalization boundary as the integration spine. Public-group authorization stays centralized, usage billing remains transactionally idempotent, and usage-log/capture submission remains best-effort at-most-once per handler finalization rather than being described as globally exactly-once.

**Tech Stack:** Go 1.27, Gin, Ent, PostgreSQL, Redis, Vue 3, TypeScript, Vite, Vitest, pnpm, Docker, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-01-sub2api-upstream-a2fb-integration-design.md`

## Global Constraints

- The immutable upstream pin is `a2fb09260a955676f99cdc92f05469febee82a08`; do not replace it with a moving ref.
- The last integrated upstream commit and merge-base must remain `2bc139ab527b4a687546d145dc7bb9063cf14510`.
- The pre-plan design commit is `fb5586126`; the plan commit may be the only commit between it and the merge branch base.
- Preserve local KIRO forwarding, capture/spool, unified-key behavior, and site-controlled pricing.
- Preserve `openai-default` and `anthropic-default` as active, standard, non-exclusive public groups with multiplier `1.0` and unchanged default routing semantics.
- Include public-group restrictions, Codex catalog refresh, WebSocket/429 fixes, usage observability, and cross-provider compatibility/failover fixes.
- Exclude the `.s2plugin` runtime/product, Composite platform, Grok Voice/TTS/STT/Realtime/custom voices/audio billing, independent `/x_search`, and upstream billing-rate writeback.
- Do not globally remove symbols named `realtime`; operational realtime traffic is not part of the excluded Grok Realtime product.
- Keep migration versions unique by mapping approved upstream migrations to `231`, `232`, and `233`; do not import upstream plugin migrations `229` or `230`.
- Do not change production, call production providers, force-push, or push any branch without a fresh explicit user authorization after the final packet is shown.
- Do not use blanket conflict choices such as `--ours`, `--theirs`, or path-wide copies as a substitute for semantic resolution.

## Responsibility Map

| Domain | Primary paths | Required proof |
| --- | --- | --- |
| Toolchain/schema | `.github/workflows/**`, `Dockerfile*`, `backend/go.*`, `backend/ent/**`, `backend/migrations/**` | Go 1.27 build/lint, unique migrations, generated code clean |
| Access/catalog | group/user services and repositories, DTOs, admin UI, model catalog | ACL matrix, default-group regression, Codex catalog tests |
| Usage/pricing | usage log, billing, pricing services, account stats | insert/query parity, idempotent billing, pricing provenance |
| Forwarding/failover | handlers, provider services, relay state, capture finalizer | state-machine branch tests, at-most-once finalization |
| Providers | OpenAI/Anthropic/Bedrock/Gemini/Antigravity/Kimi/Zhipu/DeepSeek/Grok/Ollama | provider-specific regression/race tests |
| Frontend/deploy | `frontend/**`, `deploy/**`, locale/navigation files | typecheck, lint, focused and full tests, build |

## Task 1: Protect the Workspace and Establish a Reproducible Baseline

**Files:**

- Verify: `.git/HEAD`
- Verify: `docs/superpowers/specs/2026-09-01-sub2api-upstream-a2fb-integration-design.md`
- Verify: `docs/superpowers/plans/2026-09-01-sub2api-upstream-a2fb-integration.md`
- Create in Step 3: `.worktrees/upstream-sub2api-20260901-a2fb/`

- [ ] **Step 1: Assert the plan commit is the only change after the approved design commit**

Run:

```bash
git status --short
test "$(git rev-parse HEAD^)" = fb5586126
test "$(git diff-tree --no-commit-id --name-only -r HEAD)" = docs/superpowers/plans/2026-09-01-sub2api-upstream-a2fb-integration.md
```

Expected: clean output from `git status`; both assertions exit `0`. Stop if the plan was amended with unrelated files.

- [ ] **Step 2: Refresh and verify all immutable coordinates**

Run:

```bash
git fetch origin dev main
git fetch upstream
git rev-parse origin/dev
git rev-parse upstream/master
git merge-base fb5586126 a2fb09260a955676f99cdc92f05469febee82a08
git rev-list --count 2bc139ab527b4a687546d145dc7bb9063cf14510..a2fb09260a955676f99cdc92f05469febee82a08
```

Expected: `origin/dev` is still `f768645be81754a170eaa48b8dd889692ef40473`; `upstream/master` contains the pinned commit; merge-base is exactly `2bc139ab527b4a687546d145dc7bb9063cf14510`; the pinned range has 420 commits including merges. If `origin/dev` moved, stop and recalculate the integration base rather than silently rebasing this plan.

- [ ] **Step 3: Create a recoverable backup ref and isolated merge worktree**

Run:

```bash
git branch backup/dev-before-upstream-sync-20260901-a2fb HEAD
git worktree add -b merge/upstream-sub2api-20260901-a2fb .worktrees/upstream-sub2api-20260901-a2fb HEAD
git -C .worktrees/upstream-sub2api-20260901-a2fb status --short
```

Expected: backup and merge refs point to the plan commit; the worktree is clean. If either ref already exists, verify its SHA before reusing it.

- [ ] **Step 4: Install pinned dependencies without editing lockfiles**

Run in the worktree:

```bash
go -C backend mod download
pnpm --dir frontend install --frozen-lockfile
```

Expected: both succeed and `git status --short` remains empty.

- [ ] **Step 5: Record baseline verification before the merge**

Run in the worktree:

```bash
go -C backend test -timeout=20m -tags=unit ./...
go -C backend test -timeout=20m -tags=integration ./...
make check-generate
make build-backend
pnpm --dir frontend lint:check
pnpm --dir frontend typecheck
pnpm --dir frontend test:run
pnpm --dir frontend build
```

Expected: every command succeeds. Save the commands, timestamps, and exit status in the sync archive created in Task 11. Treat any baseline failure as a pre-existing issue and diagnose it before merging.

## Task 2: Start the True Merge and Build a Complete Resolution Ledger

**Files:**

- Inspect: every path in `git diff --name-status 2bc139ab...a2fb09260`
- Inspect: every unmerged path from `git diff --name-only --diff-filter=U`
- Create in Task 11: `docs/upstream-sync/2026-09-01-sub2api-0.1.185-a2fb.md`

- [ ] **Step 1: Start, but do not commit, the pinned two-parent merge**

Run in the worktree:

```bash
git merge --no-ff --no-commit a2fb09260a955676f99cdc92f05469febee82a08
git rev-parse MERGE_HEAD
git diff --name-only --diff-filter=U
```

Expected: `MERGE_HEAD` is the exact upstream pin. The discovery simulation observed 153 explicit conflicts; a changed count requires investigation but is not itself permission to abort or weaken review.

- [ ] **Step 2: Generate the upstream-range and conflict inventories**

Run:

```bash
git diff --name-status 2bc139ab527b4a687546d145dc7bb9063cf14510 a2fb09260a955676f99cdc92f05469febee82a08
git diff --name-only --diff-filter=U
git log --no-merges --oneline 2bc139ab527b4a687546d145dc7bb9063cf14510..a2fb09260a955676f99cdc92f05469febee82a08
```

Expected: the pinned range covers 733 changed paths and 261 non-merge commits based on discovery. Assign every path to exactly one responsibility-map domain; record renames and deleted paths explicitly.

- [ ] **Step 3: Review every path with four-way evidence**

For each path, inspect the merge-base, local parent, upstream parent, and staged result:

```bash
git show 2bc139ab527b4a687546d145dc7bb9063cf14510:<path>
git show HEAD:<path>
git show a2fb09260a955676f99cdc92f05469febee82a08:<path>
git diff --cached -- <path>
```

Expected: the ledger states `accepted`, `adapted`, `preserved-local`, or `excluded` plus a concrete reason for all 733 paths. For added/deleted paths where `git show` is expected to fail, record `absent` rather than treating the failure as missing review.

- [ ] **Step 4: Resolve mechanical files only after their semantic owner is known**

Resolve independent documentation, formatting, and generated-file conflicts after their source-of-truth code is decided. Never hand-edit generated Ent/Wire output before resolving the schema and provider constructors that generate it.

## Task 3: Upgrade the Toolchain, Enforce Product Exclusions, and Reconcile Schema

**Files:**

- Modify: `.github/workflows/backend-ci.yml`
- Modify: `.github/workflows/build.yml`
- Modify: `Dockerfile`, `Dockerfile.backend`
- Modify: `backend/go.mod`, `backend/go.sum`
- Modify: `backend/ent/schema/*.go`
- Modify: `backend/ent/**` generated output
- Modify: `backend/internal/upstreamcontract/task8_rollout_contract_test.go`
- Create: `backend/migrations/231_add_usage_log_native_compaction_v2.sql`
- Create: `backend/migrations/232_add_usage_log_requested_reasoning_effort.sql`
- Create: `backend/migrations/233_user_restrict_public_groups.sql`
- Exclude: `backend/migrations/229_plugins.sql`
- Exclude: `backend/migrations/230_plugin_artifacts.sql`
- Exclude: plugin runtime/product paths enumerated below
- Exclude: `backend/internal/handler/composite_platform.go`
- Exclude: `backend/internal/service/composite_model_route.go`
- Exclude: `backend/internal/service/composite_platform.go`
- Exclude: `backend/internal/service/composite_route_resolver.go`
- Exclude: `backend/internal/handler/grok_audio.go`
- Exclude: `backend/internal/service/grok_audio.go`

- [ ] **Step 1: Extend the exclusion contract before changing imported code**

Add failing tests in `backend/internal/upstreamcontract/task8_rollout_contract_test.go` that scan tracked source, routes, Wire providers, frontend navigation, locales, migrations, and deployment defaults for forbidden product surfaces:

- plugin handlers, repositories, manager/runtime/package/manifest/compatibility/transport, `backend/pkg/pluginapi/**`, admin plugin API/view/store/navigation, `229_plugins.sql`, and `230_plugin_artifacts.sql`;
- Composite handler/service/resolver routes and provider registration;
- Grok Voice/TTS/STT/custom voices/audio billing and Grok product Realtime endpoints;
- independent `/x_search` routing;
- upstream billing-rate writeback jobs/endpoints.

The test must allow operational realtime metrics and unrelated words such as `composite index`; match import paths, types, route literals, migration names, and product registration sites rather than broad substrings.

Run:

```bash
go -C backend test -tags=unit ./internal/upstreamcontract -run 'Test.*Excluded|Test.*Rollout' -count=1
```

Expected: RED because the unfiltered upstream merge contains excluded registrations.

- [ ] **Step 2: Apply the Go 1.27 and CI/tooling upgrade**

Set the Go toolchain to `1.27.0` consistently in backend module metadata, Docker build images, and GitHub workflows. Set golangci-lint to `v2.13` wherever CI installs or invokes it. Preserve local build arguments, image labels, caching, and deployment entrypoints while accepting upstream security and reproducibility changes.

Run:

```bash
go -C backend version
go -C backend mod tidy
make build-backend
```

Expected: Go reports `go1.27.x`; module tidy and backend build succeed.

- [ ] **Step 3: Remove every excluded product surface and its dependency closure**

Remove upstream-only plugin files under:

```text
backend/internal/handler/admin/plugin_handler.go
backend/internal/repository/plugin_repo.go
backend/internal/repository/plugin_repo_integration_test.go
backend/internal/service/openai_plugin_transport.go
backend/internal/service/plugin_compatibility.go
backend/internal/service/plugin_compatibility_test.go
backend/internal/service/plugin_manager.go
backend/internal/service/plugin_manager_test.go
backend/internal/service/plugin_manifest.go
backend/internal/service/plugin_package.go
backend/internal/service/plugin_package_test.go
backend/internal/service/plugin_runtime.go
backend/internal/service/plugin_runtime_integration_test.go
backend/internal/service/plugin_runtime_security_test.go
backend/pkg/pluginapi/**
docs/PLUGIN_DEVELOPMENT.md
frontend/src/api/admin/plugins.ts
frontend/src/views/admin/PluginsView.vue
frontend/src/views/admin/PluginsView.test.ts
```

Also remove plugin registrations from config, routes, Wire, router/sidebar, stores, deploy files, and locale keys. Remove the listed Composite and Grok audio files plus their exact registrations. Retain supported Grok chat/reasoning/image behavior and operational realtime traffic metrics.

Run:

```bash
go -C backend test -tags=unit ./internal/upstreamcontract -run 'Test.*Excluded|Test.*Rollout' -count=1
```

Expected: GREEN, proving no excluded runtime or UI entrypoint remains.

- [ ] **Step 4: Write migration tests for the approved version map**

Add or extend migration tests to require exactly one file per version and this order:

```text
229_capture_spool_alert_rules.sql
230_channel_image_input_price.sql
231_add_usage_log_native_compaction_v2.sql
232_add_usage_log_requested_reasoning_effort.sql
233_user_restrict_public_groups.sql
```

The tests must fail if plugin migrations appear or if the SQL and Ent schemas disagree on nullability/defaults.

Run:

```bash
go -C backend test -tags=unit ./migrations/... -count=1
```

Expected: RED until the three remapped migrations and schema fields are correct.

- [ ] **Step 5: Recreate only the approved upstream migrations under unique local versions**

Create:

- `231_add_usage_log_native_compaction_v2.sql` for `native_compaction_v2 BOOLEAN NOT NULL DEFAULT FALSE` plus the upstream column comment, with no extra index;
- `232_add_usage_log_requested_reasoning_effort.sql` for nullable `VARCHAR(20)` requested reasoning effort with no default and no backfill;
- `233_user_restrict_public_groups.sql` for `restrict_public_groups BOOLEAN NOT NULL DEFAULT FALSE`; reuse the existing `allowed_groups` edges and do not create or rewrite group membership rows.

Do not edit already-deployed migrations `001` through `230`. Preserve local capture schema and local `channel_image_input_price` at versions `229` and `230`.

- [ ] **Step 6: Reconcile Ent source schemas, then regenerate Ent and Wire**

Add only fields and edges required by included capabilities. Remove all plugin/Composite/Grok-audio schema references. Resolve service constructors and `backend/internal/cmd/wire.go`, then run:

```bash
make generate
make check-generate
go -C backend test -tags=unit ./migrations/... ./ent/... ./internal/upstreamcontract -count=1
go -C backend test ./internal/cmd/... -count=1
```

Expected: generation is reproducible, migration/schema tests are GREEN, exclusion tests are GREEN, and Wire compiles without excluded providers.

- [ ] **Step 7: Verify empty-database and upgraded-database migration paths on PostgreSQL**

Run the repository migration integration suite against a disposable PostgreSQL database, first from an empty schema and then from a fixture/schema migrated through local version `230`. Assert both paths reach the same Ent schema, preserve existing default-group/API-key/user-group rows, and may be rerun without error.

Run:

```bash
go -C backend test -tags=integration ./internal/repository -run 'Test.*Migration|Test.*Schema' -count=1
```

Expected: both schema paths pass; versions `231`, `232`, and `233` are applied once in order; no plugin table appears.

- [ ] **Step 8: Stage this domain and inspect it before moving on**

Run:

```bash
git add .github Dockerfile Dockerfile.backend backend/go.mod backend/go.sum backend/ent backend/migrations backend/internal/cmd backend/internal/upstreamcontract
git diff --cached --check
git diff --cached --stat
```

Expected: no whitespace errors; staged changes contain the toolchain/schema decisions but no accidental product resurrection.

## Task 4: Integrate Public-Group Restrictions Without Changing Default Groups

**Files:**

- Modify: `backend/internal/service/group_service.go`
- Modify: `backend/internal/service/user.go`
- Modify: `backend/internal/service/user_service.go`
- Modify: `backend/internal/service/admin_user.go`
- Modify: `backend/internal/service/api_key_service.go`
- Modify: `backend/internal/repository/user_repo.go`
- Modify: `backend/internal/repository/simple_mode_default_groups.go`
- Modify: `backend/internal/handler/admin/user_handler.go`
- Modify: `frontend/src/components/admin/user/UserEditModal.vue`
- Modify: `frontend/src/components/admin/user/UserAllowedGroupsModal.vue`
- Modify: `frontend/src/components/admin/user/__tests__/UserEditModal.spec.ts`

- [ ] **Step 1: Write the central authorization matrix as table-driven tests**

Add tests for the canonical `(*User).CanBindGroup(groupID int64, isExclusive bool)` decision with these rows:

| Group/user state | Expected |
| --- | --- |
| standard public group, user unrestricted | allow |
| standard public group, restricted user, group in allow-list | allow |
| standard public group, restricted user, group absent from allow-list | deny |
| exclusive group, membership/ownership grants access | allow |
| exclusive group, no membership/ownership | deny |
| group removed from `AllowedGroups` while restricted | deny |

Also test that an omitted restriction pointer in an update request means “leave unchanged,” while an explicit `false` clears the restriction and its effective allow-list behavior.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/repository -run 'Test.*CanBindGroup|Test.*PublicGroupRestriction' -count=1
```

Expected: RED against the unresolved merge.

- [ ] **Step 2: Add default-group regression tests before modifying authorization code**

Exercise the real simple-mode creation and selection paths. Separately test that service/repository callers reject inactive or deleted groups before invoking the user predicate. Assert both `openai-default` and `anthropic-default` remain:

- active;
- standard/public rather than exclusive;
- multiplier `1.0`;
- selectable by an unrestricted user through the same default routing behavior as before the merge.

Add a restricted-user case proving defaults are denied only when the new restriction is explicitly enabled and the group is absent from that user's allow-list.

Run:

```bash
go -C backend test -tags=unit ./internal/repository ./internal/service -run 'Test.*DefaultGroup|Test.*SimpleMode' -count=1
```

Expected: the existing default rows stay GREEN; the new restricted-user case is RED until the central predicate is implemented.

- [ ] **Step 3: Implement one canonical public-group decision**

Port the upstream restriction field, repository query/update fields, handler DTOs, and admin operations. Keep the existing `AllowedGroups` representation. Make `(*User).CanBindGroup(groupID, isExclusive)` the sole user-level source of truth for:

- API-key/group binding;
- user default-group selection;
- request-time group validation;
- admin previews and assignment lists.

Do not duplicate the allow-list logic inside handlers. Preserve the pre-existing exclusive-group rules and apply public restrictions as an additional rule only for standard/public groups.

- [ ] **Step 4: Invalidate every affected authorization cache**

On user restriction changes and allow-list mutations, invalidate cached user authorization, group binding, and any derived selectable-group list. Add a test that warms the cache, changes the allow-list, and observes the new result without waiting for TTL expiry.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/repository -run 'Test.*CanBindGroup|Test.*PublicGroupRestriction|Test.*DefaultGroup|Test.*AuthorizationCache' -count=1
```

Expected: GREEN for the complete ACL and default-group suite.

- [ ] **Step 5: Wire the admin UI with lossless optional-field semantics**

Update TypeScript types, user/group API payloads, and admin forms so the restriction flag and allow-list round-trip. Preserve “omitted” versus explicit `false`; show standard/public groups only in the new allow-list picker and retain exclusive-group management in its existing UI.

Run:

```bash
pnpm --dir frontend typecheck
pnpm --dir frontend exec vitest run src/components/admin/user/__tests__/UserEditModal.spec.ts
```

Expected: types compile and user/group form tests prove load-edit-save-reload parity.

## Task 5: Integrate Usage Observability While Preserving Honest Idempotency Boundaries

**Files:**

- Modify: `backend/ent/schema/usage_log.go`
- Modify: `backend/internal/repository/usage_log_repo.go`
- Modify: `backend/internal/repository/usage_log_repo_insert.go`
- Modify: `backend/internal/repository/usage_log_repo_query.go`
- Modify: `backend/internal/repository/usage_log_repo_stats.go`
- Modify: `backend/internal/repository/usage_log_repo_trend.go`
- Modify: `backend/internal/repository/usage_billing_repo.go`
- Modify: `backend/internal/service/usage_log.go`
- Modify: `backend/internal/service/usage_log_helpers.go`
- Modify: `backend/internal/handler/gateway_forward_side_effects.go`
- Modify: `backend/internal/handler/admin/usage_handler.go`
- Modify: `backend/internal/handler/admin/usage_query_cache.go`
- Modify: `frontend/src/api/admin/usage.ts`
- Modify: `frontend/src/api/usage.ts`
- Modify: `frontend/src/components/admin/usage/UsageFilters.vue`
- Modify: `frontend/src/components/admin/usage/UsageTable.vue`
- Modify: `frontend/src/views/admin/UsageView.vue`
- Modify: `frontend/src/views/user/UsageView.vue`
- Modify: usage labels in `frontend/src/i18n/locales/en/admin/` and `frontend/src/i18n/locales/zh/admin/`

- [ ] **Step 1: Write one ordered-field contract covering every insert and query path**

Define a test fixture that includes all newly approved fields in this order:

```text
service_tier
reasoning_effort
requested_reasoning_effort
inbound_endpoint
upstream_endpoint
channel_id
account_id
session_id
native_compaction_v2
created_at
```

Test normal insert, streaming finalization insert, batch insert, admin list, export, aggregation scan, and any raw SQL compaction/archive query. Each path must persist or scan the same field set with matching types and nullability.

Run:

```bash
go -C backend test -tags=unit ./internal/repository ./internal/service -run 'Test.*UsageLog.*Field|Test.*UsageLog.*RoundTrip' -count=1
```

Expected: RED wherever upstream added a field to only part of the SQL/Ent surface.

- [ ] **Step 2: Preserve billing deduplication and test retry behavior**

Keep the `usage_billing_dedup` transaction in `usage_billing_repo.go`. Add a concurrent/retry test that submits the same billing identity twice and proves exactly one balance mutation and one billing-ledger effect. Test a different identity bills independently, a reused identity with a different request fingerprint returns an error, and an injected transaction failure rolls back balance, subscription/quota, ledger, and dedup effects together.

Run:

```bash
go -C backend test -tags=unit ./internal/repository -run 'Test.*UsageBilling.*Dedup|Test.*UsageBilling.*Retry' -count=1
```

Expected: GREEN only when database billing remains transactionally idempotent.

- [ ] **Step 3: Preserve local finalization at-most-once behavior without overstating it**

Keep `gateway_forward_side_effects.go` as the local coupling point for usage submission and capture finalization. Preserve its `sync.Once` guard and add tests proving repeated handler finalization attempts invoke the usage and capture callbacks no more than once. Also test callback failure: the guard prevents a second local invocation, while usage-log fallback/dedup remains best-effort and is not part of the billing transaction.

Do not name this behavior globally “exactly once.” Comments and archive prose must distinguish:

- database billing: idempotent/transactional by billing identity;
- handler usage+capture finalization: at-most-once invocation in-process;
- usage-log durability: best-effort dedup/fallback, separate from billing atomicity.

Run:

```bash
go -C backend test -tags=unit ./internal/handler -run 'Test.*ForwardSideEffects|Test.*Finaliz' -count=1
```

Expected: GREEN for success, duplicate-finalize, and callback-failure cases.

- [ ] **Step 4: Add privacy-safe filters and native-compaction queries**

Port approved upstream filtering for service tier, reasoning effort, requested reasoning effort, channel/account, upstream endpoint, and native compaction. Enforce existing authorization scoping before applying filters; do not expose raw upstream credentials, proxy details, request bodies, or hidden capture metadata in usage APIs.

Add tests for admin versus non-admin visibility, nullable values, false/true native-compaction filtering, and time-order stability.

- [ ] **Step 5: Run the complete usage and billing domain suite**

Run:

```bash
go -C backend test -tags=unit ./internal/repository ./internal/service ./internal/handler -run 'Test.*Usage|Test.*Billing|Test.*ForwardSideEffects' -count=1
go -C backend test -tags=integration ./internal/repository ./internal/service -run 'Test.*Usage|Test.*Billing' -count=1
```

Expected: all usage fields round-trip, billing retries do not double-charge, and local capture/usage finalization remains at-most-once per handler lifecycle.

## Task 6: Integrate Pricing and Accounting Fixes Under Site-Controlled Precedence

**Files:**

- Modify: `backend/internal/service/pricing_service.go`
- Modify: `backend/internal/service/model_pricing_resolver.go`
- Modify: `backend/internal/service/custom_channel_time_pricing.go`
- Modify: `backend/internal/service/billing_context_schedule.go`
- Modify: `backend/internal/service/billing_service.go`
- Modify: `backend/internal/service/account_stats_pricing.go`
- Modify: `backend/internal/repository/pricing_service.go`
- Modify: `backend/internal/repository/channel_repo_pricing.go`
- Modify: `backend/internal/repository/channel_repo_account_stats_pricing.go`
- Modify: pricing configuration fields in `backend/internal/config/config.go`
- Modify: Model Plaza pricing DTO/API components under `backend/internal/handler/model_plaza_handler.go` and `frontend/src/components/modelPlaza/`

- [ ] **Step 1: Write precedence tests before accepting upstream pricing loaders**

Create table-driven tests for `pricing.override_file` and database/site configuration. Required order:

```text
explicit site/database override
override_file value
approved upstream built-in catalog
unsupported/fail-closed
```

Test explicit zero separately from absent, malformed files, duplicate model keys, reload behavior, and a built-in upstream catalog update. A malformed or missing optional file may fall through; an explicitly unsupported price must not silently become free.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/config -run 'Test.*Pricing.*Precedence|Test.*OverrideFile' -count=1
```

Expected: RED until the merged resolver preserves local site control.

- [ ] **Step 2: Add context-, weekday-, tier-, and bucket-aware pricing tests**

Cover long-context thresholds, weekday/weekend schedules, service tiers, reasoning versus requested reasoning, cached input, image input/output, media duration/size buckets, and models with different input/output provenance. Each test must assert both numeric result and source label.

Require per-bucket provenance: an input price from a site override must not make an absent output price appear site-defined. Missing required buckets fail closed with a typed error rather than charging zero.

- [ ] **Step 3: Implement the merged pricing resolver and preserve local KIRO behavior**

Adapt upstream catalog and normalization changes behind the existing local resolver. Keep site/database values authoritative and do not import upstream billing-rate writeback. Route KIRO model aliases and capture accounting through their existing local policy; add KIRO regression fixtures for normal, cached, and missing-price cases.

- [ ] **Step 4: Integrate account-statistics and provider-specific billing fixes**

Port approved media/account-stat aggregation and DeepSeek pricing corrections. Assert totals match the sum of persisted billing ledger entries across time ranges and that a provider retry/failover cannot create an extra charge.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/repository -run 'Test.*Pric|Test.*Rate|Test.*Cost|Test.*AccountStat|Test.*DeepSeek|Test.*Kiro' -count=1
go -C backend test -tags=integration ./internal/repository ./internal/service -run 'Test.*Billing|Test.*AccountStat' -count=1
```

Expected: pricing precedence, provenance, fail-closed behavior, provider fixes, and persisted totals all pass.

## Task 7: Integrate Codex Catalog, OpenAI Compatibility, WebSocket, and 429 Fixes

**Files:**

- Modify: `backend/internal/service/openai_codex_model_metadata.go`
- Modify: `backend/internal/service/openai_codex_models_service.go`
- Modify: `backend/internal/handler/openai_codex_models_handler.go`
- Modify: `backend/internal/service/openai_quota_service.go`
- Modify: `backend/internal/service/openai_account_scheduler.go`
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/service/openai_images.go`
- Modify: WebSocket helpers reached from `backend/internal/service/openai_gateway_service.go` and `backend/internal/handler/openai_gateway_handler.go`
- Modify: TTFT fields in settings/config DTOs and their existing admin frontend controls

- [ ] **Step 1: Write catalog snapshot and alias tests**

Add assertions for the approved v185 Codex/OpenAI model set, aliases, context limits, capabilities, reasoning support, service tiers, and deprecated-model behavior. Test catalog lookup through the actual channel/model resolver rather than only a constant slice.

Run:

```bash
go -C backend test -tags=unit ./internal/model/... ./internal/service/... -run 'Test.*Catalog|Test.*Codex.*Model|Test.*ModelAlias' -count=1
```

Expected: RED for newly added or corrected catalog entries.

- [ ] **Step 2: Write the quota and 429 classification matrix**

Table-test HTTP status, provider error code, response body class, and account state. At minimum distinguish:

- transient 429/rate limit: retry/fail over with bounded cooldown;
- exhausted quota/billing limit: disable or long-cooldown according to existing policy;
- invalid credentials: disable credential/account, not the entire channel unless policy requires it;
- malformed request/context limit: terminal client error, no account failover;
- provider overload/5xx: retry/fail over before response commitment.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/handler -run 'Test.*429|Test.*Quota|Test.*Availability|Test.*ErrorClass' -count=1
```

Expected: RED for cases fixed upstream but not yet semantically integrated.

- [ ] **Step 3: Add WebSocket lifecycle tests**

Test handshake failure, provider close before first payload, client close, ping/pong, cancellation, read/write concurrency, upstream retry before output, and no retry after any client-visible frame. Run relevant cases under the race detector.

Run:

```bash
go -C backend test -race -tags=unit ./internal/handler ./internal/service -run 'Test.*WebSocket|Test.*WS' -count=1
```

Expected: RED if the upstream lifecycle fixes are missing or conflict with local finalization.

- [ ] **Step 4: Integrate OpenAI/Codex normalization and availability fixes**

Port approved request/response normalization, catalog use, 429 classification, WebSocket fixes, image cooldown, and TTFT changes. Preserve local unified-key authentication, capture hooks, pricing resolution, public-group authorization, and post-commit retry prohibition.

- [ ] **Step 5: Verify this provider slice**

Run:

```bash
go -C backend test -tags=unit ./internal/model/... ./internal/service/... ./internal/handler/... -run 'Test.*OpenAI|Test.*Codex|Test.*429|Test.*Quota|Test.*WebSocket|Test.*Image.*Cooldown|Test.*TTFT' -count=1
go -C backend test -race -tags=unit ./internal/handler ./internal/service -run 'Test.*WebSocket|Test.*429|Test.*Failover' -count=1
```

Expected: catalog, quota classification, WebSocket lifecycle, image cooldown, and TTFT tests all pass without weakening local contracts.

## Task 8: Reconcile the Shared Failover State Machine With Capture Finalization

**Files:**

- Modify: `backend/internal/handler/failover_loop.go`
- Modify: `backend/internal/service/gateway_forward.go`
- Modify: `backend/internal/service/gateway_upstream_response.go`
- Modify: `backend/internal/service/gateway_upstream_transport_error.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/handler/gateway_forward_side_effects.go`
- Modify: `backend/internal/service/capture_record.go`
- Modify: `backend/internal/service/conversation_capture_pool.go`
- Modify: capture spool implementation under `backend/internal/capture/spool/`
- Modify: `backend/internal/service/gateway_anthropic_passthrough.go`
- Modify: `backend/internal/service/gateway_bedrock.go`

- [ ] **Step 1: Encode the response-commit state machine in failing tests**

Use the real shared forwarder and fake provider attempts. Assert this fixed order:

```text
select candidate
authorize group/model
resolve price
open provider attempt
classify pre-output result
either fail over or commit client output
finalize usage and capture once
```

Required branches:

| Branch | Expected behavior |
| --- | --- |
| provider fails before headers/body/frame | classify, release attempt, fail over if eligible |
| provider returns terminal client request error | no failover, preserve normalized terminal error |
| first client-visible byte/frame committed | freeze provider/account and forbid failover |
| provider fails after partial stream | terminate partial response, no replay |
| client disconnects | cancel provider context and finalize observed usage/capture once |
| provider omits usage | use approved estimator/fallback and mark provenance |
| price missing | fail closed before provider output where possible |
| finalization called twice | one local usage+capture callback invocation |

Run:

```bash
go -C backend test -tags=unit ./internal/handler ./internal/service -run 'Test.*Failover.*State|Test.*ResponseCommit|Test.*ForwardSideEffects' -count=1
```

Expected: RED until upstream compatibility fixes and the local boundary share one lifecycle.

- [ ] **Step 2: Centralize retry eligibility and response commitment**

Adapt provider-specific upstream fixes to the shared state object. Provider adapters may classify errors and normalize usage, but they must not independently replay a request after the shared state is committed. Ensure HTTP streaming, non-streaming, and WebSocket paths all mark commitment at their first client-visible output.

- [ ] **Step 3: Integrate Anthropic and Bedrock transport fixes**

Port approved header, event-stream, usage, tool-call, stop-reason, cache-token, and status normalization changes. Add tests for:

- HTTP 200 responses that contain a provider-level terminal error;
- truncated raw error bodies, retaining enough sanitized detail for classification;
- Bedrock event-stream exception frames before and after output commitment;
- Anthropic SSE with missing or late usage;
- cancellation propagation through proxy transport.

- [ ] **Step 4: Reattach capture/spool to the final state, not individual attempts**

Capture may record per-attempt metadata internally, but only the selected terminal lifecycle finalizes the user-visible capture and usage record. Preserve local redaction, spool retry, alerting, service/model allow-list, and unified-key metadata. Test that a pre-output failed attempt does not create a duplicate finalized capture, a partial committed stream is retained as partial rather than replayed, and no newly integrated provider is captured unless it is already explicitly enabled by the local capture allow-list.

- [ ] **Step 5: Verify all state-machine branches under normal and race runs**

Run:

```bash
go -C backend test -tags=unit ./internal/handler ./internal/service -run 'Test.*Failover|Test.*ResponseCommit|Test.*Capture|Test.*Anthropic|Test.*Bedrock|Test.*ForwardSideEffects' -count=1
go -C backend test -race -tags=unit ./internal/handler ./internal/service -run 'Test.*Failover|Test.*WebSocket|Test.*Capture|Test.*Cancel' -count=1
```

Expected: every pre/post-commit branch passes, no race is reported, and billing/capture finalization counts remain correct.

## Task 9: Integrate the Remaining Cross-Provider Compatibility Fixes

**Files:**

- Modify: API-compatible provider transport and normalization services
- Modify: Gemini and Antigravity services/tests
- Modify: Kimi, Zhipu, and DeepSeek services/tests
- Modify: supported Grok chat/reasoning/image services/tests
- Modify: Ollama services/tests
- Preserve: local KIRO service, handler, tests, and reference-tracking behavior

- [ ] **Step 1: Create provider contract fixtures before porting implementations**

For each included provider family, add request and response fixtures covering model mapping, system/developer messages, tools, reasoning, image/media input, streaming events, usage, finish reason, provider error, cancellation, and proxy transport where supported. Each fixture must assert normalized gateway output plus shared error class, not merely HTTP status.

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/handler -run 'Test.*APICompat|Test.*Gemini|Test.*Antigravity|Test.*Kimi|Test.*Zhipu|Test.*DeepSeek|Test.*Grok|Test.*Ollama' -count=1
```

Expected: RED for newly upstream-fixed cases.

- [ ] **Step 2: Port generic API-compatible transport fixes first**

Integrate endpoint joining, header forwarding/redaction, proxy selection, timeout/cancellation, content-type, SSE framing, raw-error truncation, usage fallback, and error normalization in shared API-compatible code. Keep secret-bearing headers out of logs and capture. Provider implementations should reuse these primitives instead of copying them.

- [ ] **Step 3: Integrate Gemini and Antigravity fixes**

Port approved model routing, thought/reasoning parts, tool schemas, image/media handling, stream usage, quota classification, and endpoint compatibility. Test HTTP 200 provider errors and malformed stream frames before/after commitment.

- [ ] **Step 4: Integrate Kimi, Zhipu, and DeepSeek fixes**

Port approved reasoning/tool/usage/error changes. Preserve the pricing decisions from Task 6 and the retry classifier from Task 8. Add regressions for missing usage, content-filter termination, invalid context, and transient provider overload.

- [ ] **Step 5: Integrate only supported Grok and Ollama behavior**

Accept Grok chat, reasoning, and image fixes that use the shared lifecycle. Reject all excluded Grok audio, voice, STT/TTS, custom voice, and product Realtime registrations. Port Ollama endpoint/model/stream/error compatibility without exposing local-network endpoints or credentials in usage/capture output.

- [ ] **Step 6: Prove KIRO remains behaviorally unchanged**

Run existing KIRO unit/integration contract tests and compare its routes, proxy use, unified-key context, capture hooks, and pricing calls against the local parent. This task is not a KIRO-upstream synchronization; do not replace local KIRO code with an unrelated upstream implementation.

- [ ] **Step 7: Run provider and concurrency verification**

Run:

```bash
go -C backend test -tags=unit ./internal/service ./internal/handler -run 'Test.*APICompat|Test.*Gemini|Test.*Antigravity|Test.*Kimi|Test.*Zhipu|Test.*DeepSeek|Test.*Grok|Test.*Ollama|Test.*Kiro' -count=1
go -C backend test -race -tags=unit ./internal/service ./internal/handler -run 'Test.*Stream|Test.*Cancel|Test.*Failover|Test.*WebSocket' -count=1
```

Expected: all included provider contracts pass, race tests are clean, supported Grok remains available, and excluded Grok products remain absent.

## Task 10: Complete Frontend and Deployment Integration

**Files:**

- Modify: `frontend/src/api/**` and shared TypeScript types
- Modify: admin usage, user, group, model, pricing, and channel views
- Modify: `frontend/src/locales/**`
- Modify: frontend router/sidebar/store only for included features
- Modify: `frontend/package.json`, `frontend/pnpm-lock.yaml` when required by accepted upstream code
- Modify: deployment examples and configuration docs under `deploy/**` and `docs/**`

- [ ] **Step 1: Add frontend round-trip and visibility tests first**

Cover:

- user restriction flag and public-group allow-list load/edit/save/reload;
- usage-table columns and filters for the approved observability fields;
- model/catalog and pricing display of nullable/provenance values;
- permission-based visibility;
- absence of plugin, Composite, Grok audio/voice/Realtime, and `/x_search` navigation or API clients.

Run:

```bash
pnpm --dir frontend test:run
```

Expected: RED for missing included fields, while exclusion assertions remain GREEN after Task 3.

- [ ] **Step 2: Integrate API types and UI behavior**

Port the approved upstream types/components into the existing local layouts. Preserve current simple-mode/default-group UX and local naming. Render absent pricing as unsupported/unknown rather than `0`, and retain optional booleans as `undefined` until the user changes them.

- [ ] **Step 3: Reconcile routes, navigation, stores, and locales**

Add only included screens and labels. Remove orphan imports and locale keys for excluded products. Verify every new locale key exists in each supported locale or uses the repository's established fallback policy.

- [ ] **Step 4: Regenerate the frontend lockfile deterministically if dependencies changed**

Run:

```bash
pnpm --dir frontend install
pnpm --dir frontend install --frozen-lockfile
```

Expected: the second install makes no changes. If accepted source requires no new dependency, keep the local lockfile except for semantic upstream conflict resolution.

- [ ] **Step 5: Reconcile deploy/config defaults without changing production**

Update examples, compose files, and documented environment variables for included functionality and Go/runtime changes. Do not edit live production state, introduce provider credentials, enable excluded products, or make public-group restrictions default-on for existing users.

- [ ] **Step 6: Run all frontend and deploy-facing checks**

Run:

```bash
pnpm --dir frontend lint:check
pnpm --dir frontend typecheck
pnpm --dir frontend test:run
pnpm --dir frontend build
make test-frontend-critical
make test-frontend-webchat
```

Expected: lint, types, all tests, production build, critical tests, and webchat tests pass.

## Task 11: Finish the Semantic Audit, Archive Evidence, Verify, and Create the Merge Commit

**Files:**

- Create: `docs/upstream-sync/2026-09-01-sub2api-0.1.185-a2fb.md`
- Modify: `docs/upstream-sync/README.md`
- Inspect: all 733 upstream-range paths and every locally changed merge result

- [ ] **Step 1: Complete the path ledger and four-way review**

For every upstream-range path, record domain, decision, and evidence. For every explicit conflict and every auto-merged path overlapping local post-base work, inspect:

```bash
git diff 2bc139ab527b4a687546d145dc7bb9063cf14510..HEAD -- <path>
git diff 2bc139ab527b4a687546d145dc7bb9063cf14510..a2fb09260a955676f99cdc92f05469febee82a08 -- <path>
git diff --cached -- <path>
```

Expected: no unassigned or unexplained path. Pay special attention to the 48 discovery paths overlapping local capture/forwarding work.

- [ ] **Step 2: Prove exclusions are closed over code, schema, UI, and deploy config**

Run the exclusion contract plus targeted `rg` searches for exact type/import/route/migration identifiers. Explain permitted matches such as operational realtime metrics; remove forbidden registrations rather than merely hiding navigation.

Run:

```bash
go -C backend test -tags=unit ./internal/upstreamcontract -count=1
git grep -n -E 'pluginapi|PluginsView|229_plugins|230_plugin_artifacts|CompositePlatform|/x_search'
```

Expected: contract tests pass. Any grep hit is reviewed and documented; executable forbidden product surfaces produce no hits.

- [ ] **Step 3: Write the upstream sync archive**

Document:

- origin/dev, design commit, plan commit, merge-base, immutable upstream pin, upstream tag description, commit/path counts;
- included and excluded features;
- migration remap and toolchain versions;
- public default-group invariants;
- exact wording for billing idempotency versus handler at-most-once finalization;
- conflict/auto-merge audit method and resolution ledger summary;
- all test commands, timestamps, results, and any justified allowed matches;
- independent-review status, push authorization state, and CI status.

Add the archive to `docs/upstream-sync/README.md` without deleting earlier sync history.

- [ ] **Step 4: Resolve all merge markers and stage the entire intended result**

Run:

```bash
git diff --name-only --diff-filter=U
git grep -n -E '^(<<<<<<<|=======|>>>>>>>)'
git status --short
git add --all
git diff --cached --check
```

Expected: no unmerged paths or conflict markers; all intended additions/deletions are staged; whitespace check passes.

- [ ] **Step 5: Run final verification sequentially**

Run in this order so failures have unambiguous ownership:

```bash
make check-generate
go -C backend test -timeout=20m -tags=unit ./...
go -C backend test -timeout=20m -tags=integration ./...
go -C backend test -race -tags=unit ./internal/handler ./internal/service ./internal/repository
go -C backend run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 run --timeout=30m ./...
make build-backend
/bin/bash -n deploy/apple-container.sh
/bin/bash deploy/tests/apple-container-test.sh
/bin/sh deploy/tests/docker-compose-security-test.sh
/bin/sh deploy/tests/docker-runtime-resources-test.sh
/bin/sh deploy/test-caddyfile-cache.sh
pnpm --dir frontend lint:check
pnpm --dir frontend typecheck
pnpm --dir frontend test:run
pnpm --dir frontend build
make test-frontend-critical
make test-frontend-webchat
```

Expected: every command exits `0`. Record exact output summaries in the archive and re-stage the archive if results changed it.

- [ ] **Step 6: Create and validate the real two-parent merge commit**

Run:

```bash
git commit -m "merge: sync sub2api upstream through a2fb09260"
git rev-list --parents -n 1 HEAD
test "$(git rev-parse HEAD^2)" = a2fb09260a955676f99cdc92f05469febee82a08
git status --short
```

Expected: the commit has exactly two parents, first parent is the plan commit, second parent is the immutable upstream pin, and the worktree is clean.

## Task 12: Obtain Fresh Independent Review and Close Every Actionable Finding

**Files:**

- Review: final merge commit and archive
- Modify after a concrete finding only: the exact file and test named by that finding; record each such path in the archive before editing it

- [ ] **Step 1: Give a fresh reviewer only immutable coordinates and contracts**

Following `sync-sub2api-upstream` and `superpowers:requesting-code-review`, ask an independent fresh subagent to audit:

- first parent SHA, second parent SHA, merge-base SHA, and final merge SHA;
- approved design/spec path and sync archive path;
- all 733 upstream paths, explicit conflicts, and overlapping local forwarding/capture paths;
- included/excluded feature contracts, default-group invariants, migration map, pricing precedence, and finalization semantics;
- verification evidence, with permission to run focused tests.

Do not give the reviewer a hand-selected diff or claim the merge is already safe.

- [ ] **Step 2: Require a machine-checkable verdict**

The reviewer must return findings with severity, file/line, violated contract, and reproduction/evidence. A passing verdict must contain exactly:

```text
NO ACTIONABLE ISSUES / SAFE TO PUSH
```

Expected: no vague approval and no unresolved high-, medium-, or low-severity correctness issue.

- [ ] **Step 3: Fix findings with TDD and repeat full verification**

For each valid finding, add or tighten a failing test, implement the smallest semantic correction, run the focused suite, then repeat Task 11 Step 5. Amend the unpushed merge commit only after checks pass, preserving both parents.

- [ ] **Step 4: Use a new reviewer after any fix**

Do not ask the original reviewer to validate its own repair loop. Provide the new final SHA and raw coordinates to a fresh independent reviewer and repeat until the exact safe-to-push verdict is returned. Record every review cycle and SHA in the archive.

## Task 13: Recheck Drift, Present the Publication Packet, and Wait for Authorization

**Files:**

- Modify only if evidence changes: `docs/upstream-sync/2026-09-01-sub2api-0.1.185-a2fb.md`

- [ ] **Step 1: Re-fetch and verify publication preconditions**

Run:

```bash
git fetch origin dev main
git fetch upstream
git rev-parse origin/dev
git rev-parse origin/main
git rev-parse upstream/master
git merge-base --is-ancestor a2fb09260a955676f99cdc92f05469febee82a08 upstream/master
git merge-base --is-ancestor origin/dev HEAD
git merge-base --is-ancestor origin/main origin/dev
```

Expected: upstream still contains the pin; remote `dev` still matches the expected ancestor; `main` can be advanced by fast-forward after `dev`. If any remote moved incompatibly, stop and re-audit instead of force-pushing.

- [ ] **Step 2: Present the final packet and request explicit push authorization**

Show the user:

- final merge SHA and its two parent SHAs;
- included/excluded feature summary;
- default-group and migration invariants;
- complete verification result;
- exact independent-review verdict;
- proposed non-force refs to push and the CI workflows that will run.

Stop here until the user explicitly authorizes the push. Prior approvals to design or implement do not authorize publication.

- [ ] **Step 3: After authorization, push `dev` without force and wait for exact-SHA CI**

Run only after fresh explicit authorization:

```bash
git push origin HEAD:dev
```

Wait for every required workflow associated with the exact pushed merge SHA. A newer unrelated green run does not count. If CI fails, diagnose, test, repair locally, obtain fresh independent review, and request renewed push authorization for the new SHA.

- [ ] **Step 4: Advance `main` only by the approved fast-forward workflow**

After `dev` CI is green and the user has explicitly authorized the production branch advance, fast-forward local `main` to the reviewed merge SHA and push without force. Never merge a different remote tip under the existing review verdict.

- [ ] **Step 5: Finalize the archive and clean up only recoverable local integration refs**

Record exact pushed SHAs and CI URLs/results. Remove the isolated worktree and merge branch only after both remote refs and archive evidence are verified. Retain the backup ref until the user agrees it is no longer needed; do not delete it as part of automatic cleanup.

If rollback is requested after publication, revert the merge with a normal first-parent mainline revert (`git revert -m 1 <merge-sha>`), then repeat focused/full verification, independent review, explicit push authorization, and exact-SHA CI. Do not rewrite published history.
