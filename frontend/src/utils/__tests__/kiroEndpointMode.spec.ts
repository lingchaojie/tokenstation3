import { describe, expect, it } from 'vitest'

import {
  normalizeKiroEndpointMode,
  resolveKiroEndpointModeForGroupPayload,
} from '../kiroEndpointMode'

describe('normalizeKiroEndpointMode', () => {
  it.each(['q', 'krs', 'auto'] as const)('preserves supported mode %s', mode => {
    expect(normalizeKiroEndpointMode(mode)).toBe(mode)
  })

  it.each([undefined, null, '', 'bogus', 1, {}])('falls back to q for unsupported value %j', value => {
    expect(normalizeKiroEndpointMode(value)).toBe('q')
  })
})

describe('resolveKiroEndpointModeForGroupPayload', () => {
  it.each(['q', 'krs', 'auto'] as const)('serializes supported Kiro mode %s', mode => {
    expect(resolveKiroEndpointModeForGroupPayload('kiro', mode)).toBe(mode)
  })

  it('serializes unsupported Kiro values as q', () => {
    expect(resolveKiroEndpointModeForGroupPayload('kiro', 'bogus')).toBe('q')
  })

  it('forces non-Kiro group payloads to q', () => {
    expect(resolveKiroEndpointModeForGroupPayload('anthropic', 'auto')).toBe('q')
  })
})
