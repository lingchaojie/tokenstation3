# Beginner Getting Started Guide Design

**Date:** 2026-07-15

**Status:** Approved

**Branch:** `feat/beginner-getting-started`

**Base:** local `dev` at `4de6b00d5`

## Summary

Add a beginner-first, bilingual getting-started experience for people who may not know what an AI model, AI agent, terminal, API key, Claude Code, or Codex is.

The feature has two discovery layers leading to one public guide:

- The public `/home` page introduces the guide immediately after the hero and before technical gateway content.
- The authenticated `/dashboard` shows a one-time welcome prompt to eligible new users and retains permanent navigation back to the guide.
- A single public `/getting-started` route provides the detailed guided experience. Anonymous users can complete the explanatory and installation steps. Authentication is required only when the guide needs to create or select an API key.

The first release supports exactly two clients, Claude Code and Codex, across macOS, Windows, and Linux. It does not mention unsupported or future clients.

## Current Product Context

The existing product has two pages that are easy to conflate:

- `/home` is the public marketing landing page implemented by `frontend/src/views/HomeView.vue`.
- Successful user login defaults to `/dashboard`, implemented through `DashboardView.vue` and `UserDashboardContent.vue`.

The current public homepage assumes prior AI knowledge. Its hero and sections immediately discuss gateways, Claude Code, Codex, routes, models, and pricing.

The current authenticated dashboard is usage-oriented. It shows balance and usage statistics, charts, recent usage, and quick actions. It does not explain how a new user moves from an account to a working local client.

The project already has valuable configuration behavior in `UseKeyModal.vue`:

- Claude Code and Codex client tabs.
- macOS/Linux and Windows variants.
- Base URL normalization.
- API-key-aware configuration files and commands.
- Copyable code blocks and localized notes.

The guide must reuse this configuration behavior rather than maintaining a second implementation.

## Goals

1. Help a user with no AI-tool background reach a successful first Claude Code or Codex request.
2. Make the guide easy to discover from both the public homepage and the logged-in product.
3. Keep the detailed instructions out of the homepage itself.
4. Allow anonymous users to read and begin the guide without creating an account first.
5. Let authenticated users create or select an API key without leaving the guide.
6. Persist welcome-prompt suppression across devices.
7. Keep tutorial progress resumable without persisting API keys or other secrets.
8. Support Chinese and English through the existing locale switcher.
9. Keep Claude Code/Codex configuration output consistent with the existing API-key experience.

## Non-Goals

- Supporting OpenCode, WorkBuddy, Gemini CLI, or any other client in the first release.
- Showing “coming soon” cards for other clients.
- Executing commands on the user's computer.
- Remotely controlling installation or configuration.
- Shipping a video course.
- Replacing the existing admin onboarding tour.
- Replacing the general documentation URL or existing documentation links.
- Automatically validating files on the user's local filesystem.

## Chosen Approach

Use an explicit dual-entry strategy and a hybrid task-workspace guide.

This was selected over two alternatives:

1. A lightweight CTA-only approach was rejected because users with no AI knowledge can easily overlook a secondary hero button or a normal quick action.
2. A help-center/floating-button approach was rejected because it is reactive support, not proactive onboarding.
3. A full-screen, one-step-only wizard was rejected because the guide is long enough that users need a visible directory, backtracking, OS switching, and troubleshooting branches.
4. A documentation-style long page was rejected because its density makes the starting point unclear to beginners.

The selected hybrid layout combines a clear next action with a persistent progress directory and optional deeper explanations.

## Information Architecture

### Public homepage entry

For the built-in homepage only:

- Add “Beginner Guide” / “新手教程” to the desktop header navigation.
- Add a full-width beginner card after the primary hero CTA and before the authenticated chat demo and technical gateway/product-console presentation.
- Keep the card concise: reassurance, a four-part overview, and one primary CTA.
- Link both the header item and card CTA to `/getting-started`.

Recommended Chinese card copy:

- Eyebrow: `第一次使用 AI 工具？`
- Title: `完全不懂也没关系，我们一步一步带你完成`
- Description: `选择工具、安装客户端、连接本站，再完成你的第一次 AI 任务。`
- CTA: `开始新手教程`

Recommended English card copy:

- Eyebrow: `New to AI tools?`
- Title: `No prior knowledge needed. We will guide you step by step.`
- Description: `Choose a tool, install it, connect this service, and complete your first AI task.`
- CTA: `Start beginner guide`

If the administrator has configured `home_content`, the existing full-page override remains authoritative. The built-in beginner card is not injected into custom HTML or iframe content.

### Authenticated dashboard entry

The dashboard has three related behaviors:

1. Eligible new users see `BeginnerWelcomeDialog` automatically on their first dashboard visit.
2. The shared self-service sidebar navigation permanently contains “Beginner Guide” / “新手教程”, so it appears for normal users and in the administrator's personal-account navigation.
3. `UserDashboardQuickActions` permanently contains a normal guide entry.

The dialog is non-blocking and contains:

- A short explanation that no AI knowledge is required.
- A primary “Start guide” action.
- A visible close action.

Clicking either “Start guide” or close suppresses future automatic display. The persistent sidebar and quick-action entries remain available.

Existing users at rollout are suppressed by migration and are not surprised by a new automatic dialog.

### Guide route

Add a public route:

```text
/getting-started
```

Route properties:

- `requiresAuth: false`
- Available in normal public mode.
- In backend-only mode, preserve the existing allowlist: unauthenticated users are redirected to login and `/getting-started` becomes available after authentication. Normal public mode keeps the route anonymous.
- Uses a dedicated public guide shell rather than `AppLayout`, because anonymous users do not have an authenticated sidebar.
- Shows authenticated account actions when a session exists.

The route uses one canonical path. Client, OS, and progress are application state, not separate content routes.

## Guide Layout

Desktop layout:

- Top bar: brand, guide title, language switcher, theme control, account/dashboard action.
- Left column: progress, stable step names, completed markers, and permitted step navigation.
- Right column: one active task, explanations, command blocks, expected results, troubleshooting, and next/back actions.

Mobile layout:

- The left progress column becomes a compact progress header and slide-over step list.
- The primary next action remains visible after the active content.
- Code blocks scroll horizontally without widening the viewport.

Top-level selectors:

- Client: `Claude Code` or `Codex` only.
- OS: `macOS`, `Windows`, or `Linux` only.
- Browser detection may suggest an OS but never locks the selection.

The selected client and OS update the relevant installation, configuration, verification, and troubleshooting content. Switching selection preserves the completed conceptual introduction and selection steps, then invalidates completion for terminal, installation, configuration, first-run, and troubleshooting steps because those results are client/OS-specific.

## Guide Journey

### Step 1: Understand the tool

Explain, in beginner language:

- An AI model can understand instructions and produce text or code.
- An AI agent is a local tool that can use a model while working with the user's project.
- Claude Code and Codex are terminal-based AI coding agents.
- The gateway account and API key let the selected client use this service.

Avoid model architecture, provider internals, routing terminology, and unexplained abbreviations.

### Step 2: Choose client and operating system

Ask the user to choose Claude Code or Codex and confirm macOS, Windows, or Linux. Explain that the guide can be switched later without losing completed conceptual steps.

### Step 3: Open a terminal and check prerequisites

Show:

- Where to find the terminal application on the selected OS.
- How to paste and run one command.
- What normal command output looks like.
- The supported prerequisite checks for the selected client.
- A branch for installing missing prerequisites.

PowerShell is the default beginner shell on Windows unless the implementation-time official client documentation requires a different primary path. Any WSL-specific path must be clearly labeled and used only when the official support requirements make it necessary.

### Step 4: Install the client

For the chosen client and OS:

- Present one action at a time.
- Provide one-click copy.
- Explain what the command does in a collapsed beginner note.
- Show expected success output.
- Provide “I ran into a problem” branches for command-not-found, permissions, network, version, and shell-restart issues.

Installation commands must be verified against official Claude Code and official OpenAI Codex documentation during implementation. Each client content definition records its official source URL and last verification date.

#### Minimal platform-specific installation presentation

Keep the existing client and operating-system selectors as the only choices. Do not add an installation-mode selector or a generic installation-options framework.

- macOS and Linux show one copyable official CLI installation command for the selected client.
- Windows shows the selected client's official desktop-app download link as the primary action and the official CLI installation command as a secondary fallback.
- Claude Code Windows uses Anthropic's official x64 desktop installer redirect and the official PowerShell native-install command.
- Codex Windows uses OpenAI's official Microsoft installer link and `npm install -g @openai/codex` as the CLI fallback.
- A warning at the top of the guide says `操作可能需要魔法梯子。` in Chinese, with an equivalent English translation.
- Tests assert the rendered commands, links, and warning only. They must never execute an installer or download a client binary.

### Step 5: Create or select an API key

Anonymous users encounter an authentication checkpoint here, not at route entry.

- Login and registration preserve the anonymous guide state.
- Successful authentication returns directly to `/getting-started` and restores this step.
- Authentication return state contains no API key.

Authenticated users see:

- Existing active compatible API keys.
- A minimal inline create-key action.
- Any required group/type selection already enforced by the existing key APIs.
- A clear warning that the key is a secret.

The guide uses the existing key list/create APIs and their validation rules. It does not create a parallel backend key-creation contract.

### Step 6: Connect the client to this gateway

Use the selected key, the configured public API base URL, selected client, and selected OS to generate exact configuration files or commands.

- Load the selected key only when this step is active.
- Do not encode it into the route, analytics, progress, or error logs.
- Provide file path, copy button, safe merge warning, and restart instruction.
- Show why both files are required where a client needs multiple files.
- Use `window.location.origin` only when the public settings do not provide `api_base_url`.

### Step 7: Restart and run the first task

Explain how to close and reopen the client, then give a harmless first prompt. Show the expected categories of success rather than promising an exact model response.

The guide does not claim to observe the local process. The user confirms the result manually.

### Step 8: Verify and troubleshoot

Provide a checklist for:

- Client version.
- Configuration-file location.
- Base URL.
- API-key selection and active state.
- Restarting the terminal/client.
- Authentication failures.
- Connection failures.
- Unsupported shell or permissions errors.

On success, mark the guide completed and point to the dashboard, API-key management, usage, and the selected client guide entry.

## Progress and Authentication Flow

### Anonymous progress

Persist only non-secret guide state in browser storage under a versioned key:

```ts
interface BeginnerGuideProgressV1 {
  version: 1
  client: 'claude_code' | 'codex'
  os: 'macos' | 'windows' | 'linux'
  currentStep: BeginnerGuideStepId
  completedSteps: BeginnerGuideStepId[]
}
```

The stable step IDs are:

```text
understand
choose
terminal
install
api_key
configure
first_run
troubleshoot
```

Do not include an API-key ID, API-key value, email address, or command containing a key.

### Authenticated progress

Authenticated progress is account-scoped and stored by the backend. This allows cross-device resume while keeping the welcome-prompt policy authoritative.

When an anonymous user authenticates from the guide:

1. Fetch account guide state.
2. Merge completed steps by stable step ID.
3. Prefer the anonymous client, OS, and current step because they represent the active flow that triggered authentication.
4. Change an `eligible` prompt state to `suppressed`, because this user has already started the guide through the public entry and should not later receive the dashboard prompt.
5. Save the merged non-secret state to the account.
6. Remove the anonymous browser copy after a successful save.

If the save fails, keep the anonymous copy and allow retry. Never block API-key creation solely because progress persistence failed.

## Backend State Model

Add user-owned guide fields to the Ent `User` schema:

- `beginner_guide_prompt_state`: string enum `eligible | suppressed | completed`.
- `beginner_guide_progress`: optional JSON object matching the versioned progress contract.
- `beginner_guide_completed_at`: optional timestamp.

Semantics:

- New users default to `eligible`.
- Existing users are backfilled to `suppressed` during rollout.
- Starting or closing the automatic dialog changes `eligible` to `suppressed` immediately.
- Completing the guide changes the state to `completed` and records the timestamp.
- A client cannot change `suppressed` or `completed` back to `eligible` through the public user API.

The database migration must explicitly distinguish existing rows from future inserts. It must not rely on a new-column default that would make every existing account eligible.

## Backend API

Add authenticated user routes under the existing `/api/v1/user` group:

```text
GET   /api/v1/user/beginner-guide
PATCH /api/v1/user/beginner-guide
```

GET response:

```json
{
  "prompt_state": "eligible",
  "progress": null,
  "completed_at": null
}
```

PATCH accepts only:

- `prompt_state: "suppressed" | "completed"`
- A valid versioned non-secret `progress` object.

Validation rules:

- Reject unknown prompt states, clients, OS values, step IDs, and progress versions.
- Bound `completed_steps` to the known curriculum size.
- Ignore ownership input; always update the authenticated subject.
- Reject unexpected secret-looking fields rather than storing arbitrary JSON.
- Marking completed sets `completed_at` server-side.
- Repeated identical updates are idempotent.

The dashboard fetches this small endpoint when deciding whether to show the automatic dialog. A transient failure defaults to no automatic popup, preventing an outage from repeatedly interrupting users. Permanent entries remain visible.

If suppressing the prompt fails, the current browser still hides the dialog and retains a small local retry marker. The client retries on the next authenticated session and shows a non-blocking persistence warning; account-wide suppression is considered confirmed only after the server accepts the update.

## Frontend Architecture

Suggested component and module boundaries:

```text
frontend/src/views/public/GettingStartedView.vue
frontend/src/components/getting-started/BeginnerGuideCard.vue
frontend/src/components/getting-started/BeginnerWelcomeDialog.vue
frontend/src/components/getting-started/GuideShell.vue
frontend/src/components/getting-started/GuideProgressNav.vue
frontend/src/components/getting-started/GuideStepPanel.vue
frontend/src/components/getting-started/GuideCommandBlock.vue
frontend/src/components/getting-started/GuideApiKeyStep.vue
frontend/src/components/getting-started/GuideTroubleshooting.vue
frontend/src/components/getting-started/curriculum.ts
frontend/src/components/keys/clientConfigFiles.ts
frontend/src/api/beginnerGuide.ts
```

Responsibilities:

- `GettingStartedView` coordinates route state, session state, progress, and the active curriculum.
- `curriculum.ts` declares stable step IDs, supported client/OS combinations, official source metadata, and references to locale keys. It does not contain API keys.
- Presentation components render one concern and do not fetch unrelated dashboard data.
- `GuideApiKeyStep` is the only guide component that lists or creates keys.
- `clientConfigFiles.ts` is a pure module extracted from `UseKeyModal.vue`.

Avoid turning `GettingStartedView.vue` into another thousand-line page. Content definitions, configuration generation, API-key interaction, and presentation belong in separate focused units.

## Shared Client Configuration

Extract the configuration generation from `UseKeyModal.vue` into a pure module with an interface equivalent to:

```ts
type SupportedGuideClient = 'claude_code' | 'codex'
type SupportedGuideOS = 'macos' | 'windows' | 'linux'

interface ClientConfigInput {
  client: SupportedGuideClient
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  apiKey: string
  baseUrl: string
  allowMessagesDispatch?: boolean
}

interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
}

function buildClientConfigFiles(input: ClientConfigInput): ClientConfigFile[]
```

`UseKeyModal.vue` and the guide both consume this module. The existing modal may continue to support more clients than the guide; the extraction must preserve all existing behavior while exposing only Claude Code and Codex in the beginner curriculum.

## Localization and Content Ownership

Add Chinese and English locale modules for:

- Homepage entry copy.
- Dashboard dialog and quick action.
- Guide chrome and progress.
- Concept explanations.
- OS/client-specific installation steps.
- API-key warnings.
- Verification and troubleshooting.

Stable step IDs are language-independent. Switching language does not reset progress.

Commands, file paths, model/client names, and environment-variable names are structured content, not translated prose. Explanations and labels use i18n keys.

Official-source metadata is maintained alongside the curriculum. Updating an installation command requires updating its verification date and affected tests.

## Security and Privacy

- Never put an API key in a URL, route query, local storage, session storage, progress JSON, analytics event, console log, or error message.
- Fetch keys only for authenticated users and only when the API-key/configuration steps need them.
- Do not persist generated configuration blocks because they contain the key.
- Clear generated configuration from component state when the selected key changes, the user logs out, or the view unmounts.
- Keep all configuration rendering escaped by default; syntax highlighting must not introduce unsafe HTML.
- Reuse existing URL sanitation and public-settings handling for documentation and base URLs.
- The progress API accepts a strict DTO, not arbitrary user JSON.

## Error and Recovery Behavior

- Public settings unavailable: use current origin for display and configuration fallback, and show a retryable warning when appropriate.
- Progress GET unavailable: render the guide; suppress automatic dashboard popup; retry on explicit user action.
- Progress PATCH unavailable: preserve local non-secret progress and allow the guide to continue.
- Key list unavailable: remain on the key step with retry and a link to `/keys` as a fallback.
- Key creation unavailable: preserve form selections and show the existing translated API error.
- Clipboard unavailable: show selectable text and manual-copy instructions.
- Login cancelled: return to the guide without losing anonymous progress.
- Unsupported or outdated client instructions: direct the user to the recorded official source and avoid pretending the local installation was verified.

## Accessibility and Responsive Requirements

- Every step and selector is keyboard accessible.
- Progress state is not communicated by color alone.
- Dialog focus is trapped while open and restored after closing.
- The welcome dialog has an explicit accessible label for close.
- Copy buttons announce success through accessible status text.
- Code blocks have adequate contrast in light and dark modes.
- Mobile content has no horizontal page overflow.
- Motion respects `prefers-reduced-motion`.

## Testing Strategy

### Frontend unit and component tests

- `/getting-started` is a public route with the expected title.
- `HomeView` renders the new card in the built-in homepage and preserves `home_content` override behavior.
- The public-home header, authenticated shared sidebar, and dashboard quick actions expose permanent guide entries.
- `BeginnerWelcomeDialog` appears only for `eligible` state.
- Start and close both PATCH suppression before navigation/dismissal.
- Progress merge behavior is deterministic and excludes secrets.
- The curriculum exposes exactly Claude Code and Codex across macOS, Windows, and Linux.
- Chinese and English locale keys are complete and collision-free.
- Client and language switching preserve stable completed steps.
- `GuideApiKeyStep` handles anonymous, authenticated-empty, existing-key, create-success, and API-failure states.
- `buildClientConfigFiles` preserves all current `UseKeyModal` output and produces the same Claude Code/Codex files for guide input.
- No route or persisted progress contains a supplied test API key.
- Clipboard fallback is usable.
- Mobile progress navigation and dark-mode source contracts render correctly.

### Backend tests

- Ent schema fields and validation.
- Migration backfills existing users to `suppressed` while future users default to `eligible`.
- GET returns only the authenticated user's state.
- PATCH accepts valid progress and rejects invalid states, versions, clients, OS values, step IDs, oversized arrays, and unknown fields.
- PATCH cannot restore `eligible`.
- Completion timestamp is server-generated and idempotent.
- No API-key data can be written into progress.

### Integration and manual verification

Verify the complete journey in Chinese and English:

1. Visit `/home` anonymously.
2. Enter the guide from the beginner card.
3. Choose client and OS.
4. Reach the authentication checkpoint without losing progress.
5. Register or log in and resume the same step.
6. Create or select a key inline.
7. Generate configuration for the configured domain.
8. Complete the manual first-request confirmation.
9. Verify completion and permanent re-entry links.
10. Verify the automatic dashboard dialog does not return after start or close, including in a second browser session.

The implementation must also run the focused frontend suites, full frontend tests, backend tests, typecheck, lint checks on changed scope, and a production frontend build.

## Rollout and Compatibility

- Preserve the current `/home`, `/dashboard`, `/keys`, login, and registration behaviors outside the new entry and return flow.
- Do not alter existing admin onboarding-tour storage keys or steps.
- Keep custom homepage override behavior unchanged.
- Treat guide-state endpoint failure as non-fatal.
- Backfill current accounts before enabling the automatic dialog.
- The guide content schema is versioned so future curriculum changes can migrate or safely reset only guide progress.

## Acceptance Criteria

The feature is complete when:

1. Public and authenticated users can always find the guide again.
2. An anonymous beginner can read and complete installation guidance without logging in first.
3. Authentication resumes the guide at the API-key step.
4. An authenticated user can create or select a key inline.
5. The guide produces the same Claude Code/Codex gateway configuration as the existing key modal.
6. New accounts receive one automatic dashboard prompt; existing accounts do not.
7. Starting or closing the prompt suppresses it account-wide.
8. The guide supports Chinese and English and all three requested operating systems.
9. Only Claude Code and Codex appear in the first-release curriculum.
10. No secret is persisted or leaked through guide state.
11. Automated tests and the manual end-to-end journey pass.
