# CN / Command Code probe temporary-restriction CAS repair

Worktree: `/home/alvin/tokenstation3/.worktrees/upstream-sub2api-20261012-9591`.
Assigned base: `21e939e0bf9e9677d8ea631d4b6b09918612088b`. Root independently
committed the DTO/frontend fix as `031df8290853012651adf231004bd974d5eae299`
while this work was in progress. All repair source is unstaged at handoff.

## Cause and behavior

Quota/balance top-up recovery and periodic balance recovery chose ownership from
a detached `Account` snapshot, then called unconditional clear. Periodic low
balance similarly chose eligibility from the old snapshot and used the ordinary
monotonic setter, whose longer deadline overwrote a newer shorter opaque limit.

`TempUnschedulableCompareAndSetter` is an optional narrow capability. The probe
helper compares the complete observed `TempUnschedulableUntil` + reason tuple;
missing capability, mismatch, or error returns no state change. Success logging,
paused/cleared outcomes, and detached snapshot mutation happen only after an
applied CAS. Only replacement timestamps are normalized to PostgreSQL microsecond
precision. The loaded timestamp expectation is passed through exactly.

The production repository uses one conditional `UPDATE` inside a CTE, with its
outbox insert in the same statement. Match updates/clears just the restriction
pair and `updated_at`. A changed deadline, even with the same reason, rejects the
write. `updated_at` is intentionally excluded from the expectation because probe
`UpdateExtra` moves it independently. NULL and empty SQL reasons both match the
empty domain reason; nil deadline still compares its reason. Rejection refreshes
the scheduler from the authoritative row, and missing/deleted rows are evicted.
Propagation uses a bounded context detached from request cancellation. SQL/outbox
errors return false without publishing changed state.

Existing request-side `SetTempUnschedulable` and `ClearTempUnschedulable` remain
unchanged. No migration, API, DTO, frontend, generated source, or credentials are
part of this repair.

## Files

- `backend/internal/service/temp_unschedulable_probe.go`: optional capability and
  safe shared helper.
- `backend/internal/service/command_code_usage.go`: top-up window recovery uses
  CAS, with nil-account guard.
- `backend/internal/service/cn_provider_balance_check_service.go`: shared
  Kimi/DeepSeek/Command Code balance set and clear paths use CAS.
- `backend/internal/repository/account_repo_temp_probe_cas.go`: atomic pair CAS,
  compile-time capability assertion, and authoritative scheduler propagation.
- `backend/internal/service/temp_probe_cas_test.go`: unit-tagged deterministic
  interleaving regressions and success/failure coverage, 39 new leaf scenarios.
- `backend/internal/repository/account_repo_temp_probe_cas_integration_test.go`:
  26 isolated PostgreSQL leaf scenarios, including outbox rollback.
- Existing `command_code_usage_test.go` and `cn_provider_balance_service_test.go`
  doubles now model detached restriction snapshots and the CAS capability. The
  common CN double remains available without the unit build tag.

## RED receipts

All commands were invoked through `.superpowers/20261012-sync/run-check.py`.
Each listed basename has its exact `.json` command/HEAD/time/exit metadata and
`.log` output beside this file.

| Receipt | Exit | Observed failure |
| --- | --- | --- |
| `temp-probe-cas-service-red` | 1 | Quota and balance top-up erase a real request-side spend cap; periodic healthy clears it; periodic low rearms over it. Same-reason/new-deadline and opaque-rule variants also fail. The original branches report state changes despite stale writes. |
| `temp-probe-cas-repository-red` | 1 | Runtime capability assertion fails before repository implementation, including exact tuple/nil/expiry and missing/deleted scenarios. |
| `temp-probe-cas-outbox-red` | 1 | Runtime capability assertion fails before atomic outbox implementation. |
| `temp-probe-cas-empty-account-red` | 1 | Supplemental nil-account top-up recovery test detects a panic before the guard. |

The first RED ran against HEAD `21e939e0...`; later receipts record HEAD
`031df829...`. No service/repository production change preceded the initial
service RED or repository capability RED.

## GREEN receipts

| Receipt | Exit | Verification |
| --- | --- | --- |
| `temp-probe-cas-service-green` | 0 | Initial reproduced service races and set/clear applied/error/absent-capability cases. |
| `temp-probe-cas-focused-unit-verified` | 0 | Fresh verbose unit verification on final source: Command Code and CN balance tests plus existing repository monotonic setter no-op/outbox test. |
| `temp-probe-cas-focused-integration-final` | 0 | PostgreSQL exact tuple set/clear, 1-microsecond mismatch, changed reason, expiry, NULL/empty reason, nil tuple, independent extra/updated_at update, other account fields untouched, scheduler applied/mismatch refresh, missing/deleted eviction, and atomic outbox failure rollback. |
| `temp-probe-cas-common-default` | 0 | Shared CN service tests compile and pass without build tags. |
| `temp-probe-cas-common-integration` | 0 | Shared CN service tests compile and pass under integration tags. |

Final focused unit command:

```sh
python3 .superpowers/20261012-sync/run-check.py temp-probe-cas-focused-unit-verified backend go test -v -p 1 -tags unit ./internal/service ./internal/repository -run 'Test(CommandCode|HandleUpstreamError_CommandCode|CNProviderBalance|CNBalanceProbeCAS|AllCNBalances|AccountRepository_SetTempUnschedulable)' -count=1
```

Final PostgreSQL command:

```sh
python3 .superpowers/20261012-sync/run-check.py temp-probe-cas-focused-integration-final backend go test -p 1 -tags integration ./internal/repository -run '^TestAccountRepositoryTempProbeCAS' -count=1
```

Common service tag checks:

```sh
python3 .superpowers/20261012-sync/run-check.py temp-probe-cas-common-default backend go test -p 1 ./internal/service -run '^Test(CNProviderBalance|AllCNBalances)' -count=1
python3 .superpowers/20261012-sync/run-check.py temp-probe-cas-common-integration backend go test -p 1 -tags integration ./internal/service -run '^Test(CNProviderBalance|AllCNBalances)' -count=1
```

Runner uses local Go 1.27.2, `GOTOOLCHAIN=local`, `GOMAXPROCS=2`, `CI=true`, and
package concurrency 1. Repository integration uses the existing local
testcontainers harness with isolated ephemeral PostgreSQL/Redis ports, no
external DSNs. All focused final commands exited 0. `git diff --check` is clean.

## Scope and remaining gates

The CN/Command Code probe temp-write call sites are all covered by the shared
CAS. The search also found older OAuth-only `TokenRefreshService.postRefreshActions`
unconditional recovery clearing; it is outside the assigned CN/API-key scope and
was reported to root, unchanged. Other temp setters are request/transport or
quarantine paths and retain their existing semantics.

This handoff does not claim the full backend suite, full lint/build gates, race
detector, or independent final review. Root owns those next. No production access,
credentials, external upstream API calls, generation, staging, or commits were
performed by this implementation agent.
