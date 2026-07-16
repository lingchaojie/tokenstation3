<template>
  <BaseDialog :show="show" :title="plan ? t('payment.admin.editPlan') : t('payment.admin.createPlan')" width="wide" @close="emit('close')">
    <form id="plan-form" @submit.prevent="handleSavePlan" class="space-y-4">
      <div>
        <label class="input-label">{{ t('payment.admin.planName') }} <span class="text-red-500">*</span></label>
        <input v-model="planForm.name" type="text" class="input" required />
      </div>

      <div><label class="input-label">{{ t('payment.admin.planDescription') }} <span class="text-red-500">*</span></label><textarea v-model="planForm.description" rows="2" class="input" required></textarea></div>
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="input-label">{{ t('payment.admin.price') }} <span class="text-red-500">*</span></label>
          <input v-model.number="planForm.price" type="number" step="0.01" min="0.01" class="input" required />
          <p v-if="subscriptionCnyPreview" class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400">
            {{ t('payment.admin.subscriptionCnyPayPreview', { amount: subscriptionCnyPreview.amount }) }}
            <span v-if="subscriptionCnyPreview.feeRate > 0">
              {{ t('payment.admin.subscriptionCnyPayPreviewWithFee', { feeRate: subscriptionCnyPreview.feeRate, total: subscriptionCnyPreview.total }) }}
            </span>
          </p>
        </div>
        <div><label class="input-label">{{ t('payment.admin.originalPrice') }}</label><input v-model.number="planForm.original_price" type="number" step="0.01" min="0" class="input" /></div>
      </div>
      <div class="grid grid-cols-2 gap-4">
        <div><label class="input-label">{{ t('payment.admin.validityDays') }} <span class="text-red-500">*</span></label><input v-model.number="planForm.validity_days" type="number" min="1" class="input" required /></div>
        <div><label class="input-label">{{ t('payment.admin.validityUnit') }} <span class="text-red-500">*</span></label><Select v-model="planForm.validity_unit" :options="validityUnitOptions" /></div>
      </div>
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="input-label">{{ t('payment.admin.sevenDayQuota') }}</label>
          <input v-model="planForm.seven_day_quota_usd" data-testid="plan-seven-day-quota" type="number" step="0.01" min="0" class="input" />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.sevenDayQuotaHint') }}</p>
        </div>
		<div><label class="input-label">{{ t('payment.admin.sortOrder') }}</label><input v-model.number="planForm.sort_order" type="number" min="0" class="input" /></div>
		<div>
		  <label class="input-label">{{ t('payment.admin.currency') }}</label>
		  <input v-model="planForm.currency" type="text" maxlength="3" class="input uppercase" :placeholder="t('payment.admin.currencyPlaceholder')" />
		  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.currencyHint') }}</p>
		</div>
		<div class="col-span-2 grid grid-cols-2 gap-3">
          <div>
            <label class="input-label">{{ t('payment.admin.virtualSeatStart') }}</label>
            <input :value="planForm.virtual_seat_start" data-testid="plan-virtual-seat-start" type="number" min="0" step="1" class="input" :placeholder="t('payment.admin.seatLimitPlaceholder')" @input="setVirtualSeatStartInput" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.admin.virtualSeatTotal') }}</label>
            <input :value="planForm.virtual_seat_total" data-testid="plan-virtual-seat-total" type="number" min="0" step="1" class="input" :placeholder="t('payment.admin.seatLimitPlaceholder')" @input="setVirtualSeatTotalInput" />
          </div>
          <div class="col-span-2">
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.virtualSeatRangeHint') }}</p>
            <p v-if="derivedSeatLimit !== null" class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('payment.admin.derivedSeatLimit') }}: {{ derivedSeatLimit }}</p>
			<p v-if="seatLimitLowerThanUsed" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ t('payment.admin.seatLimitLowerThanUsed') }}</p>
		  </div>
		</div>
      </div>
      <div>
        <label class="input-label">{{ t('payment.admin.features') }}</label>
        <textarea v-model="planFeaturesText" rows="3" class="input" :placeholder="t('payment.admin.featuresPlaceholder')"></textarea>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.featuresHint') }}</p>
      </div>
      <div class="flex items-center gap-3">
        <label class="text-sm text-gray-700 dark:text-gray-300">{{ t('payment.admin.forSale') }}</label>
        <button
          type="button"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            planForm.for_sale ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
          ]"
          @click="planForm.for_sale = !planForm.for_sale"
        >
          <span :class="[
            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
            planForm.for_sale ? 'translate-x-5' : 'translate-x-0'
          ]" />
        </button>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" @click="emit('close')" class="btn btn-secondary">{{ t('common.cancel') }}</button>
        <button type="submit" form="plan-form" :disabled="saving" class="btn btn-primary">{{ saving ? t('common.saving') : t('common.save') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { SubscriptionPlan } from '@/types/payment'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'

const props = defineProps<{
  show: boolean
  plan: SubscriptionPlan | null
  paymentConfig?: AdminPaymentConfig | null
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const saving = ref(false)
const planForm = reactive({
  name: '',
  description: '',
	price: 0,
	original_price: 0,
	currency: '',
	seven_day_quota_usd: null as number | string | null,
  validity_days: 30,
  validity_unit: 'day',
  sort_order: 0,
  virtual_seat_start: '',
	virtual_seat_total: '',
	for_sale: true,
})
const planFeaturesText = ref('')

const validityUnitOptions = computed(() => [
  { value: 'day', label: t('payment.admin.days') },
  { value: 'week', label: t('payment.admin.weeks') },
  { value: 'month', label: t('payment.admin.months') },
])

function normalizeValidityUnit(unit: string | null | undefined): string {
  switch (unit) {
    case 'week':
    case 'weeks':
      return 'week'
    case 'month':
    case 'months':
      return 'month'
    default:
      return 'day'
  }
}

const derivedSeatLimit = computed(() => {
  try {
    const { start, total } = parseVirtualSeatRange()
    return start === null || total === null ? null : total - start
  } catch {
    return null
  }
})

const seatLimitLowerThanUsed = computed(() => {
  if (!props.plan || derivedSeatLimit.value === null) return false
  return derivedSeatLimit.value < (props.plan.seat_used || 0)
})

function setVirtualSeatStartInput(event: Event) {
  planForm.virtual_seat_start = (event.target as HTMLInputElement).value
}

function setVirtualSeatTotalInput(event: Event) {
  planForm.virtual_seat_total = (event.target as HTMLInputElement).value
}

function roundCnyAmount(value: number): number {
  return Math.round(value * 100) / 100
}

function ceilCnyAmount(value: number): number {
  return Math.ceil(value * 100) / 100
}

const subscriptionCnyPreview = computed(() => {
  const price = Number(planForm.price) || 0
  const rate = Number(props.paymentConfig?.subscription_usd_to_cny_rate) || 0
  if (price <= 0 || rate <= 0) return null

  const amount = roundCnyAmount(price * rate)
  const feeRate = Number(props.paymentConfig?.recharge_fee_rate) || 0
  const fee = feeRate > 0 ? ceilCnyAmount((amount * feeRate) / 100) : 0
  const total = feeRate > 0 ? roundCnyAmount(amount + fee) : amount

  return {
    amount: formatPaymentAmount(amount, 'CNY'),
    feeRate,
    total: formatPaymentAmount(total, 'CNY'),
  }
})

// Reset form when dialog opens
watch(() => props.show, (visible) => {
	if (!visible) return
	if (props.plan) {
	  const shouldUseSeatLimitFallback = props.plan.virtual_seat_start == null && props.plan.virtual_seat_total == null && props.plan.seat_limit != null
    const virtualSeatStart = shouldUseSeatLimitFallback ? '0' : props.plan.virtual_seat_start == null ? '' : String(props.plan.virtual_seat_start)
    const virtualSeatTotal = shouldUseSeatLimitFallback ? String(props.plan.seat_limit) : props.plan.virtual_seat_total == null ? '' : String(props.plan.virtual_seat_total)

    Object.assign(planForm, {
      name: props.plan.name,
      description: props.plan.description,
		price: props.plan.price,
		original_price: props.plan.original_price || 0,
		currency: props.plan.currency || '',
		seven_day_quota_usd: props.plan.seven_day_quota_usd ?? null,
      validity_days: props.plan.validity_days,
      validity_unit: normalizeValidityUnit(props.plan.validity_unit),
      sort_order: props.plan.sort_order || 0,
      virtual_seat_start: virtualSeatStart,
      virtual_seat_total: virtualSeatTotal,
      for_sale: props.plan.for_sale,
    })
    planFeaturesText.value = (props.plan.features || []).join('\n')
  } else {
    Object.assign(planForm, {
      name: '',
      description: '',
		price: 0,
		original_price: 0,
		currency: '',
		seven_day_quota_usd: null,
      validity_days: 30,
      validity_unit: 'day',
      sort_order: 0,
      virtual_seat_start: '',
      virtual_seat_total: '',
		for_sale: true,
	  })
	  planFeaturesText.value = ''
  }
}, { immediate: true })

function parseVirtualSeatRange(): { start: number | null; total: number | null } {
  const startTrimmed = planForm.virtual_seat_start.trim()
  const totalTrimmed = planForm.virtual_seat_total.trim()

  if (!startTrimmed && !totalTrimmed) {
    return { start: null, total: null }
  }
  if (!startTrimmed || !totalTrimmed) {
    throw new Error(t('payment.admin.virtualSeatRangeRequired'))
  }

  const start = Number(startTrimmed)
  const total = Number(totalTrimmed)
  if (!Number.isInteger(start) || !Number.isInteger(total) || start < 0 || total < 0 || total < start) {
    throw new Error(t('payment.admin.virtualSeatRangeInvalid'))
  }

  return { start, total }
}

function parseNullableNumber(value: number | string | null): number | null {
  if (value === null || value === '') return null
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

/** Build request payload with snake_case keys matching backend JSON tags */
function buildPlanPayload() {
  const features = planFeaturesText.value.split('\n').map(f => f.trim()).filter(Boolean).join('\n')
  const sevenDayQuota = parseNullableNumber(planForm.seven_day_quota_usd)
  const virtualSeatRange = parseVirtualSeatRange()
  const payload: Record<string, unknown> = {
    name: planForm.name,
	description: planForm.description,
	price: planForm.price,
	original_price: planForm.original_price || 0,
    currency: planForm.currency.trim().toUpperCase(),
    validity_days: planForm.validity_days,
    validity_unit: normalizeValidityUnit(planForm.validity_unit),
    sort_order: planForm.sort_order,
    virtual_seat_start: virtualSeatRange.start,
    virtual_seat_total: virtualSeatRange.total,
    for_sale: planForm.for_sale,
    features,
    seven_day_quota_usd: sevenDayQuota,
  }

  if (props.plan && props.plan.seven_day_quota_usd != null && sevenDayQuota === null) {
    payload.clear_seven_day_quota_usd = true
  }

  return payload
}

async function handleSavePlan() {
  if (!planForm.price || planForm.price <= 0) {
    appStore.showError(t('payment.admin.priceRequired'))
    return
  }
  if (!planForm.validity_days || planForm.validity_days < 1) {
    appStore.showError(t('payment.admin.validityDaysRequired'))
    return
  }
  let data: ReturnType<typeof buildPlanPayload>
  try {
    data = buildPlanPayload()
  } catch (err: unknown) {
    appStore.showError(err instanceof Error ? err.message : t('common.error'))
    return
  }
  saving.value = true
  try {
    if (props.plan) { await adminPaymentAPI.updatePlan(props.plan.id, data) }
    else { await adminPaymentAPI.createPlan(data) }
    appStore.showSuccess(t('common.saved'))
    emit('close')
    emit('saved')
  } catch (err: unknown) { appStore.showError(extractApiErrorMessage(err, t('common.error'))) }
  finally { saving.value = false }
}
</script>
