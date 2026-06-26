import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import DashboardView from '../DashboardView.vue'

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main data-testid="app-layout"><slot /></main>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardContent.vue', () => ({
  default: {
    props: ['showStandardCosts'],
    template: '<section data-testid="user-dashboard-content" :data-show-standard-costs="String(showStandardCosts)" />',
  },
}))

describe('DashboardView', () => {
  it('renders the shared user dashboard content inside the app layout', () => {
    const wrapper = mount(DashboardView)

    const layout = wrapper.find('[data-testid="app-layout"]')

    expect(layout.exists()).toBe(true)
    expect(layout.find('[data-testid="user-dashboard-content"]').exists()).toBe(true)
    expect(layout.find('[data-testid="user-dashboard-content"]').attributes('data-show-standard-costs')).toBe('false')
  })
})
