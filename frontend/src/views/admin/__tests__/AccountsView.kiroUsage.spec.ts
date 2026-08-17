import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const { listAccounts, listWithEtag, getBatchTodayStats, getAllProxies, getAllGroups } =
  vi.hoisted(() => ({
    listAccounts: vi.fn(),
    listWithEtag: vi.fn(),
    getBatchTodayStats: vi.fn(),
    getAllProxies: vi.fn(),
    getAllGroups: vi.fn()
  }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings: vi
        .fn()
        .mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ token: 'test-token' }) }))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

const AccountUsageCellStub = {
  props: ['account', 'requestBatchedUsage'],
  template: `
    <div :data-testid="account.platform + '-batch-managed'">
      {{ typeof requestBatchedUsage === 'function' }}
    </div>
  `
}

const baseAccount = {
  status: 'active',
  schedulable: true,
  concurrency: 1,
  priority: 0,
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
}

describe('AccountsView KIRO usage loading', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockReturnValue({
        matches: true,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn()
      })
    )
    listAccounts.mockResolvedValue({
      items: [
        { ...baseAccount, id: 1, name: 'kiro', platform: 'kiro', type: 'oauth' },
        { ...baseAccount, id: 2, name: 'anthropic', platform: 'anthropic', type: 'oauth' }
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  it('only gives the batching callback to accounts handled by the batch loader', async () => {
    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template:
              '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          AccountUsageCell: AccountUsageCellStub,
          AccountTableActions: { template: '<div><slot /></div>' },
          AccountTableFilters: true,
          AccountBulkActionsBar: true,
          AccountActionMenu: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          PlatformTypeBadge: true,
          HelpTooltip: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: true,
          Icon: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="kiro-batch-managed"]').text()).toBe('false')
    expect(wrapper.get('[data-testid="anthropic-batch-managed"]').text()).toBe('true')
  })
})
