# Capture Settings, OpenAI HTTP Archive, and Loss Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an independent admin capture-settings page, runtime capture policy with OpenAI disabled by default, complete OpenAI HTTP text capture, and observable loss accounting with 30-day PostgreSQL history.

**Architecture:** Keep ClickHouse connectivity and capacity controls in YAML while storing one versioned runtime policy in the existing `settings` table. A request-scoped compiled policy decides capture before buffers are allocated; accepted records flow through the existing worker/batch pipeline with one lifecycle tracker that retains the byte reservation until ClickHouse succeeds or the record is explicitly dropped. A dedicated PostgreSQL repository persists minute-level loss aggregates asynchronously so admin history survives restarts without adding synchronous writes to the forwarding path.

**Tech Stack:** Go 1.x, Gin, PostgreSQL (`database/sql`), ClickHouse Go v2, Pond v2, Wire, Vue 3, TypeScript, Pinia, Axios, Vitest, Vue Test Utils, Tailwind CSS.

## Global Constraints

- The admin navigation label is `转存设置` / `Capture Settings`, at `/admin/capture-settings`, immediately before System Settings in normal and simple admin modes.
- Static `gateway.capture.enabled` and ClickHouse credentials remain YAML/Secret-only and require restart; the admin API never returns credentials.
- Runtime capture defaults are master off, Anthropic on, Kiro on, and OpenAI off.
- OpenAI coverage is HTTP text only: `/v1/responses`, `/v1/chat/completions`, and `/v1/messages`; WebSocket, image, video, embeddings, and alpha-search paths remain out of scope.
- Only final successful calls and terminal upstream HTTP errors are archived; intermediate failover attempts and pre-upstream/local errors are not.
- Runtime policy or storage failure must fail closed for capture and must never block or fail the forwarding request.
- `policy_skipped` is not loss; `max_body_bytes` truncation is not whole-record loss and continues to set `is_truncated=1`.
- Whole-record and whole-batch loss must be visible in real time and in PostgreSQL minute aggregates retained for 30 days.
- `max_queue_bytes` covers a record until ClickHouse success or explicit loss, including worker queue, writer queue, and pending batch.
- No disk spool and no ClickHouse schema migration.

---

### Task 1: Provisioning Configuration and Durable Health Schema

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Create: `backend/migrations/191_capture_health_events.sql`
- Create: `backend/migrations/capture_health_events_migration_test.go`
- Create: `backend/internal/service/capture_health_port.go`
- Create: `backend/internal/repository/capture_health_repo.go`
- Create: `backend/internal/repository/capture_health_repo_test.go`
- Modify: `backend/internal/repository/wire.go`

**Interfaces:**
- Produces: `GatewayCaptureConfig.WriterQueueSize int` mapped from `gateway.capture.writer_queue_size`, default `1024`, required `> 0` when capture is provisioned.
- Produces: `CaptureHealthRepository.UpsertEvents(context.Context, []CaptureHealthEvent) error`, `ListEvents(context.Context, time.Time, time.Time) ([]CaptureHealthEvent, error)`, and `DeleteBefore(context.Context, time.Time) (int64, error)`.
- Produces: PostgreSQL uniqueness on `(minute_bucket, instance_id, reason)` and additive upsert semantics for counts/bytes.

- [ ] **Step 1: Write failing config and migration tests**

```go
func TestCaptureDefaultsExposeWriterQueueSize(t *testing.T) {
    cfg := loadTestConfig(t)
    require.Equal(t, 1024, cfg.Gateway.Capture.WriterQueueSize)
}

func TestCaptureRejectsNonPositiveWriterQueueSize(t *testing.T) {
    cfg := validCaptureConfig()
    cfg.WriterQueueSize = 0
    require.ErrorContains(t, cfg.validate(), "writer_queue_size")
}
```

Migration test assertions must execute the migration against the existing controlled migration fixture and verify the table columns `minute_bucket`, `instance_id`, `reason`, `dropped_records`, `dropped_bytes`, `worker_queue_peak`, `writer_queue_peak`, `in_flight_bytes_peak`, and `last_error`, plus the unique key.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend && go test ./internal/config ./migrations ./internal/repository -run 'Capture|capture'`

Expected: FAIL because the config field, migration, repository types, and methods do not exist.

- [ ] **Step 3: Implement config, migration, and repository**

Use this table contract:

```sql
CREATE TABLE IF NOT EXISTS capture_health_events (
  id BIGSERIAL PRIMARY KEY,
  minute_bucket TIMESTAMPTZ NOT NULL,
  instance_id VARCHAR(255) NOT NULL,
  reason VARCHAR(64) NOT NULL,
  dropped_records BIGINT NOT NULL DEFAULT 0,
  dropped_bytes BIGINT NOT NULL DEFAULT 0,
  worker_queue_peak BIGINT NOT NULL DEFAULT 0,
  writer_queue_peak BIGINT NOT NULL DEFAULT 0,
  in_flight_bytes_peak BIGINT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (minute_bucket, instance_id, reason)
);
CREATE INDEX IF NOT EXISTS idx_capture_health_events_bucket
  ON capture_health_events (minute_bucket DESC, id DESC);
```

`UpsertEvents` must add `dropped_records` and `dropped_bytes`, take `GREATEST` for queue/byte peaks, replace `last_error` only with a non-empty incoming value, and update `updated_at`.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `cd backend && go test ./internal/config ./migrations ./internal/repository -run 'Capture|capture'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go backend/migrations/191_capture_health_events.sql backend/migrations/capture_health_events_migration_test.go backend/internal/service/capture_health_port.go backend/internal/repository/capture_health_repo.go backend/internal/repository/capture_health_repo_test.go backend/internal/repository/wire.go
git commit -m "feat(capture): add health history storage"
```

### Task 2: Versioned Runtime Capture Policy

**Files:**
- Create: `backend/internal/service/capture_runtime_policy.go`
- Create: `backend/internal/service/capture_runtime_policy_test.go`
- Create: `backend/internal/service/capture_runtime_policy_service.go`
- Create: `backend/internal/service/capture_runtime_policy_service_test.go`
- Modify: `backend/internal/service/setting_service.go`

**Interfaces:**
- Produces: `CaptureRuntimePolicy`, `CompiledCapturePolicy`, `DefaultCaptureRuntimePolicy()`, `ValidateAndNormalizeCaptureRuntimePolicy(CaptureRuntimePolicy)`, and `CompiledCapturePolicy.Match(platform string, outcome CaptureOutcome, userID int64, groupID *int64) bool`.
- Produces: `SettingService.GetCaptureRuntimePolicy(context.Context) (CaptureRuntimePolicy, error)`, `SettingService.GetCompiledCaptureRuntimePolicy(context.Context) CompiledCapturePolicy`, and `SettingService.UpdateCaptureRuntimePolicy(context.Context, CaptureRuntimePolicy) (CaptureRuntimePolicy, error)`.
- Storage key: `capture_runtime_policy`; unknown JSON fields and versions are rejected on write, while missing key returns the documented default.

- [ ] **Step 1: Write failing policy behavior tests**

```go
func TestDefaultCaptureRuntimePolicyKeepsOpenAIOff(t *testing.T) {
    got := DefaultCaptureRuntimePolicy()
    require.False(t, got.Enabled)
    require.True(t, got.Platforms.Anthropic)
    require.True(t, got.Platforms.Kiro)
    require.False(t, got.Platforms.OpenAI)
}

func TestCompiledCapturePolicyRequiresBothConfiguredFilters(t *testing.T) {
    compiled := mustCompileCapturePolicy(t, CaptureRuntimePolicy{
        Version: 1, Enabled: true,
        Platforms: CapturePlatformPolicy{OpenAI: true},
        Outcomes: CaptureOutcomePolicy{Success: true},
        GroupIDs: []int64{7}, UserIDs: []int64{9},
    })
    group := int64(7)
    require.True(t, compiled.Match("openai", CaptureOutcomeSuccess, 9, &group))
    require.False(t, compiled.Match("openai", CaptureOutcomeSuccess, 8, &group))
}
```

Add cases for ID sort/dedup, zero/negative IDs, unknown fields, unknown version, platform/outcome combinations, DB error fail-closed with a five-second cache entry, cache hit/expiry/singleflight, and save-immediately-refreshes-cache.

- [ ] **Step 2: Run policy tests and verify RED**

Run: `cd backend && go test ./internal/service -run 'CaptureRuntimePolicy'`

Expected: FAIL because the policy API is absent.

- [ ] **Step 3: Implement the policy and cache**

Decode stored JSON with `json.Decoder.DisallowUnknownFields`. Use a per-`SettingService` `atomic.Value` entry and `singleflight.Group`, a 60-second success TTL, a 5-second fail-closed TTL, and a five-second detached DB timeout. Marshal the entire normalized v1 policy into one setting value; write DB first, then atomically replace the cache.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `cd backend && go test ./internal/service -run 'CaptureRuntimePolicy'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_runtime_policy.go backend/internal/service/capture_runtime_policy_test.go backend/internal/service/capture_runtime_policy_service.go backend/internal/service/capture_runtime_policy_service_test.go backend/internal/service/setting_service.go
git commit -m "feat(capture): add runtime archive policy"
```

### Task 3: Loss Tracker, Reporter, and End-to-End Byte Ownership

**Files:**
- Create: `backend/internal/service/capture_health.go`
- Create: `backend/internal/service/capture_health_test.go`
- Create: `backend/internal/service/capture_health_reporter.go`
- Create: `backend/internal/service/capture_health_reporter_test.go`
- Modify: `backend/internal/service/conversation_capture_pool.go`
- Modify: `backend/internal/service/conversation_capture_pool_test.go`
- Modify: `backend/internal/service/clickhouse_archive_writer.go`
- Modify: `backend/internal/service/clickhouse_archive_writer_test.go`

**Interfaces:**
- Produces: loss reasons `byte_budget_exceeded`, `worker_queue_full`, `writer_queue_full`, `writer_unavailable`, `clickhouse_prepare_failed`, `clickhouse_append_failed`, and `clickhouse_send_failed`.
- Produces: `CaptureHealthSnapshot` with totals, reason buckets, current/peak worker and writer queue counts, current/peak in-flight bytes, start/success/drop timestamps, and latest 100 incidents.
- Produces: `ConversationCapturePool.Health() CaptureHealthSnapshot`, `Ready() bool`, and `InitializationError() string`.
- Changes: `ArchiveWriter.Write` accepts an internal lifecycle item whose completion callback releases bytes exactly once; successful `Send` reports written records, and every terminal failure reports loss for the whole affected item/batch.

- [ ] **Step 1: Write failing tracker and queue-lifecycle tests**

```go
func TestCapturePoolReportsByteBudgetLoss(t *testing.T) {
    writer := newBlockingLifecycleWriter()
    pool := newTestCapturePool(100, 8, writer)
    pool.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
    writer.WaitForOne(t)
    pool.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
    got := pool.Health()
    require.Equal(t, uint64(1), got.DroppedByReason[CaptureDropByteBudgetExceeded].Records)
    require.Equal(t, uint64(60), got.DroppedByReason[CaptureDropByteBudgetExceeded].Bytes)
}

func TestCaptureBytesRemainReservedUntilWriterCompletion(t *testing.T) {
    writer := newDeferredLifecycleWriter()
    pool := newTestCapturePool(1024, 8, writer)
    pool.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
    item := writer.Take(t)
    require.Equal(t, int64(60), pool.Health().InFlightBytes.Current)
    item.Complete(CaptureWriteSuccess, "")
    require.Eventually(t, func() bool { return pool.Health().InFlightBytes.Current == 0 }, time.Second, time.Millisecond)
}
```

Add tests for worker queue full, writer queue full, unavailable writer, prepare/append/send batch loss counts, successful batch writes, exactly-once release, ring-buffer cap/order, sanitized/truncated error summary, minute aggregation, failed reporter upsert retained for retry without double counting, successful retry removal, and 30-day cleanup.

- [ ] **Step 2: Run health tests and verify RED**

Run: `cd backend && go test ./internal/service -run 'Capture(Health|Pool|ArchiveWriter|Reporter)'`

Expected: FAIL because health and lifecycle interfaces do not exist.

- [ ] **Step 3: Implement tracker and lifecycle ownership**

Use atomics for hot counters/gauges and a small mutex only for the 100-entry incident ring and minute map. The worker queue count decrements when a worker starts; the writer queue count increments only after writer enqueue and decrements when the batcher receives the item. A lifecycle item owns `recordBytes(rec)` and a `sync.Once` completion callback. `max_queue_bytes` is released only from that callback.

Writer rules:

```text
Write queue full       -> reject item; pool completes writer_queue_full
PrepareBatch failure   -> complete every batch item clickhouse_prepare_failed
Append failure         -> complete every batch item clickhouse_append_failed
Send failure           -> complete every batch item clickhouse_send_failed
Send success           -> complete every batch item success
```

The reporter snapshots completed minute buckets into a bounded internal retry queue, upserts asynchronously once per minute, retries failed batches without re-adding counts, deletes data older than 30 days hourly, and performs a bounded final flush during `Stop`.

- [ ] **Step 4: Run focused tests and race detector**

Run: `cd backend && go test -race ./internal/service -run 'Capture(Health|Pool|ArchiveWriter|Reporter)'`

Expected: PASS with no races.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_health.go backend/internal/service/capture_health_test.go backend/internal/service/capture_health_reporter.go backend/internal/service/capture_health_reporter_test.go backend/internal/service/conversation_capture_pool.go backend/internal/service/conversation_capture_pool_test.go backend/internal/service/clickhouse_archive_writer.go backend/internal/service/clickhouse_archive_writer_test.go
git commit -m "feat(capture): expose archive loss health"
```

### Task 4: Request-Scoped Capture Decision and Content Controls

**Files:**
- Create: `backend/internal/service/capture_context.go`
- Create: `backend/internal/service/capture_context_test.go`
- Modify: `backend/internal/service/capture_record.go`
- Modify: `backend/internal/service/capture_record_test.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`

**Interfaces:**
- Produces: `PrepareCaptureScope(context.Context, *gin.Context, *SettingService, userID int64, groupID *int64)`, `CaptureDecisionFor(*gin.Context, platform string, outcome CaptureOutcome) (CaptureContentPolicy, bool)`, and bridge methods for final outbound request body/headers and raw upstream response body/headers.
- Produces: `ApplyCaptureContentPolicy(*CaptureRecord, CaptureContentPolicy)` that clears disabled persistence fields only after metadata extraction.

- [ ] **Step 1: Write failing request-scope and content tests**

```go
func TestCaptureDecisionShortCircuitsOpenAIBeforeBufferAllocation(t *testing.T) {
    c, _ := gin.CreateTestContext(httptest.NewRecorder())
    setCompiledCaptureScopeForTest(c, compiledPolicyWithOpenAI(false), 9, nil)
    _, ok := CaptureDecisionFor(c, "openai", CaptureOutcomeSuccess)
    require.False(t, ok)
    _, exists := c.Get(captureResultContextKey)
    require.False(t, exists)
}

func TestApplyCaptureContentPolicyKeepsMetadataAndClearsBodies(t *testing.T) {
    rec := &CaptureRecord{RawRequest: []byte(`{"model":"gpt-5"}`), RawResponse: []byte(`{"usage":{"output_tokens":4}}`)}
    extractCaptureColumns(rec)
    ApplyCaptureContentPolicy(rec, CaptureContentPolicy{})
    require.Equal(t, 4, rec.OutputTokens)
    require.Empty(t, rec.RawRequest)
    require.Empty(t, rec.RawResponse)
}
```

Add tests for request/response header switches, combined AND filters, nil setting service, failed policy read, and terminal-error outcome.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'Capture(Context|Decision|Content|Scope)'`

Expected: FAIL because request scope and content application are absent.

- [ ] **Step 3: Implement request scope and prepare it in handlers**

Prepare the immutable scope immediately after authenticated subject/API key lookup and before capture response tees or snapshots can be allocated. Keep final platform/outcome matching at the point the upstream account and terminal result are known. Extend the bridge so handlers submit the actual final outbound body rather than the inbound pre-mapping body.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'Capture(Context|Decision|Content|Scope)'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_context.go backend/internal/service/capture_context_test.go backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go backend/internal/handler/gateway_handler.go backend/internal/handler/openai_gateway_handler.go
git commit -m "feat(capture): add request scoped archive policy"
```

### Task 5: Apply Runtime Policy to Anthropic and Kiro Paths

**Files:**
- Modify: `backend/internal/service/gateway_forward.go`
- Modify: `backend/internal/service/gateway_upstream_response.go`
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/kiro_capture.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: relevant existing tests in `backend/internal/service/*capture*_test.go` and `backend/internal/handler/gateway_handler*_test.go`

**Interfaces:**
- Consumes: `CaptureDecisionFor`, request bridge, and `ApplyCaptureContentPolicy` from Task 4.
- Produces: existing Anthropic/Kiro success and terminal-error archive behavior gated by master/platform/outcome/user/group policy, with all four content switches honored.

- [ ] **Step 1: Write failing Anthropic/Kiro policy regression tests**

Use controlled `httptest.Server` upstreams and a lifecycle fake writer. For each platform, verify: master off submits zero; platform off submits zero; success on submits one; terminal-error off submits zero; terminal-error on submits exactly the final attempt; disabled raw response persists an empty body while token/stop metadata remains populated.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'Capture.*(Anthropic|Kiro)|(Anthropic|Kiro).*Capture'`

Expected: at least platform-off and content-switch cases FAIL under the current static-only checks.

- [ ] **Step 3: Replace static-only conditions with scoped decisions**

Preserve current Kiro semantic boundary (Anthropic-form request/translated response plus real sanitized Kiro headers). Remove capture allocations when policy does not match. Do not capture count-tokens, model probes, OAuth refreshes, retries, or local errors.

- [ ] **Step 4: Run platform capture regressions**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'Capture|Kiro|Anthropic'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/gateway_forward.go backend/internal/service/gateway_upstream_response.go backend/internal/service/kiro_runtime.go backend/internal/service/kiro_capture.go backend/internal/handler/gateway_handler.go backend/internal/service/*capture*_test.go backend/internal/handler/gateway_handler*_test.go
git commit -m "feat(capture): gate anthropic and kiro archives"
```

### Task 6: Capture All OpenAI HTTP Text Paths Independent of Passthrough

**Files:**
- Create: `backend/internal/service/openai_http_capture.go`
- Create: `backend/internal/service/openai_http_capture_test.go`
- Modify: `backend/internal/service/openai_gateway_forward.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/openai_gateway_messages.go`
- Modify: `backend/internal/service/openai_gateway_messages_chat_fallback.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions_raw.go`
- Modify: `backend/internal/service/openai_gateway_responses_chat_fallback.go`
- Modify: `backend/internal/service/openai_gateway_upstream_errors.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler_test.go`

**Interfaces:**
- Consumes: request-scoped policy and final outbound/response bridge from Task 4.
- Produces: one capture record for the final successful or terminal-error OpenAI HTTP attempt, whether the account uses standard conversion or `openai_passthrough`.
- Produces: actual upstream endpoint and final post-mapping/post-policy outbound body/header snapshot; raw upstream JSON/SSE response before client-protocol conversion.

- [ ] **Step 1: Write failing endpoint matrix tests**

Create table-driven handler tests for inbound `/v1/responses`, `/v1/chat/completions`, and `/v1/messages`, each with passthrough false/true where supported. Assert:

```text
default policy                      -> 0 OpenAI records
explicit OpenAI + success           -> 1 record
actual outbound model/body          -> matches upstream server capture
raw response                        -> matches upstream JSON/SSE before conversion
terminal upstream error enabled     -> 1 record with real HTTP status
first failover error + final success -> only final success record
WebSocket/image/video endpoint      -> 0 records
```

For header tests, upstream fixtures must receive an authorization header while the stored JSON must not contain its value, cookies, or API keys.

- [ ] **Step 2: Run OpenAI capture tests and verify RED**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'OpenAI.*Capture|Capture.*OpenAI'`

Expected: standard non-passthrough cases FAIL because only passthrough currently populates capture response fields.

- [ ] **Step 3: Implement shared OpenAI HTTP capture helper**

At each final HTTP request send point, snapshot the actual outbound body and sanitized headers only if the request scope matches the selected account platform. Wrap successful HTTP response bodies with a bounded tee and finalize it after the downstream handler drains the body. On terminal errors, reuse the already-read raw error bytes and submit only after failover logic declines another attempt. Keep all OpenAI WS/media branches outside the helper.

- [ ] **Step 4: Run OpenAI endpoint matrix and broader gateway tests**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'OpenAI|Capture|Gateway'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_http_capture.go backend/internal/service/openai_http_capture_test.go backend/internal/service/openai_gateway_forward.go backend/internal/service/openai_gateway_passthrough.go backend/internal/service/openai_gateway_messages.go backend/internal/service/openai_gateway_messages_chat_fallback.go backend/internal/service/openai_gateway_chat_completions.go backend/internal/service/openai_gateway_chat_completions_raw.go backend/internal/service/openai_gateway_responses_chat_fallback.go backend/internal/service/openai_gateway_upstream_errors.go backend/internal/handler/openai_gateway_handler.go backend/internal/handler/openai_gateway_handler_test.go
git commit -m "feat(capture): archive OpenAI HTTP text calls"
```

### Task 7: Independent Admin Capture API and Wiring

**Files:**
- Create: `backend/internal/service/capture_admin_service.go`
- Create: `backend/internal/service/capture_admin_service_test.go`
- Create: `backend/internal/handler/admin/capture_handler.go`
- Create: `backend/internal/handler/admin/capture_handler_test.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: `backend/internal/server/api_contract_test.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/cmd/server/wire.go`
- Regenerate: `backend/cmd/server/wire_gen.go`

**Interfaces:**
- Produces: `GET /api/v1/admin/capture-settings`, `PUT /api/v1/admin/capture-settings`, and `GET /api/v1/admin/capture-settings/history?range=24h|7d|30d`.
- GET returns policy, `provisioned`, `ready`, database/table/redacted addresses, health snapshot, and recent incidents; no username/password.
- PUT replaces the complete v1 policy and returns `409` when enabling without a ready writer.

- [ ] **Step 1: Write failing service, handler, and route tests**

```go
func TestCaptureSettingsCannotEnableWhenWriterIsNotReady(t *testing.T) {
    svc := newCaptureAdminServiceForTest(false, nil)
    policy := DefaultCaptureRuntimePolicy()
    policy.Enabled = true
    _, err := svc.Update(context.Background(), policy)
    require.ErrorIs(t, err, ErrCaptureInfrastructureNotReady)
}

func TestCaptureSettingsResponseDoesNotLeakCredentials(t *testing.T) {
    body := performCaptureSettingsGET(t, configWithCaptureSecret("super-secret"))
    require.NotContains(t, body, "super-secret")
    require.NotContains(t, body, `"password"`)
}
```

Add admin-auth route contract, invalid JSON/version/ID tests, disabled-policy save while unprovisioned, history range validation, and sorted history assertions.

- [ ] **Step 2: Run API tests and verify RED**

Run: `cd backend && go test ./internal/service ./internal/handler/admin ./internal/server -run 'CaptureSettings|capture-settings'`

Expected: FAIL because service, handler, and routes do not exist.

- [ ] **Step 3: Implement service/handler/routes and dependency injection**

Use response helpers consistently with existing admin handlers. Redact ClickHouse addresses to scheme/host/port only and omit query/userinfo. Register the repository, admin service, and handler in Wire, run `cd backend && go generate ./cmd/server`, and include the pool/reporter in existing cleanup through `ConversationCapturePool.Stop()`.

- [ ] **Step 4: Run API and generated-code checks**

Run: `cd backend && go test ./internal/service ./internal/handler/admin ./internal/server -run 'CaptureSettings|capture-settings' && make check-generate`

Expected: PASS and no Wire diff after regeneration.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_admin_service.go backend/internal/service/capture_admin_service_test.go backend/internal/handler/admin/capture_handler.go backend/internal/handler/admin/capture_handler_test.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/server/routes/admin.go backend/internal/server/api_contract_test.go backend/internal/service/wire.go backend/cmd/server/wire.go backend/cmd/server/wire_gen.go
git commit -m "feat(capture): add independent admin API"
```

### Task 8: Admin Page, Navigation, Alert Badge, and Localization

**Files:**
- Create: `frontend/src/api/admin/captureSettings.ts`
- Create: `frontend/src/api/__tests__/admin.captureSettings.spec.ts`
- Modify: `frontend/src/api/admin/index.ts`
- Create: `frontend/src/views/admin/CaptureSettingsView.vue`
- Create: `frontend/src/views/admin/__tests__/CaptureSettingsView.spec.ts`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- Create: `frontend/src/stores/captureHealth.ts`
- Modify: `frontend/src/stores/index.ts`
- Create: `frontend/src/i18n/locales/zh/admin/captureSettings.ts`
- Create: `frontend/src/i18n/locales/en/admin/captureSettings.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/index.ts`
- Modify: `frontend/src/i18n/locales/en/admin/index.ts`

**Interfaces:**
- Produces: typed `getCaptureSettings`, `updateCaptureSettings`, and `getCaptureHealthHistory(range)` API functions.
- Produces: admin-only `/admin/capture-settings` route and page.
- Produces: a lightweight admin store polling the GET endpoint while an admin shell is active, with `hasUnacknowledgedLoss` persisted per process start in local storage; opening the page acknowledges the badge without changing server history.

- [ ] **Step 1: Write failing API, page, route, and sidebar tests**

The page test must assert real rendered behavior: default OpenAI switch is off, unavailable infrastructure disables only enabling the master switch, save sends a complete normalized v1 policy, content warnings render, real-time counters render, range buttons reload history, and loss rows show timestamp/reason/count/bytes. Sidebar tests must mount the real component and assert `nav.captureSettings` is present in both admin modes immediately before `nav.settings`, absent for ordinary users, and displays an accessible warning dot when the store reports unacknowledged loss.

- [ ] **Step 2: Run frontend tests and verify RED**

Run: `pnpm --dir frontend exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts`

Expected: FAIL because the API, route, page, store, and nav item do not exist.

- [ ] **Step 3: Implement the page and navigation**

Reuse `GroupSelector.vue` and `views/admin/settings/OpenAIFastPolicyUserSelector.vue`. Render separate infrastructure, master, platform, outcome, content, scope, live-health, and loss-history cards. Poll health every 15 seconds while the page/sidebar admin shell is mounted; cancel timers on unmount and do not poll for non-admin users.

- [ ] **Step 4: Run focused tests, typecheck, and locale compilation**

Run: `pnpm --dir frontend exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/i18n/__tests__/localesMessageCompile.spec.ts && pnpm --dir frontend run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/admin/captureSettings.ts frontend/src/api/__tests__/admin.captureSettings.spec.ts frontend/src/api/admin/index.ts frontend/src/views/admin/CaptureSettingsView.vue frontend/src/views/admin/__tests__/CaptureSettingsView.spec.ts frontend/src/router/index.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/stores/captureHealth.ts frontend/src/stores/index.ts frontend/src/i18n/locales/zh/admin/captureSettings.ts frontend/src/i18n/locales/en/admin/captureSettings.ts frontend/src/i18n/locales/zh/admin/index.ts frontend/src/i18n/locales/en/admin/index.ts
git commit -m "feat(capture): add admin transfer settings page"
```

### Task 9: Deployment Documentation and Full Verification

**Files:**
- Modify: `docs/clickhouse-archive-deployment.md`
- Modify: `config.example.yaml` or the repository's canonical sample config containing `gateway.capture`
- Modify: `docs/superpowers/specs/2026-08-11-capture-settings-openai-http-design.md` only if implementation-required clarifications were discovered, without weakening confirmed behavior.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: operator documentation explaining no per-second empty ClickHouse writes, one record per completed eligible model call, two bounded queues, whole-pipeline byte budget, loss reasons, admin visibility, 30-day history, and recommended initial values for the observed 2 GiB production application host.

- [ ] **Step 1: Update sample configuration and deployment guide**

Document this conservative starting profile without modifying production:

```yaml
gateway:
  capture:
    enabled: false
    max_body_bytes: 8388608
    max_queue_bytes: 134217728
    queue_size: 512
    worker_count: 2
    writer_queue_size: 1024
    overflow_policy: drop
    overflow_sample_percent: 0
    batch_max_size: 100
    batch_max_interval_ms: 2000
```

State that runtime master/platform/content switches are configured only from the new admin page after provisioning is ready, and OpenAI remains off until explicitly selected.

- [ ] **Step 2: Run formatting and targeted suites**

Run:

```bash
cd backend && gofmt -w internal/config/config.go internal/service/capture_*.go internal/service/conversation_capture_pool.go internal/service/clickhouse_archive_writer.go internal/handler/admin/capture_handler.go internal/repository/capture_health_repo.go
cd backend && go test -race ./internal/service ./internal/handler ./internal/handler/admin ./internal/repository ./internal/server
pnpm --dir frontend exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/i18n/__tests__/localesMessageCompile.spec.ts
pnpm --dir frontend run lint:check
pnpm --dir frontend run typecheck
```

Expected: all commands PASS with no race reports, lint errors, or type errors.

- [ ] **Step 3: Run repository-wide verification**

Run:

```bash
make check-generate
make test-backend
make test-frontend
make build
git diff --check
git status --short
```

Expected: all test/build commands PASS; `git diff --check` is clean; `git status` contains only this feature plus the user's pre-existing unrelated untracked documents.

- [ ] **Step 4: Perform mutation-oriented acceptance checks**

Temporarily reason through or locally mutate each critical branch and confirm a test would fail: OpenAI default changed to true, user/group filter changed from AND to OR, writer `Send` failure counted as success, byte reservation released at writer enqueue, terminal error captured before failover completes, authorization header persisted, or nav item hidden in simple mode. Add or strengthen a test if any mutation is not caught.

- [ ] **Step 5: Commit documentation and final verification adjustments**

```bash
git add docs/clickhouse-archive-deployment.md config.example.yaml docs/superpowers/specs/2026-08-11-capture-settings-openai-http-design.md
git commit -m "docs(capture): document runtime controls and loss visibility"
```

If the sample config has a different canonical path, stage that exact file instead of `config.example.yaml`.

