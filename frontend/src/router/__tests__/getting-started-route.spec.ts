import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import type { Router, RouteRecordRaw } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: false,
  isAdmin: false,
  isSimpleMode: false,
  hasPendingAuthSession: false
}))

const appStore = vi.hoisted(() => ({
  siteName: 'LINX2.AI',
  backendModeEnabled: false,
  publicSettingsLoaded: true,
  cachedPublicSettings: null as null | Record<string, unknown>,
  fetchPublicSettings: vi.fn()
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] })
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({
    initialized: true,
    fetchStatus: vi.fn(),
    requireAcknowledgement: vi.fn()
  })
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false }
  })
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn()
  })
}))

vi.mock('@/api/setup', () => ({
  getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false })
}))

describe('getting-started route contract', () => {
  it('registers the canonical public guide route before the catch-all', async () => {
    const { default: router } = (await import('../index')) as { default: Router }
    const route = router.getRoutes().find((record) => record.name === 'GettingStarted')

    expect(route?.path).toBe('/getting-started')
    expect(route?.meta).toMatchObject({
      requiresAuth: false,
      title: 'Beginner Guide',
      titleKey: 'gettingStarted.title'
    })

    const declarationRoutes = router.options.routes as RouteRecordRaw[]
    const guideIndex = declarationRoutes.findIndex((record) => record.path === '/getting-started')
    const catchAllIndex = declarationRoutes.findIndex(
      (record) => record.path === '/:pathMatch(.*)*'
    )

    expect(guideIndex).toBeGreaterThanOrEqual(0)
    expect(guideIndex).toBeLessThan(catchAllIndex)
  })

  it('does not expand the backend-only public allowlist for the guide', async () => {
    const source = await readFile(resolve(process.cwd(), 'src/router/index.ts'), 'utf8')
    const allowlist = source.match(/const BACKEND_MODE_ALLOWED_PATHS = \[(.*?)\]/s)?.[1]

    expect(allowlist).toBeDefined()
    expect(allowlist).not.toContain('/getting-started')
  })
})
