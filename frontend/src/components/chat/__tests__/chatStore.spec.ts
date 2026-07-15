import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useChatStore } from '@/stores/chat'
import { apiClient } from '@/api/client'
import {
  chatAPI,
  sendChatMessageStream,
  type WebChatStreamSendResult,
  type WebChatAttachment,
  type WebChatConversationDetail,
  type WebChatImageGenerationAspectRatio,
  type WebChatImageGenerationBackground,
  type WebChatImageGenerationOutputFormat,
  type WebChatImageGenerationQuality,
  type WebChatImageGenerationSize,
  type WebChatModel,
} from '@/api/chat'

const textOnlyModel: WebChatModel = {
  provider: 'openai',
  platform: 'openai',
  key_type: 'openai',
  model: 'gpt-text',
  display_name: 'GPT Text',
  supports_text: true,
  supports_image_input: false,
  supports_file_context: false,
  supports_artifact_output: false,
  supports_thinking: false,
  thinking_efforts: [],
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
  ...textOnlyModel,
  provider: 'anthropic',
  platform: 'anthropic',
  key_type: 'anthropic',
  model: 'claude-haiku-4-5-20251001',
  display_name: 'claude-haiku-4-5',
}

const thinkingModel: WebChatModel = {
  ...textOnlyModel,
  model: 'gpt-reasoning',
  display_name: 'GPT Reasoning',
  supports_thinking: true,
  thinking_efforts: ['low', 'medium', 'high', 'xhigh'],
}

const imageModel: WebChatModel = {
  ...textOnlyModel,
  model: 'gpt-image-2',
  display_name: 'GPT Image 2',
  supports_artifact_output: true,
  supports_image_generation: true,
  image_generation_sizes: ['1024x1024', '1536x1024'] as WebChatImageGenerationSize[],
  image_generation_aspect_ratios: ['1:1', '3:2'] as WebChatImageGenerationAspectRatio[],
  image_generation_qualities: ['low', 'medium', 'high'] as WebChatImageGenerationQuality[],
  image_generation_output_formats: ['png', 'webp'] as WebChatImageGenerationOutputFormat[],
  image_generation_backgrounds: ['opaque', 'auto'] as WebChatImageGenerationBackground[],
}

const webSearchModel: WebChatModel = {
  ...textOnlyModel,
  model: 'gpt-5.5',
  display_name: 'GPT 5.5',
  supports_web_search: true,
}

const imageAttachment: WebChatAttachment = {
  id: 10,
  user_id: 1,
  kind: 'image',
  filename: 'diagram.png',
  content_type: 'image/png',
  size_bytes: 2048,
  storage_key: 'web-chat/diagram.png',
  sha256: 'hash',
  status: 'uploaded',
  created_at: '2026-06-22T00:00:00Z',
}

const fileAttachment: WebChatAttachment = {
  ...imageAttachment,
  id: 11,
  kind: 'file',
  filename: 'notes.txt',
  content_type: 'text/plain',
  storage_key: 'web-chat/notes.txt',
}

function emptyWebSearchConversation(model: WebChatModel): WebChatConversationDetail {
  return {
    conversation: {
      id: 7,
      title: 'Search',
      default_model: model.model,
      default_provider: model.provider,
      last_model: model.model,
      last_provider: model.provider,
      status: 'active',
      message_count: 0,
      created_at: '2026-06-22T00:00:00Z',
      updated_at: '2026-06-22T00:00:00Z',
    },
    messages: [],
  }
}

function mockCompletedWebChatStream() {
  return vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
    response: new Response('data: [DONE]\n\n', {
      status: 200,
      headers: {
        'X-Web-Chat-User-Message-ID': '100',
        'X-Web-Chat-Assistant-Message-ID': '101',
      },
    }),
    userMessageId: 100,
    assistantMessageId: 101,
  })
}

describe('useChatStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('resolves known WebChat models to human-readable names without mutating routing ids', () => {
    const store = useChatStore()
    store.models = [datedHaikuModel]

    expect(store.getModelDisplayName(
      datedHaikuModel.provider,
      datedHaikuModel.model,
      datedHaikuModel.display_name,
    )).toBe('Claude Haiku 4.5')
    expect(store.models[0].model).toBe('claude-haiku-4-5-20251001')
  })

  it('prefers a provider-exact catalog display name for unknown model families', () => {
    const openAIShared: WebChatModel = {
      ...textOnlyModel,
      model: 'shared-model',
      display_name: 'OpenAI Shared',
    }
    const anthropicShared: WebChatModel = {
      ...textOnlyModel,
      provider: 'anthropic',
      platform: 'anthropic',
      key_type: 'anthropic',
      model: 'shared-model',
      display_name: 'Anthropic Shared',
    }
    const store = useChatStore()
    store.models = [openAIShared, anthropicShared]

    expect(store.getModelDisplayName('anthropic', 'shared-model')).toBe('Anthropic Shared')
    expect(store.getModelDisplayName('missing-provider', 'shared-model')).toBe('OpenAI Shared')
    expect(store.getModelDisplayName('vendor', 'historical-model', 'Historical Prime')).toBe('Historical Prime')
  })

  it('appends streamed assistant chunks without replacing prior text', () => {
    const store = useChatStore()

    store.startAssistantStream({
      conversationId: 1,
      userMessageId: 100,
      assistantMessageId: 101,
      content: 'Explain streaming',
      model: 'gpt-text',
      provider: 'openai',
    })

    store.appendAssistantDelta('Hello')
    store.appendAssistantDelta(', ')
    store.appendAssistantDelta('world')
    store.finishAssistantStream()

    expect(store.currentMessages).toHaveLength(2)
    expect(store.currentMessages[1]).toMatchObject({
      id: 101,
      role: 'assistant',
      status: 'completed',
      content_text: 'Hello, world',
    })
  })

  it('detects unsupported attachments for selected model', () => {
    const store = useChatStore()

    store.selectedModel = textOnlyModel
    store.pendingAttachments = [imageAttachment, fileAttachment]

    expect(store.capabilityWarning).toContain('image')
    expect(store.capabilityWarning).toContain('file')
  })

  it('does not call backend cancel for temporary assistant message ids', async () => {
    const cancelSpy = vi.spyOn(chatAPI, 'cancelMessage').mockResolvedValue()
    const store = useChatStore()

    store.startAssistantStream({
      conversationId: 1,
      userMessageId: null,
      assistantMessageId: null,
      content: 'Explain streaming',
      model: 'gpt-text',
      provider: 'openai',
    })

    await store.cancelStream()

    expect(cancelSpy).not.toHaveBeenCalled()
    expect(store.currentMessages[1].id).toBeLessThan(0)
    expect(store.currentMessages[1].status).toBe('canceled')
  })

  it('resumes a fresh historical assistant stream so it can be stopped', async () => {
    vi.spyOn(chatAPI, 'getConversation').mockResolvedValue({
      conversation: {
        id: 8,
        title: 'Chat',
        default_model: textOnlyModel.model,
        default_provider: textOnlyModel.provider,
        last_model: textOnlyModel.model,
        last_provider: textOnlyModel.provider,
        status: 'active',
        message_count: 2,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: new Date().toISOString(),
      },
      messages: [
        {
          id: 100,
          conversation_id: 8,
          user_id: 1,
          role: 'user',
          model: textOnlyModel.model,
          provider: textOnlyModel.provider,
          content_text: 'Hello',
          content_json: [],
          status: 'completed',
          created_at: '2026-06-22T00:00:00Z',
          updated_at: '2026-06-22T00:00:00Z',
        },
        {
          id: 101,
          conversation_id: 8,
          user_id: 1,
          role: 'assistant',
          model: textOnlyModel.model,
          provider: textOnlyModel.provider,
          content_text: '',
          content_json: [],
          status: 'streaming',
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        },
      ],
    })
    const cancelSpy = vi.spyOn(chatAPI, 'cancelMessage').mockResolvedValue()
    const store = useChatStore()

    await store.openConversation(8)
    await store.cancelStream()

    expect(store.streaming).toBe(false)
    expect(cancelSpy).toHaveBeenCalledWith(8, 101)
    expect(store.currentMessages[1].status).toBe('canceled')
  })

  it('polls a resumed historical stream until the final assistant message is available', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-25T06:00:00Z'))
    const streamingDetail = {
      conversation: {
        id: 8,
        title: 'Chat',
        default_model: textOnlyModel.model,
        default_provider: textOnlyModel.provider,
        last_model: textOnlyModel.model,
        last_provider: textOnlyModel.provider,
        status: 'active' as const,
        message_count: 2,
        created_at: '2026-06-25T05:59:00Z',
        updated_at: '2026-06-25T05:59:10Z',
      },
      messages: [
        {
          id: 100,
          conversation_id: 8,
          user_id: 1,
          role: 'user' as const,
          model: textOnlyModel.model,
          provider: textOnlyModel.provider,
          content_text: 'Hello',
          content_json: [],
          status: 'completed' as const,
          created_at: '2026-06-25T05:59:00Z',
          updated_at: '2026-06-25T05:59:00Z',
        },
        {
          id: 101,
          conversation_id: 8,
          user_id: 1,
          role: 'assistant' as const,
          model: textOnlyModel.model,
          provider: textOnlyModel.provider,
          content_text: '',
          content_json: [],
          status: 'streaming' as const,
          created_at: '2026-06-25T05:59:10Z',
          updated_at: '2026-06-25T05:59:10Z',
        },
      ],
    }
    const completedDetail = {
      ...streamingDetail,
      conversation: {
        ...streamingDetail.conversation,
        updated_at: '2026-06-25T06:00:02Z',
      },
      messages: [
        streamingDetail.messages[0],
        {
          ...streamingDetail.messages[1],
          content_text: 'Done.',
          status: 'completed' as const,
          updated_at: '2026-06-25T06:00:02Z',
        },
      ],
    }
    const getConversationSpy = vi.spyOn(chatAPI, 'getConversation')
      .mockResolvedValueOnce(streamingDetail)
      .mockResolvedValueOnce(completedDetail)
    const store = useChatStore()

    await store.openConversation(8)
    expect(store.streaming).toBe(true)

    await vi.advanceTimersByTimeAsync(3000)

    expect(getConversationSpy).toHaveBeenCalledTimes(2)
    expect(store.streaming).toBe(false)
    expect(store.currentMessages[1]).toMatchObject({
      status: 'completed',
      content_text: 'Done.',
    })
  })

  it('rejects successful stream responses without a readable body', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      })
    )

    await expect(sendChatMessageStream(1, {
      model: 'gpt-text',
      provider: 'openai',
      content: 'Hello',
    })).rejects.toThrow('readable body')
  })

  it('refreshes auth once and retries stream sends after a 401', async () => {
    localStorage.setItem('auth_token', 'old-token')
    localStorage.setItem('refresh_token', 'old-refresh')

    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 401, message: 'expired' }), { status: 401 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        code: 0,
        data: {
          access_token: 'new-token',
          refresh_token: 'new-refresh',
          expires_in: 60,
        },
      }), { status: 200 }))
      .mockResolvedValueOnce(new Response('data: [DONE]\n\n', {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }))

    const result = await sendChatMessageStream(1, {
      model: 'gpt-text',
      provider: 'openai',
      content: 'Hello',
    })

    expect(result.userMessageId).toBe(100)
    expect(result.assistantMessageId).toBe(101)
    expect(localStorage.getItem('auth_token')).toBe('new-token')
    expect(localStorage.getItem('refresh_token')).toBe('new-refresh')
    expect(Number(localStorage.getItem('token_expires_at'))).toBeGreaterThan(Date.now())
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/auth/refresh')
    expect(fetchMock.mock.calls[2][1]).toMatchObject({
      headers: expect.objectContaining({
        Authorization: 'Bearer new-token',
      }),
    })
  })

  it('sends the highest thinking effort when deep thinking is enabled', async () => {
    const streamSpy = vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
      response: new Response('data: [DONE]\n\n', {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }),
      userMessageId: 100,
      assistantMessageId: 101,
    })
    const store = useChatStore()
    store.selectedModel = thinkingModel
    store.thinkingEnabled = true
    store.thinkingEffort = 'low'
    store.currentConversation = {
      conversation: {
        id: 7,
        title: 'Reasoning',
        default_model: thinkingModel.model,
        default_provider: thinkingModel.provider,
        last_model: thinkingModel.model,
        last_provider: thinkingModel.provider,
        status: 'active',
        message_count: 0,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:00Z',
      },
      messages: [],
    }

    await store.sendMessage('Think this through')

    expect(streamSpy).toHaveBeenCalledWith(7, expect.objectContaining({
      model: 'gpt-reasoning',
      provider: 'openai',
      content: 'Think this through',
      thinking: {
        enabled: true,
        effort: 'xhigh',
      },
    }), expect.any(AbortSignal))
  })

  it('never sends a frontend search config for OpenAI', async () => {
    const streamSpy = mockCompletedWebChatStream()
    const store = useChatStore()
    store.selectedModel = webSearchModel
    store.currentConversation = emptyWebSearchConversation(webSearchModel)
    store.webSearchEnabled = true

    await store.sendMessage('Use server-managed search')

    expect(streamSpy.mock.calls[0][1]).not.toHaveProperty('web_search')
  })

  it('keeps configurable search for a non-OpenAI model', async () => {
    const model: WebChatModel = {
      ...webSearchModel,
      provider: 'anthropic',
      platform: 'anthropic',
      key_type: 'anthropic',
      model: 'claude-sonnet-4',
      display_name: 'Claude Sonnet 4',
    }
    const streamSpy = mockCompletedWebChatStream()
    const store = useChatStore()
    store.selectedModel = model
    store.currentConversation = emptyWebSearchConversation(model)
    store.webSearchEnabled = true

    await store.sendMessage('Search with Claude')

    expect(streamSpy.mock.calls[0][1]).toMatchObject({
      model: 'claude-sonnet-4',
      provider: 'anthropic',
      web_search: { enabled: true },
    })
  })

  it('streams reasoning and tool call events into assistant process blocks', async () => {
    vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
      response: new Response([
        'data: {"choices":[{"delta":{"reasoning_content":"I should inspect the repo. "}}]}\n\n',
        'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\\"path\\":"}}]}}]}\n\n',
        'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\\"README.md\\"}"}}]}}]}\n\n',
        'data: {"choices":[{"delta":{"content":"Done"}}]}\n\n',
        'data: [DONE]\n\n',
      ].join(''), {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }),
      userMessageId: 100,
      assistantMessageId: 101,
    })
    const store = useChatStore()
    store.selectedModel = thinkingModel
    store.currentConversation = {
      conversation: {
        id: 7,
        title: 'Reasoning',
        default_model: thinkingModel.model,
        default_provider: thinkingModel.provider,
        last_model: thinkingModel.model,
        last_provider: thinkingModel.provider,
        status: 'active',
        message_count: 0,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:00Z',
      },
      messages: [],
    }

    await store.sendMessage('Use a tool')

    expect(store.currentMessages[1].content_text).toBe('Done')
    expect(store.currentMessages[1].content_json).toEqual([
      {
        type: 'reasoning',
        text: 'I should inspect the repo. ',
      },
      {
        type: 'tool_call',
        id: 'call_1',
        index: 0,
        name: 'read_file',
        input: '{"path":"README.md"}',
      },
    ])
  })

  it('shows optimistic user and assistant messages before the stream request resolves', async () => {
    let resolveStream!: (value: WebChatStreamSendResult) => void
    const pendingStream = new Promise<WebChatStreamSendResult>((resolve) => {
      resolveStream = resolve
    })
    vi.spyOn(chatAPI, 'sendMessageStream').mockReturnValue(pendingStream)
    const store = useChatStore()
    store.selectedModel = textOnlyModel
    store.currentConversation = {
      conversation: {
        id: 7,
        title: 'Chat',
        default_model: textOnlyModel.model,
        default_provider: textOnlyModel.provider,
        last_model: textOnlyModel.model,
        last_provider: textOnlyModel.provider,
        status: 'active',
        message_count: 0,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:00Z',
      },
      messages: [],
    }

    const sendPromise = store.sendMessage('Hello without lag')
    await Promise.resolve()

    expect(store.currentMessages).toHaveLength(2)
    expect(store.currentMessages[0]).toMatchObject({
      role: 'user',
      content_text: 'Hello without lag',
      status: 'completed',
    })
    expect(store.currentMessages[1]).toMatchObject({
      role: 'assistant',
      status: 'streaming',
      content_text: '',
    })

    resolveStream({
      response: new Response('data: [DONE]\n\n', {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }),
      userMessageId: 100,
      assistantMessageId: 101,
    })
    await sendPromise

    expect(store.currentMessages[0].id).toBe(100)
    expect(store.currentMessages[1].id).toBe(101)
    expect(store.currentMessages[1].status).toBe('completed')
  })

  it('includes editable image generation settings when sending a supported model message', async () => {
    const streamSpy = vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
      response: new Response('data: [DONE]\n\n', {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }),
      userMessageId: 100,
      assistantMessageId: 101,
    })
    vi.spyOn(chatAPI, 'getConversation').mockResolvedValue({
      conversation: {
        id: 8,
        title: 'Image',
        default_model: imageModel.model,
        default_provider: imageModel.provider,
        last_model: imageModel.model,
        last_provider: imageModel.provider,
        status: 'active',
        message_count: 2,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:01Z',
      },
      messages: [],
    })
    const store = useChatStore()
    store.selectedModel = imageModel
    store.imageGenerationEnabled = true
    store.imageGenerationSize = '1536x1024'
    store.imageGenerationAspectRatio = '3:2'
    store.imageGenerationQuality = 'high'
    store.imageGenerationOutputFormat = 'webp'
    store.imageGenerationBackground = 'opaque'
    store.currentConversation = {
      conversation: {
        id: 8,
        title: 'Image',
        default_model: imageModel.model,
        default_provider: imageModel.provider,
        last_model: imageModel.model,
        last_provider: imageModel.provider,
        status: 'active',
        message_count: 0,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:00Z',
      },
      messages: [],
    }

    await store.sendMessage('Generate a wide hero image')

    expect(streamSpy).toHaveBeenCalledWith(8, expect.objectContaining({
      model: 'gpt-image-2',
      provider: 'openai',
      content: 'Generate a wide hero image',
      image_generation: {
        enabled: true,
        size: '1536x1024',
        aspect_ratio: '3:2',
        quality: 'high',
        output_format: 'webp',
        background: 'opaque',
      },
    }), expect.any(AbortSignal))
  })

  it('refreshes artifact-capable conversations after streaming completes', async () => {
    vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
      response: new Response('data: [DONE]\n\n', {
        status: 200,
        headers: {
          'X-Web-Chat-User-Message-ID': '100',
          'X-Web-Chat-Assistant-Message-ID': '101',
        },
      }),
      userMessageId: 100,
      assistantMessageId: 101,
    })
    const getConversationSpy = vi.spyOn(chatAPI, 'getConversation').mockResolvedValue({
      conversation: {
        id: 8,
        title: 'Image',
        default_model: imageModel.model,
        default_provider: imageModel.provider,
        last_model: imageModel.model,
        last_provider: imageModel.provider,
        status: 'active',
        message_count: 2,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:01Z',
      },
      messages: [
        {
          id: 100,
          conversation_id: 8,
          user_id: 1,
          role: 'user',
          model: imageModel.model,
          provider: imageModel.provider,
          content_text: 'Generate image',
          content_json: [],
          status: 'completed',
          created_at: '2026-06-22T00:00:00Z',
          updated_at: '2026-06-22T00:00:00Z',
        },
        {
          id: 101,
          conversation_id: 8,
          user_id: 1,
          role: 'assistant',
          model: imageModel.model,
          provider: imageModel.provider,
          content_text: 'Done.',
          content_json: [],
          status: 'completed',
          artifacts: [{
            id: 44,
            message_id: 101,
            conversation_id: 8,
            user_id: 1,
            filename: 'generated-image-1.webp',
            content_type: 'image/webp',
            size_bytes: 5,
            storage_key: 'generated/generated-image-1.webp',
            sha256: 'sha256',
            source: 'image_output',
            created_at: '2026-06-22T00:00:01Z',
          }],
          created_at: '2026-06-22T00:00:01Z',
          updated_at: '2026-06-22T00:00:01Z',
        },
      ],
    })
    const store = useChatStore()
    store.selectedModel = imageModel
    store.currentConversation = {
      conversation: {
        id: 8,
        title: 'Image',
        default_model: imageModel.model,
        default_provider: imageModel.provider,
        last_model: imageModel.model,
        last_provider: imageModel.provider,
        status: 'active',
        message_count: 0,
        created_at: '2026-06-22T00:00:00Z',
        updated_at: '2026-06-22T00:00:00Z',
      },
      messages: [],
    }

    await store.sendMessage('Generate image')

    expect(getConversationSpy).toHaveBeenCalledWith(8)
    expect(store.currentMessages[1].artifacts?.[0]).toMatchObject({
      id: 44,
      filename: 'generated-image-1.webp',
    })
  })

  it('downloads attachments as authenticated blobs with response metadata', async () => {
    const blob = new Blob(['hello'], { type: 'text/plain' })
    vi.spyOn(apiClient, 'get').mockResolvedValue({
      data: blob,
      headers: {
        'content-type': 'text/plain',
        'content-disposition': 'attachment; filename="notes.txt"',
      },
    } as never)

    const result = await chatAPI.downloadAttachment(42)

    expect(result).toEqual({
      blob,
      filename: 'notes.txt',
      contentType: 'text/plain',
    })
    expect(apiClient.get).toHaveBeenCalledWith('/chat/attachments/42/download', {
      responseType: 'blob',
    })
  })
})
