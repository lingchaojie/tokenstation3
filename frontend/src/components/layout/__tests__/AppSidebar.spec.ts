import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineComponent } from 'vue'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AppSidebar from '../AppSidebar.vue'

interface SidebarAuthState {
  isAdmin: boolean
  isSimpleMode: boolean
}

interface SidebarAppState {
  sidebarCollapsed: boolean
  mobileOpen: boolean
  sidebarScrollTop: number
  backendModeEnabled: boolean
  cachedPublicSettings: { custom_menu_items: unknown[] }
  siteName: string
  siteLogo: string
  siteVersion: string
  publicSettingsLoaded: boolean
  toggleSidebar: () => void
  setMobileOpen: (open: boolean) => void
}

const sidebarStores = vi.hoisted(() => ({
  app: null as SidebarAppState | null,
  auth: null as SidebarAuthState | null,
}))

const sidebarRoute = vi.hoisted(() => ({ path: '/dashboard' }))

vi.mock('@/stores', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  const app = reactive<SidebarAppState>({
    sidebarCollapsed: false,
    mobileOpen: false,
    sidebarScrollTop: 0,
    backendModeEnabled: false,
    cachedPublicSettings: { custom_menu_items: [] },
    siteName: 'LINX2.AI',
    siteLogo: '',
    siteVersion: 'test',
    publicSettingsLoaded: true,
    toggleSidebar: vi.fn(),
    setMobileOpen: vi.fn(),
  })
  const auth = reactive<SidebarAuthState>({ isAdmin: false, isSimpleMode: false })
  sidebarStores.app = app
  sidebarStores.auth = auth

  return {
    useAppStore: () => app,
    useAuthStore: () => auth,
    useOnboardingStore: () => ({
      isCurrentStep: () => false,
      nextStep: vi.fn(),
    }),
    useAdminSettingsStore: () => ({
      fetch: vi.fn(),
      opsMonitoringEnabled: false,
      paymentEnabled: false,
      customMenuItems: [],
    }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => sidebarStores.app,
}))

vi.mock('vue-router', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  const route = reactive(sidebarRoute)
  return {
    useRoute: () => route,
    useRouter: () => ({ push: vi.fn() }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/components/common/VersionBadge.vue', () => ({
  default: { name: 'VersionBadge', template: '<span />' },
}))

vi.mock('@/components/common/LinxWordmark.vue', () => ({
  default: { name: 'LinxWordmark', template: '<span>LINX2.AI</span>' },
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({
    canUseBatchImage: { value: false },
    refreshBatchImageAccess: vi.fn(),
  }),
}))

enableAutoUnmount(afterEach)

const RouterLinkStub = defineComponent({
  name: 'RouterLink',
  inheritAttrs: false,
  props: { to: { type: String, required: true } },
  template: '<a v-bind="$attrs" :data-route="to"><slot /></a>',
})

function mountSidebar(options: { admin: boolean; simple: boolean }) {
  sidebarStores.auth!.isAdmin = options.admin
  sidebarStores.auth!.isSimpleMode = options.simple
  sidebarRoute.path = options.admin ? '/admin/dashboard' : '/dashboard'

  return mount(AppSidebar, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        VersionBadge: true,
        LinxWordmark: true,
      },
    },
  })
}

beforeEach(() => {
  sidebarStores.app!.sidebarCollapsed = false
  sidebarStores.app!.mobileOpen = false
  sidebarStores.app!.sidebarScrollTop = 0
  sidebarStores.app!.backendModeEnabled = false
  sidebarStores.app!.cachedPublicSettings = { custom_menu_items: [] }
  sidebarStores.auth!.isAdmin = false
  sidebarStores.auth!.isSimpleMode = false
  sidebarRoute.path = '/dashboard'
})

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

describe('Orange semantic color utilities', () => {
  it('defines separate accent and identity avatar classes', () => {
    expect(styleSource).toContain('.ui-accent-dot')
    expect(styleSource).toContain('@apply bg-amber-400')
    expect(styleSource).toContain('.ui-accent-badge')
    expect(styleSource).toContain('@apply border-amber-400/30 bg-amber-500/10 text-amber-300')
    expect(styleSource).toContain('.ui-theme-toggle')
    expect(styleSource).toContain('.ui-theme-icon-accent')
    expect(styleSource).toContain('.ui-avatar-identity')
    expect(styleSource).toContain('@apply bg-gradient-to-br from-orange-700 via-orange-600 to-rose-600')
    expect(styleSource).toContain('.ui-avatar-identity-sm')
    expect(styleSource).toContain('.ui-avatar-identity-md')
    expect(styleSource).toContain('.ui-avatar-identity-lg')
  })
})

describe('AppSidebar theme toggle color hierarchy', () => {
  it('uses the semantic accent class for the sun icon', () => {
    expect(componentSource).toContain('class="h-5 w-5 flex-shrink-0 ui-theme-icon-accent"')
    expect(componentSource).not.toContain('text-amber-500')
  })
})

describe('AppSidebar admin personal dashboard navigation', () => {
  it('reuses the self dashboard item but remaps it into the admin my-account route namespace', () => {
    const adminPersonalBuilder = componentSource.match(
      /function buildAdminPersonalNavItems\(\): NavItem\[] \{[\s\S]*?\n\}/,
    )?.[0]

    expect(adminPersonalBuilder).toContain("buildSelfNavItems(true, t('nav.dashboard'))")
    expect(adminPersonalBuilder).toContain("'/dashboard': '/admin/my-account/dashboard'")
    expect(componentSource).toContain('finalizeNav(buildAdminPersonalNavItems())')
  })
})

describe('AppSidebar regular-user daily check-in navigation', () => {
  it('renames only the regular-user dashboard item to overview', () => {
    expect(componentSource).toContain("buildSelfNavItems(true, t('nav.overview'), dailyCheckInActive.value)")
    expect(componentSource).toContain("buildSelfNavItems(true, t('nav.dashboard'))")
  })

  it('inserts the check-in entry directly after the dashboard only while active', () => {
    expect(componentSource).toContain('if (includeDailyCheckIn)')
    expect(componentSource).toContain("path: '/check-in'")
    expect(componentSource).toContain("label: t('nav.dailyCheckIn')")
    expect(componentSource.indexOf("path: '/check-in'")).toBeLessThan(
      componentSource.indexOf("path: '/dashboard/models'"),
    )
  })
})

describe('AppSidebar benefits-center navigation', () => {
  const benefitsGroup = componentSource.slice(
    componentSource.indexOf("path: '/admin/affiliates'"),
    componentSource.indexOf("path: '/admin/orders'"),
  )

  it('keeps the admin benefits center available independently of the affiliate feature flag', () => {
    expect(benefitsGroup).toContain("label: t('nav.affiliateManagement')")
    expect(benefitsGroup).not.toContain('featureFlag: flagAffiliate')
    expect(componentSource).toContain("path: '/affiliate', label: t('nav.affiliate')")
    expect(componentSource).toContain("featureFlag: flagAffiliate")
  })

  it('adds check-in configuration as the fourth benefits-center child', () => {
    expect(benefitsGroup).toContain("path: '/admin/affiliates/check-in'")
    expect(benefitsGroup).toContain("label: t('nav.dailyCheckInConfig')")
    expect(benefitsGroup.indexOf("path: '/admin/affiliates/transfers'")).toBeLessThan(
      benefitsGroup.indexOf("path: '/admin/affiliates/check-in'"),
    )
  })
})

describe('AppSidebar beginner guide navigation', () => {
  const selfNavBuilder = componentSource.match(
    /function buildSelfNavItems\([\s\S]*?\): NavItem\[] \{[\s\S]*?\n\}/,
  )?.[0]
  const adminPersonalBuilder = componentSource.match(
    /function buildAdminPersonalNavItems\(\): NavItem\[] \{[\s\S]*?\n\}/,
  )?.[0]
  const adminOperationsBuilder = componentSource.match(
    /const adminNavItems = computed\(\(\): NavItem\[] => \{[\s\S]*?const visible/,
  )?.[0]

  it('keeps the canonical public route in regular-user self navigation near API keys', () => {
    expect(selfNavBuilder).toContain("path: '/getting-started'")
    expect(selfNavBuilder).toContain("t('gettingStarted.dashboard.sidebarLabel')")
    expect(selfNavBuilder!.indexOf("path: '/getting-started'")).toBeLessThan(
      selfNavBuilder!.indexOf("path: '/keys'")
    )
  })

  it('naturally includes the same public route in admin My Account without remapping it', () => {
    expect(adminPersonalBuilder).toContain("buildSelfNavItems(true, t('nav.dashboard'))")
    expect(adminPersonalBuilder).not.toContain("'/getting-started':")
    expect(adminOperationsBuilder).not.toContain("path: '/getting-started'")
  })

  it('does not hide the guide from simple-mode self navigation', () => {
    const guideItem = selfNavBuilder?.match(/\{ path: '\/getting-started'[^\n]+\}/)?.[0]

    expect(guideItem).toBeDefined()
    expect(guideItem).not.toContain('hideInSimpleMode')
    expect(componentSource).toContain(
      'authStore.isSimpleMode ? visible.filter(item => !item.hideInSimpleMode) : visible'
    )
  })

  it.each([
    ['regular simple user', { admin: false, simple: true }, false],
    ['standard admin', { admin: true, simple: false }, true],
    ['simple-mode admin', { admin: true, simple: true }, true],
  ] as const)(
    'mounts exactly one canonical guide link for %s in the expected self scope',
    (_label, options, expectsPersonalScope) => {
      const wrapper = mountSidebar(options)
      const guideLinks = wrapper.findAll('[data-route="/getting-started"]')

      expect(guideLinks).toHaveLength(1)
      const section = guideLinks[0].element.closest('.sidebar-section')
      expect(section).not.toBeNull()
      if (expectsPersonalScope) {
        expect(section?.textContent).toContain('nav.myAccount')
      } else {
        expect(section?.textContent).not.toContain('nav.myAccount')
      }
    }
  )
})

describe('AppSidebar model marketplace navigation', () => {
  it('keeps the model marketplace route in self navigation for authenticated users', () => {
    expect(componentSource).not.toContain('管理员灰度入口')
    expect(componentSource).toContain("path: '/dashboard/models'")
    expect(componentSource).toContain("t('nav.modelMarketplace')")
  })
})

describe('AppSidebar web chat navigation', () => {
  it('keeps the model marketplace route before the chat route in self navigation', () => {
    expect(componentSource).toContain("path: '/chat'")
    expect(componentSource).toContain("t('nav.chat')")
    expect(componentSource.indexOf("path: '/dashboard/models'")).toBeLessThan(componentSource.indexOf("path: '/chat'"))
  })
})
