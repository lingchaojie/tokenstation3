import { describe, expect, it } from 'vitest'

import {
  isDailyCheckInActive,
  isoToUTC8LocalInput,
  utc8LocalInputToISO,
} from '@/utils/dailyCheckIn'

describe('dailyCheckIn time helpers', () => {
  it('uses a left-closed right-open activity window', () => {
    const settings = {
      daily_check_in_enabled: true,
      daily_check_in_start_at: '2026-07-20T00:00:00Z',
      daily_check_in_end_at: '2026-07-27T00:00:00Z',
    }

    expect(isDailyCheckInActive(settings, Date.parse('2026-07-19T23:59:59.999Z'))).toBe(false)
    expect(isDailyCheckInActive(settings, Date.parse('2026-07-20T00:00:00Z'))).toBe(true)
    expect(isDailyCheckInActive(settings, Date.parse('2026-07-26T23:59:59.999Z'))).toBe(true)
    expect(isDailyCheckInActive(settings, Date.parse('2026-07-27T00:00:00Z'))).toBe(false)
  })

  it('fails closed for disabled or invalid settings', () => {
    expect(isDailyCheckInActive({ daily_check_in_enabled: false }, Date.now())).toBe(false)
    expect(isDailyCheckInActive({
      daily_check_in_enabled: true,
      daily_check_in_start_at: 'invalid',
      daily_check_in_end_at: '2026-07-27T00:00:00Z',
    }, Date.now())).toBe(false)
  })

  it('round-trips a UTC+8 datetime-local value independently of browser timezone', () => {
    expect(utc8LocalInputToISO('2026-07-20T00:00')).toBe('2026-07-19T16:00:00.000Z')
    expect(isoToUTC8LocalInput('2026-07-19T16:00:00Z')).toBe('2026-07-20T00:00')
  })
})
