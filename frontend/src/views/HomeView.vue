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
    class="linear-landing min-h-screen bg-linear-canvas text-linear-ink selection:bg-primary-500/30 selection:text-primary-900 dark:selection:text-primary-100"
  >
    <!-- Announcement bar -->
    <div
      v-if="showAnnouncement"
      class="relative z-30 flex items-center justify-center gap-3 border-b border-linear-hairline bg-linear-surface-1/70 px-4 py-2.5 text-center text-xs font-medium text-linear-ink-muted sm:text-sm"
    >
      <span class="ui-accent-dot h-1.5 w-1.5 flex-shrink-0 rounded-full"></span>
      <Transition name="banner-fade" mode="out-in">
        <span :key="currentBannerIndex">{{ currentBannerText }}</span>
      </Transition>
      <button
        class="absolute right-3 top-1/2 -translate-y-1/2 text-linear-ink-tertiary transition-colors hover:text-linear-ink"
        :aria-label="'close'"
        @click="dismissAnnouncement"
      >
        <Icon name="x" size="sm" />
      </button>
    </div>

    <!-- Header -->
    <header class="sticky top-0 z-20 border-b border-linear-hairline bg-linear-canvas/90 backdrop-blur-xl">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-6 px-4 py-3 sm:px-6 lg:px-8">
        <router-link to="/home" class="group flex items-center gap-3" :aria-label="siteName">
          <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-linear-hairline transition-colors group-hover:ring-linear-hairline-strong">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span class="hidden leading-tight sm:block">
            <span class="block text-sm font-semibold tracking-[-0.02em] text-linear-ink">
              <LinxWordmark v-if="usesDefaultBrand" />
              <span v-else>{{ siteName }}</span>
            </span>
            <span class="hidden max-w-[16rem] truncate text-[10px] font-medium uppercase tracking-[0.22em] text-linear-ink-tertiary sm:block">{{ siteSubtitle }}</span>
          </span>
        </router-link>

        <div data-testid="homepage-header-actions" class="ml-auto flex items-center gap-2 sm:gap-3">
          <div class="hidden items-center gap-6 text-sm font-medium text-linear-ink-subtle md:flex">
            <a href="#capabilities" class="transition-colors hover:text-linear-ink">{{ copy.nav.capabilities }}</a>
            <router-link
              to="/getting-started"
              data-testid="beginner-guide-nav-link"
              class="transition-colors hover:text-linear-ink"
            >
              {{ t('gettingStarted.discovery.navLabel') }}
            </router-link>
            <router-link
              to="/docs"
              data-testid="api-docs-nav-link"
              class="transition-colors hover:text-linear-ink"
            >
              {{ t('apiDocs.navLabel') }}
            </router-link>
            <router-link v-if="isAuthenticated" :to="chatPath" class="transition-colors hover:text-linear-ink">{{ t('chat.openWebChatShort') }}</router-link>
            <router-link v-if="isAuthenticated" to="/models" class="transition-colors hover:text-linear-ink">{{ t('nav.modelMarketplace') }}</router-link>
            <a href="#pricing" class="transition-colors hover:text-linear-ink">{{ copy.nav.pricing }}</a>
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
          <router-link
            to="/docs"
            data-testid="api-docs-mobile-nav-link"
            class="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-linear-ink-muted outline-none transition-colors hover:bg-linear-surface-1 hover:text-linear-ink focus-visible:ring-2 focus-visible:ring-primary-500/50 md:hidden"
            :aria-label="t('apiDocs.navLabel')"
          >
            <Icon name="document" size="sm" aria-hidden="true" />
            <span class="sr-only">{{ t('apiDocs.navLabel') }}</span>
          </router-link>
          <LocaleSwitcher />
          <button
            data-testid="homepage-theme-toggle"
            @click="toggleTheme"
            class="ui-theme-toggle"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" class="ui-theme-icon-accent" />
            <Icon v-else name="moon" size="md" class="ui-theme-icon-accent" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            :aria-label="isAuthenticated ? t('home.goToDashboard') : t('home.getStarted')"
            class="inline-flex h-10 shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-400"
          >
            <span
              v-if="isAuthenticated && userInitial"
              class="ui-avatar-identity-sm"
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
      <section class="mx-auto max-w-7xl px-4 py-16 sm:px-6 sm:py-20 lg:px-8 lg:py-24">
        <div class="mx-auto max-w-4xl text-center">
          <p class="linx-section-kicker inline-flex items-center gap-2">
            <span class="ui-accent-dot h-1.5 w-1.5 rounded-full"></span>
            {{ copy.heroKicker }}
          </p>
          <h1 class="mt-7 text-[clamp(2.75rem,7vw,5.25rem)] font-semibold leading-[0.98] tracking-[-0.065em] text-linear-ink">
            {{ copy.heroTitle }}
          </h1>
          <p class="mx-auto mt-6 max-w-2xl text-base leading-7 tracking-[-0.01em] text-linear-ink-subtle sm:text-lg sm:leading-8">
            {{ copy.heroDescription }}
          </p>
          <div data-testid="homepage-hero-actions" class="mt-9 flex flex-col justify-center gap-3 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center justify-center rounded-lg bg-primary-500 px-5 py-3 text-sm font-medium text-white transition-colors hover:bg-primary-400"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" class="ml-2" :stroke-width="2" />
            </router-link>
            <a
              :href="docUrl || '#capabilities'"
              :target="docUrl ? '_blank' : undefined"
              :rel="docUrl ? 'noopener noreferrer' : undefined"
              class="inline-flex items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 px-5 py-3 text-sm font-medium text-linear-ink transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
            >
              {{ docUrl ? copy.docsCta : copy.learnCta }}
            </a>
          </div>
        </div>

        <BeginnerGuideCard />

        <div
          v-if="isAuthenticated"
          data-testid="homepage-chat-entry"
          class="mx-auto mt-12 grid w-full max-w-6xl overflow-hidden rounded-lg border border-linear-hairline bg-linear-surface-1 text-left lg:grid-cols-[280px_minmax(0,1fr)]"
        >
          <aside data-testid="homepage-chat-demo-rail" class="flex min-h-0 flex-col border-b border-linear-hairline bg-linear-surface-0 lg:border-b-0 lg:border-r">
            <div class="flex items-center gap-2 border-b border-linear-hairline px-3 py-3">
              <router-link
                :to="chatPath"
                class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
                :aria-label="t('chat.openWebChat')"
              >
                <Icon name="plus" size="sm" />
                <span class="sr-only">{{ t('chat.newChat') }}</span>
              </router-link>
              <div class="relative min-w-0 flex-1">
                <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-linear-ink-tertiary" />
                <input
                  class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-canvas pl-9 pr-3 text-sm text-linear-ink outline-none placeholder:text-linear-ink-tertiary"
                  type="search"
                  :placeholder="t('chat.searchConversations')"
                  readonly
                />
              </div>
            </div>

            <div class="min-h-0 flex-1 px-2 py-3">
              <p class="px-2 text-xs font-medium text-linear-ink-tertiary">Recent conversations</p>
              <div class="mt-2 space-y-1">
                <div class="rounded-lg bg-linear-surface-2 px-2.5 py-2">
                  <span class="block truncate text-sm font-medium text-linear-ink">{{ t('chat.newChat') }}</span>
                  <span class="mt-0.5 block truncate text-xs text-linear-ink-tertiary">GPT-Image-2</span>
                </div>
                <div class="rounded-lg px-2.5 py-2 transition-colors hover:bg-linear-surface-1">
                  <span class="block truncate text-sm font-medium text-linear-ink-muted">Image brief iteration</span>
                  <span class="mt-0.5 block truncate text-xs text-linear-ink-tertiary">gpt-image-2</span>
                </div>
                <div class="rounded-lg px-2.5 py-2 transition-colors hover:bg-linear-surface-1">
                  <span class="block truncate text-sm font-medium text-linear-ink-muted">Claude Code handoff</span>
                  <span class="mt-0.5 block truncate text-xs text-linear-ink-tertiary">claude-sonnet-4-6</span>
                </div>
              </div>
            </div>
          </aside>

          <section class="flex min-h-[420px] min-w-0 flex-col bg-linear-canvas">
            <header data-testid="homepage-chat-demo-model-selector" class="border-b border-linear-hairline bg-linear-canvas px-4 py-3">
              <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                <div class="grid gap-2 sm:grid-cols-[minmax(9rem,12rem)_minmax(12rem,22rem)]">
                  <div>
                    <span class="mb-1 block text-xs font-medium text-linear-ink-tertiary">Provider</span>
                    <div class="flex h-9 w-full items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-left text-sm text-linear-ink">
                      <ModelIcon :model="providerIconModel('openai')" size="16px" aria-hidden="true" />
                      <span class="min-w-0 flex-1 truncate">OpenAI</span>
                      <Icon name="chevronDown" size="sm" class="shrink-0 text-linear-ink-tertiary" />
                    </div>
                  </div>
                  <label class="block">
                    <span class="mb-1 block text-xs font-medium text-linear-ink-tertiary">Model</span>
                    <select
                      class="h-9 w-full rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none"
                      aria-label="Model"
                      disabled
                    >
                      <option>GPT-Image-2</option>
                    </select>
                  </label>
                </div>

                <div class="flex flex-col gap-2 lg:items-end">
                  <div class="flex flex-wrap gap-2">
                    <span class="inline-flex items-center rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs font-medium text-linear-ink-muted">Text</span>
                    <span class="inline-flex items-center rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs font-medium text-linear-ink-muted">Image generation</span>
                  </div>
                  <div class="flex flex-wrap items-center gap-2">
                    <button type="button" class="inline-flex h-9 items-center gap-2 rounded-lg border border-primary-500 bg-primary-500/10 px-3 text-sm font-medium text-primary-700 dark:text-primary-300">
                      <Icon name="brain" size="sm" />
                      <span>Thinking</span>
                    </button>
                    <select class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none" aria-label="Thinking effort" disabled>
                      <option>High</option>
                    </select>
                    <button type="button" class="inline-flex h-9 items-center gap-2 rounded-lg border border-primary-500 bg-primary-500/10 px-3 text-sm font-medium text-primary-700 dark:text-primary-300">
                      <Icon name="image" size="sm" />
                      <span>Generate</span>
                    </button>
                    <select class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none" aria-label="Image generation size" disabled>
                      <option>1536x1024</option>
                    </select>
                    <select class="h-9 rounded-lg border border-linear-hairline bg-linear-surface-1 px-3 text-sm text-linear-ink outline-none" aria-label="Image generation aspect ratio" disabled>
                      <option>3:2</option>
                    </select>
                  </div>
                </div>
              </div>
            </header>

            <div data-testid="homepage-chat-demo-message-list" class="min-h-0 flex-1 overflow-hidden bg-linear-canvas px-4 py-5">
              <div class="mx-auto flex max-w-3xl flex-col gap-5">
                <article class="flex justify-end">
                  <div class="max-w-[min(100%,42rem)] rounded-lg border border-primary-500/30 bg-primary-500 px-4 py-3 text-white">
                    <div class="mb-2 flex items-center justify-between gap-3 text-xs text-white/75">
                      <span>You</span>
                    </div>
                    <p class="whitespace-pre-wrap break-words text-sm leading-6">Generate a product hero image in 3:2, keep the UI crisp and production-like.</p>
                  </div>
                </article>
                <article class="flex justify-start">
                  <div class="max-w-[min(100%,42rem)] rounded-lg border border-linear-hairline bg-linear-surface-1 px-4 py-3 text-linear-ink">
                    <div class="mb-2 flex items-center justify-between gap-3 text-xs text-linear-ink-tertiary">
                      <span>GPT-Image-2</span>
                      <span>streaming</span>
                    </div>
                    <p class="whitespace-pre-wrap break-words text-sm leading-6">I’ll use high quality, transparent background disabled, and return the generated artifact in the chat.</p>
                    <div class="mt-3 inline-flex items-center gap-2 rounded-lg border border-linear-hairline bg-linear-canvas px-2.5 py-1.5 text-xs text-linear-ink-muted">
                      <Icon name="image" size="xs" />
                      <span>hero-image.png</span>
                    </div>
                  </div>
                </article>
              </div>
            </div>

            <footer data-testid="homepage-chat-demo-composer" class="border-t border-linear-hairline bg-linear-canvas px-4 py-3">
              <div class="mx-auto max-w-3xl">
                <div class="mb-2 inline-flex items-center gap-2 rounded-lg border border-linear-hairline bg-linear-surface-1 px-2.5 py-1.5 text-xs text-linear-ink-muted">
                  <Icon name="paperclip" size="xs" />
                  <span>brand-reference.png</span>
                </div>
                <div class="rounded-lg border border-linear-hairline bg-linear-surface-1 p-2">
                  <textarea
                    class="max-h-44 min-h-[56px] w-full resize-none bg-transparent px-2 py-2 text-sm leading-6 text-linear-ink outline-none placeholder:text-linear-ink-tertiary"
                    :placeholder="t('chat.composerPlaceholder')"
                    readonly
                  />
                  <div class="flex items-center justify-between gap-3">
                    <div class="flex items-center gap-1.5">
                      <button class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted" type="button" :aria-label="t('chat.attachImage')">
                        <Icon name="upload" size="sm" />
                      </button>
                      <button class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted" type="button" aria-label="Upload file">
                        <Icon name="document" size="sm" />
                      </button>
                    </div>
                    <router-link
                      :to="chatPath"
                      class="inline-flex h-9 w-9 items-center justify-center rounded-lg bg-primary-500 text-white transition-colors hover:bg-primary-400"
                      :aria-label="t('chat.send')"
                    >
                      <Icon name="arrowUp" size="sm" />
                    </router-link>
                  </div>
                </div>
              </div>
            </footer>
          </section>
        </div>

        <!-- Product console -->
        <div id="models" class="mx-auto mt-14 w-full max-w-6xl" data-testid="linear-product-console">
          <div class="linx-panel-strong overflow-hidden p-3 sm:p-4">
            <div class="rounded-xl border border-linear-hairline bg-linear-canvas">
              <div class="flex flex-col gap-4 border-b border-linear-hairline p-4 text-left sm:flex-row sm:items-center sm:justify-between sm:p-5">
                <div>
                  <p class="text-xs font-medium uppercase tracking-[0.18em] text-primary-400">{{ copy.gw.badge }}</p>
                  <h2 class="mt-2 text-xl font-semibold tracking-[-0.03em] text-linear-ink">{{ copy.gw.consoleTitle }}</h2>
                  <p class="mt-1 text-sm text-linear-ink-subtle">{{ copy.gw.description }}</p>
                </div>
                <span class="inline-flex w-fit items-center gap-1.5 rounded-full border border-linear-hairline bg-linear-surface-1 px-3 py-1.5 text-xs font-medium text-linear-ink-muted">
                  <span class="h-1.5 w-1.5 rounded-full bg-[#27a644]"></span>
                  {{ copy.gw.title }}
                </span>
              </div>

              <div class="grid grid-cols-2 gap-px border-b border-linear-hairline bg-linear-hairline sm:grid-cols-3 lg:grid-cols-6">
                <div
                  v-for="provider in providers"
                  :key="provider"
                  class="bg-linear-surface-1 px-4 py-4 text-center text-sm font-medium text-linear-ink-muted"
                >
                  {{ provider }}
                </div>
              </div>

              <div class="grid gap-px bg-linear-hairline lg:grid-cols-[1.15fr_0.85fr]">
                <div class="bg-linear-surface-1 p-5 text-left sm:p-6">
                  <div class="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
                    <div>
                      <p class="linx-section-kicker">{{ copy.gw.flowTitle }}</p>
                      <h3 class="mt-3 text-lg font-semibold tracking-[-0.035em] text-linear-ink">{{ copy.gw.routeTitle }}</h3>
                    </div>
                    <p class="text-xs leading-5 text-linear-ink-tertiary">{{ copy.gw.routeSummary }}</p>
                  </div>
                  <div data-testid="homepage-route-grid" class="mt-5 grid gap-3 sm:grid-cols-2">
                    <article
                      v-for="route in routeCards"
                      :key="route.label"
                      class="rounded-xl border border-linear-hairline bg-linear-surface-2 p-4 transition-colors hover:border-linear-hairline-strong"
                    >
                      <div class="flex items-start justify-between gap-3">
                        <div>
                          <p class="text-sm font-semibold tracking-[-0.02em] text-linear-ink">{{ route.label }}</p>
                          <p class="mt-1 text-xs leading-5 text-linear-ink-subtle">{{ route.description }}</p>
                        </div>
                        <span class="font-mono-brand ui-accent-badge rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider">
                          {{ route.badge }}
                        </span>
                      </div>
                    </article>
                  </div>
                </div>

                <div class="bg-linear-surface-1 p-5 text-left sm:p-6">
                  <p class="linx-section-kicker">{{ copy.gw.baseUrlTitle }}</p>
                  <pre class="font-mono-brand mt-4 overflow-x-auto rounded-xl border border-linear-hairline bg-linear-canvas p-4 text-left text-xs leading-6 text-linear-ink-muted"><code><span class="text-primary-300">ANTHROPIC_BASE_URL</span>=https://linx2.ai/api
<span class="text-primary-300">ANTHROPIC_API_KEY</span>={LINX2_AI_API}
<span class="text-primary-300">OPENAI_BASE_URL</span>=https://linx2.ai/api
<span class="text-primary-300">OPENAI_API_KEY</span>={LINX2_AI_API}</code></pre>
                  <div class="mt-4 grid grid-cols-3 gap-2">
                    <div v-for="metric in metrics" :key="metric.label" class="linx-panel p-3 text-center">
                      <p class="text-lg font-semibold tracking-[-0.03em] text-linear-ink">{{ metric.value }}</p>
                      <p class="mt-0.5 text-[10px] leading-tight text-linear-ink-tertiary">{{ metric.label }}</p>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- ===== Features ===== -->
      <section id="features" class="border-y border-linear-hairline bg-linear-surface-1/35">
        <div class="mx-auto grid max-w-7xl gap-4 px-4 py-6 sm:px-6 md:grid-cols-3 lg:px-8">
          <article
            v-for="feature in copy.features"
            :key="feature.title"
            class="linx-panel p-6 text-left transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
          >
            <p class="text-sm font-semibold tracking-[-0.02em] text-linear-ink">{{ feature.title }}</p>
            <p class="mt-3 text-sm leading-6 text-linear-ink-subtle">{{ feature.description }}</p>
          </article>
        </div>
      </section>

      <!-- ===== Capabilities ===== -->
      <section id="capabilities" class="mx-auto max-w-7xl scroll-mt-24 px-4 py-16 sm:px-6 lg:px-8">
        <div class="grid gap-8 xl:grid-cols-[0.8fr_1.2fr] xl:items-end">
          <div class="text-left">
            <p class="linx-section-kicker">{{ copy.capabilityKicker }}</p>
            <h2 class="mt-4 max-w-3xl text-[clamp(2rem,4vw,2.9rem)] font-semibold leading-tight tracking-[-0.055em] text-linear-ink">
              {{ copy.capabilityTitle }}
            </h2>
          </div>
          <p class="text-left text-base leading-7 text-linear-ink-subtle">{{ copy.capabilityDescription }}</p>
        </div>

        <div class="mt-10 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          <article
            v-for="capability in copy.capabilities"
            :key="capability.title"
            class="linx-panel p-6 text-left transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2"
          >
            <p class="font-mono-brand text-xs font-medium uppercase tracking-[0.18em] text-primary-400/90">{{ capability.code }}</p>
            <h3 class="mt-4 text-xl font-semibold tracking-[-0.035em] text-linear-ink">{{ capability.title }}</h3>
            <p class="mt-3 text-sm leading-6 text-linear-ink-subtle">{{ capability.description }}</p>
          </article>
        </div>
      </section>

      <!-- ===== Pricing ===== -->
      <section id="pricing" class="scroll-mt-24 border-t border-linear-hairline bg-linear-surface-1/25">
        <div class="mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8">
          <div class="mb-10 max-w-2xl">
            <p class="linx-section-kicker">{{ copy.pricingKicker }}</p>
            <h2 class="mt-4 text-[clamp(2rem,4vw,2.9rem)] font-semibold leading-tight tracking-[-0.055em] text-linear-ink">{{ copy.pricingTitle }}</h2>
            <p class="mt-4 text-base leading-7 text-linear-ink-subtle">
              {{ copy.pricingDescription }}
            </p>
          </div>

          <div class="mb-6 grid gap-3 rounded-2xl border border-linear-hairline bg-linear-surface-1 p-4 text-sm leading-6 text-linear-ink-muted md:grid-cols-3">
            <div v-for="item in copy.monthlyCardInfo" :key="item.title" class="flex gap-3">
              <span class="ui-accent-dot mt-2 h-1.5 w-1.5 flex-shrink-0 rounded-full"></span>
              <div>
                <p class="font-semibold text-linear-ink">{{ item.title }}</p>
                <p class="mt-1 text-linear-ink-subtle">{{ item.description }}</p>
              </div>
            </div>
          </div>

          <div class="grid gap-5 md:grid-cols-2 xl:grid-cols-4" data-testid="linear-pricing-grid">
            <article
              v-for="plan in subscriptionPlans"
              :key="plan.name"
              data-testid="pricing-plan-card"
              class="linx-panel group relative flex h-full min-w-0 flex-col overflow-hidden p-6 text-left transition-colors hover:border-primary-500/45 hover:bg-linear-surface-2"
              :class="plan.featured ? 'border-primary-500/45 bg-primary-500/[0.045]' : ''"
            >
              <span
                v-if="plan.limitedSeatLabel"
                data-testid="homepage-limited-seat-ribbon"
                class="pointer-events-none absolute right-[-54px] top-7 z-20 w-[220px] rotate-45 whitespace-nowrap bg-gradient-to-r from-orange-950 via-orange-800 to-orange-700 py-1.5 text-center text-[11px] font-black tracking-[-0.01em] text-white drop-shadow-sm shadow-[0_12px_30px_rgba(249,115,22,0.35)] ring-1 ring-white/20 [text-shadow:0_1px_2px_rgba(0,0,0,0.45)] dark:from-orange-950 dark:via-orange-800 dark:to-orange-700"
              >
                {{ plan.limitedSeatLabel }}
              </span>
              <div :class="['flex items-center justify-between gap-3', plan.limitedSeatLabel ? 'min-h-[96px] pt-14' : '']">
                <h3 class="text-xl font-semibold tracking-[-0.035em] text-linear-ink">{{ plan.name }}</h3>
                <span class="font-mono-brand ui-accent-badge rounded-full border px-2.5 py-1 text-[10px] uppercase tracking-wider">
                  {{ plan.badge }}
                </span>
              </div>

              <div class="mt-6">
                <p class="flex items-baseline gap-2">
                  <span class="text-4xl font-semibold tracking-[-0.06em] text-linear-ink">{{ plan.price }}</span>
                  <span class="text-sm font-medium text-linear-ink-tertiary">/ {{ copy.planPeriod }}</span>
                </p>
                <div class="mt-3 flex flex-wrap gap-2">
                  <p class="font-mono-brand inline-flex rounded-lg border border-primary-500/25 bg-primary-500/10 px-3 py-1.5 text-sm font-medium text-primary-300">
                    {{ plan.quota }}
                  </p>
                  <p class="inline-flex rounded-lg border border-linear-hairline bg-linear-surface-2 px-3 py-1.5 text-sm font-medium text-linear-ink-muted">
                    {{ copy.monthlyTotalLabel }} {{ plan.monthlyTotal }}
                  </p>
                </div>
              </div>

              <p class="mt-5 text-sm leading-6 text-linear-ink-subtle">{{ plan.description }}</p>

              <ul class="mt-6 space-y-3 border-t border-linear-hairline pt-5">
                <li
                  v-for="benefit in plan.benefits"
                  :key="benefit"
                  class="flex gap-3 text-sm leading-6 text-linear-ink-muted"
                >
                  <span class="ui-accent-dot mt-2 h-1.5 w-1.5 flex-shrink-0 rounded-full"></span>
                  <span>{{ benefit }}</span>
                </li>
              </ul>

              <router-link
                to="/purchase?tab=subscription"
                :aria-label="`${copy.pricingCta} - ${plan.name}`"
                class="mt-auto inline-flex items-center justify-center rounded-lg px-4 py-2.5 text-sm font-medium transition-colors"
                :class="plan.featured ? 'bg-primary-500 text-white hover:bg-primary-400' : 'border border-linear-hairline bg-linear-canvas text-linear-ink hover:border-linear-hairline-strong hover:bg-linear-surface-1'"
              >
                {{ copy.pricingCta }}
                <Icon name="arrowRight" size="sm" class="ml-2" :stroke-width="2" />
              </router-link>
            </article>
          </div>

          <p class="mt-6 text-xs text-linear-ink-tertiary">{{ copy.pricingFootnote }}</p>

          <!-- Pay-as-you-go header -->
          <div class="mt-14 border-t border-linear-hairline pt-12" data-testid="homepage-payg-block">
            <div class="max-w-2xl">
              <p class="linx-section-kicker">{{ copy.paygKicker }}</p>
              <h2 class="mt-4 text-[clamp(2rem,4vw,2.9rem)] font-semibold leading-tight tracking-[-0.055em] text-linear-ink">{{ copy.paygTitle }}</h2>
              <p class="font-mono-brand mt-5 text-[clamp(1.6rem,3vw,2.1rem)] font-semibold tracking-[-0.04em] text-primary-300">{{ copy.paygRate }}</p>
              <p class="mt-3 text-base leading-7 text-linear-ink-subtle">{{ copy.paygDescription }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- ===== Model Pricing ===== -->
      <section id="model-pricing" class="scroll-mt-24 border-t border-linear-hairline bg-linear-canvas">
        <div class="mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8">
          <div class="mb-8 grid gap-6 lg:grid-cols-[0.78fr_1.22fr] lg:items-end">
            <div>
              <p class="linx-section-kicker">{{ showDynamicModelCatalog ? copy.modelCatalogKicker : copy.modelPricingKicker }}</p>
              <h2 class="mt-4 text-[clamp(2rem,4vw,2.9rem)] font-semibold leading-tight tracking-[-0.055em] text-linear-ink">{{ showDynamicModelCatalog ? copy.modelCatalogTitle : copy.modelPricingTitle }}</h2>
            </div>
            <div class="text-left lg:max-w-2xl">
              <p class="text-base leading-7 text-linear-ink-subtle">{{ showDynamicModelCatalog ? copy.modelCatalogDescription : copy.modelPricingDescription }}</p>
              <span class="font-mono-brand mt-4 inline-flex rounded-full border border-primary-500/25 bg-primary-500/10 px-3 py-1.5 text-xs font-medium text-primary-300">
                {{ showDynamicModelCatalog ? copy.modelCatalogUnit : copy.modelPricingUnit }}
              </span>
            </div>
          </div>

          <div
            v-if="showDynamicModelCatalog && visibleHomeCatalogModels.length > 0"
            data-testid="homepage-model-catalog-grid"
            class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"
          >
            <article
              v-for="model in visibleHomeCatalogModels"
              :key="`${model.provider}:${model.model_name}`"
              data-testid="homepage-model-catalog-card"
              class="linx-panel flex min-w-0 flex-col p-5 text-left transition-colors hover:border-primary-500/35 hover:bg-linear-surface-2"
              :data-provider="model.provider"
              :data-model="model.model_name"
            >
              <div class="flex items-start gap-3">
                <span
                  class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink"
                  aria-hidden="true"
                >
                  <ModelIcon :model="model.model_name" size="21px" />
                </span>
                <div class="min-w-0">
                  <p class="truncate text-xs font-medium text-linear-ink-muted">{{ model.provider_name }}</p>
                  <h3 class="mt-3 text-lg font-semibold text-linear-ink">{{ model.display_name }}</h3>
                  <p class="mt-1 break-words font-mono text-xs text-linear-ink-tertiary">{{ model.model_name }}</p>
                </div>
              </div>

              <p class="mt-3 text-sm leading-6 text-linear-ink-subtle">{{ model.description }}</p>

              <div class="mt-4 flex flex-wrap gap-2">
                <span
                  v-for="item in model.modalities"
                  :key="item"
                  class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs font-medium text-linear-ink-muted"
                >
                  {{ modalityLabel(item) }}
                </span>
                <span
                  v-for="feature in model.features"
                  :key="feature"
                  class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-tertiary"
                >
                  {{ feature }}
                </span>
                <span
                  v-if="formatContextWindow(model.context_window)"
                  class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-tertiary"
                >
                  {{ t('modelCatalog.context') }} {{ formatContextWindow(model.context_window) }}
                </span>
              </div>

              <div class="mt-5 divide-y divide-linear-hairline overflow-hidden rounded-lg border border-linear-hairline">
                <div
                  v-for="row in pricingRows(model)"
                  :key="row.key"
                  class="grid grid-cols-[minmax(0,1fr)_auto] gap-3 px-3 py-2 text-sm"
                >
                  <span class="min-w-0 text-linear-ink-muted">{{ row.label }}</span>
                  <span class="font-mono text-linear-ink">{{ row.value }}</span>
                </div>
              </div>

              <router-link
                :to="chatRouteForModel(model)"
                class="mt-5 inline-flex w-full items-center justify-center rounded-lg bg-primary-500 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-primary-400"
                :data-provider="model.provider"
                :data-model="model.model_name"
              >
                <Icon name="chat" size="sm" class="mr-2" />
                {{ t('modelCatalog.chatNow') }}
              </router-link>
            </article>
          </div>

          <div
            v-else-if="!showDynamicModelCatalog"
            data-testid="homepage-model-pricing-table"
            class="overflow-hidden rounded-2xl border border-linear-hairline bg-linear-surface-1 text-left"
          >
            <div class="grid gap-px bg-linear-hairline lg:grid-cols-2">
              <section
                v-for="group in modelPricingGroups"
                :key="group.provider"
                class="bg-linear-surface-1 p-5"
              >
                <div class="mb-4 flex items-center justify-between gap-3">
                  <div class="flex items-center gap-2">
                    <span class="h-2 w-2 rounded-full" :style="{ backgroundColor: group.accent_color }"></span>
                    <h4 class="text-base font-semibold tracking-[-0.025em] text-linear-ink">{{ group.provider }}</h4>
                  </div>
                  <span class="text-xs font-medium text-linear-ink-tertiary">{{ copy.modelPricingProviderLabel }}</span>
                </div>

                <div class="overflow-hidden rounded-xl border border-linear-hairline bg-linear-canvas">
                  <div class="hidden grid-cols-[1.3fr_0.8fr_0.8fr_0.8fr] border-b border-linear-hairline bg-linear-surface-2 px-4 py-3 text-[11px] font-medium uppercase tracking-[0.14em] text-linear-ink-tertiary sm:grid">
                    <span>{{ copy.modelPricingModel }}</span>
                    <span>{{ copy.modelPricingInput }}</span>
                    <span>{{ copy.modelPricingOutput }}</span>
                    <span>{{ copy.modelPricingCacheRead }}</span>
                  </div>
                  <div
                    v-for="model in visibleModelsFor(group)"
                    :key="model.model"
                    data-testid="homepage-model-pricing-row"
                    class="grid gap-3 border-b border-linear-hairline px-4 py-4 last:border-b-0 sm:grid-cols-[1.3fr_0.8fr_0.8fr_0.8fr] sm:items-center"
                  >
                    <div>
                      <p class="font-medium tracking-[-0.02em] text-linear-ink">{{ model.name }}</p>
                      <p class="mt-1 text-xs text-linear-ink-tertiary">{{ model.model }}</p>
                    </div>
                    <p class="text-sm text-linear-ink-muted"><span class="sm:hidden">{{ copy.modelPricingInput }} </span>{{ formatModelPrice(model.input_per_million) }}</p>
                    <p class="font-mono-brand text-sm font-semibold text-primary-300"><span class="font-sans sm:hidden">{{ copy.modelPricingOutput }} </span>{{ formatModelPrice(model.output_per_million) }}</p>
                    <p class="text-sm text-linear-ink-muted"><span class="sm:hidden">{{ copy.modelPricingCacheRead }} </span>{{ formatModelPrice(model.cache_read_per_million) }}</p>
                  </div>
                </div>
                <button
                  v-if="hasHiddenModels(group)"
                  type="button"
                  data-testid="homepage-model-pricing-toggle"
                  class="mt-4 inline-flex items-center justify-center rounded-lg border border-linear-hairline px-3 py-2 text-sm font-medium text-linear-ink-muted transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
                  @click="toggleModelProvider(group.provider)"
                >
                  {{ expandedModelProviders[group.provider] ? copy.modelPricingShowLess : copy.modelPricingShowMore }}
                </button>
              </section>
            </div>
          </div>

          <div v-else class="linx-panel p-8 text-center text-sm text-linear-ink-subtle">
            {{ t('modelCatalog.loading') }}
          </div>

          <button
            v-if="showDynamicModelCatalog && hasHiddenHomeCatalogModels"
            type="button"
            data-testid="homepage-model-catalog-toggle"
            class="mx-auto mt-6 inline-flex items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 px-5 py-2.5 text-sm font-medium text-linear-ink-muted transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
            @click="toggleHomeModelCatalog"
          >
            {{ homeModelCatalogExpanded ? copy.modelPricingShowLess : copy.modelPricingShowMore }}
          </button>
        </div>
      </section>

      <!-- ===== CTA ===== -->
      <section class="px-4 py-16 sm:px-6 lg:px-8">
        <div class="linx-panel-strong mx-auto max-w-5xl p-8 text-center sm:p-12">
          <p class="linx-section-kicker">{{ copy.ctaKicker }}</p>
          <h2 class="mt-4 text-3xl font-semibold tracking-[-0.055em] text-linear-ink sm:text-5xl">{{ copy.ctaTitle }}</h2>
          <p class="mx-auto mt-5 max-w-2xl text-base leading-8 text-linear-ink-muted">{{ copy.ctaDescription }}</p>
          <div class="mt-8 flex flex-col justify-center gap-4 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center justify-center rounded-lg bg-primary-500 px-6 py-3 text-sm font-medium text-white transition-colors hover:bg-primary-400"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            </router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center justify-center rounded-lg border border-linear-hairline bg-linear-canvas px-6 py-3 text-sm font-medium text-linear-ink transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-1"
            >
              {{ t('home.docs') }}
            </a>
          </div>
        </div>
      </section>
    </main>

    <!-- ===== Footer ===== -->
    <footer class="border-t border-linear-hairline px-4 py-8 sm:px-6 lg:px-8">
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-center gap-3 text-center text-sm text-linear-ink-tertiary">
        <div data-testid="homepage-footer-brand" class="flex flex-col items-center gap-2">
          <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-linear-hairline">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span>&copy; {{ currentYear }} LINX2.AI</span>
        </div>
        <div v-if="docUrl" class="flex items-center justify-center gap-5">
          <a
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="transition-colors hover:text-linear-ink"
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
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import BeginnerGuideCard from '@/components/getting-started/BeginnerGuideCard.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import LinxWordmark from '@/components/common/LinxWordmark.vue'
import { getMonthlyPlanCards, getMonthlyPlanDisplayFromPlan, monthlyPlanKeyFromName } from '@/utils/monthlyPlans'
import {
  getPublicModelCatalog,
  getPublicModelPricing,
  type PublicModelCatalogModel,
  type PublicModelPricingProvider,
} from '@/api/settings'
import { chatAPI } from '@/api/chat'
import { paymentAPI } from '@/api/payment'
import { useAnnouncementBanner } from '@/composables/useAnnouncementBanner'
import type { SubscriptionPlan } from '@/types/payment'
import {
  filterModelCatalogByWebChatModels,
  formatContextWindow,
  formatModelCatalogAmount,
  providerIconModel,
} from '@/utils/modelCatalog'
import { sanitizeUrl } from '@/utils/url'

const { t, locale } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

const DEFAULT_SITE_NAME = 'LINX2.AI'
const brandIconUrl = '/linx2-icon.png'

// Site settings
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || DEFAULT_SITE_NAME)
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI Gateway Platform')
const brandLogo = computed(() => siteLogo.value || brandIconUrl)
const usesDefaultBrand = computed(() => siteName.value.trim().toUpperCase() === DEFAULT_SITE_NAME)
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const trimmedHomeContent = computed(() => homeContent.value.trim())
const renderedHomeContent = computed(() => DOMPurify.sanitize(marked.parse(homeContent.value) as string))

const isHomeContentUrl = computed(() => {
  const content = trimmedHomeContent.value
  return content.startsWith('http://') || content.startsWith('https://')
})

const providers = ['Claude Code', 'Codex', 'Messages', 'Responses', 'Chat', 'Images']

const metrics = [
  { value: '99.9%', label: '可用性 Uptime' },
  { value: '<5s', label: '首字延迟 TTFB' },
  { value: '24/7', label: '监控 Monitor' },
]

const routeCards = [
  { label: 'Anthropic Messages', description: 'Claude Code / Messages API', badge: 'Claude' },
  { label: 'OpenAI Responses', description: 'Responses API compatible path', badge: 'OpenAI' },
  { label: 'OpenAI Chat Completions', description: 'Chat Completions compatible path', badge: 'OpenAI' },
  { label: 'OpenAI Images', description: 'Image generation and edits', badge: 'OpenAI' },
]

const homeModelPreviewCount = 6
const visibleModelCount = 4
const modelCatalogModels = ref<PublicModelCatalogModel[]>([])
const modelPricingGroups = ref<PublicModelPricingProvider[]>([])
const publicSubscriptionPlans = ref<SubscriptionPlan[]>([])
const homeModelCatalogExpanded = ref(false)
const expandedModelProviders = ref<Record<string, boolean>>({})

const visibleHomeCatalogModels = computed(() =>
  homeModelCatalogExpanded.value
    ? modelCatalogModels.value
    : modelCatalogModels.value.slice(0, homeModelPreviewCount)
)

const hasHiddenHomeCatalogModels = computed(() => modelCatalogModels.value.length > homeModelPreviewCount)

function toggleHomeModelCatalog() {
  homeModelCatalogExpanded.value = !homeModelCatalogExpanded.value
}

function formatModelPrice(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '—'
  const decimals = value < 1 ? 3 : 2
  return `$${value.toLocaleString('en-US', {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  })}`
}

function visibleModelsFor(group: PublicModelPricingProvider) {
  return expandedModelProviders.value[group.provider]
    ? group.models
    : group.models.slice(0, visibleModelCount)
}

function hasHiddenModels(group: PublicModelPricingProvider): boolean {
  return group.models.length > visibleModelCount
}

function toggleModelProvider(provider: string) {
  expandedModelProviders.value = {
    ...expandedModelProviders.value,
    [provider]: !expandedModelProviders.value[provider],
  }
}

function modalityLabel(value: string): string {
  const key = `modelCatalog.modality.${value}`
  const label = t(key)
  return label === key ? value : label
}

function pricingRows(model: PublicModelCatalogModel): Array<{ key: string; label: string; value: string }> {
  if (model.price_status !== 'confirmed') {
    return [
      {
        key: 'pending',
        label: model.pricing.note || t('modelCatalog.pending'),
        value: t('modelCatalog.pending'),
      },
    ]
  }

  const rows: Array<{ key: string; label: string; value: string }> = []
  if (model.pricing.input_per_million !== undefined) {
    rows.push({
      key: 'input',
      label: `${t('modelCatalog.pricing.input')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.input_per_million),
    })
  }
  if (model.pricing.output_per_million !== undefined) {
    rows.push({
      key: 'output',
      label: `${t('modelCatalog.pricing.output')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.output_per_million),
    })
  }
  if (model.pricing.cache_read_per_million !== undefined) {
    rows.push({
      key: 'cache-read',
      label: `${t('modelCatalog.pricing.cacheRead')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.cache_read_per_million),
    })
  }
  for (const line of model.pricing.price_lines ?? []) {
    rows.push({
      key: `line:${line.label}`,
      label: line.label,
      value: `${formatModelCatalogAmount(line.amount)} / ${line.unit}`,
    })
  }
  return rows
}

function chatRouteForModel(model: PublicModelCatalogModel) {
  return {
    path: '/chat',
    query: {
      provider: model.provider,
      model: model.model_name,
    },
  }
}

async function loadModelCatalog() {
  try {
    const [data, chatModels] = await Promise.all([
      getPublicModelCatalog(),
      chatAPI.listModels(),
    ])
    modelCatalogModels.value = filterModelCatalogByWebChatModels(data.models || [], chatModels)
  } catch (error) {
    console.error('Failed to load dynamic web chat model catalog:', error)
    modelCatalogModels.value = []
  }
}

async function loadModelPricing() {
  try {
    const data = await getPublicModelPricing()
    modelPricingGroups.value = data.providers || []
  } catch (error) {
    console.error('Failed to load public model pricing:', error)
    modelPricingGroups.value = []
  }
}

function loadHomeModelData() {
  if (showDynamicModelCatalog.value) {
    loadModelCatalog()
    return
  }
  loadModelPricing()
}

async function loadPublicSubscriptionPlans() {
  try {
    publicSubscriptionPlans.value = await paymentAPI.getPublicPlans()
  } catch (error) {
    console.error('Failed to load public subscription plans:', error)
    publicSubscriptionPlans.value = []
  }
}

// Bilingual marketing copy (mirrors LocaleSwitcher in the header).
const copies = {
  zh: {
    announcement: '统一 Claude Code · Codex 官方原生通道，国内稳定直连',
    nav: { capabilities: '能力', pricing: '价格' },
    heroKicker: 'AI 网关平台 · Claude / OpenAI 兼容路由',
    heroTitle: '一个网关密钥，接入 Claude 与 OpenAI 模型。',
    heroDescription:
      '通过统一的计费、用量与访问控制层，转发 Claude Code、Codex 与 OpenAI 兼容请求。无需繁琐配置、无需海外信用卡，开箱即用。',
    docsCta: '查看文档',
    learnCta: '了解能力',
    gw: {
      title: '供应商与能力墙',
      consoleTitle: 'API Gateway Console',
      description: '一个平台密钥路由到兼容的模型 API。',
      badge: '可用路由',
      flowTitle: '网关流程',
      routeTitle: 'Claude / OpenAI 路由矩阵',
      routeSummary: '当前聚焦 Claude 与 OpenAI 两类上游能力。',
      baseUrlTitle: 'Base URL',
      flow: [
        { title: '应用请求', description: '使用供应商兼容客户端和一个 LINX2.AI 密钥。' },
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
    capabilityTitle: '覆盖主流 AI 模型，同时保留运营控制。',
    capabilityDescription:
      'LINX2.AI 的表达保持简单：给开发者兼容模型路由，给运营者余额保护，并提供商业可见的用量记录。',
    capabilities: [
      { code: 'MESSAGES', title: 'Anthropic 风格调用', description: '在支持的流程中使用熟悉的 messages 路由、流式、工具和多模态请求。' },
      { code: 'RESPONSES', title: 'OpenAI Responses 路径', description: '让应用客户端保持标准 OpenAI Responses 请求结构，迁移成本极低。' },
      { code: 'CODEX', title: 'Codex / Chat 兼容', description: '面向 Codex 与 OpenAI Chat Completions 工作负载提供统一转发入口。' },
      { code: 'LEDGER', title: '用量与计费层', description: '跟踪模型、Token、状态和费用记录，并提供账户级余额保护。' },
    ],
    pricingKicker: 'LINX2.AI 订阅方案',
    pricingTitle: '按月订阅，每 7 天刷新可用额度。',
    pricingDescription: '四档方案覆盖试用、日常开发、主力项目与高强度并行工作；每档都可使用 Claude Code 与 OpenAI 兼容网关。',
    modelPricingKicker: '价格透明',
    modelPricingTitle: '价格透明，上游模型价格直传',
    modelPricingDescription: 'Anthropic 与 OpenAI 上游模型价格直传，按每百万 Token 展示输入、输出与缓存读取单价；实际扣费以控制台账单和渠道配置为准。',
    modelPricingUnit: '按每百万 Token 计价',
    modelPricingProviderLabel: '官方原价透传',
    modelPricingModel: '模型',
    modelPricingInput: '输入',
    modelPricingOutput: '输出',
    modelPricingCacheRead: '缓存读取',
    modelCatalogKicker: '模型广场',
    modelCatalogTitle: '真实模型目录，直接发起对话',
    modelCatalogDescription: '首页预览直接使用模型广场公开目录，展示服务商、模态、能力和官方公开价格；点击立即对话会带着选定模型进入网页对话。',
    modelCatalogUnit: '公开模型目录 · 价格与能力',
    modelPricingShowMore: '展开更多模型',
    modelPricingShowLess: '收起模型',
    pricingCta: '选择方案',
    planPeriod: '月',
    monthlyTotalLabel: '总共可获取',
    pricingFootnote: '订阅额度优先使用；额度不足时继续使用充值余额兜底，实际状态以购买页和控制台为准。',
    paygKicker: '灵活计费',
    paygTitle: '按量付费 Pay-as-you-go',
    paygRate: '¥1 = $1',
    paygDescription: '订阅套餐额度用完，任务和 Coding 却停不下来？我们提供优惠的按量付费 Pay-as-you-go，充值余额按官方美元原价计费，用多少扣多少。',
    monthlyCardInfo: [
      { title: '每周发放充值额度', description: '月卡按 7 天为一个周期刷新额度，不直接增加账户充值余额。' },
      { title: '通用模型通道', description: '所有档位都支持 Claude Code 与 OpenAI 兼容网关，不按供应商拆分。' },
      { title: '充值余额兜底', description: '订阅额度优先扣除，超出后自动使用充值余额继续请求。' },
    ],
    ctaKicker: 'Ready when you are',
    ctaTitle: '几分钟接入，立即开始使用 AI 网关',
    ctaDescription: '注册后获取专属 API Key，把 Claude Code、Codex 与 OpenAI 纳入统一、稳定、透明计费的 AI 网关。',
  },
  en: {
    announcement: 'Unified official-native routes for Claude Code · Codex — stable direct access',
    nav: { capabilities: 'Capabilities', pricing: 'Pricing' },
    heroKicker: 'AI Gateway Platform · Claude / OpenAI-compatible routes',
    heroTitle: 'One gateway key for Claude and OpenAI models.',
    heroDescription:
      'Route Claude Code, Codex and OpenAI-compatible requests through one billing, usage and access layer. No tedious setup, no overseas card — ready out of the box.',
    docsCta: 'Read docs',
    learnCta: 'Explore',
    gw: {
      title: 'Provider and capability wall',
      consoleTitle: 'API Gateway Console',
      description: 'One platform key routes to compatible model APIs.',
      badge: 'Live routes',
      flowTitle: 'Gateway flow',
      routeTitle: 'Claude / OpenAI route matrix',
      routeSummary: 'Currently focused on Claude and OpenAI upstream capabilities.',
      baseUrlTitle: 'Base URL',
      flow: [
        { title: 'App request', description: 'Use provider-compatible clients and one LINX2.AI key.' },
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
      'LINX2.AI keeps the story simple: compatible model routes for builders, balance protection for operators, and usage records for commercial visibility.',
    capabilities: [
      { code: 'MESSAGES', title: 'Anthropic-style calls', description: 'Use familiar message routes for text, streaming, tools and multimodal flows where supported.' },
      { code: 'RESPONSES', title: 'OpenAI Responses paths', description: 'Keep application clients close to standard OpenAI Responses request shapes for compatible workloads.' },
      { code: 'CODEX', title: 'Codex / Chat compatible', description: 'Provide one forwarding entry for Codex and OpenAI Chat Completions workloads.' },
      { code: 'LEDGER', title: 'Usage and billing layer', description: 'Track model, token, status and cost records with account-level balance protection.' },
    ],
    pricingKicker: 'LINX2.AI subscription plans',
    pricingTitle: 'Monthly plans with quota refreshed every seven days.',
    pricingDescription: 'Four tiers cover trials, daily development, primary projects, and high-intensity parallel work. Every tier supports Claude Code and OpenAI-compatible gateway access.',
    modelPricingKicker: 'Transparent pricing',
    modelPricingTitle: 'Transparent pricing with upstream model prices passed through',
    modelPricingDescription: 'Anthropic and OpenAI upstream model prices are passed through and shown per million tokens for input, output, and cache reads. Console billing and channel configuration remain authoritative.',
    modelPricingUnit: 'Per million tokens',
    modelPricingProviderLabel: 'Official pass-through',
    modelPricingModel: 'Model',
    modelPricingInput: 'Input',
    modelPricingOutput: 'Output',
    modelPricingCacheRead: 'Cache read',
    modelCatalogKicker: 'Model marketplace',
    modelCatalogTitle: 'Real model catalog, ready for chat',
    modelCatalogDescription: 'The homepage preview uses the same public catalog as the model marketplace, showing providers, modalities, capabilities, and public upstream pricing. Chat now opens web chat with that model selected.',
    modelCatalogUnit: 'Public model catalog · pricing and capabilities',
    modelPricingShowMore: 'Show more models',
    modelPricingShowLess: 'Show fewer models',
    pricingCta: 'Choose plan',
    planPeriod: 'month',
    monthlyTotalLabel: 'Total obtainable',
    pricingFootnote: 'Subscription quota is used first; recharge balance keeps overflow requests running. Purchase page and console state are authoritative.',
    paygKicker: 'Flexible billing',
    paygTitle: 'Pay-as-you-go',
    paygRate: '¥1 = $1',
    paygDescription: 'Subscription quota used up but the task and coding session cannot stop? Pay-as-you-go keeps model calls running, billed from recharge balance at official USD pricing.',
    monthlyCardInfo: [
      { title: 'Weekly recharge quota', description: 'Monthly cards refresh usable quota every seven days without adding to your recharge wallet.' },
      { title: 'Universal model routes', description: 'Every tier supports Claude Code and OpenAI-compatible gateway access without provider-specific splitting.' },
      { title: 'Recharge fallback', description: 'Subscription quota is consumed first, then recharge balance automatically covers overflow.' },
    ],
    ctaKicker: 'Ready when you are',
    ctaTitle: 'Connect in minutes, start using the AI gateway',
    ctaDescription: 'Sign up to get your API key and bring Claude Code, Codex and OpenAI into one stable, transparent AI gateway.',
  },
} as const

const localeCode = computed(() => (String(locale.value).startsWith('zh') ? 'zh' : 'en'))
const copy = computed(() => copies[localeCode.value])

// 顶部滚动公告:复用共享 composable。首页始终有内容——没配置时回退到内置双语
// 营销文案(fallbackText),关闭仅本次会话内存态(不传 dismissKey,与原行为一致)。
// 必须放在 copy 之后:composable 内的 watch 会在创建时同步求值 banners→fallbackText→copy。
const {
  showAnnouncement,
  currentBannerIndex,
  currentBannerText,
  dismissAnnouncement,
} = useAnnouncementBanner({ fallbackText: () => copy.value.announcement })

const publicPlanByMonthlyKey = computed(() => {
  const byKey: Partial<Record<string, SubscriptionPlan>> = {}
  for (const plan of publicSubscriptionPlans.value) {
    const key = monthlyPlanKeyFromName(plan.name)
    if (key && !byKey[key]) {
      byKey[key] = plan
    }
  }
  return byKey
})

function limitedSeatLabelForPlan(plan: SubscriptionPlan | undefined): string {
  if (!plan || plan.seat_limit === null || plan.seat_limit === undefined) return ''
  const seatUsed = plan.seat_used || 0
  if (plan.virtual_seat_start !== null && plan.virtual_seat_start !== undefined && plan.virtual_seat_total !== null && plan.virtual_seat_total !== undefined) {
    return `限时名额：${plan.virtual_seat_start + seatUsed}/${plan.virtual_seat_total}`
  }
  return `限时名额：${seatUsed}/${plan.seat_limit}`
}

const subscriptionPlans = computed(() => getMonthlyPlanCards(localeCode.value).map(plan => {
  const publicPlan = publicPlanByMonthlyKey.value[plan.key]
  const display = publicPlan ? getMonthlyPlanDisplayFromPlan(publicPlan, localeCode.value) ?? plan : plan
  return {
    name: display.name,
    badge: display.badge,
    price: display.priceLabel,
    quota: display.quotaLabel,
    monthlyTotal: display.monthlyTotalLabel,
    description: display.description,
    benefits: display.benefits,
    featured: display.featured,
    limitedSeatLabel: limitedSeatLabelForPlan(publicPlan),
  }
}))


// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const showDynamicModelCatalog = computed(() => isAuthenticated.value)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const chatPath = computed(() => (isAuthenticated.value ? '/chat' : { path: '/login', query: { redirect: '/chat' } }))
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
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
  loadHomeModelData()
  loadPublicSubscriptionPlans()
})

watch(showDynamicModelCatalog, () => {
  loadHomeModelData()
})
</script>

<style scoped>
.linear-landing {
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

.banner-fade-enter-active,
.banner-fade-leave-active {
  transition: opacity 0.4s ease;
}
.banner-fade-enter-from,
.banner-fade-leave-to {
  opacity: 0;
}
</style>
