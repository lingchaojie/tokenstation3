<template>
  <aside class="flex min-h-0 flex-col border-b border-linear-hairline bg-linear-surface-0 lg:border-b-0" data-testid="chat-conversation-rail">
    <div class="flex items-center gap-2 border-b border-linear-hairline px-3 py-3">
      <button
        class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
        type="button"
        title="New chat"
        aria-label="New chat"
        data-testid="chat-new"
        @click="startNewChat"
      >
        <Icon name="plus" size="sm" />
        <span class="sr-only">New chat</span>
      </button>
      <div class="relative min-w-0 flex-1">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-linear-ink-tertiary" />
        <input
          v-model="query"
          class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-canvas pl-9 pr-3 text-sm text-linear-ink outline-none transition-colors placeholder:text-linear-ink-tertiary focus:border-linear-hairline-strong"
          type="search"
          placeholder="Search chats"
          aria-label="Search chats"
        />
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 py-3">
      <p class="px-2 text-xs font-medium text-linear-ink-tertiary">Recent conversations</p>
      <div v-if="filteredConversations.length === 0" class="px-2 py-8 text-sm leading-6 text-linear-ink-subtle">
        No conversations yet.
      </div>
      <div v-else class="mt-2 space-y-1">
        <div
          v-for="conversation in filteredConversations"
          :key="conversation.id"
          class="group flex items-center gap-1 rounded-lg transition-colors"
          :class="isCurrent(conversation.id) ? 'bg-linear-surface-2' : 'hover:bg-linear-surface-1'"
        >
          <button
            class="min-w-0 flex-1 px-2.5 py-2 text-left"
            type="button"
            @click="chatStore.openConversation(conversation.id)"
          >
            <span class="block truncate text-sm font-medium text-linear-ink">
              {{ conversationTitle(conversation) }}
            </span>
            <span class="mt-0.5 block truncate text-xs text-linear-ink-tertiary">
              {{ conversation.last_model || conversation.default_model || 'No model' }}
            </span>
          </button>
          <button
            class="hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-linear-ink-tertiary transition-colors hover:bg-linear-canvas hover:text-linear-ink group-hover:inline-flex"
            type="button"
            title="Rename"
            aria-label="Rename conversation"
            @click="renameConversation(conversation.id, conversationTitle(conversation))"
          >
            <Icon name="edit" size="xs" />
          </button>
          <button
            class="hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-linear-ink-tertiary transition-colors hover:bg-linear-canvas hover:text-linear-ink group-hover:inline-flex"
            type="button"
            title="Delete"
            aria-label="Delete conversation"
            @click="deleteConversation(conversation.id)"
          >
            <Icon name="trash" size="xs" />
          </button>
        </div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

import type { WebChatConversation } from '@/api/chat'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()
const query = ref('')

const filteredConversations = computed(() => {
  const term = query.value.trim().toLowerCase()
  if (!term) return chatStore.conversations
  return chatStore.conversations.filter((conversation) =>
    conversationTitle(conversation).toLowerCase().includes(term) ||
    conversation.last_model.toLowerCase().includes(term) ||
    conversation.default_model.toLowerCase().includes(term)
  )
})

function conversationTitle(conversation: WebChatConversation): string {
  return conversation.title || 'Untitled chat'
}

function isCurrent(conversationId: number): boolean {
  return chatStore.currentConversation?.conversation.id === conversationId
}

function startNewChat(): void {
  chatStore.currentConversation = null
  chatStore.pendingAttachments = []
  chatStore.error = null
}

async function renameConversation(conversationId: number, currentTitle: string): Promise<void> {
  const nextTitle = window.prompt('Rename conversation', currentTitle)
  if (!nextTitle || nextTitle.trim() === currentTitle) return
  await chatStore.renameConversation(conversationId, nextTitle.trim())
}

async function deleteConversation(conversationId: number): Promise<void> {
  if (!window.confirm('Delete this conversation?')) return
  await chatStore.deleteConversation(conversationId)
}
</script>
