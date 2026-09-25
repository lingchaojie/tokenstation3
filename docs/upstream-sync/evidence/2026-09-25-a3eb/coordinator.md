# Cross-domain integration evidence

Coordinates: local `a892514e488bb880987aba7bec3b102d9964fb23`, merge base
`881f3202694c6bc932446931a30c27d9675178b9`, upstream
`a3eb7ef302961cba716dc78b39b93b60c467db0e`. This is implementation audit evidence,
not the independent pre-push review or a completion claim.

## Runtime and settings

- Retained the local monolithic setting service/handler and capture extensions;
  transplanted changed behavior from upstream's deleted split files. Claude
  version resolution is manual override, synchronized version, then static/env
  default, with finite cache TTL, singleflight and detached bounded DB lookup.
  Saving settings invalidates both Claude and Codex version caches. Synced values
  are exclusively background-owned and cannot be overwritten by panel payloads.
- Nullable OAuth scheduling override distinguishes omitted, null, zero and
  invalid finite values; missing historical setting remains 1. The local
  pre-write validation remains ahead of repository writes/cache mutations.
  Handler roundtrip and invalid-write tests cover these contracts.
- Identity creation preserves local poisoned/sentinel fingerprint protection
  and ignores caller Stainless metadata when CLI identity is invalid. Runtime
  version is now the floor, including existing cached fingerprints; account
  client IDs remain persistent. Wire connects the version resolver and starts/
  stops the new version and OpenCode refresh services, preserving capture,
  KIRO cooldown and all existing lifecycle services.

## Billing entry points

- `gateway_usage_billing.go` and `openai_gateway_usage.go` retain local pricing
  precedence, fail-closed missing bucket checks, explicit zero prices, actual
  subscription result/cache-group ownership, balance fallback, quota platform
  and duration-based video quantities. New effort maps are consumed only after
  final forwarding metadata is available. Excluded audio/search billing is not
  resurrected.
- Simple-mode window enforcement is default-off. Opt-in commands increment only
  API-key spending windows transactionally/deduplicated; no balance,
  subscription, platform or lifetime key/account charges. Preflight reads the
  DB source of truth; unavailable billing fails closed. Cache invalidation is
  best effort and is not the enforcement mechanism.

## New endpoints and shared protocol

- Seedance reuses authenticated media task ownership (user, key, group, provider),
  slots and pending billing state. IDs are provider-prefixed. Completion usage
  uses actual completion tokens, never Grok duration pricing. Stable task IDs
  deduplicate repeated polls; failed local pricing/recording releases the claim.
  Creates are not retried after ambiguous upstream errors. Existing proxy and
  base-URL validation remain in the service path.
- Fixed the automatic-merge omission in unified-key ingress detection: native
  task prefixes now select the OpenAI effective group instead of Anthropic.
  A new test failed before the change and passes afterward for all four prefixes
  and create/status shapes. No Composite resolver is introduced.
- Retained compatible image generation/edit tests by adapting Composite-only
  fixtures to real OpenAI groups and account model mapping; unknown non-image
  aliases remain rejected by the existing local validation policy.
- Handler failover exhaustion retains terminal capture and upstream Retry-After;
  metadata DTOs continue redacting managed snapshots and credentials. Channel
  DTOs accept/return effort maps with local image/TTL prices intact.

## Moderation, proxy, quota and redemption

- TypeSafe is an independently selected engine; legacy settings default to
  OpenAI. Profiles keep distinct keys, proxy, thresholds, timeouts, retry state
  and masked key-status hashes. Config updates validate both profiles before
  persistence; active snapshots select the requested profile. TypeSafe uses
  the existing proxy client, bounded response size and strict answer validation;
  errors omit provider bodies/secrets. Image-only input is explicitly marked
  unaudited, not reported as a successful image audit.
- Keyword checks still inspect client reminder blocks before semantic filtering.
  Existing local policy/cyber exclusion and async record flow remain; nullable
  engine metadata is persisted by migration 244. No live moderation test enabled.
- CN coding-plan quota 403 uses the existing window reset cooldown with
  persistence fallback, not permanent account disable. Concurrency 403 stays
  separate. Cloudflare 1010 does not count as an account strike; compatible
  API-key model-not-found 401 is model cooldown, not credential poisoning.
- Proxy fallback may traverse disabled nodes but cannot select one. Grok admin
  quota diagnostics can refresh while scheduling is paused and retain the
  configured proxy/auth path. No provider calls made during integration.
- Subscription reduction locks/rereads within the existing transaction and
  preserves partial-day remainder. Paginated redemption history is user-scoped
  and bounded; old clients without pagination retain the original array shape.
- Explicitly excluded all offline-withdraw service/repo/handler/route/test and
  SQL additions after confirming the affected affiliate paths only changed for
  that upstream feature. Local affiliate, reward and payment behavior remains.

## Common packages and verification state

- Reviewed automatic changes to Antigravity attribution/schema normalization,
  Claude runtime/effort helpers, OpenAI/XAI catalogs, TypeSafe client, config,
  generated wiring, server routes and shared handler DTOs. New model catalog
  values reflect the pinned upstream code, not a separate official price audit.
- Ent regeneration has no schema delta; Wire is regenerated from composed
  providers. Go dependencies keep locally newer requirements while accepting
  upstream security-library versions. Excluded plugin runtime/proto dependencies
  and providers are not restored.
- Frontend, gateway/protocol, repository/billing and deployment/retention domain
  coverage is documented in adjacent evidence files. Final full checks and a
  fresh independent review remain required before push.
- Initial concurrent unit run hit the host's unusually narrow ephemeral TCP
  range (60700–61000) and a capture readiness timeout under compile load.
  Target capture test passed 50 runs at the untouched local parent and 10 at
  the merge; no production behavior was changed to mask the timeout. Full unit
  reruns use an isolated user/network namespace with loopback and a wider
  namespace-only range; host sysctls are unchanged.
- User namespaces must drop all capabilities before executing tests so that
  read-only-file/fsync error-path tests retain ordinary-user permission behavior.
  Docker-dependent packages run sequentially on the host; the remaining
  integration packages run with isolated loopback. An earlier isolated run
  could not reach host-published container ports and is not acceptance evidence.
- `TestValidateCreateParams_CheckModeMatrix` failed identically at DEV_BASE and
  the candidate without external DNS: endpoint validation masked the expected
  missing-key/model errors. Its two relevant fixtures now use a public IP
  literal; no network request is made and production validation is unchanged.
  Baseline log: `/tmp/sub2api-baseline-monitor-dns.log`; the corrected focused
  test passes in the same network namespace (0.022s).

## Final local verification

- Build and generation consistency: `make build && make check-generate`, exit 0.
- Normal suite: `go test -timeout=20m ./...`, exit 0;
  `/tmp/sub2api-a3eb-normal-final.log`.
- Unit suite: `go test -timeout=20m -tags=unit ./...`, exit 0;
  `/tmp/sub2api-a3eb-unit-final-green.log`.
- Integration suite: all packages covered by the isolated non-Docker partition
  and sequential host Docker partition, both exit 0. Logs:
  `/tmp/sub2api-a3eb-integration-isolated-final.log` and
  `/tmp/sub2api-a3eb-integration-host-final.log`. Final host repository 29.919s,
  service 170.630s. Exact upstream container versions retained.
- Final lint after test-fixture change: exit 0, `0 issues.`;
  `/tmp/sub2api-a3eb-lint-verified.log`.
- Focused core race exit 0: `/tmp/sub2api-a3eb-core-race.log`.
- govulncheck exit 0, no reachable vulnerabilities:
  `/tmp/sub2api-a3eb-govuln.log`. Frontend production audit exceptions validated.
- Frontend all 407 files / 3162 tests, lint/typecheck/build, deployment and
  release-helper results are recorded in the adjacent domain evidence files.
- Full independent review, remote drift checks, push and exact-SHA CI remain
  publication gates; local test results are not a push authorization by themselves.

## Verification after first independent review fixes

- UsersView filtered-membership and OpenCode managed-state fixes are documented
  in frontend.md and billing.md with failing-before/passing-after regressions.
- Final normal/unit suites both exit 0: `/tmp/sub2api-review-normal-final.log`
  and `/tmp/sub2api-review-unit-final.log`.
- Final integration partitions both exit 0:
  `/tmp/sub2api-review-integration-isolated-final.log` and
  `/tmp/sub2api-review-integration-host-final.log`; host repository 25.850s,
  service 168.574s. PostgreSQL 18.1 and Redis 8.4 retained.
- Build and lint after production fixes both exit 0:
  `/tmp/sub2api-review-build-final.log`, `/tmp/sub2api-review-lint-final.log`.
  Wiring/schema/generator inputs did not change after the generation check.
- OpenCode race suite exits 0 (1.075s):
  `/tmp/sub2api-review-opencode-race.log`; frontend full suite now 3165 tests.
- User explicitly approved keeping Seedance's independent-video token-profit
  exemption. Balance/quota/pricing validation remains enabled; no production
  gate code was changed to resolve that policy question.
- Gateway reviewer reran profit-context, Seedance lifecycle/token billing,
  forwarded-effort pricing, Lite capture, referral, scheduling and SimpleMode
  regressions plus full apicompat/antigravity unit suites successfully. Final
  reviewer approval must still be tied to the corrected candidate coordinate.
