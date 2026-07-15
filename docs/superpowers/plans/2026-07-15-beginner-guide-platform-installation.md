# Beginner Guide Platform Installation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reuse the existing Claude Code/Codex and macOS/Windows/Linux selectors to show the correct official install command or Windows desktop download link, plus a top-of-guide network warning.

**Architecture:** Keep `GUIDE_VARIANTS` as the single six-combination data source and add only one optional `desktopDownloadUrl` field. The existing install step conditionally renders a primary desktop download button when that field exists, followed by the existing command block as the CLI fallback; no new selector or installation abstraction is introduced.

**Tech Stack:** Vue 3, TypeScript, vue-i18n, Vitest, Vue Test Utils, Tailwind CSS.

## Global Constraints

- Support exactly Claude Code and Codex on macOS, Windows, and Linux.
- Keep the existing client and OS selectors; do not add an installation-mode selector.
- macOS/Linux show one official CLI installation command.
- Windows shows the official desktop-app link first and the official CLI command second.
- Add `操作可能需要魔法梯子。` at the top of the Chinese guide and an equivalent English translation.
- Do not execute installer commands or download client binaries in tests or verification.
- Use official documentation values verified on `2026-07-15`.
- Preserve unrelated user and Graphify changes.

---

### Task 1: Lock the six platform installation contracts

**Files:**
- Modify: `frontend/src/components/getting-started/__tests__/curriculum.spec.ts`
- Modify: `frontend/src/components/getting-started/curriculum.ts`

**Interfaces:**
- Consumes: existing `GuideVariant` and `GUIDE_VARIANTS`.
- Produces: optional `GuideVariant.desktopDownloadUrl?: string` used by the guide view.

- [ ] **Step 1: Write the failing contract test**

Assert this exact mapping:

```ts
expect(installations).toEqual({
  'claude_code:macos': {
    command: 'curl -fsSL https://claude.ai/install.sh | bash',
    desktopDownloadUrl: null
  },
  'claude_code:windows': {
    command: 'irm https://claude.ai/install.ps1 | iex',
    desktopDownloadUrl:
      'https://claude.ai/api/desktop/win32/x64/setup/latest/redirect?utm_source=claude_code&utm_medium=docs'
  },
  'claude_code:linux': {
    command: 'curl -fsSL https://claude.ai/install.sh | bash',
    desktopDownloadUrl: null
  },
  'codex:macos': {
    command: 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
    desktopDownloadUrl: null
  },
  'codex:windows': {
    command: 'npm install -g @openai/codex',
    desktopDownloadUrl:
      'https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi'
  },
  'codex:linux': {
    command: 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
    desktopDownloadUrl: null
  }
})
```

- [ ] **Step 2: Run the focused test and confirm RED**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend test:run src/components/getting-started/__tests__/curriculum.spec.ts
```

Expected: FAIL because `desktopDownloadUrl` is absent and the Codex Windows command still uses a nonexistent PowerShell installer URL.

- [ ] **Step 3: Implement the minimal data change**

Add only this optional field:

```ts
desktopDownloadUrl?: string
```

Populate it only for the two Windows variants and replace only the Codex Windows CLI command.

- [ ] **Step 4: Run the focused test and confirm GREEN**

Run the same command and expect all curriculum tests to pass.

### Task 2: Render the warning and Windows primary download action

**Files:**
- Modify: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`
- Modify: `frontend/src/views/public/GettingStartedView.vue`
- Modify: `frontend/src/i18n/locales/zh/gettingStarted.ts`
- Modify: `frontend/src/i18n/locales/en/gettingStarted.ts`
- Modify: `frontend/src/components/getting-started/__tests__/curriculum.spec.ts`

**Interfaces:**
- Consumes: `selectedVariant.desktopDownloadUrl` from Task 1.
- Produces: `data-testid="guide-network-warning"` and `data-testid="guide-desktop-download"` for stable behavior tests.

- [ ] **Step 1: Write failing view and locale tests**

Assert that the top warning is visible, a Windows install step renders the exact official download URL before the CLI fallback, and a macOS install step has no desktop-download button. Add these required locale keys:

```ts
'gettingStarted.warnings.networkAccess'
'gettingStarted.installation.downloadDesktop'
'gettingStarted.installation.cliFallback'
```

- [ ] **Step 2: Run the focused tests and confirm RED**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend test:run src/components/getting-started/__tests__/curriculum.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: FAIL because the warning, download action, fallback label, and translations do not exist.

- [ ] **Step 3: Implement the minimal conditional markup and translations**

Render the warning before the existing persistence warning. In the install step, render this block only when `desktopDownloadUrl` exists:

```vue
<a
  v-if="selectedVariant.desktopDownloadUrl"
  data-testid="guide-desktop-download"
  :href="selectedVariant.desktopDownloadUrl"
  target="_blank"
  rel="noopener noreferrer"
>
  {{ t('gettingStarted.installation.downloadDesktop') }}
</a>
<p v-if="selectedVariant.desktopDownloadUrl">
  {{ t('gettingStarted.installation.cliFallback') }}
</p>
```

Keep the existing `GuideCommandBlock` immediately after this conditional block.

- [ ] **Step 4: Run the focused tests and confirm GREEN**

Run the same focused command and expect both files to pass.

### Task 3: Verify the scoped change and refresh the knowledge graph

**Files:**
- Refresh: `graphify-out/**`

**Interfaces:**
- Consumes: completed implementation and tests.
- Produces: current test/typecheck evidence and an updated project graph.

- [ ] **Step 1: Run frontend verification**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend test:run src/components/getting-started/__tests__/curriculum.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend typecheck
```

Expected: both commands exit `0`.

- [ ] **Step 2: Review the diff for scope and accidental installer execution**

```bash
git diff --check
git diff -- frontend/src/components/getting-started frontend/src/views/public/GettingStartedView.vue frontend/src/views/public/__tests__/GettingStartedView.spec.ts frontend/src/i18n/locales/zh/gettingStarted.ts frontend/src/i18n/locales/en/gettingStarted.ts
```

Expected: no whitespace errors, no new client/OS selectors, and no code that fetches or runs an installer.

- [ ] **Step 3: Refresh Graphify**

```bash
graphify update .
```

Expected: graph update completes; the known unreadable `deploy/postgres_data` warning is acceptable.
