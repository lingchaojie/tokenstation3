<template>
  <aside
    data-testid="api-docs-sidebar"
    class="hidden lg:block"
    :inert="mobileOpen ? true : undefined"
    :aria-hidden="mobileOpen ? 'true' : undefined"
  >
    <nav
      class="sticky top-[7.5rem] max-h-[calc(100vh-8.5rem)] overflow-y-auto pr-3"
      :aria-label="t('apiDocs.menu')"
    >
      <section
        v-for="group in navGroups"
        :key="group.id"
        data-testid="api-docs-nav-group"
        class="mb-6"
      >
        <h2
          class="mb-2 px-3 text-xs font-semibold uppercase tracking-[0.12em] text-gray-500 dark:text-linear-ink-tertiary"
        >
          {{ t(group.labelKey) }}
        </h2>
        <ul class="space-y-1">
          <li v-for="page in group.pages" :key="page.id">
            <RouterLink
              :to="page.path"
              :aria-current="page.id === currentPageId ? 'page' : undefined"
              class="block rounded-lg px-3 py-2 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none"
              :class="
                page.id === currentPageId
                  ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-500/10 dark:text-primary-300'
                  : 'text-gray-600 hover:bg-gray-100 hover:text-gray-950 dark:text-linear-ink-muted dark:hover:bg-linear-elevated dark:hover:text-linear-ink'
              "
              @click="handleNavigate(page.path)"
            >
              {{ t(page.titleKey) }}
            </RouterLink>
          </li>
        </ul>
      </section>
    </nav>
  </aside>

  <div v-if="mobileOpen" class="fixed inset-0 z-50 lg:hidden">
    <button
      type="button"
      class="absolute inset-0 bg-gray-950/45 backdrop-blur-sm"
      tabindex="-1"
      aria-hidden="true"
      @click="closeDrawer"
    />
    <aside
      ref="drawer"
      id="api-docs-mobile-drawer"
      data-testid="api-docs-mobile-drawer"
      role="dialog"
      aria-modal="true"
      aria-labelledby="api-docs-mobile-drawer-title"
      tabindex="-1"
      class="relative h-full w-[min(20rem,88vw)] overflow-y-auto border-r border-gray-200 bg-white p-4 shadow-2xl dark:border-linear-hairline dark:bg-linear-canvas"
      @keydown="handleDrawerKeydown"
    >
      <div class="mb-5 flex items-center justify-between gap-3">
        <h2
          id="api-docs-mobile-drawer-title"
          class="text-sm font-semibold text-gray-950 dark:text-linear-ink"
        >
          {{ t('apiDocs.menu') }}
        </h2>
        <button
          ref="closeButton"
          type="button"
          data-testid="api-docs-mobile-close"
          class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-gray-500 outline-none transition-colors hover:bg-gray-100 hover:text-gray-950 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none dark:text-linear-ink-muted dark:hover:bg-linear-elevated dark:hover:text-linear-ink"
          :aria-label="t('common.close')"
          @click="closeDrawer"
        >
          <Icon name="x" size="md" aria-hidden="true" />
        </button>
      </div>

      <nav :aria-label="t('apiDocs.menu')">
        <section
          v-for="group in navGroups"
          :key="group.id"
          data-testid="api-docs-nav-group"
          class="mb-6"
        >
          <h3
            class="mb-2 px-3 text-xs font-semibold uppercase tracking-[0.12em] text-gray-500 dark:text-linear-ink-tertiary"
          >
            {{ t(group.labelKey) }}
          </h3>
          <ul class="space-y-1">
            <li v-for="page in group.pages" :key="page.id">
              <RouterLink
                :to="page.path"
                :aria-current="page.id === currentPageId ? 'page' : undefined"
                class="block rounded-lg px-3 py-2 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none"
                :class="
                  page.id === currentPageId
                    ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-500/10 dark:text-primary-300'
                    : 'text-gray-600 hover:bg-gray-100 hover:text-gray-950 dark:text-linear-ink-muted dark:hover:bg-linear-elevated dark:hover:text-linear-ink'
                "
                @click="handleNavigate(page.path)"
              >
                {{ t(page.titleKey) }}
              </RouterLink>
            </li>
          </ul>
        </section>
      </nav>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'
import { API_DOCS_NAV, API_DOCS_PAGES } from './catalog'
import type { ApiDocsPageId } from './types'

const props = withDefaults(
  defineProps<{
    currentPageId: ApiDocsPageId
    mobileOpen?: boolean
    menuTrigger?: HTMLButtonElement | null
  }>(),
  {
    mobileOpen: false,
    menuTrigger: null
  }
)

const emit = defineEmits<{
  close: []
  navigate: [path: string]
}>()

const { t } = useI18n()
const drawer = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let bodyScrollLocked = false
let previousBodyOverflow = ''
const navGroups = computed(() =>
  API_DOCS_NAV.map((group) => ({
    ...group,
    pages: group.pageIds.flatMap((pageId) => {
      const page = API_DOCS_PAGES.find(({ id }) => id === pageId)
      return page ? [page] : []
    })
  }))
)

watch(
  () => props.mobileOpen,
  (open) => {
    if (open) {
      lockBodyScroll()
      void nextTick(() => closeButton.value?.focus())
    } else {
      unlockBodyScroll()
    }
  },
  { immediate: true }
)

onBeforeUnmount(unlockBodyScroll)

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

function closeDrawer(): void {
  emit('close')
  void nextTick(() => props.menuTrigger?.focus())
}

function handleNavigate(path: string): void {
  emit('navigate', path)
  if (props.mobileOpen) closeDrawer()
}

function handleDrawerKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.stopPropagation()
    event.preventDefault()
    closeDrawer()
    return
  }
  if (event.key !== 'Tab' || !drawer.value) return

  const focusable = Array.from(
    drawer.value.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter((element) => element.getAttribute('aria-hidden') !== 'true')
  const first = focusable[0]
  const last = focusable.at(-1)
  if (!first || !last) {
    event.preventDefault()
    drawer.value.focus()
    return
  }

  const activeElement = document.activeElement
  if (event.shiftKey && (activeElement === first || !drawer.value.contains(activeElement))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (activeElement === last || !drawer.value.contains(activeElement))) {
    event.preventDefault()
    first.focus()
  }
}
</script>
