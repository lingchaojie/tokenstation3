<template>
  <footer class="shrink-0 bg-linear-canvas px-3 pb-4 pt-2 sm:px-4" data-testid="chat-composer">
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

      <div class="relative rounded-lg border border-linear-hairline bg-linear-canvas p-2 shadow-sm focus-within:border-linear-hairline-strong">
        <div
          v-if="modelMenuOpen"
          ref="modelMenuRef"
          class="absolute bottom-full left-0 z-40 mb-2 w-[min(22rem,calc(100vw-2rem))] rounded-lg border border-linear-hairline bg-linear-surface-0 p-3 shadow-xl"
          data-testid="chat-model-menu"
        >
          <div class="mb-3 text-xs font-medium text-linear-ink-tertiary">Model</div>
          <div class="grid gap-2">
            <div ref="providerMenuRef" class="relative">
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
                <span class="min-w-0 flex-1 truncate">{{ selectedProvider || 'Provider' }}</span>
                <Icon name="chevronDown" size="sm" class="shrink-0 text-linear-ink-tertiary" />
              </button>
              <div
                v-if="providerMenuOpen"
                class="absolute left-0 top-full z-50 mt-1 w-full overflow-hidden rounded-lg border border-linear-hairline bg-linear-surface-0 py-1 shadow-lg"
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
              <span class="sr-only">Model</span>
              <select
                v-model="selectedModelKey"
                class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong"
                aria-label="Model"
                data-testid="chat-model-select"
              >
                <option v-for="model in modelOptions" :key="modelKey(model)" :value="modelKey(model)">
                  {{ model.display_name || model.model }}
                </option>
              </select>
            </label>
          </div>
        </div>

        <div
          v-if="optionsOpen && hasModelOptions"
          class="absolute bottom-full left-0 z-30 mb-2 grid w-[min(40rem,calc(100vw-2rem))] gap-2 rounded-lg border border-linear-hairline bg-linear-surface-0 p-3 shadow-xl sm:grid-cols-2"
          data-testid="chat-options-panel"
        >
          <div v-if="chatStore.selectedModelSupportsThinking" class="flex min-w-0 items-center gap-2">
            <button
              type="button"
              class="inline-flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-sm font-medium outline-none transition-colors"
              :class="chatStore.thinkingEnabled
                ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
                : 'border-linear-hairline bg-linear-surface-1 text-linear-ink-muted hover:border-linear-hairline-strong'"
              :aria-pressed="chatStore.thinkingEnabled ? 'true' : 'false'"
              aria-label="Deep thinking"
              data-testid="chat-thinking-toggle"
              @click="toggleThinking"
            >
              <Icon name="brain" size="sm" />
              <span>深度思考</span>
            </button>
            <span class="min-w-0 truncate text-xs text-linear-ink-tertiary">
              {{ chatStore.thinkingEnabled ? '使用该模型最高思考档位' : '关闭' }}
            </span>
          </div>

          <div v-if="chatStore.selectedModelSupportsImageGeneration" class="grid min-w-0 gap-2 sm:col-span-2">
            <div class="flex min-w-0 items-center gap-2">
              <button
                type="button"
                class="inline-flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-sm font-medium outline-none transition-colors"
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
            </div>
            <div class="grid min-w-0 gap-2 sm:grid-cols-3">
              <label v-if="chatStore.imageGenerationSizeOptions.length > 0" class="block min-w-0">
                <span class="sr-only">Image generation size</span>
                <select
                  v-model="chatStore.imageGenerationSize"
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!chatStore.imageGenerationEnabled"
                  aria-label="Image generation size"
                  data-testid="chat-image-generation-size"
                >
                  <option v-for="size in chatStore.imageGenerationSizeOptions" :key="size" :value="size">
                    {{ size }}
                  </option>
                </select>
              </label>
              <label v-if="chatStore.imageGenerationAspectRatioOptions.length > 0" class="block min-w-0">
                <span class="sr-only">Image generation aspect ratio</span>
                <select
                  v-model="chatStore.imageGenerationAspectRatio"
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!chatStore.imageGenerationEnabled"
                  aria-label="Image generation aspect ratio"
                  data-testid="chat-image-generation-aspect-ratio"
                >
                  <option v-for="aspectRatio in chatStore.imageGenerationAspectRatioOptions" :key="aspectRatio" :value="aspectRatio">
                    {{ aspectRatio }}
                  </option>
                </select>
              </label>
              <label v-if="chatStore.imageGenerationQualityOptions.length > 0" class="block min-w-0">
                <span class="sr-only">Image generation quality</span>
                <select
                  v-model="chatStore.imageGenerationQuality"
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!chatStore.imageGenerationEnabled"
                  aria-label="Image generation quality"
                  data-testid="chat-image-generation-quality"
                >
                  <option v-for="quality in chatStore.imageGenerationQualityOptions" :key="quality" :value="quality">
                    {{ optionLabel(quality) }}
                  </option>
                </select>
              </label>
              <label v-if="chatStore.imageGenerationOutputFormatOptions.length > 0" class="block min-w-0">
                <span class="sr-only">Image generation output format</span>
                <select
                  v-model="chatStore.imageGenerationOutputFormat"
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!chatStore.imageGenerationEnabled"
                  aria-label="Image generation output format"
                  data-testid="chat-image-generation-output-format"
                >
                  <option v-for="format in chatStore.imageGenerationOutputFormatOptions" :key="format" :value="format">
                    {{ format.toUpperCase() }}
                  </option>
                </select>
              </label>
              <label v-if="chatStore.imageGenerationBackgroundOptions.length > 0" class="block min-w-0">
                <span class="sr-only">Image generation background</span>
                <select
                  v-model="chatStore.imageGenerationBackground"
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none transition-colors focus:border-linear-hairline-strong disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!chatStore.imageGenerationEnabled"
                  aria-label="Image generation background"
                  data-testid="chat-image-generation-background"
                >
                  <option v-for="background in chatStore.imageGenerationBackgroundOptions" :key="background" :value="background">
                    {{ optionLabel(background) }}
                  </option>
                </select>
              </label>
            </div>
          </div>
        </div>

        <textarea
          v-model="draft"
          class="max-h-44 min-h-[92px] w-full resize-none bg-transparent px-2 py-2 text-sm leading-6 text-linear-ink outline-none placeholder:text-linear-ink-tertiary disabled:cursor-not-allowed disabled:opacity-60"
          placeholder="Message, paste context, or describe what to generate"
          aria-label="Message"
          :disabled="chatStore.streaming"
          @keydown.enter="handleComposerEnter"
        />

        <div class="flex items-center justify-between gap-3 border-t border-linear-hairline/70 px-1 pt-2">
          <div class="flex min-w-0 items-center gap-1.5">
            <button
              class="inline-flex h-9 max-w-[13rem] items-center gap-2 rounded-lg px-2.5 text-sm font-medium text-linear-ink transition-colors hover:bg-linear-surface-1"
              type="button"
              title="Model"
              aria-label="Model"
              data-testid="chat-model-menu-toggle"
              :aria-expanded="modelMenuOpen ? 'true' : 'false'"
              @click="toggleModelMenu"
            >
              <ModelIcon
                v-if="chatStore.selectedModel"
                :model="providerIconModel(chatStore.selectedModel.provider)"
                size="16px"
                aria-hidden="true"
              />
              <Icon v-else name="cpu" size="sm" />
              <span class="min-w-0 truncate">{{ selectedModelLabel }}</span>
              <Icon name="chevronDown" size="xs" class="shrink-0 text-linear-ink-tertiary" />
            </button>
            <button
              v-if="hasModelOptions"
              class="inline-flex h-9 items-center gap-2 rounded-lg px-2.5 text-sm text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink"
              type="button"
              title="Options"
              aria-label="Options"
              data-testid="chat-options-toggle"
              :aria-expanded="optionsOpen ? 'true' : 'false'"
              @click="toggleOptions"
            >
              <Icon name="cog" size="sm" />
              <span class="hidden sm:inline">Options</span>
            </button>
            <button
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink disabled:cursor-not-allowed disabled:opacity-50"
              type="button"
              title="Upload image"
              aria-label="Upload image"
              :disabled="chatStore.streaming || uploading"
              @click="imageInput?.click()"
            >
              <Icon name="upload" size="sm" />
            </button>
            <button
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink disabled:cursor-not-allowed disabled:opacity-50"
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
              <Icon name="send" size="sm" />
            </button>
            <button
              v-if="chatStore.streaming"
              class="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-linear-hairline bg-linear-canvas text-linear-ink transition-colors hover:bg-linear-surface-1"
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
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import type { WebChatModel } from '@/api/chat'
import AttachmentChip from '@/components/chat/AttachmentChip.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const chatStore = useChatStore()
const draft = ref('')
const uploading = ref(false)
const optionsOpen = ref(false)
const modelMenuOpen = ref(false)
const providerMenuOpen = ref(false)
const modelMenuRef = ref<HTMLElement | null>(null)
const providerMenuRef = ref<HTMLElement | null>(null)
const imageInput = ref<HTMLInputElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

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

const selectedModelLabel = computed(() => {
  const model = chatStore.selectedModel
  return model?.display_name || model?.model || 'Select model'
})

const hasDraft = computed(() => draft.value.trim().length > 0 || chatStore.pendingAttachments.length > 0)
const hasModelOptions = computed(() => chatStore.selectedModelSupportsThinking || chatStore.selectedModelSupportsImageGeneration)
const sendDisabled = computed(() =>
  chatStore.streaming ||
  uploading.value ||
  !hasDraft.value ||
  !chatStore.selectedModel ||
  !!chatStore.capabilityWarning
)

function modelKey(model: WebChatModel): string {
  return `${model.provider}:${model.model}`
}

function selectProvider(provider: string): void {
  selectedProvider.value = provider
  providerMenuOpen.value = false
}

function toggleModelMenu(): void {
  modelMenuOpen.value = !modelMenuOpen.value
  if (modelMenuOpen.value) {
    optionsOpen.value = false
  }
}

function toggleOptions(): void {
  optionsOpen.value = !optionsOpen.value
  if (optionsOpen.value) {
    modelMenuOpen.value = false
    providerMenuOpen.value = false
  }
}

async function submit(): Promise<void> {
  if (sendDisabled.value) return
  const content = draft.value
  draft.value = ''
  try {
    await chatStore.sendMessage(content)
  } catch (err) {
    if (!draft.value) {
      draft.value = content
    }
    throw err
  }
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

function toggleThinking(): void {
  if (!chatStore.selectedModelSupportsThinking) return
  chatStore.thinkingEnabled = !chatStore.thinkingEnabled
}

function toggleImageGeneration(): void {
  if (!chatStore.selectedModelSupportsImageGeneration) return
  chatStore.imageGenerationEnabled = !chatStore.imageGenerationEnabled
}

function optionLabel(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Node)) return
  if (!modelMenuRef.value?.contains(target) && !(event.target instanceof HTMLElement && event.target.closest('[data-testid="chat-model-menu-toggle"]'))) {
    modelMenuOpen.value = false
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
