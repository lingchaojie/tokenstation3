import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put },
}))

import {
  getCaptureHealthHistory,
  getCaptureSettings,
  updateCaptureSettings,
  type CaptureRuntimePolicy,
  type CaptureSettings,
} from '@/api/admin/captureSettings'

const policy: CaptureRuntimePolicy = {
  version: 1,
  enabled: false,
  platforms: { anthropic: true, kiro: true, openai: false, gemini: true, antigravity: true, grok: true },
  outcomes: { success: true, terminal_error: true },
  content: {
    raw_request: true,
    raw_response: true,
    request_headers: true,
    response_headers: true,
  },
  group_ids: [],
  user_ids: [],
}

describe('admin capture settings API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('loads and completely replaces the runtime policy', async () => {
    get.mockResolvedValue({ data: { policy } })
    put.mockResolvedValue({ data: { policy } })

    await getCaptureSettings()
    await updateCaptureSettings(policy)

    expect(get).toHaveBeenCalledWith('/admin/capture-settings')
    expect(put).toHaveBeenCalledWith('/admin/capture-settings', policy)
  })

  it('requests one of the supported durable history ranges', async () => {
    get.mockResolvedValue({ data: { range: '7d', start: '', end: '', events: [] } })

    await getCaptureHealthHistory('7d')

    expect(get).toHaveBeenCalledWith('/admin/capture-settings/history', {
      params: { range: '7d' },
    })
  })

  it('exposes spool and delivery operations without a secret-bearing endpoint contract', async () => {
    const settings: CaptureSettings = {
      policy,
      provisioned: true,
      ready: true,
      sidecar_running: true,
      spool_ready: true,
      delivery_ready: false,
      spool_used_bytes: 9 * 2 ** 30,
      spool_max_bytes: 12 * 2 ** 30,
      spool_min_free_bytes: 8 * 2 ** 30,
      filesystem_free_bytes: 10 * 2 ** 30,
      ready_records: 42,
      oldest_ready_age_seconds: 90,
      current_batch_id: '19b5c4f7-b8f7-45f1-b45f-dc17fdbb2d9d',
      sidecar_restart_count: 2,
      upload_retries: 7,
      last_upload_at: '2026-08-17T00:00:00Z',
      health_source_id: 'ebd8f2e7-aac1-479d-a1de-2b4642f12156',
      dropped_records: 3,
      dropped_by_reason: { spool_cap: 3 },
      database: 'llm_archive',
      table: 'model_call_archive',
    }
    get.mockResolvedValue({ data: settings })

    await expect(getCaptureSettings()).resolves.toEqual(settings)
    expect(JSON.stringify(settings)).not.toContain('password')
    expect(JSON.stringify(settings)).not.toContain('auth_key')
    expect(JSON.stringify(settings)).not.toContain('spool_path')
    expect(JSON.stringify(settings)).not.toContain('addresses')
  })
})
