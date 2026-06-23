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

    <div v-else class="mx-auto flex max-w-3xl flex-col gap-5">
      <article
        v-for="message in messages"
        :key="message.id"
        class="flex min-w-0"
        :class="message.role === 'user' ? 'justify-end' : 'justify-start'"
      >
        <div
          class="max-w-[min(100%,42rem)] rounded-lg border px-4 py-3"
          :class="message.role === 'user'
            ? 'border-primary-500/30 bg-primary-500 text-white'
            : 'border-linear-hairline bg-linear-surface-1 text-linear-ink'"
        >
          <div class="mb-2 flex items-center justify-between gap-3 text-xs">
            <span :class="message.role === 'user' ? 'text-white/75' : 'text-linear-ink-tertiary'">
              {{ message.role === 'user' ? 'You' : assistantLabel(message) }}
            </span>
            <span
              v-if="message.status !== 'completed'"
              :class="message.role === 'user' ? 'text-white/75' : 'text-linear-ink-tertiary'"
            >
              {{ message.status }}
            </span>
          </div>

          <p v-if="message.content_text" class="whitespace-pre-wrap break-words text-sm leading-6">
            {{ message.content_text }}
          </p>
          <p v-else-if="message.status === 'streaming'" class="text-sm text-linear-ink-subtle">
            Thinking...
          </p>

          <div v-if="message.attachments?.length" class="mt-3 flex flex-wrap gap-2">
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

          <div v-if="imageArtifacts(message).length" class="mt-3 grid gap-3 sm:grid-cols-2">
            <ArtifactImagePreview
              v-for="artifact in imageArtifacts(message)"
              :key="artifact.id"
              :artifact="artifact"
            />
          </div>

          <div v-if="fileArtifacts(message).length" class="mt-3 flex flex-wrap gap-2">
            <AttachmentChip
              v-for="artifact in fileArtifacts(message)"
              :key="artifact.id"
              kind="file"
              :filename="artifact.filename"
              :size-bytes="artifact.size_bytes"
              :downloadable="true"
              @download="downloadArtifact(artifact.id)"
            />
          </div>

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
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { chatAPI, type WebChatArtifact, type WebChatMessage } from '@/api/chat'
import ArtifactImagePreview from '@/components/chat/ArtifactImagePreview.vue'
import AttachmentChip from '@/components/chat/AttachmentChip.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()
const messages = computed(() => chatStore.currentMessages)

function assistantLabel(message: WebChatMessage): string {
  return message.model || 'Assistant'
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
