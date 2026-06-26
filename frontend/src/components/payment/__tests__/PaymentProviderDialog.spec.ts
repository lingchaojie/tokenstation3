import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PaymentProviderDialog from '@/components/payment/PaymentProviderDialog.vue'
import { STRIPE_SDK_API_VERSION } from '@/components/payment/providerConfig'
import type { ProviderInstance } from '@/types/payment'

const messages: Record<string, string> = {
  'admin.settings.payment.providerConfig': 'Credentials',
  'admin.settings.payment.paymentGuideTrigger': 'View payment guide',
  'admin.settings.payment.alipayGuideSummary': 'Desktop prefers QR precreate and falls back to cashier; mobile prefers WAP checkout.',
  'admin.settings.payment.wxpayGuideSummary': 'Desktop prefers Native QR; mobile routes to JSAPI or H5 based on browser context.',
  'admin.settings.payment.airwallexGuideSummary': 'Use Payment Acceptance read/write only.',
  'admin.settings.payment.stripeWebhookHint': 'Configure Stripe webhook.',
  'admin.settings.payment.stripeWebhookApiVersionHint': 'Use Stripe API version {version}.',
  'admin.settings.payment.airwallexWebhookHint': 'Select payment_intent.succeeded and use the latest stable API version.',
  'admin.settings.payment.field_pid': 'PID',
  'admin.settings.payment.field_merchantPrivateKey': 'Merchant private key',
  'admin.settings.payment.field_platformPublicKey': 'Platform public key',
  'admin.settings.payment.field_apiBase': 'API Base URL',
  'admin.settings.payment.field_ikunpayApiBaseHint': 'Use the IkunPay gateway base URL.',
  'admin.settings.payment.field_channelIdAlipay': 'IkunPay Alipay sub-channel ID',
  'admin.settings.payment.field_channelIdWxpay': 'IkunPay WeChat Pay sub-channel ID',
  'admin.settings.payment.field_channelId': 'IkunPay fallback sub-channel ID',
  'admin.settings.payment.field_ikunpayChannelIdAlipayHint': 'Use the IkunPay Alipay payment channel row ID.',
  'admin.settings.payment.field_ikunpayChannelIdWxpayHint': 'Use the IkunPay WeChat Pay payment channel row ID.',
  'admin.settings.payment.field_ikunpayChannelIdHint': 'Fallback channel ID.',
  'admin.settings.payment.modeQRCode': 'QR Code',
  'admin.settings.payment.modePopup': 'Popup',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const message = messages[key] ?? key
      if (!params) return message
      return Object.entries(params).reduce(
        (value, [name, replacement]) => value.replaceAll(`{${name}}`, replacement),
        message,
      )
    },
  }),
}))

function providerFactory(overrides: Partial<ProviderInstance> = {}): ProviderInstance {
  return {
    id: 1,
    provider_key: 'airwallex',
    name: 'Airwallex',
    config: {},
    supported_types: ['airwallex'],
    enabled: true,
    payment_mode: '',
    refund_enabled: false,
    allow_user_refund: false,
    limits: '',
    sort_order: 0,
    ...overrides,
  }
}

function mountDialog(options: { editing?: ProviderInstance | null } = {}) {
  return mount(PaymentProviderDialog, {
    props: {
      show: true,
      saving: false,
      editing: options.editing ?? null,
      allKeyOptions: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'stripe', label: 'Stripe' },
        { value: 'airwallex', label: 'Airwallex' },
        { value: 'ikunpay', label: 'IkunPay' },
      ],
      enabledKeyOptions: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'airwallex', label: 'Airwallex' },
        { value: 'ikunpay', label: 'IkunPay' },
      ],
      allPaymentTypes: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
      ],
      redirectLabel: 'Redirect',
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>',
        },
        Select: {
          props: ['modelValue', 'options', 'disabled'],
          template: '<div />',
        },
        ToggleSwitch: {
          template: '<div />',
        },
      },
    },
  })
}

describe('PaymentProviderDialog payment guide', () => {
  it('shows no payment guide for providers without a flow guide', () => {
    const wrapper = mountDialog()

    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.alipayGuideSummary'])
    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.wxpayGuideSummary'])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(false)
  })

  it.each([
    ['alipay', 'admin.settings.payment.alipayGuideSummary'],
    ['wxpay', 'admin.settings.payment.wxpayGuideSummary'],
    ['airwallex', 'admin.settings.payment.airwallexGuideSummary'],
  ])('shows the payment guide summary for %s', async (providerKey, summaryKey) => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset(providerKey)
    await nextTick()

    expect(wrapper.text()).toContain(messages[summaryKey])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(true)
  })

  it('shows Airwallex webhook event and API version guidance with the webhook URL', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('airwallex')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.airwallexWebhookHint'])
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/airwallex')
  })

  it('shows Stripe webhook API version guidance with the integrated SDK version', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('stripe')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.stripeWebhookHint'])
    expect(wrapper.text()).toContain(`Use Stripe API version ${STRIPE_SDK_API_VERSION}.`)
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/stripe')
  })

  it('emits an empty Airwallex accountId when the admin clears it', async () => {
    const provider = providerFactory({
      config: {
        clientId: 'cid_123',
        apiBase: 'https://api.airwallex.com/api/v1',
        countryCode: 'CN',
        currency: 'CNY',
        accountId: 'acct_123',
      },
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    const accountIdInput = wrapper
      .findAll('input[type="text"]')
      .find(input => (input.element as HTMLInputElement).value === 'acct_123')
    if (!accountIdInput) throw new Error('accountId input not found')

    await accountIdInput.setValue('')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config.accountId).toBe('')
  })

  it('creates an IkunPay provider with qrcode mode, callbacks, and Alipay/WeChat support', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('ikunpay')
    await nextTick()

    expect(wrapper.text()).toContain('Merchant private key')
    expect(wrapper.text()).toContain('Platform public key')
    expect(wrapper.text()).toContain('Use the IkunPay gateway base URL.')
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/ikunpay')
    expect(wrapper.text()).toContain('/payment/result')
    expect(wrapper.text()).toContain('QR Code')
    expect(wrapper.text()).toContain('Popup')

    const textInputs = wrapper.findAll('input[type="text"]')
    await textInputs[0].setValue('IkunPay')
    await textInputs[1].setValue('ikunpay-pid')
    expect((textInputs[2].element as HTMLInputElement).value).toBe('https://ikunpay.com/')
    await textInputs[3].setValue('2644')
    await textInputs[4].setValue('3785')
    await textInputs[5].setValue('3786')
    await textInputs[7].setValue('https://pay.example.com')
    await textInputs[8].setValue('https://app.example.com')

    const keyTextareas = wrapper.findAll('textarea')
    await keyTextareas[0].setValue('merchant-private-key')
    await keyTextareas[1].setValue('platform-public-key')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      provider_key: string
      payment_mode: string
      supported_types: string[]
      config: Record<string, string>
    }
    expect(payload.provider_key).toBe('ikunpay')
    expect(payload.payment_mode).toBe('qrcode')
    expect(payload.supported_types).toEqual(['alipay', 'wxpay'])
    expect(payload.config).toMatchObject({
      pid: 'ikunpay-pid',
      merchantPrivateKey: 'merchant-private-key',
      platformPublicKey: 'platform-public-key',
      apiBase: 'https://ikunpay.com/',
      merchantId: '2644',
      channelIdAlipay: '3785',
      channelIdWxpay: '3786',
      notifyUrl: 'https://pay.example.com/api/v1/payment/webhook/ikunpay',
      returnUrl: 'https://app.example.com/payment/result',
    })
  })

  it('edits an IkunPay provider with default qrcode mode and existing callback bases', async () => {
    const provider = providerFactory({
      provider_key: 'ikunpay',
      name: 'IkunPay',
      config: {
        pid: 'existing-pid',
        apiBase: 'https://ikunpay.com/',
        merchantId: '2644',
        channelIdAlipay: '3785',
        channelIdWxpay: '3786',
        notifyUrl: 'https://pay.example.com/api/v1/payment/webhook/ikunpay',
        returnUrl: 'https://app.example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: '',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    expect(wrapper.text()).toContain('Merchant private key')
    expect(wrapper.text()).toContain('Platform public key')
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/ikunpay')

    const values = wrapper
      .findAll('input[type="text"]')
      .map(input => (input.element as HTMLInputElement).value)
    expect(values).toContain('existing-pid')
    expect(values).toContain('https://ikunpay.com/')
    expect(values).toContain('2644')
    expect(values).toContain('3785')
    expect(values).toContain('3786')
    expect(values).toContain('https://pay.example.com')
    expect(values).toContain('https://app.example.com')

    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      payment_mode: string
      supported_types: string[]
      config: Record<string, string>
    }
    expect(payload.payment_mode).toBe('qrcode')
    expect(payload.supported_types).toEqual(['alipay', 'wxpay'])
    expect(payload.config).toMatchObject({
      pid: 'existing-pid',
      apiBase: 'https://ikunpay.com/',
      merchantId: '2644',
      channelIdAlipay: '3785',
      channelIdWxpay: '3786',
      notifyUrl: 'https://pay.example.com/api/v1/payment/webhook/ikunpay',
      returnUrl: 'https://app.example.com/payment/result',
    })
  })

  it('emits empty IkunPay optional routing fields when the admin clears them', async () => {
    const provider = providerFactory({
      provider_key: 'ikunpay',
      name: 'IkunPay',
      config: {
        pid: 'existing-pid',
        apiBase: 'https://ikunpay.com/',
        merchantId: '2644',
        channelIdAlipay: '3785',
        channelIdWxpay: '3786',
        channelId: 'legacy-channel',
        notifyUrl: 'https://pay.example.com/api/v1/payment/webhook/ikunpay',
        returnUrl: 'https://app.example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    const textInputs = wrapper.findAll('input[type="text"]')
    const merchantIdInput = textInputs.find(input => (input.element as HTMLInputElement).value === '2644')
    const alipayChannelInput = textInputs.find(input => (input.element as HTMLInputElement).value === '3785')
    const wxpayChannelInput = textInputs.find(input => (input.element as HTMLInputElement).value === '3786')
    const fallbackChannelInput = textInputs.find(input => (input.element as HTMLInputElement).value === 'legacy-channel')
    if (!merchantIdInput || !alipayChannelInput || !wxpayChannelInput || !fallbackChannelInput) {
      throw new Error('IkunPay optional routing inputs not found')
    }

    await merchantIdInput.setValue('')
    await alipayChannelInput.setValue('')
    await wxpayChannelInput.setValue('')
    await fallbackChannelInput.setValue('')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config).toMatchObject({
      merchantId: '',
      channelIdAlipay: '',
      channelIdWxpay: '',
      channelId: '',
    })
  })
})
