/**
 * Admin Kiro API endpoints
 * Handles Kiro OAuth browser and AWS Builder ID device-code flows.
 */

import { apiClient } from '../client'

export type KiroOAuthMethod = 'builder-id' | 'aws' | 'kiro-cli' | 'google'

export interface KiroAuthUrlRequest {
  method?: KiroOAuthMethod
  proxy_id?: number
}

export interface KiroAuthUrlResponse {
  mode: 'auth_url' | 'device_code'
  method: 'builder-id' | 'kiro-cli' | 'google'
  auth_url?: string
  session_id: string
  state?: string
  verification_uri?: string
  verification_uri_complete?: string
  user_code?: string
  expires_in?: number
  interval?: number
}

export interface KiroExchangeCodeRequest {
  session_id: string
  state: string
  code: string
  proxy_id?: number
}

export interface KiroPollDeviceCodeRequest {
  session_id: string
  proxy_id?: number
}

export interface KiroTokenInfo {
  access_token?: string
  refresh_token?: string
  profile_arn?: string
  expires_in?: number
  expires_at?: string
  auth_method?: 'builder-id' | 'social' | string
  provider?: 'AWS' | 'Google' | 'Github' | string
  client_id?: string
  client_secret?: string
  email?: string
  [key: string]: unknown
}

export interface KiroPollDeviceCodeResponse {
  status: 'pending' | 'complete'
  token_info?: KiroTokenInfo
  interval?: number
}

export async function generateAuthUrl(
  payload: KiroAuthUrlRequest
): Promise<KiroAuthUrlResponse> {
  const { data } = await apiClient.post<KiroAuthUrlResponse>('/admin/kiro/oauth/auth-url', payload)
  return data
}

export async function exchangeCode(
  payload: KiroExchangeCodeRequest
): Promise<KiroTokenInfo> {
  const { data } = await apiClient.post<KiroTokenInfo>('/admin/kiro/oauth/exchange-code', payload)
  return data
}

export async function pollDeviceCode(
  payload: KiroPollDeviceCodeRequest
): Promise<KiroPollDeviceCodeResponse> {
  const { data } = await apiClient.post<KiroPollDeviceCodeResponse>('/admin/kiro/oauth/poll', payload)
  return data
}

export default { generateAuthUrl, exchangeCode, pollDeviceCode }
