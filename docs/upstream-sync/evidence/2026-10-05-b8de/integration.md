# Integration evidence

## Baseline and merge

Execution worktree: `.worktrees/upstream-sub2api-20261005-b8de`.
Baseline HEAD: `eefcd4f40a47a6b2902844d70a1fbeb45b39d751` (only approved planning documents after DEV_BASE).
DEV_BASE and backup: `2229e149bb9153e90027b59bfdf78a4c3c91fc7d`.
Merge base / last upstream: `a3eb7ef302961cba716dc78b39b93b60c467db0e`.
MERGE_HEAD / pinned upstream: `b8dece9000c68815a5b867ca5a1e6f236e173905`.

Baseline results on 2026-10-05; timestamps below UTC. Raw logs currently retained in this plan's ignored `.superpowers/sdd/2026-10-05-sub2api-b8de-integration/` directory; final candidate results remain pending.

| Command | Start / end | Result | Isolation / log |
| --- | --- | --- | --- |
| backend `make check-generate` | 08:32:34 / 08:33:13 | exit 0 | Exclusive Ent/Wire generation; clean status; `baseline-generate.log` |
| frontend frozen pnpm install | 08:32:36 / 08:32:39 | exit 0 | Worktree node_modules, unchanged lock; `baseline-pnpm-install.log` |
| root `make test-frontend` | 08:33:18 / 08:34:40 | exit 0 | lint/typecheck; critical 498, WebChat 75 tests; `baseline-frontend.log` |
| backend `make build` | 08:36:49 / 08:38:31 | exit 0 | Own bin/server; `baseline-backend-build.log` |
| backend `go test -count=1 -p 2 -timeout=20m -tags=unit ./...` | 08:36:50 / 08:45:34 | exit 0 | Temporary test output, no integration DB; `baseline-backend-unit.log` |

Frontend baseline emits unresolved router-link test warnings and an outdated Browserslist warning. Local Node is 22.22.2; Node20 exact-SHA CI is still required. Go is 1.27.0, pnpm9.15.9, Docker29.1.3; PostgreSQL18.1/Redis8.4 test images available.

Ordinary `merge --no-ff --no-commit` was run only after the baseline passed and git status was clean, with rerere disabled. It returned expected conflict exit1, with 96 unmerged paths. Original user workspace and production resources were not modified. No integration completion, review, push or CI result is claimed here.

## Pending gates

### 2026-10-06 deployment / generation audit

The three Compose files replace shell-expanded Redis startup with an argv list;
save/AOF/fsync/auth flags remain, an empty requirepass keeps the former no-password
behavior, and password metacharacters no longer enter a shell. Local images,
ports, resource limits and no-new-privileges remain unchanged. Setup removes only
obsolete requests_per_minute/burst_size fields, not active cooldown configuration.
Router adds the existing Redis client solely to payment route registration and
does not restore the excluded panel limiter/Composite resolver.

Ent changes derive from bonus_amount/default0/decimal20,2 and the TypeSafe quota
validator. Generated accessors/mutation/create/update/predicates and field-index
shifts agree with those inputs. Wire constructs the shared idempotency coordinator
before Claude service injection; no extra background redemption service is added.
Exclusive `make check-generate` passed08:13:17–08:13:31 UTC with no generated drift
(`candidate-generate-2.log`). Consumers began only after completion.

README TypeSafe instructions now remove stale Composite claims and explain static
group-bound keys/capture-off default. Config comments match local eligible funding
and async billing reservation lifetime; default remains false. VERSION0.2.13
matches the pinned upstream release ancestry. Composite documentation stays
excluded. Upstream SECURITY policy is labeled as upstream policy so its contacts
and support commitments are not misrepresented as this fork's.

Deployment/release checks passed08:09:05–08:09:10 UTC (`deploy-release-checks.log`):
apple syntax/lifecycle, Compose security/gateway/simple-mode env, runtime resources,
Caddy cache/streaming,14 release-matrix tests and release shell syntax. Apple tests
use fake container executables and a unique temp directory; Compose runs config
rendering only. No real stack was started, deleted or deployed.

All semantic coverage, local adaptation regressions, final generated-code check, full final-tree verification, independent full review, remote drift check, non-force push and exact-SHA CI remain pending.
