<template>
  <article
    data-testid="beginner-guide-card"
    class="linx-panel-strong mx-auto mt-12 w-full max-w-6xl overflow-hidden p-6 text-left sm:p-8"
    :aria-labelledby="titleId"
  >
    <div class="grid gap-8 lg:grid-cols-[minmax(0,1.1fr)_minmax(24rem,0.9fr)] lg:items-center">
      <div>
        <p class="linx-section-kicker inline-flex items-center gap-2">
          <span
            class="flex h-7 w-7 items-center justify-center rounded-lg border border-primary-500/25 bg-primary-500/10 text-primary-400"
            aria-hidden="true"
          >
            <Icon name="book" size="sm" />
          </span>
          {{ t('gettingStarted.discovery.eyebrow') }}
        </p>
        <h2
          :id="titleId"
          class="mt-4 text-2xl font-semibold leading-tight tracking-[-0.045em] text-linear-ink sm:text-3xl"
        >
          {{ t('gettingStarted.discovery.title') }}
        </h2>
        <p class="mt-4 max-w-2xl text-sm leading-7 text-linear-ink-subtle sm:text-base">
          {{ t('gettingStarted.discovery.description') }}
        </p>
      </div>

      <div>
        <ol class="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-2">
          <li
            v-for="stage in stages"
            :key="stage"
            data-testid="beginner-guide-stage"
            class="flex min-w-0 items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 py-2.5 text-sm font-medium text-linear-ink-muted"
          >
            <span
              class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary-500/10 text-xs font-semibold text-primary-400"
              aria-hidden="true"
            >
              <Icon name="check" size="xs" :stroke-width="2" />
            </span>
            <span>{{ t(stage) }}</span>
          </li>
        </ol>

        <router-link
          to="/getting-started"
          data-testid="beginner-guide-card-cta"
          class="mt-5 inline-flex w-full items-center justify-center rounded-lg bg-primary-500 px-5 py-3 text-sm font-medium text-white transition-colors hover:bg-primary-400 sm:w-auto"
        >
          {{ t('gettingStarted.discovery.homeCta') }}
          <Icon name="arrowRight" size="sm" class="ml-2" :stroke-width="2" aria-hidden="true" />
        </router-link>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()

const titleId = 'beginner-guide-card-title'
const stages = [
  'gettingStarted.discovery.stages.choose',
  'gettingStarted.discovery.stages.install',
  'gettingStarted.discovery.stages.connect',
  'gettingStarted.discovery.stages.firstTask',
] as const
</script>
