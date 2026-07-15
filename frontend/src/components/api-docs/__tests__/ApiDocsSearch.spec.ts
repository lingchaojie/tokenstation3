import { enableAutoUnmount, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import enApiDocs from '@/i18n/locales/en/apiDocs'
import zhApiDocs from '@/i18n/locales/zh/apiDocs'
import { API_DOCS_PAGES } from '../catalog'
import ApiDocsSearch from '../ApiDocsSearch.vue'
import ApiDocsShell from '../ApiDocsShell.vue'
import { buildApiDocsSearchEntries } from '../search'
import type { ApiDocsSearchEntry } from '../search'

enableAutoUnmount(afterEach)

const translations: Record<string, string> = {
  'apiDocs.pages.quickstart.title': 'Localized Quickstart',
  'apiDocs.pages.quickstart.summary': 'Localized first request summary',
  'apiDocs.pages.responses.title': 'Localized Responses',
  'apiDocs.pages.responses.summary': 'Localized structured response summary'
}

const translate = (key: string): string => translations[key] ?? key

describe('buildApiDocsSearchEntries', () => {
  it('indexes localized page copy, endpoint paths, error codes, and catalog keywords', () => {
    const entries = buildApiDocsSearchEntries(translate)
    const quickstart = entries.find(({ id }) => id === 'quickstart')
    const responses = entries.find(({ id }) => id === 'responses')

    expect(quickstart).toMatchObject({
      path: '/docs',
      title: 'Localized Quickstart',
      section: 'guide'
    })
    expect(quickstart?.text).toContain('localized first request summary')
    expect(responses?.text).toContain('/v1/responses')
    expect(responses?.text).toContain('invalid_api_key')
    expect(responses?.text).toContain('reasoning')
  })

  it('contains only the approved catalog in stable catalog order', () => {
    const first = buildApiDocsSearchEntries(translate)
    const second = buildApiDocsSearchEntries(translate)

    expect(first).toEqual(second)
    expect(first.map(({ id, path }) => ({ id, path }))).toEqual(
      API_DOCS_PAGES.map(({ id, path }) => ({ id, path }))
    )
    expect(JSON.stringify(first)).not.toMatch(
      /gemini|embeddings?|video|failover|internal|admin|balance|stability/i
    )
  })
})

const searchEntries: ApiDocsSearchEntry[] = [
  {
    id: 'responses',
    path: '/docs/api-reference/responses',
    title: 'Responses',
    section: 'endpoint',
    text: 'responses openai structured input /v1/responses invalid_api_key'
  },
  {
    id: 'messages',
    path: '/docs/api-reference/messages',
    title: 'Messages',
    section: 'endpoint',
    text: 'messages anthropic streaming /v1/messages invalid_api_key'
  },
  {
    id: 'quickstart',
    path: '/docs',
    title: 'Quickstart',
    section: 'guide',
    text: 'quickstart openai first request models'
  }
]

function mountSearch(show = false) {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        apiDocs: {
          search: () => 'Search documentation',
          searchPlaceholder: () => 'Search guides and API reference',
          noResults: () => 'No documentation pages matched your search.'
        },
        common: { close: () => 'Close' }
      }
    }
  })

  return mount(ApiDocsSearch, {
    attachTo: document.body,
    props: { show, entries: searchEntries },
    global: { plugins: [i18n] }
  })
}

describe('ApiDocsSearch', () => {
  it('shows all entries in catalog order for an empty query and filters with token AND matching', async () => {
    const wrapper = mountSearch(true)
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')

    await nextTick()
    expect(document.activeElement).toBe(input.element)
    expect(
      wrapper.findAll('[data-testid="api-docs-search-result"]').map((result) => result.text())
    ).toEqual(['Responsesendpoint', 'Messagesendpoint', 'Quickstartguide'])

    await input.setValue('  OPENAI   responses  ')
    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="api-docs-search-result"]').text()).toContain('Responses')

    await input.setValue('openai messages')
    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(0)
    expect(wrapper.get('[role="status"]').text()).toBe(
      'No documentation pages matched your search.'
    )
  })

  it('selects a filtered result from the keyboard', async () => {
    const wrapper = mountSearch(true)
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')

    await input.setValue('responses')
    await input.trigger('keydown', { key: 'ArrowDown' })
    const result = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-search-result"]')
    expect(document.activeElement).toBe(result.element)

    await result.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toEqual([['/docs/api-reference/responses']])
  })

  it('closes on Escape or backdrop and restores focus after the controlled dialog closes', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const wrapper = mountSearch()

    await wrapper.setProps({ show: true })
    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('input[type="search"]').element)
    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    await wrapper.setProps({ show: false })
    await nextTick()
    expect(document.activeElement).toBe(trigger)

    await wrapper.setProps({ show: true })
    await nextTick()
    await wrapper.get('[data-testid="api-docs-search-backdrop"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(2)
    await wrapper.setProps({ show: false })
    await nextTick()
    expect(document.activeElement).toBe(trigger)
    trigger.remove()
  })

  it('is modal, labeled, focus-contained, and resets its query when reopened', async () => {
    const wrapper = mountSearch(true)
    const dialog = wrapper.get('[role="dialog"]')
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')

    expect(dialog.attributes('aria-modal')).toBe('true')
    expect(dialog.attributes('aria-labelledby')).toBe('api-docs-search-title')
    expect(input.attributes('placeholder')).toBe('Search guides and API reference')

    await input.setValue('messages')
    const lastResult = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-search-result"]')
    lastResult.element.focus()
    await dialog.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(input.element)
    await dialog.trigger('keydown', { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(lastResult.element)

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('input[type="search"]').element.value).toBe('')
    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(3)
  })
})

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

function createDocsI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
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

async function mountSearchShell() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createDocsI18n()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', component: { template: '<div />' } },
      { path: '/docs/:pathMatch(.*)*', component: { template: '<div />' } },
      { path: '/home', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } }
    ]
  })
  await router.push('/docs')
  await router.isReady()

  const wrapper = mount(ApiDocsShell, {
    attachTo: document.body,
    props: { currentPageId: 'quickstart', headings: [] },
    global: {
      plugins: [pinia, i18n, router],
      stubs: { LocaleSwitcher: true }
    }
  })

  return { i18n, router, wrapper }
}

async function settleDialog(): Promise<void> {
  await nextTick()
  await nextTick()
}

describe('ApiDocsShell search integration', () => {
  it('opens from slash, filters, navigates, and restores focus to the search trigger', async () => {
    const { router, wrapper } = await mountSearchShell()
    const routerPush = vi.spyOn(router, 'push').mockResolvedValue(undefined)
    const openButton = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-search-open"]')
    expect(openButton.attributes('aria-haspopup')).toBe('dialog')
    expect(openButton.attributes('aria-controls')).toBe('api-docs-search-dialog')
    expect(openButton.attributes('aria-expanded')).toBe('false')
    openButton.element.focus()
    const shortcut = new KeyboardEvent('keydown', { key: '/', cancelable: true })

    window.dispatchEvent(shortcut)
    await settleDialog()

    expect(shortcut.defaultPrevented).toBe(true)
    expect(openButton.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[role="dialog"]').exists()).toBe(true)
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')
    expect(document.activeElement).toBe(input.element)
    await input.setValue('responses')
    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(1)

    await wrapper.get('[data-testid="api-docs-search-result"]').trigger('click')
    await settleDialog()
    expect(routerPush).toHaveBeenCalledWith('/docs/api-reference/responses')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(openButton.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(openButton.element)
  })

  it('supports Ctrl/Command+K and ignores slash in editable controls', async () => {
    const { wrapper } = await mountSearchShell()
    const editables = [
      document.createElement('input'),
      document.createElement('textarea'),
      document.createElement('div')
    ]
    editables[2].setAttribute('contenteditable', 'true')
    editables[2].tabIndex = 0

    for (const editable of editables) {
      document.body.append(editable)
      editable.focus()
      const ignoredSlash = new KeyboardEvent('keydown', {
        key: '/',
        bubbles: true,
        cancelable: true
      })
      editable.dispatchEvent(ignoredSlash)
      await settleDialog()
      expect(ignoredSlash.defaultPrevented).toBe(false)
      expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
      editable.remove()
    }

    const ctrlShortcut = new KeyboardEvent('keydown', {
      key: 'k',
      ctrlKey: true,
      cancelable: true
    })
    window.dispatchEvent(ctrlShortcut)
    await settleDialog()
    expect(ctrlShortcut.defaultPrevented).toBe(true)
    expect(wrapper.get('[role="dialog"]').exists()).toBe(true)

    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    await settleDialog()
    const metaShortcut = new KeyboardEvent('keydown', {
      key: 'K',
      metaKey: true,
      cancelable: true
    })
    window.dispatchEvent(metaShortcut)
    await settleDialog()
    expect(metaShortcut.defaultPrevented).toBe(true)
    expect(wrapper.get('[role="dialog"]').exists()).toBe(true)
  })

  it('rebuilds localized search entries when the locale changes without navigating', async () => {
    const { i18n, router, wrapper } = await mountSearchShell()
    const initialRoute = router.currentRoute.value.fullPath
    const openButton = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-search-open"]')
    openButton.element.focus()
    await openButton.trigger('click')
    await settleDialog()
    const input = wrapper.get<HTMLInputElement>('input[type="search"]')

    await input.setValue('聊天补全')
    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(0)
    i18n.global.locale.value = 'zh'
    await settleDialog()

    expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="api-docs-search-result"]').text()).toContain('聊天补全')
    expect(router.currentRoute.value.fullPath).toBe(initialRoute)
  })

  it('hands an open mobile drawer off to search without competing modal state', async () => {
    const { wrapper } = await mountSearchShell()
    await wrapper.get('[data-testid="api-docs-mobile-menu"]').trigger('click')
    expect(wrapper.get('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(true)

    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, cancelable: true })
    )
    await settleDialog()
    await settleDialog()

    expect(wrapper.find('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(false)
    expect(wrapper.get('[role="dialog"]').attributes('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(wrapper.get('input[type="search"]').element)
    expect(wrapper.get('[data-testid="api-docs-header"]').attributes()).toHaveProperty('inert')
    expect(document.body.style.overflow).toBe('hidden')

    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    await settleDialog()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="api-docs-header"]').attributes()).not.toHaveProperty('inert')
    expect(document.body.style.overflow).toBe('')
  })
})
