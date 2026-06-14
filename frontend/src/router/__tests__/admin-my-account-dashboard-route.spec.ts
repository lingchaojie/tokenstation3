import type { Router, RouteRecordNormalized, RouteRecordRaw } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: false,
  isAdmin: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  cachedPublicSettings: null as null | Record<string, unknown>,
}))

const adminComplianceStore = vi.hoisted(() => ({
  initialized: true,
  fetchStatus: vi.fn(),
  requireAcknowledgement: vi.fn(),
}))

const adminMyAccountDashboardView = vi.hoisted(() => ({
  default: { name: 'MockAdminMyAccountDashboardView' },
}))

vi.mock('@/views/admin/MyAccountDashboardView.vue', () => adminMyAccountDashboardView)

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({
    customMenuItems: [],
  }),
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => adminComplianceStore,
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

vi.mock('@/api/setup', () => ({
  getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false }),
}))

describe('admin my account dashboard route', () => {
  it('registers an admin-only route that renders the shared personal dashboard wrapper', async () => {
    const { default: router } = (await import('../index')) as { default: Router }
    const routes = router.getRoutes()
    const route = routes.find((record: RouteRecordNormalized) => record.name === 'AdminMyAccountDashboard')

    expect(route).toBeDefined()
    expect(route?.path).toBe('/admin/my-account/dashboard')
    expect(route?.components?.default).toEqual(expect.any(Function))

    const loadComponent = route?.components?.default as () => Promise<{ default: unknown }>
    const componentModule = await loadComponent()

    expect(componentModule.default).toBe(adminMyAccountDashboardView.default)
    expect(route?.meta.requiresAuth).toBe(true)
    expect(route?.meta.requiresAdmin).toBe(true)
    expect(route?.meta.title).toBe('Dashboard')
    expect(route?.meta.titleKey).toBe('dashboard.title')
    expect(route?.meta.descriptionKey).toBe('dashboard.welcomeMessage')

    const declarationRoutes = router.options.routes as RouteRecordRaw[]
    const adminDashboardIndex = declarationRoutes.findIndex((record: RouteRecordRaw) => record.path === '/admin/dashboard')
    const myAccountDashboardIndex = declarationRoutes.findIndex((record: RouteRecordRaw) => record.path === '/admin/my-account/dashboard')
    const adminOpsIndex = declarationRoutes.findIndex((record: RouteRecordRaw) => record.path === '/admin/ops')

    expect(adminDashboardIndex).toBeGreaterThanOrEqual(0)
    expect(myAccountDashboardIndex).toBeGreaterThan(adminDashboardIndex)
    expect(adminOpsIndex).toBeGreaterThan(myAccountDashboardIndex)
  })
})
