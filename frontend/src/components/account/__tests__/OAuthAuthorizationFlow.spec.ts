import { describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: vi.fn(),
    showError: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: { value: false },
    copyToClipboard: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

function mountFlow(props: Record<string, unknown> = {}) {
  return mount(OAuthAuthorizationFlow, {
    props: {
      addMethod: 'oauth',
      platform: 'kiro',
      authUrl: 'https://example.com/authorize',
      sessionId: 'session-1',
      ...props
    },
    global: {
      stubs: {
        Icon: true
      }
    }
  })
}

describe('OAuthAuthorizationFlow', () => {
  it('uses Kiro OAuth copy for Kiro accounts', () => {
    const wrapper = mountFlow()

    expect(wrapper.text()).toContain('admin.accounts.oauth.kiro.title')
    expect(wrapper.text()).not.toContain('admin.accounts.oauth.title')
  })

  it('extracts code, state, and callback metadata from a full Kiro callback URL', async () => {
    const wrapper = mountFlow()

    const textarea = wrapper.get('textarea')
    await textarea.setValue('http://localhost:49153/oauth/callback?code=abc123&state=state456&login_option=github')
    await nextTick()

    expect((textarea.element as HTMLTextAreaElement).value).toBe('abc123')
    expect((wrapper.vm as any).oauthState).toBe('state456')
    expect((wrapper.vm as any).oauthCallbackPath).toBe('/oauth/callback')
    expect((wrapper.vm as any).oauthLoginOption).toBe('github')
  })

  it('shows external identity provider URL separately from the primary auth URL', () => {
    const wrapper = mountFlow({
      externalAuthUrl: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize'
    })

    expect(wrapper.text()).toContain('admin.accounts.oauth.kiro.externalIDPAuthUrl')
    expect(wrapper.find('input[value="https://example.com/authorize"]').exists()).toBe(true)
    expect(wrapper.find('input[value="https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize"]').exists()).toBe(true)
  })

  it('clears the portal callback before accepting the external IdP callback', async () => {
    const wrapper = mountFlow({
      isKiroExternalIdp: false,
      externalIdpStage: 'portal'
    })
    const textarea = wrapper.get('textarea')
    await textarea.setValue('kiro://kiro.aws.amazon.com/signin/redirect?code=portal-code')

    expect((textarea.element as HTMLTextAreaElement).value).toBe('portal-code')

    await wrapper.setProps({
      isKiroExternalIdp: true,
      externalIdpStage: 'idp'
    })
    await nextTick()

    expect((textarea.element as HTMLTextAreaElement).value).toBe('')
  })
})
