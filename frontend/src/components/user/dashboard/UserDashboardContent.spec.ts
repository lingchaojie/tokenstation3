import { defineComponent, nextTick } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import UserDashboardContent from './UserDashboardContent.vue'

enableAutoUnmount(afterEach)

const mockRefreshUser = vi.fn()
const mockGetDashboardStats = vi.fn()
const mockGetDashboardTrend = vi.fn()
const mockGetDashboardModels = vi.fn()
const mockGetByDateRange = vi.fn()
const mockGetMyPlatformQuotas = vi.fn()
const mockFetchActiveSubscriptions = vi.fn()
const mockGetCheckoutInfo = vi.fn()
const mockInitializeGuide = vi.fn()
const mockSuppressPrompt = vi.fn()
const mockRouterPush = vi.fn()
const mockShowWarning = vi.fn()

interface TestUser {
  id: number
  balance: number
  subscription_balance_fallback_enabled?: boolean
}

interface TestAuthStore {
  isSimpleMode: boolean
  user: TestUser | null
  refreshUser: (...args: unknown[]) => unknown
}

interface TestGuideStore {
  showPrompt: boolean
  initialize: (...args: unknown[]) => unknown
  suppressPrompt: (...args: unknown[]) => unknown
}

const storeHolders = vi.hoisted(() => ({
  auth: null as TestAuthStore | null,
  guide: null as TestGuideStore | null,
}))

vi.mock('@/stores/auth', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  storeHolders.auth = reactive<TestAuthStore>({
    isSimpleMode: false,
    user: { id: 1, balance: 25 },
    refreshUser: (...args: unknown[]) => mockRefreshUser(...args),
  })
  return { useAuthStore: () => storeHolders.auth }
})

vi.mock('@/stores/beginnerGuide', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  const guide = reactive<TestGuideStore>({
    showPrompt: false,
    initialize: (...args: unknown[]) => mockInitializeGuide(...args),
    suppressPrompt: (...args: unknown[]) => {
      guide.showPrompt = false
      return mockSuppressPrompt(...args)
    },
  })
  storeHolders.guide = guide
  return { useBeginnerGuideStore: () => guide }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showWarning: (...args: unknown[]) => mockShowWarning(...args) }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: (...args: unknown[]) => mockRouterPush(...args) }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    activeSubscriptions: [],
    subscriptionBalanceSummary: null,
    fetchActiveSubscriptions: (...args: unknown[]) => mockFetchActiveSubscriptions(...args),
  }),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats: (...args: unknown[]) => mockGetDashboardStats(...args),
    getDashboardTrend: (...args: unknown[]) => mockGetDashboardTrend(...args),
    getDashboardModels: (...args: unknown[]) => mockGetDashboardModels(...args),
    getByDateRange: (...args: unknown[]) => mockGetByDateRange(...args),
  },
}))

vi.mock('@/api/user', () => ({
  getMyPlatformQuotas: (...args: unknown[]) => mockGetMyPlatformQuotas(...args),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo: (...args: unknown[]) => mockGetCheckoutInfo(...args),
  },
}))

vi.mock('@/components/common/LoadingSpinner.vue', () => ({
  default: { template: '<div class="spinner" />' },
}))

vi.mock('@/components/user/dashboard/UserDashboardStats.vue', () => ({
  default: {
    props: [
      'stats',
      'balance',
      'isSimple',
      'subscriptionBalance',
      'subscriptionPlans',
      'activeSubscriptions',
      'subscriptionBalanceFallbackEnabled',
      'showStandardCosts',
    ],
    template: '<section class="stats-stub" data-testid="stats-stub" :data-show-standard-costs="String(showStandardCosts)">{{ String(subscriptionBalanceFallbackEnabled) }}</section>',
  },
}))

vi.mock('@/components/user/dashboard/UserDashboardCharts.vue', () => ({
  default: { emits: ['dateRangeChange', 'granularityChange', 'refresh'], template: '<section class="charts-stub" />' },
}))

vi.mock('@/components/user/dashboard/UserDashboardRecentUsage.vue', () => ({
  default: { props: ['data', 'loading'], template: '<section class="recent-stub" />' },
}))

vi.mock('@/components/user/dashboard/UserDashboardQuickActions.vue', () => ({
  default: { template: '<section class="quick-actions-stub" />' },
}))

const BeginnerWelcomeDialogStub = defineComponent({
  name: 'BeginnerWelcomeDialog',
  props: { show: { type: Boolean, required: true } },
  emits: ['start', 'close'],
  template: `
    <section v-if="show" data-testid="beginner-welcome-dialog-stub">
      <button data-testid="welcome-start-stub" @click="$emit('start')">Start</button>
      <button data-testid="welcome-close-stub" @click="$emit('close')">Close</button>
    </section>
  `,
})

function mountDashboard() {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        gettingStarted: {
          warnings: {
            promptSaveFailed: () => 'The welcome preference could not be saved.'
          }
        }
      }
    }
  })

  return mount(UserDashboardContent, {
    global: {
      plugins: [i18n],
      stubs: { BeginnerWelcomeDialog: BeginnerWelcomeDialogStub }
    }
  })
}

const stats = {
  total_api_keys: 2,
  active_api_keys: 1,
  today_requests: 12,
  total_requests: 100,
  today_actual_cost: 1.25,
  today_cost: 2,
  total_actual_cost: 10,
  total_cost: 15,
  today_tokens: 1200,
  today_input_tokens: 700,
  today_output_tokens: 500,
  total_tokens: 5000,
  total_input_tokens: 3000,
  total_output_tokens: 2000,
  rpm: 6,
  tpm: 120,
  average_duration_ms: 850,
  by_platform: [],
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('UserDashboardContent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    storeHolders.guide ??= {
      showPrompt: false,
      initialize: (...args: unknown[]) => mockInitializeGuide(...args),
      suppressPrompt: (...args: unknown[]) => mockSuppressPrompt(...args),
    }
    storeHolders.auth!.isSimpleMode = false
    storeHolders.auth!.user = { id: 1, balance: 25 }
    storeHolders.guide!.showPrompt = false
    mockRefreshUser.mockResolvedValue(storeHolders.auth!.user)
    mockInitializeGuide.mockResolvedValue(undefined)
    mockSuppressPrompt.mockResolvedValue(true)
    mockRouterPush.mockResolvedValue(undefined)
    mockGetDashboardStats.mockResolvedValue(stats)
    mockGetDashboardTrend.mockResolvedValue({ trend: [] })
    mockGetDashboardModels.mockResolvedValue({ models: [] })
    mockGetByDateRange.mockResolvedValue({ items: [] })
    mockGetMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
    mockFetchActiveSubscriptions.mockResolvedValue([])
    mockGetCheckoutInfo.mockResolvedValue({ data: { plans: [] } })
  })

  it('uses local YYYY-MM-DD dates for the default date range', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2024, 0, 7, 0, 30, 0))

    try {
      mountDashboard()
      await flushPromises()
    } finally {
      vi.useRealTimers()
    }

    expect(mockGetDashboardTrend).toHaveBeenCalledWith({
      start_date: '2024-01-01',
      end_date: '2024-01-07',
      granularity: 'day',
    })
    expect(mockGetByDateRange).toHaveBeenCalledWith('2024-01-01', '2024-01-07')
  })

  it('passes false for balance fallback when the user preference is missing', async () => {
    storeHolders.auth!.user = { id: 1, balance: 25 }

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.get('[data-testid="stats-stub"]').text()).toBe('false')
  })

  it('does not enable standard cost display by default', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.get('[data-testid="stats-stub"]').attributes('data-show-standard-costs')).toBe('false')
  })

  it('fetches checkout plans and active subscriptions in standard mode', async () => {
    mountDashboard()
    await flushPromises()

    expect(mockGetCheckoutInfo).toHaveBeenCalledTimes(1)
    expect(mockFetchActiveSubscriptions).toHaveBeenCalledTimes(1)
  })

  it('does not fetch platform quotas in standard mode', async () => {
    mountDashboard()
    await flushPromises()

    expect(mockGetMyPlatformQuotas).not.toHaveBeenCalled()
  })

  it('does not fetch platform quotas, checkout plans, or subscriptions in simple mode', async () => {
    storeHolders.auth!.isSimpleMode = true

    mountDashboard()
    await flushPromises()

    expect(mockGetMyPlatformQuotas).not.toHaveBeenCalled()
    expect(mockGetCheckoutInfo).not.toHaveBeenCalled()
    expect(mockFetchActiveSubscriptions).not.toHaveBeenCalled()
  })

  it('initializes the account-scoped prompt once and renders eligible state outside dashboard loading', async () => {
    const pendingStats = deferred<typeof stats>()
    mockGetDashboardStats.mockReturnValueOnce(pendingStats.promise)
    storeHolders.guide!.showPrompt = true

    const wrapper = mountDashboard()
    await nextTick()

    expect(mockInitializeGuide).toHaveBeenCalledTimes(1)
    expect(mockInitializeGuide).toHaveBeenCalledWith({
      authenticated: true,
      userId: 1,
    })
    expect(wrapper.find('.spinner').exists()).toBe(true)
    expect(wrapper.find('[data-testid="beginner-welcome-dialog-stub"]').exists()).toBe(true)

    pendingStats.resolve(stats)
    await flushPromises()
  })

  it.each(['suppressed', 'completed', 'loading', 'GET error'])(
    'does not render the automatic prompt for %s state when the store fails closed',
    async () => {
      storeHolders.guide!.showPrompt = false

      const wrapper = mountDashboard()
      await nextTick()

      expect(wrapper.find('[data-testid="beginner-welcome-dialog-stub"]').exists()).toBe(false)
    }
  )

  it('closes immediately, suppresses once, and does not navigate', async () => {
    const suppression = deferred<boolean>()
    mockSuppressPrompt.mockReturnValueOnce(suppression.promise)
    storeHolders.guide!.showPrompt = true
    const wrapper = mountDashboard()
    await nextTick()

    await wrapper.get('[data-testid="welcome-close-stub"]').trigger('click')
    await nextTick()

    expect(mockSuppressPrompt).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="beginner-welcome-dialog-stub"]').exists()).toBe(false)
    expect(mockRouterPush).not.toHaveBeenCalled()

    suppression.resolve(true)
    await flushPromises()
  })

  it('warns non-blockingly when close suppression is not persisted and keeps the prompt hidden', async () => {
    mockSuppressPrompt.mockResolvedValueOnce(false)
    storeHolders.guide!.showPrompt = true
    const wrapper = mountDashboard()
    await nextTick()

    await wrapper.get('[data-testid="welcome-close-stub"]').trigger('click')
    await flushPromises()

    expect(mockShowWarning).toHaveBeenCalledWith(
      'The welcome preference could not be saved.'
    )
    expect(wrapper.find('[data-testid="beginner-welcome-dialog-stub"]').exists()).toBe(false)
    expect(mockRouterPush).not.toHaveBeenCalled()
  })

  it('starts the guide after suppression and still navigates when persistence returns false', async () => {
    mockSuppressPrompt.mockResolvedValueOnce(false)
    storeHolders.guide!.showPrompt = true
    const wrapper = mountDashboard()
    await nextTick()

    await wrapper.get('[data-testid="welcome-start-stub"]').trigger('click')
    await flushPromises()

    expect(mockSuppressPrompt).toHaveBeenCalledTimes(1)
    expect(mockShowWarning).toHaveBeenCalledWith(
      'The welcome preference could not be saved.'
    )
    expect(mockRouterPush).toHaveBeenCalledWith('/getting-started')
  })

  it('still hides and starts the guide when a legacy or mocked suppression throws', async () => {
    mockSuppressPrompt.mockRejectedValueOnce(new Error('offline'))
    storeHolders.guide!.showPrompt = true
    const wrapper = mountDashboard()
    await nextTick()

    await wrapper.get('[data-testid="welcome-start-stub"]').trigger('click')
    await flushPromises()

    expect(mockShowWarning).toHaveBeenCalledWith(
      'The welcome preference could not be saved.'
    )
    expect(wrapper.find('[data-testid="beginner-welcome-dialog-stub"]').exists()).toBe(false)
    expect(mockRouterPush).toHaveBeenCalledWith('/getting-started')
  })

  it('reinitializes only when the authenticated owner changes and invalidates on logout', async () => {
    mountDashboard()
    await nextTick()
    expect(mockInitializeGuide).toHaveBeenCalledTimes(1)

    storeHolders.auth!.user = { id: 1, balance: 30 }
    await nextTick()
    expect(mockInitializeGuide).toHaveBeenCalledTimes(1)

    storeHolders.auth!.user = { id: 2, balance: 40 }
    await nextTick()
    expect(mockInitializeGuide).toHaveBeenNthCalledWith(2, {
      authenticated: true,
      userId: 2,
    })

    storeHolders.auth!.user = null
    await nextTick()
    expect(mockInitializeGuide).toHaveBeenNthCalledWith(3, {
      authenticated: false,
      userId: null,
    })
  })
})
