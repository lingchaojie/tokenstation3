import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
  },
}))

import {
  getUpstreamUserAgents,
  type AccountUpstreamUserAgent,
  type AccountUpstreamUserAgentsResponse,
} from '@/api/admin/accounts'

type Assert<T extends true> = T
type IsExact<T, U> = (
  (<G>() => G extends T ? 1 : 2) extends (<G>() => G extends U ? 1 : 2)
    ? ((<G>() => G extends U ? 1 : 2) extends (<G>() => G extends T ? 1 : 2) ? true : false)
    : false
)

type ExpectedAccountUpstreamUserAgent = {
  account_id: number
  user_agent: string
  first_seen_at: string
  last_seen_at: string
  seen_count: number
}

type ExpectedAccountUpstreamUserAgentsResponse = {
  items: AccountUpstreamUserAgent[]
}

const itemContractExact: Assert<
  IsExact<AccountUpstreamUserAgent, ExpectedAccountUpstreamUserAgent>
> = true
const responseContractExact: Assert<
  IsExact<AccountUpstreamUserAgentsResponse, ExpectedAccountUpstreamUserAgentsResponse>
> = true

describe('admin accounts upstream user agents api', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('loads deduped upstream user agents for an account newest-first', async () => {
    const response: AccountUpstreamUserAgentsResponse = {
      items: [{
        account_id: 42,
        user_agent: 'codex_cli_rs/0.125.0',
        first_seen_at: '2026-06-18T10:00:00Z',
        last_seen_at: '2026-06-18T12:00:00Z',
        seen_count: 2,
      }],
    }
    get.mockResolvedValue({ data: response })

    const result = await getUpstreamUserAgents(42, 25)

    expect(get).toHaveBeenCalledWith('/admin/accounts/42/upstream-user-agents', {
      params: { limit: 25 },
    })
    expect(result).toEqual(response)
  })

  it('keeps upstream user agent response types aligned with the backend contract', () => {
    expect(itemContractExact).toBe(true)
    expect(responseContractExact).toBe(true)
  })
})
