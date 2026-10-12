# Backend platform integration evidence (2026-10-12)

Coordinates: DEV_BASE `591a03c550491b5d9f2e5c04bbc53dc1ae3177f3`; merge ours `c517f84cd` (Fable repair); ancestor `b8dece9000c68815a5b867ca5a1e6f236e173905`; upstream `9591136d45cef26453348ed9532a7ebbce7cf99c`.

Status: assigned production files and conflict resolutions are stable for generation. No staging, commits, pushes, generated code writes or production access by this agent. Runtime tests pending root's exclusive generation gate; gofmt and owned-path whitespace/conflict checks passed. This is author integration evidence, not independent push review.

## Decisions and call chains

- User-approved new Command Code/Cline and registry integration preserves all12 actual backend platform IDs, including KIRO direct/relay. Kilo/Cursor/Ollama remain existing provider aliases, presets or client/host behavior under registered platforms; no unsolicited new concrete IDs. Registry display order follows existing frontend: anthropic/openai/gemini/antigravity/kiro/grok/kimi/zhipu/deepseek/minimax/opencode_go/typesafe/command_code/cline. Scheduler retains anthropic/gemini/openai prefix before other display IDs.
- Group binding -> catalog predicates -> admin creation/update -> stored platform uses the same14 known IDs. Existing unconstrained account/group service and repository APIs were deliberately not given new rejection policies: user approval replaces the quota DB CHECK and preserves account semantics.
- Quota admin/settings whitelist -> Ent validation and repository pre-write validation -> BulkInsertInitial/UpsertForUser/IncrementUsageWithReset/BatchSnapshotUsage. Explicit allNULL unlimited records remain active and preserve accumulated usage/windows; they are never filtered out as missing configuration. Invalid snapshot batches are validated completely before any SQL subbatch write.
- Platform/catalog -> account profile -> endpoint/routing resolver -> gateway protocol conversion. Administrator pinned protocols/rules retain precedence; protocol capability discovery neither grants API admission nor rewrites saved model mappings. Root owns transport/entry changes and existing requested-model whitelist checks.
- Command Code credits/windows and Cline pass/credits probes use official-host guards and existing URL/proxy policy; request failures remain recoverable/return errors, auth failures do not persist a new successful state. CN periodic probes dispatch platform-specific behavior without restoring any manual-offline/price-rewrite paths.
- Account stats exemption is an optional bool from root's actual forwarded account/model check; default provider-cost policy stays unchanged. Explicit custom rules/intervals precede model pricing, and the customer-price option keeps its position. Fable max default1 regression from ours retained.
- Composite route schema/services/tests remain deleted; imported Composite request types, cross-platform fallbacks, registry precedence and tests removed. Plugin, Grok audio/x_search, manual offline withdrawals and automatic upstream price rewriting are not restored.
- New248 migration drops only `user_platform_quotas_platform_check`. Historical local242..247 SQL unchanged. Channel monitor provider CHECK constraints describe implemented probe ability and remain. Root updates migration-package expectations and owns generation.
- Automatic scheduler merge fixed: bool-switch retains KIRO-specific mixed cleanup and new catalog generic branch excludes both Antigravity/KIRO. Imported outputTPS excludes local video rows as well as images because it measures text-token output.

## Cross-owner contracts

1. `BuildPlatformCatalog` JSON contains only `platforms`, including KIRO `{id:kiro,display_name:Kiro,gateway:anthropic,cn_provider:false,litellm_provider:anthropic}`. No Composite precedence. Frontend owner preserves preset aliases/local quota semantics and updates builtin JSON; JSONEq test checks the exact profile contract.
2. Root derives `IsOpenAICompatiblePlatform`/normalization from domain gateway assignment and wires generalized resolver at gateway entry points; KIRO remains shared Anthropic/direct-native dispatch. General `isConcreteRequestPlatform` now lives in domain_constants.
3. `applyAccountStatsCost(..., pricingAt time.Time, disableModelLongContextPricing ...bool)` defaults false; root passes `modelLongContextExempt`. Internal usage flag skips priority2 long-context model price and passes `CostInput.DisableModelLongContextPricing` for default provider price.
4. Root migrator tests reference248 only and root regenerates Ent/Wire after this agent declared schemas/interfaces stable.

## Per-file coverage

All upstream assigned paths are included below. Production review compares ancestor/ours/upstream/final and follows changed callers/callees described above; tests/fixtures verify their associated behavior. Deleted Composite paths were traced to imported references and cross-owner gateway exclusions. Four-way production source inputs remain in ignored scratch `platform-audit-sources/`.

| Path | Resolution and semantic coverage |
| --- | --- |
| `backend/ent/schema/composite_model_route.go` | Deleted: prior local Composite exclusion retained; no Composite table in 248. |
| `backend/ent/schema/user_platform_quota.go` | Ent validator now derives concrete IDs from domain; KIRO and new IDs supported. |
| `backend/internal/domain/constants.go` | Retained KIRO and default mapping extensions; added Command Code/Cline; no Composite constant. |
| `backend/internal/domain/platforms.go` | Concrete display registry includes original 12 plus 2; KIRO uses Anthropic gateway; no Composite fallback API. |
| `backend/internal/domain/platforms_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/handler/admin/account_handler.go` | Antigravity raw request-side mapping helper is distinct from KIRO mapping helper; probe capability expands via provider profiles. |
| `backend/internal/handler/admin/account_handler_available_models_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/handler/admin/channel_handler.go` | LiteLLM lookup derives provider metadata from domain; KIRO retains anthropic mapping; platforms with no price provider reject pricing sync only. |
| `backend/internal/handler/admin/group_handler.go` | Existing group binding whitelist derives concrete catalog; Composite route request types remain absent. |
| `backend/internal/handler/admin/group_handler_platform_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/handler/admin/platform_validation.go` | Group/concrete validators use domain catalog; does not tighten formerly unconstrained account APIs. |
| `backend/internal/model/error_passthrough_rule.go` | Supported platform list derives catalog; KIRO/error passthrough retained and new IDs included. |
| `backend/internal/model/error_passthrough_rule_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/repository/group_repo.go` | Moderation group cleanup stays in cascade transaction; locks group then settings; malformed/missing config does not block delete. |
| `backend/internal/repository/group_repo_integration_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/repository/ops_repo_dashboard.go` | Full-window individual text TPS quantiles apply to raw/preagg/auto; preserve timeout-only omission; local video excluded with images. |
| `backend/internal/repository/ops_repo_dashboard_integration_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/repository/ops_repo_dashboard_timeout_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/repository/platform_list_db_integration_test.go` | 248 repeat/drop/monitor checks; invalid insert/upsert/increment/snapshot rejection before effects; KIRO/new-platform allNULL usage retention. |
| `backend/internal/repository/setting_repo.go` | Keep local sql.ErrNoRows handling for SetIfAbsent; moderation settings lock live groups before setting row to match deletion lock order. |
| `backend/internal/repository/user_platform_quota_repo.go` | Validate all record/snapshot/native-increment inserts before side effects; preserve explicit allNULL rows, usage and window lifecycle; no configuredRecords filter. |
| `backend/internal/service/account.go` | Provider profile replaces static endpoint switches; KIRO untouched; Cline/Command Code predicates isolated; alias mapping remains separate from admission. |
| `backend/internal/service/account_header_override.go` | Eligible multi-protocol API-key providers derive profile; Anthropic/OpenAI/Grok types and existing safe override filters preserved. |
| `backend/internal/service/account_scheduling_threshold_eval.go` | Command Code monthly/rolling snapshots enter thresholds only when purchased credits do not bypass windows; unsupported KIRO evaluation unchanged. |
| `backend/internal/service/account_service.go` | Credential test capability derives multi-protocol profile; local provider-specific state cleanup persists. |
| `backend/internal/service/account_test_logging.go` | Correlated test logger binds before lookup; error redaction precedes truncation; no prompt or credential fields emitted. |
| `backend/internal/service/account_test_logging_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/account_test_service.go` | KIRO direct routing retains distinct branch; OpenCode/Command Code share model-routed tester; profile test fallback and Cline by-inbound tester wired. |
| `backend/internal/service/account_test_service_cn_adaptive.go` | Probe only supported native endpoints; version-aware Anthropic URL for aggregators; local Ollama bearer behavior retained. |
| `backend/internal/service/admin_account.go` | Create/duplicate/update/bulk normalize generic protocol rules; local billing probes, Codex fingerprints and KIRO headers/credit validations retained. |
| `backend/internal/service/admin_group.go` | Exclude Composite helper; same-platform copy restriction/default candidates/KIRO sticky/Grok image defaults retained. |
| `backend/internal/service/channel_monitor_checker.go` | Zhipu official/paas/coding and relay URLs choose correct probe path; fixed monitored-provider capabilities remain independent of catalog. |
| `backend/internal/service/channel_monitor_endpoint_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/channel_monitor_v2.go` | Insufficient request samples keep all health fields unknown; public snapshot retains computed health while redacting sensitive counts/config. |
| `backend/internal/service/channel_monitor_v2_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/channel_service.go` | Exclude Composite cross-platform price fallback; local requested/upstream/channel/response-model billing lookup and caches untouched. |
| `backend/internal/service/channel_service_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/cline.go` | Single native Chat Completions profile; separate credits/pass model cooldown keys; official-host probes only. |
| `backend/internal/service/cline_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/cline_usage.go` | Cline microcredit conversion and three subscription windows; persist no auth failure; clear only provable recovered wallet cooldowns. |
| `backend/internal/service/cn_provider_balance_check_service.go` | Bounded account probe concurrency/time budget; Command Code balances pause recoverably, Cline wallets do not pause whole account. |
| `backend/internal/service/cn_provider_balance_service.go` | Reuse balance snapshot mapping; new official-host quota/balance access checked before calls; existing CN balances retain availability semantics. |
| `backend/internal/service/cn_provider_quota_service.go` | Dispatch new account probes after capability checks; existing CN/OpenCode rolling-window behavior retained. |
| `backend/internal/service/cn_providers_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/command_code.go` | Built-in Claude/GPT protocol fallback plus catalog capability; profile defaults only apply without administrator override. |
| `backend/internal/service/command_code_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/command_code_usage.go` | whoami failure stops tenant ambiguity; window/credits probes keep purchased-credit bypass and limited cooldown recovery; failed period fetch retains monthly state. |
| `backend/internal/service/command_code_usage_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/composite_platform.go` | Deleted: prior exclusion retained; general concrete-platform predicate moved to domain_constants rather than restoring Composite machinery. |
| `backend/internal/service/composite_platform_test.go` | Deleted with excluded Composite behavior; no tests assert support for rejected Composite platform. |
| `backend/internal/service/composite_route_resolver.go` | Deleted: prior exclusion retained; no Composite caller/interface restored. |
| `backend/internal/service/composite_route_resolver_test.go` | Deleted with excluded Composite behavior; no tests assert support for rejected Composite platform. |
| `backend/internal/service/model_protocol_catalog.go` | Per-account+URL cache with shared cold-start deadline, stale use, failure backoff/cancellation; native protocols only; redirect-disabled validated GET. |
| `backend/internal/service/model_protocol_catalog_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/opencode_go.go` | Generic protocol rules normalized and first-match preserved; explicit empty rules suppress built-in defaults; unsupported dedicated endpoint models rejected only for OpenCode. |
| `backend/internal/service/opencode_go_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/opencode_go_usage.go` | 403 is ambiguous forbidden rather than subscription assertion; bound network Retry-After to24h; local snapshot-only pricing policy retained. |
| `backend/internal/service/opencode_go_usage_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/opencode_unsupported_models_routing_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/opencode_unsupported_models_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/platform_catalog.go` | Serializes display+profile data only; deep-copies URLs/rule slices; no composite_precedence field. |
| `backend/internal/service/platform_catalog_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/platform_list_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/provider_profile.go` | Default URL/routing/native-protocol data for existing providers retains mode defaults; Command Code routes by model, Cline by inbound. |
| `backend/internal/service/provider_profile_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/ratelimit_cline.go` | Recognize structured codes/fixed messages on402/403/429;400/reflected metadata excluded; cooldown only affected wallet or free model. |
| `backend/internal/service/ratelimit_cline_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/ratelimit_cn_providers.go` | Shared balance cooldown helper; Command Code window reset ignored with purchased credits; existing CN/OpenCode reset fallback preserved. |
| `backend/internal/service/ratelimit_command_code.go` | Recoverable window/credits/org spend limits; per-model spend only cools target; bounded parsed durations and account recheck. |
| `backend/internal/service/ratelimit_service.go` | New platform classifications precede defaults; local temp-disabled/auth/failover ordering remains intact; Cline402 cannot permanently set account error. |
| `backend/internal/service/scheduler_snapshot_service.go` | Historical scheduler prefix plus all catalog IDs; fixed bool-switch automatic mismerge, KIRO dual Anthropic rebuild preserved; group0/lifecycle logic retained. |
| `backend/internal/service/testdata/upstream_protocol_routing_main_golden.json` | Pinned upstream golden fixture read/driver contract audited; full matrix test required after generation (no fixture regeneration or acceptance widening). |
| `backend/internal/service/upstream_billing_probe.go` | Profile by-inbound probe URL selection; official domains exclude new vendors; KIRO retains previous ineligibility; snapshots do not rewrite rates; retry capped. |
| `backend/internal/service/upstream_billing_probe_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/upstream_models.go` | New multi-protocol providers share live registry/model GET; mapped requested IDs separate from upstream IDs; local KIRO/Grok/catalog fetch paths retained. |
| `backend/internal/service/upstream_protocol_routing.go` | Pinned > account rule > model catalog > built-in > Chat Completions; by-inbound native availability selects protocol; mappings use final upstream identity. |
| `backend/internal/service/upstream_protocol_routing_matrix_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/upstream_protocol_routing_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/internal/service/domain_constants.go` | Aliases/new defaults/profile predicates and quota whitelist derive registry; no Composite alias; scheduling thresholds remain explicit. |
| `backend/internal/service/account_stats_pricing.go` | Optional exemption skips model long-context priority2/default price only; local provider-cost behavior remains default; custom interval/pricingAt/Fable default1 retained. |
| `backend/internal/service/account_stats_pricing_test.go` | Retains local nil/zero/priority/time/Fable regressions; adds explicit exemption/default/custom-interval cases. |
| `backend/internal/service/scheduler_snapshot_group_lifecycle_test.go` | Updated expected concrete snapshots to14; KIRO/lifecycle/group0 regression assertions retained. |
| `backend/internal/service/upstream_billing_probe_multiplatform_test.go` | Reviewed changed/new behavior assertions and fixtures against associated final production call chain; excluded Composite expectations removed where imported. |
| `backend/migrations/248_drop_platform_check_constraints.sql` | New final migration drops quota CHECK idempotently only; historical local242..247 unchanged; monitor provider checks retained. |

## Validation

- `gofmt -w` on assigned extant Go paths and added local regressions: exit0.
- `git diff --check` on assigned production domains: exit0.
- Anchored conflict-marker search: no owned conflict markers.
- Runtime/domain/unit/integration checks pending root generation gate; no success claim yet.

## Follow-up unit fixture/compile verification

- Imported Command Code/Cline tests referenced upstream `cnBalanceProbeRepo`; local pre-sync balance test replacement uses `cnBalanceStateRepo` instead. Added unit-only `cn_providers_test_helpers_test.go` minimal loading/snapshot writer without changing production.
- `GOTOOLCHAIN=go1.27.2 GOPROXY=https://goproxy.cn,direct GOMAXPROCS=2 go -C backend test -count=1 -p=2 -tags=unit ./internal/domain ./internal/model`: exit0; log `platform-domain-model-unit-1.log`.
- Focused service suite initial compile after fixture addition blocked by root-owned signature adapters only; details sent root, log `platform-focused-unit-2.log`. No source/schema changes required for fixture fix.

- Repository TPS timeout/failure-policy unit checks: `GOTOOLCHAIN=go1.27.2 GOPROXY=https://goproxy.cn,direct GOMAXPROCS=2 go -C backend test -count=1 -p=2 -tags=unit ./internal/repository -run '^Test(IsQueryTimeoutErr|OpsOutputTPSOverviewFailurePolicy)'`: exit0, `platform-repository-unit-1.log`.
- Shared fixture coverage: `backend/internal/service/cn_providers_test_helpers_test.go` loads an account and records its extra writes for imported account probes; it carries the same unit tag as both caller tests.

- Focused service suite after root signature-adapter fixes: `GOTOOLCHAIN=go1.27.2 GOPROXY=https://goproxy.cn,direct GOMAXPROCS=2 go -C backend test -count=1 -p=2 -tags=unit ./internal/service -run '^Test(PlatformList|PlatformCatalog|ProviderProfile|FrontendBuiltinPlatformCatalogInSync|CommandCode|Cline|OpenCode|ApplyAccountStatsCost_SubscriptionExemption)'`: exit0, `platform-focused-unit-3.log`. Backend/frontend builtin catalog JSON matches; subscription account stats default/custom interval cases pass.
- Failed earlier focused runs are not counted as passing: run1 used parent-provided `-p2` spelling and exited backend package setup; run2 compiled after fixture addition and exposed root-owned signature adapters. Only run3 is passing evidence. No test commands remained running at handoff.

## Full integration expectation follow-up (root full run1)

Root's `/tmp/tokenstation3-20261012-backend-integration.log` demonstrated four stale test expectations against the approved new behavior. These are expectation corrections, not baseline exemptions; root reruns the integration suite after all current processes exit. No SQL/schema/production source changes were made in this follow-up.

| Additional path | Meaningful assertions retained/updated |
| --- | --- |
| `backend/internal/repository/channel_reasoning_pricing_integration_test.go` | Fable max and all other unconfigured efforts use1x after stored maps are cleared; explicit effort maps (including max3 and max4) still override. Database roundtrip, replace rollback, total/actual cost and group0.8 multiplier assertions retained. |
| `backend/internal/repository/migrations_schema_integration_test.go` | Repeat startup succeeds; quota platform CHECK is absent after248; quota platform column and active unique index persist; both channel-monitor provider CHECKs remain. Every unrelated schema/index/constraint assertion remains. |
| `backend/internal/repository/task3_upgrade_migrations_integration_test.go` | Exact audited230->current migration sequence appends248, each applied once; second startup adds no audit rows; persisted default group/user/key/allowlist/log data and exclusion of Plugin tables remain. |
| `backend/internal/repository/upstream_sync_migration_policy_test.go` | Exact post190 sequence appends248; prompt-audit gaps, nontransactional migrations and historical checksum compatibility rules remain unchanged. |

- gofmt/diff whitespace checks passed on the four test files.
- Per root instruction no integration run was started by this agent while root's full run remained active.
- Historical local migration242..247 filesystem contents equal merge ours `c517f84cd` (SHA256 comparison below).
  - `242_add_minimax_platform.sql`: `90b28898c31e32bc6dc8fe7c906277553501d9381990673c6fde74dedaa1d97d` unchanged.
  - `243_opencode_go_platform.sql`: `6d60188332ef72c3ff45b37eb4dd440d49fc7c1d30cd02b843d7d1bb1369173c` unchanged.
  - `244_content_moderation_engine_meta.sql`: `0ca7cbaf70c5365dc9f2d580bb324f35a5bef77de2543299068b6e902d19b644` unchanged.
  - `245_channel_reasoning_effort_multipliers.sql`: `abbbf31e979a5090572314a0ab01067c9c08bafd07c40363312ee746f6b8889d` unchanged.
  - `246_add_payment_order_bonus_amount.sql`: `d74e3aab657b5148c3e058f2d04a7c06c43e5fa0df75a8e2128a3d5b9f9e96d9` unchanged.
  - `247_add_typesafe_platform.sql`: `1e737dbb9e32b63579f6ca028627476ea3b68b89b472c4a6ff09966f2c1c6a04` unchanged.
