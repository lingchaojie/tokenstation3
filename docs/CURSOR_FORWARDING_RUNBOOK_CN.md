# Cursor 转发运行手册

## 适用范围

本手册对应固定参考版本 `SJwen0/cursor--@3709f0f6c83ed84b62c2a0f7f8e1ff63d6cfb7d4` 在当前 DEV 架构上的语义集成。支持范围是：

- Cursor OAuth/session 账号的凭证导入、刷新和重授权；
- Cursor api2 `AvailableModels` 模型发现；
- Cursor api5 `AgentService/Run` HTTP/2 Connect/Protobuf 上游转发；
- Chat Completions、Responses、Anthropic Messages 三种调用方协议；
- 现有 typed capture 生命周期中的调用方 JSON/SSE 采集。

不支持 Cursor API-key 账号类型、Cursor 余额主动查询、Cursor channel monitor、长生命周期有状态 Agent 会话，也不提供独立 `cursor_e2e` 命令。

## 账号和凭证导入

Cursor 账号统一创建为 `platform=cursor`、`type=oauth`。`crsr_` User API Key 是换取 session bearer 的凭证源，不是可直接发往 api2/api5 的 API-key bearer，因此不能创建 `cursor + apikey` 账号。

管理端支持以下入口：

1. 浏览器深链授权（推荐）：生成带 PKCE/state 的 Cursor 登录链接，浏览器确认后由管理端携带 `session_id`/state 轮询完成；这是当前深链授权唯一受支持的完成方式。
2. Refresh Token 导入：支持一次输入一条或批量输入；批量创建使用同一份名称、分组、代理、并发、倍率、模型映射和端点设置快照。
3. `crsr_` User API Key 导入：服务端通过官方凭证端点换取可用 bearer。
4. `WorkosCursorSessionToken` Cookie 导入：服务端先识别并保存 web refresh source；账号进入续期/转发 readiness 流程时，再通过深链升级为 chat-ready client token。
5. 重授权：新的 refresh source 会原子替换旧的 `api_key`、`refresh_token`、`web_session_token` 互斥来源，避免旧凭证复活。

`/admin/cursor/oauth/exchange-code` 是为兼容旧调用方保留的“凭证导入”别名，并不是 OAuth callback code 换证接口。它不会用请求中的 `session_id`/state 做会话绑定校验，而是把 `code` 字段直接当作凭证：以 `crsr_` 开头时按 User API Key 换证，其他值必须能解析为有效的 `WorkosCursorSessionToken`/session Cookie。操作员不要向该入口粘贴 callback URL 或裸 authorization code。

安全约束：

- 管理端 Cursor 凭证与深链轮询路由的敏感请求体按审计规则整体省略，也不会在账号 DTO、错误或日志中回显。兼容 `exchange-code` 的 `code` 是凭证输入；受支持的 `crsr_`/Cookie refresh source 可能按账号凭证规则进入加密存储，因此不要用它提交 callback URL 或裸授权码。
- 管理 UI 在成功、失败、取消、关闭和重置后清空已提交的 Cookie/API Key 等原始输入，并阻止同一次导入并发重复提交。
- 为支持后台续期，最终选定的 refresh source 会进入现有的加密账号凭证存储；账号 DTO、错误和日志仍按敏感字段规则脱敏。
- 密码授权能力当前关闭，UI 不展示密码、setup-token 或 Cursor API-key 账号入口。

账号的 `base_url` 默认是 `https://api2.cursor.sh`，用于 api2 unary 模型接口。自定义 api2 地址始终要通过 HTTP(S) URL 格式和敏感组件检查；host allowlist 与解析后 IP 检查是否启用取决于 `security.url_allowlist` 配置。OAuth 换证仍使用官方凭证 host，不跟随账号 `base_url`。

## 代理与出口要求

在创建或重授权前先为账号选择正确的应用内代理。凭证换取、Cookie 升级、模型发现、账号测试和 Agent Run 都使用应用内代理，但各调用路径的校验边界不同：

- OAuth 轮询、`crsr_` 换证、refresh 和后续 Cookie 升级：设置 `ProxyID` 后，仓库不可用、记录不存在或查询失败会 fail closed，代理 URL/transport 无法配置也会报错；该路径的 `proxyURL` 查询本身不筛选代理记录的禁用/过期状态，操作员必须确保所选记录可用。Cookie 初次导入只在本地解析，不产生上游请求。
- 模型发现和账号测试：设置 `ProxyID` 后会校验已加载的关联、ID 一致性、启用状态、过期时间和 URL，任一失败都不会静默直连。
- Agent Run：除关联、启用、过期和 URL 校验外，还要求代理 transport 可成功构造；任一失败都不会静默直连。

自定义端点始终要通过对应 URL 规则：api2 禁止用户名、密码、query、fragment，是否允许 HTTP 由 `security.url_allowlist.allow_insecure_http` 决定；api5 Agent 自定义端点始终要求 HTTPS 且只能使用 host 根路径。只有 `security.url_allowlist.enabled=true` 时，自定义 api2/api5 host 才必须出现在 `security.url_allowlist.upstream_hosts` 中，并按 `allow_private_hosts` 执行私网/解析后 IP 限制。当前 DEV 默认关闭 allowlist，因而允许格式有效的自定义/私有中继（api5 仍必须是 HTTPS）；这不绕过 URL 与代理 transport 规则。排障时不要用裸 `curl` 绕过账号代理；这样得到的网络结论不代表服务端真实调用链。

## 模型发现与模型映射

模型发现通过账号的 Cursor bearer 调用 api2 unary Protobuf `AvailableModels`，响应上限为 1 MiB。启用且可调度的 Cursor OAuth 账号由后台任务刷新；六小时是“是否跳过本次刷新”的 freshness 阈值，不是读取时的过期时间。最后一份可解析快照会继续供读取方使用，直到成功刷新、被清除或变为不可解析。管理端“测试连接”也走同一接口并在凭证 chat-ready 后写入快照。

模型列表规则：

- 有可用 observed snapshot 时，它是权威集合，不与内置 fallback 做并集；
- 无可用 snapshot 时，账号会暴露显式配置的 mapping alias；没有显式 mapping 时使用内置 fallback identity。组级目录还会在存在无可用 snapshot 的 Cursor 账号时补入 fallback；
- `auto` 与 Cursor wire model `default` 是同一选择的调用方/上游表示；
- 仅在存在可用 observed snapshot 时，账号 model mapping 的目标才会被 observed 集合过滤，alias 不会扩张该权威集合；无可用 snapshot 时按上一条的 mapping/fallback 语义暴露；
- Cursor model ID 不经过 OpenAI Codex 的重写逻辑，thinking/max 等后缀按 Cursor 语义保留。

若模型同步失败，依次检查账号是否为启用且可调度的 OAuth 账号、bearer 是否为 client token、代理是否有效、api2 `base_url` 格式是否有效（启用 allowlist 时还要检查 host/IP 策略），以及返回体是否为空、超限或无法解码。

## 三种调用方协议

同一个 Cursor 账号支持：

| 调用方协议 | 常用入口 | 非流式返回 | 流式返回 |
| --- | --- | --- | --- |
| OpenAI Chat Completions | `/v1/chat/completions` | Chat Completions JSON | Chat Completions SSE，结束时一个 `[DONE]` |
| OpenAI Responses | `/v1/responses` | Responses JSON | Responses SSE 生命周期事件 |
| Anthropic Messages | `/v1/messages` | Messages JSON | Messages SSE content-block 生命周期事件 |

三条入口都会归一化为同一份 stateless Cursor Agent turn，再编码回调用方原协议。调用方不会看到 Connect envelope、Protobuf 字段或内部 Chat 中间形态。

注意事项：

- 推荐调用方传 `model: "auto"`；上游 wire 值会变成 Cursor 的 `default`。
- 其他 model ID 在账号映射后原样送入 Cursor，不做 Codex model normalization。
- Chat Completions 的 `max_tokens`/`max_completion_tokens` 会变成本地输出上限；达到上限时返回调用方协议的 length/max_tokens 结束原因。
- Anthropic Messages 必须提供正数 `max_tokens`；Responses 的 `max_output_tokens` 可以省略，但显式值必须为正数。
- reasoning、文本、并行 tool calls、usage 和中途错误都按调用方协议编码。已经向调用方写出数据后发生上游错误时，会发送对应协议的错误事件，不伪造正常结束，也不会重放到另一个账号。

## Capture 交付格式

Cursor 上游虽使用 `application/connect+proto`，capture 仍只保存调用方看到的内容：

- 非流式请求/响应：`PayloadJSON`；
- 流式响应：`PayloadSSE`；
- 覆盖 Chat Completions、Responses、Messages 共六种“协议 × 是否流式”组合；
- `RawRequest` 是未经转译的调用方请求；
- `RawResponse` 只包含实际成功写给调用方的字节；
- stop reason 从交付后的 JSON/SSE 提取，不另存 Connect terminal 字段；
- 重试会替换未提交 attempt，最终 exact-once commit；provider error、client disconnect 和 partial write 保留真实因果。

不得新增 `connect_proto` capture format，也不要把 api5 request/response frame observer 接到持久化 capture。

## Client version 与 Agent 端点

默认 CLI identity 由 `backend/internal/pkg/cursor` 的 pinned client version 提供。Client version 的优先级从高到低为：

1. 账号 credentials `agent_client_version`；
2. 账号 extra `cursor_agent_client_version`；
3. 环境变量 `SUB2API_CURSOR_AGENT_CLIENT_VERSION`；
4. 代码内 pinned 默认值。

Agent base URL 同理支持账号 `agent_base_url`、extra `cursor_agent_base_url` 和环境变量 `SUB2API_CURSOR_AGENT_BASE_URL`，默认是 `https://agentn.global.api5.cursor.sh`。账号/环境自定义端点始终经过 HTTPS 与 URL 结构校验；allowlist 与解析后 IP 限制仅在 `security.url_allowlist.enabled=true` 时执行。不要把 api2 `base_url` 与 api5 `agent_base_url` 混用。

若上游返回可信的 client-version/update-required 拒绝，网关将其视为 provider 配置错误并停止账号轮换。此时先更新上述 client version 配置并用本地 fixture 回归；需要真实上游确认时遵守文末生产审批规则。

## 空闲、首帧与工具调用窗口

- 首帧默认等待 60 秒，可用 `SUB2API_CURSOR_AGENT_FIRST_BYTE_TIMEOUT` 覆盖；
- 已收到响应后的 idle 默认 30 秒，可用 `SUB2API_CURSOR_AGENT_IDLE_TIMEOUT` 覆盖；
- thinking、heartbeat、文本、tool 等每个响应帧都会刷新 idle 计时；
- 上游 `TurnEnded`/结束帧立即收尾，优先于 idle fallback；
- idle 仅用于回收卡死流，不是普通思考时长上限；
- 首个 tool call 后有短暂且有界的 drain window，用于收齐同一轮的并行 tool calls。

环境变量必须使用 Go duration 格式，例如 `30s`、`1m`，空值、非正数或非法值会回退到默认值。不要为了掩盖代理、client version 或协议错误而盲目增大超时。

## 安全的本地 fixture 验证

以下测试均使用内存 stream、fake repository、`httptest` 或前端 mock，不访问 Cursor、其他 provider 或生产环境。

从仓库根目录运行核心 wire/转发测试：

```bash
cd backend
go test ./internal/pkg/cursor ./internal/service -run 'Cursor' -count=1
go test -race ./internal/pkg/cursor ./internal/service -run 'Cursor' -count=1
```

验证真实 handler 路由、failover 和调用方格式 capture：

```bash
cd backend
go test -tags=unit ./internal/handler ./internal/server -run 'Cursor' -count=1
go test ./internal/service -run 'CursorCaptureStoresSixCallerProtocolModes|CursorDispatchPublicEntrypointsPreserveCallerProtocolAndCapture' -count=1
```

验证账号、OAuth UI 和平台目录：

```bash
cd frontend
pnpm exec vitest run \
  src/api/__tests__/admin.cursor.spec.ts \
  src/composables/__tests__/useCursorOAuth.spec.ts \
  src/components/account/__tests__/CreateAccountModal.cursor.spec.ts \
  src/components/admin/account/__tests__/ReAuthAccountModal.cursor.spec.ts \
  src/constants/__tests__/platforms.spec.ts
```

提交前完整 gate 以实现计划中的 `make check-generate`、backend focused/race/build/full、frontend lint/typecheck/test/build 和 scope guards 为准。

## 常见故障定位

| 现象 | 优先检查 |
| --- | --- |
| `Cursor upstream configuration is unavailable` | 账号代理关联、`agent_base_url` 格式；启用 allowlist 时再检查自定义 host 和 IP 解析策略 |
| `No Cursor access token available` | 是否错误创建了 API-key 类型；刷新源是否为 refresh token、`crsr_` 或有效 Workos Cookie |
| web token 通过模型请求但无法聊天 | Cookie 尚未升级为 client token；重新走深链/SSO 导入 |
| `update required` / client version 拒绝 | 更新 `agent_client_version` 或 `SUB2API_CURSOR_AGENT_CLIENT_VERSION`，不要轮换整池账号 |
| 模型列表只显示 fallback/配置 alias | observed snapshot 尚未成功写入或已被清除；检查 api2 URL、代理和 1 MiB 响应限制 |
| 长思考后被截断 | 确认收到的帧会刷新 activity；检查是否误设过短的 `SUB2API_CURSOR_AGENT_IDLE_TIMEOUT` |
| 流中途返回错误事件 | 查看 provider terminal/transport 分类；已有交付字节的请求不会自动换账号重放 |

## 生产环境审批边界

本地 fixture 验证不需要生产账号。任何生产环境的代码、配置、环境变量、账号或代理改动，执行前都必须先取得用户明确确认。

如果后续确实需要使用生产账号向 Cursor 发起真实请求，也必须先取得明确批准，并复用应用已有的账号选择、凭证刷新、上游安全校验和已配置 IP 代理流程，尽量完整模拟服务端真实调用链。禁止绕过现有流程直接裸调上游 API，也禁止为了探测而临时静默直连。
