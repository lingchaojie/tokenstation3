<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Actions -->
      <div class="flex items-center justify-end gap-2">
        <button @click="loadPlans" :disabled="plansLoading" class="btn btn-secondary" :title="t('common.refresh')">
          <Icon name="refresh" size="md" :class="plansLoading ? 'animate-spin' : ''" />
        </button>
        <button @click="openPlanEdit(null)" class="btn btn-primary">{{ t('payment.admin.createPlan') }}</button>
      </div>

      <!-- Plans Table -->
      <DataTable :columns="planColumns" :data="plans" :loading="plansLoading">
        <template #cell-name="{ value }">
          <span class="text-sm font-medium text-gray-900 dark:text-white">{{ value }}</span>
        </template>
        <template #cell-price="{ value, row }">
          <div class="text-sm">
            <span class="font-medium text-gray-900 dark:text-white">¥{{ formatMoney(value) }}</span>
            <span v-if="row.original_price" class="ml-1 text-xs text-gray-400 line-through">¥{{ formatMoney(row.original_price) }}</span>
          </div>
        </template>
        <template #cell-seven_day_quota_usd="{ value }">
          <span v-if="value != null" class="text-sm font-medium text-gray-900 dark:text-white">${{ formatMoney(value) }}</span>
          <span v-else class="text-sm text-gray-400">-</span>
        </template>
        <template #cell-validity_days="{ value, row }">
          <span class="text-sm">{{ value }} {{ t('payment.admin.' + validityUnitLabelKey(row.validity_unit)) }}</span>
        </template>
        <template #cell-seat_limit="{ row }">
          <span v-if="row.seat_limit === null || row.seat_limit === undefined" class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.admin.seatUnlimited') }}</span>
          <span v-else :class="getSeatUsageClass(row)">{{ row.seat_used || 0 }}/{{ row.seat_limit }}</span>
          <span v-for="display in virtualSeatDisplays(row)" :key="display" class="ml-1 text-xs text-gray-500 dark:text-gray-400">· {{ t('payment.admin.virtualSeatDisplay') }} {{ display }}</span>
        </template>
        <template #cell-for_sale="{ value, row }">
          <button
            type="button"
            :class="[
              'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
              value ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
            ]"
            @click="toggleForSale(row)"
          >
            <span :class="[
              'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              value ? 'translate-x-4' : 'translate-x-0'
            ]" />
          </button>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex items-center gap-2">
            <button @click="openPlanEdit(row)" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400">
              <Icon name="edit" size="sm" />
              <span class="text-xs">{{ t('common.edit') }}</span>
            </button>
            <button @click="confirmDeletePlan(row)" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400">
              <Icon name="trash" size="sm" />
              <span class="text-xs">{{ t('common.delete') }}</span>
            </button>
          </div>
        </template>
      </DataTable>
    </div>

    <!-- Plan Edit Dialog -->
    <PlanEditDialog :show="showPlanDialog" :plan="editingPlan" :payment-config="paymentConfig" @close="showPlanDialog = false" @saved="loadPlans" />

    <ConfirmDialog :show="showDeletePlanDialog" :title="t('payment.admin.deletePlan')" :message="t('payment.admin.deletePlanConfirm')" :confirm-text="t('common.delete')" danger @confirm="handleDeletePlan" @cancel="showDeletePlanDialog = false" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { SubscriptionPlan } from '@/types/payment'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import PlanEditDialog from './PlanEditDialog.vue'

const { t } = useI18n()
const appStore = useAppStore()

// Payment config drives the subscription CNY charge preview in PlanEditDialog.
// Plans are decoupled from groups in this fork, so no group state is loaded here.
const paymentConfig = ref<AdminPaymentConfig | null>(null)

async function loadPaymentConfig() {
  try {
    const res = await adminPaymentAPI.getConfig()
    paymentConfig.value = res.data
  } catch { /* preview only */ }
}

// ==================== Plans ====================

const plansLoading = ref(false)
const plans = ref<SubscriptionPlan[]>([])
const showPlanDialog = ref(false)
const showDeletePlanDialog = ref(false)
const editingPlan = ref<SubscriptionPlan | null>(null)
const deletingPlanId = ref<number | null>(null)

const planColumns = computed((): Column[] => [
  { key: 'id', label: 'ID' },
  { key: 'name', label: t('payment.admin.planName') },
  { key: 'price', label: t('payment.admin.price') },
  { key: 'seven_day_quota_usd', label: t('payment.admin.sevenDayQuota') },
  { key: 'validity_days', label: t('payment.admin.validityDays') },
  { key: 'seat_limit', label: t('payment.admin.seatUsage') },
  { key: 'for_sale', label: t('payment.admin.forSale') },
  { key: 'sort_order', label: t('payment.admin.sortOrder') },
  { key: 'actions', label: t('common.actions') },
])

async function loadPlans() {
  plansLoading.value = true
  try {
    const res = await adminPaymentAPI.getPlans()
    // Backend returns features as newline-separated string; parse to array
    plans.value = (res.data || []).map((p: Omit<SubscriptionPlan, 'features'> & { features: string | string[] }) => ({
      ...p,
      features: typeof p.features === 'string'
        ? p.features.split('\n').map((f: string) => f.trim()).filter(Boolean)
        : (p.features || []),
    }))
  }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { plansLoading.value = false }
}

function openPlanEdit(plan: SubscriptionPlan | null) {
  editingPlan.value = plan
  showPlanDialog.value = true
}

function formatMoney(value: number | null | undefined): string {
  return (value ?? 0).toFixed(2)
}

function validityUnitLabelKey(unit: string | null | undefined): string {
  switch (unit) {
    case 'week':
    case 'weeks':
      return 'weeks'
    case 'month':
    case 'months':
      return 'months'
    default:
      return 'days'
  }
}

function getSeatUsageClass(plan: SubscriptionPlan): string {
  const base = 'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium'
  if (plan.seat_over_limit) return `${base} bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300`
  if (plan.seat_full) return `${base} bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300`
  return `${base} bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300`
}

function virtualSeatDisplay(plan: SubscriptionPlan): string {
  if (plan.seat_limit === null || plan.seat_limit === undefined) return ''
  if (plan.virtual_seat_start === null || plan.virtual_seat_start === undefined) return ''
  if (plan.virtual_seat_total === null || plan.virtual_seat_total === undefined) return ''
  return `${plan.virtual_seat_start + (plan.seat_used || 0)}/${plan.virtual_seat_total}`
}

function virtualSeatDisplays(plan: SubscriptionPlan): string[] {
  const display = virtualSeatDisplay(plan)
  return display ? [display] : []
}

/** Quick toggle for_sale from the list */
async function toggleForSale(plan: SubscriptionPlan) {
  try {
    await adminPaymentAPI.updatePlan(plan.id, { for_sale: !plan.for_sale })
    plan.for_sale = !plan.for_sale
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

function confirmDeletePlan(plan: SubscriptionPlan) { deletingPlanId.value = plan.id; showDeletePlanDialog.value = true }
async function handleDeletePlan() {
  if (!deletingPlanId.value) return
  try { await adminPaymentAPI.deletePlan(deletingPlanId.value); appStore.showSuccess(t('common.deleted')); showDeletePlanDialog.value = false; loadPlans() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

// ==================== Lifecycle ====================

onMounted(() => {
  loadPaymentConfig()
  loadPlans()
})
</script>
