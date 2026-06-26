<template>
  <div class="linx-panel overflow-hidden">
    <div class="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-linear-hairline">
      <h2 class="text-sm font-semibold tracking-[-0.02em] text-gray-950 dark:text-linear-ink">{{ t('dashboard.recentUsage') }}</h2>
      <span class="text-xs text-gray-500 dark:text-linear-ink-subtle">{{ t('dashboard.last7Days') }}</span>
    </div>
    <div class="p-5">
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner size="lg" />
      </div>
      <div v-else-if="data.length === 0" class="py-8">
        <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
      </div>
      <div v-else class="space-y-3">
        <div v-for="log in data" :key="log.id" class="linx-data-row px-1 transition-colors hover:bg-gray-50 dark:hover:bg-linear-surface-2/60">
          <div class="flex items-center gap-4">
            <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-primary-100 dark:bg-primary-900/30">
              <Icon name="beaker" size="md" class="text-primary-600 dark:text-primary-400" />
            </div>
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">{{ log.model }}</p>
              <p class="text-xs text-gray-500 dark:text-linear-ink-subtle">{{ formatDateTime(log.created_at) }}</p>
            </div>
          </div>
          <div class="text-right">
            <p class="text-sm font-semibold">
              <span class="text-green-600 dark:text-green-400" :title="t('dashboard.actual')">${{ formatCost(log.actual_cost) }}</span>
              <span v-if="showStandardCosts" class="font-normal text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(log.total_cost) }}</span>
            </p>
            <p class="text-xs text-gray-500 dark:text-linear-ink-subtle">{{ (log.input_tokens + log.output_tokens).toLocaleString() }} tokens</p>
          </div>
        </div>

        <router-link to="/usage" class="btn btn-secondary mt-3 flex w-full items-center justify-center">
          {{ t('dashboard.viewAllUsage') }}
          <Icon name="arrowRight" size="sm" />
        </router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { UsageLog } from '@/types'

withDefaults(defineProps<{
  data: UsageLog[]
  loading: boolean
  showStandardCosts?: boolean
}>(), {
  showStandardCosts: false,
})
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)
</script>
