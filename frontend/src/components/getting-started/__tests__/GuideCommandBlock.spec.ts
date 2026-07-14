import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import GuideCommandBlock from '../GuideCommandBlock.vue'

function mountCommand(command = 'claude --version') {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        gettingStarted: {
          chrome: {
            copy: () => 'Copy',
            copied: () => 'Copied',
            copyFailed: () => 'Could not copy automatically',
            manualCopy: () => 'Select the text and copy it manually.'
          }
        }
      }
    }
  })

  return mount(GuideCommandBlock, {
    props: { command },
    global: { plugins: [i18n] }
  })
}

describe('GuideCommandBlock', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) }
    })
  })

  it('keeps escaped command text selectable and horizontal overflow local', () => {
    const command = '<img src=x onerror=alert(1)>'
    const wrapper = mountCommand(command)

    expect(wrapper.get('code').text()).toBe(command)
    expect(wrapper.get('code').classes()).toContain('select-text')
    expect(wrapper.get('pre').classes()).toContain('overflow-x-auto')
    expect(wrapper.get('[data-testid="guide-command-block"]').classes()).toContain('min-w-0')
    expect(wrapper.html()).toContain('&lt;img')
    expect(wrapper.find('img').exists()).toBe(false)
  })

  it('uses the Clipboard API and announces successful copying', async () => {
    const wrapper = mountCommand()

    await wrapper.get('button').trigger('click')

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('claude --version')
    expect(wrapper.get('[aria-live="polite"]').text()).toBe('Copied')
  })

  it('shows manual-copy guidance without hiding the command when copying fails', async () => {
    vi.mocked(navigator.clipboard.writeText).mockRejectedValueOnce(new Error('denied'))
    const wrapper = mountCommand()

    await wrapper.get('button').trigger('click')

    expect(wrapper.get('[aria-live="polite"]').text()).toContain(
      'Could not copy automatically'
    )
    expect(wrapper.text()).toContain('Select the text and copy it manually.')
    expect(wrapper.get('code').text()).toBe('claude --version')
  })
})
