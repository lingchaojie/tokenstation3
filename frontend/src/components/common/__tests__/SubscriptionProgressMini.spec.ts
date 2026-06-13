import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SubscriptionProgressMini from '../SubscriptionProgressMini.vue'
import { useSubscriptionStore } from '@/stores/subscriptions'

const mockGetActiveSubscriptions = vi.fn()

const { translate } = vi.hoisted(() => {
  const messages = {
    'subscriptionProgress.activeCount': '{count} active subscription(s)',
    'subscriptionProgress.daily': 'Daily',
    'subscriptionProgress.expired': 'Expired',
    'subscriptionProgress.expiresToday': 'Expires today',
    'subscriptionProgress.expiresTomorrow': 'Expires tomorrow',
    'subscriptionProgress.daysRemaining': '{days} days left',
    'subscriptionProgress.monthly': 'Monthly',
    'subscriptionProgress.sevenDay': '7-day',
    'subscriptionProgress.title': 'My Subscriptions',
    'subscriptionProgress.unlimited': 'Unlimited',
    'subscriptionProgress.viewAll': 'View all subscriptions',
    'subscriptionProgress.viewDetails': 'View subscription details',
    'subscriptionProgress.weekly': 'Weekly',
  }

  return {
    translate: (key: string, params?: Record<string, unknown>) => {
      let value = messages[key as keyof typeof messages] ?? key
      for (const [name, replacement] of Object.entries(params ?? {})) {
        value = value.replace(`{${name}}`, String(replacement))
      }
      return value
    },
  }
})

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      locale: { value: 'en' },
      t: translate,
    },
  }),
  useI18n: () => ({
    t: translate,
  }),
}))

vi.mock('@/api/subscriptions', () => ({
  default: {
    getActiveSubscriptions: (...args: unknown[]) => mockGetActiveSubscriptions(...args),
  },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
}))

describe('SubscriptionProgressMini', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('shows seven-day quota-only subscriptions as limited instead of unlimited', async () => {
    const store = useSubscriptionStore()
    store.activeSubscriptions = [
      {
        id: 1,
        user_id: 1,
        group_id: 10,
        plan_id: 20,
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
          id: 10,
          name: 'Quota-only Group',
          platform: 'anthropic',
          daily_limit_usd: null,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
        },
      },
    ]
    mockGetActiveSubscriptions.mockResolvedValue(store.activeSubscriptions)

    const wrapper = mount(SubscriptionProgressMini, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          transition: false,
        },
      },
    })

    await wrapper.find('button').trigger('click')
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('7-day')
    expect(text).toContain('$12.50/$50.00')
    expect(text).not.toContain('Unlimited')
  })
})
