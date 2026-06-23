<template>
  <div
    class="flex min-w-0 items-center gap-3 rounded-lg border border-linear-hairline bg-linear-canvas px-3 py-2"
    :data-testid="`chat-artifact-file-${artifact.id}`"
  >
    <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-linear-hairline bg-linear-surface-1 text-linear-ink-muted">
      <Icon name="document" size="sm" />
    </div>

    <div class="min-w-0 flex-1">
      <div class="truncate text-sm font-medium text-linear-ink">{{ artifact.filename }}</div>
      <div class="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-linear-ink-tertiary">
        <span v-if="artifact.content_type" class="min-w-0 truncate">{{ artifact.content_type }}</span>
        <span v-if="formattedSize" class="shrink-0">{{ formattedSize }}</span>
      </div>
    </div>

    <button
      class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-linear-hairline bg-linear-surface-1 text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink"
      type="button"
      :title="`Download ${artifact.filename}`"
      :data-testid="`chat-artifact-file-download-${artifact.id}`"
      @click="$emit('download')"
    >
      <Icon name="download" size="sm" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { WebChatArtifact } from '@/api/chat'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  artifact: WebChatArtifact
}>()

defineEmits<{
  (event: 'download'): void
}>()

const formattedSize = computed(() => {
  const sizeBytes = props.artifact.size_bytes
  if (!sizeBytes) return ''
  if (sizeBytes < 1024) return `${sizeBytes} B`
  if (sizeBytes < 1024 * 1024) return `${(sizeBytes / 1024).toFixed(1)} KB`
  return `${(sizeBytes / 1024 / 1024).toFixed(1)} MB`
})
</script>
