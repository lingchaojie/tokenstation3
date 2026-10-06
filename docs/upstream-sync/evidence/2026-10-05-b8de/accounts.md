# Account/admin integration — in progress

Latest domain audit covers all44 Task7 paths in coverage.tsv, including their
upstream delta, local retained paths, consumers and changed regression cases.
The excluded group-model request-allowlist test remains absent by prior policy;
TypeSafe is added to display-model candidates only. Upstream Composite ownership
gates in the OpenAI scheduler are excluded, not transplanted. TypeSafe billing
probe identity supports the existing optional read-only probe; official
typesafe.ai targets are blocked from unsupported panel probes, and the previously
excluded automatic rate writeback remains excluded. User notify verification
now delegates to the same generation-fenced consume-before-DB helper as email
verification; no later unconditional deletion can erase a resend.

Claude/status/redeem tests cover eligibility, missing scope/stores/key, server
grant choice, same-key replay, organization concurrency, crash restart, unknown
and unavailable TTLs, and response redaction. New local fault/header tests and
real-auth routes passed in security-reset-local-contracts-green.log. Dashboard
token-vs-cost query/cache contracts are traced; PostgreSQL metric regression
still awaits the full integration run. Audited here means semantic author review,
not test completion or independent SAFE TO PUSH approval.

Follow-up: `TestB8deClaudeResetLocalContract` covers failed pre-POST marker writes
(zero POST), failed final writes (unknown result; restart/replay and another
account of the same org cannot POST again), query-only behavior and proxy/OAuth
headers. Intermediate pass recorded in `admin-reset-contracts.log`. Deliberately
ignoring pre-marker persistence failure produces the expected behavioral failure
in `reset-public-route-mutation-red.log`; mutation restored. Real admin JWT
route test added; first fixture lacked GetUserAvatar and panicked, then corrected
(fixture failure is not a product RED). Final green rerun pending at this entry.

Reviewed dashboard metric from handler allowlist through cache key and repository
top-user CTE: only fixed SQL expressions are selected; ties use user ID. Snapshot
endpoint explicitly retains tokens. Scheduler cache projects auto-reset fields;
TypeSafe extends all12 platform buckets without removing KIRO. Known future reset
keeps a stale threshold paused; elapsed reset releases it. Query failures back off
one minute, recent no-credit10minutes; redemption retries retain existing same-key
behavior. Admin account/group/channel allowlists and account TypeSafe credentials
remain API-key-only; HTTP Grok fallback removes CLI-only headers. These reads do
not replace full unit, integration, race and independent review gates.

Claude reset entry points are GET query and explicit POST redeem under the
existing admin route chain; no automatic redemption task is introduced. Query
validates Anthropic OAuth and user:profile, resolves the account proxy and token,
and only reads usage. Redemption validates those prerequisites before replay,
requires a key and both coordination stores, then enters the existing durable
idempotency coordinator. Account and organization leases serialize execution.
The server fetches the organization and fresh eligibility, selects next_grant_id,
and persists an unknown-outcome fence before the irreversible POST. Failure to
persist that marker returns without POST. Failure to persist a final outcome
leaves the earlier marker in place; later operations are blocked. Unknown
outcomes fence24h, definite unavailability15min; no raw organization/grant IDs
are returned in the outcome.

HTTP construction uses resolved-IP validation, account proxy,25s client timeout
and rejects redirects to avoid forwarding OAuth credentials. Wire generation
injects the existing idempotency coordinator and Redis leader locks. Existing
tests cover same-key replay, duplicate-organization concurrency, crash/restart,
unknown and unavailable fences, missing store/lock failure, known-result and
reason/window filtering. Route tests compare the full middleware chain against
the existing Codex reset route, but use a simulated auth rejection rather than
real non-admin authentication. These observations are not final review approval.

Still required: persistence-failure fault injection with real service control
flow, combined proxy/header contract, full scheduler/admin/dashboard audit and
test coverage. Numeric OAuth scheduling backend is being aligned with the
user's already-approved numeric-only policy; defaults1 and explicit0 must not
depend on the account billing multiplier.

Numeric-policy tests observed failures before implementation: explicit null
returned200, legacy empty readback returned null, both schedulers followed the
account billing rate. After rejection/default/fallback fixes,
oauth-numeric-backend-green.log passes service and admin settings/scheduler/audit
tests. Explicit0 and API Key rate fallback are covered unchanged. The first full
candidate's lifecycle tests expected11 providers; their independent platform
fixture now includes TypeSafe, and all lifecycle cases pass the targeted rerun.
These are intermediate results, not final full-suite evidence.
