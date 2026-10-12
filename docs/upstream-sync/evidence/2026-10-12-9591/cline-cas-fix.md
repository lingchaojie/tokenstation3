# Cline probe scope CAS fix

Parent task scope: Cline usage-probe wallet writes, narrow optional repository
capability, and focused service/PostgreSQL tests. Starting HEAD:
`ee9c34250ba034e925a8d64d5be1ad1757d037df`.

## Verified cause and policy

`QueryUsage` loads an account before HTTP calls. Quota/balance `UpdateExtra`
persists before `applyClineWallets` evaluates the detached account. The former
write mutated the local scope and used unconditional `SetModelRateLimit`, so a
restriction installed after the read could be replaced by a shortage or cleared
by a healthy probe.

Existing policy remains: active opaque restrictions are preserved; expired
restrictions and probe-provable shortages may be replaced/recovered. Request-side
restrictions still use unconditional `SetModelRateLimit`.

## Implementation

- Optional exported `ModelRateLimitCompareAndSetter` capability, with a concrete
  repository compile assertion; no global `AccountRepository` mock expansion.
- Probe passes complete expected scope JSON and exact replacement JSON. `nil`
  expectation means missing scope; `null` means a present JSON-null scope.
- PostgreSQL performs one conditional JSONB UPDATE with full-scope
  `IS NOT DISTINCT FROM`; timestamps and opaque fields participate in comparison.
- Partial JSON update preserves other wallet scopes and unrelated extra keys.
  Null/missing parent containers become an object for an applied write.
- Applied writes enqueue the existing scheduler outbox event and refresh cache.
  Mismatches perform no mutation/outbox event and refresh authoritative cache.
- Probe mutates its detached scope and logs success only after `applied=true`.
  Missing capability, CAS errors, and mismatches preserve that snapshot.
- Replacement timestamps come from the probe's exact JSON, avoiding a different
  repository `time.Now()` generation and allowing reuse of the detached account.
- The Cline test repository now returns detached extra maps and persists writes,
  removing the former shared-object shortcut that concealed this race.

## RED receipts

All commands used local Go 1.27.2, `GOTOOLCHAIN=local`, `GOMAXPROCS=2`, through
`.superpowers/20261012-sync/run-check.py` (which also sets `CI=true`).

1. `cline-cas-service-detached-red.json` / `.log`: exit 1. Command:
   `go test -p 2 -tags unit ./internal/service -run 'TestClineUsageProbe(PreservesConcurrent|WalletWriteFailure)' -count=1`.
   Deterministic detached snapshots and restrictions injected at `UpdateExtra`
   show both shortage and healthy writes overwriting concurrent restrictions in
   both wallets; same-reason timestamp/metadata generations and CAS error/missing
   capability cases also fail.
2. `cline-cas-generation-red.json` / `.log`: exit 1. Command:
   `go test -p 2 -tags unit ./internal/service -run '^TestClineUsageProbeSuccessfulWriteKeepsSnapshotGeneration$' -count=1`.
   Both wallets show DB/local generation mismatch from distinct `time.Now()`.
3. `cline-cas-repository-red.json` / `.log`: exit 1 in real isolated PostgreSQL and
   Redis testcontainers. Command:
   `go test -p 1 -tags integration ./internal/repository -run '^TestAccountRepositoryModelRateLimitCAS$' -count=1`.
   Runtime capability assertion fails, demonstrating the missing production CAS.

The first `cline-cas-service-red` receipt is superseded by
`cline-cas-service-detached-red`: compilation captured the earlier helper before
its map-detachment correction. The latter is the authoritative reproduction.

## GREEN receipts

1. `cline-cas-service-green.json` / `.log`: exit 0. Command:
   `go test -p 2 -tags unit ./internal/service -run 'Test(Cline|HandleUpstreamError_Cline|CNProviderBalanceCheckRunOnceProbesCline)' -count=1`.
   Includes all new regressions, established Cline recovery/preservation policy,
   request-side Cline error behavior, and the periodic Cline probe check.
2. `cline-cas-repository-green.json` / `.log`: exit 0 in isolated local
   PostgreSQL/Redis testcontainers. Command:
   `go test -p 1 -tags integration ./internal/repository -run '^TestAccountRepositoryModelRateLimitCAS$' -count=1`.
   Matrix covers both wallets and clear/set outcomes, full scope match/mismatch,
   same-reason reset/limited-at changes, opaque metadata changes, missing vs JSON
   null, null parent normalization, preservation of other wallet/unrelated extra,
   exact replacement timestamps, outbox count, and authoritative scheduler cache.
3. `git diff --check`: exit 0 after formatting the five owned Go files.

## Handoff

Owned files:

- `backend/internal/service/cline_usage.go`
- `backend/internal/service/cline_usage_test.go`
- `backend/internal/service/cline_test.go`
- `backend/internal/repository/account_repo_model_ratelimit_cas.go`
- `backend/internal/repository/account_repo_model_ratelimit_cas_integration_test.go`

No stage/commit, generator, frontend mutation, production connection, or
credential read performed. Parent owns review, full backend validation, any
generation pass, independent final path review, and publication.
