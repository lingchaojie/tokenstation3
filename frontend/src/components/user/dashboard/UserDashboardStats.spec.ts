import { config, flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

const routerPush = vi.hoisted(() => vi.fn())
const mockUpdateProfile = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: null as Record<string, unknown> | null,
}))

import UserDashboardStats from './UserDashboardStats.vue'

const messages = vi.hoisted(() => ({
  'common.active': 'active',
  'common.available': 'available',
  'common.total': 'Total',
  'dashboard.actual': 'Actual',
  'dashboard.apiKeys': 'API Keys',
  'dashboard.averageTime': 'Average time',
  'dashboard.avgResponse': 'Avg Response',
  'dashboard.balanceFallbackToggle.disabledHint': 'Default off. Requests stop after the monthly card 7-day quota is used up, even if the account still has balance.',
  'dashboard.balanceFallbackToggle.enabledHint': 'When on, requests continue by deducting recharge balance after the monthly card 7-day quota is used up.',
  'dashboard.balanceFallbackToggle.title': 'Use balance after monthly card quota',
  'dashboard.balanceOrderHint': 'Subscription quota is used before recharge balance.',
  'dashboard.buySubscription': 'Buy subscription',
  'dashboard.input': 'Input',
  'dashboard.output': 'Output',
  'dashboard.performance': 'Performance',
  'dashboard.rechargeBalance': 'Recharge balance',
  'dashboard.rewardBalance.daily': 'Check-in {amount}, expires {expiresAt}',
  'dashboard.rewardBalance.affiliate': 'Invite rewards {amount}, earliest expiry {expiresAt}',
  'dashboard.rewardBalance.detailCount': '{count} details',
  'dashboard.requests': 'Requests',
  'dashboard.standard': 'Standard',
  'dashboard.subscriptionBalance': 'Subscription balance',
  'dashboard.currentSubscription': 'Current subscription',
  'dashboard.noCurrentSubscription': 'No active subscription',
  'dashboard.noSubscriptionPurchaseHint': 'Choose a monthly card from the purchase page.',
  'dashboard.subscriptionRemaining': '{remaining} remaining of {total}',
  'dashboard.subscriptionPlan': 'Plan: {plan}',
  'dashboard.subscriptionPlanCount': '{count} active plans',
  'dashboard.subscriptionResetAt': 'Resets {time}',
  'dashboard.subscriptionPlanPeriod': 'month',
  'dashboard.subscriptionWeeklyQuota': '{amount} / 7 days',
  'dashboard.subscriptionAllRoutes': 'Claude Code + OpenAI',
  'dashboard.renewSubscription': 'Renew subscription',
  'dashboard.pendingSubscriptionChange': '{plan} starts on {time}',
  'payment.currentSubscription': 'Current subscription',
  'payment.days': 'days',
  'payment.noPlans': 'No subscription plans available',
  'payment.planCard.quota': 'Quota',
  'payment.planCard.totalMonthlyQuota': 'Total obtainable',
  'payment.planCard.unlimited': 'Unlimited',
  'payment.renewNow': 'Renew',
  'payment.subscribeNow': 'Subscribe now',
  'payment.switchSubscription': 'Switch subscription',
  'dashboard.todayCost': 'Today Cost',
  'dashboard.todayRequests': 'Today Requests',
  'dashboard.todayTokens': 'Today Tokens',
  'dashboard.tokens': 'Tokens',
  'dashboard.totalTokens': 'Total Tokens',
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      let value = messages[key as keyof typeof messages] ?? key
      for (const [name, replacement] of Object.entries(params ?? {})) {
        value = value.replace(`{${name}}`, String(replacement))
      }
      return value
    },
    locale: { value: 'en' },
  }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('@/api/user', () => ({
  userAPI: {
    updateProfile: (...args: unknown[]) => mockUpdateProfile(...args),
  },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
}))

vi.mock('@/components/user/RewardBalanceBreakdown.vue', () => ({
  default: {
    props: ['summary'],
    template: '<div v-if="summary" data-testid="dashboard-reward-breakdown">{{ summary.daily_check_in.amount }} / {{ summary.affiliate.amount }}</div>',
  },
}))

config.global.stubs = {
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
}

function formatExpectedResetTime(value: string): string {
  const date = new Date(value)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}/${month}/${day} ${hour}:${minute}`
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

describe('UserDashboardStats', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authState.user = null
  })

  it('saves the balance fallback preference and updates the auth user when toggled on', async () => {
    const updatedUser = {
      id: 1,
      balance: 25,
      subscription_balance_fallback_enabled: true,
    }
    let resolveUpdate: (user: typeof updatedUser) => void = () => {}
    mockUpdateProfile.mockReturnValue(new Promise(resolve => { resolveUpdate = resolve }))
    authState.user = {
      id: 1,
      balance: 25,
      subscription_balance_fallback_enabled: false,
    }

    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        subscriptionBalanceFallbackEnabled: false,
      },
    })

    const toggle = wrapper.get('[data-testid="subscription-balance-fallback-toggle"]')
    expect(wrapper.text()).toContain('Default off. Requests stop after the monthly card 7-day quota is used up, even if the account still has balance.')

    await toggle.trigger('click')
    expect(toggle.attributes('disabled')).toBeDefined()

    resolveUpdate(updatedUser)
    await flushPromises()

    expect(mockUpdateProfile).toHaveBeenCalledWith({ subscription_balance_fallback_enabled: true })
    expect(authState.user).toEqual(updatedUser)
    expect(wrapper.text()).toContain('When on, requests continue by deducting recharge balance after the monthly card 7-day quota is used up.')
  })

  it('shows subscription and recharge balances separately for non-simple users', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        subscriptionBalance: {
          remaining: 72.5,
          total: 110,
          used: 37.5,
          resetAt: '2030-01-08T12:00:00Z',
          planName: 'Plus monthly',
          planKey: 'plus',
          priceCny: 399,
        },
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: 0,
            plan_id: null,
            plan_name: 'Plus monthly',
            status: 'active',
            starts_at: '2030-01-01T00:00:00Z',
            expires_at: '2030-02-01T00:00:00Z',
            daily_usage_usd: 0,
            weekly_usage_usd: 37.5,
            monthly_usage_usd: 37.5,
            seven_day_limit_usd: 110,
            seven_day_usage_usd: 37.5,
            seven_day_remaining_usd: 72.5,
            seven_day_reset_at: '2030-01-08T12:00:00Z',
            daily_window_start: null,
            weekly_window_start: null,
            monthly_window_start: null,
            created_at: '2030-01-01T00:00:00Z',
            updated_at: '2030-01-01T00:00:00Z',
          },
        ],
      },
    })

    const text = wrapper.text()

    expect(text).toContain('Current subscription')
    expect(text).toContain('Plus monthly')
    expect(text).not.toContain('Basic monthly')
    expect(text).not.toContain('Pro monthly')
    expect(text).not.toContain('Max monthly')
    expect(text).not.toContain('Renew')
    expect(text).not.toContain('Switch subscription')
    expect(text).toContain('$72.50 remaining of $110.00')
    expect(text).toContain(`Resets ${formatExpectedResetTime('2030-01-08T12:00:00Z')}`)
    expect(wrapper.find('[data-testid="dashboard-subscription-plans"]').exists()).toBe(false)
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('66')
    expect(wrapper.find('[role="progressbar"] > div').attributes('style')).toContain('width: 66%;')
    expect(text).toContain('Recharge balance')
    expect(text).toContain('$25.00')
    expect(text).toContain('Subscription quota is used before recharge balance.')
    expect(text).not.toContain('Balance$25.00available')
  })

  it('keeps total balance unchanged and shows reward balance bullets when present', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        rewardBalances: {
          daily_check_in: { amount: 5, expires_at: '2030-01-02T16:00:00Z' },
          affiliate: { amount: 10, earliest_expires_at: '2030-01-08T00:00:00Z', credit_count: 1 },
        },
      },
    })

    expect(wrapper.text()).toContain('$25.00')
    expect(wrapper.text()).not.toContain('$40.00')
    expect(wrapper.get('[data-testid="dashboard-reward-breakdown"]').text()).toBe('5 / 10')
  })

  it('hides standard cost comparison by default for regular users', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
      },
    })

    const text = wrapper.text()

    expect(text).toContain('$1.2500')
    expect(text).toContain('$10.0000')
    expect(text).not.toContain('$2.0000')
    expect(text).not.toContain('$15.0000')
    expect(wrapper.find('[title="Standard"]').exists()).toBe(false)
  })

  it('keeps standard cost comparison available when explicitly enabled', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        showStandardCosts: true,
      },
    })

    const text = wrapper.text()

    expect(text).toContain('$1.2500 / $2.0000')
    expect(text).toContain('Total: $10.0000 / $15.0000')
    expect(wrapper.find('[title="Standard"]').exists()).toBe(true)
  })

  it('shows localized active plan count for multiple subscription plans', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        subscriptionBalance: {
          remaining: 125,
          total: 160,
          used: 35,
          resetAt: '2030-01-08T12:00:00Z',
          planName: null,
          planNames: ['Basic monthly', 'Plus monthly'],
          activePlanCount: 2,
          displayMode: 'multiple',
        },
      },
    })

    expect(wrapper.text()).toContain('2 active plans')
  })

  it('does not render per-platform billing breakdown', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats: {
          ...stats,
          today_actual_cost: 1.25,
          total_actual_cost: 10,
          by_platform: [
            {
              platform: 'openai',
              today_actual_cost: 0.75,
              total_actual_cost: 6,
              total_requests: 30,
              total_tokens: 1200,
            },
          ],
        },
        balance: 25,
        isSimple: false,
      },
    })

    const text = wrapper.text()
    expect(text).not.toContain('Per-platform Breakdown')
    expect(text).not.toContain('OpenAI')
    expect(text).not.toContain('Quota Usage')
  })

  it('shows a purchase button instead of subscription cards when no subscription is active', async () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        subscriptionBalance: null,
      },
    })

    const text = wrapper.text()
    expect(text).toContain('No active subscription')
    expect(text).toContain('Choose a monthly card from the purchase page.')
    expect(text).toContain('Buy subscription')
    expect(text).not.toContain('Basic monthly')
    expect(wrapper.find('[data-testid="dashboard-subscription-plans"]').exists()).toBe(false)

    await wrapper.get('[data-testid="dashboard-buy-subscription"]').trigger('click')

    expect(routerPush).toHaveBeenCalledWith({
      path: '/purchase',
      query: { tab: 'subscription' },
    })
  })

  it('does not render pending downgrade notices on dashboard plan cards', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: false,
        subscriptionBalance: {
          remaining: 100,
          total: 260,
          used: 160,
          resetAt: '2030-01-08T12:00:00Z',
          planName: 'Pro monthly',
          planKey: 'pro',
          priceCny: 799,
        },
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: 0,
            plan_id: null,
            plan_name: 'Pro monthly',
            scheduled_plan_name: 'Basic monthly',
            scheduled_plan_effective_at: '2030-02-01T00:00:00Z',
            status: 'active',
            starts_at: '2030-01-01T00:00:00Z',
            expires_at: '2030-02-01T00:00:00Z',
            daily_usage_usd: 0,
            weekly_usage_usd: 160,
            monthly_usage_usd: 160,
            seven_day_limit_usd: 260,
            seven_day_usage_usd: 160,
            seven_day_remaining_usd: 100,
            seven_day_reset_at: '2030-01-08T12:00:00Z',
            daily_window_start: null,
            weekly_window_start: null,
            monthly_window_start: null,
            created_at: '2030-01-01T00:00:00Z',
            updated_at: '2030-01-01T00:00:00Z',
          },
        ],
      },
    })

    expect(wrapper.text()).not.toContain(`Basic monthly starts on ${formatExpectedResetTime('2030-02-01T00:00:00Z')}`)
  })

  it('does not show balance cards in simple mode', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 25,
        isSimple: true,
        subscriptionBalance: {
          remaining: 72.5,
          total: 110,
          used: 37.5,
          resetAt: '2030-01-08T12:00:00Z',
          planName: 'Plus monthly',
          planKey: 'plus',
          priceCny: 399,
        },
      },
    })

    expect(wrapper.text()).not.toContain('Current subscription')
    expect(wrapper.text()).not.toContain('Recharge balance')
  })
})
