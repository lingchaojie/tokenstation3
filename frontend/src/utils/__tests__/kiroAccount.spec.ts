import { describe, expect, it } from 'vitest'

import {
  buildKiroAPIRegionOptions,
  DEFAULT_KIRO_API_REGION,
  isKiroDirectApiKeyAccount,
  isKiroRelayAccount,
  KIRO_API_REGIONS,
  resolveKiroAPIRegion
} from '@/utils/kiroAccount'

describe('kiroAccount helpers', () => {
  it('defines the supported Kiro API regions', () => {
    expect(DEFAULT_KIRO_API_REGION).toBe('us-east-1')
    expect(KIRO_API_REGIONS).toEqual(['us-east-1', 'eu-central-1'])
  })

  it('resolves blank API regions to the default and trims configured regions', () => {
    expect(resolveKiroAPIRegion(undefined)).toBe('us-east-1')
    expect(resolveKiroAPIRegion('')).toBe('us-east-1')
    expect(resolveKiroAPIRegion(' eu-central-1 ')).toBe('eu-central-1')
  })

  it('includes an unsupported current API region as a disabled legacy option', () => {
    expect(buildKiroAPIRegionOptions('eu-north-1', region => `label:${region}`)).toEqual([
      { value: 'us-east-1', label: 'label:us-east-1' },
      { value: 'eu-central-1', label: 'label:eu-central-1' },
      { value: 'eu-north-1', label: 'label:eu-north-1', disabled: true }
    ])
  })

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
