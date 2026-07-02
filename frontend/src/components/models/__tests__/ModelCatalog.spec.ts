/* eslint-disable @typescript-eslint/triple-slash-reference */
/// <reference path="../../../vite-env.d.ts" />

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { chatAPI } from '@/api/chat'
import { getPublicModelCatalog } from '@/api/settings'
import ModelCatalog from '../ModelCatalog.vue'

vi.mock('@/api/settings', () => ({
  getPublicModelCatalog: vi.fn(),
}))

vi.mock('@/api/chat', () => ({
  chatAPI: {
    listModels: vi.fn(),
  },
}))

const i18nMessages = vi.hoisted(() => ({
  'modelCatalog.kicker': 'Model catalog',
  'modelCatalog.title': 'Available models',
  'modelCatalog.description': 'Compare model capabilities and pricing.',
  'modelCatalog.stats.models': 'Models',
  'modelCatalog.stats.providers': 'Providers',
  'modelCatalog.stats.text': 'Text models',
  'modelCatalog.stats.image': 'Image models',
  'modelCatalog.searchLabel': 'Search models',
  'modelCatalog.searchPlaceholder': 'Search by model, provider, feature',
  'modelCatalog.modelFilterLabel': 'Model',
  'modelCatalog.allFilterShort': 'All',
  'modelCatalog.providerLabel': 'Provider',
  'modelCatalog.allProviders': 'Every provider',
  'modelCatalog.modalityLabel': 'Modality',
  'modelCatalog.allModalities': 'All modalities',
  'modelCatalog.modality.text': 'Text',
  'modelCatalog.modality.image': 'Image',
  'modelCatalog.sortLabel': 'Sort',
  'modelCatalog.sort.default': 'Recommended',
  'modelCatalog.sort.newest': 'Newest',
  'modelCatalog.sort.provider': 'Provider',
  'modelCatalog.loading': 'Loading model catalog',
  'modelCatalog.loadError': 'Failed to load model catalog',
  'common.retry': 'Retry',
  'modelCatalog.emptyTitle': 'No models found',
  'modelCatalog.emptyDescription': 'Try changing search or filters.',
  'modelCatalog.context': 'Context',
  'modelCatalog.pricing.input': 'Input',
  'modelCatalog.pricing.output': 'Output',
  'modelCatalog.pricing.cacheRead': 'Cache read',
  'modelCatalog.pending': 'Pending confirmation',
  'modelCatalog.chatNow': '立即对话',
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => i18nMessages[key as keyof typeof i18nMessages] ?? key,
  }),
}))

const catalogFixture = {
  updated_at: '2026-06-21',
  providers: [
    { key: 'anthropic', name: 'Anthropic', accent_color: '#d97745', model_count: 1 },
    { key: 'openai', name: 'OpenAI', accent_color: '#27a644', model_count: 1 },
    { key: 'qwen', name: 'Alibaba Cloud', accent_color: '#7c6df2', model_count: 1 },
  ],
  models: [
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
      released_at: '2026-06-21',
      release_status: 'unverified',
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
      pricing: { currency: 'USD', unit: '1M tokens', note: 'Pending confirmation' },
      price_status: 'unverified',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: '',
      updated_at: '2026-06-21',
    },
  ],
}

const mockedGetPublicModelCatalog = vi.mocked(getPublicModelCatalog)
const mockedListChatModels = vi.mocked(chatAPI.listModels)

function chatModelsForCatalog(models = catalogFixture.models) {
  return models.map((model) => ({
    provider: model.provider,
    platform: model.provider,
    key_type: model.provider,
    model: model.model_name,
    display_name: model.display_name,
    supports_text: model.modalities.includes('text'),
    supports_image_input: model.features.includes('vision input'),
    supports_file_context: true,
    supports_artifact_output: model.features.includes('image generation'),
    supports_thinking: model.modalities.includes('text'),
    thinking_efforts: model.modalities.includes('text') ? ['low', 'medium', 'high'] : [],
    supports_web_search: false,
    supports_image_generation: model.features.includes('image generation'),
    image_generation_sizes: model.features.includes('image generation') ? ['1024x1024'] : [],
    image_generation_aspect_ratios: model.features.includes('image generation') ? ['1:1'] : [],
    image_generation_qualities: [],
    image_generation_output_formats: [],
    image_generation_backgrounds: [],
    price_status: model.price_status,
  }))
}

function mountCatalog() {
  return mount(ModelCatalog, {
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          methods: {
            hrefFor(to: string | { path: string; query?: Record<string, string> }) {
              if (typeof to === 'string') return to
              const params = new URLSearchParams()
              if (to.query) {
                Object.entries(to.query).forEach(([key, value]) => {
                  params.set(key, value)
                })
              }
              const query = params.toString()
              return query ? `${to.path}?${query}` : to.path
            },
          },
          template: '<a v-bind="$attrs" :href="hrefFor(to)"><slot /></a>',
        },
        Icon: { template: '<span data-testid="icon" />' },
        ModelIcon: { template: '<span data-testid="model-icon" />' },
      },
    },
  })
}

describe('ModelCatalog', () => {
  beforeEach(() => {
    mockedGetPublicModelCatalog.mockReset()
    mockedListChatModels.mockReset()
    mockedListChatModels.mockResolvedValue(chatModelsForCatalog())
  })

  it('renders confirmed and pending model cards', async () => {
    mockedGetPublicModelCatalog.mockResolvedValue(catalogFixture)

    const wrapper = mountCatalog()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('Claude Opus 4.8')
    expect(text).toContain('$5')
    expect(text).toContain('$25')
    expect(text).toContain('$0.5')
    expect(text).toContain('GPT-Image-2')
    expect(text).toContain('1K image')
    expect(text).toContain('$0.21')
    expect(text).toContain('Alibaba Cloud')
    expect(text).toContain('Qwen3.6 Plus')
    expect(text).toContain('Pending confirmation')
    expect(text).not.toContain('Released')
    expect(text).not.toContain('2026-06-21')
    expect(text).not.toContain('unverified')
    expect(text).not.toContain('Confirmed prices')
    expect(text).not.toContain('Updated')
    expect(text).not.toContain('Source')
  })

  it('hides public catalog models that are not available from web chat models', async () => {
    mockedGetPublicModelCatalog.mockResolvedValue(catalogFixture)
    mockedListChatModels.mockResolvedValue(chatModelsForCatalog(catalogFixture.models.slice(0, 2)))

    const wrapper = mountCatalog()
    await flushPromises()

    expect(wrapper.text()).toContain('Claude Opus 4.8')
    expect(wrapper.text()).toContain('GPT-Image-2')
    expect(wrapper.text()).not.toContain('Alibaba Cloud')
    expect(wrapper.text()).not.toContain('Qwen3.6 Plus')
    expect(wrapper.find('[data-testid="model-catalog-provider-chip"][data-provider="qwen"]').exists()).toBe(false)
  })

  it('links each model card to chat with the selected provider and model', async () => {
    mockedGetPublicModelCatalog.mockResolvedValue(catalogFixture)

    const wrapper = mountCatalog()
    await flushPromises()

    const chatLinks = wrapper.findAll('[data-testid="model-catalog-chat-link"]')
    expect(chatLinks).toHaveLength(catalogFixture.models.length)

    const imageChatLink = chatLinks.find((link) => link.attributes('data-model') === 'gpt-image-2')
    expect(imageChatLink?.text()).toContain('立即对话')
    expect(imageChatLink?.attributes('href')).toBe('/chat?provider=openai&model=gpt-image-2')
    expect(imageChatLink?.attributes('data-provider')).toBe('openai')
  })

  it('filters by search and provider', async () => {
    mockedGetPublicModelCatalog.mockResolvedValue(catalogFixture)

    const wrapper = mountCatalog()
    await flushPromises()

    const chips = wrapper.findAll('[data-testid="model-catalog-provider-chip"]')
    expect(chips[0].text()).toContain('All')
    expect(chips[0].attributes('aria-selected')).toBe('true')

    await wrapper.get('[data-testid="model-catalog-search"]').setValue('image')
    expect(wrapper.text()).toContain('GPT-Image-2')
    expect(wrapper.text()).not.toContain('Claude Opus 4.8')

    await wrapper.get('[data-testid="model-catalog-search"]').setValue('')
    const qwenChip = wrapper.find('[data-testid="model-catalog-provider-chip"][data-provider="qwen"]')
    await qwenChip.trigger('click')
    expect(wrapper.text()).toContain('Qwen3.6 Plus')
    expect(wrapper.text()).not.toContain('GPT-Image-2')
  })

  it('shows an error panel and retries loading the catalog', async () => {
    mockedGetPublicModelCatalog.mockRejectedValueOnce({}).mockResolvedValueOnce(catalogFixture)

    const wrapper = mountCatalog()
    await flushPromises()

    expect(wrapper.text()).toContain('Failed to load model catalog')
    expect(wrapper.get('[data-testid="model-catalog-retry"]').text()).toContain('Retry')

    await wrapper.get('[data-testid="model-catalog-retry"]').trigger('click')
    await flushPromises()

    expect(mockedGetPublicModelCatalog).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('Claude Opus 4.8')
  })
})
