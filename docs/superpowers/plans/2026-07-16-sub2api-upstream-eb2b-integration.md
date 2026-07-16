# sub2api Upstream eb2b Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge upstream sub2api `7d239d62e8f1c6aea79164f88903f4158cbf2f98..eb2b8632ded614bf991d7d36abfa38b513ad8c2d` into local `dev` while preserving the approved local billing, Grok media, KIRO, WebChat, payment, settings, and deployment behavior.

**Architecture:** Perform one ordinary `--no-ff` merge in the isolated linked worktree, resolve every conflict semantically, regenerate Ent and Wire outputs, and audit both conflicted and automatically merged production regions. Because Git cannot create task commits while a merge is unresolved, Tasks 1–6 are executed inline and remain uncommitted until Task 7 creates the single two-parent merge commit; a fresh subagent performs the final read-only whole-branch review after local verification.

**Tech Stack:** Go 1.26, Ent, Wire, PostgreSQL SQL migrations, Redis, Gin, Vue 3, TypeScript, pnpm 9, Vitest, Docker Compose, Caddy, GitHub Actions.

## Global Constraints

- `DEV_BASE` is exactly `3e07e005e8d86271715471ed006006247ed8dc9d`.
- `LAST_UPSTREAM` and `MERGE_BASE` are exactly `7d239d62e8f1c6aea79164f88903f4158cbf2f98`.
- `UPSTREAM_PIN` is exactly `eb2b8632ded614bf991d7d36abfa38b513ad8c2d` (`v0.1.156-15-geb2b8632d`).
- The integration commit must be an ordinary non-squash merge whose first parent is `DEV_BASE` and second parent is `UPSTREAM_PIN`.
- Existing OpenAI parent accounts missing `extra.openai_long_context_billing_enabled` become `true`; explicit booleans remain unchanged; malformed values become `false`; new omitted values become `false`; Spark shadows inherit the parent value.
- Default Grok OAuth text/Responses use `https://cli-chat-proxy.grok.com/v1`; default Grok OAuth media uses `https://api.x.ai/v1`; an explicit validated custom `base_url` handles text, media, billing, quota, and monitor probes.
- `affiliate_admin_recharge_enabled` defaults to `false` and applies only to positive admin balance `add` operations.
- `server.enable_server_timing` defaults to `false` and must not expose timings on gateway, public payment, or webhook routes.
- Local consolidated settings stay in `backend/internal/service/setting_service.go` and `backend/internal/handler/admin/setting_handler.go`; upstream split files deleted by local `dev` remain deleted after their new semantics are folded in.
- KIRO direct/relay separation, Q chat/Q MCP/KRS profile ARN placement, persisted machine ID, credits, sticky/session, cooldown, refresh, custom headers, WebChat capture, and admin workflows remain intact.
- Existing local migrations are immutable; new upstream migrations are renamed in order to local numbers `184` through `189`.
- Use explicit path staging only. Do not use `git add -A`, force-push, squash, cherry-pick the upstream range, or edit production.

---

### Task 1: Start the ordinary merge and capture the resolution ledger

**Files:**
- Create: `.superpowers/upstream-eb2b/conflicts.initial`
- Create: `.superpowers/upstream-eb2b/upstream-files.txt`
- Create: `.superpowers/upstream-eb2b/auto-merged-production.txt`
- Modify: the paths produced by the merge in later tasks

**Interfaces:**
- Consumes: clean branch `merge/upstream-sub2api-20260716-eb2b` at `DEV_BASE` and approved design `docs/superpowers/specs/2026-07-16-sub2api-upstream-eb2b-integration-design.md`.
- Produces: one in-progress merge plus stable path inventories used by Tasks 2–7.

- [ ] **Step 1: Revalidate coordinates and worktree isolation**

Run:

```bash
test "$(git rev-parse HEAD)" = "3e07e005e8d86271715471ed006006247ed8dc9d"
test "$(git rev-parse eb2b8632ded614bf991d7d36abfa38b513ad8c2d)" = "eb2b8632ded614bf991d7d36abfa38b513ad8c2d"
test "$(git merge-base HEAD eb2b8632ded614bf991d7d36abfa38b513ad8c2d)" = "7d239d62e8f1c6aea79164f88903f4158cbf2f98"
test -f "$(git rev-parse --git-path commondir)"
git status --short
```

Expected: all `test` commands exit 0; status lists only the approved untracked spec and this plan.

- [ ] **Step 2: Start the merge without committing**

Run:

```bash
git merge --no-ff --no-commit eb2b8632ded614bf991d7d36abfa38b513ad8c2d
```

Expected: exit 1 with explicit conflicts and `.git/MERGE_HEAD` resolving to `UPSTREAM_PIN`; no commit exists yet.

- [ ] **Step 3: Capture conflict and audit inventories**

Run:

```bash
mkdir -p .superpowers/upstream-eb2b
git diff --name-only --diff-filter=U | sort > .superpowers/upstream-eb2b/conflicts.initial
git diff --name-only 7d239d62e8f1c6aea79164f88903f4158cbf2f98..eb2b8632ded614bf991d7d36abfa38b513ad8c2d | sort > .superpowers/upstream-eb2b/upstream-files.txt
comm -23 .superpowers/upstream-eb2b/upstream-files.txt .superpowers/upstream-eb2b/conflicts.initial | rg '^(backend|frontend/src|deploy)/' > .superpowers/upstream-eb2b/auto-merged-production.txt
wc -l .superpowers/upstream-eb2b/conflicts.initial .superpowers/upstream-eb2b/upstream-files.txt .superpowers/upstream-eb2b/auto-merged-production.txt
```

Expected: `conflicts.initial` contains 59 paths, `upstream-files.txt` is non-empty, and every production path not in the conflict list is present in the automatic-merge audit list.

### Task 2: Integrate migrations, Ent schema, and long-context billing

**Files:**
- Rename: `backend/migrations/174_add_usage_log_long_context_billing.sql` to `backend/migrations/184_add_usage_log_long_context_billing.sql`
- Rename: `backend/migrations/175_add_ops_system_logs_host.sql` to `backend/migrations/185_add_ops_system_logs_host.sql`
- Rename: `backend/migrations/175_default_openai_long_context_billing.sql` to `backend/migrations/186_default_openai_long_context_billing.sql`
- Rename: `backend/migrations/175a_add_ops_system_logs_host_index_notx.sql` to `backend/migrations/187_add_ops_system_logs_host_index_notx.sql`
- Rename: `backend/migrations/176_channel_monitor_grok_provider.sql` to `backend/migrations/188_channel_monitor_grok_provider.sql`
- Rename: `backend/migrations/177_add_subscription_plan_currency.sql` to `backend/migrations/189_add_subscription_plan_currency.sql`
- Modify: `backend/migrations/openai_long_context_billing_migration_test.go`
- Modify: `backend/internal/repository/openai_long_context_billing_migration_integration_test.go`
- Modify: `backend/ent/schema/subscription_plan.go`
- Modify: `backend/ent/schema/usage_log.go`
- Regenerate: `backend/ent/**`

**Interfaces:**
- Consumes: upstream usage-log snapshot field, currency field, ops-host migrations, Grok monitor migration, and long-context trigger.
- Produces: local migration sequence `184`–`189`, `subscription_plans.currency`, `usage_logs.long_context_billing_applied`, and account trigger semantics used by account and billing services.

- [ ] **Step 1: Rename the six migrations with explicit paths**

Run the six `git mv` commands matching the table above. Then run:

```bash
find backend/migrations -maxdepth 1 -type f -printf '%f\n' | sort -V | tail -n 18
```

Expected: `184` through `189` appear in execution order and no new `174`, `175`, `175a`, `176`, or `177` upstream filename remains.

- [ ] **Step 2: Write the local-policy migration tests before changing SQL**

Change `backend/migrations/openai_long_context_billing_migration_test.go` so both tests read `186_default_openai_long_context_billing.sql`, and add assertions that distinguish backfill from insert defaults:

```go
require.Contains(t, sql, "parent_account_id IS NULL")
require.Contains(t, sql, "'true'::jsonb")
require.Contains(t, sql, "ELSIF NOT (NEW.extra ? 'openai_long_context_billing_enabled')")
require.Contains(t, sql, "'false'::jsonb")
```

Extend `backend/internal/repository/openai_long_context_billing_migration_integration_test.go` with an existing parent that omits the field and assert it reads `true` after applying migration 186; insert a new OpenAI parent after the migration with omitted `extra` and assert it reads `false`.

- [ ] **Step 3: Run the focused tests and record RED**

Run:

```bash
cd backend && GOMAXPROCS=2 go test -p 1 ./migrations ./internal/repository -run 'TestMigration186|TestOpenAILongContextBillingMigration' -count=1
```

Expected: FAIL because tests still reference upstream migration numbering and/or upstream backfills missing existing parents to `false`.

- [ ] **Step 4: Implement the existing-on/new-off SQL policy**

In migration 186, keep the trigger branch for a new missing value at `false`, keep malformed values normalized to `false`, but make the one-time parent backfill use `true`:

```sql
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{openai_long_context_billing_enabled}',
    'true'::jsonb,
    true
)
WHERE platform = 'openai'
  AND parent_account_id IS NULL
  AND NOT (COALESCE(extra, '{}'::jsonb) ? 'openai_long_context_billing_enabled');
```

Run the same focused test command. Expected: PASS, or repository integration tests skip only because their documented PostgreSQL test dependency is unavailable.

- [ ] **Step 5: Resolve source schema and regenerate Ent**

Semantically combine the local subscription plan fields with upstream `currency`, retain the upstream usage-log long-context boolean, then run:

```bash
cd backend && GOMAXPROCS=2 go generate ./ent
```

Expected: generated Ent files contain both local fields and the upstream `currency`/`long_context_billing_applied` fields, with no conflict markers.

- [ ] **Step 6: Verify migration ordering and generated schema**

Run:

```bash
cd backend && GOMAXPROCS=2 go test -p 1 ./migrations ./ent/... -count=1
```

Expected: PASS with no duplicate migration number or generated schema error.

### Task 3: Fold settings, Server-Timing, ops, payment, and affiliate changes into local architecture

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Modify: `backend/internal/handler/admin/setting_handler.go`
- Delete after semantic fold: `backend/internal/handler/admin/setting_handler_audit.go`
- Delete after semantic fold: `backend/internal/handler/admin/setting_handler_update.go`
- Modify: `backend/internal/handler/dto/settings.go`
- Modify: `backend/internal/service/setting_service.go`
- Delete after semantic fold: `backend/internal/service/setting_features.go`
- Delete after semantic fold: `backend/internal/service/setting_parse.go`
- Delete after semantic fold: `backend/internal/service/setting_update.go`
- Modify: `backend/internal/service/settings_view.go`
- Modify: `backend/internal/service/ops_models.go`
- Modify: `backend/internal/repository/ops_repo.go`
- Modify: `backend/internal/handler/payment_handler.go`
- Modify: `backend/internal/service/payment_config_plans.go`
- Modify: `backend/internal/service/payment_config_service.go`
- Modify: `backend/internal/service/payment_order.go`
- Modify: `backend/internal/service/admin_user.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: `frontend/src/api/admin/settings.ts`
- Modify: `frontend/src/views/admin/SettingsView.vue`
- Modify: `frontend/src/views/admin/__tests__/SettingsView.spec.ts`

**Interfaces:**
- Consumes: `affiliate_admin_recharge_enabled`, `server.enable_server_timing`, plan currency, ops host filtering, and upstream timing middleware/client wrappers.
- Produces: consolidated settings DTO/update/audit behavior, safe timing authorization, display-only subscription currency, and positive-add-only affiliate rebates.

- [ ] **Step 1: Add focused regression assertions before resolving consolidated settings**

Keep upstream settings tests and add/retain assertions that the default settings response contains:

```go
"affiliate_admin_recharge_enabled": false
```

and that parsed config defaults to:

```go
require.False(t, cfg.Server.EnableServerTiming)
```

In `frontend/src/views/admin/__tests__/SettingsView.spec.ts`, assert loading `affiliate_admin_recharge_enabled: true` and saving the form returns the same boolean.

- [ ] **Step 2: Run focused tests and record RED**

Run:

```bash
cd backend && GOMAXPROCS=2 go test -p 1 ./internal/config ./internal/handler/admin ./internal/service ./internal/server -run 'Setting|AffiliateAdminRecharge|ServerTiming|SubscriptionPlanCurrency|Ops.*Host' -count=1
cd ../frontend && pnpm vitest run src/views/admin/__tests__/SettingsView.spec.ts
```

Expected: FAIL while consolidated handlers/services are unresolved or omit the new fields.

- [ ] **Step 3: Resolve configuration and consolidated settings**

Add `EnableServerTiming bool` under the existing server config with YAML key `enable_server_timing`. Fold upstream constants, defaults, read/update parsing, allowed-key validation, audit-key inclusion, DTO fields, and API contract fields into the local consolidated files. Remove the five modify/delete split files only after verifying each upstream-added symbol is present in its consolidated destination.

- [ ] **Step 4: Resolve timing authorization and client wrapping**

Keep upstream bounded aggregate SQL/Redis metrics, instantiate wrappers only when `EnableServerTiming` is true, and preserve route scoping so authenticated admin/user UI routes can emit metrics but gateway, payment public routes, and webhooks cannot. Verify timing output contains metric names, durations, and counts only.

- [ ] **Step 5: Resolve affiliate and payment semantics**

Integrate the setting into the positive admin `add` path only. Preserve the local balance mutation transaction; invoke existing affiliate accrual after a successful add; log accrual failure without rolling back the balance update; exclude `set`, `subtract`, zero, and negative values. Merge upstream `currency` as display metadata while preserving local public-plan visibility and payment-provider behavior.

- [ ] **Step 6: Run focused backend and frontend tests**

Run the commands from Step 2. Expected: PASS.

### Task 4: Integrate account lifecycle, Agent Identity, Grok URL policy, and scheduler behavior

**Files:**
- Modify: `backend/internal/handler/admin/account_handler.go`
- Modify: `backend/internal/handler/dto/mappers.go`
- Modify: `backend/internal/repository/account_repo.go`
- Modify: `backend/internal/repository/account_repo_temp_unsched_test.go`
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/account_credentials_redact.go`
- Modify: `backend/internal/service/account_usage_service.go`
- Modify: `backend/internal/service/domain_constants.go`
- Modify: `backend/internal/service/scheduler_snapshot_service.go`
- Modify: `backend/internal/service/token_refresh_service.go`
- Modify: `backend/internal/service/account_base_url_test.go`
- Add/retain: `backend/internal/service/openai_agent_identity*.go`
- Add/retain: `backend/internal/service/openai_agent_identity*_test.go`
- Add/retain: `backend/internal/handler/admin/account_codex_agent_identity_import_test.go`
- Modify: `backend/internal/service/wire.go`
- Regenerate: `backend/cmd/server/wire_gen.go`

**Interfaces:**
- Consumes: upstream Agent Identity import/runtime primitives, Grok URL/operator policy, header validation, scheduler leases/outbox changes, and local account/KIRO semantics.
- Produces: account objects that route text/media correctly, protect credentials, recover Agent Identity tasks once, and rebuild scheduler state without losing local platform behavior.

- [ ] **Step 1: Change the Grok media tests before production resolution**

In `TestGetGrokMediaBaseURLPinsOAuthMediaToCLIProxy`, rename the test to describe split routing and use these exact expectations:

```go
// Missing or stored CLI default: media uses the official API.
expected: xai.DefaultBaseURL

// Explicit custom base URL: media uses the custom URL.
expected: "https://custom.example.com/v1"

// API key: retain its configured media URL.
expected: "https://grok.example.com/v1"
```

Retain `TestGetGrokBaseURLUsesSubscriptionProxyForOAuth` expectations that default OAuth text uses `xai.DefaultCLIBaseURL` and explicit custom text uses the custom URL.

- [ ] **Step 2: Run the URL tests and record RED**

Run:

```bash
cd backend && GOMAXPROCS=2 go test -p 1 ./internal/service -run 'TestGetGrok(BaseURL|MediaBaseURL)' -count=1
```

Expected: FAIL because upstream routes default OAuth media to the CLI base.

- [ ] **Step 3: Implement the approved media split**

Make `Account.GetGrokBaseURL()` select the CLI default for OAuth text, the official default for API-key traffic, and an explicit validated custom URL for either type. Make `Account.GetGrokMediaBaseURL()` return the official default for OAuth credentials with no URL or a stored canonical CLI/official default, return an explicit validated custom URL unchanged, preserve API-key configuration, and return empty for non-Grok accounts.

- [ ] **Step 4: Resolve Agent Identity and account conflicts**

Adopt upstream PKCS#8 Ed25519 validation, runtime ID, optional task ID, account-proxy task registration, fresh `AgentAssertion`, parent credential resolution, per-account registration serialization, exactly-once invalid-task recovery, WebSocket invalidation, and private-key/task/assertion redaction. Preserve OAuth, PAT, API-key, Spark parent/shadow behavior, local KIRO credential handling, temporary-unschedulable behavior, cooldown state, and account usage fields.

- [ ] **Step 5: Resolve scheduler and token refresh behavior**

Combine upstream snapshot coalescing/lifecycle leases/expiry events/outbox latch/stale cleanup with local platform dimensions. Verify Agent Identity does not enter OAuth refresh, KIRO continues its existing refresh/machine-ID path, and Grok OAuth authorization/refresh stays on official auth endpoints regardless of custom forwarding URL.

- [ ] **Step 6: Regenerate Wire and run focused tests**

Run:

```bash
cd backend && GOMAXPROCS=2 go generate ./cmd/server
GOMAXPROCS=2 go test -p 1 ./internal/handler/admin ./internal/repository ./internal/service -run 'AgentIdentity|CodexImport|LongContextBilling|Grok(BaseURL|MediaBaseURL|Header|OAuth)|Scheduler|TempUnsched|TokenRefresh|Kiro' -count=1
```

Expected: generation succeeds and tests PASS.

### Task 5: Integrate OpenAI forwarding, Responses compatibility, WebChat, and shared KIRO paths

**Files:**
- Modify: `backend/internal/handler/openai_images.go`
- Modify: `backend/internal/service/openai_gateway_forward.go`
- Modify: `backend/internal/service/openai_gateway_messages_chat_fallback.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/openai_gateway_response_handling.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go`
- Audit: `backend/internal/handler/gateway_handler*.go`
- Audit: `backend/internal/service/openai_gateway*.go`
- Audit: `backend/internal/service/openai_images_responses.go`
- Audit: local WebChat/KIRO gateway tests and implementations selected by `rg -l 'KIRO|Kiro|kiro|WebChat|artifact|attachment' backend frontend/src`

**Interfaces:**
- Consumes: upstream Responses namespace/additional-tools changes, image preservation/finalization, Anthropic↔Chat bridge, failover/error/timeout/keepalive behavior, plus account URLs and Agent Identity assertions from Task 4.
- Produces: one coherent OpenAI/Codex/Grok forwarding pipeline that retains local WebChat artifact capture and KIRO routing contracts.

- [ ] **Step 1: Resolve explicit gateway conflicts by following complete request lifecycles**

For each conflicted function, compare stage 1/base, stage 2/local, stage 3/upstream, and final. Keep upstream image tool calls, JSON/SSE boundaries, namespace handling, additional tools, incomplete stop reasons, parallel-tool ghost delta handling, 5xx failover, sanitized errors, first-output timeouts, WebSocket first-message timeout, H2 keepalive, and direct Anthropic↔Chat bridge while retaining local response attachment/artifact capture and local error/usage hooks.

- [ ] **Step 2: Audit shared KIRO invariants against the reference guide**

Use `docs/kiro-upstream-sync.md` and verify exact final behavior:

```text
Q chat: profileArn in request body only
Q MCP: profile ARN in the established Q MCP header location
KRS: profile ARN in both required body/header locations
machine_id: persisted or derived by the existing kiro.rs-compatible path
API-key + empty base_url: direct AWS KIRO
API-key + non-empty base_url: external Anthropic-compatible relay
```

Follow all changed callers that create requests, attach headers, record credits, refresh credentials, emit cooldown, or capture WebChat data.

- [ ] **Step 3: Run focused forwarding regression tests**

Run:

```bash
cd backend && GOMAXPROCS=2 go test -p 1 ./internal/handler ./internal/pkg/apicompat ./internal/service -run 'OpenAI|Responses|ChatCompletions|Messages|Image|WebSocket|Passthrough|Failover|Kiro|KIRO|WebChat|Artifact|Attachment' -count=1
```

Expected: PASS with no leaked upstream bodies or credentials in error output.

### Task 6: Integrate frontend and deployment conflicts without dropping local product behavior

**Files:**
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/admin/account/AccountActionMenu.vue`
- Modify: `frontend/src/views/admin/AccountsView.vue`
- Modify: `frontend/src/components/keys/UseKeyModal.vue`
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Modify: `frontend/src/components/payment/SubscriptionPlanCard.vue`
- Modify: `frontend/src/views/admin/orders/AdminPaymentPlansView.vue`
- Modify: `frontend/src/views/admin/orders/PlanEditDialog.vue`
- Modify: `frontend/src/types/payment.ts`
- Modify: `frontend/src/i18n/locales/en/dashboard.ts`
- Modify: `frontend/src/i18n/locales/zh/dashboard.ts`
- Modify: `frontend/src/views/user/__tests__/KeysView.spec.ts`
- Modify: `deploy/.env.example`
- Modify: `deploy/Caddyfile`
- Audit: `deploy/config.example.yaml`
- Audit: `deploy/docker-compose*.yml`

**Interfaces:**
- Consumes: backend account/settings/payment API contracts from Tasks 2–4.
- Produces: UI for Agent Identity, long-context opt-in, Grok URL/header controls, account copy/IDs, affiliate settings, Server-Timing config documentation, and currency display while retaining local UI/deployment extensions.

- [ ] **Step 1: Resolve account and settings UI conflicts**

Keep the OpenAI create toggle default `false`; preserve explicit values for edit/import/PAT flows; expose Agent Identity in Codex auth; expose Grok custom URL/header controls with validation feedback; preserve local KIRO platform fields, daily check-in, beginner-guide, WebChat, and account action extensions.

- [ ] **Step 2: Resolve payment, key-help, translations, and types**

Add currency as display/edit metadata without changing local public-plan or provider rules. Merge upstream key-help changes with local multi-platform examples. Retain every local translation key and add every referenced upstream key in English and Chinese.

- [ ] **Step 3: Resolve deployment files semantically**

Keep local Caddy routing and deployment topology, add upstream environment/config keys only where the deployed backend consumes them, and keep example values non-secret. Do not turn `enable_server_timing` or affiliate admin recharge on in examples.

- [ ] **Step 4: Run frontend checks**

Run:

```bash
cd frontend
pnpm lint
pnpm typecheck
pnpm vitest run src/components/account/__tests__/CreateAccountModal.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts src/components/keys/__tests__/UseKeyModal.spec.ts src/views/admin/__tests__/SettingsView.spec.ts src/views/user/__tests__/KeysView.spec.ts
```

Expected: all commands PASS.

### Task 7: Audit automatic merges, clear the index, and create the two-parent merge commit

**Files:**
- Audit: every path in `.superpowers/upstream-eb2b/auto-merged-production.txt`
- Create: `.superpowers/upstream-eb2b/audit.tsv`
- Modify: any production path whose automatic merge dropped local or upstream semantics
- Commit: all upstream merge paths, excluding plan/spec/archive documentation until after the merge commit

**Interfaces:**
- Consumes: resolved semantic domains from Tasks 2–6 and the complete automatic-merge inventory from Task 1.
- Produces: one merge commit with both fixed parents and an audit row for every automatically merged production path.

- [ ] **Step 1: Audit each automatic production merge**

For each path, compare `git diff 7d239d62e..eb2b8632d -- <path>`, `git diff 7d239d62e..3e07e005e -- <path>`, and the final file. Record one tab-separated line containing path, semantic cluster, caller/consumer checked, and verdict. Review callers, state writes, retries, cleanup, side effects, authorization, redaction, API/frontend types, and tests; fix any dropped behavior and rerun its focused test.

- [ ] **Step 2: Prove conflict and generated-file cleanliness**

Run:

```bash
test "$(wc -l < .superpowers/upstream-eb2b/audit.tsv)" -eq "$(wc -l < .superpowers/upstream-eb2b/auto-merged-production.txt)"
test -z "$(git diff --name-only --diff-filter=U)"
test "$(git rev-parse MERGE_HEAD)" = "eb2b8632ded614bf991d7d36abfa38b513ad8c2d"
rg -n '^(<<<<<<<|=======|>>>>>>>)' --glob '!pnpm-lock.yaml' . && exit 1 || true
git diff --check
GOMAXPROCS=2 make check-generate
```

Expected: audit counts match, no unresolved paths or conflict markers, whitespace check passes, generated check passes.

- [ ] **Step 3: Stage explicit merge paths only**

Use the union of `git diff --name-only`, `git diff --cached --name-only`, deleted split-setting paths, renamed migration paths, and generated files. Stage each reviewed path with `git add -- <path>` or `git rm -- <path>`. Do not stage `docs/superpowers/specs`, `docs/superpowers/plans`, `.superpowers`, or the later upstream-sync archive in this commit.

- [ ] **Step 4: Create and verify the ordinary merge commit**

Run:

```bash
git commit -m "merge: sync sub2api upstream through eb2b8632d"
test "$(git rev-parse HEAD^1)" = "3e07e005e8d86271715471ed006006247ed8dc9d"
test "$(git rev-parse HEAD^2)" = "eb2b8632ded614bf991d7d36abfa38b513ad8c2d"
```

Expected: commit succeeds and both parent assertions pass.

### Task 8: Verify, archive, independently review, push, and wait for CI

**Files:**
- Add: `docs/superpowers/specs/2026-07-16-sub2api-upstream-eb2b-integration-design.md`
- Add: `docs/superpowers/plans/2026-07-16-sub2api-upstream-eb2b-integration.md`
- Create: `docs/upstream-sync/2026-07-16-sub2api-0.1.156-eb2b.md`
- Modify: `docs/upstream-sync/README.md`
- Create: reviewer package under `.superpowers/reviews/`

**Interfaces:**
- Consumes: the merge commit from Task 7 and all test evidence.
- Produces: durable archive and plan commits, an independent `SAFE TO PUSH` verdict, non-force remote updates, and successful checks for the exact pushed `dev` SHA.

- [ ] **Step 1: Run final local verification**

Run:

```bash
GOMAXPROCS=2 make check-generate
GOMAXPROCS=2 make build-backend
cd backend && GOMAXPROCS=2 go test -run '^$' -p 1 ./...
cd .. && GOMAXPROCS=2 make test-backend
make test-frontend
make build-frontend
cd frontend && pnpm lint && pnpm typecheck
```

Expected: every command PASS. If the full backend suite exceeds local resource limits, record the exact command/failure, run all affected packages at `-p 1`, and require the unchanged full GitHub Actions backend suite to pass before completion.

- [ ] **Step 2: Run structural and security-oriented audits**

Run migration duplicate/order checks, `git diff --check`, conflict-marker scan, executable-bit/unexpected-file review, generated-code check, API contract tests, credential-redaction tests, URL-policy/header-validation tests, KIRO inventory tests, and `git status --short`. Expected: no unexplained artifact or untracked file besides documented `.superpowers` evidence.

- [ ] **Step 3: Write the upstream sync archive**

Document fixed SHAs/range, 213 commits and 59 initial conflicts, accepted features, rejected/default-off policies, migration renames, every semantic conflict cluster, automatic-merge audit completion, generated outputs, exact local test commands/results, reviewer verdict, push SHAs, CI links/status, rollback branch, and any CI-only platform checks. Add the new archive to `docs/upstream-sync/README.md`.

- [ ] **Step 4: Commit documentation explicitly**

Run:

```bash
git add -- docs/superpowers/specs/2026-07-16-sub2api-upstream-eb2b-integration-design.md docs/superpowers/plans/2026-07-16-sub2api-upstream-eb2b-integration.md docs/upstream-sync/2026-07-16-sub2api-0.1.156-eb2b.md docs/upstream-sync/README.md
git commit -m "docs: archive sub2api upstream eb2b sync"
```

Expected: documentation commit succeeds and does not alter either merge parent.

- [ ] **Step 5: Dispatch a fresh independent whole-branch reviewer**

Generate a review package from `DEV_BASE` to final `HEAD`. The reviewer must read the design, archive, full diff package, conflict inventory, and automatic-merge audit; inspect every merged production region; verify billing/Grok/KIRO/WebChat/settings/payment/deployment invariants; and return exactly `SAFE TO PUSH` only if no critical, important, or unresolved finding remains. Apply all findings in one fix wave, rerun covering tests, update the archive, and re-review until the verdict is `SAFE TO PUSH`.

- [ ] **Step 6: Refresh remote state and push without force**

Fetch `origin`; require `origin/dev` still equals `DEV_BASE`; then push the reviewed `HEAD` to `origin/dev` with a normal push. Fast-forward local `main` to the same reviewed commit only if `origin/main` is an ancestor/no-op under the repository's documented release flow, then push `main` normally. Never use `--force` or `--force-with-lease`.

- [ ] **Step 7: Wait for exact-SHA CI completion**

Capture the pushed `dev` SHA, enumerate all required GitHub Actions checks for that exact SHA, and wait until backend, frontend, generation, shell/macOS, security, and other required checks finish. Expected: every required check concludes `success`; a superseded or newer branch run does not substitute for the exact pushed SHA.

- [ ] **Step 8: Final repository state proof**

Run:

```bash
git status --short
git log -1 --format='%H %P %s'
git branch --contains eb2b8632ded614bf991d7d36abfa38b513ad8c2d
```

Expected: worktree is clean except ignored `.superpowers` evidence, the reviewed commit is on `dev` and `main` as intended, upstream pin is contained, and the rollback branch remains available.
