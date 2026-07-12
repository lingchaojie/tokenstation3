import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UsageView from '../UsageView.vue'

const { list, getStats, getSnapshotV2, getById, getModelStats, listErrorLogs, adminUsageList, saveAs, showError, showSuccess } = vi.hoisted(() => {
  vi.stubGlobal('localStorage', {
    getItem: vi.fn(() => null),
    setItem: vi.fn(),
    removeItem: vi.fn(),
  })

  return {
    list: vi.fn(),
    getStats: vi.fn(),
    getSnapshotV2: vi.fn(),
    getById: vi.fn(),
    getModelStats: vi.fn(),
    listErrorLogs: vi.fn(),
    adminUsageList: vi.fn(),
    saveAs: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }
})

const messages: Record<string, string> = {
  'admin.dashboard.timeRange': 'Time Range',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.usage.failedToLoadUser': 'Failed to load user',
  'admin.usage.billingType': 'Billing Type',
  'admin.usage.billingMode': 'Pricing Mode',
}

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      list,
      getStats,
    },
    dashboard: {
      getSnapshotV2,
      getModelStats,
    },
    users: {
      getById,
    },
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    list: adminUsageList,
  },
}))

vi.mock('@/api/admin/ops', () => ({
  listErrorLogs,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showWarning: vi.fn(),
    showSuccess,
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/utils/format', () => ({
  formatReasoningEffort: (value: string | null | undefined) => value ?? '-',
}))

vi.mock('file-saver', () => ({ saveAs }))

vi.mock('xlsx', () => ({
  utils: {
    aoa_to_sheet: vi.fn(() => ({})),
    sheet_add_aoa: vi.fn(),
    book_new: vi.fn(() => ({})),
    book_append_sheet: vi.fn(),
  },
  write: vi.fn(() => new Uint8Array()),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: {}
  })
}))

const AppLayoutStub = { template: '<div><slot /></div>' }
const UsageFiltersStub = { template: '<div><slot name="after-reset" /></div>' }
const UsageTableStub = {
  props: ['columns'],
  emits: ['userClick'],
  template: `
    <div data-test="usage-table">
      <div data-test="usage-table-columns">{{ (columns || []).map((column) => column.key).join(',') }}</div>
      <button class="user-click" @click="$emit('userClick', 2)">user</button>
    </div>
  `,
}
const UserTokenRankingStub = {
  emits: ['select-user'],
  template: '<div data-test="ranking"><button class="pick-user" @click="$emit(\'select-user\', 5, \'rank@test.com\')">pick</button></div>',
}
const ModelDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="model-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}
const GroupDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="group-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('admin UsageView distribution metric toggles', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({
      items: [],
      total: 0,
      pages: 0,
    })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({
      trend: [],
      models: [],
      groups: [],
    })
    getModelStats.mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps previous model stats visible during refresh until new data arrives', async () => {
    // 首次加载返回 A
    getModelStats.mockResolvedValueOnce({ models: [{ model: 'A', total_tokens: 10 }] })

    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: ModelDistributionChartStub, GroupDistributionChart: GroupDistributionChartStub,
        EndpointDistributionChart: true, UserTokenRanking: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // 刷新:让第二次 getModelStats 处于 pending,断言旧数据 A 仍在(不被清空成 [])
    let resolveSecond: (v: any) => void = () => {}
    getModelStats.mockReturnValueOnce(new Promise((res) => { resolveSecond = res }))
    ;(wrapper.vm as any).refreshData()
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // 新数据到达后替换为 B
    resolveSecond({ models: [{ model: 'B', total_tokens: 20 }] })
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'B', total_tokens: 20 }])
  })

  it('keeps model and group metric toggles independent without refetching chart data', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))

    const modelChart = wrapper.find('[data-test="model-chart"]')
    const groupChart = wrapper.find('[data-test="group-chart"]')

    expect(modelChart.find('.metric').text()).toBe('tokens')
    expect(groupChart.find('.metric').text()).toBe('tokens')

    await modelChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('tokens')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)

    await groupChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('actual_cost')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
  })
})

describe('admin UsageView handleUserClick', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('opens user via include_deleted when clicking a usage row user', async () => {
    getById.mockResolvedValue({ id: 2, email: 'd@test.com', deleted_at: '2026-05-28T00:00:00Z' })

    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    await wrapper.find('[data-test="usage-table"] .user-click').trigger('click')
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(2, true)
  })
})

describe('admin UsageView columns', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows billing type before pricing mode in the usage table', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    const columns = wrapper.get('[data-test="usage-table-columns"]').text().split(',')
    expect(columns).toContain('billing_type')
    expect(columns.indexOf('billing_type')).toBeLessThan(columns.indexOf('billing_mode'))
  })
})

describe('admin UsageView errors tab filter forwarding', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()
    listErrorLogs.mockReset()
    showError.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
    listErrorLogs.mockResolvedValue({ items: [], total: 0, pages: 0 })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('forwards model/account_id/group_id to listErrorLogs on the errors tab', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // 模拟用户在过滤器里选择了模型/账户/分组
    const vm = wrapper.vm as any
    vm.filters.model = 'gpt-5.3-codex'
    vm.filters.account_id = 7
    vm.filters.group_id = 3
    await flushPromises()

    // 切换到「错误请求」标签（第二个 tab 按钮）触发 loadAdminErrors
    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    await tabs[1].trigger('click')
    await flushPromises()

    expect(listErrorLogs).toHaveBeenCalledWith(expect.objectContaining({
      view: 'all',
      model: 'gpt-5.3-codex',
      account_id: 7,
      group_id: 3,
    }))
  })

  it('forwards error type filters to listErrorLogs on the errors tab', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    const vm = wrapper.vm as any
    vm.errErrorType = 'rate_limit_error'
    vm.errRequestErrorType = 'upstream'
    vm.errUpstreamErrorKind = 'failover'

    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    await tabs[1].trigger('click')
    await flushPromises()

    expect(listErrorLogs).toHaveBeenCalledWith(expect.objectContaining({
      view: 'all',
      error_type: 'rate_limit_error',
      request_error_type: 'upstream',
      upstream_error_kind: 'failover',
    }))
  })

  it.each(['resolve', 'reject'] as const)(
    'ignores a stale unfiltered error response that later %ss',
    async (staleOutcome) => {
      const stale = deferred<{ items: Array<{ id: number }>; total: number; pages: number }>()
      const current = deferred<{ items: Array<{ id: number }>; total: number; pages: number }>()
      listErrorLogs
        .mockImplementationOnce(() => stale.promise)
        .mockImplementationOnce(() => current.promise)

      const wrapper = mount(UsageView, {
        global: { stubs: {
          AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
          UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
          UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
          DateRangePicker: true, Icon: true, TokenUsageTrend: true,
          ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
          UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
        } },
      })
      vi.advanceTimersByTime(120)
      await flushPromises()

      const vm = wrapper.vm as any
      vm.activeTab = 'errors'
      void vm.loadAdminErrors()
      vm.filters.exclude_user_ids = [8]
      void vm.loadAdminErrors()
      await flushPromises()

      current.resolve({ items: [{ id: 22 }], total: 1, pages: 1 })
      await flushPromises()
      expect(vm.errRows).toEqual([{ id: 22 }])
      expect(vm.errTotal).toBe(1)
      expect(vm.errLoading).toBe(false)

      if (staleOutcome === 'resolve') {
        stale.resolve({ items: [{ id: 11 }], total: 99, pages: 1 })
      } else {
        stale.reject(new Error('stale request failed'))
      }
      await flushPromises()

      expect(vm.errRows).toEqual([{ id: 22 }])
      expect(vm.errTotal).toBe(1)
      expect(vm.errLoading).toBe(false)
      expect(showError).not.toHaveBeenCalled()
    },
  )

  it('keeps error loading active when only a stale request has settled', async () => {
    const stale = deferred<{ items: Array<{ id: number }>; total: number; pages: number }>()
    const current = deferred<{ items: Array<{ id: number }>; total: number; pages: number }>()
    listErrorLogs
      .mockImplementationOnce(() => stale.promise)
      .mockImplementationOnce(() => current.promise)

    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    const vm = wrapper.vm as any
    void vm.loadAdminErrors()
    vm.filters.exclude_user_ids = [8]
    void vm.loadAdminErrors()
    stale.resolve({ items: [{ id: 11 }], total: 1, pages: 1 })
    await flushPromises()

    expect(vm.errLoading).toBe(true)
    expect(vm.errRows).toEqual([])

    current.resolve({ items: [{ id: 22 }], total: 1, pages: 1 })
    await flushPromises()
    expect(vm.errLoading).toBe(false)
    expect(vm.errRows).toEqual([{ id: 22 }])
  })
})

describe('admin UsageView ranking tab', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('mounts ranking lazily and drill-down sets user filter then jumps back to usage tab', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: UserTokenRankingStub, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // 懒挂载:切到排行 tab 前不渲染
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(false)

    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    expect(tabs).toHaveLength(3)
    await tabs[2].trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(true)

    // 下钻:设置 user_id、解决与排除列表的冲突、切回用量明细 tab 并按新筛选重新拉取列表
    ;(wrapper.vm as any).filters.exclude_user_ids = [5, 8]
    list.mockClear()
    await wrapper.find('[data-test="ranking"] .pick-user').trigger('click')
    await flushPromises()

    expect((wrapper.vm as any).activeTab).toBe('usage')
    expect((wrapper.vm as any).filters.user_id).toBe(5)
    expect((wrapper.vm as any).filters.exclude_user_ids).toEqual([8])
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 5 }), expect.anything())
  })
})

describe('admin UsageView excluded-user propagation', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
    listErrorLogs.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    adminUsageList.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    saveAs.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('applies exclusions to every page request and the export list builder', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    list.mockClear()
    getStats.mockClear()
    getModelStats.mockClear()
    getSnapshotV2.mockClear()
    listErrorLogs.mockClear()

    const vm = wrapper.vm as any
    vm.filters.exclude_user_ids = [8, 3]
    expect(vm.breakdownFilters).toEqual(expect.objectContaining({ exclude_user_ids: [8, 3] }))
    vm.activeTab = 'errors'
    vm.applyFilters()
    await flushPromises()

    const excluded = { exclude_user_ids: [8, 3] }
    expect(list).toHaveBeenCalledWith(expect.objectContaining(excluded), expect.anything())
    expect(getStats).toHaveBeenCalledWith(expect.objectContaining(excluded))
    expect(getModelStats).toHaveBeenCalledWith(expect.objectContaining(excluded))
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining(excluded))
    expect(listErrorLogs).toHaveBeenCalledWith(expect.objectContaining(excluded))

    await vm.exportToExcel()
    expect(adminUsageList).toHaveBeenCalledWith(
      expect.objectContaining({ ...excluded, page: 1, page_size: 100, exact_total: true }),
      expect.anything(),
    )
  })

  it('uses one immutable filter and sort snapshot for every Excel export page', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    const vm = wrapper.vm as any
    Object.assign(vm.filters, {
      start_date: '2026-07-01',
      end_date: '2026-07-02',
      user_id: 9,
      exclude_user_ids: [8, 3],
      api_key_id: 7,
      account_id: 6,
      group_id: 5,
      model: 'gpt-original',
      request_type: 'stream',
      billing_type: 1,
      billing_mode: 'token',
    })
    vm.sortState.sort_by = 'model'
    vm.sortState.sort_order = 'asc'

    const log = {
      id: 1,
      created_at: '2026-07-01T00:00:00Z',
      model: 'gpt-original',
      input_tokens: 1,
      output_tokens: 2,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      total_cost: 0,
      actual_cost: 0,
      duration_ms: 1,
    }
    adminUsageList.mockImplementation(async (params: { page?: number }) => {
      if (params.page === 1) {
        vm.filters.exclude_user_ids = [99]
        vm.filters.user_id = 100
        vm.filters.model = 'gpt-mutated'
        vm.filters.request_type = 'sync'
        vm.filters.billing_type = 0
        vm.filters.billing_mode = 'per_request'
        vm.sortState.sort_by = 'created_at'
        vm.sortState.sort_order = 'desc'
        return { items: Array.from({ length: 100 }, () => log), total: 101, pages: 2 }
      }
      return { items: [log], total: 101, pages: 2 }
    })

    await vm.exportToExcel()

    expect(adminUsageList).toHaveBeenCalledTimes(2)
    for (const [params] of adminUsageList.mock.calls) {
      expect(params).toEqual(expect.objectContaining({
        exclude_user_ids: [8, 3],
        user_id: 9,
        api_key_id: 7,
        account_id: 6,
        group_id: 5,
        model: 'gpt-original',
        request_type: 'stream',
        stream: true,
        billing_type: 1,
        billing_mode: 'token',
        sort_by: 'model',
        sort_order: 'asc',
        page_size: 100,
        exact_total: true,
      }))
    }
  })

  it('reset clears exclusions before reloading page requests', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    const vm = wrapper.vm as any
    vm.filters.exclude_user_ids = [8, 3]
    list.mockClear()
    vm.resetFilters()
    await flushPromises()

    expect(vm.filters.exclude_user_ids).toBeUndefined()
    expect(list).toHaveBeenCalledWith(
      expect.not.objectContaining({ exclude_user_ids: expect.anything() }),
      expect.anything(),
    )
  })
})
