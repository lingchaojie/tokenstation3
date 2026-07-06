<template>
  <div class="linx-shell-bg min-h-screen">
    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Announcement bar (admin-configured; only shown when banners exist) -->
      <div
        v-if="bannerVisible"
        class="relative z-30 flex items-center justify-center gap-3 border-b border-gray-200 bg-white/90 px-4 py-2 text-center text-xs font-medium text-gray-600 backdrop-blur-xl dark:border-linear-hairline dark:bg-linear-canvas/90 dark:text-linear-ink-muted sm:text-sm"
      >
        <span class="ui-accent-dot h-1.5 w-1.5 flex-shrink-0 rounded-full"></span>
        <Transition name="banner-fade" mode="out-in">
          <span :key="currentBannerIndex">{{ currentBannerText }}</span>
        </Transition>
        <button
          class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 transition-colors hover:text-gray-600 dark:text-linear-ink-tertiary dark:hover:text-linear-ink"
          :aria-label="t('common.close')"
          @click="dismissAnnouncement"
        >
          <Icon name="x" size="sm" />
        </button>
      </div>

      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="p-4 md:p-5 lg:p-6">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useAnnouncementBanner } from '@/composables/useAnnouncementBanner'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

// Top announcement bar in the authenticated shell. No fallback text, so the bar
// is hidden unless an admin configured banners. Dismissal is remembered for the
// browser session.
const {
  visible: bannerVisible,
  currentBannerIndex,
  currentBannerText,
  dismissAnnouncement,
} = useAnnouncementBanner({ dismissKey: 'announcement-app-dismissed' })

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>
