# 转存设置独立管理页与 OpenAI HTTP 全链路归档设计

- 日期：2026-08-11
- 状态：已确认，待实施
- 取代范围：`2026-07-16-runtime-capture-admin-toggle-design.md` 中关于管理入口、设置模型和 OpenAI 非目标的约定
- 关联基建：`gateway.capture`、ClickHouse `model_call_archive`

## 1. 目标

1. 在管理员左侧导航新增独立的「转存设置」入口，不把转存策略放进「系统设置」。
2. 把是否转存、转存哪些平台/结果/内容、以及用户/分组过滤改成管理员可即时生效的运行时策略。
3. 让 OpenAI HTTP 文本链路的归档与账号 `openai_passthrough` 转发策略解耦。
4. 保存真正发往上游的请求快照与真正来自上游、尚未转换成下游兼容格式的响应快照。
5. 继续保证归档故障、队列拥塞或策略读取失败不影响转发主链路。

## 2. 已确认范围

### 2.1 本期覆盖

- Anthropic 已有 capture 路径。
- Kiro 已有直连和兼容中转 capture 路径。
- OpenAI HTTP 文本链路：
  - `/v1/responses`
  - `/v1/chat/completions`
  - `/v1/messages`
  - 标准转发与 `openai_passthrough` 转发
- 成功调用与终态上游 HTTP 错误。
- 原始请求体、原始响应体、脱敏请求头、脱敏响应头四类可选持久化内容。
- 按 API Key 分组和具体用户过滤。

### 2.2 本期不覆盖

- OpenAI Responses WebSocket。
- 图片、视频、Embeddings、Alpha Search 等非文本模型端点。
- failover/retry 的中间失败尝试；只记录最终选中的成功调用或最终返回客户端的终态上游错误。
- 本地生成、网关合成或在请求到达上游前产生的错误。
- ClickHouse 连接信息的后台编辑。
- 本地磁盘 spool。
- 历史归档迁移。

## 3. 方案选择

### 3.1 采用：路径内采集 + 统一运行时策略

在各上游请求构造和响应读取点取得真实 wire-boundary 快照，通过共享 capture helper 写入请求级桥，最后由现有 handler 提交到 `ConversationCapturePool`。所有采集点使用同一个运行时策略判定器。

优点：

- 能区分真实上游内容与转换后的客户端内容。
- 能正确处理流式、非流式、兼容协议和终态错误。
- 不会误采 OAuth 刷新、模型探测、配额查询等非模型请求。
- 可以在分配 SSE tee/正文 buffer 前完成策略短路。

### 3.2 未采用：HTTP Transport 全局抓包

Transport 层难以稳定区分模型调用、认证刷新、探测和重试，且流式 body 包装会扩大共享网络层风险。

### 3.3 未采用：Handler 返回前抓取

Handler 只能稳定拿到转换后的客户端响应，无法保证上游保真，也容易漏掉错误和客户端断开场景。

## 4. Provisioning 与运行时策略分离

### 4.1 Provisioning

`config.yaml` 的 `gateway.capture` 继续负责基础设施：

- `enabled=true` 表示初始化 ClickHouse 连接、自动建表和 worker pool。
- ClickHouse 地址、库、表、用户名、密码、TLS、队列和批量参数继续只存在 YAML/Secret。
- `writer_queue_size` 显式限制 worker 之后、ClickHouse batcher 之前的二级队列，不再只使用代码内隐式容量。
- `max_queue_bytes` 覆盖从一级接收开始到 ClickHouse 成功写入或明确丢弃为止的全部在途 record，包括 worker、二级队列和当前 batch。
- 修改 provisioning 配置仍需重启服务。
- `enabled=false` 时 pool 为 `nil`，所有运行时策略恒不采集。

### 4.2 Runtime

管理员策略保存于现有 `settings` 表，保存后立即刷新进程内缓存，无需重启。

使用单一 key，保证一次保存原子生效：

```text
capture_runtime_policy
```

value 是版本化 JSON：

```json
{
  "version": 1,
  "enabled": false,
  "platforms": {
    "anthropic": true,
    "kiro": true,
    "openai": false
  },
  "outcomes": {
    "success": true,
    "terminal_error": true
  },
  "content": {
    "raw_request": true,
    "raw_response": true,
    "request_headers": true,
    "response_headers": true
  },
  "group_ids": [],
  "user_ids": []
}
```

默认语义：

- 总开关默认关闭。
- Anthropic、Kiro 平台默认开启。
- OpenAI 平台默认关闭；管理员必须显式开启后才转存 OpenAI。
- 成功、终态错误默认开启。
- 四类内容默认开启。
- 用户和分组为空表示该维度不过滤。

旧实例没有该 key 时直接使用上述默认值，不需要数据库 migration。

## 5. 运行时策略模型

后端增加专用模型 `CaptureRuntimePolicy`，包含版本、总开关、平台、结果、内容和过滤集合。对热路径提供不可变编译态 `CompiledCapturePolicy`：布尔字段直接读取，用户/分组数组预编译为 `map[int64]struct{}`。

匹配顺序：

1. pool 未 provision 或策略总开关关闭：拒绝。
2. 当前平台未开启：拒绝。
3. 当前结果类型未开启：拒绝。
4. `group_ids` 非空时，请求必须带分组且命中。
5. `user_ids` 非空时，用户必须命中。
6. 两个过滤维度均配置时采用 AND；均为空表示全量。

缓存沿用 `SettingService` 现有的 `atomic.Value + TTL + singleflight` 模式：

- 正常 TTL 60 秒。
- DB 读取或 JSON 解析失败时 fail-closed，临时返回 `enabled=false`，短 TTL 5 秒重试并记录 warn。
- 管理员成功保存后立刻替换当前进程缓存，避免等待 TTL。

## 6. 独立管理员 API

新增：

```text
GET /api/v1/admin/capture-settings
PUT /api/v1/admin/capture-settings
GET /api/v1/admin/capture-settings/history?range=24h|7d|30d
```

不把字段加入通用 `GET/PUT /api/v1/admin/settings`。

GET 返回：

- 当前运行时策略。
- `provisioned`：静态 capture 子系统是否初始化。
- `ready`：ClickHouse writer 是否在启动时成功连接并可写。
- `database`、`table` 和脱敏后的地址，用于诊断。
- 当前进程实时健康快照：提交、接收、成功写入、丢失总数，丢失字节数，一级/二级队列使用量，全链路在途字节数，以及最后成功和最后丢失时间。
- 最近 100 条进程内丢失事件。PostgreSQL 中最近 30 天的按分钟聚合历史通过独立 history API 按 24 小时/7 天/30 天查询。
- 不返回 username、password 或其他凭据。

PUT 行为：

- 完整替换 version 1 策略，不做部分 patch。
- 去重并排序用户/分组 ID。
- 拒绝非正整数 ID、未知平台、未知结果类型、未知内容字段和不支持的 version。
- 未 provision 或 writer 未 ready 时，允许保存关闭状态及其他选项，但拒绝把 `enabled` 改为 `true`，返回 `409 Conflict` 和可操作错误信息。
- 写 DB 成功后再刷新内存缓存；DB 写失败时保持旧策略。

## 7. 管理员页面与导航

新增页面：

```text
/admin/capture-settings
```

左侧导航：

- 名称：中文「转存设置」，英文 `Capture Settings`。
- 位于「系统设置」之前。
- 普通管理员模式和简单模式都显示。
- 使用独立归档/数据库语义图标，与系统设置齿轮区分。

页面分区：

1. 基础设施状态卡：未 provision、已就绪、连接失败；显示脱敏地址、database、table。
2. 总开关：未 ready 时禁用开启动作并显示原因。
3. 平台：Anthropic、Kiro、OpenAI；OpenAI 初始关闭并注明覆盖三个 HTTP 文本入口、不依赖 passthrough。
4. 结果：成功、终态错误。
5. 内容：原始请求、原始响应、请求头、响应头；明确正文包含用户内容，Header 仍会脱敏。
6. 范围：复用 `GroupSelector.vue` 与 `OpenAIFastPolicyUserSelector.vue`，说明留空和 AND 语义。
7. 实时健康卡：本进程启动时间、提交/接收/写入/丢失数、队列占用、在途字节与最后成功时间。
8. 丢失历史：显示发生时间、原因、条数、估算字节数和当时队列使用量，支持 24 小时/7 天/30 天查看。
9. 导航告警标记：当前进程启动后丢失计数增加时显示；管理员打开页面后可确认，不会删除历史。
10. 独立保存按钮；保存成功后重新获取服务端规范化结果。

## 8. 上游数据边界

### 8.1 OpenAI

三条 HTTP 文本入口最终可能共享 Responses 上游，也可能因 API Key 能力回退到 Chat Completions。记录以实际 attempt 为准：

- `raw_request`：认证注入、模型映射、兼容补丁、策略修改全部完成后，真正发给上游的最终 body。
- `request_headers`：真正发给上游的 header，在提交前删除认证/Cookie 等敏感字段。
- `raw_response`：上游原始 JSON 或 SSE 字节，在转换成 Chat Completions/Anthropic/客户端模型名之前采集。
- `response_headers`：上游原始响应头的脱敏 JSON。
- `upstream_endpoint`：记录实际选中的 `/v1/responses`、`/v1/chat/completions` 或具体子路径，而不是只按入站路径推导。

现有 passthrough 路径也改用最终 outbound body，修复当前归档可能保存模型映射前 body 的不一致。

### 8.2 Anthropic 与 Kiro

保持已有协议边界：

- Anthropic 保存实际 Anthropic 上游请求/响应。
- Kiro 继续保存网关 Anthropic 语义边界的请求与翻译后的 Anthropic JSON/SSE，并保存真实 Kiro 上游脱敏头。

本期不改变 Kiro 为 AWS wire body，以避免破坏既有离线消费格式。

## 9. 内容开关与抽取列

ClickHouse schema 保持不变。关闭的内容字段写空字符串，不迁移历史表。

以下元数据列始终按现有能力保存：时间、request/session ID、平台、请求/上游模型、上游端点、stream、HTTP 状态、stop reason、thinking、token/cache token、signature、截断标记和 capture version。

当管理员关闭正文持久化但元数据仍需从正文抽取时，系统可以在内存中暂存受 `max_body_bytes` 限制的副本，先异步抽取元数据，再在 writer 入库前清空被关闭的正文。该内容：

- 不写磁盘 spool。
- 不写 ClickHouse。
- 不进入日志。
- 随该条 worker 处理结束释放。

## 10. 丢失定义、实时指标与持久历史

### 10.1 丢失定义

以下情况计为转存数据丢失：

- `byte_budget_exceeded`：`max_queue_bytes` 不足以接纳新 record。
- `worker_queue_full`：一级 worker 队列已满。
- `writer_queue_full`：ClickHouse writer 二级队列已满。
- `writer_unavailable`：ClickHouse 启动连接或建表失败，writer 未就绪。
- `clickhouse_prepare_failed`、`clickhouse_append_failed`、`clickhouse_send_failed`：已成批的 record 未能写入 ClickHouse。

管理员主动关闭、平台/结果/用户/分组策略不命中都属于 `policy_skipped`，不计入丢失和告警。`max_body_bytes` 只导致正文截断并设置 `is_truncated=1`，不等于整条 record 丢失。

### 10.2 实时指标

进程内 tracker 用 atomic counter/gauge 维护：

- `submitted_records`、`accepted_records`、`written_records`、`dropped_records`、`dropped_bytes`。
- 按上述 reason 拆分的丢失条数和字节数。
- 一级队列当前/峰值条数、二级队列当前/峰值条数、全链路当前/峰值在途字节数。
- `started_at`、`last_success_at`、`last_drop_at`、`last_drop_reason` 和最后一条脱敏错误摘要。
- 内存 ring buffer 保留最近 100 条丢失事件。
- PostgreSQL 历史待重试聚合最多保留 4096 个 bucket，每次发送最旧的 256 个；持续失败触顶时淘汰最旧 bucket，并通过 `history_dropped_buckets` 与结构化日志显式告警。

数据只有在 ClickHouse `Send` 成功后才计入 `written_records`。在成功写入或确认丢失之前，record 始终占用 `max_queue_bytes` 预算，避免二级队列和 batch 绕过内存上限。

### 10.3 PostgreSQL 持久历史

新增 `capture_health_events` 表，按 `minute_bucket + instance_id + reason` 聚合保存：丢失条数、丢失字节、队列使用峰值和最后脱敏错误摘要。

- 记录丢失的热路径只更新内存聚合器，不同步写 PostgreSQL。
- 后台 reporter 每分钟批量 upsert 上一分钟的聚合值，自身待重试 map 有 4096 bucket 上限，单次最多处理 256 个最旧 bucket；失败时保留待重试，触顶淘汰会增加可见计数并记结构化日志，均不影响转发。
- 每小时删除 30 天以前的聚合行。
- 服务停止时在有界超时内尝试最后一次 flush；进程崩溃时最多丢失当前尚未 flush 的一分钟健康聚合，不影响 ClickHouse 中已写入的转存数据。

## 11. 错误处理与隔离

- 运行时关闭或过滤不命中：在 tee/buffer 分配前短路。
- ClickHouse 启动连接失败：保留现有 noop 降级，页面显示未 ready，不影响服务启动。
- ClickHouse 运行时写失败：记录 capture 错误日志、实时丢失指标和按分钟历史，丢弃批次，不反压转发。
- 队列或字节预算满：继续使用 drop/sample 策略，每次最终丢弃都必须按真实原因计数；`sample` 重试成功不计丢失。
- 设置 DB 读取失败或策略损坏：fail-closed，不采集、不影响转发。请求热路径只读进程内策略快照；冷缓存后台加载、过期缓存 stale-while-revalidate，绝不等待 PostgreSQL。管理员保存的新快照不得被较早发起的后台读取覆盖。
- 客户端断开：沿用各路径现有 drain/usage 行为；只有已经完整形成并提交的 capture record 才入库。

## 12. 实现边界

建议新增小而明确的组件：

- `capture_runtime_policy.go`：策略类型、默认值、校验、编译和匹配。
- `capture_runtime_policy_service.go`：settings 读写、缓存和保存即刷新。
- `capture_admin_handler.go`：独立 GET/PUT API 和 infrastructure 状态 DTO。
- `capture_context.go`：请求级策略决策与上游请求快照桥。
- `capture_health.go`：实时计数、队列 gauge、最近事件 ring buffer 和健康 DTO。
- `capture_health_reporter.go`：按分钟聚合、异步 PostgreSQL upsert 和 30 天清理。
- `CaptureSettingsView.vue`：独立管理员页面。

现有 `CaptureRecord`、`ConversationCapturePool`、ClickHouse writer 和表结构继续复用。避免继续扩大巨型 `setting_handler.go` 和 `SettingsView.vue`。

## 13. 测试与验收

### 13.1 后端

- 默认策略：总关、Anthropic/Kiro 开、OpenAI 关。
- policy JSON 正常、未知版本、未知字段、脏数据、ID 去重排序。
- 平台/结果/内容/用户/分组组合矩阵和 AND 语义。
- cache 命中、过期、singleflight、DB 错误 fail-closed、保存即刷新。
- 未 provision/未 ready 时开启返回 409；关闭策略仍可保存。
- writer readiness 不泄露凭据。
- OpenAI 三个 HTTP 文本入口在标准和 passthrough 模式下：
  - OpenAI 默认关闭时不采集。
  - 显式开启时成功调用采集实际 outbound request 和原始 upstream response。
  - 终态错误按策略采集；中间 failover 不采集。
  - 四类内容开关分别生效，元数据仍可抽取。
  - Header 凭据脱敏。
- Anthropic/Kiro 既有 capture 回归通过。
- 字节预算、一级队列、二级队列和 ClickHouse Prepare/Append/Send 失败分别生成正确 reason、条数和字节数。
- `max_queue_bytes` 在真正写入或丢弃前不释放；成功和所有失败路径都会精确释放一次。
- reporter 按分钟聚合、upsert 重试不重复计数，并删除 30 天前数据。
- 进程内最近 100 条事件有界，错误摘要不包含 ClickHouse 凭据或 record 正文。

### 13.2 前端

- 路由仅管理员可访问。
- 左侧导航在普通/简单管理员模式显示且位于系统设置前；普通用户不显示。
- GET 初始化、默认 OpenAI 关闭、PUT 完整替换、保存后回读。
- 未 ready 状态禁止开启总开关并显示原因。
- 平台、结果、内容、用户、分组控件正确序列化。
- 丢失计数增长时显示导航告警，页面可查看实时队列和 24 小时/7 天/30 天历史。
- 中英文标题、描述和敏感数据提示齐全。

### 13.3 完成标准

- 不开启 OpenAI 平台时，OpenAI 标准与 passthrough 请求都不写归档。
- 管理员显式开启 OpenAI 后，三个 HTTP 文本入口均不依赖 `openai_passthrough` 即可写入真实上游快照。
- 页面策略保存后对新请求立即生效，无需重启。
- 静态 ClickHouse 未配置或不可用时，管理员无法误开启实际转存。
- 任何整条 record 或整批 record 丢失都能在管理页看到原因和发生时间；服务重启后仍可查看最近 30 天历史。
- 全部新增单测、相关网关回归、前端测试、后端构建与前端类型检查通过。
