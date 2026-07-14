<template>
  <div
    data-testid="guide-command-block"
    class="min-w-0 max-w-full overflow-hidden rounded-xl border border-gray-200 bg-gray-950 dark:border-linear-hairline"
  >
    <div class="flex items-center justify-end border-b border-gray-800 px-3 py-2">
      <button
        type="button"
        class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-300 transition-colors hover:bg-white/10 hover:text-white disabled:cursor-wait disabled:opacity-60"
        :disabled="copying"
        @click="copyCommand"
      >
        <Icon name="copy" size="sm" aria-hidden="true" />
        {{ t('gettingStarted.chrome.copy') }}
      </button>
    </div>
    <pre class="max-w-full overflow-x-auto p-4 text-sm leading-6 text-gray-100"><code
      class="select-text whitespace-pre font-mono"
      v-text="command"
    ></code></pre>
    <p
      class="min-h-6 border-t border-gray-800 px-4 py-2 text-xs text-gray-300"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ statusText }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  command: string
}>()

const { t } = useI18n()
const status = ref<'idle' | 'copied' | 'failed'>('idle')
const copying = ref(false)

const statusText = computed(() => {
  if (status.value === 'copied') {
    return t('gettingStarted.chrome.copied')
  }
  if (status.value === 'failed') {
    return `${t('gettingStarted.chrome.copyFailed')}. ${t('gettingStarted.chrome.manualCopy')}`
  }
  return ''
})

async function copyCommand(): Promise<void> {
  copying.value = true
  status.value = 'idle'
  try {
    if (!navigator.clipboard?.writeText) {
      throw new Error('Clipboard API unavailable')
    }
    await navigator.clipboard.writeText(props.command)
    status.value = 'copied'
  } catch {
    status.value = 'failed'
  } finally {
    copying.value = false
  }
}
</script>
