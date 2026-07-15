import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import type { Router, RouteRecordRaw } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

import { API_DOCS_PAGES, API_ENDPOINTS } from '@/components/api-docs/catalog'
import { i18n } from '@/i18n'

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

describe('API documentation route contract', () => {
  it('registers the canonical constrained public route family before user routes and 404', async () => {
    const { default: router } = (await import('../index')) as { default: Router }

    expect(router.getRoutes().find(({ name }) => name === 'ApiDocs')?.path).toBe('/docs')
    expect(router.getRoutes().find(({ name }) => name === 'ApiDocsPage')?.path).toBe(
      '/docs/:section(guide|api-reference|platform)/:slug'
    )

    const declarationRoutes = router.options.routes as RouteRecordRaw[]
    const docsRoutes = declarationRoutes.filter(
      ({ name }) => name === 'ApiDocs' || name === 'ApiDocsPage'
    )
    const docsIndexes = docsRoutes.map((record) => declarationRoutes.indexOf(record))
    const userRoutesIndex = declarationRoutes.findIndex((record) => record.path === '/')
    const catchAllIndex = declarationRoutes.findIndex(
      (record) => record.path === '/:pathMatch(.*)*'
    )

    expect(docsRoutes).toHaveLength(2)
    expect(docsRoutes.every(({ meta }) => meta?.requiresAuth === false)).toBe(true)
    expect(docsRoutes.every(({ meta }) => meta?.titleKey === 'apiDocs.title')).toBe(true)
    expect(docsIndexes.every((index) => index >= 0 && index < userRoutesIndex)).toBe(true)
    expect(docsIndexes.every((index) => index < catchAllIndex)).toBe(true)
  })

  it('resolves exactly the approved catalog while retaining the existing batch-image alias', async () => {
    const { default: router } = (await import('../index')) as { default: Router }

    expect(API_DOCS_PAGES).toHaveLength(14)
    for (const page of API_DOCS_PAGES) {
      expect(router.resolve(page.path).name).toBe(page.path === '/docs' ? 'ApiDocs' : 'ApiDocsPage')
    }

    expect(API_ENDPOINTS.map(({ method, path }) => `${method} ${path}`)).toEqual([
      'POST /v1/messages',
      'POST /v1/messages/count_tokens',
      'POST /v1/responses',
      'POST /v1/chat/completions',
      'GET /v1/models',
      'POST /v1/images/generations',
      'POST /v1/images/edits'
    ])
    expect(JSON.stringify({ pages: API_DOCS_PAGES, endpoints: API_ENDPOINTS })).not.toMatch(
      /v1beta|embeddings?|videos?|alpha\/search|backend-api|batch.image|jwt|admin|v1\/usage/i
    )
    expect(router.resolve('/docs/batch-image').name).toBe('BatchImageGuide')
    expect(router.resolve('/docs/internal/anything').name).toBe('NotFound')
  })

  it('allows anonymous normal-mode navigation without adding docs to the backend-only allowlist', async () => {
    const { default: router } = (await import('../index')) as { default: Router }
    const source = await readFile(resolve(process.cwd(), 'src/router/index.ts'), 'utf8')
    const allowlist = source.match(/const BACKEND_MODE_ALLOWED_PATHS = \[(.*?)\]/s)?.[1]

    i18n.global.setLocaleMessage('en', {
      apiDocs: { title: () => 'API Docs' }
    })
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)

    expect(allowlist).toBeDefined()
    expect(allowlist).not.toContain('/docs')

    await router.push('/docs')
    expect(router.currentRoute.value.name).toBe('ApiDocs')
    expect(router.currentRoute.value.path).toBe('/docs')
    expect(authStore.checkAuth).toHaveBeenCalled()
  })
})
