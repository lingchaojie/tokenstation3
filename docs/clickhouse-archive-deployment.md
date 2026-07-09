# 上游调用全量归档 · ClickHouse 部署与接入指南

本文档说明如何部署一台**远端、独立**的 ClickHouse，并把网关的「上游模型调用全量归档」无缝接入。

- 关联设计：`superpowers/specs/2026-07-07-model-call-archive-design.md`、`superpowers/specs/2026-07-08-capture-translated-paths-design.md`
- 配置示例：`deploy/config.example.yaml` 的 `gateway.capture` 段
- 相关代码：`backend/internal/service/clickhouse_archive_writer.go`、`conversation_capture_pool.go`、`capture_byte_gauge.go`

---

## 1. 它是什么 / 不是什么

- 是：与转发/计费**完全隔离的第三条异步通道**，把每次上游调用的原始 request+response 逐字异步写入远端 ClickHouse，供离线二次开发。
- 不是：不参与转发决策、不影响计费、不阻塞热路径。任何归档侧故障（库宕、慢、队列满）都被 drop / recover / no-op 降级吸收。
- 默认**关闭**（`gateway.capture.enabled=false`），关时零成本。

```
转发主路径 ──► 客户端        (不受归档影响)
     └─ tap ─► 有界内存队列(条数+字节双限, overflow=drop)
                 └─ worker: 抽取列 ─► 批量 INSERT ─► 远端 ClickHouse
```

---

## 2. 前置要求

- 一台可达的 ClickHouse（**独立于业务主库**，建议独立机器/VPC）。版本建议 **24.x LTS**（≥ 23.8 均可）。
- 网关到 ClickHouse 的 **native TCP 端口**连通：明文 `9000`，TLS `9440`。
- ClickHouse 侧一个专用账号，拥有目标库的 `CREATE TABLE / INSERT / SELECT` 权限。
- 磁盘容量按流量估算（见 §8 容量规划）。

---

## 3. 部署 ClickHouse（远端机器）

### 3.1 明文最简版（仅限内网 / VPC 私网）

```yaml
# docker-compose.yml
services:
  clickhouse:
    image: clickhouse/clickhouse-server:24.8
    container_name: llm-archive-ch
    ports:
      - "9000:9000"   # native TCP —— 网关连这个
      - "8123:8123"   # HTTP —— 调试用，生产可不暴露
    ulimits:
      nofile: { soft: 262144, hard: 262144 }
    volumes:
      - ./ch_data:/var/lib/clickhouse
      - ./users.d:/etc/clickhouse-server/users.d
    restart: unless-stopped
```

### 3.2 TLS 版（暴露到不可信网络时**必须**）

⚠️ 代码里 `secure: true` 使用 `MinVersion TLS1.2` 且**用系统根证书验证服务端证书、不 skip verify、不加载自定义 CA**。这意味着：
- ClickHouse 的服务端证书必须由**受信任 CA 签发**（Let's Encrypt / 企业内网 CA 已装进网关容器的根证书库）。
- **自签名证书会握手失败**。要用自签名，需先把 CA 证书装进网关运行环境的系统信任库（例如 Dockerfile 里 `COPY ca.crt /usr/local/share/ca-certificates/ && update-ca-certificates`）。

```yaml
# docker-compose.yml（TLS）
services:
  clickhouse:
    image: clickhouse/clickhouse-server:24.8
    container_name: llm-archive-ch
    ports:
      - "9440:9440"   # native secure TCP —— 网关连这个
    volumes:
      - ./ch_data:/var/lib/clickhouse
      - ./users.d:/etc/clickhouse-server/users.d
      - ./certs:/etc/clickhouse-server/certs:ro   # server.crt / server.key / ca.crt
      - ./config.d/tls.xml:/etc/clickhouse-server/config.d/tls.xml:ro
    restart: unless-stopped
```

```xml
<!-- config.d/tls.xml -->
<clickhouse>
    <tcp_port_secure>9440</tcp_port_secure>
    <openSSL>
        <server>
            <certificateFile>/etc/clickhouse-server/certs/server.crt</certificateFile>
            <privateKeyFile>/etc/clickhouse-server/certs/server.key</privateKeyFile>
            <caConfig>/etc/clickhouse-server/certs/ca.crt</caConfig>
            <verificationMode>none</verificationMode>
        </server>
    </openSSL>
</clickhouse>
```

---

## 4. 建库 + 专用账号（网关不建库！）

网关连上后会自动 `CREATE TABLE IF NOT EXISTS <db>.<table>`（`clickhouse_archive_writer.go:159`），但**不会建 database**。所以库和账号要你先建好：

```sql
-- 用 clickhouse-client 执行（docker exec -it llm-archive-ch clickhouse-client）
CREATE DATABASE IF NOT EXISTS llm_archive;

CREATE USER archiver IDENTIFIED BY 'CHANGE-ME-strong-password';

-- 建表 + 写入 + 读回（离线消费也可另建只读账号）
GRANT CREATE TABLE, INSERT, SELECT ON llm_archive.* TO archiver;
```

`users.d/archiver.xml`（可选，用配置文件方式管理账号 + 限制来源 IP）：

```xml
<clickhouse>
    <users>
        <archiver>
            <password_sha256_hex>__PUT_SHA256_OF_PASSWORD__</password_sha256_hex>
            <networks>
                <ip>10.0.0.0/8</ip>   <!-- 只放行网关所在网段 -->
            </networks>
            <profile>default</profile>
            <quota>default</quota>
        </archiver>
    </users>
</clickhouse>
```

> 生成密码 hash：`echo -n 'your-password' | sha256sum`。

---

## 5. 网关接入配置

在网关 `config.yaml` 填 `gateway.capture`（完整字段见下表 §6）：

```yaml
gateway:
  capture:
    enabled: true
    max_body_bytes: 8388608          # 8MB 单条原文上限
    max_queue_bytes: 1073741824      # 1GiB 在途内存上界；0=不限
    queue_size: 8192
    worker_count: 4
    overflow_policy: drop            # 满时丢弃，绝不阻塞转发
    batch_max_size: 200
    batch_max_interval_ms: 1000
    clickhouse:
      addr: ["ch.internal.example.com:9000"]   # TLS 时用 :9440
      database: llm_archive
      table: model_call_archive
      username: archiver
      password: "CHANGE-ME-strong-password"
      compression: lz4               # lz4 | zstd | none
      secure: false                  # TLS 时 true（且 addr 用 9440）
      max_open_conns: 8
      dial_timeout_ms: 2000
      read_timeout_ms: 10000
```

多副本：`addr` 可填多个 `["ch1:9000","ch2:9000"]`，clickhouse-go 会做连接级负载/故障转移。

---

## 6. 配置字段速查

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `false` | 总开关。关时零成本（不装 tee、不分配 buffer） |
| `max_body_bytes` | `8388608` (8MB) | 单条 request/response 原文上限，超出截断并标 `is_truncated=1` |
| `max_queue_bytes` | `1073741824` (1GiB) | 在途 record 总字节上界；`0`=不限；非 0 必须 `>= max_body_bytes`（否则启动校验失败） |
| `queue_size` | `8192` | pond 队列条数上限 |
| `worker_count` | `4` | 抽取+写入 worker 数 |
| `overflow_policy` | `drop` | 满时策略：`drop` / `sample`。**禁止 `sync`**（校验拦截，归档不得反压转发） |
| `overflow_sample_percent` | `0` | `sample` 策略下入队概率(1-100) |
| `batch_max_size` | `200` | 批量 INSERT 行数上限 |
| `batch_max_interval_ms` | `1000` | 批量时间窗(ms)，未满也会按时 flush |
| `clickhouse.addr` | — | `["host:port"]`，enabled 时必填。明文 9000 / TLS 9440 |
| `clickhouse.database` | `llm_archive` | enabled 时必填，**需预先建库** |
| `clickhouse.table` | `model_call_archive` | 自动建表 |
| `clickhouse.username` / `password` | 空 | 专用账号 |
| `clickhouse.compression` | `lz4` | 传输压缩：`lz4` / `zstd` / `none`（其它值启动报错） |
| `clickhouse.secure` | `false` | TLS(MinVersion 1.2)。**系统根证书验证，自签名需先装 CA** |
| `clickhouse.max_open_conns` | `8`（≤0 时代码回退 4） | 连接池大小 |
| `clickhouse.dial_timeout_ms` | `2000` | 建连/Ping 超时 |
| `clickhouse.read_timeout_ms` | `10000` | 读/建表/发送批次超时 |

启动校验（`enabled=true` 时）：`addr` 非空 + `database` 非空；`overflow_policy ∈ {drop,sample}`；`compression ∈ {lz4,zstd,none,空}`；`max_body_bytes/queue_size/worker_count/batch_max_size > 0`；`max_queue_bytes >= 0` 且（非 0 时）`>= max_body_bytes`。任一不满足则**启动失败**（fail-fast，不会静默跑错配置）。

---

## 7. 接入验证（Checklist）

1. **建库已完成**：`SHOW DATABASES` 含 `llm_archive`。
2. **启动网关**，日志无 `capture.clickhouse_init_failed_degrade_noop`（有则表示建连/建表失败，已降级为 no-op，不落库）。
3. **打一次真实请求**（Anthropic 原生或 Kiro 账号）。
4. **回读**：
   ```sql
   SELECT captured_at, platform, stream, http_status,
          session_id, thinking_effort, signature_present, is_truncated,
          length(raw_request) AS req_bytes, length(raw_response) AS resp_bytes
   FROM llm_archive.model_call_archive
   ORDER BY captured_at DESC LIMIT 10;
   ```
5. 有行进来即接入成功。若无：
   - 查网关日志 `capture.clickhouse.send_failed` / `prepare_batch_failed` / `append_failed`。
   - 确认账号有 `INSERT`；确认 `max_queue_bytes >= max_body_bytes`；确认 `addr`/`secure`/端口匹配。

---

## 8. 容量规划

- 每条记录 ≈ `raw_request + raw_response + 两个头`，ZSTD(3) 压缩后入库（列 `CODEC(ZSTD(3))`）。文本类原文压缩比通常 3~6x。
- 粗估：日调用量 N，平均单条原文 S（压缩前），压缩比 4x → 日增磁盘 ≈ `N × S × 2 / 4`（req+resp）。
  - 例：100 万次/日、平均 20KB → ≈ `1e6 × 20KB × 2 / 4` ≈ **10 GB/日**。
- 按 `toYYYYMM(captured_at)` 分区，方便按月 `DROP PARTITION` 清理。
- 内存上界：在途 ≤ `max_queue_bytes`(1GiB) + batchCh(`batch_max_size×4`,最小1024条) + 在途 batch(≤`batch_max_size`)。ClickHouse 变慢时队列满即 drop，不会无界增长。

### 8.1 留存 / 清理

按月手动清理：
```sql
ALTER TABLE llm_archive.model_call_archive DROP PARTITION '202606';
```
或建表级 TTL（如需自动过期，可在自动建表后 `ALTER TABLE ... MODIFY TTL captured_at + INTERVAL 180 DAY`）。

---

## 9. 表结构（自动建表，仅供参考）

```sql
CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive (
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
  raw_request        String CODEC(ZSTD(3)),
  raw_response       String CODEC(ZSTD(3)),
  request_headers    String CODEC(ZSTD(3)),
  response_headers   String CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (session_id, captured_at, request_id)
SETTINGS index_granularity = 8192;
```

**格式提示（重要）**：`raw_response` 的格式取决于 `stream`：
- `stream=1`：翻译后的 **Anthropic SSE 字节流**（多帧 `event:/data:`），离线需按 SSE 组装。
- `stream=0`：组装好的**单个 Anthropic JSON**，可直接解析。
- `raw_request` 两者一致，均为 Anthropic 请求 body（model-map 后）。抽取列（usage/stop_reason/signature 等）两者语义等价。

---

## 10. 安全（远端库必读）

归档库存的是**全量原始 request/response（含用户内容）**，属敏感数据：

1. **不要把 9000/9440 裸暴露公网**。放内网/VPC，安全组只放行网关 IP（配合 §4 `users.d` 的 `<networks>`）。
2. **暴露到不可信网络必开 TLS**（§3.2），并确保证书能被网关系统根证书验证（自签名先装 CA）。
3. **强密码 + 最小权限**：专用 `archiver` 账号只授 `llm_archive` 库权限；离线消费另建只读账号。禁用 `default` 空密码账号的远程访问。
4. **头已脱敏**：`authorization`、`x-api-key`、`cookie`、`set-cookie`、`proxy-authorization`、`x-goog-api-key` 在入库前剥离；但 **body 是逐字原文**，含对话内容——磁盘加密、访问审计、留存/合规策略按你的要求定。
5. **不含身份字段**：归档只存上游相关数据，不含 client IP / user-agent / api_key / 内部用户身份 FK。

---

## 11. 运维与排障

- **降级为 no-op**：建连/Ping/建表任一失败 → 日志 `capture.clickhouse_init_failed_degrade_noop`，网关照常转发，只是不落库。修好配置/权限后重启网关重连。
- **写入失败**：`capture.clickhouse.{prepare_batch,append,send}_failed`（批次被丢，不影响转发）。常见原因：库磁盘满、账号权限、网络抖动。
- **归档缺数据但转发正常**：多半是队列 drop（提交速率 > 抽取/写入速率，或 `max_queue_bytes` 触顶，或 ClickHouse 慢）。可调大 `worker_count` / `batch_max_size`，或确认 CH 侧写入吞吐。
- **OpenAI 转发不归档**：设计如此。当前归档覆盖 Anthropic 原生 + Kiro（流式/非流式/错误）。
- **优雅关闭**：进程退出时 `Stop()` 会 drain batchCh 并做最后一次 flush，尽量不丢在途数据。

---

## 12. 离线消费示例

导出 JSONL 喂 SDK 批处理脚本（ClickHouse 秒级导出）：
```sql
SELECT * FROM llm_archive.model_call_archive
WHERE captured_at >= '2026-07-01'
INTO OUTFILE '/tmp/archive_20260701.jsonl' FORMAT JSONEachRow;
```

筛 Opus 4.8 轨迹验收候选（Anthropic 原生 / Kiro-Claude、有 signature、非截断、真实成功）：
```sql
SELECT request_id, session_id, thinking_effort, raw_request, raw_response, stream
FROM llm_archive.model_call_archive
WHERE platform IN ('anthropic','kiro')
  AND signature_present = 1
  AND is_truncated = 0
  AND http_status = 200
ORDER BY session_id, captured_at;
```

模型分布 / signature 覆盖率快速统计：
```sql
SELECT platform, upstream_model,
       count() AS calls,
       round(avg(signature_present), 3) AS sig_coverage
FROM llm_archive.model_call_archive
GROUP BY platform, upstream_model
ORDER BY calls DESC;
```

> 二次开发（会话重建、SSE→完整 body 组装、cron/heartbeat 过滤、质量标注、claude-trace 格式转换）均属**读库后离线处理**，不在采集侧做——采集侧只逐字保存 + 最小抽取列。
