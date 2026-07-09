# Homepage UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update the homepage landing UI so navigation, route cards, pricing, supported-provider copy, and footer align with the approved Claude/OpenAI-only B-style design.

**Architecture:** Keep the change scoped to the existing `HomeView.vue` landing page and its existing unit tests. Do not introduce new components; refactor only the local data structures in `HomeView.vue` when needed to make the template clearer.

**Tech Stack:** Vue 3 SFC, Tailwind CSS utility classes, Vue Test Utils/Vitest, existing Docker-served local app at `http://127.0.0.1:8080`.

---

## File Structure

- Modify: `frontend/src/views/HomeView.vue`
  - Header nav layout.
  - Gateway console route card layout.
  - Claude/OpenAI-only provider/capability copy.
  - Pricing data and aligned pricing table.
  - Footer centered brand layout.
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`
  - Update homepage assertions for Claude/OpenAI-only copy.
  - Add assertions for removed Gemini copy, right-aligned nav container, route cards, latest pricing labels, and centered footer.

---

### Task 1: Update tests for approved homepage behavior

**Files:**
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`

- [ ] **Step 1: Replace the main landing shell test assertions**

In `frontend/src/views/__tests__/HomeView.spec.ts`, update the first test body to assert the approved B-style behavior. Replace the section after `const text = wrapper.text()` in `renders the dark-orange LINX2 landing shell with USD model pricing by default` with:

```ts
    expect(text).toContain('Fuse API')
    expect(text).toContain('统一 AI 编程 API · Claude / OpenAI 兼容路由')
    expect(text).toContain('一个密钥，接入 Claude 与 OpenAI 编程模型。')
    expect(text).toContain('Claude Code')
    expect(text).toContain('Codex')
    expect(text).toContain('可用路由')
    expect(text).toContain('Anthropic Messages')
    expect(text).toContain('OpenAI Responses')
    expect(text).toContain('OpenAI Chat Completions')
    expect(text).toContain('OpenAI Images')
    expect(text).not.toContain('Gemini')

    expect(text).toContain('官方原价透传')
    expect(text).toContain('Claude Opus 4.5')
    expect(text).toContain('Claude Sonnet 4.5')
    expect(text).toContain('Claude Haiku 4.5')
    expect(text).toContain('GPT-5')
    expect(text).toContain('GPT-5 mini')
    expect(text).toContain('o3')
    expect(text).toContain('$5.00')
    expect(text).toContain('$25.00')
    expect(text).toContain('Latest')

    const headerNav = wrapper.get('[data-testid="homepage-header-actions"]')
    expect(headerNav.text()).toContain('能力')
    expect(headerNav.text()).toContain('价格')

    const routeGrid = wrapper.get('[data-testid="homepage-route-grid"]')
    expect(routeGrid.text()).toContain('Anthropic Messages')
    expect(routeGrid.text()).toContain('OpenAI Responses')
    expect(routeGrid.text()).toContain('OpenAI Chat Completions')
    expect(routeGrid.text()).toContain('OpenAI Images')

    const pricingGrid = wrapper.get('[data-testid="linear-pricing-grid"]')
    expect(pricingGrid.findAll('[data-testid="pricing-model-row"]').length).toBe(6)

    const footerBrand = wrapper.get('[data-testid="homepage-footer-brand"]')
    expect(footerBrand.classes()).toContain('items-center')
    expect(footerBrand.text()).toContain('LINIX2.Ltd')

    expect(text).not.toContain('GitHub')
    expect(wrapper.get('img[alt="Fuse API logo"]').attributes('src')).toBe('/linx2-icon.png')
    expect(wrapper.get('a[href="/login"]').text()).toContain('立即开始')
    const docsLinks = wrapper.findAll('a[href="https://docs.example.test"]')
    expect(docsLinks.length).toBeGreaterThan(0)
    expect(docsLinks[0].text()).toContain('文档')
    expect(wrapper.get('header a[href="#pricing"]').text()).toContain('价格')
```

- [ ] **Step 2: Run the targeted test and confirm it fails before implementation**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: FAIL because `HomeView.vue` still contains Gemini copy, the old route layout, no `homepage-header-actions`, no `homepage-route-grid`, and no `pricing-model-row` test IDs.

---

### Task 2: Update header navigation and supported-provider copy

**Files:**
- Modify: `frontend/src/views/HomeView.vue`

- [ ] **Step 1: Move nav links into the right-aligned action group**

In `HomeView.vue`, replace the header middle nav block and action block at lines around 48-87 with this structure:

```vue
        <div data-testid="homepage-header-actions" class="ml-auto flex items-center gap-2 sm:gap-3">
          <div class="hidden items-center gap-6 text-sm font-medium text-linear-ink-subtle md:flex">
            <a href="#capabilities" class="transition-colors hover:text-linear-ink">{{ copy.nav.capabilities }}</a>
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
          <LocaleSwitcher />
          <button
            @click="toggleTheme"
            class="rounded-lg border border-linear-hairline bg-linear-surface-1 p-2 text-linear-ink-subtle transition-colors hover:border-linear-hairline-strong hover:bg-linear-surface-2 hover:text-linear-ink"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            :aria-label="isAuthenticated ? t('home.goToDashboard') : t('home.getStarted')"
            class="inline-flex h-10 items-center justify-center gap-2 rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-400"
          >
            <span
              v-if="isAuthenticated && userInitial"
              class="flex h-5 w-5 items-center justify-center rounded-md bg-white/15 text-[10px]"
            >
              {{ userInitial }}
            </span>
            <span data-testid="header-cta-label">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            </span>
          </router-link>
        </div>
```

Remove the old separate `hidden items-center gap-7...` nav block so the nav is no longer centered.

- [ ] **Step 2: Replace provider and copy data with Claude/OpenAI-only wording**

In `HomeView.vue`, replace:

```ts
const providers = ['Claude', 'Codex', 'Gemini', 'Messages', 'Responses', 'Images']
```

with:

```ts
const providers = ['Claude Code', 'Codex', 'Messages', 'Responses', 'Chat', 'Images']
```

Update the Chinese copy fields:

```ts
announcement: '统一 Claude Code · Codex 官方原生通道，国内稳定直连',
heroKicker: '统一 AI 编程 API · Claude / OpenAI 兼容路由',
heroTitle: '一个密钥，接入 Claude 与 OpenAI 编程模型。',
heroDescription:
  '通过统一的计费、用量与访问控制层，转发 Claude Code、Codex 与 OpenAI 兼容请求。无需繁琐配置、无需海外信用卡，开箱即用。',
```

Update the English copy fields:

```ts
announcement: 'Unified official-native routes for Claude Code · Codex — stable direct access',
heroKicker: 'Unified AI Coding API · Claude / OpenAI-compatible routes',
heroTitle: 'One key for Claude and OpenAI coding models.',
heroDescription:
  'Route Claude Code, Codex and OpenAI-compatible requests through one billing, usage and access layer. No tedious setup, no overseas card — ready out of the box.',
```

- [ ] **Step 3: Update capabilities to remove Gemini**

Replace the `capabilities` arrays in both `zh` and `en` copies with Claude/OpenAI-only items.

Chinese:

```ts
capabilities: [
  { code: 'MESSAGES', title: 'Anthropic 风格调用', description: '在支持的流程中使用熟悉的 messages 路由、流式、工具和多模态请求。' },
  { code: 'RESPONSES', title: 'OpenAI Responses 路径', description: '让应用客户端保持标准 OpenAI Responses 请求结构，迁移成本极低。' },
  { code: 'CODEX', title: 'Codex / Chat 兼容', description: '面向 Codex 与 OpenAI Chat Completions 工作负载提供统一转发入口。' },
  { code: 'LEDGER', title: '用量与计费层', description: '跟踪模型、Token、状态和费用记录，并提供账户级余额保护。' },
],
```

English:

```ts
capabilities: [
  { code: 'MESSAGES', title: 'Anthropic-style calls', description: 'Use familiar message routes for text, streaming, tools and multimodal flows where supported.' },
  { code: 'RESPONSES', title: 'OpenAI Responses paths', description: 'Keep application clients close to standard OpenAI Responses request shapes for compatible workloads.' },
  { code: 'CODEX', title: 'Codex / Chat compatible', description: 'Provide one forwarding entry for Codex and OpenAI Chat Completions workloads.' },
  { code: 'LEDGER', title: 'Usage and billing layer', description: 'Track model, token, status and cost records with account-level balance protection.' },
],
```

- [ ] **Step 4: Run targeted test and confirm remaining failures point to route/pricing/footer work**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: still FAIL until route cards, pricing rows, and footer test IDs/layout are implemented.

---

### Task 3: Redesign the gateway route card section

**Files:**
- Modify: `frontend/src/views/HomeView.vue`

- [ ] **Step 1: Add route card data**

After `const metrics = [...]`, add:

```ts
const routeCards = [
  { label: 'Anthropic Messages', description: 'Claude Code / Messages API', badge: 'Claude' },
  { label: 'OpenAI Responses', description: 'Responses API compatible path', badge: 'OpenAI' },
  { label: 'OpenAI Chat Completions', description: 'Chat Completions compatible path', badge: 'OpenAI' },
  { label: 'OpenAI Images', description: 'Image generation and edits', badge: 'OpenAI' },
]
```

- [ ] **Step 2: Replace the current gateway lower grid**

In `HomeView.vue`, replace the `<div class="grid gap-px bg-linear-hairline lg:grid-cols-[1.05fr_0.95fr]">...</div>` under the provider strip with:

```vue
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
                        <span class="font-mono-brand rounded-full border border-linear-hairline bg-linear-canvas px-2 py-0.5 text-[10px] uppercase tracking-wider text-primary-300">
                          {{ route.badge }}
                        </span>
                      </div>
                    </article>
                  </div>
                </div>

                <div class="bg-linear-surface-1 p-5 text-left sm:p-6">
                  <p class="linx-section-kicker">{{ copy.gw.baseUrlTitle }}</p>
                  <pre class="font-mono-brand mt-4 overflow-x-auto rounded-xl border border-linear-hairline bg-linear-canvas p-4 text-left text-xs leading-6 text-linear-ink-muted"><code><span class="text-primary-300">ANTHROPIC_BASE_URL</span>=https://linx2.ai/api
<span class="text-primary-300">OPENAI_BASE_URL</span>=https://linx2.ai/api
<span class="text-primary-300">API_KEY</span>=lx2_<span class="text-linear-ink-tertiary">••••••••</span></code></pre>
                  <div class="mt-4 grid grid-cols-3 gap-2">
                    <div v-for="metric in metrics" :key="metric.label" class="linx-panel p-3 text-center">
                      <p class="text-lg font-semibold tracking-[-0.03em] text-linear-ink">{{ metric.value }}</p>
                      <p class="mt-0.5 text-[10px] leading-tight text-linear-ink-tertiary">{{ metric.label }}</p>
                    </div>
                  </div>
                </div>
              </div>
```

- [ ] **Step 3: Add route copy fields**

In the `gw` object for Chinese, add:

```ts
routeTitle: 'Claude / OpenAI 路由矩阵',
routeSummary: '当前聚焦 Claude 与 OpenAI 两类上游能力。',
```

In the `gw` object for English, add:

```ts
routeTitle: 'Claude / OpenAI route matrix',
routeSummary: 'Currently focused on Claude and OpenAI upstream capabilities.',
```

- [ ] **Step 4: Run targeted test**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: still FAIL only if pricing/footer assertions remain unimplemented.

---

### Task 4: Align pricing table and update latest model labels

**Files:**
- Modify: `frontend/src/views/HomeView.vue`

- [ ] **Step 1: Extend pricing row type and data**

Replace:

```ts
type PriceRow = { name: string; in: string; out: string }
type PriceGroup = { provider: string; tag: string; models: PriceRow[] }
```

with:

```ts
type PriceRow = { name: string; in: string; out: string; label?: string }
type PriceGroup = { provider: string; tag: string; models: PriceRow[] }
```

Replace `pricingGroups` with:

```ts
const pricingGroups: PriceGroup[] = [
  {
    provider: 'Claude',
    tag: 'Anthropic',
    models: [
      { name: 'Claude Opus 4.5', in: '$5.00', out: '$25.00', label: 'Latest' },
      { name: 'Claude Sonnet 4.5', in: '$3.00', out: '$15.00', label: 'Latest' },
      { name: 'Claude Haiku 4.5', in: '$1.00', out: '$5.00', label: 'Latest' },
    ],
  },
  {
    provider: 'OpenAI',
    tag: 'GPT · Codex',
    models: [
      { name: 'GPT-5', in: '$1.25', out: '$10.00', label: 'Latest' },
      { name: 'GPT-5 mini', in: '$0.25', out: '$2.00', label: 'Latest' },
      { name: 'o3', in: '$2.00', out: '$8.00' },
    ],
  },
]
```

- [ ] **Step 2: Change pricing grid to two provider cards**

Replace:

```vue
<div class="grid gap-5 md:grid-cols-3" data-testid="linear-pricing-grid">
```

with:

```vue
<div class="grid gap-5 lg:grid-cols-2" data-testid="linear-pricing-grid">
```

- [ ] **Step 3: Replace pricing header and rows with fixed-width aligned columns**

Replace the pricing table header and list row area with:

```vue
              <div class="grid grid-cols-[minmax(0,1fr)_5.5rem_5.5rem_4.75rem] items-center gap-x-3 border-b border-linear-hairline pb-2 text-[11px] font-medium uppercase tracking-wide text-linear-ink-tertiary">
                <span>{{ copy.pricingCols.model }}</span>
                <span class="text-right">{{ copy.pricingCols.input }}</span>
                <span class="text-right">{{ copy.pricingCols.output }}</span>
                <span class="text-right">{{ copy.pricingCols.label }}</span>
              </div>
              <ul class="divide-y divide-linear-hairline">
                <li
                  v-for="model in group.models"
                  :key="model.name"
                  data-testid="pricing-model-row"
                  class="grid grid-cols-[minmax(0,1fr)_5.5rem_5.5rem_4.75rem] items-center gap-x-3 py-3"
                >
                  <span class="min-w-0 text-sm font-medium text-linear-ink-muted">{{ model.name }}</span>
                  <span class="font-mono-brand text-right text-sm tabular-nums text-linear-ink-subtle">{{ model.in }}</span>
                  <span class="font-mono-brand text-right text-sm font-medium tabular-nums text-linear-ink">{{ model.out }}</span>
                  <span class="text-right">
                    <span
                      v-if="model.label"
                      class="font-mono-brand rounded-full border border-primary-400/30 bg-primary-500/10 px-2 py-0.5 text-[10px] uppercase tracking-wider text-primary-300"
                    >
                      {{ model.label }}
                    </span>
                    <span v-else class="text-xs text-linear-ink-tertiary">—</span>
                  </span>
                </li>
              </ul>
```

- [ ] **Step 4: Add pricing label copy**

Change Chinese pricing columns to:

```ts
pricingCols: { model: '模型', input: '输入', output: '输出', label: '标注' },
```

Change English pricing columns to:

```ts
pricingCols: { model: 'Model', input: 'Input', output: 'Output', label: 'Label' },
```

- [ ] **Step 5: Run targeted test**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: still FAIL only if footer assertions remain unimplemented.

---

### Task 5: Center footer brand/logo

**Files:**
- Modify: `frontend/src/views/HomeView.vue`

- [ ] **Step 1: Replace footer layout**

Replace the footer inner markup at lines around 291-309 with:

```vue
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-center gap-3 text-center text-sm text-linear-ink-tertiary">
        <div data-testid="homepage-footer-brand" class="flex flex-col items-center gap-2">
          <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-white p-1.5 ring-1 ring-linear-hairline">
            <img :src="brandLogo" :alt="`${siteName} logo`" class="h-full w-full object-contain" />
          </span>
          <span>&copy; {{ currentYear }} LINIX2.Ltd</span>
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
```

- [ ] **Step 2: Run targeted homepage test**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: PASS for all `HomeView` tests.

---

### Task 6: Verify in browser and run focused quality checks

**Files:**
- No source edits unless verification reveals a defect.

- [ ] **Step 1: Run focused tests**

Run:

```bash
pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts frontend/src/__tests__/linear-theme-source.spec.ts frontend/src/views/user/__tests__/PaymentLinearSource.spec.ts
```

Expected: PASS. These cover the homepage plus recent linear-theme source expectations.

- [ ] **Step 2: Run lint on changed file scope**

Run:

```bash
pnpm --dir frontend run lint:check
```

Expected: PASS or only pre-existing unrelated failures. If it fails on `HomeView.vue` or `HomeView.spec.ts`, fix those failures before continuing.

- [ ] **Step 3: Rebuild/restart local Docker app if needed**

If the Docker app still serves an older embedded frontend bundle, run:

```bash
POSTGRES_PASSWORD=sub2api ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=admin123 JWT_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef TOTP_ENCRYPTION_KEY=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789 docker compose -f deploy/docker-compose.dev.yml --project-name tokenstation3-dev up --build -d
```

Expected: `sub2api-dev`, `sub2api-postgres-dev`, and `sub2api-redis-dev` are running and healthy.

- [ ] **Step 4: Browser verify homepage layout**

Open `http://127.0.0.1:8080/home` and verify:

- Header logo is left; `能力 / 价格 / 文档` are in the right action cluster on desktop.
- Hero and copy mention only Claude/OpenAI/Codex, not Gemini.
- Gateway console route cards align in a two-column grid on desktop.
- Pricing has two provider cards, fixed numeric columns, and Latest labels.
- Footer logo and LINIX2.Ltd are bottom centered.

- [ ] **Step 5: Capture final status**

Run:

```bash
git diff -- frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts
```

Expected: Diff only contains the planned homepage UI/test changes.

---

## Self-Review

- Spec coverage: Header right alignment, Claude/OpenAI-only copy, route card redesign, pricing alignment/latest labels, and centered footer each have a dedicated task.
- Placeholder scan: No TBD/TODO/fill-in-later items remain.
- Type consistency: `PriceRow.label`, `copy.pricingCols.label`, and `routeCards` are defined before template usage.
- Scope: Single homepage SFC plus existing test file; no unrelated refactor or new component creation.
