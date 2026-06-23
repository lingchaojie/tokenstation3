import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    kiro: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      pollDeviceCode: vi.fn()
    }
  }
}))

import { useKiroOAuth } from '@/composables/useKiroOAuth'

describe('useKiroOAuth', () => {
  it('imports Kiro IDE token JSON with camelCase fields', () => {
    const oauth = useKiroOAuth()

    const tokenInfo = oauth.parseTokenJSON(
      JSON.stringify({
        accessToken: 'at',
        refreshToken: 'rt',
        authMethod: 'builder-id',
        provider: 'AWS',
        clientId: 'client-id',
        clientSecret: 'client-secret',
        expiresAt: '2026-06-23T12:00:00Z'
      })
    )

    expect(tokenInfo).toEqual({
      access_token: 'at',
      refresh_token: 'rt',
      profile_arn: undefined,
      expires_at: '2026-06-23T12:00:00Z',
      expires_in: undefined,
      auth_method: 'builder-id',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret',
      email: undefined
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
      client_secret: 'client-secret',
      preferred_endpoint: 'codewhisperer'
    })
    expect(Object.prototype.hasOwnProperty.call(credentials, 'profile_arn')).toBe(false)
  })
})
