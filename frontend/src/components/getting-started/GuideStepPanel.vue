<template>
  <article
    class="linx-panel-strong min-w-0 overflow-hidden"
    :data-active-step="stepId"
    :aria-labelledby="`${stepId}-title`"
  >
    <header class="border-b border-gray-200 px-5 py-5 dark:border-linear-hairline sm:px-7">
      <p class="linx-section-kicker">{{ stepNumber }} / {{ stepCount }}</p>
      <h1
        :id="`${stepId}-title`"
        class="mt-2 text-2xl font-semibold tracking-[-0.03em] text-gray-950 dark:text-linear-ink sm:text-3xl"
      >
        {{ title }}
      </h1>
      <p class="mt-3 max-w-3xl text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle sm:text-base">
        {{ description }}
      </p>
    </header>

    <div class="min-w-0 space-y-6 px-5 py-6 sm:px-7">
      <ul v-if="details?.length" class="list-disc space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
        <li v-for="detail in details" :key="detail">{{ detail }}</li>
      </ul>
      <slot />
    </div>

    <footer class="sticky bottom-0 flex items-center justify-between gap-3 border-t border-gray-200 bg-white/95 px-5 py-4 backdrop-blur dark:border-linear-hairline dark:bg-linear-surface-2/95 sm:px-7">
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-linear-hairline dark:text-linear-ink dark:hover:bg-linear-surface-1"
        :disabled="backDisabled"
        @click="emit('back')"
      >
        <Icon name="arrowLeft" size="sm" aria-hidden="true" />
        {{ t('gettingStarted.chrome.back') }}
      </button>
      <button
        type="button"
        data-testid="step-primary-action"
        class="inline-flex items-center gap-2 rounded-lg bg-primary-500 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-primary-400 disabled:cursor-not-allowed disabled:opacity-45"
        :disabled="nextDisabled || nextLoading"
        :aria-busy="nextLoading || undefined"
        @click="emit('next')"
      >
        {{ nextLabel || t('gettingStarted.chrome.next') }}
        <Icon
          :name="nextLoading ? 'refresh' : 'arrowRight'"
          size="sm"
          :class="nextLoading ? 'animate-spin motion-reduce:animate-none' : ''"
          aria-hidden="true"
        />
      </button>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { BeginnerGuideStepId } from '@/api/beginnerGuide'

defineProps<{
  stepId: BeginnerGuideStepId
  stepNumber: number
  stepCount: number
  title: string
  description: string
  details?: string[]
  backDisabled?: boolean
  nextDisabled?: boolean
  nextLoading?: boolean
  nextLabel?: string
}>()

const emit = defineEmits<{
  back: []
  next: []
}>()

const { t } = useI18n()
</script>
