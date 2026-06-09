<template>
  <div class="dark linear-auth-shell relative min-h-screen overflow-hidden bg-linear-canvas text-linear-ink">
    <div class="mx-auto grid min-h-screen w-full max-w-6xl grid-cols-1 gap-8 px-4 py-8 lg:grid-cols-[0.95fr_1.05fr] lg:items-center lg:px-8">
      <section data-testid="auth-product-panel" class="hidden lg:block">
        <div class="linx-panel-strong p-8">
          <div class="mb-8 flex items-center gap-3">
            <span class="flex h-10 w-10 items-center justify-center overflow-hidden rounded-lg bg-white p-1.5 ring-1 ring-white/10">
              <img :src="siteLogo || '/linx2-icon.png'" alt="Logo" class="h-full w-full object-contain" />
            </span>
            <div>
              <p class="text-sm font-semibold tracking-[-0.02em] text-linear-ink">{{ siteName }}</p>
              <p class="text-xs text-linear-ink-tertiary">{{ siteSubtitle }}</p>
            </div>
          </div>

          <p class="linx-section-kicker">Unified AI Coding API</p>
          <h1 class="mt-4 max-w-md text-5xl font-semibold leading-[1.02] tracking-[-0.06em] text-linear-ink">
            One gateway for coding models, keys, and usage.
          </h1>
          <p class="mt-5 max-w-md text-sm leading-6 text-linear-ink-subtle">
            Sign in to manage API keys, subscriptions, billing, and channel access through a calm Linear-style console.
          </p>

          <div class="mt-8 rounded-xl border border-linear-hairline bg-linear-canvas p-4 font-mono text-xs text-linear-ink-muted">
            <div class="linx-data-row"><span>Base URL</span><span class="text-primary-300">https://linx2.ai/api</span></div>
            <div class="linx-data-row"><span>Routes</span><span>Claude · Codex · Gemini</span></div>
            <div class="linx-data-row"><span>Billing</span><span>Usage ledger enabled</span></div>
          </div>
        </div>
      </section>

      <main class="flex min-h-[calc(100vh-4rem)] items-center justify-center lg:min-h-0">
        <div class="w-full max-w-md">
          <div class="mb-7 text-center lg:hidden">
            <span class="mb-4 inline-flex h-14 w-14 items-center justify-center overflow-hidden rounded-xl bg-white p-2 ring-1 ring-white/10">
              <img :src="siteLogo || '/linx2-icon.png'" alt="Logo" class="h-full w-full object-contain" />
            </span>
            <h1 class="text-2xl font-semibold tracking-[-0.04em] text-linear-ink">{{ siteName }}</h1>
            <p class="mt-1 text-sm text-linear-ink-subtle">{{ siteSubtitle }}</p>
          </div>

          <div data-testid="auth-card" class="linx-panel-strong p-6 sm:p-8">
            <slot />
          </div>

          <div class="mt-5 text-center text-sm text-linear-ink-subtle">
            <slot name="footer" />
          </div>

          <div class="mt-8 text-center text-xs text-linear-ink-tertiary">
            &copy; {{ currentYear }} LINIX2.Ltd
          </div>
        </div>
      </main>
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

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>
