import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const userDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const paymentComponentDir = resolve(userDir, '..', '..', 'components/payment')
const paymentSource = readFileSync(resolve(userDir, 'PaymentView.vue'), 'utf8')
const ordersSource = readFileSync(resolve(userDir, 'UserOrdersView.vue'), 'utf8')
const subscriptionsSource = readFileSync(resolve(userDir, 'SubscriptionsView.vue'), 'utf8')
const planCardSource = readFileSync(resolve(paymentComponentDir, 'SubscriptionPlanCard.vue'), 'utf8')
const methodSelectorSource = readFileSync(resolve(paymentComponentDir, 'PaymentMethodSelector.vue'), 'utf8')
const paymentStatusSource = readFileSync(resolve(paymentComponentDir, 'PaymentStatusPanel.vue'), 'utf8')
const requiredSelectedClasses = [
  'border-primary-500',
  'bg-primary-50',
  'dark:border-primary-500/60',
  'dark:bg-primary-500/10',
]

describe('Payment Linear page contract', () => {
  it('uses Linear pricing and payment surfaces without changing payment state code', () => {
    expect(paymentSource).toContain('linear-payment-page')
    expect(paymentSource).toContain('linx-panel')
    expect(paymentSource).toContain("paymentPhase = ref<'select' | 'paying'>")
    expect(paymentSource).toContain('decidePaymentLaunch')
  })

  it('applies Linear surfaces to order and subscription pages', () => {
    expect(ordersSource).toContain('linear-orders-page')
    expect(subscriptionsSource).toContain('linear-subscriptions-page')
  })

  it('uses restrained pricing-card components', () => {
    expect(planCardSource).toContain('linear-plan-card')
    expect(planCardSource).toContain('linx-panel')
    expect(methodSelectorSource).toContain('linear-method-option')
    expect(paymentStatusSource).toContain('linx-panel')
  })

  it('keeps the base selected state on supported payment provider branches', () => {
    const selectedClass = methodSelectorSource.match(/const SELECTED_METHOD_CLASS = '([^']+)'/)?.[1]

    expect(selectedClass).toBeDefined()
    for (const className of requiredSelectedClasses) {
      expect(selectedClass, `selected state includes ${className}`).toContain(className)
    }
    expect(methodSelectorSource).toContain('if (isBuiltInAlipayMethod(type)) return SELECTED_METHOD_CLASS')
    expect(methodSelectorSource).toContain('if (isBuiltInWxpayMethod(type)) return SELECTED_METHOD_CLASS')
    expect(methodSelectorSource).toContain("if (type === 'stripe') return SELECTED_METHOD_CLASS")
    expect(methodSelectorSource).toContain("if (type === 'airwallex') return SELECTED_METHOD_CLASS")
  })
})
