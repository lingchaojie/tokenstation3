import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { getLocale, i18n, initI18n, setLocale } from '..'

vi.mock('@/router', () => ({ default: { currentRoute: { value: { meta: {} } } } }))
vi.mock('@/router/title', () => ({ resolveRouteDocumentTitle: () => 'TokenStation' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ siteName: 'TokenStation' }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: false }) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.removeItem('sub2api_locale')
})

describe('i18n runtime compatibility', () => {
  it('loads and switches real locales with interpolation under CSP', async () => {
    // Production enables JIT compilation: lazy-loaded string messages must not
    // require Function/eval, even though they were not compiled during build.
    vi.stubGlobal('Function', function () {
      throw new Error('CSP blocks dynamic code generation')
    })

    await initI18n()
    await setLocale('en')
    expect(i18n.global.t('common.cancel')).toBe('Cancel')
    expect(i18n.global.t('common.selectedCount', { count: 3 })).toBe('(3 selected)')
    await setLocale('zh')
    expect(getLocale()).toBe('zh')
    expect(i18n.global.t('common.cancel')).toBe('取消')
    expect(i18n.global.t('common.selectedCount', { count: 3 })).toBe('（已选 3 个）')
    expect(localStorage.getItem('sub2api_locale')).toBe('zh')
    expect(document.documentElement.lang).toBe('zh')
    expect(document.title).toBe('TokenStation')
    await setLocale('invalid')
    expect(getLocale()).toBe('zh')
  })

  it('keeps fallback, plural and linked message behavior', () => {
    const instance = createI18n({
      legacy: false,
      locale: 'zh',
      fallbackLocale: 'en',
      missingWarn: false,
      fallbackWarn: false,
      messages: {
        zh: { greeting: '你好 {name}', linked: '@:greeting' },
        en: { fallback: 'English fallback', items: 'no items | one item | {count} items' }
      }
    })
    expect(instance.global.t('linked', { name: 'Alice' })).toBe('你好 Alice')
    expect(instance.global.t('fallback')).toBe('English fallback')
    expect(instance.global.t('items', 0)).toBe('no items')
    expect(instance.global.t('items', 2)).toBe('2 items')
    instance.dispose()
  })

  it('neutralizes srcdoc when escaped translations are rendered as HTML', () => {
    const instance = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: { en: { frame: '<iframe srcdoc="{content}"></iframe>' } }
    })
    const translated = instance.global.t('frame', { content: '<b>untrusted</b>' })
    const container = document.createElement('div')
    container.innerHTML = translated
    expect(container.querySelector('iframe')?.hasAttribute('srcdoc')).toBe(false)
    instance.dispose()
  })
})
