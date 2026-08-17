# Capture Ops Alerts and Recovery Email Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make capture readiness and data loss first-class Ops metrics, seed useful default rules, create normal Ops alert events, and email both firing and recovery notifications through the existing notification system.

**Architecture:** Extend the current Ops evaluator rather than adding a separate monitor. `capture_ready` reads the dynamically recovering local capture pool; loss metrics aggregate durable minute buckets from `CaptureHealthRepository` across instances. Existing rules, leader election, cooldowns, silencing, event storage, recipients, severity filtering, and rate limiting remain authoritative. Recovery uses a distinct notification event so it has its own subject/template and deduplication identity.

**Tech Stack:** Go 1.24, PostgreSQL embedded migrations, existing Ops evaluator/repositories, existing notification email service, Vue 3, TypeScript, Vitest, Vue Test Utils.

## Global Constraints

- Implement `2026-08-11-capture-clickhouse-reconnect.md` first so `capture_ready` is dynamic.
- Do not create another scheduler, alert table, recipient list, or SMTP configuration.
- `capture_ready` is evaluated only when capture is statically provisioned; a nil pool returns “metric unavailable,” not zero.
- In the current single-app-instance production layout, `capture_ready` represents the evaluator instance. Durable loss metrics aggregate all instance rows returned by PostgreSQL.
- `capture_dropped_records` sums every drop reason. `capture_writer_failures` sums only `writer_unavailable`, `clickhouse_prepare_failed`, `clickhouse_append_failed`, and `clickhouse_send_failed`. Their overlap is intentional: the P1 rule reports any loss, while the P0 rule identifies storage-path failures.
- Minute buckets use the evaluator's existing `[windowStart, safeEnd)` interval. Do not read an incomplete current-minute bucket.
- Repository errors make the metric unavailable and must not fabricate a zero/recovery.
- Recovery email is sent only after the firing event is successfully persisted as resolved.
- Recovery email obeys the same rule `notify_email`, global alert enablement, recipients, minimum severity, silencing, and hourly rate limit as firing email.
- Keep `ops_alert_events.email_sent` as the firing-email flag. Recovery deduplication uses notification delivery identity; do not overload that column.
- Do not change production configuration or production hosts in this implementation.

---

### Task 1: Add backend metric validation and deterministic aggregation

**Files:**

- Modify: `backend/internal/handler/admin/ops_alerts_handler.go`
- Modify: `backend/internal/handler/admin/admin_helpers_test.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service_test.go`

**Step 1: Write failing handler validation tests**

Extend `TestOpsAlertRuleValidation` with one successful payload per new metric:

```go
for _, metricType := range []string{
    "capture_ready",
    "capture_dropped_records",
    "capture_writer_failures",
} {
    raw["metric_type"] = json.RawMessage(strconv.Quote(metricType))
    _, err := validateOpsAlertRulePayload(raw)
    require.NoError(t, err, metricType)
    require.False(t, isPercentOrRateMetric(metricType))
}
```

Also assert a negative threshold is rejected because these are non-negative count/gauge metrics.

**Step 2: Run handler tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/handler/admin -run TestOpsAlertRuleValidation -count=1
```

Expected: validation rejects the three unknown metric types.

**Step 3: Register the backend metric types**

Append to `validOpsAlertMetricTypes`:

```go
"capture_ready",
"capture_dropped_records",
"capture_writer_failures",
```

Do not add them to `isPercentOrRateMetric`.

**Step 4: Write failing evaluator aggregation tests**

Add a dedicated `opsCaptureHealthRepoStub` in `ops_alert_evaluator_service_test.go`. It implements all three `CaptureHealthRepository` methods, records `start/end` in `ListEvents`, and returns configured events or an error. Add:

```go
func TestComputeRuleMetricCaptureReady(t *testing.T)
func TestComputeRuleMetricCaptureDroppedRecords(t *testing.T)
func TestComputeRuleMetricCaptureWriterFailures(t *testing.T)
func TestComputeRuleMetricCaptureRepositoryErrorIsUnavailable(t *testing.T)
```

Required assertions:

- Nil pool: `(0, false)` for `capture_ready`.
- Provisioned not-ready pool: `(0, true)`.
- Recovered pool: `(1, true)` without recreating evaluator.
- Dropped records sum all returned instances/reasons, e.g. `2 + 3 + 5 = 10`.
- Writer failures sum only the four storage-path reasons and ignore `worker_queue_full`, `writer_queue_full`, and `byte_budget_exceeded`.
- Repository error: `(0, false)`.
- Repository receives exactly the evaluator's `start` and `end` arguments.

**Step 5: Run evaluator tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestComputeRuleMetricCapture' -count=1
```

Expected: the new metric cases return unavailable because they are not implemented.

**Step 6: Inject capture dependencies and implement aggregation**

Add these fields to `OpsAlertEvaluatorService`; keep the constructor signature unchanged until Task 2 so every commit compiles independently:

```go
type OpsAlertEvaluatorService struct {
    capturePool       *ConversationCapturePool
    captureHealthRepo CaptureHealthRepository
}
```

Add pure aggregation data and helper:

```go
var captureWriterFailureReasons = map[string]struct{}{
    string(CaptureDropWriterUnavailable):       {},
    string(CaptureDropClickHousePrepareFailed): {},
    string(CaptureDropClickHouseAppendFailed):  {},
    string(CaptureDropClickHouseSendFailed):    {},
}

func sumCaptureDroppedRecords(events []CaptureHealthEvent, reasons map[string]struct{}) int64
```

Add early `computeRuleMetric` cases before the dashboard overview query:

```go
case "capture_ready":
    if s == nil || s.capturePool == nil { return 0, false }
    if s.capturePool.Ready() { return 1, true }
    return 0, true
case "capture_dropped_records", "capture_writer_failures":
    if s == nil || s.captureHealthRepo == nil { return 0, false }
    events, err := s.captureHealthRepo.ListEvents(ctx, start, end)
    if err != nil { return 0, false }
    // A nil reason filter means every reason.
    return float64(sumCaptureDroppedRecords(events, filter)), true
```

**Step 7: Run focused backend tests to verify GREEN**

Run:

```bash
cd backend
go test -tags=unit ./internal/handler/admin -run TestOpsAlertRuleValidation -count=1
go test -tags=unit ./internal/service -run 'TestComputeRuleMetric(Capture|NewIndicators|Account)' -count=1
```

Expected: all validation and aggregation tests pass.

**Step 8: Commit metric support**

```bash
git add backend/internal/handler/admin/ops_alerts_handler.go backend/internal/handler/admin/admin_helpers_test.go backend/internal/service/ops_alert_evaluator_service.go backend/internal/service/ops_alert_evaluator_service_test.go
git commit -m "feat(ops): evaluate capture health metrics"
```

---

### Task 2: Wire capture dependencies through providers and generated DI

**Files:**

- Modify: `backend/internal/service/wire.go`
- Regenerate: `backend/cmd/server/wire_gen.go`

**Step 1: Update the evaluator provider**

First append `capturePool *ConversationCapturePool` and `captureHealthRepo CaptureHealthRepository` to `NewOpsAlertEvaluatorService`, assign both fields in its return value, and update the provider signature and constructor call:

```go
func ProvideOpsAlertEvaluatorService(
    opsService *OpsService,
    opsRepo OpsRepository,
    emailService *EmailService,
    redisClient *redis.Client,
    cfg *config.Config,
    proxyRepo ProxyRepository,
    capturePool *ConversationCapturePool,
    captureHealthRepo CaptureHealthRepository,
) *OpsAlertEvaluatorService {
    svc := NewOpsAlertEvaluatorService(
        opsService, opsRepo, emailService, redisClient, cfg, proxyRepo,
        capturePool, captureHealthRepo,
    )
    svc.Start()
    return svc
}
```

**Step 2: Regenerate Wire output**

Run:

```bash
cd backend
go generate ./cmd/server
```

Expected: `cmd/server/wire_gen.go` passes the already-created `conversationCapturePool` and `captureHealthRepository` to `ProvideOpsAlertEvaluatorService`.

**Step 3: Verify generated-code consistency**

Run:

```bash
cd backend
go test ./cmd/server
make check-generate
```

Expected: both commands exit 0 and a second generation produces no diff.

**Step 4: Commit DI wiring**

```bash
git add backend/internal/service/wire.go backend/cmd/server/wire_gen.go
git commit -m "chore(ops): wire capture metrics into evaluator"
```

---

### Task 3: Seed three idempotent default capture rules

**Files:**

- Create: `backend/migrations/192_capture_ops_alert_rules.sql`
- Create: `backend/migrations/capture_ops_alert_rules_migration_test.go`

**Step 1: Write the failing embedded-migration test**

Create a test which reads `192_capture_ops_alert_rules.sql`, normalizes whitespace, and asserts all exact policy fragments:

```go
func TestCaptureOpsAlertRulesMigrationSeedsDefaults(t *testing.T) {
    content, err := FS.ReadFile("192_capture_ops_alert_rules.sql")
    require.NoError(t, err)
    sql := strings.Join(strings.Fields(string(content)), " ")

    for _, fragment := range []string{
        "true, 'capture_ready', '<', 1.0, 1, 2, 'P0', true, 60",
        "true, 'capture_dropped_records', '>', 0.0, 5, 1, 'P1', true, 60",
        "true, 'capture_writer_failures', '>', 0.0, 5, 1, 'P0', true, 60",
        "ON CONFLICT (name) DO NOTHING",
    } {
        require.Contains(t, sql, fragment)
    }
    require.Equal(t, 3, strings.Count(sql, "INSERT INTO ops_alert_rules"))
}
```

**Step 2: Run the migration test to verify RED**

Run:

```bash
cd backend
go test ./migrations -run TestCaptureOpsAlertRulesMigrationSeedsDefaults -count=1
```

Expected: test fails because embedded file `192_capture_ops_alert_rules.sql` does not exist.

**Step 3: Add the idempotent migration**

Create three inserts using this exact column list and `ON CONFLICT (name) DO NOTHING`:

```sql
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '转存基础设施未就绪',
    '静态转存已配置，但当前评估实例的 ClickHouse writer 未就绪；值为 0 持续 2 分钟时触发。',
    true, 'capture_ready', '<', 1.0, 1, 2, 'P0', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '转存发生数据丢失',
    '最近 5 分钟任意转存丢失记录数大于 0 时触发；丢失数据不会自动补写。',
    true, 'capture_dropped_records', '>', 0.0, 5, 1, 'P1', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    'ClickHouse 转存写入失败',
    '最近 5 分钟发生 writer 不可用或 ClickHouse prepare、append、send 失败时触发；丢失数据不会自动补写。',
    true, 'capture_writer_failures', '>', 0.0, 5, 1, 'P0', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;
```

Use these exact row values:

| Name | Metric | Operator | Threshold | Window | Sustained | Severity | Email | Cooldown |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 转存基础设施未就绪 | `capture_ready` | `<` | 1 | 1m | 2m | P0 | true | 60m |
| 转存发生数据丢失 | `capture_dropped_records` | `>` | 0 | 5m | 1m | P1 | true | 60m |
| ClickHouse 转存写入失败 | `capture_writer_failures` | `>` | 0 | 5m | 1m | P0 | true | 60m |

Descriptions must state the metric semantics and, for the two loss rules, that data is not replayed automatically.

**Step 4: Verify migration embedding and idempotence shape**

Run:

```bash
cd backend
go test ./migrations -run 'Test(CaptureOpsAlertRules|CaptureHealthEvents)' -count=1
```

Expected: both migration tests pass. No change to `migrations.go` is required because `//go:embed *.sql` already includes the new file.

**Step 5: Commit the migration**

```bash
git add backend/migrations/192_capture_ops_alert_rules.sql backend/migrations/capture_ops_alert_rules_migration_test.go
git commit -m "feat(ops): seed capture alert rules"
```

---

### Task 4: Send a distinct recovery email after persisted resolution

**Files:**

- Modify: `backend/internal/service/notification_email_service.go`
- Modify: `backend/internal/service/notification_email_service_test.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service_test.go`

**Step 1: Write failing notification-template tests**

Extend `TestNotificationEmailAdditionalEventsAreListedAndPreviewable` with:

```go
{NotificationEmailEventOpsAlertRecovered, "resolved_at"},
```

Add explicit preview assertions:

```go
func TestNotificationEmailOpsAlertRecoveryTemplates(t *testing.T) {
    // Preview en and zh with rule_name, severity, triggered_at, resolved_at.
    // Assert subjects contain "Recovered" / "恢复" and HTML contains resolved_at.
}
```

**Step 2: Run template tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestNotificationEmail(AdditionalEvents|OpsAlertRecovery)' -count=1
```

Expected: compilation fails because `NotificationEmailEventOpsAlertRecovered` does not exist.

**Step 3: Register the recovery notification event**

Add:

```go
const NotificationEmailEventOpsAlertRecovered = "ops.alert_recovered"
```

Add it immediately after `ops.alert` in `notificationEmailEventOrder`, `notificationEmailEventDefinitions`, and `notificationEmailOfficialTemplates`.

Use this definition metadata:

```go
NotificationEmailEventOpsAlertRecovered: {
    Event:       NotificationEmailEventOpsAlertRecovered,
    Label:       "Ops alert recovered",
    Description: "Sent to configured operations recipients after a firing ops alert is resolved.",
    Category:    "ops",
    Optional:    false,
    Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...),
        "rule_name", "severity", "alert_status", "metric_type", "operator",
        "metric_value", "threshold_value", "triggered_at", "resolved_at", "alert_description"),
},
```

Use the firing placeholders plus `resolved_at`:

```go
"rule_name", "severity", "alert_status", "metric_type", "operator",
"metric_value", "threshold_value", "triggered_at", "resolved_at",
"alert_description"
```

Official subjects:

```text
[Ops Recovered][{{severity}}] {{rule_name}}
[运维恢复][{{severity}}] {{rule_name}}
```

**Step 4: Write failing evaluator resolution/email tests**

Define `opsAlertEvaluatorRepoStub` in `ops_alert_evaluator_service_test.go`. Embed `*opsRepoMock`, then override `ListAlertRules`, `GetLatestSystemMetrics`, `GetActiveAlertEvent`, `UpdateAlertEventStatus`, and `UpsertJobHeartbeat` with explicit configured values/hooks. Add this package-private email-dispatch hook to `OpsAlertEvaluatorService` for evaluator orchestration tests:

```go
type opsAlertEmailKind string

const (
    opsAlertEmailFiring   opsAlertEmailKind = "firing"
    opsAlertEmailRecovery opsAlertEmailKind = "recovery"
)

sendAlertEmail func(
    context.Context,
    *OpsAlertRuntimeSettings,
    *OpsAlertRule,
    *OpsAlertEvent,
    opsAlertEmailKind,
) bool
```

Production falls back to the real method when the hook is nil. Test:

```go
func TestOpsAlertEvaluatorSendsRecoveryEmailAfterResolution(t *testing.T)
func TestOpsAlertEvaluatorDoesNotSendRecoveryEmailWhenResolutionFails(t *testing.T)
func TestShouldSkipOpsAlertEmailTreatsEmailSentAsFiringOnly(t *testing.T)
```

Use a `capture_ready < 1` rule, a ready capture pool, and an active firing event so the rule is definitively non-breached. Assert the hook observes a copy with `Status == resolved` and non-nil `ResolvedAt`, including when the active event has `EmailSent == true`. Also assert the successful recovery increments the heartbeat's `emails_sent`, while a failed `UpdateAlertEventStatus` invokes no email. The pure skip test must assert `EmailSent == true` skips firing but not recovery.

**Step 5: Run evaluator tests to verify RED**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestOpsAlertEvaluator(Sends|DoesNotSend)RecoveryEmail' -count=1
```

Expected: tests fail because the non-breached path only resolves the event.

**Step 6: Generalize email delivery for firing and recovery**

Replace the firing-only implementation with one method which selects event metadata by kind:

```go
func (s *OpsAlertEvaluatorService) maybeSendOpsAlertEmail(
    ctx context.Context,
    runtimeCfg *OpsAlertRuntimeSettings,
    rule *OpsAlertRule,
    event *OpsAlertEvent,
    kind opsAlertEmailKind,
) bool
```

Keep all existing eligibility gates except that `event.EmailSent` suppresses only another firing email; an event whose firing email was sent must still be eligible for one recovery email. Select:

```go
func shouldSkipOpsAlertEmail(kind opsAlertEmailKind, event *OpsAlertEvent) bool {
    return event == nil || (kind == opsAlertEmailFiring && event.EmailSent)
}
```

| Kind | Notification event | Source type | Fallback subject |
|---|---|---|---|
| firing | `ops.alert` | `ops_alert` | `[Ops Alert][severity] name` |
| recovery | `ops.alert_recovered` | `ops_alert_recovery` | `[Ops Recovered][severity] name` |

Use the alert event ID as `SourceID`. The distinct notification event/source type provides durable recovery deduplication without changing `email_sent`. Call `UpdateAlertEventEmailSent` only for firing delivery.

Extend `opsAlertEmailVariables` to populate `resolved_at` from `event.ResolvedAt`, defaulting to `-`.

After a successful resolution update:

```go
resolvedEvent := *activeEvent
resolvedEvent.Status = OpsAlertStatusResolved
resolvedEvent.ResolvedAt = &resolvedAt
eventsResolved++
if s.dispatchOpsAlertEmail(ctx, runtimeCfg, rule, &resolvedEvent, opsAlertEmailRecovery) {
    emailsSent++
}
```

The dispatch wrapper first uses the test hook when present, otherwise calls `maybeSendOpsAlertEmail`.

**Step 7: Verify template and evaluator behavior**

Run:

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test(NotificationEmail.*OpsAlert|OpsAlertEvaluator.*RecoveryEmail)' -count=1
```

Expected: both locale templates render, successful persisted resolution sends one recovery notification, failed resolution sends none, and firing behavior remains green.

**Step 8: Commit recovery notifications**

```bash
git add backend/internal/service/notification_email_service.go backend/internal/service/notification_email_service_test.go backend/internal/service/ops_alert_evaluator_service.go backend/internal/service/ops_alert_evaluator_service_test.go
git commit -m "feat(ops): notify when capture alerts recover"
```

---

### Task 5: Expose capture metrics in the Ops rule editor

**Files:**

- Modify: `frontend/src/api/admin/ops.ts`
- Modify: `frontend/src/views/admin/ops/components/OpsAlertRulesCard.vue`
- Create: `frontend/src/views/admin/ops/components/__tests__/OpsAlertRulesCard.spec.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/ops.ts`
- Modify: `frontend/src/i18n/locales/en/admin/ops.ts`

**Step 1: Write the failing component test**

Mock `opsAPI.listAlertRules`, `adminAPI.groups.getAll`, `useAppStore`, and `useI18n`. The translation mock must render the recommended hint as `operator ${params.operator}; threshold ${params.threshold}`. Stub `BaseDialog` so its default/footer slots render when `show` is true, and stub `Select` with an `options` prop.

Add:

```ts
it('offers capture metrics as a dedicated group with recommended thresholds', async () => {
  const wrapper = mount(OpsAlertRulesCard, { global: { stubs } })
  await flushPromises()
  await wrapper.get('button.btn-primary').trigger('click')

  const metricSelect = wrapper.findAllComponents(SelectStub)[0]
  const options = metricSelect.props('options') as SelectOption[]
  expect(options).toEqual(expect.arrayContaining([
    expect.objectContaining({ value: '__group__capture', disabled: true }),
    expect.objectContaining({ value: 'capture_ready' }),
    expect.objectContaining({ value: 'capture_dropped_records' }),
    expect.objectContaining({ value: 'capture_writer_failures' }),
  ]))

  await metricSelect.vm.$emit('update:modelValue', 'capture_ready')
  await flushPromises()
  expect(wrapper.text()).toContain('operator <')
  expect(wrapper.text()).toContain('threshold 1')
})
```

Also import the real English and Chinese locale roots and assert all six new label/description keys exist in both locales.

**Step 2: Run the component test to verify RED**

Run:

```bash
cd frontend
pnpm test:run src/views/admin/ops/components/__tests__/OpsAlertRulesCard.spec.ts
```

Expected: TypeScript/component assertions fail because the capture types/group/labels are absent.

**Step 3: Extend types and rule definitions**

Add to `MetricType`:

```ts
| 'capture_ready'
| 'capture_dropped_records'
| 'capture_writer_failures'
```

Change:

```ts
type MetricGroup = 'system' | 'capture' | 'group' | 'account'
```

Add definitions:

```ts
{
  type: 'capture_ready', group: 'capture',
  recommendedOperator: '<', recommendedThreshold: 1,
},
{
  type: 'capture_dropped_records', group: 'capture',
  recommendedOperator: '>', recommendedThreshold: 0,
},
{
  type: 'capture_writer_failures', group: 'capture',
  recommendedOperator: '>', recommendedThreshold: 0,
},
```

Build options in this order:

```ts
return [
  ...buildGroup('system'),
  ...buildGroup('capture'),
  ...buildGroup('group'),
  ...buildGroup('account'),
]
```

Do not add capture metrics to `groupMetricTypes`; they never require `group_id`.

**Step 4: Add Chinese and English copy**

Add `metricGroups.capture`, three labels, and three descriptions. The descriptions must explain:

- readiness is `1` ready / `0` not ready for the current evaluator instance;
- dropped records are summed from persisted loss buckets over the selected window;
- writer failures are the four ClickHouse/unavailable reasons, and lost records are not replayed.

**Step 5: Verify frontend behavior**

Run:

```bash
cd frontend
pnpm test:run src/views/admin/ops/components/__tests__/OpsAlertRulesCard.spec.ts
pnpm typecheck
pnpm lint:check
```

Expected: component test, Vue/TypeScript typecheck, and lint all pass.

**Step 6: Commit the rule editor**

```bash
git add frontend/src/api/admin/ops.ts frontend/src/views/admin/ops/components/OpsAlertRulesCard.vue frontend/src/views/admin/ops/components/__tests__/OpsAlertRulesCard.spec.ts frontend/src/i18n/locales/zh/admin/ops.ts frontend/src/i18n/locales/en/admin/ops.ts
git commit -m "feat(frontend): add capture Ops alert metrics"
```

---

### Task 6: Update the deployment runbook and execute the full verification gate

**Files:**

- Modify: `docs/clickhouse-archive-deployment.md`

**Step 1: Update current/pending capabilities**

Mark Capture Ops metrics/default rules/firing and recovery emails as implemented. Add operator instructions:

- Ops monitoring must be enabled for evaluator jobs to run.
- Configure alert recipients, minimum severity, and hourly rate limit in the existing Ops email settings.
- The migration seeds enabled rules but operators may tune/disable them in the Ops rule editor.
- `capture_ready` is local-instance readiness in the current singleton deployment.
- Loss metrics come from PostgreSQL `capture_health_events` and may lag until the previous minute bucket is flushed.
- P0/P1 rule overlap for storage failures is intentional.
- Recovery email is sent after the active event is persisted as resolved.

**Step 2: Format generated and edited backend code**

Run:

```bash
cd backend
gofmt -w internal/handler/admin/ops_alerts_handler.go internal/handler/admin/admin_helpers_test.go internal/service/ops_alert_evaluator_service.go internal/service/ops_alert_evaluator_service_test.go internal/service/notification_email_service.go internal/service/notification_email_service_test.go internal/service/wire.go migrations/capture_ops_alert_rules_migration_test.go
go generate ./cmd/server
```

Expected: no unexpected generated files beyond `cmd/server/wire_gen.go`.

**Step 3: Run the backend verification matrix**

Run:

```bash
cd backend
go test -tags=unit ./internal/handler/admin ./internal/service -count=1
go test ./migrations ./cmd/server -count=1
go test -race -tags=unit ./internal/service -run 'Test(ComputeRuleMetricCapture|OpsAlertEvaluator.*RecoveryEmail|NotificationEmail.*OpsAlert)' -count=1
make check-generate
go test ./...
```

Expected: every command exits 0 and the race detector is clean.

**Step 4: Run the frontend verification matrix**

Run:

```bash
cd frontend
pnpm test:run src/views/admin/ops/components/__tests__/OpsAlertRulesCard.spec.ts
pnpm test:run
pnpm typecheck
pnpm lint:check
```

Expected: all tests and static checks pass.

**Step 5: Review operational invariants**

Inspect the final diff and confirm:

- Nil/unprovisioned capture never fires `capture_ready`.
- Capture repository failure never resolves an active loss alert as zero.
- Loss aggregation counts `DroppedRecords`, not event rows or bytes.
- Writer-failure filtering exactly matches the four declared reasons.
- Recovery notification happens only after successful resolution persistence.
- Recovery uses a distinct notification event/source identity and does not mutate the firing `email_sent` flag.
- No capture body/header content enters Ops events or emails.
- Existing leader lock, silencing, cooldown, recipient, severity, and rate-limit behavior remains in effect.

**Step 6: Commit the final documentation update**

```bash
git add docs/clickhouse-archive-deployment.md
git commit -m "docs(capture): document Ops alerts and recovery"
```

**Step 7: Produce the implementation handoff**

Record:

- Commit list and final `git status --short`.
- Backend/frontend verification commands and exit results.
- Confirmation that no production host was changed.
- Deployment order: ship application migration/code first, configure existing Ops email settings, then verify seeded rules/events with a controlled ClickHouse interruption following the runbook.
