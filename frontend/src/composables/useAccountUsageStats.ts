import { computed, ref, watch } from 'vue'
import { adminAPI } from '@/api/admin'
import type { AccountUsageStatsResponse } from '@/types'

export function useAccountUsageStats(isOpen: () => boolean, accountID: () => number | undefined) {
  const stats = ref<AccountUsageStatsResponse | null>(null)
  const loading = ref(false)

  watch([isOpen, accountID], async ([show, id], _, onCleanup) => {
    stats.value = null
    loading.value = show && id !== undefined
    if (!show || id === undefined) return

    let current = true
    onCleanup(() => { current = false })
    try {
      const result = await adminAPI.accounts.getStats(id, 30)
      if (current) stats.value = result
    } catch (error) {
      if (current) console.error('Failed to load account stats:', error)
    } finally {
      if (current) loading.value = false
    }
  }, { immediate: true })

  const breakdown = computed(() => {
    const report = stats.value
    const id = accountID()
    const history = report?.history ?? []
    const startDate = report?.start_date ?? history[0]?.date
    const endDate = report?.end_date ?? history[history.length - 1]?.date
    return {
      startDate,
      endDate,
      enableBreakdown: id !== undefined && Boolean(startDate && endDate),
      filters: { account_id: id, timezone: report?.timezone ?? '' },
    }
  })

  return { stats, loading, breakdown }
}
