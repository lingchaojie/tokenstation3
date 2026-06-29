import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    kiro: {
      generateAuthUrl: vi.fn(),
      generateIDCAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      refreshToken: vi.fn(),
      importToken: vi.fn()
    }
  }
}))

import { useKiroOAuth } from '@/composables/useKiroOAuth'
import { adminAPI } from '@/api/admin'

describe('useKiroOAuth', () => {
  it('starts Kiro social OAuth using upstream provider payload', async () => {
    vi.mocked(adminAPI.kiro.generateAuthUrl).mockResolvedValueOnce({
      auth_url: 'https://kiro.example/auth',
      session_id: 'session-1',
      state: 'state-1'
    })

    const oauth = useKiroOAuth()
    const started = await oauth.generateAuthUrl(9, 'Github')

    expect(started).toBe(true)
    expect(adminAPI.kiro.generateAuthUrl).toHaveBeenCalledWith({
      proxy_id: 9,
      provider: 'Github'
    })
    expect(oauth.authUrl.value).toBe('https://kiro.example/auth')
    expect(oauth.sessionId.value).toBe('session-1')
    expect(oauth.state.value).toBe('state-1')
  })

  it('starts Kiro Organization OAuth through the upstream IDC endpoint with normalized settings', async () => {
    vi.mocked(adminAPI.kiro.generateIDCAuthUrl).mockResolvedValueOnce({
      auth_url: 'https://device.sso.aws.amazon.com/start',
      session_id: 'session-idc',
      state: 'state-idc',
      client_id: 'client-id',
      region: 'us-east-1',
      start_url: 'https://d-99674ac649.awsapps.com/start'
    })

    const oauth = useKiroOAuth()
    const started = await oauth.generateIDCAuthUrl({
      proxyId: 3,
      startUrl: '  https://d-99674ac649.awsapps.com/start  ',
      region: '  us-east-1  '
    })

    expect(started).toBe(true)
    expect(adminAPI.kiro.generateIDCAuthUrl).toHaveBeenCalledWith({
      proxy_id: 3,
      start_url: 'https://d-99674ac649.awsapps.com/start',
      region: 'us-east-1'
    })
    expect(oauth.authUrl.value).toBe('https://device.sso.aws.amazon.com/start')
    expect(oauth.sessionId.value).toBe('session-idc')
  })

  it('imports Kiro token JSON through the upstream import endpoint', async () => {
    vi.mocked(adminAPI.kiro.importToken).mockResolvedValueOnce({
      access_token: 'at',
      refresh_token: 'rt',
      client_id: 'client-id'
    })

    const oauth = useKiroOAuth()
    const tokenInfo = await oauth.importToken('{"accessToken":"at"}', '{"clientId":"client-id"}')

    expect(tokenInfo).toEqual({
      access_token: 'at',
      refresh_token: 'rt',
      client_id: 'client-id'
    })
    expect(adminAPI.kiro.importToken).toHaveBeenCalledWith({
      token_json: '{"accessToken":"at"}',
      device_registration_json: '{"clientId":"client-id"}'
    })
  })

  it('validates Kiro refresh tokens with refresh metadata', async () => {
    vi.mocked(adminAPI.kiro.refreshToken).mockResolvedValueOnce({
      access_token: 'new-at',
      refresh_token: 'new-rt'
    })

    const oauth = useKiroOAuth()
    const tokenInfo = await oauth.validateRefreshToken({
      refreshToken: 'rt',
      authMethod: 'idc',
      provider: 'AWS',
      clientId: 'client-id',
      clientSecret: 'client-secret',
      startUrl: 'https://view.awsapps.com/start',
      region: 'us-east-1',
      profileArn: 'arn:aws:codewhisperer:us-east-1:123456789012:profile/default',
      proxyId: 10
    })

    expect(tokenInfo?.access_token).toBe('new-at')
    expect(adminAPI.kiro.refreshToken).toHaveBeenCalledWith({
      refresh_token: 'rt',
      auth_method: 'idc',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret',
      start_url: 'https://view.awsapps.com/start',
      region: 'us-east-1',
      profile_arn: 'arn:aws:codewhisperer:us-east-1:123456789012:profile/default',
      proxy_id: 10
    })
  })

  it('builds account credentials for Builder ID refresh', () => {
    const oauth = useKiroOAuth()

    const credentials = oauth.buildCredentials({
      access_token: 'at',
      refresh_token: 'rt',
      auth_method: 'builder-id',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret'
    })

    expect(credentials).toMatchObject({
      access_token: 'at',
      refresh_token: 'rt',
      auth_method: 'builder-id',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret'
    })
    expect(credentials.profile_arn).toBeUndefined()
    expect(Object.prototype.hasOwnProperty.call(credentials, 'profile_arn')).toBe(false)
  })

  it('extracts code and social callback metadata from pasted callback URL', async () => {
    vi.mocked(adminAPI.kiro.exchangeCode).mockResolvedValueOnce({
      access_token: 'at',
      refresh_token: 'rt'
    })

    const oauth = useKiroOAuth()
    const tokenInfo = await oauth.exchangeAuthCode({
      code: 'http://127.0.0.1:9876/oauth/callback?login_option=google&code=auth-code&state=callback-state',
      sessionId: 'sess-social',
      state: 'state-social',
      proxyId: 12
    })

    expect(tokenInfo).toMatchObject({ access_token: 'at' })
    expect(adminAPI.kiro.exchangeCode).toHaveBeenCalledWith({
      session_id: 'sess-social',
      state: 'state-social',
      code: 'auth-code',
      callback_path: '/oauth/callback',
      login_option: 'google',
      proxy_id: 12
    })
  })
})
