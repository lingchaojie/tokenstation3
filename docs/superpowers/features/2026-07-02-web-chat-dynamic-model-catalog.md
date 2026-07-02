# Web Chat 动态模型目录与模型广场开放

> **状态**: 已实现 · **日期**: 2026-07-02 · **分支**: `dev`

---

## 背景

首页和模型广场原来展示的是公开模型目录中的静态卡片。这个目录包含价格、provider、上下文窗口、模态等展示信息,但它不是 Web Chat 的实际可用模型列表。

因此出现了一个用户可见问题: 首页仍会展示当前 Web Chat 不支持的模型卡片,例如 Gemini。用户点击后会进入对话入口,但后端实际 Web Chat 目录并没有这些模型,造成"看得到但用不了"。

同时,Web Chat 管理员灰度已经在之前的后端路由测试中移除,但前端仍残留 `isAdmin` 分支: 首页 Web Chat 模型预览、`/models`、`/dashboard/models`、侧边栏模型广场入口仍按管理员逻辑处理。这导致普通登录用户没有完全使用同一套开放入口。

## 目标

1. 首页和模型广场不能展示当前 Web Chat 不可用的模型卡片。
2. 模型广场和 Web Chat 入口面向所有已登录用户开放,不再依赖管理员灰度。
3. 模型卡片继续使用公开 catalog 的展示元数据和价格信息。
4. 模型可用性必须来自后端动态 Web Chat 目录,而不是前端写死 provider/model 黑名单。
5. 首页是公开页面,匿名访问不能触发默认分组和账号模型查询。
6. 登录用户频繁访问首页或 `/chat/models` 时,不能把流量放大成重复默认分组/账号列表查询。

## 数据契约

### `/api/v1/settings/model-catalog`

认证用户可访问。返回模型广场展示元数据:

- provider 名称和排序
- model 卡片字段
- 公开价格
- 上下文窗口和来源
- 模态和能力标签

该接口不读取默认分组,不判断 Web Chat 可用性。实现上返回 `publicModelCatalogModelsSnapshot()`。

管理员路径 `/api/v1/admin/settings/model-catalog` 保留,用于兼容现有后台调用。

### `/api/v1/chat/models`

认证用户可访问。返回 Web Chat 实际可用模型目录。

后端通过 `resolveWebChatCatalog` 构建:

1. 读取 `default_anthropic_group_id` 和 `default_openai_group_id`。
2. 读取对应默认分组下的账号列表。
3. 只保留 active 账号的 `model_mapping`。
4. 按默认分组来源确定 provider:
   - Anthropic 默认分组 -> `provider=anthropic`
   - OpenAI 默认分组 -> `provider=openai`
5. 按模型 family 过滤交叉映射,例如 OpenAI 分组里的 `claude-*` 不会作为 OpenAI 模型暴露。
6. `-thinking` 等变体归一到用户可理解的基础模型,但保留真实 routing key 用于后续 dispatch。

当前动态目录只支持 Anthropic 和 OpenAI。Gemini 未来要接入时,需要新增默认 Gemini 分组设置,并扩展 `webChatDefaultGroupSpecs`。

## 前端数据流

首页登录态和模型广场统一使用:

1. 拉 `/settings/model-catalog` 获取展示元数据。
2. 拉 `/chat/models` 获取 Web Chat 可用模型。
3. 用 `provider + model` 做交集过滤。
4. 只展示同时存在于两边的模型卡片。

匿名首页不调用 `/chat/models` 和 `/settings/model-catalog`,继续使用 `/settings/model-pricing` 展示静态公开价格区块。这样公开首页不会因为匿名流量读取默认分组或账号模型。

## 权限和入口

本次移除了前端残留的管理员灰度分支:

- `/models`: 普通登录用户可访问。
- `/dashboard/models`: 普通登录用户可访问。
- Dashboard 侧边栏显示模型广场入口。
- HomeView 登录态展示 Web Chat 模型预览和模型广场入口。

匿名用户仍然不能访问模型广场页面,因为模型广场现在依赖 `/chat/models`,而 Web Chat 目录是认证用户能力面。

## 后端抗刷缓存

`/chat/models` 的动态目录构建涉及默认分组设置和账号列表读取。为了避免登录用户频繁刷新首页或并发打开模型广场导致 DB 放大,`WebChatService` 增加了进程内短缓存:

- TTL: 30 秒
- cache miss 使用 `singleflight` 合并并发构建
- 只缓存成功结果,错误不缓存
- 返回结果做深拷贝,避免调用方改到缓存对象

同一个缓存也用于发送前的 `resolveWebChatSendCapability`,保证展示目录和发送校验使用同一套短期一致的动态目录。

缓存的新鲜度取舍: 默认分组或账号 `model_mapping` 更新后最多 30 秒内生效。这个延迟只影响模型列表展示和发送前模型校验,不会放宽实际账号调度限制。

## 主要实现文件

| 文件 | 说明 |
|---|---|
| `backend/internal/server/routes/user.go` | 新增认证用户 `/settings/model-catalog` 路由 |
| `backend/internal/handler/model_catalog.go` | 更新 model catalog 注释,明确普通认证用户和管理员路径共用 |
| `backend/internal/service/web_chat_service.go` | `/chat/models` 动态目录增加 30 秒 TTL + singleflight 缓存 |
| `backend/internal/service/web_chat_catalog_dynamic_test.go` | 覆盖目录缓存和并发 singleflight 行为 |
| `backend/internal/handler/web_chat_handler_test.go` | 覆盖普通用户可访问 model catalog 路由 |
| `frontend/src/api/settings.ts` | model catalog API 改为普通认证路径 |
| `frontend/src/utils/modelCatalog.ts` | 新增按 Web Chat 动态模型过滤 catalog 的工具 |
| `frontend/src/views/HomeView.vue` | 首页登录态使用动态模型目录,匿名态保留静态 pricing |
| `frontend/src/components/models/ModelCatalog.vue` | 模型广场用 `/chat/models` 过滤公开 catalog |
| `frontend/src/router/index.ts` | `/models` 和 `/dashboard/models` 取消管理员权限要求 |
| `frontend/src/components/layout/AppSidebar.vue` | 普通用户侧边栏展示模型广场入口 |

## 验证

本次变更使用以下验证命令:

```bash
pnpm test:run src/views/__tests__/HomeView.spec.ts src/components/models/__tests__/ModelCatalog.spec.ts src/router/__tests__/guards.spec.ts src/components/layout/__tests__/AppSidebar.spec.ts
pnpm typecheck
go test -count=1 ./internal/service
go test -count=1 ./internal/handler -run 'Test(WebChatRoutesAreRegisteredForRegularUsers|ModelCatalogRouteIsRegisteredForRegularUsers)'
go test -count=1 ./internal/server/routes -run 'TestAuthRoutesDoNotExposeModelCatalog'
git diff --check
```

## 维护注意事项

1. 不要在前端写死过滤 Gemini/Qwen/DeepSeek 等 provider。展示是否可用应由 `/chat/models` 决定。
2. 新 provider 接入 Web Chat 时,先在后端动态目录支持该 provider 的默认分组解析,再让 catalog 交集自然展示。
3. 如果未来需要匿名首页也展示"真实可用模型",应新增无需用户上下文的公开缓存接口,不要让匿名首页直接调用 `/chat/models`。
4. 如果默认分组更新后需要立即刷新模型广场,可以在设置更新或账号模型同步完成后增加缓存失效钩子。目前 30 秒 TTL 已覆盖首页抗刷场景。
