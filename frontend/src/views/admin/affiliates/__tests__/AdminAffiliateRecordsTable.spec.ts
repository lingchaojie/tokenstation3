import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminAffiliateRecordsTable from '../AdminAffiliateRecordsTable.vue'

const { listInviteRecords, listRebateRecords, listTransferRecords, getUserOverview } = vi.hoisted(() => ({
  listInviteRecords: vi.fn(),
  listRebateRecords: vi.fn(),
  listTransferRecords: vi.fn(),
  getUserOverview: vi.fn(),
}))

vi.mock('@/api/admin/affiliates', () => {
  const affiliatesAPI = { listInviteRecords, listRebateRecords, listTransferRecords, getUserOverview }
  return { affiliatesAPI, default: affiliatesAPI }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const TablePageLayoutStub = {
  template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
}

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <table>
      <tr v-for="(row, index) in data" :key="index" :data-test="'row-' + index">
        <td v-for="column in columns" :key="column.key" :data-test="'cell-' + column.key">
          <slot :name="'cell-' + column.key" :row="row" />
        </td>
      </tr>
    </table>
  `,
}


function mountTable(type: 'invites' | 'rebates' | 'transfers') {
  return mount(AdminAffiliateRecordsTable, {
    props: { type },
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        Icon: true,
        OrderStatusBadge: true,
        BaseDialog: true,
      },
    },
  })
}

function page<T>(items: T[]) {
  return { items, total: items.length, page: 1, page_size: 20, pages: 1 }
}

describe('AdminAffiliateRecordsTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('renders non-order rebate accruals with empty order fields', async () => {
    listRebateRecords.mockResolvedValue(page([
      {
        order_id: null,
        out_trade_no: '',
        inviter_id: 1,
        inviter_email: 'inviter@example.com',
        inviter_username: 'inviter',
        invitee_id: 2,
        invitee_email: 'invitee@example.com',
        invitee_username: 'invitee',
        order_amount: null,
        pay_amount: null,
        rebate_amount: 2,
        payment_type: '',
        order_status: '',
        created_at: '2026-09-17T08:00:00Z',
      },
    ]))

    const wrapper = mountTable('rebates')
    await flushPromises()

    const row = wrapper.get('[data-test="row-0"]')
    expect(row.get('[data-test="cell-order"]').text()).toBe('-')
    expect(row.get('[data-test="cell-order_amount"]').text()).toBe('-')
    expect(row.get('[data-test="cell-pay_amount"]').text()).toBe('-')
    expect(row.get('[data-test="cell-payment_type"]').text()).toBe('-')
    expect(row.get('[data-test="cell-order_status"]').text()).toBe('-')
    expect(row.get('[data-test="cell-rebate_amount"]').text()).toBe('$2.00')
    expect(wrapper.find('[data-test="affiliate-withdraw-open"]').exists()).toBe(false)
  })

})
