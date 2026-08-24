<template>
  <div class="flex flex-wrap gap-2">
    <button
      v-for="preset in CURSOR_BASE_URL_PRESETS"
      :key="preset.url"
      type="button"
      class="rounded-md border border-gray-200 px-2.5 py-1 text-xs text-gray-600 transition-colors hover:border-indigo-400 hover:text-indigo-600 dark:border-dark-600 dark:text-gray-400"
      @click="emit('select', preset.url)"
    >
      {{ preset.labelKey ? t(`admin.accounts.cursorCustomBaseUrl.presets.${preset.labelKey}`) : preset.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { CURSOR_BASE_URL_PRESETS } from './credentialsBuilder'

const emit = defineEmits<{ select: [url: string] }>()
const { t } = useI18n()
</script>
