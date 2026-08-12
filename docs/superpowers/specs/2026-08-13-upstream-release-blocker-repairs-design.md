# Upstream Release-Blocker Repairs Design

## Context

The upstream merge through `0b3fe95afd20aba77ee7649b37febb8255fb57a5`
and the later local-dev drift merge are mechanically complete and pass the
existing test suites. A fresh whole-range review nevertheless found production
paths where the merged control flow does not satisfy the already-approved
audit, settings, and provider-capture contracts.

This work repairs those concrete release blockers. It does not redesign the
capture product, expand capture policy to new product concepts, or change the
user-approved upstream feature decisions.

## Scope

### Audit integrity

- Establish an ordering barrier between the asynchronous audit writer and an
  administrative clear.
- Execute the clear count, table clear, and mandatory clear trace in one
  repository transaction. A trace failure must roll back the clear.
- Prevent records accepted before the clear boundary from reappearing after
  the clear trace.
- Truncate audit JSON on a valid UTF-8 boundary while preserving the existing
  byte ceiling.
- Prevent one malformed row from silently discarding unrelated records in the
  same asynchronous batch.

### Hidden rollout settings

- Preserve the stored `passkey_enabled` value across unrelated consolidated
  admin settings updates while the field remains absent from public/admin DTOs.
- Do not mount Passkey routes or expose a new setting surface.

### Capture foundation

- Enforce `MaxBodyBytes` while reading request snapshots, using at most
  `limit+1` bytes to distinguish exact fit from truncation. Re-cap bridge data
  at every terminal-record assembly boundary.
- Separate non-creating capture-slot lookup from policy-approved creation.
  Runtime master/platform/user/group misses must not allocate or mutate a slot.
- Add an attempt generation identity. Response publication from an older
  account or retry must not modify the current attempt.
- A translated KIRO stream must join its translator during close so no capture
  publication can occur after the forwarding attempt returns.
- Capture continues to represent exactly one final provider-native exchange:
  the final wire request, provider response bytes/status/headers, and the
  policy/request metadata attached to that same attempt.

### Handler and provider-path wiring

- Prepare and consume capture scope in the real `/v1/chat/completions` and
  `/v1/responses` compatibility handlers, with per-account reset and exact-once
  success, committed-partial, and terminal ownership.
- Make OpenAI WebChat capture eligibility depend on the outbound OpenAI
  protocol/attempt rather than the inbound WebChat URL; provide the matching
  OpenAI WebChat terminal owner.
- Complete provider-native capture for Gemini Messages and native Gemini,
  including API-key, OAuth/Code Assist, and service-account/Vertex paths.
- Complete capture for Antigravity's advertised supported protocols and account
  types.
- Treat Bedrock as a real Anthropic-policy provider path by capturing its final
  signed/provider request and raw AWS response before transformation.
- For KIRO only-WebSearch, consume and close every terminal HTTP response,
  preserve the final AWS request/body/status/headers after semantic output,
  and never substitute translated SSE.
- Route configured retry-exhausted HTTP statuses through a metadata-rich final
  owner without changing retry or client-response semantics.
- Make all direct terminal sinks prefer the bounded final-attempt bridge over
  parsed/inbound compatibility bodies.

## Explicit Non-Goals

- No redesign of the capture policy, persistence product, operator UI, or
  ClickHouse schema.
- No new provider platform or capture category.
- No change to billing, scheduling, failover eligibility, client-visible
  protocol, or account selection semantics except where resource cleanup and
  exact final-attempt ownership require it.
- No work on the pre-existing refresh-token GET/DEL race.
- No work on the pre-existing pending-refund loss of
  `deduct_balance=false`; both are recorded separately and do not block this
  upstream-sync repair scope.
- No production access, provider request, force push, or destructive branch
  rewrite.

## Architecture

### Audit clear boundary

`AuditLogService` owns the queue lifecycle and therefore owns the clear
barrier. A clear first establishes a boundary that prevents new work from
crossing the drain, waits for accepted pre-boundary writes to finish, invokes
one repository transaction for count/clear/trace, then releases writers. The
repository transaction is the durability boundary; service queue coordination
is the ordering boundary. Failure at either boundary leaves the previous audit
history intact or returns an explicit error.

Batch writing keeps the fast COPY path. If a batch fails, it is divided or
retried in a bounded way so a single bad record cannot discard unrelated valid
records. UTF-8-safe truncation should make malformed text exceptional rather
than routine.

### Capture attempt object

The existing context bridge remains the transport, but creation becomes
policy-gated and each attempt carries a monotonically increasing generation.
All request and response setters receive or derive that generation; stale
publishers are ignored. Reading/taking a result never creates a bridge.

Request and response snapshots share one bounded-copy primitive returning
`bytes` and `truncated`. No later result/terminal assembler may replace bounded
data with an uncapped slice.

Provider services instrument the closest common boundary around a real
outbound attempt. Handlers own final submission because they know whether a
later account will be selected; WebChat owns submission only for calls that
bypass ordinary handlers. Existing ownership tokens remain the exact-once
guard and are extended only where an entry currently has no owner.

### Protocol-specific integration

Common handlers establish scope and requested-model metadata before account
selection, reset the generation for every attempt, and consume the final result
after the selection loop. Provider services publish the actual final wire
request and raw response before protocol conversion. Direct terminal helpers
consume the same bridge rather than reconstructing a request.

OpenAI WebChat supplies explicit outbound protocol eligibility. Gemini,
Antigravity, Bedrock, and KIRO use their real send/retry boundaries. Their
existing conversions, billing values, and client output remain unchanged.

## Error and Concurrency Semantics

- Pre-semantic failure may remain failoverable only when no downstream byte was
  delivered. Capture from abandoned attempts is reset and cannot publish late.
- Post-semantic errors remain committed partial results; capture records the
  final provider attempt and billing remains exact once.
- A terminal provider HTTP response is read with the configured capture limit,
  closed exactly once, and represented with the real status and headers.
- Transport failures without an HTTP response are not fabricated as provider
  responses.
- Policy misses perform no response tee, request snapshot, slot allocation,
  slot reset, or capture-pool submission.
- Audit clear cannot interleave with queued or in-flight asynchronous inserts.

## Verification Strategy

Every repair starts with a production-path failing test that demonstrates the
reviewed behavior. Tests must use real router/handler/service boundaries and
HTTP recorders or real repository transactions wherever practical; source-text
contracts alone do not satisfy a runtime requirement.

Required focused matrices:

- Audit clear: queued and in-flight writer interleavings, trace insertion
  failure rollback, concurrent enqueue, UTF-8 boundary, and bad-row batch
  isolation.
- Settings: seed hidden Passkey `true`, perform unrelated partial/full admin
  update, verify preservation and continued DTO absence.
- Capture bounds: exact limit, limit+1, and large request allocation behavior;
  terminal bridge cannot re-expand data.
- Attempt ownership: delayed KIRO translator plus immediate account switch;
  stale generation cannot publish.
- Real handler routes: Chat Completions and Responses across supported
  Anthropic/KIRO/Gemini/Antigravity outcomes.
- OpenAI WebChat: Responses/nonstream/stream/title/terminal error and policy
  miss.
- Gemini: Messages/native, API-key/OAuth/Vertex, stream/nonstream,
  success/failover/custom terminal.
- Antigravity and Bedrock: native/compat formats, stream/nonstream,
  success/partial/terminal.
- KIRO only-WebSearch: ordinary native and WebChat, failoverable and custom
  status, bounded body, close, provider-native equality, exact once.
- Custom retry exhaustion and direct terminal request correctness for generic,
  passthrough, KIRO, and Bedrock paths.

After focused review gates, run the exact final tree through backend unit and
integration suites, build, generated-code consistency, lint, frontend frozen
install/lint/typecheck/full tests/build, deployment/security checks, and a new
full-range independent review. Publication remains blocked unless that reviewer
returns an explicit finding-free `SAFE TO PUSH`.

## Delivery and Rollback

Repairs are split into small commits by audit/settings, capture foundation, and
provider/handler domains. The upstream pin and both merge parents remain
unchanged. After all gates pass, `dev` and `main` are updated only by
fast-forward/non-force push and CI is monitored for the exact published SHA.
The existing backup ref remains the rollback anchor.
