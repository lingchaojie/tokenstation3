# Security integration — intermediate evidence

2026-10-06 follow-up: real Redis `TestEmailCacheSuite/TestB8de` passes all five
cases, including30 simultaneous wrong guesses (four invalid then26 exhausted),
single successful verification, generation/legacy counters, notify generation,
and reissued hashed reset token with exactly one concurrent consumer
(`email-real-redis-reset.log`,4.686s). No test skipped.

New AuthService regression injects lookup/update DB failures: original password
remains, consumed token cannot later be reused. Signed payment resolve remains
reachable after exhausting the legacy verify budget; intentionally misapplying
the limiter fails400-vs429 (`public-route-mutation-red-2.log`) and is restored.
Real JWT/non-admin requests to Claude query/redeem reject401/403. Allowlist
keyword/hash/pre-block/observe tests now remove membership before processing
the queued task: old work remains log-only/no punishment and new requests resume
normal policy. Combined focused service/routes run passes0.262s/1.760s in
`security-reset-local-contracts-green.log`. Final full-unit/race gates pending.

Key creation keeps local binding modes, hidden WebChat-key exclusion and
non-resetting attempt history. The visible-key hard cap uses a user-row lock
inside a READ COMMITTED count/insert transaction. The stale-precheck service
regression failed when that atomic path was deliberately bypassed, and passed
after restoration. Real PostgreSQL concurrent-last-slot test passed7.757s
(`key-cap-concurrent.log`). This does not establish full quota/billing safety.

Allowlist fields were ported from the upstream split settings files into the
local centralized service/admin handler. GET/PUT retain the local shared
payload builder and optional-field semantics. Duplicate restored DU files were
deleted, recoverable at the pinned upstream SHA. Existing omission tests pass;
additional new-field roundtrip tests and complete async/failure review remain.

Email attempt increments now check the same code+issuance+expiry snapshot that
was read, carry forward pre-upgrade JSON Attempts, and align counter TTL.
Success uses conditional atomic consumption of that generation. A resend or
competing consume cannot delete/validate a replacement code. Comparison of the
submitted code stays constant-time in Go. Notification-email binding consumes
before the DB write as before; it no longer uses unconditional deletion.

Deterministic real Redis-client hooks reproduced resends after GET and after
increment; both accepted stale codes before the fix. JSON Attempts4 also
incorrectly granted extra guesses. All three failed before the change and pass
afterwards (`email-generation-red.log`, `email-generation-green.log`). Actual
Redis Lua/generation/single-winner integration passed5.225s with `CI=true go
test -count=1 -tags=integration -timeout=5m ./internal/repository -run
'^TestEmailCacheSuite$'` (`email-generation-real-redis.log`). Tests use ephemeral
containers and isolated Redis keys, no real users/providers.

The first broader security run exposed a test double sharing one mutable code
between two distinct alias addresses. Replaced that concurrent binding fixture
with the real cache and independently seeded email keys; retained the original
assertion that only one canonical inbox can bind. The second run passed:

`go test -count=1 -p 2 -tags=unit ./internal/service ./internal/repository
./internal/handler/... ./internal/server/routes -run
'B8deSecurity|CreateLimit|CreateCount|ResetToken|Verification|RiskControl|Allowlist|PublicOrder|AuthService.*Register|BindEmailIdentity|OAuthPending'`

Raw logs: `security-focused-broad.log` (failed),
`security-focused-broad-2.log` (passed; service2.425s, repository0.070s,
handler1.544s, admin0.033s, routes1.750s). These are intermediate-tree runs,
not final full-unit/race/generation/CI evidence. Anonymous payment verification
is limited20/IP/min with Redis fail-open; signed resolve and public plans keep
their routes. A signed-resolve isolation regression is still required.
