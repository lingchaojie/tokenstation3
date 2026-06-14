/// <reference path="../../../vite-env.d.ts" />

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, type PropType } from 'vue'

import RedeemView from '../RedeemView.vue'

const {
  listRedeemCodes,
  generateRedeemCodes,
  getAllGroups,
  getPlans,
  showSuccess,
  showError,
  showInfo,
} = vi.hoisted(() => ({
  listRedeemCodes: vi.fn(),
  generateRedeemCodes: vi.fn(),
  getAllGroups: vi.fn(),
  getPlans: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    redeem: {
      list: listRedeemCodes,
      generate: generateRedeemCodes,
      delete: vi.fn(),
      batchDelete: vi.fn(),
      batchUpdate: vi.fn(),
      exportCodes: vi.fn(),
    },
    groups: {
      getAll: getAllGroups,
    },
    payment: {
      getPlans,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
    showInfo,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.redeem.expiryPresetDays') return `${params?.days} days`
        if (key === 'admin.redeem.planFallback') return `Plan #${params?.id}`
        return key
      },
    }),
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <table>
      <tbody>
        <tr v-for="row in data" :key="row.id">
          <td v-for="column in columns" :key="column.key">
            <slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">
              {{ row[column.key] }}
            </slot>
          </td>
        </tr>
      </tbody>
    </table>
  `,
}

type SelectOption = { value: unknown; label: string }

const SelectStub = defineComponent({
  props: {
    modelValue: null,
    options: {
      type: Array as PropType<SelectOption[]>,
      default: () => [],
    },
  },
  emits: ['update:modelValue', 'change'],
  setup(props, { emit }) {
    const onChange = (event: Event) => {
      const raw = (event.target as HTMLSelectElement).value
      const option = props.options.find((item) => String(item.value ?? '') === raw)
      const value = option ? option.value : raw
      emit('update:modelValue', value)
      emit('change', value, option ?? null)
    }
    return { onChange }
  },
  template: `
    <select v-bind="$attrs" :value="modelValue ?? ''" @change="onChange">
      <option v-for="option in options" :key="String(option.value ?? '')" :value="option.value ?? ''">
        {{ option.label }}
      </option>
    </select>
  `,
})

function mountView() {
  return mount(RedeemView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        DataTable: DataTableStub,
        Pagination: true,
        ConfirmDialog: true,
        Select: SelectStub,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        Teleport: true,
      },
    },
  })
}

describe('admin RedeemView subscription generation modes', () => {
  beforeEach(() => {
    localStorage.clear()
    document.body.innerHTML = ''

    listRedeemCodes.mockReset()
    generateRedeemCodes.mockReset()
    getAllGroups.mockReset()
    getPlans.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    showInfo.mockReset()

    listRedeemCodes.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
      pages: 0,
    })
    generateRedeemCodes.mockResolvedValue([])
    getAllGroups.mockResolvedValue([
      {
        id: 7,
        name: 'Legacy Group',
        description: 'Existing group target',
        platform: 'claude',
        subscription_type: 'subscription',
        rate_multiplier: 1,
      },
    ])
    getPlans.mockResolvedValue({
      data: [
        {
          id: 42,
          name: 'Pro Monthly',
          product_name: 'Claude Pro',
          description: 'Operations plan',
          price: 20,
          validity_days: 30,
          validity_unit: 'days',
          features: [],
          for_sale: false,
          sort_order: 1,
          seven_day_quota_usd: 10,
        },
      ],
    })
  })

  it('defaults subscription generation to plan mode and sends plan_id without stale group_id', async () => {
    const wrapper = mountView()

    await flushPromises()
    await wrapper.find('button.btn-primary').trigger('click')
    await wrapper.get('[data-test="generate-type-select"]').setValue('subscription')
    await flushPromises()

    expect((wrapper.get('[data-test="subscription-mode-select"]').element as HTMLSelectElement).value).toBe('plan')
    await wrapper.get('[data-test="subscription-plan-select"]').setValue('42')
    await wrapper.get('[data-test="generate-count-input"]').setValue('2')
    await wrapper.get('[data-test="generate-form"]').trigger('submit')
    await flushPromises()

    expect(generateRedeemCodes).toHaveBeenCalledWith(2, 'subscription', 10, {
      planId: 42,
      validityDays: undefined,
      expiresInDays: undefined,
    })
    expect(showError).not.toHaveBeenCalled()
  })

  it('allows explicit zero validity days in plan mode to use the plan default', async () => {
    const wrapper = mountView()

    await flushPromises()
    await wrapper.find('button.btn-primary').trigger('click')
    await wrapper.get('[data-test="generate-type-select"]').setValue('subscription')
    await flushPromises()

    await wrapper.get('[data-test="subscription-plan-select"]').setValue('42')
    await wrapper.get('[data-test="subscription-plan-validity-days-input"]').setValue('0')
    await wrapper.get('[data-test="generate-form"]').trigger('submit')
    await flushPromises()

    expect(generateRedeemCodes).toHaveBeenCalledWith(1, 'subscription', 10, {
      planId: 42,
      validityDays: 0,
      expiresInDays: undefined,
    })
    expect(showError).not.toHaveBeenCalled()
  })

  it('preserves negative validity days for legacy subscription group mode', async () => {
    const wrapper = mountView()

    await flushPromises()
    await wrapper.find('button.btn-primary').trigger('click')
    await wrapper.get('[data-test="generate-type-select"]').setValue('subscription')
    await flushPromises()

    await wrapper.get('[data-test="subscription-mode-select"]').setValue('group')
    await wrapper.get('[data-test="subscription-group-select"]').setValue('7')
    await wrapper.get('[data-test="subscription-group-validity-days-input"]').setValue('-1')
    await wrapper.get('[data-test="generate-form"]').trigger('submit')
    await flushPromises()

    expect(generateRedeemCodes).toHaveBeenCalledWith(
      1,
      'subscription',
      10,
      7,
      -1,
      undefined
    )
    expect(showError).not.toHaveBeenCalled()
  })

  it('shows plan target text for plan-backed subscription codes and group text for legacy codes', async () => {
    listRedeemCodes.mockResolvedValue({
      items: [
        {
          id: 1,
          code: 'PLAN-CODE',
          type: 'subscription',
          value: 0,
          validity_days: 0,
          plan_id: 42,
          plan: {
            id: 42,
            name: 'Pro Monthly',
            product_name: 'Claude Pro',
            validity_days: 30,
            validity_unit: 'days',
            seven_day_quota_usd: 10,
            for_sale: false,
          },
          status: 'unused',
          used_by: null,
          used_at: null,
          created_at: '2026-01-01T00:00:00Z',
          expires_at: null,
        },
        {
          id: 2,
          code: 'MISSING-PLAN',
          type: 'subscription',
          value: 0,
          validity_days: 15,
          plan_id: 99,
          status: 'unused',
          used_by: null,
          used_at: null,
          created_at: '2026-01-01T00:00:00Z',
          expires_at: null,
        },
        {
          id: 3,
          code: 'GROUP-CODE',
          type: 'subscription',
          value: 0,
          validity_days: -1,
          group: { name: 'Legacy Group' },
          status: 'unused',
          used_by: null,
          used_at: null,
          created_at: '2026-01-01T00:00:00Z',
          expires_at: null,
        },
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Claude Pro / Pro Monthly')
    expect(wrapper.text()).toContain('admin.redeem.planDefaultDuration')
    expect(wrapper.text()).toContain('Plan #99')
    expect(wrapper.text()).toContain('15 admin.redeem.days')
    expect(wrapper.text()).toContain('Legacy Group')
    expect(wrapper.text()).toContain('-1 admin.redeem.days')
  })
})
