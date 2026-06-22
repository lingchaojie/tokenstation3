import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChatView from '@/views/user/ChatView.vue'
import Composer from '@/components/chat/Composer.vue'
import ModelSelector from '@/components/chat/ModelSelector.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import { chatAPI, type WebChatModel } from '@/api/chat'
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
  price_status: 'confirmed',
}

const AppLayoutStub = {
  template: '<div data-testid="app-layout"><slot /></div>',
}

describe('ChatView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
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
})
