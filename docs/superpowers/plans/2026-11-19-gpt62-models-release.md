# GPT-6.2 release proposal

Status: prepared locally; production execution requires confirmation. No production files, configuration, database, containers or service state have been changed.

## Artifact and behavior

Source branch: `codex/gpt62-models`, based on `b3bf9419b372af1d3e0a3948e93fcec5ba103d25`. The final reviewed commit and build checksum will be recorded in the implementation evidence before publication.

The release adds `gpt-6.2-sol` and `gpt-6.2-astra`, account presets, OpenCode exports, Codex manifests, gateway compatibility and price cards. Both charge Fast at 2x and Ultra Fast at 6x on all dates; standard rates are Sol $2/$10/$0.10 and Astra $8/$40/$0.40 per million input/output/cache-read tokens. Generic cache writes are $2.50/$10. Existing operator pricing remains authoritative.

The approved Headers change affects existing OpenAI OAuth traffic as well as the new models: enforced legacy TUI identities become `codex_cli_rs` with paired User-Agent/originator/version. Effective automatic/admin versions and the enforcement switch remain. The built-in version stays 0.146.0, matching the pinned upstream.

## Execution after confirmation

1. Read-only production inventory: identify the running image digest, deployment directory/compose project, active OpenAI groups and eligible accounts, explicit model allowlists/mappings/prices and proxy settings. Keep credentials and account data out of artifacts and output. Stop on a conflicting deployment topology or account entitlement that makes the intended routing ambiguous.
2. Integrate the reviewed commit through the existing non-force dev/CI/release workflow. Wait for the required checks, build the normal production image and record its immutable digest. Do not update unrelated upstream code or advance the tracked upstream baseline.
3. Preserve the current compose/image reference and the exact affected model configuration for rollback. Deploy only the application service with the pinned new image using the existing deployment mechanism. This change adds no migration; do not reset or recreate database/Redis volumes.
4. Add the two model IDs only to the intended existing OpenAI groups/accounts where an explicit allowlist requires it. Preserve existing mappings, proxy settings, rates, balances and settlement behavior. Confirm Pro 200 vs Pro 500 or discovered workspace permissions before advertising OAuth Ultra Fast.
5. Verify health, version, the two public model cards, account/client model lists and paired outbound headers. If paid acceptance is included in the confirmation, send at most one minimal Standard request per model through the existing server flow using the designated administrator Key and configured IP proxy; cap estimated combined usage at $1 before sending, with no automatic retries. Do not bypass the gateway or call providers directly.

## Impact and rollback

Application replacement can briefly interrupt requests. The header identity change covers existing OpenAI OAuth calls, so check their error rate after rollout. New-model usage is charged according to the rates above; upstream model access remains dependent on the actual account's entitlements.

If health or existing OAuth traffic regresses, restore the saved application image digest and the exact saved affected model entries, then recreate only the application service and recheck health. No database rollback or destructive cleanup is part of this release.

The exact production command, previous image digest and routing diff must be derived from the read-only inventory; none is guessed from the local compose examples. Production confirmation authorizes only the operations and impact described here.
