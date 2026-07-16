<template>
  <!-- Balance Cards -->
  <div v-if="!isSimple" class="space-y-4">
    <div class="linx-panel p-6">
      <div class="mb-5 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-orange-500 dark:text-orange-300">{{ t('dashboard.currentSubscription') }}</p>
          <p v-if="subscriptionPlanLabel" class="mt-2 text-lg font-semibold tracking-[-0.03em] text-gray-950 dark:text-linear-ink">
            {{ subscriptionPlanLabel }}
          </p>
          <p v-else class="mt-2 text-lg font-semibold tracking-[-0.03em] text-gray-400 dark:text-gray-500">
            {{ t('dashboard.noCurrentSubscription') }}
          </p>
          <p v-if="!subscriptionBalance" class="mt-2 max-w-xl text-sm leading-6 text-gray-500 dark:text-linear-ink-subtle">
            {{ t('dashboard.noSubscriptionPurchaseHint') }}
          </p>
          <button
            v-if="!subscriptionBalance"
            type="button"
            data-testid="dashboard-buy-subscription"
            class="mt-4 inline-flex items-center justify-center rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-400"
            @click="goToSubscriptionPurchase"
          >
            {{ t('dashboard.buySubscription') }}
          </button>
        </div>
        <div v-if="subscriptionBalance" class="min-w-0 lg:w-80">
          <p class="text-sm text-gray-500 dark:text-linear-ink-subtle">
            {{ t('dashboard.subscriptionRemaining', {
              remaining: `$${formatBalance(subscriptionBalance.remaining)}`,
              total: `$${formatBalance(subscriptionBalance.total)}`,
            }) }}
          </p>
          <div
            class="mt-2 h-2.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
            role="progressbar"
            :aria-valuenow="subscriptionRemainingPercent"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <div
              class="h-full rounded-full bg-green-500 transition-all dark:bg-green-400"
              :style="{ width: `${subscriptionRemainingPercent}%` }"
            />
          </div>
          <p v-if="subscriptionBalance.resetAt" class="mt-2 text-xs text-gray-500 dark:text-linear-ink-subtle">
            {{ t('dashboard.subscriptionResetAt', { time: formatResetTime(subscriptionBalance.resetAt) }) }}
          </p>
        </div>
      </div>
    </div>

    <div class="linx-panel p-6">
      <div class="flex items-start justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-emerald-500 dark:text-emerald-300">{{ t('dashboard.rechargeBalance') }}</p>
          <p class="mt-3 text-3xl font-bold tracking-[-0.05em] text-gray-950 dark:text-linear-ink">${{ formatBalance(balance) }}</p>
          <RewardBalanceBreakdown
            class="mt-3"
            :summary="rewardBalances"
          />
          <p class="mt-3 text-sm text-gray-500 dark:text-linear-ink-subtle">{{ t('dashboard.balanceOrderHint') }}</p>
        </div>
        <div class="rounded-2xl bg-emerald-100 p-3 dark:bg-emerald-900/30">
          <svg class="h-7 w-7 text-emerald-600 dark:text-emerald-300" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z" />
          </svg>
        </div>
      </div>

      <div class="mt-5 rounded-2xl border border-emerald-100 bg-emerald-50/70 p-4 dark:border-emerald-500/20 dark:bg-emerald-500/10">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <p id="subscription-balance-fallback-toggle-label" class="text-sm font-semibold tracking-[-0.02em] text-gray-950 dark:text-linear-ink">
              {{ t('dashboard.balanceFallbackToggle.title') }}
            </p>
            <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-linear-ink-subtle">
              {{ t(balanceFallbackEnabled ? 'dashboard.balanceFallbackToggle.enabledHint' : 'dashboard.balanceFallbackToggle.disabledHint') }}
            </p>
          </div>
          <button
            type="button"
            class="relative inline-flex h-6 w-11 shrink-0 rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-60"
            :class="balanceFallbackEnabled ? 'bg-emerald-500 dark:bg-emerald-400' : 'bg-gray-300 dark:bg-dark-600'"
            role="switch"
            :aria-checked="balanceFallbackEnabled"
            aria-labelledby="subscription-balance-fallback-toggle-label"
            :disabled="savingBalanceFallback"
            data-testid="subscription-balance-fallback-toggle"
            @click="toggleBalanceFallback"
          >
            <span
              class="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-transform"
              :class="balanceFallbackEnabled ? 'translate-x-5' : 'translate-x-0'"
            />
          </button>
        </div>
      </div>
    </div>
  </div>

  <!-- Row 1: Core Stats -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <!-- API Keys -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30">
          <Icon name="key" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.apiKeys') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats?.total_api_keys || 0 }}</p>
          <p class="text-xs text-green-600 dark:text-green-400">{{ stats?.active_api_keys || 0 }} {{ t('common.active') }}</p>
        </div>
      </div>
    </div>

    <!-- Today Requests -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30">
          <Icon name="chart" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.todayRequests') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats?.today_requests || 0 }}</p>
          <p class="text-xs linx-muted">{{ t('common.total') }}: {{ formatNumber(stats?.total_requests || 0) }}</p>
        </div>
      </div>
    </div>

    <!-- Today Cost -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30">
          <Icon name="dollar" size="md" class="text-purple-600 dark:text-purple-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.todayCost') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.today_actual_cost || 0) }}</span>
            <span v-if="showStandardCosts" class="text-sm font-normal text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(stats?.today_cost || 0) }}</span>
          </p>
          <p class="text-xs">
            <span class="text-gray-500 dark:text-linear-ink-subtle">{{ t('common.total') }}: </span>
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actual')">${{ formatCost(stats?.total_actual_cost || 0) }}</span>
            <span v-if="showStandardCosts" class="text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(stats?.total_cost || 0) }}</span>
          </p>
        </div>
      </div>
    </div>
  </div>

  <!-- Row 2: Token Stats -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <!-- Today Tokens -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30">
          <Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.todayTokens') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ formatTokens(stats?.today_tokens || 0) }}</p>
          <p class="text-xs linx-muted">{{ t('dashboard.input') }}: {{ formatTokens(stats?.today_input_tokens || 0) }} / {{ t('dashboard.output') }}: {{ formatTokens(stats?.today_output_tokens || 0) }}</p>
        </div>
      </div>
    </div>

    <!-- Total Tokens -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-indigo-100 p-2 dark:bg-indigo-900/30">
          <Icon name="database" size="md" class="text-indigo-600 dark:text-indigo-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.totalTokens') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ formatTokens(stats?.total_tokens || 0) }}</p>
          <p class="text-xs linx-muted">{{ t('dashboard.input') }}: {{ formatTokens(stats?.total_input_tokens || 0) }} / {{ t('dashboard.output') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</p>
        </div>
      </div>
    </div>

    <!-- Performance (RPM/TPM) -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30">
          <Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" :stroke-width="2" />
        </div>
        <div class="flex-1">
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.performance') }}</p>
          <div class="flex items-baseline gap-2">
            <p class="text-xl font-bold text-gray-900 dark:text-white">{{ formatTokens(stats?.rpm || 0) }}</p>
            <span class="text-xs linx-muted">RPM</span>
          </div>
          <div class="flex items-baseline gap-2">
            <p class="text-sm font-semibold text-violet-600 dark:text-violet-400">{{ formatTokens(stats?.tpm || 0) }}</p>
            <span class="text-xs linx-muted">TPM</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Avg Response Time -->
    <div class="linx-panel p-4">
      <div class="flex items-center gap-3">
        <div class="rounded-lg bg-rose-100 p-2 dark:bg-rose-900/30">
          <Icon name="clock" size="md" class="text-rose-600 dark:text-rose-400" :stroke-width="2" />
        </div>
        <div>
          <p class="text-xs font-medium linx-muted">{{ t('dashboard.avgResponse') }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
          <p class="text-xs linx-muted">{{ t('dashboard.averageTime') }}</p>
        </div>
      </div>
    </div>
  </div>

</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { userAPI } from '@/api/user'
import Icon from '@/components/icons/Icon.vue'
import RewardBalanceBreakdown from '@/components/user/RewardBalanceBreakdown.vue'
import { useAuthStore } from '@/stores/auth'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'
import type { RewardBalanceSummary, SubscriptionBalanceSummary, UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import { displayMonthlyPlanName } from '@/utils/monthlyPlans'

const props = withDefaults(defineProps<{
  stats: UserStatsType
  balance: number
  isSimple: boolean
  showStandardCosts?: boolean
  subscriptionBalance?: SubscriptionBalanceSummary | null
  subscriptionPlans?: SubscriptionPlan[]
  activeSubscriptions?: UserSubscription[]
  subscriptionBalanceFallbackEnabled?: boolean
  rewardBalances?: RewardBalanceSummary | null
}>(), {
  showStandardCosts: false,
})
const { t, locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const balanceFallbackEnabled = ref(props.subscriptionBalanceFallbackEnabled ?? false)
const savingBalanceFallback = ref(false)

watch(() => props.subscriptionBalanceFallbackEnabled, (value) => {
  if (!savingBalanceFallback.value) {
    balanceFallbackEnabled.value = value ?? false
  }
})

const subscriptionPlanLabel = computed(() => {
  if (!props.subscriptionBalance) return null
  if (props.subscriptionBalance.displayMode === 'multiple') {
    return t('dashboard.subscriptionPlanCount', {
      count: props.subscriptionBalance.activePlanCount ?? props.subscriptionBalance.planNames?.length ?? 0,
    })
  }
  return displayMonthlyPlanName(props.subscriptionBalance.planName, String(locale.value))
})

const subscriptionRemainingPercent = computed(() => {
  if (!props.subscriptionBalance?.total) return 0
  return calcPercent(props.subscriptionBalance.remaining, props.subscriptionBalance.total)
})

function goToSubscriptionPurchase() {
  router.push({
    path: '/purchase',
    query: {
      tab: 'subscription',
    },
  })
}

async function toggleBalanceFallback() {
  if (savingBalanceFallback.value) return

  const previous = balanceFallbackEnabled.value
  const next = !previous
  balanceFallbackEnabled.value = next
  savingBalanceFallback.value = true

  try {
    const updated = await userAPI.updateProfile({ subscription_balance_fallback_enabled: next })
    authStore.user = updated
    balanceFallbackEnabled.value = updated.subscription_balance_fallback_enabled ?? next
  } catch (error) {
    console.error('Failed to update subscription balance fallback preference:', error)
    balanceFallbackEnabled.value = previous
  } finally {
    savingBalanceFallback.value = false
  }
}

function calcPercent(usage: number, limit: number): number {
  if (!limit || limit <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((usage / limit) * 100)))
}

function formatResetTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  const hour = String(d.getHours()).padStart(2, '0')
  const minute = String(d.getMinutes()).padStart(2, '0')
  return `${year}/${month}/${day} ${hour}:${minute}`
}

const formatBalance = (b: number) =>
  new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(b)

const formatNumber = (n: number) => n.toLocaleString()
const formatCost = (c: number) => c.toFixed(4)
const formatTokens = (t: number) => {
  if (t >= 1_000_000) return `${(t / 1_000_000).toFixed(1)}M`
  if (t >= 1000) return `${(t / 1000).toFixed(1)}K`
  return t.toString()
}
const formatDuration = (ms: number) => ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms.toFixed(0)}ms`
</script>
