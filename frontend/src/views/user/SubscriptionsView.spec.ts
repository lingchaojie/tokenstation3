import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import SubscriptionsView from './SubscriptionsView.vue'

const mockGetMySubscriptions = vi.fn()
const mockShowError = vi.fn()
const mockPush = vi.fn()

const messages = vi.hoisted(() => ({
  'common.total': 'Total',
  'payment.renewNow': 'Renew now',
  'payment.planCard.totalMonthlyQuota': 'Total obtainable',
  'userSubscriptions.balanceOrderHint': 'Subscription quota is used before recharge balance.',
  'userSubscriptions.daily': 'Daily',
  'userSubscriptions.expires': 'Expires',
  'userSubscriptions.failedToLoad': 'Failed to load subscriptions',
  'userSubscriptions.monthly': 'Monthly',
  'userSubscriptions.nextReset': 'Next reset',
  'userSubscriptions.noActiveSubscriptions': 'No Active Subscriptions',
  'userSubscriptions.noActiveSubscriptionsDesc': 'No active subscriptions.',
  'userSubscriptions.noExpiration': 'No expiration',
  'userSubscriptions.plan': 'Active plan',
  'userSubscriptions.quotaEndsIn': 'Quota ends in {time}',
  'userSubscriptions.remaining': 'Remaining {amount}',
  'userSubscriptions.resetIn': 'Resets in {time}',
  'userSubscriptions.sevenDayQuota': '7-day quota',
  'userSubscriptions.status.active': 'Active',
  'userSubscriptions.status.expired': 'Expired',
  'userSubscriptions.status.revoked': 'Revoked',
  'userSubscriptions.unlimited': 'Unlimited',
  'userSubscriptions.unlimitedDesc': 'No usage limits',
  'userSubscriptions.usageOf': '{used} of {limit}',
  'userSubscriptions.weekly': 'Weekly',
  'userSubscriptions.windowNotActive': 'Awaiting first use',
}))

vi.mock('vue-i18n', () => {
  const translate = (key: string, params?: Record<string, unknown>) => {
    let value = messages[key as keyof typeof messages] ?? key
    for (const [name, replacement] of Object.entries(params ?? {})) {
      value = value.replace(`{${name}}`, String(replacement))
    }
    return value
  }

  return {
    createI18n: () => ({
      global: {
        locale: { value: 'en' },
        t: translate,
      },
    }),
    useI18n: () => ({
      t: translate,
      locale: { value: 'en' },
    }),
  }
})

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mockPush }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mockShowError }),
}))

vi.mock('@/api/subscriptions', () => ({
  default: {
    getMySubscriptions: (...args: unknown[]) => mockGetMySubscriptions(...args),
  },
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
}))

describe('SubscriptionsView', () => {
  it('shows active plan seven-day quota usage, next reset, expiry, and renewal action', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 1,
        user_id: 1,
        group_id: 10,
        plan_id: 20,
        plan_name: 'Plus monthly',
        status: 'active',
        starts_at: '2030-01-01T00:00:00Z',
        expires_at: '2030-02-01T00:00:00Z',
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        seven_day_limit_usd: 110,
        seven_day_usage_usd: 37.5,
        seven_day_remaining_usd: 72.5,
        seven_day_reset_at: '2030-01-08T12:00:00Z',
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2030-01-01T00:00:00Z',
        updated_at: '2030-01-01T00:00:00Z',
        group: {
          id: 10,
          name: 'LINX2 Subscription',
          description: 'Developer plan',
          platform: 'anthropic',
          daily_limit_usd: 25,
          weekly_limit_usd: null,
          monthly_limit_usd: 500,
        },
      },
    ])

    const wrapper = mount(SubscriptionsView)
    await flushPromises()

    const text = wrapper.text()

    expect(text).toContain('Active plan')
    expect(text).toContain('Plus monthly')
    expect(text).toContain('For everyday development when LINX2 is your steady personal coding gateway.')
    expect(text).toContain('7-day quota')
    expect(text).toContain('$37.50')
    expect(text).toContain('$72.50')
    expect(text).toContain('$110.00')
    expect(text).toContain('Total obtainable: $440.00')
    expect(text).toContain('Next reset')
    expect(text).toContain('Expires')
    expect(text).toContain('Renew now')
    expect(text).toContain('Subscription quota is used before recharge balance.')
    expect(text).toContain('Daily')
    expect(text).toContain('Monthly')
  })

  it('renews generic subscriptions by plan id instead of nullable group id', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 3,
        user_id: 1,
        group_id: null,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        starts_at: '2030-01-01T00:00:00Z',
        expires_at: '2030-02-01T00:00:00Z',
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        seven_day_limit_usd: 260,
        seven_day_usage_usd: 0,
        seven_day_remaining_usd: 260,
        seven_day_reset_at: null,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2030-01-01T00:00:00Z',
        updated_at: '2030-01-01T00:00:00Z',
      },
    ])

    const wrapper = mount(SubscriptionsView)
    await flushPromises()

    await wrapper.get('button').trigger('click')

    expect(mockPush).toHaveBeenCalledWith({
      path: '/purchase',
      query: { tab: 'subscription', plan: '7', intent: 'renew' },
    })
  })

  it('does not show unlimited for seven-day quota-only subscriptions', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 2,
        user_id: 1,
        group_id: 11,
        plan_id: 21,
        plan_name: 'Seven-day only',
        status: 'active',
        starts_at: '2030-01-01T00:00:00Z',
        expires_at: '2030-02-01T00:00:00Z',
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        seven_day_limit_usd: 50,
        seven_day_usage_usd: 12.5,
        seven_day_remaining_usd: 37.5,
        seven_day_reset_at: '2030-01-08T12:00:00Z',
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2030-01-01T00:00:00Z',
        updated_at: '2030-01-01T00:00:00Z',
        group: {
          id: 11,
          name: 'Quota-only Group',
          description: null,
          platform: 'anthropic',
          daily_limit_usd: null,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
        },
      },
    ])

    const wrapper = mount(SubscriptionsView)
    await flushPromises()

    const text = wrapper.text()

    expect(text).toContain('7-day quota')
    expect(text).toContain('$12.50')
    expect(text).toContain('$50.00')
    expect(text).not.toContain('Unlimited')
    expect(text).not.toContain('No usage limits')
  })
})
