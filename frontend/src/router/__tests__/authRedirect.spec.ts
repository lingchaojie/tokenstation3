import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { resolvePostAuthRedirect } from '@/router/authRedirect'
import LoginView from '@/views/auth/LoginView.vue'
import RegisterView from '@/views/auth/RegisterView.vue'

const authHarness = vi.hoisted(() => ({
  push: vi.fn(),
  login: vi.fn(),
  login2FA: vi.fn(),
  register: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  getPublicSettings: vi.fn(),
  isTotp2FARequired: vi.fn(),
  routeQuery: {} as Record<string, unknown>,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: (...args: unknown[]) => authHarness.push(...args),
    currentRoute: { value: { query: authHarness.routeQuery } },
  }),
  useRoute: () => ({ query: authHarness.routeQuery }),
  RouterLink: {
    props: ['to'],
    template: '<a><slot /></a>',
  },
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key,
    },
  }),
  useI18n: () => ({
    t: (key: string) => key,
    locale: { value: 'en' },
  }),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    login: (...args: unknown[]) => authHarness.login(...args),
    login2FA: (...args: unknown[]) => authHarness.login2FA(...args),
    register: (...args: unknown[]) => authHarness.register(...args),
  }),
  useAppStore: () => ({
    showSuccess: (...args: unknown[]) => authHarness.showSuccess(...args),
    showError: (...args: unknown[]) => authHarness.showError(...args),
    showWarning: (...args: unknown[]) => authHarness.showWarning(...args),
  }),
}))

vi.mock('@/api/auth', () => ({
  getPublicSettings: (...args: unknown[]) => authHarness.getPublicSettings(...args),
  isTotp2FARequired: (...args: unknown[]) => authHarness.isTotp2FARequired(...args),
  isWeChatWebOAuthEnabled: () => false,
  validatePromoCode: vi.fn(),
  validateInvitationCode: vi.fn(),
}))

const authViewStubs = {
  AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
  Icon: true,
  TurnstileWidget: true,
  EmailOAuthButtons: true,
  LinuxDoOAuthSection: true,
  DingTalkOAuthSection: true,
  WechatOAuthSection: true,
  OidcOAuthSection: true,
  LoginAgreementPrompt: true,
  TotpLoginModal: {
    template: '<button data-testid="verify-2fa" @click="$emit(\'verify\', \'123456\')" />',
    methods: {
      setVerifying: vi.fn(),
      setError: vi.fn(),
    },
  },
  RouterLink: true,
  transition: false,
}

function publicSettings(emailVerifyEnabled = false): Record<string, unknown> {
  return {
    registration_enabled: true,
    email_verify_enabled: emailVerifyEnabled,
    promo_code_enabled: false,
    invitation_code_enabled: false,
    turnstile_enabled: false,
    turnstile_site_key: '',
    site_name: 'Sub2API',
    linuxdo_oauth_enabled: false,
    dingtalk_oauth_enabled: false,
    wechat_oauth_enabled: false,
    backend_mode_enabled: false,
    oidc_oauth_enabled: false,
    oidc_oauth_provider_name: 'OIDC',
    github_oauth_enabled: false,
    google_oauth_enabled: false,
    password_reset_enabled: true,
    registration_email_suffix_whitelist: [],
    login_agreement_enabled: false,
    login_agreement_documents: [],
  }
}

async function submitAuthView(component: typeof LoginView | typeof RegisterView) {
  const wrapper = mount(component, {
    global: { stubs: authViewStubs },
  })
  await flushPromises()
  await wrapper.get('#email').setValue('guide@example.com')
  await wrapper.get('#password').setValue('secret-123')
  await wrapper.get('form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

describe('resolvePostAuthRedirect', () => {
  it.each([
    ['/getting-started', '/getting-started'],
    ['/profile?tab=security', '/profile?tab=security'],
    ['  /getting-started?from=register  ', '/getting-started?from=register'],
  ])('keeps internal absolute redirect %j', (value, expected) => {
    expect(resolvePostAuthRedirect(value)).toBe(expected)
  })

  it.each([
    undefined,
    null,
    '',
    'dashboard',
    ['/', '/getting-started'],
    'https://evil.example',
    '//evil.example',
    '/\\evil.example',
    '%2F%2Fevil.example',
    '/%2Fevil.example',
    '/%5Cevil.example',
    '/%252Fevil.example',
    '/%255Cevil.example',
  ])('uses the dashboard for unsafe redirect %j', (value) => {
    expect(resolvePostAuthRedirect(value)).toBe('/dashboard')
  })

  it('keeps an existing internal fallback for callers with a different default', () => {
    expect(resolvePostAuthRedirect('javascript:alert(1)', '/home')).toBe('/home')
  })
})

describe('password authentication redirects', () => {
  beforeEach(() => {
    authHarness.push.mockReset()
    authHarness.login.mockReset().mockResolvedValue({})
    authHarness.login2FA.mockReset().mockResolvedValue({})
    authHarness.register.mockReset().mockResolvedValue({})
    authHarness.showSuccess.mockReset()
    authHarness.showError.mockReset()
    authHarness.showWarning.mockReset()
    authHarness.getPublicSettings.mockReset().mockResolvedValue(publicSettings())
    authHarness.isTotp2FARequired.mockReset().mockReturnValue(false)
    for (const key of Object.keys(authHarness.routeQuery)) {
      delete authHarness.routeQuery[key]
    }
    sessionStorage.clear()
    localStorage.clear()
  })

  it('returns a successful login to the internal guide route', async () => {
    authHarness.routeQuery.redirect = '/getting-started'

    await submitAuthView(LoginView)

    expect(authHarness.push).toHaveBeenCalledWith('/getting-started')
  })

  it('normalizes a duplicate login redirect query to the existing dashboard default', async () => {
    authHarness.routeQuery.redirect = ['/getting-started', '//evil.example']

    await submitAuthView(LoginView)

    expect(authHarness.push).toHaveBeenCalledWith('/dashboard')
  })

  it('sanitizes the login redirect again after successful two-factor verification', async () => {
    authHarness.routeQuery.redirect = '/getting-started'
    authHarness.login.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temporary-token',
      user_email_masked: 'g***@example.com',
    })
    authHarness.isTotp2FARequired.mockReturnValue(true)

    const wrapper = await submitAuthView(LoginView)
    authHarness.routeQuery.redirect = '//evil.example'
    await wrapper.get('[data-testid="verify-2fa"]').trigger('click')
    await flushPromises()

    expect(authHarness.login2FA).toHaveBeenCalledWith('temporary-token', '123456')
    expect(authHarness.push).toHaveBeenCalledWith('/dashboard')
  })

  it('returns successful direct registration to the internal guide route', async () => {
    authHarness.routeQuery.redirect = '/getting-started'

    await submitAuthView(RegisterView)

    expect(authHarness.push).toHaveBeenCalledWith('/getting-started')
  })

  it('keeps the existing dashboard destination for direct registration without a redirect', async () => {
    await submitAuthView(RegisterView)

    expect(authHarness.push).toHaveBeenCalledWith('/dashboard')
  })

  it('stores only the sanitized return path with existing email-verification registration data', async () => {
    authHarness.routeQuery.redirect = '/getting-started'
    authHarness.getPublicSettings.mockResolvedValue(publicSettings(true))

    await submitAuthView(RegisterView)

    const registerData = JSON.parse(sessionStorage.getItem('register_data') || '{}')
    expect(registerData).toMatchObject({
      email: 'guide@example.com',
      pending_redirect: '/getting-started',
    })
    expect(registerData).not.toHaveProperty('progress')
    expect(registerData).not.toHaveProperty('api_key')
    expect(registerData).not.toHaveProperty('config')
    expect(authHarness.push).toHaveBeenCalledWith('/email-verify')
  })
})
