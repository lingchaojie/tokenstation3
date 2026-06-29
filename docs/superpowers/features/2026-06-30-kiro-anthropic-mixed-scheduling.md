# Kiro ↔ Anthropic 混合调度

> **状态**:已上线 · **日期**:2026-06-30 · **分支**:`dev` → `release`
> **Spec**:[`../specs/2026-06-30-kiro-anthropic-mixed-scheduling-design.md`](../specs/2026-06-30-kiro-anthropic-mixed-scheduling-design.md)
> **Plan**:[`../plans/2026-06-30-kiro-anthropic-mixed-scheduling.md`](../plans/2026-06-30-kiro-anthropic-mixed-scheduling.md)

---

## 一句话功能

让 kiro 账号通过 `mixed_scheduling` 开关加入 **anthropic 分组**,与 anthropic 账号在同一调度池内竞争。用户用**原 anthropic key、无任何客户端改动**,即可被无感路由到 kiro 账号——实现"用 kiro 配额服务 anthropic 流量"。

## 设计契约(不变量)

无论何时调用何路径,以下五条必须成立:

1. **kiro 只混入 anthropic,绝不混入 gemini**。kiro 只服 Claude 模型;调度准入、分组选择、桶重建三处均双重校验。
2. **原生 kiro 组(`platform=kiro`)行为零变化**。所有 resolver 第一分支取 group 配置,native-kiro 路径字节级等价于改动前。
3. **混合组内的 anthropic 账号完全不受影响**。转发链路(`Forward`)按 `isKiroDirectModeAccount(account)` 分流;anthropic 账号永远不会被 `forwardKiroMessages` 处理,不会被注入任何 kiro 头(KiroIDE/X-Amz-User-Agent 等)。
4. **kiro 专属配置随账号走**。endpoint mode (q/krs)、cache emulation、auto-sticky、TTL 优先取分组配置,kiro 账号混入非 kiro 分组时使用账号自身 `Extra` 配置。
5. **请求模型必须在 kiro 账号 `model_mapping` 白名单内**才能调度到该 kiro 账号。空映射 = 全拒。命中失败时:
   - 调度层:跳过该 kiro 账号,请求无感落到 anthropic。
   - 转发层(兜底):返回 **HTTP 400 `invalid_request_error`**,不透传上游,不触发同账号 failover 重试。

---

## 管理员启用步骤

### 1. 配置 kiro 账号(账号编辑弹窗,需勾选 `mixed_scheduling`)

| 字段 | Extra 键名 | 说明 |
|---|---|---|
| Mixed Scheduling 开关 | `extra.mixed_scheduling` | 必勾。开后该 kiro 账号可被加入 anthropic 分组。 |
| Endpoint Mode | `extra.kiro_endpoint_mode` | `q`(AWS Q,默认)或 `krs`(Kiro Runtime Service,独立限流池)。 |
| Cache Emulation Enabled | `extra.kiro_cache_emulation_enabled` | 开关 prompt 缓存计费补偿。 |
| Cache Emulation Ratio | `extra.kiro_cache_emulation_ratio` | `[0,1]`,前端会做归一化。 |
| Auto Sticky Enabled | `extra.kiro_auto_sticky_enabled` | 默认 true。控制裸客户端是否走稳定 hash。 |
| Sticky Session TTL Seconds | `extra.kiro_sticky_session_ttl_seconds` | `[60, 86400]`,默认 3600。 |
| Model Mapping | `credentials.model_mapping` | **必须**配置。空映射 = 全拒。映射示例:`{"claude-sonnet-4-5-20250929": "claude-sonnet-4.5"}`。 |

> ⚠️ **`model_mapping` 不配 = 该 kiro 账号完全不可调度**(无论原生组还是混合组)。这是 2026-06-30 起的语义变更;正式服两个 kiro 账号都已配齐,无影响。

### 2. 把 kiro 账号绑定到 anthropic 分组

在 GroupSelector 里,勾选 `mixed_scheduling` 的 kiro 账号**只能**选择 `kiro` 或 `anthropic` 分组(绝不出现 gemini)。前端会自动过滤。

### 3. 现有用户**零感知**生效

用户的 anthropic API key 仍按 `GroupBindingMode`(`default_follow` / `auto` / 静态)解析到原 anthropic 默认分组。该分组的调度池现在包含 anthropic + 启用 mixed 的 antigravity + 启用 mixed 的 kiro 账号。

---

## 调度行为

### 平等优先级

mixed kiro 账号与 anthropic 账号在池内**平等竞争**,按现有 `account.priority`(越小越优先)+ 最久未用轮转。**未引入分层、未引入 platform-preference**。如需让 kiro 作为溢出兜底,运营可调高 kiro 账号的 `priority` 数值——零代码改动。

### 粘性会话

- 客户端送会话标识(`X-Session-Id` / `metadata.user_id: ..session_xxx..` / body `conversation_id` 等)→ 会话内全程钉同一账号,不在 anthropic/kiro 之间跳。**Claude Code 等主流客户端走这一路。**
- 裸客户端 + 含 mixed-kiro-auto-sticky 的混合分组 → 走 system-prompt 稳定 hash(由 `Group.HasMixedKiroAutoStickyAccount` 标记触发)。该标记在 auth-snapshot 构建时计算,bounded-stale 但 benign(只影响优化,不影响正确性)。
- 纯 anthropic 分组(无 mixed kiro)+ 裸客户端 → 维持原 fallback hash(每轮可变,跟改动前一致)。

### 跨平台 failover

**正常情况下不会发生**——粘性绑定钉住账号。只有绑定账号当前不可用(429/529/error/不可调度/槽满/TTL 过期)才从池中重选,可能命中另一平台。 `maxAccountSwitches` 默认 10。

**跨平台 failover 不会破坏请求**:Anthropic API 和 Kiro 均无状态(每轮重放完整 Claude body)。每个请求对其所用账号都自洽——anthropic 用 anthropic 自己的 UA/指纹/`api.anthropic.com`;kiro 用 KiroIDE UA/kiro 端点。两条出站请求路径各自独立构造,**结构上无交叉污染**。每次 failover 都从原始 body `CloneForBody` 重新克隆。

**跨平台 failover 的真实代价**(非正确性问题):
- 该轮 prompt 缓存变冷(同平台换号同样如此)。
- 该轮计费落到该轮所用账号的平台单价。
- 同一 Claude 模型在 Anthropic 直连 vs Kiro 转译时可能有极小行为差异。

---

## 模型白名单(2026-06-30 强化)

### 准入层(优先,无感)

`isModelSupportedByAccount` 对 kiro 账号严格走 `account.SupportsModelInMapping(model)`——必须命中账号 `model_mapping`(支持通配符 + 归一化)才放行。不命中 → 调度器跳过该 kiro 账号 → 请求无感落到 anthropic(若池中有 anthropic 能服务该模型)。

### 转发层兜底

即使 sticky 把请求钉到了不应处理该模型的 kiro 账号,`forwardKiroMessages` 入口会再校验一次。不命中 → 返回 `*KiroModelNotSupportedError`,handler 映射为 **HTTP 400 `invalid_request_error`**:

```json
{
  "error": {
    "type": "invalid_request_error",
    "message": "kiro account 42 does not support model \"claude-future-99\" (not in account model_mapping)"
  }
}
```

不进 failover 重试同账号,不透传上游。

### 空映射告警

`SupportsModelInMapping` 检测到 kiro 平台账号 + 空映射 + 请求被拒时,会发 `slog.Warn`:
```
"kiro_account_empty_model_mapping" account_id=<id> requested_model=<name>
```
便于运维诊断"明明请求了 anthropic 默认组,为什么池里的 kiro 账号一直没分到流量"。

---

## 监控与排错

### 关键日志

| 关键字 | 含义 |
|---|---|
| `account_scheduling_list_mixed` | 混合调度选号开始,记录候选池大小(含 kiro/antigravity 数量) |
| `sticky.hash_source: kiro_system_prompt` / `kiro_first_user_message` | 走稳定 hash(原生 kiro 组 或 含 mixed-kiro 的混合组) |
| `sticky.hash_source: kiro_auto_sticky_disabled` | 原生 kiro 组 auto-sticky 关闭 → 返回空 hash |
| `kiro_account_empty_model_mapping` | kiro 账号空映射,请求被准入层拒 |
| `compute_has_mixed_kiro_auto_sticky_failed` | 计算 `HasMixedKiroAutoStickyAccount` 时 DB 查询失败(benign,fallback) |
| `KiroModelNotSupportedError` | 转发层模型白名单拒绝(400) |

### 排错速查

- **"请求都打到 anthropic,kiro 账号没动":** 检查 kiro 账号是否勾了 `mixed_scheduling` 且加入了 anthropic 分组,以及 `model_mapping` 是否覆盖了请求模型。
- **"裸客户端跨轮换账号":** 看分组是否有 schedulable + auto_sticky 开启的 mixed kiro 账号(`HasMixedKiroAutoStickyAccount` 是否为 true);auth 缓存 TTL 内有 bounded-stale。
- **"客户端收到 400 invalid_request_error / kiro account does not support model":** kiro 账号 `model_mapping` 没覆盖该模型;新增映射或确保池里有 anthropic 兜底。

---

## 实现要点(给后续维护者)

### 后端核心文件

| 文件 | 改动 |
|---|---|
| `internal/service/account.go` | `IsKiroMixedSchedulingEnabled`、`accountEligibleForMixedPlatform`、`mixedSchedulingPlatforms`、`KiroEndpointMode` 等 5 个账号配置读取方法、`SupportsModelInMapping` |
| `internal/service/gateway_service.go` | `listSchedulableAccounts` useMixed 分支用 `mixedSchedulingPlatforms` 拉候选;`isAccountAllowedForPlatform` 收口 kiro 准入;`isModelSupportedByAccount` 加 kiro 严格分支;`GenerateSessionHash` 档位 2.5 放宽到混合组;`stickySessionTTLForAccountGroup` + `BindStickySessionForAccountGroup` 账号级 TTL |
| `internal/service/scheduler_snapshot_service.go` | `rebuildPlatformsForMixedAccount`、`loadAccountsFromDB` 镜像 gateway 路径 |
| `internal/service/kiro_config_resolver.go` | 新增。三个 resolver:`resolveKiroEndpointMode` / `resolveKiroCacheEmulation` / `resolveKiroStickySessionTTLSeconds`。group-first, account-fallback。 |
| `internal/service/kiro_runtime.go` | `forwardKiroMessages` 入口加严格 model_mapping 守卫;`kiroEndpointModeForRequest` 走 resolver |
| `internal/service/kiro_cache_emulation.go` | `buildKiroCacheEmulationUsage` 走 resolver |
| `internal/service/group.go` | `Group.HasMixedKiroAutoStickyAccount bool` 字段 |
| `internal/service/api_key_auth_cache*.go` | snapshot 增 kiro 字段 + 计算 `HasMixedKiroAutoStickyAccount`(EXISTS 查询) |
| `internal/repository/group_repo.go` | `HasSchedulableMixedKiroStickyAccount` SQL(JSONB extract) |
| `internal/handler/gateway_handler.go` | `KiroModelNotSupportedError` 映射为 400;两处 sticky bind 改用 account-aware |

### 前端核心文件

| 文件 | 改动 |
|---|---|
| `src/components/account/CreateAccountModal.vue` | kiro 平台显示 `mixed_scheduling` 开关 + 4 项 kiro 配置;`buildKiroMixedExtra` 含 TTL/ratio 归一化 + endpoint mode 白名单守卫 |
| `src/components/account/EditAccountModal.vue` | 同上,加 backfill on load |
| `src/components/common/GroupSelector.vue` | kiro + mixed 时只列 `kiro` / `anthropic` 分组(绝不含 gemini) |

---

## 已知取舍(非 bug)

1. **平等调度,容量无感**:kiro 通常配额比 anthropic 紧,高负载下会更早触发 kiro 冷却 + failover(多一点延迟)。运营可用 `account.priority` 调整。
2. **裸客户端稳定 hash 在混合组里也生效**:对裸客户端,同一 system prompt 的不同对话会撞到同一账号。Claude Code 等送 session id 的客户端不受影响。
3. **bounded-stale `HasMixedKiroAutoStickyAccount`**:auth 缓存 TTL 内的 stale。短暂窗口内的稳定-hash 路径切换是无害的(粘性绑定仍然有效)。

---

## 提交清单(`fec6516d..HEAD`)

```
097a6f26 fix: surface kiro model-mapping rejection as 400 and log empty mapping
92aa3610 feat: enforce strict model_mapping whitelist for kiro accounts
42c984d6 test: regression for mixed kiro/anthropic scheduling isolation
3dd8b76f feat: allow mixed kiro accounts to bind anthropic groups in selector
eef4207d fix: clamp kiro ttl/ratio and guard endpoint mode in account modals
8e0189bc feat: expose kiro mixed-scheduling config in account modals
5c2081e1 test: pin nil-account safety in sticky TTL resolution
3a860f21 feat: resolve sticky TTL from account for mixed kiro scheduling
03370814 test: cover native-kiro regression + mixed fallthrough in session hash
d0f64439 feat: extend kiro stable session hash to mixed anthropic groups
28a06b7b fix: gofmt + EXISTS query + error log for mixed-kiro sticky flag
f9a79148 feat: expose group-level mixed-kiro auto-sticky flag via auth snapshot
514475ac test: pin kiro config resolver group-first invariant + nil-group fallback
90e0ff9f feat: resolve kiro runtime config from account when mixed-scheduled
f14450eb fix: guard account kiro readers on platform; handle string floats
f809ca97 feat: add account-level kiro config readers
064fa858 test: pin kiro-never-gemini invariant in snapshot rebuild
74960c94 feat: rebuild anthropic snapshot bucket for mixed kiro accounts
63e21d82 docs: clarify mixedSchedulingPlatforms default-branch contract
07fc4ff7 feat: admit mixed kiro accounts into anthropic scheduling pool
fd159834 test: strengthen kiro mixed-scheduling eligibility coverage
831c92b8 feat: add kiro mixed-scheduling eligibility helpers
fec6516d docs: plan kiro-anthropic mixed scheduling
8c5c0bf8 docs: design kiro-anthropic mixed scheduling
```
