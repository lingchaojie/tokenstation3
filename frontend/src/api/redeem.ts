/**
 * Redeem code API endpoints
 * Handles redeem code redemption for users
 */

import { apiClient } from './client'
import type { RedeemCodeRequest } from '@/types'

export interface RedeemCodePlanSummary {
  id: number
  name: string
  product_name: string
  validity_days: number
  validity_unit: string
  seven_day_quota_usd: number
  for_sale: boolean
}

export interface RedeemHistoryItem {
  id: number
  code: string
  type: string
  value: number
  status: string
  used_at: string
  created_at: string
  // Notes from admin for admin_balance/admin_concurrency types
  notes?: string
  // Subscription-specific fields
  group_id?: number | null
  plan_id?: number | null
  validity_days?: number
  group?: {
    id: number
    name: string
  }
  plan?: RedeemCodePlanSummary
}

export interface RedeemResult {
  message: string
  type: string
  value: number
  new_balance?: number
  new_concurrency?: number
  group_name?: string
  plan_id?: number | null
  plan?: RedeemCodePlanSummary
  validity_days?: number
}

/**
 * Redeem a code
 * @param code - Redeem code string
 * @returns Redemption result with updated balance or concurrency
 */
export async function redeem(code: string): Promise<RedeemResult> {
  const payload: RedeemCodeRequest = { code }

  const { data } = await apiClient.post<RedeemResult>('/redeem', payload)

  return data
}

/**
 * Get user's redemption history
 * @returns List of redeemed codes
 */
export async function getHistory(): Promise<RedeemHistoryItem[]> {
  const { data } = await apiClient.get<RedeemHistoryItem[]>('/redeem/history')
  return data
}

export const redeemAPI = {
  redeem,
  getHistory
}

export default redeemAPI
