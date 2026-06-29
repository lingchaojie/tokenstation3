# KIRO Upstream Sync Guide

## KIRO Reference Tracking

KIRO gateway work in this repository tracks `https://github.com/nianzs/sub2api`.
Use the nianzs fork as the reference for KIRO forwarding, request translation,
response parsing, OAuth/IDC login, refresh behavior, cache emulation, cooldown,
usage/credits, and admin account workflows. The current reference commit is
`88a5666b478e234cace9090e0d5f483f1146cb96`; update this line only when
intentionally adopting a newer KIRO implementation.

## KIRO Sync Scope

Start every KIRO sync or audit with a full inventory:

- `rg -l "Kiro|kiro|KIRO" backend frontend deploy AGENTS.md docs/kiro-upstream-sync.md`
- `backend/internal/pkg/kiro`
- `backend/internal/pkg/kirocooldown`
- `backend/internal/service/kiro_*.go`
- KIRO-related gateway paths in `backend/internal/service/gateway_service.go`,
  `gateway_forward_as_chat_completions.go`, `gateway_forward_as_responses.go`,
  and `gateway_websearch_emulation.go`
- KIRO sticky/session handling in `backend/internal/handler/gateway_handler.go`
- Shared OpenAI-compatible usage paths in `backend/internal/service/openai_gateway_*.go`
- Account usage, token refresh, token cache invalidation, ops error context, and
  account repository paths that carry KIRO state or KIRO platform selection
- Admin handlers and DTOs for KIRO accounts, OAuth, available models, group
  cache/sticky settings, runtime/quota state, and default model mappings
- `deploy/config.example.yaml` allowlist and KIRO gateway configuration
- Frontend KIRO API clients, OAuth composables, model whitelist/presets, account
  create/edit/reauth flows, status/usage/today-stat cells, group settings,
  platform badges, filters, i18n, and shared types

Do not treat backend parity as complete unless the frontend admin workflow has
also been checked against the same reference commit. KIRO OAuth endpoints,
callback parsing, model mappings, relay/direct account distinction, credit
pricing, runtime/quota state, and usage/overage fields are a front/back contract.

## Sync Principles

- KIRO direct accounts and KIRO relay accounts must stay distinct. Direct
  accounts use native KIRO runtime behavior; relay accounts with `base_url` use
  the shared Anthropic-compatible gateway path.
- Shared backend files carry KIRO behavior even when the filename is not KIRO
  specific. Audit shared gateway, token refresh, cache invalidation, account
  repository, ops, and OpenAI-compatible usage paths.
- Account-level extra fields that affect shared forwarding, such as custom
  upstream headers, must be validated on save and applied safely at runtime.
- Preserve local DEV-only platform support and product features when syncing
  shared upstream code. Reconcile shared platform lists instead of copying them
  blindly from the reference fork.
- Keep local DEV billing/quota/web-chat behavior outside KIRO unless a KIRO
  integration point requires a narrow compatibility patch.
- When behavior differs from nianzs intentionally, keep the difference scoped and
  document the reason in the relevant code or PR, not as a one-off historical
  note in this file.
