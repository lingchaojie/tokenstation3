import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import PlanEditDialog from '../PlanEditDialog.vue'
import type { SubscriptionPlan } from '@/types/payment'
import type { AdminGroup } from '@/types'

const createPlan = vi.hoisted(() => vi.fn())
const updatePlan = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan,
    updatePlan,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

const groups: AdminGroup[] = [
  {
    id: 2,
    name: 'LINX2 Subscription',
    platform: 'anthropic',
    rate_multiplier: 1,
    subscription_type: 'subscription',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
  } as AdminGroup,
]

function planFixture(overrides: Partial<SubscriptionPlan> = {}): SubscriptionPlan {
  return {
    id: 1,
    group_id: 2,
    group_platform: 'anthropic',
    group_name: 'LINX2 Subscription',
    rate_multiplier: 1,
    name: 'Plus monthly',
    description: 'Everyday development',
    price: 399,
    original_price: 0,
    seven_day_quota_usd: 110,
    validity_days: 30,
    validity_unit: 'days',
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
      groups,
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
              this.$emit('update:modelValue', Number((event.target as HTMLSelectElement).value))
            },
          },
          template: '<select :value="modelValue == null ? \'\' : modelValue" @change="onChange"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>',
        },
        Icon: true,
        GroupBadge: {
          props: ['name'],
          template: '<span>{{ name }}</span>',
        },
      },
    },
  })
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
    await wrapper.find('select').setValue('2')
    await wrapper.find('textarea').setValue('Primary development')
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('799')
    await numberInputs[2].setValue('30')

    const quota = wrapper.find('[data-testid="plan-seven-day-quota"]')
    expect(quota.exists()).toBe(true)
    await quota.setValue('260')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(createPlan).toHaveBeenCalledWith(expect.objectContaining({
      seven_day_quota_usd: 260,
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

    expect(updatePlan).toHaveBeenCalledWith(1, expect.objectContaining({
      seven_day_quota_usd: null,
      clear_seven_day_quota_usd: true,
    }))
  })
})
