import { config, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

const routerPush = vi.hoisted(() => vi.fn())

import UserDashboardStats from './UserDashboardStats.vue'

const messages = vi.hoisted(() => ({
  'common.active': 'active',
  'common.available': 'available',
  'common.total': 'Total',
  'dashboard.actual': 'Actual',
  'dashboard.apiKeys': 'API Keys',
  'dashboard.averageTime': 'Average time',
  'dashboard.avgResponse': 'Avg Response',
  'dashboard.balanceOrderHint': 'Subscription quota is used before recharge balance.',
  'dashboard.input': 'Input',
  'dashboard.output': 'Output',
  'dashboard.performance': 'Performance',
  'dashboard.platformBreakdown': 'Per-platform Breakdown',
  'dashboard.platformCount': '{count} platforms',
  'dashboard.platformOther': 'Other',
  'dashboard.platformQuota.daily': 'Daily',
  'dashboard.platformQuota.disabled': 'Disabled',
  'dashboard.platformQuota.monthly': 'Monthly',
  'dashboard.platformQuota.resetsAt': 'Resets {time}',
  'dashboard.platformQuota.title': 'Quota Usage',
  'dashboard.platformQuota.weekly': 'Weekly',
  'dashboard.rechargeBalance': 'Recharge balance',
  'dashboard.requests': 'Requests',
  'dashboard.standard': 'Standard',
  'dashboard.subscriptionBalance': 'Subscription balance',
  'dashboard.currentSubscription': 'Current subscription',
  'dashboard.noCurrentSubscription': 'No active subscription',
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

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
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
    expect(text).toContain('Basic monthly')
    expect(text).toContain('Pro monthly')
    expect(text).toContain('Max monthly')
    expect(text).toContain('¥399')
    expect(text).toContain('/ 30days')
    expect(text).toContain('$110 / 7 days')
    expect(text).toContain('Renew')
    expect(text).toContain('Switch subscription')
    expect(text).not.toContain('Claude Code + OpenAI')
    expect(text).toContain('$72.50 remaining of $110.00')
    expect(text).toContain(`Resets ${formatExpectedResetTime('2030-01-08T12:00:00Z')}`)
    expect(wrapper.findAll('[data-testid="dashboard-subscription-plans"] .linear-plan-card').length).toBe(4)
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('66')
    expect(wrapper.find('[role="progressbar"] > div').attributes('style')).toContain('width: 66%;')
    expect(text).toContain('Recharge balance')
    expect(text).toContain('$25.00')
    expect(text).toContain('Subscription quota is used before recharge balance.')
    expect(text).not.toContain('Balance$25.00available')
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

  it('renders pending downgrade notice on the current plan card', () => {
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

    expect(wrapper.text()).toContain(`Basic monthly starts on ${formatExpectedResetTime('2030-02-01T00:00:00Z')}`)
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
