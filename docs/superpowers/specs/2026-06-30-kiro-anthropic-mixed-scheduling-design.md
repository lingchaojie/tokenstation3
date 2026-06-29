# Kiro ↔ Anthropic 混合调度设计

## 目标

让走 **anthropic 默认分组**的用户，用**原有 API key、零客户端改动**，无感地命中 kiro 账号——anthropic 账号与 kiro 账号在同一分组内混合调度。

复用代码中已验证成熟的 **Antigravity 混合调度**机制，将其推广到 Kiro。转发链路不动；kiro 专属配置改为账号级以保证行为零退化。

## 背景：现有路由与调度

关键事实（均经代码查证）：

- **分组只有单一 `platform` 字段**（`groups.platform`，默认 `anthropic`）。调度时按 `platform == group.platform` 过滤候选账号，所以直接把 kiro 账号塞进 anthropic 分组会被过滤掉、永远选不中。
- **已存在的混合调度豁免**：当分组是 `anthropic`/`gemini` 且非强制平台时，`useMixed=true`，候选平台扩为 `[platform, antigravity]`，且只放行 `IsMixedSchedulingEnabled()==true` 的 antigravity 账号。判定统一收口在 `isAccountAllowedForPlatform(acc, platform, useMixed)`（`gateway_service.go:2726`）。
- **转发层 `Forward()` 完全按账号自身判定**：`isKiroDirectModeAccount(account)` 为真 → `forwardKiroMessages`，**与分组 platform 无关**（`gateway_service.go:5074`）。即只要调度器把 kiro 账号选出来，转发本就正确。
- **kiro 专属 header（KiroIDE/aws-sdk UA、X-Amz-User-Agent、tokentype）只在 kiro 专属函数里写**（`kiro_profile_resolver.go:72-76`、`kiro_http_helpers.go:218-236`），写在 kiro 自己 `http.NewRequestWithContext` 构造的出站请求上；anthropic 走 `buildUpstreamRequest` 另建出站请求。两条路各建各的请求、各定各的目标 URL，**结构上不可能交叉污染**。
- **粘性会话 key** = `buildSessionKey(groupID, sessionHash)`（`gateway_cache.go:25`），按 groupID 隔离；`sessionHash` 由 `GenerateSessionHash(parsed)` 按请求内容算，**纯本地路由 key，不进上游请求、不参与请求构造**。
- **kiro 模拟缓存状态**按 `kiroCacheCredentialKey(account)`（账号凭证）存（`kiro_cache_emulation.go:62`），本就跟随账号、与分组无关。group 只提供"开关 + ratio"两个标量。
- **kiro 专属配置目前全挂在 `*Group` 上**且 `isKiroGroup(g)` 门控：`EffectiveKiroEndpointMode`、`EffectiveKiroCacheEmulationEnabled`、`EffectiveKiroAutoStickyEnabled`、`EffectiveKiroStickySessionTTL`。

## 已敲定的设计决策

1. **方案 A**：推广混合调度——给 kiro 账号一个 `mixed_scheduling` 开关，开启后可加入 anthropic 分组参与调度。**不**改默认分组路由、**不**支持多默认分组。
2. **调度策略**：沿用现有 `account.priority`（越小越优先）+ 最久未用轮转。anthropic 与 kiro 在池内**平等竞争**，不做平台分层。
3. **kiro 专属配置来源**：**账号级**。endpoint mode / cache emulation / auto-sticky / TTL 存到 kiro 账号自身 `Extra`，混合调度到任何分组都沿用账号自己的配置，保证行为零退化（保留 KRS + 缓存模拟）。
4. **auto-sticky 选 A**：放宽 `GenerateSessionHash` 的稳定 hash 门控，使"含可调度 mixed kiro 账号"的混合 anthropic 分组也用稳定 hash，从而裸客户端（不送会话标识）也能把 kiro 账号钉住。

### 约束

- **kiro 只混入 anthropic 分组，绝不混入 gemini**（kiro 只服 Claude 模型）。antigravity 维持原样（可混入 anthropic+gemini）。
- 强制平台模式（`/antigravity` 等显式路由）**不走**混合调度，维持原样。

## 范围

**改动**：调度准入（让 mixed kiro 进 anthropic 池）、账号级 kiro 配置 resolver、auto-sticky 稳定 hash 门控、前端账号编辑 UI。

**不改**：anthropic/openai/gemini/antigravity/grok 的转发链路、选号算法本身、默认分组路由（`resolveProviderGroup`）、计费、kiro 转发实现（`forwardKiroMessages`）、`gemini_messages_compat_service.go`、数据库 schema（复用 `accounts.extra` JSONB）。

## Section 1 — 调度准入：让 mixed kiro 进入 anthropic 池

把"仅 antigravity"的混合判定推广为"平台 + 目标平台"双维度。核心约束：**antigravity → anthropic+gemini；kiro → 仅 anthropic**。

### 1.1 账号级开关（`account.go`）

新增 `IsKiroMixedSchedulingEnabled()`：`Platform==kiro && Extra["mixed_scheduling"]==true`。保留 `IsMixedSchedulingEnabled()`（antigravity）原语义不变。

引入一个目标平台感知的统一判定（供调度与快照共用）：

```
// accountEligibleForMixedPlatform 判定账号能否在 targetPlatform 的混合池中被调度。
func accountEligibleForMixedPlatform(acc *Account, targetPlatform string) bool {
    switch {
    case acc.Platform == PlatformAntigravity:
        return acc.IsMixedSchedulingEnabled()        // anthropic + gemini
    case acc.Platform == PlatformKiro:
        return targetPlatform == PlatformAnthropic && acc.IsKiroMixedSchedulingEnabled() // 仅 anthropic
    default:
        return false
    }
}
```

### 1.2 候选平台列表（两处，必须一致）

| 文件:行 | 现状 | 改为 |
|---|---|---|
| `gateway_service.go:2637` `listSchedulableAccounts` useMixed 分支 | `platforms=[platform, antigravity]` | `platform==anthropic` 时 `[anthropic, antigravity, kiro]`；`platform==gemini` 时维持 `[gemini, antigravity]` |
| `scheduler_snapshot_service.go:680` `loadAccountsFromDB`（**生产快照路径**） | 同上 | 同上 |

候选拉取后的过滤（`gateway_service.go:2656`、`scheduler_snapshot_service.go:695`）：现有 `acc.Platform==antigravity && !IsMixedSchedulingEnabled() → skip` 之外，新增 `acc.Platform==kiro && !accountEligibleForMixedPlatform(acc, platform) → skip`。复用 `accountEligibleForMixedPlatform`，让 gemini 池天然排除 kiro。

仓储方法 `ListSchedulableByGroupIDAndPlatforms` 已支持多平台（`account_repo.go:1109`），无需新增。

### 1.3 准入判定收口（`gateway_service.go:2726`）

`isAccountAllowedForPlatform(account, platform, useMixed)`：
```
if !useMixed { return account.Platform == platform }
if account.Platform == platform { return true }
return accountEligibleForMixedPlatform(account, platform)   // 取代原 antigravity 专属判断
```
此函数是所有选号路径（sticky-hit、routing、轮转、`selectAccountWithMixedScheduling`）的统一关卡，改这一处即全链路生效。

### 1.4 快照重建（`scheduler_snapshot_service.go:502`）

`rebuildByAccount`：现有"antigravity+mixed → 重建 anthropic/gemini 桶"之外，新增"kiro + `IsKiroMixedSchedulingEnabled()` → 重建 **anthropic** 桶"。保证 mixed kiro 账号增删改时，anthropic 分组快照及时刷新。

## Section 2 — 账号级 Kiro 配置（行为保真）

kiro 账号在 anthropic 分组里服务时，4 个 group-level 配置因 `isKiroGroup(anthropic组)==false` 会退化为默认（Q 端点、无缓存模拟、无 auto-sticky、默认 TTL）。改为账号级以保真。

### 2.1 账号读取方法（`account.go`，读 `Extra`，键名复用 group 同名）

- `KiroEndpointMode() string` → `Extra["kiro_endpoint_mode"]`，默认 `q`，仅接受 `q`/`krs`
- `KiroCacheEmulationEnabled() bool` / `KiroCacheEmulationRatio() float64`
- `KiroAutoStickyEnabled() bool`
- `KiroStickySessionTTLSeconds() int`（0 → 默认 `DefaultKiroStickySessionTTLSeconds`）

### 2.2 统一 resolver（组优先、账号兜底）

新增 4 个 resolver，同时接收 `account` 与 `group`，是所有消费点的唯一入口：

```
resolveKiroEndpointMode(account, group):
    if isKiroGroup(group):               return group.EffectiveKiroEndpointMode()   // 原生 kiro 组：行为一字不变
    if isKiroDirectModeAccount(account): return account.KiroEndpointMode()           // mixed kiro 混入非 kiro 组
    return KiroEndpointModeQ
```
cache emulation（开关 + ratio）、auto-sticky、TTL 同构。**原生 kiro 组路径完全不变**——resolver 第一分支即返回 group 配置。

### 2.3 消费点改造（3 处，均已持有 account）

| 文件:行 | 现状 | 改为 |
|---|---|---|
| `kiro_runtime.go:558` `kiroEndpointModeForRequest(account, parsed)` | `parsed.Group.EffectiveKiroEndpointMode()` | `resolveKiroEndpointMode(account, parsed.Group)`（API Key 强制 Q 的前置判断保留） |
| `kiro_cache_emulation.go:55` `buildKiroCacheEmulationUsage(account, group, …)` | `group.EffectiveKiroCacheEmulationEnabled()` / `group.EffectiveKiroCacheEmulationRatio()` | resolver(account, group) |
| `gateway_service.go:809-838` auto-sticky 块 | 见 Section 3 | 见 Section 3 |

TTL：`stickySessionTTLForGroup` 仅持有 group。改为在 bind 时（`BindStickySessionForGroup`，已持有 account）按 resolver 解析 TTL；保持签名兼容，非 kiro/原生 kiro 组路径不变。

### 2.4 前端（`CreateAccountModal.vue` / `EditAccountModal.vue` / `GroupSelector.vue`）

- kiro 账号也显示 `mixed_scheduling` 开关（目前仅 antigravity 显示）。
- 开关开启时，展开 4 个 kiro 配置项（endpoint mode / cache emulation 开关+ratio / auto-sticky / TTL）——这些目前只在 GroupsView 的 kiro 组里有。写入账号 `extra`。
- `GroupSelector.vue:95`：kiro + mixed 时放行选 **anthropic** 分组（**不含 gemini**）；与 antigravity（anthropic+gemini）区分。
- i18n：复用现有 `mixedScheduling*` 文案，按需补 kiro 专属提示。

## Section 3 — auto-sticky 稳定 hash（方案 A）

### 3.1 问题

`GenerateSessionHash`（`gateway_service.go:753`）是优先级阶梯。档位 0/1/1.5/2（显式 session header、metadata.user_id、body session id、cache_control ephemeral）**平台无关、最先命中**，命中即得跨轮稳定 hash → 任何账号都钉得住，**A/B 无差异**。

只有**裸客户端**（以上全不送）才掉到：档位 2.5（仅 kiro 组的 system-prompt 稳定 hash，门控 `isKiroGroup`）或档位 3（fallback：含全部 messages → 每轮漂移 → 钉不住）。已查证 `FindAnthropicSession` 摘要链主链路**未启用**，无第二安全网。故混合 anthropic 组里的裸客户端在不改的情况下钉不住 kiro 账号。

### 3.2 改法（最小化、限定混合组）

引入分组级信号 `Group.HasMixedKiroAccounts bool`——"该组含至少一个可调度、且开启 mixed_scheduling 的 kiro 账号"。在 **auth-cache 构建分组快照时计算**（`api_key_auth_cache_impl.go`，与现有 `EffectiveKiro*` 快照同处），hash 阶段零额外查询（`parsed.Group` 即该快照）。

放宽档位 2.5 门控：`isKiroGroup(parsed.Group) || parsed.Group.HasMixedKiroAccounts`。命中时用 system-prompt/首条 user 消息 + APIKeyID 的稳定 hash。auto-sticky 开关判定用 resolver（账号级，见 2.2）；混合组语义为"组内 mixed kiro 账号 auto-sticky 已启用"。

### 3.3 影响边界（对 anthropic 账号）

- 仅作用于 `HasMixedKiroAccounts==true` 的分组；纯 anthropic 组、不含 mixed kiro 的组**一字不变**。
- 仅改**档位 3 裸客户端**：路由 hash 从"全-messages（每轮漂）"换成"system-prompt 稳定 hash"——对 anthropic 裸客户端是**改进**（粘性更稳）。
- 送会话标识的客户端（含 Claude Code，走档位 0/1/1.5/2）**完全不受影响**。
- 纯本地路由 key：不进上游请求、不碰 UA/指纹/请求体构造。

## 数据流

1. 用户用原 anthropic key 请求 `/v1/messages`。鉴权按 `GroupBindingMode` 解析到 anthropic 默认分组（不变）。
2. `GenerateSessionHash`：送会话标识 → 档位 0/1/1.5/2（不变）；裸客户端 + 该组 `HasMixedKiroAccounts` → 档位 2.5 稳定 hash（Section 3）。
3. `SelectAccountWithLoadAwareness` → platform=anthropic、useMixed=true → `selectAccountWithMixedScheduling`。候选池 = anthropic + 启用 mixed 的 antigravity + 启用 mixed 的 kiro（Section 1）。
4. 选号：sticky-hit 优先（经 `isAccountAllowedForPlatform` 放行 mixed kiro）；否则按 priority + 最久未用轮转，三平台账号平等竞争。
5. 转发分流（`Forward`，不变）：
   - 选中 anthropic 账号 → anthropic 上游，自身 UA/指纹，发往 `api.anthropic.com`。
   - 选中 kiro 账号 → `forwardKiroMessages`，endpoint mode / cache emulation 由 **账号级 resolver** 解析（Section 2），KiroIDE UA 发往 kiro 端点。
6. 绑定粘性会话（`buildSessionKey(groupID, sessionHash)`），TTL 按 resolver 解析。

## 跨平台 failover 行为契约

- **同一对话默认钉在同一账号**：sessionHash 内容稳定 → sticky 绑定 → 后续轮复用同一账号。不会逐轮抖动。
- **跨平台切换仅在 failover 发生**：绑定账号本轮不可用（429/529/error/不可调度/槽满/TTL 过期）才从池中重选，可能命中另一平台。`maxAccountSwitches` 默认 10。
- **切换不破坏请求/指纹**：Anthropic API 与 Kiro 均无状态（每轮重放完整 Claude body）。每个请求对其所用账号自洽（自己的 UA/指纹/目标 URL）。各路径各建出站请求，**无交叉污染**。每次 failover `CloneForBody` 从原始 body 重新克隆（`gateway_handler.go:601`）。
- **跨平台切换的真实代价**（非正确性）：(a) 该轮 prompt 缓存变冷（同平台换号亦如此）；(b) 该轮计费落到所用账号平台的单价；(c) 同模型 Anthropic 直连 vs Kiro 转译的边缘差异。

## 不变性保证（对混合组内 anthropic 账号）

- 转发链路、UA、指纹、请求体构造、OAuth/Claude Code mimicry、计费、模型映射、选号算法本身：**零改动**。
- 账号级 kiro resolver 对 anthropic 账号永远短路（`isKiroGroup`/`isKiroDirectModeAccount` 均 false）。
- 候选池按设计扩大是功能本身——仅在管理员手动给该组加入 mixed kiro 账号时发生；不加则查询返回 0 个 kiro，行为与今日一致。
- auto-sticky 选 A 唯一触碰 anthropic 共享代码：仅 `HasMixedKiroAccounts` 组、仅裸客户端、仅本地路由 key、且为粘性改进。

## 错误处理

- mixed kiro 账号在 anthropic 池中复用现有 kiro 冷却/分类逻辑（`isKiroRuntimeSchedulable`、`tryRecoverKiroCooldownPool` 已按 useMixed 传参，`gateway_service.go:2350`）。429/月度配额触发 kiro cooldown，调度器跳过，failover 到其它账号。
- 候选池查询/重建失败不影响纯 anthropic 选号（mixed 仅是平台列表追加）。

## 测试计划

**后端单测**
- `accountEligibleForMixedPlatform`：kiro+mixed → anthropic 放行 / gemini 拒绝；kiro 未开 mixed → 拒绝；antigravity 维持 anthropic+gemini。
- `listSchedulableAccounts` / `loadAccountsFromDB`：anthropic 混合池含 mixed kiro；gemini 池排除 kiro；非 mixed kiro 被过滤。
- `isAccountAllowedForPlatform`：mixed kiro 在 anthropic+useMixed 放行；强制平台模式拒绝。
- `rebuildByAccount`：mixed kiro 账号变更重建 anthropic 桶、不重建 gemini。
- resolver：原生 kiro 组取 group 配置；mixed kiro 混入 anthropic 组取账号配置(KRS+缓存);anthropic 账号短路。
- `GenerateSessionHash`：`HasMixedKiroAccounts` 组 + 裸客户端 → 稳定 hash；送 session 标识 → 档位 0/1/1.5/2 不变；纯 anthropic 组 → 档位 3 不变。

**回归（关键）**
- 混合组里选中 anthropic 账号 → 出站请求**不含**任何 KiroIDE/aws-sdk 头、发往 `api.anthropic.com`。
- `kiro_reference_alignment_test.go` 等基于源码文本断言的测试不被破坏。

**前端**
- kiro 账号显示 mixed 开关 + 4 配置项;GroupSelector kiro+mixed 只列 anthropic 组;typecheck。

**验证**：`make build` / 后端 `go test ./internal/service/... ./internal/repository/...`、前端 typecheck。

## 上线注意

- 给某 kiro 账号开 mixed_scheduling 时，需在新 UI 填其 kiro 配置（endpoint mode 等）。建议从该账号当前所属 kiro 组的配置预填，避免遗漏导致退化到 Q。
- 正式服现状：kiro 组 `test`(id8) = KRS+缓存模拟+auto-sticky;anthropic 账号 ×4 当前 error。灰度时先在非默认 anthropic 组验证，再切默认组。
- 无 DB schema 变更（复用 `accounts.extra`、`groups` 既有 kiro 字段）。
