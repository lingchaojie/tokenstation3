import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountStatsModal from '../AccountStatsModal.vue'
import AdminAccountStatsModal from '@/components/admin/account/AccountStatsModal.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import type { Account, AccountUsageStatsResponse } from '@/types'

const { getStats, getUserBreakdown } = vi.hoisted(() => ({ getStats: vi.fn(), getUserBreakdown: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getStats } } }))
vi.mock('@/api/admin/dashboard', () => ({ getUserBreakdown }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-chartjs', () => ({ Line: { template: '<div />' }, Doughnut: { template: '<div />' } }))

const endpoint = { requests: 1, total_tokens: 10, cost: 8, actual_cost: 3, account_cost: 2 }
const report: AccountUsageStatsResponse = {
  start_date: '2026-03-01',
  end_date: '2026-03-30',
  timezone: 'America/New_York',
  history: [{ date: '2026-03-08', label: '03/08', requests: 1, tokens: 10, cost: 8, actual_cost: 2, user_cost: 3 }],
  summary: {
    days: 30, actual_days_used: 1, total_cost: 2, total_user_cost: 3, total_standard_cost: 8,
    total_requests: 1, total_tokens: 10, avg_daily_cost: 2, avg_daily_user_cost: 3,
    avg_daily_requests: 1, avg_daily_tokens: 10, avg_duration_ms: 1,
    today: null, highest_cost_day: null, highest_request_day: null,
  },
  models: [{ ...endpoint, model: 'model-a', input_tokens: 10, output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0 }],
  endpoints: [{ ...endpoint, endpoint: '/v1/messages' }],
  upstream_endpoints: [{ ...endpoint, endpoint: '/v1/responses' }],
}

beforeEach(() => {
  getStats.mockReset().mockResolvedValue(report)
  getUserBreakdown.mockReset().mockResolvedValue({ users: [] })
})

describe.each([
  { name: 'account modal', component: AccountStatsModal },
  { name: 'admin account modal', component: AdminAccountStatsModal },
])('$name', ({ component }) => {
  it('uses the account report filters and correct model and endpoint dimensions', async () => {
    const wrapper = mount(component, {
      props: { show: true, account: { id: 42, name: 'test', status: 'active' } as Account },
      global: { stubs: { BaseDialog: { template: '<section><slot /></section>' }, Icon: true } },
    })
    await flushPromises()

    const reportFilters = { account_id: 42, start_date: '2026-03-01', end_date: '2026-03-30', timezone: 'America/New_York' }
    expect(getStats).toHaveBeenCalledWith(42, 30)
    await wrapper.getComponent(ModelDistributionChart).get('tbody tr').trigger('click')
    expect(getUserBreakdown).toHaveBeenLastCalledWith({ ...reportFilters, model: 'model-a', model_source: 'requested' })

    const [inbound, upstream] = wrapper.findAllComponents(EndpointDistributionChart)
    expect(upstream.get('tbody tr').text()).toContain('/v1/responses')
    expect(upstream.findAll('tbody tr td').slice(-3).map(cell => cell.text())).toEqual(['$3.00', '$2.00', '$8.00'])
    await upstream.get('tbody tr').trigger('click')
    expect(getUserBreakdown).toHaveBeenLastCalledWith({ ...reportFilters, endpoint: '/v1/responses', endpoint_type: 'upstream' })
    await inbound.get('tbody tr').trigger('click')
    expect(getUserBreakdown).toHaveBeenLastCalledWith({ ...reportFilters, endpoint: '/v1/messages', endpoint_type: 'inbound' })
    wrapper.unmount()
  })
})

it('can render only user actual debit in the endpoint table', () => {
  const wrapper = mount(EndpointDistributionChart, {
    props: {
      endpointStats: report.endpoints,
      showAccountCost: false,
      showStandardCost: false,
      enableBreakdown: false,
    },
  })
  expect(wrapper.get('thead').text()).toContain('admin.dashboard.actual')
  expect(wrapper.findAll('thead th')).toHaveLength(4)
  expect(wrapper.get('tbody').text()).toContain('$3.00')
  expect(wrapper.get('tbody').text()).not.toContain('$2.00')
  expect(wrapper.get('tbody').text()).not.toContain('$8.00')
  wrapper.unmount()
})
