import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminPaymentPlansView from '../AdminPaymentPlansView.vue'
import type { SubscriptionPlan } from '@/types/payment'

const getPlans = vi.hoisted(() => vi.fn())
const getAllGroups = vi.hoisted(() => vi.fn())
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

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    getPlans,
    updatePlan: vi.fn(),
    deletePlan: vi.fn(),
  },
}))

vi.mock('@/api/admin', () => ({
  default: {
    groups: {
      getAll: getAllGroups,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
  }),
}))

function planFixture(overrides: Partial<SubscriptionPlan> = {}): SubscriptionPlan {
  return {
    id: 1,
    group_id: 2,
    group_platform: 'anthropic',
    group_name: 'LINX2 Subscription',
    rate_multiplier: 1,
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
        GroupBadge: {
          props: ['name'],
          template: '<span>{{ name }}</span>',
        },
        PlanEditDialog: true,
      },
    },
  })
}

describe('AdminPaymentPlansView', () => {
  beforeEach(() => {
    getPlans.mockReset().mockResolvedValue({ data: [] })
    getAllGroups.mockReset().mockResolvedValue([])
    showError.mockReset()
  })

  it('shows a seven-day quota column and formats missing quotas as a dash', async () => {
    getPlans.mockResolvedValue({
      data: [
        planFixture({ id: 1, name: 'Plus monthly', seven_day_quota_usd: 110 }),
        planFixture({ id: 2, name: 'Legacy monthly', seven_day_quota_usd: null }),
      ],
    })

    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.admin.sevenDayQuota')
    expect(text).toContain('$110.00')
    expect(text).toContain('-')
  })
})
