# Capture ClickHouse Windows 节点部署手册

- 更新日期：2026-08-16
- 目标系统：Windows 10/11 + Docker Desktop WSL2
- 用途：在独立 Windows 个人电脑上部署 capture 专用 ClickHouse，并通过 Tailscale
  私网接收正式服 sidecar 的 HTTP RowBinary 上传
- 对应设计：`docs/superpowers/specs/2026-08-16-capture-disk-spool-sidecar-design.md`

本文是一份可独立交给另一名 agent 执行的 runbook。它不要求阅读原聊天记录。

> 实现状态：仓库已实现 `sub2api capture-sidecar`、磁盘 spool 和本文表结构，并已用
> `clickhouse/clickhouse-server:26.3.17.110` 完成本地真实 HTTP RowBinary/zstd 集成验证。
> Windows/Tailnet 和正式服尚未因此自动完成部署；若目标 binary 的
> `sub2api capture-sidecar --help` 不存在，停止接入。

> 正式服纪律：Windows 节点可以独立准备；任何正式服安装、配置修改、更新、重启或
> 真实 provider 验证，执行前必须再次获得用户确认。真实请求必须走现有服务链路并复用
> 正式服账号配置的 IP 代理，不允许为了验收绕过网关裸调上游。

## 1. 最终拓扑和边界

```text
正式服 sub2api 容器
  └─ capture-sidecar 内嵌 tsnet（tag:capture-writer）
                 │
                 │ tailnet TCP 18000
                 ▼
Windows 官方 Tailscale 客户端（tag:capture-db）
  └─ Tailscale Serve --bg
       └─ 127.0.0.1:18123
            └─ Docker Desktop / WSL2
                 └─ ClickHouse HTTP 8123
                      └─ Docker named volume: clickhouse_data
```

固定端口：

| 位置 | 端口 | 暴露范围 |
|---|---:|---|
| ClickHouse 容器 | `8123/tcp` | 容器内部 HTTP |
| Windows Docker 映射 | `127.0.0.1:18123` | 仅 Windows 本机回环 |
| Tailscale Serve | `18000/tcp` | 仅 tailnet |
| ClickHouse native | `9000/tcp` | 不发布、不使用 |

这套新链路不是旧文档中的 `19000 -> 19001 -> 9000`。`18000 -> 18123 -> 8123`
分别是 tailnet 入口、Windows loopback 映射和 ClickHouse HTTP 端口。Windows 防火墙和
路由器不需要开放 ClickHouse 公网端口，禁止启用 Tailscale Funnel。

所有长期正文只在 ClickHouse named volume 中。本地正式服 spool 只是待上传缓冲，
ClickHouse ACK 后自动删除；以后查询对话不需要进入正式服容器。

## 2. 已接受的 Windows 启动模型

采用 Docker Desktop WSL2，而不是 Hyper-V 常驻 Linux VM：

- Tailscale 设置 unattended，可在无人登录时运行；
- Docker Desktop 设置为“登录 Windows 后自动启动”；
- ClickHouse 容器设置 `restart: unless-stopped`，Docker engine 启动后自动恢复；
- Windows 重启但尚未登录期间，ClickHouse 被视为离线；正式服 sidecar 在 12 GiB
  spool 范围内继续积压；登录 Windows 后自动续传。

这不是无人登录即可恢复 ClickHouse 的方案。如果以后需要真正无人值守，应迁移到
Linux 主机或 Hyper-V Linux VM，而不是在此 runbook 上继续叠加计划任务和 WSL hack。

## 3. 机器要求

建议起点：

- Windows 10 22H2+ 或 Windows 11，x86-64，已开启硬件虚拟化；
- Docker Desktop 支持的 WSL2 版本，内存至少 8 GiB，推荐 16 GiB；
- 4 个 CPU 线程；
- SSD 可用空间至少 200 GiB；长期保留必须根据实测日增量扩容；
- 稳定的 Windows 用户登录和开机密码/远程登录方式；
- 能加入与正式服 sidecar 相同的 tailnet。

不要把 `/var/lib/clickhouse` bind mount 到 `C:\...` 或 `/mnt/c/...`。ClickHouse 会产生
大量 Linux 文件系统 I/O，数据必须放 Docker named volume（WSL2 Linux filesystem）。
项目目录中的 Compose 和 `.env` 很小，可以放 `C:\sub2api-clickhouse`。

官方参考：

- [Docker Desktop Windows 安装](https://docs.docker.com/desktop/setup/install/windows-install/)
- [Docker Desktop WSL2 最佳实践](https://docs.docker.com/desktop/features/wsl/best-practices/)
- [Docker named volumes](https://docs.docker.com/engine/storage/volumes/)
- [Tailscale Windows 安装](https://tailscale.com/docs/install/windows)

## 4. 安装 Docker Desktop

1. 安装/更新 WSL2：

   ```powershell
   wsl --install
   wsl --update
   wsl --version
   ```

2. 安装 Docker Desktop，选择 WSL2 backend。完成后启动一次并接受许可。
3. Docker Desktop → Settings → General：
   - 启用 **Use the WSL 2 based engine**；
   - 启用 **Start Docker Desktop when you sign in to your computer**。
4. 验证：

   ```powershell
   docker version
   docker compose version
   docker info
   ```

如果 `docker info` 失败，先确认 Docker Desktop 已启动；不要继续建库。

## 5. 安装和配置 Windows Tailscale

安装官方 Windows 客户端并登录目标 tailnet。以管理员 PowerShell 执行：

```powershell
$Tailscale = "$env:ProgramFiles\Tailscale\tailscale.exe"
& $Tailscale status
& $Tailscale up --unattended=true
```

在 Tailscale 管理后台将此设备命名为容易识别的名称，例如 `clickhouse-win`，并赋予
`tag:capture-db`。tag owner 和访问规则示例：

```json
{
  "tagOwners": {
    "tag:capture-writer": ["autogroup:admin"],
    "tag:capture-db": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["tag:capture-writer"],
      "dst": ["tag:capture-db"],
      "ip": ["tcp:18000"]
    }
  ]
}
```

如果 tailnet policy 已经有其他规则，只合并上述 tag 和 grant，不要覆盖整份 policy。
正式服 tsnet 的预授权 key 必须带 `tag:capture-writer`，并由应用静态 secret 提供；
不要复用 Windows 设备的登录凭据。

官方参考：[Windows unattended mode](https://tailscale.com/docs/how-to/run-unattended)。

## 6. 创建部署目录和秘密

以普通 PowerShell 创建目录：

```powershell
New-Item -ItemType Directory -Force C:\sub2api-clickhouse | Out-Null
Set-Location C:\sub2api-clickhouse
```

生成只含十六进制字符的强密码，写入本机 `.env`：

```powershell
function New-HexSecret {
  $bytes = New-Object byte[] 32
  $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
  try {
    $rng.GetBytes($bytes)
  } finally {
    $rng.Dispose()
  }
  return -join ($bytes | ForEach-Object { $_.ToString("x2") })
}

$AdminPassword = New-HexSecret
$IngestPassword = New-HexSecret

@"
CLICKHOUSE_DB=llm_archive
CLICKHOUSE_ADMIN_USER=capture_admin
CLICKHOUSE_ADMIN_PASSWORD=$AdminPassword
CLICKHOUSE_INGEST_USER=capture_ingest
CLICKHOUSE_INGEST_PASSWORD=$IngestPassword
"@ | Set-Content -Encoding ascii .env
```

保护 `.env`：

```powershell
icacls .env /inheritance:r
icacls .env /grant:r "$env:USERNAME:(R,W)"
```

不要把 `.env` 发给 agent、提交 Git 或贴到聊天。需要给正式服配置时，只通过用户认可的
secret 传递方式提供 `capture_ingest` 密码，不提供 admin 密码。

## 7. Compose 配置

在 `C:\sub2api-clickhouse\compose.yaml` 写入：

```yaml
name: sub2api-capture

services:
  clickhouse:
    image: clickhouse/clickhouse-server:26.3.17.110
    container_name: sub2api-capture-clickhouse
    restart: unless-stopped
    environment:
      CLICKHOUSE_DB: ${CLICKHOUSE_DB}
      CLICKHOUSE_USER: ${CLICKHOUSE_ADMIN_USER}
      CLICKHOUSE_PASSWORD: ${CLICKHOUSE_ADMIN_PASSWORD}
      CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT: "1"
      CLICKHOUSE_INGEST_USER: ${CLICKHOUSE_INGEST_USER}
      CLICKHOUSE_INGEST_PASSWORD: ${CLICKHOUSE_INGEST_PASSWORD}
    ports:
      - "127.0.0.1:18123:8123"
    volumes:
      - clickhouse_data:/var/lib/clickhouse
      - clickhouse_logs:/var/log/clickhouse-server
    ulimits:
      nofile:
        soft: 262144
        hard: 262144
    healthcheck:
      test:
        - CMD-SHELL
        - >-
          wget -q -T 5 -O -
          --user="$${CLICKHOUSE_USER}"
          --password="$${CLICKHOUSE_PASSWORD}"
          http://127.0.0.1:8123/ping | grep -q Ok
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 30s

volumes:
  clickhouse_data:
  clickhouse_logs:
```

注意：

- 镜像必须固定到已验证的 26.3 LTS 小版本，不使用 `latest`；
- 部署当天如果要换更新的 26.3 patch，先查 release notes 并在临时容器验证 schema、
  RowBinary 和 dedup，再显式改版本；
- `127.0.0.1:18123` 不能改成 `0.0.0.0`；
- `clickhouse_data` 是长期数据；`clickhouse_logs` 可按需轮转；
- 永远不要执行 `docker compose down -v`，它会删除 named volume 和全部归档。

校验配置并启动：

```powershell
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail 100 clickhouse
```

本机健康检查：

```powershell
$Settings = ConvertFrom-StringData ((Get-Content .env | Where-Object { $_ -match '^[^#].*=.*$' }) -join "`n")
$Pair = "{0}:{1}" -f $Settings.CLICKHOUSE_ADMIN_USER, $Settings.CLICKHOUSE_ADMIN_PASSWORD
curl.exe --fail --silent --show-error --user $Pair http://127.0.0.1:18123/ping
```

预期输出 `Ok.`。

## 8. 创建数据库、表和最小权限用户

下面的 schema 是 sidecar v2 契约。不要让 sidecar 在启动时自己改表。

在 PowerShell 中加载 `.env`，再把 SQL 送入容器内的 admin client：

```powershell
$Settings = ConvertFrom-StringData ((Get-Content .env | Where-Object { $_ -match '^[^#].*=.*$' }) -join "`n")

$SchemaSql = @'
CREATE DATABASE IF NOT EXISTS llm_archive;

CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive
(
    captured_at              DateTime64(3) DEFAULT now64(3),
    capture_id               UUID,
    ingest_batch_id          UUID,
    request_id               String,
    session_id               String,
    platform                 LowCardinality(String),
    requested_model          LowCardinality(String),
    upstream_model           LowCardinality(String),
    upstream_endpoint        String,
    stream                   UInt8,
    http_status              UInt16,
    stop_reason              LowCardinality(String),
    thinking_effort          LowCardinality(String),
    thinking_type            LowCardinality(String),
    input_tokens             UInt32,
    output_tokens            UInt32,
    cache_read_tokens        UInt32,
    cache_creation_tokens    UInt32,
    signature_present        UInt8,
    is_truncated             UInt8,
    request_truncated        UInt8,
    response_truncated       UInt8,
    request_observed_bytes   UInt64,
    request_stored_bytes     UInt64,
    response_observed_bytes  UInt64,
    response_stored_bytes    UInt64,
    request_sha256           FixedString(64),
    response_sha256          FixedString(64),
    spool_version            UInt16,
    capture_version          UInt16,
    raw_request              String CODEC(ZSTD(3)),
    raw_response             String CODEC(ZSTD(3)),
    request_headers          String CODEC(ZSTD(3)),
    response_headers         String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (session_id, captured_at, request_id, capture_id)
SETTINGS
    index_granularity = 8192,
    non_replicated_deduplication_window = 100000;
'@

$SchemaSql | docker compose exec -T clickhouse clickhouse-client `
  --user $Settings.CLICKHOUSE_ADMIN_USER `
  --password $Settings.CLICKHOUSE_ADMIN_PASSWORD `
  --multiquery
```

创建只允许 INSERT 的 `capture_ingest`。密码由本机生成的 hex 组成，无需额外 SQL 转义：

```powershell
$IngestUser = $Settings.CLICKHOUSE_INGEST_USER
$IngestPassword = $Settings.CLICKHOUSE_INGEST_PASSWORD

if ($IngestUser -notmatch '^[a-zA-Z_][a-zA-Z0-9_]*$') {
  throw "Invalid CLICKHOUSE_INGEST_USER"
}
if ($IngestPassword -notmatch '^[0-9a-f]{64}$') {
  throw "CLICKHOUSE_INGEST_PASSWORD must be a 64-character lowercase hex secret"
}

$UserSql = @"
CREATE USER IF NOT EXISTS $IngestUser IDENTIFIED WITH sha256_password BY '$IngestPassword';
GRANT INSERT ON llm_archive.model_call_archive TO $IngestUser;
"@

$UserSql | docker compose exec -T clickhouse clickhouse-client `
  --user $Settings.CLICKHOUSE_ADMIN_USER `
  --password $Settings.CLICKHOUSE_ADMIN_PASSWORD `
  --multiquery
```

验证表和权限：

```powershell
"SHOW CREATE TABLE llm_archive.model_call_archive" | docker compose exec -T clickhouse clickhouse-client `
  --user $Settings.CLICKHOUSE_ADMIN_USER `
  --password $Settings.CLICKHOUSE_ADMIN_PASSWORD

"SHOW GRANTS FOR capture_ingest" | docker compose exec -T clickhouse clickhouse-client `
  --user $Settings.CLICKHOUSE_ADMIN_USER `
  --password $Settings.CLICKHOUSE_ADMIN_PASSWORD
```

预期：ingest 用户只有目标表 `INSERT`，没有 `CREATE TABLE`、`ALTER`、`DROP`、`DELETE`
或业务表查询权限。

> 如果节点已经存在旧 schema，不要依赖 `CREATE TABLE IF NOT EXISTS`。先执行 `DESCRIBE
> TABLE` 和 `SHOW CREATE TABLE`，再按实现版本提供的显式迁移 SQL 逐列升级。不要 drop
> 旧表重建，也不要在没有备份时批量复制/删除历史数据。

## 9. 发布 Tailscale Serve

ClickHouse 本机健康后，以管理员 PowerShell 执行：

```powershell
$Tailscale = "$env:ProgramFiles\Tailscale\tailscale.exe"
& $Tailscale serve --bg --tcp=18000 tcp://127.0.0.1:18123
& $Tailscale serve status
& $Tailscale status
```

`--bg` 让 Serve 配置持久化并在 Tailscale 服务重启后恢复。不要使用 `funnel`。

从另一台已获 ACL 权限的 tailnet 设备测试：

```powershell
curl.exe --fail --silent --show-error http://clickhouse-win:18000/ping
```

如果 MagicDNS 名称不是 `clickhouse-win`，使用管理后台显示的完整 MagicDNS 名称或
Tailscale IPv4。预期输出 `Ok.`。这只能证明 tailnet 和 ClickHouse HTTP 可达，不能证明
正式服内嵌 tsnet 已经接入。

官方参考：[Tailscale Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve)、
[Serve examples](https://tailscale.com/docs/reference/examples/serve)。

## 10. 正式服 sidecar 配置交接

只有在实现、Windows 本机和 tailnet 检查都完成后，才准备正式服静态配置。关键值：

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
      auth_key: "<tag:capture-writer 的预授权 key>"
    clickhouse:
      url: http://clickhouse-win:18000
      database: llm_archive
      table: model_call_archive
      username: capture_ingest
      password: "<CLICKHOUSE_INGEST_PASSWORD>"
      compression: zstd
      batch_max_rows: 100
      batch_max_bytes: 134217728
      batch_max_interval_ms: 2000
      dial_timeout_ms: 5000
      write_timeout_ms: 60000
```

此处仍保留 `enabled: false`。生产接入必须拆成独立审批步骤：

- spool cap 固定为 12 GiB，文件系统 free reserve 固定为 8 GiB；sidecar 另保留
  16 MiB 仅供 bounded sending manifest/ack 元数据，以便满载时仍可排空旧 ready 数据；
- `ready/` 与未 ack 的固定 batch 会跨 sidecar、网关和容器重启续传，未 ack batch
  保持相同 batch ID 和相同 record 集合；
- 两项 secret 只使用环境变量 `GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY` 和
  `GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD` 注入，禁止把值写进文档、argv、日志或状态 API；
- `gateway.capture.enabled=false` 是默认值；静态关闭时不启动 sidecar/tsnet，也不创建
  socket 或 spool。运行时关闭只停止新 capture，已有 backlog 继续排空。

1. 更新正式服并重启，静态关闭，验证普通转发；
2. 经用户确认后放入 endpoint/secret，把静态开关改为 true 并重启；
3. 验证 `sidecar_running=true`、`spool_ready=true`，即使 `delivery_ready` 暂时 false；
4. 最后经用户确认，从管理员“转存设置”开启运行时策略。

不要在命令行参数、日志或截图中回显 auth key 和 ingest 密码。tsnet 首次成功注册后依靠
`/app/data/capture/tsnet` 持久 state；不要随意删除该目录。

## 11. 分层验收

### 11.1 Windows 节点，不接正式服

```powershell
docker compose ps
docker compose logs --tail 100 clickhouse
& "$env:ProgramFiles\Tailscale\tailscale.exe" serve status
curl.exe --fail --silent --show-error http://127.0.0.1:18123/ping
```

查询磁盘和表：

```powershell
$Settings = ConvertFrom-StringData ((Get-Content .env | Where-Object { $_ -match '^[^#].*=.*$' }) -join "`n")

@'
SELECT
    database,
    table,
    formatReadableSize(sum(bytes_on_disk)) AS bytes_on_disk,
    sum(rows) AS rows
FROM system.parts
WHERE active AND database = 'llm_archive' AND table = 'model_call_archive'
GROUP BY database, table;
'@ | docker compose exec -T clickhouse clickhouse-client `
  --user $Settings.CLICKHOUSE_ADMIN_USER `
  --password $Settings.CLICKHOUSE_ADMIN_PASSWORD

docker volume inspect sub2api-capture_clickhouse_data
```

### 11.2 开发环境协议集成

由实现 agent 在开发机临时 ClickHouse 26.3 容器上验证：

- HTTP RowBinary + zstd 真实写入；
- JSON/SSE/AWS EventStream/NUL/非 UTF-8 byte-for-byte round-trip；
- 相同 `insert_deduplication_token` 重放不产生可见重复；
- schema 不匹配时 spool 保留并重试；
- ClickHouse 停止/恢复后 backlog 自动清空。

### 11.3 正式服 Tailnet 端到端

必须在用户批准生产变更后进行：

1. 静态 true、运行时 false，确认 sidecar 已加入 tailnet；
2. 打开运行时 capture；
3. 通过现有网关和正式代理发起一条受控请求；
4. 用 `capture_id` 在 ClickHouse 查询 request/response；
5. 受控停止 Windows ClickHouse，产生少量记录，确认正式服 `ready_records` 增长；
6. 恢复 ClickHouse，确认 backlog 归零且每个 capture ID 只有一行。

目标 Windows 机器尚未部署前，只能完成 11.2，不能声称 11.3 已验证。

## 12. 查询和取回原始对话

所有查询在 ClickHouse 上执行，不访问正式服 spool。

最近记录：

```sql
SELECT
    captured_at,
    capture_id,
    request_id,
    platform,
    upstream_model,
    http_status,
    request_stored_bytes,
    response_stored_bytes,
    is_truncated
FROM llm_archive.model_call_archive
ORDER BY captured_at DESC
LIMIT 50;
```

按 capture ID 读取文本原文：

```sql
SELECT raw_request, raw_response
FROM llm_archive.model_call_archive
WHERE capture_id = toUUID('00000000-0000-0000-0000-000000000000');
```

对 AWS EventStream 或其他二进制内容，使用 base64：

```sql
SELECT
    base64Encode(raw_request) AS request_base64,
    base64Encode(raw_response) AS response_base64,
    request_sha256,
    response_sha256
FROM llm_archive.model_call_archive
WHERE capture_id = toUUID('00000000-0000-0000-0000-000000000000');
```

检查极端重试后的重复 ID：

```sql
SELECT capture_id, count() AS copies
FROM llm_archive.model_call_archive
GROUP BY capture_id
HAVING copies > 1
ORDER BY copies DESC;
```

不要把含用户原文的查询结果贴进公开 issue、CI 日志或聊天记录。

## 13. 日常运维

### 13.1 每周检查

```powershell
docker compose ps
docker compose logs --since 24h clickhouse
docker system df
& "$env:ProgramFiles\Tailscale\tailscale.exe" status
& "$env:ProgramFiles\Tailscale\tailscale.exe" serve status
```

ClickHouse 日增量：

```sql
SELECT
    toDate(captured_at) AS day,
    count() AS calls,
    formatReadableSize(sum(length(raw_request) + length(raw_response))) AS raw_bytes
FROM llm_archive.model_call_archive
GROUP BY day
ORDER BY day DESC
LIMIT 30;
```

Windows 数据盘建议阈值：70% 开始规划、85% 高优先级、95% 紧急。ClickHouse 没有 TTL，
不会自动删历史。“永久保留”不等于单节点备份；磁盘损坏、误删 named volume 或 Windows
故障仍会丢数据。需要灾备时应另行设计 ClickHouse backup/replica，不把正式服 spool
改造成永久副本。

### 13.2 Windows 重启演练

1. 记录重启前 `docker compose ps`、最新 `capture_id` 和正式服 spool 指标；
2. 重启 Windows；
3. 登录同一 Windows 用户；
4. 等 Docker Desktop 自动启动；
5. 验证 ClickHouse healthy、Serve status 正常；
6. 确认 sidecar 自动排空重启期间 backlog。

如果 Tailscale 已恢复但 Docker Desktop 尚未启动，`18000` 可能可连接但后端 18123
失败，这是已接受的状态，不要修改 Serve 指向公网接口。

## 14. 升级、回滚和恢复

### 14.1 ClickHouse patch 升级

1. 记录当前 image、`SHOW CREATE TABLE`、行数和磁盘占用；
2. 按 ClickHouse 官方 release notes 核对目标 26.3 LTS patch；
3. 在临时容器跑 sidecar 协议测试；
4. 修改 Compose 中的精确 tag；
5. `docker compose pull`，在维护窗口 `docker compose up -d`；
6. 验证 health、schema、查询和 sidecar backlog。

不要直接改成 `latest`。镜像回滚前先确认目标版本允许读取已升级的数据格式。

### 14.2 常见故障

| 现象 | 检查 | 处理 |
|---|---|---|
| Windows 本机 18123 不通 | Docker Desktop、`docker compose ps/logs` | 先恢复容器，不改 Tailscale ACL |
| 本机通、tailnet 18000 不通 | `tailscale status/serve status`、policy/tag | 恢复 unattended/Serve 或 ACL |
| tailnet 通、sidecar鉴权失败 | ingest 用户/密码、CH logs | 修复 secret；spool 保留自动重试 |
| schema mismatch | `SHOW CREATE TABLE` 与实现 schema version | 运行显式迁移，禁止 sidecar 自改表 |
| spool 持续增长 | Windows 是否登录、delivery 状态、CH logs | 优先恢复 Windows/CH；接近 cap 时关闭运行时 capture |
| ClickHouse volume 异常 | `docker volume inspect`、Docker Desktop disk | 停止写入并做恢复评估，不执行 `down -v` |

### 14.3 停用但保留数据

```powershell
docker compose stop clickhouse
```

这会停容器但保留 named volume。再次执行 `docker compose up -d` 可恢复。

禁止操作：

```text
docker compose down -v
docker volume rm sub2api-capture_clickhouse_data
docker system prune --volumes
```

这些命令可能不可恢复地删除全部归档；除非用户明确要求删除并已有可验证备份，否则
任何 agent 都不得执行。

## 15. 完成清单

- [ ] Docker Desktop 使用 WSL2，并设置登录 Windows 后自动启动。
- [ ] ClickHouse 使用固定 26.3 LTS tag，容器 healthy。
- [ ] `/var/lib/clickhouse` 使用 named volume，不是 NTFS bind mount。
- [ ] Windows 只监听 `127.0.0.1:18123`，未发布 9000/8123 到公网或局域网。
- [ ] Tailscale unattended 已启用，设备为 `tag:capture-db`。
- [ ] Serve `18000 -> 127.0.0.1:18123` 使用 `--bg`，未启用 Funnel。
- [ ] Tailnet policy 只允许 `tag:capture-writer -> tag:capture-db:18000`。
- [ ] v2 schema 与实现版本完全一致，dedup window 已设置。
- [ ] `capture_ingest` 只有目标表 INSERT 权限。
- [ ] 开发环境已完成真实 HTTP RowBinary/zstd/重放/二进制 round-trip。
- [ ] 正式服静态配置仍默认 false，所有生产变更另行获批。
- [ ] 正式服启用后完成离线积压与重启续传验收。
- [ ] 已记录 ClickHouse 磁盘告警和 named volume 禁删规则。

## 16. 参考资料

- [ClickHouse Docker install](https://clickhouse.com/docs/install/docker)
- [ClickHouse HTTP interface](https://clickhouse.com/docs/interfaces/http)
- [ClickHouse insert deduplication](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse#6-deduplication-at-insert-time)
- [Docker Desktop Windows install](https://docs.docker.com/desktop/setup/install/windows-install/)
- [Docker Desktop WSL2 backend](https://docs.docker.com/desktop/features/wsl/)
- [Docker WSL best practices](https://docs.docker.com/desktop/features/wsl/best-practices/)
- [Docker volumes](https://docs.docker.com/engine/storage/volumes/)
- [Tailscale Windows install](https://tailscale.com/docs/install/windows)
- [Tailscale unattended mode](https://tailscale.com/docs/how-to/run-unattended)
- [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)
