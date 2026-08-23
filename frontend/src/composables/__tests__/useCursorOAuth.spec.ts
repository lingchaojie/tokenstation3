import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn() }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'admin.accounts.oauth.cursor.pollTimeout': 'Cursor authorization timed out.',
    }[key] ?? key),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    cursor: {
      getCapabilities: vi.fn(),
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      pollAuthorization: vi.fn(),
      refreshCursorToken: vi.fn(),
      validateSSOToken: vi.fn(),
      authorizePassword: vi.fn(),
    },
  },
}))

import { adminAPI } from '@/api/admin'
import { useCursorOAuth } from '@/composables/useCursorOAuth'

describe('useCursorOAuth', () => {
  it('polls pending deep-link authorization until Cursor returns an access token', async () => {
    vi.mocked(adminAPI.cursor.pollAuthorization)
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({ access_token: 'jwt' })
    const flow = useCursorOAuth()

    const token = await flow.pollForToken({ sessionId: 'sid', state: 'state', intervalMs: 1, timeoutMs: 100 })

    expect(token?.access_token).toBe('jwt')
    expect(adminAPI.cursor.pollAuthorization).toHaveBeenCalledTimes(2)
    expect(flow.polling.value).toBe(false)
  })

  it('ignores a stale polling error after explicit cancellation', async () => {
    let rejectPoll!: (error: unknown) => void
    vi.mocked(adminAPI.cursor.pollAuthorization).mockReturnValueOnce(new Promise((_, reject) => {
      rejectPoll = reject
    }))
    const flow = useCursorOAuth()
    const stalePoll = flow.pollForToken({ sessionId: 'old', state: 'old-state', intervalMs: 1 })

    flow.cancelPolling()
    rejectPoll(new Error('stale failure'))

    await expect(stalePoll).resolves.toBeNull()
    expect(flow.error.value).toBe('')
    expect(flow.polling.value).toBe(false)
  })

  it('does not let an older poll overwrite a replacement generation', async () => {
    let rejectOldPoll!: (error: unknown) => void
    vi.mocked(adminAPI.cursor.pollAuthorization)
      .mockReturnValueOnce(new Promise((_, reject) => { rejectOldPoll = reject }))
      .mockResolvedValueOnce({ access_token: 'current-token' })
    const flow = useCursorOAuth()
    const oldPoll = flow.pollForToken({ sessionId: 'old', state: 'old-state' })
    const currentPoll = flow.pollForToken({ sessionId: 'current', state: 'current-state' })

    await expect(currentPoll).resolves.toMatchObject({ access_token: 'current-token' })
    rejectOldPoll(new Error('old failure'))

    await expect(oldPoll).resolves.toBeNull()
    expect(flow.error.value).toBe('')
    expect(flow.polling.value).toBe(false)
  })

  it('resets both visible authorization state and any pending generation', () => {
    const flow = useCursorOAuth()
    flow.authUrl.value = 'https://cursor.example/auth'
    flow.sessionId.value = 'sid'
    flow.state.value = 'state'
    flow.loading.value = true
    flow.error.value = 'error'

    flow.resetState()

    expect(flow.authUrl.value).toBe('')
    expect(flow.sessionId.value).toBe('')
    expect(flow.state.value).toBe('')
    expect(flow.loading.value).toBe(false)
    expect(flow.error.value).toBe('')
  })

  it('reports a timeout when browser confirmation does not arrive', async () => {
    vi.mocked(adminAPI.cursor.pollAuthorization).mockResolvedValue({ status: 'pending' })
    const flow = useCursorOAuth()

    const token = await flow.pollForToken({ sessionId: 'sid', state: 'state', intervalMs: 1, timeoutMs: 3 })

    expect(token).toBeNull()
    expect(flow.error.value).toBe('Cursor authorization timed out.')
  })

  it('never returns or persists one-time Cursor secrets', () => {
    const flow = useCursorOAuth()
    const credentials = flow.buildCredentials({
      access_token: 'jwt',
      refresh_token: 'refresh',
      api_key: 'crsr_api_key',
      sso_token: 'canary',
      session_token: 'canary',
      password: 'canary',
      sso: 'canary',
      'sso-rw': 'canary',
      status: 'pending',
      state: 'canary',
      verifier: 'canary',
      code_verifier: 'canary',
      challenge: 'canary',
      session_id: 'canary',
    })
    const extra = flow.buildExtraInfo({ session_id: 'canary', email: 'cursor@example.com' })

    expect(credentials).toEqual({
      access_token: 'jwt',
      refresh_token: 'refresh',
      api_key: 'crsr_api_key',
    })
    expect(extra).toEqual({ email: 'cursor@example.com' })
    for (const key of ['sso_token', 'session_token', 'password', 'sso', 'sso-rw', 'status', 'state', 'verifier', 'code_verifier', 'challenge', 'session_id']) {
      expect(localStorage.getItem(key)).toBeNull()
      expect(sessionStorage.getItem(key)).toBeNull()
    }
  })

  it('keeps password authorization capability-gated', async () => {
    vi.mocked(adminAPI.cursor.getCapabilities).mockResolvedValueOnce({ password_auth_enabled: false })
    const flow = useCursorOAuth()

    await flow.loadCapabilities()
    const token = await flow.authorizePassword('user@example.com----secret')

    expect(token).toBeNull()
    expect(adminAPI.cursor.authorizePassword).not.toHaveBeenCalled()
  })
})
