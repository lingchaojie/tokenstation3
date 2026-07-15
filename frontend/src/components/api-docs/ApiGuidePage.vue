<template>
  <article class="min-w-0 max-w-full space-y-10 text-gray-700 dark:text-gray-300">
    <section
      v-for="section in definition.sections"
      :key="section.id"
      class="scroll-mt-24 space-y-4"
      :aria-labelledby="section.id"
    >
      <h2 :id="section.id" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t(section.titleKey) }}
      </h2>

      <template v-for="(block, blockIndex) in section.blocks" :key="`${section.id}:${blockIndex}`">
        <p
          v-if="block.kind === 'paragraph'"
          data-testid="guide-paragraph"
          class="max-w-3xl text-base leading-7 text-gray-600 dark:text-gray-300"
        >
          {{ t(block.textKey) }}
        </p>

        <aside
          v-else-if="block.kind === 'callout'"
          data-testid="guide-callout"
          :data-tone="block.tone"
          role="note"
          class="rounded-xl border px-4 py-3 text-sm leading-6"
          :class="block.tone === 'warning'
            ? 'border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200'
            : 'border-primary-200 bg-primary-50 text-primary-900 dark:border-primary-500/30 dark:bg-primary-500/10 dark:text-primary-200'"
        >
          {{ t(block.textKey) }}
        </aside>

        <ApiDocsCodeBlock
          v-else-if="block.kind === 'code'"
          :label="block.label"
          :language="block.language"
          :code="block.code"
        />

        <div
          v-else-if="block.kind === 'table'"
          class="max-w-full overflow-x-auto rounded-xl border border-gray-200 dark:border-linear-hairline"
        >
          <table data-testid="guide-table" class="min-w-full border-collapse text-left text-sm">
            <caption class="sr-only">
              {{ t(section.titleKey) }}
            </caption>
            <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th
                  v-for="(column, columnIndex) in block.columns"
                  :key="columnIndex"
                  scope="col"
                  class="px-4 py-3 font-semibold"
                >
                  {{ tableValue(column) }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 dark:divide-linear-hairline">
              <tr v-for="(row, rowIndex) in block.rows" :key="rowIndex">
                <td
                  v-for="(cell, cellIndex) in row"
                  :key="cellIndex"
                  class="whitespace-nowrap px-4 py-3 font-mono text-xs text-gray-700 dark:text-gray-300"
                >
                  {{ tableValue(cell) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <ul v-else-if="block.kind === 'links'" class="flex flex-wrap gap-3">
          <li v-for="link in block.links" :key="`${link.labelKey}:${link.to}`">
            <a
              v-if="isExternalLink(link.to)"
              data-testid="guide-link"
              :href="link.to"
              target="_blank"
              rel="noopener noreferrer"
              class="font-medium text-primary-600 underline decoration-primary-300 underline-offset-4 hover:text-primary-700 dark:text-primary-300 dark:hover:text-primary-200"
            >
              {{ t(link.labelKey) }}
            </a>
            <RouterLink
              v-else
              data-testid="guide-link"
              :to="link.to"
              class="font-medium text-primary-600 underline decoration-primary-300 underline-offset-4 hover:text-primary-700 dark:text-primary-300 dark:hover:text-primary-200"
            >
              {{ t(link.labelKey) }}
            </RouterLink>
          </li>
        </ul>
      </template>
    </section>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import ApiDocsCodeBlock from './ApiDocsCodeBlock.vue'
import type { ApiDocsGuideDefinition, ApiDocsTableValue } from './types'

const props = defineProps<{
  definition: ApiDocsGuideDefinition
}>()

const { t } = useI18n()

const headings = computed(() => props.definition.sections.map((section) => ({
  id: section.id,
  label: t(section.titleKey)
})))

function isExternalLink(to: string): boolean {
  return /^https?:\/\//i.test(to)
}

function tableValue(value: ApiDocsTableValue): string {
  return value.kind === 'localized' ? t(value.textKey) : value.value
}

defineExpose({ headings })
</script>
