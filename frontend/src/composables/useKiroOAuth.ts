import { ref } from 'vue'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  KiroOAuthMethod,
  KiroTokenInfo,
  KiroAuthUrlResponse
} from '@/api/admin/kiro'

export type KiroLoginMethod = 'builder-id' | 'google' | 'github'

const normalizeTokenInfo = (raw: Record<string, unknown>): KiroTokenInfo => ({
  access_token: stringValue(raw.access_token) || stringValue(raw.accessToken),
  refresh_token: stringValue(raw.refresh_token) || stringValue(raw.refreshToken),
  profile_arn: stringValue(raw.profile_arn) || stringValue(raw.profileArn),
  expires_at: stringValue(raw.expires_at) || stringValue(raw.expiresAt),
  expires_in: numberValue(raw.expires_in ?? raw.expiresIn),
  auth_method: stringValue(raw.auth_method) || stringValue(raw.authMethod),
  provider: stringValue(raw.provider),
  client_id: stringValue(raw.client_id) || stringValue(raw.clientId),
  client_secret: stringValue(raw.client_secret) || stringValue(raw.clientSecret),
  email: stringValue(raw.email)
})

const stringValue = (value: unknown): string | undefined => {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

const numberValue = (value: unknown): number | undefined => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return undefined
}

const extractCodeFromCallback = (input: string): string => {
  const raw = input.trim()
  if (!raw) return ''
  try {
    const parsed = new URL(raw)
    return parsed.searchParams.get('code') || raw
  } catch {
    const match = raw.match(/[?&]code=([^&\s]+)/)
    if (match?.[1]) return decodeURIComponent(match[1])
    return raw
  }
}

export function useKiroOAuth() {
  const appStore = useAppStore()

  const mode = ref<'auth_url' | 'device_code' | ''>('')
  const method = ref<KiroLoginMethod>('builder-id')
  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const verificationUri = ref('')
  const verificationUriComplete = ref('')
  const userCode = ref('')
  const interval = ref(5)
  const loading = ref(false)
  const polling = ref(false)
  const error = ref('')

  const resetState = () => {
    mode.value = ''
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    verificationUri.value = ''
    verificationUriComplete.value = ''
    userCode.value = ''
    interval.value = 5
    loading.value = false
    polling.value = false
    error.value = ''
  }

  const applyStartResponse = (response: KiroAuthUrlResponse) => {
    mode.value = response.mode
    method.value = response.method === 'builder-id' ? 'builder-id' : response.method
    authUrl.value = response.auth_url || ''
    sessionId.value = response.session_id
    state.value = response.state || ''
    verificationUri.value = response.verification_uri || ''
    verificationUriComplete.value = response.verification_uri_complete || ''
    userCode.value = response.user_code || ''
    interval.value = response.interval || 5
  }

  const generateAuthUrl = async (
    loginMethod: KiroLoginMethod,
    proxyId?: number | null
  ): Promise<boolean> => {
    loading.value = true
    error.value = ''
    resetState()
    method.value = loginMethod
    loading.value = true

    try {
      const payload: { method: KiroOAuthMethod; proxy_id?: number } = {
        method: loginMethod
      }
      if (proxyId) payload.proxy_id = proxyId
      applyStartResponse(await adminAPI.kiro.generateAuthUrl(payload))
      return true
    } catch (err: any) {
      error.value = err.message || err.response?.data?.detail || 'Failed to start Kiro authorization'
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const exchangeAuthCode = async (params: {
    code: string
    sessionId: string
    state: string
    proxyId?: number | null
  }): Promise<KiroTokenInfo | null> => {
    const code = extractCodeFromCallback(params.code)
    if (!code || !params.sessionId || !params.state) {
      error.value = 'Missing Kiro authorization code, session, or state'
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: { session_id: string; state: string; code: string; proxy_id?: number } = {
        session_id: params.sessionId,
        state: params.state,
        code
      }
      if (params.proxyId) payload.proxy_id = params.proxyId
      return await adminAPI.kiro.exchangeCode(payload)
    } catch (err: any) {
      error.value = err.message || err.response?.data?.detail || 'Failed to exchange Kiro authorization code'
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const pollDeviceCode = async (proxyId?: number | null): Promise<KiroTokenInfo | null> => {
    if (!sessionId.value) {
      error.value = 'Missing Kiro authorization session'
      return null
    }
    try {
      const payload: { session_id: string; proxy_id?: number } = {
        session_id: sessionId.value
      }
      if (proxyId) payload.proxy_id = proxyId
      const result = await adminAPI.kiro.pollDeviceCode(payload)
      if (result.interval) interval.value = result.interval
      if (result.status === 'complete') {
        return result.token_info || null
      }
      return null
    } catch (err: any) {
      error.value = err.message || err.response?.data?.detail || 'Failed to poll Kiro authorization status'
      appStore.showError(error.value)
      throw err
    }
  }

  const parseTokenJSON = (raw: string): KiroTokenInfo | null => {
    try {
      const parsed = JSON.parse(raw.trim()) as Record<string, unknown>
      const tokenInfo = normalizeTokenInfo(parsed)
      if (!tokenInfo.access_token || !tokenInfo.refresh_token) {
        error.value = 'Kiro token JSON must include accessToken/access_token and refreshToken/refresh_token'
        return null
      }
      return tokenInfo
    } catch {
      error.value = 'Invalid Kiro token JSON'
      return null
    }
  }

  const buildCredentials = (tokenInfo: KiroTokenInfo): Record<string, unknown> => {
    const creds: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      refresh_token: tokenInfo.refresh_token,
      preferred_endpoint: 'codewhisperer'
    }
    if (tokenInfo.profile_arn) creds.profile_arn = tokenInfo.profile_arn
    if (tokenInfo.expires_at) creds.expires_at = tokenInfo.expires_at
    if (tokenInfo.auth_method) creds.auth_method = tokenInfo.auth_method
    if (tokenInfo.provider) creds.provider = tokenInfo.provider
    if (tokenInfo.client_id) creds.client_id = tokenInfo.client_id
    if (tokenInfo.client_secret) creds.client_secret = tokenInfo.client_secret
    if (tokenInfo.email) creds.email = tokenInfo.email
    return creds
  }

  return {
    mode,
    method,
    authUrl,
    sessionId,
    state,
    verificationUri,
    verificationUriComplete,
    userCode,
    interval,
    loading,
    polling,
    error,
    resetState,
    generateAuthUrl,
    exchangeAuthCode,
    pollDeviceCode,
    parseTokenJSON,
    buildCredentials
  }
}
