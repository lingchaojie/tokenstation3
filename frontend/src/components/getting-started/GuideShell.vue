<template>
  <div class="min-h-screen min-w-0 bg-gray-50 text-gray-950 dark:bg-linear-canvas dark:text-linear-ink">
    <header class="sticky top-0 z-30 border-b border-gray-200 bg-white/90 backdrop-blur-xl dark:border-linear-hairline dark:bg-linear-canvas/90">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 py-3 sm:px-6 lg:px-8">
        <router-link
          to="/home"
          class="group flex min-w-0 items-center gap-3 rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
          :aria-label="siteName"
        >
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-gray-200 transition-colors group-hover:ring-gray-300 motion-reduce:transition-none dark:ring-linear-hairline">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span class="min-w-0 leading-tight">
            <span class="block truncate text-sm font-semibold tracking-[-0.02em]">
              <LinxWordmark v-if="usesDefaultBrand" />
              <span v-else>{{ siteName }}</span>
            </span>
            <span class="block truncate text-[10px] font-medium uppercase tracking-[0.18em] text-gray-500 dark:text-linear-ink-tertiary">
              {{ t('gettingStarted.chrome.guideLabel') }}
            </span>
          </span>
        </router-link>

        <div class="ml-auto flex shrink-0 items-center gap-2">
          <LocaleSwitcher />
          <button
            type="button"
            class="ui-theme-toggle outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" class="ui-theme-icon-accent" aria-hidden="true" />
            <Icon v-else name="moon" size="md" class="ui-theme-icon-accent" aria-hidden="true" />
          </button>
          <router-link
            :to="accountRoute"
            class="inline-flex h-10 items-center justify-center rounded-lg bg-primary-500 px-3 py-2 text-sm font-medium text-white outline-none transition-colors hover:bg-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas sm:px-4"
          >
            {{ accountLabel }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto grid min-w-0 max-w-7xl gap-5 px-4 py-5 sm:px-6 lg:grid-cols-[18rem_minmax(0,1fr)] lg:px-8 lg:py-8">
      <GuideProgressNav
        :current-step="currentStep"
        :completed-steps="completedSteps"
        @select="emit('selectStep', $event)"
      />
      <section data-testid="guide-content-column" class="min-w-0">
        <slot />
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import LinxWordmark from '@/components/common/LinxWordmark.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import type { BeginnerGuideStepId } from '@/api/beginnerGuide'
import GuideProgressNav from './GuideProgressNav.vue'

defineProps<{
  currentStep: BeginnerGuideStepId
  completedSteps: BeginnerGuideStepId[]
}>()

const emit = defineEmits<{
  selectStep: [step: BeginnerGuideStepId]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))
const defaultSiteName = 'LINX2.AI'

const siteName = computed(
  () => appStore.cachedPublicSettings?.site_name || appStore.siteName || defaultSiteName
)
const brandLogo = computed(
  () =>
    sanitizeUrl(
      appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '',
      { allowRelative: true, allowDataUrl: true }
    ) || '/linx2-icon.png'
)
const usesDefaultBrand = computed(() => siteName.value.trim().toUpperCase() === defaultSiteName)
const accountRoute = computed(() => {
  if (!authStore.isAuthenticated) {
    return { path: '/login', query: { redirect: '/getting-started' } }
  }
  return authStore.isAdmin ? '/admin/my-account/dashboard' : '/dashboard'
})
const accountLabel = computed(() =>
  authStore.isAuthenticated ? t('home.goToDashboard') : t('home.login')
)

function toggleTheme(): void {
  document.documentElement.classList.toggle('dark')
  isDark.value = document.documentElement.classList.contains('dark')
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}
</script>
