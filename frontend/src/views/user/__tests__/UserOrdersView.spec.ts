import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UserOrdersView from '../UserOrdersView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import type { PaymentOrder } from '@/types/payment'

const getMyOrders = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const getRefundEligibleProviders = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
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

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getMyOrders,
    cancelOrder,
    getRefundEligibleProviders,
  },
}))

function orderFixture(overrides: Partial<PaymentOrder> = {}): PaymentOrder {
  return {
    id: 1001,
    user_id: 77,
    amount: 20,
    pay_amount: 20,
    currency: 'CNY',
    fee_rate: 0,
    payment_type: 'alipay',
    out_trade_no: 'sub2_order_1001',
    status: 'PENDING',
    order_type: 'balance',
    created_at: '2026-06-27T10:00:00.000Z',
    expires_at: '2099-01-01T00:00:00.000Z',
    refund_amount: 0,
    ...overrides,
  }
}

function mountView() {
  return mount(UserOrdersView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        OrderTable: {
          props: ['orders', 'loading'],
          template: `
            <div>
              <div v-for="row in orders" :key="row.id" class="order-row">
                <span>{{ row.status }}</span>
                <slot name="actions" :row="row" />
              </div>
            </div>
          `,
        },
        Pagination: true,
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Select: true,
        Icon: {
          props: ['name'],
          template: '<i :data-icon="name" />',
        },
      },
    },
  })
}

function findButtonByText(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find(button => button.text().includes(text))
}

describe('UserOrdersView', () => {
  beforeEach(() => {
    localStorage.clear()
    getMyOrders.mockReset().mockResolvedValue({ data: { items: [], total: 0 } })
    cancelOrder.mockReset().mockResolvedValue({ data: {} })
    getRefundEligibleProviders.mockReset().mockResolvedValue({ data: { provider_instance_ids: ['provider-1'] } })
    routerPush.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('restores a pending order payment snapshot before returning to purchase', async () => {
    getMyOrders.mockResolvedValue({
      data: {
        items: [
          orderFixture({
            id: 2002,
            amount: 88,
            pay_amount: 88,
            qr_code: 'https://pay.example.com/qrcode/2002',
            pay_url: 'https://pay.example.com/cashier/2002',
            payment_mode: 'qrcode',
          }),
        ],
        total: 1,
      },
    })

    const wrapper = mountView()
    await flushPromises()

    const button = findButtonByText(wrapper, 'payment.orders.continuePayment')
    expect(button).toBeTruthy()
    await button!.trigger('click')

    const raw = localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)
    expect(raw).toBeTruthy()
    const snapshot = JSON.parse(raw || '{}')
    expect(snapshot).toMatchObject({
      orderId: 2002,
      amount: 88,
      payAmount: 88,
      qrCode: 'https://pay.example.com/qrcode/2002',
      payUrl: 'https://pay.example.com/cashier/2002',
      paymentType: 'alipay',
      outTradeNo: 'sub2_order_1001',
      orderType: 'balance',
      paymentMode: 'qrcode',
    })
    expect(routerPush).toHaveBeenCalledWith('/purchase')
  })

  it('does not expose user self-service refund actions', async () => {
    getMyOrders.mockResolvedValue({
      data: {
        items: [
          orderFixture({
            id: 3003,
            status: 'COMPLETED',
            provider_instance_id: 'provider-1',
          }),
        ],
        total: 1,
      },
    })

    const wrapper = mountView()
    await flushPromises()

    expect(getRefundEligibleProviders).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('payment.orders.requestRefund')
  })
})
