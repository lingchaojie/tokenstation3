import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminCheckInConfigView from '../AdminCheckInConfigView.vue'

const { getConfig, showError, showSuccess, updateConfig } = vi.hoisted(() => ({
  getConfig: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  updateConfig: vi.fn(),
}))

vi.mock('@/api/admin/checkIn', () => ({
  adminCheckInAPI: { getConfig, updateConfig },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'zh-CN' },
      t: (key: string) => key,
    }),
  }
})

function configFixture() {
  return {
    enabled: false,
    start_at: '2026-07-15T00:30:00Z',
    duration_days: 7,
    reward_amount: 10,
    end_at: '2026-07-22T00:30:00Z',
    state: 'disabled' as const,
  }
}

function mountView() {
  return mount(AdminCheckInConfigView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
      },
    },
  })
}

describe('AdminCheckInConfigView', () => {
  beforeEach(() => {
    getConfig.mockReset().mockResolvedValue(configFixture())
    updateConfig.mockReset().mockImplementation(async input => ({
      ...configFixture(),
      ...input,
      end_at: '2026-07-20T16:00:00Z',
      state: input.enabled ? 'upcoming' : 'disabled',
    }))
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('loads the existing UTC+8 activity configuration', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get<HTMLInputElement>('[data-testid="check-in-start-at"]').element.value).toBe(
      '2026-07-15T08:30',
    )
    expect(wrapper.get<HTMLInputElement>('[data-testid="check-in-duration-days"]').element.value).toBe('7')
    expect(wrapper.get<HTMLInputElement>('[data-testid="check-in-reward-amount"]').element.value).toBe('10')
    expect(wrapper.get('[data-testid="check-in-end-preview"]').text()).not.toContain('—')
  })

  it('converts the UTC+8 start time and saves all activity settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="check-in-enabled"]').trigger('click')
    await wrapper.get('[data-testid="check-in-start-at"]').setValue('2026-07-16T00:00')
    await wrapper.get('[data-testid="check-in-duration-days"]').setValue('5')
    await wrapper.get('[data-testid="check-in-reward-amount"]').setValue('12.34567891')
    await wrapper.get('[data-testid="check-in-save"]').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith({
      enabled: true,
      start_at: '2026-07-15T16:00:00.000Z',
      duration_days: 5,
      reward_amount: 12.34567891,
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.affiliates.checkIn.saveSuccess')
  })

  it('rejects rewards with more than eight decimal places before saving', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="check-in-reward-amount"]').setValue('1.123456789')
    await wrapper.get('[data-testid="check-in-save"]').trigger('click')

    expect(updateConfig).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.affiliates.checkIn.validation.reward')
  })
})
