import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'

import GuideProgressNav from '../GuideProgressNav.vue'

const stepTitles = {
  understand: 'Understand the tools',
  choose: 'Choose a client and system',
  terminal: 'Open a terminal',
  install: 'Install the client',
  api_key: 'Choose an API key',
  configure: 'Connect to this service',
  first_run: 'Run your first task',
  troubleshoot: 'Verify and troubleshoot'
}

function mountProgress(
  currentStep: keyof typeof stepTitles = 'choose',
  completedSteps: Array<keyof typeof stepTitles> = ['understand']
) {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        gettingStarted: {
          chrome: {
            progress: () => 'Guide progress',
            mobileStepMenu: () => 'Guide steps',
            openStepMenu: () => 'Open step menu',
            closeStepMenu: () => 'Close step menu'
          },
          steps: Object.fromEntries(
            Object.entries(stepTitles).map(([id, title]) => [id, { title: () => title }])
          )
        }
      }
    }
  })

  return mount(GuideProgressNav, {
    props: { currentStep, completedSteps },
    global: { plugins: [i18n] }
  })
}

describe('GuideProgressNav', () => {
  it('renders eight stable named steps with non-color current and completion state', () => {
    const wrapper = mountProgress()
    const steps = wrapper.findAll('[data-guide-step]')

    expect(steps).toHaveLength(8)
    Object.values(stepTitles).forEach((title, index) => {
      expect(steps[index].text()).toContain(title)
    })
    expect(wrapper.get('[data-guide-step="understand"]').attributes('data-state')).toBe(
      'completed'
    )
    expect(
      wrapper.get('[data-guide-step="understand"] [data-testid="completed-icon"]').exists()
    ).toBe(true)
    expect(wrapper.get('[data-guide-step="understand"]').attributes('aria-label')).toContain('✓')
    expect(wrapper.get('[data-guide-step="choose"]').attributes('aria-current')).toBe('step')
  })

  it('emits only reachable known steps and blocks prerequisite skipping', async () => {
    const wrapper = mountProgress()

    await wrapper.get('[data-guide-step="install"]').trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()

    await wrapper.get('[data-guide-step="understand"]').trigger('click')
    expect(wrapper.emitted('select')).toEqual([['understand']])
  })

  it('does not treat a preserved later completion as permission to skip an incomplete prerequisite', async () => {
    const wrapper = mountProgress('terminal', ['understand', 'choose', 'api_key'])

    await wrapper.get('[data-guide-step="api_key"]').trigger('click')

    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.get('[data-guide-step="api_key"]').attributes('disabled')).toBeDefined()
  })

  it('opens and dismisses the mobile step drawer', async () => {
    const wrapper = mountProgress()

    expect(wrapper.find('[data-testid="mobile-step-drawer"]').exists()).toBe(false)
    await wrapper.get('[data-testid="mobile-step-menu-button"]').trigger('click')
    expect(wrapper.get('[data-testid="mobile-step-drawer"]').attributes('role')).toBe('dialog')

    await wrapper.get('[data-testid="mobile-step-menu-close"]').trigger('click')
    expect(wrapper.find('[data-testid="mobile-step-drawer"]').exists()).toBe(false)
  })
})
