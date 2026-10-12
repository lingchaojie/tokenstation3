import { afterEach, describe, expect, it } from 'vitest'
import { computed } from 'vue'
import {
  CONCRETE_PLATFORM_OPTIONS,
  CONCRETE_PLATFORM_VALUES,
  GROUP_PLATFORM_OPTIONS,
  GROUP_PLATFORM_VALUES,
} from '@/constants/platforms'
import {
  BUILTIN_PLATFORM_CATALOG,
  listPlatformIds,
  platformDisplayName,
  resetPlatformCatalog,
  setPlatformCatalog
} from '@/constants/platformCatalog'
import { platformLabel } from '@/utils/platformColors'
import { normalizePlatformQuotasMap, sanitizePlatformQuotasMap } from '@/api/admin/settings'
import { platformQuotaPlatforms } from '@/api/admin/users'

// 改为读取平台清单之前的平台字面量，加上之后内置登记的 Command Code。
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
  'minimax',
  'opencode_go',
  'typesafe',
  'command_code',
  'cline'
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
})

describe('platform catalog with a newly registered platform', () => {
  const withNewProvider = {
    platforms: [
      ...BUILTIN_PLATFORM_CATALOG.platforms,
      { id: 'acme_router', display_name: 'Acme Router', gateway: 'openai', cn_provider: false }
    ],
  }

  afterEach(() => {
    resetPlatformCatalog()
  })

  it('keeps the built-in catalog equal to the legacy literals', () => {
    expect(listPlatformIds()).toEqual(concretePlatforms)
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.label)).toEqual([
      'Anthropic', 'OpenAI', 'Gemini', 'Antigravity', 'Kiro', 'Grok',
      'Kimi', 'Zhipu GLM', 'DeepSeek', 'MiniMax', 'OpenCode', 'TypeSafe / Jev', 'Command Code', 'Cline'
    ])
    expect(platformLabel('zhipu')).toBe('Zhipu GLM')
    expect(CONCRETE_PLATFORM_VALUES).not.toContain('composite')
    expect(GROUP_PLATFORM_VALUES).not.toContain('composite')
    expect(platformLabel('')).toBe('API')
  })

  it('adds newly registered platforms to options, labels and quota lists reactively', () => {
    const optionValues = computed(() => CONCRETE_PLATFORM_OPTIONS.map((option) => option.value))
    expect(optionValues.value).not.toContain('acme_router')

    setPlatformCatalog(withNewProvider)

    expect(optionValues.value).toEqual([...concretePlatforms, 'acme_router'])
    expect(CONCRETE_PLATFORM_VALUES).toEqual([...concretePlatforms, 'acme_router'])
    expect(GROUP_PLATFORM_VALUES).toEqual([...concretePlatforms, 'acme_router'])
    expect(GROUP_PLATFORM_OPTIONS.map((option) => option.value)).toEqual([
      ...concretePlatforms,
      'acme_router'
    ])
    expect(platformLabel('acme_router')).toBe('Acme Router')
    expect(platformDisplayName('acme_router')).toBe('Acme Router')
    expect(platformLabel('unregistered')).toBe('unregistered')
    expect(platformQuotaPlatforms()).toContain('acme_router')
    expect(Object.keys(normalizePlatformQuotasMap())).toEqual([...concretePlatforms, 'acme_router'])
    expect(sanitizePlatformQuotasMap({ acme_router: { daily: 5, weekly: -1, monthly: null } }).acme_router).toEqual({
      daily: 5,
      weekly: null,
      monthly: null
    })
  })

  it('restores the built-in catalog on reset', () => {
    setPlatformCatalog(withNewProvider)
    resetPlatformCatalog()
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
  })
})
