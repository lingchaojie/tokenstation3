import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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
