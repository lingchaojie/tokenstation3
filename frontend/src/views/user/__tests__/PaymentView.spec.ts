/* eslint-disable @typescript-eslint/triple-slash-reference */
/// <reference path="../../../vite-env.d.ts" />

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '../../../components/payment/paymentFlow'
import { formatPaymentAmount } from '../../../components/payment/currency'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '../../../types/payment'

const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const mockUpdateProfile = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: {
    username: 'demo-user',
    balance: 0,
    subscription_balance_fallback_enabled: false,
  } as Record<string, unknown>,
}))
const activeSubscriptionsState = vi.hoisted(() => ({
  value: [] as Array<Record<string, unknown>>,
}))
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const getCheckoutInfo = vi.hoisted(() => vi.fn())
const bridgeInvoke = vi.hoisted(() => vi.fn())

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'dashboard.pendingSubscriptionChange') {
          return `${params?.plan || ''} starts on ${params?.time || ''}`
        }
        return key
      },
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get user() {
      return authState.user
    },
    set user(value) {
      authState.user = value
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    get activeSubscriptions() {
      return activeSubscriptionsState.value
    },
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showWarning,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
  },
}))

vi.mock('@/api/user', () => ({
  userAPI: {
    updateProfile: (...args: unknown[]) => mockUpdateProfile(...args),
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => true,
}))

function checkoutInfoFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  const wxpayMethod: MethodLimit = {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
  }
  const data: CheckoutInfoResponse = {
    methods: {
      wxpay: wxpayMethod,
    },
    global_min: 0,
    global_max: 0,
    plans: [],
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 0,
    recharge_fee_rate: 0,
    help_text: '',
    help_image_url: '',
    stripe_publishable_key: '',
  }

  return {
    data: { ...data, ...overrides },
  }
}

type CheckoutInfoFixtureOptions = {
  checkout?: Partial<CheckoutInfoResponse>
  method?: Partial<MethodLimit>
  plan?: Partial<SubscriptionPlan>
}

function monthlyPlanFixture(id: number, name: string, price: number, sevenDayQuota: number) {
  return {
    id,
    name,
    description: '',
    price,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'day',
    seven_day_quota_usd: sevenDayQuota,
    features: [],
    sort_order: id,
    for_sale: true,
  }
}

function checkoutInfoWithPlansFixture(options: CheckoutInfoFixtureOptions = {}) {
  const base = checkoutInfoFixture(options.checkout).data
  return {
    data: {
      ...base,
      methods: {
        ...base.methods,
        wxpay: {
          ...base.methods.wxpay,
          ...options.method,
        },
      },
      plans: [{
        ...monthlyPlanFixture(7, 'Plus monthly', 399, 110),
        ...options.plan,
      }],
    },
  }
}

function checkoutInfoWithoutMethodsFixture() {
  return {
    data: {
      ...checkoutInfoFixture().data,
      methods: {},
    },
  }
}

function checkoutInfoWithWeeklyPlanFixture() {
  return {
    data: {
      ...checkoutInfoFixture().data,
      plans: [{
        ...monthlyPlanFixture(7, 'Weekly plan', 99, 30),
        validity_days: 1,
        validity_unit: 'week',
      }],
    },
  }
}

function checkoutInfoWithMonthlyPlansFixture() {
  return {
    data: {
      ...checkoutInfoFixture().data,
      plans: [
        monthlyPlanFixture(5, 'Basic monthly', 179, 50),
        monthlyPlanFixture(7, 'Pro monthly', 799, 260),
        monthlyPlanFixture(8, 'Max monthly', 1599, 550),
      ],
    },
  }
}

function jsapiOrderFixture(resumeToken: string) {
  return {
    order_id: 123,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    out_trade_no: 'sub2_jsapi_123',
    result_type: 'jsapi_ready' as const,
    resume_token: resumeToken,
    jsapi: {
      appId: 'wx123',
      timeStamp: '1712345678',
      nonceStr: 'nonce',
      package: 'prepay_id=wx123',
      signType: 'RSA',
      paySign: 'signed',
    },
  }
}

function oauthOrderFixture() {
  return {
    order_id: 456,
    amount: 128,
    pay_amount: 128,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    result_type: 'oauth_required' as const,
    oauth: {
      authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

const paymentViewStubs = {
  AppLayout: { template: '<div><slot /></div>' },
  Teleport: true,
  Transition: false,
  SubscriptionPlanCard: {
    props: ['plan', 'displayName', 'pendingNotice', 'activeSubscriptions'],
    emits: ['select'],
    template: '<button class="plan-card-stub" type="button" @click="$emit(\'select\', plan)">{{ displayName || plan.name }}<span v-if="pendingNotice" class="pending-notice-stub">{{ pendingNotice }}</span><span class="active-count-stub">{{ activeSubscriptions?.length || 0 }}</span></button>',
  },
  ConfirmDialog: {
    props: ['show', 'title', 'message'],
    emits: ['confirm', 'cancel'],
    template: '<section v-if="show" class="confirm-dialog-stub"><h2>{{ title }}</h2><p>{{ message }}</p><button class="confirm-dialog-confirm" @click="$emit(\'confirm\')">confirm</button><button class="confirm-dialog-cancel" @click="$emit(\'cancel\')">cancel</button></section>',
  },
}

async function mountSubscriptionConfirm(options: CheckoutInfoFixtureOptions = {}) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = {
    tab: 'subscription',
    plan: '7',
  }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture(options))
  mockUpdateProfile.mockReset()
  authState.user = {
    username: 'demo-user',
    balance: 0,
    subscription_balance_fallback_enabled: false,
  }
  activeSubscriptionsState.value = []
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: paymentViewStubs,
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('PaymentView subscription confirmation amounts', () => {
  it('shows converted CNY pay amount using the subscription rate, not the balance multiplier', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
        original_price: 12.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const convertedOriginalPrice = formatPaymentAmount(92.88, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(convertedOriginalPrice)
    expect(text).not.toContain(formatPaymentAmount(9.99, 'CNY'))
    // 换算必须使用订阅汇率（×7.15），而不是余额倍率（÷0.14 = 71.36）
    expect(text).not.toContain(formatPaymentAmount(71.36, 'CNY'))
    expect(wrapper.findAll('button').some(button => button.text().includes(convertedPrice))).toBe(true)
  })

  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // opt-in 回归锁：即使余额倍率已配置，未配置订阅汇率时 CNY 订阅仍按 price 直付
    const cnyWrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 0,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 7.99,
      },
    })

    expect(cnyWrapper.text()).toContain(formatPaymentAmount(7.99, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.07, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.13, 'CNY'))

    const usdWrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'USD',
      },
      plan: {
        price: 7.99,
        original_price: 9.99,
      },
    })

    expect(usdWrapper.text()).toContain(formatPaymentAmount(7.99, 'USD'))
    expect(usdWrapper.text()).toContain(formatPaymentAmount(9.99, 'USD'))
  })

  it('adds fee rate after CNY rate conversion to match backend pay_amount', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
        recharge_fee_rate: 2.5,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const fee = formatPaymentAmount(1.79, 'CNY')
    const total = formatPaymentAmount(73.22, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(fee)
    expect(text).toContain(total)
    expect(wrapper.findAll('button').some(button => button.text().includes(total))).toBe(true)
  })
})

describe('PaymentView payment recovery', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      methods: {
        wxpay: checkoutInfoFixture().data.methods.wxpay,
        ldc: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_rate: 0,
          available: true,
          display_name: 'LDC Pay',
        },
      },
    }))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 888,
      amount: 66,
      qrCode: 'ldc-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'ldc',
      payUrl: 'https://pay.example.com/ldc',
      outTradeNo: 'sub2_ldc_888',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          PaymentStatusPanel: {
            template: '<button data-test="payment-done" @click="$emit(\'done\')" />',
          },
          PaymentMethodSelector: {
            props: ['selected'],
            template: '<div data-test="method-selector">{{ selected }}</div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    await wrapper.find('[data-test="payment-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="method-selector"]').text()).toBe('ldc')
  })
})

describe('PaymentView WeChat JSAPI flow', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-123',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture())
    mockUpdateProfile.mockReset()
    authState.user = {
      username: 'demo-user',
      balance: 0,
      subscription_balance_fallback_enabled: false,
    }
    activeSubscriptionsState.value = []
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  it('resets payment state and redirects to /payment/result after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(routerPush).toHaveBeenCalledWith({
      path: '/payment/result',
      query: {
        order_id: '123',
        out_trade_no: 'sub2_jsapi_123',
        resume_token: 'resume-token-123',
      },
    })
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('resets payment state when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('payment.qr.cancelled')
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('clears stale recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'payment.errors.wechatJsapiUnavailable payment.errors.wechatOpenInWeChatHint',
    )
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    expect(wrapper.html()).not.toContain('payment-status-panel-stub')
  })

  it('clears a stale recovery snapshot before handling wechat resume callback params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 999,
      amount: 66,
      qrCode: 'stale-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/stale',
      outTradeNo: 'stale-out-trade-no',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))

    shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      wechat_resume_token: 'resume-token-123',
    }))
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('shows selected subscription plan seven-day quota and quota-first hint', async () => {
    routeState.query = {
      tab: 'subscription',
      plan: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.planCard.sevenDayQuota')
    expect(text).toContain('$110')
    expect(text).toContain('payment.planCard.totalMonthlyQuota')
    expect(text).toContain('$440')
    expect(text).toContain('payment.subscription.quotaFirstHint')
    expect(text).not.toContain('payment.planCard.rate')
    expect(text).not.toContain('×1')
    expect(text).not.toContain('$999')
  })

  it('shows selected weekly subscription plan validity as weeks instead of days', async () => {
    routeState.query = {
      tab: 'subscription',
      plan: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithWeeklyPlanFixture())

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('/ payment.perWeek')
    expect(text).not.toContain('/ 1payment.days')
  })

  it('shows active subscription seven-day quota instead of unlimited', async () => {
    routeState.query = {
      tab: 'subscription',
    }
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        status: 'active',
        expires_at: '2099-01-01T00:00:00.000Z',
        seven_day_limit_usd: 110,
        group: {
          id: 3,
          name: 'OpenAI Subscription',
          platform: 'openai',
          rate_multiplier: 1,
          daily_limit_usd: null,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
        },
      },
    ]

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.planCard.sevenDayQuota: $110')
    expect(text).not.toContain('payment.planCard.quota: payment.planCard.unlimited')
  })

  it('shows current subscription management notice above subscription cards for active subscribers', async () => {
    routeState.query = {
      tab: 'subscription',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        scheduled_plan_name: 'Basic monthly',
        scheduled_plan_effective_at: '2099-02-01T00:00:00.000Z',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.subscription.currentPlanNoticeTitle')
    expect(text).toContain('Pro monthly')
    expect(text).toContain('payment.subscription.manageCurrentHint')
    expect(wrapper.findAll('.plan-card-stub').length).toBe(3)
    expect(wrapper.findAll('.active-count-stub').every(node => node.text() === '1')).toBe(true)
    expect(wrapper.find('.pending-notice-stub').text()).toContain('Basic monthly')
  })

  it('shows Pay-as-you-go and fallback guidance on the recharge tab', async () => {
    routeState.query = {}

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.paygRechargeReminderTitle')
    expect(text).toContain('payment.paygRechargeReminderBody')
    expect(text).toContain('payment.subscriptionOverflowPaygHint')
    expect(text).toContain('payment.rechargeBalanceUsageOnlyHint')
    expect(text).toContain('dashboard.balanceFallbackToggle.title')
    expect(text).toContain('dashboard.balanceFallbackToggle.disabledHint')
    expect(text).not.toContain('payment.subscriptionFallbackRequiredHint')
    expect(wrapper.find('[data-testid="payment-subscription-balance-fallback-toggle"]').exists()).toBe(true)
  })

  it('explains that checkout is unavailable when no payment methods are enabled', async () => {
    routeState.query = {}
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithoutMethodsFixture())

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.noEnabledMethods')
    expect(text).toContain('payment.noEnabledMethodsHint')
    expect(text).not.toContain('payment.createOrder')
  })

  it('saves subscription balance fallback preference from the recharge tab', async () => {
    routeState.query = {}
    const updatedUser = {
      username: 'demo-user',
      balance: 0,
      subscription_balance_fallback_enabled: true,
    }
    mockUpdateProfile.mockResolvedValue(updatedUser)

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    await wrapper.get('[data-testid="payment-subscription-balance-fallback-toggle"]').trigger('click')
    await flushPromises()

    expect(mockUpdateProfile).toHaveBeenCalledWith({ subscription_balance_fallback_enabled: true })
    expect(authState.user).toEqual(updatedUser)
    expect(wrapper.text()).toContain('dashboard.balanceFallbackToggle.enabledHint')
  })

  it('uses a generic label for universal active subscriptions without a plan name', async () => {
    routeState.query = {
      tab: 'subscription',
    }
    activeSubscriptionsState.value = [
      {
        id: 10,
        group_id: 0,
        plan_name: null,
        status: 'active',
        expires_at: null,
        seven_day_limit_usd: null,
        group: null,
      },
    ]

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('payment.subscription.genericLabel')
    expect(text).not.toContain('payment.groupFallback')
    expect(text).not.toContain('Group #0')
  })

  it('shows direct quota fields for universal active subscriptions without seven-day quota', async () => {
    routeState.query = {
      tab: 'subscription',
    }
    activeSubscriptionsState.value = [
      {
        id: 11,
        group_id: null,
        plan_name: 'Custom monthly',
        status: 'active',
        expires_at: null,
        seven_day_limit_usd: null,
        monthly_limit_usd: 500,
        group: null,
      },
    ]

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('Custom monthly')
    expect(text).toContain('userSubscriptions.monthly: $500')
    expect(text).not.toContain('payment.planCard.quota: payment.planCard.unlimited')
  })

  it('keeps subscription resume context for token-only WeChat callbacks', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-7',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue(oauthOrderFixture())

    const originalLocation = window.location
    const locationState = {
      href: 'http://localhost/purchase',
      origin: 'http://localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState,
    })

    shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      wechat_resume_token: 'resume-subscription-7',
    }))
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('opens upgrade confirmation before selecting a higher plan', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    await wrapper.findAll('.plan-card-stub').find(button => button.text().includes('Max monthly'))!.trigger('click')

    expect(wrapper.text()).toContain('payment.switchConfirm.upgradeTitle')
    expect(wrapper.text()).toContain('payment.switchConfirm.upgradeMessage')
    expect(wrapper.text()).not.toContain('Confirm Payment ¥1,599')
  })

  it('opens downgrade confirmation with next-period copy before selecting a lower plan', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    await wrapper.findAll('.plan-card-stub').find(button => button.text().includes('Basic monthly'))!.trigger('click')

    expect(wrapper.text()).toContain('payment.switchConfirm.downgradeTitle')
    expect(wrapper.text()).toContain('payment.switchConfirm.downgradeMessage')
    expect(wrapper.text()).not.toContain('Confirm Payment ¥179')
  })

  it('continues to selected-plan checkout after confirming switch modal', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    await wrapper.findAll('.plan-card-stub').find(button => button.text().includes('Max monthly'))!.trigger('click')
    await wrapper.get('.confirm-dialog-confirm').trigger('click')

    expect(wrapper.text()).toContain('Max monthly')
    expect(wrapper.text()).toContain('payment.subscription.quotaFirstHint')
    expect(wrapper.find('.confirm-dialog-stub').exists()).toBe(false)
  })

  it('canceling switch modal leaves the plan list unchanged', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    await wrapper.findAll('.plan-card-stub').find(button => button.text().includes('Basic monthly'))!.trigger('click')
    await wrapper.get('.confirm-dialog-cancel').trigger('click')

    expect(wrapper.find('.confirm-dialog-stub').exists()).toBe(false)
    expect(wrapper.findAll('.plan-card-stub').length).toBe(3)
    expect(wrapper.text()).not.toContain('payment.subscription.quotaFirstHint')
  })

  it('same-plan renew does not show switch confirmation modal', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    await wrapper.findAll('.plan-card-stub').find(button => button.text().includes('Pro monthly'))!.trigger('click')

    expect(wrapper.find('.confirm-dialog-stub').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.subscription.quotaFirstHint')
  })

  it('plan query opens switch confirmation after subscriptions load', async () => {
    routeState.query = { tab: 'subscription', plan: '8', intent: 'switch' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithMonthlyPlansFixture())
    activeSubscriptionsState.value = [
      {
        id: 9,
        group_id: 3,
        plan_id: 7,
        plan_name: 'Pro monthly',
        status: 'active',
        seven_day_limit_usd: 260,
        expires_at: '2099-01-01T00:00:00.000Z',
      },
    ]

    const wrapper = shallowMount(PaymentView, { global: { stubs: paymentViewStubs } })
    await flushPromises()
    await flushPromises()

    expect(wrapper.text()).toContain('payment.switchConfirm.upgradeTitle')
  })

  it('falls back to QR flow when mobile WeChat payment is unavailable', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-h5',
      payment_type: 'wxpay_direct',
    }
    createOrder
      .mockRejectedValueOnce({ reason: 'WECHAT_H5_NOT_AUTHORIZED' })
      .mockResolvedValueOnce({
        order_id: 778,
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=fallback-native',
        out_trade_no: 'sub2_qr_778',
      })

    shallowMount(PaymentView, {
      global: {
        stubs: paymentViewStubs,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: true,
      wechat_resume_token: 'resume-token-h5',
    }))
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }))
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('weixin://wxpay/bizpayurl?pr=fallback-native')
  })
})
