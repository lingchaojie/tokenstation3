import { describe, expect, it } from 'vitest'
import {
  PAYMENT_CURRENCY_OPTIONS,
  PROVIDER_CALLBACK_PATHS,
  PROVIDER_CONFIG_FIELDS,
  PROVIDER_SUPPORTED_TYPES,
  RETURN_PATH,
  WEBHOOK_PATHS,
} from '@/components/payment/providerConfig'

function findField(providerKey: string, key: string) {
  const fields = PROVIDER_CONFIG_FIELDS[providerKey] || []
  return fields.find(field => field.key === key)
}

describe('PROVIDER_CONFIG_FIELDS.wxpay', () => {
  it('keeps admin form validation aligned with backend-required credentials', () => {
    expect(findField('wxpay', 'publicKeyId')?.optional).toBeFalsy()
    expect(findField('wxpay', 'certSerial')?.optional).toBeFalsy()
  })

  it('only keeps the simplified visible credential set in the admin form', () => {
    expect(findField('wxpay', 'mpAppId')).toBeUndefined()
    expect(findField('wxpay', 'h5AppName')).toBeUndefined()
    expect(findField('wxpay', 'h5AppUrl')).toBeUndefined()
  })
})

describe('PROVIDER_CONFIG_FIELDS.airwallex', () => {
  it('adds currency config with CNY as the default', () => {
    const currency = findField('airwallex', 'currency')

    expect(currency?.defaultValue).toBe('CNY')
    expect(currency?.hintKey).toBe('admin.settings.payment.field_paymentCurrencyHint')
    expect(currency?.options).toBe(PAYMENT_CURRENCY_OPTIONS)
  })

  it('marks accountId as optional and explains when it can be left blank', () => {
    const accountId = findField('airwallex', 'accountId')

    expect(accountId?.optional).toBe(true)
    expect(accountId?.clearable).toBe(true)
    expect(accountId?.hintKey).toBe('admin.settings.payment.field_accountIdHint')
  })

  it('explains that apiBase must match the Airwallex key environment', () => {
    expect(findField('airwallex', 'apiBase')?.hintKey).toBe('admin.settings.payment.field_airwallexApiBaseHint')
  })
})

describe('PROVIDER_CONFIG_FIELDS.stripe', () => {
  it('adds currency config with CNY as the default', () => {
    const currency = findField('stripe', 'currency')

    expect(currency?.defaultValue).toBe('CNY')
    expect(currency?.hintKey).toBe('admin.settings.payment.field_paymentCurrencyHint')
    expect(currency?.options).toBe(PAYMENT_CURRENCY_OPTIONS)
  })
})

describe('PROVIDER_CONFIG_FIELDS.ikunpay', () => {
  it('supports Alipay and WeChat Pay through IkunPay callbacks', () => {
    expect(PROVIDER_SUPPORTED_TYPES.ikunpay).toEqual(['alipay', 'wxpay'])
    expect(WEBHOOK_PATHS.ikunpay).toBe('/api/v1/payment/webhook/ikunpay')
    expect(PROVIDER_CALLBACK_PATHS.ikunpay).toEqual({
      notifyUrl: WEBHOOK_PATHS.ikunpay,
      returnUrl: RETURN_PATH,
    })
  })

  it('defines the required IkunPay credentials and default API base', () => {
    expect(findField('ikunpay', 'pid')).toMatchObject({ sensitive: false })
    expect(findField('ikunpay', 'merchantPrivateKey')).toMatchObject({ sensitive: true })
    expect(findField('ikunpay', 'platformPublicKey')).toMatchObject({ sensitive: true })
    expect(findField('ikunpay', 'apiBase')).toMatchObject({
      sensitive: false,
      defaultValue: 'https://ikunpay.com/',
      hintKey: 'admin.settings.payment.field_ikunpayApiBaseHint',
    })
    expect(findField('ikunpay', 'merchantId')).toMatchObject({
      sensitive: false,
      optional: true,
      clearable: true,
      hintKey: 'admin.settings.payment.field_ikunpayMerchantIdHint',
    })
    expect(findField('ikunpay', 'channelIdAlipay')).toMatchObject({
      sensitive: false,
      optional: true,
      clearable: true,
      hintKey: 'admin.settings.payment.field_ikunpayChannelIdAlipayHint',
    })
    expect(findField('ikunpay', 'channelIdWxpay')).toMatchObject({
      sensitive: false,
      optional: true,
      clearable: true,
      hintKey: 'admin.settings.payment.field_ikunpayChannelIdWxpayHint',
    })
    expect(findField('ikunpay', 'channelId')).toMatchObject({
      sensitive: false,
      optional: true,
      clearable: true,
      hintKey: 'admin.settings.payment.field_ikunpayChannelIdHint',
    })
  })
})
