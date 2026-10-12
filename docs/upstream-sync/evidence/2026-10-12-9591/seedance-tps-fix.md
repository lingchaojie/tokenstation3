# Seedance text TPS classification correction

Starting HEAD: `aab8be5b5ee54ecc8dc78dea6077bf300d57d716`.

## Scope and evidence

The release review reproduced a Seedance completion entering text TPS because it records positive `output_tokens`, positive duration, zero `video_count`, and token billing. The zero video count is deliberate: existing service and handler tests require token pricing. Both normalized endpoint fields are already persisted by `recordGrokMediaUsage`; model aliases are not a reliable media identity. The canonical endpoint is `/api/v3/contents/generations/tasks`.

The correction excludes a TPS sample if either persisted endpoint equals that canonical endpoint. SQL uses a parameter appended after the existing filter arguments and `IS DISTINCT FROM`, retaining ordinary text with NULL or empty endpoints. The frontend uses the same endpoint identity and retains all existing count, billing-mode, request-type, duration, and token validity checks. There are no billing, forwarding-result, producer-count, DTO, migration, or generated-file changes.

## Changed files

- `backend/internal/domain/constants.go`: canonical Seedance endpoint shared with the handler and repository.
- `backend/internal/handler/endpoint.go`: existing handler constant aliases the domain constant.
- `backend/internal/handler/endpoint_provider_test.go`: existing Seedance route-alias loop also verifies canonical endpoint normalization.
- `backend/internal/repository/ops_repo_dashboard.go`: parameterized exclusion of Seedance on either endpoint, only in `queryOutputTPS`.
- `backend/internal/repository/ops_repo_dashboard_integration_test.go`: real PostgreSQL regression for Seedance-only and mixed text/media windows, raw/auto/preagg modes, inbound-only/upstream-only identity, zero media counts, NULL billing and historical NULL image count, ordinary text with the same model alias and NULL/empty endpoints, and unchanged success/request/token totals.
- `frontend/src/utils/latencyHealth.ts`: exclude canonical Seedance endpoint from the output-rate formatter and accept the existing optional endpoint fields.
- `frontend/src/utils/__tests__/latencyHealth.spec.ts`: endpoint classification regressions for missing/zero video count plus normal text controls.
- `frontend/src/components/admin/usage/__tests__/UsageTable.spec.ts`: real component output-rate rendering for a historical Seedance row without video count, an upstream-only Seedance row, and ordinary text using the same model alias.

## Verification commands and artifacts

Every command runs from the worktree root through `.superpowers/20261012-sync/run-check.py`. The runner fixes Go to local 1.27.2 (`GOTOOLCHAIN=local`), `GOMAXPROCS=2`, `CI=true`, and uses ephemeral testcontainers PostgreSQL/Redis for integration. Frontend uses Node 22.22.2, pnpm 9.15.9, and two Vitest workers. Each label has matching `.json` metadata and `.log` raw output in this scratch directory.

```bash
python3 .superpowers/20261012-sync/run-check.py seedance-tps-red-backend backend go test -count=1 -p=1 -tags=integration ./internal/repository -run '^TestOpsOutputTPSSeedanceEndpoints$' -v
python3 .superpowers/20261012-sync/run-check.py seedance-tps-red-frontend frontend node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec vitest run src/utils/__tests__/latencyHealth.spec.ts src/components/admin/usage/__tests__/UsageTable.spec.ts --maxWorkers=2 --minWorkers=2
python3 .superpowers/20261012-sync/run-check.py seedance-tps-green-backend backend go test -count=1 -p=1 -tags=integration ./internal/repository -run '^TestOpsOutputTPS(SeedanceEndpoints|Distribution|QueryDeadline)$' -v
python3 .superpowers/20261012-sync/run-check.py seedance-tps-green-frontend frontend node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec vitest run src/utils/__tests__/latencyHealth.spec.ts src/components/admin/usage/__tests__/UsageTable.spec.ts --maxWorkers=2 --minWorkers=2
python3 .superpowers/20261012-sync/run-check.py seedance-tps-green-unit backend go test -count=1 -p=2 -tags=unit ./internal/service ./internal/handler ./internal/repository -run '^Test(Seedance|OpsOutputTPSOverviewFailurePolicy|IsQueryTimeoutErr)'
python3 .superpowers/20261012-sync/run-check.py seedance-tps-frontend-lint frontend node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec eslint src/utils/latencyHealth.ts src/utils/__tests__/latencyHealth.spec.ts src/components/admin/usage/__tests__/UsageTable.spec.ts
git diff --check
```

Observed RED before implementation:

- Backend exited 1: Seedance-only window incorrectly returned four text TPS samples, all at `205.75`, instead of zero. The historical NULL-image-count row was already excluded by the retained count filter.
- Frontend exited 1: five endpoint helper cases and the actual UsageTable regression returned `205.8 tok/s` instead of `—`; 62 existing/control tests passed.

Observed GREEN after implementation:

- Frontend focused Vitest: exit 0, 2 files / 68 tests passed.
- Frontend emitted the existing stale Browserslist/caniuse-lite warning; no test or lint failures.
- Targeted frontend ESLint: exit 0.
- Backend real PostgreSQL integration: exit 0, `TestOpsOutputTPSDistribution`, `TestOpsOutputTPSQueryDeadline`, and `TestOpsOutputTPSSeedanceEndpoints` passed. The Seedance-only window has zero TPS samples across raw/auto/preagg while retaining five successful requests and 61,725 tokens. The mixed window has three text samples (P5 1.9, P10 2.8, P50 10, average 37) and retains all eight successful requests and 61,846 tokens in every mode.
- Backend focused unit checks: exit 0 for service, handler, and repository, including existing Seedance lifecycle/forwarding/validation and `VideoCount=0` pricing assertions, the complete Seedance alias-normalization loop, and output TPS timeout/cancellation/error policy.
- `git diff --check`: exit 0.

## Limits and handoff

SQL `video_count` is `INTEGER NOT NULL DEFAULT 0` (migration 172), so a real PostgreSQL NULL-video-count fixture is impossible without changing schema. The regression instead preserves historical nullable `image_count` behavior, NULL billing/endpoint cases, and frontend DTOs with no video count. Neither database schema nor current NULL-count filtering was changed.

Rows with neither endpoint recorded and zero media counts cannot be identified as Seedance solely from the existing persisted fields. The correction intentionally uses the persisted canonical endpoint and keeps text model aliases eligible.

Only focused RED/GREEN and targeted lint were authorized for this subtask. Root will run the complete release gate after handoff. No generator, broad suite, build/dist mutation, production access, credential access, staging, or commit was performed.
