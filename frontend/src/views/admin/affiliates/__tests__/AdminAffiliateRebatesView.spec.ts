import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const listRebateRecords = vi.hoisted(() => vi.fn())

vi.mock('@/api/admin/affiliates', () => {
  const affiliatesAPI = {
    listRebateRecords,
    listInviteRecords: vi.fn(),
    listTransferRecords: vi.fn(),
    getUserOverview: vi.fn(),
  }
  return { affiliatesAPI, default: affiliatesAPI }
})

vi.mock('@/utils/format', () => ({
  formatDateTime: (value: string) => `DATE(${value})`,
}))

vi.mock('@/utils/apiError', () => ({
  extractI18nErrorMessage: () => 'error',
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn() }),
}))

vi.mock('@/components/payment/OrderStatusBadge.vue', () => ({
  default: { props: ['status'], template: '<span>{{ status || "-" }}</span>' },
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  const messages: Record<string, string> = {
    'admin.affiliates.records.source': 'Source',
    'admin.affiliates.records.sources.legacy': 'Legacy rebate',
    'admin.affiliates.records.sources.rewardCredit': 'Reward credit',
    'admin.affiliates.records.rewardRole': 'Reward role',
    'admin.affiliates.records.roles.inviter': 'Inviter reward',
    'admin.affiliates.records.roles.invitee': 'Invitee reward',
    'admin.affiliates.records.remainingAmount': 'Remaining',
    'admin.affiliates.records.expiresAt': 'Expires at',
  }
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => messages[key] ?? key }),
  }
})

const DataTableStub = defineComponent({
  props: ['columns', 'data'],
  template: `
    <div>
      <div data-testid="rebate-columns">{{ columns.map((column) => column.label).join('|') }}</div>
      <div v-for="row in data" :key="row.record_source + ':' + row.rebate_amount" class="audit-row">
        <slot name="cell-record_source" :row="row" />
        <slot name="cell-reward_role" :row="row" />
        <slot name="cell-order" :row="row" />
        <slot name="cell-rebate_amount" :row="row" />
        <slot name="cell-remaining_amount" :row="row" />
        <slot name="cell-expires_at" :row="row" />
      </div>
    </div>
  `,
})

import AdminAffiliateRebatesView from '../AdminAffiliateRebatesView.vue'

describe('AdminAffiliateRebatesView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listRebateRecords.mockResolvedValue({
      items: [
        {
          record_source: 'legacy', reward_role: null,
          order_id: 91, out_trade_no: 'ORDER-91', rebate_amount: 3,
          remaining_amount: null, expires_at: null, created_at: '2030-01-01T00:00:00Z',
        },
        {
          record_source: 'reward_credit', reward_role: 'inviter',
          order_id: 0, out_trade_no: '', rebate_amount: 10,
          remaining_amount: 8, expires_at: '2030-01-08T00:00:00Z', created_at: '2030-01-01T01:00:00Z',
        },
        {
          record_source: 'reward_credit', reward_role: 'invitee',
          order_id: 0, out_trade_no: '', rebate_amount: 5,
          remaining_amount: 5, expires_at: '2030-01-08T00:00:00Z', created_at: '2030-01-01T01:00:00Z',
        },
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1,
    })
  })

  it('shows legacy and reward-credit audit fields without losing order history', async () => {
    const wrapper = mount(AdminAffiliateRebatesView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: true,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(listRebateRecords).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 20 }))
    expect(wrapper.get('[data-testid="rebate-columns"]').text()).toContain('Source')
    expect(wrapper.get('[data-testid="rebate-columns"]').text()).toContain('Reward role')
    expect(wrapper.get('[data-testid="rebate-columns"]').text()).toContain('Remaining')
    expect(wrapper.get('[data-testid="rebate-columns"]').text()).toContain('Expires at')
    expect(wrapper.text()).toContain('Legacy rebate')
    expect(wrapper.text()).toContain('#91')
    expect(wrapper.text()).toContain('Inviter reward')
    expect(wrapper.text()).toContain('Invitee reward')
    expect(wrapper.text()).toContain('$8.00')
    expect(wrapper.text()).toContain('DATE(2030-01-08T00:00:00Z)')
  })
})
