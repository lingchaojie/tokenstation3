# YUNDU Promotion Domain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve the existing application at `yundu.linx2.ai`, force new registrations from that exact host to affiliate code `YUNDU`, preserve GitHub/Google attribution through the canonical WWW OAuth callbacks, collect visits with the existing 51.LA ID, and make additional Caddy domains reproducible for fresh deployments.

**Architecture:** Add an exact-host frontend promotion registry that sits above the existing affiliate referral storage. Keep the main-site registration contract unchanged, but make YUNDU authoritative and route its email OAuth start navigation through `www.linx2.ai`. Treat Cloudflare, host Caddy, 51.LA domain configuration, and the disabled channel-owner account as one-time production operations outside binary updates and SQL migrations.

**Tech Stack:** Vue 3, TypeScript, Vue Router, Vitest, Vue Test Utils, Bash, Caddy, Docker Compose, Cloudflare DNS, PostgreSQL, existing Go affiliate/admin APIs, 51.LA V6.

**Design:** `docs/superpowers/specs/2026-07-16-yundu-promotion-domain-design.md`

---

## Scope And File Map

No Go backend file or SQL migration changes in this plan.

Create:

- `frontend/src/utils/promotionChannel.ts` - exact hostname-to-channel registry and promotion-aware affiliate resolution.
- `frontend/src/utils/__tests__/promotionChannel.spec.ts` - pure hostname, precedence, storage, and OAuth-origin tests.
- `frontend/src/__tests__/promotionBootstrapSource.spec.ts` - verifies attribution initializes before analytics and app mount.
- `frontend/src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts` - locks the RegisterView integration contract.
- `deploy/tests/docker-deploy-test.sh` - pure Bash tests for additional-domain parsing, Caddy rendering, and backup restoration.

Modify:

- `frontend/src/main.ts` - initialize promotion attribution before analytics and Vue mount.
- `frontend/src/views/auth/RegisterView.vue` - make YUNDU authoritative, prefilled, and read-only.
- `frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts` - verify preserved affiliate submission after email verification.
- `frontend/src/components/auth/EmailOAuthButtons.vue` - use the canonical WWW start origin on YUNDU.
- `frontend/src/components/auth/__tests__/EmailOAuthButtons.spec.ts` - verify YUNDU and main-origin OAuth URLs.
- `frontend/src/utils/analytics51la.ts` - allow the YUNDU production hostname with the existing collector.
- `frontend/src/utils/__tests__/analytics51la.spec.ts` - verify YUNDU inclusion and unknown-host exclusion.
- `deploy/docker-deploy.sh` - parse and render `ADDITIONAL_DOMAINS` without changing the admin binary updater.
- `deploy/.env.example` - document deployment-script-only domain inputs.
- `deploy/README.md` - document additional domains, persistence, and the admin updater boundary.

Production-only operations after a release exists:

- Create and disable `yundu@promote.invalid` through existing admin flows.
- Assign its immutable affiliate code `YUNDU`.
- Add `yundu.linx2.ai` to the existing 51.LA application's strongly matched domains.
- Add the proxied Cloudflare DNS record.
- Back up, validate, and reload the host Caddy configuration.

---

### Task 1: Add The Exact-Host Promotion Registry And Bootstrap

**Files:**

- Create: `frontend/src/utils/promotionChannel.ts`
- Create: `frontend/src/utils/__tests__/promotionChannel.spec.ts`
- Create: `frontend/src/__tests__/promotionBootstrapSource.spec.ts`
- Modify: `frontend/src/main.ts:6-22`

- [ ] **Step 1: Write the failing promotion-channel tests**

Create `frontend/src/utils/__tests__/promotionChannel.spec.ts`:

```ts
import { beforeEach, describe, expect, it } from 'vitest'

import { loadAffiliateReferralCode, storeAffiliateReferralCode } from '@/utils/oauthAffiliate'
import {
  getPromotionOAuthOrigin,
  initializePromotionChannelAttribution,
  resolvePromotionAffiliateCode,
  resolvePromotionChannel
} from '@/utils/promotionChannel'

describe('promotionChannel', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('matches only the normalized YUNDU hostname', () => {
    expect(resolvePromotionChannel('yundu.linx2.ai')).toMatchObject({
      affiliateCode: 'YUNDU',
      oauthOrigin: 'https://www.linx2.ai'
    })
    expect(resolvePromotionChannel('YUNDU.LINX2.AI.')).toMatchObject({
      affiliateCode: 'YUNDU'
    })
    expect(resolvePromotionChannel('preview.yundu.linx2.ai')).toBeNull()
    expect(resolvePromotionChannel('other.linx2.ai')).toBeNull()
    expect(resolvePromotionChannel('linx2.ai')).toBeNull()
    expect(resolvePromotionChannel('www.linx2.ai')).toBeNull()
    expect(resolvePromotionChannel('localhost')).toBeNull()
    expect(resolvePromotionChannel('127.0.0.1')).toBeNull()
  })

  it('makes the promotion hostname authoritative over query and stored codes', () => {
    storeAffiliateReferralCode('STALE')

    expect(resolvePromotionAffiliateCode(['OTHER'], 'yundu.linx2.ai')).toBe('YUNDU')
    expect(loadAffiliateReferralCode()).toBe('YUNDU')
  })

  it('preserves existing affiliate resolution outside promotion hosts', () => {
    expect(resolvePromotionAffiliateCode([' AFF123 '], 'www.linx2.ai')).toBe('AFF123')
    expect(loadAffiliateReferralCode()).toBe('AFF123')
  })

  it('initializes the 30-day referral storage contract at bootstrap', () => {
    expect(initializePromotionChannelAttribution('yundu.linx2.ai')).toMatchObject({
      affiliateCode: 'YUNDU'
    })
    expect(loadAffiliateReferralCode()).toBe('YUNDU')
  })

  it('returns a canonical oauth origin only for YUNDU', () => {
    expect(getPromotionOAuthOrigin('yundu.linx2.ai')).toBe('https://www.linx2.ai')
    expect(getPromotionOAuthOrigin('www.linx2.ai')).toBe('')
  })
})
```

Create `frontend/src/__tests__/promotionBootstrapSource.spec.ts`:

```ts
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const srcDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = readFileSync(resolve(srcDir, 'main.ts'), 'utf8')

describe('promotion attribution bootstrap', () => {
  it('initializes attribution before analytics and app mounting', () => {
    const attributionIndex = source.indexOf('initializePromotionChannelAttribution()')
    const analyticsIndex = source.indexOf('init51laAnalytics()')
    const mountIndex = source.indexOf("app.mount('#app')")

    expect(attributionIndex).toBeGreaterThan(-1)
    expect(analyticsIndex).toBeGreaterThan(attributionIndex)
    expect(mountIndex).toBeGreaterThan(analyticsIndex)
  })
})
```

- [ ] **Step 2: Run the tests and confirm the missing module/bootstrap failures**

Run:

```bash
cd frontend
pnpm exec vitest run src/utils/__tests__/promotionChannel.spec.ts src/__tests__/promotionBootstrapSource.spec.ts
```

Expected: FAIL because `@/utils/promotionChannel` does not exist and `main.ts` does not call `initializePromotionChannelAttribution()`.

- [ ] **Step 3: Implement the exact-host promotion utility**

Create `frontend/src/utils/promotionChannel.ts`:

```ts
import {
  resolveAffiliateReferralCode,
  storeAffiliateReferralCode
} from '@/utils/oauthAffiliate'

export interface PromotionChannel {
  affiliateCode: string
  oauthOrigin: string
}

const PROMOTION_CHANNELS: Readonly<Record<string, PromotionChannel>> = Object.freeze({
  'yundu.linx2.ai': Object.freeze({
    affiliateCode: 'YUNDU',
    oauthOrigin: 'https://www.linx2.ai'
  })
})

function normalizeHostname(hostname: string): string {
  return hostname.trim().toLowerCase().replace(/\.$/, '')
}

function runtimeHostname(): string {
  return typeof window === 'undefined' ? '' : window.location.hostname
}

export function resolvePromotionChannel(hostname: string): PromotionChannel | null {
  return PROMOTION_CHANNELS[normalizeHostname(hostname)] ?? null
}

export function getCurrentPromotionChannel(): PromotionChannel | null {
  return resolvePromotionChannel(runtimeHostname())
}

export function initializePromotionChannelAttribution(
  hostname = runtimeHostname()
): PromotionChannel | null {
  const channel = resolvePromotionChannel(hostname)
  if (channel) {
    storeAffiliateReferralCode(channel.affiliateCode)
  }
  return channel
}

export function resolvePromotionAffiliateCode(
  fallbackValues: readonly unknown[] = [],
  hostname = runtimeHostname()
): string {
  const channel = resolvePromotionChannel(hostname)
  if (channel) {
    storeAffiliateReferralCode(channel.affiliateCode)
    return channel.affiliateCode
  }
  return resolveAffiliateReferralCode(...fallbackValues)
}

export function getPromotionOAuthOrigin(hostname = runtimeHostname()): string {
  return resolvePromotionChannel(hostname)?.oauthOrigin ?? ''
}
```

- [ ] **Step 4: Initialize promotion attribution before analytics**

Modify `frontend/src/main.ts`:

```ts
import { init51laAnalytics } from '@/utils/analytics51la'
import { initializePromotionChannelAttribution } from '@/utils/promotionChannel'
```

Then keep the bootstrap order exact:

```ts
initThemeClass()
initializePromotionChannelAttribution()
init51laAnalytics()
```

- [ ] **Step 5: Run the focused tests**

Run:

```bash
cd frontend
pnpm exec vitest run src/utils/__tests__/promotionChannel.spec.ts src/__tests__/promotionBootstrapSource.spec.ts
```

Expected: 2 test files PASS.

- [ ] **Step 6: Commit the promotion registry**

```bash
git add frontend/src/main.ts frontend/src/utils/promotionChannel.ts frontend/src/utils/__tests__/promotionChannel.spec.ts frontend/src/__tests__/promotionBootstrapSource.spec.ts
git commit -m "feat(frontend): add promotion channel attribution"
```

---

### Task 2: Make YUNDU Authoritative In Email Registration

**Files:**

- Create: `frontend/src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts`
- Modify: `frontend/src/views/auth/RegisterView.vue:185-202,333-350,420-470,829-914`
- Modify: `frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts:410-455`

- [ ] **Step 1: Write the failing RegisterView integration contract**

Create `frontend/src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts`:

```ts
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const authDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = readFileSync(resolve(authDir, 'RegisterView.vue'), 'utf8')

describe('RegisterView promotion channel contract', () => {
  it('keeps the affiliate field visible but read-only on promotion hosts', () => {
    expect(source).toContain(':readonly="promotionAffiliateLocked"')
    expect(source).toContain(':aria-readonly="promotionAffiliateLocked"')
  })

  it('uses promotion-aware resolution for query sync and submission', () => {
    expect(source).toContain(
      'resolvePromotionAffiliateCode([route.query.aff, route.query.aff_code])'
    )
    expect(source).toContain(
      'resolvePromotionAffiliateCode([formData.aff_code, loadAffiliateReferralCode()])'
    )
    expect(source.match(/\.\.\.\(affCode \? \{ aff_code: affCode \} : \{\}\)/g)).toHaveLength(2)
  })
})
```

- [ ] **Step 2: Run the source contract and confirm it fails**

Run:

```bash
cd frontend
pnpm exec vitest run src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts
```

Expected: FAIL because the read-only binding and promotion-aware resolver calls are absent.

- [ ] **Step 3: Wire promotion-aware resolution into RegisterView**

Replace the existing affiliate utility import in `frontend/src/views/auth/RegisterView.vue` with:

```ts
import {
  clearAffiliateReferralCode,
  loadAffiliateReferralCode
} from '@/utils/oauthAffiliate'
import {
  getCurrentPromotionChannel,
  resolvePromotionAffiliateCode
} from '@/utils/promotionChannel'
```

Add the stable host-derived state after the store declarations:

```ts
const promotionAffiliateLocked = computed(() => getCurrentPromotionChannel() !== null)
```

Replace `syncAffiliateReferralCode` with:

```ts
function syncAffiliateReferralCode(): string {
  const code = resolvePromotionAffiliateCode([route.query.aff, route.query.aff_code])
  if (code) {
    formData.aff_code = code
  }
  return code
}
```

Replace submission affiliate selection with:

```ts
const affCode = resolvePromotionAffiliateCode([
  formData.aff_code,
  loadAffiliateReferralCode()
])
if (affCode) {
  formData.aff_code = affCode
}
```

Keep the existing email-verification `register_data` payload and direct `authStore.register` payload unchanged apart from receiving this resolved `affCode`.

- [ ] **Step 4: Make the existing affiliate input read-only on YUNDU**

Modify the existing `#aff_code` input without removing it from the layout:

```vue
<input
  id="aff_code"
  v-model="formData.aff_code"
  type="text"
  autocomplete="off"
  :disabled="registrationActionDisabled"
  :readonly="promotionAffiliateLocked"
  :aria-readonly="promotionAffiliateLocked"
  class="input"
  :placeholder="t('auth.affiliateCodePlaceholder')"
/>
```

- [ ] **Step 5: Add an email-verification affiliate regression test**

Add this case to `frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts` after the normal email registration test:

```ts
it('preserves the YUNDU affiliate code through verified email registration', async () => {
  sessionStorage.setItem(
    'register_data',
    JSON.stringify({
      email: 'yundu-user@example.com',
      password: 'secret-456',
      aff_code: 'YUNDU'
    })
  )
  registerMock.mockResolvedValue({})

  const wrapper = mount(EmailVerifyView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true,
        TurnstileWidget: true,
        transition: false
      }
    }
  })

  await flushPromises()
  await wrapper.get('#code').setValue('654321')
  await wrapper.get('form').trigger('submit.prevent')
  await flushPromises()

  expect(registerMock).toHaveBeenCalledWith({
    email: 'yundu-user@example.com',
    password: 'secret-456',
    verify_code: '654321',
    turnstile_token: undefined,
    promo_code: undefined,
    invitation_code: undefined,
    aff_code: 'YUNDU'
  })
})
```

- [ ] **Step 6: Run the registration tests**

Run:

```bash
cd frontend
pnpm exec vitest run src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts src/views/auth/__tests__/EmailVerifyView.spec.ts src/utils/__tests__/promotionChannel.spec.ts
```

Expected: 3 test files PASS.

- [ ] **Step 7: Commit the registration behavior**

```bash
git add frontend/src/views/auth/RegisterView.vue frontend/src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts
git commit -m "feat(auth): lock yundu affiliate registration"
```

---

### Task 3: Route YUNDU GitHub And Google OAuth Through WWW

**Files:**

- Modify: `frontend/src/components/auth/EmailOAuthButtons.vue:31-85`
- Modify: `frontend/src/components/auth/__tests__/EmailOAuthButtons.spec.ts`

- [ ] **Step 1: Add the failing YUNDU OAuth test**

Extend the location test shape in `EmailOAuthButtons.spec.ts`:

```ts
const locationState = vi.hoisted(() => ({
  current: {
    href: 'http://localhost/register?aff=AFF123',
    hostname: 'localhost'
  } as { href: string; hostname: string }
}))
```

Reset both values in `beforeEach`, then add:

```ts
it.each(['github', 'google'] as const)(
  'starts YUNDU %s oauth on the canonical WWW origin',
  async (provider) => {
  routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
  locationState.current.hostname = 'yundu.linx2.ai'
  locationState.current.href = 'https://yundu.linx2.ai/register?aff=OTHER'

  const wrapper = mount(EmailOAuthButtons, {
    props: {
      githubEnabled: provider === 'github',
      googleEnabled: provider === 'google',
      affCode: 'OTHER'
    },
    global: {
      stubs: {
        GitHubMark: true,
        GoogleMark: true
      }
    }
  })

  await wrapper.get('button').trigger('click')

  expect(locationState.current.href).toBe(
    `https://www.linx2.ai/api/v1/auth/oauth/${provider}/start?redirect=%2Fdashboard&aff_code=YUNDU`
  )
  expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('YUNDU')
  expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBe(provider)
  }
)
```

Keep the existing localhost test as the main-origin regression that expects a relative `/api/v1/...` URL.

- [ ] **Step 2: Run the component test and confirm it fails**

Run:

```bash
cd frontend
pnpm exec vitest run src/components/auth/__tests__/EmailOAuthButtons.spec.ts
```

Expected: FAIL because the component still uses `OTHER` and a relative YUNDU start URL.

- [ ] **Step 3: Use promotion-aware affiliate and origin resolution**

Replace the affiliate import in `EmailOAuthButtons.vue` with:

```ts
import { storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'
import {
  getPromotionOAuthOrigin,
  resolvePromotionAffiliateCode
} from '@/utils/promotionChannel'
```

Replace the affiliate and base URL construction in `startLogin` with:

```ts
const affiliateCode = resolvePromotionAffiliateCode([
  props.affCode,
  route.query.aff,
  route.query.aff_code
])
storeOAuthAffiliateCode(affiliateCode)
window.sessionStorage.setItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY, provider)

const apiBase = (import.meta.env.VITE_API_BASE_URL as string | undefined) || '/api/v1'
const normalized = apiBase.replace(/\/$/, '')
const promotionOrigin = getPromotionOAuthOrigin()
const startBase = promotionOrigin
  ? new URL(normalized, `${promotionOrigin}/`).toString().replace(/\/$/, '')
  : normalized
```

Build the final URL from `startBase`:

```ts
const startURL = `${startBase}/auth/oauth/${provider}/start?${params.toString()}`
window.location.href = startURL
```

`new URL` preserves an already absolute API base while resolving the normal `/api/v1` base against `https://www.linx2.ai` on YUNDU.

- [ ] **Step 4: Run the EmailOAuthButtons and promotion utility tests**

Run:

```bash
cd frontend
pnpm exec vitest run src/components/auth/__tests__/EmailOAuthButtons.spec.ts src/utils/__tests__/promotionChannel.spec.ts
```

Expected: 2 test files PASS; localhost remains relative and YUNDU becomes absolute with `aff_code=YUNDU`.

- [ ] **Step 5: Commit the OAuth fix**

```bash
git add frontend/src/components/auth/EmailOAuthButtons.vue frontend/src/components/auth/__tests__/EmailOAuthButtons.spec.ts
git commit -m "fix(auth): route yundu oauth through canonical host"
```

---

### Task 4: Enable Existing 51.LA Collection On YUNDU

**Files:**

- Modify: `frontend/src/utils/analytics51la.ts:18-26`
- Modify: `frontend/src/utils/__tests__/analytics51la.spec.ts:31-125`

- [ ] **Step 1: Add failing YUNDU analytics expectations**

Extend the existing hostname test:

```ts
expect(shouldEnable51laAnalytics({
  isProduction: true,
  hostname: 'yundu.linx2.ai'
})).toBe(true)
expect(shouldEnable51laAnalytics({
  isProduction: true,
  hostname: 'other.linx2.ai'
})).toBe(false)
```

Add an SDK injection case:

```ts
it('injects the existing 51.LA collector on yundu.linx2.ai production', () => {
  init51laAnalytics({
    isProduction: true,
    hostname: 'yundu.linx2.ai',
    window,
    document
  })

  expect(document.getElementById('LA_COLLECT')).not.toBeNull()
  expect(analyticsWindow.LA?.ids).toHaveLength(1)
  expect(analyticsWindow.LA?.ids?.[0]).toMatchObject(LA_COLLECT_CONFIG)
})
```

- [ ] **Step 2: Run the analytics test and confirm it fails**

Run:

```bash
cd frontend
pnpm exec vitest run src/utils/__tests__/analytics51la.spec.ts
```

Expected: FAIL because `yundu.linx2.ai` is not in `OFFICIAL_HOSTNAMES`.

- [ ] **Step 3: Add only the exact YUNDU hostname**

Change the allowlist in `frontend/src/utils/analytics51la.ts` to:

```ts
const OFFICIAL_HOSTNAMES = new Set([
  'linx2.ai',
  'www.linx2.ai',
  'yundu.linx2.ai'
])
```

Do not change `LA_COLLECT_CONFIG`; retain the existing ID and CK.

- [ ] **Step 4: Run the analytics tests**

Run:

```bash
cd frontend
pnpm exec vitest run src/utils/__tests__/analytics51la.spec.ts
```

Expected: PASS, including rejection of `other.linx2.ai` and preview hosts.

- [ ] **Step 5: Commit the analytics host allowlist**

```bash
git add frontend/src/utils/analytics51la.ts frontend/src/utils/__tests__/analytics51la.spec.ts
git commit -m "feat(analytics): collect yundu visits"
```

---

### Task 5: Make Additional Caddy Domains Reproducible

**Files:**

- Create: `deploy/tests/docker-deploy-test.sh`
- Modify: `deploy/docker-deploy.sh:20-430,560-570`
- Modify: `deploy/.env.example:12-25`
- Modify: `deploy/README.md:65-115,278-315`

- [ ] **Step 1: Write failing pure Bash tests**

Create `deploy/tests/docker-deploy-test.sh`:

```bash
#!/bin/bash

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-docker-deploy-test.XXXXXX")"

cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_equals() {
    local actual="$1"
    local expected="$2"
    local message="$3"
    [[ "${actual}" == "${expected}" ]] || fail "${message}: expected '${expected}', got '${actual}'"
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    [[ "${haystack}" == *"${needle}"* ]] || fail "Expected output to contain: ${needle}"
}

if ! grep -Fq 'if [ "${DOCKER_DEPLOY_SOURCE_ONLY:-0}" != "1" ]; then' "${DEPLOY_DIR}/docker-deploy.sh"; then
    fail 'docker-deploy.sh cannot be sourced safely for unit tests'
fi

export DOCKER_DEPLOY_SOURCE_ONLY=1
source "${DEPLOY_DIR}/docker-deploy.sh"

empty="$(normalize_additional_domains '' 'www.example.com' 'example.com')"
assert_equals "${empty}" '' 'Empty additional-domain list changed'

normalized="$(normalize_additional_domains ' YUNDU.LINX2.AI, api.example.com ' 'www.example.com' 'example.com')"
assert_equals "${normalized}" $'yundu.linx2.ai\napi.example.com' 'Unexpected normalized domains'

single="$(normalize_additional_domains 'YUNDU.EXAMPLE.COM' 'www.example.com' 'example.com')"
assert_equals "${single}" 'yundu.example.com' 'Single additional domain was not normalized'

if normalize_additional_domains 'yundu.linx2.ai,YUNDU.LINX2.AI' 'www.example.com' 'example.com' >/dev/null 2>&1; then
    fail 'Duplicate domains were accepted'
fi
if normalize_additional_domains 'www.example.com' 'www.example.com' 'example.com' >/dev/null 2>&1; then
    fail 'Primary domain conflict was accepted'
fi
if normalize_additional_domains 'example.com' 'www.example.com' 'example.com' >/dev/null 2>&1; then
    fail 'Apex domain conflict was accepted'
fi
if normalize_additional_domains 'https://bad.example.com' 'www.example.com' 'example.com' >/dev/null 2>&1; then
    fail 'Invalid domain was accepted'
fi

rendered="$(render_managed_caddyfile 'www.example.com' 'example.com' '8080' "${normalized}")"
assert_contains "${rendered}" 'redir https://www.example.com{uri} permanent'
assert_contains "${rendered}" $'yundu.linx2.ai {\n\treverse_proxy 127.0.0.1:8080\n}'
assert_contains "${rendered}" $'api.example.com {\n\treverse_proxy 127.0.0.1:8080\n}'
[[ "$(grep -c '^yundu\.linx2\.ai {$' <<<"${rendered}")" == '1' ]] || fail 'YUNDU rendered more than once'

without_additional="$(render_managed_caddyfile 'www.example.com' 'example.com' '8080' '')"
[[ "${without_additional}" != *'yundu.linx2.ai'* ]] || fail 'Empty list rendered an additional domain'
assert_equals "$(grep -c 'reverse_proxy 127.0.0.1:8080' <<<"${without_additional}")" '1' \
    'Empty list changed the current primary-domain output'

original="${TEST_ROOT}/Caddyfile"
backup="${TEST_ROOT}/Caddyfile.backup"
printf 'changed\n' > "${original}"
printf 'original\n' > "${backup}"
restore_caddy_backup "${original}" "${backup}" 1
[[ "$(<"${original}")" == 'original' ]] || fail 'Existing Caddyfile was not restored'

created="${TEST_ROOT}/new.caddy"
printf 'new\n' > "${created}"
restore_caddy_backup "${created}" '' 0
[[ ! -e "${created}" ]] || fail 'New Caddyfile was not removed during restore'

stub_bin="${TEST_ROOT}/bin"
mkdir -p "${stub_bin}"
printf '%s\n' \
    '#!/bin/bash' \
    'if [ "${1:-}" = "-u" ]; then printf "0\n"; exit 0; fi' \
    'exec /usr/bin/id "$@"' > "${stub_bin}/id"
printf '%s\n' \
    '#!/bin/bash' \
    'if [ "${CADDY_TEST_FAIL_STAGE:-}" = "${1:-}" ]; then exit 1; fi' \
    'exit 0' > "${stub_bin}/caddy"
printf '%s\n' \
    '#!/bin/bash' \
    'if [ "${1:-}" = "is-active" ]; then exit 1; fi' \
    'if { [ "${1:-}" = "reload" ] || [ "${1:-}" = "restart" ]; } && [ "${CADDY_TEST_FAIL_STAGE:-}" = "reload" ]; then exit 1; fi' \
    'exit 0' > "${stub_bin}/systemctl"
chmod +x "${stub_bin}/id" "${stub_bin}/caddy" "${stub_bin}/systemctl"
export PATH="${stub_bin}:${PATH}"

assert_write_failure_restores() {
    local stage="$1"
    local stage_root="${TEST_ROOT}/${stage}"
    local root_caddyfile="${stage_root}/Caddyfile"
    local managed_caddyfile="${stage_root}/sub2api/sub2api.caddy"

    mkdir -p "$(dirname "${managed_caddyfile}")"
    printf 'import %s\n' "${managed_caddyfile}" > "${root_caddyfile}"
    printf 'original managed %s\n' "${stage}" > "${managed_caddyfile}"
    export CADDY_TEST_FAIL_STAGE="${stage}"

    if write_caddyfile \
        'www.example.com' \
        'example.com' \
        '8080' \
        'yundu.example.com' \
        "${root_caddyfile}" \
        "${managed_caddyfile}" >/dev/null 2>&1
    then
        fail "write_caddyfile unexpectedly succeeded during ${stage} failure"
    fi

    assert_equals "$(<"${root_caddyfile}")" "import ${managed_caddyfile}" \
        "Root Caddyfile was not restored after ${stage} failure"
    assert_equals "$(<"${managed_caddyfile}")" "original managed ${stage}" \
        "Managed Caddyfile was not restored after ${stage} failure"
}

assert_write_failure_restores fmt
assert_write_failure_restores validate
assert_write_failure_restores reload
unset CADDY_TEST_FAIL_STAGE

printf 'docker-deploy domain tests passed.\n'
```

Make it executable:

```bash
chmod +x deploy/tests/docker-deploy-test.sh
```

- [ ] **Step 2: Run the script test and confirm the safe source guard fails first**

Run:

```bash
bash deploy/tests/docker-deploy-test.sh
```

Expected: FAIL with `docker-deploy.sh cannot be sourced safely for unit tests`. The test stops before sourcing the current script, so the red test cannot start deployment preparation or download files.

- [ ] **Step 3: Add a source-only test guard without breaking curl-pipe installs**

Replace the unconditional tail call in `deploy/docker-deploy.sh` with:

```bash
if [ "${DOCKER_DEPLOY_SOURCE_ONLY:-0}" != "1" ]; then
    main "$@"
fi
```

Do not use a `BASH_SOURCE == $0` guard because the documented `curl ... | bash` path executes from standard input.

Run the test again:

```bash
bash deploy/tests/docker-deploy-test.sh
```

Expected: FAIL at the first missing normalization/render function, now without executing `main`.

- [ ] **Step 4: Add normalization and pure rendering functions**

Add after `validate_domain_name`:

```bash
normalize_domain_name() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

normalize_additional_domains() {
    local raw="${1:-}"
    local primary
    local apex
    local candidate
    local domain
    local seen='|'

    primary="$(normalize_domain_name "${2:-}")"
    apex="$(normalize_domain_name "${3:-}")"
    raw="${raw//,/ }"

    for candidate in ${raw}; do
        domain="$(normalize_domain_name "${candidate}")"
        if ! validate_domain_name "${domain}"; then
            printf 'Invalid ADDITIONAL_DOMAINS entry: %s\n' "${candidate}" >&2
            return 1
        fi
        if [ "${domain}" = "${primary}" ] || { [ -n "${apex}" ] && [ "${domain}" = "${apex}" ]; }; then
            printf 'ADDITIONAL_DOMAINS conflicts with primary/apex domain: %s\n' "${domain}" >&2
            return 1
        fi
        case "${seen}" in
            *"|${domain}|"*)
                printf 'Duplicate ADDITIONAL_DOMAINS entry: %s\n' "${domain}" >&2
                return 1
                ;;
        esac
        seen="${seen}${domain}|"
        printf '%s\n' "${domain}"
    done
}
```

Add before `write_caddyfile`:

```bash
render_managed_caddyfile() {
    local domain="$1"
    local apex_domain="$2"
    local upstream_port="$3"
    local additional_domains="${4:-}"
    local additional_domain

    printf '%s\n' '# Managed by Sub2API/TokenStation docker-deploy.sh.'
    printf '%s\n' '# To change domains after deployment, edit this file and reload Caddy.'

    if [ -n "${apex_domain}" ]; then
        printf '\n%s {\n\tredir https://%s{uri} permanent\n}\n' "${apex_domain}" "${domain}"
    fi

    printf '\n%s {\n\treverse_proxy 127.0.0.1:%s\n}\n' "${domain}" "${upstream_port}"

    while IFS= read -r additional_domain; do
        [ -n "${additional_domain}" ] || continue
        printf '\n%s {\n\treverse_proxy 127.0.0.1:%s\n}\n' "${additional_domain}" "${upstream_port}"
    done <<< "${additional_domains}"
}
```

- [ ] **Step 5: Replace duplicated Caddy heredocs with the renderer**

Extend `write_caddyfile` with additional-domain and test-path arguments. The last two default to the production paths and are used only by the pure failure-path test:

```bash
local additional_domains="${4:-}"
local caddyfile="${5:-/etc/caddy/Caddyfile}"
local managed_caddyfile="${6:-/etc/caddy/sub2api/sub2api.caddy}"
local managed_dir
managed_dir="$(dirname "${managed_caddyfile}")"
```

Remove the old hardcoded `caddyfile`, `managed_dir`, and `managed_caddyfile` locals so there is only one source of each path.

Replace both current managed-file heredoc branches with one fail-closed write:

```bash
if ! render_managed_caddyfile \
    "${domain}" \
    "${apex_domain}" \
    "${upstream_port}" \
    "${additional_domains}" > "${managed_caddyfile}"
then
    print_warning "Unable to write ${managed_caddyfile}."
    restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
    return 1
fi
```

Keep the existing backup, import, `caddy fmt`, `caddy validate`, enable, reload, restart fallback, and restoration code unchanged.

- [ ] **Step 6: Parse and pass ADDITIONAL_DOMAINS from configure_caddy_if_requested**

Add local state:

```bash
local additional_domains=''
```

After primary/apex validation, normalize the list:

```bash
if ! additional_domains="$(normalize_additional_domains "${ADDITIONAL_DOMAINS:-}" "${domain}" "${apex_domain}")"; then
    print_error 'Invalid ADDITIONAL_DOMAINS configuration.'
    exit 1
fi
```

Pass it to the writer:

```bash
write_caddyfile "$domain" "$apex_domain" "$upstream_port" "$additional_domains"
```

Print one DNS note per additional domain:

```bash
while IFS= read -r additional_domain; do
    [ -n "${additional_domain}" ] || continue
    print_info "  - Point DNS for ${additional_domain} to this server as well."
done <<< "${additional_domains}"
```

In the final custom-domain summary, print each additional HTTPS URL with the same loop.

- [ ] **Step 7: Document deployment-script domain inputs**

Add this commented block near the server settings in `deploy/.env.example`:

```dotenv
# docker-deploy.sh host inputs are passed in the shell environment during initial
# preparation; Docker Compose does not consume them directly.
# DOMAIN=www.example.com
# APEX_DOMAIN=example.com
# ADDITIONAL_DOMAINS=yundu.example.com,partner.example.com
```

Update the README custom-domain example:

```bash
DOMAIN=www.example.com \
APEX_DOMAIN=example.com \
ADDITIONAL_DOMAINS=yundu.example.com,partner.example.com \
sudo -E ./docker-deploy.sh
```

Document that additional domains receive their own reverse-proxy blocks, need their own DNS records, persist across admin binary updates, and are reconstructed only by fresh/manual deployment preparation. Add `ADDITIONAL_DOMAINS` to the environment-variable table as a comma-separated deployment-script input.

- [ ] **Step 8: Run Bash tests and syntax validation**

Run:

```bash
bash deploy/tests/docker-deploy-test.sh
bash -n deploy/docker-deploy.sh
```

Expected:

```text
docker-deploy domain tests passed.
```

`bash -n` exits 0 with no output.

- [ ] **Step 9: Commit deployment reproducibility**

```bash
git add deploy/docker-deploy.sh deploy/tests/docker-deploy-test.sh deploy/.env.example deploy/README.md
git commit -m "feat(deploy): support additional caddy domains"
```

---

### Task 6: Run Full Local Verification

**Files:**

- Verify only; fix failures in the owning task's files and amend that task's commit.

- [ ] **Step 1: Run all focused YUNDU tests together**

```bash
cd frontend
pnpm exec vitest run \
  src/utils/__tests__/promotionChannel.spec.ts \
  src/__tests__/promotionBootstrapSource.spec.ts \
  src/views/auth/__tests__/RegisterPromotionChannelSource.spec.ts \
  src/views/auth/__tests__/EmailVerifyView.spec.ts \
  src/components/auth/__tests__/EmailOAuthButtons.spec.ts \
  src/utils/__tests__/analytics51la.spec.ts
```

Expected: all listed files PASS.

- [ ] **Step 2: Run the complete frontend test suite**

```bash
cd frontend
pnpm test:run
```

Expected: exit 0 with no failed test files.

- [ ] **Step 3: Run frontend type checking**

```bash
cd frontend
pnpm typecheck
```

Expected: exit 0 with no TypeScript errors.

- [ ] **Step 4: Build the embedded production frontend**

```bash
cd frontend
pnpm build
```

Expected: exit 0 and Vite emits the production bundle under `backend/internal/web/dist`.

- [ ] **Step 5: Run deployment script verification**

```bash
bash deploy/tests/docker-deploy-test.sh
bash -n deploy/docker-deploy.sh
```

Expected: domain tests pass, including format/validation/reload failure restoration, and syntax validation exits 0.

- [ ] **Step 6: Check formatting and scope**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intentional implementation files or known pre-existing unrelated files appear.

- [ ] **Step 7: Review the final commit series**

```bash
git log --oneline --decorate -7
```

Expected: separate commits for promotion attribution, registration, OAuth, analytics, deployment domains, and the approved design/plan documentation.

---

### Task 7: Prepare And Execute The Gated Production Rollout

**Files:**

- Production host: `/etc/caddy/Caddyfile`
- Production database: existing `users`, `user_affiliates`, and user-subscription records through current admin flows.
- External configuration: Cloudflare DNS and the existing 51.LA application.

- [ ] **Step 1: Stop and present the verified release candidate**

Report the exact commit SHA, focused/full test counts, typecheck result, build result, Bash test result, and intended production changes. Do not mutate production in this step.

- [ ] **Step 2: Obtain explicit user confirmation for production changes**

The confirmation must cover all of these actions in one clearly enumerated request:

```text
1. Create and disable yundu@promote.invalid.
2. Assign immutable affiliate code YUNDU.
3. Add yundu.linx2.ai to the existing 51.LA domain allowlist.
4. Add the Cloudflare yundu A record pointing to 45.78.74.84 with proxy enabled.
5. Back up, edit, validate, and reload production Caddy.
6. Install/restart the verified application release.
```

Do not continue without an affirmative response.

- [ ] **Step 3: Read production conflicts before creating the channel owner**

Run this read-only transaction through the production PostgreSQL container:

```sql
BEGIN READ ONLY;
SELECT id, email, username, status, deleted_at
FROM users
WHERE lower(email) = 'yundu@promote.invalid';

SELECT user_id, aff_code, inviter_id, aff_code_custom
FROM user_affiliates
WHERE aff_code = 'YUNDU';
COMMIT;
```

Expected: both result sets contain zero rows. Stop on any row and report the conflict.

- [ ] **Step 4: Create the channel owner through the existing authenticated admin API**

The current create-user modal does not expose `notes` or `allowed_groups`, and its edit modal rejects a zero-concurrency form submission. Use the same authenticated admin API used by those screens so all fixed user fields are sent in one request; do not insert the row directly with SQL.

Obtain a current administrator bearer token through the normal admin login, then read it without echo and generate a password locally. Neither value is written to source control:

```bash
read -rsp 'Admin bearer token: ' ADMIN_BEARER_TOKEN
printf '\n'
CHANNEL_PASSWORD="$(openssl rand -base64 48)"
```

Create exactly through the existing endpoint:

```bash
create_response="$({
    jq -n \
        --arg email 'yundu@promote.invalid' \
        --arg password "${CHANNEL_PASSWORD}" \
        --arg username 'YUNDU Promotion Channel' \
        --arg notes 'Internal attribution owner for yundu.linx2.ai' \
        '{
            email: $email,
            password: $password,
            username: $username,
            notes: $notes,
            role: "user",
            balance: 0,
            concurrency: 0,
            rpm_limit: 0,
            allowed_groups: []
        }' |
    curl --fail-with-body --silent --show-error \
        -H "Authorization: Bearer ${ADMIN_BEARER_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data-binary @- \
        https://www.linx2.ai/api/v1/admin/users
})"
printf '%s\n' "${create_response}" | jq .
YUNDU_USER_ID="$(printf '%s\n' "${create_response}" | jq -er '.data.id // .id')"
unset CHANNEL_PASSWORD
```

Expected: HTTP success and a numeric `YUNDU_USER_ID`. Stop if `jq`, the authenticated request, or ID extraction fails. Do not print, retain, or distribute the generated account password.

- [ ] **Step 5: Assign YUNDU, disable the account, and remove default subscriptions**

In the Affiliate custom-user section of `/admin/settings`, select `yundu@promote.invalid` and set its custom code to `YUNDU`.

Return to `/admin/users` and set the account status to `disabled`. In `/admin/subscriptions`, filter by `yundu@promote.invalid` and revoke every automatically assigned active default subscription. In the user's Allowed Groups action, confirm that no exclusive group is selected. Leave balance, concurrency, RPM, and allowed groups at zero/empty.

- [ ] **Step 6: Verify the channel owner read-only**

Run:

```sql
BEGIN READ ONLY;
SELECT u.id, u.email, u.username, u.status, u.balance, u.concurrency,
       u.rpm_limit, u.allowed_groups, u.notes,
       ua.aff_code, ua.aff_code_custom, ua.inviter_id
FROM users u
JOIN user_affiliates ua ON ua.user_id = u.id
WHERE lower(u.email) = 'yundu@promote.invalid';

SELECT id, status, group_id
FROM user_subscriptions
WHERE user_id = (
    SELECT id FROM users WHERE lower(email) = 'yundu@promote.invalid'
)
  AND status = 'active';
COMMIT;
```

Expected: one disabled user with zero balance/concurrency/RPM, empty allowed groups, the exact internal note, `aff_code=YUNDU`, `aff_code_custom=true`, and `inviter_id` null. The active-subscription query returns zero rows. Unset `ADMIN_BEARER_TOKEN` and `YUNDU_USER_ID` after all authenticated provisioning calls are complete.

- [ ] **Step 7: Update the existing 51.LA application's allowed domains**

In the current 51.LA V6 application that owns collector `3QEWeLJeam88CaLO`, add this exact new line while keeping domain strong matching enabled:

```text
yundu.linx2.ai
```

Do not change the collector ID or CK and do not remove `linx2.ai` or `www.linx2.ai`.

- [ ] **Step 8: Install the verified application release**

Publish the verified commit through the repository's existing release workflow only after separate authorization to push/release. Once the release is visible to the current production `UPDATE_GITHUB_REPO`, use the admin update action, restart the service, and wait for `/health` to return 200.

Expected: the reported running version equals the published release version and startup migrations complete without error. No new YUNDU-specific SQL migration is expected.

- [ ] **Step 9: Add the Cloudflare DNS record**

Create exactly:

```text
type:   A
name:   yundu
value:  45.78.74.84
proxy:  enabled
TTL:    automatic
```

Do not create `ucl.linx2.ai` and do not create a wildcard record.

- [ ] **Step 10: Prepare and validate a production Caddy candidate**

Back up `/etc/caddy/Caddyfile`, then add exactly:

```caddy
yundu.linx2.ai {
	reverse_proxy 127.0.0.1:8080
}
```

Validate before replacing/reloading the active configuration:

```bash
caddy fmt --overwrite /tmp/Caddyfile.yundu
caddy validate --config /tmp/Caddyfile.yundu
```

Expected: formatting succeeds and validation reports a valid configuration. If validation fails, leave the active file and running Caddy process unchanged.

- [ ] **Step 11: Install the validated Caddyfile and reload**

After validation, install the candidate as `/etc/caddy/Caddyfile` and run:

```bash
systemctl reload caddy
systemctl is-active caddy
```

Expected: reload exits 0 and status is `active`. On reload failure, restore the timestamped backup and reload the previous validated file.

- [ ] **Step 12: Run production smoke checks**

Run read-only checks:

```bash
dig +short A yundu.linx2.ai
curl --fail --silent --show-error --head https://yundu.linx2.ai/
curl --fail --silent --show-error --head https://yundu.linx2.ai/home
curl --fail --silent --show-error --head https://yundu.linx2.ai/register
curl --fail --silent --show-error --head https://www.linx2.ai/register
curl --silent --show-error --head https://linx2.ai/ | grep -E '^HTTP/|^location: https://www\.linx2\.ai'
curl --fail --silent --show-error https://yundu.linx2.ai/health >/dev/null
curl --fail-with-body --silent --show-error https://yundu.linx2.ai/api/v1/settings/public >/tmp/yundu-public-settings.json
curl --fail-with-body --silent --show-error \
  --resolve yundu.linx2.ai:443:45.78.74.84 \
  https://yundu.linx2.ai/ >/dev/null
```

Expected:

- YUNDU resolves to Cloudflare addresses.
- YUNDU routes return 200 with the embedded frontend.
- YUNDU public settings return valid JSON through the same-origin API.
- Direct-origin SNI for YUNDU completes TLS and serves the application.
- WWW registration remains 200.
- The apex continues redirecting permanently to WWW.

Use a browser on `yundu.linx2.ai/register` to verify that Affiliate Code displays `YUNDU` and is read-only. Do not complete a disposable production registration.

- [ ] **Step 13: Confirm analytics and first real registration**

After one YUNDU visit, confirm the existing 51.LA application shows a visited URL under `yundu.linx2.ai`.

After the first real YUNDU registration, open `/admin/affiliates/invites` and confirm the invitee row reports affiliate code `YUNDU`. Do not rename or reassign the code afterward.

---

## Completion Evidence

The implementation is complete only when all of the following are recorded:

- Focused and complete frontend tests pass.
- Frontend typecheck passes.
- Frontend production build passes.
- Deployment-script tests and `bash -n` pass.
- No Go backend or SQL migration changes were introduced.
- Main-site registration and OAuth regressions remain green.
- Production changes were separately confirmed before execution.
- DNS, TLS, Caddy, YUNDU routes, and WWW routes pass smoke checks.
- The disabled YUNDU owner and immutable code are verified.
- 51.LA receives a YUNDU URL under the existing collector.
- The first real YUNDU registration is visible in existing Affiliate invitation records.
