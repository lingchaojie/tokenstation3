/* eslint-disable @typescript-eslint/triple-slash-reference */
/// <reference path="../../../vite-env.d.ts" />

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AuthLayout from '../../../components/layout/AuthLayout.vue'
import LoginView from '../LoginView.vue'

const fetchPublicSettings = vi.hoisted(() => vi.fn())
const login = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    siteName: 'LINX2.AI',
    siteLogo: '',
    cachedPublicSettings: { site_subtitle: 'AI 网关平台 · linx2.ai' },
    publicSettingsLoaded: true,
    fetchPublicSettings,
    showError,
    showWarning,
  }),
  useAuthStore: () => ({ login }),
}))

vi.mock('@/api/auth', () => ({
  getPublicSettings: vi.fn().mockResolvedValue({
    turnstile_enabled: false,
    turnstile_site_key: '',
    linuxdo_oauth_enabled: false,
    dingtalk_oauth_enabled: false,
    wechat_oauth_enabled: false,
    backend_mode_enabled: false,
    oidc_oauth_enabled: false,
    oidc_oauth_provider_name: 'OIDC',
    github_oauth_enabled: false,
    google_oauth_enabled: false,
    password_reset_enabled: true,
    login_agreement_enabled: false,
    login_agreement_documents: [],
  }),
  isTotp2FARequired: () => false,
  isWeChatWebOAuthEnabled: () => false,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), currentRoute: { value: { query: {} } } }),
  RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>' },
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({ global: { t: (key: string) => key } }),
  useI18n: () => ({
    t: (key: string) => ({
      'auth.welcomeBack': 'Welcome back',
      'auth.signInToAccount': 'Sign in to continue',
      'auth.emailLabel': 'Email',
      'auth.emailPlaceholder': 'you@example.com',
      'auth.passwordLabel': 'Password',
      'auth.passwordPlaceholder': 'Password',
      'auth.forgotPassword': 'Forgot password?',
      'auth.signIn': 'Sign in',
      'auth.signingIn': 'Signing in',
      'auth.dontHaveAccount': 'No account?',
      'auth.signUp': 'Sign up',
      'auth.layout.kicker': 'AI 网关平台',
      'auth.layout.title': '一个入口管理模型、密钥和用量。',
      'auth.layout.description': '登录后即可在沉稳的 Linear 风格控制台中管理 API 密钥、套餐、账单和渠道访问。',
      'auth.layout.baseUrl': '基础地址',
      'auth.layout.routes': '路由',
      'auth.layout.billing': '计费',
      'auth.layout.billingValue': '用量账本已启用',
    }[key] ?? key),
  }),
}))

describe('Auth Linear shell', () => {
  beforeEach(() => {
    fetchPublicSettings.mockReset().mockResolvedValue({})
    login.mockReset()
    showError.mockReset()
    showWarning.mockReset()
  })

  it('renders auth layout as a Linear-style product entry shell', () => {
    const wrapper = mount(AuthLayout, {
      slots: { default: '<div data-testid="auth-slot">Auth form</div>' },
    })

    expect(wrapper.find('.linear-auth-shell').exists()).toBe(true)
    expect(wrapper.find('[data-testid="auth-product-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="auth-card"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('LINX2.AI')
    expect(wrapper.text()).toContain('一个入口管理模型、密钥和用量。')
    expect(wrapper.text()).not.toContain('订阅转 API')
    expect(wrapper.text()).not.toContain('Subscription to API')
    expect(wrapper.html()).not.toContain('blur-3xl')
  })

  it('uses i18n copy for the product panel instead of hardcoded English', () => {
    const wrapper = mount(AuthLayout, {
      slots: { default: '<div data-testid="auth-slot">Auth form</div>' },
    })

    const productPanel = wrapper.find('[data-testid="auth-product-panel"]')

    expect(productPanel.text()).toContain('AI 网关平台')
    expect(productPanel.text()).toContain('一个入口管理模型、密钥和用量。')
    expect(productPanel.text()).toContain('登录后即可在沉稳的 Linear 风格控制台中管理 API 密钥、套餐、账单和渠道访问。')
    expect(productPanel.text()).toContain('基础地址')
    expect(productPanel.text()).toContain('路由')
    expect(productPanel.text()).toContain('计费')
    expect(productPanel.text()).toContain('用量账本已启用')

    expect(productPanel.text()).not.toContain('One gateway for models, keys, and usage.')
    expect(productPanel.text()).not.toContain('Sign in to manage API keys')
    expect(productPanel.text()).not.toContain('Usage ledger enabled')
  })

  it('renders LoginView inside the shared Linear auth card', async () => {
    const wrapper = mount(LoginView, {
      global: {
        stubs: {
          AuthLayout,
          Icon: true,
          TurnstileWidget: true,
          EmailOAuthButtons: true,
          LinuxDoOAuthSection: true,
          DingTalkOAuthSection: true,
          WechatOAuthSection: true,
          OidcOAuthSection: true,
          LoginAgreementPrompt: true,
          TotpLoginModal: true,
        },
      },
    })

    expect(wrapper.find('.linear-auth-title').exists()).toBe(true)
    expect(wrapper.find('input#email').classes()).toContain('input')
    expect(wrapper.find('button[type="submit"]').classes()).toContain('btn-primary')
  })
})
