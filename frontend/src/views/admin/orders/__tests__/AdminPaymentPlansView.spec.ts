import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminPaymentPlansView from '../AdminPaymentPlansView.vue'
import type { SubscriptionPlan } from '../../../../types/payment'

const getPlans = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/api/admin/payment', () => {
  const paymentAPI = {
    getPlans,
    updatePlan: vi.fn(),
    deletePlan: vi.fn(),
  }
  return {
    adminPaymentAPI: paymentAPI,
    default: paymentAPI,
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
  }),
}))

function planFixture(overrides: Partial<SubscriptionPlan> = {}): SubscriptionPlan {
  return {
    id: 1,
    name: 'Plus monthly',
    description: 'Everyday development',
    price: 399,
    original_price: 0,
    seven_day_quota_usd: 110,
    validity_days: 30,
    validity_unit: 'days',
    features: ['Seven-day quota'],
    for_sale: true,
    sort_order: 20,
    ...overrides,
  }
}

function mountView() {
  return mount(AdminPaymentPlansView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        DataTable: {
          props: ['columns', 'data', 'loading'],
          template: `
            <table>
              <thead><tr><th v-for="column in columns" :key="column.key">{{ column.label }}</th></tr></thead>
              <tbody>
                <tr v-for="row in data" :key="row.id">
                  <td v-for="column in columns" :key="column.key">
                    <slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">{{ row[column.key] }}</slot>
                  </td>
                </tr>
              </tbody>
            </table>
          `,
        },
        ConfirmDialog: true,
        Icon: true,
        PlanEditDialog: true,
      },
    },
  })
}

describe('AdminPaymentPlansView', () => {
  beforeEach(() => {
    getPlans.mockReset().mockResolvedValue({ data: [] })
    showError.mockReset()
  })

  it('shows user-paid prices as CNY while keeping quota totals in USD', async () => {
    getPlans.mockResolvedValue({
      data: [
        planFixture({ id: 1, name: 'Plus monthly', price: 399, original_price: 499, seven_day_quota_usd: 110 }),
        planFixture({ id: 2, name: 'Legacy monthly', price: 179, seven_day_quota_usd: null }),
      ],
    })

    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.admin.sevenDayQuota')
    expect(text).toContain('¥399.00')
    expect(text).toContain('¥499.00')
    expect(text).toContain('$110.00')
    expect(text).toContain('-')
    expect(text).not.toContain('$399.00')
    expect(text).not.toContain('$499.00')
  })

  it('shows real seat usage and virtual display range for limited plans', async () => {
    getPlans.mockResolvedValue({
      data: [
        planFixture({
          id: 3,
          name: 'Pro monthly',
          seat_limit: 100,
          seat_used: 12,
          virtual_seat_start: 4900,
          virtual_seat_total: 5000,
        }),
      ],
    })

    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('12/100')
    expect(text).toContain('4912/5000')
  })

  it('shows unlimited label without virtual display for unlimited plans with virtual fields', async () => {
    getPlans.mockResolvedValue({
      data: [
        planFixture({
          id: 4,
          name: 'Unlimited monthly',
          seat_limit: null,
          seat_used: 12,
          virtual_seat_start: 4900,
          virtual_seat_total: 5000,
        }),
      ],
    })

    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.admin.seatUnlimited')
    expect(text).not.toContain('4912/5000')
  })
})
