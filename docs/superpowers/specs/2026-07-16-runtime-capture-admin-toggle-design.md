# 会话转存运行时开关 + 分组/用户过滤 设计文档

- 日期: 2026-07-16
- 状态: 已评审待实现
- 关联既有特性: `gateway.capture`（上游模型调用全量归档 → ClickHouse）

## 1. 目标

把「是否转存（capture）」从**静态 config/环境变量**决定，改为**管理员后台运行时**决定，并支持按**用户分组**和**用户（按邮箱选择）**两个维度过滤。ClickHouse 连接串仍留在 `config.yaml`（基础设施密钥，不进业务 DB）。

## 2. 背景与现状

capture 现在在三个层面读静态 `config.gateway.capture.enabled`：

1. **建资源**：`service.NewConversationCapturePool(cfg)` 启动时执行。`enabled=false` → pool 为 `nil`，不建 ClickHouse 连接。
2. **采集响应体的门**：SSE tee（`gateway_upstream_response.go` 流式）/ 非流式快照仅在 `enabled=true` 时分配。
3. **提交的门**：所有埋点判断 `s.capturePool != nil && s.cfg.Gateway.Capture.Enabled`。

覆盖平台（当前埋点实际分布）：Anthropic 原生（成功/错误/流式）、KIRO（成功/错误）、OpenAI 原生 Responses passthrough。**Gemini 原生路径无埋点**（本次不改）。

正式服现状：config 无 capture 段 → `enabled` 默认 false → pool=nil，无 ClickHouse 容器。

已验证的可复用基建：

- 运行时 DB 设置 + 进程内缓存范式：`SettingService.GetGatewayForwardingSettings`（`atomic.Value` 缓存 + 60s TTL + `singleflight` + DB 错误短 TTL 兜底）。写入后由 `refreshCachedSettings` 统一 `SF.Forget` + `Store` 立即失效。
- `GatewayService`、`GatewayHandler`、`OpenAIGatewayHandler` 均已持有 `*SettingService`；KIRO 走 `GatewayService` 内部方法。
- 前端用户选择器 `OpenAIFastPolicyUserSelector.vue`：按邮箱搜索 `adminAPI.usage.searchUsers` → 选真实用户 → 对外存 `user_id[]`；热路径按 user_id 匹配（`openAIFastPolicyUserMatches`）。
- 前端分组选择器 `GroupSelector.vue`。
- Setting key 常量集中在 `backend/internal/service/domain_constants.go`。

## 3. 功能需求

1. 仅当管理员在后台开启「会话转存」总开关时，系统才转存。
2. 可选过滤：**用户分组**（多选）+ **用户**（多选，UI 按邮箱搜索选择）。
3. 过滤只能从系统已有的真实分组/用户里选，**禁止管理员自由填文本**（复用现有选择器组件保证）。
4. 组合语义 **AND**：两个维度都设值时，请求须同时命中；任一为空则只看另一个；两个都空 = 全量转存。
5. 管理员入口位于「系统设置 → 通用设置」tab 内，现有内容下方另起一行（新卡片）。
6. 关闭时对转发热路径零成本；开启时开销可忽略。

## 4. 非目标（Non-Goals）

- 不给 Gemini 原生 / OpenAI messages→Anthropic 桥接路径新增 capture 埋点（维持现状，属另一 scope）。
- 不把 ClickHouse 连接串迁到后台（保持 config.yaml）。
- 不做本地磁盘 spool（config 里预留的 A2，不在本次）。
- 不改动 capture 的字节预算、drop/sample、批量写、脱敏等既有归档链路机制。
- 不对分组/用户做「转存历史迁移」——开关只影响开启后的新流量。

## 5. 架构：Provisioning 与 Runtime 分离

核心是把「资源已就绪」和「当前是否采集」拆开：

- **Provisioning（建资源）**：仍由 `config.gateway.capture.enabled` 控制。语义调整为「把采集子系统建起来」——连 ClickHouse、建表、起 worker，pool 常驻空转。运维在部署 ClickHouse 时一次性设置。
- **Runtime（是否采集 + 过滤）**：新增三个后台设置，管理员随时改，60s 缓存 + 保存即刷新。

向后兼容：`enabled=false`（正式服现状）→ pool=nil → 全链路短路，行为零变化。

「临时拉容器」落地流程：① 起 ClickHouse 容器 + config 填 `addr` 并设 `enabled: true`；② 重启进程建资源；③ 之后管理员在后台随时开关/调过滤，**无需再重启**。

## 6. 数据模型 / Setting Key

在 `domain_constants.go` 新增（存于现有 key-value settings 表，**无需 migration**）：

```go
SettingKeyCaptureRuntimeEnabled  = "capture_runtime_enabled"   // "true"/"false"，默认 false
SettingKeyCaptureRuntimeGroupIDs = "capture_runtime_group_ids" // JSON int64 数组，如 "[1,2]"，默认 "[]"
SettingKeyCaptureRuntimeUserIDs  = "capture_runtime_user_ids"  // JSON int64 数组，如 "[10,20]"，默认 "[]"
```

用户维度存 **user_id**（不是 email）：UI 按邮箱选择真实用户后落 id，与 `OpenAIFastPolicy` 完全一致。

## 7. 运行时读取 + 缓存（热路径核心）

新增 `SettingService.GetCaptureRuntime(ctx) CaptureRuntimeSettings`，结构与实现照搬 `GetGatewayForwardingSettings`：

```go
type CaptureRuntimeSettings struct {
    Enabled  bool
    GroupIDs map[int64]struct{} // 空 = 该维度不过滤
    UserIDs  map[int64]struct{}
}
```

- 进程内 `atomic.Value` 缓存 + 60s TTL + `singleflight`（key `"capture_runtime"`）防击穿。
- DB 读失败：缓存兜底 `Enabled=false`（fail-closed，不采集），短 TTL（5s）快速重试，不阻塞转发。
- 写入失效：在 `refreshCachedSettings` 内加 `captureRuntimeSF.Forget("capture_runtime")` + `Store(...)`，管理员保存后立即生效。
- 解析：`group_ids`/`user_ids` 用 `json.Unmarshal` 成 `[]int64` 后转 `map[int64]struct{}`；解析失败视为空集合（不过滤该维度）并 warn。

匹配逻辑（AND 语义，纯函数，便于单测）：

```go
func (r CaptureRuntimeSettings) Matches(groupID *int64, userID int64) bool {
    if !r.Enabled {
        return false
    }
    if len(r.GroupIDs) > 0 { // 分组维度启用
        if groupID == nil {
            return false
        }
        if _, ok := r.GroupIDs[*groupID]; !ok {
            return false
        }
    }
    if len(r.UserIDs) > 0 { // 用户维度启用
        if _, ok := r.UserIDs[userID]; !ok {
            return false
        }
    }
    return true // 两个都空 = 全量
}
```

## 8. 热路径接入点

统一 helper（`GatewayService` 与两个 handler 各一份薄封装，因它们都持有 pool + settingService）：

```go
func (s *GatewayService) shouldCapture(c *gin.Context) bool {
    if s.capturePool == nil { // 未建资源，零成本短路
        return false
    }
    rt := s.settingService.GetCaptureRuntime(c.Request.Context())
    gid, uid := captureIdentityFromGin(c) // 取 apiKey.GroupID / subject.UserID
    return rt.Matches(gid, uid)
}
```

替换现有三类 gate：

- **tee 分配**（`gateway_upstream_response.go` 流式/非流式）：`if s.cfg...Enabled` → `if s.shouldCapture(c)`。**过滤不中的请求连响应体都不采集**（省去 `sseTee` 逐行拷贝 / 非流式快照 / 字节预算），是关键降本点。
- **submit 埋点**（5 处：Anthropic 成功/错误、KIRO 成功/错误、OpenAI Responses）：同样换 `shouldCapture`。
- **handler 层**（`gateway_handler.go` / `openai_gateway_handler.go`）：现有 `h.capturePool != nil && result.CaptureResponse != nil` 补一层 `h.shouldCapture(c)`（tee 已被门住 → `CaptureResponse` 本就为空，此处为稳妥双保险）。

`captureIdentityFromGin` 从 gin.Context 取身份：`apiKey := middleware.GetAPIKeyFromContext(c)` → `apiKey.GroupID`；`subject := middleware.GetUserFromContext(c)` → `subject.UserID`。二者在 capture 决策点均已在手。

## 9. Provisioning 生命周期

`NewConversationCapturePool(cfg)` 逻辑不变：

- `config.gateway.capture.enabled=false`（正式服现状）→ 返回 `nil` → `shouldCapture` 恒 false，**完全向后兼容**。
- `enabled=true` → 照旧建 CH 连接 + 建表 + 起 worker；CH 建连失败仍降级 `noopArchiveWriter`（pool 在但不落库，不阻塞启动/转发）。

`config` 语义在示例/文档中重述为「provision 采集子系统（是否真正采集由后台控制）」。`MaxBodyBytes` 等仍从 config 读。

## 10. Admin API + 前端

### 后端

- `admin/setting_handler.go` 更新 payload 增加指针字段（部分更新语义）：
  - `CaptureRuntimeEnabled *bool`
  - `CaptureRuntimeGroupIDs *[]int64`
  - `CaptureRuntimeUserIDs *[]int64`
- 校验：仅做格式/去重/非负校验（负数、非整数拒绝），存在性由前端选择器保证（与 `OpenAIFastPolicy` 一致，不做服务端存在性查询）。写入 `SetMultiple`（group/user 以 `json.Marshal` 存字符串），随后触发缓存刷新。
- `GetAllSettings` / `SystemSettings` DTO 回显三项当前值，供前端表单初始化。

### 前端

- 位置：`SettingsView.vue` 的 `general`（通用设置）tab 内，现有内容下方**另起一行**新增「会话转存」卡片（`<div class="card">`）。
- 组成：一个总开关（沿用现有 toggle 样式）+ 两个多选器：
  - 分组：复用 `components/common/GroupSelector.vue`，绑定 `capture_runtime_group_ids`。
  - 用户：复用 `settings/OpenAIFastPolicyUserSelector.vue`（按邮箱搜索、存 user_id），绑定 `capture_runtime_user_ids`。
- `stores/adminSettings.ts` / `api/admin/settings.ts` 增加三字段读写。
- i18n：`locales/zh/admin/settings.ts` 与 `locales/en/admin/settings.ts` 各加卡片标题、开关、两选择器 label 与说明（含「留空=不过滤 / 两者 AND」提示）。

## 11. 错误处理与边界

- 设置读 DB 失败 → fail-closed（不采集）+ 短 TTL 重试，不影响转发。
- `enabled=true` 但 CH 不可用 → 现有降级机制不变。
- 缓存与写入竞态 → 沿用既定 `SF.Forget` 再 `Store` 顺序。
- group/user JSON 脏数据 → 视为空集合并 warn，不 panic。
- 管理员改设置后，最坏 60s 生效上限已被「保存即刷新」规避，正常即时生效。

## 12. 性能分析

- **关时**：`capturePool==nil` 直接短路，与现状完全一致，**零成本**。
- **开时热路径**：一次缓存 bool 读（纳秒级原子 load，无 DB）+ 命中过滤才做的两次 int64 map 查表（O(1)）。不命中过滤的请求**省去响应体采集**，比现状更省。
- **重活**（字节拷贝、抽列、CH 批量 INSERT）仍在异步 worker 池、drop-safe、绝不反压转发主路径——机制不动。
- 常驻成本：`enabled=true` 且当前「关着」时，握着若干空闲 CH 连接（可接受，换取随时可开）。

结论：对转发性能影响可忽略；过滤还能进一步降低采集量。

## 13. 文件改动清单

后端：
- `backend/internal/service/domain_constants.go`：新增 3 个 setting key。
- `backend/internal/service/setting_service.go`：`CaptureRuntimeSettings` 类型 + `GetCaptureRuntime` + 缓存字段/常量 + `refreshCachedSettings` 挂钩 + update 组装。
- `backend/internal/service/gateway_service.go`：`shouldCapture` + `captureIdentityFromGin`。
- `backend/internal/service/gateway_upstream_response.go`、`kiro_runtime.go`、`openai_gateway_passthrough.go`：tee/submit gate 换 `shouldCapture`。
- `backend/internal/handler/gateway_handler.go`、`openai_gateway_handler.go`：handler 层 gate 补 `shouldCapture`。
- `backend/internal/handler/admin/setting_handler.go` + `dto`：payload/response 三字段 + 校验。

前端：
- `frontend/src/views/admin/SettingsView.vue`：general tab 新增卡片。
- `frontend/src/stores/adminSettings.ts`、`frontend/src/api/admin/settings.ts`：三字段。
- 复用 `GroupSelector.vue`、`OpenAIFastPolicyUserSelector.vue`。
- `frontend/src/i18n/locales/{zh,en}/admin/settings.ts`：文案。

配置/文档：
- `deploy/config.example.yaml`：capture 段注释重述 `enabled` 语义（provision 而非「采集中」）。

## 14. 测试计划

- 单元：`Matches()` 全组合（关/开、单维度、双维度 AND、空=全量、groupID nil、user 命中/不命中）。
- 缓存：命中/过期/singleflight/DB 错误短 TTL（照 `GetGatewayForwardingSettings` 测试写法）；`refreshCachedSettings` 保存即失效。
- 设置解析：`group_ids`/`user_ids` 正常/脏数据/空。
- 集成：`shouldCapture` 在 tee 分配前生效（过滤不中 → 不采集 body）；关时全链路零采集（守向后兼容）；admin update → 校验 → 落库 → 缓存刷新。
- 回归：`enabled=false` 时 pool=nil、无任何采集。
