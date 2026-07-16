<template>
  <section class="space-y-5" :aria-label="t('gettingStarted.steps.troubleshoot.title')">
    <ul class="grid gap-3">
      <li
        v-for="item in checks"
        :key="item.id"
        :data-troubleshooting-branch="item.id"
        class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas"
      >
        <div class="flex items-start gap-3">
          <Icon name="checkCircle" size="md" class="mt-0.5 shrink-0 text-primary-500" aria-hidden="true" />
          <div class="min-w-0">
            <p class="text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">{{ item.text }}</p>
            <code
              v-if="item.value"
              class="mt-2 block max-w-full overflow-x-auto rounded-lg bg-gray-950 px-3 py-2 font-mono text-xs text-gray-100"
              v-text="item.value"
            ></code>
          </div>
        </div>
      </li>
    </ul>

    <div v-for="command in variant.diagnosticCommands" :key="command">
      <GuideCommandBlock :command="command" />
    </div>

    <a
      :href="variant.officialSourceUrl"
      target="_blank"
      rel="noopener noreferrer"
      data-troubleshooting-branch="official-source"
      class="inline-flex items-center gap-2 rounded-md text-sm font-medium text-primary-600 outline-none transition-colors hover:text-primary-500 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:text-primary-300 dark:focus-visible:ring-offset-linear-canvas"
    >
      {{ t('gettingStarted.troubleshooting.officialSource') }}
      <Icon name="externalLink" size="sm" aria-hidden="true" />
    </a>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import GuideCommandBlock from './GuideCommandBlock.vue'
import type { GuideVariant } from './curriculum'

const props = defineProps<{
  variant: GuideVariant
  baseUrl: string
}>()

const { t } = useI18n()

const configPaths = computed(() => {
  const root = props.variant.os === 'windows' ? '%userprofile%' : '~'
  const separator = props.variant.os === 'windows' ? '\\' : '/'
  if (props.variant.client === 'claude_code') {
    return [`${root}${separator}.claude${separator}settings.json`]
  }
  if (props.variant.client === 'opencode') {
    return [`${root}${separator}.config${separator}opencode${separator}opencode.json`]
  }
  if (props.variant.client === 'cc_switch') {
    return ['CC Switch → Providers']
  }
  return [
    `${root}${separator}.codex${separator}config.toml`,
    `${root}${separator}.codex${separator}auth.json`
  ]
})

const checks = computed(() => [
  { id: 'version', text: t('gettingStarted.troubleshooting.version'), value: props.variant.verifyCommand },
  { id: 'file-path', text: t('gettingStarted.troubleshooting.filePath'), value: configPaths.value.join('\n') },
  { id: 'base-url', text: t('gettingStarted.troubleshooting.baseUrl'), value: props.baseUrl },
  { id: 'restart', text: t('gettingStarted.troubleshooting.restart') },
  { id: 'authentication', text: t('gettingStarted.troubleshooting.authentication') },
  { id: 'connection', text: t('gettingStarted.troubleshooting.connection') },
  { id: 'shell', text: t('gettingStarted.troubleshooting.shell') },
  { id: 'permissions', text: t('gettingStarted.troubleshooting.permissions') }
])
</script>
