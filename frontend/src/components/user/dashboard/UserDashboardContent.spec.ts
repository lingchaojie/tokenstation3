import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UserDashboardContent from './UserDashboardContent.vue'

const mockRefreshUser = vi.fn()
const mockGetDashboardStats = vi.fn()
const mockGetDashboardTrend = vi.fn()
const mockGetDashboardModels = vi.fn()
const mockGetByDateRange = vi.fn()
const mockGetMyPlatformQuotas = vi.fn()
const mockFetchActiveSubscriptions = vi.fn()
const mockGetCheckoutInfo = vi.fn()

const authState = vi.hoisted(() => ({
  isSimpleMode: false,
  user: { id: 1, balance: 25 } as { id: number; balance: number; subscription_balance_fallback_enabled?: boolean },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get user() {
      return authState.user
    },
    get isSimpleMode() {
      return authState.isSimpleMode
    },
    refreshUser: (...args: unknown[]) => mockRefreshUser(...args),
  }),
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

describe('UserDashboardContent', () => {
  beforeEach(() => {
    authState.isSimpleMode = false
    authState.user = { id: 1, balance: 25 }
    vi.clearAllMocks()
    mockRefreshUser.mockResolvedValue(authState.user)
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
      mount(UserDashboardContent)
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
    authState.user = { id: 1, balance: 25 }

    const wrapper = mount(UserDashboardContent)
    await flushPromises()

    expect(wrapper.get('[data-testid="stats-stub"]').text()).toBe('false')
  })

  it('does not enable standard cost display by default', async () => {
    const wrapper = mount(UserDashboardContent)
    await flushPromises()

    expect(wrapper.get('[data-testid="stats-stub"]').attributes('data-show-standard-costs')).toBe('false')
  })

  it('fetches checkout plans and active subscriptions in standard mode', async () => {
    mount(UserDashboardContent)
    await flushPromises()

    expect(mockGetCheckoutInfo).toHaveBeenCalledTimes(1)
    expect(mockFetchActiveSubscriptions).toHaveBeenCalledTimes(1)
  })

  it('does not fetch platform quotas in standard mode', async () => {
    mount(UserDashboardContent)
    await flushPromises()

    expect(mockGetMyPlatformQuotas).not.toHaveBeenCalled()
  })

  it('does not fetch platform quotas, checkout plans, or subscriptions in simple mode', async () => {
    authState.isSimpleMode = true

    mount(UserDashboardContent)
    await flushPromises()

    expect(mockGetMyPlatformQuotas).not.toHaveBeenCalled()
    expect(mockGetCheckoutInfo).not.toHaveBeenCalled()
    expect(mockFetchActiveSubscriptions).not.toHaveBeenCalled()
  })
})
