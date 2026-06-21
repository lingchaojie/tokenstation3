<template>
  <div class="linear-landing min-h-screen bg-linear-canvas text-linear-ink selection:bg-primary-500/30 selection:text-primary-900 dark:selection:text-primary-100">
    <header class="sticky top-0 z-20 border-b border-linear-hairline bg-linear-canvas/90 backdrop-blur-xl">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-6 px-4 py-3 sm:px-6 lg:px-8">
        <router-link to="/home" class="group flex items-center gap-3" :aria-label="siteName">
          <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-linear-hairline transition-colors group-hover:ring-linear-hairline-strong">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span class="leading-tight">
            <span class="block text-sm font-semibold text-linear-ink">
              <LinxWordmark v-if="usesDefaultBrand" />
              <span v-else>{{ siteName }}</span>
            </span>
            <span class="block text-[10px] font-medium uppercase tracking-[0.22em] text-linear-ink-tertiary">{{ siteSubtitle }}</span>
          </span>
        </router-link>

        <div class="ml-auto flex items-center gap-2 sm:gap-3">
          <div class="hidden items-center gap-6 text-sm font-medium text-linear-ink-subtle md:flex">
            <router-link to="/models" class="text-linear-ink">{{ t('nav.modelMarketplace') }}</router-link>
            <router-link to="/home#pricing" class="transition-colors hover:text-linear-ink">{{ t('modelCatalog.pricingNav') }}</router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="transition-colors hover:text-linear-ink"
            >
              {{ t('home.docs') }}
            </a>
          </div>
          <LocaleSwitcher />
          <button
            class="ui-theme-toggle"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" class="ui-theme-icon-accent" />
            <Icon v-else name="moon" size="md" class="ui-theme-icon-accent" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex h-10 items-center justify-center rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-400"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <ModelCatalog />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import LinxWordmark from '@/components/common/LinxWordmark.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelCatalog from '@/components/models/ModelCatalog.vue'
import { useAppStore, useAuthStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))
const DEFAULT_SITE_NAME = 'LINX2.AI'
const brandIconUrl = '/linx2-icon.png'

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || DEFAULT_SITE_NAME)
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI Gateway Platform')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const brandLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || brandIconUrl)
const usesDefaultBrand = computed(() => siteName.value.trim().toUpperCase() === DEFAULT_SITE_NAME)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')

function toggleTheme() {
  document.documentElement.classList.toggle('dark')
  isDark.value = document.documentElement.classList.contains('dark')
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

onMounted(() => {
  if (!appStore.publicSettingsLoaded) {
    void appStore.fetchPublicSettings()
  }
  if (!authStore.user) {
    void authStore.checkAuth()
  }
})
</script>
