import { enableAutoUnmount, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import { authAPI } from '@/api'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import ApiDocsShell from '../ApiDocsShell.vue'

const intersectionObserverState = vi.hoisted(() => ({
  callback: undefined as IntersectionObserverCallback | undefined,
  targets: undefined as { value: HTMLElement[] } | undefined
}))

vi.mock('@vueuse/core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@vueuse/core')>()),
  useIntersectionObserver: (
    targets: { value: HTMLElement[] },
    callback: IntersectionObserverCallback
  ) => {
    intersectionObserverState.targets = targets
    intersectionObserverState.callback = callback
    return { isSupported: { value: true }, pause: vi.fn(), resume: vi.fn(), stop: vi.fn() }
  }
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    locale: { value: 'en' },
    t: (key: string) =>
      ({
        'apiDocs.title': 'API Docs',
        'apiDocs.search': 'Search documentation',
        'apiDocs.menu': 'Documentation menu',
        'apiDocs.onThisPage': 'On this page',
        'apiDocs.login': 'Log in',
        'apiDocs.dashboard': 'Dashboard',
        'apiDocs.nav.quickstart': 'Quickstart',
        'apiDocs.nav.clients': 'Client integration',
        'apiDocs.nav.reference': 'API reference',
        'apiDocs.nav.advanced': 'Advanced capabilities',
        'apiDocs.nav.platform': 'Platform',
        'apiDocs.pages.quickstart.title': 'Quickstart',
        'apiDocs.pages.authentication.title': 'Authentication',
        'apiDocs.pages.clientIntegration.title': 'Client integration',
        'apiDocs.pages.capabilities.title': 'Capabilities',
        'apiDocs.pages.messages.title': 'Messages',
        'apiDocs.pages.countTokens.title': 'Count tokens',
        'apiDocs.pages.responses.title': 'Responses',
        'apiDocs.pages.chatCompletions.title': 'Chat Completions',
        'apiDocs.pages.models.title': 'Models',
        'apiDocs.pages.imageGenerations.title': 'Image generations',
        'apiDocs.pages.imageEdits.title': 'Image edits',
        'apiDocs.pages.errors.title': 'Errors',
        'apiDocs.pages.requestId.title': 'Request IDs',
        'apiDocs.pages.keySecurity.title': 'API key security',
        'common.close': 'Close',
        'home.switchToLight': 'Switch to Light Mode',
        'home.switchToDark': 'Switch to Dark Mode'
      })[key] ?? key
  })
}))

enableAutoUnmount(afterEach)

afterEach(() => {
  vi.restoreAllMocks()
})

const headings = [
  { id: 'overview', label: 'Overview' },
  { id: 'authentication', label: 'Authentication' }
]

async function mountShell() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', component: { template: '<div />' } }
    ]
  })
  await router.push('/docs')
  await router.isReady()

  const wrapper = mount(ApiDocsShell, {
    attachTo: document.body,
    props: { currentPageId: 'quickstart', headings },
    slots: {
      default:
        '<article data-testid="shell-default-slot"><h2 id="overview">Overview</h2><h2 id="authentication">Authentication</h2></article>',
      search: '<div data-testid="shell-search-slot">Search</div>'
    },
    global: { plugins: [pinia, router] }
  })

  return { pinia, router, wrapper }
}

beforeEach(() => {
  document.documentElement.classList.remove('dark')
  document.body.style.overflow = ''
  localStorage.clear()
  intersectionObserverState.callback = undefined
  intersectionObserverState.targets = undefined
})

describe('ApiDocsShell', () => {
  it('renders the responsive three-column shell and exact capability tags', async () => {
    const { wrapper } = await mountShell()

    expect(wrapper.get('[data-testid="api-docs-header"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="api-docs-sidebar"]').classes()).toContain('lg:block')
    expect(wrapper.get('[data-testid="api-docs-content"]').classes()).toContain('min-w-0')
    expect(wrapper.get('[data-testid="api-docs-toc"]').classes()).toContain('xl:block')
    expect(
      wrapper
        .findAll('[data-testid="api-docs-capability-tag"]')
        .map((node) => node.text())
    ).toEqual([
      'Messages',
      'Responses',
      'Chat Completions',
      'Images',
      'Tools',
      'Streaming'
    ])
    expect(wrapper.get('main').classes()).toContain('max-w-[96rem]')
    expect(wrapper.get('[data-testid="api-docs-inline-toc"]').classes()).toEqual(
      expect.arrayContaining(['hidden', 'lg:block', 'xl:hidden'])
    )
    expect(wrapper.get('[data-testid="api-docs-mobile-menu"]').classes()).toContain('lg:hidden')
    expect(wrapper.get('.min-h-screen').classes()).toContain('overflow-x-clip')
    expect(wrapper.get('.min-h-screen').classes()).not.toContain('overflow-x-hidden')
    expect(wrapper.get('[data-testid="shell-default-slot"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="shell-search-slot"]').exists()).toBe(true)

    const menuButton = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-mobile-menu"]')
    menuButton.element.focus()
    await menuButton.trigger('click')
    const drawer = wrapper.get('[data-testid="api-docs-mobile-drawer"]')
    const closeButton = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-mobile-close"]')
    expect(drawer.attributes('role')).toBe('dialog')
    expect(drawer.attributes('aria-modal')).toBe('true')
    expect(closeButton.attributes('aria-label')).toBe('Close')
    expect(document.activeElement).toBe(closeButton.element)
    expect(wrapper.get('[data-testid="api-docs-header"]').attributes()).toHaveProperty('inert')
    expect(wrapper.get('[data-testid="api-docs-content"]').attributes()).toHaveProperty('inert')
    expect(document.body.style.overflow).toBe('hidden')

    const drawerLinks = drawer.findAll<HTMLAnchorElement>('a')
    const lastLink = drawerLinks.at(-1)!
    lastLink.element.focus()
    await drawer.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(closeButton.element)
    await drawer.trigger('keydown', { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(lastLink.element)

    await closeButton.trigger('click')
    expect(wrapper.find('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(false)
    expect(document.activeElement).toBe(menuButton.element)
    expect(wrapper.get('[data-testid="api-docs-header"]').attributes()).not.toHaveProperty('inert')
    expect(document.body.style.overflow).toBe('')
  })

  it('renders grouped localized navigation and closes the drawer after navigation or Escape', async () => {
    const { wrapper } = await mountShell()
    const menuButton = wrapper.get<HTMLButtonElement>('[data-testid="api-docs-mobile-menu"]')

    expect(wrapper.findAll('[data-testid="api-docs-nav-group"]')).toHaveLength(5)
    expect(wrapper.get('[data-testid="api-docs-sidebar"] [aria-current="page"]').text()).toBe(
      'Quickstart'
    )

    menuButton.element.focus()
    await menuButton.trigger('click')
    await wrapper
      .get('[data-testid="api-docs-mobile-drawer"] a[href="/docs/guide/authentication"]')
      .trigger('click')
    expect(wrapper.emitted('navigate')).toEqual([['/docs/guide/authentication']])
    expect(wrapper.find('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(false)
    expect(document.activeElement).toBe(menuButton.element)

    await menuButton.trigger('click')
    await wrapper.get('[data-testid="api-docs-mobile-drawer"]').trigger('keydown', {
      key: 'Escape'
    })
    expect(wrapper.find('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(false)
    expect(document.activeElement).toBe(menuButton.element)
  })

  it('uses native anchors and marks the active table-of-contents location', async () => {
    const { wrapper } = await mountShell()
    const toc = wrapper.get('[data-testid="api-docs-toc"]')
    const links = toc.findAll('a')

    expect(links.map((link) => link.attributes('href'))).toEqual([
      '#overview',
      '#authentication'
    ])
    expect(links[0].attributes('aria-current')).toBe('location')
    expect(links[1].attributes('aria-current')).toBeUndefined()
    expect(wrapper.get('[data-testid="api-docs-content"]').classes()).toContain(
      '[&_h2[id]]:scroll-mt-32'
    )

    const authenticationHeading = wrapper.get<HTMLElement>('#authentication').element
    intersectionObserverState.callback?.(
      [
        {
          target: authenticationHeading,
          isIntersecting: true,
          boundingClientRect: { top: 10 }
        } as IntersectionObserverEntry
      ],
      {} as IntersectionObserver
    )
    await nextTick()
    expect(links[1].attributes('aria-current')).toBe('location')

    await wrapper.setProps({ headings: [headings[0]] })
    await nextTick()
    expect(intersectionObserverState.targets?.value.map(({ id }) => id)).toEqual(['overview'])
  })

  it('emits search, persists theme changes, and sends anonymous users back to this page', async () => {
    const { wrapper } = await mountShell()

    await wrapper.get('[data-testid="api-docs-search-open"]').trigger('click')
    expect(wrapper.emitted('openSearch')).toHaveLength(1)

    const accountLink = wrapper.get('[data-testid="api-docs-account-link"]')
    expect(accountLink.attributes('href')).toBe('/login?redirect=/docs')
    expect(accountLink.text()).toBe('Log in')

    await wrapper.get('[data-testid="api-docs-theme-toggle"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
    await wrapper.get('[data-testid="api-docs-theme-toggle"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
  })

  it('links authenticated administrators to their personal dashboard', async () => {
    const { pinia, wrapper } = await mountShell()
    const authStore = useAuthStore(pinia)
    const admin = {
      id: 1,
      username: 'admin',
      email: 'admin@example.com',
      role: 'admin'
    } as User
    vi.spyOn(authAPI, 'getCurrentUser').mockResolvedValue({ data: admin })
    vi.spyOn(authAPI, 'logout').mockResolvedValue(undefined)

    await authStore.setToken('test-token')
    await nextTick()

    expect(wrapper.get('[data-testid="api-docs-account-link"]').attributes('href')).toBe(
      '/admin/my-account/dashboard'
    )
    expect(wrapper.get('[data-testid="api-docs-account-link"]').text()).toBe('Dashboard')
    await authStore.logout()
  })
})
