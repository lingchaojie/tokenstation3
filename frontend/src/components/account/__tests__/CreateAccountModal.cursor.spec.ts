import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  createFromSSOMock,
  bulkUpdateMock,
  exchangeCodeMock,
  generateAuthUrlMock,
  listAccountsMock,
  pollAuthorizationMock,
  refreshCursorTokenMock,
  updateAccountMock,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  createFromSSOMock: vi.fn(),
  bulkUpdateMock: vi.fn(),
  exchangeCodeMock: vi.fn(),
  generateAuthUrlMock: vi.fn(),
  listAccountsMock: vi.fn(),
  pollAuthorizationMock: vi.fn(),
  refreshCursorTokenMock: vi.fn(),
  updateAccountMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      bulkUpdate: bulkUpdateMock,
      create: createAccountMock,
      list: listAccountsMock,
      probeUpstreamBilling: vi.fn(),
      update: updateAccountMock,
    },
    cursor: {
      createFromSSO: createFromSSOMock,
      exchangeCode: exchangeCodeMock,
      generateAuthUrl: generateAuthUrlMock,
      getCapabilities: vi.fn().mockResolvedValue({ password_auth_enabled: false }),
      pollAuthorization: pollAuthorizationMock,
      refreshCursorToken: refreshCursorTokenMock,
      validateSSOToken: vi.fn(),
    },
    settings: {
      getSettings: vi.fn().mockResolvedValue({}),
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue({}),
  getKiroDefaultModelMapping: vi.fn().mockResolvedValue({}),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'
import BulkEditAccountModal from '../BulkEditAccountModal.vue'
import EditAccountModal from '../EditAccountModal.vue'
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'
import * as credentialsBuilder from '../credentialsBuilder'
import en from '@/i18n/locales/en/admin/accounts'
import zh from '@/i18n/locales/zh/admin/accounts'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: { type: Array, default: () => [] },
    platform: String,
  },
  template: '<div data-testid="cursor-model-whitelist">{{ modelValue.join(",") }}</div>',
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        CnBaseUrlPresets: true,
        ConfirmDialog: true,
        GroupSelector: true,
        HeaderOverrideEditor: true,
        Icon: true,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        PlatformIcon: true,
        ProxyAdBanner: true,
        ProxySelector: true,
        QuotaLimitCard: true,
        Select: true,
        Toggle: true,
      },
    },
  })
}

async function selectCursor(wrapper: ReturnType<typeof mountModal>) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().trim() === 'Cursor')
  expect(button, 'Cursor platform button').toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}

async function openAuthorizationStep(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('input[data-tour="account-form-name"]').setValue('Cursor account')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await nextTick()
  return wrapper.getComponent(OAuthAuthorizationFlow)
}

function buttonWithText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button, `button containing ${text}`).toBeDefined()
  return button!
}

describe('CreateAccountModal Cursor OAuth', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    createAccountMock.mockResolvedValue({ id: 91, platform: 'cursor', type: 'oauth' })
    bulkUpdateMock.mockResolvedValue({ success: 2, failed: 0, results: [] })
    updateAccountMock.mockResolvedValue({ id: 42, platform: 'cursor', type: 'oauth' })
    createFromSSOMock.mockResolvedValue({
      created: [{ index: 0 }, { index: 1 }],
      failed: [],
    })
    generateAuthUrlMock.mockResolvedValue({
      auth_url: 'https://cursor.com/login/deep-control',
      session_id: 'CURSOR_SESSION_CANARY',
      state: 'CURSOR_STATE_CANARY',
    })
    listAccountsMock.mockResolvedValue({
      items: [{
        platform: 'cursor',
        type: 'oauth',
        extra: {
          cursor_observed_models: {
            models: ['observed-model-z', 'auto', 'observed-model-z'],
            fetched_at: '2026-08-24T00:00:00Z',
          },
        },
      }],
      page: 1,
      page_size: 100,
      total: 1,
      pages: 1,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders Cursor as OAuth-only and fills its whitelist from observed account snapshots', async () => {
    const wrapper = mountModal()
    await selectCursor(wrapper)

    expect(wrapper.find('[data-testid="grok-account-type-api-key"]').exists()).toBe(false)
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.setupTokenLongLived')
    expect(wrapper.text()).not.toContain('admin.accounts.channelMonitor')

    const whitelist = wrapper.getComponent(ModelWhitelistSelectorStub)
    expect(whitelist.props('platform')).toBe('cursor')
    expect(whitelist.props('modelValue')).toEqual(['observed-model-z', 'auto'])
    expect(listAccountsMock).toHaveBeenCalledWith(1, 100, { platform: 'cursor' })
  })

  it('polls the primary deep link through pending to success and stores only normalized credentials', async () => {
    vi.useFakeTimers()
    pollAuthorizationMock
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({
        access_token: 'CURSOR_ACCESS_CANARY',
        refresh_token: 'CURSOR_REFRESH_CANARY',
        expires_at: 1_900_000_000,
        sub: 'cursor-user',
        session_id: 'MUST_NOT_PERSIST',
        state: 'MUST_NOT_PERSIST',
      })
    const wrapper = mountModal()
    await selectCursor(wrapper)
    await openAuthorizationStep(wrapper)

    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.generateAuthUrl').trigger('click')
    await flushPromises()
    expect(pollAuthorizationMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(3_000)
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'cursor',
      type: 'oauth',
      credentials: expect.objectContaining({
        access_token: 'CURSOR_ACCESS_CANARY',
        refresh_token: 'CURSOR_REFRESH_CANARY',
        expires_at: 1_900_000_000,
        sub: 'cursor-user',
      }),
    }))
    const payload = createAccountMock.mock.calls[0]![0]
    expect(JSON.stringify(payload)).not.toContain('CURSOR_SESSION_CANARY')
    expect(JSON.stringify(payload)).not.toContain('CURSOR_STATE_CANARY')
    expect(JSON.stringify(payload)).not.toContain('MUST_NOT_PERSIST')
    expect(payload.credentials.base_url).toBeUndefined()
  })

  it('imports ordered cookie/API-key values once, preserves a safe custom URL, and clears secrets', async () => {
    const firstSecret = 'WORKOS_COOKIE_CANARY'
    const secondSecret = 'crsr_API_KEY_CANARY'
    const wrapper = mountModal()
    await selectCursor(wrapper)

    await wrapper.get('[data-testid="cursor-custom-base-url-toggle"]').trigger('click')
    await wrapper.get('[data-testid="cursor-custom-base-url-input"]').setValue('https://relay.example.test/cursor/v1')
    const flow = await openAuthorizationStep(wrapper)
    await flow.get('input[value="sso_cookie"]').setValue()
    const secretInput = flow.get('textarea')
    await secretInput.setValue(`  ${firstSecret}  \n\n${secondSecret}`)
    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.convertSSOAndCreate').trigger('click')
    await flushPromises()

    expect(createFromSSOMock).toHaveBeenCalledTimes(1)
    expect(createFromSSOMock).toHaveBeenCalledWith(expect.objectContaining({
      sso_tokens: [firstSecret, secondSecret],
      credentials: expect.objectContaining({
        base_url: 'https://relay.example.test/cursor/v1',
      }),
      concurrency: 10,
      priority: 1,
      rate_multiplier: 1,
    }))
    const payload = createFromSSOMock.mock.calls[0]![0]
    expect(payload.credentials).not.toHaveProperty('api_key')
    expect(payload.credentials).not.toHaveProperty('sso_token')
    expect(payload.extra).toBeUndefined()
    expect(wrapper.text()).not.toContain(firstSecret)
    expect(wrapper.text()).not.toContain(secondSecret)
    expect(wrapper.findComponent(OAuthAuthorizationFlow).exists()).toBe(false)
  })

  it('guards a mixed SSO import against double submit, includes model mapping, and clears submitted secrets', async () => {
    let resolveImport!: (value: { created: Array<{ index: number }>; failed: Array<{ index: number; error: string }> }) => void
    createFromSSOMock.mockReturnValueOnce(new Promise((resolve) => { resolveImport = resolve }))
    const wrapper = mountModal()
    await selectCursor(wrapper)
    const flow = await openAuthorizationStep(wrapper)
    await flow.get('input[value="sso_cookie"]').setValue()
    await flow.get('textarea').setValue('COOKIE_FIRST\ncrsr_SECOND')
    const submit = buttonWithText(wrapper, 'admin.accounts.oauth.cursor.convertSSOAndCreate')

    await submit.trigger('click')
    await submit.trigger('click')
    expect(createFromSSOMock).toHaveBeenCalledTimes(1)
    expect(submit.attributes('disabled')).toBeDefined()

    resolveImport({ created: [{ index: 0 }], failed: [{ index: 1, error: 'invalid key' }] })
    await flushPromises()

    expect(createFromSSOMock).toHaveBeenCalledWith(expect.objectContaining({
      sso_tokens: ['COOKIE_FIRST', 'crsr_SECOND'],
      credentials: {
        model_mapping: {
          'observed-model-z': 'observed-model-z',
          auto: 'auto',
        },
      },
    }))
    const payload = createFromSSOMock.mock.calls[0]![0]
    expect(JSON.stringify(payload.credentials)).not.toContain('COOKIE_FIRST')
    expect(JSON.stringify(payload.credentials)).not.toContain('crsr_SECOND')
    expect(payload.extra).toBeUndefined()
    expect((flow.vm as unknown as { ssoCookie: string }).ssoCookie).toBe('')
    expect(wrapper.text()).toContain('#1: invalid key')
  })

  it('creates a refresh-token batch with one stable settings snapshot and closes only after the batch', async () => {
    refreshCursorTokenMock
      .mockResolvedValueOnce({ access_token: 'access-1', refresh_token: 'refresh-1', email: 'one@example.test' })
      .mockResolvedValueOnce({ access_token: 'access-2', refresh_token: 'refresh-2', email: 'two@example.test' })
    const wrapper = mountModal()
    await selectCursor(wrapper)
    await wrapper.get('[data-testid="cursor-custom-base-url-toggle"]').trigger('click')
    await wrapper.get('[data-testid="cursor-custom-base-url-input"]').setValue('https://stable.example/cursor')
    await wrapper.get('input[data-tour="account-form-name"]').setValue('Cursor batch')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    const flow = wrapper.getComponent(OAuthAuthorizationFlow)
    await flow.get('input[value="refresh_token"]').setValue()
    await flow.get('textarea').setValue('input-rt-1\ninput-rt-2')

    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.validateAndCreate').trigger('click')
    await flushPromises()

    expect(refreshCursorTokenMock.mock.calls).toEqual([['input-rt-1', null], ['input-rt-2', null]])
    expect(createAccountMock).toHaveBeenCalledTimes(2)
    expect(createAccountMock.mock.calls.map(([payload]) => payload)).toEqual([
      expect.objectContaining({
        name: 'Cursor batch #1', platform: 'cursor', type: 'oauth', concurrency: 10, priority: 1,
        credentials: {
          access_token: 'access-1', refresh_token: 'refresh-1', base_url: 'https://stable.example/cursor',
          model_mapping: { 'observed-model-z': 'observed-model-z', auto: 'auto' },
        },
      }),
      expect.objectContaining({
        name: 'Cursor batch #2', platform: 'cursor', type: 'oauth', concurrency: 10, priority: 1,
        credentials: {
          access_token: 'access-2', refresh_token: 'refresh-2', base_url: 'https://stable.example/cursor',
          model_mapping: { 'observed-model-z': 'observed-model-z', auto: 'auto' },
        },
      }),
    ])
    expect(wrapper.findComponent(OAuthAuthorizationFlow).exists()).toBe(false)
  })

  it('ignores delayed Cursor observations after switching platform and resets stale observations on failure', async () => {
    let resolveList!: (value: { items: Array<{ extra: unknown }> }) => void
    listAccountsMock.mockReturnValueOnce(new Promise((resolve) => { resolveList = resolve }))
    const wrapper = mountModal()
    await selectCursor(wrapper)
    const anthropic = wrapper.findAll('button').find((button) => button.text().trim() === 'Anthropic')!
    await anthropic.trigger('click')
    resolveList({ items: [{ extra: { cursor_observed_models: { models: ['late-model'] } } }] })
    await flushPromises()

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('platform')).toBe('anthropic')
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('modelValue')).not.toContain('late-model')

    listAccountsMock.mockRejectedValueOnce(new Error('list failed'))
    await selectCursor(wrapper)
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('modelValue')).toEqual([
      'auto', 'cursor-small', 'composer-2.5', 'composer-2.5-fast', 'claude-4.5-sonnet',
      'claude-4.6-sonnet', 'claude-opus-4.8', 'gpt-5', 'gpt-5.6-sol', 'gemini-3-pro',
      'gemini-3.5-flash', 'deepseek-v3.1', 'grok-4.6',
    ])
  })

  it('enables the manual callback fallback and exchanges through Cursor safely', async () => {
    let resolvePoll!: (value: { status: string }) => void
    pollAuthorizationMock.mockReturnValueOnce(new Promise((resolve) => { resolvePoll = resolve }))
    exchangeCodeMock.mockResolvedValueOnce({ access_token: 'manual-access', refresh_token: 'manual-refresh' })
    const wrapper = mountModal()
    await selectCursor(wrapper)
    const flow = await openAuthorizationStep(wrapper)
    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.generateAuthUrl').trigger('click')
    await flushPromises()
    await flow.get('textarea').setValue('http://localhost/callback?code=manual-code&state=CURSOR_STATE_CANARY')
    const complete = buttonWithText(wrapper, 'admin.accounts.oauth.completeAuth')

    expect(complete.attributes('disabled')).toBeUndefined()
    await complete.trigger('click')
    await flushPromises()

    expect(exchangeCodeMock).toHaveBeenCalledWith({
      code: 'manual-code', session_id: 'CURSOR_SESSION_CANARY', state: 'CURSOR_STATE_CANARY',
    })
    expect(createAccountMock).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'cursor', type: 'oauth',
      credentials: expect.objectContaining({ access_token: 'manual-access', refresh_token: 'manual-refresh' }),
    }))
    resolvePoll({ status: 'pending' })
  })

  it('persists a custom Cursor URL through edit and bulk-edit forms', async () => {
    const account = {
      id: 42, name: 'Cursor edit', notes: '', platform: 'cursor', type: 'oauth',
      credentials: { access_token: 'access', base_url: 'https://exact.example/cursor' },
      credentials_status: { has_access_token: true }, extra: {}, proxy_id: null, concurrency: 10,
      priority: 1, rate_multiplier: 1, status: 'active', group_ids: [], expires_at: null,
      auto_pause_on_expired: false,
    } as any
    const edit = mount(EditAccountModal, {
      props: { show: true, account, proxies: [], groups: [] },
      global: { stubs: { BaseDialog: BaseDialogStub, Select: true, Icon: true, ProxySelector: true, GroupSelector: true, ModelWhitelistSelector: true, QuotaLimitCard: true, HeaderOverrideEditor: true, CnBaseUrlPresets: true, GrokBaseUrlPresets: true } },
    })
    expect((edit.get('[data-testid="cursor-custom-base-url-input"]').element as HTMLInputElement).value).toBe('https://exact.example/cursor')
    await edit.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledWith(42, expect.objectContaining({
      credentials: expect.objectContaining({ base_url: 'https://exact.example/cursor' }),
    }))

    const bulk = mount(BulkEditAccountModal, {
      props: { show: true, accountIds: [42, 43], selectedPlatforms: ['cursor'], selectedTypes: ['oauth'], proxies: [], groups: [] },
      global: { stubs: { BaseDialog: BaseDialogStub, ConfirmDialog: true, Select: true, Icon: true, ProxySelector: true, GroupSelector: true, ModelWhitelistSelector: true } },
    })
    await bulk.get('#bulk-edit-base-url-enabled').setValue(true)
    await bulk.get('#bulk-edit-base-url').setValue('https://bulk.example/cursor')
    await bulk.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(bulkUpdateMock).toHaveBeenCalledWith([42, 43], { credentials: { base_url: 'https://bulk.example/cursor' } })
  })

  it('ships every visible Cursor OAuth message in both locale objects', () => {
    const keys = [
      'refreshTokenAuth', 'refreshTokenDesc', 'refreshTokenPlaceholder', 'ssoCookieAuth',
      'ssoCookieDesc', 'ssoCookieLabel', 'ssoCookiePlaceholder', 'ssoCookieHint',
      'validating', 'convertingSSO',
    ] as const
    const enCursor = (en as any).accounts.oauth.cursor
    const zhCursor = (zh as any).accounts.oauth.cursor
    for (const key of keys) {
      expect(enCursor[key], `en ${key}`).toEqual(expect.any(String))
      expect(enCursor[key].length).toBeGreaterThan(0)
      expect(zhCursor[key], `zh ${key}`).toEqual(expect.any(String))
      expect(zhCursor[key].length).toBeGreaterThan(0)
    }
  })

  it('cancels an in-flight deep-link generation without creating a stale account', async () => {
    let resolvePoll!: (value: { access_token: string }) => void
    pollAuthorizationMock.mockReturnValueOnce(new Promise((resolve) => { resolvePoll = resolve }))
    const wrapper = mountModal()
    await selectCursor(wrapper)
    await openAuthorizationStep(wrapper)

    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.generateAuthUrl').trigger('click')
    await flushPromises()
    wrapper.getComponent(BaseDialogStub).vm.$emit('close')
    await nextTick()
    resolvePoll({ access_token: 'STALE_CURSOR_ACCESS_CANARY' })
    await flushPromises()

    expect(createAccountMock).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('STALE_CURSOR_ACCESS_CANARY')
  })

  it('rejects malformed or credential-bearing URLs as custom Cursor endpoints', () => {
    const classify = (credentialsBuilder as unknown as {
      isCustomCursorBaseUrl: (value: unknown) => boolean
    }).isCustomCursorBaseUrl

    expect(classify('https://api2.cursor.sh')).toBe(false)
    expect(classify('https://relay.example.test/cursor/v1')).toBe(true)
    expect(classify('javascript:alert(1)')).toBe(false)
    expect(classify('https://user:password@relay.example.test')).toBe(false)
    expect(classify('not a URL')).toBe(false)
  })
})
