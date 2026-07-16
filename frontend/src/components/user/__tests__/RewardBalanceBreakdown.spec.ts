import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getRewardCredits = vi.hoisted(() => vi.fn())

vi.mock('@/api/user', () => ({
  userAPI: {
    getRewardCredits: (...args: unknown[]) => getRewardCredits(...args),
  },
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      const messages: Record<string, string> = {
        'dashboard.rewardBalance.daily': 'Check-in {amount}, expires {expiresAt}',
        'dashboard.rewardBalance.affiliate': 'Invite rewards {amount}, earliest expiry {expiresAt}',
        'dashboard.rewardBalance.detailCount': '{count} details',
        'dashboard.rewardBalance.detailsAria': 'View invite reward details',
        'dashboard.rewardBalance.dialogTitle': 'Invite reward details',
        'dashboard.rewardBalance.closeAria': 'Close invite reward details',
        'dashboard.rewardBalance.loading': 'Loading reward details…',
        'dashboard.rewardBalance.empty': 'No invite reward details',
        'dashboard.rewardBalance.loadError': 'Could not load reward details',
        'dashboard.rewardBalance.inviterRole': 'Inviter reward',
        'dashboard.rewardBalance.inviteeRole': 'Invitee reward',
        'dashboard.rewardBalance.expiresAt': 'Expires {expiresAt}',
        'dashboard.rewardBalance.pageStatus': 'Page {page} of {pages}',
        'common.retry': 'Retry',
        'common.previousPage': 'Previous',
        'common.nextPage': 'Next',
      }
      let value = messages[key] ?? key
      for (const [name, replacement] of Object.entries(params ?? {})) {
        value = value.replace(`{${name}}`, String(replacement))
      }
      return value
    },
  }),
}))

vi.mock('@/utils/format', () => ({
  formatCurrency: (amount: number) => `$${amount.toFixed(2)}`,
  formatDateTime: (value: string) => `DATE(${value})`,
}))

import RewardBalanceBreakdown from '../RewardBalanceBreakdown.vue'

const summary = {
  daily_check_in: {
    amount: 5,
    expires_at: '2030-01-02T16:00:00Z',
  },
  affiliate: {
    amount: 25,
    earliest_expires_at: '2030-01-08T08:00:00Z',
    credit_count: 3,
  },
}

function mountBreakdown(props = {}) {
  return mount(RewardBalanceBreakdown, {
    props: { summary, ...props },
    global: {
      stubs: { Teleport: true },
    },
  })
}

describe('RewardBalanceBreakdown', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows non-zero check-in and affiliate balances with exact expiries', () => {
    const wrapper = mountBreakdown()

    expect(wrapper.get('[data-testid="reward-balance-daily"]').text()).toContain(
      'Check-in $5.00, expires DATE(2030-01-02T16:00:00Z)',
    )
    expect(wrapper.get('[data-testid="reward-balance-affiliate"]').text()).toContain(
      'Invite rewards $25.00, earliest expiry DATE(2030-01-08T08:00:00Z)',
    )
    expect(wrapper.get('[data-testid="reward-credit-details-trigger"]').text()).toBe('3 details')
    expect(getRewardCredits).not.toHaveBeenCalled()
  })

  it('renders nothing when neither reward balance is positive', () => {
    const wrapper = mountBreakdown({
      summary: {
        daily_check_in: { amount: 0, expires_at: null },
        affiliate: { amount: 0, earliest_expires_at: null, credit_count: 0 },
      },
    })

    expect(wrapper.find('[data-testid="reward-balance-breakdown"]').exists()).toBe(false)
    expect(wrapper.html()).toBe('<!--v-if-->')
  })

  it('loads details lazily and labels inviter and invitee batches', async () => {
    getRewardCredits.mockResolvedValue({
      items: [
        {
          id: 11,
          credit_type: 'affiliate_inviter',
          role_label: 'inviter',
          original_amount: 10,
          remaining_amount: 8,
          granted_at: '2030-01-01T00:00:00Z',
          expires_at: '2030-01-08T00:00:00Z',
        },
        {
          id: 12,
          credit_type: 'affiliate_invitee',
          role_label: 'invitee',
          original_amount: 5,
          remaining_amount: 5,
          granted_at: '2030-01-02T00:00:00Z',
          expires_at: '2030-01-09T00:00:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 10,
      pages: 1,
    })
    const wrapper = mountBreakdown()

    await wrapper.get('[data-testid="reward-credit-details-trigger"]').trigger('click')
    await flushPromises()

    expect(getRewardCredits).toHaveBeenCalledTimes(1)
    expect(getRewardCredits).toHaveBeenCalledWith({
      type: 'affiliate',
      status: 'active',
      page: 1,
      page_size: 10,
    })
    expect(wrapper.get('[role="dialog"]').text()).toContain('Inviter reward')
    expect(wrapper.get('[role="dialog"]').text()).toContain('Invitee reward')
    expect(wrapper.get('[role="dialog"]').text()).toContain('$8.00')
    expect(wrapper.get('[role="dialog"]').text()).toContain('DATE(2030-01-09T00:00:00Z)')
  })

  it('does not start duplicate requests while opening repeatedly', async () => {
    let resolveRequest: (value: unknown) => void = () => {}
    getRewardCredits.mockReturnValue(new Promise(resolve => { resolveRequest = resolve }))
    const wrapper = mountBreakdown()

    await wrapper.get('[data-testid="reward-credit-details-trigger"]').trigger('click')
    await wrapper.get('[data-testid="reward-credit-dialog-close"]').trigger('click')
    await wrapper.get('[data-testid="reward-credit-details-trigger"]').trigger('click')

    expect(getRewardCredits).toHaveBeenCalledTimes(1)

    resolveRequest({ items: [], total: 0, page: 1, page_size: 10, pages: 0 })
    await flushPromises()
  })

  it('recovers from an API failure and supports pagination', async () => {
    getRewardCredits
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValueOnce({
        items: [{
          id: 21,
          credit_type: 'affiliate_inviter',
          role_label: 'inviter',
          original_amount: 10,
          remaining_amount: 10,
          granted_at: '2030-01-01T00:00:00Z',
          expires_at: '2030-01-08T00:00:00Z',
        }],
        total: 11,
        page: 1,
        page_size: 10,
        pages: 2,
      })
      .mockResolvedValueOnce({
        items: [{
          id: 22,
          credit_type: 'affiliate_invitee',
          role_label: 'invitee',
          original_amount: 5,
          remaining_amount: 5,
          granted_at: '2030-01-02T00:00:00Z',
          expires_at: '2030-01-09T00:00:00Z',
        }],
        total: 11,
        page: 2,
        page_size: 10,
        pages: 2,
      })
    const wrapper = mountBreakdown()

    await wrapper.get('[data-testid="reward-credit-details-trigger"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="reward-credit-load-error"]').text()).toContain('Could not load')

    await wrapper.get('[data-testid="reward-credit-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="reward-credit-page-status"]').text()).toBe('Page 1 of 2')

    await wrapper.get('[data-testid="reward-credit-next-page"]').trigger('click')
    await flushPromises()
    expect(getRewardCredits).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))
    expect(wrapper.get('[role="dialog"]').text()).toContain('Invitee reward')
  })

  it('closes the detail dialog with Escape', async () => {
    getRewardCredits.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 10, pages: 0 })
    const wrapper = mountBreakdown()

    await wrapper.get('[data-testid="reward-credit-details-trigger"]').trigger('click')
    await flushPromises()
    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })

    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })
})
