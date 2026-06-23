import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import {
  chatAPI,
  type SendWebChatMessageRequest,
  type WebChatAttachment,
  type WebChatConversation,
  type WebChatConversationDetail,
  type WebChatImageGenerationAspectRatio,
  type WebChatImageGenerationBackground,
  type WebChatImageGenerationOutputFormat,
  type WebChatImageGenerationQuality,
  type WebChatImageGenerationSize,
  type WebChatMessage,
  type WebChatMessageStatus,
  type WebChatModel,
  type WebChatThinkingEffort,
} from '@/api/chat'

interface StartAssistantStreamInput {
  conversationId: number
  userMessageId: number | null
  assistantMessageId: number | null
  content: string
  model: string
  provider: string
  attachments?: WebChatAttachment[]
}

const TEMP_ID_START = -1
const DEFAULT_THINKING_EFFORTS: WebChatThinkingEffort[] = ['low', 'medium', 'high', 'xhigh']
const DEFAULT_IMAGE_GENERATION_SIZES: WebChatImageGenerationSize[] = ['1024x1024']
const DEFAULT_IMAGE_GENERATION_ASPECT_RATIOS: WebChatImageGenerationAspectRatio[] = ['1:1']

function nowISO(): string {
  return new Date().toISOString()
}

function makePlaceholderConversation(
  conversationId: number,
  model: string,
  provider: string
): WebChatConversation {
  const timestamp = nowISO()
  return {
    id: conversationId,
    title: '',
    default_model: model,
    default_provider: provider,
    last_model: model,
    last_provider: provider,
    status: 'active',
    message_count: 0,
    created_at: timestamp,
    updated_at: timestamp,
  }
}

function makeMessage(input: {
  id: number
  conversationId: number
  role: 'user' | 'assistant'
  model: string
  provider: string
  content: string
  status: WebChatMessageStatus
  attachments?: WebChatAttachment[]
}): WebChatMessage {
  const timestamp = nowISO()
  return {
    id: input.id,
    conversation_id: input.conversationId,
    user_id: 0,
    role: input.role,
    model: input.model,
    provider: input.provider,
    content_text: input.content,
    content_json: [],
    status: input.status,
    created_at: timestamp,
    updated_at: timestamp,
    attachments: input.attachments,
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function extractStreamDelta(payload: unknown): string {
  const choice = (payload as { choices?: Array<{ delta?: { content?: unknown } }> }).choices?.[0]
  const content = choice?.delta?.content
  return typeof content === 'string' ? content : ''
}

function processSSELine(line: string, onDelta: (delta: string) => void): void {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return

  const data = trimmed.slice(5).trim()
  if (!data || data === '[DONE]') return

  try {
    const delta = extractStreamDelta(JSON.parse(data))
    if (delta) onDelta(delta)
  } catch {
    // Ignore malformed event payloads; the stream may still continue with valid events.
  }
}

async function readSSEStream(response: Response, onDelta: (delta: string) => void): Promise<void> {
  if (!response.body) {
    throw new Error('Chat stream response did not include a readable body')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split(/\r?\n/)
      buffer = lines.pop() ?? ''
      for (const line of lines) {
        processSSELine(line, onDelta)
      }
    }

    buffer += decoder.decode()
    if (buffer) {
      processSSELine(buffer, onDelta)
    }
  } finally {
    reader.releaseLock()
  }
}

export const useChatStore = defineStore('chat', () => {
  const models = ref<WebChatModel[]>([])
  const conversations = ref<WebChatConversation[]>([])
  const currentConversation = ref<WebChatConversationDetail | null>(null)
  const selectedModel = ref<WebChatModel | null>(null)
  const thinkingEnabled = ref(false)
  const thinkingEffort = ref<WebChatThinkingEffort>('medium')
  const imageGenerationEnabled = ref(true)
  const imageGenerationSize = ref<WebChatImageGenerationSize>('1024x1024')
  const imageGenerationAspectRatio = ref<WebChatImageGenerationAspectRatio>('1:1')
  const imageGenerationQuality = ref<WebChatImageGenerationQuality>('medium')
  const imageGenerationOutputFormat = ref<WebChatImageGenerationOutputFormat>('png')
  const imageGenerationBackground = ref<WebChatImageGenerationBackground>('opaque')
  const pendingAttachments = ref<WebChatAttachment[]>([])
  const streaming = ref(false)
  const abortController = ref<AbortController | null>(null)
  const error = ref<string | null>(null)
  const activeUserMessageId = ref<number | null>(null)
  const activeAssistantMessageId = ref<number | null>(null)
  const activeBackendAssistantMessageId = ref<number | null>(null)
  let nextTempId = TEMP_ID_START

  const currentMessages = computed(() => currentConversation.value?.messages ?? [])
  const selectedModelSupportsThinking = computed(() => Boolean(selectedModel.value?.supports_thinking))
  const selectedModelSupportsImageGeneration = computed(() => Boolean(selectedModel.value?.supports_image_generation))
  const thinkingEffortOptions = computed<WebChatThinkingEffort[]>(() => {
    const efforts = selectedModel.value?.thinking_efforts ?? []
    return efforts.length > 0 ? efforts : DEFAULT_THINKING_EFFORTS
  })
  const imageGenerationSizeOptions = computed<WebChatImageGenerationSize[]>(() => {
    const options = selectedModel.value?.image_generation_sizes ?? []
    return options.length > 0 ? options : DEFAULT_IMAGE_GENERATION_SIZES
  })
  const imageGenerationAspectRatioOptions = computed<WebChatImageGenerationAspectRatio[]>(() => {
    const options = selectedModel.value?.image_generation_aspect_ratios ?? []
    return options.length > 0 ? options : DEFAULT_IMAGE_GENERATION_ASPECT_RATIOS
  })
  const imageGenerationQualityOptions = computed<WebChatImageGenerationQuality[]>(() => {
    const options = selectedModel.value?.image_generation_qualities ?? []
    return options.length > 0 ? options : []
  })
  const imageGenerationOutputFormatOptions = computed<WebChatImageGenerationOutputFormat[]>(() => {
    const options = selectedModel.value?.image_generation_output_formats ?? []
    return options.length > 0 ? options : []
  })
  const imageGenerationBackgroundOptions = computed<WebChatImageGenerationBackground[]>(() => {
    const options = selectedModel.value?.image_generation_backgrounds ?? []
    return options.length > 0 ? options : []
  })
  const activeThinkingEffort = computed<WebChatThinkingEffort>(() => {
    if (thinkingEffortOptions.value.includes(thinkingEffort.value)) {
      return thinkingEffort.value
    }
    return thinkingEffortOptions.value.includes('medium') ? 'medium' : thinkingEffortOptions.value[0] ?? 'medium'
  })
  const activeImageGenerationSize = computed<WebChatImageGenerationSize>(() => {
    if (imageGenerationSizeOptions.value.includes(imageGenerationSize.value)) {
      return imageGenerationSize.value
    }
    return imageGenerationSizeOptions.value[0] ?? '1024x1024'
  })
  const activeImageGenerationAspectRatio = computed<WebChatImageGenerationAspectRatio>(() => {
    if (imageGenerationAspectRatioOptions.value.includes(imageGenerationAspectRatio.value)) {
      return imageGenerationAspectRatio.value
    }
    return imageGenerationAspectRatioOptions.value[0] ?? '1:1'
  })
  const activeImageGenerationQuality = computed<WebChatImageGenerationQuality | undefined>(() => {
    if (imageGenerationQualityOptions.value.includes(imageGenerationQuality.value)) {
      return imageGenerationQuality.value
    }
    return imageGenerationQualityOptions.value[0]
  })
  const activeImageGenerationOutputFormat = computed<WebChatImageGenerationOutputFormat | undefined>(() => {
    if (imageGenerationOutputFormatOptions.value.includes(imageGenerationOutputFormat.value)) {
      return imageGenerationOutputFormat.value
    }
    return imageGenerationOutputFormatOptions.value[0]
  })
  const activeImageGenerationBackground = computed<WebChatImageGenerationBackground | undefined>(() => {
    if (imageGenerationBackgroundOptions.value.includes(imageGenerationBackground.value)) {
      return imageGenerationBackground.value
    }
    return imageGenerationBackgroundOptions.value[0]
  })

  const capabilityWarning = computed(() => {
    const model = selectedModel.value
    if (!model || pendingAttachments.value.length === 0) return null

    const unsupported: string[] = []
    if (!model.supports_image_input && pendingAttachments.value.some((attachment) => attachment.kind === 'image')) {
      unsupported.push('image attachments')
    }
    if (!model.supports_file_context && pendingAttachments.value.some((attachment) => attachment.kind === 'file')) {
      unsupported.push('file context')
    }

    if (unsupported.length === 0) return null
    return `Selected model does not support ${unsupported.join(' or ')}.`
  })

  function setError(value: unknown): void {
    error.value = value instanceof Error ? value.message : String(value)
  }

  function upsertConversation(conversation: WebChatConversation): void {
    const index = conversations.value.findIndex((item) => item.id === conversation.id)
    if (index === -1) {
      conversations.value.unshift(conversation)
    } else {
      conversations.value[index] = conversation
    }
  }

  function setSelectedModelFromConversation(conversation: WebChatConversation): void {
    const provider = conversation.last_provider || conversation.default_provider
    const model = conversation.last_model || conversation.default_model
    selectModel(provider, model)
  }

  function selectModel(provider: string, model: string): boolean {
    const match = models.value.find((item) => item.provider === provider && item.model === model)
    if (match) {
      selectedModel.value = match
      return true
    }
    return false
  }

  function reconcileThinkingSettings(): void {
    if (!selectedModelSupportsThinking.value) {
      thinkingEnabled.value = false
      return
    }
    if (!thinkingEffortOptions.value.includes(thinkingEffort.value)) {
      thinkingEffort.value = activeThinkingEffort.value
    }
  }

  function reconcileImageGenerationSettings(): void {
    if (!selectedModelSupportsImageGeneration.value) {
      imageGenerationEnabled.value = false
      return
    }
    imageGenerationEnabled.value = true
    imageGenerationSize.value = activeImageGenerationSize.value
    imageGenerationAspectRatio.value = activeImageGenerationAspectRatio.value
    if (activeImageGenerationQuality.value) {
      imageGenerationQuality.value = activeImageGenerationQuality.value
    }
    if (activeImageGenerationOutputFormat.value) {
      imageGenerationOutputFormat.value = activeImageGenerationOutputFormat.value
    }
    if (activeImageGenerationBackground.value) {
      imageGenerationBackground.value = activeImageGenerationBackground.value
    }
  }

  watch(selectedModel, () => {
    reconcileThinkingSettings()
    reconcileImageGenerationSettings()
  }, { immediate: true, flush: 'sync' })

  async function loadModels(): Promise<WebChatModel[]> {
    try {
      error.value = null
      models.value = await chatAPI.listModels()
      if (!selectedModel.value && models.value.length > 0) {
        selectedModel.value = models.value[0]
      }
      return models.value
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function loadConversations(): Promise<WebChatConversation[]> {
    try {
      error.value = null
      const result = await chatAPI.listConversations()
      conversations.value = result.items ?? []
      return conversations.value
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function openConversation(conversationId: number): Promise<WebChatConversationDetail> {
    try {
      error.value = null
      const detail = await chatAPI.getConversation(conversationId)
      currentConversation.value = detail
      upsertConversation(detail.conversation)
      setSelectedModelFromConversation(detail.conversation)
      return detail
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function createConversation(title = ''): Promise<WebChatConversation> {
    if (!selectedModel.value) {
      throw new Error('Select a model before creating a conversation')
    }

    try {
      error.value = null
      const conversation = await chatAPI.createConversation({
        title,
        model: selectedModel.value.model,
        provider: selectedModel.value.provider,
      })
      upsertConversation(conversation)
      currentConversation.value = { conversation, messages: [] }
      return conversation
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function renameConversation(conversationId: number, title: string): Promise<WebChatConversation> {
    try {
      error.value = null
      const conversation = await chatAPI.updateConversation(conversationId, { title })
      upsertConversation(conversation)
      if (currentConversation.value?.conversation.id === conversation.id) {
        currentConversation.value.conversation = conversation
      }
      return conversation
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function deleteConversation(conversationId: number): Promise<void> {
    try {
      error.value = null
      await chatAPI.deleteConversation(conversationId)
      conversations.value = conversations.value.filter((conversation) => conversation.id !== conversationId)
      if (currentConversation.value?.conversation.id === conversationId) {
        currentConversation.value = null
      }
    } catch (err) {
      setError(err)
      throw err
    }
  }

  async function uploadAttachment(file: File): Promise<WebChatAttachment> {
    try {
      error.value = null
      const attachment = await chatAPI.uploadAttachment(file)
      pendingAttachments.value.push(attachment)
      return attachment
    } catch (err) {
      setError(err)
      throw err
    }
  }

  function startAssistantStream(input: StartAssistantStreamInput): void {
    const userMessageId = input.userMessageId ?? nextTempId--
    const assistantMessageId = input.assistantMessageId ?? nextTempId--
    activeUserMessageId.value = userMessageId
    activeAssistantMessageId.value = assistantMessageId
    activeBackendAssistantMessageId.value =
      input.assistantMessageId && input.assistantMessageId > 0 ? input.assistantMessageId : null

    if (!currentConversation.value || currentConversation.value.conversation.id !== input.conversationId) {
      currentConversation.value = {
        conversation: makePlaceholderConversation(input.conversationId, input.model, input.provider),
        messages: [],
      }
    }

    const messages = currentConversation.value.messages
    if (!messages.some((message) => message.id === userMessageId)) {
      messages.push(makeMessage({
        id: userMessageId,
        conversationId: input.conversationId,
        role: 'user',
        model: input.model,
        provider: input.provider,
        content: input.content,
        status: 'completed',
        attachments: input.attachments,
      }))
    }

    if (!messages.some((message) => message.id === assistantMessageId)) {
      messages.push(makeMessage({
        id: assistantMessageId,
        conversationId: input.conversationId,
        role: 'assistant',
        model: input.model,
        provider: input.provider,
        content: '',
        status: 'streaming',
      }))
    }

    streaming.value = true
  }

  function appendAssistantDelta(delta: string): void {
    if (!delta || !currentConversation.value) return

    const assistantMessage = [...currentConversation.value.messages]
      .reverse()
      .find((message) => {
        if (activeAssistantMessageId.value !== null) {
          return message.id === activeAssistantMessageId.value
        }
        return message.role === 'assistant' && message.status === 'streaming'
      })

    if (!assistantMessage) return
    assistantMessage.content_text += delta
    assistantMessage.updated_at = nowISO()
  }

  function finishAssistantStream(status: WebChatMessageStatus = 'completed'): void {
    if (currentConversation.value && activeAssistantMessageId.value !== null) {
      const assistantMessage = currentConversation.value.messages.find(
        (message) => message.id === activeAssistantMessageId.value
      )
      if (assistantMessage) {
        assistantMessage.status = status
        assistantMessage.updated_at = nowISO()
      }
    }

    streaming.value = false
    abortController.value = null
    activeUserMessageId.value = null
    activeAssistantMessageId.value = null
    activeBackendAssistantMessageId.value = null
  }

  async function sendMessage(content: string): Promise<void> {
    if (streaming.value) {
      throw new Error('A chat response is already streaming')
    }

    const text = content.trim()
    if (!text && pendingAttachments.value.length === 0) {
      throw new Error('Message content or attachment is required')
    }
    if (!selectedModel.value) {
      throw new Error('Select a model before sending a message')
    }
    if (capabilityWarning.value) {
      throw new Error(capabilityWarning.value)
    }

    const conversation = currentConversation.value?.conversation ?? await createConversation(text.slice(0, 80))
    const attachments = [...pendingAttachments.value]
    const request: SendWebChatMessageRequest = {
      model: selectedModel.value.model,
      provider: selectedModel.value.provider,
      content: text,
      attachment_ids: attachments.map((attachment) => attachment.id),
      stream: true,
    }
    if (selectedModelSupportsThinking.value && thinkingEnabled.value) {
      request.thinking = {
        enabled: true,
        effort: activeThinkingEffort.value,
      }
    }
    if (selectedModelSupportsImageGeneration.value && imageGenerationEnabled.value) {
      const imageGeneration: NonNullable<SendWebChatMessageRequest['image_generation']> = {
        enabled: true,
        size: activeImageGenerationSize.value,
        aspect_ratio: activeImageGenerationAspectRatio.value,
      }
      if (activeImageGenerationQuality.value) {
        imageGeneration.quality = activeImageGenerationQuality.value
      }
      if (activeImageGenerationOutputFormat.value) {
        imageGeneration.output_format = activeImageGenerationOutputFormat.value
      }
      if (activeImageGenerationBackground.value) {
        imageGeneration.background = activeImageGenerationBackground.value
      }
      request.image_generation = imageGeneration
    }

    const controller = new AbortController()
    abortController.value = controller
    streaming.value = true
    error.value = null

    try {
      const streamResult = await chatAPI.sendMessageStream(conversation.id, request, controller.signal)
      startAssistantStream({
        conversationId: conversation.id,
        userMessageId: streamResult.userMessageId,
        assistantMessageId: streamResult.assistantMessageId,
        content: text,
        model: selectedModel.value.model,
        provider: selectedModel.value.provider,
        attachments,
      })
      pendingAttachments.value = []
      await readSSEStream(streamResult.response, appendAssistantDelta)
      finishAssistantStream()
    } catch (err) {
      if (isAbortError(err) || controller.signal.aborted) {
        finishAssistantStream('canceled')
        return
      }
      finishAssistantStream('failed')
      setError(err)
      throw err
    }
  }

  async function cancelStream(): Promise<void> {
    const conversationId = currentConversation.value?.conversation.id
    const assistantMessageId = activeBackendAssistantMessageId.value
    abortController.value?.abort()

    if (conversationId && assistantMessageId && assistantMessageId > 0) {
      try {
        await chatAPI.cancelMessage(conversationId, assistantMessageId)
      } catch {
        // The abort path should remain responsive even if the server already ended the stream.
      }
    }

    finishAssistantStream('canceled')
  }

  return {
    models,
    conversations,
    currentConversation,
    selectedModel,
    thinkingEnabled,
    thinkingEffort,
    imageGenerationEnabled,
    imageGenerationSize,
    imageGenerationAspectRatio,
    imageGenerationQuality,
    imageGenerationOutputFormat,
    imageGenerationBackground,
    pendingAttachments,
    streaming,
    abortController,
    error,
    currentMessages,
    selectedModelSupportsThinking,
    thinkingEffortOptions,
    selectedModelSupportsImageGeneration,
    imageGenerationSizeOptions,
    imageGenerationAspectRatioOptions,
    imageGenerationQualityOptions,
    imageGenerationOutputFormatOptions,
    imageGenerationBackgroundOptions,
    capabilityWarning,
    loadModels,
    loadConversations,
    selectModel,
    openConversation,
    createConversation,
    renameConversation,
    deleteConversation,
    uploadAttachment,
    sendMessage,
    cancelStream,
    startAssistantStream,
    appendAssistantDelta,
    finishAssistantStream,
  }
})
