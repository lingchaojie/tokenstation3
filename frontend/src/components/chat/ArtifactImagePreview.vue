<template>
  <figure class="w-full min-w-0 max-w-full">
    <div class="relative w-full overflow-hidden rounded-lg border border-linear-hairline bg-linear-canvas">
      <img
        v-if="imageUrl"
        :data-testid="`chat-artifact-image-${artifact.id}`"
        :src="imageUrl"
        :alt="artifact.filename"
        class="block h-auto max-h-[70vh] w-full max-w-full object-contain sm:max-h-96"
      >
      <div
        v-else
        class="flex aspect-[4/3] min-h-40 items-center justify-center gap-2 px-4 text-sm text-linear-ink-subtle"
      >
        <Icon name="image" size="sm" />
        <span>{{ loading ? 'Loading preview...' : 'Preview unavailable' }}</span>
      </div>

      <button
        class="absolute right-2 top-2 inline-flex h-8 w-8 items-center justify-center rounded-md border border-linear-hairline bg-linear-surface-1/95 text-linear-ink-muted shadow-sm transition-colors hover:bg-linear-surface-2 hover:text-linear-ink"
        type="button"
        :title="`Download ${artifact.filename}`"
        :data-testid="`chat-artifact-image-download-${artifact.id}`"
        @click="downloadImage"
      >
        <Icon name="download" size="sm" />
      </button>
    </div>

    <figcaption class="mt-2 flex min-w-0 items-center gap-2 text-xs text-linear-ink-muted">
      <span class="min-w-0 truncate">{{ artifact.filename }}</span>
      <span v-if="formattedSize" class="shrink-0 text-linear-ink-tertiary">{{ formattedSize }}</span>
    </figcaption>
  </figure>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { chatAPI, type WebChatArtifact, type WebChatDownload } from '@/api/chat'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  artifact: WebChatArtifact
}>()

const imageUrl = ref('')
const loading = ref(false)
const loadedDownload = ref<WebChatDownload | null>(null)
let loadToken = 0

const formattedSize = computed(() => {
  const sizeBytes = props.artifact.size_bytes
  if (!sizeBytes) return ''
  if (sizeBytes < 1024) return `${sizeBytes} B`
  if (sizeBytes < 1024 * 1024) return `${(sizeBytes / 1024).toFixed(1)} KB`
  return `${(sizeBytes / 1024 / 1024).toFixed(1)} MB`
})

function revokeImageUrl(): void {
  if (!imageUrl.value) return
  window.URL.revokeObjectURL(imageUrl.value)
  imageUrl.value = ''
}

async function loadPreview(): Promise<void> {
  const token = ++loadToken
  loading.value = true
  loadedDownload.value = null
  revokeImageUrl()

  try {
    const download = await chatAPI.downloadArtifact(props.artifact.id)
    if (token !== loadToken) return
    loadedDownload.value = download
    imageUrl.value = window.URL.createObjectURL(download.blob)
  } catch {
    if (token === loadToken) {
      loadedDownload.value = null
      revokeImageUrl()
    }
  } finally {
    if (token === loadToken) {
      loading.value = false
    }
  }
}

async function downloadImage(): Promise<void> {
  const download = loadedDownload.value ?? await chatAPI.downloadArtifact(props.artifact.id)
  saveBlob(download.blob, download.filename || props.artifact.filename)
}

function saveBlob(blob: Blob, filename: string): void {
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  window.URL.revokeObjectURL(url)
}

watch(() => props.artifact.id, () => {
  void loadPreview()
}, { immediate: true })

onBeforeUnmount(() => {
  loadToken += 1
  revokeImageUrl()
})
</script>
