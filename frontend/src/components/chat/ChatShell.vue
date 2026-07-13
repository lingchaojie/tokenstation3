<template>
  <section class="chat-page min-h-[calc(100dvh-8rem)] bg-linear-canvas text-linear-ink" data-testid="chat-page">
    <div class="flex h-[calc(100dvh-8rem)] min-h-[620px] flex-col overflow-hidden rounded-lg border border-linear-hairline bg-linear-canvas lg:grid lg:grid-cols-[292px_minmax(0,1fr)]">
      <ConversationRail
        class="min-h-0 flex-1 lg:flex"
        :class="mobilePanel === 'list' ? 'flex' : 'hidden'"
        @new-chat="showChatPanel"
        @open-conversation="showChatPanel"
      />
      <main
        class="min-h-0 min-w-0 flex-1 flex-col border-linear-hairline bg-linear-canvas lg:flex lg:border-l"
        :class="mobilePanel === 'chat' ? 'flex' : 'hidden'"
        data-testid="chat-main-panel"
      >
        <div class="flex shrink-0 items-center gap-2 border-b border-linear-hairline bg-linear-canvas px-3 py-2 lg:hidden">
          <button
            class="inline-flex h-9 items-center gap-1.5 rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 text-sm font-medium text-linear-ink-muted transition-colors hover:bg-linear-surface-2 hover:text-linear-ink"
            type="button"
            data-testid="chat-mobile-back"
            @click="showConversationList"
          >
            <Icon name="chevronLeft" size="sm" />
            <span>{{ t('chat.viewChats') }}</span>
          </button>
          <span class="min-w-0 flex-1 truncate text-sm font-medium text-linear-ink" data-testid="chat-mobile-title">
            {{ mobileTitle }}
          </span>
        </div>
        <ModelSelector />
        <MessageList class="min-h-0 flex-1" />
        <Composer />
      </main>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Composer from '@/components/chat/Composer.vue'
import ConversationRail from '@/components/chat/ConversationRail.vue'
import Icon from '@/components/icons/Icon.vue'
import MessageList from '@/components/chat/MessageList.vue'
import ModelSelector from '@/components/chat/ModelSelector.vue'
import { useChatStore } from '@/stores/chat'

const { t } = useI18n()
const chatStore = useChatStore()

const props = withDefaults(defineProps<{
  initialMobilePanel?: 'list' | 'chat'
}>(), {
  initialMobilePanel: 'list',
})

const mobilePanel = ref<'list' | 'chat'>(props.initialMobilePanel)

const mobileTitle = computed(() => {
  const conversation = chatStore.currentConversation?.conversation
  if (conversation?.title) return conversation.title

  const conversationModel = conversation?.last_model || conversation?.default_model || ''
  const conversationProvider = conversation?.last_provider || conversation?.default_provider || ''
  if (conversationModel) {
    return chatStore.getModelDisplayName(conversationProvider, conversationModel)
  }

  const selected = chatStore.selectedModel
  if (selected) {
    return chatStore.getModelDisplayName(selected.provider, selected.model, selected.display_name)
  }
  return t('chat.chatFallbackTitle')
})

function showChatPanel(): void {
  mobilePanel.value = 'chat'
}

function showConversationList(): void {
  mobilePanel.value = 'list'
}

watch(() => props.initialMobilePanel, (panel) => {
  if (panel === 'chat') {
    mobilePanel.value = 'chat'
  }
})
</script>
