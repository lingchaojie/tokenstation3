# 2026-09-04 reviewed a2fb sync: current-dev drift wrapper

## Scope and status

This supplemental record describes the wrapper merge that composes the already
reviewed, immutable `a2fb09260a955676f99cdc92f05469febee82a08` upstream sync
with the current `dev` tip. It does not reopen the upstream range and does not
authorize a fetch, push, deployment, production access, provider/account API
call, or `main` update. The only current publication scope is `dev`.

The authenticated candidate record remains
[`2026-09-01-sub2api-0.1.185-a2fb.md`](./2026-09-01-sub2api-0.1.185-a2fb.md),
unchanged at Git blob `6a20def349d5b3522513a8ac96cb29c27601b363`
and SHA-256 `c082f039df4fa948c2b910ec18c1e9083cd97e42fee94a7380fa9dd57bc1913d`.

## Immutable coordinates and topology

- wrapper first parent/current `dev`: `5c9f2b2bc59cab953ffe736653d28dcd1b799761`
- wrapper second parent/reviewed candidate: `8655bc05bd43b09711a31c6d289016a95e0ebad3`
- candidate parents: `f7ed1ba92e7e89ed307c36ed09bde0adf483941d`
  then `a2fb09260a955676f99cdc92f05469febee82a08`
- wrapper subject: `merge: integrate reviewed a2fb sync into current dev`
- retained backup ref: `backup/dev-before-upstream-sync-20260904-a2fb` =
  `5c9f2b2bc59cab953ffe736653d28dcd1b799761`

The wrapper commit SHA is intentionally reported outside its own tree after
commit creation. Its ordered parents and subject are verified post-commit.

The first-parent side contains exactly the following 11 commits after the
four-way merge base `f7ed1ba92e7e89ed307c36ed09bde0adf483941d`:

1. `2a7590905db0` — design unified model catalog endpoints
2. `5c67cb851752` — plan unified model catalog endpoints
3. `581cb4ed606b` — resolve unified model catalog groups
4. `0e60332f8824` — cache configured model catalogs
5. `57878d1641e6` — align catalog mixed-platform candidates
6. `fd6a60329566` — serve unified configured model catalog
7. `1040973a1761` — add bare Messages gateway aliases
8. `461ff3629bce` — use bare gateway URLs in client examples
9. `65bdad8920db` — check model catalog fixture writes
10. `d24e54ce27e4` — add Claude Fable 5.1 catalog and billing
11. `5c9f2b2bc59c` — satisfy Anthropic-native lint

## Merge and auto-merge audit

The candidate changes 747 paths relative to the merge base. Current `dev`
changes 34 paths; 15 overlap and 19 are current-dev-only. The union before this
supplemental record is 766 paths.

- 729 candidate-only paths are byte-identical (including identical deletions)
  to the reviewed candidate. Three additional candidate-only paths differ
  intentionally: two test paths received the deterministic-measurement and
  fixture-ownership repairs documented below, and the archive README records
  this supplemental wrapper. Production bytes remain unchanged.
- 19 current-dev-only paths are byte-identical to current `dev`.
- 12 overlap paths auto-merged and were reviewed against all four versions.
- The merge reported exactly three conflicts, all in the overlap set. Each was
  resolved by composing independent contracts; no mutually exclusive product
  or semantic choice was found.

The three explicit conflict decisions were:

1. `backend/internal/handler/gateway_models_test.go`: retain candidate
   `ListModelAvailabilityCandidates` behavior and error propagation while
   retaining current-dev per-group catalog/error fixtures. When a catalog map
   is provided the stub returns its copy; otherwise it delegates to candidate
   schedulable-group behavior. Both unified-catalog and candidate Codex/default
   model/ETag test families remain.
2. `backend/internal/server/routes/gateway.go`: retain the current-dev shared
   `messagesHandler` for prefixed and bare Messages aliases and the candidate
   platform-aware `codexModelsHandler`. In `modelsHandler`, an auto-bound key
   reaches the local aggregate catalog first; only a non-auto request with
   `client_version` dispatches the candidate Codex manifest. Static platform
   behavior therefore survives without weakening unified display semantics.
3. `backend/internal/server/routes/gateway_codex_models_test.go`: use the
   unified server-middleware import and retain both the current-dev unified
   route integration test and the candidate platform-dispatch test.

## Four-way audit of all 34 current-dev paths

`B`, `L`, `U`, and `F` mean merge base, current-dev parent, reviewed candidate,
and final staged wrapper tree. Hashes are Git blob prefixes; `—` means absent.
`local-exact` means `F=L`; `candidate-exact` means `F=U`; `union` means a
reviewed semantic composition.

| # | Path | B | L | U | F | Decision and preserved contract |
|---:|---|---|---|---|---|---|
| 1 | `backend/internal/handler/endpoint.go` | `c56002ed` | `5278c9a4` | `65b931bd` | `1de1e810` | union — retain current GET-model-catalog marker plus candidate endpoint/provider/model observability |
| 2 | `backend/internal/handler/endpoint_provider_test.go` | `c9536f39` | `7a51b039` | `c9536f39` | `7a51b039` | local-exact — current provider/path and catalog-marker regressions |
| 3 | `backend/internal/handler/gateway_handler.go` | `964119b7` | `11de7c35` | `6808a65e` | `8e60ab2e` | union — current group resolver and unified catalog branch plus candidate capture, side effects, Codex/static catalogs |
| 4 | `backend/internal/handler/gateway_models_test.go` | `0ae257bf` | `0151ee99` | `f0e7df21` | `5cf2fd9c` | union/conflict — composed repository stub and both complete test families |
| 5 | `backend/internal/handler/gateway_unified_models.go` | — | `ea40fa63` | — | `ea40fa63` | local-exact — deterministic permission-aware aggregation |
| 6 | `backend/internal/handler/setting_handler_public_test.go` | `0c880b48` | `ec4e5a59` | `0c880b48` | `ec4e5a59` | local-exact — public catalog response contract |
| 7 | `backend/internal/pkg/ctxkey/ctxkey.go` | `4f6ad45f` | `f98159f0` | `4f6ad45f` | `f98159f0` | local-exact — ingress provider/model and model-catalog context keys |
| 8 | `backend/internal/repository/gateway_model_catalog_cache.go` | — | `38382cc6` | — | `38382cc6` | local-exact — versioned ten-minute catalog cache, including empty catalogs |
| 9 | `backend/internal/repository/gateway_model_catalog_cache_test.go` | — | `2c4d9f43` | — | `2c4d9f43` | local-exact — cache key/TTL/malformed-data coverage |
| 10 | `backend/internal/server/routes/gateway.go` | `3a6552e9` | `192bbe66` | `8d9337c7` | `19389497` | union/conflict — bare Messages, unified auto catalog, and static Codex dispatch with explicit precedence |
| 11 | `backend/internal/server/routes/gateway_codex_models_test.go` | `04a8b8fa` | `1dd2e46c` | `d6279bac` | `7b6a97b0` | union/conflict — unified route and candidate dispatcher integration tests |
| 12 | `backend/internal/server/routes/gateway_test.go` | `894cc40e` | `19b8ef8d` | `894cc40e` | `19b8ef8d` | local-exact — bare Messages alias registration |
| 13 | `backend/internal/service/api_key_service.go` | `e7debcab` | `a25a232b` | `d65d7801` | `5b3a36bd` | union — display-only Anthropic/OpenAI catalog group resolution atop candidate canonical ACL/default routing |
| 14 | `backend/internal/service/api_key_service_auto_routing_test.go` | `d40c4915` | `a0d67e60` | `d40c4915` | `a0d67e60` | local-exact — POST ingress/model routing and catalog-only fallback separation |
| 15 | `backend/internal/service/api_key_service_provider_routing_test.go` | `38146f27` | `aca23d29` | `2e928e62` | `487f3835` | union — candidate ACL/provider tests plus current multi-group display tests |
| 16 | `backend/internal/service/billing_service.go` | `5275c8cf` | `26b7c948` | `418db916` | `10e34f2a` | union — Fable 5/5.1 fallback and match ordering plus candidate site-pricing/billing changes |
| 17 | `backend/internal/service/claude_fable51_billing_test.go` | — | `1134452c` | — | `1134452c` | local-exact — Fable 5.1 rate and precedence regression |
| 18 | `backend/internal/service/gateway_service.go` | `e5441262` | `32556c5d` | `b6c92342` | `633598d0` | union — optional catalog-cache wiring plus candidate service/failover/capture dependencies |
| 19 | `backend/internal/service/model_catalog.go` | — | `9821f49c` | — | `9821f49c` | local-exact — catalog aggregation/model normalization |
| 20 | `backend/internal/service/model_catalog_test.go` | — | `07a12e36` | — | `07a12e36` | local-exact — deterministic aggregation, cache, and error behavior |
| 21 | `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go` | `eb70323c` | `f7a81a1d` | `fbd32892` | `fbd32892` | candidate-exact — candidate native transport/observability already contains the current equivalent De Morgan lint form |
| 22 | `backend/internal/service/openai_gateway_responses_anthropic_native.go` | `0a43291d` | `dace517e` | `cc58abe0` | `cc58abe0` | candidate-exact — same lint preservation while retaining candidate Responses behavior |
| 23 | `backend/internal/service/public_model_catalog.go` | `4ff75f75` | `4c476111` | `4ff75f75` | `4c476111` | local-exact — Fable/public model metadata |
| 24 | `backend/internal/service/public_model_catalog_test.go` | `da4fcbbb` | `e56f775a` | `da4fcbbb` | `e56f775a` | local-exact — public catalog regressions |
| 25 | `backend/internal/service/web_chat_catalog_dynamic_test.go` | `f2b4a3be` | `9bc20921` | `f2b4a3be` | `9bc20921` | local-exact — dynamic union, provider dedup, cache, Fable enrichment |
| 26 | `backend/resources/model-pricing/model_prices_and_context_window.json` | `7dc445e3` | `6f5993ed` | `7fea8085` | `a8fdaf55` | union — candidate site catalog plus exact Fable 5 and 5.1 entries; valid JSON |
| 27 | `docs/superpowers/plans/2026-09-03-unified-model-catalog-root-endpoints.md` | — | `78d74087` | — | `78d74087` | local-exact — approved implementation plan |
| 28 | `docs/superpowers/specs/2026-09-03-unified-model-catalog-root-endpoints-design.md` | — | `37120cb9` | — | `37120cb9` | local-exact — approved display-only design |
| 29 | `frontend/src/components/keys/UseKeyModal.vue` | `4a277d3d` | `b3ef9949` | `64d5cdda` | `0db2e499` | union — candidate model-aware UI/Codex metadata plus current bare gateway examples/Fable data |
| 30 | `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts` | `32930ba6` | `7bec43a0` | `dcfade5e` | `642dbba3` | union — both candidate UI and current bare/Fable/config regressions |
| 31 | `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts` | `7a6b22a1` | `312ad120` | `7a6b22a1` | `312ad120` | local-exact — bare root endpoints while retaining platform-specific `/v1beta` cases |
| 32 | `frontend/src/components/keys/clientConfigFiles.ts` | `e1d6b2a9` | `e025b263` | `1aeb9ee7` | `35a8a2c0` | union — current bare endpoint transforms plus candidate dynamic model/reasoning/catalog configuration |
| 33 | `frontend/src/utils/__tests__/ccswitchImport.spec.ts` | `11833ebd` | `4aba6c2b` | `11833ebd` | `4aba6c2b` | local-exact — bare import/deeplink behavior |
| 34 | `frontend/src/utils/ccswitchImport.ts` | `d7858eab` | `a4847924` | `d7858eab` | `a4847924` | local-exact — bare imported gateway base URL |

The 13 union paths were read at the changed-line and consumer/call-chain level.
The two candidate-exact overlaps were checked to include the local lint-equivalent
condition. No generated artifact required a deviation among the 19 local-exact
non-overlaps.

## Preserved behavior and invariants

The current-dev unified catalog is permission-aware, deterministically combines
the effective Anthropic and OpenAI groups, caches configured results for ten
minutes (including valid empty results), and serves root model endpoints. Its
catalog resolution remains display-only: POST routing and scheduler selection
continue through the candidate's ingress-aware provider/default-group path.
Bare Messages aliases and bare client examples survive, as do Claude Fable 5.1
catalog/billing data and the Anthropic-native lint correction.

The reviewed candidate still owns KIRO direct/relay/mixed scheduling, profile
ARN and machine-ID behavior at reference `6ba76ea105e065a5aa8dd2b8d2957528ed58935b`;
capture/spool, unified-key behavior, site-controlled pricing, durable billing
deduplication, local finalization, provider compatibility, bounded failover,
WebSocket/429 fixes, public-group ACL, Codex catalog refresh, and usage
observability are preserved.

`openai-default` and `anthropic-default` remain active, standard,
non-exclusive groups with multiplier `1.0`. Public-group restriction remains
disabled by default. Migration numbering retains local 229/230, then:

- 231 adds `native_compaction_v2 BOOLEAN NOT NULL DEFAULT FALSE` without index;
- 232 adds nullable `requested_reasoning_effort VARCHAR(20)` without default,
  backfill, or index;
- 233 adds `restrict_public_groups BOOLEAN NOT NULL DEFAULT FALSE` and does not
  rewrite membership.

The exclusions remain unchanged: no `.s2plugin` runtime/product, Composite
platform, Grok Voice/TTS/STT/Realtime/custom voices/audio billing, independent
`/x_search`, or upstream billing-rate writeback. Only durable database billing
deduplication has the stronger identity/fingerprint guarantee. Process-local
callbacks, logs, and capture are not represented as globally exactly-once.

## Focused verification

The first accidental package-wide Go baseline retry overlapped an already
running identical baseline command because its yielded session identifier was
not captured. Both commands exited zero but are excluded from evidence. A
subsequent exclusive current-dev baseline passed handler/routes/service catalog,
routing, aliases and Fable selectors from 14:09:53 to 14:09:59. The frozen
frontend baseline passed 3 files / 59 tests from 14:10:58 to 14:11:01. The first
frontend attempt exited 254 solely because `node_modules` was not installed in
the new worktree; an offline, frozen-lockfile install reused 972 packages and
downloaded zero.

After conflict resolution and before staging those three files, all focused
commands below passed sequentially on 2026-09-04 (+08:00):

| # | Surface | Start | End | Result |
|---:|---|---|---|---|
| 1 | unified handler/routes/service/repository selectors | 14:22:33 | 14:23:48 | GREEN; four packages |
| 2 | ACL/default/simple-mode unit selectors | 14:23:48 | 14:23:53 | GREEN |
| 3 | simple-mode defaults and 230→233 migration integration | 14:23:53 | 14:24:24 | GREEN; repository `10.764s` |
| 4 | Fable 5.1 billing selectors | 14:24:24 | 14:24:28 | GREEN |
| 5 | KIRO service/repository/package selectors | 14:24:28 | 14:24:37 | GREEN; KIRO package `1.330s` |
| 6 | usage billing/dedup unit selectors | 14:24:37 | 14:24:42 | GREEN |
| 7 | durable billing concurrent retry/rollback/fingerprint integration | 14:24:42 | 14:24:51 | GREEN; repository `5.448s` |
| 8 | provider compatibility and bounded-failover selectors | 14:24:51 | 14:25:43 | GREEN; handler `38.838s`, service `47.872s` |
| 9 | three touched frontend specs | 14:25:43 | 14:25:46 | GREEN; 3 files / 60 tests |

No focused failure exposed a combined-path defect, so no new production or test
correction and no new TDD cycle were required.

## Full-race test-only repair

The first complete staged attempt was sequential and stopped at its first
failure as designed:

| Evidence | Start | End | Exit | Result |
|---:|---|---|---:|---|
| 1 `make check-generate` | 14:31:09 | 14:31:23 | 0 | generated tree unchanged |
| 2 full backend unit | 14:31:23 | 14:35:23 | 0 | handler `104.326s`; service `222.308s` |
| 3 full backend integration | 14:35:23 | 14:38:35 | 0 | handler `39.613s`; repository `30.183s`; service `150.272s` |
| 4 required race packages | 14:38:35 | 14:48:56 | 1 | handler/repository passed; service failed at one allocation guard after `460.905s` |

Evidence 5–17 did not run in that attempt. The exact failure was
`TestSanitizeOpenAIResponsesToolParameterTypes_LargeHitSetDoesNotAllocatePerHit`
at `openai_responses_tool_schema_test.go:597`: process-wide
`testing.AllocsPerRun` observed 521 allocations against the unchanged `<500`
guard. An isolated full-service race reproduction matched (`457.420s`, exit 1)
and contained no `WARNING: DATA RACE`. The test and sanitizer implementation
were byte-identical to the reviewed candidate before this repair.

Root-cause evidence separated the real algorithm from suite contamination:
the exact focused test passed 5/5 under race and 5/5 normally, while the direct
race benchmark measured 18 allocations/op for 2,000 schema hits. The full
service process had background goroutines left by unrelated tests, and
`AllocsPerRun` reads process-level memory statistics. The observed 521 was
therefore not per-hit production growth; it was the nondeterminism that the
existing test comment intended, but did not successfully avoid.

The single test-only fix in
`backend/internal/service/openai_responses_tool_schema_test.go` runs the same
real 2,000-hit sanitizer and the same `<500` assertion in an isolated copy of
the current test binary. No threshold was raised or removed and no production
file changed. A genuine per-hit implementation (at least 2,000 allocations)
still fails the isolated guard. This follows the standard self-test subprocess
pattern used by Go tests when process state itself is the source of noise.

The first subprocess implementation used `os.Args[0]`, matching the usual Go
self-test pattern, and produced the following runtime GREEN evidence on
2026-09-04 (+08:00):

| Check | Start | End | Exit | Result |
|---|---|---|---:|---|
| targeted race `-count=10` | 15:02:46 | 15:04:19 | 0 | 10/10; package `14.585s` after rebuild |
| targeted non-race `-count=10` | 15:04:19 | 15:04:53 | 0 | 10/10; package `0.447s` |
| full `./internal/service` race | 15:05:04 | 15:12:50 | 0 | GREEN; `459.996s` |
| required handler/service/repository race | 15:13:02 | 15:13:04 | 0 | GREEN; valid race-cache reuse after the full package runs |

The next exact-stage parallel attempt reached command 5 and exposed a
test-harness lint defect before commands 6 and 15–17 ran. Command 14 passed 325
files / 2,502 tests from 15:24:15 to 15:25:32, but that result and every other
result from the attempt were invalidated by the correction below. The pinned
linter's sole diagnostic was G702 at the `exec.Command(os.Args[0], ...)` call:
it treats the process argument as tainted command input. This was neither a
production defect nor a combined-path semantic defect.

The smallest non-suppressing correction obtains the same running test binary
through `os.Executable()` instead. This removes caller-controlled input from
the command boundary without changing the probe, sanitizer, fixture, assertion,
or threshold. Focused pinned service lint then reported `0 issues` from
15:29:57 to 15:31:23. The complete post-correction GREEN sequence was:

| Check | Start | End | Exit | Result |
|---|---|---|---:|---|
| targeted race `-count=10` | 15:31:33 | 15:32:39 | 0 | 10/10; service `14.474s` |
| targeted non-race `-count=10` | 15:32:45 | 15:32:49 | 0 | 10/10; service `0.363s` |
| full `./internal/service` race | 15:33:19 | 15:37:48 | 0 | GREEN; service `263.865s` |
| required handler/service/repository race | 15:38:14 | 15:43:29 | 0 | GREEN; handler `96.775s`, service `268.275s`, repository `45.245s` |

## Unit-tagged race fixture repair

The next final attempt locked staged tree
`6325f3611af62e40e706758c0c6e63a2782fe180`. Commands 1, 2, 3, 7–13
passed, then exact command 4 ran from 15:56:16 to 16:05:12 and failed in
service after `459.170s`. The first of nine race reports was in
`TestIsUpstreamModelRestrictedByChannel_CompactMappingMatchesForwardPath`:
two parallel table subtests shared one `*Account`, and concurrent
`GetModelMapping` calls read and wrote that fixture's lazy cache fields and
derived map. Later unrelated tests marked `race detected during execution`
were process contamination after the first report. No remaining command in
that attempt ran, and all results were invalidated by the test correction.

Four-way attribution established that this was not a merge-resolution defect.
The failing function has SHA-256
`790088939f28cb8b94d288c3a8de9895bce90a1495b5de90585778bc407b6610`
in the merge base, current `dev`, and reviewed candidate. The account cache is
also common-base behavior; candidate changes in `account.go` do not add or
alter its synchronization. The candidate had already changed the adjacent
`PassthroughFlagWithRawChatFallback` test to construct one account per parallel
subtest, but the same correction was missing from this older sibling test.

A production ownership audit found no cross-request in-memory `*Account`
cache. Repository list methods return per-call `[]Account` values; Redis
scheduler snapshot reads decode new accounts per call and the snapshot service
dereferences them to per-call values; single-account reads also decode/query a
new object. Selection code takes pointers only into its request-local slice.
The WebSocket connection pool retains an account pointer for immutable
connection-policy fields but does not call `GetModelMapping`. Every production
mapping call site operates on a request/query-local account, and no call site
fans one pointer out to concurrent mapping operations. The test fixture's
sharing therefore did not model the production ownership contract.

Targeted RED reproduced deterministically from 16:06:25 to 16:06:31: ten race
runs emitted 18 race reports and exited 1. The smallest test-only fix replaces
the shared fixture with a factory and creates one otherwise identical account
inside each parallel subtest. Both branches, production calls, and assertions
are unchanged. No production file changed. Post-fix evidence was:

| Check | Start | End | Exit | Result |
|---|---|---|---:|---|
| exact failing test, race `-count=20` | 16:10:43 | 16:12:04 | 0 | 20/20; service `1.266s` |
| exact failing test, non-race `-count=20` | 16:12:10 | 16:12:44 | 0 | 20/20; service `0.035s` |
| pinned service lint | 16:12:59 | 16:14:09 | 0 | `0 issues` |
| all channel-restriction tests, race `-count=10` | 16:14:20 | 16:14:27 | 0 | GREEN; service `1.197s` |

## Full resource-isolated verification

All product/test results from the failed attempts above were invalidated by the
test changes. Final verification uses evidence IDs 1–17 from the task brief,
not a mandatory serial order. Command 1 is an exclusive generation barrier.
The exact staged product tree is recorded before it runs. The resource map is:

| Phase | Evidence IDs | Isolation rule |
|---|---|---|
| generation barrier | 1 | exclusive; may inspect generated paths |
| shell/deploy wave A | 7, 8, 9 | maximum three; read-only syntax/Compose checks, with command 8 assigned a unique temporary fake-container state |
| shell/frontend wave B | 10, 11, 12 | maximum three; read-only runtime/Caddy checks and frontend ESLint with no shared output |
| unit/type wave C | 2, 13 | maximum two; Go unit cache and frontend no-emit typecheck are separate resources |
| integration | 3 | exclusive shared PostgreSQL/Redis integration resources |
| race | 4 | exclusive CPU/process-sensitive race suite |
| lint/test wave D | 5, 14 | maximum two; Go lint cache and Vitest resources are separate |
| build/critical wave E | 6, 16 | maximum two; backend binary output and frontend test resources are separate |
| remaining frontend gates | 17, then 15 | serialized to avoid concurrent Vitest/Vite-cache or `dist` writes |

Every command retains its own raw log, start/end timestamp, exit status, and
isolation mapping. No code or index change is allowed during a parallel wave.
After the tracked table is filled, the documentation-only update is restaged
and the tree/document gates are rechecked; under the task policy it does not
invalidate unrelated product tests.

<!-- MATRIX_RESULTS_START -->
The final product/test stage was exactly
`f1416f148459d169f91bc70e7db50809a5c9b4c6`. Command 1 left that tree
unchanged, and no source or index mutation occurred during or between the
remaining product waves. All timestamps are 2026-09-04 (+08:00).

| ID | Start | End | Exit | Isolation | Result / raw log |
|---:|---|---|---:|---|---|
| 1 | 16:16:20 | 16:16:34 | 0 | exclusive generation barrier | generated tree unchanged; `task13-devdrift-final3-01.log` |
| 2 | 16:18:19 | 16:22:07 | 0 | wave C, Go unit cache only | full unit GREEN; service `222.447s`; `task13-devdrift-final3-02.log` |
| 3 | 16:22:35 | 16:22:40 | 0 | exclusive PostgreSQL/Redis integration resources | full integration GREEN from valid exact-tree cache; `task13-devdrift-final3-03.log` |
| 4 | 16:23:17 | 16:31:04 | 0 | exclusive CPU/process-sensitive race suite | service fresh `460.939s`, handler/repository cached, zero race warnings; `task13-devdrift-final3-04.log` |
| 5 | 16:31:45 | 16:32:05 | 0 | wave D, Go lint cache | pinned full lint `0 issues`; `task13-devdrift-final3-05.log` |
| 6 | 16:33:11 | 16:33:34 | 0 | wave E, backend `bin/server` output | backend build GREEN; `task13-devdrift-final3-06.log` |
| 7 | 16:16:53 | 16:16:53 | 0 | wave A, read-only shell syntax | GREEN; `task13-devdrift-final3-07.log` |
| 8 | 16:16:53 | 16:16:55 | 0 | wave A, unique `mktemp` fake-container state | lifecycle GREEN; `task13-devdrift-final3-08.log` |
| 9 | 16:16:53 | 16:16:53 | 0 | wave A, read-only Compose files | security GREEN; `task13-devdrift-final3-09.log` |
| 10 | 16:17:20 | 16:17:20 | 0 | wave B, read-only runtime files | resources GREEN; `task13-devdrift-final3-10.log` |
| 11 | 16:17:20 | 16:17:20 | 0 | wave B, read-only Caddyfile | cache/SSE policy GREEN; `task13-devdrift-final3-11.log` |
| 12 | 16:17:20 | 16:17:43 | 0 | wave B, frontend ESLint | GREEN; `task13-devdrift-final3-12.log` |
| 13 | 16:18:19 | 16:18:50 | 0 | wave C, frontend no-emit typecheck | GREEN; `task13-devdrift-final3-13.log` |
| 14 | 16:31:45 | 16:32:45 | 0 | wave D, full Vitest | 325 files / 2,502 tests GREEN; `task13-devdrift-final3-14.log` |
| 15 | 16:34:12 | 16:35:19 | 0 | exclusive frontend `dist` output | 1,086 modules, build GREEN; `task13-devdrift-final3-15.log` |
| 16 | 16:33:11 | 16:33:25 | 0 | wave E, critical Vitest | 33 files / 352 tests GREEN; `task13-devdrift-final3-16.log` |
| 17 | 16:33:59 | 16:34:03 | 0 | serialized web-chat Vitest | 5 files / 75 tests GREEN; `task13-devdrift-final3-17.log` |

The 17 raw logs and their individual SHA-256 digests are retained under
`task-13-devdrift-wrapper-evidence/final-matrix/` beside the task report. The
matrix-result text above is a documentation-only evidence update made after
all product commands completed; it does not change the tested product bytes.
<!-- MATRIX_RESULTS_END -->

## Publication boundary

This wrapper prepares one merge commit only. No remote was fetched or changed,
no ref was pushed, and no CI/deployment was started. Publication is limited to
non-force advancement of `dev` after the surrounding workflow's authorization
and drift checks; `main` is outside this scope.
