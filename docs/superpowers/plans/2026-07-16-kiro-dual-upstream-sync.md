# KIRO Dual-Upstream Semantic Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Semantically integrate the approved KIRO changes from `nianzs/sub2api` `88a5666b478e234cace9090e0d5f483f1146cb96..006af638390c0e929204a2486d696c302ad5bc07`, fix malformed Claude Code tool calls, preserve TokenStation3's local KIRO behavior, and leave explicitly excluded general Sub2API features untouched.

**Architecture:** Integrate capabilities one at a time instead of merging or cherry-picking the nianzs branch. The Anthropic stream boundary buffers and validates complete tool JSON before emitting a tool block; KIRO authentication adopts canonical provider metadata while retaining legacy refresh compatibility; endpoint selection gains an opt-in `auto` mode without coupling provider, OIDC `region`, and runtime `api_region`. Every capability follows red-green TDD and ends in a focused commit.

**Tech Stack:** Go 1.26.5, Gin, AWS event-stream translation, PostgreSQL/Ent, Vue 3, TypeScript 5.6, Vitest, pnpm 9, GitHub Actions.

## Global Constraints

- Previous KIRO reference is exactly `88a5666b478e234cace9090e0d5f483f1146cb96`.
- Target KIRO reference is exactly `006af638390c0e929204a2486d696c302ad5bc07`; do not silently expand the nianzs range.
- Applicable completed Wei-Shaw sync coordinate is `eb2b8632ded614bf991d7d36abfa38b513ad8c2d`; later Wei-Shaw changes found inside nianzs are audit inputs, not automatic integration candidates.
- Preserve Q chat, Q MCP, KRS, profile ARN, stable machine ID, direct/relay separation, mixed scheduling, capture, usage/cache, model whitelist, and account `api_region` behavior documented in `docs/kiro-upstream-sync.md`.
- `region` remains the IDC/OIDC authentication region; `api_region` remains the Q runtime region. No task may copy one over the other.
- `q` remains the default endpoint mode. `auto` is opt-in and API Key accounts remain forced to Q.
- Existing `AWS` and `Internal` provider values remain refresh-compatible; new login/import output uses `BuilderId`, `Enterprise`, or `ExternalIdp`.
- New imports require an explicit canonical provider. Existing stored credentials are not bulk-migrated.
- Do not include the excluded Wei-Shaw-origin feature groups listed in Task 1.
- Do not modify, deploy, restart, or migrate production. Production verification is outside this plan.
- Do not stage unrelated untracked files in the primary checkout. Use explicit `git add <paths>` only.
- Do not advance the runbook reference to `006af638...` until focused tests, repository-wide verification, and independent review pass.
- Do not force-push `dev` or `main`.

---

## Execution Preflight: Isolate the Work

This preflight is performed only after the user chooses an execution mode and consents to a worktree.

- [ ] **Step 1: Confirm the protected base**

Run:

```bash
git status --short --branch
git rev-parse dev origin/dev refs/remotes/nianzs/main
git merge-base --is-ancestor origin/dev dev
```

Expected: `dev` contains `origin/dev`, nianzs resolves to `006af638390c0e929204a2486d696c302ad5bc07`, and the only tracked local-only commit is the approved design commit `db0ba3677`.

- [ ] **Step 2: Create the rollback branch and isolated worktree**

Run:

```bash
git branch backup/dev-before-kiro-sync-20260716-006af638 dev
git worktree add .worktrees/kiro-upstream-006af638 -b sync/kiro-upstream-006af638 dev
```

Expected: the backup points to `dev`, and the new worktree is clean on `sync/kiro-upstream-006af638`.

- [ ] **Step 3: Record immutable coordinates**

Run in the new worktree:

```bash
git rev-parse HEAD
git rev-parse 88a5666b478e234cace9090e0d5f483f1146cb96
git rev-parse 006af638390c0e929204a2486d696c302ad5bc07
git rev-parse eb2b8632ded614bf991d7d36abfa38b513ad8c2d
```

Expected: four full SHAs, with the first recorded as `DEV_BASE` in the audit archive created in Task 1.

---

### Task 1: Create the Dual-Upstream Audit Archive and Reusable Runbook Rules

**Files:**

- Create: `docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md`
- Modify: `docs/kiro-upstream-sync.md`
- Modify: `docs/upstream-sync/README.md`

**Interfaces:**

- Consumes: fixed nianzs range, Wei-Shaw coordinate, approved user decisions, and current `DEV_BASE`.
- Produces: a durable capability matrix and reusable dual-upstream procedure; the KIRO reference line remains `88a5666...` in this task.

- [ ] **Step 1: Write the audit archive with fixed coordinates and complete dispositions**

Create a table with these exact capability rows and dispositions:

```markdown
| Capability | Source | Classification | Disposition |
|---|---|---|---|
| KIRO relay base URL hint | `be9bfd752` | Missing | Integrate |
| Canonical provider/import validation and expiry normalization | `60006ddbf`, `5908d9db1` | Partially missing; local External IdP is stronger in other areas | Integrate compatibly |
| Q/KRS automatic endpoint fallback | `e00219064` | Missing; local account-level config must be preserved | Integrate as opt-in |
| KIRO channel default-pricing fill | `9f044c23c` | Missing product convenience | Excluded by user authorization |
| External IdP canonical flow and port 3128 | `008b26ecd`, `e556682d9` | Partially missing | Integrate compatibly |
| Access-token cache identity | `e5f554a45` | Local implementation stronger (`account.ID`) | Preserve local; no code |
| Direct gateway routing restore | `b61e96a26`, `9f4cb5dfd`; `b35904bf2` later reverted by `6c18a7b94` | Equivalent locally / superseded | Preserve local; no code |
| Direct KIRO API Key token validation | `1248950d3` | Partially missing in non-streaming path | Integrate missing guard only |
| xxhash runtime hashing | `604e9f550` | Equivalent locally | No code |
| Sonnet 5 models | `a27dc9daf` | Equivalent locally | No code |
| KIRO credit account aggregates | `8e9f84c80` | Equivalent locally | No code |
| Buffered streamed tool JSON and stop reason | `b0367d9ad` | Missing | Integrate first |
| Dimension-aware KIRO visual tokens | `9ca7adc46` | Missing | Integrate |
| Ent generated field alignment | `fdca9d670` | Superseded by local schema/generation | No code; verify generation |
| Custom model list filtering | `b638ff193`, `03ba7e822` | Equivalent/stronger locally | No code |
| OpenCode write `filePath` | `6fccaf611` | Missing | Fold into tool-stream task |
| Constructor/test alignment | `db07a60b5`, `12f0a114c`, `60a41be7e`, `f1a2cb1be` | Reference-branch maintenance only | No production code |
| Responses→Anthropic reference-fork reconciliation | `a57dd062d`, `b0f5d957b`, `4d1a6cf30` | General protocol work, not KIRO translator | Excluded; normal Wei-Shaw sync |
| Async image task API | `134179085`, `df247b436`, reverted by `502097026` | Reverted | No action |
| Upstream billing-rate introspection/probe/scheduling | `f59a6ed74`, `0765d10c1`, `90ee85f3e` | General feature missing locally | Explicitly excluded by user |
| OpenAI/Codex reliability fixes | `716fcc6f3`, `4f641208a`, `2fe7df9b8`, `56650d6ae`, `5e4da92de`, `40b8f04a6`, `3db00d3fe`, `776f3f0de`, `695665cbc`, `72fada40f6`, `ad5e2a85b` | General fixes missing locally | Explicitly excluded by user |
| Image-input pricing, usage, and OAuth image usage | `10cb2ca42`, `7c43b7327`, `06e03f467`, `62d57c02d`, `d22f4d9b5` | General billing chain partially missing | Explicitly excluded by user |
| Grok passive image intent and key templates | `410ea8490`, `e8cc1fe26` | Product-specific, missing locally | Explicitly excluded by user |
| Tablet table scroll, scheduler snapshot optimizations, TLS external-test tolerance | `858f3e4a7`, `a8a8a58f6`, `f552448fb`, `e2bb90a80` | Frontend/performance/test-ops changes | Excluded under user authorization |
```

Also record the user's approved decisions: integrate `auto`; exclude the four general feature groups; automatically exclude later product-specific, non-general, operations, and test-convenience features after documenting their function.

- [ ] **Step 2: Update the reusable runbook**

Add a `Dual-Upstream Provenance` section immediately after `KIRO Reference Tracking` that states:

```markdown
TokenStation3 follows `Wei-Shaw/sub2api` for the general product and
`nianzs/sub2api` as a KIRO reference fork. Because nianzs periodically merges
Wei-Shaw, every KIRO audit must pin both coordinates, inspect merge parents,
and classify capabilities semantically. A commit is not excluded merely
because its subject is not KIRO-specific. First determine whether it is already
present through TokenStation3's normal Wei-Shaw sync, locally equivalent, later
modified by nianzs, or genuinely absent. Genuinely absent general product
features require a user scope decision; nianzs follow-up fixes still require
separate review even when the Wei-Shaw feature is already local.
```

Add these reproducible commands:

```bash
git show -s --format='%H %P %s' "$MERGE_SHA"
git log --reverse --no-merges "$OLD_KIRO..$NEW_KIRO"
git log --left-right --cherry-mark --oneline "dev...$REFERENCE_SHA"
git show "$COMMIT_SHA" | git patch-id --stable
git diff --name-status "$BASE_SHA..$REFERENCE_SHA"
git diff dev "$REFERENCE_SHA" -- "${CAPABILITY_PATHS[@]}"
git merge-base --is-ancestor "$COMMIT_SHA" dev
```

Keep the existing KIRO inventory and intentional runtime differences unchanged.

- [ ] **Step 3: Update the archive index**

Add the new archive entry to `docs/upstream-sync/README.md`, labeling it a semantic KIRO reference sync rather than a Wei-Shaw merge.

- [ ] **Step 4: Validate and commit the documentation foundation**

Run:

```bash
git diff --check
rg -n "Dual-Upstream|006af638|General upstream feature" docs/kiro-upstream-sync.md docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md
git add docs/kiro-upstream-sync.md docs/upstream-sync/README.md docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md
git commit -m "docs: define dual-upstream KIRO audit workflow"
```

Expected: clean whitespace check; the runbook still says current reference `88a5666...`.

---

### Task 2: Buffer and Validate Streamed Tool Calls Before Anthropic Emission

**Files:**

- Modify: `backend/internal/pkg/kiro/translator.go`
- Modify: `backend/internal/pkg/kiro/translator_test.go`

**Interfaces:**

- Consumes: KIRO `toolUseEvent` fragments/snapshots and `KiroRequestContext` tool-name restoration.
- Produces: `normalizeStreamingToolInput(name, raw string) (string, map[string]any, bool)`, `isEmittableToolUse(KiroToolUse) bool`, and Anthropic SSE that never reports `tool_use` without an emitted executable tool block.

- [ ] **Step 1: Add failing protocol regression tests**

Add focused tests covering these exact cases:

```go
func TestStreamEventStreamAsAnthropicBuffersToolUntilValidStop(t *testing.T)
func TestStreamEventStreamAsAnthropicInvalidToolDowngradesStopReason(t *testing.T)
func TestStreamEventStreamAsAnthropicSnapshotReplacesFragments(t *testing.T)
func TestStreamEventStreamAsAnthropicRejectsOversizedToolInput(t *testing.T)
func TestStreamEventStreamAsAnthropicPreservesLargeJSONInteger(t *testing.T)
func TestStreamEventStreamAsAnthropicEscapesLiteralControlCharacters(t *testing.T)
func TestStreamEventStreamAsAnthropicRemovesTrailingCommasOutsideStrings(t *testing.T)
func TestStreamEventStreamAsAnthropicRejectsTrailingJSONValue(t *testing.T)
func TestStreamEventStreamAsAnthropicRejectsMissingToolIDOrName(t *testing.T)
func TestStreamEventStreamAsAnthropicDeduplicatesStreamAndAggregateTool(t *testing.T)
func TestStreamEventStreamAsAnthropicAcceptsOpenCodeWriteFilePath(t *testing.T)
func TestNormalizeStreamingToolInput(t *testing.T)
```

For the observed failure, assert both:

```go
require.NotContains(t, out.String(), `"type":"tool_use"`)
require.Contains(t, out.String(), `"stop_reason":"end_turn"`)
```

For a valid fragmented call, assert one start, one accumulated `input_json_delta`, one stop, and final `tool_use`.

- [ ] **Step 2: Run the new tests and verify red state**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro -run 'Test(StreamEventStreamAsAnthropic|NormalizeStreamingToolInput)' -count=1
```

Expected: failures show the current implementation emits fragments before complete validation and preserves an upstream `tool_use` reason without a valid tool block.

- [ ] **Step 3: Replace eager streaming state with bounded per-ID buffers**

In `StreamEventStreamAsAnthropicWithContext`, replace block-index/eager-start maps with:

```go
streamingToolNames := make(map[string]string)
streamingToolStopped := make(map[string]bool)
streamingToolInputBuf := make(map[string]*strings.Builder)
streamingToolInvalid := make(map[string]bool)
currentStreamingToolID := ""
toolBlockEmitted := false
```

Implement `bufferStreamingToolInput` so a map input resets the buffer as a snapshot, fragments append, switching IDs closes the prior buffer, and `len(fragment) > maxEventMsgSize-buf.Len()` marks the tool invalid and releases its builder.

- [ ] **Step 4: Add strict complete-object normalization**

Implement:

```go
func normalizeStreamingToolInput(name, raw string) (string, map[string]any, bool) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", nil, false
	}
	normalized = escapeControlCharsInStrings(normalized)
	normalized = removeTrailingCommasOutsideStrings(normalized)
	decoder := json.NewDecoder(strings.NewReader(normalized))
	decoder.UseNumber()
	var input map[string]any
	if err := decoder.Decode(&input); err != nil || input == nil {
		return "", nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", nil, false
	}
	if hasMissingRequiredFields(name, input) {
		return "", nil, false
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", nil, false
	}
	return string(encoded), input, true
}
```

Implement a string-aware `removeTrailingCommasOutsideStrings` scanner; do not use a regex. Extend `requiredToolFields["write"]` to `{{"filePath", "file_path", "path"}, {"content"}}`.

- [ ] **Step 5: Emit only after close-time validation and repair stop-reason precedence**

On tool close: normalize/restored tool name, validate the complete object, deduplicate by content, then emit `content_block_start`, one full `input_json_delta`, and `content_block_stop`. Set `toolBlockEmitted = true` only after all three writes succeed.

Finalize stop reason with:

```go
switch stopReason {
case "max_tokens", "stop_sequence":
case "":
	if toolBlockEmitted {
		stopReason = "tool_use"
	} else {
		stopReason = "end_turn"
	}
default:
	if toolBlockEmitted {
		stopReason = "tool_use"
	} else if stopReason == "tool_use" {
		stopReason = "end_turn"
	}
}
```

Decode event payloads and finalized raw tools with `json.Decoder.UseNumber()` and reject trailing values. Keep structured-output behavior and restored tool names.

- [ ] **Step 6: Run focused and package tests**

Run:

```bash
gofmt -w backend/internal/pkg/kiro/translator.go backend/internal/pkg/kiro/translator_test.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro -run 'Test(StreamEventStreamAsAnthropic|NormalizeStreamingToolInput|ParseNonStreamingEventStream)' -count=1
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro -count=1
```

Expected: all pass; the invalid-tool regression ends with `end_turn`, and the OpenCode `filePath` case ends with one valid `tool_use`.

- [ ] **Step 7: Commit the protocol fix**

```bash
git add backend/internal/pkg/kiro/translator.go backend/internal/pkg/kiro/translator_test.go
git commit -m "fix(kiro): validate streamed tool calls before emission"
```

---

### Task 3: Canonicalize KIRO Providers While Preserving Legacy Accounts

**Files:**

- Modify: `backend/internal/pkg/kiro/oauth.go`
- Modify: `backend/internal/pkg/kiro/oauth_test.go`
- Modify: `backend/internal/pkg/kiro/oauth_invalid_grant_test.go`
- Modify: `backend/internal/service/kiro_oauth_service.go`
- Modify: `backend/internal/service/kiro_oauth_service_test.go`
- Modify: `backend/internal/service/kiro_http_helpers.go`
- Modify: `backend/internal/service/kiro_http_helpers_test.go`

**Interfaces:**

- Produces canonical constants `ProviderGoogle`, `ProviderGithub`, `ProviderBuilderId`, `ProviderEnterprise`, and `ProviderExternalIdp`.
- Changes `RefreshIDCToken` to accept the stored provider as its final string argument.
- Preserves refresh of stored `AWS`/`Internal` credentials without accepting those aliases in new imported JSON.

- [ ] **Step 1: Add failing provider, expiry, and legacy-compatibility tests**

Add tests for:

```go
func TestParseImportedTokenRejectsMissingOrInvalidProvider(t *testing.T)
func TestParseImportedTokenAcceptsCanonicalProviders(t *testing.T)
func TestParseImportedTokenNormalizesExpiresAt(t *testing.T)
func TestParseImportedTokenRejectsInvalidExpiresAt(t *testing.T)
func TestParseImportedTokenValidatesExternalIdpRefreshFields(t *testing.T)
func TestRefreshIDCTokenPreservesStoredEnterpriseProvider(t *testing.T)
func TestRefreshIDCTokenPreservesLegacyAWSProvider(t *testing.T)
func TestResolveIDCProvider(t *testing.T)
func TestNewKiroJSONRequestPreservesLegacyAndCanonicalExternalIDPHeaders(t *testing.T)
```

Assert new import rejects `AWS`, `Internal`, blank, and unknown providers. Assert legacy stored values pass through refresh functions and request header construction.

- [ ] **Step 2: Verify the tests fail on current behavior**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(ParseImportedToken|RefreshIDCToken|ResolveIDCProvider|NewKiroJSONRequestPreservesLegacy)' -count=1
```

Expected: missing constants/signature and current permissive imports cause failures.

- [ ] **Step 3: Add canonical provider and expiry helpers**

Implement the five constants and strict canonical whitelist. `resolveIDCProvider(BuilderIDStartURL)` and empty start URL return `BuilderId`; other non-empty start URLs return `Enterprise`. `normalizeKiroExpiresAt` accepts RFC3339/RFC3339Nano and naive ISO timestamps interpreted as UTC, then returns local RFC3339.

- [ ] **Step 4: Preserve provider identity through IDC login and refresh**

New IDC exchanges set `resolveIDCProvider(startURL)`. Change the refresh signature to:

```go
func RefreshIDCToken(ctx context.Context, proxyURL, clientID, clientSecret, refreshToken, region, startURL, provider string) (*TokenData, error)
```

Set `Provider: strings.TrimSpace(provider)` and only fall back to `resolveIDCProvider(startURL)` when empty. Update every caller and test.

- [ ] **Step 5: Enforce strict new import contracts**

After optional device registration and auth-method inference, trim and validate provider. For `idc`, default only `region`; do not synthesize `AWS`. For `external_idp`, force canonical `ExternalIdp` and require non-empty `refreshToken`, `clientId`, and the endpoint metadata used by the selected local External IdP implementation. Normalize non-empty `expiresAt`.

- [ ] **Step 6: Preserve local request headers for both legacy and canonical External IdP**

Keep `TokenType: EXTERNAL_IDP`. Change the redirect header condition to accept both aliases:

```go
provider := strings.TrimSpace(account.GetCredential("provider"))
if strings.EqualFold(provider, "Internal") || strings.EqualFold(provider, kiropkg.ProviderExternalIdp) {
	req.Header.Set("redirect-for-internal", "true")
}
```

Do not change lowercase `tokentype: API_KEY`, profile ARN placement, or machine ID behavior.

- [ ] **Step 7: Run focused authentication tests and commit**

```bash
gofmt -w backend/internal/pkg/kiro/oauth.go backend/internal/pkg/kiro/oauth_test.go backend/internal/pkg/kiro/oauth_invalid_grant_test.go backend/internal/service/kiro_oauth_service.go backend/internal/service/kiro_oauth_service_test.go backend/internal/service/kiro_http_helpers.go backend/internal/service/kiro_http_helpers_test.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(ParseImportedToken|RefreshIDCToken|ResolveIDCProvider|KiroOAuth|KiroExternal|NewKiroJSONRequest|ApplyKiroConditionalHeaders)' -count=1
git add backend/internal/pkg/kiro/oauth.go backend/internal/pkg/kiro/oauth_test.go backend/internal/pkg/kiro/oauth_invalid_grant_test.go backend/internal/service/kiro_oauth_service.go backend/internal/service/kiro_oauth_service_test.go backend/internal/service/kiro_http_helpers.go backend/internal/service/kiro_http_helpers_test.go
git commit -m "fix(kiro): canonicalize providers with legacy compatibility"
```

Expected: focused tests pass and no test changes `api_region`.

---

### Task 4: Complete the External IdP Two-Stage Flow and Import UI

**Files:**

- Modify: `backend/internal/pkg/kiro/oauth.go`
- Modify: `backend/internal/pkg/kiro/oauth_test.go`
- Modify: `backend/internal/service/kiro_oauth_service.go`
- Modify: `backend/internal/service/kiro_oauth_service_test.go`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/admin/account/ReAuthAccountModal.vue`
- Modify: `frontend/src/components/account/OAuthAuthorizationFlow.vue`
- Modify: `frontend/src/composables/useKiroOAuth.ts`
- Modify: `frontend/src/composables/__tests__/useKiroOAuth.spec.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts`
- Modify: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`

**Interfaces:**

- Produces official External IdP second-stage redirect URI `http://localhost:3128/oauth/callback`.
- Produces allowlisted OIDC discovery endpoints and frontend provider choices for `Google`, `Github`, `BuilderId`, `Enterprise`, `ExternalIdp`.
- Preserves `credentials.api_region` independently during create, edit, reauth, and import.

- [ ] **Step 1: Add failing backend tests for redirect and endpoint safety**

Add/adjust tests to assert:

```go
require.Equal(t, "http://localhost:3128/oauth/callback", kiroExternalIdpRedirectURI)
```

Test discovery accepts Microsoft Online public/US/China suffixes, rejects HTTP, IP literals, unrelated hosts, and discovery documents whose authorization/token endpoints leave the allowlist.

- [ ] **Step 2: Add failing frontend source/behavior tests**

Assert create and reauth UIs contain all five canonical options, require device registration only for `BuilderId/Enterprise`, reject provider mismatch before calling the API, and preserve `credentials.api_region`. Assert the two-stage UI resets the pasted portal descriptor when the stage becomes `idp`.

- [ ] **Step 3: Run focused tests and verify failures**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(KiroExternal|DiscoverExternal|ValidateExternal)' -count=1
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/composables/__tests__/useKiroOAuth.spec.ts src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts
```

Expected: redirect currently reports 49153, discovery validation is absent, and import provider controls are absent.

- [ ] **Step 4: Implement the safe two-stage backend**

Use `http://localhost:3128/oauth/callback` only for the IdP stage. Discover authorization/token endpoints through the account's configured proxy. Validate HTTPS, non-IP host, and suffixes `.microsoftonline.com`, `.microsoftonline.us`, `.microsoftonline.cn`. Persist `token_endpoint`, `issuer_url`, and `scopes` for canonical `ExternalIdp` tokens. Keep legacy `Internal` refresh using the existing issuer-based path when stored credentials lack `token_endpoint`.

- [ ] **Step 5: Implement provider-aware import forms**

Create/re-auth state is exactly:

```ts
const kiroImportProvider = ref<'Google' | 'Github' | 'BuilderId' | 'Enterprise' | 'ExternalIdp'>('Google')
const kiroImportProviderOptions = ['Google', 'Github', 'BuilderId', 'Enterprise', 'ExternalIdp'] as const
const kiroImportNeedsDeviceRegistration = computed(
  () => kiroImportProvider.value === 'BuilderId' || kiroImportProvider.value === 'Enterprise'
)
```

Before import, parse JSON, show a localized invalid-JSON error, and require exact equality between the selected provider and JSON provider. Provide canonical placeholders, including `tokenEndpoint`, `issuerUrl`, and `scopes` for External IdP.

- [ ] **Step 6: Preserve API region across all credential builders**

Create/import continues to assign `credentials.api_region = kiroAPIRegion.value`. Reauth/edit merges credentials without deleting existing `api_region`; add explicit assertions that canonical provider changes neither `api_region` nor endpoint mode.

- [ ] **Step 7: Run backend and frontend focused tests and commit**

```bash
gofmt -w backend/internal/pkg/kiro/oauth.go backend/internal/pkg/kiro/oauth_test.go backend/internal/service/kiro_oauth_service.go backend/internal/service/kiro_oauth_service_test.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(KiroExternal|DiscoverExternal|ValidateExternal|ParseImportedToken)' -count=1
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/composables/__tests__/useKiroOAuth.spec.ts src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts
git add backend/internal/pkg/kiro/oauth.go backend/internal/pkg/kiro/oauth_test.go backend/internal/service/kiro_oauth_service.go backend/internal/service/kiro_oauth_service_test.go frontend/src/components/account/CreateAccountModal.vue frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/components/account/OAuthAuthorizationFlow.vue frontend/src/composables/useKiroOAuth.ts frontend/src/composables/__tests__/useKiroOAuth.spec.ts frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh.ts frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts frontend/src/components/account/__tests__/EditAccountModal.spec.ts
git commit -m "feat(kiro): complete canonical External IdP account flow"
```

---

### Task 5: Add Opt-In Q-to-KRS Automatic Endpoint Fallback

**Files:**

- Modify: `backend/internal/service/group.go`
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/kiro_config_resolver.go`
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_profile_resolver.go`
- Modify: `backend/internal/service/kiro_http_helpers_test.go`
- Modify: `backend/internal/service/kiro_config_resolver_test.go`
- Modify: `backend/internal/service/account_kiro_config_test.go`
- Modify: `backend/internal/service/kiro_runtime_state_test.go`
- Modify: `frontend/src/views/admin/GroupsView.vue`
- Modify: `frontend/src/views/admin/__tests__/GroupsView.kiroEndpointMode.spec.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts`
- Modify: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/zh.ts`

**Interfaces:**

- Produces `KiroEndpointModeAuto = "auto"` at group and direct-account levels.
- `buildKiroEndpoints(account, auto)` returns Q at `q.{api_region}.amazonaws.com` followed by KRS.
- API Key accounts always resolve to Q.

- [ ] **Step 1: Add failing resolver and endpoint tests**

Add assertions for group/account normalization, mixed-scheduling resolver behavior, API Key forced Q, and:

```go
endpoints := buildKiroEndpoints(&Account{Credentials: map[string]any{"api_region": "eu-west-1"}}, KiroEndpointModeAuto)
require.Equal(t, "https://q.eu-west-1.amazonaws.com/generateAssistantResponse", endpoints[0].URL)
require.Equal(t, kiroKRSEndpointURL, endpoints[1].URL)
```

Add runtime tests proving Q 429 and exhausted 408/5xx advance to KRS, non-retryable 4xx do not, and Q/KRS payloads use endpoint-correct profile ARN without changing the stored `api_region`.

- [ ] **Step 2: Verify red state**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/service -run 'Test(BuildKiroEndpointsAuto|EffectiveKiroEndpointMode|NormalizeKiroEndpoint|ResolveKiroEndpointMode|ExecuteKiroUpstream.*Auto|AccountKiroEndpointMode)' -count=1
```

Expected: `auto` currently normalizes to Q and only one endpoint is built.

- [ ] **Step 3: Add auto to group/account configuration contracts**

Accept `auto` in group normalization and `Account.KiroEndpointMode()`. Preserve Q as the unknown/default result and preserve the API Key early return in `kiroEndpointModeForRequest`.

- [ ] **Step 4: Build payloads per endpoint while preserving local profile rules**

Resolve the mode once. For KRS/auto, ensure a real profile ARN before request execution. Keep the persisted stable machine ID from `ensureKiroMachineIDPersisted`. Inside the endpoint loop:

```go
profileArn := kiroResolveProfileArnForPayload(account, KiroEndpointModeQ)
if endpoint.Name == "KiroRuntime" {
	profileArn = kiroResolveProfileArnForKRS(account)
}
buildResult, err := s.buildKiroPayloadForAccountWithArn(
	ctx, account, parsed, anthropicBody, modelID, currentToken,
	requestModel, headers, profileArn,
)
```

This intentionally differs from blindly copying nianzs: Q retains TokenStation3's documented existing-profile body placement, while KRS retains its fallback.

- [ ] **Step 5: Add auto to group and account UIs**

Extend TypeScript unions to `'q' | 'krs' | 'auto'`, add localized `Auto (Q → KRS on retryable failure)` options, and preserve the selected value through create/edit payloads. Default remains `q`.

- [ ] **Step 6: Run focused backend/frontend tests and commit**

```bash
gofmt -w backend/internal/service/group.go backend/internal/service/account.go backend/internal/service/kiro_config_resolver.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_profile_resolver.go backend/internal/service/kiro_http_helpers_test.go backend/internal/service/kiro_config_resolver_test.go backend/internal/service/account_kiro_config_test.go backend/internal/service/kiro_runtime_state_test.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/service -run 'Test(BuildKiroEndpoints|EffectiveKiroEndpointMode|NormalizeKiroEndpoint|ResolveKiroEndpointMode|ExecuteKiroUpstream|AccountKiroEndpointMode|KiroAPIRegion)' -count=1
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/admin/__tests__/GroupsView.kiroEndpointMode.spec.ts src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts
git add backend/internal/service/group.go backend/internal/service/account.go backend/internal/service/kiro_config_resolver.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_profile_resolver.go backend/internal/service/kiro_http_helpers_test.go backend/internal/service/kiro_config_resolver_test.go backend/internal/service/account_kiro_config_test.go backend/internal/service/kiro_runtime_state_test.go frontend/src/views/admin/GroupsView.vue frontend/src/views/admin/__tests__/GroupsView.kiroEndpointMode.spec.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts frontend/src/components/account/__tests__/EditAccountModal.spec.ts frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh.ts
git commit -m "feat(kiro): add opt-in Q to KRS endpoint fallback"
```

---

### Task 6: Estimate KIRO Visual Tokens from Image Dimensions

**Files:**

- Create: `backend/internal/pkg/kiro/image_tokens.go`
- Create: `backend/internal/pkg/kiro/image_tokens_test.go`
- Modify: `backend/internal/pkg/kiro/input_token_estimator.go`
- Modify: `backend/internal/pkg/kiro/translator_test.go`
- Modify: `backend/internal/service/account_test_service.go`
- Modify: `backend/internal/service/gateway_count_tokens.go`
- Create: `backend/internal/service/gateway_count_tokens_kiro_test.go`
- Modify: `backend/internal/service/gateway_websearch_emulation.go`
- Modify: `backend/internal/service/kiro_cache_emulation.go`
- Modify: `backend/internal/service/kiro_cache_emulation_test.go`
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_websearch.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

**Interfaces:**

- Produces `kiro.EstimateImageTokens(ctx context.Context, mediaType, source string) int`.
- Changes local token-estimation helpers to receive `context.Context` so remote image fetches are cancelable.
- Uses dimension-based visual tokens while retaining image bytes in cache fingerprints.

- [ ] **Step 1: Add failing estimator tests**

Cover PNG/JPEG/GIF/WebP, data URLs, raw base64, remote HTTP images, success/failure cache, singleflight, context cancellation, maximum body size, malformed input fallback, cache bound, and dimension calculations:

```go
require.Equal(t, 54, imageTokensForDimensions(200, 200))
require.Equal(t, 1334, imageTokensForDimensions(1000, 1000))
require.Equal(t, 1533, imageTokensForDimensions(2000, 1000))
require.Equal(t, 1600, imageTokensForDimensions(0, 100))
```

Add service tests proving base64 length does not dominate token counts, images remain part of cache fingerprints, and count-tokens/runtime/web-search use the same estimate.

- [ ] **Step 2: Verify red state**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(ImageTokens|EstimateImageTokens|KiroInputTokenEstimateSeparatesVisual|KiroCacheEmulationIncludesImage|CountTokens.*Kiro)' -count=1
```

Expected: the new API is undefined and the current fixed 1600-per-image estimator fails dimension assertions.

- [ ] **Step 3: Implement bounded image estimation**

Use constants from the pinned reference: long edge 1568, max 1,150,000 pixels, 750 pixels/token, fallback 1600, 256 cache entries, 5-minute success TTL, 30-second failure TTL. Limit remote response reads to `kiroRemoteImageMaxBytes + 1`, use `singleflight.Group`, and honor context cancellation. Add `golang.org/x/image/webp` only if not already present in `go.mod`.

- [ ] **Step 4: Thread context and visual tokens through all KIRO estimators**

Replace fixed `len(message.Images) * 1600` with per-image `EstimateImageTokens`. Change service helpers to:

```go
func estimateKiroInputTokens(ctx context.Context, body []byte) int
func countKiroInputTokensFromPayload(ctx context.Context, payload map[string]any) int
func countKiroMessageContentTokens(ctx context.Context, content any) int
func (s *GatewayService) buildKiroCacheEmulationUsage(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *kiroCacheEmulationUsage
```

Update every caller in runtime, account test, count-tokens, web-search, and cache emulation.

- [ ] **Step 5: Run focused and neighboring tests and commit**

```bash
gofmt -w backend/internal/pkg/kiro/image_tokens.go backend/internal/pkg/kiro/image_tokens_test.go backend/internal/pkg/kiro/input_token_estimator.go backend/internal/pkg/kiro/translator_test.go backend/internal/service/account_test_service.go backend/internal/service/gateway_count_tokens.go backend/internal/service/gateway_count_tokens_kiro_test.go backend/internal/service/gateway_websearch_emulation.go backend/internal/service/kiro_cache_emulation.go backend/internal/service/kiro_cache_emulation_test.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_websearch.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -run 'Test(ImageTokens|EstimateImageTokens|KiroInputToken|KiroCacheEmulation|ResolveKiroInputTokens|CountTokens.*Kiro)' -count=1
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/pkg/kiro ./internal/service -count=1
git add backend/go.mod backend/go.sum backend/internal/pkg/kiro/image_tokens.go backend/internal/pkg/kiro/image_tokens_test.go backend/internal/pkg/kiro/input_token_estimator.go backend/internal/pkg/kiro/translator_test.go backend/internal/service/account_test_service.go backend/internal/service/gateway_count_tokens.go backend/internal/service/gateway_count_tokens_kiro_test.go backend/internal/service/gateway_websearch_emulation.go backend/internal/service/kiro_cache_emulation.go backend/internal/service/kiro_cache_emulation_test.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_websearch.go
git commit -m "feat(kiro): estimate visual tokens from image dimensions"
```

---

### Task 7: Close Small KIRO Forwarding and Admin Gaps

**Files:**

- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/account_test_service_kiro_test.go`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`

**Interfaces:**

- Non-streaming direct KIRO accepts `tokenType == "oauth" || tokenType == "apikey"`.
- KIRO relay edit UI uses `admin.accounts.kiro.relayBaseUrlHint`.

- [ ] **Step 1: Add failing non-streaming API Key and relay-hint tests**

Add a non-streaming direct KIRO API Key test that reaches the mocked AWS upstream instead of failing with `kiro requires oauth token`. Add a frontend test that a relay account's visible base URL field uses `relayBaseUrlHint` and a native direct account does not expose that field.

- [ ] **Step 2: Verify red state**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/service -run 'TestForwardKiroMessagesNonStream.*APIKey' -count=1
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/account/__tests__/EditAccountModal.spec.ts
```

Expected: backend rejects `apikey`; frontend returns the generic KIRO hint.

- [ ] **Step 3: Apply the two narrow fixes**

Use the same guard already present in the streaming path:

```go
if tokenType != "oauth" && tokenType != "apikey" {
	return nil, fmt.Errorf("kiro requires oauth or apikey token, got %s", tokenType)
}
```

Change only the KIRO branch of `baseUrlHint` to `admin.accounts.kiro.relayBaseUrlHint`.

- [ ] **Step 4: Run tests and commit**

```bash
gofmt -w backend/internal/service/kiro_runtime.go backend/internal/service/account_test_service_kiro_test.go
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./internal/service -run 'TestForwardKiroMessages|TestApplyKiroConditionalHeadersAPIKeyTokenType|TestBuildKiroEndpointsAPIKeyDirectAWS' -count=1
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/account/__tests__/EditAccountModal.spec.ts
git add backend/internal/service/kiro_runtime.go backend/internal/service/account_test_service_kiro_test.go frontend/src/components/account/EditAccountModal.vue frontend/src/components/account/__tests__/EditAccountModal.spec.ts
git commit -m "fix(kiro): close direct API key and relay UI gaps"
```

---

### Task 8: Finalize the Audit, Advance the Reference, and Verify the Repository

**Files:**

- Modify: `docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md`
- Modify: `docs/kiro-upstream-sync.md`

**Interfaces:**

- Consumes: final implementation commits and verification output.
- Produces: final reference `006af638390c0e929204a2486d696c302ad5bc07` and exact evidence for every audit disposition.

- [ ] **Step 1: Review every changed call chain before broad tests**

Run:

```bash
git diff --stat backup/dev-before-kiro-sync-20260716-006af638..HEAD
git diff --name-status backup/dev-before-kiro-sync-20260716-006af638..HEAD
git diff --check backup/dev-before-kiro-sync-20260716-006af638..HEAD
git diff --no-ext-diff backup/dev-before-kiro-sync-20260716-006af638..HEAD -- backend/internal/pkg/kiro backend/internal/service/kiro_runtime.go backend/internal/service/kiro_oauth_service.go backend/internal/service/kiro_http_helpers.go backend/internal/service/kiro_cache_emulation.go frontend/src/components/account frontend/src/views/admin/GroupsView.vue
```

Explicitly trace stream write errors, tool deduplication, provider refresh, External IdP proxy use, API-region preservation, endpoint retry/cooldown, profile ARN, stable machine ID, image fetch limits, and direct/relay routing.

- [ ] **Step 2: Run generated-code, builds, lint, and all CI-equivalent tests**

Run sequentially:

```bash
GOMAXPROCS=2 make check-generate
GOMAXPROCS=2 make build-backend
GOMAXPROCS=2 go -C backend test -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=unit -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=integration -p 1 ./...
GOMAXPROCS=2 go -C backend run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.9.0 run --timeout=30m
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run lint:check
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run test:run
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend
COREPACK_ENABLE_PROJECT_SPEC=0 make build-frontend
```

Expected: all pass. If a broad command fails and appears pre-existing, reproduce the identical command in a separate worktree at `backup/dev-before-kiro-sync-20260716-006af638` before documenting baseline debt.

- [ ] **Step 3: Scan repository hygiene**

Run:

```bash
git diff --check
git status --short
git grep -nE '^(<<<<<<<|=======|>>>>>>>)' -- ':!docs/superpowers/plans/2026-07-16-kiro-dual-upstream-sync.md'
git diff --summary backup/dev-before-kiro-sync-20260716-006af638..HEAD
git diff --exit-code -- backend/ent backend/cmd/server/wire_gen.go backend/cmd/server/wire_gen_test.go
```

Expected: no conflict markers, unintended mode changes, generated drift, build artifacts, or unrelated user documents.

- [ ] **Step 4: Complete archive evidence and advance the runbook reference**

For every Task 1 matrix row, record the final commit or `no code` rationale and exact tests. Change only the reference line in `docs/kiro-upstream-sync.md` from `88a5666...` to `006af638...`.

- [ ] **Step 5: Commit final documentation**

```bash
git add docs/kiro-upstream-sync.md docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md
git commit -m "docs: advance audited KIRO reference to 006af638"
```

---

### Task 9: Independent Review, Publication Gate, and CI Completion

**Files:**

- Modify only files required by independently verified findings.
- Update: `docs/upstream-sync/2026-07-16-kiro-88a5666-006af638.md` with review and CI evidence.

**Interfaces:**

- Produces: an independent `NO ACTIONABLE ISSUES / SAFE TO PUSH` conclusion for the complete range and successful required checks for the exact pushed SHA.

- [ ] **Step 1: Dispatch an independent full-range reviewer**

Provide only the coordinates, requirements, and changed-file range:

```text
Review backup/dev-before-kiro-sync-20260716-006af638..HEAD in full.
Cover every changed production/test/doc file, all audit dispositions, the
Anthropic tool stream contract, provider legacy compatibility, External IdP
security/proxy behavior, API region isolation, Q/KRS retry and cooldown,
profile ARN/machine ID preservation, visual-token limits, direct/relay routing,
and excluded-feature non-inclusion. Report Critical/Important/Minor findings
with file:line evidence and end with exactly NO ACTIONABLE ISSUES / SAFE TO PUSH
only when the entire range is covered.
```

Expected: explicit coverage and `NO ACTIONABLE ISSUES / SAFE TO PUSH`. Any actionable finding is fixed with TDD, relevant and broad tests rerun, and a fresh independent reviewer examines the new exact HEAD.

- [ ] **Step 2: Refresh remote state without expanding the pinned KIRO target**

```bash
git fetch --prune --tags origin
git fetch --prune --tags upstream
git fetch --prune nianzs main
git rev-parse origin/dev upstream/main refs/remotes/nianzs/main
```

If `origin/dev` moved, semantically integrate that drift and repeat verification/review. If nianzs moved beyond `006af638`, keep this audit pinned unless the user explicitly expands it.

- [ ] **Step 3: Merge into dev and push without force**

After all gates pass, fast-forward or merge the reviewed branch into `dev` without rewriting history, update local `main` with `--ff-only` from `origin/main`, and push `dev`/`main` non-force. Record the exact remote `dev` SHA.

- [ ] **Step 4: Wait for required CI on the exact SHA**

Required checks are shell deployment-script tests, backend generate/build/unit/integration, frontend install/test/build, and golangci-lint. Do not declare completion while any required run is pending, skipped, or failed.

- [ ] **Step 5: Record final review, SHA, CI links, and rollback point**

Update the archive with the exact pushed SHA, required-run results, and `backup/dev-before-kiro-sync-20260716-006af638`. Commit/push that archive update and wait for its own required CI if it changes `dev`.

---

## Plan Self-Review

- Spec coverage: every selected KIRO capability maps to Tasks 2–7; dual-upstream background and every excluded/superseded capability map to Task 1; final reference gating maps to Tasks 8–9.
- Placeholder scan: every change step names exact behavior, files, tests, commands, and expected results; reusable runbook commands use explicit shell variable names.
- Type consistency: provider constants, `RefreshIDCToken`, `EstimateImageTokens`, context-aware estimator signatures, and `KiroEndpointModeAuto` are defined before dependent tasks use them.
- Protection coverage: explicit tests cover old provider aliases, `api_region`, profile ARN, stable machine ID, API Key forced Q, direct/relay distinction, KIRO usage/cache, and tool-stream stop reasons.
