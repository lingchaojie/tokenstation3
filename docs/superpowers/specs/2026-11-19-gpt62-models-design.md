# GPT-6.2 model listing and forwarding

## Intent and approved pricing

Add `gpt-6.2-sol` and `gpt-6.2-astra` to TokenStation's model catalog and existing OpenAI gateway. The user explicitly selected Fast at 2x and Ultra Fast at 6x for both models on 2026-11-19. Do not implement the upstream 3x promotion or change existing models' prices.

Standard per-million-token defaults are Sol $2 input / $10 output / $0.10 cache read / $2.50 generic cache write, and Astra $8 / $40 / $0.40 / $10. Preserve explicit channel/group pricing and existing funding, reservations, quotas and capture behavior. API-key model long-context pricing uses the existing >272,000 input threshold, 2x input/cache and 1.5x output policy. Real OpenAI subscription credentials receive the existing Sol-style model long-context exemption for the two new models; aliases cannot change the qualification.

## Approach

Configuration-only listing can forward basic requests but cannot express the missing 6.2 normalization, fixed Ultra Fast policy, subscription exemption or Codex capabilities. A full upstream sync would include unrelated migrations and product changes. Use a focused additive integration against current dev, using upstream `6107cf174` / `2d20707f1` and their pinned final tree `2af3f1e15` as implementation references. Keep local behavior when it differs intentionally.

## Required behavior

- List one public catalog card per new base model with official standard prices, verified 1,050,000-token API context windows and independent source URLs. Include model defaults and frontend account presets.
- Recognize only the two exact base IDs and supported reasoning/compact aliases, preserving model identity across native Responses, Chat Completions, Messages conversion, routing, usage and pricing.
- Provide offline Codex metadata and instructions for each new model; live account metadata remains authoritative. Preserve account-specific Ultra Fast entitlements and transport capabilities.
- Fix new-model service-tier calculation at 2x Fast and 6x Ultra Fast for all request dates and all used token buckets. Keep existing operator overrides and other models' behavior.
- Retain the current cache accounting contract; importing the upstream 30m/12h cache feature, MCP, migrations and general dependency updates is outside this task.

## Approved Codex identity follow-up

The user approved the upstream Headers correction on 2026-11-19: normalize legacy outgoing `codex-tui` identities to `codex_cli_rs`, rewriting the User-Agent and originator together while preserving the effective configured/auto-synced version. Keep other official clients and the existing enforcement switch semantics. The upstream and local built-in fallback version are both 0.146.0, so there is no upstream version bump to port.

## Verification and release boundary

Use test-first checks for model identity, unknown lookalikes, standard/tier costs, aliases, long-context boundaries and subscription credentials. Exercise catalog endpoints, Codex account capabilities and frontend presets. Run relevant backend/frontend suites, builds and static checks, then obtain independent review of the complete change.

No production credentials or local production instructions enter this worktree. Production writes, service restart/deployment and real provider calls require the separate concrete confirmation required by the main workspace's `PRODUCTION.local.md`. Prepare the tested change and exact rollout/rollback actions before that confirmation.

Sources: https://developers.openai.com/api/docs/models/gpt-6.2-sol and https://developers.openai.com/api/docs/models/gpt-6.2-astra; official pricing was verified in the preceding investigation. The user-selected 6x Ultra Fast price intentionally differs from the current official promotion.
