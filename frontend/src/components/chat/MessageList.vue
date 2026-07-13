<template>
  <section class="overflow-y-auto bg-linear-canvas px-4 py-6" data-testid="chat-message-list">
    <div v-if="messages.length === 0" class="mx-auto flex h-full max-w-2xl flex-col items-center justify-center text-center">
      <div class="flex h-12 w-12 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink-muted">
        <Icon name="chat" size="lg" />
      </div>
      <h1 class="mt-4 text-2xl font-semibold tracking-[-0.03em] text-linear-ink">{{ t('chat.emptyTitle') }}</h1>
      <p class="mt-2 max-w-md text-sm leading-6 text-linear-ink-subtle">
        {{ t('chat.emptyDescription') }}
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
            <span>{{ t('chat.you') }}</span>
            <span v-if="message.status !== 'completed'">{{ messageStatusLabel(message) }}</span>
          </div>

          <p v-if="message.content_text" class="whitespace-pre-wrap break-words text-sm leading-6">
            {{ message.content_text }}
          </p>
          <p v-if="!message.content_text && isLiveStreaming(message)" class="text-sm text-linear-ink-subtle">
            {{ t('chat.thinkingStatus') }}
          </p>
          <p v-else-if="!message.content_text && isStaleStreaming(message)" class="text-sm text-linear-ink-subtle">
            {{ t('chat.responseInterrupted') }}
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
                <span>{{ t('chat.thinkingAndTools') }}</span>
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

          <div
            v-if="message.content_text"
            class="chat-markdown-body text-sm leading-6 text-linear-ink"
            data-testid="chat-assistant-markdown"
            v-html="renderMarkdownContent(message.content_text)"
          />
          <div
            v-if="sourceLinks(message).length > 0"
            class="mt-4 flex flex-wrap gap-2"
            data-testid="chat-source-links"
          >
            <a
              v-for="source in sourceLinks(message)"
              :key="source.href"
              :href="source.href"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex max-w-full items-center gap-1.5 rounded-md border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs font-medium text-linear-ink-muted transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
            >
              <Icon name="globe" size="xs" class="shrink-0" />
              <span class="min-w-0 truncate">{{ source.label }}</span>
            </a>
          </div>
          <p v-if="!message.content_text && isLiveStreaming(message)" class="text-sm text-linear-ink-subtle">
            {{ t('chat.thinkingStatus') }}
          </p>
          <p v-else-if="!message.content_text && isStaleStreaming(message)" class="text-sm text-linear-ink-subtle">
            {{ t('chat.responseInterrupted') }}
          </p>

          <div v-if="message.status === 'failed'" class="mt-3 flex items-center gap-2 text-xs">
            <span class="text-red-500">{{ message.error_message || t('chat.messageFailed') }}</span>
            <button
              class="rounded-lg border border-linear-hairline bg-linear-canvas px-2 py-1 text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink"
              type="button"
              @click="retryMessage(message.id)"
            >
              {{ t('chat.retry') }}
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
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import { marked } from 'marked'

import { chatAPI, type WebChatArtifact, type WebChatMessage } from '@/api/chat'
import ArtifactFileCard from '@/components/chat/ArtifactFileCard.vue'
import ArtifactImagePreview from '@/components/chat/ArtifactImagePreview.vue'
import AttachmentChip from '@/components/chat/AttachmentChip.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const { t } = useI18n()
const chatStore = useChatStore()
const messages = computed(() => chatStore.currentMessages)
const STALE_STREAMING_MS = 10 * 60 * 1000
const MAX_SOURCE_LINKS = 8

interface SourceLink {
  href: string
  label: string
}

function assistantLabel(message: WebChatMessage): string {
  return chatStore.getModelDisplayName(message.provider, message.model) || t('chat.assistant')
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
  if (isStaleStreaming(message)) return t('chat.statusInterrupted')
  const statusKeys: Record<string, string> = {
    pending: 'chat.statusPending',
    streaming: 'chat.statusStreaming',
    completed: 'chat.statusCompleted',
    failed: 'chat.statusFailed',
  }
  const key = statusKeys[message.status]
  return key ? t(key) : message.status
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

function renderMarkdownContent(content: string): string {
  const html = marked.parse(content, {
    async: false,
    breaks: true,
    gfm: true,
  }) as string
  return DOMPurify
    .sanitize(html)
    .replace(/<a /g, '<a target="_blank" rel="noopener noreferrer" ')
}

function sourceLinks(message: WebChatMessage): SourceLink[] {
  if (message.role === 'user' || !message.content_text) return []
  return extractSourceLinks(message.content_text)
}

function extractSourceLinks(content: string): SourceLink[] {
  const links: SourceLink[] = []
  const seen = new Set<string>()
  const addLink = (label: string, href: string) => {
    if (links.length >= MAX_SOURCE_LINKS) return
    const normalized = normalizeHTTPURL(href)
    if (!normalized || seen.has(normalized)) return
    seen.add(normalized)
    links.push({
      href: normalized,
      label: sourceLabel(label, normalized),
    })
  }

  const markdownLinkPattern = /\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/gi
  for (const match of content.matchAll(markdownLinkPattern)) {
    addLink(match[1], match[2])
  }

  const bareURLPattern = /https?:\/\/[^\s)]+/gi
  for (const match of content.matchAll(bareURLPattern)) {
    addLink('', match[0])
  }

  return links
}

function normalizeHTTPURL(value: string): string {
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
    return url.toString()
  } catch {
    return ''
  }
}

function sourceLabel(label: string, href: string): string {
  const cleanedLabel = label.trim()
  try {
    const host = new URL(href).hostname.replace(/^www\./i, '')
    if (cleanedLabel && cleanedLabel.length <= 36 && !/^https?:\/\//i.test(cleanedLabel) && cleanedLabel !== host) {
      return `${cleanedLabel} · ${host}`
    }
    return host
  } catch {
    return cleanedLabel || href
  }
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
    return t('chat.toolCall', { name })
  }
  if (type === 'tool_result') return t('chat.toolResult')
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
  if (thinkingCount > 0 && toolCount > 0) {
    return `${t('chat.thoughtCount', { count: thinkingCount })} · ${t('chat.toolCount', { count: toolCount })}`
  }
  if (toolCount > 0) return t('chat.toolCount', { count: toolCount })
  return t('chat.thoughtCount', { count: thinkingCount })
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

<style scoped>
.chat-markdown-body {
  overflow-wrap: anywhere;
}

.chat-markdown-body :deep(p) {
  margin: 0.5rem 0;
}

.chat-markdown-body :deep(p:first-child) {
  margin-top: 0;
}

.chat-markdown-body :deep(p:last-child) {
  margin-bottom: 0;
}

.chat-markdown-body :deep(ul),
.chat-markdown-body :deep(ol) {
  margin: 0.5rem 0;
  padding-left: 1.25rem;
}

.chat-markdown-body :deep(ul) {
  list-style: disc;
}

.chat-markdown-body :deep(ol) {
  list-style: decimal;
}

.chat-markdown-body :deep(li) {
  margin: 0.25rem 0;
}

.chat-markdown-body :deep(a) {
  color: #ea580c;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.chat-markdown-body :deep(strong) {
  font-weight: 650;
}

.chat-markdown-body :deep(code) {
  border-radius: 0.25rem;
  background: rgb(var(--linear-surface-1) / 1);
  padding: 0.1rem 0.3rem;
  font-size: 0.875em;
}

.chat-markdown-body :deep(pre) {
  margin: 0.75rem 0;
  overflow-x: auto;
  border-radius: 0.5rem;
  background: rgb(var(--linear-surface-1) / 1);
  padding: 0.75rem;
}

.chat-markdown-body :deep(pre code) {
  background: transparent;
  padding: 0;
}

.chat-markdown-body :deep(blockquote) {
  margin: 0.75rem 0;
  border-left: 3px solid rgb(var(--linear-hairline) / 1);
  padding-left: 0.75rem;
  color: rgb(var(--linear-ink-muted) / 1);
}
</style>
