import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))

const routerPush = vi.hoisted(() => vi.fn())
const pollOrderStatus = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const verifyOrderPublic = vi.hoisted(() => vi.fn())
const resolveOrderPublicByResumeToken = vi.hoisted(() => vi.fn())
const getCurrentUser = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({ user: { id: 9, balance: 0 } }))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
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
  useAuthStore: () => authState,
}))

vi.mock('@/api/auth', () => ({ getCurrentUser }))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getOrder: async (id: number) => ({ data: await pollOrderStatus(id) }),
    verifyOrder,
    verifyOrderPublic,
    resolveOrderPublicByResumeToken,
  },
}))

import PaymentResultView from '../PaymentResultView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'

const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2026-04-20T12:30:00Z',
  refund_amount: 0,
})

const recoverySnapshotFactory = (resumeToken: string) => ({
  orderId: 42,
  amount: 88,
  qrCode: '',
  expiresAt: '2099-01-01T00:10:00.000Z',
  paymentType: 'alipay',
  payUrl: 'https://pay.example.com/session/42',
  outTradeNo: 'sub2_20260420abcd1234',
  clientSecret: '',
  intentId: '',
  currency: '',
  countryCode: '',
  paymentEnv: '',
  payAmount: 88,
  orderType: 'balance',
  paymentMode: 'popup',
  resumeToken,
  createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
})

describe('PaymentResultView', () => {
  beforeEach(() => {
    routeState.query = {}
    routerPush.mockReset()
    pollOrderStatus.mockReset()
    verifyOrder.mockReset()
    verifyOrderPublic.mockReset()
    resolveOrderPublicByResumeToken.mockReset()
    getCurrentUser.mockReset().mockResolvedValue({ data: { id: 9, balance: 88 } })
    authState.user = { id: 9, balance: 0 }
    window.localStorage.clear()
    localStorage.setItem('auth_user', JSON.stringify({ id: 9, balance: 0, username: 'current-user' }))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('does not use a late authenticated order after the browser changes accounts', async () => {
    routeState.query = { order_id: '42' }
    localStorage.setItem('auth_user', JSON.stringify({ id: 9 }))
    const recovery = JSON.stringify(recoverySnapshotFactory(''))
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, recovery)
    localStorage.setItem('payment.recovery.stripe.42', recovery)
    let complete!: (order: ReturnType<typeof orderFactory>) => void
    pollOrderStatus.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)

    localStorage.setItem('auth_user', JSON.stringify({ id: 10 }))
    complete(orderFactory('COMPLETED'))
    await flushPromises()

    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('88.00')
    expect(getCurrentUser).not.toHaveBeenCalled()
    expect(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBe(recovery)
    expect(localStorage.getItem('payment.recovery.stripe.42')).toBe(recovery)
    wrapper.unmount()
  })

  it('uses an unowned legacy snapshot after the server confirms the signed-in owner and order', async () => {
    routeState.query = { order_id: '42' }
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      ...recoverySnapshotFactory(''),
      currency: 'HKD',
    }))
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PAID'))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain(formatPaymentAmount(88, 'HKD'))
    expect(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    wrapper.unmount()
  })

  it('clears private status and drops an in-flight refresh after an account change', async () => {
    vi.useFakeTimers()
    routeState.query = { order_id: '42' }
    localStorage.setItem('auth_user', JSON.stringify({ id: 9 }))
    let complete!: (order: ReturnType<typeof orderFactory>) => void
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PENDING'))
      .mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('payment.result.processing')
    await vi.advanceTimersByTimeAsync(2000)
    expect(pollOrderStatus).toHaveBeenCalledTimes(2)

    const newUser = JSON.stringify({ id: 10 })
    localStorage.setItem('auth_user', newUser)
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_user', newValue: newUser }))
    await flushPromises()
    expect(wrapper.text()).not.toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('88.00')
    complete(orderFactory('COMPLETED'))
    await vi.advanceTimersByTimeAsync(4000)
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(pollOrderStatus).toHaveBeenCalledTimes(2)
    expect(getCurrentUser).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not schedule another status request after an in-flight poll finishes on an unmounted result', async () => {
    vi.useFakeTimers()
    routeState.query = { resume_token: 'resume-pending' }
    let complete!: (result: { data: ReturnType<typeof orderFactory> }) => void
    resolveOrderPublicByResumeToken.mockResolvedValueOnce({ data: orderFactory('PENDING') })
      .mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)

    wrapper.unmount()
    complete({ data: orderFactory('PENDING') })
    await vi.advanceTimersByTimeAsync(4000)
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)
  })

  it('allows a signed result to settle across an account change without private side effects', async () => {
    routeState.query = { resume_token: 'resume-public' }
    localStorage.setItem('auth_user', JSON.stringify({ id: 9 }))
    const recovery = JSON.stringify(recoverySnapshotFactory('resume-public'))
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, recovery)
    let complete!: (result: { data: ReturnType<typeof orderFactory> }) => void
    resolveOrderPublicByResumeToken.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    localStorage.setItem('auth_user', JSON.stringify({ id: 10 }))
    complete({ data: orderFactory('COMPLETED') })
    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.success')
    expect(getCurrentUser).not.toHaveBeenCalled()
    expect(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBe(recovery)
    wrapper.unmount()
  })

  it('preserves the replacement profile when the completion balance request finishes late', async () => {
    routeState.query = { resume_token: 'resume-public' }
    resolveOrderPublicByResumeToken.mockResolvedValueOnce({ data: orderFactory('COMPLETED') })
    let complete!: (result: { data: { id: number; balance: number } }) => void
    getCurrentUser.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    expect(getCurrentUser).toHaveBeenCalledTimes(1)

    const nextUser = { id: 10, balance: 21 }
    localStorage.setItem('auth_user', JSON.stringify(nextUser))
    authState.user = { ...nextUser }
    complete({ data: { id: 9, balance: 88 } })
    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.success')
    expect(authState.user).toEqual(nextUser)
    expect(JSON.parse(localStorage.getItem('auth_user') || '{}')).toEqual(nextUser)
    wrapper.unmount()
  })

  it('falls back to the public status for an explicit reference if private verification finishes after a switch', async () => {
    routeState.query = { out_trade_no: 'legacy-42', trade_status: 'TRADE_SUCCESS' }
    let complete!: (result: { data: ReturnType<typeof orderFactory> }) => void
    verifyOrder.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    verifyOrderPublic.mockResolvedValueOnce({ data: { out_trade_no: 'legacy-42', status: 'PAID' } })
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    expect(verifyOrder).toHaveBeenCalledWith('legacy-42')

    localStorage.setItem('auth_user', JSON.stringify({ id: 10 }))
    complete({ data: orderFactory('COMPLETED') })
    await flushPromises()

    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-42')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('legacy-42')
    expect(wrapper.text()).not.toContain('88.00')
    expect(getCurrentUser).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('renders a pending state instead of a failure state when the restored order is still pending', async () => {
    routeState.query = {
      resume_token: 'resume-42',
      order_id: '999',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'redirect',
      resumeToken: 'resume-42',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('PENDING'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-42')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('prefers the public resume-token result over a stale restored DB snapshot', async () => {
    routeState.query = {
      resume_token: 'resume-authoritative',
      order_id: '42',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      ownerId: 9,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-authoritative',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-authoritative')
    expect(getCurrentUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('103.00')
    expect(wrapper.text()).toContain('100.00')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('waits for completed fulfillment before refreshing the user balance', async () => {
    vi.useFakeTimers()
    routeState.query = {
      resume_token: 'resume-77',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify({ ...recoverySnapshotFactory('resume-77'), ownerId: 9 }),
    )
    resolveOrderPublicByResumeToken
      .mockResolvedValueOnce({
        data: orderFactory('PENDING'),
      })
      .mockResolvedValueOnce({
        data: orderFactory('COMPLETED'),
      })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(getCurrentUser).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)
    expect(getCurrentUser).toHaveBeenCalledTimes(1)
    expect(authState.user.balance).toBe(88)
    expect(JSON.parse(localStorage.getItem('auth_user') || '{}')).toEqual({ id: 9, balance: 88, username: 'current-user' })
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('keeps the successful result when refreshing the user balance fails', async () => {
    routeState.query = {
      resume_token: 'resume-refresh-failure',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })
    getCurrentUser.mockRejectedValueOnce(new Error('profile refresh failed'))

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(getCurrentUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('keeps a newer checkout snapshot when the previous result settles', async () => {
    vi.useFakeTimers()
    routeState.query = { resume_token: 'resume-old' }
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify(recoverySnapshotFactory('resume-old')))
    resolveOrderPublicByResumeToken
      .mockResolvedValueOnce({ data: orderFactory('PENDING') })
      .mockResolvedValueOnce({ data: orderFactory('COMPLETED') })
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()

    const newer = JSON.stringify({ ...recoverySnapshotFactory('resume-new'), orderId: 1000 })
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, newer)
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBe(newer)
    wrapper.unmount()
  })

  it('falls back to order_id polling when resume-token recovery fails and keeps an unrelated snapshot', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      order_id: '77',
    }
    const unrelatedRecovery = JSON.stringify({ ...recoverySnapshotFactory('resume-fail'), ownerId: 9 })
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, unrelatedRecovery)
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    pollOrderStatus.mockResolvedValueOnce({
      ...orderFactory('PAID'),
      id: 77,
      out_trade_no: 'order-77-reference',
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(pollOrderStatus).toHaveBeenCalledWith(77)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBe(unrelatedRecovery)
  })

  it('falls back to public out_trade_no verification when resume_token recovery fails in legacy return flows', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      out_trade_no: 'legacy-should-not-run',
      trade_status: 'TRADE_SUCCESS',
    }
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    verifyOrderPublic.mockResolvedValueOnce({
      data: {
        ...orderFactory('PAID'),
        out_trade_no: 'legacy-should-not-run',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-should-not-run')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('ignores a stale global recovery snapshot when legacy return markers do not identify the order', async () => {
    routeState.query = {
      trade_status: 'TRADE_SUCCESS',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-stale')),
    )

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.failed')
    expect(wrapper.text()).not.toContain('sub2_20260420abcd1234')
  })

  it('uses public out_trade_no verification when no signed resume context is available', async () => {
    routeState.query = {
      out_trade_no: 'legacy-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: orderFactory('PAID'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('legacy-123')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-123')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('renders the minimal public out_trade_no verification result without payment_type', async () => {
    routeState.query = {
      out_trade_no: 'legacy-minimal',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: {
        out_trade_no: 'legacy-minimal',
        status: 'PAID',
        paid: true,
        created_at: '2026-04-20T12:00:00Z',
        expires_at: '2026-04-20T12:30:00Z',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('legacy-minimal')
    expect(wrapper.text()).not.toContain('payment.orders.paymentMethod')
  })

  it('prefers authenticated order verification before falling back to public lookup', async () => {
    routeState.query = {
      out_trade_no: 'auth-verify-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('auth-verify-123')
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('does not use public out_trade_no verification for bare order numbers without legacy return markers', async () => {
    routeState.query = {
      out_trade_no: 'legacy-bare',
    }

    mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrderPublic).not.toHaveBeenCalled()
  })

  it('resolves order by resume token when local recovery snapshot is missing', async () => {
    routeState.query = {
      resume_token: 'resume-77',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('PAID'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-77')
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('uses the currency returned by the order API when rendering amounts', async () => {
    routeState.query = {
      resume_token: 'resume-hkd',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('PAID'),
        currency: 'HKD',
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(103, 'HKD'))
  })

  it('normalizes aliased payment methods before rendering the label', async () => {
    routeState.query = {
      resume_token: 'resume-88',
    }
    resolveOrderPublicByResumeToken.mockResolvedValueOnce({
      data: {
        ...orderFactory('PAID'),
        payment_type: 'alipay_direct',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.methods.alipay')
    expect(wrapper.text()).not.toContain('payment.methods.alipay_direct')
  })
})

describe('PaymentResultView checkout ownership', () => {
  const prepare = (user: { id: number } | null, snapshotOwner?: number) => {
    window.localStorage.clear()
    if (user) localStorage.setItem('auth_user', JSON.stringify(user))
    routeState.query = { order_id: '42' }
    const raw = JSON.stringify({ ...recoverySnapshotFactory(''), ownerId: snapshotOwner })
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, raw)
    pollOrderStatus.mockReset().mockRejectedValue({ status: 403 })
    verifyOrder.mockReset().mockRejectedValue({ status: 403 })
    verifyOrderPublic.mockReset().mockResolvedValue({ data: { out_trade_no: orderFactory('COMPLETED').out_trade_no, status: 'COMPLETED' } })
    return raw
  }

  it('preserves an unowned legacy recovery across an account replacement', async () => {
    const raw = prepare({ id: 10 })
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    const text = wrapper.text()
    const recovery = localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)
    wrapper.unmount()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(text).not.toContain(orderFactory('COMPLETED').out_trade_no)
    expect(recovery).toBe(raw)
  })

  it('does not use an owner-bound stored trade reference while signed out', async () => {
    const raw = prepare(null, 9)
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    const text = wrapper.text()
    const recovery = localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)
    wrapper.unmount()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(text).not.toContain(orderFactory('COMPLETED').out_trade_no)
    expect(recovery).toBe(raw)
  })
})
