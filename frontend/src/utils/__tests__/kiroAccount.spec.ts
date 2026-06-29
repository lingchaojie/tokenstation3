import { describe, expect, it } from 'vitest'

import { isKiroDirectApiKeyAccount, isKiroRelayAccount } from '@/utils/kiroAccount'

describe('kiroAccount helpers', () => {
  it('distinguishes Kiro direct API key from relay API key accounts', () => {
    expect(isKiroDirectApiKeyAccount({
      platform: 'kiro',
      type: 'apikey',
      credentials: { api_key: 'ksk_test' }
    })).toBe(true)
    expect(isKiroRelayAccount({
      platform: 'kiro',
      type: 'apikey',
      credentials: { api_key: 'sk-test', base_url: 'https://relay.example.com' }
    })).toBe(true)
  })

  it('does not classify non-Kiro or OAuth accounts as Kiro API key relay/direct', () => {
    expect(isKiroDirectApiKeyAccount({
      platform: 'anthropic',
      type: 'apikey',
      credentials: { api_key: 'sk-ant' }
    })).toBe(false)
    expect(isKiroRelayAccount({
      platform: 'kiro',
      type: 'oauth',
      credentials: { access_token: 'token', base_url: 'https://relay.example.com' }
    })).toBe(false)
  })
})
