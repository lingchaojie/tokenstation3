import { apiClient } from '@/api/client'
import type { DailyCheckInState } from '@/api/checkIn'

export interface DailyCheckInAdminConfig {
  enabled: boolean
  start_at: string | null
  duration_days: number
  reward_amount: number
  end_at: string | null
  state: DailyCheckInState
}

export interface UpdateDailyCheckInAdminConfig {
  enabled: boolean
  start_at: string | null
  duration_days: number
  reward_amount: number
}

export const adminCheckInAPI = {
  async getConfig(): Promise<DailyCheckInAdminConfig> {
    const { data } = await apiClient.get<DailyCheckInAdminConfig>('/admin/check-in/config')
    return data
  },

  async updateConfig(input: UpdateDailyCheckInAdminConfig): Promise<DailyCheckInAdminConfig> {
    const { data } = await apiClient.put<DailyCheckInAdminConfig>('/admin/check-in/config', input)
    return data
  },
}

export default adminCheckInAPI
