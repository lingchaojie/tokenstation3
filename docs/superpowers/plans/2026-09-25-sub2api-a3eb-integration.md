# sub2api a3eb semantic integration implementation plan

> Execute under repository skill `skills/sync-sub2api-upstream/SKILL.md`. Use parallel domain workers only on disjoint file ownership, with a fresh full-range review after verification.

**Goal:** Merge the complete upstream history through a3eb7ef302961cba716dc78b39b93b60c467db0e into dev, preserve local behavior, publish only after verification and independent review, and wait for exact-SHA required CI.

**Architecture:** Ordinary two-parent merge with a892514e488bb880987aba7bec3b102d9964fb23 as first parent. Resolve every explicit conflict and audit automatic changes against 881f3202694c6bc932446931a30c27d9675178b9. The approved risk assessment supplies the design and user decisions.

**Tech Stack:** Go 1.27, PostgreSQL, Redis, Ent/Wire, Vue/TypeScript, pnpm/Vitest, GitHub Actions.

**Spec:** docs/upstream-sync/2026-09-25-sub2api-0.2.8-risk-assessment.md

## Global constraints

- User approved migration 244 content moderation metadata and 245 reasoning multiplier map. Do not import offline affiliate withdrawal or its operation_id migration.
- Adopt new reasoning multiplier map while preserving existing default Fable 5.1 max 3x and prior explicit overrides.
- Include Seedance, TypeSafe, Codex referral/credits; no live invitations or provider requests during tests.
- Preserve KIRO direct/relay and reference 6ba76ea105e065a5aa8dd2b8d2957528ed58935b, capture/spool, WebChat, unified keys, site pricing/fail-closed/explicit zero, subscription fallback and quota lifecycle.
- Keep Plugin, Composite, Grok audio and independent x_search excluded.
- No production change, force push or alteration to original user files.
- No per-worker commits during unresolved merge. Stage explicit owned paths only. Main agent creates the final two-parent merge after verification.
- Existing isolated worktree is .worktrees/audit-sub2api-20260925-a3eb; backup is backup/dev-before-upstream-sync-20260925-a3eb.

## Review focus

- Empty DB migration order versus upgraded DB and legacy JSON pricing rollback compatibility.
- Actual charged subscription type/group versus planned billing type when balance fallback occurs.
- KIRO/capture branches retained alongside final forwarded reasoning effort.
- Excluded subsystem files entering without conflicts and settings split-file duplicate functions.
- New async Seedance task authorization, dedup claims and failed settlement recovery.

## Task 1: Billing and migrations

Owned: billing_service.go, account_stats_pricing.go, model_pricing_resolver.go, pricing_service.go,
channel pricing service/model/repository files, their tests, backend/migrations, upstream_sync_migration_policy_test.go.
Interfaces: ChannelModelPricing.ReasoningEffortMultipliers map[string]float64; CostInput.ReasoningEffort;
preserve fallback semantics for model-specific max pricing when effort has no configured override.

- [x] Read all three parent versions and nearby tests; identify existing regression assertions.
- [x] Add/update regression for no configured Fable max (3x), explicit max override, other efforts (1x), zero prices and account-stat parity.
- [x] Resolve semantic conflicts preserving image/video/cached token provenance and local cost validation.
- [x] Rename new migrations 244/245 with apply_patch; exclude withdrawal migration; update filename contract tests.
- [x] Test migration upgrade/replay and billing/account-stat behavior once shared package compiles.
- [x] Report changed paths and evidence in domain report.

## Task 2: Frontend

Owned: frontend/**, excluding generated assets. Backend API map follows Task 1.
- [x] Resolve all frontend conflicts with base/local/upstream evidence.
- [x] Preserve KIRO/all platforms, unified key, model display-only policy, all-NULL quota rows, WebChat/branding/payment/rewards.
- [x] Exclude Plugin and offline withdrawal UI/API/types/translations/tests, retaining existing affiliate features.
- [x] Integrate reasoning map UI with correct preserved default display; review automatically merged new features.
- [x] Run frozen dependency install, typecheck, lint, full tests and build when ready.
- [x] Record verification and semantic coverage.

## Task 3: Forwarding services

Owned: service/gateway_* except gateway_usage_billing.go; service/openai_gateway_* except openai_gateway_usage.go;
service/gemini_*; service/antigravity_gateway_*; service/openai_ws_http_bridge*;
service/openai_codex_models*; service/openai_upstream_transport_error*; pkg/apicompat/**.
- [x] Resolve conflicts preserving KIRO, capture final attempt, WebChat and detached-context cleanup.
- [x] Integrate final effort propagation, protocol errors and heartbeat fixes with complete caller/callee review.
- [x] Retain local model aliases/catalog/request admission decisions.
- [x] Adapt tests and add behavioral coverage where composed flows were untested.
- [x] Run targeted service/pkg tests after shared compile blockers clear; report evidence.

## Task 4: Core integration, settings, handlers, repositories and exclusions

Owned by coordinator: all paths outside Tasks 1–3 including gateway_usage_billing.go/openai_gateway_usage.go,
config, account/identity/settings/affiliate services, repositories, handlers/routes, wire, Go modules, CI/deploy.
- [x] Resolve settings split files by integrating their upstream semantic changes into local source of truth.
- [x] Remove newly imported Plugin files and references; remove offline withdrawal capability throughout backend.
- [x] Preserve actual billing-result cache updates and implement optional simple-mode key-window path.
- [x] Integrate Seedance with unified-key authorization and local async billing; retain existing capture scope.
- [x] Integrate identity version sync, OpenCode state, moderation and referral services.
- [x] Resolve toolchain/dependency/CI conflicts; regenerate only after final source interfaces stabilize.
- [x] Audit all automatic-merge regions and record coverage.

## Task 5: Verification, archive and publication

- [x] Run go test ./..., go test -tags=unit ./..., full integration package partitions and risk-focused races.
- [x] Run backend lint, build, generation consistency; frontend full verification and deploy/security checks as CI specifies.
- [x] Reproduce suspected baseline failures at DEV_BASE before classifying them.
- [x] Archive decisions/test commands and add index entry; resolve all conflict markers and inspect staged diff.
- [ ] Commit true merge; fresh independent reviewer covers every changed area and both parents. Fix and re-review any findings.
- [ ] Refresh refs; pause only on unapproved drift or functional choice. Fast-forward local dev and main only as runbook permits.
- [ ] Display final ref updates; non-force push dev/main; wait for exact dev SHA required checks. No post-CI commits.

## Progress

- Task 0 complete: risk analysis, 97 conflict inventory, full fixed coordinates, user decisions and protected original tree.
- Tasks 1–4 implemented in isolated merge worktree; local full verification passed.
- Frontend, release helpers and deployment CI equivalents passed; backend generation/build and reachable-vulnerability scan passed. See domain evidence for exact commands.
- Task 5: local verification and archive complete; independent review, ref refresh, push and exact-SHA CI still pending.
- The user approved the concrete risk assessment and migration proposal with “是的”; execution is authorized.
