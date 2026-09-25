# Deployment and retention implementation-domain audit

Reviewed changed `backup*`, `ops*` and `dashboard*` service production files and
their tests, including `request_log_retention_test.go`, against local HEAD and
upstream a3eb. The service production changes match upstream and had no divergent
local changes in these files since merge-base 881f3202. This audit is not the
required independent final review.

## Semantic coverage

- Backup: ordinary/finite-archive/permanent-archive pools; date clamping and
  schedule timezone; successful scheduled-backup-only archive assignment;
  atomic record/checkpoint persistence; protection surviving retries, deletion,
  restart and full restore; explicit archive deletion confirmation; no fixed
  100-record truncation. Retained multipart backup/restore and encrypted S3
  secret handling. The inherited-secret save fix encrypts on every persistence.
- Concurrency: common cross-instance writer lock, bounded lock acquisition and
  work timeout, no Redis-error fallback to a second lock namespace, active
  restore reservation before launching, stale restore-generation rejection,
  failed restore not recreating removed records, state-write-before-object-delete
  recovery, and deferred stale-peer recovery. Read failures fail closed.
- Request retention: nullable legacy field preserves the current deployment
  policy; explicit zero skips request and billing-dedup deletion; invalid/read
  failures skip cleanup; dedup retention is no shorter than request retention.
  Runtime retention is available when aggregation is disabled, without enabling
  aggregate deletion. Verified setting-repository wiring and existing singleton
  locks/group-usage sync remain in place.
- Read all current-sync changes in deploy `.env.example`, config example, four
  Compose files and the new Compose test; root Makefile/README/.gitignore; and
  backend-ci. New simple-mode knobs preserve default behavior and empty group
  override preserves YAML. Local GHCR/update repository, bind/security/cache
  policy, Caddy domains, analytics CSP, WebChat docs, Makefile custom tests and
  generated-code/build CI gates remain. No deployment was performed.
- Extended backend-ci's release-helper syntax check to include the new local
  branch tag resolver; otherwise no additional retention/deployment code change.

## Verification

Passed:

- `go test ./internal/service -run 'Test(Backup|RecoverStale|GracefulShutdown|StartBackup|StartRestore|RequestLogRetention|RuntimeLogConfig|DashboardAggregation)' -count=1`
- Same command with `-tags=unit` (needed for backup suites).
- `go test -race -tags=unit ./internal/service -run 'Test(BackupRetention|BackupRecovery|RequestLogRetention|RuntimeLogConfig_RequestRetention|DashboardAggregationService)' -count=1`: passed, 1.416s test execution.
- CI shell checks: apple-container syntax/lifecycle (fixture commands, no real
  containers), Compose security, Gateway env, runtime resources, Caddy cache.
- New Docker Compose simple-mode environment test: all four Compose files and
  unset/empty/true/false overrides; only `docker compose config`, no services run.
- Additional `docker-deploy-test.sh` and `install-github-token-test.sh`.
- Release helpers: 14 Python unittest cases and both shell syntax checks.

Extra historical `deploy/tests/task12-local-contract-test.sh` fails identically
in the integration worktree and detached DEV_BASE a892514e: it rejects
`gateway.live.max_session_duration_seconds`, already present at DEV_BASE. This
test is not a current CI step; no Live field was introduced by this sync. The
baseline comparison was executed, not inferred. Left the established config
unchanged; coordinator must retain this known-baseline result in final evidence.

Logs: `/tmp/sub2api-retention-{service,unit,race}-tests.log`. Global Go generation,
full suite, lint, build and integration verification remain coordinator-owned.
