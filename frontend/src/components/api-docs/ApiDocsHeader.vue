<template>
  <header
    data-testid="api-docs-header"
    class="sticky top-0 z-40 border-b border-gray-200 bg-white/95 backdrop-blur-xl dark:border-linear-hairline dark:bg-linear-canvas/95"
  >
    <nav
      class="mx-auto flex max-w-[96rem] items-center justify-between gap-3 px-4 py-3 sm:px-6 lg:px-8"
      :aria-label="t('apiDocs.title')"
    >
      <RouterLink
        to="/home"
        class="group flex min-w-0 items-center gap-3 rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
        :aria-label="siteName"
      >
        <span
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-gray-200 transition-colors group-hover:ring-gray-300 motion-reduce:transition-none dark:ring-linear-hairline"
        >
          <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
        </span>
        <span class="min-w-0 leading-tight">
          <span class="block truncate text-sm font-semibold tracking-[-0.02em]">
            <LinxWordmark v-if="usesDefaultBrand" />
            <span v-else>{{ siteName }}</span>
          </span>
          <span
            class="block truncate text-[10px] font-medium uppercase tracking-[0.18em] text-gray-500 dark:text-linear-ink-tertiary"
          >
            {{ t('apiDocs.title') }}
          </span>
        </span>
      </RouterLink>

      <div class="ml-auto flex shrink-0 items-center gap-1.5 sm:gap-2">
        <button
          ref="searchTrigger"
          type="button"
          data-testid="api-docs-search-open"
          class="inline-flex h-10 items-center gap-2 rounded-lg px-2.5 text-sm font-medium text-gray-600 outline-none transition-colors hover:bg-gray-100 hover:text-gray-950 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:text-linear-ink-muted dark:hover:bg-linear-elevated dark:hover:text-linear-ink dark:focus-visible:ring-offset-linear-canvas"
          :aria-label="t('apiDocs.search')"
          aria-haspopup="dialog"
          aria-controls="api-docs-search-dialog"
          :aria-expanded="searchOpen"
          @click="emit('openSearch')"
        >
          <Icon name="search" size="sm" aria-hidden="true" />
          <span class="hidden md:inline">{{ t('apiDocs.search') }}</span>
        </button>
        <LocaleSwitcher />
        <button
          type="button"
          data-testid="api-docs-theme-toggle"
          class="ui-theme-toggle outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
          :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          @click="toggleTheme"
        >
          <Icon
            v-if="isDark"
            name="sun"
            size="md"
            class="ui-theme-icon-accent"
            aria-hidden="true"
          />
          <Icon
            v-else
            name="moon"
            size="md"
            class="ui-theme-icon-accent"
            aria-hidden="true"
          />
        </button>
        <RouterLink
          data-testid="api-docs-account-link"
          :to="accountRoute"
          class="inline-flex h-10 items-center justify-center rounded-lg bg-primary-500 px-3 py-2 text-sm font-medium text-white outline-none transition-colors hover:bg-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas sm:px-4"
        >
          {{ accountLabel }}
        </RouterLink>
      </div>
    </nav>

    <div class="mx-auto max-w-[96rem] overflow-x-auto px-4 pb-2 sm:px-6 lg:px-8">
      <ul class="flex min-w-max items-center gap-2" :aria-label="t('apiDocs.title')">
        <li
          v-for="tag in API_DOCS_CAPABILITY_TAGS"
          :key="tag"
          data-testid="api-docs-capability-tag"
          class="rounded-full border border-gray-200 bg-gray-50 px-2.5 py-1 text-xs font-medium text-gray-600 dark:border-linear-hairline dark:bg-linear-elevated dark:text-linear-ink-muted"
        >
          {{ tag }}
        </li>
      </ul>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import LinxWordmark from '@/components/common/LinxWordmark.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { API_DOCS_CAPABILITY_TAGS } from './catalog'

withDefaults(
  defineProps<{
    searchOpen?: boolean
  }>(),
  {
    searchOpen: false
  }
)

const emit = defineEmits<{
  openSearch: []
}>()

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))
const searchTrigger = ref<HTMLButtonElement | null>(null)
const defaultSiteName = 'LINX2.AI'

const siteName = computed(
  () => appStore.cachedPublicSettings?.site_name || appStore.siteName || defaultSiteName
)
const brandLogo = computed(
  () =>
    sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
      allowRelative: true,
      allowDataUrl: true
    }) || '/linx2-icon.png'
)
const usesDefaultBrand = computed(() => siteName.value.trim().toUpperCase() === defaultSiteName)
const accountRoute = computed(() => {
  if (!authStore.isAuthenticated) {
    return { path: '/login', query: { redirect: route.fullPath } }
  }
  return authStore.isAdmin ? '/admin/my-account/dashboard' : '/dashboard'
})
const accountLabel = computed(() =>
  authStore.isAuthenticated ? t('apiDocs.dashboard') : t('apiDocs.login')
)

function toggleTheme(): void {
  document.documentElement.classList.toggle('dark')
  isDark.value = document.documentElement.classList.contains('dark')
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function focusSearchTrigger(): void {
  searchTrigger.value?.focus()
}

defineExpose({ focusSearchTrigger })
</script>
