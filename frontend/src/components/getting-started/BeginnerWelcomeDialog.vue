<template>
  <BaseDialog
    :show="show"
    :title="t('gettingStarted.welcome.title')"
    :close-aria-label="t('gettingStarted.welcome.closeLabel')"
    width="narrow"
    @close="emit('close')"
  >
    <div data-testid="beginner-welcome-dialog" class="space-y-5">
      <div
        class="flex h-12 w-12 items-center justify-center rounded-xl bg-primary-500/10 text-primary-500 dark:text-primary-400"
        aria-hidden="true"
      >
        <Icon name="book" size="lg" :stroke-width="1.75" />
      </div>

      <p class="text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
        {{ t('gettingStarted.welcome.description') }}
      </p>

      <button
        type="button"
        data-testid="beginner-welcome-start"
        class="btn btn-primary inline-flex w-full items-center justify-center gap-2"
        @click="emit('start')"
      >
        {{ t('gettingStarted.welcome.start') }}
        <Icon name="arrowRight" size="sm" :stroke-width="2" aria-hidden="true" />
      </button>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  (event: 'start'): void
  (event: 'close'): void
}>()

const { t } = useI18n()
</script>
