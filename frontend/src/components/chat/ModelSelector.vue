<template>
  <header class="border-b border-linear-hairline bg-linear-canvas px-4 py-3" data-testid="chat-model-selector">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
      <div class="grid gap-2 sm:grid-cols-[minmax(9rem,12rem)_minmax(12rem,22rem)]">
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-linear-ink-tertiary">Provider</span>
          <select
            v-model="selectedProvider"
            class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong"
            aria-label="Provider"
          >
            <option v-for="provider in providers" :key="provider" :value="provider">{{ provider }}</option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-linear-ink-tertiary">Model</span>
          <select
            v-model="selectedModelKey"
            class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong"
            aria-label="Model"
          >
            <option v-for="model in modelOptions" :key="modelKey(model)" :value="modelKey(model)">
              {{ model.display_name || model.model }}
            </option>
          </select>
        </label>
      </div>

      <div v-if="chatStore.selectedModel" class="flex flex-wrap gap-2">
        <span
          v-for="capability in capabilities"
          :key="capability.label"
          class="inline-flex items-center rounded-lg border px-2.5 py-1 text-xs font-medium"
          :class="capability.enabled
            ? 'border-linear-hairline bg-linear-surface-1 text-linear-ink-muted'
            : 'border-linear-hairline bg-linear-canvas text-linear-ink-tertiary line-through'"
        >
          {{ capability.label }}
        </span>
      </div>
    </div>

    <div
      v-if="chatStore.capabilityWarning"
      class="mt-3 flex items-start gap-2 rounded-lg border border-amber-400/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-200"
      role="alert"
    >
      <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
      <span>{{ chatStore.capabilityWarning }}</span>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { WebChatModel } from '@/api/chat'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()

const providers = computed(() => Array.from(new Set(chatStore.models.map((model) => model.provider))).sort())

const selectedProvider = computed({
  get() {
    return chatStore.selectedModel?.provider || providers.value[0] || ''
  },
  set(provider: string) {
    const nextModel = chatStore.models.find((model) => model.provider === provider)
    if (nextModel) {
      chatStore.selectedModel = nextModel
    }
  },
})

const modelOptions = computed(() =>
  chatStore.models.filter((model) => model.provider === selectedProvider.value)
)

const selectedModelKey = computed({
  get() {
    return chatStore.selectedModel ? modelKey(chatStore.selectedModel) : ''
  },
  set(key: string) {
    const nextModel = chatStore.models.find((model) => modelKey(model) === key)
    if (nextModel) {
      chatStore.selectedModel = nextModel
    }
  },
})

const capabilities = computed(() => {
  const model = chatStore.selectedModel
  if (!model) return []
  return [
    { label: 'Image', enabled: model.supports_image_input },
    { label: 'Files', enabled: model.supports_file_context },
    { label: 'Artifacts', enabled: model.supports_artifact_output },
  ]
})

function modelKey(model: WebChatModel): string {
  return `${model.provider}:${model.model}`
}
</script>
