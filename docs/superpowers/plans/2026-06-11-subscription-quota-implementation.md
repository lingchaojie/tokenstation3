# Subscription Quota Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement LINX2 Basic/Plus/Pro/Max monthly subscription plans with plan-level seven-day quota snapshots, subscription-first billing, recharge-balance fallback, and dual-balance frontend display.

**Architecture:** Keep `users.balance` as the recharge wallet and use `user_subscriptions.weekly_usage_usd` / `weekly_window_start` as the seven-day subscription ledger. Add plan-level quota to `subscription_plans`, snapshot the purchased plan and quota onto `user_subscriptions`, and decide the billing ledger at usage-record time from actual cost. Existing group daily/monthly limits remain for compatibility, while the new public plans use plan snapshot quota first and group weekly limit only as legacy fallback.

**Tech Stack:** Go 1.x, Gin, Ent ORM, PostgreSQL migrations, Vue 3, Pinia, TypeScript, TailwindCSS, vue-i18n, Vitest.

---

## File Structure

### Backend data model and migrations

- Modify: `backend/ent/schema/subscription_plan.go`
  - Add nullable `seven_day_quota_usd` to sale plan data.
- Modify: `backend/ent/schema/user_subscription.go`
  - Add nullable `plan_id`, `plan_name`, and `seven_day_limit_usd` quota snapshot fields.
- Create: `backend/migrations/150_subscription_plan_quotas.sql`
  - Add new columns, indexes, and seed/update the four LINX2 monthly plans on a shared subscription group.
- Regenerate: `backend/ent/*`
  - Run Ent code generation after schema edits.

### Backend payment and subscription services

- Modify: `backend/internal/service/payment_config_service.go`
  - Extend plan create/update request structs with `seven_day_quota_usd`.
- Modify: `backend/internal/service/payment_config_plans.go`
  - Persist the plan quota and return it in plan list results.
- Modify: `backend/internal/handler/payment_handler.go`
  - Include `seven_day_quota_usd` in `/payment/plans` and `/payment/checkout-info` responses.
- Modify: `backend/internal/handler/admin/payment_handler.go`
  - Accept `seven_day_quota_usd` in admin plan create/update JSON.
- Modify: `backend/internal/service/user_subscription.go`
  - Add snapshot fields and helper methods for seven-day limit/remaining/reset.
- Modify: `backend/internal/service/user_subscription_port.go`
  - Add snapshot fields and atomic usage methods to repository ports.
- Modify: `backend/internal/repository/user_subscription_repo.go`
  - Persist snapshot fields, map them from Ent models, switch plan snapshots, and atomically consume subscription quota.
- Modify: `backend/internal/service/subscription_service.go`
  - Make subscription assignment plan-aware, switch active subscriptions immediately, reset the seven-day window at activation/switch time, and preserve legacy group limit fallback.
- Modify: `backend/internal/service/payment_fulfillment.go`
  - Fulfill subscription orders by loading the paid plan and passing the quota snapshot into subscription assignment.
- Modify: `backend/internal/service/payment_order.go`
  - Ensure subscription orders still validate the plan and preserve `plan_id` for fulfillment.

### Backend billing path

- Modify: `backend/internal/server/middleware/api_key_auth.go`
  - Stop rejecting subscription requests solely because the seven-day quota is insufficient when recharge balance can cover fallback.
- Modify: `backend/internal/service/gateway_service.go`
  - Select subscription or balance ledger after actual cost is known.
- Modify: `backend/internal/service/openai_gateway_service.go`
  - Apply the same ledger selection for OpenAI-compatible requests.
- Modify: `backend/internal/service/usage_billing.go`
  - Add enough command/result state to represent attempted subscription billing with balance fallback.
- Modify: `backend/internal/repository/usage_billing_repo.go`
  - Atomically consume subscription quota when selected; fall back to recharge balance if the guarded subscription update cannot cover actual cost.

### Backend DTOs and user APIs

- Modify: `backend/internal/handler/dto/types.go`
  - Expose plan snapshot, seven-day limit, usage, remaining, and next reset fields on user subscription DTOs.
- Modify: `backend/internal/handler/dto/mappers.go`
  - Map service subscription snapshot and computed balance fields to DTOs.
- Modify: `backend/internal/handler/user_subscription_handler.go`
  - Keep existing endpoints but return the enriched DTOs.

### Frontend types, API, and UI

- Modify: `frontend/src/types/payment.ts`
  - Add `seven_day_quota_usd` to `SubscriptionPlan`.
- Modify: `frontend/src/types/index.ts`
  - Add subscription snapshot and seven-day balance fields to `UserSubscription` / `SubscriptionProgress` as needed.
- Modify: `frontend/src/views/HomeView.vue`
  - Replace model pricing table with static LINX2 Basic/Plus/Pro/Max subscription cards.
- Modify: `frontend/src/components/payment/SubscriptionPlanCard.vue`
  - Show plan-level seven-day quota and rephrased benefits.
- Modify: `frontend/src/views/user/PaymentView.vue`
  - Show four backend-provided plans, quota, and renewal/switch state.
- Modify: `frontend/src/stores/subscriptions.ts`
  - Keep active subscription cache and expose a computed aggregate subscription balance.
- Modify: `frontend/src/views/user/DashboardView.vue`
  - Load active subscriptions and pass subscription balance to dashboard stats.
- Modify: `frontend/src/components/user/dashboard/UserDashboardStats.vue`
  - Display subscription balance and recharge balance separately.
- Modify: `frontend/src/views/user/SubscriptionsView.vue`
  - Show active plan, seven-day usage/remaining, next reset time, expiry, and renewal action.
- Modify: `frontend/src/views/admin/orders/AdminPaymentPlansView.vue`
  - Add quota column in admin plan management.
- Modify: `frontend/src/views/admin/orders/PlanEditDialog.vue`
  - Add editable `seven_day_quota_usd` field.
- Modify: `frontend/src/i18n/locales/zh.ts`
  - Add Chinese copy for subscription quota cards and dual balances.
- Modify: `frontend/src/i18n/locales/en.ts`
  - Add English copy for the same UI.

---

## Implementation Tasks

### Task 1: Add database columns and default LINX2 plan seed

**Files:**
- Modify: `backend/ent/schema/subscription_plan.go:31-69`
- Modify: `backend/ent/schema/user_subscription.go:36-82`
- Create: `backend/migrations/150_subscription_plan_quotas.sql`
- Test: migration is validated by backend tests in later tasks and by manual `go test ./internal/service/... ./internal/repository/...`

- [ ] **Step 1: Write the migration**

Create `backend/migrations/150_subscription_plan_quotas.sql`:

```sql
-- Add plan-level seven-day quotas and subscription purchase snapshots.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS seven_day_quota_usd DECIMAL(20,8);

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_id BIGINT,
    ADD COLUMN IF NOT EXISTS plan_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS seven_day_limit_usd DECIMAL(20,8);

CREATE INDEX IF NOT EXISTS idx_subscription_plans_group_sort_sale
    ON subscription_plans(group_id, sort_order, id)
    WHERE for_sale = TRUE;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_id
    ON user_subscriptions(plan_id)
    WHERE plan_id IS NOT NULL;

WITH target_group AS (
    SELECT id
    FROM groups
    WHERE deleted_at IS NULL
      AND subscription_type = 'subscription'
    ORDER BY sort_order ASC, id ASC
    LIMIT 1
), upsert_plan AS (
    INSERT INTO subscription_plans (
        group_id,
        name,
        description,
        price,
        original_price,
        validity_days,
        validity_unit,
        features,
        product_name,
        for_sale,
        sort_order,
        seven_day_quota_usd,
        created_at,
        updated_at
    )
    SELECT
        target_group.id,
        plan_data.name,
        plan_data.description,
        plan_data.price,
        plan_data.original_price,
        30,
        'day',
        plan_data.features,
        plan_data.product_name,
        TRUE,
        plan_data.sort_order,
        plan_data.seven_day_quota_usd,
        NOW(),
        NOW()
    FROM target_group
    CROSS JOIN (VALUES
        ('Basic monthly', 'For focused personal trials and light development.', 179.00::DECIMAL, NULL::DECIMAL, 'Starter quota refreshed every seven days.
Good for validating LINX2 workflows.
Recharge balance remains available as backup.', 'LINX2 Basic monthly', 10, 50.00::DECIMAL),
        ('Plus monthly', 'For everyday individual development with steady usage.', 399.00::DECIMAL, NULL::DECIMAL, 'Larger seven-day quota for daily coding.
Designed for regular LINX2 sessions.
Recharge balance covers bursts beyond quota.', 'LINX2 Plus monthly', 20, 110.00::DECIMAL),
        ('Pro monthly', 'For primary development workflows and higher-frequency usage.', 799.00::DECIMAL, NULL::DECIMAL, 'High seven-day quota for main projects.
Better fit for intensive agent workflows.
Recharge fallback keeps requests moving.', 'LINX2 Pro monthly', 30, 260.00::DECIMAL),
        ('Max monthly', 'For heavy usage, parallel projects, and higher concurrency.', 1599.00::DECIMAL, NULL::DECIMAL, 'Largest seven-day quota for demanding work.
Built for parallel projects and power users.
Recharge balance handles overflow usage.', 'LINX2 Max monthly', 40, 550.00::DECIMAL)
    ) AS plan_data(name, description, price, original_price, features, product_name, sort_order, seven_day_quota_usd)
    ON CONFLICT DO NOTHING
    RETURNING id
)
UPDATE subscription_plans existing
SET
    description = plan_data.description,
    price = plan_data.price,
    original_price = plan_data.original_price,
    validity_days = 30,
    validity_unit = 'day',
    features = plan_data.features,
    product_name = plan_data.product_name,
    for_sale = TRUE,
    sort_order = plan_data.sort_order,
    seven_day_quota_usd = plan_data.seven_day_quota_usd,
    updated_at = NOW()
FROM target_group
CROSS JOIN (VALUES
    ('Basic monthly', 'For focused personal trials and light development.', 179.00::DECIMAL, NULL::DECIMAL, 'Starter quota refreshed every seven days.
Good for validating LINX2 workflows.
Recharge balance remains available as backup.', 'LINX2 Basic monthly', 10, 50.00::DECIMAL),
    ('Plus monthly', 'For everyday individual development with steady usage.', 399.00::DECIMAL, NULL::DECIMAL, 'Larger seven-day quota for daily coding.
Designed for regular LINX2 sessions.
Recharge balance covers bursts beyond quota.', 'LINX2 Plus monthly', 20, 110.00::DECIMAL),
    ('Pro monthly', 'For primary development workflows and higher-frequency usage.', 799.00::DECIMAL, NULL::DECIMAL, 'High seven-day quota for main projects.
Better fit for intensive agent workflows.
Recharge fallback keeps requests moving.', 'LINX2 Pro monthly', 30, 260.00::DECIMAL),
    ('Max monthly', 'For heavy usage, parallel projects, and higher concurrency.', 1599.00::DECIMAL, NULL::DECIMAL, 'Largest seven-day quota for demanding work.
Built for parallel projects and power users.
Recharge balance handles overflow usage.', 'LINX2 Max monthly', 40, 550.00::DECIMAL)
) AS plan_data(name, description, price, original_price, features, product_name, sort_order, seven_day_quota_usd)
WHERE existing.group_id = target_group.id
  AND existing.name = plan_data.name;
```

- [ ] **Step 2: Add plan quota to Ent plan schema**

In `backend/ent/schema/subscription_plan.go`, insert after `original_price`:

```go
field.Float("seven_day_quota_usd").
    SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
    Optional().
    Nillable(),
```

- [ ] **Step 3: Add subscription snapshot fields to Ent subscription schema**

In `backend/ent/schema/user_subscription.go`, insert after `group_id`:

```go
field.Int64("plan_id").
    Optional().
    Nillable(),
field.String("plan_name").
    MaxLen(100).
    Optional().
    Nillable(),
field.Float("seven_day_limit_usd").
    SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
    Optional().
    Nillable(),
```

Add a schema index after `index.Fields("group_id"),`:

```go
index.Fields("plan_id"),
```

- [ ] **Step 4: Regenerate Ent code**

Run:

```bash
cd backend && go generate ./ent
```

Expected: command exits 0 and generated files include setters/getters for `SevenDayQuotaUsd`, `PlanID`, `PlanName`, and `SevenDayLimitUsd`.

- [ ] **Step 5: Run backend compile check**

Run:

```bash
cd backend && go test ./ent/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/ent/schema/subscription_plan.go backend/ent/schema/user_subscription.go backend/ent backend/migrations/150_subscription_plan_quotas.sql
git commit -m "feat: add subscription quota schema"
```

---

### Task 2: Persist and expose plan-level seven-day quota

**Files:**
- Modify: `backend/internal/service/payment_config_service.go:152-178`
- Modify: `backend/internal/service/payment_config_plans.go`
- Modify: `backend/internal/handler/payment_handler.go:47-180`
- Modify: `backend/internal/handler/admin/payment_handler.go`
- Test: `backend/internal/service/payment_config_plans_test.go`
- Test: `backend/internal/handler/payment_handler_test.go`

- [ ] **Step 1: Write service tests for plan quota create/update/list**

Add tests to `backend/internal/service/payment_config_plans_test.go`:

```go
func TestPaymentConfigServicePlanSevenDayQuota(t *testing.T) {
    ctx := context.Background()
    svc, cleanup := newPaymentConfigServiceTestHarness(t)
    defer cleanup()

    group := createPaymentConfigTestGroup(t, ctx, svc.entClient)
    quota := 110.0

    created, err := svc.CreatePlan(ctx, CreatePlanRequest{
        GroupID: group.ID,
        Name: "Plus monthly",
        Description: "Everyday development",
        Price: 399,
        ValidityDays: 30,
        ValidityUnit: "day",
        Features: "Seven-day quota\nRecharge fallback",
        ProductName: "LINX2 Plus monthly",
        ForSale: true,
        SortOrder: 20,
        SevenDayQuotaUSD: &quota,
    })
    require.NoError(t, err)
    require.NotNil(t, created.SevenDayQuotaUsd)
    require.InDelta(t, 110.0, *created.SevenDayQuotaUsd, 0.000001)

    updatedQuota := 260.0
    updated, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
        SevenDayQuotaUSD: &updatedQuota,
    })
    require.NoError(t, err)
    require.NotNil(t, updated.SevenDayQuotaUsd)
    require.InDelta(t, 260.0, *updated.SevenDayQuotaUsd, 0.000001)

    listed, err := svc.ListPlansForSale(ctx)
    require.NoError(t, err)
    require.Len(t, listed, 1)
    require.NotNil(t, listed[0].SevenDayQuotaUsd)
    require.InDelta(t, 260.0, *listed[0].SevenDayQuotaUsd, 0.000001)
}
```

If this file uses different helper names, keep the assertion body exactly and adapt only harness setup to the existing test helpers in that file.

- [ ] **Step 2: Run the new service test and verify it fails**

Run:

```bash
cd backend && go test ./internal/service -run TestPaymentConfigServicePlanSevenDayQuota -count=1
```

Expected: FAIL because `CreatePlanRequest.SevenDayQuotaUSD` and generated Ent plan quota setters are not wired yet.

- [ ] **Step 3: Extend create/update request structs**

In `backend/internal/service/payment_config_service.go`, update the plan request structs:

```go
type CreatePlanRequest struct {
    GroupID          int64    `json:"group_id"`
    Name             string   `json:"name"`
    Description      string   `json:"description"`
    Price            float64  `json:"price"`
    OriginalPrice    *float64 `json:"original_price"`
    SevenDayQuotaUSD *float64 `json:"seven_day_quota_usd"`
    ValidityDays     int      `json:"validity_days"`
    ValidityUnit     string   `json:"validity_unit"`
    Features         string   `json:"features"`
    ProductName      string   `json:"product_name"`
    ForSale          bool     `json:"for_sale"`
    SortOrder        int      `json:"sort_order"`
}

type UpdatePlanRequest struct {
    GroupID          *int64   `json:"group_id"`
    Name             *string  `json:"name"`
    Description      *string  `json:"description"`
    Price            *float64 `json:"price"`
    OriginalPrice    *float64 `json:"original_price"`
    SevenDayQuotaUSD *float64 `json:"seven_day_quota_usd"`
    ValidityDays     *int     `json:"validity_days"`
    ValidityUnit     *string  `json:"validity_unit"`
    Features         *string  `json:"features"`
    ProductName      *string  `json:"product_name"`
    ForSale          *bool    `json:"for_sale"`
    SortOrder        *int     `json:"sort_order"`
}
```

- [ ] **Step 4: Persist create/update quota in plan service**

In `backend/internal/service/payment_config_plans.go`, update `CreatePlan` builder:

```go
builder := s.entClient.SubscriptionPlan.Create().
    SetGroupID(req.GroupID).
    SetName(strings.TrimSpace(req.Name)).
    SetDescription(strings.TrimSpace(req.Description)).
    SetPrice(req.Price).
    SetValidityDays(validityDays).
    SetValidityUnit(validityUnit).
    SetFeatures(strings.TrimSpace(req.Features)).
    SetProductName(strings.TrimSpace(req.ProductName)).
    SetForSale(req.ForSale).
    SetSortOrder(req.SortOrder)

builder.SetNillableOriginalPrice(req.OriginalPrice)
builder.SetNillableSevenDayQuotaUsd(req.SevenDayQuotaUSD)
```

Update `UpdatePlan` to apply the new field:

```go
if req.SevenDayQuotaUSD != nil {
    builder.SetSevenDayQuotaUsd(*req.SevenDayQuotaUSD)
}
```

If the admin must clear quota, add a separate boolean only if current update patterns already support clearing nullable fields. Otherwise leave clearing unsupported and document in UI that empty means no change on edit.

- [ ] **Step 5: Add quota to user-facing payment responses**

In `backend/internal/handler/payment_handler.go`, add `SevenDayQuotaUSD` to `planWithPlatform`:

```go
SevenDayQuotaUSD *float64 `json:"seven_day_quota_usd"`
```

Set it when appending:

```go
SevenDayQuotaUSD: p.SevenDayQuotaUsd,
```

Add the same field to `checkoutPlan`:

```go
SevenDayQuotaUSD *float64 `json:"seven_day_quota_usd"`
```

Set it in `GetCheckoutInfo`:

```go
SevenDayQuotaUSD: p.SevenDayQuotaUsd,
```

- [ ] **Step 6: Update admin handler request binding if it uses local request DTOs**

In `backend/internal/handler/admin/payment_handler.go`, find plan create/update request structs or bind calls. Ensure the JSON payload includes:

```go
SevenDayQuotaUSD *float64 `json:"seven_day_quota_usd"`
```

Pass it into `service.CreatePlanRequest` / `service.UpdatePlanRequest`:

```go
SevenDayQuotaUSD: req.SevenDayQuotaUSD,
```

- [ ] **Step 7: Run quota plan tests**

Run:

```bash
cd backend && go test ./internal/service ./internal/handler -run 'TestPaymentConfigServicePlanSevenDayQuota|TestGetCheckoutInfo.*Quota' -count=1
```

Expected: PASS after adding or updating the handler assertion.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/service/payment_config_service.go backend/internal/service/payment_config_plans.go backend/internal/handler/payment_handler.go backend/internal/handler/admin/payment_handler.go backend/internal/service/payment_config_plans_test.go backend/internal/handler/payment_handler_test.go
git commit -m "feat: expose subscription plan quotas"
```

---

### Task 3: Snapshot purchased plan quota on subscription fulfillment

**Files:**
- Modify: `backend/internal/service/user_subscription.go`
- Modify: `backend/internal/service/user_subscription_port.go`
- Modify: `backend/internal/repository/user_subscription_repo.go`
- Modify: `backend/internal/service/subscription_service.go`
- Modify: `backend/internal/service/payment_fulfillment.go`
- Test: `backend/internal/service/payment_fulfillment_test.go`
- Test: `backend/internal/service/subscription_assign_idempotency_test.go`

- [ ] **Step 1: Write fulfillment test for first purchase snapshot**

Add to `backend/internal/service/payment_fulfillment_test.go`:

```go
func TestPaymentFulfillmentSnapshotsSubscriptionPlanQuota(t *testing.T) {
    ctx := context.Background()
    h := newPaymentFulfillmentTestHarness(t)

    group := h.createSubscriptionGroup(t, "LINX2 Subscription")
    quota := 110.0
    plan := h.createSubscriptionPlan(t, group.ID, "Plus monthly", 399, 30, &quota)
    order := h.createPaidSubscriptionOrder(t, h.user.ID, plan.ID, group.ID, 30)

    require.NoError(t, h.paymentService.FulfillOrder(ctx, order.ID))

    sub, err := h.subscriptionRepo.GetByUserAndGroup(ctx, h.user.ID, group.ID)
    require.NoError(t, err)
    require.NotNil(t, sub.PlanID)
    require.Equal(t, int64(plan.ID), *sub.PlanID)
    require.NotNil(t, sub.PlanName)
    require.Equal(t, "Plus monthly", *sub.PlanName)
    require.NotNil(t, sub.SevenDayLimitUSD)
    require.InDelta(t, 110.0, *sub.SevenDayLimitUSD, 0.000001)
    require.NotNil(t, sub.WeeklyWindowStart)
    require.WithinDuration(t, time.Now(), *sub.WeeklyWindowStart, 2*time.Second)
    require.Zero(t, sub.WeeklyUsageUSD)
}
```

If this test harness has different helper names, add helper methods in the same file that create a group, plan, order, and service using existing patterns from the file.

- [ ] **Step 2: Write fulfillment test for active plan switch**

Add to `backend/internal/service/payment_fulfillment_test.go`:

```go
func TestPaymentFulfillmentSwitchesActiveSubscriptionPlanAndExtendsExpiry(t *testing.T) {
    ctx := context.Background()
    h := newPaymentFulfillmentTestHarness(t)

    group := h.createSubscriptionGroup(t, "LINX2 Subscription")
    basicQuota := 50.0
    proQuota := 260.0
    basic := h.createSubscriptionPlan(t, group.ID, "Basic monthly", 179, 30, &basicQuota)
    pro := h.createSubscriptionPlan(t, group.ID, "Pro monthly", 799, 30, &proQuota)

    firstOrder := h.createPaidSubscriptionOrder(t, h.user.ID, basic.ID, group.ID, 30)
    require.NoError(t, h.paymentService.FulfillOrder(ctx, firstOrder.ID))

    before, err := h.subscriptionRepo.GetByUserAndGroup(ctx, h.user.ID, group.ID)
    require.NoError(t, err)
    originalExpiry := before.ExpiresAt
    require.NoError(t, h.subscriptionRepo.IncrementUsage(ctx, before.ID, 20))

    switchOrder := h.createPaidSubscriptionOrder(t, h.user.ID, pro.ID, group.ID, 30)
    require.NoError(t, h.paymentService.FulfillOrder(ctx, switchOrder.ID))

    after, err := h.subscriptionRepo.GetByUserAndGroup(ctx, h.user.ID, group.ID)
    require.NoError(t, err)
    require.NotNil(t, after.PlanID)
    require.Equal(t, int64(pro.ID), *after.PlanID)
    require.NotNil(t, after.SevenDayLimitUSD)
    require.InDelta(t, 260.0, *after.SevenDayLimitUSD, 0.000001)
    require.Zero(t, after.WeeklyUsageUSD)
    require.NotNil(t, after.WeeklyWindowStart)
    require.True(t, after.WeeklyWindowStart.After(*before.WeeklyWindowStart) || after.WeeklyWindowStart.Equal(*before.WeeklyWindowStart))
    require.WithinDuration(t, originalExpiry.Add(30*24*time.Hour), after.ExpiresAt, time.Second)
}
```

- [ ] **Step 3: Run fulfillment tests and verify they fail**

Run:

```bash
cd backend && go test ./internal/service -run 'TestPaymentFulfillmentSnapshotsSubscriptionPlanQuota|TestPaymentFulfillmentSwitchesActiveSubscriptionPlanAndExtendsExpiry' -count=1
```

Expected: FAIL because snapshot fields and plan-aware assignment are not implemented.

- [ ] **Step 4: Extend service subscription model**

In `backend/internal/service/user_subscription.go`, add fields to `UserSubscription`:

```go
PlanID            *int64
PlanName          *string
SevenDayLimitUSD  *float64
```

Add helper methods:

```go
func (s *UserSubscription) EffectiveSevenDayLimit(group *Group) *float64 {
    if s == nil {
        return nil
    }
    if s.SevenDayLimitUSD != nil {
        return s.SevenDayLimitUSD
    }
    if group != nil && group.WeeklyLimitUSD != nil {
        return group.WeeklyLimitUSD
    }
    return nil
}

func (s *UserSubscription) SevenDayRemaining(group *Group) *float64 {
    limit := s.EffectiveSevenDayLimit(group)
    if limit == nil {
        return nil
    }
    remaining := *limit - s.WeeklyUsageUSD
    if remaining < 0 {
        remaining = 0
    }
    return &remaining
}

func (s *UserSubscription) CanUseSevenDayQuota(group *Group, cost float64) bool {
    if cost <= 0 {
        return true
    }
    remaining := s.SevenDayRemaining(group)
    return remaining != nil && *remaining+1e-9 >= cost
}
```

Update `CheckWeeklyLimit` to use snapshot first:

```go
func (s *UserSubscription) CheckWeeklyLimit(group *Group, additionalCost float64) bool {
    limit := s.EffectiveSevenDayLimit(group)
    if limit == nil {
        return true
    }
    return s.WeeklyUsageUSD+additionalCost <= *limit
}
```

- [ ] **Step 5: Extend repository port and assignment input**

In `backend/internal/service/user_subscription_port.go`, add snapshot fields to any create/update structs:

```go
PlanID           *int64
PlanName         *string
SevenDayLimitUSD *float64
```

Add a repository method for plan switch/snapshot updates:

```go
UpdatePlanSnapshot(ctx context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, windowStart time.Time, expiresAt time.Time, notes *string) error
```

If the existing repository already has a general update method, add these fields there instead and keep only one update method.

- [ ] **Step 6: Persist and map snapshot fields in repository**

In `backend/internal/repository/user_subscription_repo.go`, update create builder:

```go
builder.SetNillablePlanID(sub.PlanID)
builder.SetNillablePlanName(sub.PlanName)
builder.SetNillableSevenDayLimitUsd(sub.SevenDayLimitUSD)
```

Update mapper from Ent model to service model:

```go
PlanID:           entSub.PlanID,
PlanName:         entSub.PlanName,
SevenDayLimitUSD: entSub.SevenDayLimitUsd,
```

Implement `UpdatePlanSnapshot` with:

```go
func (r *userSubscriptionRepository) UpdatePlanSnapshot(ctx context.Context, id int64, planID *int64, planName *string, sevenDayLimitUSD *float64, windowStart time.Time, expiresAt time.Time, notes *string) error {
    update := r.client.UserSubscription.UpdateOneID(id).
        SetExpiresAt(expiresAt).
        SetWeeklyWindowStart(windowStart).
        SetWeeklyUsageUsd(0).
        SetUpdatedAt(time.Now())
    update.SetNillablePlanID(planID)
    update.SetNillablePlanName(planName)
    update.SetNillableSevenDayLimitUsd(sevenDayLimitUSD)
    if notes != nil {
        update.SetNotes(*notes)
    }
    return update.Exec(ctx)
}
```

- [ ] **Step 7: Make assignment input plan-aware**

In `backend/internal/service/subscription_service.go`, update `AssignSubscriptionInput`:

```go
type AssignSubscriptionInput struct {
    UserID           int64
    GroupID          int64
    ValidityDays     int
    AssignedBy       int64
    Notes            string
    PlanID           *int64
    PlanName         *string
    SevenDayLimitUSD *float64
}
```

Update `createSubscription` so first purchase uses exact activation time, not start-of-day:

```go
now := time.Now()
sub := &UserSubscription{
    UserID:           input.UserID,
    GroupID:          input.GroupID,
    StartsAt:         now,
    ExpiresAt:        now.AddDate(0, 0, input.ValidityDays),
    Status:           domain.SubscriptionStatusActive,
    WeeklyWindowStart: &now,
    WeeklyUsageUSD:   0,
    MonthlyWindowStart: &now,
    DailyWindowStart: &now,
    AssignedBy:       assignedByPtr(input.AssignedBy),
    AssignedAt:       now,
    Notes:            notesPtr(input.Notes),
    PlanID:           input.PlanID,
    PlanName:         input.PlanName,
    SevenDayLimitUSD: input.SevenDayLimitUSD,
}
```

Keep helper functions local if they already exist; otherwise use inline pointer assignments matching current style.

- [ ] **Step 8: Switch active subscriptions immediately and extend expiry**

In `AssignOrExtendSubscription`, when an existing subscription is active:

```go
now := time.Now()
baseExpiry := existing.ExpiresAt
if existing.IsExpired() {
    baseExpiry = now
}
newExpiry := baseExpiry.AddDate(0, 0, input.ValidityDays)
notes := input.Notes
err := s.userSubRepo.UpdatePlanSnapshot(ctx, existing.ID, input.PlanID, input.PlanName, input.SevenDayLimitUSD, now, newExpiry, &notes)
if err != nil {
    return nil, false, err
}
updated, err := s.userSubRepo.GetByID(ctx, existing.ID)
if err != nil {
    return nil, false, err
}
return updated, false, nil
```

Preserve existing semantics for expired subscriptions: restart from purchase completion time and use the same snapshot/window reset path.

- [ ] **Step 9: Load plan during payment fulfillment**

In `backend/internal/service/payment_fulfillment.go`, update `doSub` before assigning:

```go
var planID *int64
var planName *string
var sevenDayLimitUSD *float64
if o.PlanID != nil {
    plan, err := s.configService.GetPlanByID(ctx, *o.PlanID)
    if err != nil {
        return fmt.Errorf("load subscription plan: %w", err)
    }
    if plan.GroupID != gid || !plan.ForSale {
        return fmt.Errorf("plan %d no longer belongs to group %d or is unavailable", *o.PlanID, gid)
    }
    id := int64(plan.ID)
    name := plan.Name
    planID = &id
    planName = &name
    sevenDayLimitUSD = plan.SevenDayQuotaUsd
}
```

Pass snapshot fields:

```go
_, _, err = s.subscriptionSvc.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
    UserID:           o.UserID,
    GroupID:          gid,
    ValidityDays:     days,
    AssignedBy:       0,
    Notes:            orderNote,
    PlanID:           planID,
    PlanName:         planName,
    SevenDayLimitUSD: sevenDayLimitUSD,
})
```

If `PaymentService` does not currently hold `configService`, add it to the struct constructor using the existing dependency injection pattern already used by payment order creation.

- [ ] **Step 10: Run fulfillment tests**

Run:

```bash
cd backend && go test ./internal/service -run 'TestPaymentFulfillmentSnapshotsSubscriptionPlanQuota|TestPaymentFulfillmentSwitchesActiveSubscriptionPlanAndExtendsExpiry|TestAssign' -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add backend/internal/service/user_subscription.go backend/internal/service/user_subscription_port.go backend/internal/repository/user_subscription_repo.go backend/internal/service/subscription_service.go backend/internal/service/payment_fulfillment.go backend/internal/service/payment_fulfillment_test.go backend/internal/service/subscription_assign_idempotency_test.go
git commit -m "feat: snapshot subscription plan quotas"
```

---

### Task 4: Reset seven-day windows from exact activation/reset time

**Files:**
- Modify: `backend/internal/service/subscription_service.go`
- Modify: `backend/internal/service/user_subscription.go`
- Modify: `backend/internal/repository/user_subscription_repo.go`
- Test: `backend/internal/service/subscription_reset_quota_test.go`

- [ ] **Step 1: Write exact reset-time test**

Add to `backend/internal/service/subscription_reset_quota_test.go`:

```go
func TestSubscriptionSevenDayWindowResetsFromExactTimestamp(t *testing.T) {
    ctx := context.Background()
    h := newSubscriptionServiceTestHarness(t)

    start := time.Date(2026, 6, 11, 16, 45, 30, 0, time.UTC)
    quota := 50.0
    sub := h.createSubscription(t, &service.UserSubscription{
        UserID: h.user.ID,
        GroupID: h.group.ID,
        StartsAt: start,
        ExpiresAt: start.Add(30 * 24 * time.Hour),
        Status: domain.SubscriptionStatusActive,
        WeeklyWindowStart: &start,
        WeeklyUsageUSD: 49,
        SevenDayLimitUSD: &quota,
    })

    beforeReset := start.Add(7*24*time.Hour - time.Second)
    require.False(t, sub.NeedsWeeklyResetAt(beforeReset))

    resetAt := start.Add(7 * 24 * time.Hour)
    require.True(t, sub.NeedsWeeklyResetAt(resetAt))
    require.NoError(t, h.subscriptionService.CheckAndResetWindowsAt(ctx, sub, resetAt))

    updated, err := h.subscriptionRepo.GetByID(ctx, sub.ID)
    require.NoError(t, err)
    require.NotNil(t, updated.WeeklyWindowStart)
    require.Equal(t, resetAt, *updated.WeeklyWindowStart)
    require.Zero(t, updated.WeeklyUsageUSD)
}
```

- [ ] **Step 2: Run reset test and verify it fails**

Run:

```bash
cd backend && go test ./internal/service -run TestSubscriptionSevenDayWindowResetsFromExactTimestamp -count=1
```

Expected: FAIL because reset uses `startOfDay(time.Now())` and `time.Since`.

- [ ] **Step 3: Add time-injected reset helpers**

In `backend/internal/service/user_subscription.go`, add:

```go
func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
    if s.WeeklyWindowStart == nil {
        return false
    }
    return !now.Before(s.WeeklyWindowStart.Add(7 * 24 * time.Hour))
}
```

Update existing method:

```go
func (s *UserSubscription) NeedsWeeklyReset() bool {
    return s.NeedsWeeklyResetAt(time.Now())
}
```

- [ ] **Step 4: Make reset service use exact current time**

In `backend/internal/service/subscription_service.go`, add:

```go
func (s *SubscriptionService) CheckAndResetWindowsAt(ctx context.Context, sub *UserSubscription, now time.Time) error {
    if sub.NeedsDailyReset() {
        if err := s.userSubRepo.ResetDailyUsage(ctx, sub.ID, now); err != nil {
            return err
        }
        sub.DailyWindowStart = &now
        sub.DailyUsageUSD = 0
    }
    if sub.NeedsWeeklyResetAt(now) {
        if err := s.userSubRepo.ResetWeeklyUsage(ctx, sub.ID, now); err != nil {
            return err
        }
        sub.WeeklyWindowStart = &now
        sub.WeeklyUsageUSD = 0
    }
    if sub.NeedsMonthlyReset() {
        if err := s.userSubRepo.ResetMonthlyUsage(ctx, sub.ID, now); err != nil {
            return err
        }
        sub.MonthlyWindowStart = &now
        sub.MonthlyUsageUSD = 0
    }
    return nil
}
```

Update existing `CheckAndResetWindows`:

```go
func (s *SubscriptionService) CheckAndResetWindows(ctx context.Context, sub *UserSubscription) error {
    return s.CheckAndResetWindowsAt(ctx, sub, time.Now())
}
```

Update `CheckAndActivateWindow` to use exact `time.Now()` instead of `startOfDay(time.Now())`.

- [ ] **Step 5: Run reset tests**

Run:

```bash
cd backend && go test ./internal/service -run 'TestSubscriptionSevenDayWindowResetsFromExactTimestamp|Reset' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/subscription_service.go backend/internal/service/user_subscription.go backend/internal/service/subscription_reset_quota_test.go
git commit -m "fix: reset subscription quota on exact window time"
```

---

### Task 5: Implement subscription-first billing with recharge fallback

**Files:**
- Modify: `backend/internal/service/usage_billing.go`
- Modify: `backend/internal/repository/usage_billing_repo.go`
- Modify: `backend/internal/server/middleware/api_key_auth.go`
- Modify: `backend/internal/service/gateway_service.go`
- Modify: `backend/internal/service/openai_gateway_service.go`
- Test: `backend/internal/service/gateway_service_subscription_billing_test.go`
- Test: `backend/internal/repository/usage_billing_repo_integration_test.go`

- [ ] **Step 1: Write billing command test for fallback decision**

Add to `backend/internal/service/gateway_service_subscription_billing_test.go`:

```go
func TestBuildUsageBillingCommandFallsBackToBalanceWhenSubscriptionQuotaInsufficient(t *testing.T) {
    quota := 50.0
    windowStart := time.Now().Add(-time.Hour)
    sub := &UserSubscription{
        ID: 42,
        WeeklyWindowStart: &windowStart,
        WeeklyUsageUSD: 49.5,
        SevenDayLimitUSD: &quota,
    }
    group := &Group{ID: 7, SubscriptionType: domain.SubscriptionTypeSubscription}

    cmd := buildUsageBillingCommand(usageBillingParams{
        UserID: 1,
        UserBalance: 10,
        Group: group,
        Subscription: sub,
        Cost: UsageCost{ActualCost: 1.0, TotalCost: 1.0},
        IsSubscriptionBill: true,
    })

    require.Nil(t, cmd.SubscriptionID)
    require.Zero(t, cmd.SubscriptionCost)
    require.InDelta(t, 1.0, cmd.BalanceCost, 0.000001)
}

func TestBuildUsageBillingCommandUsesSubscriptionWhenQuotaCoversCost(t *testing.T) {
    quota := 50.0
    windowStart := time.Now().Add(-time.Hour)
    sub := &UserSubscription{
        ID: 42,
        WeeklyWindowStart: &windowStart,
        WeeklyUsageUSD: 49.0,
        SevenDayLimitUSD: &quota,
    }
    group := &Group{ID: 7, SubscriptionType: domain.SubscriptionTypeSubscription}

    cmd := buildUsageBillingCommand(usageBillingParams{
        UserID: 1,
        UserBalance: 10,
        Group: group,
        Subscription: sub,
        Cost: UsageCost{ActualCost: 1.0, TotalCost: 1.0},
        IsSubscriptionBill: true,
    })

    require.NotNil(t, cmd.SubscriptionID)
    require.Equal(t, int64(42), *cmd.SubscriptionID)
    require.InDelta(t, 1.0, cmd.SubscriptionCost, 0.000001)
    require.Zero(t, cmd.BalanceCost)
}
```

Adjust `UsageCost` field names to the current struct in `backend/internal/service/gateway_service.go` if they differ.

- [ ] **Step 2: Write repository integration test for guarded subscription consumption**

Add to `backend/internal/repository/usage_billing_repo_integration_test.go`:

```go
func TestUsageBillingApplyFallsBackToBalanceWhenSubscriptionQuotaRaceLoses(t *testing.T) {
    ctx := context.Background()
    h := newUsageBillingIntegrationHarness(t)

    quota := 50.0
    sub := h.createUserSubscription(t, h.user.ID, h.group.ID, quota, 49.75)
    h.setUserBalance(t, h.user.ID, 20)
    subID := sub.ID

    result, err := h.billingRepo.Apply(ctx, service.UsageBillingCommand{
        UserID: h.user.ID,
        GroupID: &h.group.ID,
        SubscriptionID: &subID,
        SubscriptionCost: 1.0,
        BalanceCost: 0,
        AllowBalanceFallback: true,
    })

    require.NoError(t, err)
    require.Equal(t, service.BillingTypeBalance, result.BillingType)
    require.InDelta(t, 19.0, result.NewBalance, 0.000001)

    updated := h.getSubscription(t, sub.ID)
    require.InDelta(t, 49.75, updated.WeeklyUsageUsd, 0.000001)
}
```

If current `UsageBillingApplyResult` has different fields, assert the persisted user balance and subscription usage instead.

- [ ] **Step 3: Run billing tests and verify they fail**

Run:

```bash
cd backend && go test ./internal/service ./internal/repository -run 'TestBuildUsageBillingCommand.*Subscription|TestUsageBillingApplyFallsBackToBalanceWhenSubscriptionQuotaRaceLoses' -count=1
```

Expected: FAIL because fallback command/result fields and guarded updates do not exist.

- [ ] **Step 4: Extend billing command/result**

In `backend/internal/service/usage_billing.go`, add fields:

```go
type UsageBillingCommand struct {
    UserID               int64
    GroupID              *int64
    SubscriptionID       *int64
    BalanceCost          float64
    SubscriptionCost     float64
    AllowBalanceFallback bool
}
```

If the struct already has these first five fields, add only `AllowBalanceFallback`.

Ensure `UsageBillingApplyResult` can represent the ledger used:

```go
type UsageBillingApplyResult struct {
    BillingType BillingType
    NewBalance  float64
}
```

If `BillingType` is already recorded in usage logs outside the result, return an equivalent boolean such as `UsedBalanceFallback bool` and adapt callers.

- [ ] **Step 5: Decide ledger in `buildUsageBillingCommand`**

In `backend/internal/service/gateway_service.go`, update the subscription branch:

```go
if p.IsSubscriptionBill && p.Subscription != nil && p.Cost.TotalCost > 0 {
    if p.Subscription.CanUseSevenDayQuota(p.Group, p.Cost.ActualCost) {
        cmd.SubscriptionID = &p.Subscription.ID
        cmd.SubscriptionCost = p.Cost.ActualCost
        cmd.AllowBalanceFallback = true
    } else if p.Cost.ActualCost > 0 {
        cmd.BalanceCost = p.Cost.ActualCost
    }
} else if p.Cost.ActualCost > 0 {
    cmd.BalanceCost = p.Cost.ActualCost
}
```

Keep `SubscriptionCost` based on `ActualCost`, preserving the existing bug fix in `gateway_service_subscription_billing_test.go`.

- [ ] **Step 6: Apply the same command logic to OpenAI gateway path**

In `backend/internal/service/openai_gateway_service.go`, find the equivalent usage billing command construction and use the same `CanUseSevenDayQuota` decision before setting `SubscriptionCost` or `BalanceCost`.

- [ ] **Step 7: Relax API-key middleware preflight**

In `backend/internal/server/middleware/api_key_auth.go`, change subscription preflight so weekly limit exceedance does not immediately abort if recharge balance exists:

```go
if subscription != nil {
    needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
    if validateErr != nil {
        isQuotaErr := errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
            errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
            errors.Is(validateErr, service.ErrMonthlyLimitExceeded)
        if !isQuotaErr || apiKey.User.Balance <= 0 {
            code := "SUBSCRIPTION_INVALID"
            status := 403
            if isQuotaErr {
                code = "USAGE_LIMIT_EXCEEDED"
                status = 429
            }
            AbortWithError(c, status, code, validateErr.Error())
            return
        }
    }
    if needsMaintenance {
        maintenanceCopy := *subscription
        subscriptionService.DoWindowMaintenance(&maintenanceCopy)
    }
} else if apiKey.User.Balance <= 0 {
    AbortWithError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
    return
}
```

If daily/monthly group limits remain hard limits for legacy subscriptions, only relax `ErrWeeklyLimitExceeded` for subscriptions with `SevenDayLimitUSD != nil`.

- [ ] **Step 8: Guard subscription update and fallback atomically**

In `backend/internal/repository/usage_billing_repo.go`, update the transaction branch for `cmd.SubscriptionCost > 0`:

```go
if cmd.SubscriptionCost > 0 && cmd.SubscriptionID != nil {
    res, err := tx.ExecContext(ctx, `
        UPDATE user_subscriptions
        SET weekly_usage_usd = weekly_usage_usd + $1,
            daily_usage_usd = daily_usage_usd + $1,
            monthly_usage_usd = monthly_usage_usd + $1,
            updated_at = NOW()
        WHERE id = $2
          AND deleted_at IS NULL
          AND (
              seven_day_limit_usd IS NULL
              OR weekly_usage_usd + $1 <= seven_day_limit_usd
          )
    `, cmd.SubscriptionCost, *cmd.SubscriptionID)
    if err != nil {
        return result, err
    }
    rows, err := res.RowsAffected()
    if err != nil {
        return result, err
    }
    if rows == 1 {
        result.BillingType = BillingTypeSubscription
        return result, nil
    }
    if !cmd.AllowBalanceFallback {
        return result, service.ErrWeeklyLimitExceeded
    }
    cmd.BalanceCost = cmd.SubscriptionCost
    cmd.SubscriptionCost = 0
}
```

Then let the existing balance deduction branch run for `cmd.BalanceCost > 0`. Ensure the balance branch rejects insufficient balance exactly as before.

- [ ] **Step 9: Set final usage log billing type from actual billing result**

In `backend/internal/service/gateway_service.go` and `backend/internal/service/openai_gateway_service.go`, after `applyUsageBilling`, set the usage log billing type from the result:

```go
if billingResult.BillingType == BillingTypeBalance {
    billingType = BillingTypeBalance
} else if billingResult.BillingType == BillingTypeSubscription {
    billingType = BillingTypeSubscription
}
```

If the current code creates the usage log before applying billing, move only the billing type assignment so it reflects fallback without changing token/cost logging.

- [ ] **Step 10: Run billing tests**

Run:

```bash
cd backend && go test ./internal/service ./internal/repository ./internal/server/middleware -run 'SubscriptionBilling|UsageBilling|APIKey' -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add backend/internal/service/usage_billing.go backend/internal/repository/usage_billing_repo.go backend/internal/server/middleware/api_key_auth.go backend/internal/service/gateway_service.go backend/internal/service/openai_gateway_service.go backend/internal/service/gateway_service_subscription_billing_test.go backend/internal/repository/usage_billing_repo_integration_test.go
git commit -m "feat: fall back to recharge balance after subscription quota"
```

---

### Task 6: Expose subscription balance in user DTOs

**Files:**
- Modify: `backend/internal/handler/dto/types.go`
- Modify: `backend/internal/handler/dto/mappers.go`
- Modify: `backend/internal/handler/user_subscription_handler.go`
- Test: `backend/internal/handler/user_subscription_handler_test.go`

- [ ] **Step 1: Write DTO mapping test**

Add to `backend/internal/handler/user_subscription_handler_test.go`:

```go
func TestUserSubscriptionDTOIncludesSevenDayBalance(t *testing.T) {
    quota := 110.0
    planID := int64(12)
    planName := "Plus monthly"
    windowStart := time.Date(2026, 6, 11, 16, 45, 30, 0, time.UTC)

    dtoSub := dto.UserSubscriptionFromService(&service.UserSubscription{
        ID: 1,
        UserID: 2,
        GroupID: 3,
        PlanID: &planID,
        PlanName: &planName,
        SevenDayLimitUSD: &quota,
        WeeklyWindowStart: &windowStart,
        WeeklyUsageUSD: 35.25,
        StartsAt: windowStart,
        ExpiresAt: windowStart.Add(30 * 24 * time.Hour),
        Status: domain.SubscriptionStatusActive,
    }, &service.Group{ID: 3})

    require.NotNil(t, dtoSub.PlanID)
    require.Equal(t, planID, *dtoSub.PlanID)
    require.NotNil(t, dtoSub.PlanName)
    require.Equal(t, planName, *dtoSub.PlanName)
    require.NotNil(t, dtoSub.SevenDayLimitUSD)
    require.InDelta(t, 110.0, *dtoSub.SevenDayLimitUSD, 0.000001)
    require.InDelta(t, 35.25, dtoSub.SevenDayUsageUSD, 0.000001)
    require.NotNil(t, dtoSub.SevenDayRemainingUSD)
    require.InDelta(t, 74.75, *dtoSub.SevenDayRemainingUSD, 0.000001)
    require.NotNil(t, dtoSub.SevenDayResetAt)
    require.Equal(t, windowStart.Add(7*24*time.Hour), *dtoSub.SevenDayResetAt)
}
```

If mapper functions are unexported, place the test in package `dto` or export a small `UserSubscriptionFromService` wrapper.

- [ ] **Step 2: Run DTO test and verify it fails**

Run:

```bash
cd backend && go test ./internal/handler/... -run TestUserSubscriptionDTOIncludesSevenDayBalance -count=1
```

Expected: FAIL because DTO fields do not exist.

- [ ] **Step 3: Extend DTO type**

In `backend/internal/handler/dto/types.go`, add to `UserSubscription`:

```go
PlanID                *int64     `json:"plan_id,omitempty"`
PlanName              *string    `json:"plan_name,omitempty"`
SevenDayLimitUSD      *float64   `json:"seven_day_limit_usd,omitempty"`
SevenDayUsageUSD      float64    `json:"seven_day_usage_usd"`
SevenDayRemainingUSD  *float64   `json:"seven_day_remaining_usd,omitempty"`
SevenDayResetAt       *time.Time `json:"seven_day_reset_at,omitempty"`
```

- [ ] **Step 4: Map computed fields**

In `backend/internal/handler/dto/mappers.go`, update `userSubscriptionFromServiceBase` or the group-aware mapper:

```go
resetAt := sub.WeeklyResetTime()
remaining := sub.SevenDayRemaining(group)
return UserSubscription{
    ID: sub.ID,
    UserID: sub.UserID,
    GroupID: sub.GroupID,
    PlanID: sub.PlanID,
    PlanName: sub.PlanName,
    SevenDayLimitUSD: sub.EffectiveSevenDayLimit(group),
    SevenDayUsageUSD: sub.WeeklyUsageUSD,
    SevenDayRemainingUSD: remaining,
    SevenDayResetAt: resetAt,
    StartsAt: sub.StartsAt,
    ExpiresAt: sub.ExpiresAt,
    Status: sub.Status,
    DailyWindowStart: sub.DailyWindowStart,
    WeeklyWindowStart: sub.WeeklyWindowStart,
    MonthlyWindowStart: sub.MonthlyWindowStart,
    DailyUsageUSD: sub.DailyUsageUSD,
    WeeklyUsageUSD: sub.WeeklyUsageUSD,
    MonthlyUsageUSD: sub.MonthlyUsageUSD,
    CreatedAt: sub.CreatedAt,
    UpdatedAt: sub.UpdatedAt,
}
```

If the base mapper does not receive `group`, map snapshot fields in base and compute legacy group fallback in the handler where group is available.

- [ ] **Step 5: Ensure subscription endpoints use enriched mapper**

In `backend/internal/handler/user_subscription_handler.go`, for `/subscriptions`, `/subscriptions/active`, and summary/progress endpoints, load the group already attached to the subscription or query group by `GroupID`, then call the group-aware mapper so legacy subscriptions can use group weekly limits.

- [ ] **Step 6: Run handler tests**

Run:

```bash
cd backend && go test ./internal/handler/... -run 'Subscription.*DTO|UserSubscription' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/handler/dto/types.go backend/internal/handler/dto/mappers.go backend/internal/handler/user_subscription_handler.go backend/internal/handler/user_subscription_handler_test.go
git commit -m "feat: expose subscription quota balance"
```

---

### Task 7: Update frontend payment types and plan card

**Files:**
- Modify: `frontend/src/types/payment.ts`
- Modify: `frontend/src/components/payment/SubscriptionPlanCard.vue`
- Modify: `frontend/src/views/user/PaymentView.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Test: `frontend/src/components/payment/__tests__/SubscriptionPlanCard.spec.ts`
- Test: `frontend/src/views/user/__tests__/PaymentView.spec.ts`

- [ ] **Step 1: Write plan-card rendering test**

Update `frontend/src/components/payment/__tests__/SubscriptionPlanCard.spec.ts` with:

```ts
it('renders the plan seven-day quota before legacy group limits', () => {
  const wrapper = mount(SubscriptionPlanCard, {
    props: {
      plan: {
        id: 2,
        group_id: 1,
        group_platform: 'anthropic',
        group_name: 'LINX2 Subscription',
        rate_multiplier: 1,
        daily_limit_usd: null,
        weekly_limit_usd: 999,
        monthly_limit_usd: null,
        supported_model_scopes: ['claude'],
        name: 'Plus monthly',
        description: 'Everyday development',
        price: 399,
        validity_days: 30,
        validity_unit: 'day',
        seven_day_quota_usd: 110,
        features: ['Larger seven-day quota', 'Recharge fallback'],
        for_sale: true,
        sort_order: 20,
      },
      activeSubscription: null,
      loading: false,
    },
    global: testGlobal,
  })

  expect(wrapper.text()).toContain('$110')
  expect(wrapper.text()).toContain('7')
  expect(wrapper.text()).not.toContain('$999')
})
```

Use the existing test setup object name instead of `testGlobal` if the file already defines a different helper.

- [ ] **Step 2: Run frontend plan-card test and verify it fails**

Run:

```bash
cd frontend && npm run test:run -- SubscriptionPlanCard
```

Expected: FAIL because `seven_day_quota_usd` is not typed/rendered.

- [ ] **Step 3: Extend frontend payment type**

In `frontend/src/types/payment.ts`, add to `SubscriptionPlan`:

```ts
seven_day_quota_usd?: number | null
```

- [ ] **Step 4: Render quota in plan card**

In `frontend/src/components/payment/SubscriptionPlanCard.vue`, replace the weekly group limit display with plan quota first:

```vue
<div v-if="plan.seven_day_quota_usd != null" class="flex items-center justify-between">
  <span>{{ t('payment.planCard.sevenDayQuota') }}</span>
  <span class="font-semibold text-amber-300">${{ formatUSD(plan.seven_day_quota_usd) }}</span>
</div>
<div v-else-if="plan.weekly_limit_usd != null" class="flex items-center justify-between">
  <span>{{ t('payment.planCard.weeklyLimit') }}</span>
  <span>${{ formatUSD(plan.weekly_limit_usd) }}</span>
</div>
```

Add or reuse a formatter in the component:

```ts
function formatUSD(value: number): string {
  return value.toLocaleString(undefined, { maximumFractionDigits: 2 })
}
```

- [ ] **Step 5: Update purchase confirmation copy**

In `frontend/src/views/user/PaymentView.vue`, in the subscription confirmation/selected plan section, add:

```vue
<div v-if="selectedPlan?.seven_day_quota_usd != null" class="flex items-center justify-between text-sm">
  <span class="linx-muted">{{ t('payment.subscription.sevenDayQuota') }}</span>
  <span class="font-semibold text-amber-300">${{ formatMoney(selectedPlan.seven_day_quota_usd) }}</span>
</div>
<p class="text-xs linx-muted">
  {{ t('payment.subscription.quotaFirstHint') }}
</p>
```

Use the existing money formatter if the component already has one.

- [ ] **Step 6: Add translations**

In `frontend/src/i18n/locales/zh.ts`, add:

```ts
sevenDayQuota: '7 日额度',
quotaFirstHint: '使用时优先扣除月卡 7 日额度，不足时再使用充值余额。',
```

under the existing `payment.planCard` and `payment.subscription` sections respectively.

In `frontend/src/i18n/locales/en.ts`, add:

```ts
sevenDayQuota: '7-day quota',
quotaFirstHint: 'Usage consumes subscription quota first, then recharge balance if needed.',
```

- [ ] **Step 7: Run frontend tests**

Run:

```bash
cd frontend && npm run test:run -- SubscriptionPlanCard PaymentView
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/types/payment.ts frontend/src/components/payment/SubscriptionPlanCard.vue frontend/src/views/user/PaymentView.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts frontend/src/components/payment/__tests__/SubscriptionPlanCard.spec.ts frontend/src/views/user/__tests__/PaymentView.spec.ts
git commit -m "feat: show subscription plan quotas in checkout"
```

---

### Task 8: Replace home model-pricing table with LINX2 subscription cards

**Files:**
- Modify: `frontend/src/views/HomeView.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Test: `frontend/src/views/__tests__/HomeView.spec.ts`

- [ ] **Step 1: Write homepage test for four plans**

Update `frontend/src/views/__tests__/HomeView.spec.ts`:

```ts
it('renders the four LINX2 subscription plans on the home pricing section', () => {
  const wrapper = mount(HomeView, { global: homeViewGlobal })

  expect(wrapper.text()).toContain('Basic')
  expect(wrapper.text()).toContain('Plus')
  expect(wrapper.text()).toContain('Pro')
  expect(wrapper.text()).toContain('Max')
  expect(wrapper.text()).toContain('¥179')
  expect(wrapper.text()).toContain('¥399')
  expect(wrapper.text()).toContain('¥799')
  expect(wrapper.text()).toContain('¥1599')
  expect(wrapper.text()).toContain('$50')
  expect(wrapper.text()).toContain('$110')
  expect(wrapper.text()).toContain('$260')
  expect(wrapper.text()).toContain('$550')
})
```

Use the existing mount helper from this file instead of `homeViewGlobal` if present.

- [ ] **Step 2: Run homepage test and verify it fails**

Run:

```bash
cd frontend && npm run test:run -- HomeView
```

Expected: FAIL because the page still renders model pricing groups.

- [ ] **Step 3: Replace pricing data in HomeView**

In `frontend/src/views/HomeView.vue`, remove `pricingGroups` and `PriceGroup` / model-pricing types. Add:

```ts
interface HomeSubscriptionPlan {
  name: string
  price: string
  quota: string
  description: string
  benefits: string[]
  accent: boolean
}

const subscriptionPlans: HomeSubscriptionPlan[] = [
  {
    name: 'Basic',
    price: '¥179',
    quota: '$50 / 7 days',
    description: 'Lightweight access for trying LINX2 and occasional development sessions.',
    benefits: ['Seven-day quota refresh', 'Recharge balance fallback', 'Best for low-frequency personal work'],
    accent: false,
  },
  {
    name: 'Plus',
    price: '¥399',
    quota: '$110 / 7 days',
    description: 'A steady monthly plan for everyday individual development.',
    benefits: ['More room for daily coding', 'Clear usage window and reset time', 'Recharge balance covers overflow'],
    accent: true,
  },
  {
    name: 'Pro',
    price: '¥799',
    quota: '$260 / 7 days',
    description: 'Higher quota for primary projects and frequent agent workflows.',
    benefits: ['Built for main development work', 'Larger seven-day allowance', 'Good fit for heavier LINX2 usage'],
    accent: false,
  },
  {
    name: 'Max',
    price: '¥1599',
    quota: '$550 / 7 days',
    description: 'The largest plan for demanding usage and parallel project work.',
    benefits: ['Highest seven-day quota', 'Supports more intensive sessions', 'Designed for power users'],
    accent: false,
  },
]
```

- [ ] **Step 4: Replace pricing section markup**

In the `<!-- ===== Pricing ===== -->` section, replace the current model pricing grid with:

```vue
<div class="grid gap-5 md:grid-cols-2 xl:grid-cols-4" data-testid="linx-subscription-pricing-grid">
  <article
    v-for="plan in subscriptionPlans"
    :key="plan.name"
    class="linx-panel relative flex h-full flex-col overflow-hidden p-6 transition duration-200 hover:-translate-y-1 hover:border-amber-400/40 hover:bg-linear-surface-2/70"
    :class="plan.accent ? 'border-amber-400/50 bg-amber-500/10 shadow-[0_0_0_1px_rgba(251,191,36,0.08),0_22px_70px_rgba(251,146,60,0.12)]' : ''"
  >
    <div v-if="plan.accent" class="absolute right-4 top-4 rounded-full border border-amber-400/30 bg-amber-500/10 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-[0.18em] text-amber-300">
      Recommended
    </div>
    <div class="mb-5">
      <p class="text-sm font-semibold uppercase tracking-[0.2em] text-amber-300">{{ plan.name }}</p>
      <div class="mt-4 flex items-end gap-1">
        <span class="text-4xl font-semibold text-linear-text">{{ plan.price }}</span>
        <span class="pb-1 text-sm linx-muted">/ month</span>
      </div>
      <p class="mt-3 inline-flex rounded-full border border-amber-400/25 bg-amber-500/10 px-3 py-1 text-sm font-semibold text-amber-200">
        {{ plan.quota }}
      </p>
      <p class="mt-4 text-sm leading-6 linx-muted">{{ plan.description }}</p>
    </div>
    <ul class="mb-6 space-y-3 text-sm linx-muted">
      <li v-for="benefit in plan.benefits" :key="benefit" class="flex gap-2">
        <span class="mt-2 h-1.5 w-1.5 rounded-full bg-amber-400"></span>
        <span>{{ benefit }}</span>
      </li>
    </ul>
    <RouterLink to="/purchase" class="btn btn-primary mt-auto w-full justify-center">
      Start with {{ plan.name }}
    </RouterLink>
  </article>
</div>
```

If the home page is localized already, move strings to i18n in the same task instead of leaving literals.

- [ ] **Step 5: Add localized strings if HomeView uses i18n for pricing**

In `frontend/src/i18n/locales/zh.ts`, add the Chinese equivalents for plan names, descriptions, benefits, and CTA under `home.pricingPlans`.

In `frontend/src/i18n/locales/en.ts`, add the English equivalents under the same keys.

- [ ] **Step 6: Run homepage test**

Run:

```bash
cd frontend && npm run test:run -- HomeView
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/views/HomeView.vue frontend/src/views/__tests__/HomeView.spec.ts frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts
git commit -m "feat: show LINX2 subscription pricing on home"
```

---

### Task 9: Display dual balances on dashboard and subscription pages

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/stores/subscriptions.ts`
- Modify: `frontend/src/views/user/DashboardView.vue`
- Modify: `frontend/src/components/user/dashboard/UserDashboardStats.vue`
- Modify: `frontend/src/views/user/SubscriptionsView.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Test: `frontend/src/components/__tests__/Dashboard.spec.ts`
- Test: `frontend/src/views/user/__tests__/SubscriptionsView.spec.ts`

- [ ] **Step 1: Write dashboard dual-balance test**

Update `frontend/src/components/__tests__/Dashboard.spec.ts` or the dashboard stats test file:

```ts
it('shows subscription balance separately from recharge balance', () => {
  const wrapper = mount(UserDashboardStats, {
    props: {
      stats: makeStats(),
      balance: 23.45,
      isSimple: false,
      platformQuotas: [],
      subscriptionBalance: {
        remaining: 74.75,
        total: 110,
        resetAt: '2026-06-18T16:45:30Z',
        planName: 'Plus monthly',
      },
    },
    global: dashboardGlobal,
  })

  expect(wrapper.text()).toContain('$74.75')
  expect(wrapper.text()).toContain('$110')
  expect(wrapper.text()).toContain('Plus monthly')
  expect(wrapper.text()).toContain('$23.45')
})
```

- [ ] **Step 2: Write subscription page balance test**

Update `frontend/src/views/user/__tests__/SubscriptionsView.spec.ts`:

```ts
it('renders seven-day subscription balance and next reset time', async () => {
  mockSubscriptionsAPI.getMySubscriptions.mockResolvedValue([
    {
      id: 1,
      user_id: 2,
      group_id: 3,
      group_name: 'LINX2 Subscription',
      plan_id: 10,
      plan_name: 'Plus monthly',
      seven_day_limit_usd: 110,
      seven_day_usage_usd: 35.25,
      seven_day_remaining_usd: 74.75,
      seven_day_reset_at: '2026-06-18T16:45:30Z',
      starts_at: '2026-06-11T16:45:30Z',
      expires_at: '2026-07-11T16:45:30Z',
      status: 'active',
      daily_usage_usd: 0,
      weekly_usage_usd: 35.25,
      monthly_usage_usd: 35.25,
    },
  ])

  const wrapper = mount(SubscriptionsView, { global: subscriptionsGlobal })
  await flushPromises()

  expect(wrapper.text()).toContain('Plus monthly')
  expect(wrapper.text()).toContain('$74.75')
  expect(wrapper.text()).toContain('$110')
  expect(wrapper.text()).toContain('2026')
})
```

Use the existing mocked API names in this test file.

- [ ] **Step 3: Run dual-balance tests and verify they fail**

Run:

```bash
cd frontend && npm run test:run -- Dashboard SubscriptionsView
```

Expected: FAIL because props/types/fields do not exist.

- [ ] **Step 4: Extend frontend subscription types**

In `frontend/src/types/index.ts`, add to `UserSubscription`:

```ts
plan_id?: number | null
plan_name?: string | null
seven_day_limit_usd?: number | null
seven_day_usage_usd?: number
seven_day_remaining_usd?: number | null
seven_day_reset_at?: string | null
```

Add a helper type:

```ts
export interface SubscriptionBalanceSummary {
  remaining: number
  total: number
  resetAt: string | null
  planName: string | null
}
```

- [ ] **Step 5: Add aggregate subscription balance computed value**

In `frontend/src/stores/subscriptions.ts`, import the type and add:

```ts
const subscriptionBalance = computed<SubscriptionBalanceSummary | null>(() => {
  const candidates = activeSubscriptions.value
    .filter(sub => sub.seven_day_limit_usd != null && sub.seven_day_remaining_usd != null)
    .sort((a, b) => (b.seven_day_remaining_usd || 0) - (a.seven_day_remaining_usd || 0))

  const sub = candidates[0]
  if (!sub || sub.seven_day_limit_usd == null || sub.seven_day_remaining_usd == null) {
    return null
  }

  return {
    remaining: sub.seven_day_remaining_usd,
    total: sub.seven_day_limit_usd,
    resetAt: sub.seven_day_reset_at || null,
    planName: sub.plan_name || sub.group_name || null,
  }
})
```

Return it from the store.

- [ ] **Step 6: Load subscriptions on dashboard**

In `frontend/src/views/user/DashboardView.vue`, import `useSubscriptionStore`, call `fetchActiveSubscriptions()` on mount for non-simple users, and pass:

```vue
:subscription-balance="subscriptionStore.subscriptionBalance"
```

to `UserDashboardStats`.

- [ ] **Step 7: Render dual balances in dashboard stats**

In `frontend/src/components/user/dashboard/UserDashboardStats.vue`, add prop:

```ts
subscriptionBalance?: SubscriptionBalanceSummary | null
```

Replace the single balance card with two cards for non-simple users:

```vue
<div v-if="!isSimple" class="linx-panel p-4">
  <div class="flex items-center justify-between">
    <div>
      <p class="text-xs font-medium linx-muted">{{ t('dashboard.subscriptionBalance') }}</p>
      <p class="text-xl font-bold text-amber-300">
        ${{ formatBalance(subscriptionBalance?.remaining || 0) }}
      </p>
      <p class="text-xs linx-muted">
        <template v-if="subscriptionBalance">
          {{ t('dashboard.subscriptionBalanceDetail', { total: formatBalance(subscriptionBalance.total), plan: subscriptionBalance.planName || '-' }) }}
        </template>
        <template v-else>{{ t('dashboard.noSubscriptionBalance') }}</template>
      </p>
    </div>
  </div>
</div>

<div v-if="!isSimple" class="linx-panel p-4">
  <div class="flex items-center justify-between">
    <div>
      <p class="text-xs font-medium linx-muted">{{ t('dashboard.rechargeBalance') }}</p>
      <p class="text-xl font-bold text-emerald-600 dark:text-emerald-400">${{ formatBalance(balance) }}</p>
      <p class="text-xs linx-muted">{{ t('dashboard.rechargeFallbackHint') }}</p>
    </div>
  </div>
</div>
```

- [ ] **Step 8: Update subscription page quota display**

In `frontend/src/views/user/SubscriptionsView.vue`, for each active subscription, show:

```vue
<div v-if="subscription.seven_day_limit_usd != null" class="linx-panel p-4">
  <div class="flex items-center justify-between gap-4">
    <div>
      <p class="text-sm font-semibold text-linear-text">{{ subscription.plan_name || subscription.group_name }}</p>
      <p class="text-xs linx-muted">{{ t('userSubscriptions.subscriptionFirstHint') }}</p>
    </div>
    <div class="text-right">
      <p class="text-lg font-semibold text-amber-300">
        ${{ formatMoney(subscription.seven_day_remaining_usd || 0) }} / ${{ formatMoney(subscription.seven_day_limit_usd) }}
      </p>
      <p class="text-xs linx-muted">
        {{ t('userSubscriptions.nextResetAt', { time: formatDateTime(subscription.seven_day_reset_at) }) }}
      </p>
    </div>
  </div>
  <div class="mt-3 h-2 overflow-hidden rounded-full bg-linear-surface-3">
    <div
      class="h-full rounded-full bg-amber-400"
      :style="{ width: `${Math.min(100, ((subscription.seven_day_usage_usd || 0) / subscription.seven_day_limit_usd) * 100)}%` }"
    />
  </div>
</div>
```

Use the component’s existing date/money helper names if they differ.

- [ ] **Step 9: Add translations**

In `frontend/src/i18n/locales/zh.ts`:

```ts
subscriptionBalance: '月卡余额',
subscriptionBalanceDetail: '{plan} · 共 ${total} / 7 日',
noSubscriptionBalance: '暂无可用月卡额度',
rechargeBalance: '充值余额',
rechargeFallbackHint: '月卡额度不足时自动使用',
subscriptionFirstHint: '请求会优先使用月卡 7 日额度，不足时回退到充值余额。',
nextResetAt: '下次重置：{time}',
```

In `frontend/src/i18n/locales/en.ts`:

```ts
subscriptionBalance: 'Subscription balance',
subscriptionBalanceDetail: '{plan} · ${total} per 7 days',
noSubscriptionBalance: 'No subscription quota available',
rechargeBalance: 'Recharge balance',
rechargeFallbackHint: 'Used automatically after subscription quota',
subscriptionFirstHint: 'Requests use subscription quota first, then recharge balance when needed.',
nextResetAt: 'Next reset: {time}',
```

- [ ] **Step 10: Run dual-balance tests**

Run:

```bash
cd frontend && npm run test:run -- Dashboard SubscriptionsView
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add frontend/src/types/index.ts frontend/src/stores/subscriptions.ts frontend/src/views/user/DashboardView.vue frontend/src/components/user/dashboard/UserDashboardStats.vue frontend/src/views/user/SubscriptionsView.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts frontend/src/components/__tests__/Dashboard.spec.ts frontend/src/views/user/__tests__/SubscriptionsView.spec.ts
git commit -m "feat: display subscription and recharge balances"
```

---

### Task 10: Add admin plan quota management UI

**Files:**
- Modify: `frontend/src/views/admin/orders/AdminPaymentPlansView.vue`
- Modify: `frontend/src/views/admin/orders/PlanEditDialog.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Test: `frontend/src/views/admin/orders/__tests__/PlanEditDialog.spec.ts`

- [ ] **Step 1: Write admin dialog test**

Create or update `frontend/src/views/admin/orders/__tests__/PlanEditDialog.spec.ts`:

```ts
it('submits seven-day quota when editing a payment plan', async () => {
  const wrapper = mount(PlanEditDialog, {
    props: {
      modelValue: true,
      plan: {
        id: 1,
        group_id: 2,
        name: 'Plus monthly',
        description: 'Everyday development',
        price: 399,
        validity_days: 30,
        validity_unit: 'day',
        seven_day_quota_usd: 110,
        features: ['Seven-day quota'],
        for_sale: true,
        sort_order: 20,
      },
      groups: [{ id: 2, name: 'LINX2 Subscription' }],
      loading: false,
    },
    global: adminGlobal,
  })

  const quota = wrapper.find('[data-testid="plan-seven-day-quota"]')
  await quota.setValue('260')
  await wrapper.find('form').trigger('submit.prevent')

  expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
    seven_day_quota_usd: 260,
  })
})
```

- [ ] **Step 2: Run admin UI test and verify it fails**

Run:

```bash
cd frontend && npm run test:run -- PlanEditDialog
```

Expected: FAIL because the field does not exist.

- [ ] **Step 3: Add quota column to admin plans table**

In `frontend/src/views/admin/orders/AdminPaymentPlansView.vue`, add a column:

```ts
{ key: 'seven_day_quota_usd', label: t('payment.admin.sevenDayQuota') },
```

Render it with:

```vue
<template #cell-seven_day_quota_usd="{ row }">
  <span v-if="row.seven_day_quota_usd != null">${{ formatMoney(row.seven_day_quota_usd) }}</span>
  <span v-else class="linx-muted">-</span>
</template>
```

Use the existing slot naming pattern in this table component if it differs.

- [ ] **Step 4: Add quota field to plan edit dialog state**

In `frontend/src/views/admin/orders/PlanEditDialog.vue`, add to `planForm`:

```ts
seven_day_quota_usd: 0,
```

When loading an existing plan:

```ts
planForm.seven_day_quota_usd = props.plan?.seven_day_quota_usd || 0
```

Add form input:

```vue
<label class="space-y-1">
  <span class="text-sm font-medium">{{ t('payment.admin.sevenDayQuota') }}</span>
  <input
    v-model.number="planForm.seven_day_quota_usd"
    data-testid="plan-seven-day-quota"
    type="number"
    min="0"
    step="0.01"
    class="input w-full"
  />
</label>
```

Update `buildPlanPayload()`:

```ts
seven_day_quota_usd: planForm.seven_day_quota_usd > 0 ? planForm.seven_day_quota_usd : null,
```

- [ ] **Step 5: Add admin translations**

In `frontend/src/i18n/locales/zh.ts` under `payment.admin`:

```ts
sevenDayQuota: '7 日额度（USD）',
```

In `frontend/src/i18n/locales/en.ts` under `payment.admin`:

```ts
sevenDayQuota: '7-day quota (USD)',
```

- [ ] **Step 6: Run admin UI tests**

Run:

```bash
cd frontend && npm run test:run -- PlanEditDialog AdminPaymentPlansView
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/views/admin/orders/AdminPaymentPlansView.vue frontend/src/views/admin/orders/PlanEditDialog.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts frontend/src/views/admin/orders/__tests__/PlanEditDialog.spec.ts
git commit -m "feat: manage subscription plan quotas in admin"
```

---

### Task 11: End-to-end validation and UI smoke test

**Files:**
- No source files should be modified unless validation finds a bug.
- Test: backend package tests
- Test: frontend typecheck/build/tests
- Browser: `/home`, `/purchase`, `/subscriptions`, dashboard route

- [ ] **Step 1: Run backend focused tests**

Run:

```bash
cd backend && go test ./internal/service ./internal/repository ./internal/handler/... ./internal/server/middleware -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader backend tests**

Run:

```bash
cd backend && go test ./...
```

Expected: PASS, or only known unrelated integration tests skipped by existing environment constraints. Any new subscription/payment/billing failure must be fixed before continuing.

- [ ] **Step 3: Run frontend tests and typecheck**

Run:

```bash
cd frontend && npm run test:run && npm run typecheck && npm run build
```

Expected: PASS.

- [ ] **Step 4: Start frontend dev server**

Run:

```bash
cd frontend && npm run dev -- --host 127.0.0.1
```

Expected: Vite starts and prints a local URL.

- [ ] **Step 5: Smoke-test `/home` in browser**

Open the Vite URL with `/home`.

Expected:
- Pricing section shows Basic, Plus, Pro, Max cards.
- Prices match ¥179, ¥399, ¥799, ¥1599.
- Quotas match $50, $110, $260, $550 per seven days.
- Visual style uses dark Linear surfaces, thin borders, and orange accents.
- CTA navigates to `/purchase`.

- [ ] **Step 6: Smoke-test `/purchase` subscription tab**

Log in if required, open `/purchase`, select the subscription tab.

Expected:
- Backend-provided plans render with the same prices and seven-day quotas.
- Selecting a plan shows quota and subscription-first hint.
- Existing recharge purchase flow still renders and is not visually merged with subscription quota.

- [ ] **Step 7: Smoke-test dashboard and `/subscriptions`**

Open dashboard and `/subscriptions` with a user that has an active plan snapshot.

Expected:
- Dashboard shows subscription balance separately from recharge balance.
- `/subscriptions` shows current active plan, seven-day used/remaining amount, next reset time, and expiry.
- Copy clearly says subscription quota is used before recharge balance.

- [ ] **Step 8: Smoke-test fallback billing manually if a local API key and test model are configured**

Set a test subscription with quota nearly exhausted and a positive recharge balance. Make one request whose actual cost exceeds remaining subscription quota.

Expected:
- Request is allowed.
- Subscription `weekly_usage_usd` does not exceed `seven_day_limit_usd`.
- User recharge balance decreases by actual cost.
- Usage log billing type records balance for the fallback request.

- [ ] **Step 9: Commit validation fixes if any**

If validation required fixes:

```bash
git add <fixed files>
git commit -m "fix: stabilize subscription quota flow"
```

If no fixes were needed, do not create an empty commit.

---

## Self-Review Checklist

- Spec coverage:
  - Four Basic/Plus/Pro/Max monthly plans are seeded and shown on `/home` and `/purchase`.
  - Plan-level seven-day quota exists on plan data and admin management.
  - Purchased plan and quota snapshot exist on `user_subscriptions`.
  - Active plan switch resets seven-day window immediately and extends expiry from current expiry.
  - Seven-day reset uses exact activation/switch/reset timestamp.
  - API billing uses subscription quota first and recharge balance fallback.
  - Both-ledger insufficient cases continue to reject via existing insufficient balance/quota errors.
  - Dashboard and subscription page display subscription balance and recharge balance separately.
- Placeholder scan:
  - No step says “TBD”, “TODO”, “similar to”, or “add tests” without concrete test content.
- Type consistency:
  - Backend JSON field is consistently `seven_day_quota_usd` for plan data.
  - Backend subscription snapshot field is consistently `seven_day_limit_usd` on user subscriptions.
  - Frontend `UserSubscription` uses `seven_day_limit_usd`, `seven_day_usage_usd`, `seven_day_remaining_usd`, and `seven_day_reset_at`.
  - Existing group `weekly_limit_usd` remains only as legacy fallback.
