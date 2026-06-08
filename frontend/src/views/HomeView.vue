<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="trimmedHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="trimmedHomeContent"
      :title="`${siteName} custom home content`"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- Markdown/HTML mode -->
    <div v-else v-html="renderedHomeContent"></div>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="relative min-h-screen overflow-hidden bg-[#070503] text-stone-100 selection:bg-orange-500/30 selection:text-orange-100"
  >
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute inset-0 bg-[radial-gradient(circle_at_20%_10%,rgba(251,146,60,0.18),transparent_32%),radial-gradient(circle_at_80%_18%,rgba(249,115,22,0.12),transparent_28%),linear-gradient(180deg,rgba(7,5,3,0)_0%,#070503_72%)]"></div>
      <div class="absolute inset-0 opacity-[0.16] landing-grid"></div>
      <div class="absolute left-1/2 top-0 h-px w-[80vw] -translate-x-1/2 bg-gradient-to-r from-transparent via-orange-300/50 to-transparent"></div>
    </div>

    <header class="relative z-20 border-b border-orange-300/10 px-6 py-4 backdrop-blur-xl">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-6">
        <router-link to="/home" class="flex items-center gap-3" :aria-label="siteName">
          <span class="flex h-11 w-11 items-center justify-center rounded-2xl border border-orange-300/25 bg-orange-400/10 p-1.5 shadow-[0_0_32px_rgba(249,115,22,0.18)]">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full rounded-xl object-contain" />
          </span>
          <span class="text-base font-semibold tracking-[0.24em] text-orange-50">{{ siteName }}</span>
        </router-link>

        <div class="hidden items-center gap-8 text-sm font-medium text-stone-300 md:flex">
          <a
            v-for="item in navItems"
            :key="item.href"
            :href="item.href"
            class="transition-colors hover:text-orange-300"
          >
            {{ item.label }}
          </a>
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-orange-300"
          >
            文档<span class="sr-only"> {{ t('home.viewDocs') }}</span>
          </a>
        </div>

        <div class="flex items-center gap-2 sm:gap-3">
          <LocaleSwitcher />
          <button
            @click="toggleTheme"
            class="rounded-full border border-orange-300/15 bg-stone-950/60 p-2 text-stone-400 transition-colors hover:border-orange-300/40 hover:text-orange-200"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            :aria-label="isAuthenticated ? t('home.goToDashboard') : t('home.getStarted')"
            class="inline-flex h-10 w-10 items-center justify-center gap-2 rounded-full bg-orange-500 px-0 py-2 text-sm font-semibold text-black shadow-[0_0_28px_rgba(249,115,22,0.28)] transition-colors hover:bg-orange-300 sm:w-auto sm:px-4"
          >
            <span
              v-if="isAuthenticated && userInitial"
              class="flex h-5 w-5 items-center justify-center rounded-full bg-black/20 text-[10px]"
            >
              {{ userInitial }}
            </span>
            <span data-testid="header-cta-label" class="hidden sm:inline">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            </span>
            <Icon v-if="!isAuthenticated" name="arrowRight" size="sm" class="sm:hidden" :stroke-width="2" />
          </router-link>
        </div>
      </nav>
    </header>

    <main class="relative z-10">
      <section class="px-6 pb-20 pt-16 sm:pt-24">
        <div class="mx-auto grid max-w-7xl items-center gap-12 lg:grid-cols-[1.05fr_0.95fr]">
          <div>
            <div class="mb-6 inline-flex items-center gap-2 rounded-full border border-orange-300/20 bg-orange-400/10 px-4 py-2 text-xs font-semibold uppercase tracking-[0.28em] text-orange-200">
              <span class="h-1.5 w-1.5 rounded-full bg-orange-400 shadow-[0_0_16px_rgba(251,146,60,0.9)]"></span>
              Claude Code & Codex API
            </div>
            <h1 class="max-w-4xl text-5xl font-black leading-[0.95] tracking-[-0.06em] text-orange-50 sm:text-6xl xl:text-7xl">
              企业级编程 API 服务
            </h1>
            <p class="mt-6 max-w-2xl text-lg leading-8 text-stone-300 sm:text-xl">
              为 Claude Code、Codex 与兼容模型构建统一接入层，提供稳定、低延迟、可观测的企业级 API 调度体验。
            </p>

            <div class="mt-8 flex flex-wrap gap-3">
              <span
                v-for="tag in heroTags"
                :key="tag"
                class="rounded-full border border-stone-700/80 bg-stone-950/70 px-4 py-2 text-sm text-stone-300"
              >
                {{ tag }}
              </span>
            </div>

            <div class="mt-10 flex flex-col gap-4 sm:flex-row">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="inline-flex items-center justify-center rounded-full bg-orange-500 px-7 py-3 text-base font-bold text-black shadow-[0_0_42px_rgba(249,115,22,0.32)] transition-colors hover:bg-orange-300"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="md" class="ml-2" :stroke-width="2" />
              </router-link>
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center justify-center rounded-full border border-orange-300/25 px-7 py-3 text-base font-semibold text-orange-100 transition-colors hover:border-orange-300/60 hover:bg-orange-300/10"
              >
                {{ t('home.viewDocs') }}
                <Icon name="externalLink" size="sm" class="ml-2" />
              </a>
            </div>
          </div>

          <div class="relative">
            <div class="absolute -inset-6 rounded-[2rem] bg-orange-500/10 blur-3xl"></div>
            <div class="relative overflow-hidden rounded-[2rem] border border-orange-200/15 bg-[#100b07]/90 p-5 shadow-2xl shadow-black/60">
              <div class="mb-4 flex items-center justify-between border-b border-orange-200/10 pb-4">
                <div class="flex items-center gap-2">
                  <span class="h-3 w-3 rounded-full bg-[#ff5f57]"></span>
                  <span class="h-3 w-3 rounded-full bg-[#ffbd2e]"></span>
                  <span class="h-3 w-3 rounded-full bg-[#28c840]"></span>
                </div>
                <span class="font-mono text-xs text-stone-500">tokenstation.gateway</span>
              </div>
              <div class="space-y-4 font-mono text-sm leading-7 text-stone-300">
                <p><span class="text-orange-400">$</span> curl -X POST /v1/messages</p>
                <p class="text-stone-500"># route: claude-code → healthy account pool</p>
                <p><span class="rounded bg-orange-500/15 px-2 py-1 text-orange-300">200 OK</span> <span class="text-stone-400">latency=628ms</span></p>
                <p class="text-orange-100">{ "content": "enterprise coding API ready" }</p>
              </div>
              <div class="mt-6 grid gap-3 sm:grid-cols-3">
                <div
                  v-for="metric in metrics"
                  :key="metric.label"
                  class="rounded-2xl border border-orange-200/10 bg-black/30 p-4"
                >
                  <p class="text-2xl font-black text-orange-300">{{ metric.value }}</p>
                  <p class="mt-1 text-xs text-stone-500">{{ metric.label }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section id="services" class="px-6 py-16">
        <div class="mx-auto max-w-7xl">
          <div class="mb-8 flex flex-col justify-between gap-4 md:flex-row md:items-end">
            <div>
              <p class="text-sm font-semibold uppercase tracking-[0.3em] text-orange-400">Services</p>
              <h2 class="mt-3 text-3xl font-black tracking-tight text-orange-50 sm:text-4xl">为编程 Agent 打造的服务层</h2>
            </div>
            <p class="max-w-xl text-sm leading-7 text-stone-400">
              从接入、调度到账单监控，把多模型 API 运营能力收敛到一个可控入口。
            </p>
          </div>

          <div class="grid gap-5 md:grid-cols-3">
            <article
              v-for="feature in features"
              :key="feature.title"
              class="group rounded-[1.75rem] border border-orange-200/10 bg-stone-950/60 p-6 transition-colors hover:border-orange-300/35 hover:bg-orange-500/[0.06]"
            >
              <div class="mb-6 flex h-12 w-12 items-center justify-center rounded-2xl bg-orange-500/15 text-orange-300 ring-1 ring-orange-300/20">
                <Icon :name="feature.icon" size="lg" />
              </div>
              <h3 class="text-xl font-bold text-orange-50">{{ feature.title }}</h3>
              <p class="mt-3 text-sm leading-7 text-stone-400">{{ feature.description }}</p>
            </article>
          </div>
        </div>
      </section>

      <section id="advantages" class="px-6 py-16">
        <div class="mx-auto max-w-7xl rounded-[2rem] border border-orange-200/10 bg-[#0d0905]/80 p-6 sm:p-8">
          <div class="mb-8 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div>
              <p class="text-sm font-semibold uppercase tracking-[0.3em] text-orange-400">Advantages</p>
              <h2 class="mt-3 text-3xl font-black tracking-tight text-orange-50">支持多提供商统一编排</h2>
            </div>
            <p class="max-w-lg text-sm leading-7 text-stone-400">
              以 OpenAI 兼容体验接入主流编程模型，后续服务商可持续扩展。
            </p>
          </div>
          <div class="flex flex-wrap gap-3">
            <span
              v-for="provider in providers"
              :key="provider"
              class="rounded-full border border-orange-200/[0.12] bg-black/25 px-5 py-3 text-sm font-semibold text-stone-200"
            >
              {{ provider }}
            </span>
          </div>
        </div>
      </section>

      <section class="px-6 py-20">
        <div class="mx-auto max-w-5xl rounded-[2.25rem] border border-orange-300/15 bg-gradient-to-br from-orange-500/[0.16] to-stone-950 p-8 text-center shadow-[0_0_80px_rgba(249,115,22,0.12)] sm:p-12">
          <p class="text-sm font-semibold uppercase tracking-[0.3em] text-orange-300">Ready for production</p>
          <h2 class="mt-4 text-3xl font-black tracking-tight text-orange-50 sm:text-5xl">把编程 API 纳入企业级运营</h2>
          <p class="mx-auto mt-5 max-w-2xl text-base leading-8 text-stone-300">
            从第一条请求开始获得统一鉴权、账号池调度、用量透明和可观测链路。
          </p>
          <div class="mt-8 flex flex-col justify-center gap-4 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center justify-center rounded-full bg-orange-500 px-7 py-3 text-base font-bold text-black transition-colors hover:bg-orange-300"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            </router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center justify-center rounded-full border border-orange-300/30 px-7 py-3 text-base font-semibold text-orange-100 transition-colors hover:bg-orange-300/10"
            >
              {{ t('home.docs') }}
            </a>
          </div>
        </div>
      </section>
    </main>

    <footer class="relative z-10 border-t border-orange-200/10 px-6 py-8">
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-4 text-center text-sm text-stone-500 sm:flex-row sm:text-left">
        <p>&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</p>
        <div class="flex items-center gap-5">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-orange-300"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-orange-300"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'

type IconName = InstanceType<typeof Icon>['$props']['name']

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

const landingIconUrl = '/landing-icon.jpg'

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const brandLogo = computed(() => siteLogo.value || landingIconUrl)
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const trimmedHomeContent = computed(() => homeContent.value.trim())
const renderedHomeContent = computed(() => DOMPurify.sanitize(marked.parse(homeContent.value) as string))

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = trimmedHomeContent.value
  return content.startsWith('http://') || content.startsWith('https://')
})

const navItems = [
  { label: '服务', href: '#services' },
  { label: '优势', href: '#advantages' },
]

const heroTags = ['统一 API', '智能调度', '透明计费']

const metrics = [
  { value: '99.9%', label: '可用性' },
  { value: '628ms', label: '示例延迟' },
  { value: '24/7', label: '监控' },
]

const features: Array<{ title: string; description: string; icon: IconName }> = [
  {
    title: '统一 API 接入',
    description: '以统一协议聚合 Claude Code、Codex 与兼容服务，减少团队接入和迁移成本。',
    icon: 'terminal',
  },
  {
    title: '智能账号池调度',
    description: '按健康度、额度和延迟动态路由请求，让高峰期调用保持稳定可控。',
    icon: 'swap',
  },
  {
    title: '用量与余额管理',
    description: '集中观察消耗、余额和调用趋势，让成本、配额和异常都清晰可见。',
    icon: 'chart',
  },
]

const providers = ['Claude Code', 'Codex', 'Gemini', 'OpenAI 兼容', '更多即将支持']

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
.landing-grid {
  background-image:
    linear-gradient(rgba(251, 146, 60, 0.45) 1px, transparent 1px),
    linear-gradient(90deg, rgba(251, 146, 60, 0.45) 1px, transparent 1px);
  background-size: 64px 64px;
  mask-image: radial-gradient(circle at 50% 18%, black, transparent 68%);
}
</style>
