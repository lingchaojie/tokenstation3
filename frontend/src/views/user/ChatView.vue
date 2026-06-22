<template>
  <AppLayout>
    <ChatShell />
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'

import ChatShell from '@/components/chat/ChatShell.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()

onMounted(async () => {
  await Promise.all([
    chatStore.loadModels(),
    chatStore.loadConversations(),
  ])
})
</script>
