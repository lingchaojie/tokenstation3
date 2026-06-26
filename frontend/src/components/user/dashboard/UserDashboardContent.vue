<template>
  <div class="linear-dashboard-page space-y-5">
    <div v-if="loading" class="linx-panel flex items-center justify-center py-12"><LoadingSpinner /></div>
    <template v-else-if="stats">
      <UserDashboardStats
        :stats="stats"
        :balance="user?.balance || 0"
        :is-simple="authStore.isSimpleMode"
        :show-standard-costs="showStandardCosts"
        :subscription-balance="subscriptionStore.subscriptionBalanceSummary"
        :subscription-plans="subscriptionPlans"
        :active-subscriptions="subscriptionStore.activeSubscriptions"
        :subscription-balance-fallback-enabled="user?.subscription_balance_fallback_enabled ?? false"
      />
      <div class="grid gap-5 lg:grid-cols-[1.2fr_0.8fr]">
        <div class="min-w-0 space-y-5">
          <UserDashboardCharts
            v-model:startDate="startDate"
            v-model:endDate="endDate"
            v-model:granularity="granularity"
            :loading="loadingCharts"
            :trend="trendData"
            :models="modelStats"
            :show-standard-costs="showStandardCosts"
            @dateRangeChange="loadCharts"
            @granularityChange="loadCharts"
            @refresh="refreshAll"
          />
          <UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" :show-standard-costs="showStandardCosts" />
        </div>
        <UserDashboardQuickActions />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { paymentAPI } from '@/api/payment'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscriptions'
import type { ModelStat, TrendDataPoint, UsageLog } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'

const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const user = computed(() => authStore.user)

withDefaults(defineProps<{
  showStandardCosts?: boolean
}>(), {
  showStandardCosts: false,
})

const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const subscriptionPlans = ref<SubscriptionPlan[]>([])

const formatLD = (d: Date) => {
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const startDate = ref(formatLD(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(formatLD(new Date()))
const granularity = ref('day')

const loadStats = async () => {
  loading.value = true
  try {
    await authStore.refreshUser()
    stats.value = await usageAPI.getDashboardStats()
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loading.value = false
  }
}

const loadCharts = async () => {
  loadingCharts.value = true
  try {
    const res = await Promise.all([
      usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }),
      usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value }),
    ])
    trendData.value = res[0].trend || []
    modelStats.value = res[1].models || []
  } catch (error) {
    console.error('Failed to load charts:', error)
  } finally {
    loadingCharts.value = false
  }
}

const loadRecent = async () => {
  loadingUsage.value = true
  try {
    const res = await usageAPI.getByDateRange(startDate.value, endDate.value)
    recentUsage.value = res.items.slice(0, 5)
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    loadingUsage.value = false
  }
}

const loadSubscriptionData = async () => {
  if (authStore.isSimpleMode) return
  try {
    const [checkout] = await Promise.all([paymentAPI.getCheckoutInfo(), subscriptionStore.fetchActiveSubscriptions()])
    subscriptionPlans.value = checkout.data.plans ?? []
  } catch (error) {
    console.warn('Failed to load subscription data:', error)
    subscriptionPlans.value = []
  }
}

const refreshAll = () => {
  loadStats()
  loadCharts()
  loadRecent()
  loadSubscriptionData()
}

onMounted(() => {
  refreshAll()
})
</script>
