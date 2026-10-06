# TypeSafe integration — in progress

## Platform plumbing checkpoint

TypeSafe is an independent API-key platform. The platform/quota schema and
scheduler set retain KIRO; the scheduler set grows to12. Existing KIRO mixed
snapshot invalidation remains, and TypeSafe uses its own platform buckets.
Account base URL resolution preserves KIRO direct-mode empty URL and gives
TypeSafe its native host. Channel pricing remains platform-isolated. Group
binding accepts existing providers plus TypeSafe and rejects Composite.
The native route inherits authentication/group checks and the text body limit;
the existing Messages dispatcher is preserved. No automatic account creation,
internal moderation setting rewrite, or channel-monitor expansion is included.

Bedrock mapping tests preserve local Mythos/Fable cases and add the upstream
Sonnet5.5 global mapping. Settings constants retain local expiring-reward and
check-in keys alongside the approved log-only user allowlist key (Task6).

## Tests

- `go test -count=1 ./internal/domain ./internal/pkg/typesafe`: exit0;
  domain0.003s, typesafe0.123s. Tool output only, intermediate tree.
- Earlier combined `go test -count=1 ./internal/domain ./internal/pkg/typesafe
  ./internal/securityaudit`: exit1; domain/typesafe passed but securityaudit
  could not compile service due to merge markers in api_key_service.go and
  settings_view.go. This is not behavioral RED and not a passing audit suite.
- Gofmt succeeds on the resolved platform/schema/admin/route files. No Ent/Wire
  regeneration yet. Service/handler contract tests and full coverage pending.

## Approved capture decision and candidate implementation

User selected option1: add a separate TypeSafe capture toggle, defaultfalse,
subject to existing scope/outcome/content policies. CapturePlatformPolicy now
recognizes TypeSafe only when explicitly enabled. Frontend defaults and legacy
policy loading keep it off, independently of other providers.

Candidate native forwarding starts an attempt, captures the actual outbound
request and upstream response, preserves failover metadata, and finalizes only
the final attempt. Terminal HTTP/decode errors can return capture-only results;
they are not submitted for billing. Handler cleanup aborts unfinished attempts.
Successful responses undergo the existing synchronous usage/pricing preflight
before queued billing and final capture; preflight failure marks the capture
outcome without appending another response or penalizing the provider account.
Service/handler behavior remains UNVERIFIED until shared conflicts compile.

Focused evidence in `.superpowers/sdd/2026-10-05-sub2api-b8de-integration/`:

- `go test -count=1 internal/service/capture_runtime_policy.go
  internal/service/capture_typesafe_policy_test.go`: observed RED on unknown
  JSON field `typesafe`, then GREEN (0.003s). Logs:
  `typesafe-capture-policy-red.log`, `typesafe-capture-policy-green.log`.
  File-based test exercises the real policy implementation, not full service.
- Frontend capture-settings view regression: RED on missing independent toggle,
  then `CaptureSettingsView.spec.ts`, `admin.captureSettings.spec.ts` and
  `captureHealth.spec.ts` GREEN, 11 tests in 3 files (2.49s). Logs:
  `typesafe-capture-ui-red.log`, `typesafe-capture-ui-green.log`.
  Existing outdated Browserslist warning remains; no full typecheck claim.
- Native service capture tests: compilation blocked by unresolved merge markers
  in `api_key_service.go` and `settings_view.go`; log
  `typesafe-capture-service-compile.log`. Not behavioral RED or a passing suite.
- Added `TestB8deSystemOneMissingUsageClassifiedBeforeCapture` in handler pricing
  preflight tests: protects terminal-error classification, provider completion
  proof, no account penalty and unchanged already-written response. Focused
  `go test -count=1 -tags=unit ./internal/handler -run
  '^TestB8deSystemOneMissingUsageClassifiedBeforeCapture$'` also failed compilation
  on the same service merge markers (exit1, tool output only). Must execute the
  approved RED/GREEN mutation check after compile staging; gofmt is not proof.

Investigation correction: recordSystemOneUsage calls clientRequestedUsageFields
inside its asynchronous closure, but the local helper currently ignores Gin
state and uses passed arguments only. Therefore a Gin race has not been
demonstrated; do not label this a reproduced bug. Constructing the usage input
before queueing will be considered with the required local pricing preflight,
not justified as an already-proven race fix.

## Approved unified-key routing decision (2026-10-06 Asia/Shanghai)

Auto/unified keys currently resolve only Anthropic/OpenAI provider defaults.
`InboundProviderFromPath` defaults `/v1/systemone` to Anthropic;
`NormalizeAPIKeyType`, `APIKeyTypeFromGroupPlatform` and user routing DTOs have
only those two providers. Asked user: option1 (recommended) accepts only keys
explicitly bound to a TypeSafe group and clearly rejects unified keys for this
endpoint; option2 adds a third TypeSafe default/user route and migration.
User subsequently answered `1`: explicit TypeSafe-group keys only; no third
provider default, user-routing field or migration. Ingress normalization now
marks native TypeSafe before auth. Dynamic auto/default-follow keys receive
`SYSTEMONE_STATIC_KEY_REQUIRED`/403 before any chat default resolution; a
handler guard also rejects dynamic keys before scheduling. Static/legacy empty
binding modes retain their stored group and normal group/auth checks. Existing
Anthropic/OpenAI ingress detection is unchanged. Added service/handler routing
regressions and auth error mapping coverage; compilation is still blocked by
remaining duplicated symbols from delete/modify conflicts, so these are not
yet proven passing. The later approval supersedes the original unified-Key
line in the Task5 brief/spec.

## Subsequent focused verification (supersedes compile blockers above)

The shared service and handler packages now compile. Service routing, capture,
Key-cap and allowlist tests passed; deliberate routing/capture/alpha-pricing
mutations failed and were restored before the passing run. Logs:
`typesafe-alpha-mutation-red.log`, `typesafe-key-security-green.log`,
`typesafe-security-service-broad.log` in the task scratch evidence directory.

Handler/router focused run `go test -count=1 -p 2 -tags=unit
./internal/handler ./internal/server/middleware ./internal/server/routes
-run 'B8deSystemOne|PublicOrder|RegisterPaymentRoutes|PaymentRoutesPublicPlans|APIKeyAuth|InvalidStream'`
passed (0.037s/0.028s/1.769s); log
`typesafe-payment-handler-focused-3.log`. Earlier two runs failed on missing
local stream parse declarations and upstream test constructor mismatches; they
are not passing evidence. The declarations were restored, and excluded
Composite model-catalog tests were removed without removing native TypeSafe
or existing local model-list tests.

Task5 remains incomplete: broader native contracts, cancellation/idempotency,
handler preflight mutation proof and final full-suite/audit checks are pending.
