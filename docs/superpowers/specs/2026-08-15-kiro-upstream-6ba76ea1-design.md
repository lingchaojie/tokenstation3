# KIRO Upstream 6ba76ea1 Semantic Sync Design

## Goal

Advance TokenStation3's audited KIRO reference from
`006af638390c0e929204a2486d696c302ad5bc07` to
`6ba76ea105e065a5aa8dd2b8d2957528ed58935b` by semantically integrating the
approved KIRO compatibility and correctness changes without replacing local
gateway behavior wholesale.

The implementation must preserve TokenStation3's stronger local behavior for
KIRO profile ARN placement, stable machine IDs, direct-versus-relay routing,
usage accounting, provider-native capture, failover boundaries, and strict
terminal-state validation.

## Provenance

- Local base: `05d8f0eccfc203e5bf5b84f84af081651c552a9b` on `dev`.
- Previous KIRO reference: `006af638390c0e929204a2486d696c302ad5bc07`.
- New KIRO reference: `6ba76ea105e065a5aa8dd2b8d2957528ed58935b`.
- The new reference is merge commit `6ba76ea1` with parents `a511da08` and
  `158f5b28`; most commits in the range are inherited from Wei-Shaw and must not
  be replayed as KIRO work.
- The implementation uses the nianzs fork as a behavioral reference. It does
  not cherry-pick shared files whose local versions contain later TokenStation3
  accounting, capture, security, or scheduling work.

## Approved Scope

### KIRO model contract

Add the nianzs KIRO model semantics represented by `9f41d6b`, `c897142`,
`80bf1ae`, and `5220bdc`:

- Recognize the approved GPT-5.6 and Claude Opus 5 model identifiers and aliases
  in KIRO model mapping.
- Apply the correct KIRO request model, maximum-token, implicit-thinking, and
  adaptive-thinking behavior for those models.
- Add KIRO-specific fallback model lists to both public `/v1/models` and admin
  available-model paths so an empty upstream model response does not expose an
  unrelated platform's defaults.
- Preserve TokenStation3's current credit usage and billing presence-bit
  accounting. Model support must not reintroduce the older nianzs accounting
  implementation.

### OAuth compatibility and tool calls

Integrate the KIRO behavior from `13cd8c4` into the current shared gateway and
Responses compatibility layers:

- KIRO OAuth accounts must not receive the Anthropic Claude Code mimicry system
  rewrite. Apply the same account predicate consistently in the shared
  Responses, Chat Completions, and token-count Anthropic request builders.
- Responses namespace flattening and restoration must support both `function`
  and `custom` child tools. Custom-tool call input delta/done events must retain
  the original namespace identity.
- The buffered Anthropic tool-input accumulator must treat an initial empty
  object as a placeholder and replace it with the first real JSON fragment,
  rather than producing concatenated invalid JSON such as
  `{}{\"cmd\":\"...\"}`.

### KIRO Responses compaction

Integrate the remote compaction behavior from `3ac141c` and the accompanying
`158f5b2` localization fix into TokenStation3's newer compact detection and
strict response state machines:

- Detect a Responses `compaction_trigger` for a KIRO-selected request.
- Resolve any configured compact model mapping, remove tools and thinking from
  the compact request, and enforce at least 32,000 output tokens.
- Replay a prior KIRO compaction summary when it is supplied in request input.
- Return exactly one Responses `compaction` output item whose
  `encrypted_content` carries the KIRO compaction envelope expected by Codex.
- Support streaming and non-streaming callers, including keepalive and a valid
  terminal failure response before semantic output is committed.
- Do not weaken local failover, capture, exact-once usage, or terminal-state
  rules while adding the compaction branch.

### Translator correctness

Integrate the narrow fixes represented by `be21e02`, `755b7c3`, `70fccff`, and
`1d882c9`:

- Normalize an empty tool input to `{}` when the tool has no required fields.
- Preserve whitespace-only chunks in the middle of streamed Markdown while
  continuing to suppress leading and trailing framing whitespace.
- Accept Responses tool-result content with `type: input_text` as text when
  converting it to Anthropic/KIRO content.

### Account usage display

Integrate the behavior from `0366879`: only pass the batched-usage callback to
account rows whose platform supports that batching path. KIRO rows must keep
their direct usage loader instead of entering a queue that deliberately drops
them.

### Transactional cache emulation

Semantically integrate the useful part of `e1bdb8e`:

- Cache-emulation preparation may calculate a candidate mutation before the
  request, but tracker state is committed only after upstream success.
- A failed, retried, or uncommitted attempt must not pollute the cache tracker.
- Preserve local cache-write presence handling, pure-cache final usage,
  accounting, capture, and request-attempt ownership.
- Do not add separate cache-creation and cache-read configuration ratios in this
  sync.

### AWS region support

Integrate the 34-region capability represented by `d8ec9c3` and `629214d` while
preserving TokenStation3's independent credential semantics:

- Expand the shared administrator region option list to the 34 AWS region codes
  present in the nianzs reference.
- Use that list in KIRO create, edit, and reauthorization workflows for both IDC
  login region selection and KIRO API-region selection where those controls are
  shown.
- `credentials.region` remains the IAM Identity Center/OIDC region.
  `credentials.api_region` remains the KIRO/Q runtime region. Changing one must
  not silently change the other.
- Prefer `api_region`, accept legacy `apiRegion`, and allow an API-key-only
  fallback from legacy `region`. OAuth credentials must not use IDC `region` as
  the runtime API region fallback.
- Existing non-list values remain visible and preservable during unrelated
  edits. New accounts continue to default both controls to `us-east-1`.
- KIRO relay API-key accounts with `base_url` do not use the native Q endpoint
  and therefore do not expose or rewrite `api_region`.

### Reference documentation

After implementation and verification:

- Add a dated KIRO sync archive recording both upstream coordinates, semantic
  inclusions, explicit exclusions, and verification evidence.
- Update `docs/kiro-upstream-sync.md` to pin `6ba76ea1` only when the approved
  implementation and verification are complete.

## Explicit Exclusions

- KIRO upstream billing probe (`9981e1d`, `e8bcaa7`). It remains excluded unless
  separately approved.
- Split cache creation/read ratios (`d9746be`, `235495e`).
- Removal of KIRO request pacing (`f5a738a`).
- Cross-provider prompt rules and unrelated nianzs product features.
- Enabling KIRO profit control, shared upstream-cost scheduling thresholds, or
  other general billing policy.
- Replacing local KIRO profile ARN, machine ID, image-token security, External
  IdP, capture, usage-accounting, or strict gateway implementations with the
  older reference versions.

## Error Handling and Safety

- Compatibility transforms reject malformed payloads through the current local
  error paths and must not fabricate a successful terminal event after semantic
  output has begun.
- Compaction errors before semantic output may use existing failover behavior;
  after output commitment they must produce a partial terminal result without a
  second account attempt.
- Region values are trimmed before persistence and URL construction. An empty
  value falls back to `us-east-1`; existing unknown values are preserved rather
  than silently rewritten.
- No production environment, production account, or upstream provider request
  is required for this sync. Verification uses unit/component tests and local
  test servers.

## Testing Strategy

Every production behavior change follows red-green-refactor:

1. Add a focused failing test that demonstrates the missing behavior.
2. Run the focused test and confirm it fails for the expected reason.
3. Add the smallest semantic implementation that passes it.
4. Re-run the focused test and the owning package/component suite.

Final verification includes:

- Go unit tests for `internal/pkg/kiro`, `internal/pkg/apicompat`,
  `internal/service`, and affected handlers.
- Focused gateway tests for KIRO OAuth mimicry, custom namespace calls,
  compaction streaming/non-streaming, exact terminal behavior, and capture.
- Frontend component tests for all KIRO create/edit/reauthorize region flows,
  model options, account usage loading, and localization keys.
- Frontend typecheck, lint check, and production build.
- Backend build and generated-file drift check.
- A final diff audit against `6ba76ea1` for every approved capability path and
  against local `dev` for all intentional KIRO behavior listed in the runbook.

## Completion Criteria

The sync is complete when every approved capability is implemented with focused
regression coverage, the required Go and frontend verification is green, the
reference archive records the semantic decisions, and the runbook pin points to
`6ba76ea1` without any explicit exclusion or local invariant being lost.
