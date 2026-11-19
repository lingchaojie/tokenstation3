# GPT-6.2 focused integration — 2026-11-19

This branch selectively integrates GPT-6.2 Sol/Astra support and the approved outgoing Codex Headers correction. It is not a full upstream merge and does not advance the tracked upstream baseline.

References: upstream `6107cf17487fe5cb0bd179457ede6b1fe8f7271e` (models), `2d20707f16dcd5d2d692a9535370dbbdb9b8b6f0` (subscription entitlements), `c591412c42ab195dacdede9eb19f9cbfee221144` (paired CLI identity); pinned source tree `2af3f1e15c98c893a89255a037f551046883b8c0` (v0.2.23). Local base is `b3bf9419b372af1d3e0a3948e93fcec5ba103d25`.

Approved local policy: both new models use Fast 2x and Ultra Fast 6x at every request date. The upstream 3x promotion is deliberately excluded. Existing models' rates, explicit channel/group pricing, zero prices, funding/capture and quotas keep their behavior. The new models use actual credential/final upstream identity for the subscription long-context exemption; API-key long-context pricing retains the >272K boundary and 2x input/cache, 1.5x output rates.

Included: exact IDs and supported aliases, model-specific prices, public cards, account presets, OpenCode exports, official Codex metadata/instructions, mapped request validation and sampling/tool compatibility, API vs subscription capabilities, legacy TUI-to-CLI outgoing paired identity. Static Codex fallback version remains 0.146.0 in both local and upstream; automatic synchronization and administrator overrides remain in place.

Excluded: general 30m/12h cache accounting, unrelated migrations, MCP, dependencies, upstream funding changes, unrelated identity-refresh changes and full dev synchronization.

Design: [approved spec](../superpowers/specs/2026-11-19-gpt62-models-design.md). Deployment scope, impact and rollback: [release proposal](../superpowers/plans/2026-11-19-gpt62-models-release.md).

## Decisions and evidence

- November 17 creation timestamps originate from the pinned upstream model list, so release dates remain marked unverified. They affect hidden default sorting only.
- Added OpenCode exports after finding their independent model map; the missing-model regression failed before the fix.
- Added new-model sampling cleanup to the raw Chat Completions path after a mapped-request test demonstrated unsupported temperature passthrough. Other models retain their previous behavior.
- Backend unit/integration suites, lint and embedded build passed after the independent review fix pass; frontend 444 files / 3,563 tests, lint, typecheck and build passed. Four Important review findings were reproduced and fixed, including the related Messages/usage alias cases; no Critical or Minor findings. Production has not been deployed.

Detailed commands and build evidence: [validation](./evidence/2026-11-19-gpt62/validation.md).
Review triggers, corrections and limits: [review](./evidence/2026-11-19-gpt62/review.md).
