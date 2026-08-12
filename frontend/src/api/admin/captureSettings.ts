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
  group_ids: number[]
  user_ids: number[]
}

export interface CaptureGaugeSnapshot {
  current: number
  peak: number
  capacity: number
}

export interface CaptureReasonStats {
  records: number
  bytes: number
}

export interface CaptureLossIncident {
  occurred_at: string
  reason: string
  records: number
  bytes: number
  worker_queue: number
  writer_queue: number
  in_flight_bytes: number
  error: string
}

export interface CaptureHealthSnapshot {
  started_at: string
  submitted_records: number
  accepted_records: number
  written_records: number
  dropped_records: number
  dropped_bytes: number
  dropped_by_reason: Record<string, CaptureReasonStats>
  worker_queue: CaptureGaugeSnapshot
  writer_queue: CaptureGaugeSnapshot
  in_flight_bytes: CaptureGaugeSnapshot
  last_success_at?: string
  last_drop_at?: string
  last_drop_reason: string
  last_error: string
  history_dropped_buckets: number
  recent_incidents: CaptureLossIncident[]
}

export interface CaptureCapacitySettings {
  max_body_bytes: number
  max_queue_bytes: number
  queue_size: number
  worker_count: number
  writer_queue_size: number
  overflow_policy: string
  overflow_sample_percent: number
  batch_max_size: number
  batch_max_interval_ms: number
}

export interface CaptureSettings {
  policy: CaptureRuntimePolicy
  provisioned: boolean
  ready: boolean
  initialization_error?: string
  addresses: string[]
  database: string
  table: string
  capacity: CaptureCapacitySettings
  health: CaptureHealthSnapshot
}

export interface CaptureHealthEvent {
  minute_bucket: string
  instance_id: string
  reason: string
  dropped_records: number
  dropped_bytes: number
  worker_queue_peak: number
  writer_queue_peak: number
  in_flight_bytes_peak: number
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
