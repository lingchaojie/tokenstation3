import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { keysAPI } from '@/api/keys'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { ApiKey, User } from '@/types'

import GuideApiKeyStep from '../GuideApiKeyStep.vue'

function userFixture(id = 42): User {
  return {
    id,
    username: `beginner-${id}`,
    email: `beginner-${id}@example.test`,
    role: 'user',
    balance: 0,
    concurrency: 1,
    status: 'active',
    allowed_groups: null,
    balance_notify_enabled: false,
    balance_notify_threshold: null,
    balance_notify_extra_emails: [],
    subscription_balance_fallback_enabled: false,
    created_at: '2026-07-15T00:00:00Z',
    updated_at: '2026-07-15T00:00:00Z'
  }
}

function keyFixture(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: 701,
    user_id: 42,
    key: 'sk-guide-component-secret',
    name: 'Guide key',
    group_id: null,
    key_type: 'unified',
    group_binding_mode: 'auto',
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '2026-07-15T00:00:00Z',
    updated_at: '2026-07-15T00:00:00Z',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    ...overrides
  }
}

function page(items: ApiKey[]) {
  return { items, total: items.length, page: 1, page_size: 100, pages: items.length ? 1 : 0 }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function runtimeMessages(value: unknown): unknown {
  if (typeof value === 'string') return () => value
  if (Array.isArray(value)) return value.map(runtimeMessages)
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>).map(([key, item]) => [
        key,
        runtimeMessages(item)
      ])
    )
  }
  return value
}

const messages = runtimeMessages({
  gettingStarted: {
    apiKey: {
      anonymousTitle: 'Sign in to continue',
      anonymousDescription: 'Sign in or register to choose a key.',
      login: 'Sign in',
      register: 'Register',
      loading: 'Loading your API keys…',
      existingTitle: 'Choose an active API key',
      emptyTitle: 'Create your first API key',
      emptyDescription: 'No compatible active key was found.',
      create: 'Create API key',
      inactive: 'This key is inactive.',
      incompatible: 'This key is not compatible with the selected client.',
      secretWarning: 'Your API key is secret.'
    },
    configuration: {
      reselectAfterRefresh: 'Choose the key again after refreshing.'
    },
    troubleshooting: {
      retry: 'Try again'
    },
    completion: {
      keys: 'Manage API Keys'
    }
  },
  keys: {
    nameLabel: 'Name',
    namePlaceholder: 'My API Key',
    saving: 'Saving...',
    failedToLoad: 'Failed to load API keys',
    failedToSave: 'Failed to save API key',
    status: {
      active: 'Active',
      inactive: 'Inactive',
      quota_exhausted: 'Quota Exhausted',
      expired: 'Expired'
    }
  }
}) as Record<string, unknown>

function mountStep(options: {
  authenticated?: boolean
  userId?: number
  client?: 'claude_code' | 'codex'
  os?: 'macos' | 'windows' | 'linux'
  selectedKey?: ApiKey | null
  reselectRequired?: boolean
} = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const authStore = useAuthStore()
  if (options.authenticated) {
    authStore.user = userFixture(options.userId)
    authStore.token = 'test-session-token'
  }
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: { en: messages }
  })
  const wrapper = mount(GuideApiKeyStep, {
    props: {
      client: options.client ?? 'claude_code',
      os: options.os ?? 'macos',
      selectedKey: options.selectedKey ?? null,
      reselectRequired: options.reselectRequired ?? false
    },
    global: {
      plugins: [pinia, i18n],
      stubs: { RouterLink: RouterLinkStub }
    }
  })
  return { wrapper, authStore, appStore: useAppStore() }
}

async function settle(): Promise<void> {
  await flushPromises()
  await flushPromises()
}

describe('GuideApiKeyStep', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(keysAPI, 'list').mockResolvedValue(page([]))
    vi.spyOn(keysAPI, 'create').mockResolvedValue(keyFixture())
  })

  it('renders only internal login and registration redirects for anonymous visitors', async () => {
    const { wrapper } = mountStep()
    await settle()

    expect(keysAPI.list).not.toHaveBeenCalled()
    expect(wrapper.getComponent('[data-testid="api-key-login"]').props('to')).toBe(
      '/login?redirect=/getting-started'
    )
    expect(wrapper.getComponent('[data-testid="api-key-register"]').props('to')).toBe(
      '/register?redirect=/getting-started'
    )
  })

  it('loads the complete first page and renders the authenticated empty state', async () => {
    const pending = deferred<ReturnType<typeof page>>()
    vi.mocked(keysAPI.list).mockReturnValueOnce(pending.promise)
    const { wrapper } = mountStep({ authenticated: true })

    expect(wrapper.get('[data-testid="api-key-loading"]').text()).toContain('Loading')
    expect(keysAPI.list).toHaveBeenCalledOnce()
    expect(keysAPI.list).toHaveBeenCalledWith(1, 100)

    pending.resolve(page([]))
    await settle()

    expect(wrapper.get('[data-testid="api-key-empty"]').text()).toContain(
      'Create your first API key'
    )
    expect(wrapper.get('[data-testid="api-key-name"]').element).toHaveProperty(
      'value',
      'My API Key'
    )
  })

  it('labels native key controls and gives every interactive control a visible focus ring', async () => {
    vi.mocked(keysAPI.list).mockResolvedValueOnce(page([keyFixture()]))
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    const fieldset = wrapper.get('fieldset')
    expect(fieldset.get('legend').text()).toBe('Choose an active API key')
    expect(fieldset.get('[data-key-id="701"]').element.tagName).toBe('BUTTON')
    expect(fieldset.get('[data-key-id="701"]').attributes('type')).toBe('button')
    expect(wrapper.get('label[for="guide-api-key-name"]').text()).toBe('Name')

    for (const control of wrapper.findAll('button, input, a')) {
      expect.soft(control.classes().join(' ')).toContain('focus-visible:ring-2')
    }
  })

  it('keeps a list failure retryable and offers the API Keys page fallback', async () => {
    vi.mocked(keysAPI.list)
      .mockRejectedValueOnce(new Error('list failed'))
      .mockResolvedValueOnce(page([]))
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    expect(wrapper.get('[data-testid="api-key-error"]').text()).toContain(
      'Failed to load API keys'
    )
    expect(wrapper.getComponent('[data-testid="api-key-fallback"]').props('to')).toBe('/keys')

    await wrapper.get('[data-testid="api-key-retry"]').trigger('click')
    await settle()

    expect(keysAPI.list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="api-key-error"]').exists()).toBe(false)
  })

  it('selects existing active keys only when they are compatible with the client', async () => {
    const unified = keyFixture({ id: 1, name: 'Unified' })
    const anthropic = keyFixture({ id: 2, name: 'Anthropic', key_type: 'anthropic' })
    const openaiDispatch = keyFixture({
      id: 3,
      name: 'OpenAI dispatch',
      key_type: 'openai',
      group_binding_mode: 'static',
      group: { platform: 'openai', allow_messages_dispatch: true } as ApiKey['group']
    })
    const openaiOnly = keyFixture({
      id: 4,
      name: 'OpenAI only',
      key_type: 'openai',
      group_binding_mode: 'static'
    })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(
      page([unified, anthropic, openaiDispatch, openaiOnly])
    )
    const { wrapper } = mountStep({ authenticated: true, client: 'claude_code' })
    await settle()

    expect(wrapper.get('[data-key-id="1"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-key-id="2"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-key-id="3"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-key-id="4"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-key-explanation="4"]').text()).toContain('not compatible')

    await wrapper.get('[data-key-id="3"]').trigger('click')
    expect(wrapper.emitted<ApiKey[]>('select')?.at(-1)).toEqual([openaiDispatch])
  })

  it('allows OpenAI keys for Codex and disables Anthropic keys', async () => {
    const anthropic = keyFixture({ id: 5, name: 'Anthropic', key_type: 'anthropic' })
    const openai = keyFixture({ id: 6, name: 'OpenAI', key_type: 'openai' })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(page([anthropic, openai]))
    const { wrapper } = mountStep({ authenticated: true, client: 'codex' })
    await settle()

    expect(wrapper.get('[data-key-id="5"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-key-id="6"]').attributes('disabled')).toBeUndefined()
  })

  it.each([
    ['inactive', 'Inactive'],
    ['expired', 'Expired'],
    ['quota_exhausted', 'Quota Exhausted']
  ] as const)('disables and explains %s keys', async (status, label) => {
    const key = keyFixture({ id: 20, status })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(page([key]))
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    expect(wrapper.get('[data-key-id="20"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-key-explanation="20"]').text()).toContain(label)
  })

  it('creates once with the translated default name and immediately emits the returned key', async () => {
    const created = keyFixture({ id: 77, name: 'My API Key' })
    const pending = deferred<ApiKey>()
    vi.mocked(keysAPI.create).mockReturnValueOnce(pending.promise)
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    const create = wrapper.get('[data-testid="api-key-create"]')
    const form = wrapper.get('form')
    await form.trigger('submit')
    await form.trigger('submit')
    expect(keysAPI.create).toHaveBeenCalledOnce()
    expect(keysAPI.create).toHaveBeenCalledWith('My API Key')
    expect(create.attributes('disabled')).toBeDefined()

    pending.resolve(created)
    await settle()

    expect(wrapper.emitted<ApiKey[]>('select')?.at(-1)).toEqual([created])
  })

  it('does not let a delayed create override a later explicit existing-key selection', async () => {
    const existing = keyFixture({ id: 71, key: 'sk-existing', name: 'Existing key' })
    const created = keyFixture({ id: 72, key: 'sk-created', name: 'Created key' })
    const pending = deferred<ApiKey>()
    vi.mocked(keysAPI.list).mockResolvedValueOnce(page([existing]))
    vi.mocked(keysAPI.create).mockReturnValueOnce(pending.promise)
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-key-id="71"]').trigger('click')
    pending.resolve(created)
    await settle()

    expect(wrapper.emitted<ApiKey[]>('select')).toEqual([[existing]])
    expect(wrapper.text()).not.toContain('Created key')
    expect(wrapper.get('[data-testid="api-key-create"]').attributes('disabled')).toBeUndefined()
  })

  it.each([
    ['client', { client: 'codex' as const }],
    ['OS', { os: 'windows' as const }]
  ])('does not auto-select or repopulate a delayed create after a %s change', async (_label, variant) => {
    const created = keyFixture({ id: 73, key: 'sk-created', name: 'Stale created key' })
    const pending = deferred<ApiKey>()
    vi.mocked(keysAPI.create).mockReturnValueOnce(pending.promise)
    const { wrapper } = mountStep({ authenticated: true })
    await settle()

    await wrapper.get('form').trigger('submit')
    await wrapper.setProps(variant)
    pending.resolve(created)
    await settle()

    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.text()).not.toContain('Stale created key')
    expect(wrapper.get('[data-testid="api-key-create"]').attributes('disabled')).toBeUndefined()
  })

  it('preserves an entered name and reports the existing API detail through the app toast', async () => {
    vi.mocked(keysAPI.create).mockRejectedValueOnce({
      response: { data: { detail: 'Name is already in use' } }
    })
    const { wrapper, appStore } = mountStep({ authenticated: true })
    await settle()

    await wrapper.get('[data-testid="api-key-name"]').setValue('My retained name')
    await wrapper.get('form').trigger('submit')
    await settle()

    expect(wrapper.get<HTMLInputElement>('[data-testid="api-key-name"]').element.value).toBe(
      'My retained name'
    )
    expect(appStore.toasts.at(-1)).toMatchObject({
      type: 'error',
      message: 'Name is already in use'
    })
  })

  it('discards a previous account response after the authenticated owner changes', async () => {
    const accountA = deferred<ReturnType<typeof page>>()
    const accountB = deferred<ReturnType<typeof page>>()
    vi.mocked(keysAPI.list)
      .mockReturnValueOnce(accountA.promise)
      .mockReturnValueOnce(accountB.promise)
    const { wrapper, authStore } = mountStep({ authenticated: true, userId: 42 })

    authStore.user = userFixture(43)
    await wrapper.vm.$nextTick()
    expect(keysAPI.list).toHaveBeenCalledTimes(2)

    accountB.resolve(page([keyFixture({ id: 43, name: 'Account B key', user_id: 43 })]))
    await settle()
    accountA.resolve(page([keyFixture({ id: 42, name: 'Account A key', user_id: 42 })]))
    await settle()

    expect(wrapper.text()).toContain('Account B key')
    expect(wrapper.text()).not.toContain('Account A key')
  })

  it('clears the secret-bearing list and ignores pending work on logout and unmount', async () => {
    const pending = deferred<ReturnType<typeof page>>()
    vi.mocked(keysAPI.list).mockReturnValueOnce(pending.promise)
    const { wrapper, authStore } = mountStep({ authenticated: true })

    authStore.token = null
    await wrapper.vm.$nextTick()
    pending.resolve(page([keyFixture({ name: 'Late secret key' })]))
    await settle()

    expect(wrapper.text()).toContain('Sign in to continue')
    expect(wrapper.text()).not.toContain('Late secret key')
    wrapper.unmount()
  })

  it('shows the reselect notice without putting key data in the component contract', async () => {
    const { wrapper } = mountStep({ authenticated: true, reselectRequired: true })
    await settle()

    expect(wrapper.get('[data-testid="api-key-reselect"]').text()).toContain(
      'Choose the key again'
    )
    expect(JSON.stringify(wrapper.props())).not.toContain('sk-')
  })
})
