# 可过期邀请与签到奖励实施计划

> **执行要求：** 实施时使用 `superpowers:test-driven-development`；每完成一组任务按本文命令验证并提交。完成前使用 `superpowers:verification-before-completion`，分支收尾时使用 `superpowers:finishing-a-development-branch`。

**目标：** 把邀请和签到奖励改为有独立有效期的奖励批次；扣费按“签到 → 邀请 → 订阅 → 账户自身余额”选择单一可完整承担请求的层；邀请活动支持开关、立即奖励/首充奖励、可配置双方金额/有效期/邀请方获奖次数上限，并完成相应用户端和管理端界面。

**总体架构：** `users.balance` 继续保存用户可用总额，新增原生 SQL 奖励批次、事件流水和批量冻结分配表。奖励发放、消费、冻结、释放、捕获和到期均与 `users.balance`/`frozen_balance` 在同一事务内更新。邀请仓储负责绑定/首充状态与双方奖励的原子操作；签到仓储在领取事务内创建奖励；普通请求与批量图片仓储执行奖励层选择和最早到期优先扣减。用户资料接口返回聚合摘要，独立接口分页返回邀请奖励明细。

**技术栈：** Go、Gin、Ent 事务上下文、PostgreSQL 原生 SQL、Redis 余额缓存、Vue 3、TypeScript、Pinia、Vitest、Tailwind CSS。

**设计依据：** `docs/superpowers/specs/2026-07-16-expiring-invite-checkin-rewards-design.md`

---

## 任务 1：建立数据库模型、约束和一次性迁移

**文件：**

- 新建：`backend/migrations/184_expiring_reward_credits.sql`
- 新建：`backend/migrations/expiring_reward_credits_migration_test.go`
- 修改：`backend/migrations/migrations.go`（仅当嵌入规则不是通配符时）

### 步骤 1：先写迁移契约测试

在 `expiring_reward_credits_migration_test.go` 中读取 SQL 并断言以下结构和执行顺序：

```go
func TestExpiringRewardCreditsMigrationContainsLedgerAndBackfills(t *testing.T) {
	data, err := os.ReadFile("184_expiring_reward_credits.sql")
	require.NoError(t, err)
	sqlText := string(data)

	require.Contains(t, sqlText, "CREATE TABLE IF NOT EXISTS user_reward_credits")
	require.Contains(t, sqlText, "CREATE TABLE IF NOT EXISTS user_reward_credit_events")
	require.Contains(t, sqlText, "CREATE TABLE IF NOT EXISTS batch_image_reward_allocations")
	require.Contains(t, sqlText, "UNIQUE (user_id, credit_type, source_key)")
	require.Contains(t, sqlText, "CHECK (remaining_amount + reserved_amount <= original_amount)")
	require.Contains(t, sqlText, "ADD COLUMN IF NOT EXISTS inviter_reward_count")
	require.Contains(t, sqlText, "ADD COLUMN IF NOT EXISTS reward_mode")
	require.Contains(t, sqlText, "ADD COLUMN IF NOT EXISTS reward_status")
	require.Contains(t, sqlText, "COUNT(DISTINCT source_user_id)")
	require.Contains(t, sqlText, "aff_quota + aff_frozen_quota")
	require.Less(t,
		strings.Index(sqlText, "UPDATE users"),
		strings.Index(sqlText, "SET aff_quota = 0"),
	)
}
```

再覆盖以下条件：

- `credit_type` 仅允许 `daily_check_in`、`affiliate_inviter`、`affiliate_invitee`。
- `event_type` 仅允许 `grant`、`consume`、`reserve`、`capture`、`release`、`expire`。
- 活跃批次、清理批次、事件用户/时间、hold 分配均有索引。
- 配置仅在当前值等于旧默认值时更新：阈值数值 20 改 0、邀请方奖励数值 5 改 10；自定义值不改。
- 老 `aff_quota + aff_frozen_quota` 先进入 `users.balance`，然后旧钱包归零；`aff_history_quota` 和历史流水不删除。
- `inviter_reward_count` 从正数 `accrue` 流水按不同 `source_user_id` 回填。
- 历史绑定统一写 `reward_mode='first_recharge'`；有成功充值或已应用/跳过审计的关系写 `resolved`，其余写 `pending`。

### 步骤 2：运行测试确认失败

```bash
cd backend && go test ./migrations -run TestExpiringRewardCreditsMigration -count=1
```

预期：因迁移文件不存在而失败。

### 步骤 3：实现迁移

`user_reward_credits` 使用以下核心列：

```sql
CREATE TABLE IF NOT EXISTS user_reward_credits (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credit_type VARCHAR(32) NOT NULL,
    source_key VARCHAR(191) NOT NULL,
    original_amount DECIMAL(20,8) NOT NULL,
    remaining_amount DECIMAL(20,8) NOT NULL,
    reserved_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    granted_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL,
    expired_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, credit_type, source_key),
    CHECK (credit_type IN ('daily_check_in', 'affiliate_inviter', 'affiliate_invitee')),
    CHECK (original_amount > 0),
    CHECK (remaining_amount >= 0),
    CHECK (reserved_amount >= 0),
    CHECK (remaining_amount + reserved_amount <= original_amount)
);
```

`user_reward_credit_events` 必须包含 `credit_id`、`user_id`、`event_type`、稳定 `event_key`、`amount`、`request_id`、`batch_id`、`created_at`，并以 `(credit_id, event_type, event_key)` 唯一防重。

`batch_image_reward_allocations` 必须包含 `hold_key`、`credit_id`、`user_id`、`reserved_amount`、`captured_amount`、`released_amount`、`expires_at_snapshot` 和时间戳，并以 `(hold_key, credit_id)` 唯一。

给 `user_affiliates` 增加：

```sql
inviter_reward_count INTEGER NOT NULL DEFAULT 0,
reward_mode VARCHAR(32) NULL,
reward_status VARCHAR(32) NULL,
reward_resolved_at TIMESTAMPTZ NULL,
reward_source_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE SET NULL,
inviter_rewarded BOOLEAN NOT NULL DEFAULT FALSE,
invitee_rewarded BOOLEAN NOT NULL DEFAULT FALSE
```

用约束保证未绑定用户允许状态为空，已绑定关系只能使用 `immediate|first_recharge` 与 `pending|resolved`。迁移中的资金转移、状态回填、默认值更新均使用幂等 SQL；不得创建历史奖励批次。

### 步骤 4：验证迁移测试

```bash
cd backend && go test ./migrations -run 'TestExpiringRewardCreditsMigration|TestBeginnerGuideMigration' -count=1
```

预期：通过。

### 步骤 5：提交

```bash
git add backend/migrations/184_expiring_reward_credits.sql backend/migrations/expiring_reward_credits_migration_test.go backend/migrations/migrations.go
git commit -m "feat: add expiring reward credit ledger"
```

---

## 任务 2：实现奖励批次领域类型和仓储原语

**文件：**

- 新建：`backend/internal/service/reward_credit.go`
- 新建：`backend/internal/repository/reward_credit_repo.go`
- 新建：`backend/internal/repository/reward_credit_repo_integration_test.go`
- 修改：`backend/internal/repository/wire.go`

### 步骤 1：写仓储集成测试

采用仓储现有 PostgreSQL 集成测试基座，覆盖：

1. 相同 `(user,type,source_key)` 发放两次只创建一批、只增加一次 `users.balance`。
2. `GetSummary` 忽略 `expires_at <= now`，返回签到金额/到期时间和邀请合计/最早到期/批次数。
3. `ListCredits` 只返回指定用户，按 `expires_at ASC,id ASC`，支持 `type/status/page/page_size`。
4. `ExpireUser` 将到期的未冻结余额归零并等额减少 `users.balance`，重复执行不重复扣。
5. `ExpireBatch` 使用 `FOR UPDATE SKIP LOCKED` 分批清理并写 `expire` 事件。
6. 并发发放同一来源键只有一方 `Applied=true`。

测试中的核心断言：

```go
grant := service.RewardCreditGrant{
	UserID:     userID,
	CreditType: service.RewardCreditDailyCheckIn,
	SourceKey:  "daily-check-in:42",
	Amount:     5,
	GrantedAt:  now,
	ExpiresAt:  now.Add(12 * time.Hour),
}
first, err := repo.Grant(ctx, grant)
require.NoError(t, err)
require.True(t, first.Applied)
second, err := repo.Grant(ctx, grant)
require.NoError(t, err)
require.False(t, second.Applied)
require.InDelta(t, 5, loadUserBalance(t, db, userID), 1e-8)
```

### 步骤 2：运行测试确认失败

```bash
cd backend && go test ./internal/repository -run TestRewardCreditRepository -count=1
```

预期：新类型/构造器未定义，编译失败。

### 步骤 3：定义稳定服务契约

在 `reward_credit.go` 定义：

```go
type RewardCreditType string

const (
	RewardCreditDailyCheckIn     RewardCreditType = "daily_check_in"
	RewardCreditAffiliateInviter RewardCreditType = "affiliate_inviter"
	RewardCreditAffiliateInvitee RewardCreditType = "affiliate_invitee"
)

type RewardCreditGrant struct {
	UserID     int64
	CreditType RewardCreditType
	SourceKey  string
	Amount     float64
	GrantedAt  time.Time
	ExpiresAt  time.Time
}

type RewardCreditGrantResult struct {
	CreditID    int64
	Applied     bool
	BalanceAfter float64
}

type DailyRewardBalanceSummary struct {
	Amount      float64    `json:"amount"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type AffiliateRewardBalanceSummary struct {
	Amount            float64    `json:"amount"`
	EarliestExpiresAt *time.Time `json:"earliest_expires_at"`
	CreditCount       int        `json:"credit_count"`
}

type RewardBalanceSummary struct {
	DailyCheckIn DailyRewardBalanceSummary     `json:"daily_check_in"`
	Affiliate   AffiliateRewardBalanceSummary `json:"affiliate"`
}

type RewardCreditListFilter struct {
	UserID     int64
	CreditType *RewardCreditType
	Status     string
	Page       int
	PageSize   int
	Now        time.Time
}

type RewardCreditExpiryResult struct {
	UserID        int64
	ExpiredAmount float64
}

type RewardCreditRepository interface {
	Grant(context.Context, RewardCreditGrant) (RewardCreditGrantResult, error)
	GetSummary(context.Context, int64, time.Time) (RewardBalanceSummary, error)
	ListCredits(context.Context, RewardCreditListFilter) ([]RewardCredit, int64, error)
	ExpireUser(context.Context, int64, time.Time) (float64, error)
	ExpireBatch(context.Context, time.Time, int) ([]RewardCreditExpiryResult, error)
}
```

`RewardCredit` 返回 `id/type/original_amount/remaining_amount/reserved_amount/granted_at/expires_at/role_label`。`role_label` 由 type 映射为 `inviter|invitee|check_in`，不从可变文案存库。

### 步骤 4：实现原子 SQL 助手和仓储

在 repository 包内提供可复用的事务助手：

```go
func grantRewardCreditTx(
	ctx context.Context,
	q rewardCreditQueryExecer,
	input service.RewardCreditGrant,
) (service.RewardCreditGrantResult, error)
```

它必须：

- `INSERT ... ON CONFLICT DO NOTHING RETURNING id`；冲突时返回 `Applied=false`。
- 插入成功后 `UPDATE users SET balance=balance+$amount RETURNING balance`。
- 写 `grant` 事件；任一步失败整笔回滚。
- 若 `ctx` 有 `dbent.TxFromContext` 则加入外部事务，否则仓储自行 `BeginTx`。

查询前先在同一事务内执行用户级惰性到期；所有金额用 `DECIMAL(20,8)` 和 epsilon 安全比较，不用字符串拼接类型/状态参数。

### 步骤 5：验证

```bash
cd backend && go test ./internal/repository -run TestRewardCreditRepository -count=1
cd backend && go test ./internal/service -run TestNonExistent -count=1
```

预期：仓储集成测试通过，service 包编译通过。

### 步骤 6：提交

```bash
git add backend/internal/service/reward_credit.go backend/internal/repository/reward_credit_repo.go backend/internal/repository/reward_credit_repo_integration_test.go backend/internal/repository/wire.go
git commit -m "feat: add reward credit repository"
```

---

## 任务 3：扩展邀请配置与管理端后端契约

**文件：**

- 修改：`backend/internal/service/domain_constants.go`
- 修改：`backend/internal/service/settings_view.go`
- 修改：`backend/internal/service/setting_service.go`
- 修改：`backend/internal/service/setting_service_update_test.go`
- 修改：`backend/internal/service/affiliate_service_test.go`
- 修改：`backend/internal/handler/admin/setting_handler.go`
- 新建：`backend/internal/handler/admin/setting_handler_affiliate_test.go`
- 修改：`backend/internal/handler/dto/settings.go`
- 修改：`backend/internal/handler/dto/public_settings_injection_schema_test.go`

### 步骤 1：先写设置测试

增加表驱动用例，断言：

- 新默认值：threshold=0、inviter=10、invitee=5、validityDays=7、limit=0。
- 有效期必须 `>=1`；获奖上限必须 `>=0`；金额/阈值必须有限、非负、最多 8 位小数。
- 更新请求漏掉新字段时保留旧值；显式传 0 的 limit/threshold 不被当成缺失。
- `GetPublicSettings` 继续只暴露 `affiliate_enabled`，不泄露奖励金额等后台配置。
- 注入设置与 HTTP DTO 的公开键一致。

```go
func TestAffiliateRewardConfigDefaults(t *testing.T) {
	cfg := newSettingServiceWithValues(t, map[string]string{})
	require.Equal(t, 0.0, cfg.GetAffiliateFirstRechargeThreshold(context.Background()))
	require.Equal(t, 10.0, cfg.GetAffiliateInviterReward(context.Background()))
	require.Equal(t, 5.0, cfg.GetAffiliateInviteeReward(context.Background()))
	require.Equal(t, 7, cfg.GetAffiliateRewardValidityDays(context.Background()))
	require.Equal(t, 0, cfg.GetAffiliateInviterRewardLimit(context.Background()))
}
```

### 步骤 2：确认失败

```bash
cd backend && go test ./internal/service ./internal/handler/dto -run 'AffiliateRewardConfig|PublicSettingsInjection' -count=1
```

### 步骤 3：实现配置常量和聚合读取

新增：

```go
const (
	AffiliateFirstRechargeThresholdDefault = 0.0
	AffiliateInviterRewardDefault = 10.0
	AffiliateInviteeRewardDefault = 5.0
	AffiliateRewardValidityDaysDefault = 7
	AffiliateRewardValidityDaysMax = 3650
	AffiliateInviterRewardLimitDefault = 0
	AffiliateInviterRewardLimitMax = 1_000_000

	SettingKeyAffiliateRewardValidityDays = "affiliate_reward_validity_days"
	SettingKeyAffiliateInviterRewardLimit = "affiliate_inviter_reward_limit"
)

type AffiliateRewardConfig struct {
	Threshold          float64
	InviterReward      float64
	InviteeReward      float64
	ValidityDays       int
	InviterRewardLimit int
}
```

提供 `SettingService.GetAffiliateRewardConfig(ctx)`，让发奖路径一次取得一致快照。保留旧单字段 getter 给兼容调用，但全部委托给同一校验规则。运行时停止读取 freeze hours；`SystemSettings` 和设置响应可保留旧字段用于兼容读取，但更新逻辑不再需要它。

### 步骤 4：扩展管理员请求和审计字段

给 `UpdateSettingsRequest`、`SystemSettings`、DTO mapper 和 changed-field 追踪加入：

- `affiliate_reward_validity_days`
- `affiliate_inviter_reward_limit`

移除 freeze hours 的新 UI 依赖，但后端允许旧客户端继续发送并忽略/保存旧设置，不影响奖励逻辑。

### 步骤 5：验证

```bash
cd backend && go test ./internal/service ./internal/handler/admin ./internal/handler/dto -run 'Affiliate|PublicSettingsInjection' -count=1
```

### 步骤 6：提交

```bash
git add backend/internal/service/domain_constants.go backend/internal/service/settings_view.go backend/internal/service/setting_service.go backend/internal/service/setting_service_update_test.go backend/internal/service/affiliate_service_test.go backend/internal/handler/admin/setting_handler.go backend/internal/handler/admin/setting_handler_affiliate_test.go backend/internal/handler/dto/settings.go backend/internal/handler/dto/public_settings_injection_schema_test.go
git commit -m "feat: make affiliate rewards configurable"
```

---

## 任务 4：原子实现邀请绑定、立即奖励、首充结算和邀请方上限

**文件：**

- 修改：`backend/internal/service/affiliate_service.go`
- 修改：`backend/internal/service/affiliate_service_test.go`
- 修改：`backend/internal/repository/affiliate_repo.go`
- 修改：`backend/internal/repository/affiliate_repo_test.go`
- 修改：`backend/internal/repository/affiliate_repo_integration_test.go`
- 修改：`backend/internal/service/payment_fulfillment.go`
- 修改：`backend/internal/service/payment_fulfillment_test.go`
- 修改：`backend/internal/service/auth_service_test.go`

### 步骤 1：写行为测试矩阵

服务测试覆盖：

- 开关关闭：邀请码静默忽略，repo 零调用。
- threshold=0：绑定输入为 `immediate`，双方奖励到期为 `grantedAt + 7*24h`。
- threshold>0：绑定为 `pending/first_recharge`，注册不发奖励。
- 邀请方奖励金额为 0：不占获奖名额；受邀方仍发。
- 上限达到：绑定成功、`aff_count` 增加、受邀方奖励发放、邀请方奖励为 0。
- 上限未达到：邀请方计数和奖励同时成功。
- 首充低于阈值：关系变 resolved，不发双方奖励，后续充值不补发。
- 订阅首充无条件达标。
- 活动关闭时首充：关系按事件终结为 resolved 且不发奖；重新开启不补发。

仓储集成测试覆盖 10+ 并发绑定/结算下 `inviter_reward_count` 严格不超过 3，但所有受邀方均有 `affiliate_invitee` 奖励；邀请方仅 3 个 `affiliate_inviter` 批次。

```go
func TestBindInviterAtLimitStillRewardsInvitee(t *testing.T) {
	result, err := repo.BindInviter(ctx, service.AffiliateBindInput{
		InviteeUserID: inviteeID,
		InviterUserID: inviterID,
		RewardMode: service.AffiliateRewardModeImmediate,
		InviterReward: 10,
		InviteeReward: 5,
		ValidityDays: 7,
		InviterRewardLimit: 3,
		GrantedAt: now,
	})
	require.NoError(t, err)
	require.True(t, result.Bound)
	require.False(t, result.InviterRewarded)
	require.True(t, result.InviteeRewarded)
}
```

### 步骤 2：运行测试确认失败

```bash
cd backend && go test ./internal/service ./internal/repository -run 'Affiliate|FirstRecharge' -count=1
```

### 步骤 3：替换仓储契约

在 `affiliate_service.go` 定义并使用：

```go
type AffiliateRewardMode string

const (
	AffiliateRewardModeImmediate AffiliateRewardMode = "immediate"
	AffiliateRewardModeFirstRecharge AffiliateRewardMode = "first_recharge"
)

type AffiliateBindInput struct {
	InviteeUserID       int64
	InviterUserID       int64
	RewardMode          AffiliateRewardMode
	InviterReward       float64
	InviteeReward       float64
	ValidityDays        int
	InviterRewardLimit  int
	GrantedAt           time.Time
}

type AffiliateSettlementInput struct {
	InviteeUserID       int64
	SourceOrderID       int64
	Qualified           bool
	InviterReward       float64
	InviteeReward       float64
	ValidityDays        int
	InviterRewardLimit  int
	GrantedAt           time.Time
}

type AffiliateRewardResult struct {
	Bound            bool
	Resolved         bool
	Qualified        bool
	InviterID        int64
	InviterReward    float64
	InviteeReward    float64
	InviterRewarded  bool
	InviteeRewarded  bool
}
```

`AffiliateRepository` 改为提供 `BindInviter(ctx,input)` 和 `ResolveFirstRecharge(ctx,input)`。仓储通过 `SELECT ... FOR UPDATE` 锁受邀方关系；需要邀请方奖励时再锁邀请方 `user_affiliates` 行，用条件 `limit=0 OR inviter_reward_count<limit` 决定是否占位。使用任务 2 的 `grantRewardCreditTx`，使绑定、状态、计数、双方奖励和 `users.balance` 更新同事务提交。

### 步骤 4：更新服务和支付履约

- `BindInviterByCode` 获取一份 `AffiliateRewardConfig`；threshold=0 传立即模式，否则传首充模式。
- 注册保持现有 fail-open：仓储错误返回给 `AuthService` 后由现有逻辑记录并继续注册；原子事务自身必须回滚。
- `GrantFirstRechargeReward` 只处理 `pending/first_recharge` 关系，并在第一笔成功充值事件上无论是否达标都 resolve。
- 活动关闭时不能在 service 顶部直接 return：第一笔成功充值仍要以 `Qualified=false` 调仓储 resolve 该历史关系，保证以后重开活动不会补发。
- `applyAffiliateRebateForOrder` 保留订单审计 claim；审计 detail 增加双方 `rewarded` 布尔值和 `inviter_limit_reached`。
- 删除新发奖对 `AccrueQuota`、`UpdateBalance`、freeze hours 的依赖；保留旧 transfer/list 读取仅供历史展示，后续任务移除用户转入入口。
- 事务提交后同时失效邀请方和受邀方的 auth/billing 缓存。

### 步骤 5：验证并发与履约幂等

```bash
cd backend && go test ./internal/repository -run 'TestAffiliateRepository.*(Immediate|Limit|Concurrent)' -count=1
cd backend && go test ./internal/service -run 'Test(BindInviter|GrantFirstRechargeReward|Payment.*Affiliate)' -count=1
```

### 步骤 6：提交

```bash
git add backend/internal/service/affiliate_service.go backend/internal/service/affiliate_service_test.go backend/internal/repository/affiliate_repo.go backend/internal/repository/affiliate_repo_test.go backend/internal/repository/affiliate_repo_integration_test.go backend/internal/service/payment_fulfillment.go backend/internal/service/payment_fulfillment_test.go backend/internal/service/auth_service_test.go
git commit -m "feat: grant capped affiliate rewards atomically"
```

---

## 任务 5：把签到领取改成当日 UTC+8 到期的奖励批次

**文件：**

- 修改：`backend/internal/service/check_in.go`
- 修改：`backend/internal/service/check_in_test.go`
- 修改：`backend/internal/repository/check_in_repo.go`
- 修改：`backend/internal/repository/check_in_repo_test.go`
- 修改：`backend/internal/repository/check_in_repo_integration_test.go`

### 步骤 1：写失败测试

覆盖：

- UTC+8 2026-07-16 09:30 领取，`expires_at` 为 2026-07-17 00:00 UTC+8。
- UTC+8 23:59:59 领取仍在下一秒到期。
- claim、奖励批次、grant 事件和余额增加在同一事务；模拟奖励插入失败时 claim 不存在、余额不变。
- 同一签到日期并发领取仍只发一次。
- 来源键严格为 `daily-check-in:<claim_id>`。

```go
func TestDailyCheckInClaimExpiresAtNextUTC8Midnight(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 30, 0, 0, time.UTC)
	_, _, expiresAt := dailyCheckInDate(now)
	require.Equal(t,
		time.Date(2026, 7, 16, 16, 0, 0, 0, time.UTC),
		expiresAt.UTC(),
	)
}
```

### 步骤 2：运行确认失败

```bash
cd backend && go test ./internal/service ./internal/repository -run 'DailyCheckIn.*(Expires|RewardCredit|Atomic)' -count=1
```

### 步骤 3：扩展输入并在 claim 事务发奖

给 `DailyCheckInClaimInput` 加 `ExpiresAt time.Time`。`CheckInService.Claim` 复用 `dailyCheckInDate` 的 `nextResetAt`，转换为 UTC 后传仓储。`CreateClaim` 保存 claim 后取得 ID，再调用 `grantRewardCreditTx`：

```go
grant := service.RewardCreditGrant{
	UserID: input.UserID,
	CreditType: service.RewardCreditDailyCheckIn,
	SourceKey: fmt.Sprintf("daily-check-in:%d", claim.ID),
	Amount: input.RewardAmount,
	GrantedAt: input.ClaimedAt.UTC(),
	ExpiresAt: input.ExpiresAt.UTC(),
}
```

删除原来的 `tx.User.UpdateOneID(...).AddBalance(...)`，由统一奖励助手增加余额并返回 `BalanceAfter`。保持现有缓存失效。

### 步骤 4：验证

```bash
cd backend && go test ./internal/service ./internal/repository ./internal/handler -run 'DailyCheckIn|CheckIn' -count=1
```

### 步骤 5：提交

```bash
git add backend/internal/service/check_in.go backend/internal/service/check_in_test.go backend/internal/repository/check_in_repo.go backend/internal/repository/check_in_repo_test.go backend/internal/repository/check_in_repo_integration_test.go
git commit -m "feat: expire daily check-in rewards at midnight"
```

---

## 任务 6：实现惰性到期、后台清理和用户奖励查询 API

**文件：**

- 新建：`backend/internal/service/reward_credit_service.go`
- 新建：`backend/internal/service/reward_credit_service_test.go`
- 新建：`backend/internal/service/reward_credit_expiry.go`
- 新建：`backend/internal/service/reward_credit_expiry_test.go`
- 修改：`backend/internal/service/wire.go`
- 修改：`backend/cmd/server/wire.go`
- 修改：`backend/internal/handler/user_handler.go`
- 修改：`backend/internal/handler/user_handler_test.go`
- 修改：`backend/internal/server/routes/user.go`
- 新建：`backend/internal/server/routes/reward_credit_routes_test.go`
- 修改：`backend/internal/handler/dto/types.go`
- 修改：`backend/internal/handler/dto/mappers.go`

### 步骤 1：写服务和 HTTP 契约测试

测试：

- cleaner 的 `RunOnce(now)` 调 `ExpireBatch(now,200)`，直到不足一批；按返回的 user ID 逐个失效鉴权与 billing balance 缓存；可 Start/Stop 两次而不 panic。
- `GET /api/v1/user/profile` 永远包含 `reward_balances.daily_check_in` 和 `reward_balances.affiliate`；零值为 amount=0、时间 null、count=0。
- profile 读取先触发 `ExpireUser`，因此过期奖励不会出现在总余额或摘要中。
- `GET /api/v1/user/reward-credits?type=affiliate&status=active&page=1&page_size=20` 只允许当前用户，只接受 affiliate，返回 `{items,total,page,page_size}`。
- 非法 type/status/page 返回 400；page_size 上限 100。

响应契约：

```json
{
  "reward_balances": {
    "daily_check_in": {"amount": 5, "expires_at": "2026-07-16T16:00:00Z"},
    "affiliate": {"amount": 25, "earliest_expires_at": "2026-07-20T08:00:00Z", "credit_count": 3}
  }
}
```

### 步骤 2：运行确认失败

```bash
cd backend && go test ./internal/service ./internal/handler ./internal/server/routes -run 'RewardCredit|RewardBalances' -count=1
```

### 步骤 3：实现 cleaner

`RewardCreditExpiryService` 采用一分钟 ticker，可注入 `now` 和 interval；`RunOnce` 使用带超时 context。服务注入 `APIKeyAuthCacheInvalidator` 和 `BillingCache`，每批事务提交后对 `ExpireBatch` 返回的每个用户做 best-effort 缓存失效，并记录失败日志。`ProvideRewardCreditExpiryService` 创建并启动；`provideCleanup` 增加 Stop 步骤，生成 Wire 前先手工保持 `wire.go` 类型签名一致。

`RewardCreditService` 暴露 `ExpireUserAndGetSummary`、`GetSummary` 和 `ListCredits`，集中执行参数校验、惰性到期与缓存失效；handler 不直接依赖 repository。

### 步骤 4：扩展 profile 和明细接口

给 `UserHandler` 注入封装了 repository 和缓存失效逻辑的 `RewardCreditService`。`buildUserProfileResponse` 在身份摘要之外先执行用户级惰性到期：若实际清除了金额，必须失效缓存并通过 `userService.GetByID` 重新读取 user，之后才构建余额和奖励摘要，禁止把清理前的 `user.Balance` 放进响应。新增 `ListRewardCredits` handler 和路由 `/user/reward-credits`。

这里的 profile `balance` 仍返回清理后的 `users.balance`，不减奖励；奖励摘要只是下钻信息。

### 步骤 5：验证

```bash
cd backend && go test ./internal/service ./internal/handler ./internal/server/routes -run 'RewardCredit|RewardBalances' -count=1
```

### 步骤 6：提交

```bash
git add backend/internal/service/reward_credit_service.go backend/internal/service/reward_credit_service_test.go backend/internal/service/reward_credit_expiry.go backend/internal/service/reward_credit_expiry_test.go backend/internal/service/wire.go backend/cmd/server/wire.go backend/internal/handler/user_handler.go backend/internal/handler/user_handler_test.go backend/internal/server/routes/user.go backend/internal/server/routes/reward_credit_routes_test.go backend/internal/handler/dto/types.go backend/internal/handler/dto/mappers.go
git commit -m "feat: expose and expire reward balances"
```

---

## 任务 7：普通请求按单层完整承担规则扣费

**文件：**

- 修改：`backend/internal/service/usage_billing.go`
- 修改：`backend/internal/repository/usage_billing_repo.go`
- 修改：`backend/internal/repository/usage_billing_repo_unit_test.go`
- 修改：`backend/internal/repository/usage_billing_repo_integration_test.go`
- 修改：`backend/internal/service/gateway_usage_billing.go`
- 新建：`backend/internal/service/gateway_usage_billing_test.go`
- 修改：`backend/internal/service/billing_cache_service.go`
- 修改：`backend/internal/service/billing_cache_service_test.go`
- 修改：`backend/internal/repository/billing_cache.go`
- 修改：`backend/internal/repository/billing_cache_test.go`

### 步骤 1：先写完整扣费矩阵

集成测试至少包含：

| 签到 | 邀请 | 订阅 | 账户自身 | fallback | 费用 | 预期 |
| ---: | ---: | --- | ---: | --- | ---: | --- |
| 10 | 20 | 足 | 100 | off | 8 | 仅签到扣 8 |
| 3 | 10 | 足 | 100 | off | 8 | 签到不动，邀请扣 8 |
| 3 | 5 | 足 | 100 | off | 8 | 奖励不动，仅订阅扣 8 |
| 3 | 5 | 不足 | 100 | on | 8 | 奖励不动，账户扣 8 |
| 3 | 5 | 不足 | 100 | off | 8 | 返回订阅不足，账户不动 |
| 3 | 5 | 无 | 100 | off | 8 | 账户直接扣 8 |
| 0 | 两批 5+5 | 无 | 100 | off | 8 | 邀请层合并扣 8，先到期批先扣 |
| 5 | 5 | 无 | 0 | off | 8 | 不拼层，按既有后计费规则记账户债务 |

并断言：

- 高优先级不足时其批次完全不变。
- 同层多个邀请批次按 `expires_at,id` 消耗。
- `users.balance` 与奖励 remaining 同额减少，事件金额总和等于请求费用。
- `usage_billing_dedup` 重放不二次扣费。
- 到期批次先惰性清零，不能被选择。

### 步骤 2：运行确认当前逻辑失败

```bash
cd backend && go test ./internal/repository -run 'TestUsageBillingRepository.*Reward' -count=1
```

### 步骤 3：扩展结果和缓存快照

新增资金来源：

```go
type UsageFundingSource string

const (
	UsageFundingDailyCheckIn UsageFundingSource = "daily_check_in"
	UsageFundingAffiliate UsageFundingSource = "affiliate"
	UsageFundingSubscription UsageFundingSource = "subscription"
	UsageFundingAccount UsageFundingSource = "account"
)

type UsageBillingApplyResult struct {
	Applied bool
	BillingType int8
	FundingSource UsageFundingSource
	NewBalance *float64
	BalanceOverdrafted bool
	APIKeyQuotaExhausted bool
	QuotaState *AccountQuotaState
}
```

余额缓存快照增加 `DailyRewardBalance`、`AffiliateRewardBalance`、`AccountBalance`，其中 account 为总 balance 减活动奖励。缓存资格判断改为“任一奖励层足额、或订阅足额、或允许使用的账户自身余额足额”，禁止用奖励合计制造跨层资格。

快照同时保存 `NextRewardExpiresAt`。Redis key 的 TTL 取现有 TTL 与 `NextRewardExpiresAt-now` 的较小值；读取到已经越过该时间的旧 payload 必须视为 miss 并从数据库重建，确保到期奖励不会因缓存仍在而通过预请求资格判断。

### 步骤 4：实现事务内层选择

在 `usage_billing_repo.go` 新增：

```go
func tryConsumeRewardLayer(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	types []service.RewardCreditType,
	amount float64,
	eventKey string,
	now time.Time,
) (bool, error)
```

算法：锁定该层所有有效非零批次 → 求和 → 不足则零修改返回 false → 足够则按到期顺序逐批扣并写 consume 事件 → `users.balance` 一次性扣 amount。签到 types 只有 daily；邀请 types 包含 inviter/invitee。

`applyUsageBillingEffects` 的顺序固定为：

1. 尝试签到。
2. 尝试邀请。
3. 若命令带有效订阅，尝试订阅。
4. 仅在无订阅或 `AllowBalanceFallback=true` 时尝试账户自身余额。
5. 已发生实际请求且无层足额时，沿用 `deductUsageBillingBalance` 的无 guard 分支把债务记到账户自身余额；预请求资格仍拒绝明显不足。

账户 guard 使用：

```sql
users.balance - COALESCE(active_daily.amount, 0) - COALESCE(active_affiliate.amount, 0) >= $amount
```

不能直接使用 `users.balance >= amount`。

### 步骤 5：验证普通请求和 fallback 回归

```bash
cd backend && go test ./internal/repository -run 'TestUsageBillingRepository' -count=1
cd backend && go test ./internal/service -run 'Test(GatewayUsageBilling|BillingCache).*' -count=1
```

### 步骤 6：提交

```bash
git add backend/internal/service/usage_billing.go backend/internal/repository/usage_billing_repo.go backend/internal/repository/usage_billing_repo_unit_test.go backend/internal/repository/usage_billing_repo_integration_test.go backend/internal/service/gateway_usage_billing.go backend/internal/service/gateway_usage_billing_test.go backend/internal/service/billing_cache_service.go backend/internal/service/billing_cache_service_test.go backend/internal/repository/billing_cache.go backend/internal/repository/billing_cache_test.go
git commit -m "feat: prioritize reward credit billing layers"
```

---

## 任务 8：让批量图片冻结、结算和释放保留奖励来源与有效期

**文件：**

- 修改：`backend/internal/service/usage_billing.go`
- 修改：`backend/internal/repository/usage_billing_repo.go`
- 修改：`backend/internal/repository/usage_billing_repo_integration_test.go`
- 修改：`backend/internal/service/batch_image_billing_hold.go`
- 新建：`backend/internal/service/batch_image_billing_hold_test.go`
- 修改：`backend/internal/service/batch_image_settlement.go`
- 修改：`backend/internal/service/batch_image_settlement_test.go`

### 步骤 1：写 reserve/capture/release 状态机测试

覆盖：

- hold 由签到层完整承担时，签到 remaining→reserved，balance→frozen，并保存 allocation。
- 签到不足而邀请足够时，签到不动，邀请多批合并冻结。
- 任一单层不足时不跨签到+邀请+账户拼接。
- capture 小于 hold：实际费用写 capture；差额仅在批次仍未过期时回 remaining/balance，已到期差额直接 expire，不返还。
- release 时未到期奖励返还原批次；已到期奖励减少 frozen 并写 expire，不进入 balance。
- account hold 维持现有 users.balance/frozen_balance 行为。
- 同一 hold/capture/release request 重放不重复变动。
- `ActualAmount > HoldAmount` 继续返回现有错误。

### 步骤 2：运行确认失败

```bash
cd backend && go test ./internal/repository ./internal/service -run 'BatchImage.*Reward' -count=1
```

### 步骤 3：扩展 hold 返回值并实现 allocation

给 `BatchImageBalanceHoldResult` 加 `FundingSource UsageFundingSource`。reserve 重用普通扣费的层汇总判断，但把选中批次的 `remaining_amount` 移至 `reserved_amount`，写 reserve 事件和 allocation；用户总额同步从 balance 移至 frozen。

capture/release 必须先按 `hold_key` 锁 allocation 和对应 credit，绝不能根据用户当前余额重新推断来源。按 allocation 的 earliest expiry 顺序 capture；差额逐 allocation 返还或过期。完成后更新 allocation 的 captured/released 数值。

### 步骤 4：验证

```bash
cd backend && go test ./internal/repository -run 'TestUsageBillingRepository.*BatchImage' -count=1
cd backend && go test ./internal/service -run 'TestBatchImage(BillingHold|Settlement)' -count=1
```

### 步骤 5：提交

```bash
git add backend/internal/service/usage_billing.go backend/internal/repository/usage_billing_repo.go backend/internal/repository/usage_billing_repo_integration_test.go backend/internal/service/batch_image_billing_hold.go backend/internal/service/batch_image_billing_hold_test.go backend/internal/service/batch_image_settlement.go backend/internal/service/batch_image_settlement_test.go
git commit -m "feat: preserve reward credits across batch holds"
```

---

## 任务 9：活动关闭时彻底关闭用户邀请入口和邀请码处理

**文件：**

- 修改：`frontend/src/views/auth/RegisterView.vue`
- 新建：`frontend/src/views/auth/__tests__/RegisterViewAffiliate.spec.ts`
- 修改：`frontend/src/router/index.ts`
- 修改：`frontend/src/router/meta.d.ts`
- 修改：`frontend/src/router/__tests__/feature-access.spec.ts`
- 修改：`frontend/src/components/layout/AppSidebar.vue`
- 修改：`frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- 修改：`frontend/src/utils/oauthAffiliate.ts`
- 修改：`frontend/src/utils/__tests__/oauthAffiliate.spec.ts`
- 修改：`backend/internal/service/auth_service_test.go`

### 步骤 1：先写前后端关闭态测试

前端断言：

- 公共设置 `affiliate_enabled=false` 时不渲染 `#aff_code`。
- `/register?aff=ABC123&redirect=%2Fdashboard` 加载设置后调用 `router.replace`，仅删除 `aff/aff_code`，保留 redirect 等参数。
- 关闭态不写 referral sessionStorage，提交 payload 和 email verify 的 `register_data` 都没有 `aff_code`。
- `/affiliate` 带 `requiresAffiliate`，设置明确关闭时重定向 `/dashboard`；设置尚未加载时先加载，网络失败按未知态不误拦。
- sidebar 不显示邀请 Tag/菜单。
- 后端 email、OAuth、email verify 注册即使伪造 aff_code 也不绑定。

### 步骤 2：运行确认失败

```bash
cd frontend && pnpm exec vitest run src/views/auth/__tests__/RegisterViewAffiliate.spec.ts src/router/__tests__/feature-access.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/utils/__tests__/oauthAffiliate.spec.ts
cd backend && go test ./internal/service -run 'TestAuth.*AffiliateDisabled' -count=1
```

### 步骤 3：实现关闭态

- `RegisterView` 增加 `affiliateEnabled`，只有 `settingsLoaded && affiliateEnabled` 才显示输入框和读取/提交 referral。
- 公共设置确认关闭后执行 `clearAffiliateReferralCode()`，清空 `formData.aff_code`，再用 `router.replace({query: rest})` 删除两个邀请参数。
- OAuth 按钮只在活动开启时传 `aff-code`。
- route meta 增加 `requiresAffiliate?: boolean`，`/affiliate` 设置 true；guard 与 payment/risk-control 一样先确保公共设置加载。
- 后端继续以 `AffiliateService.IsEnabled` 为最终防线，不依赖前端。

### 步骤 4：验证

```bash
cd frontend && pnpm exec vitest run src/views/auth/__tests__/RegisterViewAffiliate.spec.ts src/router/__tests__/feature-access.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts src/utils/__tests__/oauthAffiliate.spec.ts
cd backend && go test ./internal/service -run 'TestAuth.*Affiliate' -count=1
```

### 步骤 5：提交

```bash
git add frontend/src/views/auth/RegisterView.vue frontend/src/views/auth/__tests__/RegisterViewAffiliate.spec.ts frontend/src/router/index.ts frontend/src/router/meta.d.ts frontend/src/router/__tests__/feature-access.spec.ts frontend/src/components/layout/AppSidebar.vue frontend/src/components/layout/__tests__/AppSidebar.spec.ts frontend/src/utils/oauthAffiliate.ts frontend/src/utils/__tests__/oauthAffiliate.spec.ts backend/internal/service/auth_service_test.go
git commit -m "feat: disable affiliate entry points with activity toggle"
```

---

## 任务 10：实现可复用的用户余额分解与邀请批次明细 UI

**文件：**

- 新建：`frontend/src/components/user/RewardBalanceBreakdown.vue`
- 新建：`frontend/src/components/user/__tests__/RewardBalanceBreakdown.spec.ts`
- 修改：`frontend/src/components/layout/AppHeader.vue`
- 新建：`frontend/src/components/layout/__tests__/AppHeaderRewardBalance.spec.ts`
- 修改：`frontend/src/components/user/dashboard/UserDashboardStats.vue`
- 修改：`frontend/src/components/user/dashboard/UserDashboardStats.spec.ts`
- 修改：`frontend/src/components/user/dashboard/UserDashboardContent.vue`
- 修改：`frontend/src/types/index.ts`
- 修改：`frontend/src/api/user.ts`
- 修改：`frontend/src/i18n/locales/zh/dashboard.ts`
- 修改：`frontend/src/i18n/locales/en/dashboard.ts`
- 修改：`frontend/src/i18n/locales/zh/common.ts`
- 修改：`frontend/src/i18n/locales/en/common.ts`

### 步骤 1：写组件测试

断言选定的紧凑方案 C：

- 总余额数字仍显示 `user.balance`；不把奖励重复相加。
- 签到 amount>0 时显示 bullet：金额 + 精确到期时间；0 时不显示。
- 邀请 amount>0 时显示 bullet：合计 + 最早到期 + “N 笔明细”；0 时不显示。
- 点击明细才调用 `/user/reward-credits`，展示每批金额、到期时间和“邀请方奖励/受邀方奖励”。
- 加载中、空页、翻页和 API 失败可恢复；重复打开不重复并发请求。
- AppHeader 桌面 hover 和移动 dropdown、Dashboard 余额卡均复用同一组件。

### 步骤 2：运行确认失败

```bash
cd frontend && pnpm exec vitest run src/components/user/__tests__/RewardBalanceBreakdown.spec.ts src/components/layout/__tests__/AppHeaderRewardBalance.spec.ts src/components/user/dashboard/UserDashboardStats.spec.ts
```

### 步骤 3：补类型和 API

在 `User` 增加：

```ts
export interface DailyRewardBalance {
  amount: number
  expires_at: string | null
}

export interface AffiliateRewardBalance {
  amount: number
  earliest_expires_at: string | null
  credit_count: number
}

export interface RewardBalanceSummary {
  daily_check_in: DailyRewardBalance
  affiliate: AffiliateRewardBalance
}

export interface RewardCreditItem {
  id: number
  credit_type: 'affiliate_inviter' | 'affiliate_invitee'
  role_label: 'inviter' | 'invitee'
  original_amount: number
  remaining_amount: number
  granted_at: string
  expires_at: string
}
```

`getRewardCredits(params)` 返回分页结果。组件使用 `formatCurrency/formatDateTime`，所有按钮有 aria-label，明细弹层可键盘关闭。

### 步骤 4：接入 Header 和 Dashboard

- Header 顶部金额不变；hover 卡在现有 available/frozen/total 下方插入 bullets。
- mobile dropdown 余额区同样显示 bullets。
- Dashboard 的 recharge balance 卡在总余额数字下显示 bullets；`UserDashboardContent` 将 `user.reward_balances` 传给 Stats。
- 无奖励时不留空白占位。

### 步骤 5：验证

```bash
cd frontend && pnpm exec vitest run src/components/user/__tests__/RewardBalanceBreakdown.spec.ts src/components/layout/__tests__/AppHeaderRewardBalance.spec.ts src/components/user/dashboard/UserDashboardStats.spec.ts
cd frontend && pnpm run typecheck
```

### 步骤 6：提交

```bash
git add frontend/src/components/user/RewardBalanceBreakdown.vue frontend/src/components/user/__tests__/RewardBalanceBreakdown.spec.ts frontend/src/components/layout/AppHeader.vue frontend/src/components/layout/__tests__/AppHeaderRewardBalance.spec.ts frontend/src/components/user/dashboard/UserDashboardStats.vue frontend/src/components/user/dashboard/UserDashboardStats.spec.ts frontend/src/components/user/dashboard/UserDashboardContent.vue frontend/src/types/index.ts frontend/src/api/user.ts frontend/src/i18n/locales/zh/dashboard.ts frontend/src/i18n/locales/en/dashboard.ts frontend/src/i18n/locales/zh/common.ts frontend/src/i18n/locales/en/common.ts
git commit -m "feat: show expiring reward balance breakdown"
```

---

## 任务 11：重做邀请页文案、上限状态和后台配置 UI

**文件：**

- 修改：`backend/internal/service/affiliate_service.go`
- 修改：`backend/internal/service/affiliate_service_test.go`
- 修改：`backend/internal/handler/user_handler.go`
- 修改：`backend/internal/server/routes/user.go`
- 新建：`backend/internal/server/routes/affiliate_routes_test.go`
- 修改：`frontend/src/views/user/AffiliateView.vue`
- 新建：`frontend/src/views/user/__tests__/AffiliateView.spec.ts`
- 修改：`frontend/src/types/index.ts`
- 修改：`frontend/src/api/user.ts`
- 修改：`frontend/src/views/admin/SettingsView.vue`
- 修改：`frontend/src/views/admin/__tests__/SettingsView.spec.ts`
- 修改：`frontend/src/api/admin/settings.ts`
- 修改：`frontend/src/i18n/locales/zh/dashboard.ts`
- 修改：`frontend/src/i18n/locales/en/dashboard.ts`
- 修改：`frontend/src/i18n/locales/zh/admin/settings.ts`
- 修改：`frontend/src/i18n/locales/en/admin/settings.ts`

### 步骤 1：写接口与页面测试

Affiliate detail 新增断言：

```json
{
  "reward_mode": "immediate",
  "reward_validity_days": 7,
  "inviter_reward_limit": 3,
  "inviter_reward_count": 3,
  "inviter_reward_limit_reached": true
}
```

页面测试：

- threshold=0 显示“邀请立刻获得返现”，明确“你获得 ¥10、好友获得 ¥5”，不出现首充字样。
- threshold>0 显示现有首充门槛文案，包含阈值与双方金额。
- `limit_reached=true` 才显示 `3/3` 已达上限；未达到即使 2/3 也不显示。
- 达到上限仍显示邀请码、复制链接按钮和“好友仍可获得奖励”的解释。
- 不再显示 aff_quota、frozen quota 或手动转入卡片；历史累计可作为只读统计保留。
- 后台表单默认 0/10/5/7/0，阈值为 0 时展示立即奖励说明，大于 0 时展示首充说明。
- 后台没有 freeze hours 输入，新增有效期和每人邀请方获奖次数上限。

### 步骤 2：运行确认失败

```bash
cd backend && go test ./internal/service -run 'TestGetAffiliateDetail.*Reward' -count=1
cd frontend && pnpm exec vitest run src/views/user/__tests__/AffiliateView.spec.ts src/views/admin/__tests__/SettingsView.spec.ts
```

### 步骤 3：扩展 detail 并移除转入入口

`AffiliateDetail` 增加模式、有效期、上限、已获奖次数和 reached。`reached` 只在 limit>0 且 count>=limit 时为 true。用户路由移除 `POST /user/aff/transfer`；服务/仓储的 transfer 方法可保留供历史管理端读取，但用户不再能触发新转入。

邀请人列表中的 `total_rebate` 改为合计新 reward credit 事件和旧 accrue 流水，避免迁移前历史消失；管理端 rebate 列表同样合并新 `grant` 事件与旧记录，并标明来源 `legacy|reward_credit`。

### 步骤 4：更新页面和中英文文案

根据 `first_recharge_threshold === 0` 分支渲染两套文案。后台数字输入严格限制整数/小数语义，payload 保留显式 0。邀请页把奖励余额的到期说明链接到任务 10 的明细组件或同一 API。

### 步骤 5：验证

```bash
cd backend && go test ./internal/service ./internal/handler -run 'Affiliate' -count=1
cd frontend && pnpm exec vitest run src/views/user/__tests__/AffiliateView.spec.ts src/views/admin/__tests__/SettingsView.spec.ts
cd frontend && pnpm run typecheck
```

### 步骤 6：提交

```bash
git add backend/internal/service/affiliate_service.go backend/internal/service/affiliate_service_test.go backend/internal/handler/user_handler.go backend/internal/server/routes/user.go backend/internal/server/routes/affiliate_routes_test.go frontend/src/views/user/AffiliateView.vue frontend/src/views/user/__tests__/AffiliateView.spec.ts frontend/src/types/index.ts frontend/src/api/user.ts frontend/src/views/admin/SettingsView.vue frontend/src/views/admin/__tests__/SettingsView.spec.ts frontend/src/api/admin/settings.ts frontend/src/i18n/locales/zh/dashboard.ts frontend/src/i18n/locales/en/dashboard.ts frontend/src/i18n/locales/zh/admin/settings.ts frontend/src/i18n/locales/en/admin/settings.ts
git commit -m "feat: update affiliate campaign experience"
```

---

## 任务 12：补齐管理端奖励审计与历史兼容回归

**文件：**

- 修改：`backend/internal/service/affiliate_service.go`
- 修改：`backend/internal/repository/affiliate_repo.go`
- 修改：`backend/internal/repository/affiliate_repo_integration_test.go`
- 修改：`backend/internal/handler/admin/affiliate_handler.go`
- 新建：`backend/internal/handler/admin/affiliate_handler_test.go`
- 修改：`frontend/src/api/admin/affiliates.ts`
- 修改：`frontend/src/views/admin/affiliates/AdminAffiliateRebatesView.vue`
- 新建或修改：`frontend/src/views/admin/affiliates/__tests__/AdminAffiliateRebatesView.spec.ts`

### 步骤 1：写合并列表测试

创建一个旧 `user_affiliate_ledger accrue` 和两个新 reward grant 事件，断言管理端返利列表：

- 三条都返回，不重复。
- 旧记录保留订单、邀请双方、金额和时间。
- 新记录区分 inviter/invitee 奖励类型、到期时间、剩余金额。
- 搜索、日期范围、排序和分页在合并结果上正确。
- 旧 transfer history 仍可查询；用户侧无 transfer endpoint。
- overview 的 rewarded count 使用迁移后的 `inviter_reward_count`，不再从当前 aff_quota 推断。

### 步骤 2：运行确认失败

```bash
cd backend && go test ./internal/repository ./internal/handler/admin -run 'Affiliate.*(Rebate|History|Overview)' -count=1
cd frontend && pnpm exec vitest run src/views/admin/affiliates/__tests__/AdminAffiliateRebatesView.spec.ts
```

### 步骤 3：实现统一审计读取

仓储用 `UNION ALL` 归一化旧流水和新 grant 事件的列，再在外层应用 filter/order/limit。不要改写或删除旧流水。DTO 明确增加 `record_source`、`reward_role`、`expires_at`、`remaining_amount` 可空字段，旧客户端仍能读取原字段。

### 步骤 4：验证并提交

```bash
cd backend && go test ./internal/repository ./internal/handler/admin -run 'Affiliate' -count=1
cd frontend && pnpm exec vitest run src/views/admin/affiliates/__tests__/AdminAffiliateRebatesView.spec.ts
git add backend/internal/service/affiliate_service.go backend/internal/repository/affiliate_repo.go backend/internal/repository/affiliate_repo_integration_test.go backend/internal/handler/admin/affiliate_handler.go backend/internal/handler/admin/affiliate_handler_test.go frontend/src/api/admin/affiliates.ts frontend/src/views/admin/affiliates/AdminAffiliateRebatesView.vue frontend/src/views/admin/affiliates/__tests__/AdminAffiliateRebatesView.spec.ts
git commit -m "feat: unify affiliate reward audit history"
```

---

## 任务 13：生成依赖注入代码并完成跨模块回归

**文件：**

- 修改（生成）：`backend/cmd/server/wire_gen.go`
- 修改（生成）：`backend/cmd/server/wire_gen_test.go`
- 修改：`backend/cmd/server/wire.go`
- 修改：`Makefile`（把新增关键前端测试加入 `FRONTEND_CRITICAL_VITEST`）

### 步骤 1：先更新 Wire 清理测试

在 `wire_gen_test.go` 的 cleanup 依赖中加入 `RewardCreditExpiryService`，断言 cleanup 调用不会 panic。确认 `provideCleanup` 的参数顺序在 `wire.go` 和生成文件一致。

### 步骤 2：生成代码

```bash
cd backend && go generate ./cmd/server
```

检查生成 diff 只包含新增 reward repo/service/handler 依赖和 cleaner cleanup，不接受无关 provider 重排。

### 步骤 3：运行分层回归

```bash
cd backend && go test ./migrations ./internal/repository ./internal/service ./internal/handler ./internal/server/routes ./cmd/server -count=1
cd frontend && pnpm exec vitest run \
  src/views/auth/__tests__/RegisterViewAffiliate.spec.ts \
  src/views/user/__tests__/AffiliateView.spec.ts \
  src/components/user/__tests__/RewardBalanceBreakdown.spec.ts \
  src/components/layout/__tests__/AppHeaderRewardBalance.spec.ts \
  src/components/user/dashboard/UserDashboardStats.spec.ts \
  src/views/admin/__tests__/SettingsView.spec.ts \
  src/views/admin/affiliates/__tests__/AdminAffiliateRebatesView.spec.ts \
  src/router/__tests__/feature-access.spec.ts
```

### 步骤 4：提交

```bash
git add backend/cmd/server/wire.go backend/cmd/server/wire_gen.go backend/cmd/server/wire_gen_test.go Makefile
git commit -m "chore: wire reward credit services"
```

---

## 任务 14：最终验证、知识图更新和交付审计

**文件：**

- 修改（自动）：`graphify-out/**`
- 检查：本计划涉及的全部文件

### 步骤 1：运行生成一致性和全量后端检查

```bash
make check-generate
cd backend && go test ./... -count=1
cd backend && golangci-lint run --timeout=30m ./...
```

预期：全部通过、lint 为 0 issues。

### 步骤 2：运行全量前端检查

```bash
cd frontend && pnpm run lint:check
cd frontend && pnpm run typecheck
make test-frontend-critical
make test-frontend-webchat
```

预期：全部命令退出码为 0。记录既有测试警告，但不能新增未处理异常或失败。

### 步骤 3：进行资金不变量专项审计

用集成测试或数据库断言逐项确认：

```text
users.balance
= account permanent balance
 + active daily remaining
 + active affiliate remaining

users.frozen_balance
= account holds
 + reward allocation reserved amounts
```

并确认：

- grant/consume/expire/reserve/capture/release 每条事件金额与用户总额变化一致。
- 请求不跨层拆账，邀请层内部只按到期时间拆批次。
- 有订阅时账户 fallback 严格服从用户开关；无订阅直接使用账户余额。
- 上限 3、9 个受邀人场景中受邀方 9 次奖励、邀请方恰好 3 次奖励。
- 关闭活动时 UI、路由、注册 payload、后端绑定四层均关闭。
- 迁移不会把旧返利转两次，也不会补发旧邀请。

### 步骤 4：更新 Graphify

按仓库 `AGENTS.md` 执行：

```bash
graphify update .
graphify query "affiliate reward credit daily check-in billing fallback batch hold profile balance" --budget 6000
```

确认新节点能串起 migration → reward repository → affiliate/check-in grants → usage billing → profile/API → Vue UI。保存有效查询结果：

```bash
graphify save-result \
  --question "How do expiring affiliate and check-in rewards flow from grant to billing and UI?" \
  --answer "Rewards are granted into user_reward_credits with users.balance updated atomically; usage billing selects one complete layer in priority order; batch holds persist allocations; expiry is enforced lazily and by a worker; profile summaries and reward detail APIs feed the header, dashboard, and affiliate page." \
  --type architecture \
  --outcome useful
```

### 步骤 5：检查工作树与提交最终图更新

```bash
git status --short
git diff --check
git log --oneline --decorate -15
```

Graphify 的增量文件按仓库现有跟踪策略提交；不把临时依赖、日志或 `.superpowers/` 会话文件加入提交。

```bash
git add graphify-out
git commit -m "docs: refresh reward credit knowledge graph"
```

### 步骤 6：按完成分支流程交付

使用 `superpowers:verification-before-completion` 复核上述命令的最新输出，再使用 `superpowers:finishing-a-development-branch` 向用户提供合并、PR 或保留分支选项。未经用户明确要求，不改动生产环境、不直接推送 `dev`。
