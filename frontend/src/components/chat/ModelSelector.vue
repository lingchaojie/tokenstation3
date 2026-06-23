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

      <div v-if="chatStore.selectedModel" class="flex flex-col gap-2 lg:items-end">
        <div class="flex flex-wrap gap-2">
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
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="inline-flex h-9 items-center gap-2 rounded-lg border px-3 text-sm font-medium outline-none transition-colors disabled:cursor-not-allowed disabled:opacity-50"
            :class="chatStore.thinkingEnabled && chatStore.selectedModelSupportsThinking
              ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
              : 'border-linear-hairline bg-linear-surface-1 text-linear-ink-muted hover:border-linear-hairline-strong'"
            :aria-pressed="chatStore.thinkingEnabled && chatStore.selectedModelSupportsThinking ? 'true' : 'false'"
            :disabled="!chatStore.selectedModelSupportsThinking"
            aria-label="Thinking"
            data-testid="chat-thinking-toggle"
            @click="toggleThinking"
          >
            <Icon name="brain" size="sm" />
            <span>Thinking</span>
          </button>
          <label class="block">
            <span class="sr-only">Thinking effort</span>
            <select
              v-model="chatStore.thinkingEffort"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.selectedModelSupportsThinking || !chatStore.thinkingEnabled"
              aria-label="Thinking effort"
              data-testid="chat-thinking-effort"
            >
              <option
                v-for="effort in chatStore.thinkingEffortOptions"
                :key="effort"
                :value="effort"
              >
                {{ thinkingEffortLabel(effort) }}
              </option>
            </select>
          </label>
        </div>
        <div
          v-if="chatStore.selectedModelSupportsImageGeneration"
          class="flex flex-wrap items-center gap-2"
        >
          <button
            type="button"
            class="inline-flex h-9 items-center gap-2 rounded-lg border px-3 text-sm font-medium outline-none transition-colors"
            :class="chatStore.imageGenerationEnabled
              ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
              : 'border-linear-hairline bg-linear-surface-1 text-linear-ink-muted hover:border-linear-hairline-strong'"
            :aria-pressed="chatStore.imageGenerationEnabled ? 'true' : 'false'"
            aria-label="Image generation"
            data-testid="chat-image-generation-toggle"
            @click="toggleImageGeneration"
          >
            <Icon name="image" size="sm" />
            <span>Generate</span>
          </button>
          <label v-if="chatStore.imageGenerationSizeOptions.length > 0" class="block">
            <span class="sr-only">Image generation size</span>
            <select
              v-model="chatStore.imageGenerationSize"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.imageGenerationEnabled"
              aria-label="Image generation size"
              data-testid="chat-image-generation-size"
            >
              <option
                v-for="size in chatStore.imageGenerationSizeOptions"
                :key="size"
                :value="size"
              >
                {{ size }}
              </option>
            </select>
          </label>
          <label v-if="chatStore.imageGenerationAspectRatioOptions.length > 0" class="block">
            <span class="sr-only">Image generation aspect ratio</span>
            <select
              v-model="chatStore.imageGenerationAspectRatio"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.imageGenerationEnabled"
              aria-label="Image generation aspect ratio"
              data-testid="chat-image-generation-aspect-ratio"
            >
              <option
                v-for="aspectRatio in chatStore.imageGenerationAspectRatioOptions"
                :key="aspectRatio"
                :value="aspectRatio"
              >
                {{ aspectRatio }}
              </option>
            </select>
          </label>
          <label v-if="chatStore.imageGenerationQualityOptions.length > 0" class="block">
            <span class="sr-only">Image generation quality</span>
            <select
              v-model="chatStore.imageGenerationQuality"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.imageGenerationEnabled"
              aria-label="Image generation quality"
              data-testid="chat-image-generation-quality"
            >
              <option
                v-for="quality in chatStore.imageGenerationQualityOptions"
                :key="quality"
                :value="quality"
              >
                {{ imageGenerationLabel(quality) }}
              </option>
            </select>
          </label>
          <label v-if="chatStore.imageGenerationOutputFormatOptions.length > 0" class="block">
            <span class="sr-only">Image generation output format</span>
            <select
              v-model="chatStore.imageGenerationOutputFormat"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.imageGenerationEnabled"
              aria-label="Image generation output format"
              data-testid="chat-image-generation-output-format"
            >
              <option
                v-for="format in chatStore.imageGenerationOutputFormatOptions"
                :key="format"
                :value="format"
              >
                {{ format.toUpperCase() }}
              </option>
            </select>
          </label>
          <label v-if="chatStore.imageGenerationBackgroundOptions.length > 0" class="block">
            <span class="sr-only">Image generation background</span>
            <select
              v-model="chatStore.imageGenerationBackground"
              class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="!chatStore.imageGenerationEnabled"
              aria-label="Image generation background"
              data-testid="chat-image-generation-background"
            >
              <option
                v-for="background in chatStore.imageGenerationBackgroundOptions"
                :key="background"
                :value="background"
              >
                {{ imageGenerationLabel(background) }}
              </option>
            </select>
          </label>
        </div>
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
    { label: 'Thinking', enabled: model.supports_thinking },
    { label: 'Generate', enabled: model.supports_image_generation },
  ]
})

function toggleThinking(): void {
  if (!chatStore.selectedModelSupportsThinking) return
  chatStore.thinkingEnabled = !chatStore.thinkingEnabled
}

function toggleImageGeneration(): void {
  if (!chatStore.selectedModelSupportsImageGeneration) return
  chatStore.imageGenerationEnabled = !chatStore.imageGenerationEnabled
}

function thinkingEffortLabel(effort: string): string {
  switch (effort) {
    case 'low':
      return 'Low'
    case 'medium':
      return 'Medium'
    case 'high':
      return 'High'
    case 'xhigh':
      return 'Max'
    default:
      return effort
  }
}

function imageGenerationLabel(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

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
