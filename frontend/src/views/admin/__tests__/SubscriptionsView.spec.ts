import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { UserSubscription } from '@/types'
import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubscriptions, getAllGroups } = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: {
      list: listSubscriptions,
      assign: vi.fn(),
      extend: vi.fn(),
      revoke: vi.fn(),
      resetQuota: vi.fn()
    },
    groups: {
      getAll: getAllGroups
    },
    usage: {
      searchUsers: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const createSubscription = (): UserSubscription => ({
  id: 7,
  user_id: 42,
  group_id: 3,
  plan_id: null,
  plan_name: null,
  status: 'active',
  starts_at: '2026-06-01T00:00:00Z',
  expires_at: '2026-07-01T00:00:00Z',
  daily_usage_usd: 0,
  weekly_usage_usd: 0,
  monthly_usage_usd: 0,
  seven_day_limit_usd: 50,
  seven_day_usage_usd: 12.5,
  seven_day_remaining_usd: 37.5,
  seven_day_reset_at: '2099-06-08T00:00:00Z',
  daily_window_start: null,
  weekly_window_start: null,
  monthly_window_start: null,
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T00:00:00Z',
  user: {
    id: 42,
    username: 'quota-user',
    email: 'quota@example.com',
    role: 'user',
    balance: 0,
    concurrency: 1,
    status: 'active',
    allowed_groups: [],
    balance_notify_enabled: false,
    balance_notify_threshold: null,
    balance_notify_extra_emails: [],
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z'
  },
  group: {
    id: 3,
    name: 'Monthly Card',
    description: null,
    platform: 'openai',
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'subscription',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z'
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="subscription-row">
        <slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

describe('admin SubscriptionsView', () => {
  beforeEach(() => {
    localStorage.clear()
    listSubscriptions.mockReset()
    getAllGroups.mockReset()

    listSubscriptions.mockResolvedValue({
      items: [createSubscription()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getAllGroups.mockResolvedValue([])
  })

  it('shows seven-day quota instead of unlimited when only seven-day limit is set', async () => {
    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          Select: true,
          GroupBadge: true,
          GroupOptionItem: true,
          Icon: true,
          Teleport: true,
          RouterLink: true
        }
      }
    })

    await flushPromises()

    const usageCell = wrapper.get('[data-test="subscription-row"]')
    expect(usageCell.text()).toContain('admin.subscriptions.sevenDay')
    expect(usageCell.text()).toContain('$12.50 / $50.00')
    expect(usageCell.text()).toContain('admin.subscriptions.remaining')
    expect(usageCell.text()).not.toContain('admin.subscriptions.unlimited')
  })
})
