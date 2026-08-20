import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ auth: null as any, fetchAnnouncements: vi.fn() }))

vi.mock('@/stores', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  state.auth = reactive({ isAuthenticated: false, isAdmin: false })
  return {
    useAppStore: () => ({ siteLogo: '', siteName: 'LINX2.AI', cachedPublicSettings: null, fetchPublicSettings: vi.fn().mockResolvedValue(null) }),
    useAuthStore: () => state.auth,
    useSubscriptionStore: () => ({ fetchActiveSubscriptions: vi.fn().mockResolvedValue(undefined), startPolling: vi.fn(), clear: vi.fn() }),
    useAnnouncementStore: () => ({ fetchAnnouncements: state.fetchAnnouncements, reset: vi.fn() }),
    useAdminComplianceStore: () => ({ fetchStatus: vi.fn().mockResolvedValue(undefined), requireAcknowledgement: vi.fn(), reset: vi.fn() }),
    useAdminSettingsStore: () => ({ customMenuItems: [] }),
  }
})
vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: vi.fn(), afterEach: vi.fn() }),
  useRoute: () => ({ path: '/', fullPath: '/', name: 'Home', params: {}, meta: {} }),
  RouterView: { template: '<div />' },
}))
vi.mock('@/api/setup', () => ({ getSetupStatus: vi.fn().mockResolvedValue({ needs_setup: false }) }))

import App from '@/App.vue'

function mountApp() {
  return mount(App, { global: { stubs: {
    RouterView: true, Toast: true, NavigationProgress: true,
    AnnouncementPopup: true, AdminComplianceDialog: true,
  } } })
}

describe('App announcement popup eligibility', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    state.fetchAnnouncements.mockReset()
    state.auth.isAuthenticated = false
    state.auth.isAdmin = false
  })
  afterEach(() => vi.useRealTimers())

it.each([
  ['ordinary user', false, true],
  ['administrator', true, false],
] as const)('fetches for %s with the correct popup flag', async (_label, isAdmin, expected) => {
  const wrapper = mountApp()
  state.auth.isAdmin = isAdmin
  state.auth.isAuthenticated = true
  await nextTick()
  await vi.advanceTimersByTimeAsync(3000)
  expect(state.fetchAnnouncements).toHaveBeenCalledWith({ force: true, autoPopup: expected })
  wrapper.unmount()
})
})
