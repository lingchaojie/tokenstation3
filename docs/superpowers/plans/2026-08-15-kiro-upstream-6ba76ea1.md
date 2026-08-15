# KIRO Upstream 6ba76ea1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Semantically integrate the approved KIRO changes from nianzs `006af638..6ba76ea1`, including 34 AWS regions, without regressing TokenStation3's local KIRO runtime, accounting, capture, or scheduling behavior.

**Architecture:** Add narrow compatibility helpers and tests at the existing KIRO, Responses bridge, shared gateway, and administrator UI boundaries. Keep remote compaction and cache emulation transactional inside focused new units, then connect them to the current strict forwarding state machines instead of copying nianzs gateway files wholesale.

**Tech Stack:** Go 1.24, Gin, tidwall gjson/sjson, testify, Vue 3, TypeScript 5.6, Vitest, pnpm 9.

## Global Constraints

- Work only in `/home/alvin/tokenstation3/.worktrees/kiro-upstream-6ba76ea1` on `sync/kiro-upstream-6ba76ea1`.
- Use `6ba76ea105e065a5aa8dd2b8d2957528ed58935b` as the nianzs behavioral reference and `05d8f0eccfc203e5bf5b84f84af081651c552a9b` as the local base.
- Do not cherry-pick shared nianzs gateway files; preserve local profile ARN, machine ID, direct/relay, image security, External IdP, usage presence bits, provider-native capture, failover, and terminal-state behavior.
- Keep `credentials.region` and `credentials.api_region` independent. Only API-key legacy credentials may fall back from `api_region`/`apiRegion` to `region`.
- Do not add KIRO upstream billing probe, split cache ratios, pacing removal, prompt rules, profit control, or upstream-cost scheduling.
- Do not access production or call a real provider. Tests must use local fixtures and test servers.
- Write and run a failing regression test before every production behavior change.

---

### Task 1: Complete the KIRO model contract

**Files:**
- Modify: `backend/internal/domain/constants.go`
- Modify: `backend/internal/domain/constants_test.go`
- Modify: `backend/internal/pkg/kiro/models.go`
- Modify: `backend/internal/pkg/kiro/models_test.go`
- Modify: `backend/internal/pkg/kiro/translator.go`
- Modify: `backend/internal/pkg/kiro/translator_test.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/gateway_models_test.go`
- Modify: `backend/internal/service/admin_group.go`
- Modify: `backend/internal/service/admin_service_group_test.go`
- Modify: `frontend/src/composables/__tests__/useModelWhitelist.spec.ts`

**Interfaces:**
- Consumes: `domain.DefaultKiroModelMapping`, `kiro.DefaultModels`, `kiro.MapModel(string) string`.
- Produces: KIRO fallback model IDs for public/admin listing and exact GPT-5.6/Opus 5 translation behavior.

- [x] **Step 1: Add failing model mapping and catalog tests**

Add exact assertions:

```go
func TestDefaultKiroModelMappingIncludesGPT56AndOpus5(t *testing.T) {
	require.Equal(t, "gpt-5.6-sol", DefaultKiroModelMapping["gpt-5.6-sol"])
	require.Equal(t, "gpt-5.6-terra", DefaultKiroModelMapping["gpt-5.6-terra"])
	require.Equal(t, "gpt-5.6-luna", DefaultKiroModelMapping["gpt-5.6-luna"])
	require.Equal(t, "gpt-5.6-luna", DefaultKiroModelMapping["codex-auto-review"])
	require.Equal(t, "claude-opus-5", DefaultKiroModelMapping["claude-opus-5"])
	require.Equal(t, "claude-opus-5", DefaultKiroModelMapping["claude-opus-5-thinking"])
}
```

```go
func TestMapModelSupportsGPT56AndOpus5(t *testing.T) {
	tests := map[string]string{
		"gpt-5.6-sol": "gpt-5.6-sol",
		"gpt-5.6-terra": "gpt-5.6-terra",
		"gpt-5.6-luna": "gpt-5.6-luna",
		"claude-opus-5": "claude-opus-5",
		"claude-opus-5-thinking": "claude-opus-5",
	}
	for input, want := range tests {
		require.Equal(t, want, MapModel(input), input)
	}
}
```

Add fallback-list tests that call `defaultModelIDsForPlatform(PlatformKiro)` and `defaultModelsListCandidateIDs(PlatformKiro)`, compare them to the IDs in `kiro.DefaultModels`, and assert GPT-5.6/Opus 5 are present.

- [x] **Step 2: Run the focused tests and confirm RED**

Run:

```bash
cd backend
go test -tags=unit -count=1 ./internal/domain ./internal/pkg/kiro ./internal/handler ./internal/service -run 'Kiro.*(Model|Fallback)|DefaultKiro|MapModel|Opus5|GPT56'
```

Expected: failures for missing GPT-5.6, Opus 5, `codex-auto-review`, and missing KIRO fallback cases.

- [x] **Step 3: Add the exact model entries and predicates**

Add these mappings and catalog entries:

```go
"gpt-5.6-sol":            "gpt-5.6-sol",
"gpt-5.6-terra":          "gpt-5.6-terra",
"gpt-5.6-luna":           "gpt-5.6-luna",
"codex-auto-review":      "gpt-5.6-luna",
"claude-opus-5":          "claude-opus-5",
"claude-opus-5-thinking": "claude-opus-5",
```

Extend `MapModel`, `requiresImplicitThinkingTagStripping`, `kiroMaxOutputTokensForModel`, `thinkingDirectiveFromModel`, and `isOutputConfigPathModel`. GPT-5.6 variants map exactly and use 128,000 output tokens; Opus 5 maps to `claude-opus-5`, strips implicit thinking tags, uses the adaptive high-thinking path, and uses 128,000 output tokens.

Return KIRO catalog IDs from both fallback switches:

```go
case PlatformKiro:
	models := make([]string, 0, len(kiropkg.DefaultModels))
	for _, model := range kiropkg.DefaultModels {
		models = append(models, model.ID)
	}
	return models
```

Use the existing import alias style in each package; do not touch local billing/usage files from `9f41d6b`.

- [x] **Step 4: Run backend and frontend model tests GREEN**

Run:

```bash
cd backend
go test -tags=unit -count=1 ./internal/domain ./internal/pkg/kiro ./internal/handler ./internal/service -run 'Kiro.*(Model|Fallback)|DefaultKiro|MapModel|Opus5|GPT56'
cd ../frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/composables/__tests__/useModelWhitelist.spec.ts
```

Expected: all selected tests pass and `frontend/package.json` remains unchanged.

- [x] **Step 5: Commit the model contract**

```bash
git add backend/internal/domain/constants.go backend/internal/domain/constants_test.go backend/internal/pkg/kiro/models.go backend/internal/pkg/kiro/models_test.go backend/internal/pkg/kiro/translator.go backend/internal/pkg/kiro/translator_test.go backend/internal/handler/gateway_handler.go backend/internal/handler/gateway_models_test.go backend/internal/service/admin_group.go backend/internal/service/admin_service_group_test.go frontend/src/composables/__tests__/useModelWhitelist.spec.ts
git commit -m "feat(kiro): support GPT-5.6 and Opus 5 models"
```

### Task 2: Expand and separate AWS region selection

**Files:**
- Modify: `backend/internal/service/kiro_http_helpers.go`
- Modify: `backend/internal/service/kiro_http_helpers_test.go`
- Modify: `frontend/src/utils/kiroAccount.ts`
- Modify: `frontend/src/utils/__tests__/kiroAccount.spec.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/admin/account/ReAuthAccountModal.vue`
- Modify: `frontend/src/components/account/__tests__/CreateAccountModal.spec.ts`
- Modify: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`
- Modify: `frontend/src/components/admin/account/__tests__/ReAuthAccountModal.kiro.spec.ts`
- Modify: `frontend/src/i18n/locales/en/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/accounts.ts`

**Interfaces:**
- Consumes: `buildKiroAPIRegionOptions`, current `Select` component, credential merge paths.
- Produces: a 34-code `KIRO_API_REGIONS` catalog and `kiroAPIRegion(*Account) string` with independent OAuth/API-key fallback rules.

- [x] **Step 1: Add failing backend and frontend region tests**

Backend cases:

```go
func TestKiroAPIRegionCredentialPrecedence(t *testing.T) {
	require.Equal(t, "eu-west-1", kiroAPIRegion(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"api_region": "eu-west-1", "apiRegion": "us-west-2", "region": "ap-south-1"}}))
	require.Equal(t, "us-west-2", kiroAPIRegion(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"apiRegion": "us-west-2", "region": "ap-south-1"}}))
	require.Equal(t, "ap-south-1", kiroAPIRegion(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"region": "ap-south-1"}}))
	require.Equal(t, kiroDefaultRegion, kiroAPIRegion(&Account{Type: AccountTypeOAuth, Credentials: map[string]any{"region": "ap-south-1"}}))
}
```

Frontend catalog assertions:

```ts
expect(KIRO_API_REGIONS).toHaveLength(34)
expect(KIRO_API_REGIONS).toContain('us-east-1')
expect(KIRO_API_REGIONS).toContain('ap-southeast-7')
expect(KIRO_API_REGIONS).toContain('mx-central-1')
expect(KIRO_API_REGIONS).toContain('sa-east-1')
```

Update modal tests to select `eu-west-1`, submit, and assert `region` and `api_region` are persisted independently. Add an OAuth regression whose IDC region is `ap-south-1` while API region remains `eu-central-1`.

- [x] **Step 2: Run region tests and confirm RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service -run 'KiroAPIRegion'
cd ../frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/utils/__tests__/kiroAccount.spec.ts src/components/account/__tests__/CreateAccountModal.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts src/components/admin/account/__tests__/ReAuthAccountModal.kiro.spec.ts
```

Expected: failures for catalog length, missing options, camelCase fallback, and OAuth incorrectly falling back to IDC region.

- [x] **Step 3: Expand the catalog and fix backend precedence**

Set `KIRO_API_REGIONS` to the exact 34 codes from nianzs `frontend/src/constants/kiroRegions.ts`, preserving `us-east-1` as the first/default value. Keep `buildKiroAPIRegionOptions` so an existing non-list value is appended as a disabled legacy option.

Implement:

```go
func kiroAPIRegion(account *Account) string {
	if account == nil {
		return kiroDefaultRegion
	}
	region := strings.TrimSpace(account.GetCredential("api_region"))
	if region == "" {
		region = strings.TrimSpace(account.GetCredential("apiRegion"))
	}
	if region == "" && account.Type == AccountTypeAPIKey {
		region = strings.TrimSpace(account.GetCredential("region"))
	}
	if region == "" {
		region = kiroDefaultRegion
	}
	return region
}
```

Use `Select` with the shared option builder for IDC and API-region controls in create/edit/reauthorize flows. Do not render or rewrite API region for KIRO relay API keys.

- [x] **Step 4: Run region suites GREEN**

Repeat Step 2 and also run:

```bash
cd frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm run typecheck
```

Expected: tests and typecheck pass; create/edit/reauth preserve independent credential keys.

- [x] **Step 5: Commit region support**

```bash
git add backend/internal/service/kiro_http_helpers.go backend/internal/service/kiro_http_helpers_test.go frontend/src/utils/kiroAccount.ts frontend/src/utils/__tests__/kiroAccount.spec.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/components/account/__tests__/CreateAccountModal.spec.ts frontend/src/components/account/__tests__/EditAccountModal.spec.ts frontend/src/components/admin/account/__tests__/ReAuthAccountModal.kiro.spec.ts frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/zh/admin/accounts.ts
git commit -m "feat(kiro): support 34 AWS regions"
```

### Task 3: Exclude KIRO from Claude Code OAuth mimicry

**Files:**
- Modify: `backend/internal/service/gateway_claude_oauth_body.go`
- Create: `backend/internal/service/gateway_claude_oauth_mimicry_gate_test.go`
- Modify: `backend/internal/service/gateway_forward.go`
- Modify: `backend/internal/service/gateway_forward_as_responses.go`
- Modify: `backend/internal/service/gateway_count_tokens.go`

**Interfaces:**
- Produces: `shouldMimicClaudeCodeForAccount(account *Account, isClaudeCodeClient bool) bool`.
- Consumes: `Account.IsOAuth()` and `Account.IsKiro()`.

- [x] **Step 1: Add the failing table test**

```go
func TestShouldMimicClaudeCodeForAccount(t *testing.T) {
	tests := []struct {
		account *Account
		isCC bool
		want bool
	}{
		{&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, false, true},
		{&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, true, false},
		{&Account{Platform: PlatformKiro, Type: AccountTypeOAuth}, false, false},
		{&Account{Platform: PlatformKiro, Type: AccountTypeSetupToken}, false, false},
		{&Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, false, false},
		{nil, false, false},
	}
	for _, test := range tests {
		require.Equal(t, test.want, shouldMimicClaudeCodeForAccount(test.account, test.isCC))
	}
}
```

- [x] **Step 2: Confirm the helper is missing**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service -run TestShouldMimicClaudeCodeForAccount
```

Expected: compile failure because the helper does not exist.

- [x] **Step 3: Add the predicate and use it consistently**

```go
func shouldMimicClaudeCodeForAccount(account *Account, isClaudeCodeClient bool) bool {
	if account == nil || isClaudeCodeClient {
		return false
	}
	return account.IsOAuth() && !account.IsKiro()
}
```

Replace the three local `account.IsOAuth() && !isClaudeCode` predicates in the Messages/Chat path, Responses path, and count-tokens path with this helper. Do not change the mimicry transform itself.

- [x] **Step 4: Run service tests GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service -run 'ShouldMimicClaudeCodeForAccount|ClaudeOAuth|Kiro.*Responses|CountTokens'
```

- [x] **Step 5: Commit the mimicry gate**

```bash
git add backend/internal/service/gateway_claude_oauth_body.go backend/internal/service/gateway_claude_oauth_mimicry_gate_test.go backend/internal/service/gateway_forward.go backend/internal/service/gateway_forward_as_responses.go backend/internal/service/gateway_count_tokens.go
git commit -m "fix(kiro): bypass Claude Code OAuth mimicry"
```

### Task 4: Preserve namespace custom tools and buffered tool input

**Files:**
- Modify: `backend/internal/pkg/apicompat/responses_namespace.go`
- Modify: `backend/internal/pkg/apicompat/responses_client_tools.go`
- Modify: `backend/internal/pkg/apicompat/responses_stream_event_wire.go`
- Create: `backend/internal/pkg/apicompat/responses_namespace_custom_child_test.go`
- Modify: `backend/internal/service/gateway_forward_as_responses.go`
- Modify: `backend/internal/service/gateway_forward_as_responses_tool_input_test.go`

**Interfaces:**
- Produces: `isFlattenableNamespaceChild(map[string]any) bool`, `isNamespaceQualifiedCallType(string) bool`, and `isEmptyJSONObject(json.RawMessage) bool`.
- Consumes: `ResponsesClientToolMapping.NamespaceTools` and existing stream restorer state.

- [x] **Step 1: Add failing namespace custom-child tests**

Build a request with a namespace named `functions` containing a custom child named `exec`; call `AdaptResponsesClientTools`; assert the outgoing tool is a flattened function named `functions__exec`, history calls are flattened, and restored non-stream plus SSE outputs contain `type: custom_tool_call`, `namespace: functions`, `name: exec`.

Core request fixture:

```go
req := map[string]any{
	"tools": []any{map[string]any{
		"type": "namespace", "name": "functions",
		"tools": []any{map[string]any{"type": "custom", "name": "exec", "format": map[string]any{"type": "text"}}},
	}},
	"input": []any{map[string]any{"type": "custom_tool_call", "namespace": "functions", "name": "exec", "input": "ls"}},
}
```

Add a buffered accumulator regression that starts with `json.RawMessage("{}")`, appends `{"cmd":"ls"}`, and expects exactly `{"cmd":"ls"}`.

- [x] **Step 2: Run namespace/tool tests RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/apicompat ./internal/service -run 'Namespace.*Custom|Buffered.*ToolInput|AppendRawJSON'
```

Expected: custom namespace child is omitted/not restored and buffered input is `{}{...}`.

- [x] **Step 3: Implement namespace flattening/restoration and placeholder replacement**

```go
func isFlattenableNamespaceChild(child map[string]any) bool {
	switch strings.TrimSpace(stringValue(child["type"])) {
	case "function", "custom":
		return true
	default:
		return false
	}
}

func isNamespaceQualifiedCallType(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "function_call", "custom_tool_call":
		return true
	default:
		return false
	}
}
```

Use these helpers in declaration flattening, history rewrite, non-stream restore, item restore, and stream lifecycle routing. Include `response.custom_tool_call_input.delta` and `.done` in lifecycle restoration and emit namespace in `responsesItemWire` for `custom_tool_call`.

```go
func isEmptyJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return false
	}
	return len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0
}
```

Make `appendRawJSON` replace, rather than append to, an empty-object placeholder.

- [x] **Step 4: Run bridge and service tests GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/apicompat ./internal/service -run 'Namespace|ClientTool|Buffered.*ToolInput|AppendRawJSON|ForwardAsResponses.*Tool'
```

- [x] **Step 5: Commit custom-tool compatibility**

```bash
git add backend/internal/pkg/apicompat/responses_namespace.go backend/internal/pkg/apicompat/responses_client_tools.go backend/internal/pkg/apicompat/responses_stream_event_wire.go backend/internal/pkg/apicompat/responses_namespace_custom_child_test.go backend/internal/service/gateway_forward_as_responses.go backend/internal/service/gateway_forward_as_responses_tool_input_test.go
git commit -m "fix(kiro): preserve namespaced custom tool calls"
```

### Task 5: Fix KIRO translator edge cases

**Files:**
- Modify: `backend/internal/pkg/kiro/translator.go`
- Modify: `backend/internal/pkg/kiro/translator_test.go`
- Create: `backend/internal/pkg/kiro/midcontent_newline_test.go`
- Create: `backend/internal/pkg/kiro/tool_result_input_text_test.go`

**Interfaces:**
- Consumes: `normalizeStreamingToolInput`, stream text writer, Responses-to-KIRO tool-result extraction.
- Produces: valid `{}` for zero-argument tools, preserved mid-stream blank lines, and `input_text` tool-result conversion.

- [x] **Step 1: Add three failing regression groups**

```go
func TestNormalizeStreamingToolInputEmpty(t *testing.T) {
	jsonText, input, ok := normalizeStreamingToolInput("ExitPlanMode", "")
	require.True(t, ok)
	require.Equal(t, "{}", jsonText)
	require.Empty(t, input)

	_, _, ok = normalizeStreamingToolInput("Read", "")
	require.False(t, ok)
}
```

Add an event-stream fixture with text deltas `"# Heading"`, `"\n\n"`, and `"Body"`; assert the rendered output contains `# Heading\n\nBody`. Add a tool result with `content:[{"type":"input_text","text":"command output"}]`; assert the KIRO user tool-result text contains `command output`.

- [x] **Step 2: Run translator tests RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/kiro -run 'NormalizeStreamingToolInputEmpty|MidContent|ToolResult.*InputText'
```

- [x] **Step 3: Implement the minimal translator changes**

At the start of `normalizeStreamingToolInput`:

```go
if normalized == "" {
	if hasToolRequirements(name) {
		return "", nil, false
	}
	return "{}", map[string]any{}, true
}
```

Track whether visible text has already been emitted. Drop whitespace-only chunks only before the first visible text and at final trailing flush; pass through whitespace-only chunks between visible chunks. Accept both `text` and `input_text` in the tool-result content switch.

- [x] **Step 4: Run all KIRO translator tests GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/kiro
```

- [x] **Step 5: Commit translator fixes**

```bash
git add backend/internal/pkg/kiro/translator.go backend/internal/pkg/kiro/translator_test.go backend/internal/pkg/kiro/midcontent_newline_test.go backend/internal/pkg/kiro/tool_result_input_text_test.go
git commit -m "fix(kiro): preserve translator edge cases"
```

### Task 6: Add the reusable Responses compaction protocol

**Files:**
- Create: `backend/internal/pkg/apicompat/responses_compaction.go`
- Create: `backend/internal/pkg/apicompat/responses_compaction_test.go`
- Modify: `backend/internal/pkg/apicompat/types.go`
- Modify: `backend/internal/pkg/apicompat/responses_to_anthropic_request.go`
- Modify: `backend/internal/service/openai_gateway_grok_compact.go`

**Interfaces:**
- Produces: `HasCompactionTrigger(*ResponsesRequest) bool`, `EncodeCompactionEnvelope(string) string`, `DecodeCompactionEnvelope(string) (string, bool)`, `CompactionSummaryFromItem(*ResponsesInputItem) string`, `WrapCompactionSummaryForReplay(string) string`, and shared `CompactionSummaryPrompt`.
- Consumes: `ResponsesInputItem`, `ResponsesSummary`, and current Grok compact bridge.

- [x] **Step 1: Add failing compaction codec and request-conversion tests**

Cover envelope round trip, blank rejection, foreign payload rejection, trigger detection, prior compaction replay, plaintext-summary precedence, missing-summary skip, and trigger-to-summary-prompt conversion. Use a Unicode summary to verify base64url envelope fidelity.

```go
func TestCompactionEnvelopeRoundTrip(t *testing.T) {
	summary := "用户要求继续修改 🙂"
	encrypted := EncodeCompactionEnvelope(summary)
	require.NotEmpty(t, encrypted)
	decoded, ok := DecodeCompactionEnvelope(encrypted)
	require.True(t, ok)
	require.Equal(t, summary, decoded)
}
```

- [x] **Step 2: Run API compatibility tests RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/apicompat -run 'Compaction|ResponsesToAnthropicRequest_Compaction'
```

Expected: new compaction functions/types are missing and triggers are dropped.

- [x] **Step 3: Implement the protocol unit**

Use the `sub2api-compaction-v1.` prefix followed by raw URL-safe base64 of:

```go
type compactionEnvelope struct {
	Version int    `json:"v"`
	Summary string `json:"summary"`
}
```

Keep the existing `ResponsesInputItem.EncryptedContent` field and add
`Summary []ResponsesSummary` plus `Status string`, both with `omitempty`.
Convert `compaction_trigger` to one user summary-prompt item and a replayable
`compaction`/`compaction_summary` item to
`<conversation_summary>...</conversation_summary>` text. Replace the duplicate
Grok prompt with `apicompat.CompactionSummaryPrompt`.

- [x] **Step 4: Run API compatibility and Grok compact tests GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/apicompat ./internal/service -run 'Compaction|GrokCompact'
```

- [x] **Step 5: Commit the compaction protocol**

```bash
git add backend/internal/pkg/apicompat/responses_compaction.go backend/internal/pkg/apicompat/responses_compaction_test.go backend/internal/pkg/apicompat/types.go backend/internal/pkg/apicompat/responses_to_anthropic_request.go backend/internal/service/openai_gateway_grok_compact.go
git commit -m "feat(responses): add replayable compaction protocol"
```

### Task 7: Connect KIRO remote compaction to the strict gateway

**Files:**
- Create: `backend/internal/service/gateway_forward_as_responses_compaction.go`
- Create: `backend/internal/service/gateway_forward_as_responses_compaction_test.go`
- Modify: `backend/internal/service/gateway_forward_as_responses.go`
- Modify: `frontend/src/i18n/locales/en/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/accounts.ts`

**Interfaces:**
- Consumes: Task 6 compaction helpers, `Account.ResolveCompactMappedModel`, current Anthropic SSE collector, capture/usage ownership in `ForwardAsResponses`.
- Produces: `handleResponsesCompactionResponse(...) (*ForwardResult, error)` and one compaction item for stream/non-stream callers.

- [ ] **Step 1: Add failing request and response integration tests**

Add local HTTP/SSE fixtures and assert:

```go
require.Equal(t, "claude-haiku-4-5", gjson.Get(upstreamBody, "model").String())
require.Contains(t, upstreamBody, "produce a faithful, concise summary")
require.Equal(t, "none", gjson.Get(upstreamBody, "tool_choice.type").String())
require.GreaterOrEqual(t, int(gjson.Get(upstreamBody, "max_tokens").Int()), 32000)
require.False(t, gjson.Get(upstreamBody, "thinking").Exists())
```

For streaming and non-streaming results, assert output has exactly one `compaction` item with non-empty `encrypted_content`. Add empty-summary/no-message-start failures that never emit a compaction item, and a regression that ordinary Responses requests remain unchanged.

- [ ] **Step 2: Run service compaction tests RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service -run 'ForwardAsResponses_Compaction|HandleResponsesCompaction|AnthropicResponseText'
```

- [ ] **Step 3: Rewrite the compact request before upstream forwarding**

In `ForwardAsResponses`, compute
`isCompaction := account.IsKiro() && apicompat.HasCompactionTrigger(&responsesReq)`.
The KIRO gate prevents this sync from changing unrelated Anthropic/Bedrock
compact behavior. When true:

```go
if compactModel, matched := account.ResolveCompactMappedModel(originalModel); matched {
	mappedModel = compactModel
}
anthropicReq.Model = mappedModel
anthropicReq.ToolChoice = json.RawMessage(`{"type":"none"}`)
anthropicReq.MaxTokens = max(anthropicReq.MaxTokens, compactionMinMaxTokens)
anthropicReq.Thinking = nil
anthropicReq.OutputConfig = nil
reasoningEffort = nil
```

Keep tool declarations because historical `tool_use` blocks can reference them. Route the successful upstream response to `handleResponsesCompactionResponse`; do not route ordinary requests through the new handler.

- [ ] **Step 4: Synthesize strict stream/non-stream compaction output**

Create a summary from visible Anthropic `text` blocks only, encode it with `EncodeCompactionEnvelope`, and emit one item:

```go
item := map[string]any{
	"id":                "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
	"type":              apicompat.CompactionItemType,
	"status":            "completed",
	"encrypted_content": encrypted,
	"summary": []any{map[string]any{"type": "summary_text", "text": summary}},
}
```

For stream callers, use Responses lifecycle events ending in `response.completed`; for non-stream callers return one JSON response. If no visible summary exists, emit `response.failed` only before semantic commitment and return an error so local failover policy remains authoritative. Retain current capture and exact-once usage ownership.

Add missing `fromModel` and `toModel` locale keys at `admin.accounts` because the existing compact mapping UI already references them.

- [ ] **Step 5: Run compaction and strict-state suites GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/apicompat ./internal/service -run 'Compaction|ForwardAsResponses|Terminal|Capture'
cd ../frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm run typecheck
```

- [ ] **Step 6: Commit KIRO compaction**

```bash
git add backend/internal/service/gateway_forward_as_responses_compaction.go backend/internal/service/gateway_forward_as_responses_compaction_test.go backend/internal/service/gateway_forward_as_responses.go frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/zh/admin/accounts.ts
git commit -m "feat(kiro): support Responses remote compaction"
```

### Task 8: Restore direct KIRO usage loading in the account table

**Files:**
- Modify: `frontend/src/views/admin/AccountsView.vue`
- Create: `frontend/src/views/admin/__tests__/AccountsView.kiroUsage.spec.ts`

**Interfaces:**
- Consumes: `accountSupportsBatchUsage(account: Account)`, `AccountUsageCell.requestBatchedUsage`.
- Produces: null batching callback for KIRO rows so `AccountUsageCell` uses `adminAPI.accounts.getUsage`.

- [ ] **Step 1: Add a failing mounted-view regression**

Stub `DataTable` to render `cell-usage` for a KIRO row and stub `AccountUsageCell` to expose whether `requestBatchedUsage` is present. Assert desktop KIRO receives `null`, while Anthropic OAuth receives a function.

```ts
expect(wrapper.get('[data-testid="kiro-batch-managed"]').text()).toBe('false')
expect(wrapper.get('[data-testid="anthropic-batch-managed"]').text()).toBe('true')
```

- [ ] **Step 2: Run the view test RED**

```bash
cd frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/views/admin/__tests__/AccountsView.kiroUsage.spec.ts
```

- [ ] **Step 3: Gate the callback with the same capability predicate**

```vue
:request-batched-usage="
  isDesktopViewport && accountSupportsBatchUsage(row) ? queueBatchedUsage : null
"
```

- [ ] **Step 4: Run usage tests GREEN**

```bash
cd frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/views/admin/__tests__/AccountsView.kiroUsage.spec.ts src/components/account/__tests__/AccountUsageCell.spec.ts
```

- [ ] **Step 5: Commit the usage fix**

```bash
git add frontend/src/views/admin/AccountsView.vue frontend/src/views/admin/__tests__/AccountsView.kiroUsage.spec.ts
git commit -m "fix(kiro): keep direct account usage loading"
```

### Task 9: Make KIRO cache emulation transactional

**Files:**
- Modify: `backend/internal/service/kiro_cache_emulation.go`
- Modify: `backend/internal/service/kiro_cache_emulation_test.go`
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_websearch.go`
- Modify: `backend/internal/service/gateway_forward_as_responses.go`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions.go`
- Modify: `backend/internal/service/gateway_forward_as_responses_cache_only_test.go`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions_cache_only_test.go`

**Interfaces:**
- Produces: `kiroCacheEmulationPlan`, `result() *kiroCacheEmulationUsage`, `commit()`, and prepare variants of existing build helpers.
- Consumes: local `resolveKiroCacheEmulation`, tracker compute/update, KIRO direct/web-search success boundaries.

- [ ] **Step 1: Add tracker pollution and successful-commit tests**

```go
func TestKiroCacheEmulationPlanCommitsExplicitly(t *testing.T) {
	tracker := &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
	fingerprint := sha256.Sum256([]byte("prefix"))
	profile := &kiroCacheProfile{
		totalInputTokens: 4096,
		minCacheable:     1024,
		blocks:           []kiroCacheBlock{{prefixFingerprint: fingerprint, cumulativeTokens: 4096}},
		breakpoints:      []kiroCacheBreakpoint{{blockIndex: 0, ttl: kiroCacheDefaultTTL}},
	}
	plan := &kiroCacheEmulationPlan{
		usage: &kiroCacheEmulationUsage{CacheCreationInputTokens: 4096},
		cacheKey: 42,
		profile: profile,
		tracker: tracker,
	}
	require.Empty(t, tracker.entries)
	require.Equal(t, 4096, plan.result().CacheCreationInputTokens)
	plan.commit()
	require.NotEmpty(t, tracker.entries[42])
}
```

Add gateway regressions where the first KIRO upstream attempt fails before 2xx and the next identical request is still a cache creation, plus a success case where the next request becomes a cache read. Add the same first-success boundary to web search.

- [ ] **Step 2: Run cache tests RED**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service -run 'KiroCache.*(Prepare|Commit|Pollution|Success)|CacheOnly'
```

- [ ] **Step 3: Split prepare and commit without changing accounting**

```go
type kiroCacheEmulationPlan struct {
	usage    *kiroCacheEmulationUsage
	cacheKey uint64
	profile  *kiroCacheProfile
	tracker  *kiroCacheTracker
}

func (p *kiroCacheEmulationPlan) result() *kiroCacheEmulationUsage {
	if p == nil {
		return nil
	}
	return p.usage
}

func (p *kiroCacheEmulationPlan) commit() {
	if p == nil || p.tracker == nil || p.profile == nil || p.cacheKey == 0 {
		return
	}
	p.tracker.update(p.cacheKey, p.profile)
}
```

Prepare computes against the tracker but never updates it. Commit after a normal direct request receives 2xx, and after the first successful web-search iteration. Existing `build...Usage` wrappers may prepare+commit for callers whose success boundary is already established. Preserve local ratio resolution, presence bits, pure-cache usage, capture, and client-disconnect behavior.

- [ ] **Step 4: Run KIRO/service cache suites GREEN**

```bash
cd backend
go test -tags=unit -count=1 ./internal/service ./internal/pkg/kiro -run 'KiroCache|CacheOnly|WebSearch|Usage'
```

- [ ] **Step 5: Commit transactional cache state**

```bash
git add backend/internal/service/kiro_cache_emulation.go backend/internal/service/kiro_cache_emulation_test.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_websearch.go backend/internal/service/gateway_forward_as_responses.go backend/internal/service/gateway_forward_as_chat_completions.go backend/internal/service/gateway_forward_as_responses_cache_only_test.go backend/internal/service/gateway_forward_as_chat_completions_cache_only_test.go
git commit -m "fix(kiro): commit cache emulation after upstream success"
```

### Task 10: Audit, archive, pin, and verify the sync

**Files:**
- Create: `docs/upstream-sync/2026-08-15-kiro-006af638-6ba76ea1.md`
- Modify: `docs/kiro-upstream-sync.md`

**Interfaces:**
- Consumes: all prior commits and test evidence.
- Produces: reproducible semantic-sync archive and updated KIRO reference pin.

- [ ] **Step 1: Audit approved and excluded capabilities**

Run:

```bash
git diff --stat dev...HEAD
git diff dev...HEAD -- backend/internal/pkg/kiro backend/internal/pkg/apicompat backend/internal/service backend/internal/handler frontend/src/components frontend/src/views/admin frontend/src/utils docs
git log --reverse --no-merges --oneline 006af638390c0e929204a2486d696c302ad5bc07..6ba76ea105e065a5aa8dd2b8d2957528ed58935b
rg -n "Kiro|kiro|KIRO" backend frontend deploy AGENTS.md docs/kiro-upstream-sync.md
```

Confirm the diff contains no KIRO billing-probe eligibility, split cache ratios, pacing removal, prompt rules, profit control, or upstream-cost scheduling.

- [ ] **Step 2: Run full focused verification**

```bash
cd backend
go test -tags=unit -count=1 ./internal/pkg/kiro ./internal/pkg/apicompat ./internal/service ./internal/handler
go build ./cmd/server
make check-generate
cd ../frontend
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm exec vitest run src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts src/components/account/__tests__/CreateAccountModal.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts src/components/admin/account/__tests__/ReAuthAccountModal.kiro.spec.ts src/composables/__tests__/useModelWhitelist.spec.ts src/utils/__tests__/kiroAccount.spec.ts src/utils/__tests__/kiroEndpointMode.spec.ts src/views/admin/__tests__/AccountsView.kiroUsage.spec.ts src/components/account/__tests__/AccountUsageCell.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm run lint:check
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm run build
```

Expected: every command succeeds, no generated-file drift, and no unintended `packageManager` field appears.

- [ ] **Step 3: Write the sync archive with exact evidence**

Record:

```markdown
- Local base: `05d8f0eccfc203e5bf5b84f84af081651c552a9b`
- Previous KIRO reference: `006af638390c0e929204a2486d696c302ad5bc07`
- New KIRO reference: `6ba76ea105e065a5aa8dd2b8d2957528ed58935b`
- New reference merge parents: `a511da0873cf24f580fa70db15be2748a6fa5f7b`, `158f5b283d81a9a04d1222772be2c971dc9810a5`
```

List each included capability, each explicit exclusion with its reason, all preserved local invariants, and the exact fresh verification commands/results.

- [ ] **Step 4: Advance the runbook pin only after verification**

Replace only the reference SHA in `docs/kiro-upstream-sync.md`:

```text
6ba76ea105e065a5aa8dd2b8d2957528ed58935b
```

- [ ] **Step 5: Run final repository checks and commit documentation**

```bash
git diff --check
git status --short
git add docs/upstream-sync/2026-08-15-kiro-006af638-6ba76ea1.md docs/kiro-upstream-sync.md
git commit -m "docs: archive KIRO upstream 6ba76ea1 sync"
git status --short --branch
```

Expected: branch is clean and all approved implementation commits precede the documentation pin.
