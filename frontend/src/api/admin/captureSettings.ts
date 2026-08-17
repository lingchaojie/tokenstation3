import { apiClient } from '../client'

export type CaptureHistoryRange = '24h' | '7d' | '30d'

export interface CaptureRuntimePolicy {
  version: 1
  enabled: boolean
  platforms: {
    anthropic: boolean
    kiro: boolean
    openai: boolean
    gemini: boolean
    antigravity: boolean
    grok: boolean
  }
  outcomes: {
    success: boolean
    terminal_error: boolean
  }
  content: {
    raw_request: boolean
    raw_response: boolean
    request_headers: boolean
    response_headers: boolean
  }
  model_allowlists: {
    anthropic: string[]
    kiro: string[]
  }
  group_ids: number[]
  user_ids: number[]
}

export interface CaptureSettings {
  policy: CaptureRuntimePolicy
  provisioned: boolean
  ready: boolean
  sidecar_running: boolean
  spool_ready: boolean
  delivery_ready: boolean
  spool_used_bytes: number
  spool_max_bytes: number
  spool_min_free_bytes: number
  filesystem_free_bytes: number
  ready_records: number
  oldest_ready_age_seconds: number
  current_batch_id: string
  sidecar_restart_count: number
  upload_retries: number
  last_upload_at: string | null
  health_source_id: string
  dropped_records: number
  dropped_by_reason: Record<string, number>
  initialization_error?: string
  database: string
  table: string
}

export interface CaptureHealthEvent {
  minute_bucket: string
  instance_id: string
  reason: string
  dropped_records: number
  dropped_bytes: number
  spool_used_bytes_peak: number
  ready_records_peak: number
  oldest_ready_age_seconds_peak: number
  upload_retries: number
  sidecar_restarts: number
  last_error: string
}

export interface CaptureHealthHistory {
  range: CaptureHistoryRange
  start: string
  end: string
  events: CaptureHealthEvent[]
}

export async function getCaptureSettings(): Promise<CaptureSettings> {
  const { data } = await apiClient.get<CaptureSettings>('/admin/capture-settings')
  return data
}

export async function updateCaptureSettings(policy: CaptureRuntimePolicy): Promise<CaptureSettings> {
  const { data } = await apiClient.put<CaptureSettings>('/admin/capture-settings', policy)
  return data
}

export async function getCaptureHealthHistory(range: CaptureHistoryRange): Promise<CaptureHealthHistory> {
  const { data } = await apiClient.get<CaptureHealthHistory>('/admin/capture-settings/history', {
    params: { range },
  })
  return data
}

export const captureSettingsAPI = {
  getCaptureSettings,
  updateCaptureSettings,
  getCaptureHealthHistory,
}

export default captureSettingsAPI
