/// <reference path="../../../vite-env.d.ts" />

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import RedeemView from '../RedeemView.vue'

const {
  redeem,
  getHistory,
  getPublicSettings,
  refreshUser,
  fetchActiveSubscriptions,
  showSuccess,
  showError,
  showWarning,
} = vi.hoisted(() => ({
  redeem: vi.fn(),
  getHistory: vi.fn(),
  getPublicSettings: vi.fn(),
  refreshUser: vi.fn(),
  fetchActiveSubscriptions: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('@/api', () => ({
  redeemAPI: {
    redeem,
    getHistory,
  },
  authAPI: {
    getPublicSettings,
  },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { balance: 12.34, concurrency: 2 },
    refreshUser,
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
    showWarning,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/utils/format', () => ({
  formatDateTime: (value: string) => value,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'redeem.subscriptionDays') return `${params?.days} days`
        if (key === 'redeem.planActivated') {
          return `Activated ${params?.planName}, valid for ${params?.duration}`
        }
        if (key === 'redeem.planFallback') return `Plan #${params?.id}`
        if (key === 'redeem.subscriptionDurationUnknown') return 'duration unknown'
        if (key === 'redeem.planHistoryTitle') return `${params?.planName}`
        return key
      },
    }),
  }
})

function mountView() {
  return mount(RedeemView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
        Transition: false,
      },
    },
  })
}

describe('user RedeemView plan subscription display', () => {
  beforeEach(() => {
    redeem.mockReset()
    getHistory.mockReset()
    getPublicSettings.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    showWarning.mockReset()

    getPublicSettings.mockResolvedValue({ contact_info: '' })
    getHistory.mockResolvedValue([])
    refreshUser.mockResolvedValue(undefined)
    fetchActiveSubscriptions.mockResolvedValue(undefined)
  })

  it('shows activated plan and plan default duration after redeeming a plan code', async () => {
    redeem.mockResolvedValue({
      message: 'redeemed',
      type: 'subscription',
      value: 0,
      validity_days: 0,
      plan_id: 42,
      plan: {
        id: 42,
        name: 'Pro Monthly',
        product_name: 'Claude Pro',
        validity_days: 30,
        validity_unit: 'days',
        seven_day_quota_usd: 10,
        for_sale: false,
      },
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('input#code').setValue('PLAN-CODE')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('Activated Pro Monthly, valid for 30 days')
    expect(fetchActiveSubscriptions).toHaveBeenCalledWith(true)
  })

  it('shows plan names, fallbacks, and effective durations in redeem history without changing group-mode display', async () => {
    getHistory.mockResolvedValue([
      {
        id: 1,
        code: 'PLAN-CODE',
        type: 'subscription',
        value: 0,
        status: 'used',
        validity_days: 0,
        plan_id: 42,
        plan: {
          id: 42,
          name: 'Pro Monthly',
          product_name: 'Claude Pro',
          validity_days: 30,
          validity_unit: 'days',
          seven_day_quota_usd: 10,
          for_sale: false,
        },
        used_at: '2026-01-01T00:00:00Z',
        created_at: '2026-01-01T00:00:00Z',
      },
      {
        id: 2,
        code: 'MISSING-PLAN',
        type: 'subscription',
        value: 15,
        status: 'used',
        validity_days: 0,
        plan_id: 99,
        used_at: '2026-01-02T00:00:00Z',
        created_at: '2026-01-02T00:00:00Z',
      },
      {
        id: 3,
        code: 'GROUP-CODE',
        type: 'subscription',
        value: 7,
        status: 'used',
        validity_days: 7,
        group_id: 7,
        group: { id: 7, name: 'Legacy Group' },
        used_at: '2026-01-03T00:00:00Z',
        created_at: '2026-01-03T00:00:00Z',
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('Pro Monthly')
    expect(text).toContain('30 days')
    expect(text).toContain('Plan #99')
    expect(text).toContain('duration unknown')
    expect(text).not.toContain('15 days')
    expect(text).toContain('Legacy Group')
    expect(text).toContain('7redeem.days - Legacy Group')
  })
})
