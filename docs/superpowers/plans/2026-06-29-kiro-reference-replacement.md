# KIRO Reference Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the local DEV KIRO forwarding gateway with the KIRO implementation from `nianzs/sub2api` reference commit `88a5666b478e234cace9090e0d5f483f1146cb96`.

**Architecture:** Use the reference fork's `internal/pkg/kiro` translator and `GatewayService.forwardKiroMessages` runtime path, while preserving local DEV non-KIRO gateway, billing, admin, and upstream user-agent behavior. Integrate reference token provider, cooldown store, cache emulation, usage fetching, and OAuth pieces through local DEV wiring.

**Tech Stack:** Go 1.26.4, Gin, Wire, Redis, Ent, Vue 3, TypeScript, Vitest.

---

## File Structure

- Create/replace: `backend/internal/pkg/kiro/*` from the reference fork.
- Create/replace: `backend/internal/pkg/kirocooldown/*` from the reference fork.
- Create/replace: `backend/internal/service/kiro_*.go` from the reference fork.
- Delete local old implementation files after reference tests are in place: `backend/internal/service/kiro.go`, `backend/internal/service/kiro_bridge.go`, `backend/internal/service/kiro_eventstream.go`, `backend/internal/service/kiro_token.go`, `backend/internal/service/kiro_usage.go`, `backend/internal/service/openai_gateway_kiro.go`.
- Modify: `backend/internal/service/gateway_service.go` to merge reference KIRO fields, constructor parameters, routing, cooldown recovery, usage, and keepalive behavior with local `upstreamUARepo`.
- Modify: `backend/internal/service/wire.go`, `backend/cmd/server/wire.go`, and regenerate `backend/cmd/server/wire_gen.go` or patch generated wiring to provide `KiroTokenProvider` and `KiroCooldownStore`.
- Modify shared KIRO-adjacent files when compile errors or imported KIRO tests require an integration change: `account.go`, `account_service.go`, `account_usage_service.go`, `token_cache_invalidator.go`, `openai_account_scheduler.go`, `gateway_forward_as_chat_completions.go`, `gateway_forward_as_responses.go`, `openai_gateway_service.go`, `group.go`, handler DTOs, and admin group handlers.
- Create: `AGENTS.md` with the KIRO reference tracking rule.

### Task 1: Import KIRO Package Tests First

**Files:**
- Create: `backend/internal/pkg/kiro/*_test.go`
- Create: `backend/internal/pkg/kirocooldown/*_test.go`

- [ ] **Step 1: Write the failing tests**

Run:

```bash
mkdir -p backend/internal/pkg/kiro backend/internal/pkg/kirocooldown
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/pkg/kiro/*_test.go backend/internal/pkg/kiro/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/pkg/kirocooldown/*_test.go backend/internal/pkg/kirocooldown/
```

- [ ] **Step 2: Verify the tests fail because implementation is absent**

Run:

```bash
go test ./internal/pkg/kiro ./internal/pkg/kirocooldown
```

Expected: FAIL with undefined symbols such as `BuildKiroPayloadWithContext`, `MapModel`, or `NewStore`.

- [ ] **Step 3: Import the reference package implementation**

Run:

```bash
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/pkg/kiro/*.go backend/internal/pkg/kiro/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/pkg/kirocooldown/*.go backend/internal/pkg/kirocooldown/
```

- [ ] **Step 4: Verify the imported package tests pass**

Run:

```bash
go test ./internal/pkg/kiro ./internal/pkg/kirocooldown
```

Expected: PASS.

### Task 2: Import KIRO Service Tests First

**Files:**
- Create/replace: `backend/internal/service/kiro_*_test.go`
- Create/replace: `backend/internal/service/account_test_service_kiro*_test.go`
- Create/replace: `backend/internal/service/account_usage_service_kiro_apikey_test.go`
- Create/replace: `backend/internal/service/account_kiro_credit_unit_price_test.go`

- [ ] **Step 1: Write the failing service tests**

Run:

```bash
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/kiro_*_test.go backend/internal/service/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/account_test_service_kiro*_test.go backend/internal/service/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/account_usage_service_kiro_apikey_test.go backend/internal/service/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/account_kiro_credit_unit_price_test.go backend/internal/service/
rm -f backend/internal/service/kiro_test.go backend/internal/service/kiro_bridge_scheduling_test.go
```

- [ ] **Step 2: Verify the tests fail against the old local implementation**

Run:

```bash
go test ./internal/service -run 'Kiro|kiro'
```

Expected: FAIL with missing reference runtime symbols such as `KiroTokenProvider`, `forwardKiroMessages`, `buildKiroEndpoints`, `buildKiroPayloadForAccount`, or `KiroCooldownStore`.

- [ ] **Step 3: Replace service KIRO implementation files**

Run:

```bash
rm -f backend/internal/service/kiro.go backend/internal/service/kiro_bridge.go backend/internal/service/kiro_eventstream.go backend/internal/service/kiro_token.go backend/internal/service/kiro_usage.go backend/internal/service/openai_gateway_kiro.go
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/kiro_*.go backend/internal/service/
cp /tmp/sub2api-nianzs-kiro-reference/backend/internal/service/oauth_only_platforms.go backend/internal/service/
```

- [ ] **Step 4: Merge local DEV shared service wiring**

Patch `backend/internal/service/gateway_service.go` so it keeps local `upstreamUARepo` support and gains reference KIRO support:

```go
kiroTokenProvider     *KiroTokenProvider
kiroCooldownStore     KiroCooldownStore
upstreamUARepo        AccountUpstreamUserAgentRepository
```

Add constructor parameters before `sessionLimitCache`:

```go
kiroTokenProvider *KiroTokenProvider,
kiroCooldownStore KiroCooldownStore,
```

Assign both fields in the `GatewayService` literal, and keep the existing variadic `upstreamUARepos ...AccountUpstreamUserAgentRepository`.

- [ ] **Step 5: Merge KIRO routing into `GatewayService.Forward`**

Patch the selected-account path so direct KIRO accounts call:

```go
if isKiroDirectModeAccount(account) {
	return s.forwardKiroMessages(ctx, c, account, parsed, startTime)
}
```

Keep local non-KIRO web-search emulation and local `upstreamUARepo` behavior intact.

- [ ] **Step 6: Verify service tests progress**

Run:

```bash
go test ./internal/service -run 'Kiro|kiro'
```

Expected: compile errors only in shared integration files, or PASS if all shared integration is already compatible.

### Task 3: Wire KIRO Token Provider and Cooldown Store

**Files:**
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/cmd/server/wire.go`
- Modify: `backend/cmd/server/wire_gen.go`
- Modify test call sites using `NewGatewayService`

- [ ] **Step 1: Add provider functions and imports**

Patch `backend/internal/service/wire.go` to import:

```go
"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
"github.com/redis/go-redis/v9"
```

Add:

```go
func ProvideKiroTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	kiroOAuthService *KiroOAuthService,
	refreshAPI *OAuthRefreshAPI,
) *KiroTokenProvider {
	p := NewKiroTokenProvider(accountRepo, tokenCache, kiroOAuthService)
	executor := NewKiroTokenRefresher(kiroOAuthService)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(GeminiProviderRefreshPolicy())
	return p
}

func ProvideKiroCooldownStore(redisClient *redis.Client) KiroCooldownStore {
	return kirocooldown.NewStore(redisClient)
}
```

Include both providers in `ServiceSet`.

- [ ] **Step 2: Patch generated wiring**

Patch `backend/cmd/server/wire_gen.go` so `InitializeApp` constructs:

```go
kiroTokenProvider := service.ProvideKiroTokenProvider(accountRepository, geminiTokenCache, kiroOAuthService, oAuthRefreshAPI)
kiroCooldownStore := service.ProvideKiroCooldownStore(redisClient)
```

Pass both into `service.NewGatewayService` immediately after `claudeTokenProvider`, before session/rpm caches, while preserving the local trailing `accountUpstreamUserAgentRepository` variadic argument.

- [ ] **Step 3: Update test constructor call sites**

Patch local tests that call `NewGatewayService` by adding `nil, nil` immediately after the `claudeTokenProvider` argument.

- [ ] **Step 4: Verify wiring compiles**

Run:

```bash
go test ./cmd/server ./internal/handler ./internal/service -run 'Kiro|Gateway|Wire'
```

Expected: PASS or focused compile errors in files touched by KIRO integration.

### Task 4: Merge Shared KIRO Integration Points

**Files:**
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/account_service.go`
- Modify: `backend/internal/service/account_usage_service.go`
- Modify: `backend/internal/service/token_cache_invalidator.go`
- Modify: `backend/internal/service/openai_account_scheduler.go`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions.go`
- Modify: `backend/internal/service/gateway_forward_as_responses.go`
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/handler/admin/group_handler.go`
- Modify: `backend/internal/handler/dto/mappers.go`
- Modify: `backend/internal/handler/dto/types.go`

- [ ] **Step 1: Port reference KIRO account methods**

Ensure `Account` has the reference credential helpers used by imported KIRO files:

```go
IsKiroOAuth()
GetKiroAccessToken()
GetKiroRefreshToken()
GetKiroProfileARN()
GetKiroClientID()
GetKiroClientSecret()
GetKiroAuthMethod()
GetKiroRegion()
GetKiroBaseURL()
GetKiroAPIKey()
```

- [ ] **Step 2: Port KIRO usage fields**

Ensure `UsageInfo`, `UsageLogInput`, and usage extraction include `KiroCredits`, runtime cooldown state, and `_sub2api_kiro_credits` parsing exactly as in the reference fork.

- [ ] **Step 3: Port KIRO scheduler behavior**

Ensure KIRO direct-mode accounts are skipped when runtime cooldown is active, and transient 429 cooldown recovery can clear the earliest transient cooldown before reporting no account available.

- [ ] **Step 4: Port KIRO group endpoint/cache behavior**

Ensure KIRO group DTOs and service model include:

```go
KiroCacheEmulationEnabled
KiroAutoStickyEnabled
KiroStickySessionTTLSeconds
KiroCacheEmulationRatio
KiroEndpointMode
```

Preserve local DEV validation and persistence for these fields.

- [ ] **Step 5: Verify shared integration**

Run:

```bash
go test ./internal/service ./internal/handler/admin ./internal/handler/dto -run 'Kiro|Group|Usage|Gateway'
```

Expected: PASS.

### Task 5: Frontend and Admin API Alignment

**Files:**
- Modify: `frontend/src/api/admin/kiro.ts`
- Modify: `frontend/src/composables/useKiroOAuth.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify KIRO-related account utility files if reference behavior is missing.

- [ ] **Step 1: Compare KIRO frontend API signatures**

Run:

```bash
diff -u frontend/src/api/admin/kiro.ts /tmp/sub2api-nianzs-kiro-reference/frontend/src/api/admin/kiro.ts || true
diff -u frontend/src/composables/useKiroOAuth.ts /tmp/sub2api-nianzs-kiro-reference/frontend/src/composables/useKiroOAuth.ts || true
```

- [ ] **Step 2: Patch local frontend to match reference KIRO OAuth/token behavior**

Apply the reference fork's KIRO request/response types and credential builder behavior, while preserving local non-KIRO UI code.

- [ ] **Step 3: Verify frontend types**

Run:

```bash
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend typecheck
```

Expected: PASS.

### Task 6: Add AGENTS.md Tracking Guidance

**Files:**
- Create: `AGENTS.md`

- [ ] **Step 1: Create the guidance file**

Write:

```markdown
# AGENTS.md

## KIRO Reference Tracking

KIRO gateway work in this repository tracks `https://github.com/nianzs/sub2api`.

Before changing KIRO forwarding, request translation, response parsing, OAuth refresh, cache emulation, cooldown, or KIRO usage logic, compare against the reference fork first. Start with:

- `backend/internal/pkg/kiro`
- `backend/internal/pkg/kirocooldown`
- `backend/internal/service/kiro_*.go`
- KIRO-related sections of `backend/internal/service/gateway_service.go`

The reference commit used for the 2026-06-29 replacement is `88a5666b478e234cace9090e0d5f483f1146cb96`.

Keep local DEV features outside KIRO unless a KIRO integration point requires a narrow patch.
```

- [ ] **Step 2: Verify the document is tracked**

Run:

```bash
git status -sb
```

Expected: `AGENTS.md` appears as an untracked or modified file.

### Task 7: Final Verification and Push

**Files:**
- All modified files.

- [ ] **Step 1: Run backend KIRO verification**

Run:

```bash
go test ./internal/pkg/kiro ./internal/pkg/kirocooldown ./internal/service ./internal/handler/admin ./cmd/server -run 'Kiro|kiro|Gateway|Wire'
```

Expected: PASS.

- [ ] **Step 2: Run broader backend compile checks**

Run:

```bash
go test ./cmd/server ./internal/handler/... ./internal/service ./internal/pkg/...
```

Expected: PASS.

- [ ] **Step 3: Run frontend verification**

Run:

```bash
pnpm --dir frontend typecheck
```

Expected: PASS.

- [ ] **Step 4: Inspect diff scope**

Run:

```bash
git status -sb
git diff --stat
```

Expected: only KIRO replacement, Superpowers docs, and `AGENTS.md` changes are present.

- [ ] **Step 5: Commit and push**

Run:

```bash
git add -A
git commit -m "feat: replace kiro gateway with reference implementation"
git push -u origin codex/kiro-reference-replacement
```

Expected: branch pushed to GitHub.
