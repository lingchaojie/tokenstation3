import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import MyAccountDashboardView from '../MyAccountDashboardView.vue'

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main data-testid="app-layout"><slot /></main>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardContent.vue', () => ({
  default: {
    props: { showStandardCosts: Boolean },
    template: '<section data-testid="user-dashboard-content" :data-show-standard-costs="String(showStandardCosts)" />',
  },
}))

describe('MyAccountDashboardView', () => {
  it('renders the same shared user dashboard content inside the admin app layout', () => {
    const wrapper = mount(MyAccountDashboardView)

    const layout = wrapper.find('[data-testid="app-layout"]')
    expect(layout.exists()).toBe(true)
    expect(layout.find('[data-testid="user-dashboard-content"]').exists()).toBe(true)
    expect(layout.find('[data-testid="user-dashboard-content"]').attributes('data-show-standard-costs')).toBe('true')
  })
})
