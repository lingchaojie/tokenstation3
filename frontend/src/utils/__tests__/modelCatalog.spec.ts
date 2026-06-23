import { describe, expect, it } from 'vitest'

import type { PublicModelCatalogModel } from '@/api/settings'
import {
  buildModelCatalogProviderOptions,
  filterModelCatalog,
  formatContextWindow,
  formatModelCatalogAmount,
  sortModelCatalog,
} from '../modelCatalog'

const models: PublicModelCatalogModel[] = [
  {
    provider: 'anthropic',
    provider_name: 'Anthropic',
    model_name: 'claude-sonnet-4',
    display_name: 'Claude Sonnet 4',
    modalities: ['text'],
    description: 'General reasoning',
    context_window: 200000,
    features: ['reasoning', 'prompt caching'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 3,
      output_per_million: 15,
      cache_read_per_million: 0.3,
    },
    price_status: 'confirmed',
    released_at: '2025-05-22',
    release_status: 'confirmed',
    source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
    updated_at: '2026-06-21',
  },
  {
    provider: 'anthropic',
    provider_name: 'Anthropic',
    model_name: 'claude-opus-4-8',
    display_name: 'Claude Opus 4.8',
    modalities: ['text'],
    description: 'Newer Claude model',
    context_window: 200000,
    features: ['reasoning'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 5,
      output_per_million: 25,
    },
    price_status: 'confirmed',
    released_at: '2026-06-21',
    release_status: 'unverified',
    source_url: '',
    updated_at: '2026-06-21',
  },
  {
    provider: 'openai',
    provider_name: 'OpenAI',
    model_name: 'gpt-image-2',
    display_name: 'GPT-Image-2',
    modalities: ['image'],
    description: 'Image generation',
    context_window: 0,
    features: ['image generation'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 2.5,
      output_per_million: 5,
      price_lines: [{ label: '1K image', amount: 0.21, unit: 'image' }],
    },
    price_status: 'confirmed',
    released_at: '2026-06-15',
    release_status: 'unverified',
    source_url: 'https://openai.com/api/pricing/',
    updated_at: '2026-06-21',
  },
  {
    provider: 'qwen',
    provider_name: 'Alibaba Cloud',
    model_name: 'qwen3.6-plus',
    display_name: 'Qwen3.6 Plus',
    modalities: ['text'],
    description: 'Agentic coding',
    context_window: 1000000,
    features: ['reasoning'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      note: 'Pending confirmation',
    },
    price_status: 'unverified',
    released_at: '2026-06-21',
    release_status: 'unverified',
    source_url: '',
    updated_at: '2026-06-21',
  },
]

describe('model catalog utilities', () => {
  it('formats compact USD amounts without noisy decimals', () => {
    expect(formatModelCatalogAmount(5)).toBe('$5')
    expect(formatModelCatalogAmount(0.075)).toBe('$0.075')
    expect(formatModelCatalogAmount(0.003625)).toBe('$0.003625')
  })

  it('formats official context windows without rounding away provider values', () => {
    expect(formatContextWindow(1_050_000)).toBe('1.05M')
    expect(formatContextWindow(1_048_576)).toBe('1M')
    expect(formatContextWindow(262_144)).toBe('256K')
    expect(formatContextWindow(131_072)).toBe('128K')
    expect(formatContextWindow(200_000)).toBe('200K')
  })

  it('filters by keyword, provider, and modality', () => {
    expect(filterModelCatalog(models, { query: 'coding', provider: 'all', modality: 'all' }).map((model) => model.model_name)).toEqual(['qwen3.6-plus'])
    expect(filterModelCatalog(models, { query: '', provider: 'openai', modality: 'image' }).map((model) => model.model_name)).toEqual([
      'gpt-image-2',
    ])
  })

  it('sorts by provider name', () => {
    expect(sortModelCatalog(models, 'provider').map((model) => model.provider_name)).toEqual(['Anthropic', 'Anthropic', 'OpenAI', 'Alibaba Cloud'])
  })

  it('sorts default rows by provider then newest release date within provider', () => {
    expect(sortModelCatalog(models, 'default').map((model) => model.model_name)).toEqual([
      'claude-opus-4-8',
      'claude-sonnet-4',
      'gpt-image-2',
      'qwen3.6-plus',
    ])
  })

  it('builds provider options with stable counts', () => {
    expect(buildModelCatalogProviderOptions(models)).toEqual([
      { value: 'all', label: 'All providers', count: 4 },
      { value: 'anthropic', label: 'Anthropic', count: 2 },
      { value: 'openai', label: 'OpenAI', count: 1 },
      { value: 'qwen', label: 'Alibaba Cloud', count: 1 },
    ])
  })
})
