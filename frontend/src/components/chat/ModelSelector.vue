<template>
  <header class="border-b border-linear-hairline bg-linear-canvas px-4 py-3" data-testid="chat-model-selector">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
      <div class="grid gap-2 sm:grid-cols-[minmax(9rem,12rem)_minmax(12rem,22rem)]">
        <div ref="providerMenuRef" class="relative">
          <span class="mb-1 block text-xs font-medium text-linear-ink-tertiary">Provider</span>
          <button
            type="button"
            class="flex h-9 w-full items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-left text-sm text-linear-ink outline-none transition-colors hover:border-linear-hairline-strong focus:border-linear-hairline-strong"
            aria-label="Provider"
            aria-haspopup="listbox"
            :aria-expanded="providerMenuOpen"
            data-testid="chat-provider-trigger"
            @click="providerMenuOpen = !providerMenuOpen"
            @keydown.down.prevent="providerMenuOpen = true"
            @keydown.esc.prevent="providerMenuOpen = false"
          >
            <ModelIcon :model="providerIconModel(selectedProvider)" size="16px" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate">{{ selectedProvider }}</span>
            <Icon name="chevronDown" size="sm" class="shrink-0 text-linear-ink-tertiary" />
          </button>
          <div
            v-if="providerMenuOpen"
            class="absolute left-0 top-full z-30 mt-1 w-full overflow-hidden rounded-lg border border-linear-hairline bg-linear-surface-0 py-1 shadow-lg"
            role="listbox"
            aria-label="Provider"
          >
            <button
              v-for="provider in providers"
              :key="provider"
              type="button"
              class="flex h-9 w-full items-center gap-2 px-3 text-left text-sm text-linear-ink transition-colors hover:bg-linear-surface-1"
              :aria-selected="provider === selectedProvider"
              data-testid="chat-provider-option"
              role="option"
              @click="selectProvider(provider)"
            >
              <ModelIcon :model="providerIconModel(provider)" size="16px" aria-hidden="true" />
              <span class="min-w-0 flex-1 truncate">{{ provider }}</span>
              <Icon v-if="provider === selectedProvider" name="check" size="sm" class="text-linear-accent" />
            </button>
          </div>
        </div>
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
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import type { WebChatModel } from '@/api/chat'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const chatStore = useChatStore()
const providerMenuOpen = ref(false)
const providerMenuRef = ref<HTMLElement | null>(null)

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

function selectProvider(provider: string): void {
  selectedProvider.value = provider
  providerMenuOpen.value = false
}

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

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Node)) return
  if (!providerMenuRef.value?.contains(target)) {
    providerMenuOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleDocumentClick)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleDocumentClick)
})
</script>
