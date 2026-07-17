import { describe, expect, it } from 'vitest'

import enOverview from '@/i18n/locales/en/admin/overview'
import zhOverview from '@/i18n/locales/zh/admin/overview'
import {
  normalizeKiroEndpointMode,
  resolveKiroEndpointModeForGroupPayload,
} from '@/utils/kiroEndpointMode'

describe('GroupsView Kiro endpoint mode behavior', () => {
  it('serializes create and update payloads with auto only for Kiro groups', () => {
    expect(resolveKiroEndpointModeForGroupPayload('kiro', 'auto')).toBe('auto')
    expect(resolveKiroEndpointModeForGroupPayload('kiro', 'bogus')).toBe('q')
    expect(resolveKiroEndpointModeForGroupPayload('anthropic', 'auto')).toBe('q')
  })

  it('echoes supported edit values and normalizes unknown backend values to q', () => {
    expect(normalizeKiroEndpointMode('auto')).toBe('auto')
    expect(normalizeKiroEndpointMode('krs')).toBe('krs')
    expect(normalizeKiroEndpointMode(undefined)).toBe('q')
    expect(normalizeKiroEndpointMode('future-mode')).toBe('q')
  })

  it('exposes localized auto fallback labels', () => {
    expect(enOverview.groups.kiroCache.endpointModeAuto).toBe(
      'Auto (Q → KRS on retryable failure)',
    )
    expect(zhOverview.groups.kiroCache.endpointModeAuto).toBe(
      '自动（Q 遇到可重试失败后切换 KRS）',
    )
  })
})
