# Capture Final-Attempt Boundary Fix Design

## Context

The upstream-sync release candidate at `369e5f1c8bd282ff326c9a12312caf6977249025`
passed the full local verification matrix, but the final independent review found
three capture-boundary defects:

1. Several KIRO and Anthropic API-key response paths allocate and copy response
   data when static capture provisioning is on but the runtime policy does not
   match the request.
2. Native KIRO `/v1/messages` does not consistently preserve the final
   provider-native AWS request/response pair for streaming, non-streaming, and
   only-WebSearch requests.
3. Anthropic API-key passthrough does not snapshot the final wire request or
   explicitly carry the Anthropic platform at the real `DoWithTLS` boundary,
   so custom compatible endpoints can lose successful capture records.

The release remains blocked until all three findings are repaired and a fresh
independent reviewer returns `SAFE TO PUSH`.

## Scope and invariants

This fix changes capture ownership only. It must not change:

- account selection, model mapping, failover eligibility, retry counts, or
  retry delays;
- token usage, billing, scheduling results, cooldowns, or side-effect ordering;
- client-visible response bodies, status codes, SSE framing, or request IDs;
- runtime capture defaults, filters, content policy, or static provisioning;
- the ordinary handler's 512 KiB upstream error safety limit.

When capture does not apply, the affected paths must allocate no capture tee,
copy no request or response body for capture, create no bridge, and submit no
record. When capture applies, exactly one record must describe the final real
provider attempt.

## Design

### 1. One allocation guard

Before the upstream result is known, request snapshots and response tees use
the same guard:

```go
staticCaptureEnabled && account != nil &&
    CaptureMayApplyFor(c, string(account.Platform))
```

`CaptureMayApplyFor` remains a pure, immutable request-scope policy decision.
It covers success and terminal-error outcomes, including user/group filters.
No path may allocate merely because `Gateway.Capture.Enabled` is true.

### 2. Native KIRO final-attempt ownership

At `forwardKiroMessages`, when the allocation guard passes, wrap the request
context with `withCaptureUpstreamRequestContext(ctx, c)`. Every real KIRO
runtime or MCP `DoWithTLS` already replaces the bridge request/response metadata
at the send boundary; the context makes those calls effective for the native
route as well as the compatibility adapters.

For successful provider-native response bodies:

- streaming runtime: tee the raw AWS event stream before Anthropic conversion;
- non-streaming runtime: tee the raw AWS event stream before parsing it into
  Anthropic JSON;
- only-WebSearch non-streaming: tee each runtime response before parsing, and
  let every later real runtime attempt replace the bridge so only the final
  request/response pair survives;
- MCP discovery/search calls may update the bridge while they are the latest
  real call, but the final runtime call must replace them before the result is
  finalized.

Result assembly must use the shared bridge/finalizer. It must never substitute
translated JSON or SSE for a provider-native response when a raw response was
captured. Pre-output failover clears the attempt bridge as before.

### 3. Anthropic API-key passthrough send boundary

Immediately before every real passthrough `DoWithTLS`, call:

```go
s.captureOutboundRequest(c, account, upstreamReq, wireBody)
```

This records the explicit Anthropic platform, final sanitized/beta-adjusted
wire body, final endpoint, and redacted request headers. A retry overwrites the
previous attempt. Streaming and non-streaming response copying use the same
allocation guard; result attachment derives the content policy from the
explicit platform rather than hostname inference.

## Error handling and resource ownership

- Capture remains drop-safe; capture failures never alter forwarding.
- Response wrappers retain the original `Close` behavior and existing body
  ownership rules.
- Truncation continues to use the configured capture ceiling and is propagated
  as the OR of request and response truncation.
- Intermediate retries are not submitted. The next send resets response-side
  state before recording the new response.
- A runtime-policy miss performs no capture-specific read, allocation, lock,
  bridge mutation, or pool submission.

## Test design

Tests must demonstrate the original failure before production changes and then
pass after the minimal repair.

### Runtime-off zero side effects

Cover runtime master off, platform off, user mismatch, and group mismatch for:

- KIRO streaming runtime;
- KIRO WebSearch streaming;
- Anthropic API-key streaming;
- Anthropic API-key non-streaming.

Use observable body-wrapper/read counters and bridge/pool assertions, not source
text checks alone.

### KIRO provider-native pairing

Use a real HTTP recorder/queued upstream for native `/v1/messages` streaming,
non-streaming, and only-WebSearch. Assert byte-for-byte:

- archived request equals the final AWS envelope received by the recorder;
- archived response equals the raw AWS event stream;
- translated client JSON/SSE differs where expected and is not archived;
- retries and WebSearch iterations submit exactly one final pair;
- policy off submits nothing and does not wrap the body.

### Anthropic passthrough final wire request

Use an Anthropic account with a custom compatible base URL and a retry. Assert:

- the content policy is present and comes from the Anthropic runtime policy;
- endpoint, sanitized request body, and redacted headers match the final real
  attempt;
- the first attempt is absent from the result;
- exactly one record is submitted;
- policy off performs no capture work.

## Verification and release gate

After focused RED/GREEN:

1. run the affected service/handler capture, KIRO, WebSearch, passthrough, retry,
   partial, and capture-disabled tests;
2. run the complete backend unit suite on the final tree;
3. rerun lint, build, generation checks, and any affected integration tests;
4. update the upstream-sync archive with the review/fix evidence;
5. request a new independent full-range review at the new exact HEAD;
6. only a finding-free `SAFE TO PUSH` permits non-force fast-forward of `dev`
   and simultaneous publication of `dev` and `main`, followed by exact-SHA CI.

## Rollback

Until publication, abandon the isolated branch. After publication, revert the
post-review fix commit first, then the dev-drift merge and upstream merge only
if necessary; do not rewrite shared history. Preserve
`backup/upstream-sync-before-dev-drift-20260812-a865` until stability is
confirmed.
