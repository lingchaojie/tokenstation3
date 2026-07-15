import { apiClient } from './client'

export type DailyCheckInState = 'disabled' | 'upcoming' | 'active' | 'ended'

export interface DailyCheckInClaim {
  reward_amount: number
  balance_after: number
  claimed_at: string
}

export interface DailyCheckInStatus {
  state: DailyCheckInState
  active: boolean
  start_at: string | null
  end_at: string | null
  reward_amount: number
  check_in_date: string
  claimed_today: boolean
  claim?: DailyCheckInClaim
  next_reset_at: string
}

export interface DailyCheckInClaimResult {
  reward_amount: number
  balance_after: number
  check_in_date: string
  claimed_at: string
}

export const checkInAPI = {
  async getStatus(): Promise<DailyCheckInStatus> {
    const { data } = await apiClient.get<DailyCheckInStatus>('/user/check-in/status')
    return data
  },

  async claim(): Promise<DailyCheckInClaimResult> {
    const { data } = await apiClient.post<DailyCheckInClaimResult>('/user/check-in/claim')
    return data
  },
}

export default checkInAPI
