<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="trimmedHomeContent" class="min-h-screen">
    <iframe
      v-if="isHomeContentUrl"
      :src="trimmedHomeContent"
      :title="`${siteName} custom home content`"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <div v-else v-html="renderedHomeContent"></div>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="linx2-landing min-h-screen bg-[#0a0b0e] text-zinc-100 selection:bg-orange-500/30 selection:text-orange-100"
  >
    <!-- Announcement bar -->
    <div
      v-if="showAnnouncement"
      class="relative z-30 flex items-center justify-center gap-3 border-b border-orange-300/15 bg-orange-500/[0.08] px-4 py-2.5 text-center text-xs font-semibold text-orange-200 sm:text-sm"
    >
      <span class="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-orange-400 shadow-[0_0_12px_rgba(251,146,60,0.9)]"></span>
      <span>{{ copy.announcement }}</span>
      <button
        class="absolute right-3 top-1/2 -translate-y-1/2 text-orange-300/60 transition-colors hover:text-orange-200"
        :aria-label="'close'"
        @click="showAnnouncement = false"
      >
        <Icon name="x" size="sm" />
      </button>
    </div>

    <!-- Header -->
    <header class="sticky top-0 z-20 border-b border-white/[0.06] bg-[#0a0b0e]/80 backdrop-blur-xl">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-6 px-4 py-4 sm:px-6 lg:px-8">
        <router-link to="/home" class="group flex items-center gap-3" :aria-label="siteName">
          <span class="brand-tile flex h-10 w-10 items-center justify-center rounded-2xl bg-white p-1.5 shadow-[0_8px_24px_rgba(249,115,22,0.22)] ring-1 ring-black/5 transition-transform duration-300 group-hover:-rotate-6">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span class="leading-tight">
            <span class="font-display block text-base font-extrabold tracking-[0.06em] text-zinc-50">{{ siteName }}</span>
            <span class="block text-[10px] font-semibold uppercase tracking-[0.22em] text-zinc-500">AI Coding API</span>
          </span>
        </router-link>

        <div class="hidden items-center gap-8 text-sm font-semibold text-zinc-400 md:flex">
          <a href="#capabilities" class="transition-colors hover:text-orange-300">{{ copy.nav.capabilities }}</a>
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-orange-300"
          >
            {{ t('home.docs') }}
          </a>
        </div>

        <div class="flex items-center gap-2 sm:gap-3">
          <LocaleSwitcher />
          <button
            @click="toggleTheme"
            class="rounded-full border border-white/10 bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:border-orange-300/40 hover:text-orange-200"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            :aria-label="isAuthenticated ? t('home.goToDashboard') : t('home.getStarted')"
            class="inline-flex h-10 items-center justify-center gap-2 rounded-full bg-orange-500 px-4 py-2 text-sm font-bold text-black shadow-[0_0_28px_rgba(249,115,22,0.30)] transition-colors hover:bg-orange-400"
          >
            <span
              v-if="isAuthenticated && userInitial"
              class="flex h-5 w-5 items-center justify-center rounded-full bg-black/20 text-[10px]"
            >
              {{ userInitial }}
            </span>
            <span data-testid="header-cta-label">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            </span>
          </router-link>
        </div>
      </nav>
    </header>

    <main>
      <!-- ===== Hero ===== -->
      <section class="mx-auto max-w-7xl px-4 py-16 text-center sm:px-6 sm:py-20 lg:px-8 lg:py-24">
        <div class="mx-auto max-w-4xl animate-rise">
          <p class="inline-flex items-center gap-2 rounded-full border border-orange-300/20 bg-orange-400/10 px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-orange-200">
            <span class="h-1.5 w-1.5 rounded-full bg-orange-400 shadow-[0_0_14px_rgba(251,146,60,0.9)]"></span>
            {{ copy.heroKicker }}
          </p>
          <h1 class="font-display mt-7 text-[clamp(2.75rem,7vw,5.5rem)] font-extrabold leading-[0.96] tracking-[-0.05em] text-zinc-50">
            {{ copy.heroTitle }}
          </h1>
          <p class="mx-auto mt-6 max-w-2xl text-base leading-7 text-zinc-400 sm:text-lg sm:leading-8">
            {{ copy.heroDescription }}
          </p>
          <div class="mt-9 flex flex-col justify-center gap-3 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center justify-center rounded-full bg-orange-500 px-6 py-3.5 text-sm font-extrabold text-black shadow-[0_0_42px_rgba(249,115,22,0.32)] transition-all hover:-translate-y-0.5 hover:bg-orange-400"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" class="ml-2" :stroke-width="2" />
            </router-link>
            <a
              :href="docUrl || '#capabilities'"
              :target="docUrl ? '_blank' : undefined"
              :rel="docUrl ? 'noopener noreferrer' : undefined"
              class="inline-flex items-center justify-center rounded-full border border-white/15 bg-white/[0.02] px-6 py-3.5 text-sm font-extrabold text-zinc-100 transition-all hover:-translate-y-0.5 hover:border-orange-300/50 hover:bg-orange-300/[0.06]"
            >
              {{ docUrl ? copy.docsCta : copy.learnCta }}
            </a>
          </div>
        </div>

        <!-- Gateway illustration -->
        <div id="models" class="mx-auto mt-14 w-full max-w-5xl animate-rise-delayed">
          <div class="relative overflow-hidden rounded-[2rem] border border-white/[0.08] bg-white/[0.02] p-3 shadow-2xl shadow-black/50 sm:p-4">
            <div class="absolute -right-16 -top-16 h-48 w-48 rounded-full bg-orange-500/10 blur-3xl"></div>
            <div class="relative rounded-[1.5rem] bg-[#101216]/80 p-4 sm:p-6">
              <div class="mb-4 flex items-center justify-between gap-4 text-left">
                <div>
                  <h2 class="font-display text-sm font-extrabold text-zinc-50">{{ copy.gw.title }}</h2>
                  <p class="mt-1 text-xs font-medium text-zinc-500">{{ copy.gw.description }}</p>
                </div>
                <span class="flex items-center gap-1.5 rounded-full bg-orange-500/15 px-3 py-1.5 text-xs font-bold text-orange-300">
                  <span class="h-1.5 w-1.5 rounded-full bg-orange-400"></span>
                  {{ copy.gw.badge }}
                </span>
              </div>

              <div class="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
                <div
                  v-for="provider in providers"
                  :key="provider"
                  class="rounded-2xl border border-white/[0.06] bg-white/[0.03] px-4 py-5 text-center text-sm font-extrabold text-zinc-200"
                >
                  {{ provider }}
                </div>
              </div>

              <div class="mt-4 grid gap-3 lg:grid-cols-[1.1fr_0.9fr]">
                <div class="rounded-3xl border border-white/[0.06] bg-black/20 p-5 text-left">
                  <p class="font-mono-brand text-xs font-bold uppercase tracking-[0.18em] text-zinc-500">{{ copy.gw.flowTitle }}</p>
                  <div class="mt-5 grid gap-3 sm:grid-cols-3">
                    <div v-for="(step, i) in copy.gw.flow" :key="step.title" class="rounded-2xl bg-white/[0.03] p-4">
                      <span class="font-mono-brand text-[10px] font-bold text-orange-400">0{{ i + 1 }}</span>
                      <p class="font-display mt-1 text-sm font-extrabold text-zinc-50">{{ step.title }}</p>
                      <p class="mt-2 text-xs leading-5 text-zinc-500">{{ step.description }}</p>
                    </div>
                  </div>
                </div>

                <div class="rounded-3xl border border-orange-300/15 bg-gradient-to-br from-orange-500/[0.12] to-black/30 p-5 text-left">
                  <p class="font-mono-brand text-xs font-bold uppercase tracking-[0.18em] text-orange-300/80">{{ copy.gw.baseUrlTitle }}</p>
                  <pre class="font-mono-brand mt-4 overflow-x-auto rounded-2xl bg-black/40 p-4 text-left text-xs leading-6 text-zinc-200"><code><span class="text-orange-300">ANTHROPIC_BASE_URL</span>=https://linx2.ai/api
<span class="text-orange-300">ANTHROPIC_API_KEY</span>=lx2_<span class="text-zinc-500">••••••••</span></code></pre>
                  <div class="mt-4 grid grid-cols-3 gap-2">
                    <div v-for="metric in metrics" :key="metric.label" class="rounded-2xl bg-white/[0.04] p-3 text-center">
                      <p class="font-display text-lg font-extrabold text-orange-300">{{ metric.value }}</p>
                      <p class="mt-0.5 text-[10px] leading-tight text-zinc-500">{{ metric.label }}</p>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- ===== Features ===== -->
      <section id="features" class="border-y border-white/[0.06] bg-white/[0.015]">
        <div class="mx-auto grid max-w-7xl gap-4 px-4 py-6 sm:px-6 md:grid-cols-3 lg:px-8">
          <article
            v-for="feature in copy.features"
            :key="feature.title"
            class="rounded-3xl border border-white/[0.07] bg-white/[0.02] p-7 text-left transition-colors hover:border-orange-300/25 hover:bg-orange-500/[0.04]"
          >
            <p class="font-display text-sm font-extrabold text-zinc-50">{{ feature.title }}</p>
            <p class="mt-3 text-sm leading-6 text-zinc-400">{{ feature.description }}</p>
          </article>
        </div>
      </section>

      <!-- ===== Capabilities ===== -->
      <section id="capabilities" class="mx-auto max-w-7xl scroll-mt-24 px-4 py-16 sm:px-6 lg:px-8">
        <div class="grid gap-8 xl:grid-cols-[0.8fr_1.2fr] xl:items-end">
          <div class="text-left">
            <p class="font-mono-brand text-sm font-bold uppercase tracking-[0.18em] text-orange-400">{{ copy.capabilityKicker }}</p>
            <h2 class="font-display mt-4 max-w-3xl text-[clamp(2rem,4vw,2.9rem)] font-extrabold leading-tight tracking-[-0.04em] text-zinc-50">
              {{ copy.capabilityTitle }}
            </h2>
          </div>
          <p class="text-left text-base leading-7 text-zinc-400">{{ copy.capabilityDescription }}</p>
        </div>

        <div class="mt-10 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          <article
            v-for="capability in copy.capabilities"
            :key="capability.title"
            class="group rounded-3xl border border-white/[0.07] bg-white/[0.02] p-6 text-left transition-colors hover:border-orange-300/25"
          >
            <p class="font-mono-brand text-xs font-bold uppercase tracking-[0.18em] text-orange-400/80">{{ capability.code }}</p>
            <h3 class="font-display mt-4 text-xl font-extrabold tracking-[-0.02em] text-zinc-50">{{ capability.title }}</h3>
            <p class="mt-3 text-sm leading-6 text-zinc-400">{{ capability.description }}</p>
          </article>
        </div>
      </section>

      <!-- ===== Pricing ===== -->
      <section id="pricing" class="scroll-mt-24 border-t border-white/[0.06] bg-gradient-to-b from-white/[0.02] to-transparent">
        <div class="mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8">
          <div class="mb-10 max-w-2xl">
            <p class="font-mono-brand text-sm font-bold uppercase tracking-[0.18em] text-orange-400">{{ copy.pricingKicker }}</p>
            <h2 class="font-display mt-4 text-[clamp(2rem,4vw,2.9rem)] font-extrabold leading-tight tracking-[-0.04em] text-zinc-50">{{ copy.pricingTitle }}</h2>
            <p class="mt-4 text-base leading-7 text-zinc-400">
              {{ copy.pricingDescription }}
            </p>
          </div>

          <div class="grid gap-5 md:grid-cols-3">
            <article
              v-for="group in pricingGroups"
              :key="group.provider"
              class="group relative overflow-hidden rounded-[1.75rem] border border-white/[0.08] bg-white/[0.02] p-6 transition-colors hover:border-orange-300/25 hover:bg-orange-500/[0.04]"
            >
              <div class="absolute -right-10 -top-10 h-28 w-28 rounded-full bg-orange-500/10 opacity-0 blur-2xl transition-opacity duration-300 group-hover:opacity-100"></div>
              <div class="mb-5 flex items-center justify-between">
                <h3 class="font-display text-xl font-bold text-zinc-50">{{ group.provider }}</h3>
                <span class="font-mono-brand rounded-full border border-white/10 px-2.5 py-1 text-[10px] uppercase tracking-wider text-zinc-500">{{ group.tag }}</span>
              </div>

              <div class="grid grid-cols-[1fr_auto_auto] items-center gap-x-3 border-b border-white/[0.06] pb-2 text-[11px] font-medium uppercase tracking-wide text-zinc-500">
                <span>{{ copy.pricingCols.model }}</span>
                <span class="text-right">{{ copy.pricingCols.input }}</span>
                <span class="text-right">{{ copy.pricingCols.output }}</span>
              </div>
              <ul class="divide-y divide-white/[0.05]">
                <li
                  v-for="model in group.models"
                  :key="model.name"
                  class="grid grid-cols-[1fr_auto_auto] items-center gap-x-3 py-3"
                >
                  <span class="text-sm font-medium text-zinc-200">{{ model.name }}</span>
                  <span class="font-mono-brand text-right text-sm text-zinc-300">{{ model.in }}</span>
                  <span class="font-mono-brand text-right text-sm font-semibold text-orange-300">{{ model.out }}</span>
                </li>
              </ul>
            </article>
          </div>

          <p class="mt-6 text-xs text-zinc-600">{{ copy.pricingFootnote }}</p>
        </div>
      </section>

      <!-- ===== CTA ===== -->
      <section class="px-4 py-16 sm:px-6 lg:px-8">
        <div class="mx-auto max-w-5xl overflow-hidden rounded-[2.25rem] border border-orange-300/15 bg-gradient-to-br from-orange-500/[0.16] to-[#0a0b0e] p-8 text-center shadow-[0_0_80px_rgba(249,115,22,0.12)] sm:p-12">
          <p class="font-mono-brand text-sm font-bold uppercase tracking-[0.2em] text-orange-300">{{ copy.ctaKicker }}</p>
          <h2 class="font-display mt-4 text-3xl font-extrabold tracking-tight text-zinc-50 sm:text-5xl">{{ copy.ctaTitle }}</h2>
          <p class="mx-auto mt-5 max-w-2xl text-base leading-8 text-zinc-300">{{ copy.ctaDescription }}</p>
          <div class="mt-8 flex flex-col justify-center gap-4 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center justify-center rounded-full bg-orange-500 px-7 py-3 text-base font-bold text-black transition-colors hover:bg-orange-400"
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

    <!-- ===== Footer ===== -->
    <footer class="border-t border-white/[0.06] px-4 py-8 sm:px-6 lg:px-8">
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-4 text-center text-sm text-zinc-500 sm:flex-row sm:text-left">
        <div class="flex items-center gap-2.5">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-white p-1 ring-1 ring-black/5">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span>&copy; {{ currentYear }} LINIX2.Ltd</span>
        </div>
        <div v-if="docUrl" class="flex items-center gap-5">
          <a
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-orange-300"
          >
            {{ t('home.docs') }}
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

const { t, locale } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

const brandIconUrl = '/linx2-icon.png'

// Site settings
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'LINX2')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const brandLogo = computed(() => siteLogo.value || brandIconUrl)
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const trimmedHomeContent = computed(() => homeContent.value.trim())
const renderedHomeContent = computed(() => DOMPurify.sanitize(marked.parse(homeContent.value) as string))

const isHomeContentUrl = computed(() => {
  const content = trimmedHomeContent.value
  return content.startsWith('http://') || content.startsWith('https://')
})

const showAnnouncement = ref(true)

const providers = ['Claude', 'Codex', 'Gemini', 'Messages', 'Responses', 'Images']

const metrics = [
  { value: '99.9%', label: '可用性 Uptime' },
  { value: '<1s', label: '首字延迟 TTFB' },
  { value: '24/7', label: '监控 Monitor' },
]

// Model pricing — USD per 1M tokens (官方原价透传, source: backend model-pricing).
type PriceRow = { name: string; in: string; out: string }
type PriceGroup = { provider: string; tag: string; models: PriceRow[] }
const pricingGroups: PriceGroup[] = [
  {
    provider: 'Claude',
    tag: 'Anthropic',
    models: [
      { name: 'Opus 4.5', in: '$5.00', out: '$25.00' },
      { name: 'Sonnet 4.5', in: '$3.00', out: '$15.00' },
      { name: 'Haiku 4.5', in: '$1.00', out: '$5.00' },
    ],
  },
  {
    provider: 'OpenAI',
    tag: 'GPT · Codex',
    models: [
      { name: 'GPT-5', in: '$1.25', out: '$10.00' },
      { name: 'GPT-5 mini', in: '$0.25', out: '$2.00' },
      { name: 'o3', in: '$2.00', out: '$8.00' },
    ],
  },
  {
    provider: 'Gemini',
    tag: 'Google',
    models: [
      { name: 'Gemini 2.5 Pro', in: '$1.25', out: '$10.00' },
      { name: 'Gemini 2.5 Flash', in: '$0.30', out: '$2.50' },
    ],
  },
]

// Bilingual marketing copy (mirrors LocaleSwitcher in the header).
const copies = {
  zh: {
    announcement: '统一 Claude Code · Codex · Gemini 官方原生通道，国内稳定直连',
    nav: { capabilities: '能力', pricing: '价格' },
    heroKicker: '统一 AI 编程 API · OpenAI 兼容路由',
    heroTitle: '一个密钥，接入你需要的所有编程模型。',
    heroDescription:
      '通过统一的计费、用量与访问控制层，转发 Claude Code、Codex 与 Gemini 兼容请求。无需繁琐配置、无需海外信用卡，开箱即用。',
    docsCta: '查看文档',
    learnCta: '了解能力',
    gw: {
      title: '供应商与能力墙',
      description: '一个平台密钥路由到兼容的模型 API。',
      badge: '可用路由',
      flowTitle: '网关流程',
      baseUrlTitle: '可复制 Base URL',
      flow: [
        { title: '应用请求', description: '使用供应商兼容客户端和一个 LINX2 密钥。' },
        { title: '余额保护', description: '转发模型流量前检查账户访问和余额。' },
        { title: '用量账本', description: '记录模型、Token、状态和费用轨迹。' },
      ],
    },
    features: [
      { title: '一个密钥接入所有模型', description: '为应用、Agent 和实验发放平台密钥，上游官方凭证安全保留在网关背后。' },
      { title: '余额感知准入', description: '请求前进行余额保护，供应商响应后记录实际用量和费用，账单清晰可控。' },
      { title: '官方原生 · 稳定直连', description: '官方原价透传、原生模型通道，专线优化国内直连，减少等待与网络折损。' },
    ],
    capabilityKicker: '能力概览',
    capabilityTitle: '覆盖主流编程模型，同时保留运营控制。',
    capabilityDescription:
      'LINX2 的表达保持简单：给开发者兼容模型路由，给运营者余额保护，并提供商业可见的用量记录。',
    capabilities: [
      { code: 'MESSAGES', title: 'Anthropic 风格调用', description: '在支持的流程中使用熟悉的 messages 路由、流式、工具和多模态请求。' },
      { code: 'RESPONSES', title: 'OpenAI 兼容路径', description: '让应用客户端尽量保持标准 OpenAI 风格请求结构，迁移成本极低。' },
      { code: 'GEMINI', title: 'Gemini 路由族', description: '与 Claude、OpenAI 模型访问模式并列提供 Gemini 兼容网关路由。' },
      { code: 'LEDGER', title: '用量与计费层', description: '跟踪模型、Token、状态和费用记录，并提供账户级余额保护。' },
    ],
    pricingKicker: '模型价格',
    pricingTitle: '官方原价透传，无隐藏加价。',
    pricingDescription: '下列为各模型单价，单位为美元 / 每百万 tokens（USD / 1M tokens）。',
    pricingCols: { model: '模型', input: '输入', output: '输出' },
    pricingFootnote: '价格随上游官方调整，以控制台实际计费为准 · 缓存读写另按官方比例计价。',
    ctaKicker: 'Ready when you are',
    ctaTitle: '几分钟接入，立即开始编程',
    ctaDescription: '注册后获取专属 API Key，把 Claude Code、Codex 与 Gemini 纳入统一、稳定、透明计费的编程通道。',
  },
  en: {
    announcement: 'Unified official-native routes for Claude Code · Codex · Gemini — stable direct access',
    nav: { capabilities: 'Capabilities', pricing: 'Pricing' },
    heroKicker: 'Unified AI Coding API · OpenAI-compatible routes',
    heroTitle: 'One key for every model your code needs.',
    heroDescription:
      'Route Claude Code, Codex and Gemini-compatible requests through one billing, usage and access layer. No tedious setup, no overseas card — ready out of the box.',
    docsCta: 'Read docs',
    learnCta: 'Explore',
    gw: {
      title: 'Provider and capability wall',
      description: 'One platform key routes to compatible model APIs.',
      badge: 'Live routes',
      flowTitle: 'Gateway flow',
      baseUrlTitle: 'Copy-ready base URL',
      flow: [
        { title: 'App request', description: 'Use provider-compatible clients and one LINX2 key.' },
        { title: 'Balance guard', description: 'Check account access and balance before forwarding traffic.' },
        { title: 'Usage ledger', description: 'Record model, token, status, and cost traces.' },
      ],
    },
    features: [
      { title: 'One key for every model', description: 'Issue platform keys for apps, agents and experiments while official upstream credentials stay behind the gateway.' },
      { title: 'Balance-aware admission', description: 'Predictable billing guards before requests exceed balance, then record real usage after provider responses.' },
      { title: 'Official-native, stable', description: 'Official pass-through pricing and native model routes, with optimized lines for stable low-latency access.' },
    ],
    capabilityKicker: 'Capability overview',
    capabilityTitle: 'Provider breadth without hiding operational controls.',
    capabilityDescription:
      'LINX2 keeps the story simple: compatible model routes for builders, balance protection for operators, and usage records for commercial visibility.',
    capabilities: [
      { code: 'MESSAGES', title: 'Anthropic-style calls', description: 'Use familiar message routes for text, streaming, tools and multimodal flows where supported.' },
      { code: 'RESPONSES', title: 'OpenAI-compatible paths', description: 'Keep application clients close to standard OpenAI-style request shapes for compatible workloads.' },
      { code: 'GEMINI', title: 'Gemini route family', description: 'Expose Gemini-compatible gateway routes alongside Claude and OpenAI model access patterns.' },
      { code: 'LEDGER', title: 'Usage and billing layer', description: 'Track model, token, status and cost records with account-level balance protection.' },
    ],
    pricingKicker: 'Model pricing',
    pricingTitle: 'Official pass-through pricing, no hidden markup.',
    pricingDescription: 'Per-model rates below, in US dollars per 1M tokens (USD / 1M tokens).',
    pricingCols: { model: 'Model', input: 'Input', output: 'Output' },
    pricingFootnote: 'Prices track upstream official changes; the console billing is authoritative · cache read/write billed at official ratios.',
    ctaKicker: 'Ready when you are',
    ctaTitle: 'Connect in minutes, start coding now',
    ctaDescription: 'Sign up to get your API key and bring Claude Code, Codex and Gemini into one stable, transparent coding gateway.',
  },
} as const

const localeCode = computed(() => (locale.value === 'zh' ? 'zh' : 'en'))
const copy = computed(() => copies[localeCode.value])

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

const currentYear = computed(() => new Date().getFullYear())

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (savedTheme === 'dark' || (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

// Load distinctive brand fonts only for the landing page (keeps dashboard lean).
function ensureBrandFonts() {
  if (document.getElementById('linx2-brand-fonts')) return
  const link = document.createElement('link')
  link.id = 'linx2-brand-fonts'
  link.rel = 'stylesheet'
  link.href =
    'https://fonts.googleapis.com/css2?family=Bricolage+Grotesque:opsz,wght@12..96,500..800&family=Manrope:wght@400..700&family=JetBrains+Mono:wght@400..600&display=swap'
  document.head.appendChild(link)
}

onMounted(() => {
  initTheme()
  ensureBrandFonts()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
.linx2-landing {
  font-family: 'Manrope', system-ui, -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif;
}

.font-display {
  font-family: 'Bricolage Grotesque', 'Manrope', system-ui, 'PingFang SC', sans-serif;
}

.font-mono-brand {
  font-family: 'JetBrains Mono', ui-monospace, 'SFMono-Regular', Menlo, monospace;
}

@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(18px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.animate-rise {
  animation: rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

.animate-rise-delayed {
  animation: rise 0.8s cubic-bezier(0.22, 1, 0.36, 1) 0.12s both;
}

@media (prefers-reduced-motion: reduce) {
  .animate-rise,
  .animate-rise-delayed {
    animation: none;
  }
}
</style>
