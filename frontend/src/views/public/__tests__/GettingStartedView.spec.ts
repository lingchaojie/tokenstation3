import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { keysAPI } from '@/api/keys'
import type { BeginnerGuideProgressV1, BeginnerGuideStepId } from '@/api/beginnerGuide'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useBeginnerGuideStore } from '@/stores/beginnerGuide'
import type { ApiKey } from '@/types'
import enMessages from '@/i18n/locales/en/gettingStarted'
import zhMessages from '@/i18n/locales/zh/gettingStarted'

import GettingStartedView from '../GettingStartedView.vue'

const publicViewDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const gettingStartedDir = resolve(publicViewDir, '..', '..', 'components', 'getting-started')

function readSource(path: string): string {
  return readFileSync(path, 'utf8')
}

const { getGuideState, patchGuideState } = vi.hoisted(() => ({
  getGuideState: vi.fn(),
  patchGuideState: vi.fn()
}))

vi.mock('@/api/beginnerGuide', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/beginnerGuide')>()
  return {
    ...actual,
    getBeginnerGuideState: getGuideState,
    patchBeginnerGuideState: patchGuideState
  }
})

function progress(
  overrides: Partial<BeginnerGuideProgressV1> = {}
): BeginnerGuideProgressV1 {
  return {
    version: 1,
    client: 'claude_code',
    os: 'macos',
    currentStep: 'understand',
    completedSteps: [],
    ...overrides
  }
}

function setAnonymousProgress(value: BeginnerGuideProgressV1): void {
  localStorage.setItem('beginner_guide_progress_v1', JSON.stringify(value))
}

function userFixture(id = 42) {
  return {
    id,
    username: 'beginner',
    email: 'beginner@example.test',
    role: 'user' as const,
    balance: 0,
    concurrency: 1,
    status: 'active' as const,
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
    id: 987654321,
    user_id: 42,
    key: 'sk-guide-secret-DO-NOT-PERSIST',
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

function keyPage(items: ApiKey[]) {
  return { items, total: items.length, page: 1, page_size: 100, pages: items.length ? 1 : 0 }
}

function storageValues(storage: Storage): string[] {
  return Array.from({ length: storage.length }, (_, index) => {
    const key = storage.key(index)
    return key === null ? '' : (storage.getItem(key) ?? '')
  })
}

function progressAt(step: BeginnerGuideStepId): BeginnerGuideProgressV1 {
  const order: BeginnerGuideStepId[] = [
    'understand',
    'choose',
    'terminal',
    'install',
    'api_key',
    'configure',
    'first_run',
    'troubleshoot'
  ]
  return progress({
    currentStep: step,
    completedSteps: order.slice(0, order.indexOf(step))
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
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

function localeMessages(messages: typeof enMessages) {
  return runtimeMessages({
    ...messages,
    home: {
      switchToDark: 'Switch to dark mode',
      switchToLight: 'Switch to light mode',
      login: 'Sign in',
      goToDashboard: 'Go to Dashboard'
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
      },
      useKeyModal: {
        openai: {
          configTomlHint: 'Keep this content at the beginning of config.toml.'
        }
      }
    }
  }) as Record<string, unknown>
}

function mountView(locale: 'en' | 'zh' = 'en', attachTo?: Element) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createI18n({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: {
      en: localeMessages(enMessages),
      zh: localeMessages(zhMessages)
    }
  })
  const wrapper = mount(GettingStartedView, {
    ...(attachTo ? { attachTo } : {}),
    global: {
      plugins: [pinia, i18n],
      stubs: { RouterLink: RouterLinkStub, LocaleSwitcher: true }
    }
  })
  return { wrapper, i18n, guideStore: useBeginnerGuideStore(), authStore: useAuthStore() }
}

function mountAuthenticatedView(userId = 42, pinia = createPinia()) {
  setActivePinia(pinia)
  const authStore = useAuthStore()
  authStore.user = userFixture(userId)
  authStore.token = 'test-session-token'
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    fallbackLocale: 'en',
    messages: {
      en: localeMessages(enMessages),
      zh: localeMessages(zhMessages)
    }
  })
  const wrapper = mount(GettingStartedView, {
    global: {
      plugins: [pinia, i18n],
      stubs: { RouterLink: RouterLinkStub, LocaleSwitcher: true }
    }
  })
  return { wrapper, guideStore: useBeginnerGuideStore(), authStore, pinia }
}

async function settle(): Promise<void> {
  await flushPromises()
  await flushPromises()
}

describe('GettingStartedView', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    window.history.replaceState(null, '', '/getting-started')
    vi.clearAllMocks()
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'MacIntel' })
    getGuideState.mockResolvedValue({
      prompt_state: 'eligible',
      progress: null,
      completed_at: null
    })
    patchGuideState.mockImplementation(async (patch) => ({
      prompt_state: patch.prompt_state ?? 'suppressed',
      progress: patch.progress ?? null,
      completed_at: patch.prompt_state === 'completed' ? '2026-07-15T00:00:00Z' : null
    }))
    vi.spyOn(keysAPI, 'list').mockResolvedValue({ items: [], total: 0, page: 1, page_size: 100, pages: 0 })
    vi.spyOn(keysAPI, 'create').mockResolvedValue(keyFixture())
  })

  it('mounts anonymously at understand without guide-account or key API calls', async () => {
    const { wrapper } = mountView()
    await settle()

    expect(wrapper.get('[data-active-step="understand"]').exists()).toBe(true)
    expect(getGuideState).not.toHaveBeenCalled()
    expect(keysAPI.list).not.toHaveBeenCalled()
  })

  it('initializes account-scoped progress for an authenticated visitor', async () => {
    mountAuthenticatedView()
    await settle()

    expect(getGuideState).toHaveBeenCalledOnce()
    expect(keysAPI.list).not.toHaveBeenCalled()
  })

  it('serializes a double-click while authenticated progress persistence is pending', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progress(),
      completed_at: null
    })
    const pendingPatch = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => pendingPatch.promise)
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()

    const next = wrapper.get('[data-testid="step-primary-action"]')
    await next.trigger('click')
    await next.trigger('click')

    expect.soft(next.attributes('disabled')).toBeDefined()
    expect.soft(next.attributes('aria-busy')).toBe('true')
    expect.soft(patchGuideState).toHaveBeenCalledOnce()

    pendingPatch.resolve({
      prompt_state: 'suppressed',
      progress: progress({ completedSteps: ['understand'] }),
      completed_at: null
    })
    await settle()
    await settle()

    expect(guideStore.progress.currentStep).toBe('choose')
    expect(guideStore.progress.completedSteps).toEqual(['understand'])
  })

  it('does not let a delayed Next continuation advance a newly active account', async () => {
    getGuideState
      .mockResolvedValueOnce({
        prompt_state: 'suppressed',
        progress: progress(),
        completed_at: null
      })
      .mockResolvedValueOnce({
        prompt_state: 'suppressed',
        progress: progress({
          currentStep: 'terminal',
          completedSteps: ['understand', 'choose']
        }),
        completed_at: null
      })
    const pendingPatch = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => pendingPatch.promise)
    const { wrapper, guideStore, authStore } = mountAuthenticatedView(42)
    await settle()

    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    authStore.user = userFixture(43)
    await settle()
    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(wrapper.get('[data-testid="step-primary-action"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="step-primary-action"]').attributes('aria-busy')).toBeUndefined()

    pendingPatch.resolve({
      prompt_state: 'suppressed',
      progress: progress({ completedSteps: ['understand'] }),
      completed_at: null
    })
    await settle()
    await settle()

    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose'])
  })

  it('does not continue a deferred Next transition after the view unmounts', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progress(),
      completed_at: null
    })
    const pendingPatch = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => pendingPatch.promise)
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()

    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    expect(patchGuideState).toHaveBeenCalledOnce()
    wrapper.unmount()

    pendingPatch.resolve({
      prompt_state: 'suppressed',
      progress: progress({ completedSteps: ['understand'] }),
      completed_at: null
    })
    await settle()
    await settle()

    expect(patchGuideState).toHaveBeenCalledOnce()
    expect(guideStore.progress.currentStep).toBe('understand')
  })

  it('keeps terminal first incomplete when the client changes during a pending Next', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progress({
        currentStep: 'terminal',
        completedSteps: ['understand', 'choose']
      }),
      completed_at: null
    })
    const pendingPatch = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => pendingPatch.promise)
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()

    const next = wrapper.get('[data-testid="step-primary-action"]')
    const codexOption = wrapper.get('[data-client-option="codex"]')
    await next.trigger('click')

    expect.soft(codexOption.attributes('disabled')).toBeDefined()
    expect.soft(codexOption.attributes('aria-disabled')).toBe('true')
    await codexOption.trigger('click')
    expect.soft(guideStore.progress.client).toBe('claude_code')
    expect.soft(patchGuideState).toHaveBeenCalledOnce()

    const externalChange = guideStore.selectClient('codex')
    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose'])

    pendingPatch.resolve({
      prompt_state: 'suppressed',
      progress: progress({
        currentStep: 'terminal',
        completedSteps: ['understand', 'choose', 'terminal']
      }),
      completed_at: null
    })
    await externalChange
    await settle()
    await settle()

    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose'])
    expect(next.attributes('disabled')).toBeUndefined()
    expect(next.attributes('aria-busy')).toBeUndefined()
    expect(codexOption.attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-client-option="claude_code"]').trigger('click')
    await settle()
    expect(guideStore.progress.client).toBe('claude_code')
  })

  it('keeps terminal first incomplete when the OS changes during a pending Next', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progress({
        currentStep: 'terminal',
        completedSteps: ['understand', 'choose']
      }),
      completed_at: null
    })
    const pendingPatch = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => pendingPatch.promise)
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()

    const next = wrapper.get('[data-testid="step-primary-action"]')
    const linuxOption = wrapper.get('[data-os-option="linux"]')
    await next.trigger('click')

    expect.soft(linuxOption.attributes('disabled')).toBeDefined()
    expect.soft(linuxOption.attributes('aria-disabled')).toBe('true')
    await linuxOption.trigger('click')
    expect.soft(guideStore.progress.os).toBe('macos')
    expect.soft(patchGuideState).toHaveBeenCalledOnce()

    const externalChange = guideStore.selectOS('linux')
    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose'])

    pendingPatch.resolve({
      prompt_state: 'suppressed',
      progress: progress({
        currentStep: 'terminal',
        completedSteps: ['understand', 'choose', 'terminal']
      }),
      completed_at: null
    })
    await externalChange
    await settle()
    await settle()

    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose'])
    expect(next.attributes('disabled')).toBeUndefined()
    expect(next.attributes('aria-busy')).toBeUndefined()
    expect(linuxOption.attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-os-option="macos"]').trigger('click')
    await settle()
    expect(guideStore.progress.os).toBe('macos')
  })

  it('offers exactly the approved clients and operating systems', async () => {
    const { wrapper } = mountView()
    await settle()

    expect(wrapper.findAll('[data-client-option]').map((node) => node.attributes('data-client-option')))
      .toEqual(['claude_code', 'codex'])
    expect(wrapper.findAll('[data-os-option]').map((node) => node.attributes('data-os-option')))
      .toEqual(['macos', 'windows', 'linux'])
  })

  it('labels selector groups and exposes keyboard-operable native buttons', async () => {
    const { wrapper } = mountView()
    await settle()

    const selectorGroups = wrapper.findAll('fieldset')
    expect(selectorGroups).toHaveLength(2)
    expect(selectorGroups.map((group) => group.get('legend').text())).toEqual([
      'Choose your client',
      'Choose your operating system'
    ])

    const options = wrapper.findAll('[data-client-option], [data-os-option]')
    expect(options).toHaveLength(5)
    for (const option of options) {
      expect(option.element.tagName).toBe('BUTTON')
      expect(option.attributes('type')).toBe('button')
    }
  })

  it('marks the current step and closes the mobile dialog with Escape while restoring focus', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const { wrapper } = mountView('en', host)
    try {
      await settle()

      const currentSteps = wrapper.findAll('[data-guide-step][aria-current="step"]')
      expect(currentSteps).toHaveLength(1)
      expect(currentSteps[0].attributes('data-guide-step')).toBe('understand')

      const trigger = wrapper.get('[data-testid="mobile-step-menu-button"]')
      trigger.element.focus()
      expect(trigger.attributes('aria-expanded')).toBe('false')
      await trigger.trigger('click')

      expect(trigger.attributes('aria-expanded')).toBe('true')
      const drawer = wrapper.get('[data-testid="mobile-step-drawer"]')
      expect(drawer.attributes('role')).toBe('dialog')
      expect(drawer.attributes('aria-modal')).toBe('true')
      expect(document.activeElement).toBe(
        wrapper.get('[data-testid="mobile-step-menu-close"]').element
      )

      const close = wrapper.get('[data-testid="mobile-step-menu-close"]')
      const reachableSteps = wrapper
        .findAll('[data-testid="mobile-step-drawer"] [data-guide-step]')
        .filter((step) => step.attributes('disabled') === undefined)
      const lastStep = reachableSteps.at(-1)
      expect(lastStep).toBeDefined()
      lastStep!.element.focus()
      await lastStep!.trigger('keydown', { key: 'Tab' })
      expect(document.activeElement).toBe(close.element)

      close.element.focus()
      await close.trigger('keydown', { key: 'Tab', shiftKey: true })
      expect(document.activeElement).toBe(lastStep!.element)

      await drawer.trigger('keydown', { key: 'Escape' })

      expect(wrapper.find('[data-testid="mobile-step-drawer"]').exists()).toBe(false)
      expect(trigger.attributes('aria-expanded')).toBe('false')
      expect(document.activeElement).toBe(trigger.element)
    } finally {
      wrapper.unmount()
      host.remove()
    }
  })

  it('keeps focus, overflow, reduced-motion, and escaped-rendering contracts in guide sources', () => {
    const sources = {
      view: readSource(resolve(publicViewDir, 'GettingStartedView.vue')),
      shell: readSource(resolve(gettingStartedDir, 'GuideShell.vue')),
      progress: readSource(resolve(gettingStartedDir, 'GuideProgressNav.vue')),
      command: readSource(resolve(gettingStartedDir, 'GuideCommandBlock.vue')),
      apiKey: readSource(resolve(gettingStartedDir, 'GuideApiKeyStep.vue'))
    }

    for (const [name, source] of Object.entries(sources)) {
      expect.soft(source, `${name} has a visible keyboard focus ring`).toContain(
        'focus-visible:ring-2'
      )
      expect.soft(source, `${name} disables nonessential reduced-motion transitions`).toContain(
        'motion-reduce:transition-none'
      )
      expect.soft(source, `${name} never renders trusted HTML`).not.toContain('v-html')
    }

    expect(sources.command).toContain('overflow-x-auto')
    expect(sources.command).toContain('min-w-0')
    expect(sources.view).toContain('min-w-0')
    expect(sources.shell).toContain('min-w-0')
    expect(sources.shell).not.toMatch(/min-h-screen[^"\n]*overflow-x-(?:auto|hidden|scroll)/)
  })

  it('uses browser OS only as an initial suggestion and keeps every manual choice enabled', async () => {
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'Win32' })
    const { wrapper, guideStore } = mountView()
    await settle()

    expect(guideStore.progress.os).toBe('windows')
    expect(wrapper.findAll('[data-os-option]').every((node) => !node.attributes('disabled'))).toBe(true)

    await wrapper.get('[data-os-option="linux"]').trigger('click')
    await settle()
    expect(guideStore.progress.os).toBe('linux')
  })

  it.each([
    ['corrupt JSON', '{not-json'],
    [
      'obsolete progress',
      JSON.stringify({ ...progress({ os: 'linux' }), version: 2 })
    ]
  ])('uses browser OS after discarding %s from anonymous storage', async (_label, stored) => {
    localStorage.setItem('beginner_guide_progress_v1', stored)
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'Win32' })

    const { guideStore } = mountView()
    await settle()

    expect(guideStore.progress.os).toBe('windows')
  })

  it('does not override a valid persisted manual OS with browser detection', async () => {
    setAnonymousProgress(progress({ os: 'linux' }))
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'Win32' })

    const { guideStore } = mountView()
    await settle()

    expect(guideStore.progress.os).toBe('linux')
  })

  it.each([
    ['client', '[data-client-option="codex"]'],
    ['OS', '[data-os-option="linux"]']
  ])('invalidates only variant-specific completion after changing %s', async (_label, selector) => {
    setAnonymousProgress(
      progress({
        currentStep: 'troubleshoot',
        completedSteps: [
          'understand',
          'choose',
          'terminal',
          'install',
          'api_key',
          'configure',
          'first_run',
          'troubleshoot'
        ]
      })
    )
    const { wrapper, guideStore } = mountView()
    await settle()

    await wrapper.get(selector).trigger('click')
    await settle()

    expect(guideStore.progress.currentStep).toBe('terminal')
    expect(guideStore.progress.completedSteps).toEqual(['understand', 'choose', 'api_key'])
  })

  it('does not change stable step IDs or reset completion when language changes', async () => {
    setAnonymousProgress(
      progress({ currentStep: 'choose', completedSteps: ['understand'] })
    )
    const { wrapper, i18n, guideStore } = mountView()
    await settle()
    const before = JSON.parse(JSON.stringify(guideStore.progress)) as BeginnerGuideProgressV1

    i18n.global.locale.value = 'zh'
    await wrapper.vm.$nextTick()

    expect(guideStore.progress).toEqual(before)
    expect(wrapper.findAll('[data-guide-step]').map((node) => node.attributes('data-guide-step')))
      .toEqual([
        'understand',
        'choose',
        'terminal',
        'install',
        'api_key',
        'configure',
        'first_run',
        'troubleshoot'
      ])
  })

  it('keeps command overflow inside a min-width-zero content column', async () => {
    setAnonymousProgress(progress({ currentStep: 'install', completedSteps: ['understand', 'choose', 'terminal'] }))
    const { wrapper } = mountView()
    await settle()

    expect(wrapper.get('[data-testid="guide-content-column"]').classes()).toContain('min-w-0')
    expect(wrapper.get('[data-testid="guide-command-block"] pre').classes()).toContain(
      'overflow-x-auto'
    )
  })

  it('keeps anonymous visitors at an API-free authentication checkpoint', async () => {
    setAnonymousProgress(progressAt('api_key'))
    const { wrapper } = mountView()
    await settle()

    expect(wrapper.getComponent('[data-testid="api-key-login"]').props('to')).toBe(
      '/login?redirect=/getting-started'
    )
    expect(wrapper.getComponent('[data-testid="api-key-register"]').props('to')).toBe(
      '/register?redirect=/getting-started'
    )
    expect(wrapper.get('[data-testid="step-primary-action"]').attributes('disabled')).toBeDefined()
    expect(keysAPI.list).not.toHaveBeenCalled()
  })

  it('gates API-key completion on an in-memory selection and renders shared configuration', async () => {
    const secret = 'sk-guide-secret-DO-NOT-PERSIST'
    const key = keyFixture({ key: secret })
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(keyPage([key]))
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()

    const next = wrapper.get('[data-testid="step-primary-action"]')
    expect(next.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-key-id="987654321"]').trigger('click')
    expect(next.attributes('disabled')).toBeUndefined()

    await next.trigger('click')
    await settle()

    expect(guideStore.progress.currentStep).toBe('configure')
    expect(wrapper.findAll('[data-testid="guide-config-file"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('Terminal')
    expect(wrapper.text()).toContain('~/.claude/settings.json')
    expect(wrapper.text()).toContain('merge these settings')
    expect(wrapper.text()).toContain('fully close the client')
    expect(wrapper.text()).toContain(secret)

    for (const [patch] of patchGuideState.mock.calls) {
      const serialized = JSON.stringify(patch)
      expect(serialized).not.toContain(secret)
      expect(serialized).not.toContain('987654321')
      expect(serialized).not.toContain('selectedKey')
      expect(serialized).not.toContain('generatedFiles')
    }
    expect(window.location.href).not.toContain(secret)
    expect(JSON.stringify(window.history.state)).not.toContain(secret)
    expect(Object.values(localStorage).join('\n')).not.toContain(secret)
    expect(Object.values(sessionStorage).join('\n')).not.toContain(secret)
  })

  it('keeps one hostile secret only in active Claude and Codex configuration memory', async () => {
    const secret = 'sk-guide-secret-DO-NOT-PERSIST'
    const key = keyFixture({ key: secret })
    let accountProgress = progressAt('api_key')
    let accountPromptState: 'suppressed' | 'completed' = 'suppressed'

    getGuideState.mockImplementation(async () => ({
      prompt_state: accountPromptState,
      progress: structuredClone(accountProgress),
      completed_at: accountPromptState === 'completed' ? '2026-07-15T00:00:00Z' : null
    }))
    patchGuideState.mockImplementation(async (patch) => {
      if (patch.progress) accountProgress = structuredClone(patch.progress)
      if (patch.prompt_state) accountPromptState = patch.prompt_state
      return {
        prompt_state: accountPromptState,
        progress: structuredClone(accountProgress),
        completed_at: accountPromptState === 'completed' ? '2026-07-15T00:00:00Z' : null
      }
    })
    vi.mocked(keysAPI.list).mockResolvedValue(keyPage([key]))

    const pinia = createPinia()
    setActivePinia(pinia)
    const appStore = useAppStore()
    const warningCalls = vi.spyOn(appStore, 'showWarning')
    const errorCalls = vi.spyOn(appStore, 'showError')
    const first = mountAuthenticatedView(42, pinia)
    await settle()

    await first.wrapper.get('[data-key-id="987654321"]').trigger('click')
    await first.wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()

    const claudeFiles = first.wrapper.findAll('[data-testid="guide-config-file"]')
    expect(claudeFiles).toHaveLength(2)
    expect(claudeFiles.every((file) => file.text().includes(secret))).toBe(true)

    await first.wrapper.get('[data-active-step="configure"] footer button').trigger('click')
    await settle()
    await first.wrapper.get('[data-client-option="codex"]').trigger('click')
    await settle()
    await first.wrapper.get('[data-key-id="987654321"]').trigger('click')
    await first.wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()

    const codexFiles = first.wrapper.findAll('[data-testid="guide-config-file"]')
    expect(codexFiles).toHaveLength(2)
    expect(codexFiles.find((file) => file.text().includes('config.toml'))?.text()).not.toContain(
      secret
    )
    expect(codexFiles.find((file) => file.text().includes('auth.json'))?.text()).toContain(secret)

    first.wrapper.unmount()

    expect(window.location.href).not.toContain(secret)
    expect(storageValues(localStorage)).not.toContainEqual(expect.stringContaining(secret))
    expect(storageValues(sessionStorage)).not.toContainEqual(expect.stringContaining(secret))
    for (const [patch] of patchGuideState.mock.calls) {
      expect(JSON.stringify(patch)).not.toContain(secret)
    }
    for (const call of [...warningCalls.mock.calls, ...errorCalls.mock.calls]) {
      expect(JSON.stringify(call)).not.toContain(secret)
    }
    expect(JSON.stringify(appStore.toasts)).not.toContain(secret)

    const secondPinia = createPinia()
    setActivePinia(secondPinia)
    const secondAppStore = useAppStore()
    const secondWarningCalls = vi.spyOn(secondAppStore, 'showWarning')
    const secondErrorCalls = vi.spyOn(secondAppStore, 'showError')
    const second = mountAuthenticatedView(42, secondPinia)
    await settle()
    await settle()

    expect(JSON.stringify(second.guideStore.progress)).not.toContain(secret)
    expect(second.wrapper.text()).not.toContain(secret)
    expect(storageValues(localStorage)).not.toContainEqual(expect.stringContaining(secret))
    expect(storageValues(sessionStorage)).not.toContainEqual(expect.stringContaining(secret))
    for (const [patch] of patchGuideState.mock.calls) {
      expect(JSON.stringify(patch)).not.toContain(secret)
    }
    for (const call of [...secondWarningCalls.mock.calls, ...secondErrorCalls.mock.calls]) {
      expect(JSON.stringify(call)).not.toContain(secret)
    }
    expect(JSON.stringify(secondAppStore.toasts)).not.toContain(secret)
  })

  it('replaces prior generated files when a different key is selected', async () => {
    const first = keyFixture({ id: 1, key: 'sk-first-memory-only', name: 'First key' })
    const second = keyFixture({ id: 2, key: 'sk-second-memory-only', name: 'Second key' })
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValue(keyPage([first, second]))
    const { wrapper } = mountAuthenticatedView()
    await settle()

    await wrapper.get('[data-key-id="1"]').trigger('click')
    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain(first.key)

    await wrapper.get('[data-active-step="configure"] footer button').trigger('click')
    await settle()
    await wrapper.get('[data-key-id="2"]').trigger('click')
    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()

    expect(wrapper.text()).toContain(second.key)
    expect(wrapper.text()).not.toContain(first.key)
  })

  it('clears selection on client changes and requires another explicit selection', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(keyPage([keyFixture()]))
    const { wrapper } = mountAuthenticatedView()
    await settle()

    await wrapper.get('[data-key-id="987654321"]').trigger('click')
    expect(wrapper.get('[data-testid="step-primary-action"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-client-option="codex"]').trigger('click')
    await settle()

    expect(wrapper.get('[data-testid="step-primary-action"]').attributes('disabled')).toBeDefined()
  })

  it('clears secret-bearing configuration when the authenticated owner logs out', async () => {
    const secret = 'sk-guide-secret-DO-NOT-PERSIST'
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValueOnce(keyPage([keyFixture({ key: secret })]))
    const { wrapper, authStore } = mountAuthenticatedView()
    await settle()

    await wrapper.get('[data-key-id="987654321"]').trigger('click')
    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain(secret)

    authStore.token = null
    await settle()

    expect(wrapper.text()).not.toContain(secret)
    expect(Object.values(localStorage).join('\n')).not.toContain(secret)
    expect(Object.values(sessionStorage).join('\n')).not.toContain(secret)
  })

  it('returns a refreshed configure step to key selection without recovering a secret', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('configure'),
      completed_at: null
    })
    const { wrapper, guideStore } = mountAuthenticatedView()
    await settle()
    await settle()

    expect(guideStore.progress.currentStep).toBe('api_key')
    expect(wrapper.get('[data-testid="api-key-reselect"]').text()).toContain(
      'selected key is not saved'
    )
    expect(wrapper.text()).not.toContain('sk-guide-secret-DO-NOT-PERSIST')
  })

  it('still requires reselection for a new owner while the previous owner redirect save is pending', async () => {
    getGuideState
      .mockResolvedValueOnce({
        prompt_state: 'suppressed',
        progress: progressAt('configure'),
        completed_at: null
      })
      .mockResolvedValueOnce({
        prompt_state: 'suppressed',
        progress: progressAt('configure'),
        completed_at: null
      })
    const accountARedirect = deferred<{
      prompt_state: 'suppressed'
      progress: BeginnerGuideProgressV1
      completed_at: null
    }>()
    patchGuideState.mockImplementationOnce(() => accountARedirect.promise)
    const { wrapper, guideStore, authStore } = mountAuthenticatedView(42)
    await settle()
    expect(guideStore.progress.currentStep).toBe('api_key')

    authStore.user = userFixture(43)
    await settle()

    expect(guideStore.progress.currentStep).toBe('api_key')
    expect(wrapper.get('[data-testid="api-key-reselect"]').exists()).toBe(true)

    accountARedirect.resolve({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    await settle()
    expect(guideStore.progress.currentStep).toBe('api_key')
  })

  it('does not recover a prior selected key or configuration after unmounting', async () => {
    const secret = 'sk-guide-secret-DO-NOT-PERSIST'
    getGuideState.mockResolvedValue({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValue(keyPage([keyFixture({ key: secret })]))
    const first = mountAuthenticatedView()
    await settle()
    await first.wrapper.get('[data-key-id="987654321"]').trigger('click')
    await first.wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()
    expect(first.wrapper.text()).toContain(secret)
    first.wrapper.unmount()

    const second = mountAuthenticatedView()
    await settle()

    expect(second.wrapper.text()).not.toContain(secret)
    expect(second.wrapper.get('[data-testid="step-primary-action"]').attributes('disabled')).toBeDefined()
  })

  it('reconciles configure immediately when remounting with the same Pinia instance', async () => {
    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('api_key'),
      completed_at: null
    })
    vi.mocked(keysAPI.list).mockResolvedValue(keyPage([keyFixture()]))
    const first = mountAuthenticatedView()
    await settle()
    await first.wrapper.get('[data-key-id="987654321"]').trigger('click')
    await first.wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()
    expect(first.guideStore.progress.currentStep).toBe('configure')
    first.wrapper.unmount()

    getGuideState.mockResolvedValueOnce({
      prompt_state: 'suppressed',
      progress: progressAt('configure'),
      completed_at: null
    })
    const second = mountAuthenticatedView(42, first.pinia)
    await settle()
    await settle()

    expect(second.guideStore.progress.currentStep).toBe('api_key')
    expect(second.wrapper.get('[data-testid="api-key-reselect"]').exists()).toBe(true)
    expect(second.wrapper.text()).not.toContain('sk-guide-secret-DO-NOT-PERSIST')
  })

  it('marks troubleshooting complete explicitly and shows the three destination links', async () => {
    setAnonymousProgress(
      progress({
        currentStep: 'troubleshoot',
        completedSteps: ['understand', 'choose', 'terminal', 'install', 'api_key', 'configure', 'first_run']
      })
    )
    const { wrapper, guideStore } = mountView()
    await settle()

    await wrapper.get('[data-testid="step-primary-action"]').trigger('click')
    await settle()

    expect(guideStore.promptState).toBe('completed')
    expect(wrapper.findAll('[data-testid="completion-link"]')).toHaveLength(3)
  })
})
