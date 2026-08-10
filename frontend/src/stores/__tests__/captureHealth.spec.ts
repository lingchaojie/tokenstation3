import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getCaptureSettings } = vi.hoisted(() => ({ getCaptureSettings: vi.fn() }))

vi.mock('@/api/admin/captureSettings', () => ({ getCaptureSettings }))

import { useCaptureHealthStore } from '../captureHealth'

function settings(startedAt: string, dropped: number) {
  return {
    policy: {
      version: 1 as const,
      enabled: false,
      platforms: { anthropic: true, kiro: true, openai: false },
      outcomes: { success: true, terminal_error: true },
      content: { raw_request: true, raw_response: true, request_headers: true, response_headers: true },
      group_ids: [],
      user_ids: [],
    },
    provisioned: false,
    ready: false,
    addresses: [],
    database: '',
    table: '',
    capacity: {
      max_body_bytes: 0,
      max_queue_bytes: 0,
      queue_size: 0,
      worker_count: 0,
      writer_queue_size: 0,
      overflow_policy: 'drop',
      overflow_sample_percent: 0,
      batch_max_size: 0,
      batch_max_interval_ms: 0,
    },
    health: {
      started_at: startedAt,
      submitted_records: dropped,
      accepted_records: 0,
      written_records: 0,
      dropped_records: dropped,
      dropped_bytes: dropped * 100,
      dropped_by_reason: {},
      worker_queue: { current: 0, peak: 0, capacity: 0 },
      writer_queue: { current: 0, peak: 0, capacity: 0 },
      in_flight_bytes: { current: 0, peak: 0, capacity: 0 },
      last_drop_reason: '',
      last_error: '',
      recent_incidents: [],
    },
  }
}

describe('capture health store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    getCaptureSettings.mockReset()
  })

  it('persists acknowledgement per process start and alerts on later losses', async () => {
    getCaptureSettings.mockResolvedValueOnce(settings('process-a', 2))
    const store = useCaptureHealthStore()
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(true)

    store.acknowledgeLoss()
    expect(store.hasUnacknowledgedLoss).toBe(false)
    expect(localStorage.getItem('capture-loss-ack:process-a')).toBe('2')

    getCaptureSettings.mockResolvedValueOnce(settings('process-a', 2))
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(false)

    getCaptureSettings.mockResolvedValueOnce(settings('process-a', 3))
    await store.refresh()
    expect(store.hasUnacknowledgedLoss).toBe(true)

    getCaptureSettings.mockResolvedValueOnce(settings('process-b', 1))
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

    resolveRequest(settings('process-a', 0))
    await Promise.all([first, second])
    expect(store.settings?.health.started_at).toBe('process-a')

    store.stopPolling()
    store.stopPolling()
  })
})
