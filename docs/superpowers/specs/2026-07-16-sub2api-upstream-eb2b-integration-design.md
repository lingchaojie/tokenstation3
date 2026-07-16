# sub2api Upstream eb2b Integration Design

## Goal

Merge the complete upstream range from `7d239d62e8f1c6aea79164f88903f4158cbf2f98`
through `eb2b8632ded614bf991d7d36abfa38b513ad8c2d` into local `dev`, while
preserving local billing, Grok media, KIRO, WebChat, payment, deployment, and
settings behavior.

## Fixed Coordinates

- `DEV_BASE`: `3e07e005e8d86271715471ed006006247ed8dc9d`
- `LAST_UPSTREAM`: `7d239d62e8f1c6aea79164f88903f4158cbf2f98`
- `MERGE_BASE`: `7d239d62e8f1c6aea79164f88903f4158cbf2f98`
- `UPSTREAM_PIN`: `eb2b8632ded614bf991d7d36abfa38b513ad8c2d`
- Upstream description: `v0.1.156-15-geb2b8632d`
- Rollback branch: `backup/dev-before-upstream-sync-20260716-062211`

The integration uses an ordinary `--no-ff` merge of `UPSTREAM_PIN`. It must
retain both parents and must not be replaced by cherry-picks or a squash.

## Confirmed Product Decisions

### OpenAI long-context billing

Add upstream's per-account boolean
`extra.openai_long_context_billing_enabled`, usage-log snapshot, admin UI,
validation, CRS/import propagation, Spark-shadow inheritance, and provider
account-cost correction.

Preserve existing user-billing behavior without making the upstream opt-in
policy ineffective:

- Existing OpenAI parent accounts that do not have the field are backfilled to
  `true`.
- Existing explicit boolean values are preserved.
- Existing malformed values are normalized to `false`.
- Existing Spark shadows inherit their parent account's effective value.
- New OpenAI accounts default to `false` when the field is omitted.
- Future Spark shadows inherit their parent value.
- The database trigger rejects non-boolean values and propagates parent changes
  to shadows through scheduler outbox events.
- The frontend create flow starts disabled; edit/import/update flows preserve
  explicit values.

The account flag controls user billing. Provider account-cost estimation still
uses the actual long-context price so margin remains observable when user
billing is disabled.

### OpenAI Agent Identity

Adopt the upstream Agent Identity import and runtime flow:

- Accept `auth_mode=agentIdentity` Codex auth JSON with a validated PKCS#8
  Ed25519 private key and runtime identity.
- Do not require or synthesize OAuth access/refresh tokens or OAuth expiry.
- Register and persist a task ID through the account's configured proxy.
- Generate a fresh signed `AgentAssertion` for upstream requests.
- Recover an invalid/expired task at most once, serialize registration per
  credential account, and invalidate stale WebSocket connections.
- Resolve Spark shadows to their credential parent.
- Redact private keys, task IDs, assertions, and related credentials from
  returned errors, logs, and ops events.

Existing OAuth, PAT, and API-key accounts must retain their current behavior.

### Grok custom upstream and header overrides

Adopt upstream's operator-policy-validated custom Grok `base_url`, frontend
editing/import tools, billing/quota/monitor routing, and header override support
for Grok API-key and OAuth accounts.

Preserve the local OAuth media split:

- Default Grok OAuth text/Responses traffic uses the CLI subscription gateway.
- Default Grok OAuth image/video media traffic uses the official API gateway to
  avoid the CLI gateway's smaller request-body limit.
- An explicitly configured custom `base_url` routes text, media, and probes to
  that custom host after URL-policy validation.
- OAuth authorization and token refresh always use official authentication
  endpoints.

Header overrides remain opt-in and must retain the shared blocked-header,
length, count, casing, and control-character validation. Authentication,
connection, WebSocket, content framing, and session-isolation headers cannot be
overridden.

### Admin recharge affiliate rebate

Adopt the upstream setting `affiliate_admin_recharge_enabled` with default
`false`.

- Only a positive admin balance `add` operation may accrue a rebate.
- `set`, `subtract`, zero, and negative operations never accrue a rebate.
- Existing affiliate eligibility, rate, freeze, duration, and cap rules apply.
- Rebate failure is logged and does not roll back a successful balance change.
- The setting is exposed in the consolidated local settings API, audit list,
  frontend form, and tests.

### Server-Timing

Adopt `server.enable_server_timing` with default `false`.

- When disabled, SQL and Redis clients are not timing-wrapped.
- When enabled, only authenticated admin/user UI scopes may receive aggregated
  `Server-Timing` response metrics.
- Gateway traffic, public payment endpoints, and webhooks do not expose timing.
- Request headers may scope collection but never grant authorization.
- Metrics remain bounded and contain durations/counts rather than SQL, Redis
  keys, credentials, or request payloads.

## Local Architecture Preservation

### Consolidated settings implementation

Local `dev` consolidated upstream's split settings files into
`backend/internal/service/setting_service.go` and
`backend/internal/handler/admin/setting_handler.go`. Upstream changes from
deleted split files must be semantically folded into the consolidated files.
The deleted files must not be restored merely to resolve modify/delete
conflicts.

### KIRO

No upstream KIRO-specific file is introduced in this range, but shared gateway,
account, token refresh, usage, admin, frontend, and deployment paths overlap.
The final merge must preserve:

- Direct versus relay account routing.
- Q chat, Q MCP, and KRS profile ARN placement.
- Persisted machine ID behavior.
- KIRO credits propagation and usage display.
- Sticky/session, cooldown, refresh, custom-header, WebChat capture, and admin
  workflows.
- The existing KIRO reference pin in `docs/kiro-upstream-sync.md`.

### Other local behavior

Preserve local WebChat Responses attachments/artifact capture, payment/public
plan behavior, custom deployment/Caddy configuration, daily check-in, beginner
guide, KIRO, and local platform lists while adding upstream fields and flows.

## Migration Integration

Do not modify or rename already-published local migrations. Rename the six new
upstream migrations in execution order:

| Upstream filename | Local filename |
|---|---|
| `174_add_usage_log_long_context_billing.sql` | `185_add_usage_log_long_context_billing.sql` |
| `175_add_ops_system_logs_host.sql` | `186_add_ops_system_logs_host.sql` |
| `175_default_openai_long_context_billing.sql` | `187_default_openai_long_context_billing.sql` |
| `175a_add_ops_system_logs_host_index_notx.sql` | `188_add_ops_system_logs_host_index_notx.sql` |
| `176_channel_monitor_grok_provider.sql` | `189_channel_monitor_grok_provider.sql` |
| `177_add_subscription_plan_currency.sql` | `190_add_subscription_plan_currency.sql` |

Update migration tests to use the local filenames. The long-context migration
implements the existing-on/new-off policy above. The ops host index remains a
non-transactional, idempotent concurrent index migration.

## Merge and Review Strategy

1. Run `git merge --no-ff --no-commit "$UPSTREAM_PIN"`.
2. Review every explicit conflict with base/ours/theirs/final comparison.
3. Regenerate Ent and Wire rather than hand-editing generated conflicts.
4. Inspect all automatically merged production regions, not only conflict
   files, following callers, state writes, side effects, retries, cleanup, and
   API/frontend contracts.
5. Add focused regression tests before implementing local deviations: existing
   OpenAI accounts remain enabled, new accounts default disabled, default Grok
   OAuth media stays on the official API host, and explicit custom Grok media
   follows the configured host.
6. Stage explicit paths only; never use `git add -A`.
7. Commit the merge with both parents, then commit archive/plan/generated review
   follow-ups separately if needed.

## Verification

Run, against the final code:

- `GOMAXPROCS=2 make check-generate`
- `GOMAXPROCS=2 make build-backend`
- Low-concurrency Go package compilation and affected unit/integration tests.
- Focused billing, migration, Agent Identity, Grok URL/header/media, affiliate,
  Server-Timing, scheduler, OpenAI forwarding, KIRO, payment, and WebChat tests.
- Frontend lint, typecheck, affected Vitest files, critical/WebChat suites, and
  production build.
- Shell syntax checks and the macOS-only Apple Container test in CI.
- Whitespace, conflict-marker, unexpected-file, generated-code, migration-order,
  API-contract, and KIRO inventory audits.

After local verification and archive updates, a fresh independent reviewer must
cover every merged production region and explicitly return `SAFE TO PUSH`.
Only then may `dev` and the fast-forward/no-op `main` be pushed without force.
Completion requires all required GitHub checks for the exact pushed `dev` SHA to
succeed.

## Rollback

Before push, abandon the merge branch or return to the backup branch. After
push, use a normal revert of the integration commits; do not rewrite shared
branch history. Preserve
`backup/dev-before-upstream-sync-20260716-062211` until the user confirms the
synced release is stable.
