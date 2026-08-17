import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getCaptureSettings, type CaptureSettings } from '@/api/admin/captureSettings'

const POLL_INTERVAL_MS = 15_000
const ACK_PREFIX = 'capture-loss-ack:'

export const useCaptureHealthStore = defineStore('captureHealth', () => {
  const settings = ref<CaptureSettings | null>(null)
  const loading = ref(false)
  const error = ref('')
  const hasUnacknowledgedLoss = ref(false)
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let pollConsumers = 0
  let activeRefresh: Promise<CaptureSettings> | null = null

  function acknowledgementKey(healthSourceID: string): string {
    return `${ACK_PREFIX}${healthSourceID || 'unknown'}`
  }

  function readAcknowledgedCount(healthSourceID: string): number {
    try {
      const value = Number(localStorage.getItem(acknowledgementKey(healthSourceID)))
      return Number.isFinite(value) && value >= 0 ? value : 0
    } catch {
      return 0
    }
  }

  function applySettings(next: CaptureSettings): void {
    settings.value = next
    const dropped = Math.max(0, Number(next.dropped_records) || 0)
    hasUnacknowledgedLoss.value = dropped > readAcknowledgedCount(next.health_source_id)
  }

  function refresh(): Promise<CaptureSettings> {
    if (activeRefresh) return activeRefresh
    loading.value = true
    activeRefresh = (async () => {
      try {
        const next = await getCaptureSettings()
        applySettings(next)
        error.value = ''
        return next
      } catch (cause) {
        error.value = cause instanceof Error ? cause.message : 'Failed to load capture settings'
        throw cause
      } finally {
        loading.value = false
        activeRefresh = null
      }
    })()
    return activeRefresh
  }

  async function startPolling(): Promise<void> {
    pollConsumers += 1
    if (pollConsumers > 1) {
      if (activeRefresh) {
        try {
          await activeRefresh
        } catch {
          // The first polling consumer reports the shared refresh failure.
        }
      }
      return
    }
    try {
      await refresh()
    } catch (cause) {
      console.error('[captureHealth] Failed to refresh capture settings:', cause)
    }
    if (pollConsumers > 0 && pollTimer === null) {
      pollTimer = setInterval(() => {
        void refresh().catch((cause) => {
          console.error('[captureHealth] Failed to refresh capture settings:', cause)
        })
      }, POLL_INTERVAL_MS)
    }
  }

  function stopPolling(): void {
    pollConsumers = Math.max(0, pollConsumers - 1)
    if (pollConsumers === 0 && pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  function acknowledgeLoss(): void {
    const current = settings.value
    if (!current) return
    try {
      localStorage.setItem(
        acknowledgementKey(current.health_source_id),
        String(Math.max(0, Number(current.dropped_records) || 0)),
      )
    } catch {
      // The live badge can still be cleared for this page lifetime.
    }
    hasUnacknowledgedLoss.value = false
  }

  return {
    settings,
    loading,
    error,
    hasUnacknowledgedLoss,
    refresh,
    applySettings,
    startPolling,
    stopPolling,
    acknowledgeLoss,
  }
})
