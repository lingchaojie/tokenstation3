# Web Chat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the logged-in `/chat` web product with persistent conversations, uploads, downloads, model switching, and API-Key-equivalent subscription-first billing.

**Architecture:** Add a Web Chat layer above the existing gateway services. Web Chat owns persistence, upload/download storage, model capabilities, UI state, and provider-neutral history; gateway services still own account selection, upstream forwarding, usage logs, and billing. Internal hidden Web Chat API Keys keep `usage_logs.api_key_id` non-null while staying invisible to users and rejected by public API Key auth.

**Tech Stack:** Go, Gin, Ent, PostgreSQL SQL migrations, Wire, Vue 3, Vue Router, Pinia, Vite, Vitest, Tailwind CSS.

---

## Scope Check

This is one cohesive feature because each part is required for a working logged-in chat turn:

- Backend data model and storage persist conversations, messages, attachments, and artifacts.
- Hidden API Keys keep the existing billing ledger unchanged.
- Web Chat dispatch converts conversation history into provider requests and reuses gateway `RecordUsage`.
- Frontend `/chat`, homepage CTA, and sidebar entry expose the product.

Do not split this into independent branches during execution. Commit after each task so the branch stays reviewable.

## Existing Context

- Worktree: `/home/alvin/tokenstation3/.claude/worktrees/model-marketplace`
- Branch: `worktree-model-marketplace`
- Approved spec: `docs/superpowers/specs/2026-06-22-web-chat-design.md`
- Authenticated user routes live in `backend/internal/server/routes/user.go`.
- API Key auth lives in `backend/internal/server/middleware/api_key_auth.go`.
- API Key service and repository live in `backend/internal/service/api_key_service.go` and `backend/internal/repository/api_key_repo.go`.
- Gateway billing entry points are `GatewayService.RecordUsage` and `OpenAIGatewayService.RecordUsage`.
- Public model catalog is currently defined in `backend/internal/handler/model_catalog.go` and `backend/internal/handler/dto/settings.go`.
- Frontend API client is `frontend/src/api/client.ts`.
- Authenticated route list is `frontend/src/router/index.ts`.
- User sidebar entries are built in `frontend/src/components/layout/AppSidebar.vue`.
- Homepage is `frontend/src/views/HomeView.vue`.

## Data And API Contracts

Backend API paths, all under `/api/v1` and JWT-authenticated:

- `GET /chat/conversations`
- `POST /chat/conversations`
- `GET /chat/conversations/:id`
- `PATCH /chat/conversations/:id`
- `DELETE /chat/conversations/:id`
- `POST /chat/conversations/:id/messages`
- `POST /chat/conversations/:id/messages/:message_id/cancel`
- `POST /chat/attachments`
- `GET /chat/attachments/:id/download`
- `GET /chat/artifacts/:id/download`
- `GET /chat/models`

`POST /chat/attachments` uses multipart form field `file`. It creates an unattached row owned by the current user. `POST /chat/conversations/:id/messages` binds uploaded attachment IDs to the new user message.

`POST /chat/conversations/:id/messages` streams an OpenAI Chat Completions compatible response body so the first frontend implementation can parse one stream format. It also sets:

```http
X-Web-Chat-Conversation-ID: <conversation_id>
X-Web-Chat-User-Message-ID: <user_message_id>
X-Web-Chat-Assistant-Message-ID: <assistant_message_id>
```

The server captures the same response bytes while streaming them to the browser, persists assistant text after completion, stores generated artifacts found in response content, and records usage through the existing gateway billing code.

## File Structure

### Backend Files To Create

- `backend/migrations/160_web_chat.sql`: SQL tables, indexes, constraints, and the partial unique index for hidden Web Chat API Keys.
- `backend/ent/schema/web_chat_conversation.go`: Ent schema for `web_chat_conversations`.
- `backend/ent/schema/web_chat_message.go`: Ent schema for `web_chat_messages`.
- `backend/ent/schema/web_chat_attachment.go`: Ent schema for `web_chat_attachments`.
- `backend/ent/schema/web_chat_artifact.go`: Ent schema for `web_chat_artifacts`.
- `backend/internal/service/web_chat.go`: service-level Web Chat domain structs and request/response types.
- `backend/internal/service/web_chat_errors.go`: typed errors used by handlers and tests.
- `backend/internal/service/web_chat_capabilities.go`: provider/model capability resolution and context compatibility checks.
- `backend/internal/service/web_chat_storage.go`: local file storage abstraction rooted under `cfg.Pricing.DataDir/web-chat`.
- `backend/internal/service/web_chat_adapter.go`: provider-neutral history to OpenAI Chat Completions request conversion.
- `backend/internal/service/web_chat_capture.go`: response writer capture and assistant/artifact extraction.
- `backend/internal/service/web_chat_dispatch.go`: account selection, gateway forwarding, usage recording, and message finalization.
- `backend/internal/service/web_chat_service.go`: conversation, message, attachment, artifact orchestration.
- `backend/internal/repository/web_chat_repo.go`: Ent repository for CRUD and ownership checks.
- `backend/internal/handler/web_chat_handler.go`: Gin handler for Web Chat APIs.
- `backend/internal/service/web_chat_service_test.go`: service tests with stubs.
- `backend/internal/service/web_chat_capabilities_test.go`: model-switching and attachment validation tests.
- `backend/internal/service/web_chat_adapter_test.go`: payload conversion tests.
- `backend/internal/repository/web_chat_repo_test.go`: repository ownership tests.
- `backend/internal/handler/web_chat_handler_test.go`: route-level auth/error tests.

### Backend Files To Modify

- `backend/internal/service/domain_constants.go`: add `APIKeyTypeWebChat` without allowing it in `NormalizeAPIKeyType`.
- `backend/internal/service/api_key_service.go`: add hidden Web Chat key methods to `APIKeyRepository` interface and `APIKeyService`.
- `backend/internal/repository/api_key_repo.go`: implement hidden key lookup/create, exclude hidden keys from user lists/count/search, and keep auth lookup able to find them so middleware can reject leaked keys.
- `backend/internal/repository/api_key_repo_integration_test.go`: assert hidden keys are omitted from user-facing list/count.
- `backend/internal/server/middleware/api_key_auth.go`: reject `key_type = web_chat`.
- `backend/internal/server/middleware/api_key_auth_test.go`: assert leaked internal keys are unauthorized.
- `backend/internal/service/account_usage_service.go`: add `GetByRequestIDAndAPIKeyID` to `UsageLogRepository`.
- `backend/internal/repository/usage_log_repo.go`: implement `GetByRequestIDAndAPIKeyID`.
- `backend/internal/repository/usage_log_repo_integration_test.go`: verify usage log lookup by deterministic Web Chat request ID.
- `backend/internal/service/wire.go`: add `NewWebChatService` provider.
- `backend/internal/repository/wire.go`: add `NewWebChatRepository`.
- `backend/internal/handler/handler.go`: add `Chat *WebChatHandler`.
- `backend/internal/handler/wire.go`: add `NewWebChatHandler` and wire it into `ProvideHandlers`.
- `backend/internal/server/routes/user.go`: register authenticated `/chat` routes.
- `backend/cmd/server/wire_gen.go`: generated by `go generate ./cmd/server`.
- `backend/ent/*`: generated by `go generate ./ent`.

### Frontend Files To Create

- `frontend/src/api/chat.ts`: typed Web Chat API client and streaming sender.
- `frontend/src/stores/chat.ts`: Pinia state for conversations, selected model, messages, attachments, and streaming state.
- `frontend/src/views/user/ChatView.vue`: authenticated `/chat` page.
- `frontend/src/components/chat/ChatShell.vue`: layout container.
- `frontend/src/components/chat/ConversationRail.vue`: conversation list, create, rename, delete.
- `frontend/src/components/chat/MessageList.vue`: message rendering and artifact download chips.
- `frontend/src/components/chat/ModelSelector.vue`: model selector and capability warnings.
- `frontend/src/components/chat/Composer.vue`: text input, upload buttons, send, stop.
- `frontend/src/components/chat/AttachmentChip.vue`: attachment preview chip.
- `frontend/src/components/chat/__tests__/chatStore.spec.ts`: store behavior tests.
- `frontend/src/components/chat/__tests__/ChatView.spec.ts`: page/component smoke tests.
- `frontend/src/views/user/__tests__/ChatViewSource.spec.ts`: source-level guard for layout conventions.

### Frontend Files To Modify

- `frontend/src/router/index.ts`: add authenticated `/chat` route.
- `frontend/src/components/layout/AppSidebar.vue`: add Chat icon and nav entry next to Models.
- `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`: assert sidebar exposes Chat.
- `frontend/src/router/__tests__/guards.spec.ts`: assert `/chat` requires auth.
- `frontend/src/views/HomeView.vue`: add logged-in/logged-out chat CTA and near-hero chat product entry.
- `frontend/src/i18n/locales/zh.ts`: add `nav.chat` and `chat.*` copy.
- `frontend/src/i18n/locales/en.ts`: add `nav.chat` and `chat.*` copy.
- `frontend/src/components/icons/Icon.vue`: add `paperclip`, `image`, `send`, and `stop` icons if missing.

---

## Task 1: Database Schema And Ent Models

**Files:**
- Create: `backend/migrations/160_web_chat.sql`
- Create: `backend/ent/schema/web_chat_conversation.go`
- Create: `backend/ent/schema/web_chat_message.go`
- Create: `backend/ent/schema/web_chat_attachment.go`
- Create: `backend/ent/schema/web_chat_artifact.go`
- Modify: generated files under `backend/ent`

- [ ] **Step 1: Write the migration**

Create `backend/migrations/160_web_chat.sql` with this schema. Use nullable `message_id` and `conversation_id` on attachments so uploads can happen before a message is submitted.

```sql
CREATE TABLE IF NOT EXISTS web_chat_conversations (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(200) NOT NULL DEFAULT '',
    default_model VARCHAR(100) NOT NULL DEFAULT '',
    default_provider VARCHAR(50) NOT NULL DEFAULT '',
    last_model VARCHAR(100) NOT NULL DEFAULT '',
    last_provider VARCHAR(50) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    message_count INTEGER NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_conversations_status_check CHECK (status IN ('active', 'archived', 'deleted'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_conversations_user_updated
    ON web_chat_conversations(user_id, updated_at DESC)
    WHERE status <> 'deleted';

CREATE TABLE IF NOT EXISTS web_chat_messages (
    id BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL,
    model VARCHAR(100) NOT NULL DEFAULT '',
    provider VARCHAR(50) NOT NULL DEFAULT '',
    content_text TEXT NOT NULL DEFAULT '',
    content_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'completed',
    error_code VARCHAR(80),
    error_message TEXT,
    usage_log_id BIGINT REFERENCES usage_logs(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_messages_role_check CHECK (role IN ('user', 'assistant', 'system')),
    CONSTRAINT web_chat_messages_status_check CHECK (status IN ('pending', 'streaming', 'completed', 'failed', 'canceled'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_conversation_created
    ON web_chat_messages(conversation_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_web_chat_messages_user_created
    ON web_chat_messages(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_web_chat_messages_usage_log_id
    ON web_chat_messages(usage_log_id)
    WHERE usage_log_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS web_chat_attachments (
    id BIGSERIAL PRIMARY KEY,
    message_id BIGINT REFERENCES web_chat_messages(id) ON DELETE SET NULL,
    conversation_id BIGINT REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind VARCHAR(20) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(120) NOT NULL,
    size_bytes BIGINT NOT NULL,
    storage_key VARCHAR(500) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    text_preview TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'uploaded',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_attachments_kind_check CHECK (kind IN ('image', 'file')),
    CONSTRAINT web_chat_attachments_status_check CHECK (status IN ('uploaded', 'processed', 'unsupported', 'deleted'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_user_created
    ON web_chat_attachments(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_message
    ON web_chat_attachments(message_id)
    WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_conversation
    ON web_chat_attachments(conversation_id)
    WHERE conversation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS web_chat_artifacts (
    id BIGSERIAL PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES web_chat_messages(id) ON DELETE CASCADE,
    conversation_id BIGINT NOT NULL REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(120) NOT NULL,
    size_bytes BIGINT NOT NULL,
    storage_key VARCHAR(500) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    source VARCHAR(30) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_artifacts_source_check CHECK (source IN ('model_output', 'image_output', 'generated_file'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_artifacts_message
    ON web_chat_artifacts(message_id);
CREATE INDEX IF NOT EXISTS idx_web_chat_artifacts_user_created
    ON web_chat_artifacts(user_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_web_chat_user_group_unique
    ON api_keys(user_id, group_id)
    WHERE key_type = 'web_chat' AND deleted_at IS NULL;
```

- [ ] **Step 2: Add Ent schemas**

Create four Ent schemas with table annotations matching the SQL names. The `WebChatMessage` schema must use `field.JSON("content_json", []map[string]any{})` with PostgreSQL `jsonb`.

```go
field.JSON("content_json", []map[string]any{}).
    Optional().
    SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
```

Use `mixins.TimeMixin{}` for conversation/message and explicit `created_at` for attachment/artifact because the SQL tables do not have `updated_at` on attachment/artifact.

- [ ] **Step 3: Generate Ent code**

Run:

```bash
cd backend
go generate ./ent
```

Expected: generated code under `backend/ent` changes and command exits with status 0.

- [ ] **Step 4: Compile generated schema**

Run:

```bash
cd backend
go test ./ent/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/migrations/160_web_chat.sql backend/ent
git commit -m "feat: add web chat persistence schema"
```

## Task 2: Hidden Web Chat API Keys

**Files:**
- Modify: `backend/internal/service/domain_constants.go`
- Modify: `backend/internal/service/api_key_service.go`
- Modify: `backend/internal/repository/api_key_repo.go`
- Modify: `backend/internal/server/middleware/api_key_auth.go`
- Test: `backend/internal/service/api_key_service_provider_routing_test.go`
- Test: `backend/internal/repository/api_key_repo_integration_test.go`
- Test: `backend/internal/server/middleware/api_key_auth_test.go`

- [ ] **Step 1: Add the hidden key constant**

In `backend/internal/service/domain_constants.go`, add:

```go
const (
    APIKeyTypeAnthropic = PlatformAnthropic
    APIKeyTypeOpenAI    = PlatformOpenAI
    APIKeyTypeWebChat   = "web_chat"
    APIKeyTypeUnknown   = "unknown"
    APIKeyTypeUnified   = "unified"
)
```

Keep `NormalizeAPIKeyType` unchanged except for any required formatting. `web_chat` must not be accepted by user API Key creation.

- [ ] **Step 2: Extend the repository interface**

In `APIKeyRepository` add:

```go
GetWebChatKeyByUserAndGroup(ctx context.Context, userID, groupID int64) (*APIKey, error)
```

In `APIKeyService`, add:

```go
func (s *APIKeyService) EnsureWebChatKey(ctx context.Context, user *User, group *Group) (*APIKey, error) {
    if user == nil || user.ID <= 0 {
        return nil, ErrUserNotFound
    }
    if group == nil || group.ID <= 0 {
        return nil, ErrDefaultAPIKeyGroupMissing
    }
    if !group.IsActive() {
        return nil, ErrDefaultAPIKeyGroupInvalid
    }
    if !s.canUserBindGroup(ctx, user, group) {
        return nil, ErrGroupNotAllowed
    }
    existing, err := s.apiKeyRepo.GetWebChatKeyByUserAndGroup(ctx, user.ID, group.ID)
    if err == nil && existing != nil {
        existing.User = user
        existing.Group = group
        return existing, nil
    }
    if err != nil && !errors.Is(err, ErrAPIKeyNotFound) {
        return nil, fmt.Errorf("get web chat api key: %w", err)
    }
    key := &APIKey{
        UserID:           user.ID,
        Key:              "wc_" + generateAPIKey(),
        Name:             "Web Chat",
        KeyType:          APIKeyTypeWebChat,
        GroupID:          &group.ID,
        GroupBindingMode: APIKeyGroupBindingModeStatic,
        Status:           StatusActive,
        Quota:            0,
        RateLimit5h:      0,
        RateLimit1d:      0,
        RateLimit7d:      0,
        User:             user,
        Group:            group,
    }
    if err := s.apiKeyRepo.Create(ctx, key); err != nil {
        if errors.Is(err, ErrAPIKeyExists) {
            existing, getErr := s.apiKeyRepo.GetWebChatKeyByUserAndGroup(ctx, user.ID, group.ID)
            if getErr == nil && existing != nil {
                existing.User = user
                existing.Group = group
                return existing, nil
            }
        }
        return nil, fmt.Errorf("create web chat api key: %w", err)
    }
    return key, nil
}
```

- [ ] **Step 3: Implement hidden key repository behavior**

In `backend/internal/repository/api_key_repo.go`, implement `GetWebChatKeyByUserAndGroup` using `activeQuery()`, `apikey.UserIDEQ(userID)`, `apikey.GroupIDEQ(groupID)`, and `apikey.KeyTypeEQ(service.APIKeyTypeWebChat)`, with `WithUser` and `WithGroup`.

Update user-facing list/count/search queries:

```go
q := r.activeQuery().Where(
    apikey.UserIDEQ(userID),
    apikey.Or(apikey.KeyTypeIsNil(), apikey.KeyTypeNEQ(service.APIKeyTypeWebChat)),
)
```

Apply the same hidden-key exclusion to `CountByUserID` and `SearchAPIKeys`.

- [ ] **Step 4: Reject leaked Web Chat keys in public auth**

In `backend/internal/server/middleware/api_key_auth.go`, immediately after `apiKeyService.GetByKey` succeeds, add:

```go
if apiKey.KeyType == service.APIKeyTypeWebChat {
    AbortWithError(c, 401, "INVALID_API_KEY", "Invalid API key")
    return
}
```

- [ ] **Step 5: Add failing tests**

Add these assertions before implementation if the implementation was not already written:

```go
func TestEnsureWebChatKey_CreatesHiddenStaticKey(t *testing.T) {
    svc, repo := newAPIKeyServiceWithRepoStub()
    groupID := int64(9)
    key, err := svc.EnsureWebChatKey(context.Background(), &User{ID: 42, Status: StatusActive}, &Group{ID: groupID, Status: StatusActive})
    require.NoError(t, err)
    require.Equal(t, APIKeyTypeWebChat, key.KeyType)
    require.Equal(t, APIKeyGroupBindingModeStatic, key.GroupBindingMode)
    require.Equal(t, groupID, *key.GroupID)
    require.Zero(t, key.Quota)
    require.Zero(t, key.RateLimit1d)
    require.Equal(t, repo.created.ID, key.ID)
}
```

```go
func (s *APIKeyRepoSuite) TestListByUserID_ExcludesWebChatKeys() {
    user := s.createTestUser("hidden-webchat@example.com")
    group := s.createTestGroup("hidden webchat group")
    visible := s.createTestAPIKey(user.ID, group.ID, service.APIKeyTypeOpenAI)
    hidden := s.createTestAPIKey(user.ID, group.ID, service.APIKeyTypeWebChat)

    keys, page, err := s.repo.ListByUserID(s.ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{})
    s.Require().NoError(err)
    s.Require().Equal(int64(1), page.Total)
    s.Require().Len(keys, 1)
    s.Require().Equal(visible.ID, keys[0].ID)
    s.Require().NotEqual(hidden.ID, keys[0].ID)
}
```

```go
func TestAPIKeyAuthRejectsWebChatKey(t *testing.T) {
    apiKey := &service.APIKey{
        ID: 1,
        Key: "wc_leaked",
        KeyType: service.APIKeyTypeWebChat,
        Status: service.StatusActive,
        User: &service.User{ID: 42, Status: service.StatusActive},
        Group: &service.Group{ID: 9, Status: service.StatusActive},
    }
    router := gin.New()
    router.Use(apiKeyAuthWithSubscription(fakeAPIKeyService(apiKey), nil, &config.Config{}))
    router.GET("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })

    req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
    req.Header.Set("Authorization", "Bearer wc_leaked")
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    require.Equal(t, http.StatusUnauthorized, w.Code)
    require.Contains(t, w.Body.String(), "INVALID_API_KEY")
}
```

- [ ] **Step 6: Run tests**

Run:

```bash
cd backend
go test ./internal/service -run TestEnsureWebChatKey -count=1
go test ./internal/server/middleware -run TestAPIKeyAuthRejectsWebChatKey -count=1
go test ./internal/repository -run 'TestAPIKeyRepoSuite/TestListByUserID_ExcludesWebChatKeys' -count=1
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/domain_constants.go backend/internal/service/api_key_service.go backend/internal/repository/api_key_repo.go backend/internal/server/middleware/api_key_auth.go backend/internal/service/*api_key*test.go backend/internal/repository/*api_key*test.go backend/internal/server/middleware/*api_key*test.go
git commit -m "feat: add hidden web chat api keys"
```

## Task 3: Web Chat Repository And Ownership

**Files:**
- Create: `backend/internal/service/web_chat.go`
- Create: `backend/internal/service/web_chat_errors.go`
- Create: `backend/internal/repository/web_chat_repo.go`
- Test: `backend/internal/repository/web_chat_repo_test.go`
- Modify: `backend/internal/repository/wire.go`

- [ ] **Step 1: Define service domain types**

Create `backend/internal/service/web_chat.go` with these constants and structs:

```go
package service

import "time"

const (
    WebChatConversationStatusActive  = "active"
    WebChatConversationStatusArchived = "archived"
    WebChatConversationStatusDeleted = "deleted"

    WebChatRoleUser      = "user"
    WebChatRoleAssistant = "assistant"
    WebChatRoleSystem    = "system"

    WebChatMessageStatusPending   = "pending"
    WebChatMessageStatusStreaming = "streaming"
    WebChatMessageStatusCompleted = "completed"
    WebChatMessageStatusFailed    = "failed"
    WebChatMessageStatusCanceled  = "canceled"

    WebChatAttachmentKindImage = "image"
    WebChatAttachmentKindFile  = "file"

    WebChatArtifactSourceModelOutput   = "model_output"
    WebChatArtifactSourceImageOutput   = "image_output"
    WebChatArtifactSourceGeneratedFile = "generated_file"
)

type WebChatConversation struct {
    ID             int64     `json:"id"`
    UserID         int64     `json:"user_id"`
    Title          string    `json:"title"`
    DefaultModel   string    `json:"default_model"`
    DefaultProvider string   `json:"default_provider"`
    LastModel      string    `json:"last_model"`
    LastProvider   string    `json:"last_provider"`
    Status         string    `json:"status"`
    MessageCount   int       `json:"message_count"`
    LastMessageAt  *time.Time `json:"last_message_at,omitempty"`
    CreatedAt      time.Time `json:"created_at"`
    UpdatedAt      time.Time `json:"updated_at"`
}

type WebChatMessage struct {
    ID             int64            `json:"id"`
    ConversationID int64            `json:"conversation_id"`
    UserID         int64            `json:"user_id"`
    Role           string           `json:"role"`
    Model          string           `json:"model"`
    Provider       string           `json:"provider"`
    ContentText    string           `json:"content_text"`
    ContentJSON    []map[string]any `json:"content_json"`
    Status         string           `json:"status"`
    ErrorCode      *string          `json:"error_code,omitempty"`
    ErrorMessage   *string          `json:"error_message,omitempty"`
    UsageLogID     *int64           `json:"usage_log_id,omitempty"`
    CreatedAt      time.Time        `json:"created_at"`
    UpdatedAt      time.Time        `json:"updated_at"`
    Attachments    []WebChatAttachment `json:"attachments,omitempty"`
    Artifacts      []WebChatArtifact   `json:"artifacts,omitempty"`
}
```

Continue the file with `WebChatAttachment`, `WebChatArtifact`, `CreateWebChatConversationInput`, `UpdateWebChatConversationInput`, `CreateWebChatMessageInput`, and `UpdateWebChatMessageInput`. Keep field names identical to the JSON names used by the API.

- [ ] **Step 2: Define typed errors**

Create `backend/internal/service/web_chat_errors.go`:

```go
package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

var (
    ErrWebChatConversationNotFound = infraerrors.NotFound("WEB_CHAT_CONVERSATION_NOT_FOUND", "conversation not found")
    ErrWebChatMessageNotFound      = infraerrors.NotFound("WEB_CHAT_MESSAGE_NOT_FOUND", "message not found")
    ErrWebChatAttachmentNotFound   = infraerrors.NotFound("WEB_CHAT_ATTACHMENT_NOT_FOUND", "attachment not found")
    ErrWebChatArtifactNotFound     = infraerrors.NotFound("WEB_CHAT_ARTIFACT_NOT_FOUND", "artifact not found")
    ErrWebChatInvalidModel         = infraerrors.BadRequest("WEB_CHAT_INVALID_MODEL", "model is not available for web chat")
    ErrWebChatUnsupportedContext   = infraerrors.BadRequest("WEB_CHAT_UNSUPPORTED_CONTEXT", "selected model does not support the current conversation context")
    ErrWebChatUploadRejected       = infraerrors.BadRequest("WEB_CHAT_UPLOAD_REJECTED", "file upload rejected")
)
```

- [ ] **Step 3: Write repository ownership tests**

Create `backend/internal/repository/web_chat_repo_test.go` with tests that fail until repository methods exist:

```go
func TestWebChatRepository_ConversationOwnership(t *testing.T) {
    client, cleanup := newEntTestClient(t)
    defer cleanup()
    repo := NewWebChatRepository(client)
    ctx := context.Background()

    ownerID := createRepoTestUser(t, client, "chat-owner@example.com")
    otherID := createRepoTestUser(t, client, "chat-other@example.com")

    conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{
        UserID: ownerID,
        Title: "Owned conversation",
        DefaultModel: "gpt-5",
        DefaultProvider: "openai",
    })
    require.NoError(t, err)

    _, err = repo.GetConversationForUser(ctx, otherID, conv.ID)
    require.ErrorIs(t, err, service.ErrWebChatConversationNotFound)

    got, err := repo.GetConversationForUser(ctx, ownerID, conv.ID)
    require.NoError(t, err)
    require.Equal(t, conv.ID, got.ID)
}
```

Also add tests for:

- `ListConversations` excludes `status = deleted`.
- `AttachUploadedFilesToMessage` rejects attachment IDs owned by another user.
- `GetArtifactForUser` rejects non-owner access.

- [ ] **Step 4: Implement repository**

Create `backend/internal/repository/web_chat_repo.go` with interface satisfaction for:

```go
func NewWebChatRepository(client *dbent.Client) service.WebChatRepository
func (r *webChatRepository) CreateConversation(ctx context.Context, in service.CreateWebChatConversationInput) (*service.WebChatConversation, error)
func (r *webChatRepository) ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.WebChatConversation, *pagination.PaginationResult, error)
func (r *webChatRepository) GetConversationForUser(ctx context.Context, userID, conversationID int64) (*service.WebChatConversation, error)
func (r *webChatRepository) UpdateConversation(ctx context.Context, userID, conversationID int64, in service.UpdateWebChatConversationInput) (*service.WebChatConversation, error)
func (r *webChatRepository) SoftDeleteConversation(ctx context.Context, userID, conversationID int64) error
func (r *webChatRepository) CreateMessage(ctx context.Context, in service.CreateWebChatMessageInput) (*service.WebChatMessage, error)
func (r *webChatRepository) ListMessages(ctx context.Context, userID, conversationID int64) ([]service.WebChatMessage, error)
func (r *webChatRepository) UpdateMessage(ctx context.Context, userID, messageID int64, in service.UpdateWebChatMessageInput) (*service.WebChatMessage, error)
func (r *webChatRepository) CreateAttachment(ctx context.Context, in service.CreateWebChatAttachmentInput) (*service.WebChatAttachment, error)
func (r *webChatRepository) AttachUploadedFilesToMessage(ctx context.Context, userID, conversationID, messageID int64, attachmentIDs []int64) ([]service.WebChatAttachment, error)
func (r *webChatRepository) GetAttachmentForUser(ctx context.Context, userID, attachmentID int64) (*service.WebChatAttachment, error)
func (r *webChatRepository) CreateArtifact(ctx context.Context, in service.CreateWebChatArtifactInput) (*service.WebChatArtifact, error)
func (r *webChatRepository) GetArtifactForUser(ctx context.Context, userID, artifactID int64) (*service.WebChatArtifact, error)
```

Use Ent transactions for message creation plus attachment binding:

```go
tx, err := r.client.Tx(ctx)
if err != nil {
    return nil, err
}
defer rollbackUnlessCommitted(tx)
```

After creating a user message, increment `message_count`, set `last_message_at`, `last_model`, `last_provider`, and `updated_at` on the conversation.

- [ ] **Step 5: Wire repository**

Add `NewWebChatRepository` to `backend/internal/repository/wire.go`.

- [ ] **Step 6: Run repository tests**

Run:

```bash
cd backend
go test ./internal/repository -run TestWebChatRepository -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/web_chat*.go backend/internal/repository/web_chat_repo.go backend/internal/repository/web_chat_repo_test.go backend/internal/repository/wire.go
git commit -m "feat: add web chat repository"
```

## Task 4: Attachment And Artifact Storage

**Files:**
- Create: `backend/internal/service/web_chat_storage.go`
- Test: `backend/internal/service/web_chat_storage_test.go`
- Modify: `backend/internal/service/web_chat_service.go`

- [ ] **Step 1: Write storage tests**

Create `backend/internal/service/web_chat_storage_test.go`:

```go
func TestLocalWebChatStorage_StoresFileUnderDataDir(t *testing.T) {
    root := t.TempDir()
    storage := NewLocalWebChatStorage(root)
    saved, err := storage.Save(context.Background(), WebChatStorageSaveInput{
        UserID: 42,
        Filename: "hello.txt",
        ContentType: "text/plain",
        Reader: strings.NewReader("hello world"),
        MaxBytes: 1024,
    })
    require.NoError(t, err)
    require.Equal(t, int64(11), saved.SizeBytes)
    require.Len(t, saved.SHA256, 64)
    require.NotContains(t, saved.StorageKey, "..")

    rc, meta, err := storage.Open(context.Background(), saved.StorageKey)
    require.NoError(t, err)
    defer rc.Close()
    body, err := io.ReadAll(rc)
    require.NoError(t, err)
    require.Equal(t, "hello world", string(body))
    require.Equal(t, saved.SizeBytes, meta.SizeBytes)
}

func TestLocalWebChatStorage_RejectsTooLargeFile(t *testing.T) {
    storage := NewLocalWebChatStorage(t.TempDir())
    _, err := storage.Save(context.Background(), WebChatStorageSaveInput{
        UserID: 42,
        Filename: "large.bin",
        ContentType: "application/octet-stream",
        Reader: strings.NewReader("123456"),
        MaxBytes: 5,
    })
    require.ErrorIs(t, err, ErrWebChatUploadRejected)
}
```

- [ ] **Step 2: Implement local storage**

Create a storage interface:

```go
type WebChatStorage interface {
    Save(ctx context.Context, in WebChatStorageSaveInput) (*WebChatStoredFile, error)
    Open(ctx context.Context, storageKey string) (io.ReadCloser, WebChatStoredFileMeta, error)
}
```

Use root path `filepath.Join(cfg.Pricing.DataDir, "web-chat")`. Save files under:

```go
filepath.Join(root, strconv.FormatInt(userID, 10), time.Now().UTC().Format("2006/01/02"), uuidLikeName)
```

Use `io.LimitReader` with `MaxBytes + 1`, compute SHA-256 while writing, create parent directories with `0700`, create files with `0600`, and never return an absolute path as `StorageKey`.

- [ ] **Step 3: Add upload validation**

In `WebChatService.UploadAttachment`, enforce:

```go
const (
    webChatMaxUploadBytes = 20 << 20
    webChatMaxTextPreviewBytes = 64 << 10
)
```

Allowed MIME families:

- `image/png`, `image/jpeg`, `image/webp`, `image/gif`
- `text/plain`, `text/markdown`, `application/json`, `text/csv`
- `application/pdf`, `application/vnd.openxmlformats-officedocument.wordprocessingml.document`

Store `kind = image` only for the image MIME types. Store bounded UTF-8 text preview for text and JSON files.

- [ ] **Step 4: Run storage tests**

Run:

```bash
cd backend
go test ./internal/service -run 'TestLocalWebChatStorage|TestWebChatService_UploadAttachment' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/web_chat_storage.go backend/internal/service/web_chat_storage_test.go backend/internal/service/web_chat_service.go
git commit -m "feat: add web chat file storage"
```

## Task 5: Model Capabilities And Switching Rules

**Files:**
- Create: `backend/internal/service/web_chat_capabilities.go`
- Test: `backend/internal/service/web_chat_capabilities_test.go`
- Modify: `backend/internal/handler/model_catalog.go`

- [ ] **Step 1: Write capability tests**

Create tests:

```go
func TestWebChatCapabilities_BlocksImageWhenTargetDoesNotSupportImage(t *testing.T) {
    caps := WebChatModelCapability{
        Provider: "anthropic",
        Model: "claude-text-only",
        SupportsText: true,
        SupportsImageInput: false,
        SupportsFileContext: false,
    }
    err := ValidateWebChatContextForModel(caps, WebChatContextSummary{ImageAttachmentCount: 1})
    require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
    require.Contains(t, err.Error(), "image")
}

func TestWebChatCapabilities_AllowsTextAcrossProviders(t *testing.T) {
    caps := WebChatModelCapability{Provider: "openai", Model: "gpt-5", SupportsText: true}
    err := ValidateWebChatContextForModel(caps, WebChatContextSummary{TextMessageCount: 12})
    require.NoError(t, err)
}
```

- [ ] **Step 2: Implement capability model**

Create:

```go
type WebChatModelCapability struct {
    Provider              string `json:"provider"`
    Platform              string `json:"platform"`
    KeyType               string `json:"key_type"`
    Model                 string `json:"model"`
    DisplayName           string `json:"display_name"`
    SupportsText          bool   `json:"supports_text"`
    SupportsImageInput    bool   `json:"supports_image_input"`
    SupportsFileContext   bool   `json:"supports_file_context"`
    SupportsArtifactOutput bool  `json:"supports_artifact_output"`
    PriceStatus           string `json:"price_status"`
}

type WebChatContextSummary struct {
    TextMessageCount     int
    ImageAttachmentCount int
    FileAttachmentCount  int
}
```

Provider routing rules:

```go
var webChatProviderRoutes = map[string]struct {
    Platform string
    KeyType  string
}{
    "anthropic": {Platform: PlatformAnthropic, KeyType: APIKeyTypeAnthropic},
    "openai":    {Platform: PlatformOpenAI, KeyType: APIKeyTypeOpenAI},
    "qwen":      {Platform: PlatformOpenAI, KeyType: APIKeyTypeOpenAI},
    "gemini":    {Platform: PlatformGemini, KeyType: ""},
}
```

Capabilities:

- `supports_text`: true for every catalog model.
- `supports_image_input`: true when model modalities contain `image` or features contain `vision input`.
- `supports_file_context`: true for text-capable models; only text previews are sent as context.
- `supports_artifact_output`: true for image models and models whose modalities include `image`.

Model switching rule:

```go
func ValidateWebChatContextForModel(caps WebChatModelCapability, summary WebChatContextSummary) error {
    if !caps.SupportsText && summary.TextMessageCount > 0 {
        return fmt.Errorf("%w: text context is not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
    }
    if !caps.SupportsImageInput && summary.ImageAttachmentCount > 0 {
        return fmt.Errorf("%w: image attachments are not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
    }
    if !caps.SupportsFileContext && summary.FileAttachmentCount > 0 {
        return fmt.Errorf("%w: file context is not supported by %s", ErrWebChatUnsupportedContext, caps.Model)
    }
    return nil
}
```

- [ ] **Step 3: Expose catalog snapshot for service use**

Move the catalog snapshot helper from handler-only code into a service-safe function or export a handler package function with no Gin dependency:

```go
func PublicModelCatalogModelsForWebChat() []dto.PublicModelCatalogModel {
    models := publicModelCatalogModelsSnapshot()
    sortPublicModelCatalog(models)
    return models
}
```

Keep `GetPublicModelCatalog` behavior unchanged.

- [ ] **Step 4: Run capability tests**

Run:

```bash
cd backend
go test ./internal/service -run TestWebChatCapabilities -count=1
go test ./internal/handler -run TestSettingHandler_GetPublicModelCatalog -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/web_chat_capabilities.go backend/internal/service/web_chat_capabilities_test.go backend/internal/handler/model_catalog.go
git commit -m "feat: add web chat model capabilities"
```

## Task 6: Provider-Neutral History Adapter

**Files:**
- Create: `backend/internal/service/web_chat_adapter.go`
- Test: `backend/internal/service/web_chat_adapter_test.go`

- [ ] **Step 1: Write adapter tests**

Create tests:

```go
func TestBuildWebChatCompletionsPayload_IncludesTextImageAndFilePreview(t *testing.T) {
    messages := []WebChatMessage{{
        Role: WebChatRoleUser,
        ContentText: "Explain this image and notes",
        Attachments: []WebChatAttachment{
            {Kind: WebChatAttachmentKindImage, ContentType: "image/png", StorageKey: "u/1/image.png"},
            {Kind: WebChatAttachmentKindFile, ContentType: "text/plain", TextPreview: stringPtr("notes")},
        },
    }}
    payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeStorageWithImage("iVBORw0KGgo="), WebChatModelCapability{
        Model: "gpt-5",
        SupportsText: true,
        SupportsImageInput: true,
        SupportsFileContext: true,
    }, messages, true)
    require.NoError(t, err)
    require.JSONEq(t, `{
        "model":"gpt-5",
        "stream":true,
        "stream_options":{"include_usage":true},
        "messages":[{"role":"user","content":[
            {"type":"text","text":"Explain this image and notes\n\nAttached file notes:\nnotes"},
            {"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}
        ]}]
    }`, string(payload))
}
```

- [ ] **Step 2: Implement payload conversion**

Implement `BuildWebChatCompletionsPayload` to output OpenAI Chat Completions compatible JSON for all platforms. Existing gateway conversion paths already translate Chat Completions into Anthropic/Gemini formats when needed.

Core request shape:

```go
type webChatCCRequest struct {
    Model         string              `json:"model"`
    Stream        bool                `json:"stream"`
    StreamOptions *webChatStreamUsage `json:"stream_options,omitempty"`
    Messages      []webChatCCMessage  `json:"messages"`
}
```

Rules:

- Always set `stream_options.include_usage = true` when stream is true.
- User messages with images use content parts.
- Text file previews are appended to the text part under `Attached file <filename>:` blocks.
- Unsupported image/file context returns `ErrWebChatUnsupportedContext` before payload creation.
- General binary files are visible in the UI and downloadable, but not sent to the model as context.

- [ ] **Step 3: Run adapter tests**

Run:

```bash
cd backend
go test ./internal/service -run TestBuildWebChatCompletionsPayload -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/web_chat_adapter.go backend/internal/service/web_chat_adapter_test.go
git commit -m "feat: add web chat provider adapter"
```

## Task 7: Dispatch, Streaming Capture, And Billing

**Files:**
- Create: `backend/internal/service/web_chat_capture.go`
- Create: `backend/internal/service/web_chat_dispatch.go`
- Modify: `backend/internal/service/account_usage_service.go`
- Modify: `backend/internal/repository/usage_log_repo.go`
- Test: `backend/internal/service/web_chat_service_test.go`
- Test: `backend/internal/repository/usage_log_repo_integration_test.go`

- [ ] **Step 1: Add deterministic usage log lookup**

Add to `UsageLogRepository`:

```go
GetByRequestIDAndAPIKeyID(ctx context.Context, requestID string, apiKeyID int64) (*UsageLog, error)
```

Implement in `usage_log_repo.go`:

```go
func (r *usageLogRepository) GetByRequestIDAndAPIKeyID(ctx context.Context, requestID string, apiKeyID int64) (*service.UsageLog, error) {
    requestID = strings.TrimSpace(requestID)
    if requestID == "" || apiKeyID <= 0 {
        return nil, service.ErrUsageLogNotFound
    }
    query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE request_id = $1 AND api_key_id = $2 LIMIT 1"
    row := r.sql.QueryRowContext(ctx, query, requestID, apiKeyID)
    return scanUsageLog(row)
}
```

Use the repository's existing not-found error style if `ErrUsageLogNotFound` has a different name.

- [ ] **Step 2: Write dispatch tests**

Create tests that use stubs for gateway forwarding and billing:

```go
func TestWebChatSend_UsesHiddenKeyAndSubscriptionFirstBilling(t *testing.T) {
    svc := newWebChatServiceWithStubs(t)
    svc.gatewayForwardResult = &ForwardResult{
        RequestID: "upstream_req",
        Model: "claude-sonnet-4",
        Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 20},
        Duration: 100 * time.Millisecond,
    }

    result, err := svc.SendMessage(context.Background(), WebChatSendInput{
        UserID: 42,
        ConversationID: 7,
        Model: "claude-sonnet-4",
        Provider: "anthropic",
        Text: "hello",
        Stream: true,
    })

    require.NoError(t, err)
    require.Equal(t, int64(42), svc.ensureWebChatKeyUserID)
    require.Equal(t, "/api/v1/chat/conversations/7/messages", svc.recordUsageInput.InboundEndpoint)
    require.NotNil(t, svc.recordUsageInput.Subscription)
    require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
}

func TestWebChatSend_BlocksUnsupportedContextBeforeBilling(t *testing.T) {
    svc := newWebChatServiceWithStubs(t)
    _, err := svc.SendMessage(context.Background(), WebChatSendInput{
        UserID: 42,
        ConversationID: 7,
        Model: "text-only",
        Provider: "anthropic",
        AttachmentIDs: []int64{99},
    })
    require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
    require.False(t, svc.recordUsageCalled)
}
```

- [ ] **Step 3: Implement response capture**

Create a writer wrapper:

```go
type WebChatResponseCapture struct {
    gin.ResponseWriter
    body bytes.Buffer
    maxCaptureBytes int
}

func NewWebChatResponseCapture(w gin.ResponseWriter, maxCaptureBytes int) *WebChatResponseCapture {
    return &WebChatResponseCapture{ResponseWriter: w, maxCaptureBytes: maxCaptureBytes}
}

func (w *WebChatResponseCapture) Write(p []byte) (int, error) {
    if w.body.Len() < w.maxCaptureBytes {
        remaining := w.maxCaptureBytes - w.body.Len()
        if len(p) > remaining {
            w.body.Write(p[:remaining])
        } else {
            w.body.Write(p)
        }
    }
    return w.ResponseWriter.Write(p)
}

func (w *WebChatResponseCapture) Body() []byte {
    return append([]byte(nil), w.body.Bytes()...)
}
```

Add extractors:

```go
func ExtractAssistantTextFromChatCompletions(body []byte, streamed bool) string
func ExtractArtifactsFromChatCompletions(body []byte, streamed bool) []WebChatArtifactCandidate
```

For streamed bodies, parse `data: <json>` lines, ignore `[DONE]`, append `choices[0].delta.content`. For buffered bodies, append `choices[0].message.content` or text content parts.

- [ ] **Step 4: Implement dispatch flow**

In `web_chat_dispatch.go`, implement a method used by `WebChatService.SendMessage`:

```go
func (s *WebChatService) dispatchChatCompletions(c *gin.Context, input webChatDispatchInput) (*webChatDispatchResult, error)
```

Flow:

1. Resolve available group for the selected model's platform from `APIKeyService.GetAvailableGroups`.
2. Call `APIKeyService.EnsureWebChatKey(ctx, user, group)`.
3. Resolve subscription with `SubscriptionService.ResolveActiveSubscriptionForRoutedGroup(ctx, user.ID, group.ID)`.
4. Run the same preflight shape used by gateway handlers:
   - subscription limit validation through `ValidateAndCheckLimits`
   - `BillingCacheService.CheckBillingEligibility`
5. Build Chat Completions request with `BuildWebChatCompletionsPayload`.
6. Wrap `c.Writer` with `NewWebChatResponseCapture(c.Writer, 4<<20)`.
7. Select and forward:
   - OpenAI platform: use `OpenAIGatewayService.SelectAccountWithLoadAwareness` and `OpenAIGatewayService.ForwardAsChatCompletions`.
   - Anthropic platform: use `GatewayService.SelectAccountWithLoadAwareness` and `GatewayService.ForwardAsChatCompletions`.
   - Gemini platform: use `GatewayService.SelectAccountWithLoadAwareness`; if selected account platform is Gemini, use `GeminiMessagesCompatService.ForwardAsChatCompletions`, otherwise use `GatewayService.ForwardAsChatCompletions`.
8. Set deterministic request ID before recording usage:

```go
usageClientID := fmt.Sprintf("webchat-message-%d", input.AssistantMessageID)
usageCtx := context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, usageClientID)
usageRequestID := "client:" + usageClientID
```

9. Call the matching `RecordUsage` method with:

```go
InboundEndpoint: fmt.Sprintf("/api/v1/chat/conversations/%d/messages", input.ConversationID),
UserAgent: c.GetHeader("User-Agent"),
IPAddress: ip.GetClientIP(c),
APIKeyService: s.apiKeyService,
QuotaPlatform: service.QuotaPlatform(c.Request.Context(), hiddenKey),
ChannelUsageFields: channelMapping.ToUsageFields(input.Model, result.UpstreamModel),
```

10. Lookup usage log with `GetByRequestIDAndAPIKeyID(ctx, usageRequestID, hiddenKey.ID)` and update assistant `usage_log_id` when found.

- [ ] **Step 5: Run dispatch tests**

Run:

```bash
cd backend
go test ./internal/service -run 'TestWebChatSend|TestWebChatResponseCapture' -count=1
go test ./internal/repository -run TestUsageLogRepository_GetByRequestIDAndAPIKeyID -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/web_chat_capture.go backend/internal/service/web_chat_dispatch.go backend/internal/service/web_chat_service_test.go backend/internal/service/account_usage_service.go backend/internal/repository/usage_log_repo.go backend/internal/repository/usage_log_repo_integration_test.go
git commit -m "feat: dispatch web chat through gateway billing"
```

## Task 8: Web Chat Service, Handler, Routes, And Wire

**Files:**
- Create: `backend/internal/service/web_chat_service.go`
- Create: `backend/internal/handler/web_chat_handler.go`
- Modify: `backend/internal/server/routes/user.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/cmd/server/wire_gen.go`
- Test: `backend/internal/handler/web_chat_handler_test.go`

- [ ] **Step 1: Write handler tests**

Create `web_chat_handler_test.go`:

```go
func TestWebChatRoutesRequireAuthenticatedUser(t *testing.T) {
    router := gin.New()
    h := &handler.Handlers{Chat: handler.NewWebChatHandler(fakeWebChatService{})}
    routes.RegisterUserRoutes(router.Group("/api/v1"), h, func(c *gin.Context) {
        c.AbortWithStatus(http.StatusUnauthorized)
    }, fakeSettingService())

    req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil)
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWebChatCreateConversationUsesCurrentUser(t *testing.T) {
    svc := &fakeWebChatService{}
    router := newAuthenticatedWebChatRouter(t, svc, 42)
    req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations", strings.NewReader(`{"model":"gpt-5","provider":"openai"}`))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    require.Equal(t, http.StatusOK, w.Code)
    require.Equal(t, int64(42), svc.createConversationUserID)
}
```

- [ ] **Step 2: Implement service methods**

`WebChatService` constructor dependencies:

```go
func NewWebChatService(
    repo WebChatRepository,
    storage WebChatStorage,
    userRepo UserRepository,
    apiKeyService *APIKeyService,
    subscriptionService *SubscriptionService,
    billingCacheService *BillingCacheService,
    gatewayService *GatewayService,
    openAIGatewayService *OpenAIGatewayService,
    geminiCompatService *GeminiMessagesCompatService,
    usageLogRepo UsageLogRepository,
    cfg *config.Config,
) *WebChatService
```

Public methods:

```go
ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]WebChatConversation, *pagination.PaginationResult, error)
CreateConversation(ctx context.Context, userID int64, in CreateWebChatConversationInput) (*WebChatConversation, error)
GetConversation(ctx context.Context, userID, conversationID int64) (*WebChatConversationDetail, error)
UpdateConversation(ctx context.Context, userID, conversationID int64, in UpdateWebChatConversationInput) (*WebChatConversation, error)
DeleteConversation(ctx context.Context, userID, conversationID int64) error
UploadAttachment(ctx context.Context, userID int64, file multipart.File, header *multipart.FileHeader) (*WebChatAttachment, error)
OpenAttachment(ctx context.Context, userID, attachmentID int64) (io.ReadCloser, WebChatDownloadMeta, error)
OpenArtifact(ctx context.Context, userID, artifactID int64) (io.ReadCloser, WebChatDownloadMeta, error)
ListModels(ctx context.Context, userID int64) ([]WebChatModelCapability, error)
SendMessage(c *gin.Context, in WebChatSendInput) (*WebChatSendResult, error)
CancelMessage(ctx context.Context, userID, conversationID, messageID int64) error
```

- [ ] **Step 3: Implement handler**

Handler responsibilities:

- Read current user from `middleware.GetAuthSubjectFromContext`.
- Parse JSON bodies and `multipart/form-data`.
- Convert service errors with existing response helpers:
  - not found -> 404
  - unsupported context/upload rejected -> 400
  - billing errors -> their existing status/code when possible
- For downloads, set:

```go
c.Header("Content-Type", meta.ContentType)
c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Filename}))
c.Header("Content-Length", strconv.FormatInt(meta.SizeBytes, 10))
```

- [ ] **Step 4: Register routes**

In `RegisterUserRoutes`, add:

```go
chat := authenticated.Group("/chat")
{
    chat.GET("/models", h.Chat.ListModels)
    chat.GET("/conversations", h.Chat.ListConversations)
    chat.POST("/conversations", h.Chat.CreateConversation)
    chat.GET("/conversations/:id", h.Chat.GetConversation)
    chat.PATCH("/conversations/:id", h.Chat.UpdateConversation)
    chat.DELETE("/conversations/:id", h.Chat.DeleteConversation)
    chat.POST("/conversations/:id/messages", h.Chat.SendMessage)
    chat.POST("/conversations/:id/messages/:message_id/cancel", h.Chat.CancelMessage)
    chat.POST("/attachments", h.Chat.UploadAttachment)
    chat.GET("/attachments/:id/download", h.Chat.DownloadAttachment)
    chat.GET("/artifacts/:id/download", h.Chat.DownloadArtifact)
}
```

- [ ] **Step 5: Wire dependencies**

Modify provider sets and run:

```bash
cd backend
go generate ./cmd/server
```

Expected: `backend/cmd/server/wire_gen.go` updates without manual edits.

- [ ] **Step 6: Run backend route tests and compile**

Run:

```bash
cd backend
go test ./internal/handler -run TestWebChat -count=1
go test ./internal/server/routes -run Test -count=1
go test ./cmd/server -run Test -count=1
```

Expected: PASS or "no test files" for packages without tests, with exit status 0.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/web_chat_service.go backend/internal/handler/web_chat_handler.go backend/internal/server/routes/user.go backend/internal/service/wire.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/cmd/server/wire_gen.go backend/internal/handler/web_chat_handler_test.go
git commit -m "feat: expose web chat api"
```

## Task 9: Frontend Chat API And Store

**Files:**
- Create: `frontend/src/api/chat.ts`
- Create: `frontend/src/stores/chat.ts`
- Test: `frontend/src/components/chat/__tests__/chatStore.spec.ts`

- [ ] **Step 1: Write API types**

Create `frontend/src/api/chat.ts` with exact response shapes:

```ts
import { apiClient } from './client'

export interface WebChatModel {
  provider: string
  platform: string
  key_type: string
  model: string
  display_name: string
  supports_text: boolean
  supports_image_input: boolean
  supports_file_context: boolean
  supports_artifact_output: boolean
  price_status: 'confirmed' | 'unverified'
}

export interface WebChatConversation {
  id: number
  title: string
  default_model: string
  default_provider: string
  last_model: string
  last_provider: string
  status: 'active' | 'archived' | 'deleted'
  message_count: number
  last_message_at?: string
  created_at: string
  updated_at: string
}
```

Add functions:

```ts
export async function listChatModels(): Promise<WebChatModel[]> {
  const { data } = await apiClient.get<WebChatModel[]>('/chat/models')
  return data
}

export async function uploadChatAttachment(file: File): Promise<WebChatAttachment> {
  const form = new FormData()
  form.append('file', file)
  const { data } = await apiClient.post<WebChatAttachment>('/chat/attachments', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return data
}
```

For streaming send, use native `fetch` so the browser can read `ReadableStream`:

```ts
export async function streamChatMessage(input: StreamChatMessageInput): Promise<StreamChatMessageResult> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(`/api/v1/chat/conversations/${input.conversationId}/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify({
      model: input.model,
      provider: input.provider,
      content: input.content,
      attachment_ids: input.attachmentIds,
      stream: true,
    }),
    signal: input.signal,
  })
  if (!response.ok || !response.body) {
    throw await buildChatStreamError(response)
  }
  return {
    response,
    userMessageId: Number(response.headers.get('X-Web-Chat-User-Message-ID') || 0),
    assistantMessageId: Number(response.headers.get('X-Web-Chat-Assistant-Message-ID') || 0),
  }
}
```

- [ ] **Step 2: Write store tests**

Create a store test:

```ts
it('appends streamed assistant chunks without replacing prior text', async () => {
  setActivePinia(createPinia())
  const store = useChatStore()
  store.startAssistantStream({ id: 10, conversationId: 1, model: 'gpt-5', provider: 'openai' })
  store.appendAssistantDelta('hello')
  store.appendAssistantDelta(' world')
  store.finishAssistantStream()
  expect(store.currentMessages.at(-1)?.content_text).toBe('hello world')
  expect(store.streaming).toBe(false)
})

it('detects unsupported attachments for selected model', () => {
  setActivePinia(createPinia())
  const store = useChatStore()
  store.selectedModel = {
    provider: 'anthropic',
    platform: 'anthropic',
    key_type: 'anthropic',
    model: 'text-only',
    display_name: 'Text Only',
    supports_text: true,
    supports_image_input: false,
    supports_file_context: true,
    supports_artifact_output: false,
    price_status: 'confirmed',
  }
  store.pendingAttachments = [{ id: 1, kind: 'image', filename: 'x.png', content_type: 'image/png', size_bytes: 10 }]
  expect(store.capabilityWarning).toContain('image')
})
```

- [ ] **Step 3: Implement Pinia store**

State:

```ts
export const useChatStore = defineStore('chat', {
  state: () => ({
    models: [] as WebChatModel[],
    conversations: [] as WebChatConversation[],
    currentConversation: null as WebChatConversationDetail | null,
    selectedModel: null as WebChatModel | null,
    pendingAttachments: [] as WebChatAttachment[],
    streaming: false,
    abortController: null as AbortController | null,
    error: '',
  }),
  getters: {
    currentMessages: (state) => state.currentConversation?.messages ?? [],
    capabilityWarning: (state) => buildCapabilityWarning(state.selectedModel, state.pendingAttachments),
  },
})
```

Actions:

- `loadModels`
- `loadConversations`
- `openConversation`
- `createConversation`
- `renameConversation`
- `deleteConversation`
- `uploadAttachment`
- `sendMessage`
- `cancelStream`
- `startAssistantStream`
- `appendAssistantDelta`
- `finishAssistantStream`

Stream parser reads `data: <json>` lines and appends `choices[0].delta.content`.

- [ ] **Step 4: Run frontend store tests**

Run:

```bash
cd frontend
pnpm test:run src/components/chat/__tests__/chatStore.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/chat.ts frontend/src/stores/chat.ts frontend/src/components/chat/__tests__/chatStore.spec.ts
git commit -m "feat: add web chat frontend state"
```

## Task 10: Frontend Chat Page And Components

**Files:**
- Create: `frontend/src/views/user/ChatView.vue`
- Create: `frontend/src/components/chat/ChatShell.vue`
- Create: `frontend/src/components/chat/ConversationRail.vue`
- Create: `frontend/src/components/chat/MessageList.vue`
- Create: `frontend/src/components/chat/ModelSelector.vue`
- Create: `frontend/src/components/chat/Composer.vue`
- Create: `frontend/src/components/chat/AttachmentChip.vue`
- Test: `frontend/src/components/chat/__tests__/ChatView.spec.ts`
- Test: `frontend/src/views/user/__tests__/ChatViewSource.spec.ts`

- [ ] **Step 1: Write component smoke tests**

Create tests:

```ts
it('renders the chat workspace without marketing copy', () => {
  const wrapper = mount(ChatView, {
    global: {
      plugins: [createTestingPinia()],
      stubs: { RouterLink: true },
    },
  })
  expect(wrapper.text()).toContain('New chat')
  expect(wrapper.find('textarea').exists()).toBe(true)
  expect(wrapper.text()).not.toContain('Get started')
})

it('disables send while streaming', async () => {
  const wrapper = mount(Composer, {
    props: { streaming: true, disabled: false, capabilityWarning: '' },
    global: { stubs: { Icon: true } },
  })
  expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
  expect(wrapper.get('[data-testid="chat-stop"]').exists()).toBe(true)
})
```

- [ ] **Step 2: Implement layout**

`ChatView.vue` loads models and conversations on mount and renders `ChatShell`.

`ChatShell.vue` layout:

```vue
<template>
  <section class="chat-page min-h-[calc(100vh-4rem)] bg-linear-canvas text-linear-ink">
    <div class="grid h-[calc(100vh-4rem)] min-h-[640px] grid-cols-1 lg:grid-cols-[280px_minmax(0,1fr)]">
      <ConversationRail />
      <main class="flex min-w-0 flex-col">
        <ModelSelector />
        <MessageList class="min-h-0 flex-1" />
        <Composer />
      </main>
    </div>
  </section>
</template>
```

Keep radii at `rounded-lg` or smaller. Avoid nested cards. Use full-height panes with hairline borders.

- [ ] **Step 3: Implement expected controls**

`ConversationRail`:

- new chat icon button
- search input
- recent conversations list
- rename button using `Icon name="edit"`
- delete button using `Icon name="trash"`

`ModelSelector`:

- provider/model select
- capability badges for image, file context, artifact output
- warning banner when `store.capabilityWarning` is non-empty

`MessageList`:

- user/assistant bubbles
- attachment chips under user messages
- artifact download chips with `Icon name="download"`
- failed message state with retry action

`Composer`:

- textarea with Enter send and Shift+Enter newline
- image upload button
- file upload button
- send icon button
- stop icon button while streaming
- disabled state for empty text plus no attachments, streaming, or capability warning

- [ ] **Step 4: Run component tests**

Run:

```bash
cd frontend
pnpm test:run src/components/chat/__tests__/ChatView.spec.ts src/views/user/__tests__/ChatViewSource.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/views/user/ChatView.vue frontend/src/components/chat frontend/src/views/user/__tests__/ChatViewSource.spec.ts
git commit -m "feat: build web chat interface"
```

## Task 11: Route, Sidebar, Homepage, Icons, And I18n

**Files:**
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- Modify: `frontend/src/router/__tests__/guards.spec.ts`
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/components/icons/Icon.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`

- [ ] **Step 1: Add route guard test**

In `guards.spec.ts`, add:

```ts
it('未认证用户访问 /chat 重定向到 /login', () => {
  const redirect = simulateGuard('/chat', { requiresAuth: true, requiresAdmin: false }, authState)
  expect(redirect).toBe('/login')
})
```

Add authenticated case:

```ts
it('已认证普通用户可以访问 /chat', () => {
  const redirect = simulateGuard('/chat', { requiresAuth: true, requiresAdmin: false }, authState)
  expect(redirect).toBeNull()
})
```

- [ ] **Step 2: Add route**

In `router/index.ts`, add after `/dashboard/models`:

```ts
{
  path: '/chat',
  name: 'WebChat',
  component: () => import('@/views/user/ChatView.vue'),
  meta: {
    requiresAuth: true,
    requiresAdmin: false,
    title: 'Chat',
    titleKey: 'chat.title',
    descriptionKey: 'chat.description',
  },
},
```

- [ ] **Step 3: Add sidebar item**

In `AppSidebar.vue`, add `ChatIcon` using the existing chat path or `Icon.vue` path style. Insert the item before Models:

```ts
items.push(
  { path: '/chat', label: t('nav.chat'), icon: ChatIcon },
  { path: '/dashboard/models', label: t('nav.modelMarketplace'), icon: ModelCatalogIcon },
  ...
)
```

Update test:

```ts
describe('AppSidebar web chat navigation', () => {
  it('exposes the authenticated chat route before the model marketplace route', () => {
    expect(componentSource).toContain("path: '/chat'")
    expect(componentSource).toContain("t('nav.chat')")
    expect(componentSource.indexOf("path: '/chat'")).toBeLessThan(componentSource.indexOf("path: '/dashboard/models'"))
  })
})
```

- [ ] **Step 4: Add homepage chat entry**

In `HomeView.vue`, add header nav link `to="/chat"` for authenticated users and `to="/login?redirect=/chat"` for visitors. Add a near-hero product entry that visually resembles a chat composer and conversation rail, using actual UI markup rather than explanatory text blocks.

Use computed:

```ts
const chatPath = computed(() => (isAuthenticated.value ? '/chat' : { path: '/login', query: { redirect: '/chat' } }))
```

CTA text:

- zh: `开始网页对话`
- en: `Open web chat`

- [ ] **Step 5: Add icons and translations**

In `Icon.vue`, add paths:

```ts
paperclip: 'M18.375 12.739l-6.638 6.638a4.5 4.5 0 01-6.364-6.364l8.486-8.486a3 3 0 014.243 4.243l-8.486 8.486a1.5 1.5 0 11-2.121-2.121l7.425-7.425',
image: 'M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z',
send: 'M6 12L3.269 3.125A59.769 59.769 0 0121.485 12 59.768 59.768 0 013.27 20.875L6 12zm0 0h7.5',
stop: 'M5.25 5.25h13.5v13.5H5.25V5.25z',
```

Add translations:

```ts
nav: {
  chat: '对话',
}
chat: {
  title: '网页对话',
  description: '直接在网页中和可用模型对话',
  newChat: '新对话',
  searchConversations: '搜索对话',
  composerPlaceholder: '输入消息',
  send: '发送',
  stop: '停止',
  attachImage: '上传图片',
  attachFile: '上传文件',
  modelSelector: '选择模型',
  unsupportedImage: '当前模型不支持图片输入',
  unsupportedFile: '当前模型不支持文件上下文',
}
```

Add English equivalents in `en.ts`.

- [ ] **Step 6: Run frontend tests**

Run:

```bash
cd frontend
pnpm test:run src/components/layout/__tests__/AppSidebar.spec.ts src/router/__tests__/guards.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/router/index.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/router/__tests__/guards.spec.ts frontend/src/views/HomeView.vue frontend/src/components/icons/Icon.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts
git commit -m "feat: add web chat entry points"
```

## Task 12: End-To-End Verification

**Files:**
- Modify only files required to fix failures discovered by these commands.

- [ ] **Step 1: Regenerate backend code**

Run:

```bash
cd backend
go generate ./ent
go generate ./cmd/server
```

Expected: no command errors. If generated files change, inspect them and include them in the final commit.

- [ ] **Step 2: Run focused backend tests**

Run:

```bash
cd backend
go test ./internal/service -run 'TestWebChat|TestEnsureWebChatKey|TestBuildWebChatCompletionsPayload' -count=1
go test ./internal/repository -run 'TestWebChatRepository|TestUsageLogRepository_GetByRequestIDAndAPIKeyID|TestAPIKeyRepoSuite/TestListByUserID_ExcludesWebChatKeys' -count=1
go test ./internal/handler -run 'TestWebChat|TestSettingHandler_GetPublicModelCatalog' -count=1
go test ./internal/server/middleware -run TestAPIKeyAuthRejectsWebChatKey -count=1
```

Expected: all PASS.

- [ ] **Step 3: Run broader backend compile**

Run:

```bash
cd backend
go test ./internal/service ./internal/handler ./internal/server ./cmd/server
```

Expected: PASS or "no test files" with exit status 0.

- [ ] **Step 4: Run frontend tests**

Run:

```bash
cd frontend
pnpm test:run src/components/chat/__tests__/chatStore.spec.ts src/components/chat/__tests__/ChatView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/router/__tests__/guards.spec.ts
pnpm typecheck
pnpm build
```

Expected: all commands exit 0.

- [ ] **Step 5: Browser verification**

Start frontend dev server:

```bash
cd frontend
pnpm dev --host 0.0.0.0
```

Use the printed URL. Verify these viewports with Playwright or the browser plugin:

- Desktop `1440x900`: `/home` shows a chat CTA and no overlapping text.
- Desktop `1440x900`: logged-in `/chat` shows conversation rail, message pane, model selector, and composer.
- Mobile `390x844`: `/chat` keeps controls reachable and text inside buttons.
- While streaming, composer send button is disabled and stop button is visible.
- Upload chips do not resize the composer unexpectedly.

- [ ] **Step 6: Manual billing verification in local/dev data**

With a test user that can route to a configured group:

1. Record current subscription usage and balance.
2. Send one short `/chat` message.
3. Confirm a `usage_logs` row exists with `inbound_endpoint = '/api/v1/chat/conversations/<id>/messages'`.
4. Confirm `usage_logs.api_key_id` points to an `api_keys.key_type = 'web_chat'` row.
5. Confirm user-visible `/keys` API does not return that hidden key.
6. Confirm subscription usage increases before balance decreases when an active subscription applies.

- [ ] **Step 7: Final commit**

If fixes were needed during verification:

```bash
git add backend frontend
git commit -m "test: verify web chat integration"
```

If no fixes were needed, do not create an empty commit.

## Completion Criteria

The work is complete only when all of these are true:

- `/chat` is authenticated and usable after login.
- Homepage and user sidebar both link to Web Chat.
- Conversations and message history persist.
- Users can upload allowed images and files.
- Unsupported attachments are blocked before billing for models that cannot use them.
- Model switching is allowed per turn with server-side capability validation.
- Assistant responses stream to the browser and are persisted.
- Artifacts stored by Web Chat can be downloaded only by their owner.
- Usage logs and billing reuse the existing gateway logic with hidden Web Chat API Key attribution.
- Hidden Web Chat API Keys are invisible in user API Key list/detail and rejected by public API Key auth.
- Focused backend tests, frontend tests, frontend typecheck, and frontend build pass.

## Self-Review

Spec coverage:

- Product entry points: Task 11.
- Persistent conversations/history: Tasks 1, 3, 8, 9, 10.
- Streaming assistant responses: Tasks 7, 9, 10.
- Model switching with capability checks: Tasks 5, 6, 7, 9.
- Image/file upload and downloads: Tasks 4, 8, 10.
- Model-generated artifacts: Tasks 1, 4, 7, 8, 10.
- Billing parity and hidden keys: Tasks 2, 7, 12.
- Security and ownership: Tasks 3, 4, 8, 12.
- Tests and verification: each task plus Task 12.

Placeholder scan:

- This plan intentionally avoids deferred implementation markers. Every task names files, functions, test commands, and commit commands.

Type consistency:

- `WebChatModelCapability`, `WebChatContextSummary`, `WebChatConversation`, `WebChatMessage`, `WebChatAttachment`, and `WebChatArtifact` are introduced before subsequent tasks reference them.
- Hidden API Key type is `service.APIKeyTypeWebChat` everywhere.
- The authenticated frontend route is `/chat`, matching the approved spec.
