import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import DingTalkOAuthSection from '@/components/auth/DingTalkOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>
}))

const locationState = vi.hoisted(() => ({
  current: { href: 'http://localhost/login' } as { href: string }
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('OAuth login sections', () => {
  beforeEach(() => {
    routeState.query = { redirect: '/billing?plan=pro', aff: 'AFF123' }
    locationState.current = { href: 'http://localhost/login' }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current
    })
    window.sessionStorage.clear()
  })

  it.each([
    ['linuxdo', LinuxDoOAuthSection],
    ['dingtalk', DingTalkOAuthSection],
    ['oidc', OidcOAuthSection]
  ] as const)('navigates the original %s button to its GET OAuth start route', async (provider, component) => {
    const wrapper = mount(component, { props: { affCode: 'AFF456' } })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      `/api/v1/auth/oauth/${provider}/start?redirect=%2Fbilling%3Fplan%3Dpro`
    )
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('AFF456')
  })

  it('includes a trimmed promo code in the LinuxDo OAuth request', async () => {
    const wrapper = mount(LinuxDoOAuthSection, {
      props: {
        affCode: 'AFF456',
        promoCode: ' PROMO789 '
      }
    })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      '/api/v1/auth/oauth/linuxdo/start?redirect=%2Fbilling%3Fplan%3Dpro&promo_code=PROMO789'
    )
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('AFF456')
  })
})
