import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { setLocaleMock } = vi.hoisted(() => ({ setLocaleMock: vi.fn() }))

vi.mock('@/i18n', () => ({
  availableLocales: [
    { code: 'en', name: 'English', flag: '🇺🇸' },
    { code: 'zh', name: '中文', flag: '🇨🇳' }
  ],
  setLocale: setLocaleMock
}))

import LocaleSwitcher from '../LocaleSwitcher.vue'

const componentSource = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '..', 'LocaleSwitcher.vue'),
  'utf8'
)

function mountSwitcher() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {}, zh: {} } })
  return mount(LocaleSwitcher, { global: { plugins: [i18n] } })
}

describe('LocaleSwitcher', () => {
  beforeEach(() => {
    setLocaleMock.mockReset()
    setLocaleMock.mockResolvedValue(undefined)
  })

  it('uses real focus-visible and reduced-motion controls for the trigger and options', async () => {
    const wrapper = mountSwitcher()
    const trigger = wrapper.get('[data-testid="locale-switcher-trigger"]')

    expect(trigger.element.tagName).toBe('BUTTON')
    expect(trigger.attributes('type')).toBe('button')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(trigger.classes()).toContain('focus-visible:ring-2')
    expect(trigger.classes()).toContain('motion-reduce:transition-none')
    const chevron = wrapper.get('[data-testid="locale-switcher-chevron"]')
    expect(chevron.classes()).toContain('motion-reduce:transition-none')
    expect(chevron.classes()).toContain('motion-reduce:transform-none')

    await trigger.trigger('click')

    expect(trigger.attributes('aria-expanded')).toBe('true')
    const options = wrapper.findAll('[data-locale-option]')
    expect(options).toHaveLength(2)
    for (const option of options) {
      expect(option.element.tagName).toBe('BUTTON')
      expect(option.attributes('type')).toBe('button')
      expect(option.classes()).toContain('focus-visible:ring-2')
      expect(option.classes()).toContain('motion-reduce:transition-none')
    }

    await wrapper.get('[data-locale-option="zh"]').trigger('click')
    await flushPromises()
    expect(setLocaleMock).toHaveBeenCalledWith('zh')
    expect(trigger.attributes('aria-expanded')).toBe('false')
  })

  it('disables dropdown transition and transform under reduced motion', () => {
    expect(componentSource).toContain('@media (prefers-reduced-motion: reduce)')
    expect(componentSource).toMatch(/@media \(prefers-reduced-motion: reduce\)[\s\S]*transition: none/)
    expect(componentSource).toMatch(/@media \(prefers-reduced-motion: reduce\)[\s\S]*transform: none/)
  })
})
