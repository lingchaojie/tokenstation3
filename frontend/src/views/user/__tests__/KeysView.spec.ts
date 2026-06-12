import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/client', () => ({
  apiClient: {
    post: vi.fn(async (_url: string, payload: unknown) => ({ data: payload }))
  }
}))

import { apiClient } from '@/api/client'
import { create } from '@/api/keys'

describe('keysAPI.create provider routing payload', () => {
  it('sends key_type and omits group_id for normal user provider keys', async () => {
    await create('Provider key', 'openai')

    expect(apiClient.post).toHaveBeenCalledWith('/keys', {
      name: 'Provider key',
      key_type: 'openai'
    })
  })
})
