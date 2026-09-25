# Frontend and release implementation evidence

Coordinates: base `881f3202694c6bc932446931a30c27d9675178b9`, local
`a892514e488bb880987aba7bec3b102d9964fb23`, upstream
`a3eb7ef302961cba716dc78b39b93b60c467db0e`. This is implementation-domain
evidence, not the independent final review or authorization to publish.

## Semantic decisions and coverage

- Reviewed frontend conflicts and automatically merged production changes across
  API/types, accounts, pricing/channels/groups, keys/model catalogs, quotas,
  common/user/payment components, settings/backup/moderation, stores, locales,
  affiliates, usage/export and remaining changed views/utilities.
- Adopted the effort multiplier map throughout serializers, forms, statistics
  and plaza display. Missing Fable 5.1 max remains 3x; explicit max=1 overrides
  it; other missing efforts remain 1x. UI placeholders and both locales explain
  this. Preserved image-input prices, independent cache pricing and local shared
  conversion helpers. Added fallback/matcher and serialization/display tests.
- Preserved KIRO (all 11 quota platforms), persisted all-NULL quota rows and
  reset behavior, local unified-key/client config generation and root endpoints,
  display-only model names, branding, WebChat, RMB payment and reward columns.
  Integrated new model catalogs without replacing local catalogs.
- Integrated TypeSafe drafts/thresholds, Seedance opt-in capabilities, Codex
  referral/credits, OpenCode usage and backup retention UI. Added stale-account
  response guards for OpenCode usage rows and edit modal. No live referral or
  provider calls were performed.
- Removed reintroduced Plugin files and new offline affiliate withdrawal API,
  UI, types, locales and dedicated tests. Retained nullable affiliate rendering
  improvements and existing reward/balance functionality. Existing Composite,
  Grok audio and standalone x_search exclusions remain.
- Composed common-dialog scroll/ID fixes with local focus handling; retained
  local announcement auto-popup/reset behavior while adding stale-fetch guards.
  Kept both local and upstream payment/subscription regression suites where
  add/add test files collided. Adapted upstream tests to local endpoint/branding
  contracts, rather than changing those contracts to satisfy upstream fixtures.

## Verification

Linux Node 24.15.0 with `corepack pnpm` 9.15.9 was used (plain `pnpm` resolved
to a Windows wrapper). All commands ran in the isolated merge worktree.

- `corepack pnpm install --frozen-lockfile`: passed.
- `corepack pnpm typecheck`: passed.
- `corepack pnpm lint:check`: passed.
- `corepack pnpm test:run`: final rerun 407 files / 3162 tests passed with no
  unhandled errors (52.87s). Pricing/i18n targeted suite: 3 files / 50 tests.
- `corepack pnpm build`: passed, including vue-tsc and i18n checks; final build
  20.32s. Non-fatal warnings: stale Browserslist metadata, large chunks and Node
  DEP0190. Generated frontend assets are ignored and were not staged.
- `git diff --check` and marker scans for owned paths: passed.

Logs: `/tmp/sub2api-frontend-{tests-final,lint-final,build-final,pricing-final}.log`.

A repeat full run exposed an existing KeyUsageView animation timer leak after
unmount (407 files / 3159 assertions passed but two unhandled RAF errors made the
process fail). Both the component and original test were unchanged from DEV_BASE.
Added a deterministic three-phase lifecycle test: it failed identically at
DEV_BASE and in the merge worktree for pending frame, delayed start and active
animation. Added cancellation and stale-generation/unmount guards; all seven
KeyUsage tests and the final full suite pass. This also prevents work against a
departed view during normal navigation. Baseline diagnostic test edits were
restored; A/B logs are `/tmp/sub2api-keyusage-{baseline-red,red,green}.log`.

## Release workflow

Reviewed both parent workflows, the resolved workflow, GoReleaser configurations
and all new release helpers. Adopted one-source-SHA frontend/binary matrix,
archive provenance/checksum validation, target caches, image contexts and dry-run
publication guards. Retained local `release` branch triggering and auto-tagging
as `v0.1000.<run_number>`, and disabled VERSION writeback for branch releases.
The source checkout is pinned to the triggering SHA unless a dispatch ref was
explicitly supplied. The tag resolver permits an idempotent rerun only when the
existing tag resolves to that same source commit.

- Release helper unittest suite: 14 passed (including 4 local branch/tag/workflow
  contract tests using disposable local repositories, never remote publication).
- `bash -n` for both release shell helpers: passed.
- Host Python 3.10 lacks `hashlib.file_digest`; tests were rerun successfully in
  isolated Python 3.11 venv `/tmp/sub2api-release-python.gTcovk` with the pinned
  requirements. Workflow uses Python 3.12.
- No release workflow was dispatched, no image published, no production change.

Parent coordinator retains responsibility for final cross-domain verification,
independent review, merge commit and publication.
