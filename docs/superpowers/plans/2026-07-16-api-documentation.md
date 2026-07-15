# LINX2 API Documentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a public, bilingual, searchable LINX2 API documentation experience at `/docs` that documents only the approved gateway surface and reuses the product's existing client configuration sources.

**Architecture:** Extend the existing pure client-configuration layer first, then build a typed documentation catalog consumed by focused Vue components for the shell, search, guide articles, and endpoint reference. Add two constrained Vue Router records under `/docs`, reuse existing app settings/theme/i18n behavior, and add permanent homepage and shared-sidebar entries without changing backend gateway behavior.

**Tech Stack:** Vue 3 Composition API, TypeScript 5.6, Vue Router 4, Vue I18n 9, Tailwind CSS, `@vueuse/core`, Vitest 2, Vue Test Utils, Go backend regression tests, graphify.

## Global Constraints

- Work only in `/home/alvin/tokenstation3/.worktrees/api-docs` on branch `feat/api-docs`, based on `dev` commit `b58e9f197`.
- Follow test-driven development: add a failing focused test, observe the expected failure, implement the minimum behavior, then rerun the focused test.
- Do not add a new frontend dependency, documentation server, Markdown runtime, or backend documentation endpoint.
- Normal public mode exposes `/docs` anonymously; backend-only mode keeps its existing anonymous allowlist unchanged.
- Preserve the configured external `doc_url`, custom homepage override, `/docs/batch-image`, existing key modal, and beginner guide.
- The public endpoint allowlist is exactly: `POST /v1/messages`, `POST /v1/messages/count_tokens`, `POST /v1/responses`, `POST /v1/chat/completions`, `GET /v1/models`, `POST /v1/images/generations`, and `POST /v1/images/edits`.
- Do not display Gemini, `/v1beta`, Embeddings, video, alpha search, internal aliases, administrator/JWT APIs, batch-image management APIs, or stability/retry/failover/scheduler claims.
- Do not add, rename, or document a balance endpoint; keep `/v1/usage` and `/v1/balance` out of the first release pending a separate decision.
- Public documentation examples always use `$LINX2_API_KEY`; never fetch, inject, persist, log, or route a real API key.
- Keep client commands, Base URL normalization, installation commands, and example model IDs in shared pure modules rather than copying them into documentation components or locale files.
- Preserve the real gateway, Anthropic, OpenAI, and streaming error envelopes as separate documented formats.
- Run `graphify update .` after code changes as required by `AGENTS.md`.

---

## File and Responsibility Map

### Shared configuration sources

- Create `frontend/src/components/keys/clientConfigContract.ts`: own canonical endpoint roots, placeholder/model constants, and shared file/OS types without importing any builder.
- Modify `frontend/src/components/keys/clientConfigFiles.ts`: re-export the public contract and keep Claude Code/Codex/OpenCode/CC Switch generation.
- Create `frontend/src/components/keys/clientExampleFiles.ts`: own pure rich OpenCode, WorkBuddy, and Python SDK example builders currently embedded in `UseKeyModal.vue`.
- Modify `frontend/src/components/keys/UseKeyModal.vue`: render files returned by shared builders; retain only presentation, tab state, and syntax highlighting.

### Documentation domain

- Create `frontend/src/components/api-docs/types.ts`: documentation page, endpoint, parameter, error, navigation, search, and content-block interfaces.
- Create `frontend/src/components/api-docs/catalog.ts`: stable page IDs, routes, groups, capability tags, keywords, and the exact endpoint allowlist.
- Create `frontend/src/components/api-docs/examples.ts`: pure request/response/error examples using shared endpoint and model constants.
- Create `frontend/src/components/api-docs/guideContent.ts`: localized-key-based guide/platform blocks and shared client example files.

### Documentation presentation

- Create `frontend/src/components/api-docs/ApiDocsCodeBlock.vue`: copyable, non-executable code block.
- Create `frontend/src/components/api-docs/ApiEndpointPage.vue`: generic structured endpoint reference.
- Create `frontend/src/components/api-docs/ApiGuidePage.vue`: generic guide/platform block renderer.
- Create `frontend/src/components/api-docs/ApiDocsHeader.vue`: brand, capability tags, search, locale, theme, and account action.
- Create `frontend/src/components/api-docs/ApiDocsSidebar.vue`: grouped desktop navigation and accessible mobile drawer.
- Create `frontend/src/components/api-docs/ApiDocsToc.vue`: desktop/inline table of contents and active heading state.
- Create `frontend/src/components/api-docs/ApiDocsSearch.vue`: in-memory localized search dialog.
- Create `frontend/src/components/api-docs/ApiDocsShell.vue`: responsive three-column composition.
- Create `frontend/src/views/public/ApiDocsView.vue`: route resolution, page content, title, Base URL, and not-found state.

### Product integration

- Modify `frontend/src/router/index.ts`: add constrained public documentation routes before the global catch-all without matching `/docs/batch-image`.
- Modify `frontend/src/views/HomeView.vue`: add the first-party API Docs header link while preserving external `doc_url` and custom-home behavior.
- Modify `frontend/src/components/layout/AppSidebar.vue`: add one shared self-service API Docs item visible in normal and simple modes.
- Create `frontend/src/i18n/locales/zh/apiDocs.ts` and `frontend/src/i18n/locales/en/apiDocs.ts`; import them from both locale indexes.

---

### Task 1: Canonical Gateway Endpoints and Example Constants

**Files:**
- Create: `frontend/src/components/keys/clientConfigContract.ts`
- Modify: `frontend/src/components/keys/clientConfigFiles.ts`
- Modify: `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts`

**Interfaces:**
- Produces from a dependency-free contract module: `DOCS_API_KEY_PLACEHOLDER`, `EXAMPLE_MODELS`, `SupportedGuideOS`, `ClientConfigFile`, `GatewayEndpoints`, and `resolveGatewayEndpoints(baseUrl: string): GatewayEndpoints`.
- Consumed by: Tasks 2, 3, and 7.

- [ ] **Step 1: Write the failing endpoint-normalization and constant tests**

Add these imports and cases to `clientConfigFiles.spec.ts`:

```ts
import {
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  buildClientConfigFiles,
  resolveGatewayEndpoints,
  type ClientConfigInput
} from '../clientConfigFiles'

describe('resolveGatewayEndpoints', () => {
  it.each([
    ['https://gateway.example.com', 'https://gateway.example.com', 'https://gateway.example.com/v1'],
    ['https://gateway.example.com/', 'https://gateway.example.com', 'https://gateway.example.com/v1'],
    ['https://gateway.example.com/v1', 'https://gateway.example.com', 'https://gateway.example.com/v1'],
    ['https://gateway.example.com/v1/', 'https://gateway.example.com', 'https://gateway.example.com/v1']
  ])('normalizes %s once', (input, bare, v1) => {
    expect(resolveGatewayEndpoints(input)).toEqual({
      bare,
      v1,
      messages: `${v1}/messages`,
      countTokens: `${v1}/messages/count_tokens`,
      responses: `${v1}/responses`,
      chatCompletions: `${v1}/chat/completions`,
      models: `${v1}/models`,
      imageGenerations: `${v1}/images/generations`,
      imageEdits: `${v1}/images/edits`
    })
  })

  it('exports one obvious docs placeholder and the existing displayed models', () => {
    expect(DOCS_API_KEY_PLACEHOLDER).toBe('$LINX2_API_KEY')
    expect(EXAMPLE_MODELS).toEqual({
      anthropic: 'claude-opus-4-8',
      anthropicOpenCode: 'claude-fable-5',
      openai: 'gpt-5.5',
      image: 'gpt-image-2'
    })
  })
})
```

- [ ] **Step 2: Run the focused test and observe the missing exports**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientConfigFiles.spec.ts
```

Expected: FAIL because `DOCS_API_KEY_PLACEHOLDER`, `EXAMPLE_MODELS`, and `resolveGatewayEndpoints` are not exported.

- [ ] **Step 3: Add the dependency-free contract and replace the private root helper**

Create `clientConfigContract.ts`:

```ts
export const DOCS_API_KEY_PLACEHOLDER = '$LINX2_API_KEY'

export const EXAMPLE_MODELS = {
  anthropic: 'claude-opus-4-8',
  anthropicOpenCode: 'claude-fable-5',
  openai: 'gpt-5.5',
  image: 'gpt-image-2'
} as const

export type SupportedGuideOS = 'macos' | 'windows' | 'linux'

export interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
  hint?: string
}

export interface GatewayEndpoints {
  bare: string
  v1: string
  messages: string
  countTokens: string
  responses: string
  chatCompletions: string
  models: string
  imageGenerations: string
  imageEdits: string
}

export function resolveGatewayEndpoints(baseUrl: string): GatewayEndpoints {
  const bare = baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const v1 = `${bare}/v1`
  return {
    bare,
    v1,
    messages: `${v1}/messages`,
    countTokens: `${v1}/messages/count_tokens`,
    responses: `${v1}/responses`,
    chatCompletions: `${v1}/chat/completions`,
    models: `${v1}/models`,
    imageGenerations: `${v1}/images/generations`,
    imageEdits: `${v1}/images/edits`
  }
}
```

In `clientConfigFiles.ts`, remove its local `SupportedGuideOS` and `ClientConfigFile` declarations, then import and re-export the contract:

```ts
import {
  EXAMPLE_MODELS,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type SupportedGuideOS
} from './clientConfigContract'

export {
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type GatewayEndpoints,
  type SupportedGuideOS
} from './clientConfigContract'
```

Delete the private `gatewayRoots` helper and replace its call with:

```ts
const { bare, v1 } = resolveGatewayEndpoints(input.baseUrl)
```

Replace copied model literals in the existing builders:

```ts
const model = isAnthropic ? EXAMPLE_MODELS.anthropicOpenCode : EXAMPLE_MODELS.openai
```

Use `EXAMPLE_MODELS.openai` in the Codex and CC Switch templates.

- [ ] **Step 4: Run the focused test and existing guide consumer test**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientConfigFiles.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: both files PASS; generated Claude and Codex content remains byte-for-byte compatible.

- [ ] **Step 5: Commit the canonical endpoint source**

```bash
git add frontend/src/components/keys/clientConfigContract.ts frontend/src/components/keys/clientConfigFiles.ts frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts
git commit -m "refactor: centralize gateway example endpoints"
```

---

### Task 2: Extract Shared OpenCode, WorkBuddy, and Python SDK Builders

**Files:**
- Create: `frontend/src/components/keys/clientExampleFiles.ts`
- Create: `frontend/src/components/keys/__tests__/clientExampleFiles.spec.ts`
- Modify: `frontend/src/components/keys/clientConfigFiles.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue`
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Modify: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`

**Interfaces:**
- Consumes: `EXAMPLE_MODELS`, `GatewayEndpoints`, `resolveGatewayEndpoints`, and `ClientConfigFile` from Task 1's dependency-free contract.
- Produces:
  - `buildOpenCodeConfigFile(input: OpenCodeConfigInput): ClientConfigFile`
  - `buildWorkBuddyConfigFile(input: WorkBuddyConfigInput): ClientConfigFile`
  - `buildPythonSdkExample(input: PythonSdkExampleInput): ClientConfigFile`
- Consumed by: `UseKeyModal.vue`, `buildClientConfigFiles`, and Task 7.

- [ ] **Step 1: Add failing pure-builder parity tests**

Create `clientExampleFiles.spec.ts` with these contracts:

```ts
import { describe, expect, it } from 'vitest'

import {
  buildOpenCodeConfigFile,
  buildPythonSdkExample,
  buildWorkBuddyConfigFile
} from '../clientExampleFiles'
import { DOCS_API_KEY_PLACEHOLDER, resolveGatewayEndpoints } from '../clientConfigFiles'

const endpoints = resolveGatewayEndpoints('https://gateway.example.com/v1/')

describe('client example files', () => {
  it('preserves the rich OpenCode OpenAI model catalog', () => {
    const file = buildOpenCodeConfigFile({
      platform: 'openai',
      baseUrl: endpoints.v1,
      apiKey: DOCS_API_KEY_PLACEHOLDER,
      path: 'opencode.json'
    })
    const parsed = JSON.parse(file.content)

    expect(file.hintKey).toBe('keys.useKeyModal.opencode.hint')
    expect(parsed.model).toBe('openai/gpt-5.5')
    expect(parsed.provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com/v1',
      apiKey: '$LINX2_API_KEY'
    })
    expect(parsed.provider.openai.models['gpt-5.6'].variants).toHaveProperty('max')
    expect(parsed.provider.openai.models['gpt-5.4-mini'].limit.context).toBe(400000)
  })

  it('preserves the rich Anthropic OpenCode model catalog', () => {
    const file = buildOpenCodeConfigFile({
      platform: 'anthropic',
      baseUrl: endpoints.v1,
      apiKey: DOCS_API_KEY_PLACEHOLDER,
      path: 'opencode.json'
    })
    const parsed = JSON.parse(file.content)

    expect(parsed.model).toBe('anthropic/claude-fable-5')
    expect(parsed.provider.anthropic.models['claude-fable-5'].options.thinking.type).toBe('adaptive')
    expect(parsed.provider.anthropic.models).toHaveProperty('claude-opus-4-8')
  })

  it('builds WorkBuddy from the same gateway and displayed models', () => {
    const file = buildWorkBuddyConfigFile({
      os: 'macos',
      platform: 'unified',
      endpoints,
      apiKey: DOCS_API_KEY_PLACEHOLDER
    })
    const parsed = JSON.parse(file.content)

    expect(file.path).toBe('~/.workbuddy/models.json')
    expect(file.hintKey).toBe('keys.useKeyModal.workBuddy.hint')
    expect(parsed.availableModels).toEqual(['gpt-5.5', 'claude-sonnet-5', 'claude-opus-4-8'])
    expect(parsed.models.every((model: { url: string }) => model.url === endpoints.chatCompletions)).toBe(true)
  })

  it.each([
    ['anthropic', 'anthropic_client.py', 'base_url="https://gateway.example.com"', 'model="claude-opus-4-8"'],
    ['openai', 'openai_client.py', 'base_url="https://gateway.example.com/v1"', 'model="gpt-5.5"'],
    ['image', 'gpt_image_2_client.py', 'base_url="https://gateway.example.com/v1"', 'model="gpt-image-2"']
  ] as const)('builds the current %s Python example', (kind, path, baseLine, modelLine) => {
    const file = buildPythonSdkExample({
      kind,
      endpoints,
      apiKey: DOCS_API_KEY_PLACEHOLDER
    })

    expect(file.path).toBe(path)
    expect(file.content).toContain(baseLine)
    expect(file.content).toContain(modelLine)
    expect(file.content).toContain('$LINX2_API_KEY')
  })
})
```

- [ ] **Step 2: Run the new test and observe the missing module**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientExampleFiles.spec.ts
```

Expected: FAIL because `clientExampleFiles.ts` does not exist.

- [ ] **Step 3: Create the pure example module and move existing output without changing it**

Create `clientExampleFiles.ts` with these public types and builders:

```ts
import type { GroupPlatform } from '@/types'
import {
  EXAMPLE_MODELS,
  type ClientConfigFile,
  type GatewayEndpoints,
  type SupportedGuideOS
} from './clientConfigContract'

export type OpenCodeConfigPlatform =
  | GroupPlatform
  | 'antigravity-claude'
  | 'antigravity-gemini'

export interface OpenCodeConfigInput {
  platform: OpenCodeConfigPlatform
  baseUrl: string
  apiKey: string
  path: string
}

export interface WorkBuddyConfigInput {
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  endpoints: GatewayEndpoints
  apiKey: string
}

export interface PythonSdkExampleInput {
  kind: 'anthropic' | 'openai' | 'image'
  endpoints: GatewayEndpoints
  apiKey: string
}

export function buildPythonSdkExample(input: PythonSdkExampleInput): ClientConfigFile {
  if (input.kind === 'anthropic') {
    return {
      path: 'anthropic_client.py',
      content: `from anthropic import Anthropic

client = Anthropic(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.bare}",
)

with client.messages.stream(
    model="${EXAMPLE_MODELS.anthropic}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello, Claude"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()`
    }
  }

  if (input.kind === 'openai') {
    return {
      path: 'openai_client.py',
      content: `from openai import OpenAI

client = OpenAI(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.v1}",
)

stream = client.responses.create(
    model="${EXAMPLE_MODELS.openai}",
    input="Hello, GPT",
    stream=True,
)

for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
print()`
    }
  }

  return {
    path: 'gpt_image_2_client.py',
    content: `from base64 import b64decode
from pathlib import Path

from openai import OpenAI

client = OpenAI(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.v1}",
)

stream = client.images.generate(
    model="${EXAMPLE_MODELS.image}",
    prompt="A fox mascot using an AI gateway",
    size="1024x1024",
    stream=True,
    partial_images=2,
)

for event in stream:
    image_b64 = getattr(event, "b64_json", None)
    if not image_b64:
        continue
    if event.type == "image_generation.partial_image":
        output_path = Path(f"partial_{event.partial_image_index}.png")
    elif event.type == "image_generation.completed":
        output_path = Path("image.png")
    else:
        continue
    output_path.write_bytes(b64decode(image_b64))
    print(f"Wrote {output_path}")`
  }
}
```

Move the existing `WorkBuddyModelConfig`, `workBuddyModelsForPlatform`, and `generateWorkBuddyConfigFile` logic from `UseKeyModal.vue` into `buildWorkBuddyConfigFile`. Make OS and dependencies explicit, replace the local URL with `input.endpoints.chatCompletions`, return `hintKey: 'keys.useKeyModal.workBuddy.hint'`, and preserve the existing model order and numeric limits.

Move the complete existing `generateOpenCodeConfig` function body and all of its current model dictionaries from `UseKeyModal.vue` into `buildOpenCodeConfigFile`. The move is mechanical: replace `platform`, `baseUrl`, `apiKey`, and `pathLabel` reads with `input.platform`, `input.baseUrl`, `input.apiKey`, and `input.path`; return `hintKey: 'keys.useKeyModal.opencode.hint'` instead of calling `t`. Do not remove any existing Gemini, Antigravity, or Grok branch from the shared key-modal generator; those branches remain supported by the existing modal but will be filtered out of the docs catalog. Import only from `clientConfigContract.ts`, never from `clientConfigFiles.ts`, so `clientConfigFiles.ts` can consume this builder without a cycle.

- [ ] **Step 4: Route all existing consumers through the pure builders**

In `UseKeyModal.vue`, import:

```ts
import {
  buildOpenCodeConfigFile,
  buildPythonSdkExample,
  buildWorkBuddyConfigFile
} from './clientExampleFiles'
import { buildClientConfigFiles } from './clientConfigFiles'
import { resolveGatewayEndpoints, type ClientConfigFile } from './clientConfigContract'
```

Replace local endpoint closures with:

```ts
const endpoints = resolveGatewayEndpoints(props.baseUrl || window.location.origin)
```

Replace OpenCode, WorkBuddy, and Python branches with shared calls, mapping `hintKey` through `t` in the same place used for `buildClientConfigFiles`:

```ts
const localizeFiles = (files: ClientConfigFile[]): FileConfig[] =>
  files.map(({ hintKey, ...file }) => hintKey ? { ...file, hint: t(hintKey) } : file)
```

Delete the moved local builders and model dictionaries from `UseKeyModal.vue`.

In `clientConfigFiles.ts`, replace its private simplified `buildOpenCodeFile` with `buildOpenCodeConfigFile` calls. Preserve the existing per-OS paths, unified two-file behavior, platform-specific Base URLs, and hint key. This makes the beginner guide and key modal consume the same rich OpenCode output.

- [ ] **Step 5: Add explicit modal/guide parity assertions and run focused tests**

Extend `UseKeyModal.spec.ts` so unified OpenCode output equals `buildClientConfigFiles({ client: 'opencode', ... })`, and extend `GettingStartedView.spec.ts` to assert the rendered OpenCode file contains `gpt-5.4-mini` and the same default model as the shared builder.

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientExampleFiles.spec.ts src/components/keys/__tests__/clientConfigFiles.spec.ts src/components/keys/__tests__/UseKeyModal.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: all four files PASS and existing Claude, Codex, SDK, WorkBuddy, and rich OpenCode assertions remain green.

- [ ] **Step 6: Commit the shared instruction extraction**

```bash
git add frontend/src/components/keys/clientExampleFiles.ts frontend/src/components/keys/clientConfigFiles.ts frontend/src/components/keys/UseKeyModal.vue frontend/src/components/keys/__tests__/clientExampleFiles.spec.ts frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts frontend/src/components/keys/__tests__/UseKeyModal.spec.ts frontend/src/views/public/__tests__/GettingStartedView.spec.ts
git commit -m "refactor: share client setup examples"
```

---

### Task 3: Typed Documentation Catalog, Examples, and Locale Contract

**Files:**
- Create: `frontend/src/components/api-docs/types.ts`
- Create: `frontend/src/components/api-docs/catalog.ts`
- Create: `frontend/src/components/api-docs/examples.ts`
- Create: `frontend/src/components/api-docs/__tests__/catalog.spec.ts`
- Create: `frontend/src/i18n/locales/zh/apiDocs.ts`
- Create: `frontend/src/i18n/locales/en/apiDocs.ts`
- Modify: `frontend/src/i18n/locales/zh/index.ts`
- Modify: `frontend/src/i18n/locales/en/index.ts`

**Interfaces:**
- Consumes: `DOCS_API_KEY_PLACEHOLDER`, `EXAMPLE_MODELS`, and `resolveGatewayEndpoints` from `clientConfigContract.ts` in Task 1.
- Produces: `API_DOCS_PAGES`, `API_DOCS_NAV`, `API_DOCS_CAPABILITY_TAGS`, `API_ENDPOINTS`, `findApiDocsPage(path)`, and `buildEndpointExamples(endpointId, baseUrl)`.
- Consumed by: Tasks 4–8.

- [ ] **Step 1: Write the failing catalog allowlist and locale-parity tests**

Create `catalog.spec.ts`:

```ts
import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en/apiDocs'
import zh from '@/i18n/locales/zh/apiDocs'
import {
  API_DOCS_CAPABILITY_TAGS,
  API_DOCS_PAGES,
  API_ENDPOINTS,
  findApiDocsPage
} from '../catalog'
import { buildEndpointExamples } from '../examples'

function leafPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]
  return Object.entries(value).flatMap(([key, child]) =>
    leafPaths(child, prefix ? `${prefix}.${key}` : key)
  )
}

describe('API docs catalog', () => {
  it('contains exactly the approved endpoint allowlist', () => {
    expect(API_ENDPOINTS.map(({ method, path }) => `${method} ${path}`)).toEqual([
      'POST /v1/messages',
      'POST /v1/messages/count_tokens',
      'POST /v1/responses',
      'POST /v1/chat/completions',
      'GET /v1/models',
      'POST /v1/images/generations',
      'POST /v1/images/edits'
    ])
  })

  it('keeps excluded capabilities out of routes, tags, keywords, and examples', () => {
    const searchable = JSON.stringify({ API_DOCS_CAPABILITY_TAGS, API_DOCS_PAGES, API_ENDPOINTS })
    expect(searchable).not.toMatch(/gemini|v1beta|embedding|video|alpha\/search|failover|scheduler|stability/i)
    expect(searchable).not.toContain('/v1/usage')
    expect(searchable).not.toContain('/v1/balance')
  })

  it('has unique IDs and routes and resolves the docs index to quickstart', () => {
    expect(new Set(API_DOCS_PAGES.map(({ id }) => id)).size).toBe(API_DOCS_PAGES.length)
    expect(new Set(API_DOCS_PAGES.map(({ path }) => path)).size).toBe(API_DOCS_PAGES.length)
    expect(findApiDocsPage('/docs')?.id).toBe('quickstart')
  })

  it('keeps Chinese and English locale leaves identical', () => {
    expect(leafPaths(zh).sort()).toEqual(leafPaths(en).sort())
  })

  it('uses shared placeholders and normalized endpoints in every example', () => {
    for (const endpoint of API_ENDPOINTS) {
      const examples = buildEndpointExamples(endpoint.id, 'https://gateway.example.com/v1/')
      expect(examples.curl).toContain('https://gateway.example.com/v1')
      expect(examples.curl).not.toContain('/v1/v1')
      expect(examples.curl).toContain('$LINX2_API_KEY')
      expect(examples.success).not.toContain('$LINX2_API_KEY')
    }
  })
})
```

- [ ] **Step 2: Run the catalog test and observe missing modules**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/catalog.spec.ts
```

Expected: FAIL because the catalog, examples, and locale modules do not exist.

- [ ] **Step 3: Define the documentation domain types**

Create `types.ts`:

```ts
export type ApiDocsPageKind = 'guide' | 'endpoint' | 'platform'
export type ApiDocsMethod = 'GET' | 'POST'
export type ApiDocsProtocol = 'anthropic' | 'openai' | 'common'

export type ApiDocsPageId =
  | 'quickstart'
  | 'authentication'
  | 'client-integration'
  | 'capabilities'
  | 'messages'
  | 'count-tokens'
  | 'responses'
  | 'chat-completions'
  | 'models'
  | 'image-generations'
  | 'image-edits'
  | 'errors'
  | 'request-id'
  | 'key-security'

export interface ApiDocsPage {
  id: ApiDocsPageId
  kind: ApiDocsPageKind
  path: string
  titleKey: string
  summaryKey: string
  keywords: string[]
  endpointId?: ApiEndpointId
}

export interface ApiDocsNavGroup {
  id: 'quickstart' | 'clients' | 'reference' | 'advanced' | 'platform'
  labelKey: string
  pageIds: ApiDocsPageId[]
}

export interface ApiParameter {
  name: string
  location: 'body' | 'header' | 'path'
  required: boolean
  type: string
  descriptionKey: string
}

export type ApiEndpointId =
  | 'messages'
  | 'count-tokens'
  | 'responses'
  | 'chat-completions'
  | 'models'
  | 'image-generations'
  | 'image-edits'

export interface ApiEndpointDefinition {
  id: ApiEndpointId
  pageId: ApiDocsPageId
  method: ApiDocsMethod
  path: string
  protocol: ApiDocsProtocol
  titleKey: string
  summaryKey: string
  parameters: ApiParameter[]
  errorCodes: string[]
  supportsStreaming: boolean
}

export interface ApiEndpointExamples {
  curl: string
  python?: string
  success: string
  stream?: string
}
```

- [ ] **Step 4: Create the exact page, navigation, tag, and endpoint registries**

In `catalog.ts`, define these routes and groupings:

```ts
export const API_DOCS_CAPABILITY_TAGS = [
  'Messages',
  'Responses',
  'Chat Completions',
  'Images',
  'Tools',
  'Streaming'
] as const

export const API_DOCS_NAV: ApiDocsNavGroup[] = [
  { id: 'quickstart', labelKey: 'apiDocs.nav.quickstart', pageIds: ['quickstart', 'authentication'] },
  { id: 'clients', labelKey: 'apiDocs.nav.clients', pageIds: ['client-integration'] },
  { id: 'reference', labelKey: 'apiDocs.nav.reference', pageIds: ['messages', 'count-tokens', 'responses', 'chat-completions', 'models', 'image-generations', 'image-edits'] },
  { id: 'advanced', labelKey: 'apiDocs.nav.advanced', pageIds: ['capabilities'] },
  { id: 'platform', labelKey: 'apiDocs.nav.platform', pageIds: ['errors', 'request-id', 'key-security'] }
]
```

Create `API_DOCS_PAGES` with the exact paths below and localized title/summary keys:

```text
/docs
/docs/guide/authentication
/docs/guide/client-integration
/docs/guide/capabilities
/docs/api-reference/messages
/docs/api-reference/count-tokens
/docs/api-reference/responses
/docs/api-reference/chat-completions
/docs/api-reference/models
/docs/api-reference/image-generations
/docs/api-reference/image-edits
/docs/platform/errors
/docs/platform/request-id
/docs/platform/key-security
```

Create `API_ENDPOINTS` in the approved order. Use parameter rows for the fields actually shown in examples:

- Messages: `model`, `max_tokens`, `messages`, `stream`, `tools`.
- Count Tokens: `model`, `messages`, `system`, `tools`.
- Responses: `model`, `input`, `stream`, `tools`, `text`, `reasoning`.
- Chat Completions: `model`, `messages`, `stream`, `tools`, `response_format`.
- Models: no body parameters.
- Image Generations: `model`, `prompt`, `size`, `n`, `quality`.
- Image Edits: `model`, `image`, `prompt`, `size`.

Implement lookup without fallback guessing:

```ts
export function findApiDocsPage(path: string): ApiDocsPage | undefined {
  const normalized = path.length > 1 ? path.replace(/\/+$/, '') : path
  return API_DOCS_PAGES.find((page) => page.path === normalized)
}
```

- [ ] **Step 5: Add real, bounded examples from shared constants**

In `examples.ts`, use `resolveGatewayEndpoints`, `DOCS_API_KEY_PLACEHOLDER`, and `EXAMPLE_MODELS`. Implement one switch over `ApiEndpointId`; each branch returns complete cURL and response JSON. Use these canonical request bodies:

```ts
const requestBodies = {
  messages: {
    model: EXAMPLE_MODELS.anthropic,
    max_tokens: 1024,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  'count-tokens': {
    model: EXAMPLE_MODELS.anthropic,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  responses: {
    model: EXAMPLE_MODELS.openai,
    input: 'Hello'
  },
  'chat-completions': {
    model: EXAMPLE_MODELS.openai,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  'image-generations': {
    model: EXAMPLE_MODELS.image,
    prompt: 'A fox mascot using an AI gateway',
    size: '1024x1024'
  }
} as const
```

For image edits, use multipart `-F` fields and `-F image=@input.png`. For Models, emit a GET without a body. Use an Anthropic success envelope for Messages/count tokens, OpenAI Responses/Chat/Image success envelopes for their respective pages, and an OpenAI list envelope for Models. Streaming samples must use Anthropic `event: content_block_delta`, Responses `response.output_text.delta` plus `response.completed`, and Chat Completions `data: {"choices":[{"delta":{"content":"Hello"}}]}` plus `[DONE]`.

- [ ] **Step 6: Add complete Chinese and English locale trees and import them**

Each locale module must export `{ apiDocs: { ... } }` with identical keys for:

- `title`, `navLabel`, `search`, `searchPlaceholder`, `noResults`, `menu`, `onThisPage`, `copy`, `copied`, `dashboard`, `login`, `notFoundTitle`, `notFoundDescription`.
- Navigation groups and every page title/summary.
- Section headings: overview, authentication, request, parameters, response, streaming, errors, troubleshooting, installation, configuration, security.
- Every parameter description listed in Step 4.
- Guide prose for quickstart, authentication, client integration, capabilities, request IDs, and key security.
- Error-code recommended actions from the approved design.

Use the exact product labels `API 文档` / `API Docs`, `新手教程` / `Beginner Guide`, and `API 密钥` / `API Keys`. Keep endpoint paths, JSON fields, commands, environment variables, error codes, and model IDs outside localized prose.

Import and spread the new locale in both indexes:

```ts
import apiDocs from './apiDocs'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...gettingStarted,
  ...apiDocs,
  admin,
  ...misc,
  ...webchat
}
```

- [ ] **Step 7: Run the catalog contract test**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/catalog.spec.ts
```

Expected: PASS with exactly seven endpoints and identical locale leaf paths.

- [ ] **Step 8: Commit the typed documentation domain**

```bash
git add frontend/src/components/api-docs/types.ts frontend/src/components/api-docs/catalog.ts frontend/src/components/api-docs/examples.ts frontend/src/components/api-docs/__tests__/catalog.spec.ts frontend/src/i18n/locales/zh/apiDocs.ts frontend/src/i18n/locales/en/apiDocs.ts frontend/src/i18n/locales/zh/index.ts frontend/src/i18n/locales/en/index.ts
git commit -m "feat: add typed API documentation catalog"
```

---

### Task 4: Copyable Code Blocks and Endpoint Reference Renderer

**Files:**
- Create: `frontend/src/components/api-docs/ApiDocsCodeBlock.vue`
- Create: `frontend/src/components/api-docs/ApiEndpointPage.vue`
- Create: `frontend/src/components/api-docs/__tests__/ApiDocsCodeBlock.spec.ts`
- Create: `frontend/src/components/api-docs/__tests__/ApiEndpointPage.spec.ts`

**Interfaces:**
- Consumes: `ApiEndpointDefinition`, `ApiEndpointExamples`, and locale keys from Task 3.
- Produces: reusable code and endpoint article components for Tasks 7 and 8.

- [ ] **Step 1: Write failing component tests for copy behavior and protocol rendering**

Cover these exact assertions:

```ts
it('copies the unchanged example and announces success', async () => {
  const wrapper = mount(ApiDocsCodeBlock, {
    props: { label: 'curl', language: 'bash', code: 'curl https://example.test' }
  })
  await wrapper.get('[data-testid="api-docs-copy"]').trigger('click')
  expect(copyToClipboardMock).toHaveBeenCalledWith('curl https://example.test', 'apiDocs.copied')
  expect(wrapper.get('[role="status"]').text()).toContain('apiDocs.copied')
})

it('renders the endpoint method, path, parameters, examples, and errors', () => {
  const wrapper = mount(ApiEndpointPage, {
    props: {
      endpoint: API_ENDPOINTS.find(({ id }) => id === 'responses')!,
      examples: buildEndpointExamples('responses', 'https://gateway.example.com')
    }
  })
  expect(wrapper.get('[data-testid="endpoint-method"]').text()).toBe('POST')
  expect(wrapper.get('[data-testid="endpoint-path"]').text()).toBe('/v1/responses')
  expect(wrapper.findAll('[data-testid="endpoint-parameter"]').length).toBeGreaterThan(0)
  expect(wrapper.text()).toContain('invalid_request_error')
  expect(wrapper.findAll('[data-testid="api-docs-code-block"]')).toHaveLength(4)
})
```

- [ ] **Step 2: Run the component tests and observe missing components**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsCodeBlock.spec.ts src/components/api-docs/__tests__/ApiEndpointPage.spec.ts
```

Expected: FAIL because both components are missing.

- [ ] **Step 3: Implement the non-executable code block**

`ApiDocsCodeBlock.vue` must:

- Accept `label: string`, `language: string`, and `code: string`.
- Render code with `v-text`, never `v-html`.
- Call the existing `useClipboard` composable.
- Keep a two-second copied state with cleanup in `onBeforeUnmount`.
- Expose `data-testid="api-docs-code-block"` and `data-testid="api-docs-copy"`.
- Put horizontal overflow on the `<pre>` only.
- Use `role="status"` and `aria-live="polite"` for copy feedback.
- Never include a send/run button.

Use this state shape:

```ts
const copied = ref(false)
let resetTimer: number | undefined

async function copy(): Promise<void> {
  if (!await copyToClipboard(props.code, t('apiDocs.copied'))) return
  copied.value = true
  if (resetTimer !== undefined) window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => { copied.value = false }, 2000)
}
```

- [ ] **Step 4: Implement the generic endpoint article**

`ApiEndpointPage.vue` must render these stable heading IDs in order:

```ts
const headings = [
  { id: 'overview', labelKey: 'apiDocs.sections.overview' },
  { id: 'authentication', labelKey: 'apiDocs.sections.authentication' },
  { id: 'parameters', labelKey: 'apiDocs.sections.parameters' },
  { id: 'request', labelKey: 'apiDocs.sections.request' },
  { id: 'response', labelKey: 'apiDocs.sections.response' },
  { id: 'streaming', labelKey: 'apiDocs.sections.streaming' },
  { id: 'errors', labelKey: 'apiDocs.sections.errors' }
].filter(({ id }) => id !== 'streaming' || props.endpoint.supportsStreaming)
```

Expose the headings through:

```ts
defineExpose({ headings })
```

Render one authentication example using `Authorization: Bearer $LINX2_API_KEY`, a semantic parameter table, cURL and optional Python request blocks, a success response block, optional stream block, and endpoint error chips. Add the gateway-envelope warning to every OpenAI protocol page.

- [ ] **Step 5: Run the focused component tests**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsCodeBlock.spec.ts src/components/api-docs/__tests__/ApiEndpointPage.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit the endpoint primitives**

```bash
git add frontend/src/components/api-docs/ApiDocsCodeBlock.vue frontend/src/components/api-docs/ApiEndpointPage.vue frontend/src/components/api-docs/__tests__/ApiDocsCodeBlock.spec.ts frontend/src/components/api-docs/__tests__/ApiEndpointPage.spec.ts
git commit -m "feat: render API endpoint references"
```

---

### Task 5: Classic Three-Column Documentation Shell

**Files:**
- Create: `frontend/src/components/api-docs/ApiDocsHeader.vue`
- Create: `frontend/src/components/api-docs/ApiDocsSidebar.vue`
- Create: `frontend/src/components/api-docs/ApiDocsToc.vue`
- Create: `frontend/src/components/api-docs/ApiDocsShell.vue`
- Create: `frontend/src/components/api-docs/__tests__/ApiDocsShell.spec.ts`

**Interfaces:**
- Consumes: page/navigation/tag types and locale keys from Task 3.
- Produces: slots `default` and `search`, plus events `openSearch` and `navigate`, for Task 8.

- [ ] **Step 1: Write the failing desktop/mobile shell test**

The test must mount the shell with `currentPageId="quickstart"` and assert:

```ts
expect(wrapper.get('[data-testid="api-docs-header"]').exists()).toBe(true)
expect(wrapper.get('[data-testid="api-docs-sidebar"]').classes()).toContain('lg:block')
expect(wrapper.get('[data-testid="api-docs-content"]').classes()).toContain('min-w-0')
expect(wrapper.get('[data-testid="api-docs-toc"]').classes()).toContain('xl:block')
expect(wrapper.findAll('[data-testid="api-docs-capability-tag"]').map((node) => node.text())).toEqual([
  'Messages', 'Responses', 'Chat Completions', 'Images', 'Tools', 'Streaming'
])

await wrapper.get('[data-testid="api-docs-mobile-menu"]').trigger('click')
expect(wrapper.get('[data-testid="api-docs-mobile-drawer"]').attributes('aria-modal')).toBe('true')
await wrapper.get('[data-testid="api-docs-mobile-close"]').trigger('click')
expect(wrapper.find('[data-testid="api-docs-mobile-drawer"]').exists()).toBe(false)
```

- [ ] **Step 2: Run the shell test and observe missing components**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsShell.spec.ts
```

Expected: FAIL because the shell components do not exist.

- [ ] **Step 3: Implement header, grouped navigation, and table of contents**

`ApiDocsHeader.vue` reuses `LocaleSwitcher`, `LinxWordmark`, `Icon`, `useAppStore`, `useAuthStore`, and `sanitizeUrl`. It emits `openSearch`, shows the six catalog tags in a horizontally scrollable row, toggles the existing `dark` class/local-storage theme value, and links anonymous users to:

```ts
{ path: '/login', query: { redirect: route.fullPath } }
```

Authenticated users link to `/dashboard` or `/admin/my-account/dashboard`.

`ApiDocsSidebar.vue` renders `API_DOCS_NAV` and resolves pages by ID. Use `RouterLink` and close the mobile drawer after navigation. The drawer has `role="dialog"`, `aria-modal="true"`, a labeled close button, Escape handling, initial focus on close, and focus restoration to the menu trigger.

`ApiDocsToc.vue` accepts:

```ts
defineProps<{
  headings: Array<{ id: string; label: string }>
  activeId: string
  inline?: boolean
}>()
```

Every link is `#${heading.id}` and uses `aria-current="location"` for the active item.

- [ ] **Step 4: Implement the responsive composition**

`ApiDocsShell.vue` uses this layout contract:

```html
<main class="mx-auto grid min-w-0 max-w-[96rem] gap-8 px-4 py-6 sm:px-6 lg:grid-cols-[17rem_minmax(0,1fr)] xl:grid-cols-[17rem_minmax(0,1fr)_13rem] lg:px-8">
```

- Desktop sidebar: hidden below `lg`, sticky under the docs header.
- Desktop TOC: hidden below `xl`, sticky under the docs header.
- Tablet inline TOC: visible from `lg` through `xl` using the same heading input.
- Mobile menu button: visible below `lg`.
- Content: `min-w-0`, bounded article width, no page-level horizontal overflow.

Use `@vueuse/core` `useIntersectionObserver` in the shell to update `activeHeadingId` from elements matching the current heading IDs. On hash navigation, rely on native anchors and `scroll-margin-top` classes.

- [ ] **Step 5: Run the shell test**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsShell.spec.ts
```

Expected: PASS, including drawer open/close and the exact six tags.

- [ ] **Step 6: Commit the documentation shell**

```bash
git add frontend/src/components/api-docs/ApiDocsHeader.vue frontend/src/components/api-docs/ApiDocsSidebar.vue frontend/src/components/api-docs/ApiDocsToc.vue frontend/src/components/api-docs/ApiDocsShell.vue frontend/src/components/api-docs/__tests__/ApiDocsShell.spec.ts
git commit -m "feat: add responsive API docs shell"
```

---

### Task 6: Localized Keyboard Search

**Files:**
- Create: `frontend/src/components/api-docs/ApiDocsSearch.vue`
- Create: `frontend/src/components/api-docs/search.ts`
- Create: `frontend/src/components/api-docs/__tests__/ApiDocsSearch.spec.ts`
- Modify: `frontend/src/components/api-docs/ApiDocsHeader.vue`
- Modify: `frontend/src/components/api-docs/ApiDocsShell.vue`

**Interfaces:**
- Consumes: `API_DOCS_PAGES`, `API_ENDPOINTS`, and i18n translations.
- Produces: `buildApiDocsSearchEntries(t)` and a controlled `show` search dialog emitting `close` and `select(path)`.
- Consumed by: Task 8.

- [ ] **Step 1: Write failing search-index and keyboard tests**

Test these behaviors:

```ts
it('indexes localized titles, endpoint paths, and error codes', () => {
  const entries = buildApiDocsSearchEntries((key) => key)
  expect(entries.some(({ text }) => text.includes('/v1/responses'))).toBe(true)
  expect(entries.some(({ text }) => text.includes('INVALID_API_KEY'))).toBe(true)
  expect(JSON.stringify(entries)).not.toMatch(/gemini|embedding|video|failover/i)
})

it('opens from slash, filters, selects, and restores focus', async () => {
  const routerPush = vi.spyOn(router, 'push').mockResolvedValue(undefined)
  const wrapper = mount(ApiDocsShell, {
    props: { currentPageId: 'quickstart', headings: [] },
    global: { plugins: [router] }
  })
  const openButton = wrapper.get('[data-testid="api-docs-search-open"]')
  openButton.element.focus()
  window.dispatchEvent(new KeyboardEvent('keydown', { key: '/' }))
  await nextTick()
  expect(wrapper.get('[role="dialog"]').exists()).toBe(true)
  await wrapper.get('input[type="search"]').setValue('responses')
  expect(wrapper.findAll('[data-testid="api-docs-search-result"]')).toHaveLength(1)
  await wrapper.get('[data-testid="api-docs-search-result"]').trigger('click')
  expect(routerPushMock).toHaveBeenCalledWith('/docs/api-reference/responses')
  expect(document.activeElement).toBe(openButton.element)
})
```

- [ ] **Step 2: Run the search test and observe missing implementations**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsSearch.spec.ts
```

Expected: FAIL because `search.ts` and `ApiDocsSearch.vue` do not exist.

- [ ] **Step 3: Implement the in-memory localized search index**

Define:

```ts
export interface ApiDocsSearchEntry {
  id: string
  path: string
  title: string
  section: string
  text: string
}

export function buildApiDocsSearchEntries(t: (key: string) => string): ApiDocsSearchEntry[] {
  return API_DOCS_PAGES.map((page) => {
    const endpoint = API_ENDPOINTS.find(({ pageId }) => pageId === page.id)
    const text = [
      t(page.titleKey),
      t(page.summaryKey),
      endpoint?.path ?? '',
      ...(endpoint?.errorCodes ?? []),
      ...page.keywords
    ].join(' ').toLowerCase()
    return { id: page.id, path: page.path, title: t(page.titleKey), section: page.kind, text }
  })
}
```

Filtering is a case-insensitive whitespace-token AND match. Empty queries show all pages in catalog order.

- [ ] **Step 4: Implement the accessible search dialog and global shortcuts**

`ApiDocsSearch.vue` accepts `show` and `entries`, filters locally, focuses the input after opening, closes on Escape/backdrop, emits the selected path, and restores focus to the triggering search button after close. `ApiDocsShell.vue` listens for `/` unless focus is already in an input/textarea/contenteditable, and for `metaKey || ctrlKey` with `k`; both prevent default and open search.

- [ ] **Step 5: Run the search and shell tests**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/ApiDocsSearch.spec.ts src/components/api-docs/__tests__/ApiDocsShell.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit localized search**

```bash
git add frontend/src/components/api-docs/ApiDocsSearch.vue frontend/src/components/api-docs/search.ts frontend/src/components/api-docs/__tests__/ApiDocsSearch.spec.ts frontend/src/components/api-docs/ApiDocsHeader.vue frontend/src/components/api-docs/ApiDocsShell.vue
git commit -m "feat: add API docs search"
```

---

### Task 7: Guide, Capability, Error, and Security Content

**Files:**
- Create: `frontend/src/components/api-docs/guideContent.ts`
- Create: `frontend/src/components/api-docs/ApiGuidePage.vue`
- Create: `frontend/src/components/api-docs/__tests__/guideContent.spec.ts`
- Create: `frontend/src/components/api-docs/__tests__/ApiGuidePage.spec.ts`

**Interfaces:**
- Consumes: shared client builders from Tasks 1–2, `GUIDE_VARIANTS` from the existing beginner curriculum, and page metadata from Task 3.
- Produces: `buildGuidePage(pageId, baseUrl): ApiDocsGuideDefinition` and `ApiGuidePage` headings for Task 8.

- [ ] **Step 1: Write failing guide-content safety and consistency tests**

Import `GUIDE_VARIANTS` from `@/components/getting-started/curriculum`. Create tests that assert:

```ts
const quickstart = buildGuidePage('quickstart', 'https://gateway.example.com/v1/')
const clients = buildGuidePage('client-integration', 'https://gateway.example.com/v1/')
const capabilities = buildGuidePage('capabilities', 'https://gateway.example.com/v1/')
const errors = buildGuidePage('errors', 'https://gateway.example.com/v1/')
const macClaude = GUIDE_VARIANTS.find(({ client, os }) => client === 'claude_code' && os === 'macos')!

expect(JSON.stringify([quickstart, clients, capabilities, errors])).not.toMatch(
  /gemini|embedding|video|failover|scheduler|stability|\/v1\/usage|\/v1\/balance/i
)
expect(JSON.stringify(clients)).toContain('$LINX2_API_KEY')
expect(JSON.stringify(clients)).toContain('ANTHROPIC_BASE_URL')
expect(JSON.stringify(clients)).toContain('wire_api = "responses"')
expect(JSON.stringify(clients)).toContain('opencode.json')
expect(JSON.stringify(clients)).toContain('CC Switch')
expect(JSON.stringify(clients)).toContain(macClaude.installCommand!)
expect(JSON.stringify(errors)).toContain('API_KEY_REQUIRED')
expect(JSON.stringify(errors)).toContain('response.failed')
```

Mount `ApiGuidePage` and assert it renders heading IDs, paragraph/callout/code/table blocks, and exposes its headings.

- [ ] **Step 2: Run the guide tests and observe missing modules**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/guideContent.spec.ts src/components/api-docs/__tests__/ApiGuidePage.spec.ts
```

Expected: FAIL because the content builder and renderer do not exist.

- [ ] **Step 3: Define focused guide block types and build all approved guide pages**

Add these types to `types.ts`:

```ts
export type ApiDocsBlock =
  | { kind: 'paragraph'; textKey: string }
  | { kind: 'callout'; tone: 'info' | 'warning'; textKey: string }
  | { kind: 'code'; label: string; language: string; code: string }
  | { kind: 'table'; columns: string[]; rows: string[][] }
  | { kind: 'links'; links: Array<{ labelKey: string; to: string }> }

export interface ApiDocsGuideSection {
  id: string
  titleKey: string
  blocks: ApiDocsBlock[]
}

export interface ApiDocsGuideDefinition {
  pageId: ApiDocsPageId
  sections: ApiDocsGuideSection[]
}
```

`buildGuidePage` must implement these exact section IDs:

- Quickstart: `base-url`, `api-key`, `first-request`, `available-models`.
- Authentication: `bearer`, `x-api-key`, `key-safety`, `deprecated-query`.
- Client integration: `claude-code`, `codex-cli`, `opencode`, `cc-switch`, `python-sdk`.
- Capabilities: `streaming`, `tools`, `structured-output`, `reasoning`, `prompt-cache`.
- Errors: `gateway-envelope`, `gateway-codes`, `anthropic-envelope`, `openai-envelope`, `stream-errors`.
- Request ID: `headers`, `support-checklist`, `redaction`.
- Key security: `expiration`, `quota`, `rate-windows`, `ip-rules`.

Client integration reads client installation commands and official-source links from `GUIDE_VARIANTS`, then uses `buildClientConfigFiles` with platform `unified`, Base URL input, and `DOCS_API_KEY_PLACEHOLDER`; use macOS as the initial displayed variant and label Windows alternatives in localized prose. Append the three `buildPythonSdkExample` files. Never copy installation or configuration commands into locale strings.

Quickstart uses the Responses cURL from `buildEndpointExamples`, explains that a unified key can call the documented Anthropic and OpenAI-compatible endpoints, and uses the Models cURL for available models. Capabilities use bounded prose and small JSON request fragments; prompt caching mentions Anthropic `cache_control` with `5m` and `1h` TTL values but makes no provider-native claim about internal KIRO behavior. Key security names the existing `5h`, `1d`, and `7d` limit windows and distinguishes IP/CIDR whitelist and blacklist behavior.

The gateway error table contains the approved code/status pairs from the design. Error-envelope code blocks are exactly:

```json
{"code":"INVALID_API_KEY","message":"Invalid API key"}
```

```json
{"type":"error","error":{"type":"invalid_request_error","message":"model is required"}}
```

```json
{"error":{"type":"invalid_request_error","message":"model is required"}}
```

Streaming prose and examples distinguish Anthropic `event: error`, Responses `response.failed`, and Chat Completions stream errors after an HTTP `200` has started.

- [ ] **Step 4: Implement the generic guide renderer**

`ApiGuidePage.vue` accepts one `ApiDocsGuideDefinition`, resolves localization only for `textKey`, `titleKey`, and link labels, renders code using `ApiDocsCodeBlock`, uses semantic `<table>` for table blocks, and exposes:

```ts
const headings = computed(() => props.definition.sections.map((section) => ({
  id: section.id,
  label: t(section.titleKey)
})))

defineExpose({ headings })
```

- [ ] **Step 5: Run guide tests and shared-instruction regression tests**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/api-docs/__tests__/guideContent.spec.ts src/components/api-docs/__tests__/ApiGuidePage.spec.ts src/components/keys/__tests__/clientExampleFiles.spec.ts src/components/keys/__tests__/clientConfigFiles.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit the guide and platform content**

```bash
git add frontend/src/components/api-docs/guideContent.ts frontend/src/components/api-docs/ApiGuidePage.vue frontend/src/components/api-docs/types.ts frontend/src/components/api-docs/__tests__/guideContent.spec.ts frontend/src/components/api-docs/__tests__/ApiGuidePage.spec.ts
git commit -m "feat: add API docs guide content"
```

---

### Task 8: Public Documentation View and Constrained Routes

**Files:**
- Create: `frontend/src/views/public/ApiDocsView.vue`
- Create: `frontend/src/views/public/__tests__/ApiDocsView.spec.ts`
- Create: `frontend/src/router/__tests__/api-docs-route.spec.ts`
- Modify: `frontend/src/router/index.ts`

**Interfaces:**
- Consumes: shell/search/renderers/catalog/content/examples from Tasks 3–7.
- Produces: canonical `/docs` experience and route names `ApiDocs` and `ApiDocsPage`.

- [ ] **Step 1: Write failing route and view integration tests**

Import `readFile` from `node:fs/promises` and `resolve` from `node:path`, following the existing `getting-started-route.spec.ts` source-contract pattern. The route test must assert:

```ts
expect(router.getRoutes().find(({ name }) => name === 'ApiDocs')?.path).toBe('/docs')
expect(router.getRoutes().find(({ name }) => name === 'ApiDocsPage')?.path)
  .toBe('/docs/:section(guide|api-reference|platform)/:slug')

const docsRoutes = router.options.routes.filter(({ name }) =>
  name === 'ApiDocs' || name === 'ApiDocsPage'
)
expect(docsRoutes.every(({ meta }) => meta?.requiresAuth === false)).toBe(true)
const source = await readFile(resolve(process.cwd(), 'src/router/index.ts'), 'utf8')
const allowlist = source.match(/const BACKEND_MODE_ALLOWED_PATHS = \[(.*?)\]/s)?.[1]
expect(allowlist).toBeDefined()
expect(allowlist).not.toContain('/docs')
expect(router.resolve('/docs/batch-image').name).toBe('BatchImageGuide')
```

The view test mounts `/docs`, Responses, and an unknown docs path. Assert quickstart content, normalized configured Base URL, endpoint renderer selection, documentation-scoped not found, locale-preserving page title, and search navigation.

- [ ] **Step 2: Run the route/view tests and observe missing route and view**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/api-docs-route.spec.ts src/views/public/__tests__/ApiDocsView.spec.ts
```

Expected: FAIL because routes and view do not exist.

- [ ] **Step 3: Register constrained routes before user routes and the catch-all**

Add:

```ts
{
  path: '/docs',
  name: 'ApiDocs',
  component: () => import('@/views/public/ApiDocsView.vue'),
  meta: {
    requiresAuth: false,
    title: 'API Docs',
    titleKey: 'apiDocs.title'
  }
},
{
  path: '/docs/:section(guide|api-reference|platform)/:slug',
  name: 'ApiDocsPage',
  component: () => import('@/views/public/ApiDocsView.vue'),
  meta: {
    requiresAuth: false,
    title: 'API Docs',
    titleKey: 'apiDocs.title'
  }
},
```

Do not change `BACKEND_MODE_ALLOWED_PATHS`. Because the dynamic route requires one of three section values, `/docs/batch-image` remains available to its existing explicit alias.

- [ ] **Step 4: Compose the page from the current route and public settings**

In `ApiDocsView.vue`:

```ts
const baseUrl = computed(() => appStore.apiBaseUrl || window.location.origin)
const page = computed(() => findApiDocsPage(route.path))
const endpoint = computed(() => API_ENDPOINTS.find(({ pageId }) => page.value?.id === pageId))
const endpointExamples = computed(() => endpoint.value
  ? buildEndpointExamples(endpoint.value.id, baseUrl.value)
  : null)
const guide = computed(() => page.value && page.value.kind !== 'endpoint'
  ? buildGuidePage(page.value.id, baseUrl.value)
  : null)
```

Render endpoint pages with `ApiEndpointPage`, all other known pages with `ApiGuidePage`, and unknown pages with a docs-scoped not-found panel linking to `/docs` and opening search. Pass exposed headings into `ApiDocsShell` as localized labels. On page or locale change, set:

```ts
document.title = `${t(page.value?.titleKey ?? 'apiDocs.title')} - ${appStore.siteName || 'LINX2.AI'}`
```

Do not fetch keys. Load public settings through the existing app-store path only if not already loaded.

- [ ] **Step 5: Run route/view integration and title tests**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/api-docs-route.spec.ts src/views/public/__tests__/ApiDocsView.spec.ts src/router/__tests__/title.spec.ts
```

Expected: PASS; `/docs/batch-image` remains the existing guide and backend-only allowlist is unchanged.

- [ ] **Step 6: Commit the public route and view**

```bash
git add frontend/src/views/public/ApiDocsView.vue frontend/src/views/public/__tests__/ApiDocsView.spec.ts frontend/src/router/index.ts frontend/src/router/__tests__/api-docs-route.spec.ts
git commit -m "feat: add public API documentation routes"
```

---

### Task 9: Homepage and Shared Sidebar Discovery

**Files:**
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`

**Interfaces:**
- Consumes: route `/docs` and locale key `apiDocs.navLabel` from earlier tasks.
- Produces: permanent public-header and authenticated self-service discovery.

- [ ] **Step 1: Write failing navigation-discovery tests**

Add HomeView assertions:

```ts
const apiDocsLink = wrapper.get('[data-testid="api-docs-nav-link"]')
expect(apiDocsLink.attributes('href')).toBe('/docs')
expect(apiDocsLink.text()).toBe('API 文档')

appState.docUrl = 'https://docs.example.test/'
expect(wrapper.get('[data-testid="api-docs-nav-link"]').attributes('href')).toBe('/docs')
expect(wrapper.findAll('a[href="https://docs.example.test/"]').length).toBeGreaterThan(0)
```

Extend the three custom-home tests to assert `api-docs-nav-link` is absent.

Add AppSidebar assertions that `/docs` occurs exactly once, follows `/getting-started`, precedes `/keys`, is inherited by admin personal navigation without remapping, and remains visible for regular simple user, standard admin, and simple-mode admin.

- [ ] **Step 2: Run the two navigation test files and observe missing links**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: FAIL because the internal API Docs links are absent.

- [ ] **Step 3: Add the built-in homepage header entry without changing external docs**

In the built-in header navigation, immediately after Beginner Guide, add:

```vue
<router-link
  to="/docs"
  data-testid="api-docs-nav-link"
  class="transition-colors hover:text-linear-ink"
>
  {{ t('apiDocs.navLabel') }}
</router-link>
```

Do not change any existing `v-if="docUrl"`, external link target, footer link, sanitization, CTA, or the early custom-home branches.

- [ ] **Step 4: Add the shared sidebar entry and preserve it in simple mode**

Add an `ApiDocsIcon` using the existing inline icon-component pattern. In `buildSelfNavItems`, order the entries:

```ts
{ path: '/getting-started', label: t('gettingStarted.dashboard.sidebarLabel'), icon: BeginnerGuideIcon },
{ path: '/docs', label: t('apiDocs.navLabel'), icon: ApiDocsIcon },
{ path: '/keys', label: t('nav.apiKeys'), icon: KeyIcon },
```

Neither guide nor docs receives `hideInSimpleMode`. Update `visiblePersonalNavItems` simple-mode filter to:

```ts
personalNavItems.value.filter((item) =>
  item.path === '/getting-started' || item.path === '/docs'
)
```

- [ ] **Step 5: Run navigation and URL-sanitization tests**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/components/layout/__tests__/docUrlSanitization.spec.ts
```

Expected: PASS; internal and configured external docs links coexist.

- [ ] **Step 6: Commit product discovery**

```bash
git add frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts
git commit -m "feat: expose API docs navigation"
```

---

### Task 10: Cross-Feature Regression, Build, and Graph Refresh

**Files:**
- Modify only if a verification failure reveals an in-scope defect: files already listed in Tasks 1–9.
- Update: `graphify-out/` generated graph artifacts.

**Interfaces:**
- Consumes: all earlier task deliverables.
- Produces: verified branch ready for independent review.

- [ ] **Step 1: Run the complete focused documentation and shared-instruction suite**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run \
  src/components/api-docs/__tests__ \
  src/views/public/__tests__/ApiDocsView.spec.ts \
  src/router/__tests__/api-docs-route.spec.ts \
  src/components/keys/__tests__/clientExampleFiles.spec.ts \
  src/components/keys/__tests__/clientConfigFiles.spec.ts \
  src/components/keys/__tests__/UseKeyModal.spec.ts \
  src/views/public/__tests__/GettingStartedView.spec.ts \
  src/views/__tests__/HomeView.spec.ts \
  src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: all selected files PASS; no snapshots are updated blindly.

- [ ] **Step 2: Run frontend lint checking and type checking**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend lint:check
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend typecheck
```

Expected: both commands exit 0 with no errors.

- [ ] **Step 3: Run the complete frontend test suite**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend test:run
```

Expected: the full Vitest suite exits 0. Existing tests may emit their established warning output, but there are no failed tests.

- [ ] **Step 4: Build the production frontend**

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend build
```

Expected: `vue-tsc -b` and Vite build both exit 0.

- [ ] **Step 5: Run the complete backend regression suite**

Run:

```bash
go test ./...
```

Expected: every Go package exits 0. No backend gateway behavior was changed.

- [ ] **Step 6: Refresh the repository knowledge graph**

Run:

```bash
graphify update .
```

Expected: graph update completes successfully and captures new API-doc routes, components, imports, and tests.

- [ ] **Step 7: Inspect the final diff for scope, secrets, and excluded claims**

Run:

```bash
git diff --check
git status --short
rg -n "AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----" frontend docs graphify-out && exit 1 || true
rg -n "Gemini|/v1beta|Embedding|/v1/usage|/v1/balance|failover|scheduler|稳定性" frontend/src/components/api-docs frontend/src/i18n/locales/zh/apiDocs.ts frontend/src/i18n/locales/en/apiDocs.ts
```

Expected:

- `git diff --check` exits 0.
- No production credential match is printed.
- The excluded-term scan prints no API-doc content matches. If a test contains an exclusion assertion, inspect it manually and keep only the assertion.
- Dirty files are limited to planned implementation files and generated `graphify-out/` artifacts.

- [ ] **Step 8: Commit the graph refresh or any verified cleanup**

If only generated graph artifacts changed:

```bash
git add graphify-out
git commit -m "chore: refresh API docs knowledge graph"
```

If verification required in-scope code cleanup, commit that cleanup first with a focused message and rerun the failed command before committing graph artifacts.

- [ ] **Step 9: Record final evidence for review**

Run:

```bash
git status --short --branch
git log --oneline --decorate -12
```

Expected: `feat/api-docs` is clean and the log shows the design, plan, incremental feature commits, and graph refresh without unrelated changes.

---

## Final Review Checklist

- [ ] `/docs` is public in normal mode and respects backend-only mode.
- [ ] `/docs/batch-image` still resolves to the existing authenticated guide.
- [ ] Homepage, regular user, administrator personal navigation, and simple mode expose one internal API Docs entry.
- [ ] External `doc_url` and custom homepage behavior are unchanged.
- [ ] The classic desktop layout has left navigation, center content, and right TOC; tablet/mobile adaptations are usable.
- [ ] Search is localized, keyboard accessible, and contains no excluded capabilities.
- [ ] Exactly seven approved endpoints appear.
- [ ] Gemini, Embeddings, video, alpha search, internal aliases, stability features, `/v1/usage`, and `/v1/balance` do not appear as documented features.
- [ ] Client examples are generated by shared pure modules and use `$LINX2_API_KEY`.
- [ ] Base URL and `/v1` joining are consistent with the key modal and beginner guide.
- [ ] Gateway, Anthropic, OpenAI, and streaming errors remain distinct.
- [ ] Full frontend tests, type checking, build, backend tests, and graph update pass.
