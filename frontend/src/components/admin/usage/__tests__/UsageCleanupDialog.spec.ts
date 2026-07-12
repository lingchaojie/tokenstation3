import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UsageCleanupDialog from '../UsageCleanupDialog.vue'

const { listCleanupTasks, createCleanupTask, cancelCleanupTask } = vi.hoisted(() => ({
  listCleanupTasks: vi.fn(),
  createCleanupTask: vi.fn(),
  cancelCleanupTask: vi.fn(),
}))

vi.mock('@/api/admin/usage', () => ({
  default: {},
  adminUsageAPI: {
    listCleanupTasks,
    createCleanupTask,
    cancelCleanupTask,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const UsageFiltersStub = {
  name: 'UsageFilters',
  props: ['modelValue', 'startDate', 'endDate', 'showExcludedUsers'],
  emits: ['update:modelValue', 'update:startDate', 'update:endDate', 'change'],
  template: '<div data-testid="cleanup-filters" />',
}

describe('UsageCleanupDialog excluded-user safety', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    listCleanupTasks.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 })
    createCleanupTask.mockReset().mockResolvedValue({})
    cancelCleanupTask.mockReset().mockResolvedValue({ id: 1, status: 'canceled' })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides exclusions, omits them from nested state, and never submits them', async () => {
    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: false,
        filters: { user_id: 4, exclude_user_ids: [8, 3] },
        startDate: '2026-07-01',
        endDate: '2026-07-08',
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          ConfirmDialog: true,
          Pagination: true,
          UsageFilters: UsageFiltersStub,
        },
      },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const nestedFilters = wrapper.getComponent(UsageFiltersStub)
    expect(nestedFilters.props('showExcludedUsers')).toBe(false)
    expect(nestedFilters.props('modelValue')).not.toHaveProperty('exclude_user_ids')

    await (wrapper.vm as any).submitCleanup()
    await flushPromises()

    expect(createCleanupTask).toHaveBeenCalledWith(expect.objectContaining({
      user_id: 4,
      start_date: '2026-07-01',
      end_date: '2026-07-08',
    }))
    expect(createCleanupTask.mock.calls[0][0]).not.toHaveProperty('exclude_user_ids')

    wrapper.unmount()
  })
})
