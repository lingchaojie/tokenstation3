import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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

import MessageList from '@/components/chat/MessageList.vue'
import { chatAPI, type WebChatArtifact, type WebChatConversation, type WebChatMessage } from '@/api/chat'
import { useChatStore } from '@/stores/chat'

const conversation: WebChatConversation = {
  id: 8,
  title: 'Image',
  default_model: 'gpt-image-2',
  default_provider: 'openai',
  last_model: 'gpt-image-2',
  last_provider: 'openai',
  status: 'active',
  message_count: 1,
  created_at: '2026-06-22T00:00:00Z',
  updated_at: '2026-06-22T00:00:01Z',
}

const imageArtifact: WebChatArtifact = {
  id: 44,
  message_id: 101,
  conversation_id: 8,
  user_id: 1,
  filename: 'generated-image-1.png',
  content_type: 'image/png',
  size_bytes: 12,
  storage_key: 'generated/generated-image-1.png',
  sha256: 'image-sha256',
  source: 'image_output',
  created_at: '2026-06-22T00:00:01Z',
}

const fileArtifact: WebChatArtifact = {
  ...imageArtifact,
  id: 45,
  filename: 'notes.txt',
  content_type: 'text/plain',
  storage_key: 'generated/notes.txt',
  sha256: 'file-sha256',
  source: 'generated_file',
}

function assistantMessage(artifacts: WebChatArtifact[]): WebChatMessage {
  return {
    id: 101,
    conversation_id: 8,
    user_id: 1,
    role: 'assistant',
    model: 'gpt-image-2',
    provider: 'openai',
    content_text: 'Done.',
    content_json: [],
    status: 'completed',
    artifacts,
    created_at: '2026-06-22T00:00:01Z',
    updated_at: '2026-06-22T00:00:01Z',
  }
}

function processAssistantMessage(status: WebChatMessage['status'] = 'completed'): WebChatMessage {
  const timestamp = status === 'streaming' ? new Date().toISOString() : '2026-06-22T00:00:01Z'
  return {
    ...assistantMessage([]),
    status,
    content_text: status === 'completed' ? 'Final answer.' : '',
    content_json: [
      {
        type: 'reasoning',
        text: 'I need to inspect the current implementation.',
      },
      {
        type: 'tool_call',
        name: 'read_file',
        input: '{"path":"frontend/src/stores/chat.ts"}',
      },
    ],
    created_at: timestamp,
    updated_at: timestamp,
  }
}

function staleStreamingAssistantMessage(): WebChatMessage {
  return {
    id: 102,
    conversation_id: 8,
    user_id: 1,
    role: 'assistant',
    model: 'gpt-image-2',
    provider: 'openai',
    content_text: '',
    content_json: [],
    status: 'streaming',
    created_at: '2026-06-20T00:00:00Z',
    updated_at: '2026-06-20T00:00:00Z',
  }
}

describe('MessageList', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    Object.defineProperty(window.URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:generated-image-preview'),
    })
    Object.defineProperty(window.URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders image artifacts inline using authenticated blob downloads', async () => {
    const imageBlob = new Blob(['image-bytes'], { type: 'image/png' })
    vi.spyOn(chatAPI, 'downloadArtifact').mockResolvedValue({
      blob: imageBlob,
      filename: imageArtifact.filename,
      contentType: imageArtifact.content_type,
    })
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [assistantMessage([imageArtifact, fileArtifact])],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    const preview = wrapper.get('[data-testid="chat-artifact-image-44"]')
    expect(preview.attributes('src')).toBe('blob:generated-image-preview')
    expect(preview.attributes('alt')).toBe('generated-image-1.png')
    expect(wrapper.find('[data-testid="chat-artifact-image-45"]').exists()).toBe(false)
    expect(chatAPI.downloadArtifact).toHaveBeenCalledWith(44)
    expect(window.URL.createObjectURL).toHaveBeenCalledWith(imageBlob)
  })

  it('renders non-image artifacts as in-message downloadable file cards', async () => {
    const textBlob = new Blob(['notes'], { type: 'text/plain' })
    vi.spyOn(chatAPI, 'downloadArtifact').mockResolvedValue({
      blob: textBlob,
      filename: fileArtifact.filename,
      contentType: fileArtifact.content_type,
    })
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [assistantMessage([fileArtifact])],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const fileCard = wrapper.get('[data-testid="chat-artifact-file-45"]')
    expect(fileCard.text()).toContain('notes.txt')
    expect(fileCard.text()).toContain('12 B')

    await wrapper.get('[data-testid="chat-artifact-file-download-45"]').trigger('click')

    expect(chatAPI.downloadArtifact).toHaveBeenCalledWith(45)
  })

  it('renders thinking and tool process blocks collapsed after completion', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [processAssistantMessage('completed')],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const process = wrapper.get('[data-testid="chat-process-block"]')
    expect(process.text()).toContain('Thinking and tools')
    expect(process.attributes('open')).toBeUndefined()
    expect(process.text()).toContain('read_file')
  })

  it('renders assistant markdown and exposes source links as readable anchors', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [{
        ...assistantMessage([]),
        content_text: [
          'Here are the **highlights**:',
          '',
          '- [OpenAI post](https://example.com/openai)',
          '- [DeepMind update](https://deepmind.example/news)',
        ].join('\n'),
      }],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const body = wrapper.get('[data-testid="chat-assistant-markdown"]')
    expect(body.find('strong').text()).toBe('highlights')
    expect(body.findAll('li')).toHaveLength(2)
    expect(body.get('a[href="https://example.com/openai"]').text()).toBe('OpenAI post')
    expect(wrapper.get('[data-testid="chat-source-links"]').text()).toContain('example.com')
  })

  it('keeps process blocks expanded while the assistant is streaming', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [processAssistantMessage('streaming')],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.get('[data-testid="chat-process-block"]').attributes('open')).toBeDefined()
  })

  it('does not show a thinking placeholder once a streaming assistant has text', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [{
        ...processAssistantMessage('streaming'),
        content_text: 'Partial answer',
      }],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Partial answer')
    expect(wrapper.text()).not.toContain('Thinking...')
  })

  it('renders artifacts as separate bubbles outside the assistant text block', async () => {
    const imageBlob = new Blob(['image-bytes'], { type: 'image/png' })
    vi.spyOn(chatAPI, 'downloadArtifact').mockResolvedValue({
      blob: imageBlob,
      filename: imageArtifact.filename,
      contentType: imageArtifact.content_type,
    })
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [assistantMessage([imageArtifact, fileArtifact])],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('[data-testid="chat-artifact-bubble-101"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="chat-assistant-open-message"]').find('[data-testid="chat-artifact-image-44"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="chat-artifact-bubble-101"]').find('[data-testid="chat-artifact-image-44"]').exists()).toBe(true)
  })

  it('formats the assistant model label from historical routing fields', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation: {
        ...conversation,
        default_provider: 'anthropic',
        last_provider: 'anthropic',
        default_model: 'claude-sonnet-4-5-20250929-thinking',
        last_model: 'claude-sonnet-4-5-20250929-thinking',
      },
      messages: [{
        ...assistantMessage([]),
        provider: 'anthropic',
        model: 'claude-sonnet-4-5-20250929-thinking',
      }],
    }

    const wrapper = mount(MessageList, {
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.get('[data-testid="chat-assistant-open-message"]').text()).toContain('Claude Sonnet 4.5')
    expect(wrapper.text()).not.toContain('20250929')
    expect(wrapper.text()).not.toContain('-thinking')
  })

  it('shows stale historical streaming messages as interrupted instead of thinking forever', () => {
    const store = useChatStore()
    store.currentConversation = {
      conversation,
      messages: [staleStreamingAssistantMessage()],
    }

    const wrapper = mount(MessageList, {
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).toContain('interrupted')
    expect(wrapper.text()).not.toContain('Thinking...')
  })
})
