import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, string | string[] | undefined>,
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => ({
      query: routeState.query,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: en } = await vi.importActual<{ default: Record<string, unknown> }>('@/i18n/locales/en')
  const resolve = (key: string): unknown =>
    key.split('.').reduce<unknown>((acc, part) => (acc && typeof acc === 'object' ? (acc as Record<string, unknown>)[part] : undefined), en)
  const translate = (key: string, params?: Record<string, unknown>): string => {
    const value = resolve(key)
    if (typeof value !== 'string') return key
    if (!params) return value
    return value.replace(/\{(\w+)\}/g, (_match, name: string) => (params[name] !== undefined ? String(params[name]) : `{${name}}`))
  }
  return {
    ...actual,
    useI18n: () => ({ t: translate, locale: { value: 'en' } }),
  }
})

import ChatView from '@/views/user/ChatView.vue'
import ChatShell from '@/components/chat/ChatShell.vue'
import Composer from '@/components/chat/Composer.vue'
import ModelSelector from '@/components/chat/ModelSelector.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import { chatAPI, type WebChatConversation, type WebChatMessage, type WebChatModel } from '@/api/chat'
import { useChatStore } from '@/stores/chat'

const chatModel: WebChatModel = {
  provider: 'openai',
  platform: 'openai',
  key_type: 'openai',
  model: 'gpt-5.4',
  display_name: 'GPT-5.4',
  supports_text: true,
  supports_image_input: true,
  supports_file_context: true,
  supports_artifact_output: true,
  supports_thinking: true,
  thinking_efforts: ['low', 'medium', 'high', 'xhigh'],
  supports_web_search: true,
  supports_image_generation: false,
  image_generation_sizes: [],
  image_generation_aspect_ratios: [],
  image_generation_qualities: [],
  image_generation_output_formats: [],
  image_generation_backgrounds: [],
  price_status: 'confirmed',
}

const anthropicModel: WebChatModel = {
  provider: 'anthropic',
  platform: 'anthropic',
  key_type: 'anthropic',
  model: 'claude-opus-4-8',
  display_name: 'Claude Opus 4.8',
  supports_text: true,
  supports_image_input: true,
  supports_file_context: true,
  supports_artifact_output: true,
  supports_thinking: true,
  thinking_efforts: ['medium', 'high', 'xhigh'],
  supports_web_search: false,
  supports_image_generation: false,
  image_generation_sizes: [],
  image_generation_aspect_ratios: [],
  image_generation_qualities: [],
  image_generation_output_formats: [],
  image_generation_backgrounds: [],
  price_status: 'confirmed',
}

const datedHaikuModel: WebChatModel = {
  ...anthropicModel,
  model: 'claude-haiku-4-5-20251001',
  display_name: 'claude-haiku-4-5',
}

const imageModel: WebChatModel = {
  provider: 'openai',
  platform: 'openai',
  key_type: 'openai',
  model: 'gpt-image-2',
  display_name: 'GPT Image 2',
  supports_text: true,
  supports_image_input: false,
  supports_file_context: true,
  supports_artifact_output: true,
  supports_thinking: false,
  thinking_efforts: [],
  supports_web_search: false,
  supports_image_generation: true,
  image_generation_sizes: ['1024x1024', '1536x1024'],
  image_generation_aspect_ratios: ['1:1', '3:2'],
  image_generation_qualities: ['low', 'medium', 'high'],
  image_generation_output_formats: ['png', 'webp'],
  image_generation_backgrounds: ['opaque', 'auto'],
  price_status: 'confirmed',
}

const AppLayoutStub = {
  template: '<div data-testid="app-layout"><slot /></div>',
}

const historicalConversation: WebChatConversation = {
  id: 8,
  title: 'Historical image chat',
  default_model: 'gpt-image-2',
  default_provider: 'openai',
  last_model: 'gpt-image-2',
  last_provider: 'openai',
  status: 'active',
  message_count: 2,
  created_at: '2026-06-22T00:00:00Z',
  updated_at: '2026-06-22T00:00:01Z',
}

const historicalMessage: WebChatMessage = {
  id: 101,
  conversation_id: 8,
  user_id: 1,
  role: 'assistant',
  model: 'gpt-image-2',
  provider: 'openai',
  content_text: 'Done.',
  content_json: [],
  status: 'completed',
  created_at: '2026-06-22T00:00:01Z',
  updated_at: '2026-06-22T00:00:01Z',
}

describe('ChatView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    routeState.query = {}
    vi.restoreAllMocks()
    vi.spyOn(chatAPI, 'listModels').mockResolvedValue([chatModel])
    vi.spyOn(chatAPI, 'listConversations').mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 50,
      pages: 0,
    })
  })

  it('renders the logged-in chat workspace instead of a getting-started page', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('New chat')
    expect(wrapper.find('textarea').exists()).toBe(true)
    expect(wrapper.get('[data-testid="app-layout"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="chat-immersive-view"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Get started')
    expect(chatAPI.listModels).toHaveBeenCalledOnce()
    expect(chatAPI.listConversations).toHaveBeenCalledOnce()
  })

  it('selects the requested model from chat route query parameters', async () => {
    vi.spyOn(chatAPI, 'listModels').mockResolvedValue([chatModel, anthropicModel])
    routeState.query = {
      provider: 'anthropic',
      model: 'claude-opus-4-8',
    }

    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
        },
      },
    })

    await flushPromises()

    const store = useChatStore()
    expect(store.selectedModel).toMatchObject({
      provider: 'anthropic',
      model: 'claude-opus-4-8',
    })
    expect(wrapper.findComponent(ChatShell).props('initialMobilePanel')).toBe('chat')
  })
})

describe('Composer', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('disables send while streaming and exposes a stop control', () => {
    const store = useChatStore()
    store.selectedModel = chatModel
    store.streaming = true

    const wrapper = mount(Composer)

    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="chat-stop"]').exists()).toBe(true)
  })

  it('clears the draft immediately after submit so the click has instant feedback', async () => {
    const store = useChatStore()
    store.selectedModel = chatModel
    vi.spyOn(store, 'sendMessage').mockReturnValue(new Promise(() => {}))

    const wrapper = mount(Composer)
    const textarea = wrapper.get('textarea')
    await textarea.setValue('Hello without lag')
    await wrapper.get('[data-testid="chat-send"]').trigger('click')
    await Promise.resolve()

    expect((textarea.element as HTMLTextAreaElement).value).toBe('')
  })

  it('hosts model selection in the composer control row', async () => {
    const store = useChatStore()
    store.models = [anthropicModel, chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(Composer)

    expect(wrapper.get('[data-testid="chat-model-menu-toggle"]').text()).toContain('GPT-5.4')
    expect(wrapper.find('[data-testid="chat-model-menu"]').exists()).toBe(false)

    await wrapper.get('[data-testid="chat-model-menu-toggle"]').trigger('click')

    const menu = wrapper.get('[data-testid="chat-model-menu"]')
    expect(menu.get('[data-testid="chat-provider-trigger"]').findComponent(ModelIcon).props('model')).toBe('gpt-5')

    await menu.get('[data-testid="chat-provider-trigger"]').trigger('click')
    const options = menu.findAll('[data-testid="chat-provider-option"]')
    expect(options).toHaveLength(2)
    expect(options.map((option) => option.findComponent(ModelIcon).props('model'))).toEqual(['claude', 'gpt-5'])
    expect(options.map((option) => option.text())).toEqual(['Anthropic', 'OpenAI'])
    expect(menu.get('[data-testid="chat-provider-options"]').classes()).not.toContain('absolute')

    await options[0].trigger('click')

    expect(store.selectedModel).toMatchObject({
      provider: 'anthropic',
      model: 'claude-opus-4-8',
    })
  })

  it('shows provider labels in title case without changing selected routing values', async () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(Composer)

    await wrapper.get('[data-testid="chat-model-menu-toggle"]').trigger('click')

    const menu = wrapper.get('[data-testid="chat-model-menu"]')
    expect(menu.get('[data-testid="chat-provider-trigger"]').text()).toContain('OpenAI')
    expect(store.selectedModel).toMatchObject({
      provider: 'openai',
      model: 'gpt-5.4',
    })
  })

  it('formats model fallback labels without changing selected routing values', async () => {
    const rawModel: WebChatModel = {
      ...chatModel,
      model: 'gpt-5.4',
      display_name: '',
    }
    const store = useChatStore()
    store.models = [rawModel]
    store.selectedModel = rawModel

    const wrapper = mount(Composer)

    expect(wrapper.get('[data-testid="chat-model-menu-toggle"]').text()).toContain('GPT-5.4')

    await wrapper.get('[data-testid="chat-model-menu-toggle"]').trigger('click')

    const option = wrapper.get('[data-testid="chat-model-select"] option')
    expect(option.text()).toBe('GPT-5.4')
    expect(store.selectedModel).toMatchObject({
      provider: 'openai',
      model: 'gpt-5.4',
    })
  })

  it('shows a human-readable dated Claude model without changing its routing value', async () => {
    const store = useChatStore()
    store.models = [datedHaikuModel]
    store.selectedModel = datedHaikuModel

    const wrapper = mount(Composer)

    expect(wrapper.get('[data-testid="chat-model-menu-toggle"]').text()).toContain('Claude Haiku 4.5')
    expect(wrapper.text()).not.toContain('20251001')
    expect(store.selectedModel.model).toBe('claude-haiku-4-5-20251001')
  })

  it('omits secondary status text for thinking and web search toggles', async () => {
    const store = useChatStore()
    store.selectedModel = chatModel

    const wrapper = mount(Composer)

    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    expect(wrapper.get('[data-testid="chat-thinking-toggle"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="chat-web-search-toggle"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('关闭')
    expect(wrapper.text()).not.toContain('强制搜索')
    expect(wrapper.text()).not.toContain('使用该模型最高思考档位')
  })
})

describe('ChatShell', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('lets mobile users open a historical conversation and return to the conversation list', async () => {
    vi.spyOn(chatAPI, 'getConversation').mockResolvedValue({
      conversation: historicalConversation,
      messages: [historicalMessage],
    })
    const store = useChatStore()
    store.models = [imageModel]
    store.selectedModel = imageModel
    store.conversations = [historicalConversation]

    const wrapper = mount(ChatShell, {
      global: {
        stubs: {
          Icon: true,
          ModelIcon: true,
        },
      },
    })

    await wrapper.get('[data-testid="chat-conversation-open-8"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="chat-mobile-back"]').text()).toContain('Chats')
    expect(wrapper.get('[data-testid="chat-conversation-rail"]').classes()).toContain('hidden')

    await wrapper.get('[data-testid="chat-mobile-back"]').trigger('click')

    expect(wrapper.get('[data-testid="chat-conversation-rail"]').classes()).not.toContain('hidden')
  })

  it('can start on the mobile chat panel when opened from a model link', () => {
    const store = useChatStore()
    store.models = [imageModel]
    store.selectedModel = imageModel
    store.conversations = [historicalConversation]

    const wrapper = mount(ChatShell, {
      props: {
        initialMobilePanel: 'chat',
      },
      global: {
        stubs: {
          Icon: true,
          ModelIcon: true,
        },
      },
    })

    expect(wrapper.get('[data-testid="chat-conversation-rail"]').classes()).toContain('hidden')
    expect(wrapper.get('[data-testid="chat-main-panel"]').classes()).not.toContain('hidden')
  })

  it('formats the model fallback used by the mobile conversation title', () => {
    const store = useChatStore()
    store.models = [datedHaikuModel]
    store.selectedModel = datedHaikuModel
    store.currentConversation = {
      conversation: {
        ...historicalConversation,
        title: '',
        default_provider: 'anthropic',
        last_provider: 'anthropic',
        default_model: datedHaikuModel.model,
        last_model: datedHaikuModel.model,
      },
      messages: [],
    }

    const wrapper = mount(ChatShell, {
      props: { initialMobilePanel: 'chat' },
      global: { stubs: { Icon: true, ModelIcon: true } },
    })

    expect(wrapper.get('[data-testid="chat-mobile-title"]').text()).toBe('Claude Haiku 4.5')
  })
})

describe('ModelSelector', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders a compact conversation header instead of top-level model form controls', () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel
    store.currentConversation = {
      conversation: historicalConversation,
      messages: [],
    }

    const wrapper = mount(ModelSelector)

    expect(wrapper.text()).toContain('Historical image chat')
    expect(wrapper.text()).toContain('GPT-5.4')
    expect(wrapper.find('[data-testid="chat-provider-trigger"]').exists()).toBe(false)
    expect(wrapper.find('select[aria-label="Model"]').exists()).toBe(false)
  })

  it('uses the same human-readable name in the compact model chip', () => {
    const store = useChatStore()
    store.models = [datedHaikuModel]
    store.selectedModel = datedHaikuModel

    const wrapper = mount(ModelSelector)

    expect(wrapper.get('[data-testid="chat-current-model-chip"]').text()).toContain('Claude Haiku 4.5')
    expect(wrapper.text()).not.toContain('20251001')
  })

  it('exposes deep thinking as one composer option without model effort controls', async () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(Composer)
    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    const toggle = wrapper.get('[data-testid="chat-thinking-toggle"]')
    expect(toggle.text()).toContain('Deep thinking')
    expect(toggle.attributes('aria-pressed')).toBe('false')
    expect(wrapper.find('[data-testid="chat-thinking-effort"]').exists()).toBe(false)

    await toggle.trigger('click')
    expect(store.thinkingEnabled).toBe(true)
    expect(toggle.attributes('aria-pressed')).toBe('true')
  })

  it('embeds composer options above the textarea instead of rendering them as an overlay', async () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(Composer)
    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    const panel = wrapper.get('[data-testid="chat-options-panel"]')
    expect(panel.classes()).toContain('mb-2')
    expect(panel.classes()).not.toContain('absolute')
    expect(panel.element.nextElementSibling).toBe(wrapper.get('textarea').element)
  })

  it('lets users change image generation parameters from composer options', async () => {
    const store = useChatStore()
    store.models = [imageModel]
    store.selectedModel = imageModel

    const wrapper = mount(Composer)
    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    const toggle = wrapper.get('[data-testid="chat-image-generation-toggle"]')
    expect(toggle.attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-testid="chat-thinking-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="chat-web-search-toggle"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="chat-image-generation-background"] option').map((option) => option.text())).toEqual(['Opaque', 'Auto'])

    await toggle.trigger('click')
    expect(store.imageGenerationEnabled).toBe(false)
    expect(toggle.attributes('aria-pressed')).toBe('false')

    await toggle.trigger('click')
    await wrapper.get('[data-testid="chat-image-generation-size"]').setValue('1536x1024')
    await wrapper.get('[data-testid="chat-image-generation-aspect-ratio"]').setValue('3:2')
    await wrapper.get('[data-testid="chat-image-generation-quality"]').setValue('high')
    await wrapper.get('[data-testid="chat-image-generation-output-format"]').setValue('webp')

    expect(store.imageGenerationEnabled).toBe(true)
    expect(store.imageGenerationSize).toBe('1536x1024')
    expect(store.imageGenerationAspectRatio).toBe('3:2')
    expect(store.imageGenerationQuality).toBe('high')
    expect(store.imageGenerationOutputFormat).toBe('webp')
    expect(store.imageGenerationBackground).toBe('opaque')
  })

  it('does not render an Artifacts capability control in the model header', () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(ModelSelector)

    expect(wrapper.text()).not.toContain('Artifacts')
  })

  it('keeps image generation parameters out of the top model header', () => {
    const store = useChatStore()
    store.models = [imageModel]
    store.selectedModel = imageModel

    const wrapper = mount(ModelSelector)

    expect(wrapper.find('[data-testid="chat-image-generation-size"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="chat-image-generation-quality"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="chat-image-generation-output-format"]').exists()).toBe(false)
  })
})

describe('ConversationRail reference layout', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('formats and searches historical conversation model labels', async () => {
    const haikuConversation: WebChatConversation = {
      ...historicalConversation,
      id: 10,
      title: 'Haiku research',
      default_provider: 'anthropic',
      last_provider: 'anthropic',
      default_model: datedHaikuModel.model,
      last_model: datedHaikuModel.model,
    }
    const store = useChatStore()
    store.models = [datedHaikuModel]
    store.conversations = [haikuConversation]

    const wrapper = mount(ChatShell, {
      global: {
        stubs: {
          Icon: true,
          ModelIcon: true,
        },
      },
    })

    const rail = wrapper.get('[data-testid="chat-conversation-rail"]')
    expect(rail.text()).toContain('Claude Haiku 4.5')
    expect(rail.text()).not.toContain('20251001')

    await rail.get('input[type="search"]').setValue('Haiku 4.5')
    expect(rail.text()).toContain('Haiku research')

    await rail.get('input[type="search"]').setValue('claude-haiku-4-5-20251001')
    expect(rail.text()).toContain('Haiku research')
  })

  it('shows a workspace rail with model-grouped conversations', () => {
    const store = useChatStore()
    store.models = [imageModel, chatModel]
    store.selectedModel = imageModel
    store.conversations = [
      historicalConversation,
      {
        ...historicalConversation,
        id: 9,
        title: 'Text chat',
        default_model: 'gpt-5.4',
        last_model: 'gpt-5.4',
      },
    ]

    const wrapper = mount(ChatShell, {
      global: {
        stubs: {
          Icon: true,
          ModelIcon: true,
        },
      },
    })

    const rail = wrapper.get('[data-testid="chat-conversation-rail"]')
    expect(rail.text()).toContain('Conversations')
    expect(rail.text()).toContain('Start a model conversation instantly')
    expect(rail.text()).toContain('GPT Image 2')
    expect(rail.text()).toContain('GPT-5.4')
    expect(wrapper.find('[data-testid="chat-model-group-gpt-image-2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="chat-model-group-gpt-5.4"]').exists()).toBe(true)
  })
})
