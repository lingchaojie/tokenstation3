import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import HomeView from '../HomeView.vue'

const { appState, authState, fetchPublicSettingsMock, checkAuthMock } = vi.hoisted(() => ({
  appState: {
    cachedPublicSettings: null as null | {
      site_name?: string
      site_logo?: string
      site_subtitle?: string
      doc_url?: string
      home_content?: string
    },
    siteName: 'Sub2API',
    siteLogo: '',
    siteSubtitle: 'AI API Gateway Platform',
    docUrl: '',
    publicSettingsLoaded: true,
  },
  authState: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as null | { email: string },
  },
  fetchPublicSettingsMock: vi.fn(),
  checkAuthMock: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return appState.cachedPublicSettings
    },
    get siteName() {
      return appState.siteName
    },
    get siteLogo() {
      return appState.siteLogo
    },
    get siteSubtitle() {
      return appState.siteSubtitle
    },
    get docUrl() {
      return appState.docUrl
    },
    get publicSettingsLoaded() {
      return appState.publicSettingsLoaded
    },
    fetchPublicSettings: fetchPublicSettingsMock,
  }),
  useAuthStore: () => ({
    get user() {
      return authState.user
    },
    get isAuthenticated() {
      return authState.isAuthenticated
    },
    get isAdmin() {
      return authState.isAdmin
    },
    checkAuth: checkAuthMock,
  }),
}))

const messages: Record<string, string> = {
  'home.docs': '文档',
  'home.footer.allRightsReserved': '保留所有权利。',
  'home.getStarted': '立即开始',
  'home.goToDashboard': '进入控制台',
  'home.login': '登录',
  'home.dashboard': '控制台',
  'home.switchToLight': '切换到浅色模式',
  'home.switchToDark': '切换到深色模式',
  'home.viewDocs': '查看文档',
  'home.tags.subscriptionToApi': '订阅转 API',
  'home.tags.stickySession': '会话保持',
  'home.tags.realtimeBilling': '实时计费',
  'home.features.unifiedGateway': '统一网关',
  'home.features.unifiedGatewayDesc': '统一网关说明',
  'home.features.multiAccount': '多账号池',
  'home.features.multiAccountDesc': '多账号池说明',
  'home.features.balanceQuota': '余额配额',
  'home.features.balanceQuotaDesc': '余额配额说明',
  'home.providers.title': '服务商',
  'home.providers.description': '服务商说明',
  'home.providers.claude': 'Claude',
  'home.providers.gemini': 'Gemini',
  'home.providers.antigravity': 'Antigravity',
  'home.providers.more': '更多',
  'home.providers.supported': '已支持',
  'home.providers.soon': '即将支持',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

function mountHome() {
  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
        },
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<svg data-testid="icon" />' },
      },
    },
  })
}

describe('HomeView landing page', () => {
  beforeEach(() => {
    appState.cachedPublicSettings = null
    appState.siteName = 'Sub2API'
    appState.siteLogo = ''
    appState.siteSubtitle = 'AI API Gateway Platform'
    appState.docUrl = ''
    appState.publicSettingsLoaded = true
    authState.isAuthenticated = false
    authState.isAdmin = false
    authState.user = null
    fetchPublicSettingsMock.mockReset()
    checkAuthMock.mockReset()
    fetchPublicSettingsMock.mockResolvedValue({})
    document.documentElement.classList.remove('dark')
    localStorage.clear()

    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false }),
    })
  })

  it('renders the black-orange enterprise API landing shell by default', async () => {
    appState.cachedPublicSettings = {
      site_name: 'Fuse API',
      site_subtitle: 'Custom subtitle should not replace the approved hero copy',
      doc_url: 'https://docs.example.test',
    }

    const wrapper = mountHome()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('Fuse API')
    expect(text).toContain('Claude Code & Codex API')
    expect(text).toContain('企业级编程 API 服务')
    expect(text).toContain('稳定、低延迟、可观测')
    expect(text).toContain('统一 API 接入')
    expect(text).toContain('智能账号池调度')
    expect(text).toContain('用量与余额管理')
    expect(text).toContain('Claude Code')
    expect(text).toContain('Codex')
    expect(text).toContain('OpenAI 兼容')
    expect(wrapper.get('img[alt="Fuse API logo"]').attributes('src')).toBe('/landing-icon.jpg')
    expect(wrapper.get('a[href="/login"]').text()).toContain('立即开始')
    expect(wrapper.get('a[href="https://docs.example.test"]').text()).toContain('查看文档')
  })

  it('routes authenticated admin users to the dashboard CTA', async () => {
    authState.isAuthenticated = true
    authState.isAdmin = true
    authState.user = { email: 'admin@example.com' }

    const wrapper = mountHome()
    await flushPromises()

    const headerCta = wrapper.get('header a[href="/admin/dashboard"]')
    expect(headerCta.text()).toContain('进入控制台')
    expect(headerCta.attributes('aria-label')).toBe('进入控制台')
    expect(wrapper.text()).toContain('控制台')
  })

  it('keeps the header CTA compact and accessible on mobile screens', async () => {
    const wrapper = mountHome()
    await flushPromises()

    const headerCta = wrapper.get('header a[href="/login"]')
    const headerCtaLabel = headerCta.get('span[data-testid="header-cta-label"]')

    expect(headerCta.attributes('aria-label')).toBe('立即开始')
    expect(headerCta.classes()).toContain('h-10')
    expect(headerCta.classes()).toContain('w-10')
    expect(headerCta.classes()).toContain('px-0')
    expect(headerCta.classes()).toContain('sm:w-auto')
    expect(headerCta.classes()).toContain('sm:px-4')
    expect(headerCtaLabel.classes()).toContain('hidden')
    expect(headerCtaLabel.classes()).toContain('sm:inline')
  })

  it('renders URL custom home content in a full-page iframe before the default landing page', async () => {
    appState.cachedPublicSettings = {
      site_name: 'Fuse API',
      home_content: 'https://landing.example.test',
    }

    const wrapper = mountHome()
    await flushPromises()

    const iframe = wrapper.get('iframe')
    expect(iframe.attributes('src')).toBe('https://landing.example.test')
    expect(iframe.attributes('title')).toBe('Fuse API custom home content')
    expect(wrapper.text()).not.toContain('企业级编程 API 服务')
  })

  it('renders Markdown custom home content before the default landing page', async () => {
    appState.cachedPublicSettings = {
      home_content: '# Custom Home\n\nThis is **custom** content.',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('Custom Home')
    expect(wrapper.html()).toContain('<strong>custom</strong>')
    expect(wrapper.text()).not.toContain('企业级编程 API 服务')
  })

  it('renders HTML custom home content before the default landing page', async () => {
    appState.cachedPublicSettings = {
      home_content: '<section data-testid="custom-home">Custom Home</section>',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.html()).toContain('data-testid="custom-home"')
    expect(wrapper.text()).toContain('Custom Home')
    expect(wrapper.text()).not.toContain('企业级编程 API 服务')
  })
})
