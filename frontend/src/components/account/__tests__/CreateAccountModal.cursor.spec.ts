import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  createFromSSOMock,
  generateAuthUrlMock,
  listAccountsMock,
  pollAuthorizationMock,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  createFromSSOMock: vi.fn(),
  generateAuthUrlMock: vi.fn(),
  listAccountsMock: vi.fn(),
  pollAuthorizationMock: vi.fn(),
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
      create: createAccountMock,
      list: listAccountsMock,
      probeUpstreamBilling: vi.fn(),
    },
    cursor: {
      createFromSSO: createFromSSOMock,
      exchangeCode: vi.fn(),
      generateAuthUrl: generateAuthUrlMock,
      getCapabilities: vi.fn().mockResolvedValue({ password_auth_enabled: false }),
      pollAuthorization: pollAuthorizationMock,
      refreshCursorToken: vi.fn(),
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
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'
import * as credentialsBuilder from '../credentialsBuilder'

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
