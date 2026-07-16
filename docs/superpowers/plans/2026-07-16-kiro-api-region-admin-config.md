# KIRO API Region Admin Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an independent KIRO API Region selector to administrator create, edit, and reauthorization workflows, persisting `credentials.api_region` without changing the IAM Identity Center `credentials.region`.

**Architecture:** Keep the backend contract unchanged because it already prefers `api_region` for Q runtime requests. Add a small shared frontend region model for the two commercial regions, defaulting, and legacy-value handling, then use it from the three existing account modals. Direct OAuth, imported-token, and native API-key accounts persist the selected region; relay API-key accounts remain unchanged.

**Tech Stack:** Vue 3 Composition API, TypeScript, existing `Select.vue` component, vue-i18n, Vitest, Vue Test Utils.

---

## File Structure

- `frontend/src/utils/kiroAccount.ts`: own the supported API-region constants, defaulting, and select-option construction used by all three modals.
- `frontend/src/utils/__tests__/kiroAccount.spec.ts`: unit-test supported options, missing-value defaulting, and disabled legacy values.
- `frontend/src/components/account/CreateAccountModal.vue`: render the create selector and write `api_region` for direct OAuth/import/API-key credentials only.
- `frontend/src/components/account/EditAccountModal.vue`: load, render, and save `api_region` independently from IDC `region` for direct KIRO accounts.
- `frontend/src/components/admin/account/ReAuthAccountModal.vue`: load the existing value and merge it into both exchanged and imported OAuth credentials.
- `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`: behaviorally verify independent IDC/API regions during edit.
- `frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts`: assert create/reauth credential paths, stable selectors, and translations remain aligned.
- `frontend/src/i18n/locales/en/admin/accounts.ts`: English API-region labels and help text.
- `frontend/src/i18n/locales/zh/admin/accounts.ts`: Chinese API-region labels and help text.

### Task 1: Shared KIRO API-region model

**Files:**
- Modify: `frontend/src/utils/kiroAccount.ts`
- Test: `frontend/src/utils/__tests__/kiroAccount.spec.ts`

- [ ] **Step 1: Write failing unit tests for supported, default, and legacy values**

Update the test import and add these cases:

```ts
import {
  DEFAULT_KIRO_API_REGION,
  KIRO_API_REGIONS,
  buildKiroAPIRegionOptions,
  isKiroDirectApiKeyAccount,
  isKiroRelayAccount,
  resolveKiroAPIRegion
} from '@/utils/kiroAccount'

it('defines only the two supported commercial Kiro API regions', () => {
  expect(DEFAULT_KIRO_API_REGION).toBe('us-east-1')
  expect(KIRO_API_REGIONS).toEqual(['us-east-1', 'eu-central-1'])
})

it('defaults a missing API region independently of the IDC region', () => {
  expect(resolveKiroAPIRegion(undefined)).toBe('us-east-1')
  expect(resolveKiroAPIRegion('')).toBe('us-east-1')
  expect(resolveKiroAPIRegion(' eu-central-1 ')).toBe('eu-central-1')
})

it('keeps an existing unsupported region as a disabled legacy option', () => {
  const options = buildKiroAPIRegionOptions('eu-north-1', region => `label:${region}`)

  expect(options).toEqual([
    { value: 'us-east-1', label: 'label:us-east-1' },
    { value: 'eu-central-1', label: 'label:eu-central-1' },
    { value: 'eu-north-1', label: 'label:eu-north-1', disabled: true }
  ])
})
```

- [ ] **Step 2: Run the utility test and confirm the new API is missing**

Run:

```bash
cd frontend
npx vitest run src/utils/__tests__/kiroAccount.spec.ts
```

Expected: FAIL because the three new exports do not exist.

- [ ] **Step 3: Implement the shared model**

Add this near the top of `kiroAccount.ts`:

```ts
export const DEFAULT_KIRO_API_REGION = 'us-east-1'
export const KIRO_API_REGIONS = ['us-east-1', 'eu-central-1'] as const

export interface KiroAPIRegionOption {
  value: string
  label: string
  disabled?: boolean
}

export function resolveKiroAPIRegion(value: unknown): string {
  return typeof value === 'string' && value.trim()
    ? value.trim()
    : DEFAULT_KIRO_API_REGION
}

export function buildKiroAPIRegionOptions(
  currentValue: unknown,
  labelFor: (region: string, legacy: boolean) => string
): KiroAPIRegionOption[] {
  const options: KiroAPIRegionOption[] = KIRO_API_REGIONS.map(region => ({
    value: region,
    label: labelFor(region, false)
  }))
  const current = resolveKiroAPIRegion(currentValue)

  if (!KIRO_API_REGIONS.some(region => region === current)) {
    options.push({ value: current, label: labelFor(current, true), disabled: true })
  }

  return options
}
```

- [ ] **Step 4: Run the utility test and confirm it passes**

Run:

```bash
cd frontend
npx vitest run src/utils/__tests__/kiroAccount.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Commit the shared model**

```bash
git add frontend/src/utils/kiroAccount.ts frontend/src/utils/__tests__/kiroAccount.spec.ts
git commit -m "feat(kiro): define supported api regions"
```

### Task 2: Add translations and create-account behavior

**Files:**
- Modify: `frontend/src/i18n/locales/en/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/accounts.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Test: `frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts`

- [ ] **Step 1: Write failing create-contract and translation assertions**

Add a new test to `CreateAccountModal.kiroReference.spec.ts`:

```ts
it('configures the Kiro API region independently in create flows', () => {
  expect(source).toContain('data-testid="kiro-api-region-select-create"')
  expect(source).toContain("const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)")
  expect(source).toContain('credentials.api_region = kiroAPIRegion.value')
  expect(source).toContain('api_region: kiroAPIRegion.value')
  expect(source).toContain("kiroAPIRegion.value = DEFAULT_KIRO_API_REGION")
  expect(enSource).toContain("apiRegionLabel: 'API Region'")
  expect(enSource).toContain("apiRegionUsEast: 'US East (N. Virginia)'")
  expect(enSource).toContain("apiRegionEuCentral: 'Europe (Frankfurt)'")
  expect(zhSource).toContain("apiRegionLabel: 'API Region'")
  expect(zhSource).toContain("apiRegionUsEast: '美国东部（弗吉尼亚北部）'")
  expect(zhSource).toContain("apiRegionEuCentral: '欧洲（法兰克福）'")
})
```

- [ ] **Step 2: Run the contract test and confirm it fails**

Run:

```bash
cd frontend
npx vitest run src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts
```

Expected: FAIL because the selector, state, credential assignments, and translations are absent.

- [ ] **Step 3: Add English and Chinese translations**

Add these keys inside each existing `oauth.kiro` object.

English:

```ts
apiRegionLabel: 'API Region',
apiRegionHint: 'Select the region of this account\'s Kiro/Q Developer Profile. It can differ from the IAM Identity Center region.',
apiRegionUsEast: 'US East (N. Virginia)',
apiRegionEuCentral: 'Europe (Frankfurt)',
apiRegionLegacy: '{region} (current legacy value)',
```

Chinese:

```ts
apiRegionLabel: 'API Region',
apiRegionHint: '请选择该账号 Kiro/Q Developer Profile 所在区域，它可以与 IAM Identity Center Region 不同。',
apiRegionUsEast: '美国东部（弗吉尼亚北部）',
apiRegionEuCentral: '欧洲（法兰克福）',
apiRegionLegacy: '{region}（当前历史值）',
```

- [ ] **Step 4: Add create state, localized options, and direct-account selector**

Import the shared helpers:

```ts
import {
  DEFAULT_KIRO_API_REGION,
  buildKiroAPIRegionOptions
} from '@/utils/kiroAccount'
```

Define state and options beside the IDC refs:

```ts
const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)
const kiroAPIRegionOptions = computed(() =>
  buildKiroAPIRegionOptions(kiroAPIRegion.value, (region, legacy) => {
    if (legacy) return t('admin.accounts.oauth.kiro.apiRegionLegacy', { region })
    return region === 'eu-central-1'
      ? `${region} - ${t('admin.accounts.oauth.kiro.apiRegionEuCentral')}`
      : `${region} - ${t('admin.accounts.oauth.kiro.apiRegionUsEast')}`
  })
)
```

Render the control in the KIRO section only for `oauth-based` and native `apikey`, not `apikey-relay`:

```vue
<div
  v-if="form.platform === 'kiro' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')"
  data-testid="kiro-api-region-select-create"
  class="space-y-1.5"
>
  <label class="input-label">{{ t('admin.accounts.oauth.kiro.apiRegionLabel') }}</label>
  <Select v-model="kiroAPIRegion" :options="kiroAPIRegionOptions" />
  <p class="text-xs text-gray-500 dark:text-gray-400">
    {{ t('admin.accounts.oauth.kiro.apiRegionHint') }}
  </p>
</div>
```

Reset with:

```ts
kiroAPIRegion.value = DEFAULT_KIRO_API_REGION
```

- [ ] **Step 5: Persist the selection in every direct create path**

At the end of `buildKiroCredentials`, before returning, assign:

```ts
credentials.api_region = kiroAPIRegion.value
```

This covers browser OAuth and token import. In the native KIRO API-key branch, construct:

```ts
const credentials: Record<string, unknown> = {
  api_key: apiKeyValue.value.trim(),
  api_region: kiroAPIRegion.value
}
```

Do not add `api_region` to the `apikey-relay` branch.

- [ ] **Step 6: Run create and utility tests**

Run:

```bash
cd frontend
npx vitest run src/utils/__tests__/kiroAccount.spec.ts src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit create support**

```bash
git add frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/zh/admin/accounts.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts
git commit -m "feat(kiro): configure api region when creating accounts"
```

### Task 3: Add edit-account load and save behavior

**Files:**
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Test: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`

- [ ] **Step 1: Write a failing behavioral edit test**

In the KIRO organization test fixture, add an API region that differs from the
IDC region:

```ts
credentials: {
  auth_method: 'idc',
  start_url: 'https://d-99674ac649.awsapps.com/start',
  region: 'eu-north-1',
  api_region: 'us-east-1',
  access_token: 'at'
}
```

Then extend the existing organization edit test, keeping the two form values
independent:

```ts
const startUrlInput = wrapper.get<HTMLInputElement>('[data-testid="kiro-idc-start-url-input"]')
const regionInput = wrapper.get<HTMLInputElement>('[data-testid="kiro-idc-region-input"]')
const apiRegionSelect = wrapper.get('[data-testid="kiro-api-region-select-edit"] select')

expect(regionInput.element.value).toBe('eu-north-1')
expect((wrapper.get('[data-testid="kiro-api-region-select-edit"] select').element as HTMLSelectElement).value)
  .toBe('us-east-1')

await startUrlInput.setValue('  https://d-1111111111.awsapps.com/start  ')
await regionInput.setValue('  eu-west-1  ')
await apiRegionSelect.setValue('eu-central-1')
await wrapper.get('form#edit-account-form').trigger('submit.prevent')

expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
  start_url: 'https://d-1111111111.awsapps.com/start',
  region: 'eu-west-1',
  api_region: 'eu-central-1'
})
```

Add a missing-value case proving IDC region is not reused:

```ts
it('defaults a missing Kiro API region to us-east-1 instead of the IDC region', async () => {
  const account = buildKiroOrganizationAccount()
  delete account.credentials.api_region
  account.credentials.region = 'eu-north-1'
  const wrapper = mountModal(account)

  expect((wrapper.get('[data-testid="kiro-api-region-select-edit"] select').element as HTMLSelectElement).value)
    .toBe('us-east-1')
})
```

- [ ] **Step 2: Run the edit test and confirm the selector is absent**

Run:

```bash
cd frontend
npx vitest run src/components/account/__tests__/EditAccountModal.spec.ts
```

Expected: FAIL because `kiro-api-region-select-edit` is not rendered.

- [ ] **Step 3: Load and render the edit selector**

Extend the utility import:

```ts
import {
  buildKiroAPIRegionOptions,
  isKiroRelayAccount,
  resolveKiroAPIRegion
} from '@/utils/kiroAccount'
```

Add state and localized options:

```ts
const editKiroAPIRegion = ref('us-east-1')
const editKiroAPIRegionOptions = computed(() =>
  buildKiroAPIRegionOptions(editKiroAPIRegion.value, (region, legacy) => {
    if (legacy) return t('admin.accounts.oauth.kiro.apiRegionLegacy', { region })
    return region === 'eu-central-1'
      ? `${region} - ${t('admin.accounts.oauth.kiro.apiRegionEuCentral')}`
      : `${region} - ${t('admin.accounts.oauth.kiro.apiRegionUsEast')}`
  })
)
```

When the modal loads credentials, assign only from `api_region`:

```ts
editKiroAPIRegion.value = resolveKiroAPIRegion(currentCredentials.api_region)
```

Render within `isKiroAccount && !isKiroRelay`:

```vue
<div data-testid="kiro-api-region-select-edit" class="space-y-1.5">
  <label class="input-label">{{ t('admin.accounts.oauth.kiro.apiRegionLabel') }}</label>
  <Select v-model="editKiroAPIRegion" :options="editKiroAPIRegionOptions" />
  <p class="text-xs text-gray-500 dark:text-gray-400">
    {{ t('admin.accounts.oauth.kiro.apiRegionHint') }}
  </p>
</div>
```

The option helper will append an unsupported current value as disabled, so an unrelated edit preserves it.

- [ ] **Step 4: Save the edit selection for direct API-key and OAuth accounts**

In the API-key credential construction, after merging the existing credentials and only when the account is direct KIRO, assign:

```ts
if (account.platform === 'kiro' && !isKiroRelayAccount(account)) {
  newCredentials.api_region = editKiroAPIRegion.value
}
```

In the existing direct KIRO OAuth block, add:

```ts
newCredentials.api_region = editKiroAPIRegion.value
```

Leave the existing IDC-only assignment to `newCredentials.region` unchanged.

- [ ] **Step 5: Run the edit and utility tests**

Run:

```bash
cd frontend
npx vitest run src/utils/__tests__/kiroAccount.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts
```

Expected: PASS, including the test with `region = eu-north-1` and `api_region = eu-central-1`.

- [ ] **Step 6: Commit edit support**

```bash
git add frontend/src/components/account/EditAccountModal.vue frontend/src/components/account/__tests__/EditAccountModal.spec.ts
git commit -m "feat(kiro): edit account api region"
```

### Task 4: Add reauthorization load and merge behavior

**Files:**
- Modify: `frontend/src/components/admin/account/ReAuthAccountModal.vue`
- Test: `frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts`

- [ ] **Step 1: Write failing reauthorization contract assertions**

Add this test to the reference spec:

```ts
it('preserves the selected API region during Kiro reauthorization and import', () => {
  expect(reauthSource).toContain('data-testid="kiro-api-region-select-reauth"')
  expect(reauthSource).toContain('resolveKiroAPIRegion(creds.api_region)')
  expect(reauthSource).toContain("kiroAPIRegion.value = DEFAULT_KIRO_API_REGION")
  expect(reauthSource).toContain('credentials.api_region = kiroAPIRegion.value')
  expect(reauthSource).toContain('const credentials = kiroOAuth.buildCredentials(tokenInfo)')
  expect(reauthSource.match(/credentials\.api_region = kiroAPIRegion\.value/g)).toHaveLength(2)
})
```

The count of two verifies both browser exchange and imported-token replacement paths.

- [ ] **Step 2: Run the contract test and confirm it fails**

Run:

```bash
cd frontend
npx vitest run src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts
```

Expected: FAIL because reauthorization has no API-region state or merge assignments.

- [ ] **Step 3: Add reauthorization state, options, loading, and reset**

Import `Select` and the shared helpers:

```ts
import Select from '@/components/common/Select.vue'
import {
  DEFAULT_KIRO_API_REGION,
  buildKiroAPIRegionOptions,
  resolveKiroAPIRegion
} from '@/utils/kiroAccount'
```

Add state and options beside the IDC refs:

```ts
const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)
const kiroAPIRegionOptions = computed(() =>
  buildKiroAPIRegionOptions(kiroAPIRegion.value, (region, legacy) => {
    if (legacy) return t('admin.accounts.oauth.kiro.apiRegionLegacy', { region })
    return region === 'eu-central-1'
      ? `${region} - ${t('admin.accounts.oauth.kiro.apiRegionEuCentral')}`
      : `${region} - ${t('admin.accounts.oauth.kiro.apiRegionUsEast')}`
  })
)
```

In the `props.show` watcher load only `api_region`:

```ts
kiroAPIRegion.value = resolveKiroAPIRegion(creds.api_region)
```

In `resetState` use:

```ts
kiroAPIRegion.value = DEFAULT_KIRO_API_REGION
```

- [ ] **Step 4: Render the reauthorization selector for all KIRO OAuth modes**

Place the selector in the KIRO form outside the IDC-only and import-only sections:

```vue
<div v-if="isKiro" data-testid="kiro-api-region-select-reauth" class="space-y-1.5">
  <label class="input-label">{{ t('admin.accounts.oauth.kiro.apiRegionLabel') }}</label>
  <Select v-model="kiroAPIRegion" :options="kiroAPIRegionOptions" />
  <p class="text-xs text-gray-500 dark:text-gray-400">
    {{ t('admin.accounts.oauth.kiro.apiRegionHint') }}
  </p>
</div>
```

- [ ] **Step 5: Merge the selection after browser exchange and token import**

After each call to `kiroOAuth.buildCredentials(tokenInfo)`, add:

```ts
credentials.api_region = kiroAPIRegion.value
```

For `handleKiroImport`, first store the credentials so it has the same explicit merge path:

```ts
const credentials = kiroOAuth.buildCredentials(tokenInfo)
credentials.api_region = kiroAPIRegion.value

const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
  type: 'oauth',
  credentials: buildUpdatedCredentials(credentials)
})
```

Do not assign from `kiroIDCRegion` or `tokenInfo.region` to `api_region`.

- [ ] **Step 6: Run focused KIRO tests**

Run:

```bash
cd frontend
npx vitest run \
  src/utils/__tests__/kiroAccount.spec.ts \
  src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts \
  src/components/account/__tests__/EditAccountModal.spec.ts \
  src/composables/__tests__/useKiroOAuth.spec.ts \
  src/components/account/__tests__/OAuthAuthorizationFlow.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit reauthorization support**

```bash
git add frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts
git commit -m "feat(kiro): preserve api region on reauthorization"
```

### Task 5: Type-check and final regression verification

**Files:**
- Verify all files changed in Tasks 1-4

- [ ] **Step 1: Run the frontend type check**

Run:

```bash
cd frontend
npm run typecheck
```

Expected: exit code 0 with no TypeScript or Vue template errors.

- [ ] **Step 2: Run the complete focused KIRO regression set again**

Run:

```bash
cd frontend
npx vitest run \
  src/utils/__tests__/kiroAccount.spec.ts \
  src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts \
  src/components/account/__tests__/EditAccountModal.spec.ts \
  src/composables/__tests__/useKiroOAuth.spec.ts \
  src/components/account/__tests__/OAuthAuthorizationFlow.spec.ts
```

Expected: all test files and tests PASS.

- [ ] **Step 3: Review the final diff for credential-boundary mistakes**

Run:

```bash
git diff --check
git diff -- frontend/src/utils/kiroAccount.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/zh/admin/accounts.ts
```

Expected: `git diff --check` exits 0. The review must show `api_region` in direct create/edit/reauth paths, no assignment in the KIRO relay create path, and no derivation from IDC `region`.

- [ ] **Step 4: Commit any verification-only corrections**

If type checking or the final review required a correction, stage only the files changed for this feature and commit:

```bash
git add frontend/src/utils/kiroAccount.ts frontend/src/utils/__tests__/kiroAccount.spec.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/components/account/__tests__/CreateAccountModal.kiroReference.spec.ts frontend/src/components/account/__tests__/EditAccountModal.spec.ts frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/zh/admin/accounts.ts
git commit -m "fix(kiro): finalize api region admin configuration"
```

Expected: no commit is needed when all prior tasks already pass unchanged.
