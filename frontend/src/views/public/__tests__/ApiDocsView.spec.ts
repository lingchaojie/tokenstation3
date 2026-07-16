import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import enApiDocs from '@/i18n/locales/en/apiDocs'
import zhApiDocs from '@/i18n/locales/zh/apiDocs'
import { useAppStore } from '@/stores/app'

import ApiDocsView from '../ApiDocsView.vue'

enableAutoUnmount(afterEach)

type RuntimeMessage = (() => string) | { [key: string]: RuntimeMessage }

function asRuntimeMessage(value: unknown): RuntimeMessage {
  if (typeof value === 'string') return () => value
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, nestedValue]) => [key, asRuntimeMessage(nestedValue)])
    )
  }
  return () => String(value ?? '')
}

function createDocsI18n(locale: 'en' | 'zh' = 'en') {
  return createI18n({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: {
      en: asRuntimeMessage({
        ...enApiDocs,
        common: { close: 'Close' },
        home: {
          switchToLight: 'Switch to Light Mode',
          switchToDark: 'Switch to Dark Mode'
        }
      }),
      zh: asRuntimeMessage({
        ...zhApiDocs,
        common: { close: '关闭' },
        home: {
          switchToLight: '切换到浅色模式',
          switchToDark: '切换到深色模式'
        }
      })
    }
  })
}

interface MountOptions {
  locale?: 'en' | 'zh'
  publicSettingsLoaded?: boolean
  configureSettingsLoad?: (appStore: ReturnType<typeof useAppStore>) => void
}

async function mountView(path: string, options: MountOptions = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createDocsI18n(options.locale)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', name: 'ApiDocs', component: ApiDocsView },
      {
        path: '/docs/:section(guide|api-reference|platform)/:slug',
        name: 'ApiDocsPage',
        component: ApiDocsView
      },
      { path: '/home', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
      { path: '/dashboard', component: { template: '<div />' } },
      { path: '/admin/my-account/dashboard', component: { template: '<div />' } },
      { path: '/keys', component: { template: '<div />' } },
      { path: '/getting-started', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'NotFound', component: ApiDocsView }
    ]
  })
  const appStore = useAppStore()
  appStore.siteName = 'Developer Portal'
  appStore.apiBaseUrl = 'https://gateway.example.test/v1/'
  appStore.publicSettingsLoaded = options.publicSettingsLoaded ?? true
  options.configureSettingsLoad?.(appStore)

  await router.push(path)
  await router.isReady()
  const wrapper = mount(ApiDocsView, {
    attachTo: document.body,
    global: {
      plugins: [pinia, i18n, router],
      stubs: {
        LocaleSwitcher: { template: '<button type="button">Locale</button>' },
        LinxWordmark: { template: '<span>LINX2.AI</span>' }
      }
    }
  })
  await flushPromises()
  await nextTick()

  return { appStore, i18n, router, wrapper }
}

beforeEach(() => {
  document.title = ''
  document.body.style.overflow = ''
  document.documentElement.classList.remove('dark')
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
  document.body.style.overflow = ''
})

describe('ApiDocsView', () => {
  it('renders the canonical quickstart with normalized runtime Base URL and localized anchors', async () => {
    let settingsSpy: ReturnType<typeof vi.spyOn> | undefined
    const { wrapper } = await mountView('/docs', {
      configureSettingsLoad(store) {
        settingsSpy = vi.spyOn(store, 'fetchPublicSettings')
      }
    })

    expect(wrapper.get('[data-testid="api-docs-guide-title"]').text()).toBe('Quickstart')
    expect(wrapper.text()).toContain('Authenticate, choose a Base URL, and make your first request.')
    expect(wrapper.text()).toContain('https://gateway.example.test/v1')
    expect(wrapper.text()).not.toContain('/v1/v1')
    expect(document.title).toBe('Quickstart - Developer Portal')
    expect(settingsSpy).not.toHaveBeenCalled()

    const tocLinks = wrapper.get('[data-testid="api-docs-toc"]').findAll('a')
    expect(tocLinks.map((link) => [link.attributes('href'), link.text()])).toEqual([
      ['#base-url', 'Base URL'],
      ['#api-key', 'API key'],
      ['#first-request', 'First request'],
      ['#available-models', 'Available models']
    ])
    expect(wrapper.get('#base-url').element.parentElement?.classList).toContain('scroll-mt-24')
  })

  it('selects the endpoint renderer and keeps the page title and TOC localized', async () => {
    const { i18n, router, wrapper } = await mountView('/docs/api-reference/responses')

    expect(wrapper.get('[data-testid="endpoint-method"]').text()).toBe('POST')
    expect(wrapper.get('[data-testid="endpoint-path"]').text()).toBe('/v1/responses')
    expect(wrapper.find('[data-testid="api-docs-guide-title"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('https://gateway.example.test/v1/responses')
    expect(document.title).toBe('Responses - Developer Portal')
    expect(router.currentRoute.value.path).toBe('/docs/api-reference/responses')
    expect(
      wrapper.get('[data-testid="api-docs-toc"]').findAll('a').map((link) => link.text())
    ).toEqual([
      'Overview',
      'Authentication',
      'Parameters',
      'Request',
      'Response',
      'Streaming',
      'Errors'
    ])

    i18n.global.locale.value = 'zh'
    await nextTick()
    await nextTick()

    expect(document.title).toBe('响应 - Developer Portal')
    expect(router.currentRoute.value.path).toBe('/docs/api-reference/responses')
    expect(
      wrapper.get('[data-testid="api-docs-toc"]').findAll('a').map((link) => link.text())
    ).toEqual(['概述', '身份验证', '参数', '请求', '响应', '流式响应', '错误'])
  })

  it('renders a documentation-scoped not-found state for excluded or unknown slugs', async () => {
    const { router, wrapper } = await mountView('/docs/api-reference/embeddings')

    expect(router.currentRoute.value.name).toBe('ApiDocsPage')
    expect(wrapper.get('[data-testid="api-docs-not-found"]').text()).toContain(
      'Documentation page not found'
    )
    expect(wrapper.get('[data-testid="api-docs-not-found-home"]').attributes('href')).toBe(
      '/docs'
    )
    expect(wrapper.find('[data-testid="api-docs-sidebar"] [aria-current="page"]').exists()).toBe(
      false
    )
    expect(document.title).toBe('API Docs - Developer Portal')

    await wrapper.get('[data-testid="api-docs-not-found-search"]').trigger('click')
    await nextTick()

    expect(wrapper.get('#api-docs-search-dialog').attributes('role')).toBe('dialog')
  })

  it('navigates to a selected localized search result without authentication state', async () => {
    const { router, wrapper } = await mountView('/docs')

    await wrapper.get('[data-testid="api-docs-search-open"]').trigger('click')
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')
    await input.setValue('responses')
    await wrapper.get('[data-testid="api-docs-search-result"]').trigger('click')
    await flushPromises()
    await nextTick()

    expect(router.currentRoute.value.path).toBe('/docs/api-reference/responses')
    expect(wrapper.get('[data-testid="endpoint-path"]').text()).toBe('/v1/responses')
    expect(document.title).toBe('Responses - Developer Portal')
  })

  it('loads public settings once when the existing app-store cache is not ready', async () => {
    let fetchSettings: ReturnType<typeof vi.spyOn> | undefined
    const { appStore, wrapper } = await mountView('/docs', {
      publicSettingsLoaded: false,
      configureSettingsLoad(store) {
        fetchSettings = vi.spyOn(store, 'fetchPublicSettings').mockImplementation(async () => {
          store.siteName = 'Loaded Portal'
          store.apiBaseUrl = 'https://loaded.example.test/'
          store.publicSettingsLoaded = true
          return null
        })
      }
    })

    expect(fetchSettings).toHaveBeenCalledTimes(1)
    expect(appStore.publicSettingsLoaded).toBe(true)
    expect(wrapper.text()).toContain('https://loaded.example.test/v1')
    expect(document.title).toBe('Quickstart - Loaded Portal')
  })
})
