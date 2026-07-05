# 邀请返现改造设计：固定金额 · 仅首充达标 · 双方各得

- 日期：2026-07-05
- 状态：已实现
- 涉及模块：affiliate（邀请返利）、payment（支付履约）、redeem（余额）、auth（注册）、admin settings、frontend（注册页 / 后台设置 / 用户邀请页）

## 1. 背景

当前系统已有一套完整的邀请返利功能（总开关默认关闭）。现状行为：

- 被邀请人**每一笔**充值订单完成时，按**百分比**（默认 20%）返现给邀请方
- 返现进邀请方的**返利余额 `aff_quota`**（需手动转账到账户余额才可用）
- 被邀请方无任何奖励
- 支持全局比例、专属用户比例、冻结期、有效期、单人上限等配置
- 兑换码激活也会触发返现

## 2. 目标（改造后行为）

把"按比例、每笔返、仅邀请方得"彻底替换为"**固定金额、仅首充达标、邀请双方各得**"的拉新奖励。

一句话：**被邀请的新用户首次充值达标后，邀请方和被邀请方各得一笔固定奖励，仅此一次。**

## 3. 需求与决策（已与需求方确认）

| # | 决策点 | 结论 |
|---|--------|------|
| 1 | 改造形态 | **硬替换**，去掉每笔按比例返现逻辑 |
| 2 | 触发口径 | **仅被邀请人的第一笔充值订单**。非首充一律跳过 |
| 3 | 达标门槛 | 余额充值到账额 `≥ 阈值`（默认 20，USD）；订阅订单**无条件达标**（不看金额） |
| 4 | 首充不达标 | 谁都不发；且因只认第一笔，该用户**之后永久无资格**（自动成立，无需额外标记） |
| 5 | 奖励对象与去向 | 邀请方 `+inviterReward` → **返利余额 `aff_quota`**（沿用冻结/转账）；被邀请方 `+inviteeReward` → **账户余额**（直接可用） |
| 6 | 金额单位 | 到账余额口径（USD），与现有返现基数 `o.Amount` 一致 |
| 7 | 可配置性 | 阈值、邀请方奖励、被邀请方奖励**均管理员可配** |
| 8 | 旧配置 | 移除比例/有效期/单人上限/专属比例 4 项（界面 + 后端读取）；保留总开关与冻结期 |
| 9 | 兑换码路径 | 兑换码激活**不再触发**邀请奖励，仅真实付款充值订单（余额 + 订阅）触发 |
| 10 | 注册邀请码 | 注册页新增**选填**邀请码输入框（保留 URL 参数自动回填） |
| 11 | "新用户"边界 | 依赖"邀请人仅注册时绑定"这层天然保证，**不加注册时间卡点**；历史被邀请用户首充也算数 |
| 12 | 退款回收 | 首充退款**不回收**已发奖励（已知行为，本期不做） |

## 4. 关键事实（代码调研结论）

- 两个返现入口汇聚到核心函数 `AffiliateService.AccrueInviteRebateForOrder`（`backend/internal/service/affiliate_service.go:318`）
  - 支付充值：`payment_fulfillment.go:638`（订单履约事务内）
  - 兑换码：`redeem_service.go:708 → AccrueInviteRebate`（本次将摘除）
- 幂等由 `tryClaimAffiliateRebateAudit`（按 order ID）保证每单只结算一次（`payment_fulfillment.go:626`）
- `affiliateRebateBaseAmount`（`payment_fulfillment.go:684`）已区分 `OrderTypeBalance` / `OrderTypeSubscription`
- 账户余额（`user.balance`）与返利余额（`aff_quota`）是两个独立钱包；账户余额直接用于计费扣费
- 到账余额通过"生成 `RedeemTypeBalance` 兑换码并核销"实现（`doBalance`，`payment_fulfillment.go`）；管理员调余额亦走类似审计化路径
- 邀请人绑定仅发生在注册时（`auth_service.go:240`、`bindOAuthAffiliate`），且 `BindInviterByCode` 对已绑定用户直接返回（`affiliate_service.go:289`），老用户无法事后补绑

## 5. 行为规则（改造后）

被邀请方**每次充值订单完成时**，在履约事务内执行判定：

1. 总开关 `affiliate_enabled` 为 false → 跳过
2. 被邀请方无绑定邀请人（`inviter_id` 为空）→ 跳过
3. 判定是否首充：查该用户此前是否已有成功充值订单（状态 `Paid`/`Recharging`/`Completed`，排除当前订单）
   - 非首充 → 跳过（这自动保证"首充不达标后永久无资格"）
   - 是首充 → 进入达标判定
4. 达标判定（仅对首充这一笔）：
   - `order_type = subscription` → **无条件达标**
   - `order_type = balance` 且到账额 `≥ affiliate_first_recharge_threshold` → 达标
   - 否则 → 不发放（且因非首充规则，未来也不再发）
5. 达标后一次性发放（同一事务）：
   - 邀请方 `AccrueQuota(+affiliate_inviter_reward)` → `aff_quota`，冻结期沿用 `affiliate_rebate_freeze_hours`
   - 被邀请方 `+affiliate_invitee_reward` → 账户余额（生成并核销余额兑换码，带审计）

### 状态机（首充这一笔）

```
首充订单完成
  ├─ 无邀请人 ────────────────→ 不发
  ├─ 订阅订单 ───────────────→ 双方各得奖励
  ├─ 余额充值 ≥ 阈值 ─────────→ 双方各得奖励
  └─ 余额充值 < 阈值 ─────────→ 不发（且永久失格）
```

## 6. 配置项

### 保留
- `affiliate_enabled`：总开关，默认 false
- `affiliate_rebate_freeze_hours`：冻结期（小时），仅作用于邀请方进 `aff_quota` 的奖励，默认 0

### 新增

| Key | 含义 | 默认 | 约束 |
|-----|------|------|------|
| `affiliate_first_recharge_threshold` | 首充达标阈值（USD，订阅不受限） | 20 | ≥ 0 |
| `affiliate_inviter_reward` | 邀请方奖励额（进返利余额） | 5 | ≥ 0 |
| `affiliate_invitee_reward` | 被邀请方奖励额（进账户余额） | 5 | ≥ 0 |

奖励额拆成两个 key，便于后续差异化调整（当前默认均为 5）。任一奖励额配为 0 时该侧不发放。

### 移除（后端读取 + DTO + 前端界面）
- `affiliate_rebate_rate`（全局返利比例）
- `affiliate_rebate_duration_days`（返利有效期）
- `affiliate_rebate_per_invitee_cap`（单人上限）
- 用户专属比例 `aff_rebate_rate_percent`（后台专属用户管理里的比例项）

数据库列 `aff_rebate_rate_percent` 暂保留（不读取，避免迁移风险），仅停止使用。

## 7. 技术实现落点

### 后端 · 核心逻辑（`affiliate_service.go`）
- 重写 `AccrueInviteRebateForOrder(ctx, inviteeUserID, baseAmount, orderType, sourceOrderID)`：
  - 新增入参 `orderType`（用于订阅无条件达标判定）
  - 首充判定调用新增 repo 方法（见下）
  - 达标后：邀请方 `AccrueQuota`；被邀请方走余额兑换码核销
  - 返回结构可扩展为同时反映"是否发放、发给谁、各多少"，供审计日志记录
- 删除 `resolveRebateRatePercent` / 专属比例、`globalRebateRatePercent` 相关逻辑
- 删除 `AccrueInviteRebate`（兑换码入口）；`redeem_service.go` 的 `tryAccrueAffiliateRebateForRedeem` 及其调用点(`redeem_service.go:555`)摘除

### 后端 · 首充判定（新增 repo 能力）
- 新增 `CountSucceededRechargeOrders(ctx, userID, excludeOrderID)`，状态取 `Paid/Recharging/Completed`；返回 0 即为首充
- 查询走 `payment_orders`，已有 `user_id`、`status` 索引

### 后端 · 被邀请方加余额
- 复用 `doBalance` 的"生成 `RedeemTypeBalance` 兑换码 → 核销"模式，写审计日志（新增审计动作，如 `AFFILIATE_INVITEE_REWARD_APPLIED`）
- 在履约事务上下文内执行，与邀请方发放同事务，失败一起回滚

### 后端 · 配置（`domain_constants.go` + `setting_service.go`）
- 新增 3 个 SettingKey + 常量默认值 + getter（解析失败/越界回退默认，clamp ≥ 0）
- 移除旧 4 项的 getter/DTO 字段/`setting_handler.go` 写入分支
- 更新 `settings_view.go`、`dto/settings.go` 中的 affiliate 相关字段

### 后端 · 履约调用点（`payment_fulfillment.go`）
- `applyAffiliateRebateForOrder` 把 `order_type` 传入核心函数
- 幂等 `tryClaimAffiliateRebateAudit` 不变；审计 `AFFILIATE_REBATE_APPLIED/SKIPPED/FAILED` 语义保留并补充"被邀请方奖励"记录

### 前端 · 注册页（`RegisterView.vue`）
- 新增选填邀请码 `<input v-model="formData.aff_code">`（放在表单合适位置，加占位/说明文案）
- 保留 `resolveAffiliateReferralCode` URL 回填：URL 带参则自动填入且可编辑，无参则用户可手填
- 提交透传逻辑已存在，无需改动

### 前端 · 后台设置（`SettingsView.vue` + `api/admin/affiliates.ts`、settings DTO）
- 移除返利比例 / 有效期 / 单人上限 3 个输入框 + 专属用户"专属比例"项
- 新增 3 个输入框：首充阈值、邀请方奖励、被邀请方奖励（含校验 ≥ 0）

### 前端 · 用户邀请页（`AffiliateView.vue`）
- 文案改为"邀请好友首充满 X，双方各得 Y"口径（比例相关展示移除）

## 8. 边界情况

1. **并发/重复回调**：`tryClaimAffiliateRebateAudit` 按 order ID 保证只结算一次；首充判定在同一事务内，避免两笔订单同判首充
2. **首充退款**：奖励不回收（本期已知行为）
3. **无邀请人用户**：不触发（正常）
4. **首充恰等于阈值**：`≥` 判达标（含等于）
5. **首笔余额充值 < 阈值后再买订阅**：不触发（严格只认第一笔，符合确认）
6. **总开关关闭期间的首充**：不触发；之后开关打开也不补发（该用户已非首充）
7. **被邀请方加余额失败**：与邀请方发放同事务，一起回滚，不出现只发一半
8. **奖励额配置为 0**：对应一侧不发放
9. **历史被邀请用户（活动前注册、活动后首充）**：算数（已确认不加时间卡点）

## 9. 测试计划

### 后端单元/集成
- 首充 + 余额达标 → 双方各得，金额正确、去向正确（邀请方 aff_quota / 被邀请方 balance）
- 首充 + 订阅 → 无条件达标发放
- 首充 + 余额不达标 → 不发放
- 非首充（已有成功订单）→ 跳过
- 无邀请人 → 跳过
- 阈值边界（等于阈值）→ 达标
- 奖励额为 0 → 对应侧不发
- 幂等：同订单重复触发只发一次
- 事务回滚：被邀请方加余额失败 → 邀请方也不落账
- 兑换码激活 → 不再触发邀请奖励
- 配置 getter：默认值、解析失败回退、负值 clamp

### 前端
- 注册页：URL 带 `?aff=` 自动回填；无参手动输入；空值不报错
- 后台设置：新 3 项保存/回显；旧项已移除
- 用户邀请页文案更新

## 10. 非目标（本期不做）
- 首充退款时回收奖励
- 注册时间卡点 / 历史账号排除
- 多级/阶梯邀请奖励
- 删除数据库 `aff_rebate_rate_percent` 列

## 11. 风险
- **行为语义变更大**：老用户此前理解的"按比例每笔返"将失效，需同步对外文案/公告（产品侧）
- **旧配置移除**：确保后端不再读取的同时，历史 `options` 表遗留值不影响新逻辑
- **审计连续性**：保留原审计动作名，新增被邀请方奖励审计，便于对账

