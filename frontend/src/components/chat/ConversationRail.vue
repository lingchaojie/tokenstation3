<template>
  <aside class="flex min-h-0 flex-col border-b border-linear-hairline bg-linear-surface-0 lg:border-b-0" data-testid="chat-conversation-rail">
    <div class="border-b border-linear-hairline px-3 py-4">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0">
          <h2 class="text-2xl font-semibold text-linear-ink">{{ t('chat.title') }}</h2>
          <p class="mt-1 truncate text-xs text-linear-ink-tertiary">{{ t('chat.description') }}</p>
        </div>
        <div class="flex items-center gap-1">
          <button
            class="inline-flex h-8 w-8 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink"
            type="button"
            :title="t('chat.refresh')"
            :aria-label="t('chat.refreshConversations')"
            @click="refreshConversations"
          >
            <Icon name="refresh" size="sm" />
          </button>
          <button
            class="inline-flex h-8 w-8 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink"
            type="button"
            :title="t('chat.search')"
            :aria-label="t('chat.focusSearch')"
            @click="searchInput?.focus()"
          >
            <Icon name="search" size="sm" />
          </button>
        </div>
      </div>

      <button
        class="mt-4 inline-flex h-9 w-full items-center justify-center gap-2 rounded-lg border border-primary-500/70 bg-linear-canvas px-3 text-sm font-medium text-primary-600 transition-colors hover:bg-primary-500/5 dark:text-primary-300"
        type="button"
        :title="t('chat.newChat')"
        :aria-label="t('chat.newChat')"
        data-testid="chat-new"
        @click="startNewChat"
      >
        <Icon name="plus" size="sm" />
        <span>{{ t('chat.newChat') }}</span>
      </button>

      <div class="mt-3 flex items-center gap-2">
        <div class="relative min-w-0 flex-1">
          <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-linear-ink-tertiary" />
          <input
            ref="searchInput"
            v-model="query"
            class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-canvas pl-9 pr-3 text-sm text-linear-ink outline-none transition-colors placeholder:text-linear-ink-tertiary focus:border-linear-hairline-strong"
            type="search"
            :placeholder="t('chat.searchChats')"
            :aria-label="t('chat.searchChats')"
          />
        </div>
        <div class="inline-flex shrink-0 rounded-lg border border-linear-hairline bg-linear-canvas p-0.5">
          <button
            class="h-7 rounded-md px-2 text-xs font-medium transition-colors"
            :class="groupedView ? 'bg-primary-500/10 text-primary-600 dark:text-primary-300' : 'text-linear-ink-tertiary hover:text-linear-ink'"
            type="button"
            @click="groupedView = true"
          >
            {{ t('chat.viewGroup') }}
          </button>
          <button
            class="h-7 rounded-md px-2 text-xs font-medium transition-colors"
            :class="!groupedView ? 'bg-primary-500/10 text-primary-600 dark:text-primary-300' : 'text-linear-ink-tertiary hover:text-linear-ink'"
            type="button"
            @click="groupedView = false"
          >
            {{ t('chat.viewChats') }}
          </button>
        </div>
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-3 py-3">
      <p class="text-xs font-medium text-linear-ink-tertiary">{{ t('chat.recentlyUsed') }}</p>
      <div v-if="filteredConversations.length === 0" class="px-1 py-8 text-sm leading-6 text-linear-ink-subtle">
        {{ t('chat.noConversations') }}
      </div>

      <div v-else-if="groupedView" class="mt-3 space-y-3">
        <section
          v-for="group in groupedConversations"
          :key="group.model"
          :data-testid="`chat-model-group-${modelGroupId(group.model)}`"
        >
          <div class="flex items-center gap-2">
            <ModelIcon :model="providerIconModel(group.provider || group.model)" size="28px" aria-hidden="true" />
            <button
              class="flex min-w-0 flex-1 items-center justify-between gap-2 rounded-lg px-1.5 py-1 text-left transition-colors hover:bg-linear-surface-1"
              type="button"
              @click="toggleGroup(group.model)"
            >
              <span class="min-w-0 truncate text-sm font-medium text-linear-ink">{{ group.label }}</span>
              <Icon
                name="chevronDown"
                size="xs"
                class="shrink-0 text-linear-ink-tertiary transition-transform"
                :class="collapsedGroups.has(group.model) ? '-rotate-90' : ''"
              />
            </button>
          </div>

          <div v-if="!collapsedGroups.has(group.model)" class="ml-3 mt-1 border-l border-linear-hairline pl-3">
            <ConversationRow
              v-for="conversation in group.conversations"
              :key="conversation.id"
              :conversation="conversation"
              :current="isCurrent(conversation.id)"
              :editing="editingConversationId === conversation.id"
              :editing-title="editingTitle"
              :saving="editingSaving"
              @open="openConversation(conversation.id)"
              @rename="beginRename(conversation.id, conversationTitle(conversation))"
              @update-title="editingTitle = $event"
              @save="saveRename"
              @cancel="cancelRename"
              @delete="deleteConversation(conversation.id)"
            />
          </div>
        </section>
      </div>

      <div v-else class="mt-2 space-y-1">
        <ConversationRow
          v-for="conversation in filteredConversations"
          :key="conversation.id"
          :conversation="conversation"
          :current="isCurrent(conversation.id)"
          :editing="editingConversationId === conversation.id"
          :editing-title="editingTitle"
          :saving="editingSaving"
          @open="openConversation(conversation.id)"
          @rename="beginRename(conversation.id, conversationTitle(conversation))"
          @update-title="editingTitle = $event"
          @save="saveRename"
          @cancel="cancelRename"
          @delete="deleteConversation(conversation.id)"
        />
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { WebChatConversation } from '@/api/chat'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChatStore } from '@/stores/chat'
import { providerIconModel } from '@/utils/modelCatalog'

const { t } = useI18n()
const chatStore = useChatStore()
const query = ref('')
const groupedView = ref(true)
const searchInput = ref<HTMLInputElement | null>(null)
const collapsedGroups = ref(new Set<string>())
const editingConversationId = ref<number | null>(null)
const editingTitle = ref('')
const editingSaving = ref(false)

const emit = defineEmits<{
  (event: 'new-chat'): void
  (event: 'open-conversation'): void
}>()

function conversationModel(conversation: WebChatConversation): { provider: string; model: string } {
  return {
    provider: conversation.last_provider || conversation.default_provider || '',
    model: conversation.last_model || conversation.default_model || '',
  }
}

function conversationModelLabel(conversation: WebChatConversation): string {
  const { provider, model } = conversationModel(conversation)
  return chatStore.getModelDisplayName(provider, model)
}

const filteredConversations = computed(() => {
  const term = query.value.trim().toLowerCase()
  if (!term) return chatStore.conversations
  return chatStore.conversations.filter((conversation) =>
    conversationTitle(conversation).toLowerCase().includes(term) ||
    conversation.last_model.toLowerCase().includes(term) ||
    conversation.default_model.toLowerCase().includes(term) ||
    conversationModelLabel(conversation).toLowerCase().includes(term)
  )
})

const groupedConversations = computed(() => {
  const groups = new Map<string, {
    model: string
    provider: string
    label: string
    conversations: WebChatConversation[]
  }>()

  for (const conversation of filteredConversations.value) {
    const { provider, model } = conversationModel(conversation)
    const existing = groups.get(model)
    if (existing) {
      existing.conversations.push(conversation)
    } else {
      groups.set(model, {
        model,
        provider,
        label: chatStore.getModelDisplayName(provider, model) || model,
        conversations: [conversation],
      })
    }
  }

  return Array.from(groups.values())
})

const ConversationRow = defineComponent({
  name: 'ConversationRow',
  props: {
    conversation: {
      type: Object as () => WebChatConversation,
      required: true,
    },
    current: {
      type: Boolean,
      default: false,
    },
    editing: {
      type: Boolean,
      default: false,
    },
    editingTitle: {
      type: String,
      default: '',
    },
    saving: {
      type: Boolean,
      default: false,
    },
  },
  emits: ['open', 'rename', 'update-title', 'save', 'cancel', 'delete'],
  setup(props, { emit }) {
    return () => h('div', {
      class: [
        'group flex items-center gap-1 rounded-lg transition-colors',
        props.current ? 'bg-primary-500/10 text-primary-600 dark:text-primary-300' : 'text-linear-ink hover:bg-linear-surface-1',
      ],
    }, props.editing ? [
      h('div', {
        class: 'flex min-w-0 flex-1 items-center gap-1 px-1.5 py-1.5',
        onFocusout: (event: FocusEvent) => {
          const nextTarget = event.relatedTarget as Node | null
          if (nextTarget && (event.currentTarget as HTMLElement).contains(nextTarget)) return
          emit('save')
        },
      }, [
        h('input', {
          class: 'h-8 min-w-0 flex-1 rounded-md border border-linear-hairline-strong bg-linear-canvas px-2 text-sm text-linear-ink outline-none',
          value: props.editingTitle,
          'aria-label': t('chat.renameConversation'),
          disabled: props.saving,
          onInput: (event: Event) => emit('update-title', (event.target as HTMLInputElement).value),
          onKeydown: (event: KeyboardEvent) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              emit('save')
            } else if (event.key === 'Escape') {
              event.preventDefault()
              emit('cancel')
            }
          },
        }),
        h('button', {
          class: 'inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-primary-600 transition-colors hover:bg-primary-500/10 disabled:opacity-50 dark:text-primary-300',
          type: 'button',
          title: t('chat.saveRename'),
          'aria-label': t('chat.saveRename'),
          disabled: props.saving,
          onMousedown: (event: MouseEvent) => event.preventDefault(),
          onClick: () => emit('save'),
        }, [h(Icon, { name: 'check', size: 'xs' })]),
        h('button', {
          class: 'inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-linear-ink-tertiary transition-colors hover:bg-linear-canvas hover:text-linear-ink disabled:opacity-50',
          type: 'button',
          title: t('chat.cancelRename'),
          'aria-label': t('chat.cancelRename'),
          disabled: props.saving,
          onMousedown: (event: MouseEvent) => event.preventDefault(),
          onClick: () => emit('cancel'),
        }, [h(Icon, { name: 'x', size: 'xs' })]),
      ]),
    ] : [
      h('button', {
        class: 'min-w-0 flex-1 px-2.5 py-2 text-left',
        type: 'button',
        'data-testid': `chat-conversation-open-${props.conversation.id}`,
        onClick: () => emit('open'),
      }, [
        h('span', {
          class: 'block truncate text-sm font-medium',
        }, conversationTitle(props.conversation)),
        h('span', {
          class: 'mt-0.5 block truncate text-xs text-linear-ink-tertiary',
        }, conversationModelLabel(props.conversation) || t('chat.noModel')),
      ]),
      h('button', {
        class: 'hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-linear-ink-tertiary transition-colors hover:bg-linear-canvas hover:text-linear-ink group-hover:inline-flex',
        type: 'button',
        title: t('chat.rename'),
        'aria-label': t('chat.renameConversation'),
        onClick: () => emit('rename'),
      }, [h(Icon, { name: 'edit', size: 'xs' })]),
      h('button', {
        class: 'hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-linear-ink-tertiary transition-colors hover:bg-linear-canvas hover:text-linear-ink group-hover:inline-flex',
        type: 'button',
        title: t('chat.deleteAction'),
        'aria-label': t('chat.deleteConversation'),
        onClick: () => emit('delete'),
      }, [h(Icon, { name: 'trash', size: 'xs' })]),
    ])
  },
})

function conversationTitle(conversation: WebChatConversation): string {
  return conversation.title || t('chat.untitledChat')
}

function modelGroupId(model: string): string {
  return model.toLowerCase().replace(/[^a-z0-9._-]+/g, '-')
}

function isCurrent(conversationId: number): boolean {
  return chatStore.currentConversation?.conversation.id === conversationId
}

function toggleGroup(model: string): void {
  const next = new Set(collapsedGroups.value)
  if (next.has(model)) {
    next.delete(model)
  } else {
    next.add(model)
  }
  collapsedGroups.value = next
}

function startNewChat(): void {
  chatStore.currentConversation = null
  chatStore.pendingAttachments = []
  chatStore.error = null
  emit('new-chat')
}

async function refreshConversations(): Promise<void> {
  await chatStore.loadConversations()
}

async function openConversation(conversationId: number): Promise<void> {
  await chatStore.openConversation(conversationId)
  emit('open-conversation')
}

function beginRename(conversationId: number, currentTitle: string): void {
  editingConversationId.value = conversationId
  editingTitle.value = currentTitle
}

function cancelRename(): void {
  editingConversationId.value = null
  editingTitle.value = ''
}

async function saveRename(): Promise<void> {
  const conversationId = editingConversationId.value
  if (!conversationId || editingSaving.value) return
  const current = chatStore.conversations.find((conversation) => conversation.id === conversationId)
  const currentTitle = current ? conversationTitle(current) : ''
  const nextTitle = editingTitle.value.trim()
  if (!nextTitle || nextTitle === currentTitle) {
    cancelRename()
    return
  }
  editingSaving.value = true
  try {
    await chatStore.renameConversation(conversationId, nextTitle)
    cancelRename()
  } finally {
    editingSaving.value = false
  }
}

async function deleteConversation(conversationId: number): Promise<void> {
  if (!window.confirm(t('chat.deleteConfirm'))) return
  await chatStore.deleteConversation(conversationId)
}
</script>
