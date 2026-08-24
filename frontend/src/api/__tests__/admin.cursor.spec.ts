import { beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post },
}))

import {
  createFromSSO,
  exchangeCode,
  generateAuthUrl,
  getCapabilities,
  getCursorSSOImportTimeout,
  pollAuthorization,
  refreshAccountToken,
  refreshCursorToken,
  authorizePassword,
  validateSSOToken,
} from '@/api/admin/cursor'
import type { CursorExchangeCodeRequest } from '@/api/admin/cursor'

describe('admin Cursor OAuth API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: { password_auth_enabled: false } })
    post.mockResolvedValue({ data: { access_token: 'cursor-access' } })
  })

  it('uses the committed OAuth routes with their exact payloads', async () => {
    await getCapabilities()
    await generateAuthUrl({ proxy_id: 7, redirect_uri: 'http://localhost/callback' })
    await exchangeCode({ session_id: 'sid', state: 'state', code: 'crsr_key', proxy_id: 7 })
    await pollAuthorization({ session_id: 'sid', state: 'state', proxy_id: 7 })
    await refreshCursorToken('refresh-token', 7)
    await validateSSOToken('user::session', 7)
    await authorizePassword('user@example.com----  password with spaces  ', 7)
    await refreshAccountToken(42)

    expect(get).toHaveBeenCalledWith('/admin/cursor/oauth/capabilities')
    expect(post).toHaveBeenNthCalledWith(1, '/admin/cursor/oauth/auth-url', {
      proxy_id: 7,
      redirect_uri: 'http://localhost/callback',
    })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/cursor/oauth/exchange-code', {
      session_id: 'sid',
      state: 'state',
      code: 'crsr_key',
      proxy_id: 7,
    })
    expect(post).toHaveBeenNthCalledWith(3, '/admin/cursor/oauth/poll', {
      session_id: 'sid',
      state: 'state',
      proxy_id: 7,
    })
    expect(post).toHaveBeenNthCalledWith(4, '/admin/cursor/oauth/refresh-token', {
      refresh_token: 'refresh-token',
      proxy_id: 7,
    })
    expect(post).toHaveBeenNthCalledWith(5, '/admin/cursor/oauth/sso-token', {
      sso_token: 'user::session',
      proxy_id: 7,
    }, { timeout: 120_000 })
    expect(post).toHaveBeenNthCalledWith(6, '/admin/cursor/oauth/password', {
      email: 'user@example.com',
      password: '  password with spaces  ',
      proxy_id: 7,
    }, { timeout: 120_000 })
    expect(post).toHaveBeenNthCalledWith(7, '/admin/cursor/accounts/42/refresh')
  })

  it('sends only Task 16 exchange-code DTO fields', async () => {
    await exchangeCode({
      session_id: 'sid',
      state: 'state',
      code: 'credential',
      proxy_id: 7,
      redirect_uri: 'unsupported' as never,
    } as CursorExchangeCodeRequest)

    expect(post).toHaveBeenCalledWith('/admin/cursor/oauth/exchange-code', {
      session_id: 'sid',
      state: 'state',
      code: 'credential',
      proxy_id: 7,
    })
  })

  it('types exchange-code requests to the exact backend DTO', () => {
    expectTypeOf<CursorExchangeCodeRequest>().toEqualTypeOf<{
      session_id: string
      code: string
      state?: string
      proxy_id?: number
    }>()
  })

  it.each([
    [1, 180_000],
    [3, 180_000],
    [4, 270_000],
    [7, 360_000],
  ])('preserves bulk import order and uses a timeout sized for %i tokens', async (count, timeout) => {
    const sso_tokens = Array.from({ length: count }, (_, index) => `credential-${count - index}`)
    expect(getCursorSSOImportTimeout(count)).toBe(timeout)

    await createFromSSO({ sso_tokens, name: 'Cursor import' })

    expect(post).toHaveBeenCalledWith(
      '/admin/cursor/sso-to-oauth',
      { sso_tokens, name: 'Cursor import' },
      { timeout },
    )
  })
})
