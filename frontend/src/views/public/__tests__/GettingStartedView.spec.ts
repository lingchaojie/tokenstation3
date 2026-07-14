import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { keysAPI } from '@/api/keys'
import type { BeginnerGuideProgressV1, BeginnerGuideStepId } from '@/api/beginnerGuide'
import { useAuthStore } from '@/stores/auth'
import { useBeginnerGuideStore } from '@/stores/beginnerGuide'
import enMessages from '@/i18n/locales/en/gettingStarted'
import zhMessages from '@/i18n/locales/zh/gettingStarted'

import GettingStartedView from '../GettingStartedView.vue'

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
    }
  }) as Record<string, unknown>
}

function mountView(locale: 'en' | 'zh' = 'en') {
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
    global: {
      plugins: [pinia, i18n],
      stubs: { RouterLink: RouterLinkStub, LocaleSwitcher: true }
    }
  })
  return { wrapper, i18n, guideStore: useBeginnerGuideStore(), authStore: useAuthStore() }
}

function mountAuthenticatedView(userId = 42) {
  const pinia = createPinia()
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
  return { wrapper, guideStore: useBeginnerGuideStore(), authStore }
}

async function settle(): Promise<void> {
  await flushPromises()
  await flushPromises()
}

describe('GettingStartedView', () => {
  beforeEach(() => {
    localStorage.clear()
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

  it('keeps Task 8 key and configuration interactions as API-free placeholders', async () => {
    for (const step of ['api_key', 'configure'] satisfies BeginnerGuideStepId[]) {
      setAnonymousProgress(progress({ currentStep: step, completedSteps: ['understand', 'choose', 'terminal', 'install'] }))
      const { wrapper } = mountView()
      await settle()

      expect(wrapper.get(`[data-active-step="${step}"] [data-testid="task-8-placeholder"]`).exists())
        .toBe(true)
      wrapper.unmount()
    }
    expect(keysAPI.list).not.toHaveBeenCalled()
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
