<template>
  <div
    :class="[
      'linear-plan-card linx-panel group flex h-full flex-col p-5 transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2',
      borderClass,
    ]"
  >
    <!-- Colored top accent bar -->
    <div :class="['mb-4 h-1 rounded-full', accentClass]" />

    <div class="flex flex-1 flex-col">
      <!-- Header: name + badge + price -->
      <div class="mb-3 flex items-start justify-between gap-2">
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
          <span
            v-if="hasSeatLimit"
            class="mt-2 inline-flex items-center rounded-full border border-orange-200/60 bg-orange-50 px-2.5 py-1 text-[11px] font-medium text-orange-700 shadow-sm dark:border-orange-400/20 dark:bg-orange-400/10 dark:text-orange-200"
          >
            当前已开通 {{ seatUsed }}/{{ plan.seat_limit }}
          </span>
        </div>
        <div class="shrink-0 text-right">
          <div class="flex items-baseline gap-1">
            <span :class="['text-3xl font-semibold tracking-[-0.05em] text-gray-950 dark:text-linear-ink', textClass]">{{ priceLabel }}</span>
          </div>
          <span class="text-[11px] text-gray-400 dark:text-dark-500">/ {{ validitySuffix }}</span>
          <div v-if="plan.original_price" class="mt-0.5 flex items-center justify-end gap-1.5">
            <span class="text-xs text-gray-400 line-through dark:text-dark-500">${{ plan.original_price }}</span>
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
const priceLabel = computed(() => monthlyDisplay.value?.priceLabel ?? formatMonthlyPlanCny(props.plan.price))
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
