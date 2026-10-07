# sub2api b8de Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking. Execution method is awaiting user selection; no product implementation is authorized by this document alone.

**Goal:** 将完整上游历史合至 `b8dece9000c68815a5b867ca5a1e6f236e173905`，保护本地行为，经独立全量复审后普通推送 dev，并等待精确 SHA CI 全绿。

**Architecture:** 固定终点的普通双父 merge；现有本地架构不重写。按下列领域整合 base / ours / theirs / final，所有自动合并文件同样审查。领域任务共享一个候选树，最终作为一次同步交付，不拆成未验证的独立发布。

**Tech Stack:** Go 1.27.0、PostgreSQL 18.1、Redis 8.4、Ent/Wire、Vue/TypeScript、Node 20/pnpm 9、Vitest、GitHub Actions、golangci-lint 2.13。

**Spec:** `docs/superpowers/specs/2026-10-05-sub2api-b8de-integration-design.md`（用户已回复 ok 确认书面方案）。

## Global Constraints

- DEV_BASE=`2229e149bb9153e90027b59bfdf78a4c3c91fc7d`；BASE/LAST_UPSTREAM=`a3eb7ef302961cba716dc78b39b93b60c467db0e`；PIN=`b8dece9000c68815a5b867ca5a1e6f236e173905`。
- 唯一实现工作区 `/home/alvin/tokenstation3/.worktrees/upstream-sub2api-20261005-b8de`，分支 `codex/upstream-sub2api-20261005-b8de`；备份 `backup/dev-before-upstream-sync-20261005-b8de` 保持 DEV_BASE。
- 原工作区 `.gitignore`、`AGENTS.md` 用户改动不动；保护 stash 保留；不读取生产配置，不操作生产数据库或真实 provider。
- 在途余额预留纳入、默认关闭；显式启用适配奖励优先和订阅回退，简易模式不引入余额 / 订阅扣费。
- 充值赠金 / 折扣纳入、默认无活动；赠金为不过期普通余额；邀请奖励基数剔除赠送，既有到期奖励不变。
- 新 migration 246 充值赠金、247 TypeSafe；旧 SQL/checksum 不动，平台约束保留 KIRO，不引用 Composite 表。
- Fable 5.1 max 无显式配置=3；显式 effort 配置优先。Astra Ultrafast 独立=6；普通 Fast/免费 Fast/Flex 保持。
- OAuth 调度倍率必须为数值，默认1、显式0不变。模型列表 display-only，不恢复请求准入 allowlist。
- API Key 默认200个未删除/用户、60次创建尝试/小时；0关闭对应限制，删除不返还次数，不影响旧 Key。
- 邮箱验证码5次原子计数；密码重置令牌哈希与原子单次消费，升级前链接失效已获确认；匿名订单 verify=20/IP/min，Redis故障放行。
- 风控白名单默认空，只豁免本地风控处罚；鉴权、计费、额度及上游拦截不豁免。
- TypeSafe 原生入口需手工配置账号/组；Claude 仅管理员手动重置、二次确认、防重、无自动兑换。
- Claude Code 专用组的兼容入口：有备用组才降级选号，否则403；费用/用量归 Key 所属组。
- 保持 KIRO 参考 `6ba76ea105e065a5aa8dd2b8d2957528ed58935b`、unified key、capture/spool/WebChat、分层奖励和订阅回退、全NULL额度行、稀疏价格/显式0/缺价拒绝。
- 排除 Plugin、Composite、Grok音频、独立`/x_search`、新增线下提现和自动上游费率回写；保留媒体利润门既有豁免。
- 不用 ours/theirs 整文件覆盖、不 add-A、不 force。未解决 merge 中不做任务级 commit；先显式暂存本任务路径，全部验证后创建双父 merge。后续修复可单独 commit 并重新验证。
- 每次测试记录命令、树坐标、起止时间、退出码、资源隔离和日志。新发现的语义冲突、验证缺口或远端漂移先询问，不猜测。

## Review Focus

1. 订阅存在但奖励优先，或订阅额度已满后回退余额：不能被上游“订阅就不预留”分支漏掉；Task 3测试。
2. 赠金订单重复回调、活动变更后到账、部分退款：不能重复赠送、把免费额度变成实收或扩大邀请奖励；Task 2测试。
3. 原生 TypeSafe 新入口与 unified key / 强制平台 / 非聊天协议：不能走错协议、绕过审核计费或复活 Composite；Task 5测试。
4. 请求中途删Key、客户端取消及异步结算交接：费用不能丢失、预留不能提前释放或泄漏，普通数据库错误不能被吞；Tasks 3/4测试。
5. 风控名单移除时旧异步任务、同组织多账号重复兑换、令牌并发消费：不能产生额外处罚、双重兑换或重复验证成功；Tasks 6/7测试。

## 文件覆盖与执行约定

新增 `docs/upstream-sync/evidence/2026-10-05-b8de/coverage.tsv`，对最初394个上游变更文件分配Task 2–9，初始状态全部`planned`。
执行中将 `git diff --name-only DEV_BASE`（含暂存结果）、新增未跟踪候选文件、上游394路径并集补入清单。
每行只有一个主负责人；跨领域调用链在证据中交叉引用。未分配路径、只标“自动合并”或没有证据的行都不算完成。
领域证据使用同目录 `data.md`、`billing.md`、`gateway.md`、`typesafe.md`、`security.md`、`accounts.md`、`frontend.md`、`integration.md`。

以下路径均相对实现工作区。命令的工作目录明确标注；不在原工作区执行暂存或产品编辑。
上游新文件从固定PIN读取；本地已删除的上游拆分文件按实际调用方接入现存文件，不直接恢复。
领域Task 2–9先形成可编译候选，再完成受共享包依赖阻塞的红/绿测试。编译错误不是有效的行为失败证据：
新增组合回归需要在受控移除对应适配的候选上出现预期断言失败，再恢复适配变绿；只动自己的测试补丁，不reset其他改动。

### Task 1: 干净基线、合并现场与覆盖清单

**Files:** Read `AGENTS.md`, `docs/kiro-upstream-sync.md`, spec、上轮归档、根/后端Makefile、CI/Security workflows；Modify coverage/integration证据。
**Interfaces:** 输出固定四坐标、MERGE_HEAD、冲突列表和每文件审查清单，供Tasks 2–9使用。

- [ ] 检查分支、worktree和用户现场，验证备份=DEV_BASE；当前方案文档提交可以在第一父系，不得改变PIN。
- [ ] 执行环境只读预检：工具版本、CPU/内存、Docker可用性、测试镜像和临时端口范围；执行前检查相关AGENTS。
- [ ] 安装计划所需依赖（此时须已获得计划执行批准）：frontend使用`pnpm install --frozen-lockfile`；backend使用module指定Go工具链，不修改go.mod/version floor。
- [ ] 在未合入的当前树记录基线：backend `make check-generate`独占，之后`make build`、`go test -count=1 -p 2 -timeout=20m -tags=unit ./...`；根目录`make test-frontend`。验证无意外文件漂移，失败先报告，不当作本轮通过。
- [ ] 记录`git diff --name-status BASE PIN`、缺失历史与本地交集；创建/核对coverage，全部状态仍待审查。
- [ ] 干净隔离树执行`git -c core.hooksPath=/dev/null -c rerere.enabled=false merge --no-ff --no-commit b8dece9000c68815a5b867ca5a1e6f236e173905`，冲突退出码1只代表预期中间状态，确认MERGE_HEAD=PIN。
- [ ] 对每个冲突和自动合并区域登记双方意图，按Tasks 2–9处理；保持未提交merge，不推送。

### Task 2: 迁移、充值与本地奖励账务

**Files:** Adopt `backend/internal/service/payment_recharge_bonus.go`及测试；Modify `payment_order.go`, `payment_fulfillment.go`, `payment_config_service.go`, `payment_service.go`、支付handler/DTO、`backend/ent/schema/payment_order.go`；Create `backend/migrations/246_add_payment_order_bonus_amount.sql`, `247_add_typesafe_platform.sql`, `recharge_bonus_migration_test.go`；Modify `typesafe_platform_migration_test.go`, `backend/internal/repository/upstream_sync_migration_policy_test.go`。
**Interfaces:** `quoteRechargeBonus(cfg *PaymentConfig, paymentAmount float64, currency string) rechargeBonusQuote`返回`PayBase/Credited/Bonus`；`paymentOrderAmountWithoutBonus(o *dbent.PaymentOrder) float64`供返利基数使用；API/Ent字段`bonus_amount`/`BonusAmount`。

- [ ] 先增加`TestUpstreamB8deMigrationPolicy`：历史迁移字节/hash不变，新文件只有246/247；空库/升级/重放后存在bonus列、旧订单值0、KIRO和typesafe可用、Composite表不要求存在。
- [ ] 增加`TestRechargeBonusLocalFulfillmentContract`，复用现有订单/奖励fixture；加入报价断言：
  ```go
  cfg := &PaymentConfig{BalanceRechargeMultiplier: 1, RechargeBonusMode: "bonus", RechargeBonusTiers: []RechargeBonusTier{{MinAmount: 100, BonusPercent: 10}}}
  q := quoteRechargeBonus(cfg, 100, "USD")
  require.Equal(t, float64(100), q.PayBase)
  require.Equal(t, float64(110), q.Credited)
  require.Equal(t, float64(10), q.Bonus)
  require.Equal(t, float64(100), paymentOrderAmountWithoutBonus(&dbent.PaymentOrder{OrderType: payment.OrderTypeBalance, Amount: 110, BonusAmount: 10}))
  ```
- [ ] 同一fixture测试空活动=旧行为、订阅不优惠、折扣输入100/10%=实付基数90到账100免费部分10；重复回调只入账一次、活动变化不重算、既有奖励不改到期时间、退款现金按原实收比例。
- [ ] 逐调用链整合报价→订单快照→支付回调→本地幂等履约→首充奖励；新迁移重编号和约束按spec，不改变退款状态机。若退款/奖励回收需要新规则，先问用户。
- [ ] backend运行`go test -count=1 ./migrations`及`go test -count=1 -tags=unit ./internal/service -run 'RechargeBonus|Payment|Affiliate|Reward'`；运行`CI=true go test -count=1 -p 1 -timeout=20m -tags=integration ./internal/repository -run 'UpstreamB8de|Migration|Payment|Reward'`。要求测试实际执行而非skip。
- [ ] 显式暂存Task 2路径，写data证据；Ent生成文件留Task 9独占生成。

### Task 3: 本地计费、在途预留与删Key结算

**Files:** Adopt service/handler的`billing_inflight_reservation.go`/`gateway_inflight_reservation.go`和repository的`billing_inflight_cache.go`及对应测试；Modify `backend/internal/config/config.go`, `backend/internal/service/billing_cache_service.go`, `billing_service.go`, `service_tier_billing.go`, `model_pricing_resolver.go`, `account_stats_pricing.go`, `pricing_service.go`, `model_plaza_service.go`, `gateway_usage_billing.go`, `openai_gateway_usage.go`, `backend/internal/repository/usage_billing_repo.go`和对应测试/价格资源；Create `backend/internal/service/billing_inflight_local_funding_test.go`, `backend/internal/config/upstream_b8de_defaults_test.go`。
**Interfaces:** `(*BillingCacheService).ReserveInflight(ctx context.Context, user *User, group *Group, subscription *UserSubscription, estimate float64) (*InflightReservation,error)`；`(*GatewayService)`和`(*OpenAIGatewayService).EstimateInflightReservation(ctx context.Context, apiKey *APIKey, req InflightEstimateRequest) (float64,bool)`；`WithInflightReservation`/`InflightReservationFromContext`及`Acquire/HandlerDone`交接。

- [ ] 测试`TestUpstreamB8deDefaults`加载生产配置默认：`InflightReservation.Enabled=false`、Key上限200/60；部署示例/env默认一致。
- [ ] 增加`TestInflightLocalFunding`矩阵：默认关闭不访问预留缓存；订阅有余量且无奖励不预留；奖励优先即使有订阅也预留；周额度不足且fallback开时预留普通余额；fallback关不放宽拒绝；过期奖励不计可用；简单模式不预留。
- [ ] 以当前`getBillingBalanceSnapshot`、`CheckBillingEligibility`及仓储`applyUsageBillingRewardLayer`的整层付款规则适配预留，禁止把不同奖励层拼起来冒充可用单层。预估不改变最终资金源/实际扣款；保留上游首请求与Redis故障放行边界。
- [ ] 增加预留取消/异步交接测试：handler完成后计费未结束仍占用，任务失败和未提交均释放；续期不得复活过期预留。WS按turn处理，不给已生成媒体查询额外设付款门槛。
- [ ] 增加`TestUpstreamB8dePricingPreservation`并扩展既有reasoning/account-stat测试：
  ```go
  require.Equal(t, float64(3), modelReasoningEffortBillingMultiplier("claude-fable-5-1", "max", nil))
  require.Equal(t, float64(1), modelReasoningEffortBillingMultiplier("claude-fable-5-1", "max", map[string]float64{"max": 1}))
  require.Equal(t, float64(6), configuredServiceTierMultiplier("ultrafast", &ModelPricing{UltrafastMultiplier: 6}))
  ```
- [ ] 接入Astra独立倍率和新模型价格；对真实价格解析、显式0/缺失、Fast/Flex、长上下文、图像/视频、1h/5m和模型广场/账号成本做端到端断言，而非仅helper测试。
- [ ] 仓储删Key回归：Key删除后用户/账号应结算一次；只忽略Key quota/window的ErrAPIKeyNotFound；其他SQL错误仍回滚，保留结果BillingType驱动缓存更新。
- [ ] backend执行`go test -count=1 -p 2 -tags=unit ./internal/config ./internal/service ./internal/handler ./internal/repository -run 'B8de|Inflight|ReasoningEffort|Tier|Pricing|Billing|SimpleMode|Reward'`及对应integration；全部绿后显式暂存、记billing证据。

### Task 4: 共享网关、协议、客户端取消与展示边界

**Files:** Modify `backend/internal/handler/gateway_handler*.go`, `openai_*.go`, `gemini_v1beta_handler.go`, `grok_media.go`, `ops_error_logger.go`；service的gateway/openai_gateway/gemini/antigravity/WS/probe/model-catalog文件；`backend/internal/pkg/apicompat`, `antigravity`, `googleapi`, `openai`, `claude`, `xai`；详见coverage的Task 4逐文件清单，Task 3/5/6/7已归属文件不重复负责。
**Interfaces:** 消费Task 3预留接口；保留现有`ForwardResult`/`OpenAIForwardResult`、最终tier/effort及capture上下文；已有`checkClaudeCodeRestriction(ctx context.Context, groupID *int64) (*Group,*int64,error)`处理备用组。

- [ ] 阅读KIRO guide及共享入口/转发/usage/capture完整链，保留direct/relay、ARN、machine_id、sticky、冷却、WebChat和最终attempt。
- [ ] 先补`TestB8deClaudeCodeFallbackBillingOwnership`：兼容入口无备用403；有备用按备用组选号，但API Key组/订阅组和结算归属不被覆写；失败/环路不退成任意组。禁止上游展示allowlist变成请求准入。
- [ ] 为工具初始参数、最终空文本恢复、disabled thinking、context rollover WS、首内容keepalive、客户端取消499补/保留断言；已写响应后不再次写错误、不重复usage/capture。
- [ ] 整合所有协议修复并在本地入口接预留；模型目录补mapped models但保持display-only；保留Grok身份与代理，清理Antigravity上游错误中的敏感标识。
- [ ] backend运行`go test -count=1 -p 2 ./internal/pkg/...`和`go test -count=1 -p 2 -tags=unit ./internal/service ./internal/handler -run 'B8de|ClaudeCode|Kiro|Capture|Cancel|Disconnect|Stream|Tool|Thinking|Responses|WebSocket|Model'`；记录红/绿及覆盖证据，显式暂存。

### Task 5: TypeSafe 独立平台与原生入口

**Files:** Adopt `backend/internal/handler/gateway_systemone.go`, `backend/internal/service/gateway_systemone.go`, `account_test_service_typesafe.go`, `backend/internal/pkg/typesafe/systemone.go`及测试；Modify `backend/internal/server/routes/gateway.go`, `backend/internal/securityaudit/prompt_snapshot.go`, platform常量、`backend/ent/schema/user_platform_quota.go`、account/channel/group平台校验；Task 2负责247，Task 6负责moderation共有逻辑。
**Interfaces:** `(*GatewayHandler).SystemOne(c *gin.Context)`；`(*GatewayService).ForwardSystemOne(ctx context.Context,c *gin.Context,account *Account,body []byte) (*SystemOneForwardResult,error)`；`typesafe.ValidateSystemOneRequest(body []byte) (string,error)`；平台值`typesafe`。

- [ ] 先补`TestB8deSystemOneLocalContracts`：未鉴权拒绝、统一Key正确绑定组、错误平台拒绝、只走原生endpoint；prompt审计/额度/价格校验不能被跳过；重复或取消不双计费。
- [ ] 适配TypeSafe入口、调度、错误重试、最终usage及账务；删除Composite分支而不是恢复Composite helper；非SystemOne入口不得把TypeSafe凭据发往聊天地址。
- [ ] 保留原内部TypeSafe审核引擎配置；不自动创建provider账号/组，不扩展channel monitor provider；已有KIRO和其他平台校验不缩水。
- [ ] backend执行`go test -count=1 ./internal/pkg/typesafe`及`go test -count=1 -p 2 -tags=unit ./internal/handler ./internal/service ./internal/server/... ./internal/securityaudit -run 'SystemOne|TypeSafe|B8de|PromptAudit|Platform'`，显式暂存，记typesafe证据。

### Task 6: 安全、Key创建与风控名单

**Files:** Modify `backend/internal/service/api_key_service.go`, `email_service.go`, `content_moderation.go`, `content_moderation_input.go`, `setting_service.go`, `settings_view.go`、repository的`api_key_cache.go`, `api_key_repo.go`, `email_cache.go`, `content_moderation_repo.go`、admin settings/DTO、routes/payment；Adopt `cyber_policy_allowlist.go`, `openai_cyber_allowlist.go`及上游相关测试；保留本地settings合并结构，不恢复重复拆分文件。
**Interfaces:** `IncrementCreateCount(ctx context.Context,userID int64,window time.Duration) (int64,error)`；`ConsumePasswordResetToken(ctx context.Context,email,tokenHash string) (bool,error)`（cache）；`ParseCyberPolicyUserAllowlist(raw string) (map[int64]struct{},error)`；`IsCyberPolicyUserAllowlisted(ctx context.Context,userID int64) bool`。

- [ ] 先补`TestB8deSecurityLocalContracts`：200禁止再建但不影响旧Key；固定窗口第61次拒绝；删除不归零；0关闭；Redis错误按批准规则处理；并发计数不丢更新。检查数量上限并发竞态，不能仅靠非原子count-then-insert宣称硬上限。
- [ ] 验证码并发最多评估5次；重置token并发消费恰有一个成功；缓存不含明文；旧链接和重发前链接失效；身份服务失败路径不意外改密码。
- [ ] 名单测试：关键词/hash/API审核均留log-only且无封号、邮件或历史处罚计数；删除名单后新请求恢复规则，旧异步任务沿快照；数据库错误不把普通用户全体放行；计费鉴权仍生效。
- [ ] 移植安全修复到本地服务、settings保存/缓存失效、DTO和审计；对所有mock接口同步更新。匿名verify第21次429、不同IP隔离、Redis错误放行、签名resolve不受新增限流。
- [ ] backend执行`go test -count=1 -p 2 -tags=unit ./internal/service ./internal/repository ./internal/handler/... ./internal/server/routes -run 'B8deSecurity|CreateLimit|CreateCount|ResetToken|Verification|RiskControl|Allowlist|PublicOrder'`；Redis Lua用integration真实Redis验证；显式暂存、记security证据。

### Task 7: Claude 重置、账号调度与管理数据

**Files:** Adopt service的`claude_reset_credits.go`, `claude_reset_redeem.go`和admin的`claude_reset_handler.go`及对应测试；Modify admin/account、dashboard/query-cache、group/channel管理；service account/admin/scheduler/quota-auto-reset/dashboard/user相关文件；repository scheduler/http_upstream/usage_log_trend；`backend/internal/server/routes/admin.go`。
**Interfaces:** `(*ClaudeResetCreditService).Query(ctx context.Context,id int64) (*ClaudeResetCredits,error)`；`Redeem(ctx context.Context,id int64,key string) (*ClaudeResetOutcome,error)`；`ConfigureRedemption(idem *IdempotencyCoordinator,locks LeaderLockCache)`；管理兑换必须`Idempotency-Key`。

- [ ] 先补`TestB8deClaudeResetLocalContract`：非管理员拒绝；同Key重放仅1次POST、同组织不同账号并发仅1次消费；锁/持久化不可用不发送兑换；结果不明24h、明确不可用15min防重；只读查询0次POST。模拟代理和请求header，不访问真实provider。
- [ ] 接入查询/兑换及Wire输入定义，沿现有账号代理/身份、组织锁/持久化幂等；不新增自动兑换任务，返回不泄露grant/org标识。
- [ ] 审查scheduler quota reset字段、无credits退避/已知reset时间、账号priority、dashboard趋势缓存和统计变化；保留KIRO字段及数值调度倍率。测试默认1、显式0、null拒绝和跨请求配置保持。
- [ ] backend执行`go test -count=1 -p 2 -tags=unit ./internal/service ./internal/handler/admin ./internal/repository ./internal/server/routes -run 'ClaudeReset|Scheduling|Scheduler|QuotaAutoReset|Dashboard|B8de|UpstreamBilling'`；接口/平台管理契约测试全覆盖，显式暂存、记accounts证据。

### Task 8: 前端契约、充值展示和管理操作

**Files:** Modify/Adopt coverage中全部`frontend/`路径；核心为`RechargeBonusTierEditor.vue`, `ClaudeResetCreditsCell.vue`, `AccountPriorityCell.vue`, `AmountInput.vue`, payment types/API、平台常量、账号/分组/设置页、Key页及modelPlaza；保留本地其他组件、WebChat、品牌和奖励页。
**Interfaces:** Task 2的`bonus_amount`与充值设置；Task 7查询/兑换JSON及`Idempotency-Key`；Task 5平台值`typesafe`；Task 6名单设置；不向API发送nullable scheduling rate。

- [ ] 先补各组件现有spec：空活动维持原报价；100赠10展示实付100到账110；折扣展示实付90到账100；订单展示使用快照；名单默认空及移除保存；TypeSafe/KIRO/全NULL平台额度行共存。
- [ ] Claude组件：查询不兑换、取消确认0次兑换、确认一次POST、超时重试复用同一幂等Key、切换账号/卸载不回写过期响应；Priority单元输入/防抖清理不误改别的账号。
- [ ] Fable max默认显示3，Astra Ultrafast显示6，Fast/显式0不回退；调度倍率数值0可保存、空值不静默变为账户倍率；model listing仍只展示，历史排除项不回流。
- [ ] 完整核对后端DTO与前端类型、API、表单、i18n、路由；保留自动合并区中的本地事件/生命周期；支付Markdown文案沿既有安全渲染。
- [ ] frontend运行`pnpm exec vitest run src/components/account/__tests__/ClaudeResetCreditsCell.spec.ts src/components/account/__tests__/AccountPriorityCell.spec.ts src/components/payment/__tests__/AmountInput.spec.ts src/components/modelPlaza/__tests__/PlazaModelPricingTable.spec.ts src/views/admin/__tests__/SettingsView.spec.ts`；之后`pnpm run lint:check`、`pnpm run typecheck`、`pnpm run test:run`、`pnpm run build`串行，显式暂存、记frontend证据。

### Task 9: 生成、部署和未归类自动合并审查

**Files:** `backend/cmd/server/wire_gen.go`, `backend/ent`生成代码、各wire输入、`backend/internal/server/router.go`, `backend/internal/setup`、`deploy/config.example.yaml`, 三份Compose、README/README_CN、`.github/SECURITY.md`、VERSION；coverage所有剩余路径。
**Interfaces:** Task 2 Ent schema、Task 3缓存接口、Task 5handler、Task 6新增Redis注入、Task 7Claude服务；输出可构建的完整候选。

- [ ] 逐文件审查剩余路径；上游Composite文档及排除路径保持删除，未审计路径数必须为0。检查新设置env接入、默认预留false、Key200/60；保留本地部署镜像/服务/安全边界。
- [ ] `git diff --name-only --diff-filter=U`必须为空；检查冲突标记、重复symbol、孤立调用、已排除产品残留；明确暂存清单，不add-A。
- [ ] backend独占`make generate`；只接受由已审schema/Wire输入产生的生成diff，显式暂存。`git diff --exit-code -- ent cmd/server/wire_gen.go cmd/server/wire_gen_test.go`验证无未暂存漂移。
- [ ] 形成可编译候选后，完成Tasks 2–8之前阻塞的行为红/绿证明。仅编译成功不能关闭领域任务。
- [ ] backend运行`make build`；根目录部署/发布脚本执行Task 10列表；记录integration证据。候选所有必要代码必须对应同一树再进入全量验证。

### Task 10: 同一最终候选的全量验证与归档

**Files:** Evidence各领域报告、coverage、`docs/upstream-sync/2026-10-05-sub2api-0.2.13-b8de.md`、索引README；产品修复回对应Task。
**Interfaces:** 消费已暂存候选树和Tasks 2–9证据；输出精确树的完整测试记录、双父merge、待独立审查HEAD。

- [ ] `git diff --cached --check`、`git diff --check`、无unmerged、无意外文件；`git write-tree`记录候选树。backend再次独占`make check-generate`，比较工作树与已暂存候选；消费任务不能并行改生成文件。
- [ ] 执行下表。初始最大两个资源独立队列：backend与frontend；backend normal/unit/race/lint顺序，integration另作独占容器队列。其他已证明独立的检查可并行；为每条命令保存独立起止/退出码，不把skip当通过。

| 工作目录 | 必需命令 / 资源 |
| --- | --- |
| backend | `make build`；输出`bin/server`独占 |
| backend | `go test -count=1 -p 2 -timeout=20m ./...`；接着相同命令增加`-tags=unit` |
| backend | `CI=true go test -count=1 -p 1 -timeout=20m -tags=integration ./...`；PG18.1/Redis8.4真实容器、唯一端口/实例；先检查Docker，禁止TestMain静默skip |
| backend | `go test -race -count=1 -p 1 -timeout=20m -tags=unit ./internal/service ./internal/handler ./internal/repository -run 'Inflight|ClaudeReset|RiskControl|ResetToken|Billing|Capture|Kiro|CreateCount'` |
| backend | `golangci-lint run --timeout=30m`；`govulncheck ./...` |
| 根目录 | `make test-frontend`；`make build-frontend`；与完整Vitest合计覆盖critical/WebChat/full；vue-tsc/build串行 |
| frontend | `pnpm run test:run`；`pnpm audit --prod --audit-level=high --json`保存原始JSON，再用`tools/check_pnpm_audit_exceptions.py`按workflow校验，audit非零不能直接当成功 |
| 根目录 | `bash -n deploy/apple-container.sh`；`bash deploy/tests/apple-container-test.sh`；`sh deploy/tests/docker-compose-security-test.sh`；`sh deploy/tests/docker-compose-gateway-env-test.sh`；`sh deploy/tests/docker-runtime-resources-test.sh`；`sh deploy/test-caddyfile-cache.sh`；`sh deploy/tests/docker-compose-simple-mode-env-test.sh` |
| 根目录 | `python -m unittest discover -s .github/release-tools -p 'test_release_matrix.py'`；`bash -n .github/release-tools/release-images.sh .github/release-tools/resolve-release-ref.sh` |

- [ ] `make check-generate`中的`git diff --exit-code -- ent cmd/server/wire_gen.go cmd/server/wire_gen_test.go`比较工作树与index，不是HEAD；所以须先显式暂存已审生成结果，再独占执行检查。创建merge后原样再跑一次，防止暂存遗漏。
- [ ] 临时端口耗尽/容器故障不能当代码失败或通过；先停止相关批次、在隔离测试资源重跑，不改宿主生产参数。疑似基线欠账用DEV_BASE独立worktree相同环境/命令A/B，失败或验证不足先报告。
- [ ] 对新树修复后重跑受影响全量套件；归档真实命令/结果、决策、备份和coverage。`git diff --cached --check`成功后创建普通merge；验证双父中第二父=PIN、第一父系含DEV_BASE、范围完整。
- [ ] 在最终HEAD运行原样`make check-generate`，无漂移才关闭生成门禁；任何生成/代码修复都重新验证并更新审查坐标。此时仍不push。

### Task 11: 全新独立复审、非强推与精确SHA CI

**Files:** Archive/coverage/evidence；不引入与发现无关的修改。
**Interfaces:** 将工作区、DEV_BASE/BASE/PIN/HEAD/双父、全量路径并集及原始测试日志交全新reviewer；不提供“安全”预期结论。

- [ ] 按sync技能启动全新独立subagent；全量检查每个变更路径和调用链，包含自动合并与排除项。可按清单分区，但每个分区必须无遗漏并明确`NO ACTIONABLE ISSUES / SAFE TO PUSH`。
- [ ] finding先修复或询问，重跑对应测试；代码变化后交新的独立reviewer重审。未覆盖、仅抽样、pending或NOT SAFE TO PUSH一律阻止推送。
- [ ] 再fetch origin/upstream，核对远端dev仍为DEV_BASE、上游仍为PIN；出现漂移先询问。main只fast-forward到origin/main，不能merge dev进去。
- [ ] 先展示将推ref/SHA。安全地将原工作区dev以ff-only指向候选（已有dirty文件若重叠则先询问）；不reset、不覆盖。`git push origin dev main`非强制，确认远端dev精确SHA。
- [ ] 用`gh`按精确SHA枚举check-runs/status与保护要求，持续等shell/test/frontend/golangci-lint/release-helpers/backend-security/frontend-security及其他必需项全success。失败读日志修复、重测、新review、新SHA后重新等待。
- [ ] 归档最终结果，任何额外归档提交也须追踪其精确SHA CI；完成报告含PIN/devSHA/main状态/验证/复审/CI链接/回滚引用。备份保留，仅清理本轮且干净的临时工作区；不清理他人工作区或用户stash。

## 自审与执行方式

- spec的每项已确认决策已分配至Tasks 2–8；本地保护和完整范围由Tasks 1/4/9覆盖；测试、独立审查、远端与CI由Tasks 10/11覆盖。
- 五项Review Focus均有明确回归归属；生产操作和实际provider兑换保持禁止。
- 推荐Native：由主agent在本会话按计划实施，共享计费/路由/配置文件顺序整合；完成后仍执行runbook强制的全新独立全量复审。
- 备选Subagent-driven：逐任务由独立实现agent和reviewer执行，共享文件严格交接，不允许并发写同一文件或在未解决merge内提交。
- 用户审阅本计划并选择执行方式之前，不开始正式merge、依赖安装或产品实现。
