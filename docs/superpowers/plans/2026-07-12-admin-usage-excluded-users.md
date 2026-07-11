# Admin Usage Excluded Users Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add a searchable multi-user exclusion filter to the admin usage page and apply it consistently to details, statistics, charts, ranking, errors, and export.

**Architecture:** Keep exclude_user_ids as number[] in Vue state and encode it as a normalized comma-separated query value at API boundaries. Parse and normalize the value once in admin handlers, carry it through shared usage/error filter structs, and add a parameterized NULL-safe exclusion predicate to every raw usage/error aggregation. Cache keys include the normalized set so filtered results cannot collide.

**Tech Stack:** Vue 3, TypeScript, Vitest, Axios, Go, Gin, PostgreSQL, sqlmock/testify.

## Global Constraints

- Preserve the existing positive single-user filter.
- Exclusion applies to details, stats, all usage charts, user ranking, error records, and Excel export.
- Preserve records whose user_id is NULL.
- Accept at most 100 unique positive user IDs.
- Cleanup tasks must not display or inherit the exclusion filter.
- Reuse the existing admin user search endpoint and deleted-user badge behavior.
- Use parameterized SQL only.

---

## File Structure

New files:

- backend/internal/handler/admin/excluded_user_ids.go: shared query parser.
- backend/internal/handler/admin/excluded_user_ids_test.go: parser tests.
- backend/internal/repository/usage_log_repo_excluded_users_test.go: SQL predicate tests.
- frontend/src/utils/excludedUserIds.ts: query encoder.
- frontend/src/utils/__tests__/excludedUserIds.spec.ts: encoder tests.

Backend modifications cover the shared filter types, usage/dashboard/Ops handlers, dashboard and usage cache keys, dashboard service boundary, and usage/Ops repositories. Frontend modifications cover admin usage/dashboard/Ops APIs, UsageFilters, UsageView, UserTokenRanking, UsageCleanupDialog, English/Chinese translations, and adjacent tests.

---

### Task 1: Define and validate the exclusion contract

**Files:**

- Create: backend/internal/handler/admin/excluded_user_ids.go
- Create: backend/internal/handler/admin/excluded_user_ids_test.go
- Modify: backend/internal/pkg/usagestats/usage_log_types.go
- Modify: backend/internal/service/ops_models.go

**Interfaces:**

- Produces: usagestats.NormalizeExcludedUserIDs([]int64) []int64
- Produces: parseExcludedUserIDs(*gin.Context) ([]int64, error)
- Produces: UsageLogFilters.ExcludedUserIDs []int64
- Produces: UserBreakdownDimension.ExcludedUserIDs []int64
- Produces: OpsErrorLogFilter.ExcludedUserIDs []int64

- [ ] **Step 1: Write failing parser tests**

Add table tests for empty input, 3,1,3,2 normalization to 1,2,3, text, zero, negative IDs, empty segments, and 101 unique IDs:

~~~go
func TestParseExcludedUserIDs_Normalizes(t *testing.T) {
    c, _ := gin.CreateTestContext(httptest.NewRecorder())
    c.Request = httptest.NewRequest(http.MethodGet, "/?exclude_user_ids=3%2C1%2C3%2C2", nil)
    got, err := parseExcludedUserIDs(c)
    require.NoError(t, err)
    require.Equal(t, []int64{1, 2, 3}, got)
}
~~~

- [ ] **Step 2: Run RED**

Run:

~~~bash
cd backend && go test ./internal/handler/admin -run TestParseExcludedUserIDs -count=1
~~~

Expected: FAIL because parseExcludedUserIDs does not exist.

- [ ] **Step 3: Implement canonical normalization and parsing**

Add MaxExcludedUserIDs = 100. Normalize by dropping non-positive values, de-duplicating, and sorting ascending. The HTTP parser is stricter: every provided comma-separated segment must parse to a positive base-10 integer; malformed input or more than 100 normalized IDs returns an error containing Invalid exclude_user_ids. Blank/absent input returns nil.

~~~go
func NormalizeExcludedUserIDs(ids []int64) []int64 {
    seen := make(map[int64]struct{}, len(ids))
    out := make([]int64, 0, len(ids))
    for _, id := range ids {
        if id <= 0 {
            continue
        }
        if _, ok := seen[id]; ok {
            continue
        }
        seen[id] = struct{}{}
        out = append(out, id)
    }
    sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
    return out
}
~~~

- [ ] **Step 4: Run GREEN**

~~~bash
cd backend && go test ./internal/handler/admin -run TestParseExcludedUserIDs -count=1
~~~

Expected: PASS.

- [ ] **Step 5: Commit**

~~~bash
git add backend/internal/handler/admin/excluded_user_ids.go backend/internal/handler/admin/excluded_user_ids_test.go backend/internal/pkg/usagestats/usage_log_types.go backend/internal/service/ops_models.go
git commit -m "feat(usage): define excluded user filter contract"
~~~

---

### Task 2: Apply NULL-safe exclusion in repositories

**Files:**

- Create: backend/internal/repository/usage_log_repo_excluded_users_test.go
- Modify: backend/internal/repository/usage_log_repo_query.go
- Modify: backend/internal/repository/usage_log_repo_stats.go
- Modify: backend/internal/repository/usage_log_repo_trend.go
- Modify: backend/internal/repository/ops_repo.go
- Modify: backend/internal/repository/ops_error_where_test.go

**Interfaces:**

- Consumes the three ExcludedUserIDs fields from Task 1.
- Produces (user_id IS NULL OR NOT (user_id = ANY($n))) and aliased equivalents.

- [ ] **Step 1: Write failing repository tests**

Use sqlmock to exercise ListWithFilters, GetStatsWithFilters, GetUsageTrendWithUsageFilters, GetModelStatsWithUsageFiltersBySource, GetGroupStatsWithUsageFilters, and GetUserBreakdownStats. Match the NULL-safe predicate and assert the array argument represents 2 and 7. Extend the Ops where test:

~~~go
filter.ExcludedUserIDs = []int64{7, 2, 7}
where, args := buildOpsErrorLogsWhere(filter)
require.Contains(t, where, "(e.user_id IS NULL OR NOT (e.user_id = ANY($")
require.Contains(t, fmt.Sprint(args), "[2 7]")
~~~

Add a separate assertion that an empty exclusion set produces no exclusion predicate.

- [ ] **Step 2: Run RED**

~~~bash
cd backend && go test ./internal/repository -run 'ExcludedUser|BuildOpsErrorLogsWhere_UserScopedFilters' -count=1
~~~

Expected: FAIL because repository builders ignore ExcludedUserIDs.

- [ ] **Step 3: Add parameterized helpers**

~~~go
func appendExcludedUserIDsCondition(conditions []string, args []any, column string, ids []int64) ([]string, []any) {
    normalized := usagestats.NormalizeExcludedUserIDs(ids)
    if len(normalized) == 0 {
        return conditions, args
    }
    args = append(args, pq.Array(normalized))
    conditions = append(conditions, fmt.Sprintf("(%s IS NULL OR NOT (%s = ANY($%d)))", column, column, len(args)))
    return conditions, args
}
~~~

Add a query-string counterpart that appends AND plus this condition. Use user_id for plain usage_logs queries, ul.user_id for joined aggregations, and e.user_id for Ops errors.

- [ ] **Step 4: Apply the helper everywhere**

Apply it to usage detail list, stats summary, three endpoint aggregations, trend, model, group, user ranking, and Ops errors. Extend private trend/model/group/endpoint helpers with excludedUserIDs []int64. Legacy scalar wrappers pass nil; existing WithUsageFilters wrappers pass filters.ExcludedUserIDs. User ranking reads dim.ExcludedUserIDs.

Update shouldUsePreaggregatedTrend so a non-empty normalized exclusion set always forces the raw usage_logs query; pre-aggregated tables cannot subtract selected users.

- [ ] **Step 5: Run GREEN**

~~~bash
cd backend && go test ./internal/repository -run 'ExcludedUser|BuildOpsErrorLogsWhere_UserScopedFilters|UsageTrend|ModelStats|GroupStats|UserBreakdown' -count=1
~~~

Expected: PASS.

- [ ] **Step 6: Commit**

~~~bash
git add backend/internal/repository/usage_log_repo_excluded_users_test.go backend/internal/repository/usage_log_repo_query.go backend/internal/repository/usage_log_repo_stats.go backend/internal/repository/usage_log_repo_trend.go backend/internal/repository/ops_repo.go backend/internal/repository/ops_error_where_test.go
git commit -m "feat(usage): exclude users from usage aggregations"
~~~

---

### Task 3: Propagate exclusions through handlers, services, and caches

**Files:**

- Modify: backend/internal/service/account_usage_service.go
- Modify: backend/internal/service/dashboard_service.go
- Modify: backend/internal/handler/admin/usage_handler.go
- Modify: backend/internal/handler/admin/usage_query_cache.go
- Modify: backend/internal/handler/admin/usage_query_cache_test.go
- Modify: backend/internal/handler/admin/dashboard_handler.go
- Modify: backend/internal/handler/admin/dashboard_snapshot_v2_handler.go
- Modify: backend/internal/handler/admin/dashboard_query_cache.go
- Modify: backend/internal/handler/admin/dashboard_handler_user_breakdown_test.go
- Modify: backend/internal/handler/admin/ops_handler.go

**Interfaces:**

- Produces repository/service methods accepting UsageLogFilters for trend/model/group.
- Produces cache keys containing normalized excluded_user_ids.
- Produces HTTP 400 for malformed exclusions on each affected endpoint.

- [ ] **Step 1: Write failing handler and cache tests**

Add usage cache assertions:

~~~go
withExcluded := base
withExcluded.ExcludedUserIDs = []int64{9, 3}
sameSet := base
sameSet.ExcludedUserIDs = []int64{3, 9, 3}
require.NotEqual(t, usageStatsCacheKey(base), usageStatsCacheKey(withExcluded))
require.Equal(t, usageStatsCacheKey(withExcluded), usageStatsCacheKey(sameSet))
~~~

For user breakdown, request exclude_user_ids=9,3,9 and assert capturedDim.ExcludedUserIDs equals []int64{3,9}. Add malformed-query tests for usage list/stats, dashboard model/snapshot/ranking, and Ops errors expecting 400.

- [ ] **Step 2: Run RED**

~~~bash
cd backend && go test ./internal/handler/admin -run 'ExcludedUser|UsageStatsCacheKey|UserBreakdown' -count=1
~~~

Expected: FAIL because handlers and cache keys do not propagate exclusions.

- [ ] **Step 3: Add filter-struct service methods**

Extend UsageLogRepository with the concrete repository methods already present:

~~~go
GetUsageTrendWithUsageFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error)
GetModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters, source string) ([]usagestats.ModelStat, error)
GetGroupStatsWithUsageFilters(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters) ([]usagestats.GroupStat, error)
~~~

Add matching DashboardService methods. Keep existing scalar methods as compatibility wrappers that construct UsageLogFilters and delegate.

- [ ] **Step 4: Parse once and propagate**

Call parseExcludedUserIDs at each relevant handler entry and return response.BadRequest on error. Set ExcludedUserIDs on usage list/stats filters, dashboard trend/model/group/snapshot filters, UserBreakdownDimension, and OpsErrorLogFilter.

Refactor cached dashboard helpers to accept UsageLogFilters instead of another positional scalar. Add ExcludedUserIDs []int64 to usageStatsCacheKeyData, dashboardTrendCacheKey, dashboardModelGroupCacheKey, dashboardSnapshotV2Filters, and dashboardSnapshotV2CacheKey. Store only NormalizeExcludedUserIDs output.

- [ ] **Step 5: Run GREEN**

~~~bash
cd backend && go test ./internal/handler/admin ./internal/service -run 'ExcludedUser|UsageStatsCacheKey|UserBreakdown|Dashboard' -count=1
~~~

Expected: PASS.

- [ ] **Step 6: Commit**

~~~bash
git add backend/internal/service/account_usage_service.go backend/internal/service/dashboard_service.go backend/internal/handler/admin/usage_handler.go backend/internal/handler/admin/usage_query_cache.go backend/internal/handler/admin/usage_query_cache_test.go backend/internal/handler/admin/dashboard_handler.go backend/internal/handler/admin/dashboard_snapshot_v2_handler.go backend/internal/handler/admin/dashboard_query_cache.go backend/internal/handler/admin/dashboard_handler_user_breakdown_test.go backend/internal/handler/admin/ops_handler.go
git commit -m "feat(usage): propagate excluded users across admin queries"
~~~

---

### Task 4: Encode the frontend API contract

**Files:**

- Create: frontend/src/utils/excludedUserIds.ts
- Create: frontend/src/utils/__tests__/excludedUserIds.spec.ts
- Modify: frontend/src/api/admin/usage.ts
- Modify: frontend/src/api/admin/dashboard.ts
- Modify: frontend/src/api/admin/ops.ts

**Interfaces:**

- Produces normalizeExcludedUserIds(ids?: number[]): number[]
- Produces encodeExcludedUserIds(ids?: number[]): string | undefined
- Adds exclude_user_ids?: number[] to affected API parameter types.

- [ ] **Step 1: Write failing encoder tests**

~~~ts
expect(normalizeExcludedUserIds([9, 3, 9, 0, -1])).toEqual([3, 9])
expect(encodeExcludedUserIds([9, 3, 9])).toBe('3,9')
expect(encodeExcludedUserIds([])).toBeUndefined()
~~~

- [ ] **Step 2: Run RED**

~~~bash
cd frontend && npm run test:run -- src/utils/__tests__/excludedUserIds.spec.ts
~~~

Expected: FAIL because the utility does not exist.

- [ ] **Step 3: Implement encoding and API mapping**

~~~ts
export const normalizeExcludedUserIds = (ids?: number[]): number[] =>
  Array.from(new Set((ids ?? []).filter((id) => Number.isSafeInteger(id) && id > 0))).sort((a, b) => a - b)

export const encodeExcludedUserIds = (ids?: number[]): string | undefined => {
  const normalized = normalizeExcludedUserIds(ids)
  return normalized.length ? normalized.join(',') : undefined
}

export const withEncodedExcludedUserIds = <T extends { exclude_user_ids?: number[] }>(params: T) => ({
  ...params,
  exclude_user_ids: encodeExcludedUserIds(params.exclude_user_ids),
})
~~~

Use withEncodedExcludedUserIds without mutating inputs in usage list/getStats, dashboard trend/model/group/snapshot/ranking, and Ops listErrorLogs.

- [ ] **Step 4: Run GREEN and typecheck**

~~~bash
cd frontend && npm run test:run -- src/utils/__tests__/excludedUserIds.spec.ts && npm run typecheck
~~~

Expected: PASS and exit 0.

- [ ] **Step 5: Commit**

~~~bash
git add frontend/src/utils/excludedUserIds.ts frontend/src/utils/__tests__/excludedUserIds.spec.ts frontend/src/api/admin/usage.ts frontend/src/api/admin/dashboard.ts frontend/src/api/admin/ops.ts
git commit -m "feat(usage): encode excluded users in admin APIs"
~~~

---

### Task 5: Build the searchable multi-user exclusion control

**Files:**

- Modify: frontend/src/components/admin/usage/UsageFilters.vue
- Modify: frontend/src/components/admin/usage/__tests__/UsageFilters.spec.ts
- Modify: frontend/src/i18n/locales/en/admin/resources.ts
- Modify: frontend/src/i18n/locales/zh/admin/resources.ts

**Interfaces:**

- Reads/writes modelValue.exclude_user_ids?: number[].
- Adds showExcludedUsers?: boolean default true.
- Keeps SimpleUser labels locally while IDs remain the page contract.

- [ ] **Step 1: Write failing component tests**

Test searching, selecting two users, removable chips, duplicate prevention, one-chip removal, external reset, deleted-user sorting, positive/excluded conflict resolution, and the 100-user limit. Use data-testid values excluded-user-filter, excluded-user-option, and excluded-user-chip.

- [ ] **Step 2: Run RED**

~~~bash
cd frontend && npm run test:run -- src/components/admin/usage/__tests__/UsageFilters.spec.ts
~~~

Expected: FAIL because the control does not exist.

- [ ] **Step 3: Implement the UI**

Add state mirroring the existing user dropdown:

~~~ts
const excludedUserSearchRef = ref<HTMLElement | null>(null)
const excludedUserKeyword = ref('')
const excludedUserResults = ref<SimpleUser[]>([])
const selectedExcludedUsers = ref<SimpleUser[]>([])
const showExcludedUserDropdown = ref(false)
const MAX_EXCLUDED_USERS = 100
~~~

selectExcludedUser rejects the current positive user, duplicates, and selection 101; otherwise it assigns a new normalized ID array, clears search state, closes the dropdown, and emits change. removeExcludedUser removes the chip and ID then emits change. Selecting a positive user first removes that user from exclusions. Extend outside-click handling, unmount timer cleanup, and watchers for external reset/shrink.

Add English labels Exclude users, Search users to exclude..., Up to 100 users can be excluded. Add Chinese labels 排除用户, 搜索要排除的用户..., 最多可排除 100 个用户。 Keep the existing deleted badge.

- [ ] **Step 4: Run GREEN**

~~~bash
cd frontend && npm run test:run -- src/components/admin/usage/__tests__/UsageFilters.spec.ts
~~~

Expected: PASS.

- [ ] **Step 5: Commit**

~~~bash
git add frontend/src/components/admin/usage/UsageFilters.vue frontend/src/components/admin/usage/__tests__/UsageFilters.spec.ts frontend/src/i18n/locales/en/admin/resources.ts frontend/src/i18n/locales/zh/admin/resources.ts
git commit -m "feat(usage): add excluded-user multi-select"
~~~

---

### Task 6: Wire the full page and keep cleanup safe

**Files:**

- Modify: frontend/src/views/admin/UsageView.vue
- Modify: frontend/src/views/admin/__tests__/UsageView.spec.ts
- Modify: frontend/src/components/admin/usage/UserTokenRanking.vue
- Modify: frontend/src/components/admin/usage/UsageCleanupDialog.vue
- Modify/create: adjacent cleanup component test.

**Interfaces:**

- Sends identical exclusion arrays to list, stats, model, snapshot, ranking, errors, and export.
- Guarantees cleanup state and payload omit exclude_user_ids.

- [ ] **Step 1: Write failing propagation tests**

Set filters.exclude_user_ids to [8,3], call applyFilters, and assert list, getStats, getModelStats, getSnapshotV2, and listErrorLogs receive that array. Assert UserTokenRanking sends it to getUserBreakdown. Assert UsageCleanupDialog renders UsageFilters with showExcludedUsers=false and cleanup submission has no exclude_user_ids property.

- [ ] **Step 2: Run RED**

~~~bash
cd frontend && npm run test:run -- src/views/admin/__tests__/UsageView.spec.ts src/components/admin/usage/__tests__/UserTokenRanking.spec.ts
~~~

Expected: FAIL because explicit parameter builders omit exclusions and cleanup clones them.

- [ ] **Step 3: Propagate page state**

Add exclusions to breakdownFilters, model stats base params, snapshot params, and error params:

~~~ts
if (filters.value.exclude_user_ids?.length) {
  f.exclude_user_ids = [...filters.value.exclude_user_ids]
}
~~~

List, stats, and export already spread filters; preserve that single-source behavior. Initialize/reset with exclude_user_ids undefined. Ranking drill-down removes its selected positive user from the excluded set before applying filters.

- [ ] **Step 4: Strip cleanup state**

Pass showExcludedUsers=false to the cleanup dialog's nested UsageFilters and omit exclusions from its cloned state:

~~~ts
const { exclude_user_ids: _excludedUserIds, ...cleanupFilters } = props.filters
localFilters.value = { ...cleanupFilters }
~~~

Keep CreateUsageCleanupTaskRequest unchanged.

- [ ] **Step 5: Run GREEN and typecheck**

~~~bash
cd frontend && npm run test:run -- src/views/admin/__tests__/UsageView.spec.ts src/components/admin/usage/__tests__/UsageFilters.spec.ts src/components/admin/usage/__tests__/UserTokenRanking.spec.ts && npm run typecheck
~~~

Expected: PASS and exit 0.

- [ ] **Step 6: Commit**

~~~bash
git add frontend/src/views/admin/UsageView.vue frontend/src/views/admin/__tests__/UsageView.spec.ts frontend/src/components/admin/usage/UserTokenRanking.vue frontend/src/components/admin/usage/UsageCleanupDialog.vue frontend/src/components/admin/usage/__tests__
git commit -m "feat(usage): apply excluded users across admin usage page"
~~~

---

### Task 7: Full verification and graph refresh

**Files:**

- Update: graphify-out generated graph artifacts.

- [ ] **Step 1: Run backend verification**

~~~bash
cd backend && go test ./internal/handler/admin ./internal/repository ./internal/service -count=1
~~~

Expected: all packages PASS.

- [ ] **Step 2: Run frontend verification**

~~~bash
cd frontend && npm run test:run -- src/components/admin/usage/__tests__/UsageFilters.spec.ts src/components/admin/usage/__tests__/UserTokenRanking.spec.ts src/views/admin/__tests__/UsageView.spec.ts src/utils/__tests__/excludedUserIds.spec.ts
~~~

Expected: all files PASS.

- [ ] **Step 3: Run static/build checks**

~~~bash
cd frontend && npm run typecheck && npm run build
~~~

Expected: both commands exit 0.

- [ ] **Step 4: Format and inspect**

Run gofmt over every touched Go file, then run git diff --check. Expected: no diff-check output and exit 0.

- [ ] **Step 5: Refresh graph**

~~~bash
graphify update .
~~~

Expected: successful incremental update.

- [ ] **Step 6: Verify requirements from fresh evidence**

Confirm multi-select search/removal, all data-source request propagation, NULL-user retention, cache isolation, export reuse, and cleanup omission from test output and final diff.

- [ ] **Step 7: Commit graph artifacts only if tracked changes exist**

~~~bash
git add graphify-out
git diff --cached --quiet || git commit -m "chore: refresh graphify after usage filter changes"
~~~

---

## Plan Self-Review

- Spec coverage: UI, parsing, list, stats, endpoints, models, groups, trend, ranking, errors, export, cache isolation, NULL semantics, limit, conflict handling, and cleanup safety each map to a task.
- Placeholder scan: every implementation step names concrete behavior, files, commands, and expected results.
- Type consistency: frontend uses exclude_user_ids?: number[]; HTTP encoding is isolated at API boundaries; Go uses ExcludedUserIDs []int64 in all three filter structs.
- Scope: no persistent exclusion list and no exclusion support for API keys, accounts, or groups.

