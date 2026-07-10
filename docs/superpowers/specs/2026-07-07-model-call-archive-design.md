# 上游模型调用全量归档（ClickHouse）设计文档

- 日期：2026-07-07
- 状态：设计待评审
- 关联需求：`skl/sdk/req.pdf`（Opus 4.8 用户日志验收标准）为众多下游消费者之一，**不以其裁剪 schema**

## 1. 背景与目标

### 1.1 目标
在网关转发完成后，把**每一次上游模型调用**的**原始 request + response（逐字）**异步写入一个**远端、独立**的 ClickHouse 数据库，形成一个**厂商无关、无损的全量归档库**，供开放式二次开发使用。

### 1.2 核心原则
- **忠实归档**：采集侧只做逐字保存 + 最小抽取列；所有面向具体需求的归一化 / 过滤 / 格式转换（含 SDK 的 Anthropic 交付格式、cron 过滤、质量标注）全部放到**读库后的离线二次开发**。
- **只存上游相关**：只保存"打到厂商的真实请求"与"厂商返回的真实响应"。**不存**网关 / 客户端侧的运维与身份字段（`client_request_id`、client IP、user agent、api_key / user 身份、回指内部表的 FK 等）。
- **绝不影响转发**：归档是与转发、计费完全隔离的**第三条异步通道**。任何归档侧的失败（外部库宕机、慢、队列满）都**不得外溢**到转发路径。
- **开关默认关**：新增总开关 `gateway.capture.enabled`，默认 `false`。**关时零成本**（不安装 tee、不分配 buffer、不提交任务）。

### 1.3 非目标
- 不在本库做会话重建、格式转换、质量评分（均属离线二次开发）。
- 不复用主 PostgreSQL；不与主库做跨库外键。
- 不实现 A2 落盘 spool（本期先 A1 内存队列，A2 作为后续优化在配置中预留说明）。

## 2. 关键决策（已与需求方确认）

| 决策项 | 结论 |
|---|---|
| 传输 durability | **A1 有界内存队列**（本期）；A2 本地落盘 spool + 后台 shipper 作为后续优化预留 |
| 存储引擎 | **ClickHouse**（远端，需配置） |
| 采集开关 | 新增 `gateway.capture.enabled`，**默认 false**，开关不影响转发性能 |
| 采集范围 | **全平台 + 全模型**全量归档 |
| 流式写入粒度 | **一次调用 = 一行**；累积原始 SSE 字节至终止事件后整条写入。**非**每个 SSE 事件一行 |
| response 表示 | **只存原始 SSE 字节流**（逐字）；SSE→完整 Anthropic body 的组装放离线二次开发 |
| session_id 来源 | 从**上游请求体** `metadata.user_id` 解析（`ParseMetadataUserID`），非 Anthropic 协议 fallback 到 `extractBodySessionID` |
| overflow 策略 | **drop**（绝不 sync 回写，与计费的 usage 池不同）|

## 3. 架构与数据流

```
inbound → [apiKeyAuth ...] → GatewayHandler.Messages
                                     │
                          captureEnabled?  ──no──► 原路转发，零增量成本
                                     │yes
                          GatewayService.Forward
                            ├─ 非流式: handleNonStreamingResponse 拷贝完整响应 body
                            └─ 流式:   上游 SSE 读取 goroutine 处 tee，按序累积原始 SSE 字节到 buffer
                                        （终止事件 message_stop / [DONE] 后收束）
                                     │
                          ForwardResult{ ..., CaptureResponse, CaptureTruncated }
                                     │
                     usage tap 旁（gateway_handler.go:963-1013）：
                       快照 最终上游 body(attemptParsedReq.Body.Bytes())
                          + 脱敏后上游请求头 / 响应头 + result
                                     │  submit（可 drop，绝不阻塞）
                          ConversationCapturePool（有界内存队列，overflow=drop）
                                     │  worker: 抽取列 + 组批
                          ClickHouseArchiveWriter（批量 INSERT）
                                     │
                          远端 ClickHouse: model_call_archive（一次调用一行）
```

### 3.1 零成本开关保证
- **关时**：请求顶部一次性读 `captureEnabled` bool。为 false 时：不安装 SSE tee、不分配 buffer、`ForwardResult` 的 capture 字段保持 nil、不构造也不提交任务。转发路径**零增量分配、零额外分支开销**（仅一次 bool 判断）。
- **开时**：仅新增「每 SSE 行 append 到 buffer」+ 结束后一次「可 drop 的异步提交」。**永不 sync 回写、永不阻塞**。外部 ClickHouse 宕机 → 队列填满 → 直接 drop + 计数指标，转发无感。

### 3.2 采集点与四条硬约束（来自字段 review，焊死到实现）

| 采集内容 | 位置（file:line 锚点） | 硬约束 |
|---|---|---|
| 流式响应 tee | `service/gateway_service.go:8613`（上游 SSE 读取 goroutine） | **坑2**：必须在**上游读取侧**抓厂商原始 SSE，免疫客户端侧 `thinking_delta↔thinking` 等协议改写（`:4427`） |
| 非流式响应 | `service/gateway_service.go:9305` `handleNonStreamingResponse` | 完整 body 已在手，拷贝一份 |
| 结果回传 | `service/gateway_service.go:561` `ForwardResult` | 新增 `CaptureResponse []byte` + `CaptureTruncated bool`（今天该结构不带正文）|
| 提交归档 | `handler/gateway_handler.go:963-1013`（usage tap 旁） | **坑1**：快照 `attemptParsedReq.Body.Bytes()` = **最终上游 body、且是成功那次尝试**（含 `FilterThinkingBlocks:5400` 之后只剩合法 signature 的 thinking 块）；**坑4**：一并存上游响应头 |

- **坑3（存储侧）**：原文列存**字节串**（ClickHouse `String` 天然是字节串），不经 UTF-8 校验，保住乱码数据供离线 `is_garbled` 判定。
- 抽取（session_id / thinking_effort / signature_present / stop_reason 等）**在 worker goroutine 内完成，不占热路径**；tap 仅做内存快照（memcpy）。

## 4. 配置

新增 `gateway.capture`，结构与命名对齐现有 `GatewayUsageRecordConfig`（`config/config.go:997`）。三步模式：struct + `mapstructure` → `viper.SetDefault` → `Validate()`。

```yaml
gateway:
  capture:
    enabled: false            # 总开关，默认关。关时整条链路不初始化
    max_body_bytes: 8388608   # 单条原文上限(8MB)，超出停止 append 并标 is_truncated=1
    queue_size: 8192          # A1 有界内存队列容量
    worker_count: 4           # worker 数
    overflow_policy: drop      # 队列满策略：drop（默认，绝不 sync）/ sample
    overflow_sample_percent: 0
    batch_max_size: 200        # 批量 INSERT 行数上限
    batch_max_interval_ms: 1000 # 批量时间窗
    clickhouse:
      addr: ["ch-host:9000"]   # 远端地址，支持多节点
      database: llm_archive
      username: default
      password: ""             # 读 secret，日志中按 key 名引用、绝不回显 value
      dial_timeout_ms: 2000
      read_timeout_ms: 10000
      compression: lz4          # 传输层压缩：lz4 / zstd / none
      secure: false             # TLS 开关
      max_open_conns: 8
    # ── 后续优化预留（本期不实现）──
    # spool:
    #   enabled: false          # A2：本地落盘 spool + 后台 shipper，扛外部瞬断不丢
    #   dir: /var/lib/sub2api/capture-spool
    #   max_disk_bytes: 1073741824
```

**校验规则（`Validate()`，仅 enabled=true 时生效）**：
- `clickhouse.addr` 非空、`clickhouse.database` 非空。
- `overflow_policy ∈ {drop, sample}`（**不允许 sync**，防止阻塞转发）。
- `max_body_bytes > 0`、`queue_size > 0`、`worker_count > 0`、`batch_max_size > 0`。
- `compression ∈ {lz4, zstd, none}`。

### 4.1 部署与访问

**部署拓扑**：ClickHouse **独立于网关部署**（另一台服务器 / 另一云 / 托管版 ClickHouse 均可），不与转发生产机争抢存储与查询资源。网关只需**出站网络可达** `clickhouse.addr` 即可；写入异步批量，跨机网络延迟对转发零影响。

**`addr` 在 Docker 场景怎么填**：
- CH 在**另一台服务器** → 填该机 IP / 域名 + 端口，放行网关出口到 CH 的入站（原生 TCP `9000`，TLS `9440`）。
- CH 是**同一 compose 的兄弟容器** → 填 compose service 名（如 `clickhouse:9000`）。
- CH 在**宿主机上** → 填 `host.docker.internal:9000` 或宿主机 IP。

**上线后行为**：`enabled=true` 且连接配置就绪后，每次上游调用的原文异步批量写入你配的 CH；有最多一个批窗（`batch_max_interval_ms`，默认 1s）的延迟；CH 不可达时按 overflow=drop 丢弃 + 计数，**转发不受影响**。

**本地查看数据**：
- `clickhouse-client --host <ch-host> --user ... --password ...` → `SELECT * FROM llm_archive.model_call_archive LIMIT 10`。
- HTTP 接口（`8123`）：`curl 'http://<ch-host>:8123/?query=...'`；浏览器开 `http://<ch-host>:8123/play` 内置 Web SQL。
- GUI：DBeaver / TABIX / Grafana。
- 导 JSONL 喂离线脚本：`SELECT ... INTO OUTFILE 'out.jsonl' FORMAT JSONEachRow`。
- CH 在远端且端口未开公网时：`clickhouse-client` 直连内网地址，或 `ssh -L 9000:ch-host:9000 user@jump` 建隧道。

**安全基线（重要）**：本库存**完整对话原文**（用户真实输入 + 模型输出），属敏感数据。CH 机器务必：
- **不裸暴露公网**（`9000` / `8123` 不对 `0.0.0.0` 开）；
- 强密码 + 防火墙仅允许网关 IP 入站；
- 跨不可信网络传输开 `secure: true`（TLS，端口 `9440`）。

## 5. 组件（对齐现有 wire / 池 / batcher 模式）

| 组件 | 仿照 | 职责 |
|---|---|---|
| `ConversationCapturePool` | `service/usage_record_worker_pool.go` 的 `UsageRecordWorkerPool` | 有界内存队列 + worker 池；**overflow 默认 drop**（区别于计费的 sync）；`Submit` 非阻塞；`Stop()` 优雅关闭 |
| `ClickHouseArchiveWriter` | `repository/usage_log_repo.go` 的 `runBestEffortBatcher` | 批量 channel 累积到 `batch_max_size` 或时间窗后执行多行 `INSERT`；启动时 `CREATE TABLE IF NOT EXISTS`；`Stop()` flush 剩余批次 |
| `CaptureRecord`（DTO） | `RecordUsageInput` | 承载原文字节 + 抽取列，worker 内从原文抽列 |
| 抽取函数 | `metadata_userid.go` / `gateway_request.go` | 复用 `ParseMetadataUserID`、`NormalizeClaudeOutputEffort`、`extractBodySessionID` |

**依赖注入（google/wire）**：新增 provider（config → pool → writer），注入 `GatewayHandler` 与 `OpenAIGatewayHandler`；`provideCleanup` 注册 `.Stop()`。**enabled=false 时注入 no-op 实现**——不建立 ClickHouse 连接、`Submit` 直接返回。客户端库：`github.com/ClickHouse/clickhouse-go/v2`。

## 6. ClickHouse 表结构

独立于 ent / PG 迁移，由 `ClickHouseArchiveWriter` 启动时 `CREATE TABLE IF NOT EXISTS` 建表。

```sql
CREATE TABLE IF NOT EXISTS model_call_archive (
  -- 抽取列（查询 / 验收 / 分析用）
  captured_at        DateTime64(3) DEFAULT now64(3),
  request_id         String,
  session_id         String,
  platform           LowCardinality(String),
  requested_model    LowCardinality(String),
  upstream_model     LowCardinality(String),
  upstream_endpoint  String,
  stream             UInt8,
  http_status        UInt16,
  stop_reason        LowCardinality(String),
  thinking_effort    LowCardinality(String),
  thinking_type      LowCardinality(String),
  input_tokens       UInt32,
  output_tokens      UInt32,
  cache_read_tokens  UInt32,
  cache_creation_tokens UInt32,
  signature_present  UInt8,
  is_truncated       UInt8,
  capture_version    UInt16,
  -- 原文（字节串，天然容乱码；ZSTD 高压缩）
  raw_request        String CODEC(ZSTD(3)),  -- 最终上游请求体逐字
  raw_response       String CODEC(ZSTD(3)),  -- 流式=原始 SSE 字节流；非流式=完整 JSON；错误也原样
  request_headers    String CODEC(ZSTD(3)),  -- 脱敏后 JSON
  response_headers   String CODEC(ZSTD(3))   -- 脱敏后 JSON
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (session_id, captured_at, request_id)
SETTINGS index_granularity = 8192;
```

- `ORDER BY session_id` → SDK 的 `GROUP BY session_id` + 会话内时序排序天然高效。
- `is_garbled` **不入列**：离线对 `raw_*` 做 UTF-8 / mojibake 检测得出（spec 本就要求离线判定）。
- 抽取列全部可由 `raw_*` 原文重算 → **任何未预抽字段都能从原文兜底，后续不会缺字段**。

## 7. SDK 需求字段覆盖映射（req.pdf → 本库）

| spec 字段 | 来源 | 归档处 |
|---|---|---|
| `session_id` | 上游 body `metadata.user_id` → `ParseMetadataUserID` | 抽取列 `session_id` |
| `request_id` | 上游响应头 `x-request-id`（spec 首选）/ 内部 request_id | 抽取列 `request_id` + `response_headers` 原文 |
| `timestamp` | 采集打点 | 抽取列 `captured_at` |
| `thinking_effort` | 上游 body `output_config.effort`（low/medium/high/xhigh/max，**客户端实选档位，非 budget_tokens 反推**）| 抽取列 `thinking_effort` |
| `is_garbled` | 离线对 `raw_*` 检测 | 离线算（原文兜底） |
| `request`（不套 wrapper 的 raw body） | 最终上游请求体逐字 | `raw_request` |
| `request.system/tools/messages` | 在 body 内 | `raw_request`（离线抽取） |
| `response.response_data.*`（stop_reason、content blocks） | 流式 SSE / 非流式 JSON | `raw_response`（离线 SSE→body 组装） |
| thinking / redacted_thinking / tool_use / tool_result / image 各 block + signature | 全在 req/resp 原文内 | `raw_request` / `raw_response` |
| §4 质量指标、§5 deepseek 标注 | 读库后离线计算 | 不采集 |

## 8. 流式写入语义（一次调用 = 一行）

- 流式响应在上游 SSE 读取 goroutine 处按序累积**原始 SSE 字节**到 buffer，直到终止事件（`message_stop` / `[DONE]`，`gateway_service.go:8731/8835`）出现，**整条调用写一行**。**不做**每 SSE 事件一行（会导致行数爆炸且需 JOIN 重组）。
- `raw_response` 存**原始 SSE 逐字**：`signature_delta`、事件顺序、所有 delta 全保住，是"源真"，免疫本地解码器 bug，并可服务 SDK 之外的其它需求。
- worker 只做**轻扫描**取抽取列（`stop_reason` 从 `message_delta`、`signature_present` 看有无 `signature_delta`、usage 从 `message_start` / `message_delta`），**不做完整组装**。
- SSE → 完整 Anthropic response body 的组装是**离线二次开发**步骤（spec §2 亦将其列为后处理）。
- **中途断开**：`ForwardResult.ClientDisconnect` 为真时，buffer 为残缺 SSE，照存并标 `is_truncated=1`；SDK 的"末轮必须 end_turn"过滤会自然剔除残缺轨迹。

## 9. 脱敏

- 请求 / 响应头：**剥离凭证类**（`Authorization`、`x-api-key`、`cookie` 等），**保留影响模型行为 / 诊断的**（`anthropic-version`、`anthropic-beta`、`x-request-id`、限流头等）。头脱敏后以 JSON 存入 `request_headers` / `response_headers`。
- 请求 / 响应体：**不做内容 masking**（spec §3.4 明确警告勿过度清洗正文）。仅当 body 内出现已知凭证模式（如内联 api key）时按需剥离——本期默认不动 body，作为可选项预留。
- 日志：ClickHouse 密码等 secret 按 key 名引用，绝不回显 value。

## 10. 错误处理（核心：capture 失败绝不外溢到转发）

| 场景 | 处理 |
|---|---|
| ClickHouse 连不上 / 慢 | 批量 INSERT 在 worker 内 `context` 超时 → 该批 **drop** + `capture_dropped_total` 计数。转发无感 |
| 队列满 | `overflow_policy=drop` → 直接丢 + `capture_overflow_total` 计数 |
| worker panic | `recover` + log，不影响其它任务与转发 |
| 原文超 `max_body_bytes` | 停止 append、标 `is_truncated=1`，仍写抽取列（不丢整条） |
| 建表失败 | log 错误 + 进入降级（Submit 变 no-op），**不阻塞启动**、不影响转发 |
| enabled=false | 整条链路不初始化，无连接、无队列、无 goroutine |

**可观测性**：`capture_submitted_total` / `capture_dropped_total` / `capture_overflow_total` / `capture_batch_insert_seconds` / `capture_queue_depth`。

## 11. 测试策略

- **单元**：
  - config 解析 + `Validate()`（enabled 时缺 addr/database 报错、overflow=sync 被拒）。
  - 抽取函数：从 `metadata.user_id` 解 `session_id`（新 JSON 格式 + legacy 格式）、`output_config.effort` 取 `thinking_effort`、`signature_present` 扫描、`stop_reason` 解析。
  - 流式 tee 组装字节 == 客户端实收字节（保真）。
  - 非流式拷贝正确。
- **零成本断言**：enabled=false 时，tee / tap / Submit 均不被调用（spy / mock 校验）。
- **overflow**：队列打满 → 验证 drop 且转发返回不受影响。
- **集成**：有 ClickHouse 环境则往返一条 → 读回 `raw_*` 逐字节相等；无环境则 skip。
- **spec 契约测试**：构造含 thinking+signature+tool_use 的上游响应 → 归档 → 离线抽取能还原 spec §3 要求的全部 block 字段。

## 12. 实施顺序（供后续 writing-plans 展开）

1. config：`GatewayCaptureConfig` + `ClickHouseConfig` 结构、viper 默认、`Validate()`。
2. `ForwardResult` 加 `CaptureResponse []byte` + `CaptureTruncated bool`；enabled 时在非流式 / 流式 tee 点填充（feature-flag 守卫）。
3. `ClickHouseArchiveWriter`：连接、建表、批量 batcher、`Stop()` flush。
4. `ConversationCapturePool`：有界队列 + worker + overflow=drop + `Stop()`。
5. 抽取逻辑 + `CaptureRecord` DTO；在 `gateway_handler.go` / `openai_gateway_handler.go` 的 usage tap 旁提交。
6. wire：provider + 注入两个 handler + `provideCleanup` 注册 `.Stop()`；enabled=false 注入 no-op。
7. 指标 + 测试（含零成本断言、overflow、契约）。

## 13. 实现补充与后续优化

### 已实现（超出初版计划，评审驱动）
- **上游头保真 + 只存上游相关**：`request_headers` 存**上游请求头**（`resp.Request.Header`）而非客户端头；`response_headers` 存上游响应头（`resp.Header`）。均脱敏，经 gin.Context 桥回传，不含任何客户端身份字段。
- **非流式逐字保真**：非流式在 `ReadUpstreamResponseBody` 之后、任何改写（model/tool 名还原、Kimi/cache-TTL usage 规整、SSE→JSON）之前快照，和流式 tee 一致 —— 同一对话流式/非流式归档结果一致。
- **错误响应归档**：终态错误响应（`handleErrorResponse` / OpenAI `handleErrorResponsePassthrough`）也归档，`http_status` 记录真实状态码。failover 中间重试不归档（在 `shouldDisable` / `UpstreamFailoverError` 分支已提前返回）。采集池经 setter 注入 `GatewayService` / `OpenAIGatewayService`，`Submit` 非阻塞 + recover，绝不影响转发。
- **禁用路径纯 branch-only**：`takeCaptureResult` 读取也置于 `Capture.Enabled` 守卫内。

### 后续优化（预留，非本期）
- **A2 落盘 spool + 后台 shipper**：网关容器持久卷上追加式 WAL，外部库瞬断时本地堆积、恢复后补发，把 best-effort 升级为近似不丢。配置段已在 §4 注释预留。
- **OpenAI WebSocket 实时路径归档**：协议不同，当前未采集（`ResponsesWebSocket`）。
- 可选 `response_json` 组装列（离线组装固化）——当前决策为只存 raw SSE，不做。
- body 内凭证模式剥离（当前默认不动 body）。
