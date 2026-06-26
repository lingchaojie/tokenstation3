import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AmountInput from '../AmountInput.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

describe('AmountInput', () => {
  it('shows recharge amount choices and input prefix with the RMB symbol', () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: null,
        amounts: [10, 1000],
      },
    })

    expect(wrapper.text()).toContain('¥10')
    expect(wrapper.text()).toContain('¥1,000')
    expect(wrapper.text()).not.toContain('$')
  })
})
