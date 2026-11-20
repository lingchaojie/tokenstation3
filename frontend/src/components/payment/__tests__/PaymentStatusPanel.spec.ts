import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getOrder: async (id: number) => ({ data: await pollOrderStatus(id) }),
    cancelOrder,
    verifyOrder,
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas,
  },
}))

import PaymentStatusPanel from '../PaymentStatusPanel.vue'

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
  expires_at: '2099-01-01T12:30:00Z',
  refund_amount: 0,
})

describe('PaymentStatusPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides payable controls immediately when active verification reports a hold', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: {
      ...orderFactory('PENDING'), provider_key: 'easypay', confirmation_pending: true,
    } })
    const wrapper = mount(PaymentStatusPanel, {
      props: { orderId: 42, qrCode: 'old-qr', payUrl: 'https://pay.example.com/42',
        expiresAt: '2099-01-01T12:30:00Z', paymentType: 'alipay' },
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.text()).toContain('payment.qr.confirmationPending')
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.emitted('confirmation-pending')).toEqual([[]])
    wrapper.unmount()
  })

  it('stops polling when the authenticated order lookup denies access', async () => {
    pollOrderStatus.mockRejectedValue({ status: 403 })
    const wrapper = mount(PaymentStatusPanel, {
      props: { orderId: 42, qrCode: '', expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay', confirmationPending: true },
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await vi.advanceTimersByTimeAsync(9000)
    expect(pollOrderStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('unavailable')).toEqual([[]])
    wrapper.unmount()
  })

  it('treats RECHARGING as a successful terminal state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('shows reopen button in QR mode when payUrl is also available', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false } as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')

    await wrapper.get('button.btn.btn-secondary.text-sm').trigger('click')
    expect(openSpy).toHaveBeenCalledWith(
      'https://pay.example.com/session/42',
      'paymentPopup',
      expect.any(String),
    )

    openSpy.mockRestore()
  })

  it('uses generic QR copy for custom methods that contain built-in names', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'card_alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.qr.scanToPay')
    expect(wrapper.text()).not.toContain('payment.qr.scanAlipay')
  })

  it('actively verifies a stuck pending order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('actively verifies a stuck alipay-source QR order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('PENDING'),
      payment_type: 'alipay',
    })
    verifyOrder.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        payment_type: 'alipay',
      },
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/example',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
  })

  it.each(['alipay', 'wxpay', 'ikunpay', 'easypay'])('actively verifies pending %s orders without retrying within 15 seconds', async (paymentType) => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PENDING'), payment_type: paymentType })
    verifyOrder.mockResolvedValue({ data: { ...orderFactory('PENDING'), payment_type: paymentType } })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType,
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(verifyOrder).toHaveBeenCalledTimes(1)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')

    await vi.advanceTimersByTimeAsync(12000)
    await flushPromises()
    expect(verifyOrder).toHaveBeenCalledTimes(1)
    expect(cancelOrder).not.toHaveBeenCalled()
    expect(wrapper.emitted('success')).toBeUndefined()

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(verifyOrder).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it.each([
    ['QR', 'https://pay.example.com/qr/42', 'COMPLETED', 'success'],
    ['redirect', '', 'EXPIRED', 'expired'],
  ])('waits for authoritative EasyPay status after the %s countdown ends', async (_mode, qrCode, terminalStatus, outcome) => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('PENDING') })
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode,
        expiresAt: new Date(Date.now() + 1000).toISOString(),
        paymentType: 'alipay',
        providerKey: 'easypay',
        payUrl: 'https://pay.example.com/session/42',
      },
      global: { stubs: { Icon: true, RouterLink: true } },
    })

    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.text()).toContain('payment.qr.confirmationPending')
    expect(wrapper.text()).not.toContain('payment.qr.openPayWindow')
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.emitted('confirmation-pending')).toBeUndefined()
    expect(pollOrderStatus).toHaveBeenCalledTimes(1)
    expect(verifyOrder).toHaveBeenCalledTimes(1)

    pollOrderStatus.mockResolvedValue(orderFactory(terminalStatus))
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.emitted('settled')).toEqual([[outcome]])
    expect(verifyOrder).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('waits for an order without provider metadata when cancellation returns the typed confirmation hold', async () => {
    cancelOrder.mockImplementation(() => new Promise((_resolve, reject) => {
      setTimeout(() => reject({ status: 409, reason: 'PAYMENT_CONFIRMATION_PENDING' }), 2500)
    }))
    pollOrderStatus.mockResolvedValue(orderFactory('PAID'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: new Date(Date.now() + 2000).toISOString(),
        paymentType: 'alipay',
      },
      global: { stubs: { Icon: true, RouterLink: true } },
    })

    await wrapper.get('button').trigger('click')
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.emitted('settled')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(500)
    expect(wrapper.text()).toContain('payment.qr.confirmationPending')
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.emitted('confirmation-pending')).toEqual([[]])
    expect(showError).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(500)
    expect(wrapper.emitted('settled')).toEqual([['success']])
    wrapper.unmount()
  })

  it('restores a typed hold before the payment deadline without reoffering payment', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        confirmationPending: true,
      },
      global: { stubs: { RouterLink: true } },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.confirmationPending')
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('payment.qr.openPayWindow')
    expect(wrapper.emitted('settled')).toBeUndefined()
    wrapper.unmount()
  })

  it.each([undefined, 'alipay'])('keeps the checkout timeout behavior for provider %s without a typed hold', async (providerKey) => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: new Date(Date.now() + 1000).toISOString(),
        paymentType: 'alipay',
        providerKey,
      },
      global: { stubs: { Icon: true } },
    })

    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.text()).toContain('payment.qr.expired')
    expect(wrapper.emitted('settled')).toEqual([['expired']])
    expect(pollOrderStatus).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
