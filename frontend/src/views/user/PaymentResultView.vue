<template>
  <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4 dark:bg-dark-900">
    <div class="w-full max-w-md space-y-6">
      <!-- Loading -->
      <div v-if="loading" class="flex items-center justify-center py-20">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>
      <template v-else>
        <!-- Status Icon -->
        <div class="text-center">
          <div v-if="isSuccess"
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
            <svg class="h-10 w-10 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"
              stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <div v-else-if="isPending"
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-yellow-100 dark:bg-yellow-900/30">
            <div class="h-10 w-10 animate-spin rounded-full border-4 border-yellow-500 border-t-transparent"></div>
          </div>
          <div v-else
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/30">
            <svg class="h-10 w-10 text-red-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <h2 class="mt-4 text-2xl font-bold text-gray-900 dark:text-white">
            {{ statusTitle }}
          </h2>
          <p v-if="isPending" class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('payment.result.processingHint') }}
          </p>
        </div>
        <!-- Order Info -->
        <div v-if="order" class="rounded-xl bg-white p-5 shadow-sm dark:bg-dark-800">
          <div class="space-y-3 text-sm">
            <div v-if="hasOrderId(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">#{{ order.id }}</span>
            </div>
            <div v-if="order.out_trade_no" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ order.out_trade_no }}</span>
            </div>
            <div v-if="hasAmountFields(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.baseAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(baseAmount) }}</span>
            </div>
            <div v-if="hasAmountFields(order) && order.fee_rate > 0" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.fee') }} ({{ order.fee_rate }}%)</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(feeAmount) }}</span>
            </div>
            <div v-if="hasAmountFields(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
              <span class="font-bold text-primary-600 dark:text-primary-400">{{ formatGatewayAmount(order.pay_amount) }}</span>
            </div>
            <div v-if="hasAmountFields(order) && orderBonusAmount(order) > 0" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.bonusAmount') }}</span>
              <span class="font-medium text-amber-600 dark:text-amber-400">+${{ orderBonusAmount(order).toFixed(2) }}</span>
            </div>
            <div v-if="hasAmountFields(order) && (order.amount !== order.pay_amount || orderBonusAmount(order) > 0)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ order.order_type === 'balance' ? '$' + order.amount.toFixed(2) : formatGatewayAmount(order.amount) }}</span>
            </div>
            <div v-if="hasPaymentType(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ t(paymentMethodI18nKey(order.payment_type), normalizedOrderPaymentType(order.payment_type)) }}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</span>
              <OrderStatusBadge :status="displayOrderStatus(order.status)" />
            </div>
          </div>
        </div>
        <!-- EasyPay return info (when no order loaded) -->
        <div v-else-if="returnInfo" class="rounded-xl bg-white p-5 shadow-sm dark:bg-dark-800">
          <div class="space-y-3 text-sm">
            <div v-if="returnInfo.outTradeNo" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ returnInfo.outTradeNo }}</span>
            </div>
            <div v-if="returnInfo.money" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(Number(returnInfo.money) || 0) }}</span>
            </div>
            <div v-if="returnInfo.type" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ t(paymentMethodI18nKey(returnInfo.type), normalizedOrderPaymentType(returnInfo.type)) }}</span>
            </div>
          </div>
        </div>
        <!-- Actions -->
        <div class="flex gap-3">
          <button class="btn btn-secondary flex-1" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          <button class="btn btn-primary flex-1" @click="router.push('/orders')">{{ t('payment.result.viewOrders') }}</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  clearPaymentRecoverySnapshot,
  clearStripePaymentRecoverySnapshot,
  readPaymentRecoverySnapshot,
  readStripePaymentRecoverySnapshot,
} from '@/components/payment/paymentFlow'
import { useAuthStore } from '@/stores/auth'
import { getCurrentUser } from '@/api/auth'
import { captureStoredUserIdentity, getStoredUserID } from '@/api/tokenRefresh'
import { paymentAPI } from '@/api/payment'
import type { PublicOrderVerifyResult } from '@/api/payment'
import type { OrderStatus, PaymentOrder } from '@/types/payment'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import { normalizePaymentMethodForDisplay, paymentMethodI18nKey } from './paymentUx'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const ownerId = getStoredUserID()
const isOriginalUser = captureStoredUserIdentity()

type ResolvedOrder = PaymentOrder | PublicOrderVerifyResult
type OrderLookup = { data: ResolvedOrder; authenticated: boolean; signed?: true }

const order = ref<ResolvedOrder | null>(null)
const loading = ref(true)
const currency = ref('CNY')

interface ReturnInfo {
  outTradeNo: string
  money: string
  type: string
  tradeStatus: string
}
const returnInfo = ref<ReturnInfo | null>(null)

const SUCCESS_STATUSES = new Set(['COMPLETED', 'PAID', 'RECHARGING'])
const PENDING_STATUSES = new Set(['PENDING', 'CREATED', 'WAITING', 'PROCESSING'])
const STATUS_REFRESH_INTERVAL_MS = 2000
const STATUS_REFRESH_MAX_ATTEMPTS = 15

let statusRefreshTimer: ReturnType<typeof setTimeout> | null = null
let userBalanceRefreshStarted = false
let disposed = false
let ownerChanged = false
let hasPrivateOrder = false
const refreshAttempts = ref(0)

/** 充值金额 = pay_amount / (1 + fee_rate/100)，fee_rate=0 时等于 pay_amount */
const baseAmount = computed(() => {
  if (!hasAmountFields(order.value)) return 0
  const feeRate = Number(order.value.fee_rate) || 0
  if (feeRate <= 0) return order.value.pay_amount ?? 0
  return Math.round((order.value.pay_amount / (1 + feeRate / 100)) * 100) / 100
})

/** 手续费 = pay_amount - baseAmount */
const feeAmount = computed(() => {
  if (!hasAmountFields(order.value)) return 0
  const feeRate = Number(order.value.fee_rate) || 0
  if (feeRate <= 0) return 0
  return Math.round((order.value.pay_amount - baseAmount.value) * 100) / 100
})

const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String((raw as { value?: string }).value || '')
  }
  return undefined
})

const isSuccess = computed(() => {
  return isSuccessStatus(order.value?.status)
})

const isPending = computed(() => {
  return isPendingStatus(order.value?.status)
})

const statusTitle = computed(() => {
  if (isSuccess.value) {
    return t('payment.result.success')
  }
  if (isPending.value) {
    return t('payment.result.processing')
  }
  return t('payment.result.failed')
})

function normalizedOrderPaymentType(paymentType: string): string {
  return normalizePaymentMethodForDisplay(paymentType || '') || paymentType || ''
}

function formatGatewayAmount(value: number): string {
  return formatPaymentAmount(value, currency.value, localeCode.value)
}

function checkOwner(): void {
  if (ownerChanged || isOriginalUser()) return
  ownerChanged = true
  restoredRecoveryRaw = null
  if (hasPrivateOrder) {
    order.value = null
    hasPrivateOrder = false
    clearStatusRefreshTimer()
  }
  const publicCurrency = order.value && 'currency' in order.value ? order.value.currency : undefined
  currency.value = normalizePaymentCurrency(publicCurrency)
}

function canUsePrivateState(): boolean {
  checkOwner()
  return !disposed && !ownerChanged && ownerId !== null
}

function setResolvedOrder(result: OrderLookup): void {
  checkOwner()
  if (disposed || (result.authenticated && !canUsePrivateState())) return
  order.value = result.data
  hasPrivateOrder = result.authenticated
  restoredRecoveryRaw = null
  let recoveredCurrency: string | undefined
  if (recovery && canUsePrivateState() && hasOrderId(result.data)) {
    const { snapshot, raw } = recovery
    const sameOrder = result.data.id === snapshot.orderId &&
      (!snapshot.outTradeNo || result.data.out_trade_no === snapshot.outTradeNo)
    const sameOwner = result.authenticated || (result.signed && snapshot.ownerId === ownerId)
    if (sameOrder && sameOwner) {
      restoredRecoveryRaw = raw
      recoveredCurrency = snapshot.currency
    }
  }
  const orderCurrency = 'currency' in result.data ? result.data.currency : undefined
  currency.value = normalizePaymentCurrency(orderCurrency || recoveredCurrency)
  void refreshUserBalanceForSuccessfulOrder(result.data)
}

async function refreshUserBalanceForSuccessfulOrder(nextOrder: ResolvedOrder): Promise<void> {
  if (!canUsePrivateState() || userBalanceRefreshStarted || normalizeOrderStatus(nextOrder.status) !== 'COMPLETED') {
    return
  }
  if ('order_type' in nextOrder && nextOrder.order_type !== 'balance') {
    return
  }

  userBalanceRefreshStarted = true
  try {
    // This page only needs the balance. Check ownership again before updating
    // the reactive and persisted profiles, since the request can outlive login.
    const { data } = await getCurrentUser()
    if (!canUsePrivateState() || data.id !== ownerId || authStore.user?.id !== ownerId) return
    const rawUser = localStorage.getItem('auth_user')
    if (!rawUser) return
    const persistedUser = JSON.parse(rawUser)
    localStorage.setItem('auth_user', JSON.stringify({ ...persistedUser, balance: data.balance }))
    authStore.user.balance = data.balance
  } catch {
    // The order result remains authoritative when the balance request fails.
  }
}

function hasOrderId(nextOrder: ResolvedOrder | null): nextOrder is PaymentOrder {
  return !!nextOrder && 'id' in nextOrder && typeof nextOrder.id === 'number'
}

function hasAmountFields(nextOrder: ResolvedOrder | null): nextOrder is PaymentOrder {
  return !!nextOrder && 'pay_amount' in nextOrder && typeof nextOrder.pay_amount === 'number' && 'amount' in nextOrder && typeof nextOrder.amount === 'number'
}

/** 充值赠送额度（USD）；老接口/订阅订单没有该字段时视为 0 */
function orderBonusAmount(target: unknown): number {
  const value = (target as { bonus_amount?: unknown } | null | undefined)?.bonus_amount
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

function hasPaymentType(nextOrder: ResolvedOrder | null): nextOrder is PaymentOrder {
  return !!nextOrder && 'payment_type' in nextOrder && typeof nextOrder.payment_type === 'string' && nextOrder.payment_type.trim() !== ''
}

function normalizeOrderStatus(status: string | null | undefined): string {
  return String(status || '').trim().toUpperCase()
}

function displayOrderStatus(status: string): OrderStatus {
  return normalizeOrderStatus(status) as OrderStatus
}

function isSuccessStatus(status: string | null | undefined): boolean {
  return SUCCESS_STATUSES.has(normalizeOrderStatus(status))
}

function isPendingStatus(status: string | null | undefined): boolean {
  return PENDING_STATUSES.has(normalizeOrderStatus(status))
}

function readRouteQueryString(key: string): string {
  const value = route.query[key]
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

let restoredRecoveryRaw: string | null = null
let recovery: ReturnType<typeof restoreRecoverySnapshot> = null

function restoreRecoverySnapshot(context: {
  resumeToken: string
  routeOrderId: number
  routeOutTradeNo: string
}) {
  if (typeof window === 'undefined') {
    return null
  }

  const rawSnapshot = window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)
  if (!rawSnapshot) {
    return null
  }

  if (!context.resumeToken && !context.routeOrderId && !context.routeOutTradeNo) {
    return null
  }

  const restored = readPaymentRecoverySnapshot(rawSnapshot, {
    ownerId: ownerId ?? undefined,
    resumeToken: context.resumeToken || undefined,
    allowExpiredConfirmation: true,
  })
  if (!restored) {
    return null
  }

  if (!context.resumeToken && context.routeOrderId > 0 && restored.orderId !== context.routeOrderId) {
    return null
  }

  if (!context.resumeToken && context.routeOutTradeNo && restored.outTradeNo !== context.routeOutTradeNo) {
    return null
  }

  return { raw: rawSnapshot, snapshot: restored }
}

async function resolveOrderFromResumeToken(resumeToken: string): Promise<OrderLookup | null> {
  if (disposed) return null
  try {
    const result = await paymentAPI.resolveOrderPublicByResumeToken(resumeToken)
    return !disposed && result.data ? { data: result.data, authenticated: false, signed: true } : null
  } catch {
    return null
  }
}

async function resolvePrivateOrder(orderId: number): Promise<OrderLookup | null> {
  if (!canUsePrivateState()) return null
  try {
    const { data } = await paymentAPI.getOrder(orderId)
    if (canUsePrivateState() && data?.id === orderId && data.user_id === ownerId) {
      return { data, authenticated: true }
    }
  } catch {
    // The result may still be available through a public callback.
  }
  return null
}

async function resolveOrderFromOutTradeNo(outTradeNo: string): Promise<OrderLookup | null> {
  if (canUsePrivateState()) {
    try {
      const { data } = await paymentAPI.verifyOrder(outTradeNo)
      if (canUsePrivateState() && data?.user_id === ownerId) return { data, authenticated: true }
    } catch {
      // Fall back to public verification for an eligible return flow.
    }
  }
  if (disposed) return null
  try {
    const { data } = await paymentAPI.verifyOrderPublic(outTradeNo)
    return !disposed && data ? { data, authenticated: false } : null
  } catch {
    return null
  }
}

function clearStatusRefreshTimer(): void {
  if (statusRefreshTimer !== null) {
    clearTimeout(statusRefreshTimer)
    statusRefreshTimer = null
  }
}

function clearRecoverySnapshot(): void {
  if (!canUsePrivateState() || !restoredRecoveryRaw || window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY) !== restoredRecoveryRaw) return
  clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY)
  restoredRecoveryRaw = null
}

function clearRecoverySnapshotForTerminalStatus(status: string | null | undefined): void {
  if (!status || !canUsePrivateState()) return
  if (!isPendingStatus(status)) {
    if (hasOrderId(order.value)) {
      const storage = window.localStorage
      const snapshot = readStripePaymentRecoverySnapshot(storage, order.value.id, undefined, ownerId ?? undefined)
      if (hasPrivateOrder || snapshot?.ownerId === ownerId) {
        clearStripePaymentRecoverySnapshot(storage, order.value.id)
      }
    }
    clearRecoverySnapshot()
  }
}

function scheduleStatusRefresh(refreshOrder: () => Promise<OrderLookup | null>): void {
  clearStatusRefreshTimer()
  if (disposed || !isPending.value || refreshAttempts.value >= STATUS_REFRESH_MAX_ATTEMPTS) {
    return
  }

  statusRefreshTimer = setTimeout(async () => {
    statusRefreshTimer = null
    checkOwner()
    if (disposed || !isPending.value) return
    refreshAttempts.value += 1
    const refreshedOrder = await refreshOrder()
    checkOwner()
    if (disposed) return
    if (refreshedOrder) {
      setResolvedOrder(refreshedOrder)
      clearRecoverySnapshotForTerminalStatus(order.value?.status)
    }

    if (isPendingStatus(order.value?.status)) {
      scheduleStatusRefresh(refreshOrder)
    }
  }, STATUS_REFRESH_INTERVAL_MS)
}

onMounted(async () => {
  window.addEventListener('storage', checkOwner)
  const resumeToken = readRouteQueryString('resume_token')
  const routeOrderId = Number(readRouteQueryString('order_id')) || 0
  const outTradeNo = readRouteQueryString('out_trade_no')
  let orderId = routeOrderId

  recovery = restoreRecoverySnapshot({
    resumeToken,
    routeOrderId,
    routeOutTradeNo: outTradeNo,
  })

  if (resumeToken) {
    const resolvedOrder = await resolveOrderFromResumeToken(resumeToken)
    if (disposed) return
    if (resolvedOrder) {
      setResolvedOrder(resolvedOrder)
      if (hasOrderId(resolvedOrder.data)) {
        orderId = resolvedOrder.data.id
      }
    }
  }

  const hasLegacyFallbackContext = readRouteQueryString('trade_status').trim() !== ''
  const shouldUsePublicOutTradeNo = outTradeNo !== '' && (hasLegacyFallbackContext || orderId > 0)

  if (!order.value && orderId && (!resumeToken || routeOrderId > 0)) {
    const privateOrder = await resolvePrivateOrder(orderId)
    if (disposed) return
    if (privateOrder) setResolvedOrder(privateOrder)
  }

  if (!order.value && shouldUsePublicOutTradeNo) {
    const legacyOrder = await resolveOrderFromOutTradeNo(outTradeNo)
    if (disposed) return
    if (legacyOrder) {
      setResolvedOrder(legacyOrder)
      if (!orderId) {
        orderId = hasOrderId(legacyOrder.data) ? legacyOrder.data.id : 0
      }
    }
  }

  if (!order.value && !orderId && outTradeNo && hasLegacyFallbackContext) {
    returnInfo.value = {
      outTradeNo,
      money: String(route.query.money || ''),
      type: String(route.query.type || ''),
      tradeStatus: String(route.query.trade_status || ''),
    }
  }

  const refreshOrder = async (): Promise<OrderLookup | null> => {
    if (resumeToken) {
      const resolvedOrder = await resolveOrderFromResumeToken(resumeToken)
      if (resolvedOrder) {
        return resolvedOrder
      }
    }

    if (orderId) {
      const resolvedOrder = await resolvePrivateOrder(orderId)
      if (resolvedOrder) return resolvedOrder
    }

    if (shouldUsePublicOutTradeNo) {
      return await resolveOrderFromOutTradeNo(outTradeNo)
    }

    return null
  }

  checkOwner()
  if (isPendingStatus(order.value?.status)) {
    scheduleStatusRefresh(refreshOrder)
  } else if (order.value) {
    clearRecoverySnapshotForTerminalStatus(order.value.status)
  }
  loading.value = false
})

watch(() => authStore.user?.id, checkOwner)
onBeforeUnmount(() => {
  disposed = true
  window.removeEventListener('storage', checkOwner)
  clearStatusRefreshTimer()
})
</script>
