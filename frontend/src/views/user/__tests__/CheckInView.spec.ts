import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { DailyCheckInStatus } from '@/api/checkIn'
import CheckInView from '../CheckInView.vue'

const { claim, getStatus, refreshUser, showError, showSuccess } = vi.hoisted(() => ({
  claim: vi.fn(),
  getStatus: vi.fn(),
  refreshUser: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/checkIn', () => ({
  checkInAPI: { claim, getStatus },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess }),
  useAuthStore: () => ({ refreshUser }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'zh-CN' },
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

function statusFixture(overrides: Partial<DailyCheckInStatus> = {}): DailyCheckInStatus {
  return {
    state: 'active',
    active: true,
    start_at: '2026-07-15T00:00:00Z',
    end_at: '2026-07-25T00:00:00Z',
    reward_amount: 10,
    check_in_date: '2026-07-15',
    claimed_today: false,
    next_reset_at: '2099-07-16T00:00:00Z',
    ...overrides,
  }
}

function mountView() {
  return mount(CheckInView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
      },
    },
  })
}

describe('CheckInView', () => {
  beforeEach(() => {
    getStatus.mockReset().mockResolvedValue(statusFixture())
    claim.mockReset().mockResolvedValue({
      reward_amount: 10,
      balance_after: 35,
      check_in_date: '2026-07-15',
      claimed_at: '2026-07-15T04:00:00Z',
    })
    refreshUser.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('claims the active daily reward, refreshes the balance, and switches to claimed state', async () => {
    const wrapper = mountView()
    await flushPromises()

    const button = wrapper.get('[data-testid="check-in-claim"]')
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()

    expect(claim).toHaveBeenCalledOnce()
    expect(refreshUser).toHaveBeenCalledOnce()
    expect(showSuccess).toHaveBeenCalledWith('checkIn.claimSuccess:{"amount":"$10.00"}')
    expect(wrapper.get('[data-testid="check-in-claimed"]').exists()).toBe(true)
  })

  it('keeps direct access read-only when the activity is inactive', async () => {
    getStatus.mockResolvedValue(statusFixture({ state: 'upcoming', active: false }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="check-in-claim"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('checkIn.states.upcoming')
  })

  it('reloads status after a duplicate claim race', async () => {
    getStatus
      .mockResolvedValueOnce(statusFixture())
      .mockResolvedValueOnce(statusFixture({ claimed_today: true }))
    claim.mockRejectedValue({ reason: 'DAILY_CHECK_IN_ALREADY_CLAIMED' })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="check-in-claim"]').trigger('click')
    await flushPromises()

    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="check-in-claimed"]').exists()).toBe(true)
    expect(showError).not.toHaveBeenCalled()
  })
})
