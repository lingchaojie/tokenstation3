# Billing and migration integration evidence

Coordinates: base `881f3202694c6bc932446931a30c27d9675178b9`, local
`a892514e488bb880987aba7bec3b102d9964fb23`, upstream
`a3eb7ef302961cba716dc78b39b93b60c467db0e`.

## Semantic decisions and coverage

- `billing_service.go`, `account_stats_pricing.go`, `model_pricing_resolver.go`:
  explicit valid effort-map entries win, including max=1; missing max for Fable
  5.1 retains 3x, other omitted levels retain 1x. Configured maps remain separate
  from defaults and are cloned at pricing boundaries. Account statistics uses its
  own map and retains local provider-long-context/custom-rule/source priority.
- Preserved local token bucket presence/provenance, explicit zero, independent
  image input/output prices, cache 5m/1h and priority buckets, per-second video
  quantities, size tiers, and missing-price fail-closed validation. Repaired an
  automatic merge that bypassed local validation and pricing policies when no
  resolver is supplied. The path calls `calculateCostInternalWithPolicy` and
  applies the reasoning multiplier only after successful validation.
- `pricing_service.go` and automatic billing catalog changes: incorporated GPT-6
  Sol/Luna, Opus 5.5 and Grok 4.7 fixed upstream catalog entries, preserving local
  GPT-5.6 prices. Opus 5.5 LiteLLM fallback explicitly records TTL bucket presence
  and 1h priority price so local fail-closed validation can consume that price.
- Channel models, validation and repository pricing CRUD adopt the map and deep
  clone it. Account-stat SQL includes both the new map and local image input
  price in INSERT/SELECT/Scan order. Local billing-source normalization and
  concrete-platform isolation remain intact.
- Reviewed conflict and automatic changes in the corresponding billing,
  reasoning, channel pricing/time pricing, account-stat and repository tests.
  Retained local zero-vs-missing tests alongside upstream new-model tests.
- Coordinator delegated account probe/OpenCode repository mock integration:
  SELECT fixtures preserve local no-rate-sync semantics and add the three new
  OpenCode fields (11 columns total). Removed the upstream rate-sync inference
  test and adapted the OpenCode helper signature; excluded Composite is tested
  as an unsupported literal rather than reinstating its platform constant.

## Migrations and rollback

- Approved filenames: `244_content_moderation_engine_meta.sql` and
  `245_channel_reasoning_effort_multipliers.sql`. Migration 245 executes after
  local 240 creates the legacy max column. Migration 244 adds nullable metadata.
- Excluded `240_affiliate_ledger_operation_id.sql` completely. Historical SQL
  verification compared all 297 preexisting SQL files byte-for-byte with the
  local parent: none changed or disappeared.
- 245 migrates explicit channel max values on first column creation only.
  Existing maps, including intentionally empty maps, survive replay. Group JSON
  migrates its explicit legacy max only when no map exists, keeps array order and
  consumes the legacy key. Account-stat maps begin empty, preserving model
  defaults at runtime.
- A code revert alone is not a data rollback. Before any deployment, preserve a
  database snapshot and copies of channel pricing, group `model_pricing`, and
  account-stat pricing. Stop configuration writes during a rollback. To preserve
  CURRENT max settings for old code, project channel
  `reasoning_effort_multipliers->>'max'` into `max_reasoning_effort_multiplier`
  (NULL when absent), and each group JSON entry's `map.max` into its legacy key.
  The integration test validates the group JSON projection. Keep the map columns
  for forward recovery. Non-max and account-stat effort overrides have no old
  representation: identify those rows before rollback and obtain an explicit
  decision to restore the predeployment snapshot or accept changed pricing.
  This report does not authorize production changes and no rollback SQL was run
  on production.

## Verification

- `go test ./migrations`: PASS.
- Historical SQL comparison: 297 files, 0 modified/missing.
- Targeted pricing/billing/account-stat/repository unit run: PASS (service
  0.263s, repository 0.431s). Broader service `Test.*(Billing|Pricing|AccountStats|TokenCost|ChannelModel|Reasoning)` run: PASS (3.101s).
- Normal-harness PostgreSQL 18.1 / Redis 8.4 targeted integration (migration245,
  group pricing roundtrip, deterministic rollup snapshot): PASS (6.146s).
  Exact Redis 8.4-alpine became available after its initial slow pull; no older
  image was substituted and no Docker daemon configuration was changed.
- Focused converter / rollup / simple-mode unit checks: PASS (0.035s).
- Full-host `go test -tags=integration ./... -count=1 -timeout=15m` exposed
  the persisted-230 upgrade fixture missing approved 244/245 names (corrected)
  and two handler miniredis bind failures from the host's narrow ephemeral range.
  The obsolete full-host process was stopped after those failures; it is not a
  passing full-suite result. Log: `/tmp/sub2api-a3eb-full-integration.log`.
- Full repository integration rerun, normal host with exact images:
  `go test -tags=integration ./internal/repository -count=1 -timeout=15m`:
  PASS, 26.889s. Log: `/tmp/sub2api-a3eb-repository-integration.log`.
  Coordinator runs all other integration packages in an isolated namespace;
  this avoids changing host sysctl and keeps Docker host ports accessible to
  the repository harness. See coordinator evidence for the other packages.
- Corrected an upstream unit fixture that declared forged/desensitized
  `inputExtra` but never assigned it. The managed-state test now applies that
  input before invoking the repository; focused rerun PASS (0.019s).
- Added behavioral regressions for aliases/default/explicit max override,
  channel-versus-account-stat parity with both cache TTLs and image buckets,
  no-resolver missing bucket rejection/explicit zero, migration ordering,
  upgrade/replay and rollback JSON representation.

## Additional integration findings

- The original local `api_key_repo.go:groupEntityToService` omitted both
  `LongContextPricingEnabled` and `ModelPricing` (confirmed in the local parent
  blob). A new focused converter test failed before repair, and the new real
  group-reasoning integration test likewise read an empty price list. Restored
  both fields, preserving explicit zero prices and decoding a fresh slice.
  The focused test and real create/update/read/billing roundtrip now pass.
  This prerequisite repair was explicitly approved by the coordinator.
- Upstream split the local rollup's single SQL statement into a watermark SELECT
  followed by a parameterized totals SELECT. A deterministic PostgreSQL test
  commits historical and current-day usage between the reads: unprotected reads
  return 6 although the valid before/after totals are 2 and 9. Production DB
  reads now use a read-only REPEATABLE READ transaction, retaining the upstream
  indexed tail query while recovering the old statement-snapshot guarantee.
  The same interleaving returns the consistent old total 2. Caller-supplied
  transactions retain their caller-owned isolation policy.
- Proxy expiry fallback/manual restore clears the generic billing probe but not
  Ollama/OpenCode display snapshots, unlike explicit proxy editing. Ollama has
  the same preexisting gap; OpenCode snapshots are display-only and no wrong
  routing/billing was demonstrated. Recorded as residual follow-up, not expanded
  into a speculative production change in this sync.

## Expanded repository and account-domain semantic audit inventory

All paths below are relative to `backend/internal/`; review includes automatic
merge changes, not only conflict markers. Production changes were read against
the local parent; associated changed test bodies were inspected for retained
invariants. Full repository integration execution covers its tagged tests.

- `repository/account_repo.go`, `account_repo_codex_display_snapshot_test.go`,
  `account_repo_integration_test.go`, `account_repo_temp_unsched_test.go`,
  `account_repo_upstream_billing_probe_update_test.go`: display snapshots remain
  scheduler-neutral; paused active OAuth accounts remain refresh candidates
  without re-enabling scheduling; credentials/proxy identity and managed-state
  row-lock merging retain local no-rate-sync behavior.
- `repository/account_repo_opencode_go_usage.go`, its `_test.go` and
  `_integration_test.go`: Go/Zen mode and official-base-url eligibility match
  service predicates; group sharing is exact API-key scoped, not platform scoped;
  ordered row locks and proxy/credential/state comparisons prevent stale writes;
  activity/debounce/minimum-fetch/backoff filtering occurs before LIMIT. The
  OpenCode CASE must precede the broader Ollama cleanup branch. Tests cover
  mixed-platform edits, forged/missing managed fields, proxy-only versus key
  changes, NULL base URLs, default port443, default Go and excluded Zen mode.
- `repository/api_key_repo.go`, `group_pricing_mapping_test.go`: restored group
  pricing and long-context hydration; fresh JSON decode retains zero pointers.
- `repository/channel_repo_pricing.go`, `channel_repo_account_stats_pricing.go`,
  `channel_repo_pricing_reasoning_test.go`, `channel_repo_pricing_time_test.go`,
  `channel_repo_account_stats_image_input_test.go`,
  `channel_reasoning_pricing_integration_test.go`: both map/legacy image columns
  preserved, null/empty map roundtrip and rollback after malformed replacement;
  account-stat configuration remains separate from customer billing.
- `repository/channel_reasoning_effort_migration_integration_test.go`,
  `upstream_sync_migration_policy_test.go`,
  `task3_upgrade_migrations_integration_test.go`: new244/245 names, historical
  parent boundary, real upgrade/reapply order, explicit/default map conversion,
  existing empty-map preservation and rollback projection.
- `repository/custom_group_usage_rollup_repo.go`,
  `usage_log_repo_group_summary_test.go`,
  `group_usage_rollup_snapshot_integration_test.go`: indexed parameterized tail,
  invalid-watermark fallback, DST/timezone boundaries, and repaired two-read
  snapshot regression described above.
- `repository/dashboard_aggregation_repo.go`,
  `dashboard_aggregation_group_usage_test.go`,
  `request_log_retention_integration_test.go`: prune exact timestamp after whole
  partition drops, identify row locations by `(tableoid,ctid)` to avoid deleting
  a retained row in another partition; transactional watermark invalidation.
- `repository/content_moderation_repo.go`, `content_moderation_repo_test.go`:
  nullable engine metadata is JSON encoded/decoded at matching INSERT/SELECT
  positions; existing request model and scoring fields unchanged.
- `repository/http_upstream.go`, `http_upstream_body_lifecycle_test.go`,
  `http_upstream_billing_lifecycle_test.go`,
  `http_upstream_http2_keepalive_test.go`, `http_upstream_http2_ping_test.go`:
  cancellation is per attempt, before waiting for an active reader; concurrent
  close releases tracked in-flight exactly once, gzip closes safely, complete
  bodies keep connections, HTTP2 sibling streams survive, and disconnected
  clients still drain final usage into billing. Local OpenAI15s/15s versus
  long-stream10s/5s PING policies and explicit H1 platform policies are retained.
- `repository/openai_referral_client.go`, `openai_referral_client_test.go`:
  existing privacy/proxy client factory, fixed endpoints, sanitized errors,
  no retries of sends or queries, and uncertain-success reporting for lost or
  malformed send responses; no provider API calls made during this audit.
- `repository/proxy_repo.go`, `proxy_expiry_renewal_integration_test.go`,
  `proxy_inactive_backup_integration_test.go`,
  `proxy_restore_probe_integration_test.go`: compare scanned expiry/status/
  fallback mode/backup ID before mutation, preserve disabled backups and first
  fallback origin; restoring a changed proxy clears generic billing probe but
  not unrelated metadata. Display-only snapshot residual is recorded above.
- `repository/redeem_code_repo.go`, `redeem_code_repo_sort_integration_test.go`,
  `redeem_reduction_lock_integration_test.go`,
  `redeem_reduction_remainder_integration_test.go`: used-at plus ID provides
  stable pagination and user isolation; integration covers transaction-locked
  renewal/concurrent reduction and partial-day preservation (service owned by
  coordinator).
- `repository/ent.go`, `simple_mode_startup_test.go`: seeding flag does not skip
  independent admin setup, and local KIRO plus other existing platforms remain;
  fixture counts adjusted to six platforms/five named-default checks.
- `repository/scheduler_cache.go`, `scheduler_cache_unit_test.go`: base RPM,
  strategy and sticky buffer survive scheduler metadata projection; tests
  reproduce red-zone and sticky-only decisions after JSON roundtrip.
- `repository/wire.go`: new referral adapter is provided through the existing
  privacy client factory, not an unproxied ad-hoc client.

Additional service-domain production/test paths reviewed:

- `service/admin_account.go`, `admin_account_upstream_billing_probe_test.go`:
  strip client-forged OpenCode fields on create/extra/bulk; ordinary edit retains
  persisted state and identity change clears managed state. Repository row locks
  remain authoritative when refresh races an edit.
- `service/account.go`: Seedance/video and image capabilities stay behind
  concrete account/platform/base-URL checks; no excluded Composite branch.
- `service/account_usage_service.go`, `account_usage_service_batch_test.go`,
  `account_usage_service_spark_shadow_test.go`: cached/failed/throttled usage
  inspection cannot erase a permanent refresh error; Spark quota constructor
  change is dependency adaptation only.
- `service/account_test_service.go`, `account_test_service_cn_adaptive.go`,
  `account_test_models_test.go`: picker projects account aliases from immutable
  raw discovery, exact beats wildcard, passthrough ignores stale mapping,
  OAuth local-image aliases check resolved targets; fresh default header maps
  and OpenCode headers retain later account-specific overrides.
- `service/opencode_go.go`, `opencode_go_test.go`, `opencode_go_usage.go`,
  `opencode_go_usage_test.go`: normalized quota URL, default catalog update;
  display-only subscription runner defaults off, fixed official usage endpoint,
  account proxy respected, no redirects, bounded body/time/concurrency,
  group singleflight, credential/proxy CAS, activity-aware refresh floor/backoff,
  sanitized failure retaining last good display data, and shutdown cancellation.
  No API-key balance, scheduling or billing state is updated by these snapshots.
- `service/model_plaza_service.go`, `model_plaza_service_test.go`: group prices
  override channel prices and configured maps are cloned; implicit Fable3x is
  intentionally represented in billing/UI defaults, not injected into stored
  configured maps. Frontend worker was informed of this contract.
- `service/upstream_models.go`, `upstream_response_model.go`: default headers
  are constructed per call and Grok4.7 response canonicalization is isolated to
  its alias family. Billing/catalog/resolver/channel paths are covered above.
