import { describe, expect, it } from 'vitest'
import {
  CONCRETE_PLATFORM_OPTIONS,
  CONCRETE_PLATFORM_VALUES,
  GROUP_PLATFORM_OPTIONS,
  GROUP_PLATFORM_VALUES,
} from '@/constants/platforms'
import { PROVIDERS } from '@/constants/channelMonitor'
import {
  normalizePlatformQuotasMap,
  SCHEDULING_THRESHOLD_PLATFORMS,
} from '@/api/admin/settings'

const concretePlatforms = [
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'kiro',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'cursor'
]

describe('platform option catalogs', () => {
  it('exposes every concrete account platform', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
    expect(CONCRETE_PLATFORM_VALUES).toEqual(concretePlatforms)
  })


  it('keeps group-backed filters limited to concrete platforms', () => {
    expect(GROUP_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
    expect(GROUP_PLATFORM_VALUES).toEqual(concretePlatforms)
  })

  it('registers Cursor as an account and quota platform only', () => {
    expect(CONCRETE_PLATFORM_VALUES).toContain('cursor')
    expect(normalizePlatformQuotasMap()).toHaveProperty('cursor')
    expect(SCHEDULING_THRESHOLD_PLATFORMS).not.toContain('cursor')
    expect(PROVIDERS).not.toContain('cursor')
  })
})
