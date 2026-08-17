# Capture 磁盘 Spool 与独立 Sidecar 设计

- 日期：2026-08-16
- 状态：已实现并通过本地单元、竞态、故障恢复，以及固定镜像
  `clickhouse/clickhouse-server:26.3.17.110` 的真实 HTTP RowBinary/zstd 集成验证；
  Windows/Tailnet 与正式服部署仍需另行审批和验收
- 适用分支：`dev`
- 关联文档：`docs/capture-clickhouse-windows-deployment.md`

## 1. 背景与问题判断

历史 capture 在 `sub2api` 主进程中完成以下工作：

1. 最终上游调用的 request/response 被复制成 `CaptureRecord`；
2. record 进入 Pond worker 队列；
3. worker 抽取字段后进入 ClickHouse writer 队列；
4. writer 在内存中组 batch，再通过 ClickHouse native TCP 写入。

两层队列都有条数上限，另外用 `max_queue_bytes` 限制在途原文，但原文仍和
转发、计费、HTTP 连接共享同一个 Go heap 和同一次 GC。ClickHouse 断线、写入
失败、进程重启或队列溢出时，已经完成的 capture 不会补传。这一设计适合
“可丢的辅助遥测”，不适合“希望最终全部进入 ClickHouse 的原始对话归档”。

直接在请求协程中同步写 ClickHouse 也不成立：远端个人电脑、Tailscale、Windows
登录和 Docker Desktop 都可能短时或长时离线，同步写会把归档故障反压到核心转发。
因此本次把可靠性边界改为：主进程只做非阻塞流式交接；sidecar 先将最终记录提交到
本地持久 spool，再独立重试 ClickHouse。

## 2. 已确认目标

- 所有被 capture 策略选中的内容最终写入 ClickHouse；ClickHouse 是长期数据源。
- 不引入 S3、R2、MinIO 或其他对象存储。管理员以后读取原始对话只查询
  ClickHouse，不回正式服容器取文件。
- 正式服本地文件只作为待上传缓冲；ClickHouse 确认写入后立即清理。
- capture 故障、磁盘不足、sidecar 崩溃和远端离线绝不阻断或改变上游转发结果。
- 避免大 body 和积压 batch 长时间驻留 `sub2api` 主进程 heap。
- 已提交到 spool 的记录在应用或容器重启后自动续传。
- 复用现有镜像、Compose、systemd/容器重启和管理员更新流程；不新增 Docker 服务。
- 静态开关默认关闭；静态关闭时不启动 sidecar、不创建 socket、不启动 tsnet。
- ClickHouse 节点采用 Windows + Docker Desktop WSL2；允许 Windows 登录后才恢复服务。

## 3. 非目标和明确边界

- 不承诺无限大小的单条正文。request 和 response 各自最多保存 32 MiB；超过上限
  保存精确前缀、完整观测字节数、所观测内容的 SHA-256，并标记截断。
- 不承诺在主进程崩溃瞬间保存尚未向 sidecar 发出 `COMMIT` 的 attempt。
- 不把本地 spool 当作查询库、永久副本或备份。
- 不让 sidecar 自动执行 `CREATE TABLE`、`ALTER TABLE` 或其他 schema 迁移。
- 不在正式服宿主机安装 Tailscale；正式服容器内由 sidecar 的嵌入式 tsnet 入网。
- 不给应用容器 Docker socket，也不把管理员一键更新扩展为宿主机部署系统。
- 本次不改 provider 的选择、重试、failover、计费、usage、响应内容或 KIRO 语义。

## 4. 总体架构

```text
                                 同一个 sub2api 容器
┌──────────────────────────────────────────────────────────────────────┐
│                                                                      │
│  sub2api 主进程                                                      │
│  ├─ 正常转发/计费/运行时策略                                         │
│  ├─ 最终 attempt 判定                                                │
│  ├─ 非阻塞 Unix socket 分帧发送                                      │
│  └─ sidecar supervisor                                               │
│                   │                                                  │
│                   ▼                                                  │
│  同一版本二进制：sub2api capture-sidecar                             │
│  ├─ IPC receiver                                                     │
│  ├─ zstd 流式 spool writer                                           │
│  ├─ durable batch manifest / retry                                   │
│  ├─ HTTP RowBinary uploader                                          │
│  └─ embedded tsnet ──────────────────────────────────────┐           │
│                                                         │           │
│  /app/data/capture/spool（现有持久卷；上传后删除）        │           │
└─────────────────────────────────────────────────────────┼───────────┘
                                                          │ tailnet
                                                          ▼
Windows Tailscale Serve :18000
          └─► 127.0.0.1:18123
                    └─► ClickHouse 容器 HTTP :8123
                              └─► Docker named volume
```

sidecar 是独立 OS 进程，因此拥有独立 Go heap 和 GC；它不是独立容器，仍共享容器
cgroup 的总内存和磁盘。进程边界的目标是隔离分配与 GC，并通过有界流式缓冲减少
主进程压力，不把它描述成独立 cgroup 的硬资源隔离。

## 5. 进程与启动生命周期

### 5.1 二进制模式

同一个 release binary 支持两个入口：

- `sub2api`：现有网关主进程；
- `sub2api capture-sidecar`：只加载 capture 所需静态配置，不初始化 HTTP server、
  PostgreSQL、Redis、计费或 provider 服务。

静态 `gateway.capture.enabled=false` 时，主进程返回现有 nil/no-op capture 依赖，不创建
`/app/data/capture`，也不启动子进程或 tsnet。静态配置改为 true 后必须重启主进程。

### 5.2 监督与停止

静态开启时，主进程在配置校验完成后启动 sidecar，并采用带抖动的指数退避重启，
建议从 2 秒递增到 60 秒。sidecar 启动失败不阻止主服务启动。主进程正常退出时先向
sidecar 发送 `SIGTERM`，最多等待 10 秒完成当前 fsync 和状态收尾，超时才强制终止。
Linux 子进程设置 parent-death signal，父进程意外退出时 sidecar 也会退出，防止孤儿
进程继续占用 socket。

主进程必须通过 `/proc/self/exe` 启动子进程，而不是重新解析 `/app/sub2api`。管理员
原地更新会先替换路径上的文件、再提示重启；`/proc/self/exe` 保证重启前如果 sidecar
恰好崩溃，旧父进程仍拉起完全相同的旧版本。容器重启后，父进程和 sidecar 再一起
切换到新版本。IPC 握手还必须校验 protocol version，版本不一致时拒绝接收并告警。

### 5.3 与现有部署和更新的关系

- Compose 不增加 service、image、profile 或宿主机 socket。
- spool 与 tsnet state 都位于现有 `/app/data` 持久卷；容器重建不丢。
- `restart: unless-stopped` 继续只管理一个 `sub2api` 容器；父进程管理 sidecar。
- 管理员一键更新、指定版本回滚仍只替换一个 binary。更新完成后的现有“需要重启”
  语义不变。
- Docker image 重新拉取/重建仍以镜像内版本为准；管理员原地更新只存在于当前容器
  writable layer，这一既有边界不在本次扩展。

## 6. 最终 attempt 与 IPC 协议

### 6.1 所有权

主进程仍是“哪个 provider attempt 最终返回客户端”的唯一判断者。每次真实上游发送
边界创建新的 `capture_id` 和独立 IPC 会话：

1. `BEGIN`：版本、capture ID、平台、模型、endpoint、运行时内容策略等元数据；
2. `REQUEST_HEADERS` / `REQUEST_CHUNK`：最终 wire request，最多 64 KiB 一帧；
3. `RESPONSE_HEADERS` / `RESPONSE_CHUNK`：网关从真实 provider 读取到的原始字节；
4. `FINAL`：HTTP 状态、usage、stop reason、完整性/截断标记等终态元数据；
5. `COMMIT` 或 `ABORT`。

发生 retry/failover 时先对旧 attempt 发 `ABORT`，sidecar 删除对应 partial。最终成功或
最终返回客户端的真实上游错误才发 `COMMIT`。本地合成错误、请求到达 provider 前的
失败以及无法证明最终 attempt 的记录继续 fail closed，不用入口兼容 body 伪造原文。

### 6.2 非阻塞约束

每个在途 attempt 使用独立 Unix domain socket 会话，帧最大约 64 KiB。主进程不建立
新的 record 队列，不 deep-copy 整个 response，也不等待磁盘 fsync 或 ClickHouse。
socket 写使用立即 deadline/非阻塞语义；任何连接失败、EAGAIN、部分写或协议错误都
关闭本 attempt 会话，由 sidecar 丢弃 partial，并计为 capture loss。绝不重试到请求
协程，更不回压客户端响应。

认证、Cookie 和敏感自定义 header 必须在主进程中脱敏后才允许进入 IPC。运行时内容
策略关闭某个原文列时，该列不得写入 spool；如果 sidecar 仍需观察字节以计算长度、
hash 或抽取元数据，只能流式处理并丢弃，不能先完整落盘再删除。

请求 body 已是上游发送所需的 wire buffer，发送 IPC 时按切片分帧，不再复制一份完整
record。响应 wrapper 在正常读取过程中把同一批已读取字节分帧交给 IPC；不得为了
capture 额外读取、延迟关闭或改变现有 provider 生命周期。

### 6.3 socket 安全

- 默认路径：`/app/data/capture/capture.sock`；
- socket 和父目录权限为 `0600`/`0700`，仅容器内 `sub2api` 用户可访问；
- 不通过 argv 传 ClickHouse 密码或 Tailscale auth key；
- 日志只记录 capture ID、错误类别和大小，不记录原始 body、headers 或秘密。

## 7. Spool 格式、容量和提交语义

### 7.1 目录

```text
/app/data/capture/
├── spool/
│   ├── partial/   # 尚未收到 COMMIT，不可上传
│   ├── ready/     # 已完整、已 fsync、可上传的 immutable record
│   └── sending/   # immutable batch manifest 与 ack marker
└── tsnet/         # tsnet node key/state；不是归档正文
```

每条 partial 是一个独立目录，包含版本化 manifest、zstd request/response 文件和必要的
小型 metadata。原始二进制按字节保存；不假定 UTF-8。manifest 至少包含：

- `spool_version`、`capture_version`、`capture_id`；
- 最终 attempt 元数据与内容策略；
- request/response 的 observed bytes、stored bytes、SHA-256；
- 每个文件的压缩后长度、未压缩长度和校验值；
- request/response/header 的独立截断或完整性状态。

正文 request 和 response 各自上限 32 MiB；脱敏后的 request headers 和 response
headers 各自上限 1 MiB。内容开关关闭的列在最终 record 中为空，不会写入 ClickHouse。

### 7.2 durable commit

收到 `COMMIT` 后 sidecar：

1. 结束 zstd stream；
2. 写完并 fsync 所有文件；
3. 写 manifest 临时文件并 fsync；
4. fsync partial record 目录；
5. 在同一文件系统原子 rename 到 `ready/<capture_id>`；
6. fsync `ready` 目录。

只有完成第 6 步才叫“已提交到 spool”。主进程不等待这套流程，因此在最终结果已经
确定但 `COMMIT` 尚未被 sidecar 接收/落稳的崩溃窗口内，仍可能丢这一条。这个窗口会
被计数和测试，但不能在 fail-open 前提下伪装成严格零丢失。

### 7.3 物理容量

固定默认值：

| 项目 | 值 |
|---|---:|
| spool 最大物理占用 | 12 GiB（`12884901888` bytes） |
| 文件系统最小剩余空间 | 8 GiB（`8589934592` bytes） |
| sidecar Go memory soft limit | 256 MiB |
| IPC frame | 约 64 KiB |
| request/response 单方向保存上限 | 32 MiB |
| 单组 headers 保存上限 | 1 MiB |
| 同时接收的 attempt 上限 | 32 |
| 单批上传最大字节数 | 128 MiB（`134217728` bytes） |
| sending 元数据保留空间 | 16 MiB（`16777216` bytes） |

占用统计覆盖 `partial + ready + sending manifest/marker`，以实际文件分配和 sidecar 的
并发预留账本为准，启动时全量扫描校准。每次写下一帧前都同时检查 12 GiB cap 和
8 GiB free reserve；任一条件不满足，就拒绝新的 capture 或中止尚未 commit 的新
record。已经 ready 的旧积压优先保留，uploader 继续排空，不能为了接收新数据删除旧
数据。磁盘限制只丢 capture，不影响转发。

结合已只读核对的正式服容量（根盘约 38 GiB、当时可用约 27 GiB），spool 达到 12 GiB
时仍约有 15 GiB 余量；如果日志、镜像或其他数据先增长，8 GiB reserve 会更早生效。

### 7.4 重启恢复

- `ready/`：启动后全部重新加入上传顺序；
- `sending/*.manifest` 且没有 ack：使用相同 batch ID 和完全相同 record 集合重传；
- `sending/*.acked`：只完成本地清理，禁止再次上传；
- 旧进程遗留 `partial/`：没有 COMMIT 证明，启动时计为 orphan 并删除；
- manifest 或数据校验失败：记录 `spool_corrupt` loss，删除损坏 record，避免永久卡队。

ClickHouse 离线时只是 delivery degraded，不等于数据丢失；只要记录已经在 `ready/`
且未触发本地容量限制，应用/容器/sidecar 重启后会自动续传。

## 8. ClickHouse 上传与去重

### 8.1 HTTP streaming

sidecar 不再使用 clickhouse-go native batch 把多行 `String` 全部物化到 heap。它按
immutable batch manifest 打开 ready 文件，流式解压并编码 `FORMAT RowBinary`，通过
HTTP 写入。HTTP request 本身使用流式 zstd 压缩；集成测试必须验证目标 ClickHouse
版本的解压设置和 RowBinary 二进制 round-trip，不能仅靠 mock 通过。

默认单 uploader、每批最多 100 行，并同时受 batch byte ceiling 约束。batch 不在内存
中拼接；内存只包含元数据、压缩器状态和少量固定 buffer。

### 8.2 durable batch manifest

上传前先生成 `sending/<batch_id>.manifest.tmp`，写入有序 capture ID 列表和各 record
校验值，fsync 后原子 rename 为 `.manifest`。record 本身保持在 `ready/`，直到收到
ClickHouse 成功响应并把 `.acked` marker fsync。这样即使清理一半时崩溃，恢复过程也
只继续删除；如果在 ClickHouse commit 后、ack marker 前崩溃，则使用完全相同的 batch
和 batch ID 重传。

每次 INSERT 使用固定 `batch_id` 作为 `insert_deduplication_token`。Windows 节点的
非复制 MergeTree 启用 `non_replicated_deduplication_window=100000`，抑制上述不确定
ack 窗口造成的重复 block。`capture_id` 作为稳定行标识保存在表中，便于审计和极端
情况下离线去重。这里提供的是 crash-safe at-least-once + ClickHouse 去重，不宣称跨
手工改表、去重窗口淘汰或灾难恢复后的数学 exactly-once。

### 8.3 表结构演进

保留现有业务列和四个原文 `String` 列，新表契约固定新增以下字段：

- `capture_id UUID`、`ingest_batch_id UUID`；
- `request_observed_bytes`、`request_stored_bytes`；
- `response_observed_bytes`、`response_stored_bytes`；
- `request_sha256`、`response_sha256`；
- `request_truncated`、`response_truncated`，并保留兼容汇总列 `is_truncated`；
- `spool_version` 与递增后的 `capture_version`。

ClickHouse `String` 可以保存任意二进制，所以 AWS EventStream、SSE、JSON 和非 UTF-8
body 都直接在表里。正常查询原文从 ClickHouse 读取；二进制内容可用 `hex()` 或
`base64Encode()` 导出，不依赖容器 spool。

schema 由 Windows 部署手册中的显式 SQL 创建/迁移。sidecar 用户只授予目标表的
`INSERT`，不再持有 `CREATE TABLE`、`ALTER`、`DROP` 或 `DELETE` 权限。应用版本和表
版本不兼容时 delivery 失败并保留 spool，由管理员先执行迁移再自动续传。

## 9. 嵌入式 Tailscale 网络

sidecar 内嵌 `tailscale.com/tsnet`，使用 `/app/data/capture/tsnet` 持久化 node state，
设备身份为 `tag:capture-writer`。首次加入 tailnet 需要预授权 key；成功后依靠持久 state
重启，不重复注册设备。auth key 和 ClickHouse 密码从静态配置/环境读取，统一脱敏，
不得出现在状态 API、进程 argv 或日志。

Windows ClickHouse 主机运行官方 Tailscale 客户端，设备标记 `tag:capture-db`，并配置：

```text
tailnet tcp:18000
  -> Tailscale Serve --bg
  -> Windows 127.0.0.1:18123
  -> Docker ClickHouse 8123
```

Tailnet policy 只允许 `tag:capture-writer` 访问 `tag:capture-db:18000`。Windows 不开放
公网或局域网 ClickHouse 端口，也不启用 Funnel。正式服应用宿主机不需要 Tailscale。

静态开关为 true、运行时总开关为 false 时，sidecar 仍保持运行并排空旧 spool；只是
主进程不产生新 capture。静态 false 才完全不启动 sidecar/tsnet。

## 10. 管理状态和错误模型

现有 `provisioned/ready` 需要拆成可解释状态：

- `provisioned`：静态 capture 已开启；
- `sidecar_running`：受监督子进程和 IPC 握手正常；
- `spool_ready`：目录可写、格式兼容、容量检查可用；
- `delivery_ready`：tsnet 和 ClickHouse HTTP 最近一次探测/上传成功；
- `spool_used_bytes`、`spool_max_bytes`、`filesystem_free_bytes`；
- `ready_records`、`oldest_ready_age_seconds`、当前 batch、最近成功上传时间；
- sidecar restart count、upload retry count 和按原因 loss counters。

管理员打开运行时总开关的前置条件改为 `sidecar_running && spool_ready`，不再要求
ClickHouse 当时在线。否则 Windows 未登录时永远无法开始可靠落盘。`delivery_ready=false`
只显示积压/重试状态，不阻止本地 capture。

建议 spool 告警阈值为 70%/85%/95%；达到硬 cap 或 free reserve 时明确显示“只丢新
capture，转发未受影响”。sidecar 不连接 PostgreSQL；它通过 IPC status 帧把容量、
上传和 drop 增量交给主进程，由现有 capture health reporter 写入 PostgreSQL/告警。

### 10.1 结果矩阵

| 场景 | 转发 | 已 commit spool | 新 capture |
|---|---|---|---|
| ClickHouse/Windows 离线 | 不受影响 | 保留并重试 | 继续落 spool |
| sidecar 重启 | 不受影响 | 启动后续传 | IPC 恢复前丢弃并计数 |
| 主进程/容器重启 | 正常恢复 | ready/sending 自动续传 | 未 COMMIT partial 丢弃 |
| spool 达 12 GiB | 不受影响 | 保留并继续上传 | 丢弃 |
| 剩余空间低于 8 GiB | 不受影响 | 保留并继续上传 | 丢弃 |
| spool 文件损坏 | 不受影响 | 损坏条目计数后删除 | 其他记录继续 |
| schema 不兼容/鉴权失败 | 不受影响 | 保留并退避重试 | 继续落 spool，直到容量边界 |

## 11. 配置契约

最终字段名可在实现计划中按现有 Viper 风格细化，但语义和默认值固定如下：

```yaml
gateway:
  capture:
    enabled: false
    max_body_bytes: 33554432
    max_header_bytes: 1048576
    spool:
      dir: /app/data/capture/spool
      max_bytes: 12884901888
      min_free_bytes: 8589934592
    sidecar:
      socket: /app/data/capture/capture.sock
      frame_bytes: 65536
      memory_limit_bytes: 268435456
      max_active_attempts: 32
    tailscale:
      state_dir: /app/data/capture/tsnet
      hostname: sub2api-capture-writer
      auth_key: ""
    clickhouse:
      url: http://clickhouse-win:18000
      database: llm_archive
      table: model_call_archive
      username: capture_ingest
      password: ""
      compression: zstd
      batch_max_rows: 100
      batch_max_bytes: 134217728
      batch_max_interval_ms: 2000
      dial_timeout_ms: 5000
      write_timeout_ms: 60000
```

校验规则：静态 false 时不要求任何 secret/endpoint；静态 true 时所有目录必须位于
`/app/data/capture`，spool cap、free reserve、frame 和内容上限必须为正，ClickHouse
URL 必须是 HTTP(S) 且不能包含 userinfo，秘密不得通过 URL query 配置。

部署时只通过环境变量 `GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY` 与
`GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD` 注入两项 secret；文档、状态、日志和 argv
均不得包含其值。`gateway.capture.enabled=false` 是默认值，静态关闭时不创建 sidecar、
tsnet、socket 或 spool。`ready/` 记录和未 ack 的固定 batch 会在 sidecar、网关或容器
重启后使用原 batch ID 和原 record 集合自动续传。

## 12. 资源控制

- sidecar 启动时设置 Go memory soft limit 256 MiB；所有 body I/O 使用固定小 buffer。
- sidecar 将自己的 Linux `oom_score_adj` 调高，使极端 cgroup OOM 时优先牺牲归档进程；
  若内核/容器策略不允许，记录告警但继续运行。
- 单 uploader、有限并发压缩、有限同时打开文件数；不能按 backlog 大小创建 goroutine。
- 主进程只保留每个活跃 attempt 的 socket/少量状态，不再按 `queue_size × body` 保留
  capture record。
- 12 GiB 是 spool 硬边界，不允许设置 0 表示无限。

## 13. 测试与验证

### 13.1 单元和故障注入

- 静态关闭：不创建目录、socket、进程或 tsnet；capture 热路径零副作用。
- 运行时关闭：不接收新 record，但 sidecar 能排空既有 backlog。
- 最终 attempt：success、terminal error、retry、failover、Kiro WebSearch/MCP、Bedrock、
  OpenAI、Gemini、Antigravity、Grok 都只 commit 最终真实 pair。
- IPC：EAGAIN、partial write、协议版本错误、sidecar crash 不改变客户端结果。
- spool：在文件写、manifest fsync、rename、ready fsync 各阶段 kill，恢复结果符合状态机。
- upload：在发送前、ClickHouse commit 后 ack 前、ack 后清理中 kill；相同 batch 重放且
  无可见重复。
- 容量：12 GiB cap、8 GiB reserve、并发 reservation、启动扫描和旧 backlog 优先。
- 二进制：非 UTF-8、AWS EventStream、NUL byte、32 MiB 边界、截断/hash/length。
- race/load：高并发下主进程 heap 不随 ClickHouse backlog 增长；sidecar buffer 有界。
- lifecycle：管理员原地更新未重启时 sidecar 仍与父进程同版本；容器重启后切新版本。

### 13.2 本地集成

在开发机启动固定版本 ClickHouse 26.3 临时容器，直接连接其 HTTP 8123，验证真实 DDL、
RowBinary、HTTP zstd、二进制 round-trip、dedup token、离线重试和 schema mismatch。
测试容器不能证明 Windows/Tailnet 已验证，只证明上传协议和 ClickHouse 兼容。

### 13.3 Windows/Tailnet 验收

目标 Windows 机器部署后，再按独立 runbook 验证 Docker Desktop 登录启动、named
volume、Tailscale unattended/Serve、ACL、真实 tailnet 上传、Windows 重启后的积压
恢复。目标机尚未部署前，这一层必须明确标为“未验证”。

正式服的任何安装、配置、重启或真实 provider 测试都需要用户再次确认。真实测试必须
走现有网关代码和正式服账号配置的 IP 代理，不允许绕过服务裸调 provider。

## 14. 迁移和发布顺序

1. 实现 sidecar、IPC、spool、HTTP uploader 和新 health contract，保持静态默认 false。
2. 用临时 ClickHouse 完成本地 unit/integration/race/load/kill-point 验证。
3. 在 Windows 节点部署 ClickHouse、schema、最小权限账号和 Tailscale Serve。
4. 只读验证网络和表；此时正式服 capture 仍为 false。
5. 经用户确认后更新正式服 binary/镜像并重启，仍保持静态 false 验证普通转发。
6. 经再次确认后写入静态 endpoint/secret 并重启；确认 sidecar/spool ready。
7. 最后从管理员页面打开运行时策略，观察 70/85/95% 容量与上传指标。
8. 做一次受控 ClickHouse 离线/恢复演练，确认 ready backlog 自动清空且 capture ID 不重。

回滚时先在管理员页面关闭运行时总开关，让已存在 backlog 尽量排空，再回滚 binary。
如果必须立即回滚，保留 `/app/data/capture`；旧版本不识别它但不得删除，恢复新版本后
可以继续上传。Windows ClickHouse 表不需要随应用回滚删除。

## 15. 验收标准

- 静态默认 false，关闭时不存在 sidecar、tsnet 和 spool 活动。
- 主进程中不再存在保存完整 capture record 的 Pond 队列和 ClickHouse writer 队列。
- 高并发/远端离线时主进程 capture heap 使用与 backlog 大小解耦。
- ready/sending 数据跨 sidecar、应用和容器重启自动续传。
- spool 物理占用不越过 12 GiB，且保留至少 8 GiB 文件系统余量。
- ClickHouse 写入失败绝不改变 provider 转发、计费或客户端响应。
- 所有保留正文都在 ClickHouse `String` 列中；上传确认后容器不保留永久正文。
- 通过 ClickHouse 可以按 `capture_id` 取回 request/response，包括二进制内容。
- 管理页能区分 sidecar/spool 可用和远端 delivery 可用，并显示积压、最旧年龄和 loss。
- 一键更新/回滚只处理同一个 binary，父子进程版本不会混用。
- Windows 部署手册可由另一名 agent 在没有本次聊天上下文的情况下独立执行。

## 16. 参考资料

- [tsnet Go package](https://pkg.go.dev/tailscale.com/tsnet)
- [Tailscale Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve)
- [Tailscale unattended mode](https://tailscale.com/docs/how-to/run-unattended)
- [Docker Desktop on Windows](https://docs.docker.com/desktop/setup/install/windows-install/)
- [Docker Desktop WSL best practices](https://docs.docker.com/desktop/features/wsl/best-practices/)
- [Docker volumes](https://docs.docker.com/engine/storage/volumes/)
- [ClickHouse HTTP interface](https://clickhouse.com/docs/interfaces/http)
- [ClickHouse insert deduplication notes](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse#6-deduplication-at-insert-time)
