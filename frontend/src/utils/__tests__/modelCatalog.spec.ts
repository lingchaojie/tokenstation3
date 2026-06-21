import { describe, expect, it } from 'vitest'

import type { PublicModelCatalogModel } from '@/api/settings'
import {
  buildModelCatalogProviderOptions,
  filterModelCatalog,
  formatModelCatalogAmount,
  sortModelCatalog,
} from '../modelCatalog'

const models: PublicModelCatalogModel[] = [
  {
    provider: 'anthropic',
    provider_name: 'Anthropic',
    model_name: 'claude-opus-4-8',
    display_name: 'Claude Opus 4.8',
    modalities: ['text'],
    description: 'Complex reasoning and coding',
    context_window: 200000,
    features: ['reasoning', 'prompt caching'],
    pricing: {
      currency: 'USD',
      unit: '1M tokens',
      input_per_million: 5,
      output_per_million: 25,
      cache_read_per_million: 0.5,
    },
    price_status: 'confirmed',
    source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
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
    source_url: 'https://openai.com/api/pricing/',
    updated_at: '2026-06-21',
  },
  {
    provider: 'qwen',
    provider_name: 'Qwen',
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

  it('filters by keyword, provider, and modality', () => {
    expect(filterModelCatalog(models, { query: 'coding', provider: 'all', modality: 'all' }).map((model) => model.model_name)).toEqual([
      'claude-opus-4-8',
      'qwen3.6-plus',
    ])
    expect(filterModelCatalog(models, { query: '', provider: 'openai', modality: 'image' }).map((model) => model.model_name)).toEqual([
      'gpt-image-2',
    ])
  })

  it('sorts by provider name and confirmation status', () => {
    expect(sortModelCatalog(models, 'provider').map((model) => model.provider_name)).toEqual(['Anthropic', 'OpenAI', 'Qwen'])
    expect(sortModelCatalog(models, 'status').map((model) => model.price_status)).toEqual(['confirmed', 'confirmed', 'unverified'])
  })

  it('builds provider options with stable counts', () => {
    expect(buildModelCatalogProviderOptions(models)).toEqual([
      { value: 'all', label: 'All providers', count: 3 },
      { value: 'anthropic', label: 'Anthropic', count: 1 },
      { value: 'openai', label: 'OpenAI', count: 1 },
      { value: 'qwen', label: 'Qwen', count: 1 },
    ])
  })
})
