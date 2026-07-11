# Codex API Key Example Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate a standard `/v1` Codex provider configuration and make the two-file credential setup explicit and difficult to misuse.

**Architecture:** Keep the existing `config.toml` plus `auth.json` authentication contract. Reuse the component’s existing `apiBase` normalizer for Codex, route all Codex tabs to Codex-specific guidance, and lock the complete generated-file contract down in the existing Vue component test suite.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vue Test Utils, Vitest, vue-i18n, graphify.

## Global Constraints

- Preserve the custom provider ID `OpenAI` and `requires_openai_auth = true`.
- Keep the API key exclusively in generated `auth.json`; never include it in generated `config.toml`.
- Do not generate an `env_key` entry.
- Normalize Codex base URLs to exactly one trailing `/v1` segment.
- Preserve every non-Codex usage example and backend route.
- Provide equivalent Chinese and English guidance.
- Stage and commit only files named by the task; unrelated workspace changes remain untouched.

---

### Task 1: Lock Down the Generated Codex File Contract

**Files:**
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts:1-320`
- Modify: `frontend/src/components/keys/UseKeyModal.vue:413-493`

**Interfaces:**
- Consumes: `currentFiles`, `ensureV1(value: string)`, and `generateOpenAIFiles(baseUrl: string, apiKey: string)` from `UseKeyModal.vue`.
- Produces: Codex `config.toml` with a normalized `/v1` base URL and a separate `auth.json` containing `OPENAI_API_KEY`.

- [ ] **Step 1: Add a generated-file lookup helper to the component test**

Change the Vue Test Utils import and add this helper immediately after importing `UseKeyModal`:

```ts
import { mount, type VueWrapper } from '@vue/test-utils'

function generatedFileContent(wrapper: VueWrapper, pathSuffix: string): string {
  const panel = wrapper.findAll('.linx-code-panel').find((candidate) =>
    candidate.find('span.font-mono').text().endsWith(pathSuffix)
  )

  expect(panel, `generated file ending in ${pathSuffix}`).toBeDefined()
  return panel!.find('pre code').text()
}
```

- [ ] **Step 2: Replace the shallow Codex config test with failing contract tests**

Replace `renders GPT-5.5 and goals feature in OpenAI Codex config` with:

```ts
it.each([
  ['https://example.com', 'https://example.com/v1'],
  ['https://example.com/v1/', 'https://example.com/v1']
])('renders a complete Codex config/auth contract for %s', (baseUrl, expectedBaseUrl) => {
  const wrapper = mount(UseKeyModal, {
    props: {
      show: true,
      apiKey: 'sk-test',
      baseUrl,
      platform: 'openai'
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>'
        },
        Icon: {
          template: '<span />'
        }
      }
    }
  })

  const configToml = generatedFileContent(wrapper, 'config.toml')
  const authJson = generatedFileContent(wrapper, 'auth.json')

  expect(configToml).toContain('model_provider = "OpenAI"')
  expect(configToml).toContain('model = "gpt-5.5"')
  expect(configToml).toContain('review_model = "gpt-5.5"')
  expect(configToml).toContain(`[model_providers.OpenAI]\nname = "OpenAI"\nbase_url = "${expectedBaseUrl}"`)
  expect(configToml).toContain('wire_api = "responses"')
  expect(configToml).toContain('requires_openai_auth = true')
  expect(configToml).toContain('[features]\ngoals = true')
  expect(configToml).not.toContain('sk-test')
  expect(configToml).not.toContain('env_key')
  expect(JSON.parse(authJson)).toEqual({ OPENAI_API_KEY: 'sk-test' })
})
```

- [ ] **Step 3: Run the focused test and verify the bare-URL case fails**

Run:

```bash
cd frontend
./node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: the `https://example.com` case fails because generated `base_url` is still `https://example.com` instead of `https://example.com/v1`. The `/v1/` case may also expose duplicate/trailing-slash behavior.

- [ ] **Step 4: Route normalized `apiBase` into the Codex generator**

In both Codex call sites inside `currentFiles`, replace the raw base URL:

```ts
return generateOpenAIFiles(apiBase, apiKey)
```

This applies to the `unified` Codex branch and the default OpenAI Codex branch.

- [ ] **Step 5: Run the focused test and verify the contract passes**

Run:

```bash
cd frontend
./node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: all `UseKeyModal.spec.ts` tests pass, including both table-driven Codex URL cases.

- [ ] **Step 6: Commit Task 1**

```bash
git add frontend/src/components/keys/UseKeyModal.vue frontend/src/components/keys/__tests__/UseKeyModal.spec.ts
git commit -m "fix(keys): normalize Codex API base URL"
```

---

### Task 2: Make the Two-File Credential Workflow Explicit

**Files:**
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue:339-392`
- Modify: `frontend/src/i18n/locales/zh/dashboard.ts:182-188`
- Modify: `frontend/src/i18n/locales/en/dashboard.ts:182-188`

**Interfaces:**
- Consumes: `activeClientTab`, `activeTab`, and existing `keys.useKeyModal.openai.*` locale keys.
- Produces: Codex-specific description and platform note for both OpenAI and unified keys.

- [ ] **Step 1: Add failing guidance-routing tests**

Add the locale imports after `UseKeyModal`:

```ts
import zhDashboard from '@/i18n/locales/zh/dashboard'
import enDashboard from '@/i18n/locales/en/dashboard'
```

Add these tests after the Codex contract test:

```ts
it('shows Codex-specific guidance when a unified key selects Codex', async () => {
  const wrapper = mount(UseKeyModal, {
    props: {
      show: true,
      apiKey: 'sk-test',
      baseUrl: 'https://example.com',
      platform: 'unified'
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>'
        },
        Icon: {
          template: '<span />'
        }
      }
    }
  })

  const codexTab = wrapper.findAll('nav[aria-label="Client"] button').find((button) =>
    button.text().includes('keys.useKeyModal.cliTabs.codexCli')
  )

  expect(codexTab).toBeDefined()
  await codexTab!.trigger('click')
  await nextTick()

  expect(wrapper.text()).toContain('keys.useKeyModal.openai.description')
  expect(wrapper.text()).toContain('keys.useKeyModal.openai.note')
  expect(wrapper.text()).not.toContain('keys.useKeyModal.unified.note')

  const windowsTab = wrapper.findAll('nav[aria-label="Tabs"] button').find((button) =>
    button.text().includes('Windows')
  )
  expect(windowsTab).toBeDefined()
  await windowsTab!.trigger('click')
  await nextTick()

  expect(wrapper.text()).toContain('keys.useKeyModal.openai.noteWindows')
})

it('documents safe Codex auth.json handling in Chinese and English', () => {
  const zhOpenAI = zhDashboard.keys.useKeyModal.openai
  const enOpenAI = enDashboard.keys.useKeyModal.openai

  expect(zhOpenAI.description).toContain('config.toml')
  expect(zhOpenAI.description).toContain('auth.json')
  expect(zhOpenAI.note).toContain('OPENAI_API_KEY')
  expect(zhOpenAI.note).toContain('env_key')
  expect(zhOpenAI.note).toContain('重启 Codex')
  expect(enOpenAI.description).toContain('config.toml')
  expect(enOpenAI.description).toContain('auth.json')
  expect(enOpenAI.note).toContain('OPENAI_API_KEY')
  expect(enOpenAI.note).toContain('env_key')
  expect(enOpenAI.note).toContain('restart Codex')
})
```

- [ ] **Step 2: Run the focused test and verify both guidance tests fail**

Run:

```bash
cd frontend
./node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: unified Codex still shows the generic unified guidance, and the locale copy lacks the two-file, merge, `env_key`, and restart warnings.

- [ ] **Step 3: Route every Codex client tab to OpenAI guidance**

Add this branch in `platformDescription` after the WorkBuddy branch and before the Python SDK branch:

```ts
if (activeClientTab.value === 'codex') {
  return t('keys.useKeyModal.openai.description')
}
```

Add this branch in `platformNote` after the WorkBuddy branch and before the Python SDK branch:

```ts
if (activeClientTab.value === 'codex') {
  return activeTab.value === 'windows'
    ? t('keys.useKeyModal.openai.noteWindows')
    : t('keys.useKeyModal.openai.note')
}
```

Leave the existing switch branches in place for non-Codex clients.

- [ ] **Step 4: Replace the Chinese Codex guidance**

Use these exact values in `frontend/src/i18n/locales/zh/dashboard.ts`:

```ts
openai: {
  description: '请同时保存下方的 config.toml 和 auth.json 到 Codex CLI 配置目录，两个文件缺一不可。',
  configTomlHint: '请确保以下内容位于 config.toml 文件的开头部分',
  note: '如果 auth.json 已存在，请只合并 OPENAI_API_KEY 字段，不要覆盖其他登录信息。不要把真实密钥写入 env_key；本示例使用 auth.json。保存后请完全退出并重启 Codex，再创建一个新会话。macOS/Linux 用户可运行 mkdir -p ~/.codex 创建目录。',
  noteWindows:
    '如果 auth.json 已存在，请只合并 OPENAI_API_KEY 字段，不要覆盖其他登录信息。不要把真实密钥写入 env_key；本示例使用 auth.json。保存后请完全退出并重启 Codex，再创建一个新会话。按 Win+R，输入 %userprofile%\\.codex 打开配置目录；目录不存在时请先创建。'
},
```

- [ ] **Step 5: Replace the English Codex guidance**

Use these exact values in `frontend/src/i18n/locales/en/dashboard.ts`:

```ts
openai: {
  description: 'Save both config.toml and auth.json below in the Codex CLI config directory; both files are required.',
  configTomlHint: 'Make sure the following content is at the beginning of the config.toml file',
  note: 'If auth.json already exists, merge only the OPENAI_API_KEY property instead of overwriting other sign-in data. Do not put the literal key in env_key; this example uses auth.json. Fully quit and restart Codex after saving, then create a new conversation. On macOS/Linux, run mkdir -p ~/.codex if the directory does not exist.',
  noteWindows: 'If auth.json already exists, merge only the OPENAI_API_KEY property instead of overwriting other sign-in data. Do not put the literal key in env_key; this example uses auth.json. Fully quit and restart Codex after saving, then create a new conversation. Press Win+R and enter %userprofile%\\.codex; create the directory first if it does not exist.',
},
```

- [ ] **Step 6: Run focused tests and type checking**

Run:

```bash
cd frontend
./node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
./node_modules/.bin/vue-tsc --noEmit
```

Expected: the focused suite passes and `vue-tsc` exits with no type errors.

- [ ] **Step 7: Commit Task 2**

```bash
git add frontend/src/components/keys/UseKeyModal.vue \
  frontend/src/components/keys/__tests__/UseKeyModal.spec.ts \
  frontend/src/i18n/locales/zh/dashboard.ts \
  frontend/src/i18n/locales/en/dashboard.ts
git commit -m "fix(keys): clarify Codex credential setup"
```

---

### Task 3: Verify the Change and Refresh the Knowledge Graph

**Files:**
- Verify: `frontend/src/components/keys/UseKeyModal.vue`
- Verify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Verify: `frontend/src/i18n/locales/zh/dashboard.ts`
- Verify: `frontend/src/i18n/locales/en/dashboard.ts`
- Update: `graphify-out/` through the project-prescribed incremental command

**Interfaces:**
- Consumes: completed Task 1 and Task 2 commits.
- Produces: test evidence, static-validation evidence, and an updated project knowledge graph.

- [ ] **Step 1: Run the focused component suite from a clean command path**

```bash
cd frontend
./node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: one test file passes with every `UseKeyModal` test green.

- [ ] **Step 2: Run frontend static validation**

```bash
cd frontend
./node_modules/.bin/vue-tsc --noEmit
./node_modules/.bin/eslint src/components/keys/UseKeyModal.vue \
  src/components/keys/__tests__/UseKeyModal.spec.ts \
  src/i18n/locales/zh/dashboard.ts \
  src/i18n/locales/en/dashboard.ts
```

Expected: both commands exit zero without modifying files.

- [ ] **Step 3: Check patch hygiene and scope**

```bash
git diff --check
git status --short
git diff HEAD~2 -- frontend/src/components/keys/UseKeyModal.vue \
  frontend/src/components/keys/__tests__/UseKeyModal.spec.ts \
  frontend/src/i18n/locales/zh/dashboard.ts \
  frontend/src/i18n/locales/en/dashboard.ts
```

Expected: no whitespace errors; only the four intended frontend files differ across the two implementation commits. Pre-existing untracked files remain unmodified.

- [ ] **Step 4: Update graphify after the code changes**

Run from the repository root:

```bash
graphify update .
```

Expected: incremental graph update completes successfully and reflects the changed Vue, test, and locale files. Dirty `graphify-out/` files are expected by project policy.

- [ ] **Step 5: Record final evidence**

Report the focused test count, type-check and ESLint status, the two implementation commit hashes, and whether graphify completed. Do not claim an end-to-end provider inference test because this plan deliberately avoids sending a billable model request.
