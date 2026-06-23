<template>
  <button
    v-if="downloadable"
    class="inline-flex max-w-full items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1.5 text-left text-xs text-linear-ink-muted transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
    type="button"
    @click="$emit('download')"
  >
    <Icon :name="iconName" size="sm" class="shrink-0" />
    <span class="min-w-0 truncate">{{ filename }}</span>
    <span v-if="formattedSize" class="shrink-0 text-linear-ink-tertiary">{{ formattedSize }}</span>
    <Icon name="download" size="xs" class="shrink-0" />
  </button>
  <span
    v-else
    class="inline-flex max-w-full items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1.5 text-xs text-linear-ink-muted"
  >
    <Icon :name="iconName" size="sm" class="shrink-0" />
    <span class="min-w-0 truncate">{{ filename }}</span>
    <span v-if="formattedSize" class="shrink-0 text-linear-ink-tertiary">{{ formattedSize }}</span>
    <span v-if="status" class="shrink-0 text-linear-ink-tertiary">{{ status }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  kind?: 'image' | 'file' | string
  filename: string
  sizeBytes?: number
  status?: string
  downloadable?: boolean
}>(), {
  kind: 'file',
  sizeBytes: 0,
  status: '',
  downloadable: false,
})

defineEmits<{
  (event: 'download'): void
}>()

const iconName = computed(() => props.kind === 'image' ? 'upload' : 'document')

const formattedSize = computed(() => {
  if (!props.sizeBytes) return ''
  if (props.sizeBytes < 1024) return `${props.sizeBytes} B`
  if (props.sizeBytes < 1024 * 1024) return `${(props.sizeBytes / 1024).toFixed(1)} KB`
  return `${(props.sizeBytes / 1024 / 1024).toFixed(1)} MB`
})
</script>
