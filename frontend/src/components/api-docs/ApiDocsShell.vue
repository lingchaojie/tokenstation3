<template>
  <div
    class="min-h-screen min-w-0 overflow-x-clip bg-gray-50 text-gray-950 dark:bg-linear-canvas dark:text-linear-ink"
  >
    <ApiDocsHeader
      ref="header"
      :search-open="searchOpen"
      :inert="mobileOpen || searchOpen ? true : undefined"
      :aria-hidden="mobileOpen || searchOpen ? 'true' : undefined"
      @open-search="openSearch"
    />

    <div
      class="mx-auto max-w-[96rem] px-4 pt-4 sm:px-6 lg:hidden"
      :inert="mobileOpen || searchOpen ? true : undefined"
      :aria-hidden="mobileOpen || searchOpen ? 'true' : undefined"
    >
      <button
        ref="menuTrigger"
        type="button"
        data-testid="api-docs-mobile-menu"
        class="inline-flex h-10 items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 text-sm font-medium text-gray-700 outline-none transition-colors hover:bg-gray-100 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none dark:border-linear-hairline dark:bg-linear-elevated dark:text-linear-ink-muted dark:hover:text-linear-ink lg:hidden"
        aria-controls="api-docs-mobile-drawer"
        :aria-expanded="mobileOpen"
        @click="mobileOpen = true"
      >
        <Icon name="menu" size="sm" aria-hidden="true" />
        {{ t('apiDocs.menu') }}
      </button>
    </div>

    <main
      class="mx-auto grid min-w-0 max-w-[96rem] gap-8 px-4 py-6 sm:px-6 lg:grid-cols-[17rem_minmax(0,1fr)] xl:grid-cols-[17rem_minmax(0,1fr)_13rem] lg:px-8"
      :inert="searchOpen ? true : undefined"
      :aria-hidden="searchOpen ? 'true' : undefined"
    >
      <ApiDocsSidebar
        :current-page-id="currentPageId"
        :mobile-open="mobileOpen"
        :menu-trigger="menuTrigger"
        @close="mobileOpen = false"
        @navigate="emit('navigate', $event)"
      />

      <section
        data-testid="api-docs-content"
        class="min-w-0 overflow-x-hidden [&_h2[id]]:scroll-mt-32"
        :inert="mobileOpen ? true : undefined"
        :aria-hidden="mobileOpen ? 'true' : undefined"
      >
        <div class="mx-auto min-w-0 max-w-3xl">
          <div data-testid="api-docs-inline-toc" class="mb-6 hidden lg:block xl:hidden">
            <ApiDocsToc
              :headings="headings"
              :active-id="activeHeadingId"
              inline
            />
          </div>
          <slot />
        </div>
      </section>

      <aside
        data-testid="api-docs-toc"
        class="hidden xl:block"
        :inert="mobileOpen ? true : undefined"
        :aria-hidden="mobileOpen ? 'true' : undefined"
      >
        <div class="sticky top-[7.5rem] max-h-[calc(100vh-8.5rem)] overflow-y-auto">
          <ApiDocsToc :headings="headings" :active-id="activeHeadingId" />
        </div>
      </aside>
    </main>

    <div
      v-if="$slots.search"
      :inert="mobileOpen || searchOpen ? true : undefined"
      :aria-hidden="mobileOpen || searchOpen ? 'true' : undefined"
    >
      <slot name="search" />
    </div>

    <ApiDocsSearch
      :show="searchOpen"
      :entries="searchEntries"
      @close="closeSearch"
      @select="handleSearchSelect"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useIntersectionObserver } from '@vueuse/core'

import Icon from '@/components/icons/Icon.vue'
import ApiDocsHeader from './ApiDocsHeader.vue'
import ApiDocsSearch from './ApiDocsSearch.vue'
import ApiDocsSidebar from './ApiDocsSidebar.vue'
import ApiDocsToc from './ApiDocsToc.vue'
import { buildApiDocsSearchEntries } from './search'
import type { ApiDocsPageId } from './types'

interface ApiDocsHeading {
  id: string
  label: string
}

interface ApiDocsHeaderExposed {
  focusSearchTrigger: () => void
}

const props = withDefaults(
  defineProps<{
    currentPageId?: ApiDocsPageId
    headings?: ApiDocsHeading[]
  }>(),
  {
    headings: () => []
  }
)

const emit = defineEmits<{
  openSearch: []
  navigate: [path: string]
}>()

const { locale, t } = useI18n()
const router = useRouter()
const mobileOpen = ref(false)
const searchOpen = ref(false)
const header = ref<ApiDocsHeaderExposed | null>(null)
const menuTrigger = ref<HTMLButtonElement | null>(null)
const headingElements = shallowRef<HTMLElement[]>([])
const activeHeadingId = ref(props.headings[0]?.id ?? '')
const searchEntries = computed(() => {
  locale.value
  return buildApiDocsSearchEntries(t)
})

watch(
  () => props.headings.map(({ id }) => id),
  async (ids) => {
    if (!ids.includes(activeHeadingId.value)) activeHeadingId.value = ids[0] ?? ''
    await nextTick()
    headingElements.value = ids.flatMap((id) => {
      const element = document.getElementById(id)
      return element ? [element] : []
    })
  },
  { immediate: true }
)

useIntersectionObserver(
  headingElements,
  (entries) => {
    const firstVisible = entries
      .filter(({ isIntersecting }) => isIntersecting)
      .sort((left, right) => left.boundingClientRect.top - right.boundingClientRect.top)[0]
    if (firstVisible?.target instanceof HTMLElement && firstVisible.target.id) {
      activeHeadingId.value = firstVisible.target.id
    }
  },
  { rootMargin: '-120px 0px -65% 0px' }
)

onMounted(() => {
  window.addEventListener('keydown', handleGlobalSearchShortcut)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleGlobalSearchShortcut)
})

async function openSearch(): Promise<void> {
  emit('openSearch')
  if (searchOpen.value) return

  if (mobileOpen.value) {
    mobileOpen.value = false
    await nextTick()
  }

  header.value?.focusSearchTrigger()
  searchOpen.value = true
}

function closeSearch(): void {
  searchOpen.value = false
}

function handleSearchSelect(path: string): void {
  searchOpen.value = false
  void router.push(path)
}

function isEditableTarget(event: KeyboardEvent): boolean {
  const target = event.target instanceof Element ? event.target : document.activeElement
  if (!(target instanceof Element)) return false
  return Boolean(
    target.closest('input, textarea, [contenteditable]:not([contenteditable="false"])')
  )
}

function handleGlobalSearchShortcut(event: KeyboardEvent): void {
  const commandK = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k'
  const slash =
    event.key === '/' &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.altKey &&
    !isEditableTarget(event)

  if (!commandK && !slash) return
  event.preventDefault()
  void openSearch()
}
</script>
