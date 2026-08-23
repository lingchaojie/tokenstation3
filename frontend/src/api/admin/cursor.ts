/**
 * Typed Cursor credential endpoints for the admin UI.
 *
 * Session cookies and API keys are one-shot request values. This module never
 * stores them; callers receive only the normalized token response.
 */

import { apiClient } from '../client'
import type { Account } from '@/types'

export interface CursorAuthUrlResponse {
  auth_url: string
  session_id: string
  state: string
}

export interface CursorAuthUrlRequest {
  proxy_id?: number
  redirect_uri?: string
}

export interface CursorOAuthCapabilities {
  password_auth_enabled: boolean
  deep_link_enabled?: boolean
  api_key_import_enabled?: boolean
  cookie_import_enabled?: boolean
}

export interface CursorExchangeCodeRequest {
  session_id: string
  code: string
  state?: string
  proxy_id?: number
  redirect_uri?: string
}

export interface CursorPollRequest {
  session_id: string
  state: string
  proxy_id?: number
}

export interface CursorTokenInfo {
  access_token?: string
  refresh_token?: string
  api_key?: string
  expires_at?: number | string
  sub?: string
  status?: string
  [key: string]: unknown
}

export interface CursorSSOToOAuthRequest {
  sso_tokens: string[]
  name?: string
  notes?: string | null
  proxy_id?: number | null
  group_ids?: number[]
  credentials?: Record<string, unknown>
  extra?: Record<string, unknown>
  concurrency?: number
  load_factor?: number
  priority?: number
  rate_multiplier?: number
  expires_at?: number | null
  auto_pause_on_expired?: boolean
}

export interface CursorSSOToOAuthItemResult {
  index: number
  name?: string
  account?: Account
  error?: string
}

export interface CursorSSOToOAuthResponse {
  created: CursorSSOToOAuthItemResult[]
  failed: CursorSSOToOAuthItemResult[]
}

const CURSOR_AUTHORIZATION_TIMEOUT_MS = 120_000
const CURSOR_SSO_IMPORT_CONCURRENCY = 3
const CURSOR_SSO_IMPORT_TIMEOUT_PER_BATCH_MS = 90_000
const CURSOR_SSO_IMPORT_TIMEOUT_BUFFER_MS = 90_000

export function getCursorSSOImportTimeout(count: number): number {
  const batches = Math.ceil(Math.max(1, count) / CURSOR_SSO_IMPORT_CONCURRENCY)
  return batches * CURSOR_SSO_IMPORT_TIMEOUT_PER_BATCH_MS + CURSOR_SSO_IMPORT_TIMEOUT_BUFFER_MS
}

export async function getCapabilities(): Promise<CursorOAuthCapabilities> {
  const { data } = await apiClient.get<CursorOAuthCapabilities>('/admin/cursor/oauth/capabilities')
  return data
}

export async function generateAuthUrl(payload: CursorAuthUrlRequest): Promise<CursorAuthUrlResponse> {
  const { data } = await apiClient.post<CursorAuthUrlResponse>('/admin/cursor/oauth/auth-url', payload)
  return data
}

/** Compatibility import endpoint; deep-link completion should use pollAuthorization. */
export async function exchangeCode(payload: CursorExchangeCodeRequest): Promise<CursorTokenInfo> {
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/exchange-code', payload)
  return data
}

export async function pollAuthorization(payload: CursorPollRequest): Promise<CursorTokenInfo> {
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/poll', payload)
  return data
}

export async function refreshCursorToken(
  refreshToken: string,
  proxyId?: number | null
): Promise<CursorTokenInfo> {
  const payload: Record<string, unknown> = { refresh_token: refreshToken }
  if (proxyId) payload.proxy_id = proxyId
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/refresh-token', payload)
  return data
}

export async function validateSSOToken(
  ssoToken: string,
  proxyId?: number | null
): Promise<CursorTokenInfo> {
  const payload: Record<string, unknown> = { sso_token: ssoToken }
  if (proxyId) payload.proxy_id = proxyId
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/sso-token', payload, {
    timeout: CURSOR_AUTHORIZATION_TIMEOUT_MS,
  })
  return data
}

/** Kept for capability-gated parity; Cursor currently reports this as disabled. */
export async function authorizePassword(
  emailAndPassword: string,
  proxyId?: number | null
): Promise<CursorTokenInfo> {
  const separator = '----'
  const index = emailAndPassword.indexOf(separator)
  const email = (index >= 0 ? emailAndPassword.slice(0, index) : emailAndPassword).trim()
  const password = index >= 0 ? emailAndPassword.slice(index + separator.length) : ''
  const payload: Record<string, unknown> = { email, password }
  if (proxyId) payload.proxy_id = proxyId
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/password', payload, {
    timeout: CURSOR_AUTHORIZATION_TIMEOUT_MS,
  })
  return data
}

export async function createFromSSO(payload: CursorSSOToOAuthRequest): Promise<CursorSSOToOAuthResponse> {
  const { data } = await apiClient.post<CursorSSOToOAuthResponse>(
    '/admin/cursor/sso-to-oauth',
    payload,
    { timeout: getCursorSSOImportTimeout(payload.sso_tokens.length) },
  )
  return data
}

export async function refreshAccountToken(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/cursor/accounts/${id}/refresh`)
  return data
}

export default {
  getCapabilities,
  generateAuthUrl,
  exchangeCode,
  pollAuthorization,
  refreshCursorToken,
  validateSSOToken,
  authorizePassword,
  createFromSSO,
  refreshAccountToken,
}
