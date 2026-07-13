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
import { formatWebChatModelName } from '@/utils/webChatModelName'

interface StartAssistantStreamInput {
  conversationId: number
  userMessageId: number | null
  assistantMessageId: number | null
  content: string
  model: string
  provider: string
  attachments?: WebChatAttachment[]
}

interface StartAssistantStreamResult {
  userMessageId: number
  assistantMessageId: number
}

interface StreamToolCallDelta {
  id?: string
  index?: number
  name?: string
  input?: string
  inputMode?: 'append' | 'replace'
}

interface StreamPayloadDelta {
  content?: string
  reasoning?: string
  toolCalls?: StreamToolCallDelta[]
}

const TEMP_ID_START = -1
const DEFAULT_THINKING_EFFORTS: WebChatThinkingEffort[] = ['low', 'medium', 'high', 'xhigh']
const DEFAULT_IMAGE_GENERATION_SIZES: WebChatImageGenerationSize[] = ['1024x1024']
const DEFAULT_IMAGE_GENERATION_ASPECT_RATIOS: WebChatImageGenerationAspectRatio[] = ['1:1']
const STREAM_RENDER_CHARS_PER_FRAME = 8
const STREAM_RENDER_SMOOTHING_THRESHOLD = 24
const HISTORICAL_STREAMING_STALE_MS = 10 * 60 * 1000
const HISTORICAL_STREAM_POLL_INTERVAL_MS = 3000
const THINKING_EFFORT_ORDER: WebChatThinkingEffort[] = ['low', 'medium', 'high', 'xhigh']

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

function highestThinkingEffort(options: WebChatThinkingEffort[]): WebChatThinkingEffort {
  for (let index = THINKING_EFFORT_ORDER.length - 1; index >= 0; index -= 1) {
    const effort = THINKING_EFFORT_ORDER[index]
    if (options.includes(effort)) return effort
  }
  return options[0] ?? 'medium'
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function textValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function jsonTextValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === null || value === undefined) return ''
  try {
    return JSON.stringify(value)
  } catch {
    return ''
  }
}

function firstTextValue(...values: unknown[]): string {
  for (const value of values) {
    const text = textValue(value)
    if (text) return text
  }
  return ''
}

function extractChatCompletionsDelta(payload: Record<string, unknown>): StreamPayloadDelta {
  const choice = (payload as { choices?: Array<{ delta?: Record<string, unknown> }> }).choices?.[0]
  const delta = choice?.delta
  if (!delta) return {}

  const result: StreamPayloadDelta = {
    content: textValue(delta.content),
    reasoning: firstTextValue(
      delta.reasoning_content,
      delta.reasoning,
      delta.reasoning_summary,
      delta.reasoning_text,
      delta.thinking,
    ),
  }

  const toolCalls: StreamToolCallDelta[] = []
  const rawToolCalls = Array.isArray(delta.tool_calls) ? delta.tool_calls : []
  for (const item of rawToolCalls) {
    if (!item || typeof item !== 'object') continue
    const call = item as Record<string, unknown>
    const fn = call.function && typeof call.function === 'object' ? call.function as Record<string, unknown> : {}
    const input = firstTextValue(fn.arguments, call.arguments, call.input) || jsonTextValue(call.input)
    toolCalls.push({
      id: textValue(call.id),
      index: typeof call.index === 'number' ? call.index : undefined,
      name: firstTextValue(fn.name, call.name),
      input,
    })
  }

  const functionCall = delta.function_call
  if (functionCall && typeof functionCall === 'object') {
    const call = functionCall as Record<string, unknown>
    toolCalls.push({
      name: textValue(call.name),
      input: textValue(call.arguments),
    })
  }

  if (toolCalls.length > 0) {
    result.toolCalls = toolCalls
  }

  return result
}

function extractResponsesDelta(payload: Record<string, unknown>): StreamPayloadDelta {
  const type = textValue(payload.type)
  switch (type) {
    case 'response.output_text.delta':
      return { content: textValue(payload.delta) }
    case 'response.reasoning_summary_text.delta':
    case 'response.reasoning_text.delta':
      return { reasoning: textValue(payload.delta) }
    case 'response.output_item.added':
    case 'response.output_item.done': {
      const item = payload.item && typeof payload.item === 'object' ? payload.item as Record<string, unknown> : {}
      const itemType = textValue(item.type)
      if (itemType !== 'function_call' && itemType !== 'tool_call' && !itemType.endsWith('_call')) return {}
      const input = firstTextValue(item.arguments, item.query, item.input) || jsonTextValue(item.action)
      return {
        toolCalls: [{
          id: firstTextValue(item.call_id, item.id),
          name: firstTextValue(item.name, item.type),
          input,
          inputMode: input ? 'replace' : undefined,
        }],
      }
    }
    case 'response.function_call_arguments.delta':
      return {
        toolCalls: [{
          id: firstTextValue(payload.call_id, payload.item_id),
          input: textValue(payload.delta),
        }],
      }
    default:
      return {}
  }
}

function extractAnthropicDelta(payload: Record<string, unknown>): StreamPayloadDelta {
  const type = textValue(payload.type)
  if (type === 'content_block_start') {
    const block = payload.content_block && typeof payload.content_block === 'object'
      ? payload.content_block as Record<string, unknown>
      : {}
    if (textValue(block.type) !== 'tool_use') return {}
    return {
      toolCalls: [{
        id: textValue(block.id),
        index: typeof payload.index === 'number' ? payload.index : undefined,
        name: textValue(block.name),
        input: jsonTextValue(block.input),
        inputMode: 'replace',
      }],
    }
  }

  if (type !== 'content_block_delta') return {}
  const delta = payload.delta && typeof payload.delta === 'object'
    ? payload.delta as Record<string, unknown>
    : {}
  const deltaType = textValue(delta.type)
  if (deltaType === 'text_delta') {
    return { content: textValue(delta.text) }
  }
  if (deltaType === 'thinking_delta') {
    return { reasoning: textValue(delta.thinking) }
  }
  if (deltaType === 'input_json_delta') {
    return {
      toolCalls: [{
        index: typeof payload.index === 'number' ? payload.index : undefined,
        input: textValue(delta.partial_json),
      }],
    }
  }
  return {}
}

function extractStreamDelta(payload: unknown): StreamPayloadDelta {
  if (!payload || typeof payload !== 'object') return {}
  const record = payload as Record<string, unknown>
  const chatCompletionsDelta = extractChatCompletionsDelta(record)
  if (chatCompletionsDelta.content || chatCompletionsDelta.reasoning || chatCompletionsDelta.toolCalls?.length) {
    return chatCompletionsDelta
  }

  const responsesDelta = extractResponsesDelta(record)
  if (responsesDelta.content || responsesDelta.reasoning || responsesDelta.toolCalls?.length) {
    return responsesDelta
  }

  return extractAnthropicDelta(record)
}

function waitForRenderFrame(): Promise<void> {
  return new Promise((resolve) => {
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
      window.requestAnimationFrame(() => resolve())
      return
    }
    setTimeout(resolve, 16)
  })
}

async function emitStreamDelta(delta: string, onDelta: (delta: string) => void): Promise<void> {
  if (delta.length <= STREAM_RENDER_SMOOTHING_THRESHOLD) {
    onDelta(delta)
    return
  }

  for (let offset = 0; offset < delta.length; offset += STREAM_RENDER_CHARS_PER_FRAME) {
    onDelta(delta.slice(offset, offset + STREAM_RENDER_CHARS_PER_FRAME))
    await waitForRenderFrame()
  }
}

async function processSSELine(line: string, onDelta: (delta: string) => void, onProcessDelta: (delta: StreamPayloadDelta) => void): Promise<void> {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return

  const data = trimmed.slice(5).trim()
  if (!data || data === '[DONE]') return

  try {
    const delta = extractStreamDelta(JSON.parse(data))
    if (delta.reasoning || delta.toolCalls?.length) {
      onProcessDelta({
        reasoning: delta.reasoning,
        toolCalls: delta.toolCalls,
      })
    }
    if (delta.content) await emitStreamDelta(delta.content, onDelta)
  } catch {
    // Ignore malformed event payloads; the stream may still continue with valid events.
  }
}

async function readSSEStream(response: Response, onDelta: (delta: string) => void, onProcessDelta: (delta: StreamPayloadDelta) => void): Promise<void> {
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
        await processSSELine(line, onDelta, onProcessDelta)
      }
    }

    buffer += decoder.decode()
    if (buffer) {
      await processSSELine(buffer, onDelta, onProcessDelta)
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
  const webSearchEnabled = ref(false)
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
  let historicalStreamPollTimer: number | null = null

  const currentMessages = computed(() => currentConversation.value?.messages ?? [])
  const selectedModelSupportsThinking = computed(() => Boolean(selectedModel.value?.supports_thinking))
  const selectedModelSupportsWebSearch = computed(() => Boolean(selectedModel.value?.supports_web_search))
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
    return highestThinkingEffort(thinkingEffortOptions.value)
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

  function clearHistoricalStreamPoll(): void {
    if (historicalStreamPollTimer !== null) {
      window.clearTimeout(historicalStreamPollTimer)
      historicalStreamPollTimer = null
    }
  }

  function clearActiveStreamState(): void {
    clearHistoricalStreamPoll()
    streaming.value = false
    abortController.value = null
    activeUserMessageId.value = null
    activeAssistantMessageId.value = null
    activeBackendAssistantMessageId.value = null
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

  function getModelDisplayName(provider: string, model: string, displayName = ''): string {
    const normalizedProvider = provider.trim().toLowerCase()
    const normalizedModel = model.trim()
    const exactMatch = models.value.find((item) =>
      item.provider.trim().toLowerCase() === normalizedProvider && item.model === normalizedModel
    )
    const modelMatch = exactMatch ?? models.value.find((item) => item.model === normalizedModel)

    return formatWebChatModelName({
      provider: normalizedProvider || modelMatch?.provider,
      model: normalizedModel,
      displayName: displayName.trim() || modelMatch?.display_name,
    })
  }

  function selectModel(provider: string, model: string): boolean {
    const match = models.value.find((item) => item.provider === provider && item.model === model)
    if (match) {
      selectedModel.value = match
      return true
    }
    return false
  }

  function isFreshHistoricalStreamingMessage(message: WebChatMessage): boolean {
    if (message.role !== 'assistant') return false
    if (message.status !== 'streaming' && message.status !== 'pending') return false
    const updatedAt = Date.parse(message.updated_at || message.created_at)
    if (!Number.isFinite(updatedAt)) return false
    return Date.now() - updatedAt <= HISTORICAL_STREAMING_STALE_MS
  }

  function latestFreshHistoricalAssistant(messages: WebChatMessage[]): WebChatMessage | undefined {
    return [...messages].reverse().find(isFreshHistoricalStreamingMessage)
  }

  function applyConversationDetail(detail: WebChatConversationDetail): void {
    currentConversation.value = detail
    upsertConversation(detail.conversation)
    setSelectedModelFromConversation(detail.conversation)
  }

  async function refreshHistoricalStream(conversationId: number): Promise<void> {
    historicalStreamPollTimer = null
    if (currentConversation.value?.conversation.id !== conversationId || !streaming.value) return

    try {
      const detail = await chatAPI.getConversation(conversationId)
      applyConversationDetail(detail)
      syncHistoricalStreamState(detail, true)
    } catch {
      if (currentConversation.value?.conversation.id === conversationId && streaming.value) {
        scheduleHistoricalStreamPoll(conversationId)
      }
    }
  }

  function scheduleHistoricalStreamPoll(conversationId: number): void {
    clearHistoricalStreamPoll()
    historicalStreamPollTimer = window.setTimeout(() => {
      void refreshHistoricalStream(conversationId)
    }, HISTORICAL_STREAM_POLL_INTERVAL_MS)
  }

  function syncHistoricalStreamState(detail: WebChatConversationDetail, poll: boolean): void {
    const assistant = latestFreshHistoricalAssistant(detail.messages)
    if (!assistant) {
      clearActiveStreamState()
      return
    }

    clearHistoricalStreamPoll()
    activeUserMessageId.value = null
    activeAssistantMessageId.value = assistant.id
    activeBackendAssistantMessageId.value = assistant.id
    abortController.value = null
    streaming.value = true
    if (poll) {
      scheduleHistoricalStreamPoll(detail.conversation.id)
    }
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

  function reconcileWebSearchSettings(): void {
    if (!selectedModelSupportsWebSearch.value) {
      webSearchEnabled.value = false
    }
  }

  watch(selectedModel, () => {
    reconcileThinkingSettings()
    reconcileWebSearchSettings()
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
      applyConversationDetail(detail)
      syncHistoricalStreamState(detail, true)
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

  function startAssistantStream(input: StartAssistantStreamInput): StartAssistantStreamResult {
    clearHistoricalStreamPoll()
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

    return { userMessageId, assistantMessageId }
  }

  function reconcileActiveStreamMessageIds(userMessageId: number | null, assistantMessageId: number | null): void {
    if (!currentConversation.value) return

    const messages = currentConversation.value.messages

    if (activeUserMessageId.value !== null && userMessageId && userMessageId > 0) {
      const tempUserMessage = messages.find((message) => message.id === activeUserMessageId.value)
      const existingUserMessage = messages.find((message) => message.id === userMessageId)
      if (tempUserMessage && (!existingUserMessage || existingUserMessage === tempUserMessage)) {
        tempUserMessage.id = userMessageId
      }
      activeUserMessageId.value = userMessageId
    }

    if (activeAssistantMessageId.value !== null && assistantMessageId && assistantMessageId > 0) {
      const tempAssistantMessage = messages.find((message) => message.id === activeAssistantMessageId.value)
      const existingAssistantMessage = messages.find((message) => message.id === assistantMessageId)
      if (tempAssistantMessage && (!existingAssistantMessage || existingAssistantMessage === tempAssistantMessage)) {
        tempAssistantMessage.id = assistantMessageId
      }
      activeAssistantMessageId.value = assistantMessageId
      activeBackendAssistantMessageId.value = assistantMessageId
    }
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

  function activeAssistantMessage(): WebChatMessage | undefined {
    if (!currentConversation.value) return undefined

    return [...currentConversation.value.messages]
      .reverse()
      .find((message) => {
        if (activeAssistantMessageId.value !== null) {
          return message.id === activeAssistantMessageId.value
        }
        return message.role === 'assistant' && message.status === 'streaming'
      })
  }

  function appendAssistantReasoningDelta(delta: string): void {
    if (!delta) return
    const assistantMessage = activeAssistantMessage()
    if (!assistantMessage) return

    const blocks = assistantMessage.content_json
    const lastBlock = blocks[blocks.length - 1]
    if (lastBlock?.type === 'reasoning' && typeof lastBlock.text === 'string') {
      lastBlock.text += delta
    } else {
      blocks.push({
        type: 'reasoning',
        text: delta,
      })
    }
    assistantMessage.updated_at = nowISO()
  }

  function appendAssistantToolCallDelta(delta: StreamToolCallDelta): void {
    if (!delta.id && !delta.name && !delta.input) return
    const assistantMessage = activeAssistantMessage()
    if (!assistantMessage) return

    const blocks = assistantMessage.content_json
    const existing = delta.id
      ? blocks.find((block) => block.type === 'tool_call' && block.id === delta.id)
      : typeof delta.index === 'number'
        ? blocks.find((block) => block.type === 'tool_call' && block.index === delta.index)
        : [...blocks].reverse().find((block) => block.type === 'tool_call' && (!delta.name || block.name === delta.name))

    if (existing) {
      if (delta.id && typeof existing.id !== 'string') existing.id = delta.id
      if (typeof delta.index === 'number' && typeof existing.index !== 'number') existing.index = delta.index
      if (delta.name) existing.name = delta.name
      if (delta.input) {
        existing.input = delta.inputMode === 'replace' ? delta.input : `${textValue(existing.input)}${delta.input}`
      }
    } else {
      blocks.push({
        type: 'tool_call',
        ...(delta.id ? { id: delta.id } : {}),
        ...(typeof delta.index === 'number' ? { index: delta.index } : {}),
        ...(delta.name ? { name: delta.name } : {}),
        ...(delta.input ? { input: delta.input } : {}),
      })
    }
    assistantMessage.updated_at = nowISO()
  }

  function appendAssistantProcessDelta(delta: StreamPayloadDelta): void {
    if (delta.reasoning) {
      appendAssistantReasoningDelta(delta.reasoning)
    }
    for (const toolCall of delta.toolCalls ?? []) {
      appendAssistantToolCallDelta(toolCall)
    }
  }

  function finishAssistantStream(status: WebChatMessageStatus = 'completed'): void {
    clearHistoricalStreamPoll()
    if (currentConversation.value && activeAssistantMessageId.value !== null) {
      const assistantMessage = currentConversation.value.messages.find(
        (message) => message.id === activeAssistantMessageId.value
      )
      if (assistantMessage) {
        assistantMessage.status = status
        assistantMessage.updated_at = nowISO()
      }
    }

    clearActiveStreamState()
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

    const model = selectedModel.value
    const conversation = currentConversation.value?.conversation ?? await createConversation(text.slice(0, 80))
    const attachments = [...pendingAttachments.value]
    const request: SendWebChatMessageRequest = {
      model: model.model,
      provider: model.provider,
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
    if (selectedModelSupportsWebSearch.value && webSearchEnabled.value) {
      request.web_search = {
        enabled: true,
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
    error.value = null
    startAssistantStream({
      conversationId: conversation.id,
      userMessageId: null,
      assistantMessageId: null,
      content: text,
      model: model.model,
      provider: model.provider,
      attachments,
    })
    pendingAttachments.value = []
    let requestAccepted = false

    try {
      const streamResult = await chatAPI.sendMessageStream(conversation.id, request, controller.signal)
      requestAccepted = true
      reconcileActiveStreamMessageIds(streamResult.userMessageId, streamResult.assistantMessageId)
      await readSSEStream(streamResult.response, appendAssistantDelta, appendAssistantProcessDelta)
      finishAssistantStream()
      if (model.supports_artifact_output) {
        try {
          await openConversation(conversation.id)
        } catch {
          // Keep the completed streamed message visible even if the post-stream artifact refresh fails.
        }
      }
    } catch (err) {
      if (isAbortError(err) || controller.signal.aborted) {
        finishAssistantStream('canceled')
        return
      }
      finishAssistantStream('failed')
      if (!requestAccepted) {
        pendingAttachments.value = attachments
      }
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
    webSearchEnabled,
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
    selectedModelSupportsWebSearch,
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
    getModelDisplayName,
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
    appendAssistantProcessDelta,
    finishAssistantStream,
  }
})
