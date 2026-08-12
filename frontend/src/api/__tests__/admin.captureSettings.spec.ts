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
})
