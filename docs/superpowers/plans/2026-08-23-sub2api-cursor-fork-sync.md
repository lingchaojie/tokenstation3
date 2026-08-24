# Sub2API Cursor Fork Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the complete Cursor forwarding and credential behavior from the pinned sub2api fork into the latest DEV while capturing caller-facing JSON/SSE through the current local capture lifecycle.

**Architecture:** Port the fork's final Cursor implementation semantically into isolated `pkg/cursor`, credential/control-plane, and gateway layers. Keep Connect/Protobuf private to the upstream transport, convert all output back to the caller's Chat Completions, Responses, or Messages protocol, and feed those delivered bytes into DEV's typed capture attempt.

**Tech Stack:** Go 1.x, Gin, net/http HTTP/2, hand-written Protobuf/Connect framing, PostgreSQL migrations, Redis-backed token cache/refresh locks, Vue 3, TypeScript, Pinia, Vitest, pnpm.

**Spec:** `docs/superpowers/specs/2026-08-23-sub2api-cursor-fork-sync-design.md`

## Global Constraints

- Start from `origin/dev@f768645be81754a170eaa48b8dd889692ef40473`; fetch `origin/dev` immediately before Task 1 and rebase this branch if it advanced.
- Use `SJwen0/cursor--@3709f0f6c83ed84b62c2a0f7f8e1ff63d6cfb7d4` as the Cursor behavior source. If its `main` ref advances, stop and audit the new range before changing code.
- Do not copy, merge, or inspect implementation files from the local `cursor-channel` or `feat/cursor-channel` experimental worktrees.
- Preserve fork behavior unless the current DEV architecture, capture semantics, or stronger local security constraints require an adaptation.
- Do not add a `connect_proto` capture format. Capture the original caller request and the exact caller-facing JSON/SSE bytes successfully written by the gateway.
- Do not write `model.Final.StopReason`; the current extractor derives finish/stop reason from the captured response bytes.
- Do not add Cursor to `AllowedSchedulingThresholdPlatforms`; Cursor exposes no reliable native usage window.
- Do not add Cursor to channel-monitor provider constraints or UI. No Cursor-specific monitor exists.
- Do not port `backend/cmd/cursor_e2e`, `QUICKSTART.md`, or the fork OpenSpec tree into runtime code.
- Do not call production or a real production account. A live Cursor smoke test requires separate explicit approval and must use the application's configured proxy path.
- Write a failing deterministic test before each production change, then make only the change required for that task.
- Commit after every task with the exact task-scoped files; never mix generated artifacts or unrelated formatting into a commit.

## File and Interface Map

- `backend/internal/pkg/cursor/`: upstream-only Protobuf, Connect framing, auth/session helpers, model parsing, request encoding, response decoding, and HTTP/2 stream lifecycle.
- `backend/internal/repository/cursor_oauth_client.go`: official Cursor credential endpoint HTTP client.
- `backend/internal/service/cursor_*.go`: credential lifecycle, observed models, transport configuration, translation, delivery, retry, and capture integration.
- `backend/internal/service/openai_gateway_cursor*.go`: protocol-neutral Cursor turn plus Chat/Responses/Messages encoders.
- `backend/internal/handler/admin/cursor_oauth_handler.go`: admin credential and account import endpoints.
- `backend/migrations/231_add_cursor_platform.sql`: additive quota constraint update for the current DEV schema.
- `frontend/src/api/admin/cursor.ts` and `frontend/src/composables/useCursorOAuth.ts`: typed admin client and browser authorization state machine.
- Existing account, scheduler, handler, route, Wire, and frontend catalog files: add Cursor without replacing newer DEV platforms or capture behavior.

---

### Task 1: Refresh the Baseline and Register the Cursor Platform

**Files:**
- Create: `backend/internal/service/cursor_platform_test.go`
- Create: `backend/migrations/231_add_cursor_platform.sql`
- Create: `backend/migrations/cursor_platform_migration_test.go`
- Modify: `backend/internal/domain/constants.go`
- Modify: `backend/internal/service/domain_constants.go`
- Modify: `backend/internal/model/error_passthrough_rule.go`
- Modify: `backend/ent/schema/user_platform_quota.go`

**Interfaces:**
- Produces: `domain.PlatformCursor`, `service.PlatformCursor`, quota eligibility, error-passthrough eligibility, and a database constraint that accepts all current platforms plus `cursor`.
- Invariant: `PlatformCursor` is absent from `AllowedSchedulingThresholdPlatforms`.

- [ ] **Step 1: Refresh both remote baselines before touching code**

Run:

```bash
git fetch origin dev
git fetch https://github.com/SJwen0/cursor--.git main:refs/remotes/cursor-fork/main
git rebase origin/dev
git rev-parse origin/dev cursor-fork/main
```

Expected: the worktree is clean after rebase; `cursor-fork/main` is `3709f0f6c83ed84b62c2a0f7f8e1ff63d6cfb7d4`. If that fork SHA differs, stop this plan and audit the new commits first.

- [ ] **Step 2: Write failing platform and migration tests**

```go
func TestCursorPlatformRegistration(t *testing.T) {
	require.Equal(t, "cursor", PlatformCursor)
	require.Contains(t, AllowedQuotaPlatforms, PlatformCursor)
	require.NotContains(t, AllowedSchedulingThresholdPlatforms, PlatformCursor)
	require.Contains(t, model.AllPlatforms(), PlatformCursor)
}

func TestCursorPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("231_add_cursor_platform.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "'deepseek', 'cursor'")
	require.NotContains(t, sql, "channel_monitors_provider_check")
}
```

- [ ] **Step 3: Run the focused tests and verify failure**

Run: `cd backend && go test ./internal/service ./migrations -run 'CursorPlatform' -count=1`

Expected: FAIL because `PlatformCursor` and migration `231_add_cursor_platform.sql` do not exist.

- [ ] **Step 4: Add the platform constants and additive constraint**

```go
// internal/domain/constants.go
const PlatformCursor = "cursor"

// internal/service/domain_constants.go
const PlatformCursor = domain.PlatformCursor

var AllowedQuotaPlatforms = []string{
	PlatformAnthropic, PlatformOpenAI, PlatformKiro, PlatformGemini,
	PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu,
	PlatformDeepseek, PlatformCursor,
}
```

Use this exact SQL platform set in migration 231 and preserve every current DEV value:

```sql
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'kiro', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'cursor'));
```

Add `cursor` to the Ent validator and `model.AllPlatforms()`. Do not alter monitor constraints or create the fork's removed `composite_model_routes` constraint.

- [ ] **Step 5: Run tests and commit**

Run: `cd backend && go test ./internal/service ./internal/model ./ent/schema ./migrations -run 'Cursor|Platform' -count=1`

Expected: PASS.

```bash
git add backend/internal/domain/constants.go backend/internal/service/domain_constants.go backend/internal/service/cursor_platform_test.go backend/internal/model/error_passthrough_rule.go backend/ent/schema/user_platform_quota.go backend/migrations/231_add_cursor_platform.sql backend/migrations/cursor_platform_migration_test.go
git commit -m "feat(cursor): register platform and quota schema"
```

### Task 2: Add Bounded Protobuf and Connect Envelope Primitives

**Files:**
- Create: `backend/internal/pkg/cursor/proto.go`
- Create: `backend/internal/pkg/cursor/proto_test.go`
- Create: `backend/internal/pkg/cursor/envelope.go`
- Create: `backend/internal/pkg/cursor/envelope_test.go`

**Interfaces:**
- Produces: `Writer`, `Fields`, `Decode`, `EncodeFrame`, `Frame`, `FrameReader`, and `NewFrameReader`.
- Safety bounds: 64 MiB encoded frame and 64 MiB decompressed frame; malformed and truncated inputs return errors without panics.

- [ ] **Step 1: Write failing framing and Protobuf tests**

```go
func TestEncodeFrameRoundTrip(t *testing.T) {
	want := []byte("payload")
	frame, err := NewFrameReader(bytes.NewReader(EncodeFrame(want, false))).Next()
	require.NoError(t, err)
	require.Equal(t, want, frame.Payload)
	require.False(t, frame.EndStream)
}

func TestDecodeRejectsTruncatedLengthDelimitedField(t *testing.T) {
	_, err := Decode([]byte{0x0a, 0x05, 'a'})
	require.Error(t, err)
}
```

Add cases for flags `0x00`, `0x01`, `0x02`, and `0x03`, gzip expansion beyond 64 MiB, invalid wire types, varint overflow, fixed64 writing, repeated bytes, and recursive `google.protobuf.Value` depth.

- [ ] **Step 2: Verify the package fails to compile**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestEncodeFrame|TestDecode' -count=1`

Expected: FAIL because the package and symbols are absent.

- [ ] **Step 3: Port the final bounded primitives**

```go
const maxFrameSize = 64 << 20
const maxDecompressedFrameSize = 64 << 20

type Frame struct {
	Compressed bool
	EndStream  bool
	Payload    []byte
}

type Writer struct{ buf []byte }
type Value struct {
	WireType int
	Varint   uint64
	Bytes    []byte
}
type Fields map[int][]Value
```

Implement big-endian 5-byte Connect framing, bounded gzip decoding, strict truncation checks, Protobuf varint/tag handling, repeated fields, and deterministic map-key encoding exactly as the pinned fork.

- [ ] **Step 4: Run package tests and commit**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestEncodeFrame|TestFrameReader|TestDecode|TestWrite' -count=1`

Expected: PASS.

```bash
git add backend/internal/pkg/cursor/proto.go backend/internal/pkg/cursor/proto_test.go backend/internal/pkg/cursor/envelope.go backend/internal/pkg/cursor/envelope_test.go
git commit -m "feat(cursor): add protobuf and Connect framing"
```

### Task 3: Encode Agent Requests and Decode Agent Events

**Files:**
- Create: `backend/internal/pkg/cursor/agent_const.go`
- Create: `backend/internal/pkg/cursor/agent_const_test.go`
- Create: `backend/internal/pkg/cursor/agent_request.go`
- Create: `backend/internal/pkg/cursor/agent_request_test.go`
- Create: `backend/internal/pkg/cursor/agent_response.go`
- Create: `backend/internal/pkg/cursor/agent_response_test.go`
- Create: `backend/internal/pkg/cursor/image.go`
- Create: `backend/internal/pkg/cursor/image_test.go`
- Create: `backend/internal/pkg/cursor/tools_test.go`

**Interfaces:**
- Produces: `AgentRunParams`, `AgentTool`, `AgentImage`, `FramePlan`, `BuildRunFrameSequence`, `AgentEvent`, `AgentUsage`, `AgentError`, `ParseAgentServerMessage`, and `ParseAgentTrailer`.
- Consumes: Task 2 Protobuf and envelope primitives.

- [ ] **Step 1: Write failing final-fork parity tests**

```go
func TestBuildRunFrameSequencePacing(t *testing.T) {
	plans := BuildRunFrameSequence(AgentRunParams{Prompt: "hello", MessageID: "m", ConversationID: "c"})
	require.Equal(t, "run_request", plans[0].Label)
	require.Equal(t, "request_context_env", plans[1].Label)
	require.Equal(t, "exec_stream_close", plans[2].Label)
	require.Len(t, plans, 12)
}

func TestParseAgentServerMessageMcpToolCall(t *testing.T) {
	event, err := ParseAgentServerMessage(mcpArgsPayload(t, "weather", "weather", "call-1", map[string]any{"city": "Paris"}))
	require.NoError(t, err)
	require.Equal(t, AgentEventToolCall, event.Type)
	require.Equal(t, `{"city":"Paris"}`, event.ToolCall.Arguments)
}
```

Add assertions for exactly ten CLI headers, default version `cli-2026.08.11-e8db854`, system prompt, max mode, native MCP schemas, inline image bytes, text/thinking/token/usage events, Connect error mapping, unknown oneof arms, and recursion bounds.

- [ ] **Step 2: Verify tests fail**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestBuildRunFrameSequence|TestBuildAgentHeaders|TestParseAgent|TestParseImage' -count=1`

Expected: FAIL because agent codecs are absent.

- [ ] **Step 3: Port the final agent contracts**

```go
type AgentRunParams struct {
	Prompt         string
	Model          string
	MaxMode        bool
	SystemPrompt   string
	Mode           int32
	Tools          []AgentTool
	Images         []AgentImage
	ConversationID string
	Cwd            string
	MessageID      string
	Env            AgentEnv
}

type AgentEvent struct {
	Type     AgentEventType
	Text     string
	ToolCall *AgentToolCall
	Usage    *AgentUsage
	Err      error
}
```

Preserve the captured request sequence: Run request, environment, exec close, KV ack 0 plus ack 1 through 8. Encode tool schemas as `google.protobuf.Value`; reject data-URI images above 16 MiB; decode full MCP calls and all final-fork event enums.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestBuildAgent|TestEncodeAgent|TestParseAgent|TestConnectCode|TestParseImage|TestDefaultCLI' -count=1`

Expected: PASS.

```bash
git add backend/internal/pkg/cursor/agent_const.go backend/internal/pkg/cursor/agent_const_test.go backend/internal/pkg/cursor/agent_request.go backend/internal/pkg/cursor/agent_request_test.go backend/internal/pkg/cursor/agent_response.go backend/internal/pkg/cursor/agent_response_test.go backend/internal/pkg/cursor/image.go backend/internal/pkg/cursor/image_test.go backend/internal/pkg/cursor/tools_test.go
git commit -m "feat(cursor): add agent request and event codecs"
```

### Task 4: Implement the HTTP/2 Bidirectional Agent Stream

**Files:**
- Create: `backend/internal/pkg/cursor/agent_stream.go`
- Create: `backend/internal/pkg/cursor/agent_stream_test.go`

**Interfaces:**
- Produces: `AgentStreamOptions`, `AgentStream`, `OpenAgentStream`, `Events`, `Response`, and `Close`.
- Consumes: Task 3 frame plans, headers, and decoded events.

- [ ] **Step 1: Write failing stream lifecycle tests**

```go
func TestAgentStreamExplicitEndBeatsIdleTimeout(t *testing.T) {
	s := newReaderStream(bytes.NewReader(trailerFrame(`{}`)), AgentStreamOptions{IdleTimeout: time.Hour})
	events := drainEvents(t, s)
	require.Equal(t, AgentEventTurnEnded, events[len(events)-1].Type)
}

func TestAgentStreamParallelToolCallsDrainTogether(t *testing.T) {
	body := bytes.Join([][]byte{toolFrame("call-1"), toolFrame("call-2")}, nil)
	s := newReaderStream(bytes.NewReader(body), AgentStreamOptions{ToolCallDrainWindow: 20 * time.Millisecond})
	events := drainEvents(t, s)
	require.Len(t, filterToolCalls(events), 2)
}
```

Cover HTTP/2 enforcement, non-2xx bounded bodies, late response cleanup, first-byte timeout, 30-second default idle timeout, heartbeats, clean EOF, explicit trailers, tool drain, and cancellation.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestAgentStream' -count=1`

Expected: FAIL because `AgentStream` is absent.

- [ ] **Step 3: Implement the stream with fork timing**

```go
type AgentStreamOptions struct {
	BaseURL                 string
	Token                   string
	ClientVersion           string
	GhostMode               bool
	RequestID               string
	HTTPClient              *http.Client
	FirstByteTimeout        time.Duration
	IdleTimeout             time.Duration
	HeartbeatInterval       time.Duration
	KeepReadingAfterToolCall bool
	ToolCallDrainWindow     time.Duration
	AllowHTTP1              bool
	OnRequestFrame          func(AgentFrameInfo)
	OnResponseFrame         func(AgentFrameInfo, *Frame)
}
```

Use an `io.Pipe` request body, pace every `FramePlan`, keep the request half open with heartbeats, require negotiated HTTP/2 outside tests, reset activity on every response frame, and translate terminal read conditions exactly as the pinned fork.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestAgentStream|TestOpenAgentStream' -count=1`

Expected: PASS with no goroutine leaks under `go test -race` for this package.

```bash
git add backend/internal/pkg/cursor/agent_stream.go backend/internal/pkg/cursor/agent_stream_test.go
git commit -m "feat(cursor): add HTTP2 agent stream"
```

### Task 5: Add Cursor Authentication, Session, and Model Helpers

**Files:**
- Create: `backend/internal/pkg/cursor/auth.go`
- Create: `backend/internal/pkg/cursor/auth_test.go`
- Create: `backend/internal/pkg/cursor/oauth.go`
- Create: `backend/internal/pkg/cursor/oauth_test.go`
- Create: `backend/internal/pkg/cursor/session.go`
- Create: `backend/internal/pkg/cursor/session_test.go`
- Create: `backend/internal/pkg/cursor/models.go`
- Create: `backend/internal/pkg/cursor/models_test.go`

**Interfaces:**
- Produces: `BuildHeaders`, `ParseToken`, `TokenClaims`, `JWTExpiry`, `IsUserAPIKey`, `BuildDeepLoginURL`, `ExchangeWebSessionWithOptions`, `Model`, `ParseAvailableModelsResponse`, and `DefaultModelIDs`.
- Security: no raw token is returned in formatted errors.

- [ ] **Step 1: Write failing credential and model tests**

```go
func TestParseTokenAcceptsEncodedCookie(t *testing.T) {
	token, uid := ParseToken("user_1%3A%3Aaaa.bbb.ccc")
	require.Equal(t, "aaa.bbb.ccc", token)
	require.Equal(t, "user_1", uid)
}

func TestTokenResponseAcceptsSnakeAndCamelCase(t *testing.T) {
	var got TokenResponse
	require.NoError(t, json.Unmarshal([]byte(`{"access_token":"a","refreshToken":"r","expires_in":3600}`), &got))
	require.Equal(t, "a", got.AccessToken)
	require.Equal(t, "r", got.RefreshToken)
	require.EqualValues(t, 3600, got.ExpiresIn)
}
```

Add tests for checksum/header identity, JWT claims and expiry, web-session detection, deep-link PKCE, pending poll behavior, `crsr_` detection, model variants, and the exact fallback IDs from the fork.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/pkg/cursor -run 'TestParseToken|TestTokenResponse|TestDeep|TestAvailableModels|TestBuildHeaders' -count=1`

Expected: FAIL because the helpers are absent.

- [ ] **Step 3: Port the final helpers**

```go
const (
	DefaultBaseURL            = "https://api2.cursor.sh"
	EndpointAvailableModels   = "/aiserver.v1.AiService/AvailableModels"
	EndpointExchangeUserAPIKey = "/auth/exchange_user_api_key"
	EndpointOAuthToken        = "/oauth/token"
	EndpointAuthPoll          = "/auth/poll"
)

type TokenResponse struct {
	AccessToken  string
	RefreshToken string
	AuthID       string
	ExpiresIn    int64
}
```

Keep credential endpoints on official hosts, normalize `userId::JWT`, distinguish web and client token claims, bound every response read, and use `AvailableModels` raw Protobuf without a Connect envelope.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/pkg/cursor -count=1`

Expected: PASS.

```bash
git add backend/internal/pkg/cursor/auth.go backend/internal/pkg/cursor/auth_test.go backend/internal/pkg/cursor/oauth.go backend/internal/pkg/cursor/oauth_test.go backend/internal/pkg/cursor/session.go backend/internal/pkg/cursor/session_test.go backend/internal/pkg/cursor/models.go backend/internal/pkg/cursor/models_test.go
git commit -m "feat(cursor): add auth session and model helpers"
```

### Task 6: Add Cursor Account Semantics and Credential Validation

**Files:**
- Create: `backend/internal/service/cursor_account_test.go`
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/admin_account.go`
- Modify: `backend/internal/service/account_credentials_redact.go`
- Modify: `backend/internal/service/account_service.go`
- Modify: `backend/internal/service/account_usage_service.go`
- Modify: `backend/internal/repository/account_repo.go`
- Modify: `backend/internal/repository/account_repo_temp_unsched_test.go`

**Interfaces:**
- Produces: `Account.IsCursor`, `IsCursorOAuth`, `GetCursorAccessToken`, `GetCursorRefreshToken`, `GetCursorAPIKey`, `GetCursorWebSessionToken`, `GetCursorBaseURL`, `HasExplicitModelMapping`, and `CursorTokenCacheKey` consumers.
- Rejects: `platform=cursor,type=apikey` on create and update.

- [ ] **Step 1: Write failing account tests**

```go
func TestCursorAccountRejectsAPIKeyType(t *testing.T) {
	err := validateCursorAccountType(PlatformCursor, AccountTypeAPIKey)
	require.Error(t, err)
	require.NoError(t, validateCursorAccountType(PlatformCursor, AccountTypeOAuth))
}

func TestCursorWebSessionFallback(t *testing.T) {
	a := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": webJWT(t)}}
	require.Equal(t, webJWT(t), a.GetCursorWebSessionToken())
}
```

Cover default base URL, identity fallback model mapping, endpoint capabilities, sensitive `web_session_token`, and alternate background-refresh credential sources.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service ./internal/repository -run 'CursorAccount|CursorWebSession|Cursor.*RefreshCandidate' -count=1`

Expected: FAIL because Cursor account helpers are absent.

- [ ] **Step 3: Add exact account behavior**

```go
func (a *Account) IsCursor() bool {
	return a != nil && a.Platform == PlatformCursor
}

func (a *Account) IsCursorOAuth() bool {
	return a.IsCursor() && a.Type == AccountTypeOAuth
}

func validateCursorAccountType(platform, accountType string) error {
	if platform == PlatformCursor && accountType == AccountTypeAPIKey {
		return infraerrors.New(http.StatusBadRequest, "CURSOR_APIKEY_ACCOUNT_UNSUPPORTED", "import a crsr_ credential through the Cursor login flow")
	}
	return nil
}
```

Use the fork's final credential getters and source-normalization rules. Preserve all newer DEV account modes and platform branches while inserting Cursor additively.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service ./internal/repository -run 'Cursor|RefreshCandidate|SensitiveCredential' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/cursor_account_test.go backend/internal/service/account.go backend/internal/service/admin_account.go backend/internal/service/account_credentials_redact.go backend/internal/service/account_service.go backend/internal/service/account_usage_service.go backend/internal/repository/account_repo.go backend/internal/repository/account_repo_temp_unsched_test.go
git commit -m "feat(cursor): add account credential semantics"
```

### Task 7: Implement Cursor OAuth and Credential Import Services

**Files:**
- Create: `backend/internal/repository/cursor_oauth_client.go`
- Create: `backend/internal/repository/cursor_oauth_client_test.go`
- Create: `backend/internal/service/cursor_oauth_service.go`
- Create: `backend/internal/service/cursor_oauth_service_test.go`
- Modify: `backend/internal/util/logredact/redact.go`
- Modify: `backend/internal/util/logredact/redact_test.go`
- Modify: `backend/internal/service/audit_log.go`
- Modify: `backend/internal/service/audit_log_test.go`
- Modify: `backend/internal/server/middleware/audit_log.go`
- Modify: `backend/internal/server/middleware/audit_log_test.go`

**Interfaces:**
- Produces: `CursorOAuthClient`, `CursorOAuthTokenService`, `CursorTokenInfo`, `CursorOAuthService`, `NormalizeCursorReauthorizedCredentials`.
- Credential sources: deep-link PKCE, `crsr_` exchange, and Workos web-session import/upgrade.

- [ ] **Step 1: Write failing service and redaction tests**

```go
func TestCursorOAuthServiceImportAPIKey(t *testing.T) {
	client := &fakeCursorOAuthClient{exchange: &cursor.TokenResponse{AccessToken: clientJWT(t), ExpiresIn: 3600}}
	svc := NewCursorOAuthService(nil, client)
	info, err := svc.ImportFromAPIKey(context.Background(), "crsr_secret", nil)
	require.NoError(t, err)
	require.Equal(t, cursor.CredentialSourceAPIKey, info.Source)
	require.Equal(t, "crsr_secret", info.APIKey)
}

func TestCursorDeepLinkCanaryIsRedacted(t *testing.T) {
	raw := `{"state":"CURSOR_TOKEN_CANARY","verifier":"secret"}`
	require.NotContains(t, logredact.String(raw), "CURSOR_TOKEN_CANARY")
}
```

Cover pending poll, official auth hosts despite custom forwarding base URL, proxy selection, response size limits, refresh-source replacement, web-session retention for re-upgrade, and non-sensitive client errors.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/repository ./internal/service ./internal/util/logredact ./internal/server/middleware -run 'CursorOAuth|CursorDeepLink' -count=1`

Expected: FAIL because the OAuth service and redaction cases are absent.

- [ ] **Step 3: Implement the service contracts**

```go
type CursorOAuthClient interface {
	ExchangeUserAPIKey(context.Context, string, string) (*cursor.TokenResponse, error)
	RefreshToken(context.Context, string, string) (*cursor.TokenResponse, error)
	ExchangeWebSession(context.Context, string, string) (*cursor.TokenResponse, error)
	PollDeepLink(context.Context, string, string, string) (*cursor.TokenResponse, error)
}

type CursorTokenInfo struct {
	AccessToken  string
	RefreshToken string
	APIKey       string
	WebSession   string
	ExpiresAt    time.Time
	Source       string
}
```

Implement bounded HTTP clients, proxy lookup, one-hour default access TTL, credential-map construction, and mutually exclusive refresh-source normalization exactly as the fork's final state. Add deep-link/session fields to log and audit redaction.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/repository ./internal/service ./internal/util/logredact ./internal/server/middleware -run 'CursorOAuth|CursorDeepLink|Redact' -count=1`

Expected: PASS.

```bash
git add backend/internal/repository/cursor_oauth_client.go backend/internal/repository/cursor_oauth_client_test.go backend/internal/service/cursor_oauth_service.go backend/internal/service/cursor_oauth_service_test.go backend/internal/util/logredact/redact.go backend/internal/util/logredact/redact_test.go backend/internal/service/audit_log.go backend/internal/service/audit_log_test.go backend/internal/server/middleware/audit_log.go backend/internal/server/middleware/audit_log_test.go
git commit -m "feat(cursor): add OAuth credential imports"
```

### Task 8: Integrate Token Caching, Refresh, and Invalidation

**Files:**
- Create: `backend/internal/service/cursor_token_provider.go`
- Create: `backend/internal/service/cursor_token_provider_test.go`
- Create: `backend/internal/service/cursor_token_refresher.go`
- Create: `backend/internal/service/cursor_credential_failure.go`
- Modify: `backend/internal/service/token_refresh_service.go`
- Modify: `backend/internal/service/token_cache_invalidator.go`
- Modify: `backend/internal/service/token_cache_invalidator_test.go`
- Modify: `backend/internal/service/refresh_policy.go`

**Interfaces:**
- Produces: `CursorTokenProvider.GetAccessToken`, `InvalidateToken`, `CursorTokenRefresher`, and Cursor credential failover classifications.
- Consumes: Task 6 account getters and Task 7 OAuth service.

- [ ] **Step 1: Write failing lifecycle tests**

```go
func TestCursorTokenProviderRejectedFingerprintForcesRotation(t *testing.T) {
	provider, account := cursorProviderHarness(t, rejectedJWT(t), refreshedJWT(t))
	require.NoError(t, provider.InvalidateToken(context.Background(), account))
	got, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, refreshedJWT(t), got)
}

func TestCursorTokenRefresherReexchangesAPIKey(t *testing.T) {
	r := NewCursorTokenRefresher(&fakeCursorTokenService{token: &CursorTokenInfo{AccessToken: clientJWT(t)}})
	creds, err := r.Refresh(context.Background(), cursorAPIKeyAccount())
	require.NoError(t, err)
	require.Equal(t, clientJWT(t), creds["access_token"])
}
```

Cover valid cache hits, JWT skew, browser-token upgrade, distributed lock wait/backoff, refresh error policy, same rejected token rejection, invalidation keys, and background refresh candidates without `refresh_token`.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service -run 'CursorToken|CursorCredential|CompositeTokenCacheInvalidator' -count=1`

Expected: FAIL because Cursor token types are absent.

- [ ] **Step 3: Implement provider and refresher**

```go
type CursorTokenProvider struct {
	accountRepo   AccountRepository
	tokenCache    GeminiTokenCache
	refreshAPI    *OAuthRefreshAPI
	executor      OAuthRefreshExecutor
	refreshPolicy ProviderRefreshPolicy
}

func CursorTokenCacheKey(account *Account) string {
	if account == nil {
		return ""
	}
	return fmt.Sprintf("cursor:%d", account.ID)
}
```

Use rejected-token SHA-256 fingerprints, cache polling while another worker holds the refresh lock, web-token upgrade before chat, and provider-scoped stop only for missing provider configuration. Register Cursor in refresh and composite invalidation switches without changing other providers.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service -run 'CursorToken|CursorCredential|Refresh|TokenCacheInvalidator' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/cursor_token_provider.go backend/internal/service/cursor_token_provider_test.go backend/internal/service/cursor_token_refresher.go backend/internal/service/cursor_credential_failure.go backend/internal/service/token_refresh_service.go backend/internal/service/token_cache_invalidator.go backend/internal/service/token_cache_invalidator_test.go backend/internal/service/refresh_policy.go
git commit -m "feat(cursor): add token lifecycle and invalidation"
```

### Task 9: Discover Observed Models and Test Accounts Safely

**Files:**
- Create: `backend/internal/service/cursor_observed_models.go`
- Create: `backend/internal/service/cursor_observed_models_test.go`
- Modify: `backend/internal/service/account_test_service.go`
- Create: `backend/internal/service/account_test_service_cursor_test.go`
- Modify: `backend/internal/service/gateway_service.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_codex_models_handler_test.go`

**Interfaces:**
- Produces: `CursorObservedModelIDs`, `CursorObservedModelSet`, `CursorModelObserved`, background six-hour model sync, and Cursor `AvailableModels` account test.
- Consumes: `CursorTokenProvider` and `HTTPUpstream` with account proxy resolution.

- [ ] **Step 1: Write failing observed-model and account-test tests**

```go
func TestCursorObservedModelsAreAuthoritative(t *testing.T) {
	extra := map[string]any{"cursor_observed_models": map[string]any{"models": []any{"auto", "gpt-5"}, "fetched_at": time.Now().UTC().Format(time.RFC3339)}}
	require.Equal(t, []string{"auto", "gpt-5"}, CursorObservedModelIDs(extra))
	require.True(t, CursorModelObserved(CursorObservedModelSet(extra), "default"))
}

func TestCursorAccountTestUsesAvailableModels(t *testing.T) {
	h := newCursorAccountTestHarness(t)
	require.NoError(t, h.service.testCursorAccountConnection(h.context, h.account))
	require.Equal(t, cursor.EndpointAvailableModels, h.request.URL.Path)
}
```

Assert that a configured-but-unresolved proxy fails closed, a web token fails the chat-readiness test even if models succeed, observed models enter `/v1/models`, and fallback IDs are used only without an observed snapshot.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'CursorObserved|CursorAccountTest|Cursor.*Models' -count=1`

Expected: FAIL because observed-model integration is absent.

- [ ] **Step 3: Implement discovery and safe probing**

```go
type cursorObservedModelsSnapshot struct {
	Models    []string `json:"models"`
	FetchedAt string   `json:"fetched_at"`
	Source    string   `json:"source,omitempty"`
}

const cursorObservedModelsTTL = 6 * time.Hour
```

Call api2 `AvailableModels` with a 1 MiB response limit, store `Extra.cursor_observed_models`, deduplicate IDs, merge enabled-account observations into public/admin model lists, and keep the test path away from Anthropic and billed chat endpoints.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'CursorObserved|CursorAccount|AvailableModels|Models' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/cursor_observed_models.go backend/internal/service/cursor_observed_models_test.go backend/internal/service/account_test_service.go backend/internal/service/account_test_service_cursor_test.go backend/internal/service/gateway_service.go backend/internal/handler/gateway_handler.go backend/internal/handler/openai_codex_models_handler_test.go
git commit -m "feat(cursor): add observed models and account probe"
```

### Task 10: Translate Requests, Tools, Images, Models, and Token Limits

**Files:**
- Create: `backend/internal/service/openai_gateway_cursor_translate.go`
- Create: `backend/internal/service/openai_gateway_cursor_translate_test.go`

**Interfaces:**
- Produces: `buildCursorAgentRun`, `buildCursorAgentRunParams`, `cursorAgentWireModel`, `planCursorAgentTools`, `cursorAgentMessageParts`, `cursorInputEstimate`, and `cursorRequestOutputLimit`.
- Consumes: current `apicompat.ChatCompletionsRequest` and Task 3 `cursor.AgentRunParams`.

- [ ] **Step 1: Write failing translation tests**

```go
func TestBuildCursorAgentRunFlattensHistoryAndToolResults(t *testing.T) {
	req := cursorToolRoundTripRequest(t)
	params, estimate, err := buildCursorAgentRun(cursorAccount(), "auto", req)
	require.NoError(t, err)
	require.Contains(t, params.Prompt, "Tool result (call-1):")
	require.NotEmpty(t, estimate.text)
}

func TestCursorAgentWireModelUsesObservedThinkingVariant(t *testing.T) {
	model, maxMode := cursorAgentWireModel("gpt-5-thinking", "high", []string{"gpt-5-thinking"})
	require.Equal(t, "gpt-5-thinking", model)
	require.False(t, maxMode)
}
```

Cover single-user prompt preservation, multi-role labels, native tools, tool-choice none/required/named, schema deduplication, data-URI image decoding, remote-image text fallback, `auto` to `default`, `-max`, observed thinking variants, and both output-limit fields.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service -run 'BuildCursorAgent|CursorAgentWireModel|CursorRequestOutputLimit|CursorImage' -count=1`

Expected: FAIL because the translator is absent.

- [ ] **Step 3: Implement the normalized translation types**

```go
type cursorInputEstimate struct {
	text        string
	imageTokens int
}

type cursorToolPlan struct {
	declarations []cursor.AgentTool
	instruction  string
}

func cursorRequestOutputLimit(req *apicompat.ChatCompletionsRequest) int {
	if req != nil && req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		return *req.MaxCompletionTokens
	}
	if req != nil && req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens
	}
	return 0
}
```

Port the fork's final prompt, image, model, tool, and estimation logic. Do not run OpenAI Codex model normalization on Cursor model IDs.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service -run 'Cursor.*Translate|BuildCursorAgent|CursorAgentWireModel|CursorRequestOutputLimit|DataURI' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/openai_gateway_cursor_translate.go backend/internal/service/openai_gateway_cursor_translate_test.go
git commit -m "feat(cursor): translate requests to agent turns"
```

### Task 11: Add Proxy-Aware Transport and Failure Classification

**Files:**
- Create: `backend/internal/service/openai_gateway_cursor_transport.go`
- Create: `backend/internal/service/openai_gateway_cursor_transport_test.go`
- Create: `backend/internal/service/openai_gateway_cursor.go`
- Create: `backend/internal/service/openai_gateway_cursor_stream_test.go`
- Create: `backend/internal/service/openai_gateway_cursor_bridges_test.go`
- Modify: `backend/internal/service/openai_gateway_service.go`

**Interfaces:**
- Produces: `cursorAgentHTTPClient`, `validateCursorAgentHost`, `openCursorAgentStream`, `cursorAgentFailure`, `consumeCursorAgentEvents`, `cursorChatOutcome`, and `resolveCursorUsage`.
- Consumes: Tasks 4, 8, 9, and 10.

- [ ] **Step 1: Write failing transport and failure tests**

```go
func TestCursorAgentHTTPClientFailsClosedOnUnresolvedProxy(t *testing.T) {
	proxyID := int64(9)
	_, err := cursorAgentHTTPClient(&Account{Platform: PlatformCursor, ProxyID: &proxyID})
	require.ErrorIs(t, err, errCursorAgentProxyUnresolved)
}

func TestCursorAgentFailureTransportRetriesSameAccount(t *testing.T) {
	err := (&OpenAIGatewayService{}).cursorAgentFailure(ginTestContext(), cursorAccount(), io.ErrUnexpectedEOF)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.RetryableOnSameAccount)
}
```

Cover per-proxy HTTP/2 client reuse and eviction, unsupported proxy schemes, SSRF/host allowlists, account/env defaults, cancellation not retried, auth invalidation, 429 switching without quarantine, client-version provider stop, mid-stream partial usage, local `max_tokens`, and parallel tool indexes.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service -run 'CursorAgentHTTPClient|ValidateCursorAgentHost|CursorAgentFailure|ConsumeCursorAgentEvents|ResolveCursorUsage' -count=1`

Expected: FAIL because transport and Cursor turn execution are absent.

- [ ] **Step 3: Implement transport and terminal outcome**

```go
type cursorChatOutcome struct {
	content          string
	reasoning        string
	toolCalls        []apicompat.ChatToolCall
	finishReason     string
	firstTokenMs     *int
	usage            *cursor.AgentUsage
	truncated        bool
	providerTerminal bool
}
```

Use the fork's credential/extra/env precedence, proxy client cache, allowlist validation, 30-second idle behavior, same-account transient retry, and Connect verdict mapping. Set `providerTerminal` only after `AgentEventTurnEnded`; a local token-limit cut or mid-stream read error must not claim a native provider terminal.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service -run 'CursorAgent|ConsumeCursor|ResolveCursor|CursorFitText|CursorUpstream' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/openai_gateway_cursor_transport.go backend/internal/service/openai_gateway_cursor_transport_test.go backend/internal/service/openai_gateway_cursor.go backend/internal/service/openai_gateway_cursor_stream_test.go backend/internal/service/openai_gateway_cursor_bridges_test.go backend/internal/service/openai_gateway_service.go
git commit -m "feat(cursor): add transport retry and turn execution"
```

### Task 12: Deliver Native Chat Completions JSON and SSE

**Files:**
- Modify: `backend/internal/service/openai_gateway_cursor.go`
- Create: `backend/internal/service/openai_gateway_cursor_delivery.go`
- Create: `backend/internal/service/openai_gateway_cursor_chat_test.go`

**Interfaces:**
- Produces: `forwardCursorChatCompletions`, `streamCursorChatCompletions`, `bufferCursorChatCompletions`, `cursorChatCompletionsResponse`, and protocol-valid in-band stream errors.
- Consumes: Task 11 normalized deltas and outcome.

- [ ] **Step 1: Write failing buffered and streaming tests**

```go
func TestCursorChatStreamingDeliversNativeSSE(t *testing.T) {
	result, body := runCursorChatWithEvents(t, true, textEvent("hello"), turnEndedEvent(7, 2))
	require.Contains(t, body, `"object":"chat.completion.chunk"`)
	require.Contains(t, body, "data: [DONE]\n\n")
	require.Equal(t, 7, result.Usage.InputTokens)
	require.True(t, result.CaptureResponseComplete)
}

func TestCursorChatMidStreamErrorHasNoFalseFinish(t *testing.T) {
	result, body := runCursorChatWithEvents(t, true, textEvent("partial"), errorEvent(io.ErrUnexpectedEOF))
	require.NotContains(t, body, `"finish_reason":"stop"`)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
}
```

Also assert reasoning content, tool calls, `length`, buffered JSON, first-byte withholding, client-disconnect draining, and exact `OpenAIForwardResult` terminal flags.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service -run 'CursorChat' -count=1`

Expected: FAIL because delivery methods are absent or terminal flags are incomplete.

- [ ] **Step 3: Implement Chat delivery**

```go
func cursorChatCompletionsResponse(model string, outcome cursorChatOutcome, usage OpenAIUsage) *apicompat.ChatCompletionsResponse {
	return &apicompat.ChatCompletionsResponse{
		ID: "chatcmpl-" + uuid.NewString(), Object: "chat.completion", Created: time.Now().Unix(), Model: model,
		Choices: []apicompat.ChatChoice{{Index: 0, Message: cursorAssistantMessage(outcome), FinishReason: outcome.finishReason}},
		Usage: &apicompat.ChatUsage{PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.InputTokens + usage.OutputTokens},
	}
}
```

Create the delivery helper now and keep every Cursor body write behind it:

```go
func writeCursorDeliveryBytes(c *gin.Context, payload []byte) (int, error) {
	n, err := c.Writer.Write(payload)
	if n > 0 {
		if attempt := captureAttemptForRequest(c); attempt != nil {
			attempt.WriteResponse(payload[:n])
		}
	}
	return n, err
}
```

Before Task 14 starts a Cursor capture attempt this behaves like a direct caller write. Once capture is active, it records exactly the successfully delivered prefix without changing the delivery path.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service -run 'CursorChat|CursorChunk|CursorToolCallDelta' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/openai_gateway_cursor.go backend/internal/service/openai_gateway_cursor_delivery.go backend/internal/service/openai_gateway_cursor_chat_test.go
git commit -m "feat(cursor): deliver Chat Completions output"
```

### Task 13: Bridge Responses and Anthropic Messages

**Files:**
- Create: `backend/internal/service/openai_gateway_cursor_bridges.go`
- Modify: `backend/internal/service/openai_gateway_cursor_bridges_test.go`
- Modify: `backend/internal/service/openai_gateway_cursor_stream_test.go`

**Interfaces:**
- Produces: `cursorChunkSynthesizer`, `forwardCursorResponses`, `forwardCursorAnthropic`, and protocol-specific stream error encoders.
- Consumes: current `apicompat` Chat-to-Responses and Chat-to-Anthropic state machines.

- [ ] **Step 1: Write failing three-protocol parity tests**

```go
func TestCursorRunParamsIdenticalAcrossInboundProtocols(t *testing.T) {
	chat, responses, messages := cursorEquivalentRequests(t)
	require.Equal(t, chat.Prompt, responses.Prompt)
	require.Equal(t, chat.Prompt, messages.Prompt)
	require.Equal(t, chat.Tools, responses.Tools)
	require.Equal(t, chat.Tools, messages.Tools)
}

func TestCursorResponsesMidStreamFailureUsesErrorEvent(t *testing.T) {
	result, body := runCursorResponsesWithEvents(t, textEvent("partial"), errorEvent(io.ErrUnexpectedEOF))
	require.Contains(t, body, "event: error")
	require.NotContains(t, body, "response.completed")
	require.True(t, result.CaptureTerminalError)
}
```

Cover buffered/streaming Responses, buffered/streaming Messages, `max_output_tokens`, required Anthropic `max_tokens`, parallel tools, `tool_use`, `max_tokens`, Responses incomplete details, and no clean terminal after upstream failure.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service -run 'CursorResponses|CursorAnthropic|CursorRunParamsIdentical' -count=1`

Expected: FAIL because bridges are absent.

- [ ] **Step 3: Implement bridge reuse**

```go
type cursorChunkSynthesizer struct {
	completionID string
	created      int64
	model        string
	roleSent     bool
	emit         func(*apicompat.ChatCompletionsChunk)
}
```

Convert inbound requests to Chat Completions, execute the same Cursor turn builder, synthesize Chat chunks, and pass them through current DEV `apicompat` state machines. Serialize only the caller's original protocol.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service ./internal/pkg/apicompat -run 'CursorResponses|CursorAnthropic|CursorRunParams|ChatCompletionsTo' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/openai_gateway_cursor_bridges.go backend/internal/service/openai_gateway_cursor_bridges_test.go backend/internal/service/openai_gateway_cursor_stream_test.go
git commit -m "feat(cursor): bridge Responses and Messages"
```

### Task 14: Integrate Caller-Format Capture

**Files:**
- Create: `backend/internal/service/openai_gateway_cursor_capture.go`
- Create: `backend/internal/service/openai_gateway_cursor_capture_test.go`
- Modify: `backend/internal/service/openai_gateway_cursor_delivery.go`
- Modify: `backend/internal/service/openai_gateway_cursor.go`
- Modify: `backend/internal/service/openai_gateway_cursor_bridges.go`
- Modify: `backend/internal/service/capture_context.go`
- Modify: `backend/internal/service/capture_context_test.go`
- Modify: `backend/internal/service/openai_gateway_typed_capture_test.go`

**Interfaces:**
- Produces: `beginCursorDeliveryCapture` and `markCursorDeliveryResponse`; reuses Task 12's `writeCursorDeliveryBytes`.
- Consumes: current typed `CaptureAttempt`, request ownership slot, `CommitOpenAIForwardCaptureAttempt`, JSON/SSE extractors, and terminal-state fields on `OpenAIForwardResult`.

- [ ] **Step 1: Write failing capture format tests**

```go
type capturedCursorExchange struct {
	Begin   model.Begin
	Final   model.Final
	Record  *CaptureRecord
	Outcome CaptureOutcome
}

func TestCursorCaptureStoresChatDeliveryNotConnectFrames(t *testing.T) {
	capture := runCapturedCursorChat(t, true, textEvent("hello"), turnEndedEvent(5, 1))
	require.Equal(t, model.PayloadSSE, capture.Begin.Format)
	require.JSONEq(t, `{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`, string(capture.Record.RawRequest))
	require.Contains(t, string(capture.Record.RawResponse), `"object":"chat.completion.chunk"`)
	require.NotContains(t, string(capture.Record.RawResponse), "exec_stream_close")
	require.Equal(t, "stop", capture.Record.StopReason)
}

func TestCursorCapturePreservesProviderErrorAfterClientDisconnect(t *testing.T) {
	terminal := runCapturedCursorDisconnect(t, textEvent("partial"), errorEvent(io.ErrUnexpectedEOF))
	require.Equal(t, CaptureOutcomeTerminalError, terminal.Outcome)
	require.False(t, terminal.Final.ResponseComplete)
	require.Empty(t, terminal.Final.StopReason)
}
```

Add one test for each endpoint and mode: Chat JSON/SSE, Responses JSON/SSE, Messages JSON/SSE. Add retry replacement, exact-once commit, redacted inbound Authorization/Cookie, bounded delivered prefix after disconnect, normal provider terminal, local max-token truncation, and capture-disabled zero-allocation guards.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'CursorCapture' -count=1`

Expected: FAIL because Cursor writes bypass typed capture.

- [ ] **Step 3: Begin capture from the caller request**

```go
func (s *OpenAIGatewayService) beginCursorDeliveryCapture(c *gin.Context, account *Account, body []byte, upstreamModel, endpoint string, stream bool) {
	if s == nil || s.cfg == nil || s.capturePool == nil || c == nil || c.Request == nil || account == nil {
		return
	}
	transitionCaptureAttemptOwner(c, captureAttemptOwnerTyped)
	if !s.cfg.Gateway.Capture.Enabled || !CaptureMayApplyFor(c, PlatformCursor) {
		return
	}
	policy, enabled := captureContentPolicyForAttempt(c, PlatformCursor)
	if !enabled {
		return
	}
	format := model.PayloadJSON
	if stream {
		format = model.PayloadSSE
	}
	begin := model.Begin{CaptureID: uuid.New(), CapturedAt: time.Now().UTC(), RequestID: CaptureRequestID(""), SessionID: captureSessionID(c), Platform: PlatformCursor, RequestedModel: captureRequestedModel(c), UpstreamModel: upstreamModel, UpstreamEndpoint: endpoint, Stream: stream, Format: format, Policy: captureModelContentPolicy(policy)}
	attempt, ok := s.capturePool.Begin(c.Request.Context(), begin)
	if !ok {
		return
	}
	attempt.headerLimit = s.cfg.Gateway.Capture.MaxHeaderBytes
	replaceCaptureAttemptForRequest(c, attempt)
	setCaptureAttemptStreamGeometry(c, attempt, stream, true)
	attempt.WriteRequestHeaders(captureHeaderBytes(c.Request.Header, attempt.headerLimit))
	attempt.WriteRequest(body)
}
```

- [ ] **Step 4: Capture only bytes actually delivered**

```go
func writeCursorDeliveryBytes(c *gin.Context, payload []byte) (int, error) {
	n, err := c.Writer.Write(payload)
	if n > 0 {
		if attempt := captureAttemptForRequest(c); attempt != nil {
			attempt.WriteResponse(payload[:n])
		}
	}
	return n, err
}
```

`markCursorDeliveryResponse` must record the caller response status and redacted caller response headers before the first body write. Replace every Cursor `c.JSON`, `fmt.Fprint`, `WriteString`, and raw `Write` with marshal-plus-helper calls. Set `UpstreamFailed`, `CaptureTerminalError`, `CaptureResponseComplete`, and `ClientDisconnect` from the Cursor outcome; leave `model.Final.StopReason` empty so extractors read the delivery bytes.

- [ ] **Step 5: Run capture and existing terminal-state tests**

Run: `cd backend && go test ./internal/service ./internal/handler ./internal/capture/... -run 'CursorCapture|Capture.*Disconnect|ProviderTerminal|StopReason' -count=1`

Expected: PASS; capture records contain no Connect bytes and current disconnect tests remain green.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/openai_gateway_cursor_capture.go backend/internal/service/openai_gateway_cursor_capture_test.go backend/internal/service/openai_gateway_cursor_delivery.go backend/internal/service/openai_gateway_cursor.go backend/internal/service/openai_gateway_cursor_bridges.go backend/internal/service/capture_context.go backend/internal/service/capture_context_test.go backend/internal/service/openai_gateway_typed_capture_test.go
git commit -m "feat(cursor): capture caller protocol delivery"
```

### Task 15: Wire Cursor into Gateway Dispatch, Scheduling, and Billing

**Files:**
- Modify: `backend/internal/service/openai_gateway_chat_completions.go`
- Modify: `backend/internal/service/openai_gateway_forward.go`
- Modify: `backend/internal/service/openai_gateway_messages.go`
- Modify: `backend/internal/service/openai_compatible.go`
- Create: `backend/internal/service/openai_compatible_cursor_test.go`
- Modify: `backend/internal/service/openai_gateway_scheduling.go`
- Modify: `backend/internal/service/openai_profit_control.go`
- Modify: `backend/internal/service/scheduler_snapshot_service.go`
- Modify: `backend/internal/service/group.go`
- Modify: `backend/internal/service/admin_group.go`
- Modify: `backend/internal/service/channel_service.go`
- Modify: `backend/internal/service/account_scheduling_threshold_eval.go`
- Modify: `backend/internal/handler/endpoint.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/handler/openai_quota_platform_contract_test.go`
- Modify: `backend/internal/server/api_contract_test.go`

**Interfaces:**
- Produces: Cursor selection for all three inbound protocols, normal concurrency/usage/billing side effects, quota platform attribution, and model-aware scheduler eligibility.
- Consumes: Tasks 9 through 14.

- [ ] **Step 1: Write failing dispatch and billing tests**

```go
func TestCursorDispatchesAllOpenAIEntrypoints(t *testing.T) {
	h := newCursorGatewayHarness(t, textEvent("ok"), turnEndedEvent(1, 1))
	_, err := h.service.Forward(context.Background(), h.context, h.account, h.responsesBody)
	require.NoError(t, err)
	_, err = h.service.ForwardAsChatCompletions(context.Background(), h.context, h.account, h.chatBody, "", "auto")
	require.NoError(t, err)
	_, err = h.service.ForwardAsAnthropic(context.Background(), h.context, h.account, h.messagesBody, "", "auto")
	require.NoError(t, err)
	require.Equal(t, 3, h.agentOpenCalls)
}

func TestCursorUsesLocalUsageForBilling(t *testing.T) {
	result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 11, OutputTokens: 4}, Model: "auto", BillingModel: "auto"}
	require.True(t, result.HasBillableUsage())
	apiKey := &APIKey{Group: &Group{Platform: PlatformCursor}}
	require.Equal(t, PlatformCursor, QuotaPlatform(context.Background(), apiKey))
}
```

Add scheduler snapshot inclusion, group create/update, profit-control support, channel selection, endpoint labels, failover behavior, no scheduling-threshold eligibility, and API contract coverage.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/service ./internal/handler ./internal/server -run 'Cursor.*Dispatch|Cursor.*Billing|Cursor.*Scheduler|Cursor.*Quota|Cursor.*Group' -count=1`

Expected: FAIL because platform switches do not include Cursor.

- [ ] **Step 3: Add Cursor branches without replacing DEV branches**

```go
if account.Platform == PlatformCursor {
	return s.forwardCursorChatCompletions(ctx, c, account, body, mappedModel)
}
```

Use the equivalent explicit branch in Responses and Messages. Add Cursor to `IsOpenAICompatiblePlatform` and `NormalizeOpenAICompatiblePlatform`, scheduler snapshot platforms, group/profit/channel platform sets, quota attribution, and account capability checks. Preserve Kimi, Zhipu, DeepSeek, Kiro, Grok, WebSocket, compact, image, video, and capture branches already present in DEV.

Place each Cursor branch immediately after the shared attempt/capture-scope initialization and before OpenAI/Codex-specific normalization or protocol conversion. Pass the untouched inbound `body` into `beginCursorDeliveryCapture` so `RawRequest` is the caller representation, while the translator receives a separately parsed value and cannot mutate the captured bytes.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/service ./internal/handler ./internal/server -run 'Cursor|Dispatch|SchedulerSnapshot|QuotaPlatform|ProfitControl|APIContract' -count=1`

Expected: PASS.

```bash
git add backend/internal/service/openai_gateway_chat_completions.go backend/internal/service/openai_gateway_forward.go backend/internal/service/openai_gateway_messages.go backend/internal/service/openai_compatible.go backend/internal/service/openai_compatible_cursor_test.go backend/internal/service/openai_gateway_scheduling.go backend/internal/service/openai_profit_control.go backend/internal/service/scheduler_snapshot_service.go backend/internal/service/group.go backend/internal/service/admin_group.go backend/internal/service/channel_service.go backend/internal/service/account_scheduling_threshold_eval.go backend/internal/handler/endpoint.go backend/internal/handler/openai_gateway_handler.go backend/internal/handler/openai_quota_platform_contract_test.go backend/internal/server/api_contract_test.go
git commit -m "feat(cursor): integrate gateway scheduling and billing"
```

### Task 16: Expose Admin OAuth Routes and Dependency Wiring

**Files:**
- Create: `backend/internal/handler/admin/cursor_oauth_handler.go`
- Create: `backend/internal/handler/admin/cursor_oauth_handler_test.go`
- Modify: `backend/internal/handler/admin/account_handler.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/repository/wire.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: `backend/internal/server/routes/gateway.go`
- Modify: `backend/internal/server/api_contract_test.go`
- Modify: `backend/cmd/server/wire_gen.go`

**Interfaces:**
- Produces admin routes under `/admin/cursor/oauth/*` plus `/admin/cursor/sso-to-oauth`, with bounded concurrency for bulk import.
- Injects `CursorOAuthService`, `CursorTokenProvider`, `CursorTokenRefresher`, and handler dependencies into existing services.

- [ ] **Step 1: Write failing route and handler tests**

```go
func TestCursorOAuthRoutesAreRegistered(t *testing.T) {
	routes := registeredAdminRoutes(t)
	require.Contains(t, routes, "POST /admin/cursor/oauth/auth-url")
	require.Contains(t, routes, "POST /admin/cursor/oauth/poll")
	require.Contains(t, routes, "POST /admin/cursor/oauth/sso-token")
	require.Contains(t, routes, "POST /admin/cursor/sso-to-oauth")
}

func TestNormalizeCursorImportTokensDeduplicates(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, normalizeCursorImportTokens([]string{" a ", "b", "a"}, ""))
}
```

Cover capabilities, deep-link generation/poll, refresh, web-session validation, `crsr_` import, bulk result ordering, three-worker cap, sanitized errors, and account creation as OAuth.

- [ ] **Step 2: Verify failure**

Run: `cd backend && go test ./internal/handler/admin ./internal/server/routes ./internal/server -run 'CursorOAuth|Cursor.*Routes|CursorImport' -count=1`

Expected: FAIL because handlers and routes are absent.

- [ ] **Step 3: Implement handler surface and providers**

```go
type CursorOAuthHandler struct {
	oauthService *service.CursorOAuthService
	adminService service.AdminService
}

const cursorSSOImportConcurrency = 3
```

Register explicit Gin routes, pass proxy IDs through credential operations, create only OAuth Cursor accounts, and use the current account DTO/redaction path. Add Wire providers and bindings, then regenerate `wire_gen.go`; do not hand-edit generated behavior beyond the generator result.

- [ ] **Step 4: Generate, test, and commit**

Run: `cd backend && go generate ./cmd/server`

Run: `cd backend && go test ./internal/handler/admin ./internal/server/routes ./internal/server ./cmd/server -run 'Cursor|APIContract|Wire' -count=1`

Expected: PASS and `wire_gen.go` matches generator output.

```bash
git add backend/internal/handler/admin/cursor_oauth_handler.go backend/internal/handler/admin/cursor_oauth_handler_test.go backend/internal/handler/admin/account_handler.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/repository/wire.go backend/internal/service/wire.go backend/internal/server/routes/admin.go backend/internal/server/routes/gateway.go backend/internal/server/api_contract_test.go backend/cmd/server/wire_gen.go
git commit -m "feat(cursor): expose admin OAuth and wire services"
```

### Task 17: Add the Typed Frontend Cursor API and OAuth State Machine

**Files:**
- Create: `frontend/src/api/admin/cursor.ts`
- Create: `frontend/src/api/__tests__/admin.cursor.spec.ts`
- Create: `frontend/src/composables/useCursorOAuth.ts`
- Create: `frontend/src/composables/__tests__/useCursorOAuth.spec.ts`
- Modify: `frontend/src/api/admin/index.ts`

**Interfaces:**
- Produces: `adminAPI.cursor` and `useCursorOAuth()` with deep-link polling, cookie/API-key import, refresh, cancellation, and credential builders.
- Security: raw password/session inputs never enter returned credential maps or browser storage.

- [ ] **Step 1: Write failing API and composable tests**

```ts
it('polls until Cursor returns an access token', async () => {
  vi.mocked(adminAPI.cursor.pollAuthorization)
    .mockResolvedValueOnce({ status: 'pending' })
    .mockResolvedValueOnce({ access_token: 'jwt' })
  const flow = useCursorOAuth()
  const token = await flow.pollForToken({ sessionId: 'sid', state: 'state', intervalMs: 1, timeoutMs: 100 })
  expect(token?.access_token).toBe('jwt')
})

it('never persists one-time Cursor secrets', () => {
  const flow = useCursorOAuth()
  expect(flow.buildCredentials({ access_token: 'jwt', sso_token: 'canary', password: 'canary' } as any)).toEqual({ access_token: 'jwt' })
})
```

Cover route payloads, bulk timeout calculation, generation cancellation, timeout errors, `crsr_` preservation for refresh, web-session omission, and reset behavior.

- [ ] **Step 2: Verify failure**

Run: `cd frontend && pnpm exec vitest run src/api/__tests__/admin.cursor.spec.ts src/composables/__tests__/useCursorOAuth.spec.ts`

Expected: FAIL because the modules are absent.

- [ ] **Step 3: Implement exact frontend contracts**

```ts
export interface CursorTokenInfo {
  access_token?: string
  refresh_token?: string
  api_key?: string
  expires_at?: number | string
  status?: string
  [key: string]: unknown
}

const CURSOR_POLL_INTERVAL_MS = 3_000
const CURSOR_POLL_TIMEOUT_MS = 300_000
```

Port the fork's final endpoints and polling state machine, use the current `apiClient` and error helpers, cancel stale poll generations, and filter `sso_token`, `session_token`, `password`, `sso`, `sso-rw`, and `status` from persisted credentials.

- [ ] **Step 4: Run tests and commit**

Run: `cd frontend && pnpm exec vitest run src/api/__tests__/admin.cursor.spec.ts src/composables/__tests__/useCursorOAuth.spec.ts`

Expected: PASS.

```bash
git add frontend/src/api/admin/cursor.ts frontend/src/api/__tests__/admin.cursor.spec.ts frontend/src/composables/useCursorOAuth.ts frontend/src/composables/__tests__/useCursorOAuth.spec.ts frontend/src/api/admin/index.ts
git commit -m "feat(cursor): add admin API and OAuth flow"
```

### Task 18: Add Cursor to Frontend Platform Catalogs and Quotas

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/constants/platforms.ts`
- Modify: `frontend/src/constants/__tests__/platforms.spec.ts`
- Modify: `frontend/src/utils/platformColors.ts`
- Modify: `frontend/src/components/common/PlatformIcon.vue`
- Modify: `frontend/src/components/common/PlatformTypeBadge.vue`
- Create: `frontend/src/components/common/__tests__/PlatformTypeBadge.cursor.spec.ts`
- Modify: `frontend/src/components/common/GroupBadge.vue`
- Modify: `frontend/src/components/admin/account/AccountTableFilters.vue`
- Modify: `frontend/src/components/admin/channel/types.ts`
- Modify: `frontend/src/components/admin/user/UserPlatformQuotaModal.vue`
- Modify: `frontend/src/components/admin/user/__tests__/UserPlatformQuotaModal.spec.ts`
- Modify: `frontend/src/components/user/UserPlatformQuotaCell.vue`
- Modify: `frontend/src/components/user/dashboard/UserDashboardStats.vue`
- Modify: `frontend/src/api/admin/settings.ts`
- Modify: `frontend/src/api/__tests__/settings.authSourceDefaults.spec.ts`
- Modify: `frontend/src/api/admin/users.ts`

**Interfaces:**
- Produces: `'cursor'` in account/group/quota/filter types and visual components.
- Invariant: Cursor remains absent from channel-monitor provider constants and scheduling-threshold settings.

- [ ] **Step 1: Write failing catalog and quota tests**

```ts
it('registers Cursor as an account and quota platform only', () => {
  expect(CONCRETE_PLATFORM_VALUES).toContain('cursor')
  expect(normalizePlatformQuotasMap()).toHaveProperty('cursor')
  expect(SCHEDULING_THRESHOLD_PLATFORMS).not.toContain('cursor')
})
```

Add badge label/color/icon tests and update the existing platform count assertions from nine to ten.

- [ ] **Step 2: Verify failure**

Run: `cd frontend && pnpm exec vitest run src/constants/__tests__/platforms.spec.ts src/api/__tests__/settings.authSourceDefaults.spec.ts src/components/common/__tests__/PlatformTypeBadge.cursor.spec.ts src/components/admin/user/__tests__/UserPlatformQuotaModal.spec.ts`

Expected: FAIL because platform catalogs omit Cursor.

- [ ] **Step 3: Add Cursor additively**

```ts
export type AccountPlatform = 'anthropic' | 'openai' | 'gemini' | 'antigravity' | 'kiro' | 'grok' | 'kimi' | 'zhipu' | 'deepseek' | 'cursor'
export type GroupPlatform = AccountPlatform
```

Use a distinct Cursor color entry and icon branch, add quota labels, and retain every current DEV platform. Do not touch `frontend/src/constants/channelMonitor.ts`.

- [ ] **Step 4: Run tests and commit**

Run: `cd frontend && pnpm exec vitest run src/constants/__tests__/platforms.spec.ts src/api/__tests__/settings.authSourceDefaults.spec.ts src/components/common/__tests__/PlatformTypeBadge.cursor.spec.ts src/components/admin/user/__tests__/UserPlatformQuotaModal.spec.ts`

Expected: PASS.

```bash
git add frontend/src/types/index.ts frontend/src/constants/platforms.ts frontend/src/constants/__tests__/platforms.spec.ts frontend/src/utils/platformColors.ts frontend/src/components/common/PlatformIcon.vue frontend/src/components/common/PlatformTypeBadge.vue frontend/src/components/common/__tests__/PlatformTypeBadge.cursor.spec.ts frontend/src/components/common/GroupBadge.vue frontend/src/components/admin/account/AccountTableFilters.vue frontend/src/components/admin/channel/types.ts frontend/src/components/admin/user/UserPlatformQuotaModal.vue frontend/src/components/admin/user/__tests__/UserPlatformQuotaModal.spec.ts frontend/src/components/user/UserPlatformQuotaCell.vue frontend/src/components/user/dashboard/UserDashboardStats.vue frontend/src/api/admin/settings.ts frontend/src/api/__tests__/settings.authSourceDefaults.spec.ts frontend/src/api/admin/users.ts
git commit -m "feat(cursor): add frontend platform catalog"
```

### Task 19: Add Cursor Account Forms, Reauthorization, Models, and Localization

**Files:**
- Create: `frontend/src/components/account/CursorBaseUrlPresets.vue`
- Create: `frontend/src/components/account/__tests__/CreateAccountModal.cursor.spec.ts`
- Create: `frontend/src/components/admin/account/__tests__/ReAuthAccountModal.cursor.spec.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/account/BulkEditAccountModal.vue`
- Modify: `frontend/src/components/account/OAuthAuthorizationFlow.vue`
- Modify: `frontend/src/components/account/credentialsBuilder.ts`
- Modify: `frontend/src/components/admin/account/ReAuthAccountModal.vue`
- Modify: `frontend/src/composables/useModelWhitelist.ts`
- Modify: `frontend/src/views/admin/GroupsView.vue`
- Modify: `frontend/src/views/admin/ChannelsView.vue`
- Modify: `frontend/src/views/admin/ProxiesView.vue`
- Modify: `frontend/src/views/admin/ops/components/OpsDashboardHeader.vue`
- Modify: `frontend/src/i18n/locales/en/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/en/admin/overview.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/overview.ts`

**Interfaces:**
- Produces: create/edit/bulk-edit/reauth support for Cursor OAuth accounts, observed-model whitelist, base URL override, and paired English/Chinese copy.
- Invariant: the UI never offers Cursor `apikey`, balance refresh, or channel monitoring.

- [ ] **Step 1: Write failing account-flow tests**

```ts
it('creates Cursor imports as OAuth accounts', async () => {
  const wrapper = mountCursorCreateModal()
  await selectPlatform(wrapper, 'cursor')
  await pasteCursorCredential(wrapper, 'crsr_example')
  await submitCursorImport(wrapper)
  expect(adminAPI.cursor.createFromSSO).toHaveBeenCalledWith(expect.objectContaining({
    sso_tokens: ['crsr_example']
  }))
})

it('does not render unsupported Cursor controls', async () => {
  const wrapper = mountCursorCreateModal()
  await selectPlatform(wrapper, 'cursor')
  expect(wrapper.text()).not.toContain('API Key account')
  expect(wrapper.text()).not.toContain('Balance refresh')
  expect(wrapper.text()).not.toContain('Channel monitor')
})
```

Cover deep-link polling, cookie/API-key imports, reauthorization source replacement, custom base URL persistence, observed-model whitelist, secret clearing, and paired locale-key compilation.

- [ ] **Step 2: Verify failure**

Run: `cd frontend && pnpm exec vitest run src/components/account/__tests__/CreateAccountModal.cursor.spec.ts src/components/admin/account/__tests__/ReAuthAccountModal.cursor.spec.ts src/i18n/__tests__/localesMessageCompile.spec.ts`

Expected: FAIL because Cursor forms and locale keys are absent.

- [ ] **Step 3: Implement the supported UI only**

```ts
export const CURSOR_BASE_URL_PRESETS = [
  { labelKey: 'official', url: 'https://api2.cursor.sh' }
]

export function isCustomCursorBaseUrl(value: unknown): boolean {
  if (typeof value !== 'string' || !value.trim()) return false
  try {
    return new URL(value).hostname.toLowerCase() !== 'api2.cursor.sh'
  } catch {
    return false
  }
}
```

Compose `useCursorOAuth` into existing OAuth forms, submit imports through `adminAPI.cursor`, clear pasted secrets after submission, and use `CursorObservedModelIDs` results through existing model APIs. Keep all newer DEV form fields and provider branches intact.

- [ ] **Step 4: Run tests and commit**

Run: `cd frontend && pnpm exec vitest run src/components/account/__tests__/CreateAccountModal.cursor.spec.ts src/components/admin/account/__tests__/ReAuthAccountModal.cursor.spec.ts src/composables/__tests__/useCursorOAuth.spec.ts src/i18n/__tests__/localesMessageCompile.spec.ts`

Expected: PASS.

```bash
git add frontend/src/components/account/CursorBaseUrlPresets.vue frontend/src/components/account/__tests__/CreateAccountModal.cursor.spec.ts frontend/src/components/admin/account/__tests__/ReAuthAccountModal.cursor.spec.ts frontend/src/components/account/CreateAccountModal.vue frontend/src/components/account/EditAccountModal.vue frontend/src/components/account/BulkEditAccountModal.vue frontend/src/components/account/OAuthAuthorizationFlow.vue frontend/src/components/account/credentialsBuilder.ts frontend/src/components/admin/account/ReAuthAccountModal.vue frontend/src/composables/useModelWhitelist.ts frontend/src/views/admin/GroupsView.vue frontend/src/views/admin/ChannelsView.vue frontend/src/views/admin/ProxiesView.vue frontend/src/views/admin/ops/components/OpsDashboardHeader.vue frontend/src/i18n/locales/en/admin/accounts.ts frontend/src/i18n/locales/en/admin/overview.ts frontend/src/i18n/locales/zh/admin/accounts.ts frontend/src/i18n/locales/zh/admin/overview.ts
git commit -m "feat(cursor): add account management UI"
```

### Task 20: Run Generated-Code, Security, Parity, and Full Regression Gates

**Files:**
- Modify when generation requires it: `backend/ent/**`
- Modify when generation requires it: `backend/cmd/server/wire_gen.go`
- Create: `docs/CURSOR_FORK_PARITY.md`
- Create: `docs/CURSOR_FORWARDING_RUNBOOK_CN.md`
- Modify: `docs/superpowers/plans/2026-08-23-sub2api-cursor-fork-sync.md` only to check completed boxes and record exact verification evidence.

**Interfaces:**
- Produces: reproducible generated code, an auditable fork-commit-to-behavioral-test parity matrix, shell-only exclusion guards, an operator runbook without the excluded standalone E2E CLI, and evidence that all local gates pass.

- [x] **Step 1: Write the final parity matrix and verify existing behavioral contracts**

Do not commit source-grep/path-existence tests: they are change detectors rather than behavior tests. In `docs/CURSOR_FORK_PARITY.md`, map each fork commit `8b628eb20`, `a085dcf8b`, `24d48450e`, `ec176befd`, `d87149806`, `5ffd09fdf`, `563fe0d52`, `53294c5b3`, `d006da61d`, and `3709f0f6c` to exact focused behavioral test names created in Tasks 1–19. Map capture exclusion to the Task 14 caller-protocol capture tests and route support/exclusion to the real Task 15 server route/catalog tests. Explicitly mark standalone E2E CLI, channel monitor, stateful Agent sessions, and raw Connect capture as intentionally excluded. Keep file/path absence checks only as final shell scope guards.

- [x] **Step 2: Regenerate and verify no drift**

Run: `make check-generate`

Expected: PASS after committing any required Ent/Wire output; a second run produces no diff.

- [x] **Step 3: Run backend focused, race, build, and full tests**

Run: `cd backend && go test ./internal/pkg/cursor ./internal/service ./internal/handler/... ./internal/server/... ./internal/capture/... ./migrations -run 'Cursor|Capture|Platform' -count=1`

Run: `cd backend && go test -race ./internal/pkg/cursor ./internal/service -run 'Cursor' -count=1`

Run: `make build-backend`

Run: `cd backend && go test ./...`

Expected: all commands exit 0.

- [x] **Step 4: Run frontend focused and full gates**

Run: `cd frontend && pnpm run lint:check`

Run: `cd frontend && pnpm run typecheck`

Run: `cd frontend && pnpm run test:run`

Run: `cd frontend && pnpm run build`

Expected: lint and typecheck exit 0; all Vitest files pass; production build succeeds.

- [x] **Step 5: Run secret and scope guards**

Run: `git grep -n 'CURSOR_TOKEN_CANARY' -- ':!**/*_test.go' ':!docs/**'`

Expected: no credential literal or canary in production files.

Run: `git diff origin/dev...HEAD --name-only | rg 'cursor_e2e|QUICKSTART|openspec/changes/add-cursor-platform|channelMonitor'`

Expected: no output.

Run: `git grep -nE 'connect_proto|application/connect\+proto' -- backend/internal/capture frontend/src/constants/channelMonitor.ts`

Expected: no raw Connect capture format and no Cursor channel-monitor registration; interpret any test fixture hit by behavior and document it rather than weakening a behavioral test.

- [x] **Step 6: Write the operational runbook**

Document supported credential imports, required proxy behavior, model discovery, the three caller protocols, capture format, client-version override, 30-second idle behavior, and safe local fixture tests. State that production probing requires explicit approval and existing configured proxies.

- [x] **Step 7: Commit final verification artifacts**

```bash
git add backend/ent backend/cmd/server/wire_gen.go docs/superpowers/plans/2026-08-23-sub2api-cursor-fork-sync.md
git add -f docs/CURSOR_FORK_PARITY.md docs/CURSOR_FORWARDING_RUNBOOK_CN.md
git commit -m "test(cursor): verify fork parity and regressions"
```

- [x] **Step 8: Confirm final branch state**

Run:

```bash
git status --short --branch
git log --oneline origin/dev..HEAD
git diff --check origin/dev...HEAD
```

Expected: clean worktree, only task-scoped commits ahead of `origin/dev`, and no whitespace errors.

**Verification evidence (2026-08-24):**

- Parity audit: all 10 pinned fork commits are present in `docs/CURSOR_FORK_PARITY.md`; every backticked Go test name resolves to an existing `func Test...`; all four intentional exclusions and the Task 14/15 behavioral authorities are documented.
- Generated code: `make check-generate` ran twice and exited 0 both times. Each run regenerated Ent/Wire and its internal `git diff --exit-code` passed; a final explicit `git diff --exit-code -- backend/ent backend/cmd/server/wire_gen.go backend/cmd/server/wire_gen_test.go` also exited 0. No generated file is retained in this task.
- Backend focused: `cd backend && go test ./internal/pkg/cursor ./internal/service ./internal/handler/... ./internal/server/... ./internal/capture/... ./migrations -run 'Cursor|Capture|Platform' -count=1` exited 0; service completed in 12.470s and every selected package passed or correctly reported no matching test.
- Backend race: `cd backend && go test -race ./internal/pkg/cursor ./internal/service -run 'Cursor' -count=1` exited 0; service completed in 14.441s.
- Backend build/full: `make build-backend` and `cd backend && go test ./...` both exited 0.
- Frontend lint/typecheck: `cd frontend && pnpm run lint:check` and `cd frontend && pnpm run typecheck` both exited 0. Corepack printed its package-manager advisory and temporarily added a `packageManager` field; that tool side effect was reverted and is absent from the task diff.
- Frontend tests: `cd frontend && pnpm run test:run` exited 0 with 318 files and 2399 tests passed. Existing fixture stderr/advisories included intentional network-error paths, unresolved test stubs, an old Browserslist database, and a duplicate-key warning in `UsersView.spec.ts`; none failed a test.
- Frontend build: `cd frontend && pnpm run build` exited 0 (`1088 modules transformed`, Vite build 34.74s). Existing advisory output covered the old Browserslist database, mixed static/dynamic imports, and chunks over 500 kB.
- Scope guards: the canary grep, excluded-path diff grep, and Connect/channel-monitor grep each produced no output (expected grep exit 1). No credential canary, standalone CLI/OpenSpec/channel-monitor addition, raw Connect capture registration, or Cursor monitor registration was found.
- Final branch state: `git status --short --branch` was clean and 42 commits ahead of `origin/dev`; `git log --oneline origin/dev..HEAD` contained only the reviewed Cursor sync series; `git diff --check origin/dev...HEAD` and `git show --check HEAD` exited 0. A Task 20 scope ruling allowed removal of the two pre-existing trailing spaces at lines 3–4 of the Cursor design spec so the required branch-wide whitespace gate could pass; the Task 20 diff from base contains exactly the parity doc, runbook, tracked plan, and that two-line whitespace-only repair.
