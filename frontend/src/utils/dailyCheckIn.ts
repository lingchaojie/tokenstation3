export interface DailyCheckInPublicSettings {
  daily_check_in_enabled?: boolean
  daily_check_in_start_at?: string
  daily_check_in_end_at?: string
}

export function isDailyCheckInActive(
  settings: DailyCheckInPublicSettings | null | undefined,
  now = Date.now(),
): boolean {
  if (settings?.daily_check_in_enabled !== true) return false
  const start = Date.parse(settings.daily_check_in_start_at || '')
  const end = Date.parse(settings.daily_check_in_end_at || '')
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return false
  return now >= start && now < end
}

export function utc8LocalInputToISO(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) {
    throw new Error('Invalid UTC+8 datetime-local value')
  }
  const parsed = new Date(`${value}:00+08:00`)
  if (!Number.isFinite(parsed.getTime()) || isoToUTC8LocalInput(parsed.toISOString()) !== value) {
    throw new Error('Invalid UTC+8 datetime-local value')
  }
  return parsed.toISOString()
}

export function isoToUTC8LocalInput(value: string | null | undefined): string {
  if (!value) return ''
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return ''
  return new Date(parsed + 8 * 60 * 60 * 1000).toISOString().slice(0, 16)
}
