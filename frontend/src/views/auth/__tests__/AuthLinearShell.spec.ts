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
      'auth.welcomeBack': 'LINX2.AI 控制台',
      'auth.signInToAccount': '登录后管理 Claude Code、Codex 与 OpenAI 兼容网关。',
      'auth.emailLabel': 'Email',
      'auth.emailPlaceholder': 'you@example.com',
      'auth.passwordLabel': 'Password',
      'auth.passwordPlaceholder': 'Password',
      'auth.forgotPassword': 'Forgot password?',
      'auth.signIn': 'Sign in',
      'auth.signingIn': 'Signing in',
      'auth.dontHaveAccount': 'No account?',
      'auth.signUp': 'Sign up',
      'auth.layout.kicker': 'LINX2.AI AI 网关平台',
      'auth.layout.title': '一个入口接入 Claude Code、Codex 与 OpenAI。',
      'auth.layout.description': '登录后管理 API 密钥、订阅额度、用量账单和 Claude / OpenAI 兼容路由。',
      'auth.layout.baseUrl': '基础地址',
      'auth.layout.routes': '路由',
      'auth.layout.billing': '计费',
      'auth.layout.billingValue': '用量账本已启用',
    }[key] ?? key),
  }),
}))

describe('Auth LINX2 shell', () => {
  beforeEach(() => {
    fetchPublicSettings.mockReset().mockResolvedValue({})
    login.mockReset()
    showError.mockReset()
    showWarning.mockReset()
  })

  it('renders auth layout as a LINX2 product entry shell', () => {
    const wrapper = mount(AuthLayout, {
      slots: { default: '<div data-testid="auth-slot">Auth form</div>' },
    })

    const shell = wrapper.get('.linear-auth-shell')
    expect(shell.classes()).not.toContain('dark')
    expect(shell.classes()).toContain('bg-linear-canvas')
    expect(wrapper.find('[data-testid="auth-product-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="auth-card"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('LINX2.AI')
    expect(wrapper.text()).toContain('一个入口接入 Claude Code、Codex 与 OpenAI。')
    expect(wrapper.text()).toContain('Anthropic · OpenAI')
    expect(wrapper.text()).not.toContain('Gemini')
    expect(wrapper.text()).not.toContain('Linear')
    expect(wrapper.text()).not.toContain('订阅转 API')
    expect(wrapper.text()).not.toContain('Subscription to API')
    expect(wrapper.html()).not.toContain('blur-3xl')
  })

  it('uses i18n copy for the product panel instead of hardcoded English', () => {
    const wrapper = mount(AuthLayout, {
      slots: { default: '<div data-testid="auth-slot">Auth form</div>' },
    })

    const productPanel = wrapper.find('[data-testid="auth-product-panel"]')

    expect(productPanel.text()).toContain('LINX2.AI AI 网关平台')
    expect(productPanel.text()).toContain('一个入口接入 Claude Code、Codex 与 OpenAI。')
    expect(productPanel.text()).toContain('登录后管理 API 密钥、订阅额度、用量账单和 Claude / OpenAI 兼容路由。')
    expect(productPanel.text()).toContain('基础地址')
    expect(productPanel.text()).toContain('路由')
    expect(productPanel.text()).toContain('计费')
    expect(productPanel.text()).toContain('用量账本已启用')
    expect(productPanel.text()).toContain('Anthropic · OpenAI')

    expect(productPanel.text()).not.toContain('Gemini')
    expect(productPanel.text()).not.toContain('Linear')
    expect(productPanel.text()).not.toContain('One gateway for models, keys, and usage.')
    expect(productPanel.text()).not.toContain('Sign in to manage API keys')
    expect(productPanel.text()).not.toContain('Usage ledger enabled')
  })

  it('renders LoginView inside the shared LINX2 auth card', async () => {
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
          RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>' },
        },
      },
    })

    expect(wrapper.find('.linear-auth-title').exists()).toBe(true)
    expect(wrapper.find('input#email').classes()).toContain('input')
    expect(wrapper.find('button[type="submit"]').classes()).toContain('btn-primary')
  })
})
