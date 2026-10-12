import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import UsageCleanupDialog from '../UsageCleanupDialog.vue'
import Pagination from '@/components/common/Pagination.vue'

enableAutoUnmount(afterEach)

const { listCleanupTasks, createCleanupTask, cancelCleanupTask, showError } = vi.hoisted(() => ({
  listCleanupTasks: vi.fn(),
  createCleanupTask: vi.fn(),
  cancelCleanupTask: vi.fn(),
  showError: vi.fn(),
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
    showError,
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


beforeEach(() => {
  listCleanupTasks.mockReset()
  showError.mockReset()
  vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => { vi.restoreAllMocks() })
const page = (number: number) => ({ items: [{ id: number, status: 'succeeded', deleted_rows: number, filters: {} }], total: 15, page: number, page_size: 5 })
async function openDialog() {
  listCleanupTasks.mockResolvedValueOnce(page(1))
  const wrapper = mount(UsageCleanupDialog, {
    props: { show: false, filters: {}, startDate: '2026-09-01', endDate: '2026-09-02' },
    global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, UsageFilters: UsageFiltersStub, ConfirmDialog: true, Pagination: true } },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('cleanup task pagination', () => {
  it.each(['success', 'failure'])('ignores an older page request that completes with %s', async (outcome) => {
    const wrapper = await openDialog()
    let resolve!: (value: object) => void
    let reject!: (error: Error) => void
    listCleanupTasks.mockImplementationOnce(() => new Promise((res, rej) => { resolve = res; reject = rej }))
    wrapper.findComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    listCleanupTasks.mockResolvedValueOnce(page(3))
    wrapper.findComponent(Pagination).vm.$emit('update:page', 3)
    await flushPromises()
    if (outcome === 'success') resolve(page(2))
    else reject(new Error('old failure'))
    await flushPromises()
    expect(wrapper.findComponent(Pagination).props('page')).toBe(3)
    expect(wrapper.text()).toContain('#3')
    expect(showError).not.toHaveBeenCalled()
  })

  it('ignores a pending result after the dialog is closed and reopened', async () => {
    const wrapper = await openDialog()
    let resolve!: (value: object) => void
    listCleanupTasks.mockImplementationOnce(() => new Promise(res => { resolve = res }))
    wrapper.findComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    await wrapper.setProps({ show: false })
    listCleanupTasks.mockResolvedValueOnce(page(1))
    await wrapper.setProps({ show: true })
    await flushPromises()
    resolve(page(2))
    await flushPromises()
    expect(wrapper.findComponent(Pagination).props('page')).toBe(1)
    expect(wrapper.text()).toContain('#1')
  })
})
