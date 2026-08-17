import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getCaptureSettings } = vi.hoisted(() => ({ getCaptureSettings: vi.fn() }))

vi.mock('@/api/admin/captureSettings', () => ({ getCaptureSettings }))

import { useCaptureHealthStore } from '../captureHealth'

function settings(healthSourceID: string, dropped: number) {
  return {
    policy: {
      version: 1 as const,
      enabled: false,
      platforms: { anthropic: true, kiro: true, openai: false, gemini: true, antigravity: true, grok: true },
      outcomes: { success: true, terminal_error: true },
      content: { raw_request: true, raw_response: true, request_headers: true, response_headers: true },
      model_allowlists: { anthropic: ['claude-fable-5', 'claude-opus-5'], kiro: ['claude-fable-5', 'claude-opus-5'] },
      group_ids: [],
      user_ids: [],
    },
    provisioned: false,
    ready: false,
    sidecar_running: false,
    spool_ready: false,
    delivery_ready: false,
    spool_used_bytes: 0,
    spool_max_bytes: 12 * 2 ** 30,
    spool_min_free_bytes: 8 * 2 ** 30,
    filesystem_free_bytes: 0,
    ready_records: 0,
    oldest_ready_age_seconds: 0,
    current_batch_id: '',
    sidecar_restart_count: 0,
    upload_retries: 0,
    health_source_id: healthSourceID,
    dropped_records: dropped,
    dropped_by_reason: {},
    database: '',
    table: '',
  }
}

describe('capture health store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    getCaptureSettings.mockReset()
  })

  it('persists acknowledgement per sidecar health source and alerts on later losses', async () => {
    getCaptureSettings.mockResolvedValueOnce(settings('source-a', 2))
    const store = useCaptureHealthStore()
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(true)

    store.acknowledgeLoss()
    expect(store.hasUnacknowledgedLoss).toBe(false)
    expect(localStorage.getItem('capture-loss-ack:source-a')).toBe('2')

    getCaptureSettings.mockResolvedValueOnce(settings('source-a', 2))
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(false)

    getCaptureSettings.mockResolvedValueOnce(settings('source-a', 3))
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(true)

    getCaptureSettings.mockResolvedValueOnce(settings('source-b', 1))
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(true)
  })

  it('shares an in-flight initial refresh between polling consumers', async () => {
    let resolveRequest!: (value: ReturnType<typeof settings>) => void
    getCaptureSettings.mockReturnValue(new Promise((resolve) => { resolveRequest = resolve }))
    const store = useCaptureHealthStore()

    const first = store.startPolling()
    const second = store.startPolling()
    expect(getCaptureSettings).toHaveBeenCalledTimes(1)

    resolveRequest(settings('source-a', 0))
    await Promise.all([first, second])
    expect(store.settings?.health_source_id).toBe('source-a')

    store.stopPolling()
    store.stopPolling()
  })
})
