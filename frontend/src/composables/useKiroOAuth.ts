import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { KiroTokenInfo } from '@/api/admin/kiro'

const extractCallbackParts = (input: string): { code: string; callbackPath?: string; loginOption?: string } => {
  const raw = input.trim()
  if (!raw) return { code: '' }
  try {
    const parsed = new URL(raw)
    return {
      code: parsed.searchParams.get('code') || raw,
      callbackPath: parsed.pathname || undefined,
      loginOption: parsed.searchParams.get('login_option') || undefined
    }
  } catch {
    const match = raw.match(/[?&]code=([^&\s]+)/)
    const pathMatch = raw.match(/^(?:https?:\/\/[^/?#]+)?([^?#\s]+)[?#]/)
    const loginOptionMatch = raw.match(/[?&]login_option=([^&\s]+)/)
    if (match?.[1]) {
      return {
        code: decodeURIComponent(match[1]),
        callbackPath: pathMatch?.[1],
        loginOption: loginOptionMatch?.[1] ? decodeURIComponent(loginOptionMatch[1]) : undefined
      }
    }
    return { code: raw }
  }
}

const assignIfPresent = (target: Record<string, unknown>, key: string, value: unknown) => {
  if (value !== undefined && value !== null && value !== '') {
    target[key] = value
  }
}

export function useKiroOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const loading = ref(false)
  const error = ref('')

  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    loading.value = false
    error.value = ''
  }

  const generateAuthUrl = async (
    proxyId: number | null | undefined,
    provider: 'Google' | 'Github' = 'Google'
  ): Promise<boolean> => {
    loading.value = true
    error.value = ''
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''

    try {
      const response = await adminAPI.kiro.generateAuthUrl({
        proxy_id: proxyId || undefined,
        provider
      })
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      error.value = err.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const generateIDCAuthUrl = async (
    params: { proxyId?: number | null; startUrl?: string; region?: string }
  ): Promise<boolean> => {
    loading.value = true
    error.value = ''
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''

    try {
      const startUrl = params.startUrl?.trim()
      const region = params.region?.trim()
      const response = await adminAPI.kiro.generateIDCAuthUrl({
        proxy_id: params.proxyId || undefined,
        start_url: startUrl || undefined,
        region: region || undefined
      })
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      error.value = err.response?.data?.detail || t('admin.accounts.oauth.authFailed')
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
    callbackPath?: string
    loginOption?: string
    proxyId?: number | null
  }): Promise<KiroTokenInfo | null> => {
    const callbackParts = extractCallbackParts(params.code)
    const code = callbackParts.code
    if (!code || !params.sessionId || !params.state) {
      error.value = t('admin.accounts.oauth.authFailed')
      return null
    }

    loading.value = true
    error.value = ''
    try {
      return await adminAPI.kiro.exchangeCode({
        session_id: params.sessionId,
        state: params.state,
        code,
        callback_path: params.callbackPath || callbackParts.callbackPath,
        login_option: params.loginOption || callbackParts.loginOption,
        proxy_id: params.proxyId || undefined
      })
    } catch (err: any) {
      error.value = err.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const validateRefreshToken = async (payload: {
    refreshToken: string
    authMethod?: string
    provider?: string
    clientId?: string
    clientSecret?: string
    startUrl?: string
    region?: string
    profileArn?: string
    proxyId?: number | null
  }): Promise<KiroTokenInfo | null> => {
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.kiro.refreshToken({
        refresh_token: payload.refreshToken.trim(),
        auth_method: payload.authMethod,
        provider: payload.provider,
        client_id: payload.clientId,
        client_secret: payload.clientSecret,
        start_url: payload.startUrl,
        region: payload.region,
        profile_arn: payload.profileArn,
        proxy_id: payload.proxyId || undefined
      })
    } catch (err: any) {
      error.value = err.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      return null
    } finally {
      loading.value = false
    }
  }

  const importToken = async (
    tokenJSON: string,
    deviceRegistrationJSON?: string
  ): Promise<KiroTokenInfo | null> => {
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.kiro.importToken({
        token_json: tokenJSON,
        device_registration_json: deviceRegistrationJSON
      })
    } catch (err: any) {
      error.value = err.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const buildCredentials = (tokenInfo: KiroTokenInfo): Record<string, unknown> => {
    const creds: Record<string, unknown> = {}
    assignIfPresent(creds, 'access_token', tokenInfo.access_token)
    assignIfPresent(creds, 'refresh_token', tokenInfo.refresh_token)
    assignIfPresent(creds, 'profile_arn', tokenInfo.profile_arn)
    assignIfPresent(creds, 'expires_at', tokenInfo.expires_at)
    assignIfPresent(creds, 'auth_method', tokenInfo.auth_method)
    assignIfPresent(creds, 'provider', tokenInfo.provider)
    assignIfPresent(creds, 'client_id', tokenInfo.client_id)
    assignIfPresent(creds, 'client_secret', tokenInfo.client_secret)
    assignIfPresent(creds, 'client_id_hash', tokenInfo.client_id_hash)
    assignIfPresent(creds, 'email', tokenInfo.email)
    assignIfPresent(creds, 'start_url', tokenInfo.start_url)
    assignIfPresent(creds, 'region', tokenInfo.region)
    return creds
  }

  return {
    authUrl,
    sessionId,
    state,
    loading,
    error,
    resetState,
    generateAuthUrl,
    generateIDCAuthUrl,
    exchangeAuthCode,
    validateRefreshToken,
    importToken,
    buildCredentials
  }
}
