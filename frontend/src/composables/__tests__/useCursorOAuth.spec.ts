import { beforeEach, describe, expect, it, vi } from 'vitest'

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
  beforeEach(() => {
    vi.clearAllMocks()
  })

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

  it('returns null when an older poll succeeds after a replacement generation', async () => {
    let resolveOldPoll!: (token: { access_token: string }) => void
    vi.mocked(adminAPI.cursor.pollAuthorization)
      .mockReturnValueOnce(new Promise((resolve) => { resolveOldPoll = resolve }))
      .mockResolvedValueOnce({ access_token: 'current-token' })
    const flow = useCursorOAuth()
    const oldPoll = flow.pollForToken({ sessionId: 'old', state: 'old-state' })
    const currentPoll = flow.pollForToken({ sessionId: 'current', state: 'current-state' })

    await expect(currentPoll).resolves.toMatchObject({ access_token: 'current-token' })
    resolveOldPoll({ access_token: 'stale-token' })

    await expect(oldPoll).resolves.toBeNull()
    expect(flow.error.value).toBe('')
    expect(flow.polling.value).toBe(false)
  })

  it('reset invalidates an in-flight poll that later succeeds', async () => {
    let resolvePoll!: (token: { access_token: string }) => void
    vi.mocked(adminAPI.cursor.pollAuthorization).mockReturnValueOnce(new Promise((resolve) => {
      resolvePoll = resolve
    }))
    const flow = useCursorOAuth()
    flow.authUrl.value = 'https://cursor.example/auth'
    flow.sessionId.value = 'visible-session'
    flow.state.value = 'visible-state'
    const poll = flow.pollForToken({ sessionId: 'sid', state: 'state' })

    flow.resetState()
    resolvePoll({ access_token: 'stale-token' })

    await expect(poll).resolves.toBeNull()
    expect(flow.authUrl.value).toBe('')
    expect(flow.sessionId.value).toBe('')
    expect(flow.state.value).toBe('')
    expect(flow.error.value).toBe('')
    expect(flow.polling.value).toBe(false)
  })

  it('reset ignores an in-flight poll error', async () => {
    let rejectPoll!: (error: unknown) => void
    vi.mocked(adminAPI.cursor.pollAuthorization).mockReturnValueOnce(new Promise((_, reject) => {
      rejectPoll = reject
    }))
    const flow = useCursorOAuth()
    const poll = flow.pollForToken({ sessionId: 'sid', state: 'state' })

    flow.resetState()
    rejectPoll(new Error('stale failure'))

    await expect(poll).resolves.toBeNull()
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

  it('reset invalidates a pending authorization URL request', async () => {
    let resolveGenerate!: (value: { auth_url: string; session_id: string; state: string }) => void
    vi.mocked(adminAPI.cursor.generateAuthUrl).mockReturnValueOnce(new Promise((resolve) => {
      resolveGenerate = resolve
    }))
    const flow = useCursorOAuth()
    const pending = flow.generateAuthUrl(7)

    flow.resetState()
    resolveGenerate({ auth_url: 'https://stale.example/auth', session_id: 'stale-session', state: 'stale-state' })

    await expect(pending).resolves.toBe(false)
    expect(flow.authUrl.value).toBe('')
    expect(flow.sessionId.value).toBe('')
    expect(flow.state.value).toBe('')
    expect(flow.loading.value).toBe(false)
  })

  it('an older authorization URL request cannot overwrite its replacement', async () => {
    let resolveOld!: (value: { auth_url: string; session_id: string; state: string }) => void
    vi.mocked(adminAPI.cursor.generateAuthUrl)
      .mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve }))
      .mockResolvedValueOnce({ auth_url: 'https://current.example/auth', session_id: 'current-session', state: 'current-state' })
    const flow = useCursorOAuth()

    const oldRequest = flow.generateAuthUrl(1)
    await expect(flow.generateAuthUrl(2)).resolves.toBe(true)
    resolveOld({ auth_url: 'https://old.example/auth', session_id: 'old-session', state: 'old-state' })

    await expect(oldRequest).resolves.toBe(false)
    expect(flow.authUrl.value).toBe('https://current.example/auth')
    expect(flow.sessionId.value).toBe('current-session')
    expect(flow.state.value).toBe('current-state')
    expect(flow.loading.value).toBe(false)
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
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const cookieWrite = vi.fn()
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => '',
      set: cookieWrite,
    })
    try {
      const credentials = flow.buildCredentials({
        access_token: 'jwt',
        refresh_token: 'refresh',
        api_key: 'crsr_api_key',
        expires_at: 1_900_000_000,
        sub: 'cursor-user',
        sso_token: 'canary',
        session_token: 'canary',
        web_session_token: 'canary',
        cookie: 'canary',
        Cookie: 'canary',
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
      const extra = flow.buildExtraInfo({
        session_id: 'canary',
        web_session_token: 'canary',
        cookie: 'canary',
        Cookie: 'canary',
        email: 'cursor@example.com',
      })

      expect(credentials).toEqual({
        access_token: 'jwt',
        refresh_token: 'refresh',
        api_key: 'crsr_api_key',
        expires_at: 1_900_000_000,
        sub: 'cursor-user',
      })
      expect(extra).toEqual({ email: 'cursor@example.com' })
      expect(storageWrite).not.toHaveBeenCalled()
      expect(cookieWrite).not.toHaveBeenCalled()
    } finally {
      storageWrite.mockRestore()
      delete (document as { cookie?: string }).cookie
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
