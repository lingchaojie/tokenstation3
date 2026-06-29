# AGENTS.md

## KIRO Reference Tracking

KIRO gateway work in this repository tracks `https://github.com/nianzs/sub2api`.

Before changing KIRO forwarding, request translation, response parsing, OAuth refresh, cache emulation, cooldown, KIRO usage, or KIRO admin OAuth behavior, compare against the reference fork first.

Start with:

- `rg -l "Kiro|kiro|KIRO" backend frontend deploy AGENTS.md`
- `backend/internal/pkg/kiro`
- `backend/internal/pkg/kirocooldown`
- `backend/internal/service/kiro_*.go`
- KIRO-related sections of `backend/internal/service/gateway_service.go`
- KIRO sticky/session wiring in `backend/internal/handler/gateway_handler.go`
- KIRO-related sections of `backend/internal/service/gateway_forward_as_chat_completions.go`
- KIRO-related sections of `backend/internal/service/gateway_forward_as_responses.go`
- KIRO-related sections of `backend/internal/service/gateway_websearch_emulation.go`
- KIRO credit propagation in `backend/internal/service/openai_gateway_*.go`
- KIRO-related sections of `backend/internal/service/account_usage_service.go`
- KIRO-related sections of `backend/internal/service/token_refresh_service.go`
- KIRO token cache invalidation in `backend/internal/service/token_cache_invalidator.go`
- KIRO upstream error fields in `backend/internal/service/ops_service.go`
- KIRO-related sections of `backend/internal/repository/account_repo.go`
- `backend/internal/handler/admin/kiro_oauth_handler.go`
- KIRO-related sections of `backend/internal/handler/admin/account_handler.go`
- KIRO group fields in `backend/internal/handler/admin/group_handler.go`
- KIRO-related sections of `backend/internal/handler/dto/types.go` and `backend/internal/handler/dto/mappers.go`
- KIRO-related sections of `deploy/config.example.yaml`
- `frontend/src/api/admin/kiro.ts`
- `frontend/src/composables/useKiroOAuth.ts`
- `frontend/src/composables/useModelWhitelist.ts`
- KIRO-related sections of `frontend/src/components/account/CreateAccountModal.vue`
- KIRO-related sections of `frontend/src/components/account/EditAccountModal.vue`
- KIRO-related sections of `frontend/src/components/account/AccountUsageCell.vue`
- KIRO-related sections of `frontend/src/components/account/AccountStatusIndicator.vue`
- KIRO-related sections of `frontend/src/components/account/AccountTodayStatsCell.vue`
- KIRO reauthorization flows in `frontend/src/components/admin/account/ReAuthAccountModal.vue`
- KIRO group cache/sticky/endpoint-mode UI in `frontend/src/views/admin/GroupsView.vue`
- KIRO platform filter/options in `frontend/src/components/admin/ErrorPassthroughRulesModal.vue`, `frontend/src/views/admin/SubscriptionsView.vue`, and `frontend/src/views/admin/ops/components/OpsDashboardHeader.vue`
- KIRO-related account badges and list wiring in `frontend/src/components/common/PlatformTypeBadge.vue` and `frontend/src/views/admin/AccountsView.vue`
- `frontend/src/types/index.ts`

Do not treat backend parity as complete unless the frontend admin workflow has also been checked against the same reference commit. KIRO OAuth endpoints, callback parsing, model mappings, relay/direct account distinction, credit unit price, and usage/overage fields are a front/back contract.

Reference commit used for the 2026-06-29 replacement: `88a5666b478e234cace9090e0d5f483f1146cb96`.

Keep local DEV features outside KIRO unless a KIRO integration point requires a narrow patch.

## 2026-06-29 Sync Misses

The first KIRO sync missed items because the comparison focused on KIRO-named backend files and did not inventory every `Kiro|kiro|KIRO` reference across backend, frontend, deploy examples, and tests. KIRO behavior in this fork is spread through large shared files, not only `backend/internal/pkg/kiro` and `backend/internal/service/kiro_*.go`.

Specific misses from that approach:

- The local `upstream` remote is not the nianzs reference fork. Always compare against `https://github.com/nianzs/sub2api` at the recorded commit, not only local remotes.
- Admin OAuth is a frontend/back contract: `frontend/src/api/admin/kiro.ts`, `useKiroOAuth.ts`, create/edit modals, callback parsing, model whitelist, i18n, and `frontend/src/types/index.ts` must move together.
- KIRO model support is split between backend `DefaultKiroModelMapping` and frontend whitelist/preset mappings. Updating only backend can leave the UI capped at stale models.
- KIRO direct accounts and KIRO relay accounts must be distinguished with `isKiroDirectModeAccount` or equivalent logic. Relay accounts with `base_url` should not use KIRO runtime, usage, count-tokens blocking, or credits display.
- Usage changed from old `kiro_usage` assumptions to `kiro_credit`, `kiro_bonus`, `kiro_overage`, runtime/quota state, and `kiro_credits` window stats. DTOs, service enrichment, account list status, today stats, and usage cells must be checked together.
- Shared backend files carry KIRO forwarding details: model mapping, sticky session TTL, web search cache emulation, SSE internal credit stripping, stream keepalive, cooldown recovery, token provider routing, and background OAuth refresh candidates.
- `gateway_handler.go` is part of KIRO sticky behavior even though it is not KIRO-named. Missing the upstream explicit session headers or `BindStickySessionForGroup` calls makes KIRO group TTL/auto-sticky settings ineffective.
- OpenAI-compatible gateway files (`openai_gateway_chat_completions.go`, `openai_gateway_messages.go`, and `openai_gateway_service.go`) propagate `kiro_credits` from terminal stream/JSON usage into usage logs. Missing these paths causes admin/user usage to lose KIRO credits even when forwarding works.
- Token refresh and cache invalidation are coupled: refreshing or reauthing a KIRO account must clear both the generic account token cache and KIRO-specific cache keys.
- Admin reauthorization is separate from account create/edit. Syncing only `CreateAccountModal.vue` and `EditAccountModal.vue` leaves existing KIRO accounts unable to run the upstream OAuth/IDC/import-token reauth flows.
- KIRO group cache/sticky/endpoint-mode settings span backend group DTOs, repository persistence, auth-cache group snapshots, and `GroupsView.vue`. Syncing backend fields without the admin UI creates a front/back mismatch where settings exist but cannot be managed.
- Smaller platform-option surfaces still matter: error passthrough rules, subscriptions, ops filters, badges, and user/admin quota UI need KIRO options where the upstream fork exposes them.
- Ops error context carries `kiro_model_id`; shared ops sanitization must trim/truncate it with requested/mapped model fields.
- `deploy/config.example.yaml` is part of the KIRO integration because URL allowlist deployments need KIRO auth, AWS SSO OIDC, and AWS Q runtime hosts.
- Tests originally covered gateway/OAuth pieces but not the full admin path: start login, Builder ID/IDC/import fields, model whitelist, direct vs relay split, KIRO credits/window stats, runtime status, background refresh candidates, and keepalive config.

When syncing KIRO again, record which local differences are intentional. Current acceptable local differences include DEV billing/quota features, web chat stream capture, local helper placement, and other non-KIRO behavior that does not change the nianzs KIRO contract.
