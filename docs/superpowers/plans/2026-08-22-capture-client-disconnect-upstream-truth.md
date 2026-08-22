# Client Disconnect Capture Upstream Truth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist capture attempts interrupted by a downstream client while archiving only provider-observed stop reasons and continuing to discard real upstream failures when `terminal_error` capture is disabled.

**Architecture:** Add a private client-disconnect terminal class that bypasses only the success/error outcome toggles while retaining every scope and content filter. Make both gateway result families classify real upstream failure before downstream disconnect, propagate an explicit provider-terminal signal into `ResponseComplete`, and make the sidecar response extractor the sole authority for archived stop reasons.

**Tech Stack:** Go 1.26.6, Gin request scopes, synchronous Unix-socket capture protocol, zstd disk spool, ClickHouse RowBinary uploader, `testify/require`, Docker-hosted Go toolchain on this Windows workspace.

**Spec:** `docs/superpowers/specs/2026-08-22-capture-client-disconnect-upstream-truth-design.md`

## Global Constraints

- Preserve provider selection, retries, failover, billing, usage, and all client-visible HTTP/SSE behavior.
- Do not add a ClickHouse column, runtime-policy JSON field, protocol field, custom archived outcome, or custom archived stop reason.
- `pre_commit_disconnect` remains only an operational IPC loss reason and must never become an archived model stop reason.
- A real upstream failure (`UpstreamFailed` or `CaptureTerminalError`) always outranks `ClientDisconnect`.
- Client-disconnect capture bypasses only `outcomes.success` and `outcomes.terminal_error`; it must still satisfy runtime master, platform, model allowlist, user, group, and content filters.
- `ResponseComplete` describes a provider terminal boundary observed by the gateway, not whether the downstream client remained connected.
- Preserve the current fail-open capture contract: capture failure must not alter forwarding.
- Keep the known sidecar idle-probe/upload-loop exit bug out of these commits; repair and verify it under a separate design/plan before production release.
- Do not mutate production, restart production services, or send production provider traffic without a new explicit user confirmation.

---

### Task 1: Admit attempts that may end in a client disconnect

**Files:**
- Modify: `backend/internal/service/capture_runtime_policy.go:17-22,241-285`
- Modify: `backend/internal/service/capture_context.go:178-212`
- Test: `backend/internal/service/capture_runtime_policy_test.go`
- Test: `backend/internal/service/capture_context_test.go`

**Interfaces:**
- Consumes: `CaptureOutcome`, `CompiledCapturePolicy.Decide`, and the request-scoped platform/model/user/group snapshot.
- Produces: private `captureOutcomeClientDisconnect CaptureOutcome`; `CaptureDecisionFor` accepts it internally; `CaptureMayApplyFor` and `captureContentPolicyForAttempt` admit it before evaluating ordinary outcomes.

- [ ] **Step 1: Write the failing policy tests**

Add table-driven tests proving that the private class ignores only the outcome switches:

```go
func TestCompiledCapturePolicyClientDisconnectIgnoresOutcomeTogglesOnly(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = false
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)

	content, ok := compiled.DecideForModel(
		PlatformAnthropic,
		"claude-opus-5",
		captureOutcomeClientDisconnect,
		9,
		nil,
	)
	require.True(t, ok)
	require.Equal(t, policy.Content, content)

	_, ok = compiled.DecideForModel(
		PlatformOpenAI,
		"claude-opus-5",
		captureOutcomeClientDisconnect,
		9,
		nil,
	)
	require.False(t, ok, "platform filters still apply")
}
```

Extend the table with a model outside the Anthropic allowlist, a user mismatch, a group mismatch, disabled master policy, and unknown platform. Every case must be false.

In `capture_context_test.go`, add a request-scope test with both outcome flags false:

```go
func TestCaptureMayApplyForClientDisconnectWhenOrdinaryOutcomesAreOff(t *testing.T) {
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Outcomes.Success = false
	policy.Outcomes.TerminalError = false
	c, _, _, _ := newFinalAttemptFixture(t, policy)
	SetCaptureRequestedModel(c, "claude-opus-5")

	require.True(t, CaptureMayApplyFor(c, PlatformAnthropic))
	content, ok := captureContentPolicyForAttempt(c, PlatformAnthropic)
	require.True(t, ok)
	require.Equal(t, policy.Content, content)
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run from `D:\linix2ai`:

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'TestCompiledCapturePolicyClientDisconnect|TestCaptureMayApplyForClientDisconnect' -count=1
```

Expected: compilation fails because `captureOutcomeClientDisconnect` does not exist, or the admission assertion fails because both configured outcomes are off.

- [ ] **Step 3: Implement the private terminal class**

Add an unexported constant without extending `CaptureOutcomePolicy`:

```go
const (
	CaptureOutcomeSuccess       CaptureOutcome = "success"
	CaptureOutcomeTerminalError CaptureOutcome = "terminal_error"
	captureOutcomeClientDisconnect CaptureOutcome = "client_disconnect"
)
```

In `CompiledCapturePolicy.decide`, keep platform validation before outcome validation and add the bypass case:

```go
switch outcome {
case CaptureOutcomeSuccess:
	if !p.outcomes.Success {
		return CaptureContentPolicy{}, false
	}
case CaptureOutcomeTerminalError:
	if !p.outcomes.TerminalError {
		return CaptureContentPolicy{}, false
	}
case captureOutcomeClientDisconnect:
	// Deliberately bypass outcome toggles only. User/group/model checks below
	// and platform/master checks above remain authoritative.
default:
	return CaptureContentPolicy{}, false
}
```

Make the possible-disconnect class part of allocation and content admission:

```go
func CaptureMayApplyFor(c *gin.Context, platform string) bool {
	if _, ok := CaptureDecisionFor(c, platform, captureOutcomeClientDisconnect); ok {
		return true
	}
	if _, ok := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); ok {
		return true
	}
	_, ok := CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
	return ok
}

func captureContentPolicyForAttempt(c *gin.Context, platform string) (CaptureContentPolicy, bool) {
	if content, ok := CaptureDecisionFor(c, platform, captureOutcomeClientDisconnect); ok {
		return content, true
	}
	if content, ok := CaptureDecisionFor(c, platform, CaptureOutcomeSuccess); ok {
		return content, true
	}
	return CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
}
```

- [ ] **Step 4: Run the policy and allocation tests and verify GREEN**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'CaptureRuntimePolicy|CaptureMayApplyFor|CaptureDecisionFor' -count=1
```

Expected: PASS. Confirm the tests cover both-outcomes-off admission and every scope filter.

- [ ] **Step 5: Commit the policy boundary**

```powershell
git add backend/internal/service/capture_runtime_policy.go `
        backend/internal/service/capture_runtime_policy_test.go `
        backend/internal/service/capture_context.go `
        backend/internal/service/capture_context_test.go
git commit -m "feat: admit client-disconnect capture attempts"
```

---

### Task 2: Centralize terminal classification and preserve failure precedence

**Files:**
- Modify: `backend/internal/service/capture_context.go:355-480`
- Modify: `backend/internal/service/capture_record.go:1680-1724`
- Modify: `backend/internal/service/gateway_service.go:617-620`
- Modify: `backend/internal/service/openai_gateway_service.go:265-282`
- Test: `backend/internal/service/capture_context_test.go:855-956`

**Interfaces:**
- Consumes: `ForwardResult.UpstreamFailed`, `ForwardResult.CaptureTerminalError`, `ForwardResult.ClientDisconnect`, `ForwardResult.CaptureResponseComplete` and the equivalent OpenAI fields.
- Produces: `captureTerminalOutcome(upstreamFailed, terminalError, clientDisconnect bool) CaptureOutcome` and `captureFinalResponseComplete(upstreamFailed, terminalError, clientDisconnect, explicitlyComplete bool) bool`; both result families and legacy content-policy refresh paths use the same precedence.

- [ ] **Step 1: Write the failing terminal matrix tests**

Replace the old pre-commit custom-reason tests with a table that runs once for `ForwardResult` and once for `OpenAIForwardResult`. The core cases are:

```go
tests := []struct {
	name                 string
	upstreamFailed       bool
	terminalError        bool
	clientDisconnect     bool
	explicitlyComplete   bool
	wantOutcome          CaptureOutcome
	wantResponseComplete bool
}{
	{"success", false, false, false, false, CaptureOutcomeSuccess, true},
	{"disconnect partial", false, false, true, false, captureOutcomeClientDisconnect, false},
	{"disconnect drained terminal", false, false, true, true, captureOutcomeClientDisconnect, true},
	{"upstream failure beats disconnect", true, false, true, false, CaptureOutcomeTerminalError, false},
	{"semantic terminal error beats disconnect", false, true, true, true, CaptureOutcomeTerminalError, true},
}
```

Add an integration-style sink test using a policy with both outcomes off. Begin an Anthropic attempt, write a partial response, and assert a pure client disconnect commits with an empty `Final.StopReason`. Begin a second attempt with `ClientDisconnect=true` plus `UpstreamFailed=true`; assert it aborts because `terminal_error` is off.

Add the same pair for `CommitOpenAIForwardCaptureAttempt` with `Platforms.OpenAI=true`.

Add a bounded flood regression with `terminal_error=false`: create and finalize 50
attempts whose results have HTTP 503 plus `UpstreamFailed=true` (alternate
`ClientDisconnect` true/false). Assert all 50 attempts terminate as Abort and
none records a Final/Commit. This proves disconnect cannot turn a provider
outage into archived ready records.

- [ ] **Step 2: Run the focused tests and verify RED**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'CaptureTerminalOutcome|ClientDisconnect.*Commit|UpstreamFailure.*Disconnect|PreCommit' -count=1
```

Expected: the current code chooses client disconnect before upstream failure, writes `pre_commit_disconnect`, and forces every disconnect incomplete.

- [ ] **Step 3: Add the two shared helpers**

Implement the helpers beside the terminal sinks:

```go
func captureTerminalOutcome(upstreamFailed, terminalError, clientDisconnect bool) CaptureOutcome {
	if upstreamFailed || terminalError {
		return CaptureOutcomeTerminalError
	}
	if clientDisconnect {
		return captureOutcomeClientDisconnect
	}
	return CaptureOutcomeSuccess
}

func captureFinalResponseComplete(upstreamFailed, terminalError, clientDisconnect, explicitlyComplete bool) bool {
	if explicitlyComplete {
		return true
	}
	return !upstreamFailed && !terminalError && !clientDisconnect
}
```

Delete `CommitCapturePreCommitDisconnect`. In both final result sinks:

1. build `model.Final` with `captureFinalResponseComplete`;
2. do not set `Final.StopReason`;
3. call `CommitCaptureAttempt` once with `captureTerminalOutcome`.

The final shape is:

```go
outcome := captureTerminalOutcome(
	result.UpstreamFailed,
	result.CaptureTerminalError,
	result.ClientDisconnect,
)
final.ResponseComplete = captureFinalResponseComplete(
	result.UpstreamFailed,
	result.CaptureTerminalError,
	result.ClientDisconnect,
	result.CaptureResponseComplete,
)
return CommitCaptureAttempt(c, platform, outcome, final)
```

Use `captureTerminalOutcome` in `attachCaptureToForwardResult`, `RefreshForwardCaptureContentPolicy`, `RefreshOpenAIForwardCaptureContentPolicy`, and the corresponding OpenAI attach block so legacy buffered paths select the same content policy as typed paths.

Broaden the comments on `CaptureResponseComplete` in both result structs: it means an observed final provider terminal boundary for any outcome, including a successful drain after downstream disconnect.

- [ ] **Step 4: Run the terminal matrix and legacy bridge tests**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'CaptureTerminalOutcome|ClientDisconnect|FinalAttempt|Refresh.*CaptureContentPolicy|TypedCapture' -count=1
```

Expected: PASS. Specifically verify the `UpstreamFailed=true, ClientDisconnect=true` attempt aborts under `terminal_error=false`, while a pure disconnect commits under both outcome flags false.

- [ ] **Step 5: Commit the centralized classification**

```powershell
git add backend/internal/service/capture_context.go `
        backend/internal/service/capture_context_test.go `
        backend/internal/service/capture_record.go `
        backend/internal/service/gateway_service.go `
        backend/internal/service/openai_gateway_service.go
git commit -m "fix: classify upstream failure before client disconnect"
```

---

### Task 3: Propagate provider completeness for Anthropic, Bedrock, and KIRO

**Files:**
- Modify: `backend/internal/service/gateway_upstream_response.go:766-771,890-1270`
- Modify: `backend/internal/service/gateway_forward.go:869-969`
- Modify: `backend/internal/service/gateway_anthropic_passthrough.go:289-339,559-790`
- Modify: `backend/internal/service/bedrock_stream.go:46-179`
- Modify: `backend/internal/service/gateway_bedrock.go:132-166`
- Modify: `backend/internal/service/kiro_runtime.go:256-273`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions.go:306-395,487-568`
- Modify: `backend/internal/service/gateway_forward_as_responses.go:474-569,657-740`
- Test: `backend/internal/service/gateway_streaming_test.go`
- Test: `backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`
- Test: `backend/internal/service/bedrock_client_disconnect_test.go`
- Test: `backend/internal/service/gateway_bedrock_capture_test.go`
- Test: `backend/internal/service/kiro_runtime_state_test.go`
- Test: `backend/internal/service/openai_compat_model_test.go`

**Interfaces:**
- Consumes: the provider-specific `sawTerminalEvent` / `terminalObserved` flags already maintained by stream parsers.
- Produces: `streamingResult.responseComplete bool`, copied to `ForwardResult.CaptureResponseComplete`; a client-causal cancel without a terminal remains false, while an observed `message_stop`, Bedrock message-stop event, response terminal event, or equivalent remains true even after the client disconnects.

- [ ] **Step 1: Add failing completeness assertions to existing disconnect tests**

For each active route, use two fixtures:

1. downstream write fails, then provider sends its official terminal event;
2. downstream write fails, then the provider read ends with `context.Canceled`, timeout, or a partial EOF before the official terminal event.

Assert the first result has `ClientDisconnect=true` and `CaptureResponseComplete=true`; assert the second has `ClientDisconnect=true` and `CaptureResponseComplete=false`.

For the shared Anthropic SSE result, add:

```go
require.True(t, result.clientDisconnect)
require.True(t, result.responseComplete)
```

to a stream containing `message_delta` plus `message_stop`, and:

```go
require.True(t, result.clientDisconnect)
require.False(t, result.responseComplete)
```

to the client-canceled partial stream.

In the complete fixture, put one text delta and the official stop event after
the first downstream write failure. Assert the recording capture transport
contains those late provider bytes. This proves capture follows bytes actually
read from upstream and does not stop at the downstream disconnect boundary.

For Bedrock, extend `TestBedrockClientDisconnectReturnsCollectedUsageOnCanceledProviderStream` to require false, and add a terminal fixture whose decoded payload includes `{"type":"message_stop"}` and requires true.

- [ ] **Step 2: Run the provider-focused tests and verify RED**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'ClientDisconnect.*(Terminal|Complete|CanceledProvider)|BedrockClientDisconnect|Kiro.*ClientDisconnect' -count=1
```

Expected: compilation fails because `streamingResult.responseComplete` is absent, or the final result never sets `CaptureResponseComplete` on a drained disconnect.

- [ ] **Step 3: Carry the existing terminal signal through the shared result**

Extend the shared private result:

```go
type streamingResult struct {
	usage              *ClaudeUsage
	firstTokenMs       *int
	clientDisconnect   bool
	semanticOutput     bool
	responseComplete   bool
}
```

When `handleStreamingResponse` constructs the final result, assign
`responseComplete: sawTerminalEvent`. In the forwarding constructors, copy it:

```go
result.CaptureResponseComplete = streamResult.responseComplete
```

Do the same in KIRO and Anthropic passthrough. The passthrough scanner must set a local `terminalObserved` when `anthropicStreamEventIsTerminal(eventName, data)` returns true, and its `streamResult` closure must copy that value.

In Bedrock, set `terminalObserved=true` only after decoding an official `message_stop` event. Return it through `streamingResult.responseComplete`; timeout or client-causal cancellation before that event leaves it false. `gateway_bedrock.go` copies it to the final `ForwardResult`.

The Anthropic-to-OpenAI compatibility loops already maintain `terminalObserved`; copy it into `CaptureResponseComplete` when constructing their `ForwardResult`. Do not infer completeness from downstream `[DONE]` synthesized by the gateway.

- [ ] **Step 4: Run the complete Anthropic/KIRO/Bedrock regression set**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'Streaming|ClientDisconnect|Bedrock|Kiro|AnthropicAPIKeyPassthrough|ForwardAs(ChatCompletions|Responses)' -count=1
```

Expected: PASS. Existing provider-error-after-disconnect tests must still return typed upstream errors; they must not become successful client-disconnect captures.

- [ ] **Step 5: Commit provider completeness for the production platforms**

```powershell
git add backend/internal/service/gateway_upstream_response.go `
        backend/internal/service/gateway_forward.go `
        backend/internal/service/gateway_anthropic_passthrough.go `
        backend/internal/service/bedrock_stream.go `
        backend/internal/service/gateway_bedrock.go `
        backend/internal/service/kiro_runtime.go `
        backend/internal/service/gateway_forward_as_chat_completions.go `
        backend/internal/service/gateway_forward_as_responses.go `
        backend/internal/service/gateway_streaming_test.go `
        backend/internal/service/gateway_anthropic_apikey_passthrough_test.go `
        backend/internal/service/bedrock_client_disconnect_test.go `
        backend/internal/service/gateway_bedrock_capture_test.go `
        backend/internal/service/kiro_runtime_state_test.go `
        backend/internal/service/openai_compat_model_test.go
git commit -m "fix: preserve upstream completion after client disconnect"
```

---

### Task 4: Apply the same completeness contract to OpenAI, Gemini, and Antigravity

**Files:**
- Modify: `backend/internal/service/antigravity_gateway_streaming.go:20-26`
- Modify: `backend/internal/service/antigravity_gateway_claude.go:431-471`
- Modify: `backend/internal/service/antigravity_gateway_compat.go:350-383`
- Modify: `backend/internal/service/antigravity_gateway_gemini.go:425-477`
- Modify: `backend/internal/service/antigravity_gateway_upstream.go:131-185`
- Modify: `backend/internal/service/gemini_chat_completions_compat_service.go:589-928`
- Modify: `backend/internal/service/gemini_messages_compat_service.go:2216-2221,2517-2582,2804-2809,3058-3087`
- Modify: `backend/internal/service/openai_gateway_chat_completions.go:682-699`
- Modify: `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go:210-349`
- Modify: `backend/internal/service/openai_gateway_messages.go:568-716,938`
- Modify: `backend/internal/service/openai_gateway_messages_anthropic_native.go:294-514`
- Modify: `backend/internal/service/openai_gateway_messages_chat_fallback.go:220-275`
- Modify: `backend/internal/service/openai_gateway_responses_anthropic_native.go:210-353`
- Modify: `backend/internal/service/openai_gateway_responses_chat_fallback.go:230-282`
- Test: `backend/internal/service/antigravity_gateway_compat_test.go:668-735`
- Test: `backend/internal/service/antigravity_gateway_service_test.go:1973-2245`
- Test: `backend/internal/service/gemini_messages_compat_service_test.go:1390-1435`
- Test: `backend/internal/service/openai_gateway_chat_completions_test.go:502-548`
- Test: `backend/internal/service/openai_gateway_chat_completions_raw_test.go:693-735`
- Test: `backend/internal/service/openai_gateway_cn_fixes_test.go:93-135`
- Test: `backend/internal/service/openai_gateway_passthrough_flush_test.go:278-330`
- Test: `backend/internal/service/openai_gateway_response_flush_test.go:462-510`
- Test: `backend/internal/service/openai_gateway_service_test.go:2251-2295`

**Interfaces:**
- Consumes: provider-specific official-terminal booleans (`terminalObserved`, `sawTerminalEvent`, Responses terminal type, or upstream `[DONE]` only when `[DONE]` is itself the provider boundary).
- Produces: every production constructor that assigns `ClientDisconnect` also assigns `CaptureResponseComplete` from an upstream terminal signal; local/synthetic downstream finalization never sets it.

- [ ] **Step 1: Add failing assertions to the named cross-platform tests**

Run this read-only inventory and retain it in the task notes:

```powershell
rg -l "ClientDisconnect:" backend/internal/service --glob '*.go' | Sort-Object
```

Extend these existing complete-drain tests with
`require.True(t, result.CaptureResponseComplete)`:

- `TestAntigravityCompatClientDisconnectDrainsUsage`;
- `TestStreamUpstreamResponse_ClientDisconnectDrainsUsage`;
- `TestGeminiStreamingClientDisconnectReturnsCollectedProviderResult`;
- `TestForwardAsChatCompletions_ClientDisconnectDrainsUpstreamUsage`;
- `TestForwardAsRawChatCompletions_ClientDisconnectDrainsUsage`;
- `TestResponsesStreamingFromNativeAnthropic_ClientDisconnectDrainsUsage`;
- `TestOpenAIStreamingPassthroughClientDisconnectStillDrainsTerminalUsage`;
- `TestOpenAIResponseFlush_ClientDisconnectStillDrainsUsage`;
- `TestOpenAIStreamingClientDisconnectDrainsUpstreamUsage`.

The assertion block is identical at the final result boundary:

```go
require.NotNil(t, result)
require.True(t, result.ClientDisconnect)
require.True(t, result.CaptureResponseComplete)
```

Extend these partial/error tests with a false completeness assertion on their
returned private stream result or final capture result:

- `TestAntigravityCompatClientDisconnectDoesNotHideProviderReadError`;
- `TestStreamUpstreamResponse_TimeoutAfterClientDisconnect`;
- `TestStreamUpstreamResponse_ReadErrorAfterClientDisconnectIsTerminalPartial`;
- `TestAntigravityNativeClientDisconnectDoesNotHideProviderReadError`.

Use the private result field when the test stops below the public result boundary:

```go
require.NotNil(t, result)
require.True(t, result.clientDisconnect)
require.False(t, result.terminalObserved)
```

For OpenAI Chat Completions, add one sibling fixture that removes the provider
`[DONE]` line and returns a client-causal `context.Canceled`; assert
`ClientDisconnect=true` and `CaptureResponseComplete=false`. For native
Responses/Anthropic adapters, use the same fixture shape but remove
`response.completed` / `message_stop` respectively.

- [ ] **Step 2: Run the cross-platform tests and verify RED**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'Antigravity.*ClientDisconnect|Gemini.*ClientDisconnect|OpenAI.*ClientDisconnect|ClientDisconnect.*(Drains|Terminal|Partial)' -count=1
```

Expected: at least one complete disconnect result lacks `CaptureResponseComplete`, demonstrating the propagation gap.

- [ ] **Step 3: Propagate only official upstream terminal observations**

For Antigravity and Gemini result constructors, use the existing private member:

```go
CaptureResponseComplete: streamResult.terminalObserved,
```

For OpenAI result constructors whose parser exposes a local boolean, use:

```go
CaptureResponseComplete: sawTerminalEvent,
```

For OpenAI Responses, true terminal types are `response.completed` and `response.done`; `response.failed`, `response.incomplete`, `response.cancelled`, and `response.canceled` remain real provider terminal errors and must set `CaptureTerminalError`/`UpstreamFailed` as they do today. For Chat Completions, accept the provider's observed `[DONE]`, not a gateway-generated downstream sentinel. For Antigravity and Gemini, reuse the existing `terminalObserved` members and never infer completeness from a nil Go error alone.

Where an OpenAI helper returns a private stream result, add a
`terminalObserved bool` member, set it only on provider `response.completed`,
`response.done`, `message_stop`, or provider `[DONE]`, and copy that member into
the public `OpenAIForwardResult`. Do not alter provider parsing, output staging,
usage calculation, or write-error behavior; only carry the already-computed
terminal fact into the final capture result.

- [ ] **Step 4: Verify every client-disconnect constructor carries a completeness source**

Run:

```powershell
rg -n -C 8 "ClientDisconnect:" backend/internal/service --glob '*.go'
```

Inspect every non-test constructor. Each must either set `CaptureResponseComplete` in the same literal or assign it immediately afterward from a provider-terminal boolean. A constructor representing a known partial/canceled provider stream may intentionally leave the zero value false; its adjacent comment and test must state that reason.

- [ ] **Step 5: Run the cross-platform regression set and commit**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service -run 'Antigravity|Gemini|OpenAI|ClientDisconnect|CaptureResponseComplete' -count=1
```

Expected: PASS, including existing tests that ensure provider read errors and official error events remain visible after downstream disconnect.

Stage the exact production files and test files listed for this task, inspect
`git diff --cached --name-only`, then commit:

```powershell
git commit -m "fix: propagate provider terminal capture state"
```

---

### Task 5: Make raw upstream response extraction authoritative for stop reason

**Files:**
- Modify: `backend/internal/capture/extract/extract.go:180-203,1131-1134`
- Modify: `backend/internal/capture/model/model.go:49-57`
- Modify: `backend/internal/service/conversation_capture_pool.go:288-296`
- Modify: `backend/internal/service/conversation_capture_unit_support.go:151-192`
- Test: `backend/internal/capture/extract/extract_test.go:36-72,96-107`
- Test: `backend/internal/capture/sidecar/protocol_extraction_integration_test.go:21-134`
- Test: `backend/internal/capture/spool/attempt_test.go:162-189`
- Test: `backend/internal/service/capture_context_test.go:881-956`
- Test: `backend/internal/service/conversation_capture_unit_support_test.go`

**Interfaces:**
- Consumes: response bytes fed to `metadataStream` for JSON, SSE, and AWS EventStream formats.
- Produces: `model.Extracted.StopReason` only from response parsing; `model.Final.StopReason` remains wire-compatible but is documented as legacy and ignored for extraction; unknown provider values are preserved without normalization.

- [ ] **Step 1: Reverse the existing override tests**

Rename `TestExtractJSONMetadataAndAuthoritativeFinal` to
`TestExtractJSONMetadataKeepsProviderStopReasonAndAuthoritativeFinalUsage`. Keep final token counters authoritative, but expect `payload-stop` rather than `final-stop`.

Add exact-value coverage:

```go
func TestExtractJSONPreservesUnknownProviderStopReasonExactly(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadJSON,
		Response: strings.NewReader(`{"stop_reason":"future_provider_reason"}`),
		Final:    model.Final{StopReason: "gateway_custom_value"},
	})
	require.NoError(t, err)
	require.Equal(t, "future_provider_reason", got.StopReason)
}
```

Add a provider-terminal table with `refusal`, `content_filtered`, and
`guardrail_intervened` as HTTP-200 response values. For each value, feed it in
the supported response field and require the same value in
`model.Extracted.StopReason`; these are successful provider terminal states,
not service-unavailable errors.

In `protocol_extraction_integration_test.go`, rename the test to
`TestProtocolSpoolExtractionAlwaysRetainsProviderStopReason`. Keep the JSON, SSE, and AWS cases that send `Final.StopReason="pre_commit_disconnect"`, but change every expected stop reason to the value in the response bytes.

In the malformed-response spool test, keep the final usage assertion but require an empty extracted stop reason; malformed wire bytes cannot be repaired by a gateway string.

- [ ] **Step 2: Run extractor, spool, and protocol tests and verify RED**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/capture/extract ./internal/capture/spool ./internal/capture/sidecar `
    -run 'StopReason|MalformedExtraction|ProtocolSpoolExtraction' -count=1
```

Expected: current `Final.StopReason` overrides the provider response and fails the new assertions.

- [ ] **Step 3: Remove the Final override and gateway injection**

In `metadataStream.finalize`, retain final token authority but delete the stop-reason override:

```go
if finalPresent {
	extracted.InputTokens = final.InputTokens
	extracted.OutputTokens = final.OutputTokens
	extracted.CacheReadTokens = final.CacheReadTokens
	extracted.CacheCreationTokens = final.CacheCreationTokens
}
```

In `responseState.setStop`, preserve the provider string without mapping or trimming:

```go
func (s *responseState) setStop(value string, rank int) {
	if !s.stopPresent || rank >= s.stopRank {
		s.value.StopReason, s.stopPresent, s.stopRank = value, true, rank
	}
}
```

Keep `Final.StopReason` for protocol compatibility in this release, but add a comment that extraction intentionally ignores it and that new producers must not set it. Remove `StopReason: record.StopReason` from `ConversationCapturePool.Submit` and remove both Final-based stop-reason assignments from the unit-only record reconstruction helper. That helper should call `extractCaptureColumns(record)` and retain only its raw-response-derived value.

Use this repository assertion:

```powershell
rg -n "Final\{[^}]*StopReason|StopReason:\s+record\.StopReason|final\.StopReason" `
  backend/internal/service backend/internal/capture --glob '*.go'
```

After production-code edits, matches may remain only in compatibility tests deliberately proving that Final is ignored, plus the model field itself.

- [ ] **Step 4: Run the source-authority regression tests**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/capture/... ./internal/service `
    -run 'StopReason|ProtocolSpoolExtraction|MalformedExtraction|ClientDisconnect|ConversationCapture' -count=1
```

Expected: PASS. Verify JSON, SSE, and AWS official values survive, unknown official values are unchanged, absent/malformed official values stay empty, and custom Final values never reach `Extracted.StopReason`.

- [ ] **Step 5: Commit stop-reason source isolation**

```powershell
git add backend/internal/capture/extract/extract.go `
        backend/internal/capture/extract/extract_test.go `
        backend/internal/capture/model/model.go `
        backend/internal/capture/sidecar/protocol_extraction_integration_test.go `
        backend/internal/capture/spool/attempt_test.go `
        backend/internal/service/conversation_capture_pool.go `
        backend/internal/service/conversation_capture_unit_support.go `
        backend/internal/service/conversation_capture_unit_support_test.go `
        backend/internal/service/capture_context_test.go
git commit -m "fix: derive archived stop reason from upstream bytes"
```

---

### Task 6: Verify end-to-end commit, filtering, and spool cleanup invariants

**Files:**
- Verify: all files changed in Tasks 1-5
- Verify unchanged lifecycle coverage: `backend/internal/capture/sidecar/runtime_test.go`
- Verify unchanged ACK/recovery coverage: `backend/internal/capture/spool/batch_test.go`

**Interfaces:**
- Consumes: private client-disconnect outcome, unified terminal precedence, provider completeness propagation, upstream-only stop-reason extraction, existing ACK/cleanup runtime.
- Produces: evidence that client disconnect reaches ready/upload cleanup, upstream failures remain filtered, uploader failure retains ready data, and no schema/protocol/config surface changed.

- [ ] **Step 1: Run the focused end-to-end matrix**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/service ./internal/capture/extract ./internal/capture/spool ./internal/capture/protocol ./internal/capture/sidecar `
    -run 'ClientDisconnect|UpstreamFailure|TerminalError|StopReason|ProtocolSpoolExtraction|SidecarRestartDrains|OutageRetains|Ack|Cleanup' `
    -count=1
```

Expected: PASS. Existing sidecar tests must still show successful upload removes ready data, retryable upload preserves it, and ACK recovery cleans without reupload.

- [ ] **Step 2: Run race-enabled focused tests**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test -race ./internal/capture/... ./internal/service `
    -run 'ClientDisconnect|CaptureTerminalOutcome|ProtocolSpoolExtraction|SidecarRestartDrains' `
    -count=1
```

Expected: PASS with no race reports. This protects request-slot terminal ownership and concurrent response tee behavior.

- [ ] **Step 3: Run complete package regressions**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test ./internal/capture/... ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the complete backend unit suite**

```powershell
docker run --rm -v "${PWD}/backend:/src" -w /src golang:1.26.6-bookworm `
  go test -timeout=20m -tags=unit ./... -count=1
```

Expected: PASS. If an integration-only dependency is unavailable, record the exact package and error; do not weaken or skip a feature assertion.

- [ ] **Step 5: Verify repository and schema boundaries**

Run:

```powershell
git diff --check
git status --short
git diff --name-only HEAD~5..HEAD
rg -n 'pre_commit_disconnect' backend/internal --glob '*.go'
rg -n 'client_disconnected|client_disconnect' backend/internal/capture backend/internal/service --glob '*.go'
```

Expected:

- `pre_commit_disconnect` appears only in operational loss/status code and tests for that health reason;
- `client_disconnect` appears only in the private service classification/tests, never capture model, manifest, RowBinary encoder, ClickHouse migration, or API DTO;
- no migration, generated schema, configuration, or frontend file changed;
- the user's unrelated untracked plan remains untouched.

- [ ] **Step 6: Record the verified revision**

Run `git rev-parse HEAD` and include the exact SHA plus every passing command in
the implementation handoff. Verification is read-only; do not create an empty
commit.

## Production release dependency

Do not deploy this feature immediately after local completion. The already reproduced sidecar uploader defect can silently stop the upload worker after a retryable ClickHouse timeout and leave ready files accumulating. Prepare and execute its separate TDD fix first, rerun the sidecar restart/idle-probe/upload tests, then request explicit user authorization for any production deployment, restart, traffic exercise, or spool observation.
