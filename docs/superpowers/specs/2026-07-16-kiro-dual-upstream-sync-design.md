# KIRO Dual-Upstream Audit and Semantic Sync Design

## Objective

Audit and semantically integrate KIRO-related changes from
`nianzs/sub2api` between the currently tracked reference
`88a5666b478e234cace9090e0d5f483f1146cb96` and the pinned target
`006af638390c0e929204a2486d696c302ad5bc07`.

The immediate high-priority outcome is to fix malformed streamed tool calls
that can make Claude Code receive `stop_reason: "tool_use"` without a valid
`tool_use` content block. The broader outcome is a complete, auditable review
of every relevant change in the pinned nianzs range, including general
Sub2API changes that may have entered nianzs through its own upstream merges.

This work does not deploy to production and does not change production code,
configuration, or data.

## Repository Background: Two Upstreams

TokenStation3 follows two related upstreams:

1. `Wei-Shaw/sub2api` is the general product upstream. TokenStation3
   periodically integrates its platform, gateway, admin, billing, operations,
   and frontend changes.
2. `nianzs/sub2api` is the KIRO reference fork. It implements KIRO behavior but
   also periodically merges changes from `Wei-Shaw/sub2api`.

Consequently, a commit found in the nianzs reference range is not necessarily
a KIRO-specific change. It may be:

- a nianzs KIRO implementation;
- an unmodified Wei-Shaw change merged into nianzs;
- a Wei-Shaw change subsequently adjusted by nianzs;
- a merge or repair commit that restores behavior lost during another merge.

The audit must not assume that a non-KIRO commit is irrelevant. It must first
identify the feature and its source, then determine whether TokenStation3 has
already integrated it from the Wei-Shaw upstream, implemented equivalent or
stronger behavior independently, or still lacks it.

## Fixed Coordinates

The audit is reproducible against these fixed coordinates:

- Previous KIRO reference: `88a5666b478e234cace9090e0d5f483f1146cb96`
- Target KIRO reference: `006af638390c0e929204a2486d696c302ad5bc07`
- KIRO reference repository: `https://github.com/nianzs/sub2api`
- General upstream repository: `https://github.com/Wei-Shaw/sub2api`

The exact TokenStation3 `dev` base and the applicable Wei-Shaw reference must
be recorded when implementation begins. If any pinned upstream or target
branch moves later, the current audit stays pinned to the coordinates above;
expanding the range requires a new explicit decision.

`docs/kiro-upstream-sync.md` must not advance its KIRO reference until every
item in the audit matrix has a disposition, all selected changes are
integrated, required verification is complete, and the final independent
review has no actionable findings.

## Audit Architecture

### Inventory

The audit begins with the full inventory required by
`docs/kiro-upstream-sync.md`, including KIRO-specific packages, shared gateway
paths, scheduling, token refresh and cache invalidation, account persistence,
usage and billing, operations context, admin DTOs and handlers, migrations,
configuration, and frontend account/group workflows.

The audit also inspects every non-merge commit and meaningful merge result in
the pinned nianzs range whose final patch touches one of those integration
surfaces. Commit subjects alone are not sufficient evidence.

### Dual-Upstream Source Tracing

For each candidate update, source tracing compares:

1. the relevant Wei-Shaw implementation or merge parent;
2. the final nianzs implementation at the pinned target;
3. the current TokenStation3 implementation.

The comparison uses ancestry, merge parents, patch equivalence, commit
history, final code, callers, consumers, and neighboring tests. Suggested Git
evidence includes `git log --cherry-mark`, merge-parent inspection, stable
patch IDs where applicable, and direct end-state semantic comparison.

Ancestry alone is not authoritative: TokenStation3 may have integrated the
same behavior through a different merge or an independently authored patch.

### Audit Matrix

The durable audit matrix records, for each capability or coherent update:

- source upstream and original/merge coordinates;
- upstream intent and externally visible behavior;
- files and runtime/data/API/frontend surfaces involved;
- KIRO relevance;
- TokenStation3's current implementation and historical evidence;
- whether TokenStation3 already integrated the Wei-Shaw source;
- local behavior that must be preserved;
- classification and final disposition;
- user decision when required;
- tests or other verification proving the disposition.

Allowed classifications are:

- **Equivalent locally:** the behavior already exists and no code is needed.
- **Local implementation stronger:** preserve the local implementation and
  add only missing compatible edge behavior or tests.
- **Missing:** semantically integrate the complete compatible behavior.
- **Partially missing:** integrate only the missing semantics after comparing
  full call chains and contracts.
- **Behavior conflict:** stop that decision domain and ask the user.
- **General upstream feature pending decision:** describe the feature, value,
  affected surfaces, and risks, then ask the user whether to include it.
- **Superseded or reverted:** record why the net target behavior requires no
  integration.

There is no automatic "unrelated, skip" classification. A general feature can
be excluded only after its function and provenance are understood and either
its equivalent is already present or the user declines to expand the scope.

## Integration Strategy

### Approach

Use per-capability semantic integration rather than merging the complete
nianzs branch or blindly cherry-picking commits. Each selected capability is
implemented as a small, reviewable, reversible commit.

Before implementation, create an isolated worktree from a refreshed and
verified `dev` base. Existing user files and untracked documents in the main
checkout must remain untouched.

### Priority Order

1. Complete the audit matrix sufficiently to identify dependencies and avoid
   applying a patch that a later upstream commit superseded.
2. Integrate the streamed tool-input fix first because it addresses the
   observed Claude Code failure and protects the Anthropic protocol boundary.
3. Integrate other confirmed missing KIRO capabilities in dependency order.
   Current known candidates include image-token estimation and OpenCode
   `filePath` compatibility, but the audit matrix—not this preliminary list—is
   authoritative.
4. For capabilities already implemented independently, preserve the local
   design and add only missing upstream edge semantics and regression tests.
5. Present general Wei-Shaw-origin features that are genuinely absent from
   TokenStation3 to the user by coherent decision domain before including
   them.
6. Update the runbook, audit archive, and KIRO reference only after code and
   verification are complete.

### Streamed Tool-Use Contract

The streamed tool fix must preserve Anthropic's content-block contract:

- buffer incremental and snapshot tool inputs by tool-use ID;
- bound buffered input size;
- normalize only safe syntactic defects supported by the reference behavior;
- parse the complete JSON object when the tool block closes;
- preserve JSON number precision while decoding and re-encoding;
- reject incomplete, scalar, array, null, oversized, or missing-required-field
  inputs rather than emitting a malformed `tool_use` block;
- emit `content_block_start`, one valid accumulated `input_json_delta`, and
  `content_block_stop` only after validation succeeds;
- set final `stop_reason` to `tool_use` only when at least one client-executable
  tool block was actually emitted;
- downgrade an upstream `tool_use` stop reason to `end_turn` when no valid tool
  block was emitted, except where a higher-priority terminal reason such as
  `max_tokens` or `stop_sequence` applies;
- retain valid aggregate/snapshot tool uses and deduplicate them consistently
  with streamed events;
- preserve structured-output and restored tool-name behavior.

### General Upstream Decision Gate

When the matrix finds a general Wei-Shaw-origin feature that TokenStation3 has
not already integrated, implementation pauses only for that decision domain.
The user receives:

- what the feature does;
- whether nianzs changed it after merging;
- why it appeared in the KIRO audit;
- expected product or operational value;
- API, data, configuration, UI, security, billing, or maintenance impact;
- interaction with local behavior;
- recommendation and rollback boundary.

Safe read-only audit work in other domains can continue while the decision is
pending.

## Local Behavior Protection

Semantic integration must preserve these documented or evidenced local
behaviors unless the user explicitly approves a change:

- profile ARN placement rules documented in `docs/kiro-upstream-sync.md`;
- stable machine ID derivation and persistence across refresh-token rotation;
- distinction between KIRO direct and relay accounts;
- mixed KIRO/Anthropic scheduling and its isolation from unrelated platforms;
- account-level KIRO API-region configuration;
- KIRO capture/archive integration and redaction boundaries;
- local authoritative usage, cache, and adapter propagation fixes;
- KIRO model-mapping whitelist behavior;
- local DEV platform support and product-specific billing, quota, and web-chat
  behavior.

These are protection constraints, not an exhaustive local feature inventory.
The audit must use current code and tests to discover additional protected
behavior in every touched call chain.

## Runbook Update

`docs/kiro-upstream-sync.md` will gain a durable dual-upstream section that:

- explains the relationship between Wei-Shaw, nianzs, and TokenStation3;
- requires pinning both the nianzs range and the applicable Wei-Shaw sync
  coordinate;
- requires merge-parent and provenance inspection;
- distinguishes ancestry from semantic equivalence;
- requires checking whether general changes were already integrated through
  TokenStation3's normal Wei-Shaw sync;
- forbids automatic exclusion merely because a change is not KIRO-specific;
- requires user approval for absent general features that expand product
  scope;
- requires separate review of nianzs follow-up fixes even when the original
  Wei-Shaw feature already exists locally;
- lists reproducible commands for ancestry, cherry equivalence, patch IDs,
  merge-parent inspection, changed-file inventory, and end-state comparison;
- retains the existing full KIRO backend/frontend inventory and intentional
  runtime-difference rules.

The runbook remains a reusable process guide. Per-run commit lists and detailed
dispositions belong in the audit archive rather than becoming permanent
feature lists in the runbook.

## Testing and Verification

### TDD Per Capability

Every missing or partial behavior follows a red-green cycle:

1. add the smallest regression test that demonstrates the current semantic
   gap;
2. run it against the pre-fix implementation and confirm the expected failure;
3. implement the minimal semantic change;
4. run the focused test and neighboring package tests;
5. run broader affected gateway, service, repository, handler, and frontend
   contract tests before committing.

The streamed tool fix must cover at least fragmented JSON, snapshot replacement,
truncated JSON, literal control characters, trailing commas outside strings,
large integers, missing required fields, missing tool ID/name, oversized input,
duplicate aggregate/stream events, write failures, and `stop_reason` consistency.

### Repository Verification

Verification commands are derived from the current CI workflows and build
configuration. The final implementation must run all required checks for the
affected repository, including:

- generated-code consistency where applicable;
- backend build, unit tests, integration tests, and static checks;
- frontend lint, typecheck, affected tests, broader test suites, and production
  build;
- explicit scans for conflict markers, unintended files, and stale generated
  code.

If a broad check fails and appears pre-existing, run the same command against
the recorded `dev` base in a separate worktree before classifying it as
baseline debt.

### Independent Review

After integration, documentation, and local verification, an independent
reviewer must examine the complete implementation range, not only conflict or
KIRO-named files. The review covers:

- every audit disposition and selected integration;
- explicit and silent semantic conflicts;
- call-chain ordering, state, errors, retries, concurrency, and cleanup;
- API, persistence, configuration, usage/billing, and frontend contracts;
- preservation of local KIRO behavior;
- test coverage and documentation accuracy.

The work is not ready for publication until the review reports no actionable
issues and explicitly considers it safe to publish.

## Documentation and Commit Structure

Durable outputs are:

1. the updated reusable KIRO runbook;
2. a per-run dual-upstream audit matrix/archive containing coordinates,
   dispositions, decisions, and verification evidence;
3. focused code and test commits for each integrated capability;
4. the final reference update to `006af638390c0e929204a2486d696c302ad5bc07`.

Documentation and code changes use explicit path staging. Existing untracked
or unrelated user files must not be committed. No force push, production
deployment, or production mutation is part of this work.

## Completion Criteria

The implementation is complete only when:

- every candidate in the pinned nianzs range has an auditable disposition;
- every absent general feature has an explicit user decision;
- selected missing and partial behaviors are integrated with regression tests;
- the Claude Code malformed-tool-call gateway defect is covered by passing
  protocol-level tests;
- local KIRO protections remain covered and passing;
- the updated runbook documents the dual-upstream process;
- required backend and frontend verification passes, or any baseline failures
  are proven by A/B evidence and explicitly reported;
- independent review has no actionable findings;
- the audit archive matches the final code and verification evidence;
- the KIRO reference is advanced to the pinned target only at the end.
