# Sub2API Upstream Sync Design

**Date:** 2026-08-11
**Status:** Approved in conversation

## Objective

Synchronize the complete missing `Wei-Shaw/sub2api` history into the local
`dev` line while preserving every local product behavior. Process the merge by
functional domain, accept upstream behavior directly only when the local tree
has no special implementation and the surrounding call chain remains
compatible, and stop for explicit user decisions whenever the two sides differ
semantically.

## Fixed Coordinates

- Pre-design dev code baseline: `c6d81ed091525c0444e73d649bcf83210fb8f69c`
- Origin dev at design time: `77dd0ae48f4f2e3d4dacb627a66b3e1ac6d89d4a`
- Local-only commits above origin dev: two approved capture design/plan commits
- Last integrated upstream point: `eb2b8632ded614bf991d7d36abfa38b513ad8c2d`
- Upstream pin: `0b3fe95afd20aba77ee7649b37febb8255fb57a5`
- Upstream description: `v0.1.173-26-g0b3fe95af`
- Missing range at design time: 912 commits, including 595 non-merge commits

The upstream pin is the reproducible endpoint for this integration. A later
`upstream/main` advance does not silently expand the range; it triggers the
Runbook drift decision gate.

The effective `DEV_BASE` for the merge branch is the final `dev` tip containing
this committed design document. The implementation plan records that concrete
SHA after the design commit is finalized. Relative to the approved pre-design
baseline, that tip changes documentation only; production code is identical.

## Selected Integration Strategy

Create a backup ref at the dev base and a dedicated branch in a new isolated
worktree. Perform one ordinary `--no-ff --no-commit` merge of the fixed upstream
pin so the final merge commit retains both parents and the complete upstream
history.

Resolve and review the merge by functional domain rather than by conflict-file
order. This gives each domain a coherent runtime model while avoiding repeated
conflicts from release-by-release merges. Selective cherry-picking is not the
upstream-sync mechanism because it would omit dependency and repair history.
An urgent isolated security backport, if separately requested, is treated as a
different workflow and does not shrink the full upstream range.

## Local Invariants

The final tree must preserve all existing local behavior, including:

- KIRO as a real platform in account, group, scheduler, usage, quota and UI
  contracts; direct and relay execution; External IdP; Q/KRS endpoint and
  profile behavior; persisted machine identity; visual-token estimation;
  `KiroCredits`; mixed scheduling; cooldown and cache behavior.
- WebChat routes, hidden API-key behavior, native Responses dispatch,
  conversations, messages, attachments, artifacts, cancellation and title
  generation.
- ClickHouse capture of Anthropic, OpenAI and KIRO request/response/error paths,
  including streaming tees, request policy, redaction, truncation, bounded
  queues and loss visibility.
- Public plans and public model/pricing contracts, IkunPay behavior, payment
  provider instances and existing order/fulfillment behavior.
- Expiring reward credits, check-in and invitation rewards, consumption order,
  holds/releases, affiliate logic and first-recharge concurrency semantics.
- Universal subscription resolution independent of the routed group's billing
  mode.
- Legacy custom account headers, structured header overrides, account user
  agent history and existing proxy policy.
- Consolidated settings behavior and all local settings, including KIRO,
  payment, reward, affiliate, capture, announcement and `alvin` fields.
- Local branding, images, repository defaults, Caddy domain behavior,
  `127.0.0.1:8080`, local container image defaults and deployment tests.

An upstream change that removes, disables, bypasses or reinterprets any of
these invariants is a semantic conflict even when Git auto-merges it.

## Functional Work Order

1. Inventory explicit conflicts, auto-merged shared paths, upstream-only
   production paths, generated files and migration additions.
2. Resolve migration ordering, schemas and data contracts; regenerate Ent and
   Wire only after schema decisions are approved.
3. Integrate authentication, security, client-IP handling and settings.
4. Integrate gateway conversion, OpenAI, Grok, KIRO, scheduling, proxy and
   header behavior from request entry through cleanup.
5. Integrate usage, billing, subscription, payment, rewards and account stats.
6. Integrate WebChat, capture and public API contracts.
7. Integrate frontend, i18n, branding, deployment and documentation.
8. Audit every automatically merged production region after explicit
   conflicts are resolved.

Within a domain, upstream-only code with a compatible call chain is accepted
directly. Local-only code is preserved. Shared code is reconstructed from the
merge base, ours and theirs according to the desired final control flow; it is
never resolved by taking an entire side or by concatenating both sides.

Generated Ent, Wire and lock files are regenerated from the approved source
definitions rather than text-merged.

## Decision Protocol

Only genuine behavior choices block the user. For each decision, present one
coherent domain with:

1. current local behavior and its callers/consumers;
2. upstream behavior and its intended benefit;
3. the exact conflict in control flow, data, API, default or side effect;
4. impact on compatibility, production data, security, billing or operations;
5. available options and a technically justified recommendation;
6. the targeted tests and rollback implication for each option.

No code resolution, staging or commit records that decision until the user
chooses. Unrelated safe analysis may continue while the decision waits.

The following known domains always require this gate unless further evidence
proves that the behaviors are compatible without a choice:

- migration renumbering and any production migration-ledger reconciliation;
- Composite routing support or exclusion for KIRO;
- profit-control and scheduling-threshold semantics for KIRO;
- response-model billing and requested/forwarded/response-model precedence;
- prompt-audit coverage, full-prompt retention and interaction with capture;
- panel API rate-limit defaults and coverage of WebChat/payment/check-in paths;
- billing-probe automatic multiplier writeback;
- restoration of user self-service refund routes or changed refund semantics;
- rollout of Passkey, captcha, OpenAI Live and Grok voice/search/video surfaces;
- destructive or cleanup migrations, including upstream migration 220;
- any upstream default whose release notes and final code disagree.

## Data and Migration Design

The local migration history already reaches 190, while the upstream range adds
38 migrations with reused numbers from 172 through 220 and duplicate upstream
numbers. The original filenames cannot be installed unchanged.

Before editing migrations, present the complete ordered mapping and confirm the
state assumptions for deployed `schema_migrations`. The current candidate is a
monotonic remap of the complete upstream sequence to local 191 through 228,
preserving lexical dependency order and `_notx` behavior. Runner filename
constants, checksum compatibility rules, non-transactional handling, fixtures
and tests must be changed with the approved mapping. Environments that have
already recorded original upstream filenames require a separate ledger
reconciliation decision.

The usage-log contract must be a semantic union. Upstream adds image-input,
session and upstream-response-model fields but omits local `kiro_credits`; the
expected combined insert contract is 60 ordered columns. Single, prepared and
batch insert paths, scanners, DTOs, API contracts and frontend consumers must
all share the same order and null/default behavior.

No production database or production environment is inspected or changed
without separate confirmation under `AGENTS.md`.

## Error, Retry and Side-Effect Rules

For every gateway and billing integration, review success, upstream failure,
pre-output failover, in-stream failure, cancellation, timeout and cleanup.
Account state, cooldown, proxy quarantine, sticky selection, capture, usage
logging, charging, reward holds and response emission must occur exactly once
and in a documented order.

Authentication integrations must preserve session ownership proofs, cache
invalidation, universal-subscription lookup and local OAuth flows. Payment
integrations must preserve provider idempotency, fulfillment atomicity, public
contract minimization and existing local provider behavior.

## Verification Strategy

Before merging, establish a clean isolated baseline using the repository's CI
and Makefile commands. A baseline failure is reported and requires a proceed or
investigate decision; it is not silently attributed to the merge.

After the final integrated tree is ready, run sequentially where generators
write shared files:

- generation consistency and generated-file diff checks;
- backend default, unit and integration suites with controlled concurrency;
- frontend full tests, lint, typecheck, critical tests and production build;
- backend build and embedded-web tests;
- deployment and Caddy contract tests;
- targeted KIRO direct/relay/mixed-scheduling/credits tests;
- WebChat streaming, attachment, artifact, cancellation and title tests;
- capture streaming/non-stream/error/retry/redaction/loss tests;
- migration empty-database, historical-upgrade, advisory-lock, non-transactional
  and checksum tests;
- real PostgreSQL usage insert tests for the final 60-column contract;
- billing, reward, subscription, public-plan, IkunPay and payment-provider tests;
- conflict-marker, whitespace, mode and unexpected-file audits.

Any code change after a test invalidates the affected evidence and triggers the
appropriate rerun. Suspected baseline debt is confirmed with the same command
against the fixed dev base in a separate worktree.

## Review, Publication and Completion

Archive the exact coordinates, decisions, commands, results and rollback ref in
`docs/upstream-sync/`. After all local verification, give a fresh independent
reviewer the complete `DEV_BASE..HEAD` integration range, both merge parents,
the explicit-conflict set and the auto-merged production set. Push is forbidden
until that reviewer returns no actionable findings and explicitly states
`SAFE TO PUSH`.

Immediately before publication, refresh origin and upstream. Any movement of
`origin/dev`, `origin/main` or `upstream/main` triggers the Runbook drift gate.
Use only non-force pushes, and wait for all required checks on the exact pushed
dev SHA. Production-environment changes remain subject to the explicit
confirmation rule in `AGENTS.md`.

The synchronization is complete only when the fixed full range is present in
the final two-parent merge history, all approved local invariants are covered,
tests and independent review pass, the exact remote dev SHA has successful
required CI, and the rollback point is recorded.
