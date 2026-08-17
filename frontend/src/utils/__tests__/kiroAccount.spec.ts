import { describe, expect, it } from 'vitest'

import {
  buildKiroAPIRegionOptions,
  DEFAULT_KIRO_API_REGION,
  isKiroDirectApiKeyAccount,
  isKiroRelayAccount,
  KIRO_API_REGIONS,
  resolveKiroAPIRegion,
  resolveKiroAPIRegionFromCredentials
} from '@/utils/kiroAccount'

describe('kiroAccount helpers', () => {
  it('defines the supported Kiro API regions', () => {
    expect(DEFAULT_KIRO_API_REGION).toBe('us-east-1')
    expect(KIRO_API_REGIONS).toHaveLength(34)
    expect(KIRO_API_REGIONS[0]).toBe('us-east-1')
    expect(KIRO_API_REGIONS).toContain('ap-southeast-7')
    expect(KIRO_API_REGIONS).toContain('mx-central-1')
    expect(KIRO_API_REGIONS).toContain('sa-east-1')
  })

  it('resolves blank API regions to the default and trims configured regions', () => {
    expect(resolveKiroAPIRegion(undefined)).toBe('us-east-1')
    expect(resolveKiroAPIRegion('')).toBe('us-east-1')
    expect(resolveKiroAPIRegion(' eu-central-1 ')).toBe('eu-central-1')
  })

  it('resolves persisted API region aliases without confusing OAuth IDC region', () => {
    expect(resolveKiroAPIRegionFromCredentials({ api_region: ' eu-west-1 ', apiRegion: 'ap-south-1' }, 'oauth')).toBe('eu-west-1')
    expect(resolveKiroAPIRegionFromCredentials({ apiRegion: ' ap-south-1 ' }, 'oauth')).toBe('ap-south-1')
    expect(resolveKiroAPIRegionFromCredentials({ region: ' ca-west-1 ' }, 'apikey')).toBe('ca-west-1')
    expect(resolveKiroAPIRegionFromCredentials({ region: ' eu-north-1 ' }, 'oauth')).toBe(DEFAULT_KIRO_API_REGION)
  })

  it('includes an unsupported current API region as a disabled legacy option', () => {
    const options = buildKiroAPIRegionOptions('legacy-1', region => `label:${region}`)
    expect(options).toHaveLength(35)
    expect(options.at(-1)).toEqual({
      value: 'legacy-1',
      label: 'label:legacy-1',
      disabled: true
    })
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
