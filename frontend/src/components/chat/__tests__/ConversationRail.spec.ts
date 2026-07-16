import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => ({
      'chat.title': 'Chat',
      'chat.description': 'Conversations',
      'chat.refresh': 'Refresh',
      'chat.refreshConversations': 'Refresh conversations',
      'chat.search': 'Search',
      'chat.focusSearch': 'Focus search',
      'chat.newChat': 'New chat',
      'chat.searchChats': 'Search chats',
      'chat.viewGroup': 'Group',
      'chat.viewChats': 'Chats',
      'chat.recentlyUsed': 'Recently used',
      'chat.noConversations': 'No conversations',
      'chat.noModel': 'No model',
      'chat.rename': 'Rename',
      'chat.renameConversation': 'Rename conversation',
      'chat.saveRename': 'Save title',
      'chat.cancelRename': 'Cancel rename',
      'chat.deleteAction': 'Delete',
      'chat.deleteConversation': 'Delete conversation',
      'chat.deleteConfirm': 'Delete?',
      'chat.untitledChat': 'Untitled',
      }[key] ?? key),
    }),
  }
})

import ConversationRail from '@/components/chat/ConversationRail.vue'
import { useChatStore } from '@/stores/chat'

describe('ConversationRail', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renames a conversation with an inline input and save button', async () => {
    const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('Prompt title')
    const store = useChatStore()
    store.conversations = [{
      id: 7,
      title: 'Old title',
      default_model: 'gpt-5.4',
      default_provider: 'openai',
      last_model: 'gpt-5.4',
      last_provider: 'openai',
      status: 'active',
      message_count: 2,
      created_at: '2026-06-22T00:00:00Z',
      updated_at: '2026-06-22T00:00:00Z',
    }]
    const renameSpy = vi.spyOn(store, 'renameConversation').mockResolvedValue({
      ...store.conversations[0],
      title: 'New title',
    })

    const wrapper = mount(ConversationRail, {
      global: {
        stubs: {
          Icon: { template: '<span />' },
          ModelIcon: { template: '<span />' },
        },
      },
    })
    await wrapper.get('button[aria-label="Rename conversation"]').trigger('click')

    expect(promptSpy).not.toHaveBeenCalled()
    const input = wrapper.get('input[aria-label="Rename conversation"]')
    await input.setValue('New title')
    await wrapper.get('button[aria-label="Save title"]').trigger('click')

    expect(renameSpy).toHaveBeenCalledWith(7, 'New title')
  })
})
