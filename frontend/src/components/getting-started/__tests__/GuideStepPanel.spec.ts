import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'

import GuideStepPanel from '../GuideStepPanel.vue'

function mountPanel() {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        gettingStarted: {
          chrome: {
            back: () => 'Back',
            next: () => 'Next'
          }
        }
      }
    }
  })

  return mount(GuideStepPanel, {
    props: {
      stepId: 'choose',
      stepNumber: 2,
      stepCount: 8,
      title: 'Choose',
      description: 'Choose a client and operating system.'
    },
    global: { plugins: [i18n] }
  })
}

describe('GuideStepPanel', () => {
  it('keeps Back and Next as keyboard-visible reduced-motion controls', async () => {
    const wrapper = mountPanel()
    const [back, next] = wrapper.findAll('footer button')

    for (const control of [back, next]) {
      expect(control.attributes('type')).toBe('button')
      expect(control.classes()).toContain('focus-visible:ring-2')
      expect(control.classes()).toContain('motion-reduce:transition-none')
    }

    await back.trigger('click')
    await next.trigger('click')
    expect(wrapper.emitted('back')).toEqual([[]])
    expect(wrapper.emitted('next')).toEqual([[]])
  })
})
