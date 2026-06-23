<template>
  <footer class="border-t border-linear-hairline bg-linear-canvas px-4 py-3" data-testid="chat-composer">
    <div class="mx-auto max-w-3xl">
      <div v-if="chatStore.pendingAttachments.length > 0" class="mb-2 flex flex-wrap gap-2">
        <AttachmentChip
          v-for="attachment in chatStore.pendingAttachments"
          :key="attachment.id"
          :kind="attachment.kind"
          :filename="attachment.filename"
          :size-bytes="attachment.size_bytes"
          :status="attachment.status"
        />
        <button
          class="inline-flex h-8 items-center rounded-lg border border-linear-hairline px-2.5 text-xs text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink"
          type="button"
          @click="clearAttachments"
        >
          Clear
        </button>
      </div>

      <div class="rounded-lg border border-linear-hairline bg-linear-surface-1 p-2 focus-within:border-linear-hairline-strong">
        <textarea
          v-model="draft"
          class="max-h-44 min-h-[56px] w-full resize-none bg-transparent px-2 py-2 text-sm leading-6 text-linear-ink outline-none placeholder:text-linear-ink-tertiary disabled:cursor-not-allowed disabled:opacity-60"
          placeholder="Message the selected model"
          aria-label="Message"
          :disabled="chatStore.streaming"
          @keydown.enter="handleComposerEnter"
        />

        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-1.5">
            <button
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink disabled:cursor-not-allowed disabled:opacity-50"
              type="button"
              title="Upload image"
              aria-label="Upload image"
              :disabled="chatStore.streaming || uploading"
              @click="imageInput?.click()"
            >
              <Icon name="upload" size="sm" />
            </button>
            <button
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink disabled:cursor-not-allowed disabled:opacity-50"
              type="button"
              title="Upload file"
              aria-label="Upload file"
              :disabled="chatStore.streaming || uploading"
              @click="fileInput?.click()"
            >
              <Icon name="document" size="sm" />
            </button>
          </div>

          <div class="flex items-center gap-1.5">
            <button
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg bg-primary-500 text-white transition-colors hover:bg-primary-400 disabled:cursor-not-allowed disabled:bg-linear-surface-2 disabled:text-linear-ink-tertiary"
              type="button"
              title="Send"
              aria-label="Send message"
              data-testid="chat-send"
              :disabled="sendDisabled"
              @click="submit"
            >
              <Icon name="arrowUp" size="sm" />
            </button>
            <button
              v-if="chatStore.streaming"
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-linear-hairline bg-linear-canvas text-linear-ink transition-colors hover:bg-linear-surface-2"
              type="button"
              title="Stop"
              aria-label="Stop response"
              data-testid="chat-stop"
              @click="chatStore.cancelStream()"
            >
              <Icon name="x" size="sm" />
            </button>
          </div>
        </div>
      </div>

      <p v-if="chatStore.error" class="mt-2 text-xs text-red-500" role="alert">{{ chatStore.error }}</p>
      <p v-else-if="chatStore.capabilityWarning" class="mt-2 text-xs text-amber-600 dark:text-amber-300">
        {{ chatStore.capabilityWarning }}
      </p>
    </div>

    <input
      ref="imageInput"
      class="hidden"
      type="file"
      accept="image/*"
      @change="handleFileInput"
    />
    <input
      ref="fileInput"
      class="hidden"
      type="file"
      @change="handleFileInput"
    />
  </footer>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

import AttachmentChip from '@/components/chat/AttachmentChip.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()
const draft = ref('')
const uploading = ref(false)
const imageInput = ref<HTMLInputElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const hasDraft = computed(() => draft.value.trim().length > 0 || chatStore.pendingAttachments.length > 0)
const sendDisabled = computed(() =>
  chatStore.streaming ||
  uploading.value ||
  !hasDraft.value ||
  !chatStore.selectedModel ||
  !!chatStore.capabilityWarning
)

async function submit(): Promise<void> {
  if (sendDisabled.value) return
  const content = draft.value
  await chatStore.sendMessage(content)
  draft.value = ''
}

function handleComposerEnter(event: KeyboardEvent): void {
  if (event.shiftKey || event.isComposing) return
  event.preventDefault()
  void submit()
}

async function handleFileInput(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return

  uploading.value = true
  try {
    await chatStore.uploadAttachment(file)
  } finally {
    uploading.value = false
  }
}

function clearAttachments(): void {
  chatStore.pendingAttachments = []
}
</script>
