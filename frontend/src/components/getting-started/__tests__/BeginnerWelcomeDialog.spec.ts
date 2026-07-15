import { nextTick } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, describe, expect, it } from 'vitest'

import BeginnerWelcomeDialog from '../BeginnerWelcomeDialog.vue'

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        gettingStarted: {
          welcome: {
            title: () => 'Let us help you get started',
            description: () =>
              'You do not need any AI experience. The guide walks you through every step and you can return at any time.',
            start: () => 'Start guide',
            closeLabel: () => 'Close beginner guide welcome'
          }
        }
      }
    }
  })
}

let wrapper: VueWrapper | null = null

function mountDialog(show = true) {
  wrapper = mount(BeginnerWelcomeDialog, {
    attachTo: document.body,
    props: { show },
    global: { plugins: [createTestI18n()] }
  })
  return wrapper
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('BeginnerWelcomeDialog', () => {
  it('renders concise localized beginner copy and a visible primary start button', async () => {
    mountDialog()
    await nextTick()

    expect(document.body.textContent).toContain('Let us help you get started')
    expect(document.body.textContent).toContain('You do not need any AI experience')
    const start = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="beginner-welcome-start"]'
    )
    expect(start?.tagName).toBe('BUTTON')
    expect(start?.textContent).toContain('Start guide')
    expect(document.body.querySelector('[aria-label="Close beginner guide welcome"]')).not.toBeNull()
  })

  it('emits start without embedding navigation or API behavior', async () => {
    const mounted = mountDialog()
    await nextTick()

    document.body
      .querySelector<HTMLButtonElement>('[data-testid="beginner-welcome-start"]')
      ?.click()
    await nextTick()

    expect(mounted.emitted('start')).toHaveLength(1)
  })

  it('emits close from both the translated visible close button and Escape', async () => {
    const mounted = mountDialog()
    await nextTick()

    document.body
      .querySelector<HTMLButtonElement>('[aria-label="Close beginner guide welcome"]')
      ?.click()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()

    expect(mounted.emitted('close')).toHaveLength(2)
  })

  it('restores focus through BaseDialog after the parent hides it', async () => {
    const launcher = document.createElement('button')
    launcher.textContent = 'Open guide welcome'
    document.body.appendChild(launcher)
    launcher.focus()

    const mounted = mountDialog()
    await nextTick()
    expect(document.activeElement).not.toBe(launcher)

    await mounted.setProps({ show: false })
    await nextTick()

    expect(document.activeElement).toBe(launcher)
  })

  it('renders nothing when show is false', () => {
    mountDialog(false)

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })
})
