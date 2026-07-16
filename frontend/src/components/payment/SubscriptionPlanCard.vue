<template>
  <div :class="rootCardClass">
    <span
      v-if="limitedSeatLabel"
      data-testid="limited-seat-ribbon"
      class="pointer-events-none absolute right-[-54px] top-7 z-20 w-[220px] rotate-45 whitespace-nowrap bg-gradient-to-r from-orange-950 via-orange-800 to-orange-700 py-1.5 text-center text-[11px] font-black tracking-[-0.01em] text-white drop-shadow-sm shadow-[0_12px_30px_rgba(249,115,22,0.35)] ring-1 ring-white/20 [text-shadow:0_1px_2px_rgba(0,0,0,0.45)] dark:from-orange-950 dark:via-orange-800 dark:to-orange-700"
    >
      {{ limitedSeatLabel }}
    </span>

    <!-- Colored top accent bar -->
    <div :class="['mb-4 h-1 rounded-full', accentClass]" />

    <div class="flex flex-1 flex-col">
      <!-- Header: name + badge + price -->
      <div
        data-testid="plan-card-header"
        :class="['mb-3 flex items-start justify-between gap-2', limitedSeatLabel ? 'limited-seat-ribbon-gutter min-h-[112px] pt-16' : '']"
      >
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <h3 class="truncate text-lg font-semibold tracking-[-0.03em] text-gray-950 dark:text-linear-ink">{{ displayName }}</h3>
            <span v-if="monthlyDisplay?.badge" class="rounded-full border border-primary-500/25 bg-primary-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-primary-600 dark:text-primary-300">
              {{ monthlyDisplay.badge }}
            </span>
          </div>
          <p v-if="displayDescription" class="mt-0.5 text-xs leading-relaxed text-gray-500 dark:text-dark-400">
            {{ displayDescription }}
          </p>
        </div>
		<div data-testid="plan-price-block" :class="['shrink-0 text-right', limitedSeatLabel ? 'mt-2 self-start' : '']">
		  <div class="flex items-baseline gap-1">
			<span :class="['text-3xl font-semibold tracking-[-0.05em] text-gray-950 dark:text-linear-ink', textClass]">{{ priceLabel }}</span>
			<span v-if="plan.currency" class="text-xs font-medium text-gray-400 dark:text-dark-500">{{ plan.currency }}</span>
		  </div>
		  <span class="text-[11px] text-gray-400 dark:text-dark-500">/ {{ validitySuffix }}</span>
		  <div v-if="plan.original_price" class="mt-0.5 flex items-center justify-end gap-1.5">
			<span class="text-xs text-gray-400 line-through dark:text-dark-500">{{ originalPriceLabel }}<template v-if="plan.currency"> {{ plan.currency }}</template></span>
            <span :class="['rounded px-1 py-0.5 text-[10px] font-semibold', discountClass]">{{ discountText }}</span>
          </div>
        </div>
      </div>

      <div class="mb-4 flex flex-wrap gap-2">
        <p v-if="sevenDayQuotaLabel" class="inline-flex rounded-lg border border-primary-500/25 bg-primary-500/10 px-3 py-1.5 text-sm font-medium text-primary-600 dark:text-primary-300">
          {{ sevenDayQuotaLabel }}
        </p>
        <p v-if="monthlyTotalLabel" class="inline-flex rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm font-medium text-gray-600 dark:border-linear-hairline dark:bg-linear-surface-2 dark:text-linear-ink-muted">
          {{ t('payment.planCard.totalMonthlyQuota') }} {{ monthlyTotalLabel }}
        </p>
        <p v-if="!sevenDayQuotaLabel && plan.seven_day_quota_usd == null" class="inline-flex rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm font-medium text-gray-600 dark:border-linear-hairline dark:bg-linear-surface-2 dark:text-linear-ink-muted">
          {{ t('payment.planCard.quota') }}: {{ t('payment.planCard.unlimited') }}
        </p>
      </div>

      <!-- Features list (compact) -->
      <div v-if="displayFeatures.length > 0" class="mb-3 space-y-1">
        <div v-for="feature in displayFeatures" :key="feature" class="flex items-start gap-1.5">
          <svg :class="['mt-0.5 h-3.5 w-3.5 flex-shrink-0', iconClass]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          </svg>
          <span class="text-xs text-gray-600 dark:text-gray-300">{{ feature }}</span>
        </div>
      </div>

      <p v-if="pendingNotice" class="mb-3 rounded-lg border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-700 dark:text-amber-200">
        {{ pendingNotice }}
      </p>

      <div class="flex-1" />

<div class="space-y-2">
        <button
          v-if="isCurrentPlan"
          type="button"
          disabled
          class="w-full cursor-default rounded-xl border border-green-500/30 bg-green-500/10 py-2.5 text-sm font-semibold text-green-700 dark:text-green-300"
        >
          {{ t('payment.currentSubscription') }}
        </button>
        <button
          type="button"
          :disabled="isDisabled"
          :class="[
            'w-full rounded-xl py-2.5 text-sm font-semibold transition-all active:scale-[0.98]',
            isDisabled ? 'cursor-not-allowed bg-gray-200 text-gray-500 shadow-none dark:bg-dark-700 dark:text-dark-400' : btnClass,
          ]"
          @click="handleSelect"
        >
          {{ buttonText }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SubscriptionPlan } from '@/types/payment'
import type { UserSubscription } from '@/types'
import {
  platformAccentBarClass,
  platformBorderClass,
  platformTextClass,
  platformIconClass,
  platformButtonClass,
  platformDiscountClass,
} from '@/utils/platformColors'
import {
  getMonthlyPlanDisplayFromPlan,
  monthlyPlanKeyFromName,
  formatMonthlyPlanCny,
  formatMonthlyPlanUsd,
  type SubscriptionPlanSelectIntent,
} from '@/utils/monthlyPlans'

const props = defineProps<{
  plan: SubscriptionPlan
  activeSubscriptions?: UserSubscription[]
  displayName?: string
  displayDescription?: string
  displayFeatures?: string[]
  pendingNotice?: string
}>()
const emit = defineEmits<{ select: [plan: SubscriptionPlan, intent: SubscriptionPlanSelectIntent] }>()
const i18n = useI18n()
const { t } = i18n

const localeValue = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) return String((raw as { value?: string }).value || '')
  return ''
})
const monthlyDisplay = computed(() => getMonthlyPlanDisplayFromPlan(props.plan, localeValue.value))
const displayName = computed(() => props.displayName || monthlyDisplay.value?.name || props.plan.name)
const displayDescription = computed(() => props.displayDescription || monthlyDisplay.value?.description || props.plan.description || '')
const displayFeatures = computed(() => props.displayFeatures ?? monthlyDisplay.value?.benefits ?? props.plan.features)
const planKey = computed(() => monthlyPlanKeyFromName(props.plan.name))
const baseCardClass = 'linear-plan-card linx-panel group relative flex h-full flex-col overflow-hidden p-5 transition-colors'
const tierCardClass = computed(() => {
  switch (planKey.value) {
    case 'basic':
      return 'linx-plan-tier-basic border-orange-200/40 bg-gradient-to-br from-white via-orange-50/70 to-amber-100/70 hover:shadow-[0_18px_50px_rgba(249,115,22,0.14)] dark:border-orange-400/20 dark:from-[#15120f] dark:via-[#20150d] dark:to-[#2a1808] dark:hover:shadow-[0_20px_64px_rgba(249,115,22,0.18)]'
    case 'plus':
      return 'linx-plan-tier-plus border-orange-300/45 bg-gradient-to-br from-white via-orange-50 to-orange-200/75 hover:shadow-[0_20px_56px_rgba(249,115,22,0.16)] dark:border-orange-400/25 dark:from-[#17110d] dark:via-[#2a1608] dark:to-[#3a2109] dark:hover:shadow-[0_22px_70px_rgba(249,115,22,0.2)]'
    case 'pro':
      return 'linx-plan-tier-pro border-orange-400/55 bg-gradient-to-br from-white via-orange-100/90 to-rose-100/75 shadow-[0_20px_60px_rgba(249,115,22,0.16)] hover:shadow-[0_24px_68px_rgba(249,115,22,0.2)] dark:border-orange-300/35 dark:from-[#1c120d] dark:via-[#3a1708] dark:to-[#431407] dark:shadow-[0_24px_80px_rgba(249,115,22,0.22)]'
    case 'max':
      return 'linx-plan-tier-max border-orange-500/60 bg-gradient-to-br from-white via-orange-100 to-red-100/80 hover:shadow-[0_24px_70px_rgba(249,115,22,0.2)] dark:border-orange-300/40 dark:from-[#1f100c] dark:via-[#431407] dark:to-[#4a0d0d] dark:hover:shadow-[0_28px_88px_rgba(249,115,22,0.24)]'
    default:
      return ''
  }
})
const rootCardClass = computed(() => {
  if (tierCardClass.value) {
    return [baseCardClass, tierCardClass.value]
  }
  return [baseCardClass, 'hover:border-linear-hairline-strong hover:bg-linear-surface-2', borderClass.value]
})
const activeGenericSubscription = computed(() =>
  props.activeSubscriptions?.find((subscription) => {
    if (subscription.status !== 'active') return false
    if (subscription.plan_id != null && subscription.plan_id === props.plan.id) return true
    const activeKey = monthlyPlanKeyFromName(subscription.plan_name)
    return !!activeKey && !!planKey.value && activeKey === planKey.value
  }) ?? null
)
const hasAnyActiveGenericSubscription = computed(() =>
  props.activeSubscriptions?.some(subscription => subscription.status === 'active') ?? false
)
const isCurrentPlan = computed(() => activeGenericSubscription.value !== null)
const hasActiveGenericSubscription = computed(() => isCurrentPlan.value || hasAnyActiveGenericSubscription.value)
const actionIntent = computed<SubscriptionPlanSelectIntent>(() => {
  if (isCurrentPlan.value) return 'renew'
  if (hasActiveGenericSubscription.value) return 'switch'
  return 'subscribe'
})
const actionLabel = computed(() => {
  if (actionIntent.value === 'renew') return t('payment.renewNow')
  if (actionIntent.value === 'switch') return t('payment.switchSubscription')
  return t('payment.subscribeNow')
})
const hasSeatLimit = computed(() => props.plan.seat_limit !== null && props.plan.seat_limit !== undefined)
const seatUsed = computed(() => props.plan.seat_used || 0)
const hasVirtualSeatRange = computed(() =>
  props.plan.virtual_seat_start !== null &&
  props.plan.virtual_seat_start !== undefined &&
  props.plan.virtual_seat_total !== null &&
  props.plan.virtual_seat_total !== undefined
)
const limitedSeatLabel = computed(() => {
  if (!hasSeatLimit.value) return ''
  if (hasVirtualSeatRange.value) {
    const current = (props.plan.virtual_seat_start || 0) + seatUsed.value
    return `限时名额：${current}/${props.plan.virtual_seat_total}`
  }
  return `限时名额：${seatUsed.value}/${props.plan.seat_limit}`
})
const isFullForNewOpening = computed(() => hasSeatLimit.value && props.plan.seat_full === true && !isCurrentPlan.value)
const isDisabled = computed(() => isFullForNewOpening.value)
const buttonText = computed(() => {
  if (isFullForNewOpening.value) return '名额已满'
  return actionLabel.value
})
function handleSelect() {
  if (isDisabled.value) return
  emit('select', props.plan, actionIntent.value)
}
const priceLabel = computed(() => monthlyDisplay.value?.priceLabel ?? (props.plan.currency ? String(props.plan.price) : formatMonthlyPlanCny(props.plan.price)))
const originalPriceLabel = computed(() => props.plan.currency ? String(props.plan.original_price ?? '') : `¥${props.plan.original_price ?? ''}`)
const sevenDayQuotaLabel = computed(() => {
  if (monthlyDisplay.value) return monthlyDisplay.value.quotaLabel
  return props.plan.seven_day_quota_usd != null ? `${formatMonthlyPlanUsd(props.plan.seven_day_quota_usd)} / 7 ${t('payment.days')}` : ''
})
const monthlyTotalLabel = computed(() => {
  if (monthlyDisplay.value) return monthlyDisplay.value.monthlyTotalLabel
  return props.plan.seven_day_quota_usd != null ? formatMonthlyPlanUsd(props.plan.seven_day_quota_usd * 4) : ''
})


// Derived color classes from central config
const accentClass = computed(() => platformAccentBarClass(''))
const borderClass = computed(() => platformBorderClass(''))
const textClass = computed(() => platformTextClass(''))
const iconClass = computed(() => platformIconClass(''))
const btnClass = computed(() => platformButtonClass(''))
const discountClass = computed(() => platformDiscountClass(''))
const discountText = computed(() => {
  if (!props.plan.original_price || props.plan.original_price <= 0) return ''
  const pct = Math.round((1 - props.plan.price / props.plan.original_price) * 100)
  return pct > 0 ? `-${pct}%` : ''
})

const validitySuffix = computed(() => {
  const u = props.plan.validity_unit || 'day'
  if (u === 'week' || u === 'weeks') return t('payment.perWeek')
  if (u === 'month' || u === 'months') return t('payment.perMonth')
  if (u === 'year' || u === 'years') return t('payment.perYear')
  return `${props.plan.validity_days}${t('payment.days')}`
})
</script>
