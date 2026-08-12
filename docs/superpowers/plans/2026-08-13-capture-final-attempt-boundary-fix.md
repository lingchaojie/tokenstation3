# Capture Final-Attempt Boundary Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make runtime-disabled capture perform no capture work and make native KIRO plus Anthropic API-key passthrough archive exactly one final provider-native request/response pair.

**Architecture:** Reuse the existing immutable request-scope policy and capture bridge. Apply `CaptureMayApplyFor` before every request snapshot or response tee, propagate the gin capture context into native KIRO calls, and update the shared bridge only at real provider `DoWithTLS` boundaries so retries naturally replace intermediate attempts.

**Tech Stack:** Go 1.26, Gin, `net/http`, AWS event-stream KIRO adapters, Testify, existing capture runtime policy and conversation capture pool.

## Global Constraints

- Do not change account selection, model mapping, failover eligibility, retry counts, retry delays, billing, scheduling, cooldowns, or side-effect ordering.
- Do not change client-visible bodies, status codes, SSE framing, request IDs, runtime defaults, policy filters, or the normal handler's 512 KiB error-read ceiling.
- A policy miss must allocate no capture tee, copy no body for capture, mutate no capture bridge, and submit no capture record.
- A policy hit must retain only the final real provider attempt; request and response must use provider-native bytes from the same attempt.
- Capture remains bounded, truncation-aware, drop-safe, and unable to alter forwarding.
- Do not push until a new independent full-range reviewer returns finding-free `SAFE TO PUSH`.

---

### Task 1: Runtime policy allocation guard

**Files:**
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_websearch.go`
- Modify: `backend/internal/service/gateway_anthropic_passthrough.go`
- Test: `backend/internal/service/capture_context_test.go`
- Test: `backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`
- Test: `backend/internal/service/account_test_service_kiro_test.go`

**Interfaces:**
- Consumes: `CaptureMayApplyFor(c *gin.Context, platform string) bool`.
- Produces: a single pre-result capture allocation decision used by KIRO and Anthropic passthrough response paths.

- [ ] **Step 1: Write failing policy-miss tests**

Add table-driven tests for master/platform/user/group misses. Use a static capture-enabled config plus a compiled runtime scope that does not match. For affected stream/nonstream paths, assert the response body is read only by forwarding, `takeCaptureResult(c)` is empty, and the capture pool receives no record. Include a counted `ReadCloser`/wrapper assertion so a bridge-only final assertion cannot hide response tee allocation.

- [ ] **Step 2: Run the policy-miss tests and verify RED**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service \
  -run '^(TestCapturePolicyMissAvoidsKiroResponseTee|TestCapturePolicyMissAvoidsAnthropicPassthroughResponseTee)$' -v
```

Expected: FAIL because static `Gateway.Capture.Enabled` still installs a KIRO raw wrapper or Anthropic SSE/body copy despite `CaptureMayApplyFor == false`.

- [ ] **Step 3: Apply the minimal allocation guard**

For each affected path compute:

```go
captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
    account != nil && CaptureMayApplyFor(c, string(account.Platform))
```

Use that value for `beginCaptureResponse`, `newSSETee`, and nonstream `captureWithLimit`. Do not change the final `CaptureDecisionFor` outcome decision.

- [ ] **Step 4: Run focused GREEN and existing disabled-policy tests**

Run the RED command plus:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service \
  -run '^(TestCaptureDecision|TestPrepareCaptureScope|TestForwardKiro|TestGatewayService_AnthropicAPIKeyPassthrough_)'
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add -- backend/internal/service/kiro_runtime.go \
  backend/internal/service/kiro_websearch.go \
  backend/internal/service/gateway_anthropic_passthrough.go \
  backend/internal/service/capture_context_test.go \
  backend/internal/service/gateway_anthropic_apikey_passthrough_test.go \
  backend/internal/service/account_test_service_kiro_test.go
git commit -m "fix(capture): honor runtime allocation policy"
```

### Task 2: Native KIRO final provider pair

**Files:**
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_websearch.go`
- Modify: `backend/internal/service/kiro_capture.go`
- Test: `backend/internal/service/account_test_service_kiro_test.go`
- Test: `backend/internal/service/web_chat_final_request_test.go`

**Interfaces:**
- Consumes: `withCaptureUpstreamRequestContext`, `setCaptureUpstreamRequestFromContext`, `setCaptureUpstreamResponseFromContext`, `beginCaptureResponse`, and `finalizeKiroCapture`.
- Produces: native KIRO results whose `UpstreamRequest`, `CaptureResponse`, endpoint, headers, status, truncation, and content policy describe the final AWS runtime attempt.

- [ ] **Step 1: Write failing native KIRO pair tests**

Exercise native `/v1/messages` stream, nonstream, and nonstream only-WebSearch with a recorder/queued upstream. Capture the actual final request body received by the transport and preserve the raw AWS event-stream response bytes. Assert:

```go
require.Equal(t, recorder.FinalRequestBody(), result.UpstreamRequest)
require.Equal(t, rawFinalAWSEventStream, result.CaptureResponse)
require.NotEqual(t, clientTranslatedBody, result.CaptureResponse)
require.NotNil(t, result.CaptureContentPolicy)
```

For retry/WebSearch iterations assert intermediate payloads/bodies are absent and one final record/pair survives.

- [ ] **Step 2: Run KIRO pair tests and verify RED**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service \
  -run '^TestNativeKiroCaptureFinalProviderPair' -v
```

Expected: stream request differs from the AWS envelope; nonstream response contains translated JSON; only-WebSearch lacks a finalized raw response.

- [ ] **Step 3: Propagate capture context and tee raw responses**

In `forwardKiroMessages`, only when `CaptureMayApplyFor` passes:

```go
ctx = withCaptureUpstreamRequestContext(ctx, c)
setCapturePlatform(c, string(account.Platform))
```

For normal nonstream and only-WebSearch nonstream, install `beginCaptureResponse` on the real AWS response before parsing and invoke its finisher after the parser consumes/closes the body. Assemble the final result through the shared capture bridge/finalizer. Each later runtime `DoWithTLS` already calls the context setters and must overwrite prior MCP/retry state.

- [ ] **Step 4: Run KIRO GREEN and regression matrix**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service \
  -run '^(TestNativeKiroCaptureFinalProviderPair|TestForwardKiro|TestWebChatKiro|TestKiroWebSearch|TestGatewayService_ForwardKiro)'
```

Expected: PASS with exact provider-native byte equality, final-attempt ownership, correct credits/usage, and unchanged client output.

- [ ] **Step 5: Commit Task 2**

```bash
git add -- backend/internal/service/kiro_runtime.go \
  backend/internal/service/kiro_websearch.go \
  backend/internal/service/kiro_capture.go \
  backend/internal/service/account_test_service_kiro_test.go \
  backend/internal/service/web_chat_final_request_test.go
git commit -m "fix(capture): preserve native KIRO final exchange"
```

### Task 3: Anthropic API-key final wire request

**Files:**
- Modify: `backend/internal/service/gateway_anthropic_passthrough.go`
- Test: `backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`
- Test: `backend/internal/handler/gateway_anthropic_apikey_stream_integration_test.go`

**Interfaces:**
- Consumes: `(*GatewayService).captureOutboundRequest(c, account, req, wireBody)` and existing result attachment/handler sink.
- Produces: successful passthrough results with explicit Anthropic platform policy and the final real request body, endpoint, and redacted headers.

- [ ] **Step 1: Write failing custom-endpoint/retry test**

Use an Anthropic API-key account whose compatible base URL is `https://relay.example`. Make the first attempt retryable and the second successful with a different sanitized/beta-adjusted wire body or request marker. Assert final result and drained record contain only the second request, `relay.example` endpoint, redacted headers, and non-nil Anthropic content policy.

- [ ] **Step 2: Run passthrough test and verify RED**

Run:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service ./internal/handler \
  -run '^TestAnthropicAPIKeyPassthroughCaptureUsesFinalCustomEndpointAttempt$' -v
```

Expected: result has no content policy or final wire request and the successful record is skipped.

- [ ] **Step 3: Snapshot at every real send boundary**

Immediately before `DoWithTLS` add:

```go
s.captureOutboundRequest(c, account, upstreamReq, wireBody)
```

Keep it inside the retry loop so the final attempt replaces the earlier request and clears earlier response metadata. Do not add hostname-specific inference or a second submission path.

- [ ] **Step 4: Run passthrough GREEN and related regressions**

Run the RED command plus:

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service ./internal/handler \
  -run '(AnthropicAPIKeyPassthrough|AnthropicAPIKey.*Capture|CommittedPartial|FinalRequest|CaptureDisabled)'
```

Expected: PASS, with exact-once handler capture and unchanged retry/usage/client semantics.

- [ ] **Step 5: Commit Task 3**

```bash
git add -- backend/internal/service/gateway_anthropic_passthrough.go \
  backend/internal/service/gateway_anthropic_apikey_passthrough_test.go \
  backend/internal/handler/gateway_anthropic_apikey_stream_integration_test.go
git commit -m "fix(capture): record final Anthropic passthrough request"
```

### Task 4: Aggregate verification, archive, and release review

**Files:**
- Modify: `docs/upstream-sync/2026-08-12-sub2api-0.1.173-0b3f.md`
- Modify: `/home/alvin/tokenstation3/.superpowers/sdd/2026-08-11-sub2api-upstream-sync/progress.md` (external archive, not staged)

**Interfaces:**
- Consumes: Tasks 1-3 fixed commits.
- Produces: a clean exact HEAD eligible for a fresh full-range independent review.

- [ ] **Step 1: Run affected aggregate tests**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./internal/service ./internal/handler \
  -run '(Capture|Kiro|WebChat|AnthropicAPIKeyPassthrough|CommittedPartial|FirstOutput|RawCC|Grok|Gemini)'
```

- [ ] **Step 2: Run complete verification required by changed Go production code**

```bash
GOMAXPROCS=2 go -C backend test -tags=unit -count=1 -p 1 ./...
GOMAXPROCS=2 go -C backend test -tags=integration -count=1 -p 1 ./...
make -C backend build
make -C backend check-generate
golangci-lint run ./backend/...
```

Use the repository's established lint invocation if the last command's working-directory form differs. Every command must exit 0 on the exact final tree.

- [ ] **Step 3: Update the archive and commit**

Record the three-review-finding RED/GREEN evidence, exact test commands, commit topology, and unchanged rollback ref in the tracked upstream archive and external SDD ledger. Stage only the tracked archive and commit it.

- [ ] **Step 4: Audit repository state**

Verify clean status, no unmerged paths or conflict markers, `git diff --check`, generated-file consistency, migration order, executable modes, and no user-owned untracked overlap.

- [ ] **Step 5: Request a new independent full-range review**

Review `3e3b0e7536647bb0007eb3d34a5447d905629853..HEAD`, both merge parents, all three fixes, and the complete product-decision matrix. Any Critical/Important/Minor finding blocks publication and restarts focused RED/GREEN plus fresh review.

- [ ] **Step 6: Publish only after `SAFE TO PUSH`**

Fetch origin/upstream again, require unchanged `origin/dev` or stop, fast-forward local `dev`, non-force push the exact HEAD to both `dev` and `main`, and wait for every required workflow associated with that exact SHA. Record final SHA and CI URLs externally; do not use superseded runs.
