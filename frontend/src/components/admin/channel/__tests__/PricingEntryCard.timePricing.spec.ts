import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PricingEntryCard from '../PricingEntryCard.vue'
import type { PricingFormEntry } from '../types'

const { getModelDefaultPricingMock } = vi.hoisted(() => ({
  getModelDefaultPricingMock: vi.fn(),
}))

vi.mock('@/api/admin/channels', () => ({
  default: { getModelDefaultPricing: getModelDefaultPricingMock },
}))

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

beforeEach(() => {
  getModelDefaultPricingMock.mockReset()
})

function createEntry(billingMode: PricingFormEntry['billing_mode'] = 'token'): PricingFormEntry {
  return {
    models: [],
    billing_mode: billingMode,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    fast_multiplier: null,
    flex_multiplier: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: {
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }],
    },
  }
}

describe('PricingEntryCard time pricing visibility', () => {
  it('is hidden by default', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry() },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(false)
  })

  it('is shown for token pricing when explicitly enabled', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry(), enableTimePricing: true },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(true)
  })

  it('is hidden for non-token pricing even when explicitly enabled', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry('per_request'), enableTimePricing: true },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(false)
  })

  it('clears time periods when changing billing mode', () => {
    const entry = createEntry()
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry, enableTimePricing: true },
    })

    wrapper.findComponent({ name: 'Select' }).vm.$emit('update:modelValue', 'image')

    expect(wrapper.emitted('update')?.[0]?.[0]).toEqual({
      ...entry,
      billing_mode: 'image',
      intervals: [],
      time_pricing: { timezone: 'Asia/Shanghai', periods: [] },
    })
    expect(entry.time_pricing.periods).toHaveLength(1)
  })
})

describe('PricingEntryCard service tier multipliers', () => {
  it('shows Fast and Flex controls only when explicitly enabled', () => {
    const hidden = shallowMount(PricingEntryCard, { props: { entry: createEntry() } })
    expect(hidden.text()).not.toContain('admin.channels.form.fastMultiplier')

    const shown = shallowMount(PricingEntryCard, {
      props: { entry: createEntry(), enableTierMultipliers: true },
    })
    expect(shown.text()).toContain('admin.channels.form.fastMultiplier')
    expect(shown.text()).toContain('admin.channels.form.flexMultiplier')
  })
})

describe('PricingEntryCard image input pricing', () => {
  it('shows a dedicated image input price control', () => {
    const wrapper = shallowMount(PricingEntryCard, { props: { entry: createEntry() } })

    expect(wrapper.text()).toContain('admin.channels.form.imageInputPrice')
  })

  it('preserves image input price from model default pricing', async () => {
    getModelDefaultPricingMock.mockResolvedValue({
      found: true,
      input_price: 1e-6,
      output_price: 2e-6,
      cache_write_price: 3e-6,
      cache_read_price: 4e-6,
      image_input_price: 5e-6,
      image_output_price: 6e-6,
    })
    const wrapper = shallowMount(PricingEntryCard, { props: { entry: createEntry() } })

    const onModelsUpdate = wrapper.vm.$.setupState.onModelsUpdate as (models: string[]) => Promise<void>
    await onModelsUpdate(['gpt-image-2'])
    await flushPromises()

    const updates = wrapper.emitted('update') ?? []
    expect(getModelDefaultPricingMock).toHaveBeenCalledWith('gpt-image-2')
    expect(updates.at(-1)?.[0]).toMatchObject({
      models: ['gpt-image-2'],
      image_input_price: 5,
    })
  })
})
