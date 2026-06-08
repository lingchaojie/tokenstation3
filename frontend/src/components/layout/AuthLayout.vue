<template>
  <!-- `dark` scope forces auth pages into the dark branded theme regardless of app theme -->
  <div class="dark linx2-auth relative flex min-h-screen items-center justify-center overflow-hidden bg-[#0a0b0e] p-4 text-zinc-100">
    <!-- Background atmosphere -->
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute inset-0 bg-[radial-gradient(circle_at_15%_-5%,rgba(249,115,22,0.20),transparent_38%),radial-gradient(circle_at_85%_110%,rgba(251,146,60,0.14),transparent_40%)]"></div>
      <div class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-orange-500/15 blur-3xl"></div>
      <div class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-orange-600/12 blur-3xl"></div>
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(249,115,22,0.04)_1px,transparent_1px),linear-gradient(90deg,rgba(249,115,22,0.04)_1px,transparent_1px)] bg-[size:64px_64px]"
      ></div>
      <div class="absolute left-1/2 top-0 h-px w-[70vw] -translate-x-1/2 bg-gradient-to-r from-transparent via-orange-400/40 to-transparent"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo / Brand -->
      <div class="mb-8 text-center">
        <template v-if="settingsLoaded">
          <div
            class="brand-tile mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl bg-white p-2 shadow-[0_12px_36px_rgba(249,115,22,0.28)] ring-1 ring-black/5"
          >
            <img :src="siteLogo || '/linx2-icon.png'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="font-display mb-2 text-3xl font-extrabold tracking-[0.06em]">
            <span class="bg-gradient-to-r from-orange-300 via-orange-400 to-amber-200 bg-clip-text text-transparent">
              {{ siteName }}
            </span>
          </h1>
          <p class="text-sm text-zinc-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="rounded-2xl border border-white/[0.08] bg-white/[0.03] p-8 shadow-2xl shadow-black/50 backdrop-blur-xl">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-zinc-500">
        &copy; {{ currentYear }} LINIX2.Ltd
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'LINX2')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI 编程 API 平台 · linx2.ai')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

// Load distinctive brand fonts for the auth experience (no-op if already injected by the landing page).
function ensureBrandFonts() {
  if (document.getElementById('linx2-brand-fonts')) return
  const link = document.createElement('link')
  link.id = 'linx2-brand-fonts'
  link.rel = 'stylesheet'
  link.href =
    'https://fonts.googleapis.com/css2?family=Bricolage+Grotesque:opsz,wght@12..96,500..800&family=Manrope:wght@400..700&display=swap'
  document.head.appendChild(link)
}

onMounted(() => {
  ensureBrandFonts()
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.linx2-auth {
  font-family: 'Manrope', system-ui, -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif;
}

.font-display {
  font-family: 'Bricolage Grotesque', 'Manrope', system-ui, 'PingFang SC', sans-serif;
}
</style>
