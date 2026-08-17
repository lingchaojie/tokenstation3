import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const shared = vi.hoisted(() => ({ appStore: null as any }))

vi.mock('@/stores', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  shared.appStore = reactive({
    siteLogo: 'https://example.com/custom.png',
    siteName: 'LINX2.AI',
    cachedPublicSettings: null,
    fetchPublicSettings: vi.fn().mockResolvedValue(undefined)
  })
  return {
    useAppStore: () => shared.appStore,
    useAuthStore: () => ({ isAdmin: false, isAuthenticated: false }),
    useSubscriptionStore: () => ({
      fetchActiveSubscriptions: vi.fn(),
      startPolling: vi.fn(),
      clear: vi.fn()
    }),
    useAnnouncementStore: () => ({
      fetchAnnouncements: vi.fn(),
      reset: vi.fn()
    }),
    useAdminComplianceStore: () => ({
      fetchStatus: vi.fn(),
      requireAcknowledgement: vi.fn(),
      reset: vi.fn()
    }),
    useAdminSettingsStore: () => ({ customMenuItems: [] })
  }
})

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRouter: () => ({ replace: vi.fn(), afterEach: vi.fn() }),
    useRoute: () => ({ path: '/', fullPath: '/', name: 'Home', params: {}, meta: {} })
  }
})

vi.mock('@/api/setup', () => ({
  getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false })
}))

import App from '@/App.vue'

describe('App branding', () => {
  beforeEach(() => {
    document.head.innerHTML = '<link rel="icon" href="/linx2-icon.png">'
    shared.appStore.siteLogo = 'https://example.com/custom.png'
  })

  it('restores the LINX2 favicon when an administrator clears the custom logo', async () => {
    const wrapper = mount(App, {
      global: {
        stubs: {
          RouterView: true,
          Toast: true,
          NavigationProgress: true,
          AnnouncementPopup: true,
          AdminComplianceDialog: true
        }
      }
    })
    await nextTick()
    expect(document.querySelector('link[rel="icon"]')?.getAttribute('href')).toBe(
      'https://example.com/custom.png'
    )

    shared.appStore.siteLogo = ''
    await nextTick()

    expect(document.querySelector('link[rel="icon"]')?.getAttribute('href')).toBe(
      '/linx2-icon.png'
    )
    wrapper.unmount()
  })
})
