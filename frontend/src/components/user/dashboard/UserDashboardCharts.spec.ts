import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import UserDashboardCharts from './UserDashboardCharts.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'common.refresh': 'Refresh',
      'dashboard.timeRange': 'Time range',
      'dashboard.granularity': 'Granularity',
      'dashboard.day': 'Day',
      'dashboard.hour': 'Hour',
      'dashboard.modelDistribution': 'Model Distribution',
      'dashboard.model': 'Model',
      'dashboard.requests': 'Requests',
      'dashboard.tokens': 'Tokens',
      'dashboard.actual': 'Actual',
      'dashboard.standard': 'Standard',
      'dashboard.noDataAvailable': 'No data available',
    }[key] ?? key),
  }),
}))

vi.mock('vue-chartjs', () => ({
  Doughnut: { template: '<canvas data-testid="doughnut" />' },
}))

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  CategoryScale: {},
  LinearScale: {},
  PointElement: {},
  LineElement: {},
  ArcElement: {},
  Title: {},
  Tooltip: {},
  Legend: {},
  Filler: {},
}))

vi.mock('@/components/common/LoadingSpinner.vue', () => ({
  default: { template: '<div class="spinner" />' },
}))

vi.mock('@/components/common/DateRangePicker.vue', () => ({
  default: { template: '<div class="date-range" />' },
}))

vi.mock('@/components/charts/TokenUsageTrend.vue', () => ({
  default: {
    props: ['showStandardCosts'],
    template: '<section data-testid="trend" :data-show-standard-costs="String(showStandardCosts)" />',
  },
}))

vi.mock('@/utils/format', () => ({
  formatCostFixed: (value: number) => value.toFixed(4),
  formatNumberLocaleString: (value: number) => value.toLocaleString(),
  formatTokensK: (value: number) => value.toString(),
}))

describe('UserDashboardCharts', () => {
  const model = {
    model: 'gpt-5.4',
    requests: 2,
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    total_tokens: 30,
    cost: 0.3,
    actual_cost: 0.45,
    account_cost: 0.2,
  }

  it('shows only actual model cost by default', () => {
    const wrapper = mount(UserDashboardCharts, {
      props: {
        loading: false,
        startDate: '2026-03-01',
        endDate: '2026-03-08',
        granularity: 'day',
        trend: [],
        models: [model],
      },
    })

    expect(wrapper.text()).toContain('$0.4500')
    expect(wrapper.text()).not.toContain('$0.3000')
    expect(wrapper.text()).not.toContain('Standard')
    expect(wrapper.get('[data-testid="trend"]').attributes('data-show-standard-costs')).toBe('false')
  })

  it('keeps standard model cost available when explicitly enabled', () => {
    const wrapper = mount(UserDashboardCharts, {
      props: {
        loading: false,
        startDate: '2026-03-01',
        endDate: '2026-03-08',
        granularity: 'day',
        trend: [],
        models: [model],
        showStandardCosts: true,
      },
    })

    expect(wrapper.text()).toContain('$0.4500')
    expect(wrapper.text()).toContain('$0.3000')
    expect(wrapper.text()).toContain('Standard')
    expect(wrapper.get('[data-testid="trend"]').attributes('data-show-standard-costs')).toBe('true')
  })
})
