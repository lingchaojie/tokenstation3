import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import PlanEditDialog from '../PlanEditDialog.vue'
import type { SubscriptionPlan } from '../../../../types/payment'

const createPlan = vi.hoisted(() => vi.fn())
const updatePlan = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'payment.admin.subscriptionCnyPayPreview') return `preview ${params?.amount}`
        if (key === 'payment.admin.subscriptionCnyPayPreviewWithFee') return `fee ${params?.feeRate} ${params?.total}`
        return key
      },
    }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan,
    updatePlan,
  },
}))

function planFixture(overrides: Partial<SubscriptionPlan> = {}): SubscriptionPlan {
  return {
    id: 1,
    name: 'Plus monthly',
    description: 'Everyday development',
    price: 399,
    original_price: 0,
    seven_day_quota_usd: 110,
    validity_days: 30,
    validity_unit: 'day',
    features: ['Seven-day quota'],
    for_sale: true,
    sort_order: 20,
    ...overrides,
  }
}

function mountDialog(plan: SubscriptionPlan | null) {
  return mount(PlanEditDialog, {
    props: {
      show: false,
      plan,
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Select: {
          props: ['modelValue', 'options', 'placeholder'],
          emits: ['update:modelValue'],
          methods: {
            onChange(event: Event) {
              this.$emit('update:modelValue', (event.target as HTMLSelectElement).value)
            },
          },
          template: '<select :value="modelValue == null ? \'\' : modelValue" @change="onChange"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>',
        },
        Icon: true,
      },
    },
  })
}

// mountCnyDialog mounts the dialog for the subscription CNY preview suite. Plans
// are decoupled from groups in this fork, so no `groups` prop is passed.
function mountCnyDialog(paymentConfig: Record<string, unknown> | null) {
  return mount(PlanEditDialog, {
    props: {
      show: true,
      plan: null,
      paymentConfig,
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Select: true,
        Icon: true,
      },
    },
  })
}

async function fillRequiredCreateFields(wrapper: ReturnType<typeof mountDialog>) {
  await wrapper.find('input[type="text"]').setValue('Pro monthly')
  await wrapper.find('textarea').setValue('Primary development')
  const numberInputs = wrapper.findAll('input[type="number"]')
  await numberInputs[0].setValue('799')
  await numberInputs[2].setValue('30')
}
describe('PlanEditDialog', () => {
  beforeEach(() => {
    createPlan.mockReset().mockResolvedValue({})
    updatePlan.mockReset().mockResolvedValue({})
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('submits seven-day quota when creating a payment plan', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await wrapper.find('input[type="text"]').setValue('Pro monthly')
    await wrapper.find('textarea').setValue('Primary development')
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('799')
    await numberInputs[2].setValue('30')

    const quota = wrapper.find('[data-testid="plan-seven-day-quota"]')
    expect(quota.exists()).toBe(true)
    await quota.setValue('260')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const payload = createPlan.mock.calls[0][0]
    expect(payload).toEqual(expect.objectContaining({
      seven_day_quota_usd: 260,
    }))
    expect(payload).not.toHaveProperty('group_id')
  })

  it('submits backend-compatible singular validity unit values', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await wrapper.find('input[type="text"]').setValue('Weekly plan')
    await wrapper.find('textarea').setValue('Weekly quota')
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('99')
    await numberInputs[2].setValue('1')
    await wrapper.find('select').setValue('week')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(createPlan.mock.calls[0][0]).toEqual(expect.objectContaining({
      validity_days: 1,
      validity_unit: 'week',
    }))
  })

  it('normalizes legacy plural validity units when updating an existing plan', async () => {
    const wrapper = mountDialog(planFixture({ validity_days: 1, validity_unit: 'months' }))
    await wrapper.setProps({ show: true })
    await nextTick()

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(updatePlan).toHaveBeenCalledWith(1, expect.objectContaining({
      validity_days: 1,
      validity_unit: 'month',
    }))
  })
  it('submits clear flag when an existing seven-day quota is cleared', async () => {
    const wrapper = mountDialog(planFixture({ seven_day_quota_usd: 110 }))
    await wrapper.setProps({ show: true })
    await nextTick()

    const quota = wrapper.find('[data-testid="plan-seven-day-quota"]')
    expect((quota.element as HTMLInputElement).value).toBe('110')
    await quota.setValue('')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const payload = updatePlan.mock.calls[0][1]
    expect(updatePlan).toHaveBeenCalledWith(1, expect.objectContaining({
      seven_day_quota_usd: null,
      clear_seven_day_quota_usd: true,
    }))
    expect(payload).not.toHaveProperty('group_id')
  })

  it('submits virtual seat range without direct seat_limit when creating a limited plan', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await fillRequiredCreateFields(wrapper)
    await wrapper.find('[data-testid="plan-virtual-seat-start"]').setValue('4900')
    await wrapper.find('[data-testid="plan-virtual-seat-total"]').setValue('5000')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const payload = createPlan.mock.calls[0][0]
    expect(payload).toEqual(expect.objectContaining({
      virtual_seat_start: 4900,
      virtual_seat_total: 5000,
    }))
    expect(payload).not.toHaveProperty('seat_limit')
  })

  it('shows the derived real seat count from the virtual seat range', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await wrapper.find('[data-testid="plan-virtual-seat-start"]').setValue('4900')
    await wrapper.find('[data-testid="plan-virtual-seat-total"]').setValue('5000')

    expect(wrapper.text()).toContain('payment.admin.derivedSeatLimit: 100')
  })

  it('blocks submit when only one virtual seat range value is filled', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await fillRequiredCreateFields(wrapper)
    await wrapper.find('[data-testid="plan-virtual-seat-start"]').setValue('4900')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(createPlan).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('payment.admin.virtualSeatRangeRequired')
  })

  it('blocks submit when the virtual seat range is inverted', async () => {
    const wrapper = mountDialog(null)
    await wrapper.setProps({ show: true })
    await nextTick()

    await fillRequiredCreateFields(wrapper)
    await wrapper.find('[data-testid="plan-virtual-seat-start"]').setValue('5000')
    await wrapper.find('[data-testid="plan-virtual-seat-total"]').setValue('4900')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(createPlan).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('payment.admin.virtualSeatRangeInvalid')
  })
  it('prefills virtual seat range values when editing an existing plan', async () => {
    const wrapper = mountDialog(planFixture({ virtual_seat_start: 4900, virtual_seat_total: 5000 }))
    await wrapper.setProps({ show: true })
    await nextTick()

    expect((wrapper.find('[data-testid="plan-virtual-seat-start"]').element as HTMLInputElement).value).toBe('4900')
    expect((wrapper.find('[data-testid="plan-virtual-seat-total"]').element as HTMLInputElement).value).toBe('5000')
  })

  it('falls back to legacy seat limit when editing a plan without virtual seat values', async () => {
    const wrapper = mountDialog(planFixture({
      seat_limit: 100,
      virtual_seat_start: null,
      virtual_seat_total: null,
    }))
    await wrapper.setProps({ show: true })
    await nextTick()

    expect((wrapper.find('[data-testid="plan-virtual-seat-start"]').element as HTMLInputElement).value).toBe('0')
    expect((wrapper.find('[data-testid="plan-virtual-seat-total"]').element as HTMLInputElement).value).toBe('100')

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const payload = updatePlan.mock.calls[0][1]
    expect(payload).toEqual(expect.objectContaining({
      virtual_seat_start: 0,
      virtual_seat_total: 100,
    }))
    expect(payload).not.toHaveProperty('seat_limit')
  })
})

describe('PlanEditDialog subscription CNY payment preview', () => {
  it('shows CNY channel charge using the configured subscription rate and fee', async () => {
    const wrapper = mountCnyDialog({
      subscription_usd_to_cny_rate: 7.15,
      recharge_fee_rate: 2.5,
    })

    await wrapper.find('input[type="number"]').setValue('9.99')

    expect(wrapper.text()).toContain('preview')
    expect(wrapper.text()).toContain('¥71.43')
    expect(wrapper.text()).toContain('fee 2.5')
    expect(wrapper.text()).toContain('¥73.22')
  })

  it('hides the preview when the subscription rate is not configured', async () => {
    const wrapper = mountCnyDialog({
      subscription_usd_to_cny_rate: 0,
      recharge_fee_rate: 2.5,
    })

    await wrapper.find('input[type="number"]').setValue('9.99')

    expect(wrapper.text()).not.toContain('preview')
    expect(wrapper.text()).not.toContain('¥71.43')
  })
})
