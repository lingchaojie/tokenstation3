# GPT Image 2 Python SDK Streaming Example Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the existing synchronous, incorrectly named image example with a copy-pasteable GPT Image 2 Python SDK streaming example that writes partial previews and a final PNG.

**Architecture:** Reuse the dedicated OpenAI image SDK tab and its existing generator. Change only the generated Python content, the visible bilingual label, and focused component contract tests; the backend streaming gateway remains untouched.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vue Test Utils, Vitest, vue-i18n, OpenAI Python SDK.

## Global Constraints

- Keep the internal tab ID `openai-imagen2-python-sdk`, locale key `openaiImagen2PythonSdk`, and generated filename `imagen2_client.py` unchanged.
- Use the public model name `gpt-image-2`; do not generate `model="imagen-2"`.
- Use the dedicated Images API with `stream=True` and `partial_images=2`.
- Decode `b64_json`, write partial previews as `partial_<index>.png`, and write the completion event as `image.png`.
- Leave the general OpenAI Responses text-streaming example unchanged.
- Do not change backend routes, model routing, pricing, or account behavior.
- Stage and commit only files named by this task; preserve unrelated workspace changes.

---

### Task 1: Stream GPT Image 2 Output in the Dedicated Python SDK Tab

**Files:**
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts:1-315`
- Modify: `frontend/src/components/keys/UseKeyModal.vue:738-754`
- Modify: `frontend/src/i18n/locales/zh/dashboard.ts:190-196`
- Modify: `frontend/src/i18n/locales/en/dashboard.ts:189-195`

**Interfaces:**
- Consumes: `generateOpenAIImagen2PythonSdkFile(baseUrl: string, apiKey: string): FileConfig` and the `openaiImagen2PythonSdk` locale key.
- Produces: `imagen2_client.py`, a synchronous Python script that consumes `Stream[ImageGenStreamEvent]` from `client.images.generate(...)` and writes image files.

- [ ] **Step 1: Import the real locale objects in the focused component test**

After the `UseKeyModal` import, ensure these imports exist:

```ts
import zhDashboard from '@/i18n/locales/zh/dashboard'
import enDashboard from '@/i18n/locales/en/dashboard'
```

If the earlier Codex guidance task already added them, do not duplicate them.

- [ ] **Step 2: Replace the non-streaming image-example assertions with the streaming contract**

Replace the body of `renders OpenAI Imagen 2 Python SDK image generation config` after locating and selecting the tab with these assertions, and rename the test to `renders a streaming GPT Image 2 Python SDK example`:

```ts
const codeBlock = wrapper.find('pre code')
expect(codeBlock.exists()).toBe(true)
expect(codeBlock.text()).toContain('from base64 import b64decode')
expect(codeBlock.text()).toContain('from pathlib import Path')
expect(codeBlock.text()).toContain('from openai import OpenAI')
expect(codeBlock.text()).toContain('api_key="sk-test"')
expect(codeBlock.text()).toContain('base_url="https://example.com/v1"')
expect(codeBlock.text()).toContain('stream = client.images.generate(')
expect(codeBlock.text()).toContain('model="gpt-image-2"')
expect(codeBlock.text()).not.toContain('model="imagen-2"')
expect(codeBlock.text()).toContain('prompt="A fox mascot using an AI gateway"')
expect(codeBlock.text()).toContain('stream=True')
expect(codeBlock.text()).toContain('partial_images=2')
expect(codeBlock.text()).toContain('event.type == "image_generation.partial_image"')
expect(codeBlock.text()).toContain('event.type == "image_generation.completed"')
expect(codeBlock.text()).toContain('Path(f"partial_{event.partial_image_index}.png")')
expect(codeBlock.text()).toContain('Path("image.png")')
expect(codeBlock.text()).toContain('output_path.write_bytes(b64decode(image_b64))')
```

Add a second test immediately after it:

```ts
it('labels the image SDK tab as GPT Image 2 in Chinese and English', () => {
  expect(zhDashboard.keys.useKeyModal.cliTabs.openaiImagen2PythonSdk).toBe(
    'GPT Image 2 Python SDK'
  )
  expect(enDashboard.keys.useKeyModal.cliTabs.openaiImagen2PythonSdk).toBe(
    'GPT Image 2 Python SDK'
  )
})
```

- [ ] **Step 3: Run the focused tests and verify the new contract fails**

Run from the worktree root:

```bash
cd frontend
/home/alvin/tokenstation3/frontend/node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: FAIL because the generated code still uses `image = client.images.generate(...)`, `model="imagen-2"`, and neither locale label says `GPT Image 2 Python SDK`.

- [ ] **Step 4: Replace the generated Python code with the minimal streaming implementation**

Replace `generateOpenAIImagen2PythonSdkFile` with:

```ts
function generateOpenAIImagen2PythonSdkFile(baseUrl: string, apiKey: string): FileConfig {
  return {
    path: 'imagen2_client.py',
    content: `from base64 import b64decode
from pathlib import Path

from openai import OpenAI

client = OpenAI(
    api_key="${apiKey}",
    base_url="${baseUrl}",
)

stream = client.images.generate(
    model="gpt-image-2",
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

- [ ] **Step 5: Update the bilingual visible label**

Set the Chinese locale value to:

```ts
openaiImagen2PythonSdk: 'GPT Image 2 Python SDK',
```

Set the English locale value to:

```ts
openaiImagen2PythonSdk: 'GPT Image 2 Python SDK',
```

- [ ] **Step 6: Run the focused tests and verify they pass**

Run:

```bash
cd frontend
/home/alvin/tokenstation3/frontend/node_modules/.bin/vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
```

Expected: all focused tests PASS, including the streaming generated-code contract and bilingual label assertions.

- [ ] **Step 7: Run frontend static validation**

Run:

```bash
cd frontend
/home/alvin/tokenstation3/frontend/node_modules/.bin/vue-tsc --noEmit
```

Expected: exit code 0 with no TypeScript or Vue template errors.

- [ ] **Step 8: Check the patch and commit**

Run:

```bash
git diff --check
git diff -- frontend/src/components/keys/UseKeyModal.vue \
  frontend/src/components/keys/__tests__/UseKeyModal.spec.ts \
  frontend/src/i18n/locales/zh/dashboard.ts \
  frontend/src/i18n/locales/en/dashboard.ts
git add frontend/src/components/keys/UseKeyModal.vue \
  frontend/src/components/keys/__tests__/UseKeyModal.spec.ts \
  frontend/src/i18n/locales/zh/dashboard.ts \
  frontend/src/i18n/locales/en/dashboard.ts
git commit -m "feat(keys): stream GPT Image 2 SDK example"
```

Expected: one task-scoped commit containing only the component, focused test, and two locale files.
