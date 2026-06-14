import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '../../../components/payment/paymentFlow'

const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
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
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      username: 'demo-user',
      balance: 0,
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

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => true,
}))

function checkoutInfoFixture() {
  return {
    data: {
      methods: {
        wxpay: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_rate: 0,
          available: true,
        },
      },
      global_min: 0,
      global_max: 0,
      plans: [],
      balance_disabled: false,
      balance_recharge_multiplier: 1,
      recharge_fee_rate: 0,
      help_text: '',
      help_image_url: '',
      stripe_publishable_key: '',
    },
  }
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

function checkoutInfoWithPlansFixture() {
  return {
    data: {
      ...checkoutInfoFixture().data,
      plans: [monthlyPlanFixture(7, 'Plus monthly', 399, 110)],
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
    props: ['plan'],
    emits: ['select'],
    template: '<button class="plan-card-stub" type="button" @click="$emit(\'select\', plan)">{{ plan.name }}</button>',
  },
  ConfirmDialog: {
    props: ['show', 'title', 'message'],
    emits: ['confirm', 'cancel'],
    template: '<section v-if="show" class="confirm-dialog-stub"><h2>{{ title }}</h2><p>{{ message }}</p><button class="confirm-dialog-confirm" @click="$emit(\'confirm\')">confirm</button><button class="confirm-dialog-cancel" @click="$emit(\'cancel\')">cancel</button></section>',
  },
}

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
