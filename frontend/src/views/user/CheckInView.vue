<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-6">
      <header>
        <h1 class="text-2xl font-semibold text-gray-950 dark:text-white">
          {{ t('checkIn.title') }}
        </h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('checkIn.description') }}
        </p>
      </header>

      <section class="card overflow-hidden">
        <div class="bg-gradient-to-br from-primary-500 to-violet-600 px-6 py-10 text-center text-white">
          <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-2xl bg-white/15 ring-1 ring-white/20">
            <Icon name="gift" size="xl" />
          </div>
          <p class="mt-5 text-sm font-medium text-white/80">{{ t('checkIn.rewardLabel') }}</p>
          <p class="mt-2 text-5xl font-bold tracking-tight">
            {{ formattedReward }}
          </p>
          <p class="mt-3 text-sm text-white/75">{{ t('checkIn.rewardHint') }}</p>
        </div>

        <div class="space-y-5 p-6">
          <div class="rounded-xl border px-4 py-3 text-sm" :class="statusClass">
            <div class="flex items-center gap-2 font-medium">
              <span class="h-2 w-2 rounded-full bg-current" />
              {{ stateText }}
            </div>
            <p v-if="periodText" class="mt-1 pl-4 opacity-80">{{ periodText }}</p>
          </div>

          <div
            v-if="status?.claimed_today"
            data-testid="check-in-claimed"
            class="rounded-xl border border-emerald-200 bg-emerald-50 p-5 text-center dark:border-emerald-800/50 dark:bg-emerald-900/20"
          >
            <Icon name="checkCircle" size="lg" class="mx-auto text-emerald-600 dark:text-emerald-400" />
            <h2 class="mt-3 font-semibold text-emerald-800 dark:text-emerald-300">
              {{ t('checkIn.claimedToday') }}
            </h2>
            <p class="mt-1 text-sm text-emerald-700 dark:text-emerald-400">
              {{ t('checkIn.claimedHint') }}
            </p>
            <p v-if="claimedBalance" class="mt-2 text-sm font-medium text-emerald-800 dark:text-emerald-300">
              {{ t('checkIn.balanceAfter', { amount: claimedBalance }) }}
            </p>
          </div>

          <button
            v-else
            data-testid="check-in-claim"
            type="button"
            class="btn btn-primary w-full py-3"
            :disabled="loading || claiming || !status?.active"
            @click="handleClaim"
          >
            <span
              v-if="loading || claiming"
              class="mr-2 h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white"
            />
            {{ claiming ? t('checkIn.claiming') : t('checkIn.claimButton') }}
          </button>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { checkInAPI, type DailyCheckInClaimResult, type DailyCheckInStatus } from '@/api/checkIn'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { extractApiErrorCode } from '@/utils/apiError'
import { formatCurrency } from '@/utils/format'

const MAX_TIMEOUT_MS = 2_147_000_000

const { locale, t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const status = ref<DailyCheckInStatus | null>(null)
const claimResult = ref<DailyCheckInClaimResult | null>(null)
const loading = ref(true)
const claiming = ref(false)
let refreshTimer: ReturnType<typeof setTimeout> | undefined

const formattedReward = computed(() => formatCurrency(status.value?.reward_amount ?? 0))
const claimedBalance = computed(() => {
  const balance = claimResult.value?.balance_after ?? status.value?.claim?.balance_after
  return balance == null ? '' : formatCurrency(balance)
})
const stateText = computed(() => t(`checkIn.states.${status.value?.state ?? 'disabled'}`))
const statusClass = computed(() => {
  if (status.value?.active) {
    return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/50 dark:bg-emerald-900/20 dark:text-emerald-300'
  }
  return 'border-gray-200 bg-gray-50 text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300'
})
const periodText = computed(() => {
  if (!status.value?.start_at || !status.value.end_at) return ''
  return t('checkIn.activityPeriod', {
    start: formatDateTime(status.value.start_at),
    end: formatDateTime(status.value.end_at),
  })
})

function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value))
}

function scheduleRefresh(): void {
  if (refreshTimer) clearTimeout(refreshTimer)
  if (!status.value) return

  const now = Date.now()
  const candidates = [status.value.next_reset_at, status.value.start_at, status.value.end_at]
    .map(value => Date.parse(value || ''))
    .filter(timestamp => Number.isFinite(timestamp) && timestamp > now)
  if (candidates.length === 0) return

  const delay = Math.min(Math.min(...candidates) - now + 250, MAX_TIMEOUT_MS)
  refreshTimer = setTimeout(() => {
    void loadStatus()
  }, delay)
}

async function loadStatus(showFailure = false): Promise<void> {
  try {
    status.value = await checkInAPI.getStatus()
    if (!status.value.claimed_today) claimResult.value = null
    scheduleRefresh()
  } catch {
    if (showFailure) appStore.showError(t('checkIn.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function handleClaim(): Promise<void> {
  if (claiming.value || !status.value?.active || status.value.claimed_today) return

  claiming.value = true
  try {
    const result = await checkInAPI.claim()
    claimResult.value = result
    status.value = {
      ...status.value,
      claimed_today: true,
      claim: {
        reward_amount: result.reward_amount,
        balance_after: result.balance_after,
        claimed_at: result.claimed_at,
      },
    }
    appStore.showSuccess(t('checkIn.claimSuccess', { amount: formatCurrency(result.reward_amount) }))
    await authStore.refreshUser().catch(() => undefined)
  } catch (error) {
    const code = extractApiErrorCode(error)
    if (code === 'DAILY_CHECK_IN_ALREADY_CLAIMED' || code === 'DAILY_CHECK_IN_INACTIVE') {
      await loadStatus()
      if (code === 'DAILY_CHECK_IN_INACTIVE') appStore.showError(stateText.value)
    } else {
      appStore.showError(t('checkIn.claimFailed'))
    }
  } finally {
    claiming.value = false
  }
}

onMounted(() => {
  void loadStatus(true)
})

onBeforeUnmount(() => {
  if (refreshTimer) clearTimeout(refreshTimer)
})
</script>
