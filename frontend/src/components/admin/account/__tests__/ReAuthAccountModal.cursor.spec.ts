import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const {
  applyOAuthCredentialsMock,
  generateAuthUrlMock,
  pollAuthorizationMock,
  refreshCursorTokenMock,
  validateSSOTokenMock,
} = vi.hoisted(() => ({
  applyOAuthCredentialsMock: vi.fn(),
  generateAuthUrlMock: vi.fn(),
  pollAuthorizationMock: vi.fn(),
  refreshCursorTokenMock: vi.fn(),
  validateSSOTokenMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { applyOAuthCredentials: applyOAuthCredentialsMock },
    cursor: {
      exchangeCode: vi.fn(),
      generateAuthUrl: generateAuthUrlMock,
      getCapabilities: vi.fn().mockResolvedValue({ password_auth_enabled: false }),
      pollAuthorization: pollAuthorizationMock,
      refreshCursorToken: refreshCursorTokenMock,
      validateSSOToken: validateSSOTokenMock,
    },
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import OAuthAuthorizationFlow from '@/components/account/OAuthAuthorizationFlow.vue'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

function cursorAccount(credentials: Record<string, unknown>): Account {
  return {
    id: 42,
    name: 'Cursor OAuth',
    platform: 'cursor',
    type: 'oauth',
    credentials,
    credentials_status: {},
    extra: {},
    proxy_id: 7,
    concurrency: 2,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '2026-08-24T00:00:00Z',
    updated_at: '2026-08-24T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
  } as Account
}

function mountModal(credentials: Record<string, unknown>) {
  return mount(ReAuthAccountModal, {
    props: { show: true, account: cursorAccount(credentials) },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Icon: true,
        Select: true,
      },
    },
  })
}

function buttonWithText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button, `button containing ${text}`).toBeDefined()
  return button!
}

describe('ReAuthAccountModal Cursor source replacement', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    applyOAuthCredentialsMock.mockResolvedValue(cursorAccount({ access_token: 'new-access' }))
    generateAuthUrlMock.mockResolvedValue({
      auth_url: 'https://cursor.com/login/deep-control',
      session_id: 'reauth-session',
      state: 'reauth-state',
    })
  })

  it('replaces an API-key source with refresh-token credentials and retains the custom base URL', async () => {
    refreshCursorTokenMock.mockResolvedValue({
      access_token: 'new-access',
      refresh_token: 'new-refresh',
      expires_at: 1_900_000_001,
    })
    const wrapper = mountModal({
      access_token: 'old-access',
      api_key: 'OLD_API_KEY_CANARY',
      base_url: 'https://relay.example.test/cursor/v1',
    })
    const flow = wrapper.getComponent(OAuthAuthorizationFlow)
    await flow.get('input[value="refresh_token"]').setValue()
    await flow.get('textarea').setValue('NEW_REFRESH_INPUT_CANARY')
    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.validateAndCreate').trigger('click')
    await flushPromises()

    expect(refreshCursorTokenMock).toHaveBeenCalledWith('NEW_REFRESH_INPUT_CANARY', 7)
    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(42, {
      type: 'oauth',
      credentials: {
        access_token: 'new-access',
        refresh_token: 'new-refresh',
        expires_at: 1_900_000_001,
        base_url: 'https://relay.example.test/cursor/v1',
      },
      extra: {},
    })
    expect(JSON.stringify(applyOAuthCredentialsMock.mock.calls[0]![1])).not.toContain('OLD_API_KEY_CANARY')
    expect(wrapper.text()).not.toContain('NEW_REFRESH_INPUT_CANARY')
  })

  it('replaces a cookie source with an API-key source without resurrecting the cookie', async () => {
    validateSSOTokenMock.mockResolvedValue({
      access_token: 'new-access',
      api_key: 'crsr_NEW_API_KEY_CANARY',
    })
    const wrapper = mountModal({
      access_token: 'old-web-access',
      web_session_token: 'OLD_COOKIE_CANARY',
    })
    const flow = wrapper.getComponent(OAuthAuthorizationFlow)
    await flow.get('input[value="sso_cookie"]').setValue()
    await flow.get('textarea').setValue('NEW_COOKIE_INPUT_CANARY')
    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.convertSSOAndCreate').trigger('click')
    await flushPromises()

    expect(validateSSOTokenMock).toHaveBeenCalledWith('NEW_COOKIE_INPUT_CANARY', 7)
    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(42, {
      type: 'oauth',
      credentials: {
        access_token: 'new-access',
        api_key: 'crsr_NEW_API_KEY_CANARY',
      },
      extra: {},
    })
    const payload = JSON.stringify(applyOAuthCredentialsMock.mock.calls[0]![1])
    expect(payload).not.toContain('OLD_COOKIE_CANARY')
    expect(payload).not.toContain('NEW_COOKIE_INPUT_CANARY')
    expect(wrapper.text()).not.toContain('NEW_COOKIE_INPUT_CANARY')
  })

  it('cancels an in-flight deep-link reauthorization and clears callback state', async () => {
    let resolveGenerate!: (value: { auth_url: string; session_id: string; state: string }) => void
    generateAuthUrlMock.mockReturnValueOnce(new Promise((resolve) => { resolveGenerate = resolve }))
    const wrapper = mountModal({ access_token: 'old-access' })

    await buttonWithText(wrapper, 'admin.accounts.oauth.cursor.generateAuthUrl').trigger('click')
    await flushPromises()

    wrapper.getComponent(BaseDialogStub).vm.$emit('close')
    await nextTick()
    resolveGenerate({ auth_url: 'https://stale.example', session_id: 'stale-session', state: 'stale-state' })
    await flushPromises()

    expect(pollAuthorizationMock).not.toHaveBeenCalled()
    expect(applyOAuthCredentialsMock).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('https://stale.example')
  })

  it('does not expose password, setup-token, or API-key account controls', () => {
    const wrapper = mountModal({ access_token: 'old-access' })

    expect(wrapper.find('input[value="setup-token"]').exists()).toBe(false)
    expect(wrapper.find('input[value="email_password"]').exists()).toBe(false)
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.apiKey')
  })
})
