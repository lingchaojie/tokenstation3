<template>
  <section class="overflow-y-auto bg-linear-canvas px-4 py-6" data-testid="chat-message-list">
    <div v-if="messages.length === 0" class="mx-auto flex h-full max-w-2xl flex-col items-center justify-center text-center">
      <div class="flex h-12 w-12 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink-muted">
        <Icon name="chat" size="lg" />
      </div>
      <h1 class="mt-4 text-2xl font-semibold tracking-[-0.03em] text-linear-ink">Start a new chat</h1>
      <p class="mt-2 max-w-md text-sm leading-6 text-linear-ink-subtle">
        Choose a model, upload context if needed, and send a message. Usage is billed through the same subscription-first rules as API keys.
      </p>
    </div>

    <div v-else class="mx-auto flex max-w-3xl flex-col gap-8">
      <article
        v-for="message in messages"
        :key="message.id"
        class="flex min-w-0 flex-col gap-3"
        :class="message.role === 'user' ? 'items-end' : 'items-start'"
      >
        <div
          v-if="message.role === 'user'"
          class="max-w-[min(100%,22rem)] rounded-lg bg-primary-500 px-4 py-3 text-white shadow-sm"
          data-testid="chat-user-message"
        >
          <div class="mb-1.5 flex items-center justify-between gap-3 text-xs text-white/75">
            <span>You</span>
            <span v-if="message.status !== 'completed'">{{ messageStatusLabel(message) }}</span>
          </div>

          <p v-if="message.content_text" class="whitespace-pre-wrap break-words text-sm leading-6">
            {{ message.content_text }}
          </p>
          <p v-else-if="isLiveStreaming(message)" class="text-sm text-linear-ink-subtle">
            Thinking...
          </p>
          <p v-else-if="isStaleStreaming(message)" class="text-sm text-linear-ink-subtle">
            Response interrupted before completion.
          </p>

        </div>

        <div
          v-if="message.role === 'user' && message.attachments?.length"
          class="max-w-[min(100%,24rem)] rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 py-2 shadow-sm"
          :data-testid="`chat-attachment-bubble-${message.id}`"
        >
          <div class="flex flex-wrap gap-2">
            <AttachmentChip
              v-for="attachment in message.attachments"
              :key="attachment.id"
              :kind="attachment.kind"
              :filename="attachment.filename"
              :size-bytes="attachment.size_bytes"
              :status="attachment.status"
              :downloadable="true"
              @download="downloadAttachment(attachment.id)"
            />
          </div>
        </div>

        <div v-if="message.role !== 'user'" class="w-full min-w-0 text-linear-ink" data-testid="chat-assistant-open-message">
          <div class="mb-3 flex min-w-0 items-center justify-between gap-3">
            <div class="flex min-w-0 items-center gap-2">
              <ModelIcon :model="providerIconModel(message.provider || message.model)" size="28px" aria-hidden="true" />
              <span class="min-w-0 truncate text-sm font-semibold text-linear-ink">{{ assistantLabel(message) }}</span>
            </div>
            <span v-if="message.status !== 'completed'" class="shrink-0 text-xs text-linear-ink-tertiary">
              {{ messageStatusLabel(message) }}
            </span>
          </div>

          <details
            v-if="processBlocks(message).length > 0"
            class="mb-3 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 py-2 text-sm"
            :open="message.status !== 'completed'"
            data-testid="chat-process-block"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 text-linear-ink-muted">
              <span class="inline-flex min-w-0 items-center gap-2 font-medium">
                <Icon name="brain" size="sm" />
                <span>Thinking and tools</span>
              </span>
              <span class="shrink-0 text-xs text-linear-ink-tertiary">
                {{ processSummary(message) }}
              </span>
            </summary>
            <div class="mt-3 space-y-3 border-t border-linear-hairline pt-3">
              <div
                v-for="(block, index) in processBlocks(message)"
                :key="`${processBlockType(block)}-${index}`"
                class="rounded-md bg-linear-canvas px-3 py-2"
              >
                <div class="mb-1 flex items-center gap-2 text-xs font-medium uppercase text-linear-ink-tertiary">
                  <Icon :name="processBlockType(block) === 'tool_call' ? 'terminal' : 'brain'" size="xs" />
                  <span>{{ processBlockTitle(block) }}</span>
                </div>
                <pre
                  v-if="processBlockBody(block)"
                  class="max-h-44 overflow-auto whitespace-pre-wrap break-words text-xs leading-5 text-linear-ink-muted"
                >{{ processBlockBody(block) }}</pre>
              </div>
            </div>
          </details>

          <p v-if="message.content_text" class="whitespace-pre-wrap break-words text-sm leading-6 text-linear-ink">
            {{ message.content_text }}
          </p>
          <p v-else-if="isLiveStreaming(message)" class="text-sm text-linear-ink-subtle">
            Thinking...
          </p>
          <p v-else-if="isStaleStreaming(message)" class="text-sm text-linear-ink-subtle">
            Response interrupted before completion.
          </p>

          <div v-if="message.status === 'failed'" class="mt-3 flex items-center gap-2 text-xs">
            <span class="text-red-500">{{ message.error_message || 'Message failed.' }}</span>
            <button
              class="rounded-lg border border-linear-hairline bg-linear-canvas px-2 py-1 text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink"
              type="button"
              @click="retryMessage(message.id)"
            >
              Retry
            </button>
          </div>
        </div>

        <div
          v-if="message.role !== 'user' && hasArtifacts(message)"
          class="w-full max-w-[min(100%,42rem)] rounded-lg border border-linear-hairline bg-linear-surface-1 p-3 shadow-sm"
          :data-testid="`chat-artifact-bubble-${message.id}`"
        >
          <div v-if="imageArtifacts(message).length" class="grid gap-3 sm:grid-cols-2">
            <ArtifactImagePreview
              v-for="artifact in imageArtifacts(message)"
              :key="artifact.id"
              :artifact="artifact"
            />
          </div>

          <div v-if="fileArtifacts(message).length" class="mt-3 grid gap-2 first:mt-0">
            <ArtifactFileCard
              v-for="artifact in fileArtifacts(message)"
              :key="artifact.id"
              :artifact="artifact"
              @download="downloadArtifact(artifact.id)"
            />
          </div>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { chatAPI, type WebChatArtifact, type WebChatMessage } from '@/api/chat'
import ArtifactFileCard from '@/components/chat/ArtifactFileCard.vue'
import ArtifactImagePreview from '@/components/chat/ArtifactImagePreview.vue'
import AttachmentChip from '@/components/chat/AttachmentChip.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const chatStore = useChatStore()
const messages = computed(() => chatStore.currentMessages)
const STALE_STREAMING_MS = 10 * 60 * 1000

function assistantLabel(message: WebChatMessage): string {
  return message.model || 'Assistant'
}

function isStreamingStatus(message: WebChatMessage): boolean {
  return message.status === 'streaming' || message.status === 'pending'
}

function isStaleStreaming(message: WebChatMessage): boolean {
  if (!isStreamingStatus(message)) return false
  const updatedAt = Date.parse(message.updated_at || message.created_at)
  if (!Number.isFinite(updatedAt)) return false
  return Date.now() - updatedAt > STALE_STREAMING_MS
}

function isLiveStreaming(message: WebChatMessage): boolean {
  return message.status === 'streaming' && !isStaleStreaming(message)
}

function messageStatusLabel(message: WebChatMessage): string {
  return isStaleStreaming(message) ? 'interrupted' : message.status
}

function isImageArtifact(artifact: WebChatArtifact): boolean {
  const contentType = artifact.content_type.toLowerCase()
  if (contentType.startsWith('image/')) return true
  return /\.(png|jpe?g|webp|gif|avif)$/i.test(artifact.filename)
}

function imageArtifacts(message: WebChatMessage): WebChatArtifact[] {
  return message.artifacts?.filter(isImageArtifact) ?? []
}

function fileArtifacts(message: WebChatMessage): WebChatArtifact[] {
  return message.artifacts?.filter((artifact) => !isImageArtifact(artifact)) ?? []
}

function hasArtifacts(message: WebChatMessage): boolean {
  return imageArtifacts(message).length > 0 || fileArtifacts(message).length > 0
}

function processBlocks(message: WebChatMessage): Array<Record<string, unknown>> {
  return message.content_json.filter((block) =>
    block.type === 'reasoning' ||
    block.type === 'tool_call' ||
    block.type === 'tool_result'
  )
}

function processBlockType(block: Record<string, unknown>): string {
  return typeof block.type === 'string' ? block.type : 'process'
}

function processBlockTitle(block: Record<string, unknown>): string {
  const type = processBlockType(block)
  if (type === 'tool_call') {
    const name = typeof block.name === 'string' && block.name.trim() ? block.name : 'tool'
    return `Tool: ${name}`
  }
  if (type === 'tool_result') return 'Tool result'
  return 'Thinking'
}

function processBlockBody(block: Record<string, unknown>): string {
  if (typeof block.text === 'string') return block.text
  if (typeof block.input === 'string') return block.input
  if (typeof block.result === 'string') return block.result
  return ''
}

function processSummary(message: WebChatMessage): string {
  const blocks = processBlocks(message)
  const thinkingCount = blocks.filter((block) => block.type === 'reasoning').length
  const toolCount = blocks.filter((block) => block.type === 'tool_call' || block.type === 'tool_result').length
  if (thinkingCount > 0 && toolCount > 0) return `${thinkingCount} thought · ${toolCount} tool`
  if (toolCount > 0) return `${toolCount} tool`
  return `${thinkingCount} thought`
}

async function retryMessage(messageId: number): Promise<void> {
  const index = messages.value.findIndex((message) => message.id === messageId)
  for (let cursor = index - 1; cursor >= 0; cursor -= 1) {
    const message = messages.value[cursor]
    if (message.role === 'user') {
      await chatStore.sendMessage(message.content_text)
      return
    }
  }
}

async function downloadAttachment(id: number): Promise<void> {
  const download = await chatAPI.downloadAttachment(id)
  saveBlob(download.blob, download.filename)
}

async function downloadArtifact(id: number): Promise<void> {
  const download = await chatAPI.downloadArtifact(id)
  saveBlob(download.blob, download.filename)
}

function saveBlob(blob: Blob, filename: string): void {
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  window.URL.revokeObjectURL(url)
}
</script>
