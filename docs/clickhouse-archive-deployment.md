# 上游调用转存：独立 ClickHouse 部署与正式服接入手册

- 更新日期：2026-08-11
- 适用分支：`dev`
- 目标：在任意一台独立机器上部署 ClickHouse，并让正式服 `sub2api` 通过 Tailscale 私网写入
- 数据留存：永久保留，不设置 ClickHouse TTL；容量不足时由管理员扩容或手工归档
- 关联设计：
  - `docs/superpowers/specs/2026-07-07-model-call-archive-design.md`
  - `docs/superpowers/specs/2026-08-11-capture-settings-openai-http-design.md`

本文档是部署 runbook，也是本次方案的统一记录。它不再依赖原先讨论的 Windows、WSL 或 D 盘；标准方案是一台独立 Linux 主机。以后换机器时，只需替换 ClickHouse 主机、数据盘和 Tailscale 地址。

> 生产纪律：阅读、检查可以直接执行；任何正式服更新、配置修改、安装 Tailscale、重启或真实上游验证，都必须在执行前单独获得确认。本手册中的命令不会自动授权生产变更。

---

## 1. 最终方案摘要

### 1.1 已确定的架构

```text
用户请求
   │
   ▼
正式服 sub2api 容器
   ├── 原转发链路 ─────────────────────────► 上游 Provider
   │                                              │
   │                                   最终请求/原始响应快照
   │                                              │
   └── 内存一级队列 ─► worker ─► 二级写入队列 ─► batch
                                                  │
                                                  ▼
正式服宿主机 Tailscale（tag:capture-writer）
                                                  │ 加密 tailnet
                                                  ▼
ClickHouse 主机 Tailscale（tag:capture-db）:19000
                                                  │ Tailscale Serve raw TCP
                                                  ▼
ClickHouse 主机 127.0.0.1:19001
                                                  │ Docker 端口映射
                                                  ▼
ClickHouse 容器 :9000 ─► 独立持久数据盘
```

固定端口约定：

| 位置 | 端口 | 暴露范围 |
|---|---:|---|
| ClickHouse 容器 | `9000/tcp` | 仅容器内部 native TCP |
| ClickHouse 宿主机 | `127.0.0.1:19001` | 仅本机回环 |
| Tailscale Serve | `19000/tcp` | 仅 tailnet |
| ClickHouse HTTP | `8123` | 不发布 |

正式服配置连接 ClickHouse 主机的 Tailscale IPv4 `:19000`。Docker 不向公网或局域网发布 ClickHouse 端口，Tailscale policy 只允许 `tag:capture-writer` 访问 `tag:capture-db` 的 `tcp:19000`。

### 1.2 为什么采用这个方案

- ClickHouse 与 2 GiB 的正式服应用机隔离，查询、合并和磁盘 IO 不抢转发资源。
- Tailscale 提供端到端加密和设备身份，不需要把 `9000/9440` 裸露公网。
- Tailscale 安装在宿主机，不在 `sub2api` 容器中；应用更新、容器重启不会让机器重新加入 tailnet。
- ClickHouse 只绑定本机回环；即使 Docker 的防火墙规则变化，也没有公网监听面。
- 归档链路 fail-open：ClickHouse 故障不影响用户请求，但可能丢失转存记录，丢失必须可见。

### 1.3 明确不做的事

- 不把 ClickHouse 部署到正式服应用机。
- 不向公网开放 ClickHouse native/HTTP 端口。
- 不实现本地磁盘 spool；断线期间的记录不会事后补写。
- 不让转存写入反压或阻断主转发链路。
- 不自动清理历史记录；“永久保留”意味着没有 TTL，但磁盘仍然是有限的。
- 不把“永久保留”当成备份保证；当前是单节点，磁盘损坏仍可能丢数据，需要时另配数据盘快照或异机备份。
- 不因为接入 Tailscale 或 ClickHouse 重装正式服系统。

---

## 2. 当前实现与待补能力

部署前必须先核对目标 `sub2api` 版本，不能只看文档。

| 能力 | 当前 `dev` | 部署要求 |
|---|---|---|
| 管理员左侧「转存设置」 | 已实现 | 正式服更新后应能看到独立菜单 |
| 静态 ClickHouse provisioning | 已实现，默认关闭 | `gateway.capture.enabled=false` 是安全默认值 |
| Kiro / Anthropic 转存 | 已实现 | 仍受运行时平台、结果、用户和分组策略控制 |
| OpenAI 三个 HTTP 文本入口 | 已实现 | OpenAI 平台默认关闭；与 `openai_passthrough` 无关 |
| 队列/写入丢失实时可见 | 已实现 | 管理页显示计数、时间、原因、峰值和最近事件 |
| 30 天丢失历史 | 已实现 | 写入主 PostgreSQL 的 `capture_health_events` |
| 启动时 ClickHouse 初始化失败后自动重连 | 尚未实现 | 当前需恢复 ClickHouse 后重启 `sub2api`；目标版本要加入指数退避重连 |
| Capture 指标接入现有 Ops 邮件 | 尚未实现 | 当前有管理页和日志；目标版本再补告警事件/恢复邮件 |

因此推荐把正式服接入拆成两步：

1. 先更新正式服代码，但保持 `gateway.capture.enabled=false`，验证新菜单和普通转发。
2. ClickHouse 主机部署完成、网络和账号验证通过后，再配置并开启静态 provisioning。

这样可以先安全发布代码，不要求 ClickHouse 同时在线。

---

## 3. 会转存什么

### 3.1 支持范围

| 平台 | 当前覆盖 | 备注 |
|---|---|---|
| Anthropic | 已接入的 Anthropic capture 路径 | 保存最终实际调用的上游请求/响应边界 |
| Kiro | Kiro 直连和兼容中转路径 | 保持现有 Anthropic 语义请求/翻译响应边界，同时保存脱敏的真实 Kiro 上游头 |
| OpenAI | `/v1/responses`、`/v1/chat/completions`、`/v1/messages` | 保存最终 outbound body 和协议转换前的原始上游 JSON/SSE；不依赖 passthrough |

OpenAI WebSocket、图片、视频、Embeddings、Alpha Search/compact 等非上述 HTTP 文本路径不在当前范围。中间 retry/failover 不单独入库：成功只保存最终成功 attempt；错误只保存最终返回客户端的终态上游 HTTP 错误。本地合成、请求到达上游前产生的错误不存。

### 3.2 每条记录的字段

一次完成的最终上游调用最多写一行 `llm_archive.model_call_archive`：

| 类别 | 字段 |
|---|---|
| 时间与关联 | `captured_at`、`request_id`、`session_id` |
| 路由与模型 | `platform`、`requested_model`、`upstream_model`、`upstream_endpoint` |
| 调用结果 | `stream`、`http_status`、`stop_reason`、`is_truncated`、`capture_version` |
| token / thinking | `thinking_effort`、`thinking_type`、`input_tokens`、`output_tokens`、`cache_read_tokens`、`cache_creation_tokens`、`signature_present` |
| 可选敏感内容 | `raw_request`、`raw_response`、`request_headers`、`response_headers` |

四个敏感内容列受管理员内容开关控制；关闭时写空字符串。元数据列在能抽取时仍会保留。请求头和响应头会删除认证、Cookie 等敏感字段，但原始 body 包含用户对话，仍属于敏感数据。

示例记录（内容已缩短和脱敏）：

```json
{
  "captured_at": "2026-08-11 16:30:01.235",
  "request_id": "req_01J...",
  "session_id": "session_abc",
  "platform": "openai",
  "requested_model": "gpt-5",
  "upstream_model": "gpt-5-2026-08-07",
  "upstream_endpoint": "/v1/responses",
  "stream": 1,
  "http_status": 200,
  "input_tokens": 42,
  "output_tokens": 128,
  "is_truncated": 0,
  "raw_request": "{\"model\":\"gpt-5-2026-08-07\",...}",
  "raw_response": "event: response.created\ndata: {...}\n\n...",
  "request_headers": "{\"content-type\":[\"application/json\"]}",
  "response_headers": "{\"content-type\":[\"text/event-stream\"]}"
}
```

### 3.3 两层开关

静态配置与管理员运行时策略是两层独立开关：

1. `config.yaml` 的 `gateway.capture.enabled` 负责连接 ClickHouse、启动 worker 和队列，修改后需重启。
2. 管理员「转存设置」负责是否转存、平台、结果、内容、用户和分组范围，保存后即时生效。

运行时默认值：

- 总开关：关闭。
- Anthropic：开启。
- Kiro：开启。
- OpenAI：关闭。
- 成功：开启。
- 最终上游错误：开启。
- 原始请求、原始响应、请求头、响应头：开启。
- 用户和分组：空，表示不按该维度过滤。

如果用户同时有 GPT 和 Kiro 账号，两者是否转存取决于请求实际选择的平台，而不是账号是否存在。要让两者都存，必须同时满足：静态 provisioning 已就绪、运行时总开关打开、Kiro 和 OpenAI 两个平台都打开、结果开关命中、用户/分组过滤命中。OpenAI 不要求开启 `openai_passthrough`。

---

## 4. 机器与容量准备

### 4.1 推荐起点

对于单节点归档，建议从以下配置起步，再按实际增量调整：

- Linux：Ubuntu 22.04/24.04 LTS 或同级发行版。
- CPU：4 vCPU。
- 内存：8 GiB；低流量可从 4 GiB 起步。
- 数据盘：SSD，初始至少 200 GiB，挂载到 `/srv/sub2api-clickhouse/data`。
- 系统盘与数据盘分离更容易扩容和恢复。
- Docker Engine + Compose plugin。
- Tailscale 宿主机客户端。

200 GiB 只是起步容量，不等于能无限永久保存。例如每天压缩后新增 10 GiB，200 GiB 只够约 20 天，还要预留 ClickHouse merge 和系统空间。

### 4.2 粗略容量估算

```text
日增磁盘 ≈ 日调用量 × 每条 request+response 的平均原文大小 ÷ 实际压缩比
```

文本原文通常有较好压缩率，但不得把固定的 3～6 倍压缩比当成保证。上线后应直接查询 `system.parts` 和文件系统增量，以 7 天实测修正预测。

建议告警阈值：

- 数据盘使用率 70%：提示扩容规划。
- 85%：高优先级告警。
- 95%：紧急；优先关闭运行时转存，避免 ClickHouse 写满。
- 宿主机剩余空间低于 50 GiB：预警。
- 宿主机剩余空间低于 20 GiB：紧急。

---

## 5. 在独立 Linux 主机部署 ClickHouse

以下以 `/srv/sub2api-clickhouse` 为根目录。若使用独立数据盘，先通过 `/etc/fstab` 将它稳定挂载到该目录的 `data` 子目录，并验证重启后仍能自动挂载。

### 5.1 目录和秘密

```bash
sudo install -d -m 0750 /srv/sub2api-clickhouse
sudo install -d -m 0750 /srv/sub2api-clickhouse/data
sudo chown -R "$USER":"$USER" /srv/sub2api-clickhouse
cd /srv/sub2api-clickhouse
umask 077
printf 'CLICKHOUSE_DB=llm_archive\nCLICKHOUSE_ADMIN_USER=capture_admin\nCLICKHOUSE_ADMIN_PASSWORD=%s\n' "$(openssl rand -hex 32)" > .env
chmod 600 .env
```

`.env` 只留在 ClickHouse 主机，不提交 Git。管理员密码仅用于本机维护，不放进正式服配置。

### 5.2 `compose.yaml`

使用经过验证的 LTS 小版本，不使用 `latest`。本文编写时固定为 `26.3.17.110`；以后部署时可以先在测试机验证更新的 LTS，再显式改版本。

```yaml
services:
  clickhouse:
    image: clickhouse/clickhouse-server:26.3.17.110
    container_name: llm-archive-clickhouse
    restart: unless-stopped
    environment:
      CLICKHOUSE_DB: ${CLICKHOUSE_DB}
      CLICKHOUSE_USER: ${CLICKHOUSE_ADMIN_USER}
      CLICKHOUSE_PASSWORD: ${CLICKHOUSE_ADMIN_PASSWORD}
      CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT: "1"
    ports:
      - "127.0.0.1:19001:9000"
    volumes:
      - ./data:/var/lib/clickhouse
    ulimits:
      nofile:
        soft: 262144
        hard: 262144
    healthcheck:
      test:
        - CMD-SHELL
        - >-
          clickhouse-client
          --user "$${CLICKHOUSE_USER}"
          --password "$${CLICKHOUSE_PASSWORD}"
          --query "SELECT 1"
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 30s
```

注意：

- 不发布 `8123`。
- `9000` 只映射到 `127.0.0.1:19001`。
- 官方镜像支持 `/var/lib/clickhouse`、`users.d`、`config.d` 和 `/docker-entrypoint-initdb.d` 挂载；本方案只挂载数据目录，降低配置复杂度。
- 如果外接盘挂载失败，必须让 ClickHouse 启动失败或人工阻断启动，不能悄悄把数据写到系统盘同名目录。

启动并检查：

```bash
cd /srv/sub2api-clickhouse
docker compose config
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 clickhouse
```

期望 `docker compose ps` 显示 `healthy`。

### 5.3 创建最小权限写入账号

管理员用户由官方镜像首次初始化。另建一个只给网关使用的账号：

```bash
cd /srv/sub2api-clickhouse
umask 077
openssl rand -hex 32 > capture-ingest.password
chmod 600 capture-ingest.password
set -a
. ./.env
set +a
CAPTURE_INGEST_PASSWORD="$(cat capture-ingest.password)"
printf "CREATE USER IF NOT EXISTS capture_ingest IDENTIFIED WITH sha256_password BY '%s';\nGRANT CREATE TABLE, INSERT ON llm_archive.* TO capture_ingest;\n" "$CAPTURE_INGEST_PASSWORD" \
  | docker compose exec -T clickhouse clickhouse-client \
      --user "$CLICKHOUSE_ADMIN_USER" \
      --password "$CLICKHOUSE_ADMIN_PASSWORD" \
      --multiquery
unset CAPTURE_INGEST_PASSWORD CLICKHOUSE_ADMIN_PASSWORD
```

`capture_ingest` 不授予 `DROP`、`ALTER`、`DELETE` 或业务表查询权限。网关启动时会自动 `CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive`，但不会创建 database，所以 `llm_archive` 必须预先存在；上面的官方镜像环境变量已负责首次建库。

部署完成后，把 `capture-ingest.password` 的值安全复制到正式服 `config.yaml`，并将原文件移入密码管理器或安全离线备份。不要通过聊天、工单或 Git 传递。

### 5.4 本机读回验证

```bash
cd /srv/sub2api-clickhouse
set -a
. ./.env
set +a
docker compose exec clickhouse clickhouse-client \
  --user "$CLICKHOUSE_ADMIN_USER" \
  --password "$CLICKHOUSE_ADMIN_PASSWORD" \
  --query "SHOW DATABASES"
unset CLICKHOUSE_ADMIN_PASSWORD
```

输出必须包含 `llm_archive`。

---

## 6. 配置 Tailscale 私网

### 6.1 先配置 tailnet policy

在 Tailscale 管理后台定义两个 tag，并添加最小权限 Grant：

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
      "ip": ["tcp:19000"]
    }
  ]
}
```

这是需要合并到现有 policy 的片段，不是无条件覆盖整个文件。

必须同时审计现有 ACL/Grants。Tailscale 规则是权限并集：更窄的新规则不会覆盖已有的 `* -> *:*` 或其他宽权限。如果 tailnet 还保留默认 allow-all，`tag:capture-writer` 实际上可能仍能访问其他设备和端口，必须先收窄或明确接受该风险。

### 6.2 ClickHouse 主机加入 tailnet

安装方式以 Tailscale 官方 Linux 文档为准：

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo systemctl enable --now tailscaled
sudo tailscale up --hostname=capture-db-1 --advertise-tags=tag:capture-db
```

也可以使用后台生成的“一次性、带 `tag:capture-db`、预批准”auth key。不要把 key 直接写进 shell history：

```bash
read -rsp 'Tailscale one-off auth key: ' TS_CAPTURE_AUTH_KEY
printf '\n'
sudo tailscale up \
  --auth-key="$TS_CAPTURE_AUTH_KEY" \
  --hostname=capture-db-1 \
  --advertise-tags=tag:capture-db
unset TS_CAPTURE_AUTH_KEY
```

Tagged server 的 node key 默认关闭过期，适合长期运行；机器失陷时要在 Tailscale 管理后台删除设备。不要使用 ephemeral tag/key，否则离线后设备身份可能被回收。

记录 ClickHouse 主机地址：

```bash
tailscale status
tailscale ip -4
```

### 6.3 使用 Tailscale Serve 发布 raw TCP

```bash
sudo tailscale serve --bg --tcp=19000 tcp://127.0.0.1:19001
tailscale serve status
```

`--bg` 会让 Serve 配置在设备重启和 `tailscale down/up` 后自动恢复。此处是 raw TCP 转发，不在 ClickHouse 内再启 TLS；链路加密由 Tailscale 提供。因此正式服配置使用 `secure: false`。

### 6.4 正式服加入 tailnet

正式服宿主机安装 Tailscale，并用一次性、带 `tag:capture-writer`、预批准的 auth key 加入：

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo systemctl enable --now tailscaled
sudo tailscale up --hostname=sub2api-prod --advertise-tags=tag:capture-writer
tailscale status
tailscale ping capture-db-1
```

正式服安装/加入是生产变更，执行前必须再次确认。Tailscale 安装在宿主机后，其状态由宿主机 `/var/lib/tailscale` 和 systemd 管理，与 `/app/sub2api` 二进制、`sub2api` 容器、PostgreSQL 和 Redis 相互独立。

### 6.5 从正式服容器网络验证 ClickHouse

只验证宿主机 `tailscale ping` 不够；必须验证 `sub2api` 容器使用的网络命名空间能访问 ClickHouse。

在正式服上准备 ClickHouse 主机的 Tailscale IPv4 和 `capture_ingest` 密码，然后使用与 `sub2api` 相同网络命名空间做一次临时探测：

```bash
read -rp 'ClickHouse Tailscale IPv4: ' CAPTURE_DB_TS_IP
read -rsp 'capture_ingest password: ' CAPTURE_INGEST_PASSWORD
printf '\n'
docker run --rm \
  --network container:sub2api \
  --entrypoint clickhouse-client \
  clickhouse/clickhouse-server:26.3.17.110 \
  --host "$CAPTURE_DB_TS_IP" \
  --port 19000 \
  --user capture_ingest \
  --password "$CAPTURE_INGEST_PASSWORD" \
  --query "SELECT 1"
unset CAPTURE_DB_TS_IP CAPTURE_INGEST_PASSWORD
```

期望输出 `1`。若宿主机可达而容器不可达，先排查 Docker FORWARD、宿主机防火墙和 Tailscale policy，不要直接打开公网端口作为绕过方案。

---

## 7. 正式服已核实的基线与更新行为

2026-08-11 只读检查确认：

- 正式服为 Ubuntu 22.04，约 2 GiB 内存、1 GiB swap。
- 使用 Docker 运行 `sub2api`、PostgreSQL 和 Redis。
- `sub2api` 数据目录绑定到宿主机：`/root/sub2api-deploy/sub2api-deploy/data` → 容器 `/app/data`。
- 实际配置文件：`/root/sub2api-deploy/sub2api-deploy/data/config.yaml`。
- 容器配置了 `restart: unless-stopped`。
- 检查时正式服尚无 Tailscale、ClickHouse，也还没有 `gateway.capture` 配置。
- 检查时镜像版本早于本次转存设置功能。

只读检查没有修改正式服。

### 7.1 推荐先更新代码，再部署 ClickHouse

可以先更新正式服应用，条件是保持：

```yaml
gateway:
  capture:
    enabled: false
```

或者暂时不写 `capture` 段，代码默认也是关闭。这样新版本不会连接 ClickHouse、不会分配转存队列、不会存任何数据。

更新后先验收：

- 普通转发和计费无回归。
- 管理员左侧出现独立的「转存设置」。
- 页面显示静态基础设施“未配置/未 provision”，且不能误开启运行时总开关。
- OpenAI 默认平台开关是关闭。

### 7.2 管理员一键更新是否影响 Tailscale

不会。当前一键更新逻辑下载新二进制、校验 checksum、把旧二进制原子改名为 backup、把新二进制换入，再让进程退出；Docker 的 `restart: unless-stopped` 会启动同一个容器。

因此：

- 宿主机 Tailscale 不会重新配置或重新入网。
- 绑定挂载的 `/app/data/config.yaml` 不会丢。
- ClickHouse 在另一台机器，也不会被触碰。

但要注意：管理员一键更新修改的是“当前容器里的二进制”。如果以后执行 `docker compose pull/up` 重建容器，新容器会恢复为镜像内的版本。每次重建后必须检查应用版本；正式发布应让镜像版本与已验证二进制一致，不能长期依赖容器内临时替换。

### 7.3 为什么正式服不需要重建系统

本方案只增加宿主机 Tailscale 和 `config.yaml` 的 ClickHouse 连接配置，不需要重装、reimage 或重建 Ubuntu。只有真的重装系统、删除 `/var/lib/tailscale` 或在管理后台删除设备，才需要让该宿主机重新加入 tailnet。

---

## 8. 正式服静态配置

在修改前先备份：

```bash
cd /root/sub2api-deploy/sub2api-deploy
cp -a data/config.yaml "data/config.yaml.before-capture.$(date +%Y%m%d-%H%M%S)"
```

在 `data/config.yaml` 的 `gateway:` 下加入以下配置。把 `100.x.y.z` 换成 ClickHouse 主机 `tailscale ip -4` 的真实地址，把密码换成 `capture-ingest.password` 的值。

以下是按正式服 2 GiB 内存选择的保守起点：

```yaml
gateway:
  capture:
    enabled: true
    max_body_bytes: 8388608
    max_queue_bytes: 134217728
    queue_size: 512
    worker_count: 2
    writer_queue_size: 1024
    overflow_policy: drop
    overflow_sample_percent: 0
    batch_max_size: 100
    batch_max_interval_ms: 2000
    clickhouse:
      addr: ["100.x.y.z:19000"]
      database: llm_archive
      table: model_call_archive
      username: capture_ingest
      password: "使用独立生成的 64 位十六进制密码"
      dial_timeout_ms: 2000
      read_timeout_ms: 10000
      compression: lz4
      secure: false
      max_open_conns: 8
```

配置文件必须只允许 root/部署账号读取：

```bash
chmod 600 /root/sub2api-deploy/sub2api-deploy/data/config.yaml
```

容器中的 Tailscale DNS 不一定与宿主机一致，所以优先填稳定的 Tailscale IPv4，而不是 MagicDNS 名称。

### 8.1 字段与代码默认值

| 字段 | 代码默认 | 正式服起始值 | 说明 |
|---|---:|---:|---|
| `enabled` | `false` | `true`（接入阶段才改） | 静态 provisioning；不是管理员运行时总开关 |
| `max_body_bytes` | 8 MiB | 8 MiB | 单条正文上限；超出只截断正文，不算整条丢失 |
| `max_queue_bytes` | 1 GiB | 128 MiB | 从一级队列接收到写入/丢弃的全链路在途字节预算；`0` 为不限，不推荐 |
| `queue_size` | 8192 | 512 | 一级队列条数 |
| `worker_count` | 4 | 2 | 抽取列并送入二级队列的 worker 数 |
| `writer_queue_size` | 1024 | 1024 | 二级 ClickHouse batcher 队列条数 |
| `overflow_policy` | `drop` | `drop` | 只允许 `drop` / `sample`，不能同步回写 |
| `batch_max_size` | 200 | 100 | 达到此行数立即发送批次 |
| `batch_max_interval_ms` | 1000 | 2000 | 已有非空批次的最长等待；空闲时不写空批次 |
| `max_open_conns` | 8 | 8 | ClickHouse 连接池上限 |

启动校验要求：所有队列/worker/batch 正数；`max_queue_bytes` 为 `0` 或至少不小于 `max_body_bytes`；ClickHouse 地址和 database 非空；压缩只允许 `lz4`、`zstd`、`none`。

`max_queue_bytes` 或 `queue_size` 太小确实会造成丢失，但不会造成用户请求失败。先用保守值上线，观察管理页峰值再调，不能仅凭感觉把内存上限放大到挤占转发进程。

### 8.2 重启并检查静态就绪

```bash
cd /root/sub2api-deploy/sub2api-deploy
docker compose restart sub2api
docker compose ps
docker compose logs --tail=200 sub2api
```

管理员「转存设置」应显示 `provisioned=true`、`ready=true`，地址已脱敏，database/table 正确。

当前 `dev` 有一个必须知道的限制：如果应用启动时 `Open + Ping + CREATE TABLE IF NOT EXISTS` 任一步失败，它会降级为 unavailable/no-op，主服务继续运行，但不会在后台重新初始化。修复网络/权限后要重启 `sub2api`。已确定的后续改动是加入约 2 秒起步、最大约 60 秒、带 jitter 的指数退避初始化重连；它只恢复后续写入，不补写已经丢失的数据。

---

## 9. 管理员运行时启用顺序

静态 `ready=true` 后，也不要一次性全开。建议：

1. 保持运行时总开关关闭，确认页面健康数据和容量信息正常。
2. 打开总开关，只开 Kiro，范围限制到一个测试用户或测试分组。
3. 发一次真实 Kiro 请求，确认 ClickHouse 有一行。
4. 显式打开 OpenAI，仍只限测试用户/分组。
5. 分别验证 `/v1/responses`、`/v1/chat/completions` 或 `/v1/messages` 中正式业务实际使用的入口。
6. 观察至少 15～30 分钟；确认 `written_records` 增长、`dropped_records` 不增长、队列峰值安全。
7. 再移除测试范围，扩大到需要转存的用户/分组。

用户和分组同时填写时采用 AND；两个都为空才表示全量。管理员关闭某类正文后，该列从新记录开始写空，不会修改历史记录。

---

## 10. 写入频率、队列和丢失语义

### 10.1 不是“每秒无条件写一次 DB”

每个命中策略且完成的最终调用立即尝试进入一级队列。worker 抽取元数据后交给二级队列，writer 按以下任一条件发送：

- 批次达到 `batch_max_size`，立即发送。
- 批次非空且等待达到 `batch_max_interval_ms`，发送当前批次。
- 服务优雅停止，尽量 flush 剩余批次。

没有记录时不会周期性发送空 INSERT。`batch_max_interval_ms=2000` 只表示非空小批次最多等约 2 秒，不表示每 2 秒必写一次，也不表示服务每秒主动向 worker 塞数据。

### 10.2 丢失条件

| 原因 | 含义 |
|---|---|
| `byte_budget_exceeded` | `max_queue_bytes` 无法接纳新 record |
| `worker_queue_full` | 一级队列已满 |
| `writer_queue_full` | 二级写入队列已满 |
| `writer_unavailable` | ClickHouse writer 初始化不可用 |
| `clickhouse_prepare_failed` | 批次准备失败 |
| `clickhouse_append_failed` | 批次 append 失败 |
| `clickhouse_send_failed` | 批次发送失败 |

策略关闭、平台/结果/用户/分组不命中属于 `policy_skipped`，不是丢失。超过 `max_body_bytes` 是正文截断并标 `is_truncated=1`，不是整条记录丢失。

没有磁盘 spool 和批次重放。发生上述丢失时，管理员能够知道“何时、为什么、多少条、估算多少字节、当时队列多高”，但无法从系统自动找回原文。

---

## 11. 验证步骤

### 11.1 ClickHouse 建表和回读

网关 `ready=true` 后，表应自动创建：

```sql
SHOW CREATE TABLE llm_archive.model_call_archive;
```

验证最近记录：

```sql
SELECT
    captured_at,
    platform,
    requested_model,
    upstream_model,
    upstream_endpoint,
    stream,
    http_status,
    is_truncated,
    length(raw_request) AS request_bytes,
    length(raw_response) AS response_bytes
FROM llm_archive.model_call_archive
ORDER BY captured_at DESC
LIMIT 20;
```

### 11.2 Kiro 验收

- 请求由测试范围内用户发出。
- 管理页总开关和 Kiro 开关已开。
- ClickHouse 新增一行，`platform='kiro'`。
- `requested_model` 与用户请求模型一致；`upstream_model` 表示实际上游模型。
- 管理页 `written_records` 增长，`dropped_records` 不增长。

### 11.3 OpenAI 验收

- 管理页 OpenAI 平台开关显式开启。
- 请求来自支持的三个 HTTP 文本入口之一。
- 无论账号 `openai_passthrough` 为 true 还是 false，只要实际发生受支持的 OpenAI 上游 HTTP 调用，都应按策略转存。
- ClickHouse 新增一行，`platform='openai'`，保存最终映射后的 outbound 请求和转换前的原始上游响应。

### 11.4 故障演练

在受控时段执行：

1. 临时停止 ClickHouse 容器。
2. 发一个测试请求，确认主转发仍成功。
3. 管理页应出现 writer/ClickHouse 丢失事件，时间和计数可见。
4. 恢复 ClickHouse。
5. 当前版本若启动初始化失败，重启 `sub2api` 后 `ready=true`；目标自动重连版本应自行恢复。
6. 恢复后的新请求应能写入；故障期间已经丢失的记录不会补写。

再重启 ClickHouse 主机一次，确认：

- 数据盘自动挂载。
- Docker 自动启动。
- ClickHouse 容器恢复为 healthy。
- `tailscaled` 自动启动。
- `tailscale serve status` 仍有 `19000 -> 127.0.0.1:19001`。
- 正式服能重新执行 `SELECT 1`。

---

## 12. 可见性与告警

### 12.1 当前已有

管理员「转存设置」页面约每 15 秒刷新：

- `submitted_records`、`accepted_records`、`written_records`。
- `dropped_records`、`dropped_bytes` 及按原因拆分。
- 最近成功、最近丢失时间和安全分类错误。
- 一级/二级队列当前值和历史峰值。
- 全链路在途字节当前值和历史峰值。
- 最近 100 条本进程丢失事件。
- 24 小时、7 天、30 天 PostgreSQL 分钟级丢失历史。

健康历史 reporter 最多暂存 4096 个分钟/原因 bucket，每批重试最旧的 256 个。它自身溢出也有页面计数和结构化日志。管理 API 不返回 ClickHouse 密码，不把 DSN 或凭据写入历史错误。

### 12.2 已选定但尚待实现

后续要把以下 capture 指标接入现有 Ops 事件/邮件：

- `capture_ready` 连续 2 分钟不就绪：critical。
- `capture_dropped_records` 5 分钟窗口出现任何新增：warning。
- `capture_writer_failures` 出现连接或批次失败：critical。
- 恢复后生成 recovery 事件/邮件。
- 同类告警冷却 60 分钟，避免邮件风暴。
- 告警只包含安全分类、计数和时间，不包含 body、headers、DSN 或密码。

在该功能实现前，生产启用后必须由管理员页面、日志或现有外部监控承担主动巡检，不能把“页面可见”误认为“已经主动发邮件”。

### 12.3 ClickHouse 主机监控

无论应用告警是否完成，ClickHouse 主机都应由现有监控系统每 5 分钟检查：

- 数据盘是否仍挂载在预期路径。
- `docker compose ps` 是否 healthy。
- `tailscale serve status` 是否仍发布 19000。
- 数据盘使用率和剩余空间。
- 最近容器重启次数与 ClickHouse error 日志。

本手册规定阈值，不绑定某一家监控产品；接入新机器现有的 Prometheus、云监控或主机告警即可。

---

## 13. 回滚边界

按影响从小到大：

### 13.1 立即停止新转存

在管理员「转存设置」关闭运行时总开关。即时生效，不需重启；队列中已经接受的记录仍可能完成写入。

### 13.2 关闭静态 provisioning

把正式服配置改回：

```yaml
gateway:
  capture:
    enabled: false
```

重启 `sub2api`。这会停止 ClickHouse writer 和队列初始化，不影响历史 ClickHouse 数据。

### 13.3 断开网络发布

在 ClickHouse 主机执行：

```bash
sudo tailscale serve --tcp=19000 off
```

或者先在 tailnet policy 移除 writer → db 的 Grant。不要通过删除 ClickHouse 数据作为“回滚”。

### 13.4 回滚应用版本

可以使用管理员更新功能保留的 binary backup，或回到上一个已验证镜像。应用回滚前先关闭运行时转存，再确认旧版本能否识别当前 `config.yaml`；必要时恢复修改前备份。

### 13.5 不在普通回滚中执行

- 不删除 `/srv/sub2api-clickhouse/data`。
- 不 `DROP DATABASE` / `DROP TABLE`。
- 不删除 Tailscale 设备身份，除非机器退役或失陷。
- 不重建正式服操作系统。

---

## 14. 验收标准

全部满足才算完成生产接入：

- [ ] 正式服已更新到包含独立「转存设置」和 OpenAI HTTP capture 的已验证版本。
- [ ] 更新期间静态和运行时转存保持关闭，普通转发无回归。
- [ ] ClickHouse 使用固定 LTS 镜像，数据在独立持久盘，重启后数据仍在。
- [ ] ClickHouse 未发布公网/LAN 端口，8123 未暴露。
- [ ] 两台宿主机以正确 tag 加入同一 tailnet。
- [ ] 已审计整个 Tailscale policy，不存在使最小权限失效的宽规则。
- [ ] 只有 `tag:capture-writer` 能访问 `tag:capture-db:19000/tcp`。
- [ ] 从 `sub2api` 容器网络执行 `SELECT 1` 成功。
- [ ] `capture_ingest` 仅有目标库的 `CREATE TABLE` 和 `INSERT`。
- [ ] 正式服配置权限为 0600，凭据未进入 Git、日志或管理 API。
- [ ] 管理页显示 provisioned/ready。
- [ ] Kiro 测试请求成功入库。
- [ ] OpenAI 支持入口测试请求成功入库，且不依赖 passthrough。
- [ ] 空闲时没有周期性空 INSERT。
- [ ] 故障时用户请求不受影响，丢失在页面/历史中可见。
- [ ] ClickHouse 主机重启后数据盘、Docker、容器、Tailscale 和 Serve 自动恢复。
- [ ] 已配置磁盘与服务健康告警。
- [ ] 已验证运行时关闭、静态关闭和应用版本回滚路径。

如果要求“无人值守后能主动邮件提醒”，还必须等 Capture 指标接入 Ops 邮件完成并验证 recovery 邮件；当前管理页可见性本身不满足这一条。

---

## 15. 日常运维

### 15.1 查询磁盘占用

```sql
SELECT
    database,
    table,
    formatReadableSize(sum(bytes_on_disk)) AS disk,
    sum(rows) AS rows
FROM system.parts
WHERE active AND database = 'llm_archive'
GROUP BY database, table;
```

### 15.2 查询每日增量

```sql
SELECT
    toDate(captured_at) AS day,
    count() AS calls,
    formatReadableSize(sum(length(raw_request) + length(raw_response))) AS raw_body_size
FROM llm_archive.model_call_archive
GROUP BY day
ORDER BY day DESC
LIMIT 30;
```

### 15.3 永久留存与人工清理

当前表没有 TTL，数据永久保留。只有在管理员明确决定后才可以按月删除分区：

```sql
ALTER TABLE llm_archive.model_call_archive DROP PARTITION '202607';
```

这是不可逆的数据删除操作，执行前必须有单独确认和备份/导出策略。

### 15.4 ClickHouse 升级

1. 记录当前镜像版本和 `SHOW CREATE TABLE`。
2. 做数据盘快照或可验证备份。
3. 先在测试数据副本验证目标 LTS。
4. 修改 compose 中的显式版本，`docker compose pull && docker compose up -d`。
5. 验证 healthy、表可读、正式服可写、数据增量正常。
6. 出现问题时回到旧镜像；不要删除数据目录。

---

## 16. 常见问题

### 管理员界面为什么没有「转存设置」？

正式服应用版本还没更新到包含该页面的版本，或前端静态资源/容器仍是旧版本。先核对应用版本和镜像，而不是先开启数据库。

### 管理页能保存设置，但为什么不能打开总开关？

静态 `gateway.capture.enabled` 未开启，或 ClickHouse writer 未 ready。管理 API 会阻止在未 provision/未 ready 时把运行时总开关改为 true。

### OpenAI 为什么没有数据？

确认运行时 OpenAI 平台开关已显式开启，请求来自 `/v1/responses`、`/v1/chat/completions` 或 `/v1/messages`，结果和用户/分组范围命中。`openai_passthrough` 不控制转存。

### ClickHouse 恢复了，为什么页面仍不 ready？

当前实现只在 `sub2api` 启动时初始化 writer；启动失败后不会自动重试。修复连接后重启 `sub2api`。目标自动重连版本完成后，此限制会取消，但故障期间丢失的数据仍不会补写。

### 为什么正式服一键更新后不需要重配 Tailscale？

更新替换的是应用容器内二进制；Tailscale 运行在正式服宿主机，状态也在宿主机。只有宿主机系统/状态被删除或设备被移出 tailnet 才要重新认证。

### 队列设得太小会不会丢很多？

会。`max_queue_bytes`、一级队列、二级队列任意一个触顶都会 drop。管理页能看到丢失原因、时间和峰值；先根据真实峰值调参，同时确认 ClickHouse 写入和网络没有成为瓶颈。

### 能不能保证零丢失？

不能。当前设计优先保证转发服务，且没有磁盘 spool。要保证可重放，需要另行设计持久 spool/消息队列，这不在本方案范围。

---

## 17. 官方参考

- [ClickHouse 官方 Docker 镜像说明](https://github.com/ClickHouse/ClickHouse/blob/master/docker/server/README.md)
- [ClickHouse 26.3 LTS release](https://github.com/ClickHouse/ClickHouse/releases/tag/v26.3.17.110-lts)
- [Tailscale Linux 安装](https://tailscale.com/docs/install/linux)
- [Tailscale Serve TCP 与持久后台模式](https://tailscale.com/docs/reference/tailscale-cli/serve)
- [Tailscale Auth keys](https://tailscale.com/docs/features/access-control/auth-keys)
- [Tailscale tags](https://tailscale.com/docs/features/tags)
- [Tailscale Grants 语法](https://tailscale.com/docs/reference/syntax/grants)
