<template>
  <div v-if="show" class="fixed inset-0 z-[60] flex items-start justify-center p-3 pt-[10vh] sm:p-6 sm:pt-[12vh]">
    <button
      type="button"
      data-testid="api-docs-search-backdrop"
      class="absolute inset-0 bg-gray-950/50 backdrop-blur-sm"
      tabindex="-1"
      aria-hidden="true"
      @click="requestClose"
    />

    <section
      ref="dialog"
      id="api-docs-search-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="api-docs-search-title"
      class="relative flex max-h-[min(42rem,80vh)] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-linear-hairline dark:bg-linear-canvas"
      @keydown="handleDialogKeydown"
    >
      <h2 id="api-docs-search-title" class="sr-only">
        {{ t('apiDocs.search') }}
      </h2>

      <div class="flex items-center gap-2 border-b border-gray-200 p-3 dark:border-linear-hairline">
        <Icon
          name="search"
          size="sm"
          class="shrink-0 text-gray-400 dark:text-linear-ink-tertiary"
          aria-hidden="true"
        />
        <input
          ref="searchInput"
          v-model="query"
          type="search"
          class="h-10 min-w-0 flex-1 bg-transparent px-1 text-base text-gray-950 outline-none placeholder:text-gray-400 dark:text-linear-ink dark:placeholder:text-linear-ink-tertiary"
          :aria-label="t('apiDocs.search')"
          :placeholder="t('apiDocs.searchPlaceholder')"
          aria-controls="api-docs-search-results"
          @keydown="handleInputKeydown"
        />
        <button
          type="button"
          class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-500 outline-none transition-colors hover:bg-gray-100 hover:text-gray-950 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none dark:text-linear-ink-muted dark:hover:bg-linear-elevated dark:hover:text-linear-ink"
          :aria-label="t('common.close')"
          @click="requestClose"
        >
          <Icon name="x" size="md" aria-hidden="true" />
        </button>
      </div>

      <ul
        v-if="filteredEntries.length"
        id="api-docs-search-results"
        class="min-h-0 overflow-y-auto p-2 sm:p-3"
      >
        <li v-for="(entry, index) in filteredEntries" :key="entry.id">
          <button
            type="button"
            data-testid="api-docs-search-result"
            class="flex w-full items-center justify-between gap-4 rounded-xl px-3 py-3 text-left outline-none transition-colors hover:bg-gray-100 focus-visible:bg-primary-50 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none dark:hover:bg-linear-elevated dark:focus-visible:bg-primary-500/10"
            @click="select(entry.path)"
            @keydown="handleResultKeydown($event, index, entry.path)"
          >
            <span class="min-w-0 truncate font-medium text-gray-950 dark:text-linear-ink">
              {{ entry.title }}
            </span>
            <span
              class="shrink-0 text-xs font-medium uppercase tracking-[0.1em] text-gray-500 dark:text-linear-ink-tertiary"
            >
              {{ entry.section }}
            </span>
          </button>
        </li>
      </ul>

      <p
        v-else
        id="api-docs-search-results"
        role="status"
        class="p-8 text-center text-sm text-gray-500 dark:text-linear-ink-muted"
      >
        {{ t('apiDocs.noResults') }}
      </p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'
import type { ApiDocsSearchEntry } from './search'

const props = defineProps<{
  show: boolean
  entries: ApiDocsSearchEntry[]
}>()

const emit = defineEmits<{
  close: []
  select: [path: string]
}>()

const { t } = useI18n()
const dialog = ref<HTMLElement | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)
const query = ref('')
let previouslyFocused: HTMLElement | null = null
let bodyScrollLocked = false
let previousBodyOverflow = ''

const queryTokens = computed(() =>
  query.value
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
)

const filteredEntries = computed(() => {
  if (!queryTokens.value.length) return props.entries
  return props.entries.filter((entry) => {
    const searchableText = entry.text.toLowerCase()
    return queryTokens.value.every((token) => searchableText.includes(token))
  })
})

watch(
  () => props.show,
  (show) => {
    if (show) {
      previouslyFocused =
        document.activeElement instanceof HTMLElement ? document.activeElement : null
      query.value = ''
      lockBodyScroll()
      void nextTick(() => searchInput.value?.focus())
      return
    }

    unlockBodyScroll()
    const focusTarget = previouslyFocused
    previouslyFocused = null
    void nextTick(() => {
      if (focusTarget?.isConnected) focusTarget.focus()
    })
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  unlockBodyScroll()
})

function lockBodyScroll(): void {
  if (bodyScrollLocked) return
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  bodyScrollLocked = true
}

function unlockBodyScroll(): void {
  if (!bodyScrollLocked) return
  document.body.style.overflow = previousBodyOverflow
  bodyScrollLocked = false
}

function requestClose(): void {
  emit('close')
}

function select(path: string): void {
  emit('select', path)
}

function resultButtons(): HTMLButtonElement[] {
  return dialog.value
    ? Array.from(
        dialog.value.querySelectorAll<HTMLButtonElement>(
          '[data-testid="api-docs-search-result"]'
        )
      )
    : []
}

function focusResult(index: number): void {
  const buttons = resultButtons()
  if (!buttons.length) return
  const normalizedIndex = (index + buttons.length) % buttons.length
  buttons[normalizedIndex]?.focus()
}

function handleInputKeydown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    focusResult(0)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    focusResult(-1)
  } else if (event.key === 'Enter' && filteredEntries.value[0]) {
    event.preventDefault()
    select(filteredEntries.value[0].path)
  }
}

function handleResultKeydown(event: KeyboardEvent, index: number, path: string): void {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    focusResult(index + 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    focusResult(index - 1)
  } else if (event.key === 'Home') {
    event.preventDefault()
    focusResult(0)
  } else if (event.key === 'End') {
    event.preventDefault()
    focusResult(filteredEntries.value.length - 1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    select(path)
  }
}

function handleDialogKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.stopPropagation()
    event.preventDefault()
    requestClose()
    return
  }
  if (event.key !== 'Tab' || !dialog.value) return

  const focusable = Array.from(
    dialog.value.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter((element) => element.getAttribute('aria-hidden') !== 'true')
  const first = focusable[0]
  const last = focusable.at(-1)

  if (!first || !last) {
    event.preventDefault()
    dialog.value.focus()
    return
  }

  const activeElement = document.activeElement
  if (event.shiftKey && (activeElement === first || !dialog.value.contains(activeElement))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (activeElement === last || !dialog.value.contains(activeElement))) {
    event.preventDefault()
    first.focus()
  }
}
</script>
