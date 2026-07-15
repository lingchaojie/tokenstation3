<template>
  <div
    data-testid="api-docs-code-block"
    class="min-w-0 max-w-full overflow-hidden rounded-xl border border-gray-200 bg-gray-950 shadow-sm dark:border-linear-hairline"
  >
    <div class="flex items-center justify-between gap-3 border-b border-gray-800 px-3 py-2">
      <span class="truncate text-xs font-semibold uppercase tracking-wide text-gray-400">
        {{ label }}
      </span>
      <button
        type="button"
        data-testid="api-docs-copy"
        class="inline-flex shrink-0 items-center rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-300 outline-none transition-colors hover:bg-white/10 hover:text-white focus-visible:ring-2 focus-visible:ring-primary-400/70 focus-visible:ring-offset-2 focus-visible:ring-offset-gray-950 motion-reduce:transition-none"
        :aria-label="t('apiDocs.copy')"
        @click="copy"
      >
        {{ copied ? t('apiDocs.copied') : t('apiDocs.copy') }}
      </button>
    </div>

    <pre class="max-w-full overflow-x-auto p-4 text-sm leading-6 text-gray-100"><code
      :class="[`language-${language}`, 'select-text whitespace-pre font-mono']"
      :data-language="language"
      v-text="code"
    ></code></pre>

    <p
      role="status"
      aria-live="polite"
      aria-atomic="true"
      class="min-h-8 border-t border-gray-800 px-4 py-2 text-xs text-gray-300"
    >
      {{ copied ? t('apiDocs.copied') : '' }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useClipboard } from '@/composables/useClipboard'

const props = defineProps<{
  label: string
  language: string
  code: string
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const copied = ref(false)
let resetTimer: number | undefined

async function copy(): Promise<void> {
  if (!await copyToClipboard(props.code, t('apiDocs.copied'))) return
  copied.value = true
  if (resetTimer !== undefined) window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => { copied.value = false }, 2000)
}

onBeforeUnmount(() => {
  if (resetTimer !== undefined) window.clearTimeout(resetTimer)
})
</script>
