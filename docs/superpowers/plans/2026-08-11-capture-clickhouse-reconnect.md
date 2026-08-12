# Capture ClickHouse Initialization Reconnect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make a statically provisioned capture pipeline recover automatically when ClickHouse is unavailable during application startup, without restarting the application or blocking request forwarding.

**Architecture:** Keep one stable `ArchiveWriter` manager inside `ConversationCapturePool`. It performs the existing synchronous first initialization attempt, delegates writes to an unavailable writer while retrying, and atomically switches once to a real ClickHouse writer after a successful retry. Retry waits are exponential, capped, jittered, cancellable, and test-injected. Existing runtime connection recovery remains clickhouse-go's responsibility; records lost before initialization succeeds are not replayed.

**Tech Stack:** Go 1.24, clickhouse-go/v2, `sync/atomic`, Pond worker pool, Testify, Go race detector.

## Global Constraints

- Preserve the hot-path contract: capture submission and writer delegation remain non-blocking.
- Preserve the initial synchronous connection attempt so a healthy deployment is ready immediately.
- Retry only startup initialization failures. Do not add a disk spool, replay queue, or retry failed `INSERT` batches.
- Use approximately 2 seconds initial delay, exponential backoff capped at 60 seconds, and bounded jitter in production.
- `Ready()` and `InitializationError()` must be concurrency-safe and change immediately after recovery.
- `Stop()` must cancel a pending wait, wait for an in-progress factory call, and stop every created real writer exactly once.
- Close a ClickHouse connection when `Ping` or `CREATE TABLE` initialization fails; repeated retries must not leak connections.
- Admin responses continue exposing only the generic initialization error. Detailed addresses, credentials, and driver errors remain server-log-only.
- Do not change production configuration or production hosts as part of this implementation.
- Execute this plan before `2026-08-11-capture-ops-alerts.md`; the `capture_ready` Ops metric relies on dynamic readiness.

---

### Task 1: Add a deterministic, concurrency-safe writer manager

**Files:**

- Create: `backend/internal/service/capture_archive_writer_manager.go`
- Create: `backend/internal/service/capture_archive_writer_manager_test.go`
- Reference: `backend/internal/service/clickhouse_archive_writer.go`

**Step 1: Write the failing lifecycle tests**

Add test doubles and deterministic retry controls to the new test file. Use the production `ArchiveWriter` interface and these test shapes:

```go
type archiveWriterFactory func(
    context.Context,
    config.GatewayCaptureConfig,
    *captureHealthTracker,
) (ArchiveWriter, error)

type captureRetryWait func(context.Context, time.Duration) bool

type captureWriterRetryOptions struct {
    InitialDelay time.Duration
    MaxDelay     time.Duration
    Jitter       func(time.Duration) time.Duration
    Wait         captureRetryWait
    Factory      archiveWriterFactory
}
```

Cover all of these behaviors:

```go
func TestCaptureArchiveWriterManagerRecoversAfterInitializationFailures(t *testing.T)
func TestCaptureArchiveWriterManagerBackoffDoublesAndCaps(t *testing.T)
func TestCaptureArchiveWriterManagerStopCancelsPendingRetry(t *testing.T)
func TestCaptureArchiveWriterManagerStopWaitsForInFlightFactory(t *testing.T)
func TestCaptureArchiveWriterManagerStopsRecoveredWriterExactlyOnce(t *testing.T)
```

In the recovery test, make factory calls 1 and 2 return sentinel errors and call 3 return a counting writer. Assert:

```go
require.False(t, manager.Ready())
require.ErrorIs(t, manager.Write(context.Background(), item), errArchiveWriterUnavailable)
require.NotEmpty(t, manager.InitializationError())

// Release two injected waits, then wait for the successful third factory call.
require.Eventually(t, manager.Ready, time.Second, time.Millisecond)
require.Empty(t, manager.InitializationError())
require.NoError(t, manager.Write(context.Background(), itemAfterRecovery))
require.Equal(t, int32(1), recoveredWriter.writeCount.Load())
```

In the backoff test, inject identity jitter and record wait arguments. Assert the exact sequence `2s, 4s, 8s, 10s, 10s` for `InitialDelay: 2s` and `MaxDelay: 10s`.

In the in-flight stop test, have the factory block on a channel, call `Stop` in another goroutine, assert it has not returned, then release a newly created writer. The retry loop must see cancellation, stop that writer once without publishing it, and allow `Stop` to return.

**Step 2: Run the tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestCaptureArchiveWriterManager' -count=1
```

Expected: compilation fails because `captureArchiveWriterManager` and its constructor do not exist.

**Step 3: Implement the stable manager**

Create a stable delegating manager with atomically published writer/status and one retry goroutine:

```go
type archiveWriterState struct {
    writer    ArchiveWriter
    ready     bool
    initError string
}

type captureArchiveWriterManager struct {
    cfg     config.GatewayCaptureConfig
    tracker *captureHealthTracker
    opts    captureWriterRetryOptions

    state atomic.Pointer[archiveWriterState]

    ctx      context.Context
    cancel   context.CancelFunc
    retryWG  sync.WaitGroup
    stopOnce sync.Once
}
```

Implement:

```go
func newCaptureArchiveWriterManager(
    cfg config.GatewayCaptureConfig,
    tracker *captureHealthTracker,
    opts captureWriterRetryOptions,
) *captureArchiveWriterManager

func (m *captureArchiveWriterManager) Write(context.Context, *archiveWriteItem) error
func (m *captureArchiveWriterManager) Ready() bool
func (m *captureArchiveWriterManager) InitializationError() string
func (m *captureArchiveWriterManager) Stop()
```

Constructor rules:

1. Normalize missing options to `2s`, `60s`, production jitter, a cancellable timer wait, and a small factory adapter that calls the existing `newClickHouseArchiveWriter(cfg, tracker)`. Task 2 replaces only this default adapter with the contextual constructor; injected test factories already receive the context.
2. Atomically store `{writer: unavailableArchiveWriter{}, ready: false}` before the first attempt.
3. Call the factory synchronously once.
4. On success, publish the writer, set ready, and return without a goroutine.
5. On error, store the detailed error only in the manager/log, log `capture.clickhouse_init_failed_degrade_noop`, and start one retry goroutine.

Retry-loop rules:

```go
delay := opts.InitialDelay
for opts.Wait(m.ctx, opts.Jitter(delay)) {
    writer, err := opts.Factory(m.ctx, m.cfg, m.tracker)
    if m.ctx.Err() != nil {
        if writer != nil { writer.Stop() }
        return
    }
    if err == nil {
        m.state.Store(&archiveWriterState{writer: writer, ready: true})
        logger.L().Info("capture.clickhouse_init_recovered")
        return
    }
    m.state.Store(&archiveWriterState{
        writer: unavailableArchiveWriter{}, ready: false, initError: err.Error(),
    })
    delay = min(delay*2, opts.MaxDelay)
    logger.L().Warn(
        "capture.clickhouse_init_retry_failed",
        zap.Error(err),
        zap.Duration("next_retry_delay", delay),
    )
}
```

The production jitter function must stay bounded, for example 80%-120% of the current delay, and clamp the final wait to the configured maximum. Avoid package-global mutable test hooks.

`Write`, `Ready`, and `InitializationError` each load one immutable state snapshot. `Stop()` must cancel first, wait for `retryWG`, then load and stop the published writer. `unavailableArchiveWriter.Stop()` is safe and empty.

**Step 4: Run focused tests and race tests to verify GREEN**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestCaptureArchiveWriterManager' -count=1
go test -race -tags=unit ./internal/service -run 'TestCaptureArchiveWriterManager' -count=1
```

Expected: all manager tests pass and the race detector reports no data race.

**Step 5: Commit the manager**

```bash
git add backend/internal/service/capture_archive_writer_manager.go backend/internal/service/capture_archive_writer_manager_test.go
git commit -m "feat(capture): retry ClickHouse initialization"
```

---

### Task 2: Make ClickHouse initialization cancellable and leak-free

**Files:**

- Modify: `backend/internal/service/clickhouse_archive_writer.go`
- Modify: `backend/internal/service/clickhouse_archive_writer_test.go`
- Modify: `backend/internal/service/capture_archive_writer_manager.go`

**Step 1: Write failing connection-cleanup tests**

Introduce a minimal internal connection interface and constructor option so tests do not mutate a package global:

```go
type archiveClickHouseConnection interface {
    Ping(context.Context) error
    Exec(context.Context, string, ...any) error
    PrepareBatch(context.Context, string, ...driver.PrepareBatchOption) (driver.Batch, error)
    Close() error
}

type clickHouseArchiveWriterInitOptions struct {
    Open func(*clickhouse.Options) (archiveClickHouseConnection, error)
}
```

Add fake connections that record `Close` calls, then write:

```go
func TestNewClickHouseArchiveWriterClosesConnectionAfterPingFailure(t *testing.T)
func TestNewClickHouseArchiveWriterClosesConnectionAfterCreateTableFailure(t *testing.T)
func TestNewClickHouseArchiveWriterContextHonorsCancellation(t *testing.T)
```

Assert `Close()` is called exactly once after each post-open failure. For cancellation, pass an already-cancelled parent context and assert the initialization fails without publishing a writer.

**Step 2: Run the tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestNewClickHouseArchiveWriter' -count=1
```

Expected: compilation fails because the contextual/options constructor and minimal interface are absent.

**Step 3: Refactor the writer constructor**

Keep the existing call shape as a compatibility wrapper:

```go
func newClickHouseArchiveWriter(
    cc config.GatewayCaptureConfig,
    trackers ...*captureHealthTracker,
) (ArchiveWriter, error) {
    return newClickHouseArchiveWriterContext(context.Background(), cc, trackers...)
}
```

Add:

```go
func newClickHouseArchiveWriterContext(
    parent context.Context,
    cc config.GatewayCaptureConfig,
    trackers ...*captureHealthTracker,
) (ArchiveWriter, error)

func newClickHouseArchiveWriterWithOptions(
    parent context.Context,
    cc config.GatewayCaptureConfig,
    tracker *captureHealthTracker,
    opts clickHouseArchiveWriterInitOptions,
) (_ ArchiveWriter, err error)
```

Use `parent` as the parent for the existing dial/read timeout contexts. Immediately after `Open`, protect all failure exits:

```go
initialized := false
defer func() {
    if !initialized {
        _ = conn.Close()
    }
}()
```

Set `initialized = true` only after `Ping`, `CREATE TABLE`, writer allocation, and batcher startup all succeed. Change `clickHouseArchiveWriter.conn` to the minimal interface; its batch code still uses the same driver `Batch` contract.

Finally, change the manager's production factory adapter to call `newClickHouseArchiveWriterContext(ctx, cfg, tracker)`. Keep the explicit adapter because the variadic constructor is not directly assignable to `archiveWriterFactory`.

**Step 4: Verify focused and existing writer tests**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test(NewClickHouseArchiveWriter|ArchiveWriter|CreateTableDDL)' -count=1
go test -race -tags=unit ./internal/service -run 'Test(NewClickHouseArchiveWriter|ArchiveWriter)' -count=1
```

Expected: cleanup/cancellation tests and existing batch completion/drop tests pass.

**Step 5: Commit the constructor hardening**

```bash
git add backend/internal/service/clickhouse_archive_writer.go backend/internal/service/clickhouse_archive_writer_test.go backend/internal/service/capture_archive_writer_manager.go
git commit -m "fix(capture): close failed ClickHouse initializations"
```

---

### Task 3: Wire dynamic readiness into the capture pool and admin view

**Files:**

- Modify: `backend/internal/service/conversation_capture_pool.go`
- Modify: `backend/internal/service/conversation_capture_pool_test.go`
- Modify: `backend/internal/service/capture_admin_service_test.go`

**Step 1: Write failing pool/admin transition tests**

Add a narrow status contract:

```go
type archiveWriterStatus interface {
    Ready() bool
    InitializationError() string
}
```

Write tests proving that the pool does not cache readiness:

```go
func TestCapturePoolReadinessDelegatesToWriterStatus(t *testing.T)
func TestCaptureSettingsViewReflectsWriterRecovery(t *testing.T)
```

Use a test writer/status whose state can transition under a mutex or atomics. Assert the admin view changes from not-ready with the generic ClickHouse error to ready with an empty initialization error, without constructing a new service or pool.

**Step 2: Run the tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestCapture(PoolReadiness|SettingsViewReflects)' -count=1
```

Expected: the tests fail because `ConversationCapturePool` stores immutable `ready` and `initError` fields.

**Step 3: Replace immutable state with the manager/status delegate**

Change `ConversationCapturePool` to hold:

```go
writer       ArchiveWriter
writerStatus archiveWriterStatus
```

Remove `ready bool` and `initError string`. Update the test constructor so status can be injected explicitly; make the normal `newConversationCapturePool` use an always-ready status helper for existing unit tests.

Update production construction:

```go
manager := newCaptureArchiveWriterManager(cc, tracker, captureWriterRetryOptions{})
return newConversationCapturePoolWithStatus(opts, manager, manager, tracker, reporter)
```

Update status methods:

```go
func (p *ConversationCapturePool) Ready() bool {
    return p != nil && p.writerStatus != nil && p.writerStatus.Ready()
}

func (p *ConversationCapturePool) InitializationError() string {
    if p == nil || p.writerStatus == nil { return "" }
    return sanitizeCaptureHealthError(errors.New(p.writerStatus.InitializationError()))
}
```

Do not change `CaptureAdminService`'s generic public error text. `ConversationCapturePool.Stop()` continues stopping the stable manager through `writer.Stop()`.

**Step 4: Verify the pool/admin suites under race detection**

Run:

```bash
cd backend
go test -race -tags=unit ./internal/service -run 'TestCapture(Pool|Settings)' -count=1
```

Expected: all capture pool/admin tests pass with no race.

**Step 5: Commit dynamic status wiring**

```bash
git add backend/internal/service/conversation_capture_pool.go backend/internal/service/conversation_capture_pool_test.go backend/internal/service/capture_admin_service_test.go
git commit -m "feat(capture): expose recovered ClickHouse readiness"
```

---

### Task 4: Update operational documentation and run the full verification gate

**Files:**

- Modify: `docs/clickhouse-archive-deployment.md`

**Step 1: Update the runbook**

Change the current/pending capability table so startup automatic reconnect is marked implemented. Document these exact runtime semantics:

- First connection attempt remains synchronous.
- Failed initialization retries in the background with exponential capped jitter.
- The admin Capture Settings page changes to ready after recovery without application restart.
- Records arriving before readiness are dropped and visible as `writer_unavailable`; they are not replayed.
- Established-connection recovery remains handled by clickhouse-go, while failed batches remain visible losses.

Keep Capture Ops rules/recovery emails marked pending until the second plan is implemented.

**Step 2: Run formatting and focused verification**

Run:

```bash
cd backend
gofmt -w internal/service/capture_archive_writer_manager.go internal/service/capture_archive_writer_manager_test.go internal/service/clickhouse_archive_writer.go internal/service/clickhouse_archive_writer_test.go internal/service/conversation_capture_pool.go internal/service/conversation_capture_pool_test.go internal/service/capture_admin_service_test.go
go test -race -tags=unit ./internal/service -run 'Test(Capture|ArchiveWriter|NewClickHouseArchiveWriter|CreateTableDDL)' -count=1
go test -tags=unit ./internal/service -count=1
go vet ./internal/service/...
```

Expected: formatting produces no unexpected diff, all service unit tests pass, race detection is clean, and vet succeeds.

**Step 3: Run repository-level checks**

Run:

```bash
cd backend
go test ./...
go test -race -tags=unit ./internal/service/... -count=1
git diff --check
```

Expected: all commands exit 0.

**Step 4: Review security and lifecycle invariants**

Inspect the final diff and confirm:

- No password, DSN, or raw driver error is returned by the admin API.
- No retry operation runs after `Stop()` returns.
- No real writer can escape without exactly one `Stop()`.
- Only the stable manager is visible to capture workers.
- No request thread waits for ClickHouse or retry timers.
- No spool/replay behavior was introduced.

**Step 5: Commit the runbook update**

```bash
git add docs/clickhouse-archive-deployment.md
git commit -m "docs(capture): document startup reconnect behavior"
```
