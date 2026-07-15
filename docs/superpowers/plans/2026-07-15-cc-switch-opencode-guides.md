# CC Switch and OpenCode Guide Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add official OpenCode and CC Switch configuration guidance to “使用密钥” and the beginner guide, with the requested client ordering.

**Architecture:** Reuse one `buildClientConfigFiles` boundary for Claude Code, Codex CLI, OpenCode, and CC Switch so both UI entry points generate the same data. Extend the existing version-1 beginner-guide client enum end-to-end, and make desktop-only guide commands optional for CC Switch rather than inventing shell commands.

**Tech Stack:** Vue 3, TypeScript, Pinia, Vitest, Go, Gin

## Global Constraints

- Primary client order is Claude Code, Codex CLI, OpenCode, CC Switch, WorkBuddy.
- CC Switch configuration follows the official app UI; do not edit its database or fabricate a CLI.
- OpenCode uses the official `provider.options.baseURL` and `provider.options.apiKey` schema.
- Do not download installers during verification.
- Preserve SDK tabs and existing client behavior.

---

### Task 1: Shared configuration generators and key modal

**Files:**
- Modify: `frontend/src/components/keys/clientConfigFiles.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue`
- Modify: `frontend/src/i18n/locales/zh/dashboard.ts`
- Modify: `frontend/src/i18n/locales/en/dashboard.ts`
- Test: `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts`
- Test: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`

**Interfaces:**
- Consumes: `ClientConfigInput`, key platform, API key, base URL, OS.
- Produces: `SupportedGuideClient = 'claude_code' | 'codex' | 'opencode' | 'cc_switch'` and `buildClientConfigFiles(input): ClientConfigFile[]` for all four clients.

- [x] **Step 1: Write failing generator tests**

Add tests asserting OpenCode returns the official provider schema and OS-specific path, CC Switch returns Claude/Codex copyable field lists for compatible platforms, and unified CC Switch returns both alternatives.

- [x] **Step 2: Run the generator test and verify RED**

Run: `COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm test:run src/components/keys/__tests__/clientConfigFiles.spec.ts`

Expected: failures because `opencode` and `cc_switch` are not accepted by `SupportedGuideClient` and have no shared implementation.

- [x] **Step 3: Implement minimal shared generation**

Extend the client union, add OpenCode JSON generation and CC Switch field-list generation, preserving the existing Claude/Codex branches.

- [x] **Step 4: Run the generator test and verify GREEN**

Run the command from Step 2. Expected: all generator tests pass.

- [x] **Step 5: Write failing modal tests**

Assert the primary tab order, the CC Switch tab and instructions, and that WorkBuddy follows CC Switch while SDK tabs remain present.

- [x] **Step 6: Run the modal test and verify RED**

Run: `COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm test:run src/components/keys/__tests__/UseKeyModal.spec.ts`

Expected: failures because the tab and localized copy do not exist and OpenCode is ordered after WorkBuddy.

- [x] **Step 7: Implement the modal and translations**

Add `cc_switch` tab handling, call the shared generator for OpenCode/CC Switch, order primary tabs as requested, and add concise Chinese/English field-entry instructions.

- [x] **Step 8: Run modal and generator tests**

Run both Task 1 test files. Expected: all pass.

### Task 2: Beginner-guide contracts, curriculum, and persistence

**Files:**
- Modify: `frontend/src/api/beginnerGuide.ts`
- Modify: `frontend/src/stores/beginnerGuide.ts`
- Modify: `frontend/src/components/getting-started/curriculum.ts`
- Modify: `frontend/src/i18n/locales/zh/gettingStarted.ts`
- Modify: `frontend/src/i18n/locales/en/gettingStarted.ts`
- Modify: `backend/internal/service/user_beginner_guide.go`
- Test: `frontend/src/components/getting-started/__tests__/curriculum.spec.ts`
- Test: `frontend/src/stores/__tests__/beginnerGuide.spec.ts`
- Test: `backend/internal/service/user_beginner_guide_test.go`

**Interfaces:**
- Consumes: existing `BeginnerGuideProgressV1` wire format.
- Produces: accepted client IDs `claude_code`, `codex`, `opencode`, `cc_switch` in frontend and backend, plus twelve new OS variants.

- [x] **Step 1: Write failing frontend contract/curriculum tests**

Assert four client IDs, twelve total client/OS combinations, official OpenCode install/download metadata, CC Switch official release links, and absence of fabricated CC Switch CLI commands.

- [x] **Step 2: Run frontend contract tests and verify RED**

Run: `COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm test:run src/components/getting-started/__tests__/curriculum.spec.ts src/stores/__tests__/beginnerGuide.spec.ts`

Expected: failures because the two IDs and variants are rejected or missing.

- [x] **Step 3: Write failing backend validation tests**

Extend the valid-client test table with `opencode` and `cc_switch`.

- [x] **Step 4: Run backend service test and verify RED**

Run: `go test ./internal/service -run 'TestValidateBeginnerGuideProgress' -count=1`

Expected: the two new client cases return `BEGINNER_GUIDE_PROGRESS_INVALID`.

- [x] **Step 5: Implement contract and curriculum changes**

Extend the TypeScript union, Pinia allowlist, Go validation, curriculum variants, and client labels. Make guide commands optional only where CC Switch is desktop-only.

- [x] **Step 6: Run frontend and backend tests and verify GREEN**

Run the commands from Steps 2 and 4. Expected: all selected tests pass.

### Task 3: Beginner-guide rendering and configuration reuse

**Files:**
- Modify: `frontend/src/views/public/GettingStartedView.vue`
- Modify: `frontend/src/components/getting-started/GuideApiKeyStep.vue`
- Modify: `frontend/src/components/getting-started/GuideTroubleshooting.vue`
- Test: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`
- Test: `frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts`
- Test: `frontend/src/components/getting-started/__tests__/GuideTroubleshooting.spec.ts`

**Interfaces:**
- Consumes: optional curriculum commands and shared `buildClientConfigFiles` output.
- Produces: usable desktop-only CC Switch install/first-run panels and reusable OpenCode/CC Switch configuration blocks.

- [x] **Step 1: Write failing component tests**

Assert OpenCode/CC Switch key compatibility, generated configuration blocks, CC Switch release link, hidden absent command blocks, and client-specific troubleshooting paths.

- [x] **Step 2: Run component tests and verify RED**

Run: `COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm test:run src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/components/getting-started/__tests__/GuideTroubleshooting.spec.ts`

Expected: failures because the components assume only Claude/Codex and all variants have shell commands.

- [x] **Step 3: Implement minimal rendering changes**

Pass the selected client through the shared generator, broaden compatibility for OpenCode/CC Switch, conditionally render optional commands, and map troubleshooting locations per client.

- [x] **Step 4: Run component tests and verify GREEN**

Run the command from Step 2. Expected: all selected component tests pass.

### Task 4: Verification and graph refresh

**Files:**
- Update generated graph files under `graphify-out/` using the repository-required command.

**Interfaces:**
- Consumes: all implementation tasks.
- Produces: verified build and updated repository knowledge graph.

- [x] **Step 1: Run focused frontend regression tests**

Run all key-modal, config-generator, beginner-guide store/curriculum/component/view test files. Expected: zero failures.

- [x] **Step 2: Run frontend typecheck and build**

Run: `COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm typecheck && COREPACK_ENABLE_PROJECT_SPEC=0 corepack pnpm build`

Expected: both commands exit 0.

- [x] **Step 3: Run focused backend tests**

Run: `go test ./internal/service ./internal/handler -count=1`

Expected: both packages pass.

- [x] **Step 4: Refresh Graphify**

Run: `graphify update .`

Expected: graph update completes without an error.

- [x] **Step 5: Review scope**

Inspect `git diff --check`, `git status --short`, and `git diff --stat`; confirm no installer was downloaded and no unrelated file was changed.
