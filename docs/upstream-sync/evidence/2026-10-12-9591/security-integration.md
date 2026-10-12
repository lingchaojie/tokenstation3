# Security, build and deployment integration evidence

Author domain review, 2026-10-12. This is not the independent final review or an
exact-final-tip CI result. No production credentials, servers, accounts or
providers were accessed. No staging, commit, push, reset, checkout or generation
was performed by this domain owner.

Coordinates: DEV_BASE `591a03c550491b5d9f2e5c04bbc53dc1ae3177f3`, merge first
parent `c517f84cd`, base `b8dece9000c68815a5b867ca5a1e6f236e173905`, upstream
pin `9591136d45cef26453348ed9532a7ebbce7cf99c`.

Read root AGENTS.md, repository sync skill, the 2026-10-12 assessment and prior
2026-10-06 archive/security evidence. Coverage below includes all 20 assigned
upstream paths, plus README_JA.md and Makefile requested by the coordinator.
Compared base, ours, pinned upstream and final content/diffs. Automatically
merged files were included in the audit.

## Per-file semantic audit

| File | Upstream intent; local/final behavior; evidence |
| --- | --- |
| `.github/audit-exceptions.yml` | Clarify existing two xlsx advisories as write-only; only `UsageView.vue` dynamically imports xlsx and calls worksheet builders/`XLSX.write`, no XLSX.read path. Retain local `2027-07-07` expiry, package/advisory/severity/owner and other exceptions exactly. Adopt narrower rationale/mitigation; no new exemption or expiry extension. Existing Vue/source-map repairs remain frontend-owned. |
| `.github/workflows/backend-ci.yml` | Require Go 1.27.2 and lint 2.14.0. Retain shell checks, generate consistency, backend build, unit/integration, frontend typecheck/critical+WebChat tests/build, release-helper unit/syntax steps. Generate remains before build/tests. |
| `.github/workflows/release.yml` | Sole candidate delta is Go version assertion 1.27.2. Retain local release branch/tag/dry-run contract, source-SHA pinning, preserved workflow tooling, target matrix and caching/publishing machinery. Release helper 14 tests pass. |
| `.github/workflows/security-scan.yml` | Sole candidate delta is Go assertion 1.27.2. Actual scanner command remains govulncheck ./..., frontend production audit still enforced through existing exception checker, read permissions and scheduling preserved. Default scheduled branch remains release; dev sync alone cannot repair release's next scheduled run. |
| `Dockerfile` | Only Go builder image moves to 1.27.2-alpine. Native-platform cross-compilation, node/pnpm images, cache mounts, legal document build resources, PostgreSQL18 client, runtime resources, non-root/drop-privilege entrypoint and healthcheck remain intact. |
| `backend/Dockerfile` | Only development Go image moves to 1.27.2-alpine; existing module download/build/version resolution remains. |
| `deploy/Dockerfile` | Only Go builder image moves to 1.27.2-alpine. Retain Node memory limit, pnpm9.15.9, legal/resources copies, version resolution, non-root ownership/entrypoint/healthcheck. Runtime resources test passes. |
| `backend/go.mod` | Set Go1.27.2, upstream security/tool versions crypto0.57/mod0.41/net0.60/sync0.23/sys0.48/term0.46/text0.42/tools0.50. Keep higher/local zap1.28/image0.45/exp20260410/time0.15 and complete local AWS/testcontainers/Tailscale graph. Validator becomes direct because new admin platform validation imports it. Do not reintroduce removed plugin/captcha or direct gRPC dependency; protobuf stays indirect. Module graph confirms selected versions. |
| `backend/go.sum` | Automatic merge adds pinned x/* hashes without dropping local dependency hashes; copied graph loads under -mod=readonly and `go mod verify` passes. No whole-project dependency upgrade or tidy was used. |
| `backend/.golangci.yml` | Preserve all original linters/rules. Add upstream exact-path+SA1019-symbol exclusions for HTTP2 adapters and legacy GOAWAY error only. Actual local usage includes frame-level PING tests, which x/net0.60 does not deprecate; no new exclusion needed. Config verify and staticcheck for repository/server/service pass under lint2.14.0. |
| `README.md` | New installs leave admin email/password empty for secure setup generation, log search matches actual `Generated admin` output; all local repo/image branding and surrounding deployment instructions unchanged. |
| `README_CN.md` | Same bootstrap update; conflict resolves to local script-created docker-compose.yml command with new log prefix, preserves local Caddy/domain/data-management guidance. |
| `README_JA.md` | Same bootstrap update/log prefix; upstream automatic hunk retains local branding and surrounding manual deployment commands. |
| `deploy/.env.example` | Empty admin email fallback and 8-72 byte password explanation align with setup.prepareAdminCredentials; local simple-mode/auth/protocol environment options remain. |
| `deploy/APPLE_CONTAINER.md` | Explain both generated email/password; actual Apple helper path and surrounding lifecycle commands untouched. Apple fixture tests pass. |
| `deploy/README.md` | Preserve local Caddy/domain inputs and script-created docker-compose.yml commands while updating generated credentials, log prefix and password length. Domain table entries remain; admin defaults match templates/setup. |
| `deploy/docker-compose.dev.yml` | Only ADMIN_EMAIL fallback becomes empty. Preserve no-new-privileges and dev build/dependency/environment contract. Compose renders unset/empty/explicit email correctly. |
| `deploy/docker-compose.local.yml` | Same admin fallback change; local GHCR image and bind host, storage, environment, no-new-privileges unchanged. Compose contracts pass. |
| `deploy/docker-compose.standalone.yml` | Same admin fallback change; local image, standalone dependency settings and no-new-privileges unchanged. Compose contracts pass. |
| `deploy/docker-compose.yml` | Same admin fallback change; local image, bind host and named volumes, environment/security contract unchanged. Compose contracts pass. |
| `deploy/docker-deploy.sh` | Generate unpredictable admin login on newly prepared installs, fail on entropy failure, persist via GNU/BSD sed and show in local completion summary. Existing early domain validation, overwrite prompt, release download URL, secure .env mode, Caddy opt-in/config rollback and normalized summaries remain. Fixture domain tests and random-email shape/uniqueness/default-exclusion/failure proof pass. |
| `Makefile` | Upstream adds eight critical frontend specs for platform/account/protocol/profile/default-pricing behavior. Preserve every local listed spec, WebChat list and lint/typecheck-before-tests sequence. Static path check finds 53 unique existing specs across both lists, no missing test paths; make -n verifies intended command sequence. |

HTTP2 dependency control flow was checked in cached x/net0.60 sources, not only
deprecation comments: go1.27 `configureTransports` links the returned legacy
Transport to native net/http; transportConfig.HTTP2Config reads ReadIdleTimeout
as SendPingTimeout and preserves PingTimeout when the request initializes.
`configureServer` registers its config before serving; serverConfig.HTTP2Config
maps MaxConcurrentStreams/MaxReadFrameSize and MaxUploadBufferPerConnection/
PerStream to native stream/frame/receive limits, with separate IdleTimeout.
This explains why keeping the compatibility adapter retains local timeout and
resource policy. Actual client protocol/PING regressions pass; server regression
scope is stated precisely below.

## Tool preparation and isolated A/B validation

Scratch: `/tmp/tokenstation3-20261012-security`. No scratch files or binaries are
inside the repository. Go1.27.2 initial proxy.golang.org download failed with
unexpected EOF; retry via `GOPROXY=https://goproxy.cn,direct` succeeded. Global
Git/network settings were not changed. Exact tools:

```sh
export PATH=/home/alvin/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.2.linux-amd64/bin:/tmp/tokenstation3-20261012-security/bin:$PATH
export GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct GOFLAGS=-mod=readonly
export GOMAXPROCS=2
```

Go1.27.2 is module-cached; scratch GOBIN contains govulncheck1.9.0 (built with
Go1.27.0, matching failed CI scanner version) and golangci-lint2.14.0 (built with
Go1.27.2). Tool versions/install logs retained in scratch. Scanner DB metadata
reports `2026-10-08 22:31:09 +0000 UTC`.

DEV_BASE backend was exported using `git archive` into `baseline-dev`; an
independent `baseline-with-security-deps` copy receives only the merged go.mod,
go.sum and lint config. Both are immutable baseline source for this comparison,
not the still-evolving full merge candidate.

| Command / scope | Result / raw evidence |
| --- | --- |
| Go1.27.0 govulncheck1.9 `./...` on DEV_BASE source | exit3, same 11 reachable advisory IDs as failed release run38232613680: GO-2026-6603/6604/6605/6607/6608/6609/6610/6611/6612/6613/6617. `baseline-dev-govulncheck.log`; matched CI `/tmp/tokenstation3-security-38232613680.log`. |
| Go1.27.2 govulncheck1.9 `./...` on same source + final dependency graph | exit0, zero symbol/package vulnerabilities. `baseline-with-security-deps-govulncheck.log`. |
| `govulncheck -show verbose ./...` on upgraded-dependency baseline | exit0. One module-only unimported advisory GO-2026-5932, x/crypto/openpgp unmaintained, Fixed N/A; no imported package/call. No exemption added. `baseline-with-security-deps-govulncheck-verbose.log`. |
| `go list -m all` / `go mod verify` on upgraded graph | both exit0, expected local and updated selected module versions, all modules verified. `candidate-module-graph.log`, `candidate-mod-verify.log`. |
| lint2.14.0 `config verify` on active lint config | exit0. Config-only check reads no merge-source packages. |
| lint2.14.0 `run --enable-only staticcheck --concurrency=2 --timeout=30m --max-issues-per-linter=0 --max-same-issues=0 ./internal/repository/... ./internal/server/... ./internal/service/...` on upgraded baseline | exit0 / 0 issues. `http2-deprecation-baseline.log`; demonstrates local HTTP2 breadth fits narrow upstream compatibility rules. |
| Go1.27.2 `go test -count=1 -p 1 -tags=unit -timeout=5m ./internal/repository -run 'HTTP2PingPolicy|HTTP2KeepAlive'` on upgraded baseline | exit0, repository0.022s. Local real frame/PING policy and adapter timeout regressions retained. `http2-preserved-regressions.log`. |
| Broader HTTP2 regression selection: `go test -v -count=1 -p 1 -tags=unit -timeout=5m ./internal/repository -run 'TestEnableHTTP2KeepAlive\|TestBuildUpstreamTransport_(HTTP2\|LongStreamH2\|NonHTTP2\|NegotiatesExpectedProtocol)'` | exit0, repository0.046s, all six test functions plus subcases pass: adapter settings, long-stream settings, non-H2 lazy setup, negotiated protocols, real CONNECT-proxy keepalive, seven actual PING-policy cases. `http2-all-local-regressions.log`. |
| Server adapter/ingress regressions: `go test -count=1 -p 1 -tags=unit -timeout=5m ./internal/server -run 'TestProvideHTTPServer\|TestHTTPServer'` on upgraded baseline | exit0, server1.764s. Existing HTTP2 enablement and header/body/slow-header limits remain under new stdlib/x/net; H2C test checks protocol enablement, not actual HTTP2 frame-cap enforcement. `http2-server-ingress-regressions.log`. |
| Deployment/release fixture checks | syntax, docker-deploy domains/rollback, Apple helper, Compose security/gateway/simple-mode, runtime resource packaging, Caddy cache/SSE and release matrix14 tests all exit0. `deploy-release-checks.log`. |
| Four actual `docker compose config --format json` files, ADMIN_EMAIL unset/empty/explicit | all12 cases render expected value; no compose up or provider request performed. |
| Generated admin helper proof | random login shape, distinct random invocations, fixed-default exclusion, entropy failure propagate; exit0. `admin-email-proof.log`. |
| Assigned paths conflict markers / git diff --check | no remaining content markers; diff-check exit0. Index/staging is coordinator-owned and was intentionally untouched by this domain owner. |
| Final active-tree Go1.27.2/govulncheck1.9 `govulncheck ./...`, GOMAXPROCS2, GOTOOLCHAIN=local, GOFLAGS=-mod=readonly | exit0, zero reachable and zero imported-package vulnerabilities; one module-only uncalled advisory. 11:28:23.080430–11:28:36.404865 UTC,13.324s. Production-only manifest includes1480 backend Go files excluding *_test.go plus go.mod/go.sum; before/after SHA256 `c280c98025c018f490f0a921e4a65e1c40fae8e7c4cdfbc729222c6194381cab` identical. `final-candidate-govulncheck.log`/`.json`/`-inputs.sha256`/`-inputs-after.sha256`. |

## Final orchestration still required

First active-tree `govulncheck ./...` attempt, 11:07:35.173342–11:07:48.894260 UTC
(12.040s), exit1 during package loading. Inputs:3187 Go/module files, manifest
digest `591f69f76460ef4e6a1a9548677058b15ecf0af80f7d55b63e44227d0e1fb64e`.
Errors: duplicate isConcreteRequestPlatform in openai_compatible/domain_constants,
unused scheduling domain import, undefined turnBillingAPIKey in gateway handler.
These cross-domain compile errors were handled by the coordinator; no security
conclusion from this attempt and no final-tree stability claim. Raw files:
`final-candidate-govulncheck-attempt1.log`/`.json`/`-inputs.sha256` in scratch.
The coordinator later confirmed successful runtime compilation (build-2), then
authorized the final retry whose successful result is recorded above. That
manifest intentionally excludes test fixtures being adjusted concurrently;
govulncheck analyzes default production packages. Fresh filename+content hashes
were recomputed after scanning, detecting additions/deletions as well as edits.

The coordinator must finish final candidate generation/build/unit/integration/
full lint and frontend audit with the existing exception checker. Any later
production Go/module change requires another govulncheck; current success is
bound to the production-input digest above, not a future commit. These scratch
A/B and final scanner results do not establish semantic safety of all new
upstream code or replace exact-final-tip CI checks. No cross-owner HTTP2
source changes or extra lint compatibility exclusions are currently required.
Scheduled release-branch security remains unresolved by a dev-only sync; release
publication/deployment needs its own authorization and check cycle.

## Final production coordinate

The author scan at production digest c280c980 describes an intermediate pre-commit candidate.
After final model-listing/obsolete-helper integration, root repeated govulncheck on committed
production code `896ca30d6b809f72ddb20a455e2410508792183d`: exit0 at
2026-10-12T11:46:45 UTC, 0 reachable/imported-package vulnerabilities. Full lint
on the same commit exited0 with 0 issues; earlier scans are supporting A/B evidence.
