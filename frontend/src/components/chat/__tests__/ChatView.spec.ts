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
  supports_image_generation: false,
  image_generation_sizes: [],
  image_generation_aspect_ratios: [],
  image_generation_qualities: [],
  image_generation_output_formats: [],
  image_generation_backgrounds: [],
  price_status: 'confirmed',
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
  supports_image_generation: true,
  image_generation_sizes: ['1024x1024', '1536x1024'],
  image_generation_aspect_ratios: ['1:1', '3:2'],
  image_generation_qualities: ['medium', 'high'],
  image_generation_output_formats: ['png', 'webp'],
  image_generation_backgrounds: ['opaque', 'transparent'],
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
})

describe('ModelSelector', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('shows marketplace-style provider logos in the provider selector', async () => {
    const store = useChatStore()
    store.models = [anthropicModel, chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(ModelSelector)

    expect(wrapper.get('[data-testid="chat-provider-trigger"]').findComponent(ModelIcon).props('model')).toBe('gpt-5')

    await wrapper.get('[data-testid="chat-provider-trigger"]').trigger('click')

    const options = wrapper.findAll('[data-testid="chat-provider-option"]')
    expect(options).toHaveLength(2)
    expect(options.map((option) => option.findComponent(ModelIcon).props('model'))).toEqual(['claude', 'gpt-5'])

    await options[0].trigger('click')

    expect(store.selectedModel).toMatchObject({
      provider: 'anthropic',
      model: 'claude-opus-4-8',
    })
  })

  it('lets users change thinking mode and effort from composer options', async () => {
    const store = useChatStore()
    store.models = [chatModel]
    store.selectedModel = chatModel

    const wrapper = mount(Composer)
    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    const toggle = wrapper.get('[data-testid="chat-thinking-toggle"]')
    expect(toggle.attributes('aria-pressed')).toBe('false')

    await toggle.trigger('click')
    expect(store.thinkingEnabled).toBe(true)
    expect(toggle.attributes('aria-pressed')).toBe('true')

    await wrapper.get('[data-testid="chat-thinking-effort"]').setValue('high')
    expect(store.thinkingEffort).toBe('high')
  })

  it('lets users change image generation parameters from composer options', async () => {
    const store = useChatStore()
    store.models = [imageModel]
    store.selectedModel = imageModel

    const wrapper = mount(Composer)
    await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

    const toggle = wrapper.get('[data-testid="chat-image-generation-toggle"]')
    expect(toggle.attributes('aria-pressed')).toBe('true')

    await toggle.trigger('click')
    expect(store.imageGenerationEnabled).toBe(false)
    expect(toggle.attributes('aria-pressed')).toBe('false')

    await toggle.trigger('click')
    await wrapper.get('[data-testid="chat-image-generation-size"]').setValue('1536x1024')
    await wrapper.get('[data-testid="chat-image-generation-aspect-ratio"]').setValue('3:2')
    await wrapper.get('[data-testid="chat-image-generation-quality"]').setValue('high')
    await wrapper.get('[data-testid="chat-image-generation-output-format"]').setValue('webp')
    await wrapper.get('[data-testid="chat-image-generation-background"]').setValue('transparent')

    expect(store.imageGenerationEnabled).toBe(true)
    expect(store.imageGenerationSize).toBe('1536x1024')
    expect(store.imageGenerationAspectRatio).toBe('3:2')
    expect(store.imageGenerationQuality).toBe('high')
    expect(store.imageGenerationOutputFormat).toBe('webp')
    expect(store.imageGenerationBackground).toBe('transparent')
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
