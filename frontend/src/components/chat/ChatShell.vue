<template>
  <section class="chat-page min-h-[calc(100vh-4rem)] bg-linear-canvas text-linear-ink" data-testid="chat-page">
    <div class="grid h-[calc(100vh-4rem)] min-h-[640px] grid-cols-1 lg:grid-cols-[280px_minmax(0,1fr)]">
      <ConversationRail />
      <main class="flex min-w-0 flex-col border-linear-hairline bg-linear-canvas lg:border-l">
        <ModelSelector />
        <MessageList class="min-h-0 flex-1" />
        <Composer />
      </main>
    </div>
  </section>
</template>

<script setup lang="ts">
import Composer from '@/components/chat/Composer.vue'
import ConversationRail from '@/components/chat/ConversationRail.vue'
import MessageList from '@/components/chat/MessageList.vue'
import ModelSelector from '@/components/chat/ModelSelector.vue'
</script>
