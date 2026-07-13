<template>
  <header
    class="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-linear-hairline bg-linear-canvas px-4"
    data-testid="chat-model-selector"
  >
    <div class="min-w-0">
      <div class="flex min-w-0 items-center gap-2">
        <h1 class="truncate text-sm font-semibold text-linear-ink">
          {{ conversationTitle }}
        </h1>
        <Icon name="edit" size="xs" class="hidden shrink-0 text-linear-ink-tertiary sm:block" />
      </div>
      <p class="mt-0.5 truncate text-xs text-linear-ink-tertiary">
        {{ subtitle }}
      </p>
    </div>

    <div
      v-if="chatStore.selectedModel"
      class="hidden shrink-0 items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1.5 text-xs text-linear-ink-muted sm:inline-flex"
      data-testid="chat-current-model-chip"
    >
      <ModelIcon :model="providerIconModel(chatStore.selectedModel.provider)" size="16px" aria-hidden="true" />
      <span class="max-w-44 truncate">{{ modelLabel }}</span>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const { t } = useI18n()
const chatStore = useChatStore()

const conversationTitle = computed(() => {
  const conversation = chatStore.currentConversation?.conversation
  return conversation?.title || t('chat.newChat')
})

const modelLabel = computed(() => {
  const model = chatStore.selectedModel
  if (!model) return t('chat.selectModel')
  return chatStore.getModelDisplayName(model.provider, model.model, model.display_name) || t('chat.selectModel')
})

const subtitle = computed(() => {
  const capabilities: string[] = []
  const model = chatStore.selectedModel
  if (!model) return t('chat.chooseModelInComposer')
  if (model.supports_image_input) capabilities.push(t('chat.capabilityImages'))
  if (model.supports_file_context) capabilities.push(t('chat.capabilityFiles'))
  if (model.supports_thinking) capabilities.push('Thinking')
  if (model.supports_image_generation) capabilities.push(t('chat.capabilityGenerate'))
  return capabilities.length > 0 ? capabilities.join(' · ') : modelLabel.value
})
</script>
