# 邀请返现改造实现计划：固定金额 · 仅首充达标 · 双方各得

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把"按比例、每笔返、仅邀请方得"的邀请返现，改成"固定金额、仅被邀请方首充达标、邀请双方各得"，且阈值与两侧奖励额均管理员可配。

**Architecture:** 核心结算仍在 `PaymentService.applyAffiliateRebateForOrder`（订单履约事务内，六个 fulfillment 路径共用）。首充判定新增 `PaymentService` 内的订单计数辅助；发放逻辑重写 `AffiliateService`：邀请方进 `aff_quota`（沿用 `AccrueQuota`+冻结），被邀请方经 `userRepo.UpdateBalance` 直接进账户余额（同一事务，失败整体回滚）。兑换码路径不再触发。配置层新增 3 项、移除旧 3 项（比例/有效期/单人上限）。

**Tech Stack:** Go（ent ORM、testify/require、手写 fake repo）、Vue3 + TS（Vitest、Tailwind 自定义 `input`/`Toggle` 组件）。测试命令：后端 `cd backend && go test -tags=unit ./internal/service/...`；前端 `cd frontend && npm run test:run` 与 `npm run typecheck`。

**设计依据：** `docs/superpowers/specs/2026-07-05-invite-first-recharge-reward-design.md`

---

## 关键设计决策（实现前必读）

- **发放去向**：邀请方 → `aff_quota`（`AccrueQuota`，冻结期沿用 `affiliate_rebate_freeze_hours`）；被邀请方 → 账户余额（`userRepo.UpdateBalance`，同事务）。
- **首充判定**：被邀请人此前**成功充值订单数**（`order_type ∈ {balance, subscription}` 且 `status ∈ {PAID, RECHARGING, COMPLETED}`，排除当前订单 ID）为 0 → 首充。计数在履约事务内进行（此时当前订单已置 `RECHARGING`，故须排除自身）。
- **达标**：订阅无条件达标；余额充值到账额 `≥ affiliate_first_recharge_threshold`。
- **仅首充**：非首充直接跳过；首充不达标不发且未来不再发（"仅第一笔"天然保证永久失格）。
- **依赖注入**：`AffiliateService` 新增 `userRepo UserRepository` 字段（用于给被邀请方加余额），构造函数与 `wire_gen.go` 同步。
- **旧配置**：移除 `affiliate_rebate_rate` / `affiliate_rebate_duration_days` / `affiliate_rebate_per_invitee_cap` 的读写与前端界面；保留 `affiliate_enabled`、`affiliate_rebate_freeze_hours`。数据库列 `aff_rebate_rate_percent` 不删（停用）。
- **展示层**：`AffiliateDetail.EffectiveRebateRatePercent` 与 `AffiliateUserOverview.RebateRatePercent` 改为展示新奖励配置（阈值/两侧奖励额），移除百分比语义。
- **兑换码**：`redeem_service.go` 摘除 affiliate 触发；`ContextSkipRedeemAffiliate` / `ctxKeySkipRedeemAffiliate` 保留（无害）。

## 文件清单

**后端（`backend/`）**
- 修改 `internal/service/domain_constants.go` — 新增/移除常量与 SettingKey
- 修改 `internal/service/setting_service.go` — 新增 getter、save/parse/init，移除旧 3 项读写
- 修改 `internal/service/settings_view.go` — `SystemSettings` 字段增删
- 修改 `internal/handler/dto/settings.go` — DTO 字段增删
- 修改 `internal/handler/admin/setting_handler.go` — 请求字段与 diff 增删
- 修改 `internal/service/affiliate_service.go` — 重写发放逻辑、依赖注入、展示层
- 修改 `internal/service/payment_fulfillment.go` — 首充计数、调用改造
- 修改 `internal/service/redeem_service.go` — 摘除 affiliate 触发
- 修改 `cmd/server/wire_gen.go` — 构造函数注入 `userRepository`
- 测试 `internal/service/affiliate_service_test.go`、`internal/service/payment_fulfillment_test.go`

**前端（`frontend/`）**
- 修改 `src/views/auth/RegisterView.vue` — 新增选填邀请码输入框
- 修改 `src/views/admin/SettingsView.vue` — 替换配置输入框
- 修改 settings TS 类型（含 `affiliate_rebate_rate` 的 interface）
- 修改 `src/views/user/AffiliateView.vue` — 文案与展示字段
- 测试 `src/views/auth/__tests__/`（新增，若无目录则建）

---

## Phase 1 — 后端配置层（新增 3 项 / 移除旧 3 项）

### Task 1: 新增/调整配置常量与 SettingKey

**Files:**
- Modify: `backend/internal/service/domain_constants.go:25-36`（常量块）
- Modify: `backend/internal/service/domain_constants.go:169-173`（SettingKey）

- [ ] **Step 1: 替换常量块（行 25-36）**

把 `// Affiliate rebate settings` 常量块整体替换为：

```go
// Affiliate rebate settings
const (
	AffiliateEnabledDefault           = false // 邀请返利总开关默认关闭
	AffiliateRebateFreezeHoursDefault = 0     // 0 = 不冻结（向后兼容）
	AffiliateRebateFreezeHoursMax     = 720   // 最大 30 天

	// 首充固定奖励模型（替代旧的按比例返现）
	AffiliateFirstRechargeThresholdDefault = 20.0 // 首充达标阈值（USD，订阅无条件达标）
	AffiliateInviterRewardDefault          = 5.0  // 邀请方奖励（进返利余额 aff_quota）
	AffiliateInviteeRewardDefault          = 5.0  // 被邀请方奖励（进账户余额）
	AffiliateRewardMax                     = 1000000.0 // 奖励/阈值上限，防误配
)
```

> 说明：删除了 `AffiliateRebateRateDefault/Min/Max`、`AffiliateRebateDurationDaysDefault/Max`、`AffiliateRebatePerInviteeCapDefault`。后续任务会清掉所有引用，编译错误是预期的中间状态。

- [ ] **Step 2: 替换 SettingKey（行 169-173）**

把这 5 行替换为：

```go
	SettingKeyAffiliateEnabled                 = "affiliate_enabled"                    // 邀请返利功能总开关
	SettingKeyAffiliateRebateFreezeHours       = "affiliate_rebate_freeze_hours"        // 返利冻结期（小时，0=不冻结）
	SettingKeyAffiliateFirstRechargeThreshold  = "affiliate_first_recharge_threshold"   // 首充达标阈值（USD）
	SettingKeyAffiliateInviterReward           = "affiliate_inviter_reward"             // 邀请方奖励（进返利余额）
	SettingKeyAffiliateInviteeReward           = "affiliate_invitee_reward"             // 被邀请方奖励（进账户余额）
```

- [ ] **Step 3: 编译（预期失败）**

Run: `cd backend && go build ./internal/service/ 2>&1 | head -30`
Expected: 出现引用已删除常量/key 的报错（`AffiliateRebateRateDefault` 等 undefined）。这是预期中间态，后续任务修复。

---

### Task 2: SettingService — 新增 getter、移除旧 getter

**Files:**
- Modify: `backend/internal/service/setting_service.go`（getter 区 ~2796-2857、clamp ~3889、save ~2140-2161、parse ~3378-3395、init ~3166-3169、SystemSettings 映射 ~950）

- [ ] **Step 1: 删除旧 getter 与 clamp**

删除以下函数整体：
- `GetAffiliateRebateRatePercent`（约 2796-2809）
- `GetAffiliateRebateDurationDays`（约 2828-2843）
- `GetAffiliateRebatePerInviteeCap`（约 2845-2857）
- `clampAffiliateRebateRate`（约 3889-3900）

保留 `GetAffiliateRebateFreezeHours`（2811-2826）与 `IsAffiliateEnabled`（2788-2794）不动。

- [ ] **Step 2: 新增 3 个 getter**

在 `GetAffiliateRebateFreezeHours` 之后插入：

```go
// clampAffiliateReward 把奖励/阈值裁剪到 [0, AffiliateRewardMax]，非法值回退默认。
func clampAffiliateReward(value, def float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return def
	}
	if value > AffiliateRewardMax {
		return AffiliateRewardMax
	}
	return value
}

// GetAffiliateFirstRechargeThreshold 返回首充达标阈值（USD）。
func (s *SettingService) GetAffiliateFirstRechargeThreshold(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateFirstRechargeThreshold)
	if err != nil {
		return AffiliateFirstRechargeThresholdDefault
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return AffiliateFirstRechargeThresholdDefault
	}
	return clampAffiliateReward(v, AffiliateFirstRechargeThresholdDefault)
}

// GetAffiliateInviterReward 返回邀请方奖励（进返利余额）。
func (s *SettingService) GetAffiliateInviterReward(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateInviterReward)
	if err != nil {
		return AffiliateInviterRewardDefault
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return AffiliateInviterRewardDefault
	}
	return clampAffiliateReward(v, AffiliateInviterRewardDefault)
}

// GetAffiliateInviteeReward 返回被邀请方奖励（进账户余额）。
func (s *SettingService) GetAffiliateInviteeReward(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateInviteeReward)
	if err != nil {
		return AffiliateInviteeRewardDefault
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return AffiliateInviteeRewardDefault
	}
	return clampAffiliateReward(v, AffiliateInviteeRewardDefault)
}
```

- [ ] **Step 3: 改 save 写入块（约 2140-2161）**

把 `settings.AffiliateRebateRate = clampAffiliateRebateRate(...)` 起至 `updates[SettingKeyAffiliateRebatePerInviteeCap] = ...` 的整段（含 rate/duration/perInviteeCap 三段），替换为（保留 freezeHours 段，新增三项）：

```go
	if settings.AffiliateRebateFreezeHours < 0 {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursDefault
	}
	if settings.AffiliateRebateFreezeHours > AffiliateRebateFreezeHoursMax {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursMax
	}
	updates[SettingKeyAffiliateRebateFreezeHours] = strconv.Itoa(settings.AffiliateRebateFreezeHours)
	settings.AffiliateFirstRechargeThreshold = clampAffiliateReward(settings.AffiliateFirstRechargeThreshold, AffiliateFirstRechargeThresholdDefault)
	updates[SettingKeyAffiliateFirstRechargeThreshold] = strconv.FormatFloat(settings.AffiliateFirstRechargeThreshold, 'f', 8, 64)
	settings.AffiliateInviterReward = clampAffiliateReward(settings.AffiliateInviterReward, AffiliateInviterRewardDefault)
	updates[SettingKeyAffiliateInviterReward] = strconv.FormatFloat(settings.AffiliateInviterReward, 'f', 8, 64)
	settings.AffiliateInviteeReward = clampAffiliateReward(settings.AffiliateInviteeReward, AffiliateInviteeRewardDefault)
	updates[SettingKeyAffiliateInviteeReward] = strconv.FormatFloat(settings.AffiliateInviteeReward, 'f', 8, 64)
```

- [ ] **Step 4: 改 parse 读取块（约 3378-3395）**

把读取 rate/freeze/duration/perInviteeCap 的四段，替换为（保留 freeze，新增三项）：

```go
	if freezeHours, err := strconv.Atoi(settings[SettingKeyAffiliateRebateFreezeHours]); err == nil && freezeHours >= 0 {
		result.AffiliateRebateFreezeHours = freezeHours
	}
	if v, err := strconv.ParseFloat(settings[SettingKeyAffiliateFirstRechargeThreshold], 64); err == nil {
		result.AffiliateFirstRechargeThreshold = clampAffiliateReward(v, AffiliateFirstRechargeThresholdDefault)
	} else {
		result.AffiliateFirstRechargeThreshold = AffiliateFirstRechargeThresholdDefault
	}
	if v, err := strconv.ParseFloat(settings[SettingKeyAffiliateInviterReward], 64); err == nil {
		result.AffiliateInviterReward = clampAffiliateReward(v, AffiliateInviterRewardDefault)
	} else {
		result.AffiliateInviterReward = AffiliateInviterRewardDefault
	}
	if v, err := strconv.ParseFloat(settings[SettingKeyAffiliateInviteeReward], 64); err == nil {
		result.AffiliateInviteeReward = clampAffiliateReward(v, AffiliateInviteeRewardDefault)
	} else {
		result.AffiliateInviteeReward = AffiliateInviteeRewardDefault
	}
```

- [ ] **Step 5: 改 init 默认值（约 3166-3169）**

把 rate/freeze/duration/perInviteeCap 四行默认值，替换为：

```go
		SettingKeyAffiliateRebateFreezeHours:                strconv.Itoa(AffiliateRebateFreezeHoursDefault),
		SettingKeyAffiliateFirstRechargeThreshold:           strconv.FormatFloat(AffiliateFirstRechargeThresholdDefault, 'f', 2, 64),
		SettingKeyAffiliateInviterReward:                    strconv.FormatFloat(AffiliateInviterRewardDefault, 'f', 2, 64),
		SettingKeyAffiliateInviteeReward:                    strconv.FormatFloat(AffiliateInviteeRewardDefault, 'f', 2, 64),
```

- [ ] **Step 6: 检查行 950 的 SystemSettings 映射**

Run: `cd backend && grep -n "AffiliateRebateRate\|AffiliateRebateDurationDays\|AffiliateRebatePerInviteeCap" internal/service/setting_service.go`
若行 950 附近有 `AffiliateRebateRate: settings[...]` 这类映射赋值，一并删除 rate/duration/cap，新增三项（`AffiliateFirstRechargeThreshold`/`AffiliateInviterReward`/`AffiliateInviteeReward`）。参照该处现有写法照抄字段名。

---

### Task 3: SystemSettings 与 DTO 字段增删

**Files:**
- Modify: `backend/internal/service/settings_view.go:150-154`
- Modify: `backend/internal/handler/dto/settings.go:147-150`
- Modify: `backend/internal/handler/admin/setting_handler.go:526-529`、diff `~2578-2589`

- [ ] **Step 1: `settings_view.go` 行 150-154**

替换为：

```go
	AffiliateEnabled                bool
	AffiliateRebateFreezeHours      int
	AffiliateFirstRechargeThreshold float64
	AffiliateInviterReward          float64
	AffiliateInviteeReward          float64
```

- [ ] **Step 2: `dto/settings.go` 行 147-150**

替换为：

```go
	AffiliateRebateFreezeHours      int     `json:"affiliate_rebate_freeze_hours"`
	AffiliateFirstRechargeThreshold float64 `json:"affiliate_first_recharge_threshold"`
	AffiliateInviterReward          float64 `json:"affiliate_inviter_reward"`
	AffiliateInviteeReward          float64 `json:"affiliate_invitee_reward"`
```

> 注意：原行 147 `AffiliateRebateRate` 被移除。检查该 struct（`SystemSettings` DTO）里是否还有 `AffiliateRebateFreezeHours` 重复定义，避免重复字段。

- [ ] **Step 3: `setting_handler.go` UpdateSettingsRequest（行 526-529）**

把 `AffiliateRebateRate/FreezeHours/DurationDays/PerInviteeCap` 四行替换为：

```go
	AffiliateRebateFreezeHours                *int                              `json:"affiliate_rebate_freeze_hours"`
	AffiliateFirstRechargeThreshold           *float64                          `json:"affiliate_first_recharge_threshold"`
	AffiliateInviterReward                    *float64                          `json:"affiliate_inviter_reward"`
	AffiliateInviteeReward                    *float64                          `json:"affiliate_invitee_reward"`
```

- [ ] **Step 4: `setting_handler.go` 请求→settings 赋值**

Run: `cd backend && grep -n "req.AffiliateRebateRate\|req.AffiliateRebateFreezeHours\|req.AffiliateRebateDurationDays\|req.AffiliateRebatePerInviteeCap" internal/handler/admin/setting_handler.go`
把命中的 rate/duration/perInviteeCap 赋值分支删除，新增三项赋值（照 freezeHours 的 `if req.X != nil { settings.X = *req.X }` 写法）：

```go
	if req.AffiliateFirstRechargeThreshold != nil {
		settings.AffiliateFirstRechargeThreshold = *req.AffiliateFirstRechargeThreshold
	}
	if req.AffiliateInviterReward != nil {
		settings.AffiliateInviterReward = *req.AffiliateInviterReward
	}
	if req.AffiliateInviteeReward != nil {
		settings.AffiliateInviteeReward = *req.AffiliateInviteeReward
	}
```

- [ ] **Step 5: `setting_handler.go` diffSettings（行 2578-2589）**

把 rate/freeze/duration/perInviteeCap 四段 diff，替换为：

```go
	if before.AffiliateRebateFreezeHours != after.AffiliateRebateFreezeHours {
		changed = append(changed, "affiliate_rebate_freeze_hours")
	}
	if before.AffiliateFirstRechargeThreshold != after.AffiliateFirstRechargeThreshold {
		changed = append(changed, "affiliate_first_recharge_threshold")
	}
	if before.AffiliateInviterReward != after.AffiliateInviterReward {
		changed = append(changed, "affiliate_inviter_reward")
	}
	if before.AffiliateInviteeReward != after.AffiliateInviteeReward {
		changed = append(changed, "affiliate_invitee_reward")
	}
```

- [ ] **Step 6: 全量搜残留引用**

Run: `cd backend && grep -rn "AffiliateRebateRate\|AffiliateRebateDurationDays\|AffiliateRebatePerInviteeCap\|SettingKeyAffiliateRebateRate\|SettingKeyAffiliateRebateDurationDays\|SettingKeyAffiliateRebatePerInviteeCap" internal/ | grep -v "_test.go"`
Expected: 除 `affiliate_service.go`（下个 Phase 处理）外，无其它命中。若有遗漏，按上述模式清理。

---

## Phase 2 — 后端核心发放逻辑

### Task 4: AffiliateService 注入 userRepo

**Files:**
- Modify: `backend/internal/service/affiliate_service.go:207-221`
- Modify: `backend/cmd/server/wire_gen.go:79`

- [ ] **Step 1: struct 加字段（行 207-212）**

把 struct 定义替换为：

```go
type AffiliateService struct {
	repo                 AffiliateRepository
	settingService       *SettingService
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCacheService  *BillingCacheService
	userRepo             UserRepository
}
```

- [ ] **Step 2: 构造函数加参数（行 214-221）**

把 `NewAffiliateService` 替换为：

```go
func NewAffiliateService(repo AffiliateRepository, settingService *SettingService, authCacheInvalidator APIKeyAuthCacheInvalidator, billingCacheService *BillingCacheService, userRepo UserRepository) *AffiliateService {
	return &AffiliateService{
		repo:                 repo,
		settingService:       settingService,
		authCacheInvalidator: authCacheInvalidator,
		billingCacheService:  billingCacheService,
		userRepo:             userRepo,
	}
}
```

- [ ] **Step 3: wire_gen.go 行 79 补参数**

把该行改为（`userRepository` 在行 47 已构造，可用）：

```go
	affiliateService := service.NewAffiliateService(affiliateRepository, settingService, apiKeyAuthCacheInvalidator, billingCacheService, userRepository)
```

---

### Task 5: 重写发放核心（AccrueInviteRebateForOrder → 首充固定奖励）

**Files:**
- Modify: `backend/internal/service/affiliate_service.go:314-409`

- [ ] **Step 1: 定义结果结构体 + 重写函数**

删除旧 `AccrueInviteRebate`（314-316）、`AccrueInviteRebateForOrder`（318-387）、`resolveRebateRatePercent`（389-400）、`globalRebateRatePercent`（402-409），整体替换为：

```go
// AffiliateRewardResult 描述一次首充奖励结算的结果，供调用方写审计与失效缓存。
type AffiliateRewardResult struct {
	Qualified     bool    // 是否达标并已发放
	InviterID     int64   // 邀请方（达标时 > 0）
	InviterReward float64 // 邀请方所得（进 aff_quota）
	InviteeReward float64 // 被邀请方所得（进账户余额）
}

// GrantFirstRechargeReward 结算被邀请人首充奖励。
// 前置：调用方已确认这是被邀请人的首充订单（isFirstRecharge）。
// isSubscription=true 时无条件达标；否则要求 baseRechargeAmount >= 阈值。
// 达标后：邀请方 +InviterReward 进 aff_quota（冻结期沿用设置）；
// 被邀请方 +InviteeReward 进账户余额（同事务，经 userRepo）。
// 必须在履约事务上下文内调用（ctx 携带 tx），以保证两侧发放原子性。
func (s *AffiliateService) GrantFirstRechargeReward(ctx context.Context, inviteeUserID int64, baseRechargeAmount float64, isSubscription, isFirstRecharge bool, sourceOrderID *int64) (AffiliateRewardResult, error) {
	var res AffiliateRewardResult
	if s == nil || s.repo == nil {
		return res, nil
	}
	if inviteeUserID <= 0 || !isFirstRecharge {
		return res, nil
	}
	if !s.IsEnabled(ctx) {
		return res, nil
	}
	// 达标判定：订阅无条件达标；余额充值需 >= 阈值
	if !isSubscription {
		if baseRechargeAmount <= 0 || math.IsNaN(baseRechargeAmount) || math.IsInf(baseRechargeAmount, 0) {
			return res, nil
		}
		threshold := AffiliateFirstRechargeThresholdDefault
		if s.settingService != nil {
			threshold = s.settingService.GetAffiliateFirstRechargeThreshold(ctx)
		}
		if baseRechargeAmount < threshold {
			return res, nil
		}
	}

	inviteeSummary, err := s.repo.EnsureUserAffiliate(ctx, inviteeUserID)
	if err != nil {
		return res, err
	}
	if inviteeSummary.InviterID == nil || *inviteeSummary.InviterID <= 0 {
		return res, nil
	}
	inviterID := *inviteeSummary.InviterID

	inviterReward := AffiliateInviterRewardDefault
	inviteeReward := AffiliateInviteeRewardDefault
	freezeHours := AffiliateRebateFreezeHoursDefault
	if s.settingService != nil {
		inviterReward = s.settingService.GetAffiliateInviterReward(ctx)
		inviteeReward = s.settingService.GetAffiliateInviteeReward(ctx)
		freezeHours = s.settingService.GetAffiliateRebateFreezeHours(ctx)
	}
	inviterReward = roundTo(inviterReward, 8)
	inviteeReward = roundTo(inviteeReward, 8)

	// 邀请方奖励 → aff_quota（冻结期沿用）
	if inviterReward > 0 {
		applied, err := s.repo.AccrueQuota(ctx, inviterID, inviteeUserID, inviterReward, freezeHours, sourceOrderID)
		if err != nil {
			return res, err
		}
		if applied {
			res.InviterReward = inviterReward
		}
	}
	// 被邀请方奖励 → 账户余额（同事务）
	if inviteeReward > 0 {
		if s.userRepo == nil {
			return res, errors.New("affiliate: userRepo unavailable for invitee reward")
		}
		if err := s.userRepo.UpdateBalance(ctx, inviteeUserID, inviteeReward); err != nil {
			return res, fmt.Errorf("credit invitee reward: %w", err)
		}
		res.InviteeReward = inviteeReward
	}

	res.Qualified = res.InviterReward > 0 || res.InviteeReward > 0
	res.InviterID = inviterID
	return res, nil
}

// InvalidateUserBalanceCache 供履约层在事务提交后失效被邀请方余额缓存。
func (s *AffiliateService) InvalidateUserBalanceCache(ctx context.Context, userID int64) {
	if s == nil || s.billingCacheService == nil || userID <= 0 {
		return
	}
	if err := s.billingCacheService.InvalidateUserBalance(ctx, userID); err != nil {
		logger.LegacyPrintf("service.affiliate", "invalidate invitee balance cache failed: user_id=%d err=%v", userID, err)
	}
}
```

> 需要 `fmt`、`errors`、`math` import——`affiliate_service.go` 已 import 全部三者（见文件头 3-12 行）。

- [ ] **Step 2: 修展示层引用（编译修复）**

`resolveRebateRatePercent`/`globalRebateRatePercent` 已删，两处引用需改：

行 264（`AffiliateDetail` 组装）——把 `EffectiveRebateRatePercent: s.resolveRebateRatePercent(ctx, summary),` 改为读取奖励配置。先改 `AffiliateDetail` struct（行 82-95）：删除 `EffectiveRebateRatePercent float64` 字段，新增：

```go
	// 首充奖励展示（固定金额模型）
	FirstRechargeThreshold float64 `json:"first_recharge_threshold"`
	InviterReward          float64 `json:"inviter_reward"`
	InviteeReward          float64 `json:"invitee_reward"`
```

再把行 264 那行替换为：

```go
		FirstRechargeThreshold: s.affiliateRewardConfig(ctx).threshold,
		InviterReward:          s.affiliateRewardConfig(ctx).inviter,
		InviteeReward:          s.affiliateRewardConfig(ctx).invitee,
```

在文件内新增辅助（放 `GrantFirstRechargeReward` 之后）：

```go
type affiliateRewardConfigValues struct {
	threshold float64
	inviter   float64
	invitee   float64
}

func (s *AffiliateService) affiliateRewardConfig(ctx context.Context) affiliateRewardConfigValues {
	v := affiliateRewardConfigValues{
		threshold: AffiliateFirstRechargeThresholdDefault,
		inviter:   AffiliateInviterRewardDefault,
		invitee:   AffiliateInviteeRewardDefault,
	}
	if s.settingService != nil {
		v.threshold = s.settingService.GetAffiliateFirstRechargeThreshold(ctx)
		v.inviter = s.settingService.GetAffiliateInviterReward(ctx)
		v.invitee = s.settingService.GetAffiliateInviteeReward(ctx)
	}
	return v
}
```

- [ ] **Step 3: 修 overview 引用（行 595-610）**

`AffiliateUserOverview` 的 `RebateRatePercent` 依赖 `globalRebateRatePercent`（已删）。改 `AffiliateUserOverview` struct：删除 `RebateRatePercent float64` 与 `RebateRateCustom bool` 两字段，新增：

```go
	FirstRechargeThreshold float64 `json:"first_recharge_threshold"`
	InviterReward          float64 `json:"inviter_reward"`
	InviteeReward          float64 `json:"invitee_reward"`
```

把 `GetAffiliateUserOverview`（行 595-610）里 `if overview != nil { ... clampAffiliateRebateRate ... }` 整块替换为：

```go
	if overview != nil {
		cfg := s.affiliateRewardConfig(ctx)
		overview.FirstRechargeThreshold = cfg.threshold
		overview.InviterReward = cfg.inviter
		overview.InviteeReward = cfg.invitee
	}
```

> 注意：`repo.GetAffiliateUserOverview` 仍会给已删字段赋值（repo 层未改），删字段后 repo 里对应赋值会编译错。执行时若报错，把 repo 中对 `RebateRatePercent`/`RebateRateCustom` 的赋值一并删除（搜 `RebateRateCustom` 定位）。

- [ ] **Step 4: 编译**

Run: `cd backend && go build ./internal/service/ 2>&1 | head -30`
Expected: `affiliate_service.go` 相关报错清除；可能剩 `payment_fulfillment.go`（下个 Task 处理）的 `AccrueInviteRebateForOrder` undefined。

---

### Task 6: PaymentService — 首充计数 + 调用改造

**Files:**
- Modify: `backend/internal/service/payment_fulfillment.go:607-682`（`applyAffiliateRebateForOrder`）
- Modify: `backend/internal/service/payment_fulfillment.go:684-694`（`affiliateRebateBaseAmount`）

- [ ] **Step 1: 新增首充计数辅助**

在 `affiliateRebateBaseAmount`（684）之前插入。`payment_fulfillment.go` 已 import `ent/paymentorder`（行 18）与 `internal/payment`：

```go
// countPriorSucceededRecharges 统计用户在 excludeOrderID 之外的成功充值订单数
// （order_type ∈ {balance, subscription}，status ∈ {PAID, RECHARGING, COMPLETED}）。
// 返回 0 表示当前订单为首充。必须在履约事务上下文内调用（ctx 携带 tx）。
func (s *PaymentService) countPriorSucceededRecharges(ctx context.Context, userID, excludeOrderID int64) (int, error) {
	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}
	n, err := client.PaymentOrder.Query().
		Where(
			paymentorder.UserIDEQ(userID),
			paymentorder.IDNEQ(excludeOrderID),
			paymentorder.OrderTypeIn(payment.OrderTypeBalance, payment.OrderTypeSubscription),
			paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted),
		).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count prior recharges: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 2: 重写 applyAffiliateRebateForOrder（行 607-682）**

整体替换为：

```go
func (s *PaymentService) applyAffiliateRebateForOrder(ctx context.Context, o *dbent.PaymentOrder) error {
	baseAmount := affiliateRebateBaseAmount(o)
	if o == nil || baseAmount <= 0 {
		return nil
	}
	if s.affiliateService == nil {
		return nil
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": fmt.Sprintf("begin affiliate rebate tx: %v", err),
		})
		return fmt.Errorf("begin affiliate rebate tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := s.tryClaimAffiliateRebateAudit(txCtx, tx.Client(), o.ID, baseAmount)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("claim affiliate rebate audit: %w", err)
	}
	if !claimed {
		return nil
	}

	priorCount, err := s.countPriorSucceededRecharges(txCtx, o.UserID, o.ID)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("count prior recharges: %w", err)
	}
	isFirstRecharge := priorCount == 0
	isSubscription := o.OrderType == payment.OrderTypeSubscription

	sourceOrderID := o.ID
	result, err := s.affiliateService.GrantFirstRechargeReward(txCtx, o.UserID, baseAmount, isSubscription, isFirstRecharge, &sourceOrderID)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("grant first recharge reward: %w", err)
	}

	if !result.Qualified {
		reason := "not first recharge or below threshold or no inviter"
		if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_SKIPPED", map[string]any{
			"baseAmount":      baseAmount,
			"isFirstRecharge": isFirstRecharge,
			"isSubscription":  isSubscription,
			"reason":          reason,
		}); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": err.Error()})
			return fmt.Errorf("update affiliate rebate skipped audit: %w", err)
		}
		if err := tx.Commit(); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": fmt.Sprintf("commit: %v", err)})
			return fmt.Errorf("commit affiliate rebate tx: %w", err)
		}
		return nil
	}

	if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_APPLIED", map[string]any{
		"baseAmount":     baseAmount,
		"inviterID":      result.InviterID,
		"inviterReward":  result.InviterReward,
		"inviteeReward":  result.InviteeReward,
		"isSubscription": isSubscription,
	}); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": err.Error()})
		return fmt.Errorf("update affiliate rebate applied audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{"error": fmt.Sprintf("commit: %v", err)})
		return fmt.Errorf("commit affiliate rebate tx: %w", err)
	}

	// 提交后失效被邀请方余额缓存（best-effort）
	if result.InviteeReward > 0 {
		s.affiliateService.InvalidateUserBalanceCache(ctx, o.UserID)
	}
	return nil
}
```

> `affiliateRebateBaseAmount`（684-694）保持不变——余额/订阅返回 `o.Amount`，其它返回 0，天然把非充值订单排除在触发之外。

- [ ] **Step 3: 编译**

Run: `cd backend && go build ./... 2>&1 | head -30`
Expected: 仅剩 `redeem_service.go` 对 `AccrueInviteRebate` 的引用报错（下个 Task 处理），或已全绿。

---

### Task 7: 摘除兑换码触发

**Files:**
- Modify: `backend/internal/service/redeem_service.go:555`、`698-716`

- [ ] **Step 1: 删除调用点（行 555）**

删除该行：`s.tryAccrueAffiliateRebateForRedeem(ctx, userID, redeemCode.Value)`

- [ ] **Step 2: 删除函数（行 698-716）**

删除整个 `tryAccrueAffiliateRebateForRedeem` 函数。

> `ContextSkipRedeemAffiliate`/`ctxKeySkipRedeemAffiliate`（行 33-40）**保留**——`payment_fulfillment.go` 的 `doBalance` 仍调用它，删除会引发编译错误。它现在是无害 no-op。

- [ ] **Step 3: 检查 redeemService.affiliateService 字段是否变为未使用**

Run: `cd backend && grep -n "affiliateService" internal/service/redeem_service.go`
若仅剩 struct 字段定义与构造赋值、无其它使用：保留字段（wire 仍注入），Go 不会因结构体未使用字段报错。无需改动。

- [ ] **Step 4: 全量编译**

Run: `cd backend && go build ./... 2>&1 | head -30`
Expected: 无输出（全绿）。

---

## Phase 3 — 后端测试

> 测试沿用现有风格：`testify/require` + 手写 fake repo；`SettingService` 用内存 `settingRepo` stub 注入配置值（参见 `affiliate_service_test.go` 与 `payment_fulfillment_test.go` 现有构造）。

### Task 8: AffiliateService.GrantFirstRechargeReward 单元测试

**Files:**
- Modify/Create: `backend/internal/service/affiliate_service_test.go`

- [ ] **Step 1: 补 fake repo/userRepo 能力**

确认测试用的 fake `AffiliateRepository` 已记录 `AccrueQuota` 调用（现有 `accrueCalls` 结构）。新增一个 fake `UserRepository`，只需实现被调用的 `UpdateBalance`（其余方法可 panic/空实现，或复用现有 fake）：

```go
type affiliateFakeUserRepo struct {
	UserRepository // 嵌入接口，未实现方法留空；测试只用 UpdateBalance
	balanceCalls []struct {
		userID int64
		amount float64
	}
	failUserID int64 // 若命中则返回错误，用于回滚测试
}

func (r *affiliateFakeUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	if r.failUserID != 0 && id == r.failUserID {
		return fmt.Errorf("forced balance failure for %d", id)
	}
	r.balanceCalls = append(r.balanceCalls, struct {
		userID int64
		amount float64
	}{id, amount})
	return nil
}
```

- [ ] **Step 2: 构造 helper**

```go
func newRewardTestService(t *testing.T, repo AffiliateRepository, userRepo UserRepository, settings map[string]string) *AffiliateService {
	t.Helper()
	base := map[string]string{
		SettingKeyAffiliateEnabled:                "true",
		SettingKeyAffiliateFirstRechargeThreshold: "20",
		SettingKeyAffiliateInviterReward:          "5",
		SettingKeyAffiliateInviteeReward:          "5",
		SettingKeyAffiliateRebateFreezeHours:      "0",
	}
	for k, v := range settings {
		base[k] = v
	}
	ss := NewSettingService(&paymentFulfillmentSettingRepoStub{values: base}, nil)
	return NewAffiliateService(repo, ss, nil, nil, userRepo)
}
```

> 若 `paymentFulfillmentSettingRepoStub` 定义在 `payment_fulfillment_test.go` 同包内可直接复用；否则照它定义一个等价的内存 `settingRepo` stub（实现 `GetValue(ctx, key) (string, error)`）。

- [ ] **Step 3: 写测试用例**

```go
func TestGrantFirstRechargeReward(t *testing.T) {
	ctx := context.Background()
	const inviteeID, inviterID = int64(100), int64(200)
	orderID := int64(9)

	newRepo := func() *fakeAffiliateRepo {
		return &fakeAffiliateRepo{
			summaries: map[int64]*AffiliateSummary{
				inviteeID: {UserID: inviteeID, InviterID: ptrInt64(inviterID)},
				inviterID: {UserID: inviterID},
			},
			accrueApplied: true,
		}
	}

	t.Run("首充余额达标_双方各得", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Equal(t, 5.0, res.InviterReward)
		require.Equal(t, 5.0, res.InviteeReward)
		require.Len(t, repo.accrueCalls, 1)
		require.Equal(t, inviterID, repo.accrueCalls[0].inviterID)
		require.Equal(t, 5.0, repo.accrueCalls[0].amount)
		require.Len(t, ur.balanceCalls, 1)
		require.Equal(t, inviteeID, ur.balanceCalls[0].userID)
		require.Equal(t, 5.0, ur.balanceCalls[0].amount)
	})

	t.Run("首充余额不达标_不发", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 19.99, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, repo.accrueCalls)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("订阅无条件达标", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 1, true, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Len(t, ur.balanceCalls, 1)
	})

	t.Run("非首充_跳过", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 100, false, false, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("无邀请人_跳过", func(t *testing.T) {
		repo := newRepo()
		repo.summaries[inviteeID].InviterID = nil
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 50, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
	})

	t.Run("阈值边界_等于阈值达标", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, nil)
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
	})

	t.Run("被邀请方奖励为0_只发邀请方", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, map[string]string{SettingKeyAffiliateInviteeReward: "0"})
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.NoError(t, err)
		require.True(t, res.Qualified)
		require.Equal(t, 5.0, res.InviterReward)
		require.Equal(t, 0.0, res.InviteeReward)
		require.Empty(t, ur.balanceCalls)
	})

	t.Run("总开关关闭_跳过", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{}
		svc := newRewardTestService(t, repo, ur, map[string]string{SettingKeyAffiliateEnabled: "false"})
		res, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 50, false, true, &orderID)
		require.NoError(t, err)
		require.False(t, res.Qualified)
	})

	t.Run("被邀请方加余额失败_返回错误", func(t *testing.T) {
		repo := newRepo()
		ur := &affiliateFakeUserRepo{failUserID: inviteeID}
		svc := newRewardTestService(t, repo, ur, nil)
		_, err := svc.GrantFirstRechargeReward(ctx, inviteeID, 20, false, true, &orderID)
		require.Error(t, err)
	})
}
```

> `fakeAffiliateRepo`、`ptrInt64`、`accrueCalls` 结构：对齐现有测试文件里的命名。若现有 fake 结构字段名不同（如 `summaries`/`accrueApplied`），按实际字段调整；核心是能返回被邀请人 summary（含 InviterID）并记录 `AccrueQuota` 调用。

- [ ] **Step 4: 运行**

Run: `cd backend && go test -tags=unit -run TestGrantFirstRechargeReward ./internal/service/ -v 2>&1 | tail -30`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/service/ internal/handler/ cmd/server/wire_gen.go && git commit -m "feat(affiliate): 首充固定奖励模型（后端逻辑+配置）"
```

---

### Task 9: 首充计数集成测试（payment fulfillment）

**Files:**
- Modify: `backend/internal/service/payment_fulfillment_test.go`

- [ ] **Step 1: 复用现有履约测试脚手架**

参照现有 `TestExecuteSubscriptionFulfillment`/balance 履约测试（用真实 ent client + 内存 settingRepo stub 构造 `PaymentService`）。新增用例覆盖：
- 被邀请人首笔余额充值达标 → `AFFILIATE_REBATE_APPLIED` 审计存在，`accrueCalls` 记录邀请方奖励，被邀请方 balance 增加
- 被邀请人已有一笔 COMPLETED 充值 → 第二笔触发 `AFFILIATE_REBATE_SKIPPED`（`countPriorSucceededRecharges` > 0）

- [ ] **Step 2: 写"非首充跳过"用例（核心新逻辑）**

```go
func TestApplyAffiliateRebate_SecondRechargeSkipped(t *testing.T) {
	ctx := context.Background()
	client, cleanup := newTestEntClient(t) // 复用现有测试 helper 名
	defer cleanup()

	// arrange：用户已有一笔 COMPLETED 余额充值
	user := createTestUser(t, client)
	bindInviter(t, client, user.ID /* inviterID */, 999)
	createCompletedBalanceOrder(t, client, user.ID, 50) // 历史首充
	secondOrder := createPaidBalanceOrder(t, client, user.ID, 50) // 当前第二笔

	svc := newPaymentServiceForTest(t, client /* 注入 affiliateService 等 */)

	// act
	err := svc.applyAffiliateRebateForOrder(ctx, secondOrder)
	require.NoError(t, err)

	// assert：第二笔应被判为非首充 → SKIPPED
	skipped, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(secondOrder.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_SKIPPED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, skipped.Detail, `"isFirstRecharge":false`)
}
```

> helper 名（`newTestEntClient`/`createTestUser`/`createCompletedBalanceOrder`/`bindInviter`/`newPaymentServiceForTest`）对齐现有测试文件里的实际命名；若不存在则按现有 fulfillment 测试的建单方式内联构造（用 `client.PaymentOrder.Create()...SetStatus(OrderStatusCompleted)`）。

- [ ] **Step 3: 运行**

Run: `cd backend && go test -tags=unit -run TestApplyAffiliateRebate ./internal/service/ -v 2>&1 | tail -30`
Expected: PASS。

- [ ] **Step 4: 全量后端测试 + lint**

Run: `cd backend && go test -tags=unit ./internal/service/... 2>&1 | tail -20 && golangci-lint run ./internal/service/... 2>&1 | tail -20`
Expected: 测试全绿；lint 无新增告警。

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/service/ && git commit -m "test(affiliate): 首充判定与发放测试"
```

---

## Phase 4 — 前端

### Task 10: 注册页新增选填邀请码输入框

**Files:**
- Modify: `frontend/src/views/auth/RegisterView.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`（及其它 locale 文件）

- [ ] **Step 1: 加输入框（模板）**

在 email 输入框（约行 40-55）那个 `<div>` 之后、OAuth 组件之前，插入（照 email 字段的 `input` class 风格）：

```vue
<div>
  <label for="aff_code" class="input-label">
    {{ t('auth.affiliateCodeLabel') }}
  </label>
  <input
    id="aff_code"
    v-model="formData.aff_code"
    type="text"
    autocomplete="off"
    :disabled="registrationActionDisabled"
    class="input"
    :placeholder="t('auth.affiliateCodePlaceholder')"
  />
  <p class="mt-1 text-xs text-gray-400">
    {{ t('auth.affiliateCodeHint') }}
  </p>
</div>
```

> `formData.aff_code`（行 400）与提交透传（行 859-891）已存在，URL 回填 `syncAffiliateReferralCode`（行 440+）也会写入同一字段，手动输入与自动回填共用，无需改脚本逻辑。

- [ ] **Step 2: 加 i18n key**

在 `zh.ts` 的 `auth` 段加：

```ts
affiliateCodeLabel: '邀请码（选填）',
affiliateCodePlaceholder: '有邀请码可在此填写',
affiliateCodeHint: '填写好友的邀请码，首充达标后双方都可获得奖励',
```

其它 locale（如 `en.ts`）加对应英文键，避免 i18n 缺键告警。

- [ ] **Step 3: 类型检查**

Run: `cd frontend && npm run typecheck 2>&1 | tail -20`
Expected: 无新增错误。

---

### Task 11: 后台设置界面替换配置项

**Files:**
- Modify: `frontend/src/views/admin/SettingsView.vue`（模板 5877-5947、form 定义、loadSettings 回填 ~8838、saveSettings payload ~9219）
- Modify: settings TS 类型文件（含 `affiliate_rebate_rate` 的 interface）
- Modify: i18n locale

- [ ] **Step 1: 替换模板（行 5877-5947）**

把 `v-if="form.affiliate_enabled"` 内的四个输入框（rebateRate/freezeHours/durationDays/perInviteeCap）整块替换为（保留 freezeHours，替换其余三个为新项）：

```vue
<div v-if="form.affiliate_enabled" class="space-y-6">
  <div>
    <label class="input-label">{{ t('admin.settings.features.affiliate.firstRechargeThreshold') }}</label>
    <input v-model.number="form.affiliate_first_recharge_threshold" type="number" step="0.01" min="0" class="input" placeholder="20" />
    <p class="mt-1 text-xs text-gray-400">{{ t('admin.settings.features.affiliate.firstRechargeThresholdHint') }}</p>
  </div>
  <div>
    <label class="input-label">{{ t('admin.settings.features.affiliate.inviterReward') }}</label>
    <input v-model.number="form.affiliate_inviter_reward" type="number" step="0.01" min="0" class="input" placeholder="5" />
    <p class="mt-1 text-xs text-gray-400">{{ t('admin.settings.features.affiliate.inviterRewardHint') }}</p>
  </div>
  <div>
    <label class="input-label">{{ t('admin.settings.features.affiliate.inviteeReward') }}</label>
    <input v-model.number="form.affiliate_invitee_reward" type="number" step="0.01" min="0" class="input" placeholder="5" />
    <p class="mt-1 text-xs text-gray-400">{{ t('admin.settings.features.affiliate.inviteeRewardHint') }}</p>
  </div>
  <div>
    <label class="input-label">{{ t('admin.settings.features.affiliate.freezeHours') }}</label>
    <input v-model.number="form.affiliate_rebate_freeze_hours" type="number" step="1" min="0" max="720" class="input" />
    <p class="mt-1 text-xs text-gray-400">{{ t('admin.settings.features.affiliate.freezeHoursDesc') }}</p>
  </div>
</div>
```

- [ ] **Step 2: form 定义增删**

Run: `cd frontend && grep -n "affiliate_rebate_rate\|affiliate_rebate_duration_days\|affiliate_rebate_per_invitee_cap\|affiliate_rebate_freeze_hours\|affiliate_first_recharge\|affiliate_inviter_reward\|affiliate_invitee_reward" src/views/admin/SettingsView.vue`
在 `form` 响应式对象定义处，删除 `affiliate_rebate_rate`/`affiliate_rebate_duration_days`/`affiliate_rebate_per_invitee_cap` 三个初始值，新增：

```ts
affiliate_first_recharge_threshold: 20,
affiliate_inviter_reward: 5,
affiliate_invitee_reward: 5,
```

保留 `affiliate_rebate_freeze_hours`。

- [ ] **Step 3: loadSettings 回填（约 8838-8845）**

删除对 `form.affiliate_rebate_rate`/`duration_days`/`per_invitee_cap` 的赋值，新增（照现有逐字段赋值风格）：

```ts
form.affiliate_first_recharge_threshold = data.affiliate_first_recharge_threshold ?? 20
form.affiliate_inviter_reward = data.affiliate_inviter_reward ?? 5
form.affiliate_invitee_reward = data.affiliate_invitee_reward ?? 5
```

- [ ] **Step 4: saveSettings payload（约 9219-9225）**

删除 payload 中 rate/duration/perInviteeCap 字段与相关裁剪，新增：

```ts
affiliate_first_recharge_threshold: form.affiliate_first_recharge_threshold,
affiliate_inviter_reward: form.affiliate_inviter_reward,
affiliate_invitee_reward: form.affiliate_invitee_reward,
```

- [ ] **Step 5: TS 类型 interface**

在含 `affiliate_rebate_rate` 的 interface（settings 类型文件，用 grep 定位）里，删除 `affiliate_rebate_rate`/`affiliate_rebate_duration_days`/`affiliate_rebate_per_invitee_cap`，新增：

```ts
affiliate_first_recharge_threshold?: number
affiliate_inviter_reward?: number
affiliate_invitee_reward?: number
```

- [ ] **Step 6: 专属用户管理移除比例项**

Run: `cd frontend && grep -n "aff_rebate_rate_percent\|rebateRate\|专属比例\|rebate_rate" src/views/admin/SettingsView.vue src/api/admin/affiliates.ts`
移除专属用户列表/编辑弹窗中的"专属返利比例"输入与列展示（保留邀请码管理）。`affiliates.ts` 里 `aff_rebate_rate_percent` 相关 API 函数**保留**（后端端点仍在，属未使用，删前端调用即可）。

- [ ] **Step 7: i18n key**

在 locale 的 `admin.settings.features.affiliate` 段新增 `firstRechargeThreshold`/`firstRechargeThresholdHint`/`inviterReward`/`inviterRewardHint`/`inviteeReward`/`inviteeRewardHint`；移除 `rebateRate`/`rebateRateHint`/`durationDays`/`durationDaysDesc`/`perInviteeCap` 等已删项（或留着无害）。中英文都要加。

- [ ] **Step 8: 类型检查**

Run: `cd frontend && npm run typecheck 2>&1 | tail -20`
Expected: 无新增错误（残留 `affiliate_rebate_rate` 引用会在此暴露，逐一清理）。

---

### Task 12: 用户邀请页文案与展示

**Files:**
- Modify: `frontend/src/views/user/AffiliateView.vue`

- [ ] **Step 1: 定位比例展示**

Run: `cd frontend && grep -n "effective_rebate_rate_percent\|rebate_rate\|返利比例\|rebateRate\|%" src/views/user/AffiliateView.vue`

- [ ] **Step 2: 替换为固定奖励文案**

把展示 `effective_rebate_rate_percent`（后端已改为返回 `first_recharge_threshold`/`inviter_reward`/`invitee_reward`）的地方，改为读取新字段，展示形如：

```vue
<p>{{ t('affiliate.rewardIntro', { threshold: detail.first_recharge_threshold, inviter: detail.inviter_reward, invitee: detail.invitee_reward }) }}</p>
```

i18n（zh）：

```ts
rewardIntro: '邀请好友注册，其首充满 {threshold} 后，你获得 {inviter}、好友获得 {invitee}（仅首充一次）',
```

- [ ] **Step 3: 类型检查 + 前端测试**

Run: `cd frontend && npm run typecheck 2>&1 | tail -10 && npm run test:run 2>&1 | tail -20`
Expected: 无新增类型错误；测试通过。

- [ ] **Step 4: Commit**

```bash
cd frontend && git add src/ && git commit -m "feat(affiliate): 前端首充奖励配置+注册邀请码输入框"
```

---

## Phase 5 — 验证与收尾

### Task 13: 全量验证

- [ ] **Step 1: 后端**

Run: `cd backend && go build ./... && go test -tags=unit ./internal/service/... 2>&1 | tail -20`
Expected: 编译通过；测试全绿。

- [ ] **Step 2: 后端 lint**

Run: `cd backend && golangci-lint run ./... 2>&1 | tail -20`
Expected: 无新增告警。

- [ ] **Step 3: 前端 build**

Run: `cd frontend && npm run build 2>&1 | tail -20`
Expected: 构建成功（含 vue-tsc 类型检查）。

- [ ] **Step 4: 残留引用扫描**

Run: `cd backend && grep -rn "AffiliateRebateRate\|RebateRatePercent\|AccrueInviteRebate\b\|GetAffiliateRebateDurationDays\|GetAffiliateRebatePerInviteeCap" internal/ --include="*.go" | grep -v "_test.go"; cd ../frontend && grep -rn "affiliate_rebate_rate\|affiliate_rebate_duration_days\|affiliate_rebate_per_invitee_cap\|effective_rebate_rate_percent" src/`
Expected: 无输出（或仅剩有意保留的 repo 层/未使用 API）。

### Task 14: 手工冒烟（可选，需本地起服务）

- [ ] **Step 1:** 后台设置 → 功能设置：开启邀请返利，设阈值 20 / 邀请方 5 / 被邀请方 5，保存后刷新回显正确
- [ ] **Step 2:** 注册页出现"邀请码（选填）"输入框；带 `?aff=CODE` 访问时自动回填
- [ ] **Step 3:** 用邀请码注册新用户 → 首充 20 → 邀请方 `aff_quota` +5、被邀请方账户余额 +5；查 `payment_audit_logs` 有 `AFFILIATE_REBATE_APPLIED`
- [ ] **Step 4:** 同一被邀请人再充 → 无新增奖励（`AFFILIATE_REBATE_SKIPPED`）

### Task 15: 更新设计文档状态 + PR

- [ ] **Step 1:** 把 spec 顶部 `状态：待评审` 改为 `状态：已实现`
- [ ] **Step 2:** Commit 文档：`git add docs/superpowers && git commit -m "docs: 标记邀请返现改造为已实现"`
- [ ] **Step 3:** 推送分支并建 PR（base: `release`），PR 描述含：改造摘要、行为变更（每笔比例→首充固定、双方各得）、被移除配置项、DB 列 `aff_rebate_rate_percent` 停用说明、手工验证结果

---

## 附：已知限制（记录在案）
- 首充退款不回收已发奖励
- 无注册时间卡点，历史被邀请用户首充也算数
- DB 列 `aff_rebate_rate_percent` 与相关 admin 端点保留但停用（前端不再暴露）
- 被邀请方奖励经 `UpdateBalance` 入账会计入其 `total_recharged`（金额小，影响可忽略）

