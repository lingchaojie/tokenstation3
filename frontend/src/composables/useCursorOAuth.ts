import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  CursorExchangeCodeRequest,
  CursorOAuthCapabilities,
  CursorPollRequest,
  CursorTokenInfo,
} from '@/api/admin/cursor'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'

const CURSOR_POLL_INTERVAL_MS = 3_000
const CURSOR_POLL_TIMEOUT_MS = 300_000

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

export function useCursorOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()
  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const loading = ref(false)
  const polling = ref(false)
  const error = ref('')
  const capabilities = ref<CursorOAuthCapabilities | null>(null)
  const passwordAuthEnabled = ref(false)
  let pollGeneration = 0

  const cancelPolling = () => {
    pollGeneration += 1
    polling.value = false
  }

  const resetState = () => {
    cancelPolling()
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    loading.value = false
    error.value = ''
  }

  const loadCapabilities = async (): Promise<CursorOAuthCapabilities | null> => {
    try {
      const result = await adminAPI.cursor.getCapabilities()
      capabilities.value = result
      passwordAuthEnabled.value = result.password_auth_enabled === true
      return result
    } catch (err: unknown) {
      error.value = extractApiErrorMessage(err, 'Failed to load Cursor authorization capabilities.')
      return null
    }
  }

  const generateAuthUrl = async (proxyId?: number | null): Promise<boolean> => {
    cancelPolling()
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    error.value = ''
    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) payload.proxy_id = proxyId
      const result = await adminAPI.cursor.generateAuthUrl(payload)
      authUrl.value = result.auth_url
      sessionId.value = result.session_id
      state.value = result.state
      return true
    } catch (err: unknown) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.cursor.failedToGenerateUrl'))
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const exchangeAuthCode = async (params: {
    code: string
    sessionId: string
    state?: string
    proxyId?: number | null
  }): Promise<CursorTokenInfo | null> => {
    const code = params.code?.trim()
    if (!code || !params.sessionId) {
      error.value = t('admin.accounts.oauth.cursor.missingExchangeParams')
      return null
    }
    loading.value = true
    error.value = ''
    try {
      const payload: CursorExchangeCodeRequest = { session_id: params.sessionId, code }
      if (params.state) payload.state = params.state
      if (params.proxyId) payload.proxy_id = params.proxyId
      return await adminAPI.cursor.exchangeCode(payload)
    } catch (err: unknown) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.cursor.errors',
        t('admin.accounts.oauth.cursor.failedToExchangeCode'),
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const pollForToken = async (params: {
    sessionId: string
    state: string
    proxyId?: number | null
    intervalMs?: number
    timeoutMs?: number
  }): Promise<CursorTokenInfo | null> => {
    if (!params.sessionId || !params.state) {
      error.value = t('admin.accounts.oauth.cursor.missingExchangeParams')
      return null
    }
    const generation = ++pollGeneration
    const intervalMs = Math.max(0, params.intervalMs ?? CURSOR_POLL_INTERVAL_MS)
    const deadline = Date.now() + Math.max(0, params.timeoutMs ?? CURSOR_POLL_TIMEOUT_MS)
    polling.value = true
    error.value = ''
    try {
      while (Date.now() < deadline) {
        if (generation !== pollGeneration) return null
        try {
          const payload: CursorPollRequest = { session_id: params.sessionId, state: params.state }
          if (params.proxyId) payload.proxy_id = params.proxyId
          const tokenInfo = await adminAPI.cursor.pollAuthorization(payload)
          if (generation !== pollGeneration) return null
          if (tokenInfo.access_token) return tokenInfo
        } catch (err: unknown) {
          if (generation !== pollGeneration) return null
          error.value = extractI18nErrorMessage(
            err,
            t,
            'admin.accounts.oauth.cursor.errors',
            t('admin.accounts.oauth.cursor.failedToPoll'),
          )
          appStore.showError(error.value)
          return null
        }
        await sleep(intervalMs)
      }
      if (generation === pollGeneration) {
        error.value = t('admin.accounts.oauth.cursor.pollTimeout')
        appStore.showError(error.value)
      }
      return null
    } finally {
      if (generation === pollGeneration) polling.value = false
    }
  }

  const validateRefreshToken = async (
    refreshToken: string,
    proxyId?: number | null,
  ): Promise<CursorTokenInfo | null> => {
    if (!refreshToken.trim()) {
      error.value = t('admin.accounts.oauth.cursor.pleaseEnterRefreshToken')
      return null
    }
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.cursor.refreshCursorToken(refreshToken.trim(), proxyId)
    } catch (err: unknown) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.cursor.errors',
        t('admin.accounts.oauth.cursor.failedToValidateRT'),
      )
      return null
    } finally {
      loading.value = false
    }
  }

  const validateSSOToken = async (
    ssoToken: string,
    proxyId?: number | null,
  ): Promise<CursorTokenInfo | null> => {
    if (!ssoToken.trim()) {
      error.value = t('admin.accounts.oauth.cursor.pleaseEnterSSOToken', 'Please enter a session token')
      return null
    }
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.cursor.validateSSOToken(ssoToken.trim(), proxyId)
    } catch (err: unknown) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.cursor.errors',
        t('admin.accounts.oauth.cursor.failedToValidateSSO', 'Failed to validate session token'),
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const authorizePassword = async (
    emailAndPassword: string,
    proxyId?: number | null,
  ): Promise<CursorTokenInfo | null> => {
    if (!passwordAuthEnabled.value) {
      error.value = 'Password authorization is not supported for Cursor.'
      return null
    }
    if (!emailAndPassword.trim()) {
      error.value = t('admin.accounts.oauth.cursor.pleaseEnterPassword', 'Please enter email----password')
      return null
    }
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.cursor.authorizePassword(emailAndPassword, proxyId)
    } catch (err: unknown) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.cursor.errors',
        t('admin.accounts.oauth.cursor.failedToAuthorizePassword', 'Password authorization failed'),
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const buildCredentials = (tokenInfo: CursorTokenInfo): Record<string, unknown> => {
    const credentials: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      refresh_token: tokenInfo.refresh_token,
      api_key: tokenInfo.api_key,
      expires_at: tokenInfo.expires_at,
      sub: tokenInfo.sub,
    }
    return Object.fromEntries(
      Object.entries(credentials).filter(([, value]) => value !== undefined && value !== ''),
    )
  }

  const buildExtraInfo = (tokenInfo: CursorTokenInfo): Record<string, unknown> => {
    const extra: Record<string, unknown> = {}
    if (typeof tokenInfo.email === 'string' && tokenInfo.email) extra.email = tokenInfo.email
    return extra
  }

  return {
    authUrl,
    sessionId,
    state,
    loading,
    polling,
    error,
    capabilities,
    passwordAuthEnabled,
    resetState,
    cancelPolling,
    loadCapabilities,
    generateAuthUrl,
    exchangeAuthCode,
    pollForToken,
    validateRefreshToken,
    validateSSOToken,
    authorizePassword,
    buildCredentials,
    buildExtraInfo,
  }
}
