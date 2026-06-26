import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import UserDashboardRecentUsage from './UserDashboardRecentUsage.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'dashboard.recentUsage': 'Recent Usage',
      'dashboard.last7Days': 'Last 7 days',
      'dashboard.actual': 'Actual',
      'dashboard.standard': 'Standard',
      'dashboard.viewAllUsage': 'View all usage',
    }[key] ?? key),
  }),
}))

vi.mock('@/components/common/LoadingSpinner.vue', () => ({
  default: { template: '<div class="spinner" />' },
}))

vi.mock('@/components/common/EmptyState.vue', () => ({
  default: { template: '<div class="empty" />' },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
}))

vi.mock('@/utils/format', () => ({
  formatDateTime: (value: string) => value,
}))

describe('UserDashboardRecentUsage', () => {
  const row = {
    id: 1,
    user_id: 1,
    api_key_id: 1,
    account_id: null,
    request_id: 'req_1',
    model: 'gpt-5.4',
    group_id: null,
    subscription_id: null,
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    cache_creation_5m_tokens: 0,
    cache_creation_1h_tokens: 0,
    input_cost: 0.1,
    output_cost: 0.2,
    cache_creation_cost: 0,
    cache_read_cost: 0,
    total_cost: 0.3,
    actual_cost: 0.45,
    rate_multiplier: 1.5,
    billing_type: 1,
    stream: false,
    duration_ms: 100,
    first_token_ms: null,
    image_count: 0,
    image_size: null,
    image_input_size: null,
    image_output_size: null,
    image_size_source: null,
    image_size_breakdown: null,
    image_output_tokens: 0,
    image_output_cost: 0,
    user_agent: null,
    cache_ttl_overridden: false,
    created_at: '2026-03-08T00:00:00Z',
  }

  it('shows only the billed actual cost by default', () => {
    const wrapper = mount(UserDashboardRecentUsage, {
      props: { data: [row], loading: false },
      global: {
        stubs: {
          RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
        },
      },
    })

    expect(wrapper.text()).toContain('$0.4500')
    expect(wrapper.text()).not.toContain('$0.3000')
    expect(wrapper.find('[title="Standard"]').exists()).toBe(false)
  })
})
