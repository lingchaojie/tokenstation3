<template>
  <div class="min-h-screen bg-linear-canvas text-linear-ink" data-testid="chat-immersive-view">
    <ChatShell :initial-mobile-panel="hasRouteModel ? 'chat' : 'list'" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'

import ChatShell from '@/components/chat/ChatShell.vue'
import { useChatStore } from '@/stores/chat'

const chatStore = useChatStore()
const route = useRoute()

function queryStringValue(value: unknown): string {
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

function selectRouteModel(): void {
  const provider = queryStringValue(route.query.provider).trim()
  const model = queryStringValue(route.query.model).trim()
  if (!provider || !model) return

  chatStore.selectModel(provider, model)
}

const hasRouteModel = computed(() => {
  return Boolean(queryStringValue(route.query.provider).trim() && queryStringValue(route.query.model).trim())
})

onMounted(async () => {
  await Promise.all([
    chatStore.loadModels(),
    chatStore.loadConversations(),
  ])
  selectRouteModel()
})

watch(
  () => [route.query.provider, route.query.model, chatStore.models.length],
  () => {
    selectRouteModel()
  },
)
</script>
