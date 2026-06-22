import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useChatStore } from '@/stores/chat'
import { apiClient } from '@/api/client'
import {
  chatAPI,
  sendChatMessageStream,
  type WebChatAttachment,
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
  price_status: 'confirmed',
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

describe('useChatStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
    sessionStorage.clear()
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
