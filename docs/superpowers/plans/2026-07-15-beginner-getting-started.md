# Beginner Getting Started Guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bilingual beginner journey that takes an anonymous or authenticated user from “I do not know what an AI agent is” to a working Claude Code or Codex installation configured with this gateway, while keeping the detailed tutorial off the homepage and keeping API keys out of persisted guide state.

**Architecture:** Add one public `/getting-started` task workspace, backed by a strict account-scoped guide-state API and a versioned anonymous browser-state adapter. Reuse the current API-key endpoints and extract the existing Claude Code/Codex file generation into a pure shared module. Point the built-in homepage, one-time dashboard welcome dialog, dashboard quick actions, and shared user/admin-personal sidebar at the same canonical route.

**Tech Stack:** Go 1.24, Gin, Ent, PostgreSQL JSONB migrations, Vue 3 Composition API, Pinia, TypeScript, Vue Router, vue-i18n, Tailwind CSS, Vitest, Vue Test Utils, Go testing, graphify.

## Global Constraints

- Implement the approved design in `docs/superpowers/specs/2026-07-15-beginner-getting-started-design.md`; do not add clients, states, steps, or discovery surfaces that contradict it.
- The first release exposes exactly `Claude Code` and `Codex`; do not render “coming soon” clients.
- Support exactly `macOS`, `Windows`, and `Linux`. Windows beginner instructions use PowerShell by default; WSL is a clearly labeled fallback, not the primary path.
- Keep the canonical route `/getting-started`. Client, OS, current step, and completion are state, not query parameters or separate routes.
- Keep the existing backend-mode allowlist unchanged. Therefore `/getting-started` is public in normal mode, but an anonymous backend-mode visitor is redirected to `/login`; an authenticated administrator can still open the public route.
- Keep the current `home_content` full-page override authoritative. Add the homepage guide entry only to the built-in landing page.
- New users default to prompt state `eligible`; the rollout migration must backfill every existing user to `suppressed` before adding the future-row default.
- Starting or closing the automatic dashboard dialog suppresses future automatic display account-wide. A permanent sidebar entry and dashboard quick action remain after suppression or completion.
- Persist only the versioned non-secret fields `version`, `client`, `os`, `currentStep`, and `completedSteps`. Never persist an API-key ID, API-key value, generated configuration, email address, or command containing a key.
- Never put an API key in a URL, query, local/session storage, guide-state API payload, analytics event, console log, error text, or graph fixture.
- Fetch keys only while the authenticated guide is on `api_key` or `configure`. Keep the selected key and generated files in component memory and clear them when the selection changes, authentication ends, or the view unmounts.
- Reuse `GET /api/v1/keys` and `POST /api/v1/keys`; do not add a guide-specific key-creation endpoint.
- Preserve every existing `UseKeyModal.vue` client tab and output. Only Claude Code and Codex are shared with the beginner guide.
- Preserve Claude Code gateway semantics: use a bare `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and the existing nonessential-traffic/attribution flags.
- Preserve Codex gateway semantics: normalize `base_url` to one `/v1`, keep the key only in `auth.json`, retain `requires_openai_auth = true`, and do not generate `env_key`.
- Installation content is structured data with official source metadata and verification date `2026-07-15`:
  - Claude Code installation: `https://code.claude.com/docs/en/installation`
  - Claude Code terminal guide: `https://code.claude.com/docs/en/terminal-guide`
  - Claude Code gateway configuration: `https://code.claude.com/docs/en/llm-gateway`
  - Codex CLI installation: `https://learn.chatgpt.com/docs/codex/cli/install`
  - Codex configuration: `https://learn.chatgpt.com/docs/codex/config-basic`
- Use the current official native installers:
  - Claude Code macOS/Linux: `curl -fsSL https://claude.ai/install.sh | bash`
  - Claude Code Windows PowerShell: `irm https://claude.ai/install.ps1 | iex`
  - Codex macOS/Linux: `curl -fsSL https://chatgpt.com/codex/install.sh | sh`
  - Codex Windows PowerShell: `irm https://chatgpt.com/codex/install.ps1 | iex`
- Verification and diagnostics use the current client commands: `claude --version`, `claude doctor`, `codex --version`, `codex login status`, and `codex doctor`.
- Keep explanations beginner-safe: define model, agent, terminal, gateway, and API key before using the terms; do not introduce provider routing or architecture jargon in the main path.
- Use existing `BaseDialog`, theme, toast, URL sanitation, clipboard, and public-settings behavior. Extend shared primitives only in backward-compatible ways.
- Use strict request decoding with unknown-field rejection and an 8 KiB body limit for the guide PATCH endpoint.
- Follow TDD: add a failing focused test, observe the intended failure, implement the smallest behavior, rerun the focused suite, then commit.
- Run package commands with `COREPACK_ENABLE_PROJECT_SPEC=0` so Corepack does not add a `packageManager` field to `package.json`.
- Preserve unrelated user changes. Stage only the files named by the current task.

---

## File Map

### Backend state and API

- Create: `backend/migrations/183_add_beginner_guide_state.sql`
- Create: `backend/migrations/beginner_guide_migration_test.go`
- Modify: `backend/ent/schema/user.go`
- Generate: `backend/ent/user.go`
- Generate: `backend/ent/user_create.go`
- Generate: `backend/ent/user_update.go`
- Generate: `backend/ent/user/where.go`
- Generate: `backend/ent/user/user.go`
- Generate: `backend/ent/mutation.go`
- Generate: `backend/ent/migrate/schema.go`
- Create: `backend/internal/service/user_beginner_guide.go`
- Create: `backend/internal/service/user_beginner_guide_test.go`
- Modify: `backend/internal/service/user_service.go`
- Create: `backend/internal/repository/user_beginner_guide_repo.go`
- Create: `backend/internal/repository/user_beginner_guide_repo_integration_test.go`
- Create: `backend/internal/handler/user_beginner_guide_handler.go`
- Create: `backend/internal/handler/user_beginner_guide_handler_test.go`
- Modify: `backend/internal/handler/user_handler_test.go`
- Modify: `backend/internal/server/routes/user.go`

### Frontend state, content, and shared configuration

- Create: `frontend/src/api/beginnerGuide.ts`
- Create: `frontend/src/stores/beginnerGuide.ts`
- Create: `frontend/src/stores/__tests__/beginnerGuide.spec.ts`
- Modify: `frontend/src/stores/index.ts`
- Create: `frontend/src/components/keys/clientConfigFiles.ts`
- Create: `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue`
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`
- Create: `frontend/src/components/getting-started/curriculum.ts`
- Create: `frontend/src/components/getting-started/__tests__/curriculum.spec.ts`
- Create: `frontend/src/i18n/locales/zh/gettingStarted.ts`
- Create: `frontend/src/i18n/locales/en/gettingStarted.ts`
- Modify: `frontend/src/i18n/locales/zh/index.ts`
- Modify: `frontend/src/i18n/locales/en/index.ts`

### Guide route and UI

- Create: `frontend/src/views/public/GettingStartedView.vue`
- Create: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`
- Create: `frontend/src/components/getting-started/GuideShell.vue`
- Create: `frontend/src/components/getting-started/GuideProgressNav.vue`
- Create: `frontend/src/components/getting-started/GuideStepPanel.vue`
- Create: `frontend/src/components/getting-started/GuideCommandBlock.vue`
- Create: `frontend/src/components/getting-started/GuideApiKeyStep.vue`
- Create: `frontend/src/components/getting-started/GuideTroubleshooting.vue`
- Create: `frontend/src/components/getting-started/__tests__/GuideProgressNav.spec.ts`
- Create: `frontend/src/components/getting-started/__tests__/GuideCommandBlock.spec.ts`
- Create: `frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts`
- Modify: `frontend/src/router/index.ts`
- Create: `frontend/src/router/__tests__/getting-started-route.spec.ts`

### Discovery, prompt, and authentication return

- Create: `frontend/src/components/getting-started/BeginnerGuideCard.vue`
- Create: `frontend/src/components/getting-started/BeginnerWelcomeDialog.vue`
- Create: `frontend/src/components/getting-started/__tests__/BeginnerWelcomeDialog.spec.ts`
- Modify: `frontend/src/components/common/BaseDialog.vue`
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`
- Modify: `frontend/src/components/user/dashboard/UserDashboardContent.vue`
- Modify: `frontend/src/components/user/dashboard/UserDashboardContent.spec.ts`
- Modify: `frontend/src/components/user/dashboard/UserDashboardQuickActions.vue`
- Create: `frontend/src/components/user/dashboard/UserDashboardQuickActions.spec.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- Create: `frontend/src/router/authRedirect.ts`
- Create: `frontend/src/router/__tests__/authRedirect.spec.ts`
- Modify: `frontend/src/views/auth/LoginView.vue`
- Modify: `frontend/src/views/auth/RegisterView.vue`
- Modify: `frontend/src/views/auth/EmailVerifyView.vue`
- Modify: `frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts`

---

### Task 1: Add the Rollout-Safe User Schema and Migration

**Files:**
- Create: `backend/migrations/183_add_beginner_guide_state.sql`
- Create: `backend/migrations/beginner_guide_migration_test.go`
- Modify: `backend/ent/schema/user.go`
- Generate: the User-related Ent files listed in the File Map

**Interfaces:**
- Database produces nullable JSON progress and completion time plus a non-null prompt state.
- New rows receive `eligible`; rows that existed when migration 183 runs receive `suppressed`.

- [ ] **Step 1: Add a static migration contract test**

Create `backend/migrations/beginner_guide_migration_test.go` and assert that the SQL:

```go
func TestBeginnerGuideMigrationBackfillsBeforeDefault(t *testing.T) {
	sqlBytes, err := os.ReadFile("183_add_beginner_guide_state.sql")
	require.NoError(t, err)
	sql := string(sqlBytes)

	addAt := strings.Index(sql, "ADD COLUMN IF NOT EXISTS beginner_guide_prompt_state")
	backfillAt := strings.Index(sql, "SET beginner_guide_prompt_state = 'suppressed'")
	defaultAt := strings.Index(sql, "SET DEFAULT 'eligible'")
	require.Greater(t, addAt, -1)
	require.Greater(t, backfillAt, addAt)
	require.Greater(t, defaultAt, backfillAt)
	require.Contains(t, sql, "beginner_guide_progress JSONB")
	require.Contains(t, sql, "beginner_guide_completed_at TIMESTAMPTZ")
	require.Contains(t, sql, "CHECK (beginner_guide_prompt_state IN ('eligible', 'suppressed', 'completed'))")
}
```

- [ ] **Step 2: Run the migration test and verify it fails**

Run from `backend`:

```bash
go test ./migrations -run TestBeginnerGuideMigrationBackfillsBeforeDefault
```

Expected: FAIL because `183_add_beginner_guide_state.sql` does not exist.

- [ ] **Step 3: Add the transactional migration in the tested order**

Create `backend/migrations/183_add_beginner_guide_state.sql` with this order:

```sql
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS beginner_guide_prompt_state VARCHAR(20),
    ADD COLUMN IF NOT EXISTS beginner_guide_progress JSONB,
    ADD COLUMN IF NOT EXISTS beginner_guide_completed_at TIMESTAMPTZ;

UPDATE users
SET beginner_guide_prompt_state = 'suppressed'
WHERE beginner_guide_prompt_state IS NULL;

ALTER TABLE users
    ALTER COLUMN beginner_guide_prompt_state SET DEFAULT 'eligible',
    ALTER COLUMN beginner_guide_prompt_state SET NOT NULL;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_beginner_guide_prompt_state_check,
    ADD CONSTRAINT users_beginner_guide_prompt_state_check
        CHECK (beginner_guide_prompt_state IN ('eligible', 'suppressed', 'completed'));
```

- [ ] **Step 4: Add matching Ent fields**

In `backend/ent/schema/user.go`, import `encoding/json`, then add:

```go
field.String("beginner_guide_prompt_state").
	MaxLen(20).
	Default("eligible").
	Validate(func(value string) error {
		switch value {
		case "eligible", "suppressed", "completed":
			return nil
		default:
			return fmt.Errorf("must be eligible, suppressed, or completed")
		}
	}),
field.JSON("beginner_guide_progress", json.RawMessage{}).
	Optional().
	SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
field.Time("beginner_guide_completed_at").
	Optional().
	Nillable().
	SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
```

- [ ] **Step 5: Generate Ent and rerun the focused checks**

Run:

```bash
make generate
go test ./migrations -run TestBeginnerGuideMigrationBackfillsBeforeDefault
go test ./ent/...
```

Expected: PASS. Inspect the generated mutation, create, update, predicate, and migration schema code to confirm all three fields are present and progress remains nullable.

- [ ] **Step 6: Commit the schema slice**

```bash
git add backend/migrations/183_add_beginner_guide_state.sql backend/migrations/beginner_guide_migration_test.go backend/ent/schema/user.go backend/ent
git commit -m "feat: add beginner guide user state"
```

---

### Task 2: Implement Strict Guide-State Domain Rules and Persistence

**Files:**
- Create: `backend/internal/service/user_beginner_guide.go`
- Create: `backend/internal/service/user_beginner_guide_test.go`
- Modify: `backend/internal/service/user_service.go`
- Create: `backend/internal/repository/user_beginner_guide_repo.go`
- Create: `backend/internal/repository/user_beginner_guide_repo_integration_test.go`

**Interfaces:**

```go
type BeginnerGuidePromptState string

type BeginnerGuideProgress struct {
	Version        int      `json:"version"`
	Client         string   `json:"client"`
	OS             string   `json:"os"`
	CurrentStep    string   `json:"currentStep"`
	CompletedSteps []string `json:"completedSteps"`
}

type BeginnerGuideState struct {
	PromptState BeginnerGuidePromptState `json:"prompt_state"`
	Progress    *BeginnerGuideProgress   `json:"progress"`
	CompletedAt *time.Time               `json:"completed_at"`
}

type BeginnerGuideRepository interface {
	GetBeginnerGuideState(context.Context, int64) (*BeginnerGuideState, error)
	UpdateBeginnerGuideState(context.Context, int64, BeginnerGuideState) (*BeginnerGuideState, error)
}
```

- [ ] **Step 1: Add table-driven service tests for the complete state machine**

In `user_beginner_guide_test.go`, cover:

- valid progress for both clients and all three OS values;
- invalid version, client, OS, current step, duplicate step, unknown step, and more than eight completed steps;
- `eligible -> suppressed`, `eligible -> completed`, and `suppressed -> completed`;
- rejection of a request that tries to restore `eligible`;
- preserving the original server timestamp on a repeated `completed` patch;
- leaving current progress unchanged when PATCH omits progress;
- returning `ErrBeginnerGuideUnavailable` when the injected user repository does not implement `BeginnerGuideRepository`.

Use the exact known steps:

```go
var beginnerGuideStepOrder = []string{
	"understand",
	"choose",
	"terminal",
	"install",
	"api_key",
	"configure",
	"first_run",
	"troubleshoot",
}
```

- [ ] **Step 2: Run the service tests and verify they fail**

```bash
go test -tags=unit ./internal/service -run BeginnerGuide
```

Expected: FAIL because the guide domain types and service methods do not exist.

- [ ] **Step 3: Define validation and transition methods**

Implement in `user_beginner_guide.go`:

```go
const BeginnerGuideProgressVersion = 1

type PatchBeginnerGuideStateRequest struct {
	PromptState *BeginnerGuidePromptState
	Progress    *BeginnerGuideProgress
}

func validateBeginnerGuideProgress(progress *BeginnerGuideProgress) error {
	if progress == nil {
		return nil
	}
	if progress.Version != BeginnerGuideProgressVersion {
		return ErrBeginnerGuideProgressInvalid
	}
	if progress.Client != "claude_code" && progress.Client != "codex" {
		return ErrBeginnerGuideProgressInvalid
	}
	if progress.OS != "macos" && progress.OS != "windows" && progress.OS != "linux" {
		return ErrBeginnerGuideProgressInvalid
	}
	return validateBeginnerGuideSteps(progress.CurrentStep, progress.CompletedSteps)
}
```

`PatchBeginnerGuideState` must load the authenticated user's current state, validate before persistence, make `completed` monotonic, and call `time.Now().UTC()` only when completion has no existing timestamp.

- [ ] **Step 4: Attach the narrow repository without expanding `UserRepository`**

Add to `UserService`:

```go
beginnerGuideRepo BeginnerGuideRepository
```

In `NewUserService`, use a checked type assertion:

```go
beginnerGuideRepo, _ := userRepo.(BeginnerGuideRepository)
```

This avoids forcing every unrelated user-repository test double to implement guide methods while making missing production wiring fail explicitly at the guide service boundary.

- [ ] **Step 5: Implement Ent persistence in a focused repository file**

`GetBeginnerGuideState` reads only the three guide columns. Decode non-empty JSON into the strict service type and return a wrapped persistence error if stored JSON is malformed.

`UpdateBeginnerGuideState` must:

```go
update := r.client.User.UpdateOneID(userID).
	SetBeginnerGuidePromptState(string(state.PromptState)).
	SetNillableBeginnerGuideCompletedAt(state.CompletedAt)

if state.Progress == nil {
	update = update.ClearBeginnerGuideProgress()
} else {
	raw, err := json.Marshal(state.Progress)
	if err != nil {
		return nil, fmt.Errorf("marshal beginner guide progress: %w", err)
	}
	update = update.SetBeginnerGuideProgress(raw)
}
```

Save, translate a missing user to `ErrUserNotFound`, and return the stored state.

- [ ] **Step 6: Add a repository integration test**

Using the existing repository integration harness, create two users and prove that:

- reading user A never returns user B's state;
- progress round-trips with the exact camel-case JSON contract;
- completion timestamp and state persist;
- clearing progress writes SQL `NULL` rather than `{}`.

- [ ] **Step 7: Run focused and package tests**

```bash
go test -tags=unit ./internal/service -run BeginnerGuide
go test -tags=integration ./internal/repository -run BeginnerGuide
go test ./internal/repository ./internal/service
```

Expected: PASS. The integration test may report an explicit skip when the repository test database is unavailable; it must not fail for compilation or schema reasons.

- [ ] **Step 8: Commit the state service**

```bash
git add backend/internal/service/user_beginner_guide.go backend/internal/service/user_beginner_guide_test.go backend/internal/service/user_service.go backend/internal/repository/user_beginner_guide_repo.go backend/internal/repository/user_beginner_guide_repo_integration_test.go
git commit -m "feat: persist beginner guide progress"
```

---

### Task 3: Expose Authenticated GET and PATCH Endpoints

**Files:**
- Create: `backend/internal/handler/user_beginner_guide_handler.go`
- Create: `backend/internal/handler/user_beginner_guide_handler_test.go`
- Modify: `backend/internal/handler/user_handler_test.go`
- Modify: `backend/internal/server/routes/user.go`

**Interfaces:**

```text
GET   /api/v1/user/beginner-guide
PATCH /api/v1/user/beginner-guide
```

PATCH accepts only `prompt_state: "suppressed" | "completed"` and a valid `progress` object. Both routes derive ownership from `middleware.AuthSubject`.

- [ ] **Step 1: Extend the existing handler repository stub with the narrow interface**

Add state storage and these two methods to `userHandlerRepoStub` in `user_handler_test.go` so the new handler tests use the real service:

```go
func (s *userHandlerRepoStub) GetBeginnerGuideState(context.Context, int64) (*service.BeginnerGuideState, error) {
	state := s.beginnerGuideState
	return &state, nil
}

func (s *userHandlerRepoStub) UpdateBeginnerGuideState(_ context.Context, _ int64, state service.BeginnerGuideState) (*service.BeginnerGuideState, error) {
	s.beginnerGuideState = state
	return &state, nil
}
```

- [ ] **Step 2: Add handler tests before routes**

Test `GetBeginnerGuide` and `PatchBeginnerGuide` directly with Gin contexts. Assert:

- missing auth subject returns 401;
- GET uses the subject user ID and returns only `prompt_state`, `progress`, `completed_at`;
- valid PATCH returns 200;
- `eligible`, unknown top-level fields, secret-looking fields such as `api_key`, unknown nested progress fields, malformed JSON, empty body, and body larger than 8 KiB return 400;
- repeated `completed` returns the same non-null timestamp.

- [ ] **Step 3: Run the focused handler tests and verify failure**

```bash
go test -tags=unit ./internal/handler -run BeginnerGuide
```

Expected: FAIL because the handler methods do not exist.

- [ ] **Step 4: Implement strict decoding and authenticated ownership**

Define a handler-only request DTO with pointers. Before decoding, wrap the body with `http.MaxBytesReader`, then use:

```go
decoder := json.NewDecoder(c.Request.Body)
decoder.DisallowUnknownFields()
if err := decoder.Decode(&req); err != nil {
	response.BadRequest(c, "Invalid request")
	return
}
if err := decoder.Decode(&struct{}{}); err != io.EOF {
	response.BadRequest(c, "Invalid request")
	return
}
```

Convert the DTO to `service.PatchBeginnerGuideStateRequest`. Do not accept a user ID, timestamp, API key, or arbitrary map.

- [ ] **Step 5: Register the routes in the existing authenticated user group**

Add to `backend/internal/server/routes/user.go`:

```go
user.GET("/beginner-guide", h.User.GetBeginnerGuide)
user.PATCH("/beginner-guide", h.User.PatchBeginnerGuide)
```

- [ ] **Step 6: Run handler, route, and backend package tests**

```bash
go test -tags=unit ./internal/handler -run BeginnerGuide
go test ./internal/server/routes ./internal/handler
```

Expected: PASS.

- [ ] **Step 7: Commit the API slice**

```bash
git add backend/internal/handler/user_beginner_guide_handler.go backend/internal/handler/user_beginner_guide_handler_test.go backend/internal/handler/user_handler_test.go backend/internal/server/routes/user.go
git commit -m "feat: expose beginner guide state api"
```

---

### Task 4: Add the Frontend Guide API and Secret-Free Progress Store

**Files:**
- Create: `frontend/src/api/beginnerGuide.ts`
- Create: `frontend/src/stores/beginnerGuide.ts`
- Create: `frontend/src/stores/__tests__/beginnerGuide.spec.ts`
- Modify: `frontend/src/stores/index.ts`

**Interfaces:**

```ts
export type BeginnerGuideClient = 'claude_code' | 'codex'
export type BeginnerGuideOS = 'macos' | 'windows' | 'linux'
export type BeginnerGuideStepId =
  | 'understand'
  | 'choose'
  | 'terminal'
  | 'install'
  | 'api_key'
  | 'configure'
  | 'first_run'
  | 'troubleshoot'

export interface BeginnerGuideProgressV1 {
  version: 1
  client: BeginnerGuideClient
  os: BeginnerGuideOS
  currentStep: BeginnerGuideStepId
  completedSteps: BeginnerGuideStepId[]
}
```

- [ ] **Step 1: Write store tests for normalization, invalidation, merge, and retries**

Cover these behaviors with a real Pinia and mocked API module:

- invalid local JSON is removed and replaced by a safe default;
- serialization contains exactly the five allowed keys and never includes `api_key`, `apiKey`, `key`, or `selectedKeyId` from hostile input;
- changing client or OS preserves `understand`, `choose`, and existing `api_key` completion, invalidates exactly `terminal`, `install`, `configure`, `first_run`, and `troubleshoot`, and moves a now-invalid current step back to `terminal`;
- anonymous/account merge unions completed steps in curriculum order but prefers anonymous client, OS, and current step;
- a successful authenticated merge PATCHes `suppressed`, saves progress, and removes the anonymous copy;
- a failed merge retains the anonymous copy and remains usable;
- prompt GET failure returns `showPrompt = false`;
- failed suppression hides the prompt locally, writes an account-scoped `beginner_guide_prompt_retry_v1:<user-id>` marker, and the next authenticated initialization for that account retries it;
- entering the guide while already authenticated changes an `eligible` prompt to `suppressed`, even when there is no anonymous progress to merge;
- completion PATCH sets local state to completed without adding secrets.

- [ ] **Step 2: Run the focused store test and verify failure**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/stores/__tests__/beginnerGuide.spec.ts
```

Expected: FAIL because the API and store do not exist.

- [ ] **Step 3: Implement the thin API module**

`beginnerGuide.ts` exports types plus:

```ts
export async function getBeginnerGuideState(): Promise<BeginnerGuideState> {
  const { data } = await apiClient.get<BeginnerGuideState>('/user/beginner-guide')
  return data
}

export async function patchBeginnerGuideState(
  patch: PatchBeginnerGuideStateRequest
): Promise<BeginnerGuideState> {
  const { data } = await apiClient.patch<BeginnerGuideState>('/user/beginner-guide', patch)
  return data
}
```

- [ ] **Step 4: Implement a whitelist-based Pinia store**

Use constants:

```ts
const ANONYMOUS_PROGRESS_KEY = 'beginner_guide_progress_v1'
const PROMPT_RETRY_KEY_PREFIX = 'beginner_guide_prompt_retry_v1:'
const SELECTION_INVARIANT_STEPS = new Set<BeginnerGuideStepId>([
  'understand',
  'choose',
  'api_key'
])
```

Never spread untrusted objects into persisted progress. Build a new object field by field after enum validation. Export actions for `initialize`, `selectClient`, `selectOS`, `goToStep`, `completeStep`, `suppressPrompt`, and `completeGuide`.

- [ ] **Step 5: Export the store and rerun tests**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/stores/__tests__/beginnerGuide.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit the state adapter**

```bash
git add frontend/src/api/beginnerGuide.ts frontend/src/stores/beginnerGuide.ts frontend/src/stores/__tests__/beginnerGuide.spec.ts frontend/src/stores/index.ts
git commit -m "feat: add beginner guide progress store"
```

---

### Task 5: Extract Claude Code and Codex Configuration Generation

**Files:**
- Create: `frontend/src/components/keys/clientConfigFiles.ts`
- Create: `frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts`
- Modify: `frontend/src/components/keys/UseKeyModal.vue`
- Modify: `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts`

**Interfaces:**

```ts
export type SupportedGuideClient = 'claude_code' | 'codex'
export type SupportedGuideOS = 'macos' | 'windows' | 'linux'
export type WindowsGuideShell = 'powershell' | 'cmd'

export interface ClientConfigInput {
  client: SupportedGuideClient
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  apiKey: string
  baseUrl: string
  allowMessagesDispatch?: boolean
  windowsShell?: WindowsGuideShell
}

export interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
  hint?: string
}

export function buildClientConfigFiles(input: ClientConfigInput): ClientConfigFile[]
```

`windowsShell` defaults to `powershell` for the beginner guide; `UseKeyModal` passes `cmd` only when its existing CMD tab is selected.

- [ ] **Step 1: Add pure-function golden tests before extraction**

Assert exact output for:

- Claude Code macOS and Linux: Terminal exports plus `~/.claude/settings.json`;
- Claude Code Windows PowerShell and CMD: the current shell syntax plus `%userprofile%\.claude\settings.json`;
- Codex macOS/Linux: `~/.codex/config.toml` and `~/.codex/auth.json`;
- Codex Windows: `%userprofile%\.codex\config.toml` and `auth.json`;
- an input base ending in `/v1/` becomes bare for Claude and exactly `/v1` for Codex;
- Claude output contains the key only in the required environment/config values;
- Codex `config.toml` never contains the key or `env_key`, while `auth.json` parses to `{ OPENAI_API_KEY: 'sk-test' }`.

- [ ] **Step 2: Run the new test and verify failure**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientConfigFiles.spec.ts
```

Expected: FAIL because the pure module does not exist.

- [ ] **Step 3: Move only the shared generators into the pure module**

Implement base normalization without Vue or i18n dependencies:

```ts
function gatewayRoots(baseUrl: string): { bare: string; v1: string } {
  const bare = baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  return { bare, v1: `${bare}/v1` }
}
```

Keep the current Claude environment variables and Codex TOML byte-for-byte. Return `hintKey` instead of calling `t()` inside the module.

- [ ] **Step 4: Replace the modal's Claude/Codex branches with the shared function**

Map tabs as follows:

```ts
const selectedOS = activeTab.value === 'windows' || activeTab.value === 'cmd' || activeTab.value === 'powershell'
  ? 'windows'
  : 'macos'
const windowsShell = activeTab.value === 'cmd' ? 'cmd' : 'powershell'
```

Use `buildClientConfigFiles` only for `activeClientTab === 'claude'` and `activeClientTab === 'codex'`. Leave WorkBuddy, OpenCode, SDK, Gemini, Antigravity, and Grok generators in the modal. Resolve `hintKey` through `t()` when adapting returned files to the current `FileConfig`.

- [ ] **Step 5: Strengthen existing modal tests against regressions**

Add assertions that both the direct pure function and mounted modal produce the same paths and contents for Claude Unix, Claude PowerShell, Claude CMD, Codex Unix, and Codex Windows.

- [ ] **Step 6: Run the complete focused configuration suite**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/keys/__tests__/clientConfigFiles.spec.ts src/components/keys/__tests__/UseKeyModal.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS with every pre-existing client-tab test still green.

- [ ] **Step 7: Commit the shared generator**

```bash
git add frontend/src/components/keys/clientConfigFiles.ts frontend/src/components/keys/__tests__/clientConfigFiles.spec.ts frontend/src/components/keys/UseKeyModal.vue frontend/src/components/keys/__tests__/UseKeyModal.spec.ts
git commit -m "refactor: share agent client configuration"
```

---

### Task 6: Define the Bilingual Curriculum and Official Command Matrix

**Files:**
- Create: `frontend/src/components/getting-started/curriculum.ts`
- Create: `frontend/src/components/getting-started/__tests__/curriculum.spec.ts`
- Create: `frontend/src/i18n/locales/zh/gettingStarted.ts`
- Create: `frontend/src/i18n/locales/en/gettingStarted.ts`
- Modify: `frontend/src/i18n/locales/zh/index.ts`
- Modify: `frontend/src/i18n/locales/en/index.ts`

**Interfaces:**

```ts
export interface GuideVariant {
  client: BeginnerGuideClient
  os: BeginnerGuideOS
  shell: 'terminal' | 'powershell'
  installCommand: string
  verifyCommand: string
  launchCommand: string
  diagnosticCommands: string[]
  officialSourceUrl: string
  verifiedAt: '2026-07-15'
}
```

- [ ] **Step 1: Add curriculum contract tests**

Assert:

- `GUIDE_STEP_IDS` is exactly the approved eight-step sequence;
- supported clients are exactly `claude_code` and `codex`;
- each client has macOS, Windows, and Linux variants;
- the four native installer commands exactly match Global Constraints;
- every variant has an HTTPS official source and verification date `2026-07-15`;
- no serialized curriculum text contains `opencode`, `workbuddy`, `gemini cli`, or `coming soon`;
- locale objects have the same recursive key set;
- source metadata and commands remain structured values, not translated HTML.

- [ ] **Step 2: Run the focused content test and verify failure**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/curriculum.spec.ts
```

Expected: FAIL because curriculum and locale modules do not exist.

- [ ] **Step 3: Implement structured curriculum variants**

Use one entry per client/OS pair. For example:

```ts
{
  client: 'claude_code',
  os: 'windows',
  shell: 'powershell',
  installCommand: 'irm https://claude.ai/install.ps1 | iex',
  verifyCommand: 'claude --version',
  launchCommand: 'claude',
  diagnosticCommands: ['claude doctor'],
  officialSourceUrl: 'https://code.claude.com/docs/en/installation',
  verifiedAt: '2026-07-15'
}
```

The Codex diagnostic list also contains `codex login status`.

- [ ] **Step 4: Add complete Chinese and English copy**

Each locale must cover:

- homepage card and nav label;
- dashboard welcome dialog, quick action, and sidebar label;
- guide header, selectors, progress, Back/Next, copy status, and mobile step menu;
- beginner definitions for model, agent, terminal, gateway, and API key;
- terminal discovery for macOS Terminal, Windows PowerShell, and Linux terminal apps;
- installation explanation, expected result, safe shell-restart note, and official-source link;
- anonymous login/register checkpoint and authenticated key states;
- configuration file merge warning and restart instruction;
- harmless first prompt and manual success confirmation;
- troubleshooting categories, retry labels, completion, dashboard/keys/usage destinations;
- non-blocking progress/prompt persistence warnings.

Do not put trusted HTML in these messages; render prose as normal escaped Vue text.

- [ ] **Step 5: Register locale modules and run locale tests**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/curriculum.spec.ts src/i18n/__tests__/localesNoKeyCollision.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit the curriculum**

```bash
git add frontend/src/components/getting-started/curriculum.ts frontend/src/components/getting-started/__tests__/curriculum.spec.ts frontend/src/i18n/locales/zh/gettingStarted.ts frontend/src/i18n/locales/en/gettingStarted.ts frontend/src/i18n/locales/zh/index.ts frontend/src/i18n/locales/en/index.ts
git commit -m "feat: add bilingual beginner curriculum"
```

---

### Task 7: Build the Public Guide Shell, Navigation, and Generic Steps

**Files:**
- Create: `frontend/src/views/public/GettingStartedView.vue`
- Create: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`
- Create: `frontend/src/components/getting-started/GuideShell.vue`
- Create: `frontend/src/components/getting-started/GuideProgressNav.vue`
- Create: `frontend/src/components/getting-started/GuideStepPanel.vue`
- Create: `frontend/src/components/getting-started/GuideCommandBlock.vue`
- Create: `frontend/src/components/getting-started/GuideTroubleshooting.vue`
- Create: `frontend/src/components/getting-started/__tests__/GuideProgressNav.spec.ts`
- Create: `frontend/src/components/getting-started/__tests__/GuideCommandBlock.spec.ts`
- Modify: `frontend/src/router/index.ts`
- Create: `frontend/src/router/__tests__/getting-started-route.spec.ts`

**Interfaces:**
- `GettingStartedView` owns active-step orchestration and ephemeral selected-key state.
- Presentation components receive curriculum/progress props and emit intent; they do not call dashboard or key APIs.

- [ ] **Step 1: Add route and view contract tests first**

Assert that:

- router source registers `/getting-started` with `requiresAuth: false` and `gettingStarted.title`;
- the existing backend-mode allowlist does not include `/getting-started`;
- anonymous mounting renders `understand` without calling the guide account API or key API;
- authenticated mounting initializes account progress;
- client and OS selectors expose exactly the approved choices;
- browser detection may suggest an OS but never disables or overrides a user's manual OS selection;
- desktop progress navigation has eight named steps and completion text/icons;
- mobile renders a menu button and a dismissible step drawer;
- changing language does not change step IDs or reset completion;
- changing OS/client invalidates only variant-specific completion;
- code blocks do not widen the page.

- [ ] **Step 2: Add clipboard tests and observe failure**

Test successful Clipboard API use plus a failed-copy state where the command remains selectable and manual-copy text is shown. Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/getting-started-route.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideProgressNav.spec.ts src/components/getting-started/__tests__/GuideCommandBlock.spec.ts
```

Expected: FAIL because route and components do not exist.

- [ ] **Step 3: Register the public route before the catch-all**

```ts
{
  path: '/getting-started',
  name: 'GettingStarted',
  component: () => import('@/views/public/GettingStartedView.vue'),
  meta: {
    requiresAuth: false,
    title: 'Beginner Guide',
    titleKey: 'gettingStarted.title'
  }
}
```

Do not modify `BACKEND_MODE_ALLOWED_PATHS`.

- [ ] **Step 4: Implement the dedicated public shell**

`GuideShell` contains brand/home link, guide title, `LocaleSwitcher`, theme control, and one account action:

- anonymous: `/login?redirect=/getting-started`;
- normal authenticated user: `/dashboard`;
- authenticated admin: `/admin/my-account/dashboard`.

Desktop uses `lg:grid-cols-[18rem_minmax(0,1fr)]`; mobile uses a compact progress header. Apply `min-w-0` to the content column and `overflow-x-auto` only to command blocks.

- [ ] **Step 5: Implement progress and generic step presentation**

`GuideProgressNav` emits only known step IDs and communicates current/completed state in text plus icon, not color alone. `GuideStepPanel` renders the active step title, explanation, optional details, Back, and Next. Do not allow skipping ahead beyond the first incomplete prerequisite. Detect the browser OS only to preselect/suggest the initial value; every OS button remains available after that choice.

- [ ] **Step 6: Implement command and troubleshooting components**

`GuideCommandBlock` renders command text with `v-text`, a copy button, an `aria-live="polite"` status, and manual-copy guidance on failure. `GuideTroubleshooting` renders version, file path, base URL, restart, authentication, connection, shell, permission, and official-source branches for the selected variant.

- [ ] **Step 7: Coordinate steps 1-4 and 7-8 in the view**

Render `understand`, `choose`, `terminal`, `install`, `first_run`, and `troubleshoot` from curriculum/i18n. Step completion is an explicit user action; the app does not claim to observe a local command. Completing `troubleshoot` calls the store's completion action and shows Dashboard, API Keys, and Usage links.

- [ ] **Step 8: Run focused tests and typecheck**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/getting-started-route.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideProgressNav.spec.ts src/components/getting-started/__tests__/GuideCommandBlock.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 9: Commit the public guide workspace**

```bash
git add frontend/src/views/public/GettingStartedView.vue frontend/src/views/public/__tests__/GettingStartedView.spec.ts frontend/src/components/getting-started/GuideShell.vue frontend/src/components/getting-started/GuideProgressNav.vue frontend/src/components/getting-started/GuideStepPanel.vue frontend/src/components/getting-started/GuideCommandBlock.vue frontend/src/components/getting-started/GuideTroubleshooting.vue frontend/src/components/getting-started/__tests__/GuideProgressNav.spec.ts frontend/src/components/getting-started/__tests__/GuideCommandBlock.spec.ts frontend/src/router/index.ts frontend/src/router/__tests__/getting-started-route.spec.ts
git commit -m "feat: add public beginner guide workspace"
```

---

### Task 8: Add Inline Authentication Checkpoint, Key Selection, and Configuration

**Files:**
- Create: `frontend/src/components/getting-started/GuideApiKeyStep.vue`
- Create: `frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts`
- Modify: `frontend/src/views/public/GettingStartedView.vue`
- Modify: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`

**Interfaces:**
- Anonymous checkpoint produces only internal login/register redirect links.
- Authenticated state reuses `keysAPI.list` and `keysAPI.create`.
- The selected `ApiKey` and `buildClientConfigFiles` output live only in memory.

- [ ] **Step 1: Add component tests for all key states**

Cover:

- anonymous: no key API call, login and register links both include `redirect=/getting-started`;
- authenticated list loading, empty state, retryable error, and `/keys` fallback;
- existing active compatible key selection;
- inactive/expired/quota-exhausted keys are disabled and explained;
- minimal creation calls `keysAPI.create(translatedDefaultName)` and immediately selects the returned key;
- creation failure preserves the entered name and reports the existing API error through the app toast;
- emitted selection is in memory only and never passed to progress actions;
- selected key changes clear prior generated files;
- unmount and logout clear selected key/configuration;
- a test key string never appears in route state, localStorage, sessionStorage, or PATCH payloads.

- [ ] **Step 2: Run focused tests and verify failure**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts
```

Expected: FAIL because key checkpoint/configuration integration is absent.

- [ ] **Step 3: Implement the anonymous/authenticated key step**

Use `authStore.isAuthenticated` to branch. Call:

```ts
const page = await keysAPI.list(1, 100)
```

Render a minimal name input and create action; do not copy the full Keys page form. Rely on current unified-key backend defaults and current API validation. A key is compatible when it is active and either unified, Anthropic for Claude Code, OpenAI for Codex, or OpenAI with `allow_messages_dispatch` for Claude Code. Render incompatible/inactive keys disabled with an explanation.

- [ ] **Step 4: Integrate ephemeral selection with configuration**

Keep:

```ts
const selectedKey = shallowRef<ApiKey | null>(null)
function resolveConfigPlatform(key: ApiKey): GroupPlatform | 'unified' {
  if (key.key_type === 'unified') return 'unified'
  if (key.group?.platform) return key.group.platform
  if (key.key_type === 'anthropic' || key.key_type === 'openai') return key.key_type
  return 'unified'
}

const generatedFiles = computed(() => {
  if (!selectedKey.value) return []
  return buildClientConfigFiles({
    client: guideStore.progress.client,
    os: guideStore.progress.os,
    platform: resolveConfigPlatform(selectedKey.value),
    apiKey: selectedKey.value.key,
    baseUrl: appStore.apiBaseUrl || window.location.origin,
    allowMessagesDispatch: selectedKey.value.group?.allow_messages_dispatch ?? false
  })
})
```

If group/platform metadata is absent for a unified key, use `unified`. Render file path, safe-merge warning, copy action, and restart instruction. If the page refreshes on `configure`, return to `api_key` and ask the user to reselect rather than persisting the key.

- [ ] **Step 5: Add authentication-state and lifecycle clearing**

Watch authenticated user ID and selection. On user ID becoming null, selected-key change, OS/client change, and `onBeforeUnmount`, clear any non-computed key/config references. Do not log caught values from key API errors.

- [ ] **Step 6: Run focused tests and typecheck**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts src/components/keys/__tests__/clientConfigFiles.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit the authenticated guide flow**

```bash
git add frontend/src/components/getting-started/GuideApiKeyStep.vue frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts frontend/src/views/public/GettingStartedView.vue frontend/src/views/public/__tests__/GettingStartedView.spec.ts
git commit -m "feat: add guide key and configuration steps"
```

---

### Task 9: Preserve Login, Registration, and Email-Verification Return Paths

**Files:**
- Create: `frontend/src/router/authRedirect.ts`
- Create: `frontend/src/router/__tests__/authRedirect.spec.ts`
- Modify: `frontend/src/views/auth/LoginView.vue`
- Modify: `frontend/src/views/auth/RegisterView.vue`
- Modify: `frontend/src/views/auth/EmailVerifyView.vue`
- Modify: `frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts`

**Interfaces:**
- Only internal absolute paths are accepted as post-auth redirects.
- `/getting-started` survives both direct registration and email-verification registration.

- [ ] **Step 1: Add redirect sanitizer tests**

Create a pure helper and test:

```ts
expect(resolvePostAuthRedirect('/getting-started')).toBe('/getting-started')
expect(resolvePostAuthRedirect('/profile?tab=security')).toBe('/profile?tab=security')
expect(resolvePostAuthRedirect('https://evil.example')).toBe('/dashboard')
expect(resolvePostAuthRedirect('//evil.example')).toBe('/dashboard')
expect(resolvePostAuthRedirect(undefined)).toBe('/dashboard')
```

The guide itself never emits the query-bearing example; the helper remains generally safe for existing internal redirects.

- [ ] **Step 2: Add email-verification return test and verify failure**

Extend `EmailVerifyView.spec.ts` so `register_data.pending_redirect = '/getting-started'` results in `router.push('/getting-started')` after successful verification.

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts src/views/auth/__tests__/EmailVerifyView.spec.ts
```

Expected: FAIL because the helper and registration propagation are not implemented.

- [ ] **Step 3: Implement the internal-path sanitizer**

```ts
export function resolvePostAuthRedirect(value: unknown, fallback = '/dashboard'): string {
  if (typeof value !== 'string') return fallback
  const path = value.trim()
  if (!path.startsWith('/') || path.startsWith('//')) return fallback
  return path
}
```

- [ ] **Step 4: Use the helper in login and both registration paths**

- Login and 2FA use the sanitized route query.
- Direct registration pushes the sanitized query redirect.
- Email-verification registration stores it as `pending_redirect` inside the existing `register_data` object.
- EmailVerify reads the existing `pending_redirect` field and sanitizes it immediately before navigation.

Do not store progress or a key in `register_data`; only store the internal path already supported by EmailVerify.

- [ ] **Step 5: Run focused auth tests**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts src/views/auth/__tests__/EmailVerifyView.spec.ts src/views/auth/__tests__/AuthLinearShell.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit return-path support**

```bash
git add frontend/src/router/authRedirect.ts frontend/src/router/__tests__/authRedirect.spec.ts frontend/src/views/auth/LoginView.vue frontend/src/views/auth/RegisterView.vue frontend/src/views/auth/EmailVerifyView.vue frontend/src/views/auth/__tests__/EmailVerifyView.spec.ts
git commit -m "feat: resume beginner guide after authentication"
```

---

### Task 10: Add the Built-In Homepage Discovery Layer

**Files:**
- Create: `frontend/src/components/getting-started/BeginnerGuideCard.vue`
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/views/__tests__/HomeView.spec.ts`

**Interfaces:**
- Desktop header and one full-width card link to `/getting-started`.
- Custom HTML/iframe homepage mode remains untouched.

- [ ] **Step 1: Add homepage tests before markup**

Assert:

- built-in homepage header has a localized beginner-guide link;
- the beginner card appears after the hero actions and before `homepage-chat-entry`/technical console content;
- card copy reassures a novice and shows four compact stages: choose, install, connect, first task;
- its primary CTA routes to `/getting-started` for anonymous and authenticated visitors;
- URL, Markdown, and HTML `home_content` override tests do not render the card or header link.

- [ ] **Step 2: Run the focused homepage test and verify failure**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
```

Expected: FAIL because the entry layer is absent.

- [ ] **Step 3: Implement the reusable card**

`BeginnerGuideCard.vue` is presentation-only. It renders normal text, a four-item overview, and:

```vue
<router-link to="/getting-started" data-testid="beginner-guide-card-cta">
  {{ t('gettingStarted.discovery.homeCta') }}
</router-link>
```

- [ ] **Step 4: Insert header and card only in default landing markup**

Add the desktop nav link alongside Capabilities/Pricing. Place the card after the hero's primary action block and before the authenticated chat demo. Because both additions live under the existing `v-else` default homepage branch, custom content remains authoritative.

- [ ] **Step 5: Run focused tests and commit**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/__tests__/HomeView.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
git add frontend/src/components/getting-started/BeginnerGuideCard.vue frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts
git commit -m "feat: add homepage beginner guide entry"
```

---

### Task 11: Add One-Time Dashboard Prompt and Permanent Product Entries

**Files:**
- Create: `frontend/src/components/getting-started/BeginnerWelcomeDialog.vue`
- Create: `frontend/src/components/getting-started/__tests__/BeginnerWelcomeDialog.spec.ts`
- Modify: `frontend/src/components/common/BaseDialog.vue`
- Modify: `frontend/src/components/user/dashboard/UserDashboardContent.vue`
- Modify: `frontend/src/components/user/dashboard/UserDashboardContent.spec.ts`
- Modify: `frontend/src/components/user/dashboard/UserDashboardQuickActions.vue`
- Create: `frontend/src/components/user/dashboard/UserDashboardQuickActions.spec.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`

**Interfaces:**
- Dialog auto-opens only when GET succeeds with `eligible`.
- Start and close both attempt server suppression; neither action can trap the user if PATCH fails.
- Sidebar and quick action never depend on prompt state.

- [ ] **Step 1: Add focused dialog tests**

Assert:

- `eligible` renders the dialog; `suppressed`, `completed`, loading, and GET error do not;
- close button and Escape call suppression, hide immediately, and do not navigate;
- Start calls suppression and then routes to `/getting-started` even if PATCH rejects;
- failure shows the non-blocking persistence warning and leaves the local retry marker behavior to the store;
- close has a translated accessible label and focus returns through `BaseDialog`;
- permanent entries render for suppressed/completed users.

- [ ] **Step 2: Add quick-action and sidebar tests**

Assert a `/getting-started` entry for:

- regular user self navigation;
- admin `My Account` navigation without remapping the public route;
- simple mode;
- dashboard quick actions.

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/BeginnerWelcomeDialog.spec.ts src/components/user/dashboard/UserDashboardContent.spec.ts src/components/user/dashboard/UserDashboardQuickActions.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts
```

Expected: FAIL because prompt and permanent entries do not exist.

- [ ] **Step 3: Make `BaseDialog` close labeling configurable**

Add optional `closeAriaLabel` with default `Close modal`, and bind it to the existing close button. This preserves all current callers while allowing the guide dialog to pass `t('gettingStarted.welcome.closeLabel')`.

- [ ] **Step 4: Implement the welcome dialog**

The component receives `show`, emits `start` and `close`, and includes no API logic. Its visible close button is never removed. Use concise novice-oriented copy and make the primary CTA a button so the parent can finish suppression before navigation.

- [ ] **Step 5: Initialize prompt state in shared dashboard content**

Mount `BeginnerWelcomeDialog` outside the stats loading branch. On mount, call `guideStore.initialize({ authenticated: true, promptContext: true })`; transient GET failure keeps `showPrompt` false. Handlers await `suppressPrompt`, catch only to show the warning, and always complete the user's requested dismiss/navigation action.

- [ ] **Step 6: Add permanent quick action and sidebar item**

Place the quick action near Create API Key. In `buildSelfNavItems`, add `/getting-started` near `/keys` so `buildAdminPersonalNavItems()` naturally includes the same canonical public path; do not add it to the admin operations section and do not remap it.

- [ ] **Step 7: Run focused dashboard/navigation tests**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/getting-started/__tests__/BeginnerWelcomeDialog.spec.ts src/components/user/dashboard/UserDashboardContent.spec.ts src/components/user/dashboard/UserDashboardQuickActions.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS.

- [ ] **Step 8: Commit dashboard discovery**

```bash
git add frontend/src/components/getting-started/BeginnerWelcomeDialog.vue frontend/src/components/getting-started/__tests__/BeginnerWelcomeDialog.spec.ts frontend/src/components/common/BaseDialog.vue frontend/src/components/user/dashboard/UserDashboardContent.vue frontend/src/components/user/dashboard/UserDashboardContent.spec.ts frontend/src/components/user/dashboard/UserDashboardQuickActions.vue frontend/src/components/user/dashboard/UserDashboardQuickActions.spec.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts
git commit -m "feat: add beginner dashboard onboarding"
```

---

### Task 12: Harden Accessibility, Secret Boundaries, and Responsive Behavior

**Files:**
- Modify: `frontend/src/views/public/__tests__/GettingStartedView.spec.ts`
- Modify: `frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts`
- Modify: `frontend/src/components/getting-started/__tests__/GuideCommandBlock.spec.ts`
- Modify: `frontend/src/components/getting-started/GuideShell.vue`
- Modify: `frontend/src/components/getting-started/GuideProgressNav.vue`
- Modify: `frontend/src/components/getting-started/GuideCommandBlock.vue`
- Modify: `frontend/src/components/getting-started/GuideApiKeyStep.vue`
- Modify: `frontend/src/views/public/GettingStartedView.vue`

**Interfaces:**
- All selectors, steps, drawers, dialogs, and copy controls are keyboard and screen-reader usable.
- No test key crosses the memory-only boundary.

- [ ] **Step 1: Add a single hostile-secret integration test**

Mount the real view/store/API stubs with key `sk-guide-secret-DO-NOT-PERSIST`, select it, generate both client configs, move through progress, and unmount. Assert the value is absent from:

- `window.location.href`;
- every localStorage and sessionStorage value;
- every guide PATCH call;
- emitted warning/error calls;
- progress JSON after remount.

It is expected to appear only in the rendered in-memory Claude/Codex credential file while that selection is active.

- [ ] **Step 2: Add accessibility and source-contract assertions**

Assert:

- selector groups have labels and keyboard-operable native controls/buttons;
- current step uses `aria-current="step"`;
- mobile drawer has `aria-expanded`, dialog semantics, Escape close, and focus restoration;
- copy success/failure uses `aria-live`;
- visible focus rings exist on interactive guide controls;
- code uses `overflow-x-auto`, while page/grid containers use `min-w-0` and no page-wide `overflow-x`;
- reduced-motion media handling disables nonessential transitions.

- [ ] **Step 3: Run tests and observe any failures**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/components/getting-started/__tests__/GuideCommandBlock.spec.ts
```

Expected: any missing security or accessibility contract fails before hardening.

- [ ] **Step 4: Implement only the failing hardening contracts**

Use Vue text bindings, native controls, explicit labels, focus-visible rings, and lifecycle clearing. Do not introduce HTML rendering or a second persistence mechanism.

- [ ] **Step 5: Run focused tests, lint, and typecheck**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/components/getting-started/__tests__/GuideCommandBlock.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run lint:check
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
```

Expected: PASS with no lint or TypeScript errors.

- [ ] **Step 6: Commit hardening**

```bash
git add frontend/src/views/public/GettingStartedView.vue frontend/src/views/public/__tests__/GettingStartedView.spec.ts frontend/src/components/getting-started/GuideShell.vue frontend/src/components/getting-started/GuideProgressNav.vue frontend/src/components/getting-started/GuideCommandBlock.vue frontend/src/components/getting-started/GuideApiKeyStep.vue frontend/src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts frontend/src/components/getting-started/__tests__/GuideCommandBlock.spec.ts
git commit -m "test: harden beginner guide boundaries"
```

---

### Task 13: Verify the Complete Journey and Refresh the Knowledge Graph

**Files:**
- Modify: `graphify-out/*` only as produced by `graphify update .`

- [ ] **Step 1: Run backend generation drift and all backend tests**

From the worktree root:

```bash
make check-generate
(cd backend && go test ./...)
```

Expected: PASS. `make check-generate` leaves no Ent/Wire diff beyond already committed generated files.

- [ ] **Step 2: Run all frontend tests**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run test:run
```

Expected: every Vitest file passes.

- [ ] **Step 3: Run frontend static checks and production build**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run lint:check
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run build
```

Expected: all commands exit 0.

- [ ] **Step 4: Manually verify the end-to-end matrix**

Run the local app and verify in both Chinese and English:

1. Anonymous `/home` shows the header entry and beginner card.
2. Custom `home_content` still replaces the built-in homepage.
3. `/getting-started` works anonymously in normal mode.
4. Backend mode sends an anonymous guide visitor to login without changing the allowlist.
5. Claude Code and Codex each show macOS, Windows, and Linux instructions from the official command matrix.
6. Login, direct registration, and email-verification registration return to the guide with anonymous progress merged.
7. Existing-key selection and inline key creation both generate the same files as UseKeyModal.
8. Refreshing a configuration step requires key reselection and does not recover the key from storage.
9. Completing the guide produces dashboard/keys/usage next links.
10. A newly created eligible account sees the dashboard prompt once.
11. Start and close each suppress the prompt; a second browser session does not auto-open it.
12. Sidebar and quick-action entries remain after suppression/completion for normal users and admin My Account.
13. Keyboard, mobile drawer, dark theme, copy fallback, and reduced motion are usable.

- [ ] **Step 5: Refresh graphify and inspect the scoped graph**

```bash
graphify update .
graphify query "How does the beginner getting-started guide connect homepage discovery, dashboard prompt state, API key selection, and shared client configuration?"
```

Expected: the query surfaces the new route, guide store/API, dashboard entry points, and shared configuration module. Review tracked graph changes for generated secrets; none should exist.

- [ ] **Step 6: Check final diff and commit graph artifacts if tracked**

```bash
git status --short
git diff --check
git diff --stat dev...HEAD
```

If `graphify update .` changed tracked `graphify-out` files, stage those exact generated files and commit:

```bash
git add graphify-out
git commit -m "chore: refresh beginner guide graph"
```

If graphify produced no tracked change, do not create an empty commit.

- [ ] **Step 7: Re-run the final changed-file and secret audit**

```bash
rg -n "sk-guide-secret-DO-NOT-PERSIST|apiKey.*localStorage|api_key.*sessionStorage" frontend/src backend graphify-out
git status --short
```

Expected: the test sentinel appears only in the security test source; no production persistence path contains an API key; the worktree is clean.
