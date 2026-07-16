import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RegisterView from '@/views/auth/RegisterView.vue'

const mocks = vi.hoisted(() => ({
  route: { query: {} as Record<string, string> },
  push: vi.fn(),
  replace: vi.fn(),
  register: vi.fn(),
  getPublicSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push, replace: mocks.replace }),
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({ global: { t: (key: string) => key } }),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ register: (...args: unknown[]) => mocks.register(...args) }),
  useAppStore: () => ({
    showSuccess: (...args: unknown[]) => mocks.showSuccess(...args),
    showError: (...args: unknown[]) => mocks.showError(...args),
    showWarning: (...args: unknown[]) => mocks.showWarning(...args),
  }),
}))

vi.mock('@/api/auth', () => ({
  getPublicSettings: (...args: unknown[]) => mocks.getPublicSettings(...args),
  isWeChatWebOAuthEnabled: () => false,
  validatePromoCode: vi.fn(),
  validateInvitationCode: vi.fn(),
}))

const publicSettings = (affiliateEnabled: boolean, emailVerifyEnabled = false) => ({
  registration_enabled: true,
  email_verify_enabled: emailVerifyEnabled,
  promo_code_enabled: false,
  invitation_code_enabled: false,
  turnstile_enabled: false,
  turnstile_site_key: '',
  site_name: 'Sub2API',
  linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false,
  oidc_oauth_enabled: false,
  github_oauth_enabled: false,
  google_oauth_enabled: false,
  registration_email_suffix_whitelist: [],
  affiliate_enabled: affiliateEnabled,
})

function mountView() {
  return mount(RegisterView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true,
        TurnstileWidget: true,
        LoginAgreementPrompt: true,
        EmailOAuthButtons: true,
        LinuxDoOAuthSection: true,
        WechatOAuthSection: true,
        OidcOAuthSection: true,
        RouterLink: true,
        transition: false,
      },
    },
  })
}

async function fillRequiredFields(wrapper: ReturnType<typeof mountView>) {
  await wrapper.get('#email').setValue('new-user@example.com')
  await wrapper.get('#password').setValue('secret-123')
}

describe('RegisterView affiliate activity switch', () => {
  beforeEach(() => {
    mocks.route.query = {}
    mocks.push.mockReset()
    mocks.replace.mockReset()
    mocks.register.mockReset().mockResolvedValue(undefined)
    mocks.getPublicSettings.mockReset()
    mocks.showSuccess.mockReset()
    mocks.showError.mockReset()
    mocks.showWarning.mockReset()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('removes referral UI, storage, and URL credentials when the activity is disabled', async () => {
    mocks.route.query = { aff: 'FRIEND123', redirect: '/dashboard/models', lang: 'zh' }
    localStorage.setItem('affiliate_referral_code', JSON.stringify({
      code: 'STORED123',
      expiresAt: Date.now() + 60_000,
    }))
    sessionStorage.setItem('oauth_aff_code', 'SESSION123')
    mocks.getPublicSettings.mockResolvedValue(publicSettings(false, true))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('#aff_code').exists()).toBe(false)
    expect(localStorage.getItem('affiliate_referral_code')).toBeNull()
    expect(sessionStorage.getItem('oauth_aff_code')).toBeNull()
    expect(mocks.replace).toHaveBeenCalledWith({
      query: { redirect: '/dashboard/models', lang: 'zh' },
    })

    await fillRequiredFields(wrapper)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const registerData = JSON.parse(sessionStorage.getItem('register_data') || '{}')
    expect(registerData.aff_code).toBeUndefined()
  })

  it('shows and submits the referral code only while the activity is enabled', async () => {
    mocks.route.query = { aff: 'FRIEND123' }
    mocks.getPublicSettings.mockResolvedValue(publicSettings(true))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('#aff_code').exists()).toBe(true)
    expect((wrapper.get('#aff_code').element as HTMLInputElement).value).toBe('FRIEND123')
    await fillRequiredFields(wrapper)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.register).toHaveBeenCalledWith(expect.objectContaining({ aff_code: 'FRIEND123' }))
  })
})
