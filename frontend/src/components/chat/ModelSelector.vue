<template>
  <header class="shrink-0 border-b border-linear-hairline bg-linear-canvas px-3 py-2 sm:px-4" data-testid="chat-model-selector">
    <div class="flex flex-col gap-2 md:flex-row md:items-end md:justify-between">
      <div class="grid min-w-0 gap-2 sm:grid-cols-[minmax(8rem,11rem)_minmax(11rem,20rem)]">
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

      <div
        v-if="capabilitySummary"
        class="min-w-0 truncate text-xs text-linear-ink-tertiary md:max-w-xs md:pb-2 md:text-right"
      >
        {{ capabilitySummary }}
      </div>
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

const capabilitySummary = computed(() => {
  const model = chatStore.selectedModel
  if (!model) return ''
  const capabilities: string[] = []
  if (model.supports_image_input) capabilities.push('Images')
  if (model.supports_file_context) capabilities.push('Files')
  if (model.supports_thinking) capabilities.push('Thinking')
  if (model.supports_image_generation) capabilities.push('Generate')
  return capabilities.join(' · ')
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
