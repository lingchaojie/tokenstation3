<template>
  <details v-if="inline" open class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-elevated">
    <summary
      class="cursor-pointer text-sm font-semibold text-gray-950 outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 dark:text-linear-ink"
    >
      {{ t('apiDocs.onThisPage') }}
    </summary>
    <nav class="mt-3" :aria-label="t('apiDocs.onThisPage')">
      <TocLinks :headings="headings" :active-id="activeId" />
    </nav>
  </details>
  <nav v-else :aria-label="t('apiDocs.onThisPage')">
    <h2
      class="mb-3 text-xs font-semibold uppercase tracking-[0.12em] text-gray-500 dark:text-linear-ink-tertiary"
    >
      {{ t('apiDocs.onThisPage') }}
    </h2>
    <TocLinks :headings="headings" :active-id="activeId" />
  </nav>
</template>

<script setup lang="ts">
import { defineComponent, h, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'

interface TocHeading {
  id: string
  label: string
}

defineProps<{
  headings: TocHeading[]
  activeId: string
  inline?: boolean
}>()

const { t } = useI18n()

const TocLinks = defineComponent({
  props: {
    headings: { type: Array as PropType<TocHeading[]>, required: true },
    activeId: { type: String, required: true }
  },
  setup(props) {
    return () =>
      h(
        'ul',
        { class: 'space-y-1 border-l border-gray-200 dark:border-linear-hairline' },
        props.headings.map((heading) =>
          h('li', { key: heading.id }, [
            h(
              'a',
              {
                href: `#${heading.id}`,
                'aria-current': heading.id === props.activeId ? 'location' : undefined,
                class: [
                  '-ml-px block border-l px-3 py-1.5 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none',
                  heading.id === props.activeId
                    ? 'border-primary-500 font-medium text-primary-700 dark:text-primary-300'
                    : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-950 dark:text-linear-ink-tertiary dark:hover:text-linear-ink'
                ]
              },
              heading.label
            )
          ])
        )
      )
  }
})
</script>
