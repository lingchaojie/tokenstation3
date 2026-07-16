# YUNDU Promotion Domain Design

**Date:** 2026-07-16

**Status:** Approved

**Branch:** `dev`

**Base:** local `dev` at `3e07e005e`

## Summary

Add `yundu.linx2.ai` as a promotion-domain alias for the existing LINX2 application. The alias serves the same embedded Vue homepage, login, registration, email-verification, API, and authenticated application as `www.linx2.ai`; it does not deploy or maintain a second frontend.

Visits to the exact `yundu.linx2.ai` hostname establish the fixed affiliate attribution code `YUNDU` for 30 days. New accounts created through the email or GitHub/Google registration flows bind to a disabled internal channel owner account. Visit reporting reuses the existing 51.LA application, while real registrations remain visible through the existing Affiliate invitation records.

The feature requires one-time Cloudflare DNS and host Caddy configuration. Those host-level changes remain outside the web application's binary updater and database migrations.

## Current Production Context

The read-only production inspection on 2026-07-16 established the following deployment shape:

- `linx2.ai` and `www.linx2.ai` are proxied through Cloudflare.
- Host Caddy terminates origin TLS on ports 80/443.
- `linx2.ai` redirects permanently to `www.linx2.ai`.
- `www.linx2.ai` reverse proxies to `127.0.0.1:8080`.
- The Sub2API application runs in Docker and publishes host port 8080.
- The application container mounts only its `/app/data` directory from the host.
- PID 1 runs as the non-root `sub2api` user.
- Host `/etc/caddy` is not mounted into the application container.
- The installed Caddy build has no DNS provider module.
- `yundu.linx2.ai` currently has no DNS record and no Caddy site block.

The current admin update flow downloads a release archive, extracts only the `sub2api` executable, and atomically replaces `/app/sub2api` inside the current container. The restart endpoint exits the process, after which Docker's `unless-stopped` policy restarts the same container. On startup, the new binary runs all embedded SQL migrations before serving traffic.

The updater cannot safely reach host Caddy. It also does not update deployment scripts, Compose files, Cloudflare records, or the Docker image.

## Goals

1. Serve the existing product at `https://yundu.linx2.ai` without maintaining a duplicate frontend.
2. Support `https://yundu.linx2.ai/register` and all existing email-registration behavior.
3. Attribute new registrations from this hostname to affiliate code `YUNDU`.
4. Preserve attribution through GitHub and Google registration.
5. Reuse the existing 51.LA `id` and `ck` for visit reporting.
6. Show real registered users through the existing admin Affiliate invitation records.
7. Keep `www.linx2.ai` behavior unchanged.
8. Make fresh-server Caddy generation reproducible through the deployment script.
9. Keep host Caddy and Cloudflare privileges out of the application container and admin update endpoint.

## Non-Goals

- Building a separate promotion frontend or deployment.
- Adding a unified visits-and-registrations dashboard.
- Adding backend analytics tables, counters, or reporting APIs.
- Creating a standalone channel-code data model.
- Giving the YUNDU operator an active login account.
- Automatically modifying Cloudflare DNS from the LINX2 application.
- Allowing the admin binary updater or database migrations to edit host Caddy.
- Automatically treating arbitrary `*.linx2.ai` hosts as affiliate channels.
- Restricting the YUNDU hostname to only `/home` and `/register`; it is an alias for the full existing application.
- Closing the production host's public port 8080 as part of this feature.

## Chosen Approach

Use the same application deployment with an exact-host promotion registry in the frontend.

This approach was selected over:

1. A separate frontend deployment, which would duplicate builds and drift from the main product.
2. Redirecting YUNDU traffic to `www.linx2.ai?aff=YUNDU`, which would remove the promotion hostname from the browser and make attribution easier to lose.
3. Giving the web application host Caddy or Docker privileges, which would turn an admin-web compromise into a host-level compromise.
4. A wildcard hostname and wildcard origin certificate, which would add Cloudflare certificate management and would accept a broader hostname surface than this first channel requires.

## Fixed Channel Identity

The first promotion channel has one immutable mapping:

```text
hostname:       yundu.linx2.ai
affiliate code: YUNDU
owner email:    yundu@promote.invalid
owner username: YUNDU Promotion Channel
owner status:   disabled
```

The old `ucl.linx2.ai`, `ucl@promote.invalid`, and `UCL` names are not created or retained as aliases.

The `YUNDU` code must not be renamed or reassigned. The current schema stores an invitee's `inviter_id`, then resolves the inviter's current `aff_code`; it does not snapshot the original code string on the invitee row.

## Frontend Channel Resolution

Add a small promotion-channel utility with an explicit hostname map. It must use exact, normalized hostname equality rather than suffix matching.

For `yundu.linx2.ai`, bootstrap performs the following before mounting the Vue application:

1. Resolve the channel as `YUNDU`.
2. Store `YUNDU` through the existing affiliate referral storage contract.
3. Preserve the existing 30-day TTL.
4. Initialize the existing 51.LA collector for this allowed hostname.

For `linx2.ai`, `www.linx2.ai`, local development, preview hosts, IP addresses, and unknown hosts, no promotion channel is inferred.

The channel hostname is authoritative on the promotion origin:

- A different `?aff=` or `?aff_code=` value cannot override `YUNDU` on `yundu.linx2.ai`.
- The registration form submits `YUNDU` even if a stale referral value exists in local storage.
- The visible Affiliate Code input remains in the existing form layout, is prefilled with `YUNDU`, and is read-only on the YUNDU hostname.
- Main-site registration preserves its current editable/query-driven affiliate behavior.

The resulting policy is last-promotion-host attribution. Visiting the YUNDU hostname updates the stored promotion attribution to `YUNDU`. Existing registered users who already have an inviter remain unchanged because the backend does not rebind an established inviter.

## Registration Data Flow

### Email registration

```text
yundu.linx2.ai visit
  -> frontend stores YUNDU
  -> user opens /register
  -> form submits aff_code=YUNDU
  -> email verification preserves aff_code in the existing registration session
  -> successful account creation binds inviter_id to the YUNDU owner
  -> admin Affiliate invitation records show the new user under YUNDU
```

No registration-form submission, verification-code request, or failed registration counts as a registered user. The source of truth is a successfully created account with the affiliate relationship stored in `user_affiliates`.

### GitHub and Google registration

Production GitHub and Google callback URLs are fixed to `www.linx2.ai`. Their OAuth state cookies are host-only. Starting OAuth on YUNDU and receiving the callback on WWW would therefore fail state validation.

On the YUNDU hostname, the GitHub and Google buttons must navigate to an absolute start URL on the canonical origin:

```text
https://www.linx2.ai/api/v1/auth/oauth/{provider}/start
```

The start URL includes the existing internal redirect target and `aff_code=YUNDU`. WWW sets the OAuth state and affiliate cookies, the provider returns to the configured WWW callback, and the backend binds `YUNDU` when it creates the user. OAuth completion lands on the existing WWW frontend callback and then the canonical dashboard.

On the main hostname, OAuth start behavior remains relative and unchanged.

## 51.LA Visit Reporting

Reuse the existing public collector configuration:

```text
id: 3QEWeLJeam88CaLO
ck: 3QEWeLJeam88CaLO
```

Code changes add only `yundu.linx2.ai` to the production host allowlist. They do not create another collector ID.

The existing 51.LA application must add `yundu.linx2.ai` as an exact allowed statistics domain while retaining domain strong matching. Main-site and YUNDU data share one 51.LA application. YUNDU visits are inspected by filtering visited pages or visit URLs for the `yundu.linx2.ai` hostname.

51.LA loading remains asynchronous and non-blocking. SDK download or collection failures cannot block rendering, login, registration, or API requests.

## Internal Channel Account Provisioning

The channel owner is environment-specific operational data. It must not be seeded by a repository SQL migration, hardcoded into startup, or stored with a plaintext password in Git or deployment files.

Provision it once through the existing authenticated admin user and Affiliate APIs:

1. Confirm that `yundu@promote.invalid` does not belong to an active or soft-deleted user that would conflict.
2. Confirm that affiliate code `YUNDU` is unassigned.
3. Create a normal user with a generated high-entropy password, zero balance, zero concurrency, zero RPM, no allowed groups, and an explanatory internal note.
4. Set the user's custom affiliate code to `YUNDU`; this also ensures the `user_affiliates` profile exists.
5. Disable the user account.
6. Remove any default subscription automatically assigned by the standard admin creation flow.
7. Verify the disabled user and affiliate profile through the existing read APIs.

The generated password is never committed or placed in a migration. Because the account is disabled, no operator needs to retain or distribute the password. A future decision to make the account interactive must use the normal admin password-reset and status-change workflow.

Affiliate lookup queries only `user_affiliates.aff_code`; they do not require the owner user to be active. The disabled account can therefore continue receiving invitation relationships.

Provisioning is fail-closed and rerunnable. If the email or affiliate code already exists but does not match the expected disabled YUNDU owner, the operation stops without overwriting or reassigning anything.

## Cloudflare and Caddy

Cloudflare receives one explicit proxied DNS record:

```text
type:    A
name:    yundu
target:  45.78.74.84
proxy:   enabled
```

Host Caddy receives one explicit site block:

```caddy
yundu.linx2.ai {
	reverse_proxy 127.0.0.1:8080
}
```

Caddy obtains and renews an individual origin certificate for this hostname. No wildcard certificate or Cloudflare DNS API token is required.

Before editing production Caddy, back up the existing file. Validate the full configuration before reload. A validation failure leaves the running configuration untouched. Reload Caddy only after validation succeeds; do not restart PostgreSQL, Redis, or the application containers for the Caddy change.

The Caddy site block and Cloudflare DNS record are one-time persistent infrastructure. Normal admin binary updates and database migrations neither modify nor remove them.

## Reproducible Fresh Deployment

Extend `deploy/docker-deploy.sh` with an optional, validated list such as:

```text
ADDITIONAL_DOMAINS=yundu.linx2.ai
```

The deploy script must:

- Parse and trim the configured host list.
- Validate every hostname with the existing domain validator.
- Reject duplicates and conflicts with the primary/apex domains.
- Render one reverse-proxy site block per additional hostname in the managed Caddy file.
- Back up existing managed and root Caddy files.
- Format and validate the complete Caddy configuration before reload.
- Restore backups if generation, formatting, validation, or reload fails.

Document the variable in `.env.example` and deployment documentation.

This change makes new installs and disaster recovery reproducible. It does not make the current admin updater execute host deployment scripts: release updates extract only the application binary.

## Admin Update Boundary

Do not add Caddy mutation to `UpdateService`, the restart endpoint, or SQL migrations.

The post-release admin flow remains:

```text
download and verify release
  -> replace application binary
  -> request restart
  -> Docker restarts the current container
  -> embedded database migrations run
  -> application begins serving
```

Once the one-time DNS/Caddy setup exists, YUNDU continues working through this flow without reconfiguration.

The current Docker update implementation changes the executable in the current container layer rather than updating the image. The inspected production instance demonstrates this divergence: its image label is older than its running application version. A later container recreation can restore the image's embedded binary. Correcting Docker-aware update semantics is a separate task and is not required for YUNDU domain persistence.

## Security and Privacy

- Match only the exact configured promotion hostname.
- Never infer affiliate codes from arbitrary subdomain text.
- Never mount `/etc/caddy` or the Docker socket into the application for this feature.
- Never place Cloudflare credentials, account plaintext passwords, or admin tokens in source control.
- Use `promote.invalid`, a non-deliverable reserved domain, to avoid sending password-reset or notification mail to an unrelated real domain owner.
- Keep the promotion account disabled and without usable quota.
- Continue using the existing 51.LA privacy and content-security-policy allowance; do not add another analytics vendor.
- Treat client-side attribution as protection against accidental loss, not as tamper-proof security. A user can still forge a direct registration request. Server-authoritative host attribution would require a separate backend design.

## Failure Handling and Rollback

- Unknown host: do not infer or submit a promotion code.
- Missing `YUNDU` affiliate owner: report the setup error and do not enable DNS traffic.
- Account or code conflict: stop without overwriting existing records.
- 51.LA failure: leave product behavior unaffected.
- OAuth start construction failure: do not silently fall back to a cross-host-invalid flow; surface the existing authentication error behavior.
- Caddy validation failure: do not reload.
- Caddy reload failure: restore the backup and reload the previous validated configuration.
- Promotion-domain incident: remove or disable the Cloudflare record and restore the previous Caddy file; the main site remains available.
- Application rollback: the DNS, Caddy block, and disabled account remain. A binary older than this feature still serves the application through YUNDU but stops automatic affiliate inference and YUNDU analytics loading.

## Testing

### Automated frontend tests

- Exact YUNDU hostname resolves to affiliate code `YUNDU`.
- Main, local, preview, IP, and unknown hosts do not resolve to a promotion channel.
- Bootstrap stores YUNDU with the existing referral TTL contract.
- YUNDU overrides query and stale-storage affiliate values.
- The registration Affiliate Code input is prefilled and read-only on YUNDU.
- Registration submits `YUNDU` through both direct and email-verification paths.
- Main-site registration behavior remains unchanged.
- GitHub and Google use the absolute WWW start origin on YUNDU and include `aff_code=YUNDU`.
- GitHub and Google retain existing relative behavior on the main origin.
- 51.LA initializes with the existing collector on YUNDU in production.
- 51.LA remains disabled for development and unknown hosts.

### Deployment-script tests

- No additional domain preserves current output.
- One valid additional domain renders exactly one proxy block.
- Multiple domains render deterministically without duplicates.
- Invalid, duplicate, primary-domain, and apex-domain entries fail before any reload.
- Backup restoration runs after format, validation, or reload failure.

### Verification commands

- Run focused frontend unit tests.
- Run frontend type checking.
- Run the frontend production build.
- Run deploy-script tests covering Caddy generation.
- Run `caddy validate` on the production candidate configuration before reload.

### Production smoke checks

- DNS resolves through Cloudflare.
- Direct origin SNI presents a valid certificate for `yundu.linx2.ai`.
- `/`, `/home`, and `/register` return the embedded frontend.
- Public settings and registration support APIs remain same-origin on YUNDU.
- `linx2.ai` still redirects to `www.linx2.ai`.
- `www.linx2.ai` homepage, registration, and health checks remain healthy.
- A YUNDU page visit appears in the existing 51.LA application under the YUNDU hostname.
- The disabled YUNDU account and `YUNDU` code are visible through existing admin Affiliate APIs.

Do not create a disposable production registration merely for smoke testing. Confirm the first real successful YUNDU registration through the Affiliate invitation records.

## Production Rollout Order

1. Complete automated tests and build verification locally.
2. Back up the production Caddyfile.
3. Provision and verify the disabled YUNDU channel owner.
4. Add `yundu.linx2.ai` to the existing 51.LA application's strongly matched domains.
5. Publish and install the application release containing exact-host attribution.
6. Add the proxied Cloudflare `yundu` DNS record.
7. Add the explicit YUNDU Caddy block.
8. Validate and reload Caddy.
9. Run production DNS, TLS, route, API, main-site regression, and account/code checks.
10. Confirm 51.LA receives a YUNDU visit.
11. Confirm the first real YUNDU registration through existing Affiliate records.

Every production mutation in this sequence requires explicit user confirmation immediately before execution, consistent with the repository's production-investigation policy.

## Acceptance Criteria

- `https://yundu.linx2.ai` serves the same current homepage as the main application.
- `https://yundu.linx2.ai/register` serves the same registration experience with a fixed read-only `YUNDU` code.
- Email, GitHub, and Google registrations created from YUNDU bind to the disabled YUNDU owner.
- Main-site affiliate and OAuth behavior is unchanged.
- The existing 51.LA collector loads on YUNDU and its traffic can be filtered by hostname.
- Real YUNDU registrations are visible in existing admin Affiliate invitation records.
- The YUNDU owner is disabled, has no usable quota, and is not seeded through migrations.
- Cloudflare and Caddy changes persist across normal admin binary updates.
- Fresh deployment tooling can reproduce additional-domain Caddy blocks.
- The application updater has no host Caddy, Cloudflare, or Docker privileges.
