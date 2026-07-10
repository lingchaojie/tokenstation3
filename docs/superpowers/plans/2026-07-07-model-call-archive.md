# 上游模型调用全量归档（ClickHouse）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增一条与转发/计费完全隔离的第三条异步通道，把每次上游模型调用的原始 request+response 逐字异步写入远端 ClickHouse，默认开关关闭、关时零成本。

**Architecture:** 复用现有 `UsageRecordWorkerPool`（有界 pond 池）与 `runBestEffortBatcher`（批量 channel）两个已验证模式。采集点在 `GatewayService.Forward` 之后的 usage tap 旁（`gateway_handler.go:963`），流式在上游 SSE 读取 goroutine（`gateway_service.go:8613`）tee 原始字节。抽取列在 worker 内完成，不占热路径。ClickHouse 独立部署、远端可配。

**Tech Stack:** Go、`github.com/ClickHouse/clickhouse-go/v2`、`github.com/alitto/pond/v2`（已有）、`github.com/tidwall/gjson`（已有）、google/wire、viper。模块路径 `github.com/Wei-Shaw/sub2api`。

**关联设计文档:** `docs/superpowers/specs/2026-07-07-model-call-archive-design.md`

---

## 文件结构

**新建（均在 `backend/internal/service/`，与现有 `usage_record_worker_pool.go` 同包，零 import 环）：**
- `capture_record.go` — `CaptureRecord` DTO + 从原文抽取列的纯函数
- `capture_record_test.go`
- `clickhouse_archive_writer.go` — `ArchiveWriter` 接口 + ClickHouse 实现 + no-op 实现 + 批量 batcher + 建表
- `clickhouse_archive_writer_test.go`
- `conversation_capture_pool.go` — 有界 worker 池（overflow=drop），`Submit` / `Stop`
- `conversation_capture_pool_test.go`

**修改：**
- `backend/internal/config/config.go` — `GatewayCaptureConfig` + `CaptureClickHouseConfig` 结构、nest 进 `GatewayConfig`、viper 默认、`Validate()`
- `backend/internal/service/gateway_service.go` — `ForwardResult` 加字段（:561）；`streamingResult` 加字段；流式 tee（:8613/:8939）；非流式采集（:9305）
- `backend/internal/handler/gateway_handler.go` — usage tap 旁构建并提交 capture（:963-1013）
- `backend/internal/handler/openai_gateway_handler.go` — 同上（:525-533、:941-948）
- `backend/internal/service/wire.go` + `backend/cmd/server/wire_gen.go` — provider + 注入两 handler + cleanup `.Stop()`
- `backend/go.mod` — 加 clickhouse-go/v2

---

## Phase 0：依赖与配置

### Task 0: 添加 ClickHouse 客户端依赖

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`

- [ ] **Step 1: 添加依赖**

Run: `cd backend && go get github.com/ClickHouse/clickhouse-go/v2@latest`
Expected: `go.mod` 出现 `github.com/ClickHouse/clickhouse-go/v2` 行。

- [ ] **Step 2: 整理并验证可编译**

Run: `cd backend && go mod tidy && go build ./...`
Expected: 无错误。

- [ ] **Step 3: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "chore(capture): add clickhouse-go/v2 dependency"
```

### Task 1: 配置结构 GatewayCaptureConfig

**Files:**
- Modify: `backend/internal/config/config.go`（结构体紧邻 `GatewayUsageRecordConfig` 定义处 :997 之后；nest 点 :808-809 附近）

- [ ] **Step 1: 定义结构体**

在 `config.go` 中 `GatewayUsageRecordConfig` 结构体定义之后追加：

```go
// GatewayCaptureConfig 上游调用全量归档异步通道配置（默认关闭，关时零成本）。
type GatewayCaptureConfig struct {
	Enabled               bool                    `mapstructure:"enabled"`
	MaxBodyBytes          int                     `mapstructure:"max_body_bytes"`
	QueueSize             int                     `mapstructure:"queue_size"`
	WorkerCount           int                     `mapstructure:"worker_count"`
	OverflowPolicy        string                  `mapstructure:"overflow_policy"`
	OverflowSamplePercent int                     `mapstructure:"overflow_sample_percent"`
	BatchMaxSize          int                     `mapstructure:"batch_max_size"`
	BatchMaxIntervalMs    int                     `mapstructure:"batch_max_interval_ms"`
	ClickHouse            CaptureClickHouseConfig `mapstructure:"clickhouse"`
}

// CaptureClickHouseConfig 远端 ClickHouse 连接配置。
type CaptureClickHouseConfig struct {
	Addr          []string `mapstructure:"addr"`
	Database      string   `mapstructure:"database"`
	Table         string   `mapstructure:"table"`
	Username      string   `mapstructure:"username"`
	Password      string   `mapstructure:"password"`
	DialTimeoutMs int      `mapstructure:"dial_timeout_ms"`
	ReadTimeoutMs int      `mapstructure:"read_timeout_ms"`
	Compression   string   `mapstructure:"compression"`
	Secure        bool     `mapstructure:"secure"`
	MaxOpenConns  int      `mapstructure:"max_open_conns"`
}
```

- [ ] **Step 2: nest 进 GatewayConfig**

在 `GatewayConfig` 结构体（:696）中 `UsageRecord` 字段（:809）之后追加：

```go
	// Capture: 上游调用全量归档异步通道配置（有界队列 + ClickHouse）
	Capture GatewayCaptureConfig `mapstructure:"capture"`
```

- [ ] **Step 3: 编译**

Run: `cd backend && go build ./internal/config/`
Expected: 无错误。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/config/config.go
git commit -m "feat(capture): add GatewayCaptureConfig struct"
```

### Task 2: viper 默认值 + Validate 校验

**Files:**
- Modify: `backend/internal/config/config.go`（viper 默认区 :1969 附近；`Validate()` :2745 附近）
- Test: `backend/internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

在 `config_test.go` 追加：

```go
func TestGatewayCaptureConfigDefaults(t *testing.T) {
	cfg, err := Load("") // 复用现有 Load 签名；若不同按现有测试写法调整
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Gateway.Capture.Enabled {
		t.Fatal("capture must default to disabled")
	}
	if cfg.Gateway.Capture.OverflowPolicy != UsageRecordOverflowPolicyDrop {
		t.Fatalf("default overflow=drop, got %q", cfg.Gateway.Capture.OverflowPolicy)
	}
	if cfg.Gateway.Capture.MaxBodyBytes <= 0 || cfg.Gateway.Capture.QueueSize <= 0 {
		t.Fatal("defaults must be positive")
	}
}

func TestGatewayCaptureValidateRejectsSyncAndEmptyAddr(t *testing.T) {
	c := &Config{}
	c.Gateway.Capture.Enabled = true
	c.Gateway.Capture.OverflowPolicy = UsageRecordOverflowPolicySync
	c.Gateway.Capture.MaxBodyBytes = 1
	c.Gateway.Capture.QueueSize = 1
	c.Gateway.Capture.WorkerCount = 1
	c.Gateway.Capture.BatchMaxSize = 1
	if err := c.Gateway.Capture.validate(); err == nil {
		t.Fatal("sync overflow must be rejected for capture")
	}
	c.Gateway.Capture.OverflowPolicy = UsageRecordOverflowPolicyDrop
	if err := c.Gateway.Capture.validate(); err == nil {
		t.Fatal("empty clickhouse addr/database must be rejected")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/config/ -run TestGatewayCapture -v`
Expected: 编译失败或断言失败（`validate` 未定义 / 默认值缺失）。

- [ ] **Step 3: 加 viper 默认值**

在 viper 默认区（`gateway.usage_record.*` 之后，:1981 附近）追加：

```go
	viper.SetDefault("gateway.capture.enabled", false)
	viper.SetDefault("gateway.capture.max_body_bytes", 8388608)
	viper.SetDefault("gateway.capture.queue_size", 8192)
	viper.SetDefault("gateway.capture.worker_count", 4)
	viper.SetDefault("gateway.capture.overflow_policy", UsageRecordOverflowPolicyDrop)
	viper.SetDefault("gateway.capture.overflow_sample_percent", 0)
	viper.SetDefault("gateway.capture.batch_max_size", 200)
	viper.SetDefault("gateway.capture.batch_max_interval_ms", 1000)
	viper.SetDefault("gateway.capture.clickhouse.database", "llm_archive")
	viper.SetDefault("gateway.capture.clickhouse.table", "model_call_archive")
	viper.SetDefault("gateway.capture.clickhouse.dial_timeout_ms", 2000)
	viper.SetDefault("gateway.capture.clickhouse.read_timeout_ms", 10000)
	viper.SetDefault("gateway.capture.clickhouse.compression", "lz4")
	viper.SetDefault("gateway.capture.clickhouse.secure", false)
	viper.SetDefault("gateway.capture.clickhouse.max_open_conns", 8)
```

- [ ] **Step 4: 加 validate 方法**

在 `config.go` 中 `GatewayCaptureConfig` 附近追加，并在 `Config.Validate()` 中调用：

```go
// validate 仅在 Enabled=true 时校验；关闭时任意配置都放行。
func (c GatewayCaptureConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.ClickHouse.Addr) == 0 || strings.TrimSpace(c.ClickHouse.Database) == "" {
		return fmt.Errorf("gateway.capture: clickhouse addr/database required when enabled")
	}
	switch c.OverflowPolicy {
	case UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample:
	default:
		return fmt.Errorf("gateway.capture.overflow_policy must be drop|sample, got %q", c.OverflowPolicy)
	}
	switch c.ClickHouse.Compression {
	case "lz4", "zstd", "none", "":
	default:
		return fmt.Errorf("gateway.capture.clickhouse.compression must be lz4|zstd|none, got %q", c.ClickHouse.Compression)
	}
	if c.MaxBodyBytes <= 0 || c.QueueSize <= 0 || c.WorkerCount <= 0 || c.BatchMaxSize <= 0 {
		return fmt.Errorf("gateway.capture: max_body_bytes/queue_size/worker_count/batch_max_size must be > 0")
	}
	return nil
}
```

在 `Config.Validate()` 内（usage_record 校验 :2745 之后）追加：

```go
	if err := c.Gateway.Capture.validate(); err != nil {
		return err
	}
```

- [ ] **Step 5: 运行验证通过**

Run: `cd backend && go test ./internal/config/ -run TestGatewayCapture -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -m "feat(capture): viper defaults + validate for capture config"
```

---

## Phase 1：CaptureRecord DTO 与抽取函数（纯函数，热路径外）

### Task 3: CaptureRecord 结构与字节快照

**Files:**
- Create: `backend/internal/service/capture_record.go`
- Test: `backend/internal/service/capture_record_test.go`

- [ ] **Step 1: 写失败测试**

```go
package service

import "testing"

func TestSnapshotBytesCopiesInput(t *testing.T) {
	src := []byte(`{"a":1}`)
	got := snapshotBytes(src)
	if string(got) != string(src) {
		t.Fatalf("copy mismatch: %q", got)
	}
	src[0] = 'X' // 篡改源
	if got[0] == 'X' {
		t.Fatal("snapshot must be an independent copy")
	}
	if snapshotBytes(nil) != nil {
		t.Fatal("nil in -> nil out")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestSnapshotBytes -v`
Expected: FAIL（`snapshotBytes` / `CaptureRecord` 未定义）。

- [ ] **Step 3: 实现结构与快照**

创建 `capture_record.go`：

```go
package service

import "time"

// CaptureRecord 是提交给归档通道的一条原始上游调用快照。
// 所有 []byte 字段在提交前已 deep-copy，worker 内安全读取。
type CaptureRecord struct {
	CapturedAt      time.Time
	Platform        string
	RequestID       string
	RequestedModel  string
	UpstreamModel   string
	UpstreamEndpoint string
	Stream          bool
	HTTPStatus      int
	RawRequest      []byte // 最终上游请求体逐字
	RawResponse     []byte // 流式=原始 SSE；非流式=完整 JSON
	RequestHeaders  []byte // 脱敏后 JSON
	ResponseHeaders []byte // 脱敏后 JSON
	Truncated       bool

	// 以下抽取列由 worker 调用 extractCaptureColumns 填充，提交时可留空。
	SessionID        string
	ThinkingEffort   string
	ThinkingType     string
	StopReason       string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheCreationTokens int
	SignaturePresent bool
}

// snapshotBytes 返回 src 的独立副本，避免 worker 读到被后续改写的底层数组。
func snapshotBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd backend && go test ./internal/service/ -run TestSnapshotBytes -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "feat(capture): CaptureRecord DTO + snapshotBytes"
```

### Task 4: 抽取列函数（session_id / thinking / stop_reason / signature / usage）

**Files:**
- Modify: `backend/internal/service/capture_record.go`
- Test: `backend/internal/service/capture_record_test.go`

复用已有：`ParseMetadataUserID`（`metadata_userid.go:35`）、`extractBodySessionID`（`gateway_request.go:165`，同包可直接调）、`NormalizeClaudeOutputEffort`（`gateway_request.go:1220`）。

- [ ] **Step 1: 写失败测试**

```go
func TestExtractSessionIDFromMetadataUserID(t *testing.T) {
	body := []byte(`{"model":"claude","metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"11111111-1111-1111-1111-111111111111\"}"}}`)
	if got := extractCaptureSessionID(body); got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("session_id from metadata.user_id, got %q", got)
	}
	// fallback: 无 metadata.user_id 时取 body 的 session_id 提示字段
	body2 := []byte(`{"conversation_id":"conv-42"}`)
	if got := extractCaptureSessionID(body2); got != "conv-42" {
		t.Fatalf("fallback session hint, got %q", got)
	}
}

func TestExtractResponseColumnsNonStream(t *testing.T) {
	resp := []byte(`{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2},"content":[{"type":"thinking","signature":"sig"}]}`)
	cols := extractResponseColumns(resp, false)
	if cols.StopReason != "end_turn" || cols.InputTokens != 10 || cols.OutputTokens != 5 || cols.CacheReadTokens != 2 {
		t.Fatalf("bad cols: %+v", cols)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature must be detected")
	}
}

func TestExtractResponseColumnsStreamSSE(t *testing.T) {
	sse := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"s\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n")
	cols := extractResponseColumns(sse, true)
	if cols.StopReason != "tool_use" || cols.InputTokens != 7 || cols.OutputTokens != 3 {
		t.Fatalf("bad stream cols: %+v", cols)
	}
	if !cols.SignaturePresent {
		t.Fatal("signature_delta must set SignaturePresent")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestExtract -v`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: 实现抽取函数**

在 `capture_record.go` 追加（`import` 补 `"bufio"`, `"bytes"`, `"strings"`, `"github.com/tidwall/gjson"`）：

```go
type responseColumns struct {
	StopReason          string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	SignaturePresent    bool
}

// extractCaptureSessionID 优先从上游 body 的 metadata.user_id 解出 session_id，
// 无则 fallback 到 body 内 session 提示字段（prompt_cache_key/conversation_id/...）。
func extractCaptureSessionID(body []byte) string {
	if uid := gjson.GetBytes(body, "metadata.user_id").String(); uid != "" {
		if parsed := ParseMetadataUserID(uid); parsed != nil && parsed.SessionID != "" {
			return parsed.SessionID
		}
	}
	return extractBodySessionID(string(body))
}

// extractResponseColumns 轻扫描响应，取 stop_reason/usage/signature 抽取列。
// 流式=按 SSE 行累积（后到覆盖先到）；非流式=单个 JSON。不做完整组装。
func extractResponseColumns(resp []byte, stream bool) responseColumns {
	var cols responseColumns
	apply := func(js string) {
		if sr := gjson.Get(js, "stop_reason").String(); sr != "" {
			cols.StopReason = sr
		}
		if sr := gjson.Get(js, "delta.stop_reason").String(); sr != "" {
			cols.StopReason = sr
		}
		if v := gjson.Get(js, "usage.input_tokens"); v.Exists() {
			cols.InputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "message.usage.input_tokens"); v.Exists() {
			cols.InputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.output_tokens"); v.Exists() {
			cols.OutputTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.cache_read_input_tokens"); v.Exists() {
			cols.CacheReadTokens = int(v.Int())
		}
		if v := gjson.Get(js, "usage.cache_creation_input_tokens"); v.Exists() {
			cols.CacheCreationTokens = int(v.Int())
		}
		if strings.Contains(js, "\"signature\"") || strings.Contains(js, "signature_delta") {
			if gjson.Get(js, "signature").Exists() ||
				gjson.Get(js, "delta.signature").Exists() ||
				gjson.Get(js, "delta.type").String() == "signature_delta" {
				cols.SignaturePresent = true
			}
			// 非流式：content 数组内任意 block 带非空 signature
			gjson.Get(js, "content").ForEach(func(_, b gjson.Result) bool {
				if b.Get("signature").String() != "" {
					cols.SignaturePresent = true
					return false
				}
				return true
			})
		}
	}
	if !stream {
		apply(string(resp))
		return cols
	}
	sc := bufio.NewScanner(bytes.NewReader(resp))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload != "" && payload != "[DONE]" {
				apply(payload)
			}
		}
	}
	return cols
}

// extractCaptureColumns 在 worker 内填充 rec 的抽取列。
func extractCaptureColumns(rec *CaptureRecord) {
	rec.SessionID = extractCaptureSessionID(rec.RawRequest)
	rec.ThinkingEffort = NormalizeClaudeOutputEffort(gjson.GetBytes(rec.RawRequest, "output_config.effort").String())
	rec.ThinkingType = gjson.GetBytes(rec.RawRequest, "thinking.type").String()
	cols := extractResponseColumns(rec.RawResponse, rec.Stream)
	rec.StopReason = cols.StopReason
	rec.InputTokens = cols.InputTokens
	rec.OutputTokens = cols.OutputTokens
	rec.CacheReadTokens = cols.CacheReadTokens
	rec.CacheCreationTokens = cols.CacheCreationTokens
	rec.SignaturePresent = cols.SignaturePresent
}
```

> 注：`NormalizeClaudeOutputEffort` 返回 `*string`，需按其实际签名取值；若返回 nil 用 `""`。实现时确认签名并适配（见 `gateway_request.go:1220`）。

- [ ] **Step 4: 运行验证通过**

Run: `cd backend && go test ./internal/service/ -run TestExtract -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "feat(capture): raw request/response column extraction"
```

---

## Phase 2：ClickHouse 归档写入器

### Task 5: ArchiveWriter 接口 + no-op 实现

**Files:**
- Create: `backend/internal/service/clickhouse_archive_writer.go`
- Test: `backend/internal/service/clickhouse_archive_writer_test.go`

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"context"
	"testing"
)

func TestNoopArchiveWriterNeverErrors(t *testing.T) {
	var w ArchiveWriter = noopArchiveWriter{}
	if err := w.Write(context.Background(), &CaptureRecord{}); err != nil {
		t.Fatalf("noop write must not error: %v", err)
	}
	w.Stop() // 不 panic
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestNoopArchiveWriter -v`
Expected: FAIL（`ArchiveWriter` / `noopArchiveWriter` 未定义）。

- [ ] **Step 3: 实现接口与 no-op**

创建 `clickhouse_archive_writer.go`：

```go
package service

import "context"

// ArchiveWriter 把一条 CaptureRecord 写入归档存储。实现必须自身吸收所有
// 错误（不得外溢到调用方转发路径）；此处返回的 error 仅用于内部计数/日志。
type ArchiveWriter interface {
	Write(ctx context.Context, rec *CaptureRecord) error
	Stop()
}

// noopArchiveWriter 在 capture 关闭时注入，零副作用。
type noopArchiveWriter struct{}

func (noopArchiveWriter) Write(context.Context, *CaptureRecord) error { return nil }
func (noopArchiveWriter) Stop()                                       {}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd backend && go test ./internal/service/ -run TestNoopArchiveWriter -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/clickhouse_archive_writer.go backend/internal/service/clickhouse_archive_writer_test.go
git commit -m "feat(capture): ArchiveWriter interface + noop impl"
```

### Task 6: ClickHouse 实现（连接、建表、批量 batcher）

**Files:**
- Modify: `backend/internal/service/clickhouse_archive_writer.go`
- Test: `backend/internal/service/clickhouse_archive_writer_test.go`

- [ ] **Step 1: 写失败测试（建表 DDL 内容断言，不需真连库）**

```go
func TestCreateTableDDLContainsRawColumns(t *testing.T) {
	ddl := archiveCreateTableDDL("llm_archive", "model_call_archive")
	for _, must := range []string{
		"CREATE TABLE IF NOT EXISTS llm_archive.model_call_archive",
		"raw_request        String CODEC(ZSTD(3))",
		"raw_response       String CODEC(ZSTD(3))",
		"session_id         String",
		"ORDER BY (session_id, captured_at, request_id)",
		"PARTITION BY toYYYYMM(captured_at)",
	} {
		if !strings.Contains(ddl, must) {
			t.Fatalf("DDL missing %q", must)
		}
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestCreateTableDDL -v`
Expected: FAIL（`archiveCreateTableDDL` 未定义）。

- [ ] **Step 3: 实现 DDL + ClickHouse writer + batcher**

在 `clickhouse_archive_writer.go` 追加（import：`fmt strings sync time context`，`ch "github.com/ClickHouse/clickhouse-go/v2"`，`"github.com/Wei-Shaw/sub2api/internal/config"`，`"github.com/Wei-Shaw/sub2api/internal/pkg/logger"` — 按仓库实际 logger 包路径校正）：

```go
func archiveCreateTableDDL(database, table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (
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
SETTINGS index_granularity = 8192`, database, table)
}

const captureVersion = 1
```

同文件继续（`clickHouseArchiveWriter` 结构 + 批量 channel，仿 `usage_log_repo.go` 的 `runBestEffortBatcher`）：

```go
type clickHouseArchiveWriter struct {
	conn      ch.Conn
	database  string
	table     string
	batchCh   chan *CaptureRecord
	batchMax  int
	batchWait time.Duration
	stopOnce  sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}
```

方法要点（实现时补全）：
- `Write`：`select { case w.batchCh <- rec: return nil; default: return errArchiveQueueFull }`（batcher 自身队列满即丢，绝不阻塞）。
- 后台 `runBatcher()`：`for` + `select`，累积到 `batchMax` 或 `batchWait` ticker 触发即 `flush`。
- `flush(batch)`：`w.conn.PrepareBatch(ctx, "INSERT INTO db.table")` → 逐行 `batch.Append(rec.CapturedAt, rec.RequestID, rec.SessionID, ...)`（顺序与 DDL 列一致，`stream`/`signature_present`/`is_truncated` 用 `b2u8`）→ `batch.Send()`；错误 log + 计数，**不 panic、不外溢**。
- `Stop()`：`stopOnce` → close(done) → `wg.Wait()`（flush 剩余）。

```go
func b2u8(b bool) uint8 { if b { return 1 }; return 0 }
```

- [ ] **Step 4: 实现构造函数**

```go
// newClickHouseArchiveWriter 建连、建表、启动 batcher。失败返回 error，
// 调用方（provider）据此降级为 noopArchiveWriter，绝不阻塞启动。
func newClickHouseArchiveWriter(cfg config.GatewayCaptureConfig) (ArchiveWriter, error) {
	// ch.Open(&ch.Options{Addr: cfg.ClickHouse.Addr, Auth: ch.Auth{Database, Username, Password}, ...})
	// conn.Ping(ctx); conn.Exec(ctx, archiveCreateTableDDL(db, table))
	// 启动 runBatcher goroutine
	// 返回 *clickHouseArchiveWriter
	panic("implement per anchors above")
}
```

> 实现时删除 `panic`，按注释补全；`Compression`/`Secure`/超时映射到 `ch.Options`。

- [ ] **Step 5: 运行 DDL 测试通过**

Run: `cd backend && go test ./internal/service/ -run TestCreateTableDDL -v`
Expected: PASS。

- [ ] **Step 6: 编译**

Run: `cd backend && go build ./internal/service/`
Expected: 无错误。

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/clickhouse_archive_writer.go backend/internal/service/clickhouse_archive_writer_test.go
git commit -m "feat(capture): clickhouse archive writer with batching + create table"
```

---

## Phase 3：ConversationCapturePool（有界队列，overflow=drop）

### Task 7: 采集 worker 池

**Files:**
- Create: `backend/internal/service/conversation_capture_pool.go`
- Test: `backend/internal/service/conversation_capture_pool_test.go`

模式参照 `usage_record_worker_pool.go`（pond 池、`Submit`、`Stop`），但 **overflow 只支持 drop/sample，绝不 sync**；worker 内先 `extractCaptureColumns(rec)` 再 `writer.Write`。

- [ ] **Step 1: 写失败测试**

```go
package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWriter struct{ n int32 }

func (f *fakeWriter) Write(_ context.Context, rec *CaptureRecord) error {
	atomic.AddInt32(&f.n, 1)
	return nil
}
func (f *fakeWriter) Stop() {}

func TestCapturePoolSubmitAndExtract(t *testing.T) {
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 2, QueueSize: 16, OverflowPolicy: "drop",
	}, fw)
	defer p.Stop()

	rec := &CaptureRecord{
		RawRequest: []byte(`{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"sess-1\"}"}}`),
		RawResponse: []byte(`{"stop_reason":"end_turn","usage":{"output_tokens":4}}`),
	}
	p.Submit(rec)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&fw.n) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&fw.n) != 1 {
		t.Fatal("record not written")
	}
	if rec.SessionID != "sess-1" || rec.StopReason != "end_turn" {
		t.Fatalf("columns not extracted in worker: %+v", rec)
	}
}

func TestCapturePoolDropsOnFullQueue(t *testing.T) {
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 1, OverflowPolicy: "drop",
	}, fw)
	defer p.Stop()
	// 猛灌，验证不阻塞、不 panic（丢弃是允许的）
	for i := 0; i < 1000; i++ {
		p.Submit(&CaptureRecord{RawResponse: []byte(`{}`)})
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestCapturePool -v`
Expected: FAIL（未定义）。

- [ ] **Step 3: 实现池**

创建 `conversation_capture_pool.go`：

```go
package service

import (
	"context"
	"sync"

	"github.com/alitto/pond/v2"
)

type conversationCapturePoolOptions struct {
	WorkerCount    int
	QueueSize      int
	OverflowPolicy string // drop | sample
	SamplePercent  int
}

// ConversationCapturePool 是与转发/计费隔离的第三条异步通道。
// 队列满时按 overflow 策略 drop/sample，绝不 sync 回写、绝不阻塞热路径。
type ConversationCapturePool struct {
	pool     pond.Pool
	writer   ArchiveWriter
	overflow string
	sample   int
	stopOnce sync.Once
}

func newConversationCapturePool(opts conversationCapturePoolOptions, writer ArchiveWriter) *ConversationCapturePool {
	pool := pond.NewPool(opts.WorkerCount, pond.WithQueueSize(opts.QueueSize))
	return &ConversationCapturePool{
		pool:     pool,
		writer:   writer,
		overflow: opts.OverflowPolicy,
		sample:   opts.SamplePercent,
	}
}

// Submit 非阻塞提交。队列满时按策略丢弃/采样。
func (p *ConversationCapturePool) Submit(rec *CaptureRecord) {
	if p == nil || rec == nil {
		return
	}
	task := func() {
		defer func() { _ = recover() }() // worker panic 不外溢
		extractCaptureColumns(rec)
		_ = p.writer.Write(context.Background(), rec)
	}
	if ok := p.pool.TrySubmit(task); ok {
		return
	}
	// 队列满：drop（默认）。sample 策略可在此按 p.sample 概率再试一次同步执行；
	// 但为绝不阻塞热路径，这里 sample 也仅做“概率性再 TrySubmit”，失败即丢。
	if p.overflow == "sample" && p.sample > 0 && sampleHit(p.sample) {
		_ = p.pool.TrySubmit(task)
	}
	// drop：什么都不做。
}

func (p *ConversationCapturePool) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		p.pool.StopAndWait()
		p.writer.Stop()
	})
}
```

`sampleHit` 复用现有实现（`usage_record_worker_pool.go` 内若有则复用，否则加最小实现）：

```go
// 若同包已有等价采样函数，删除此实现并复用。
func sampleHit(percent int) bool {
	if percent <= 0 { return false }
	if percent >= 100 { return true }
	return fastRandN(100) < uint32(percent)
}
```

> 实现时确认 `pond.Pool` 是否有 `TrySubmit`（v2 API）；若为 `SubmitErr`/`TrySubmit` 名称差异按实际 API 调整（参照 `usage_record_worker_pool.go:146` 现有用法）。`fastRandN` 若无则用 `math/rand`。

- [ ] **Step 4: 运行验证通过**

Run: `cd backend && go test ./internal/service/ -run TestCapturePool -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/conversation_capture_pool.go backend/internal/service/conversation_capture_pool_test.go
git commit -m "feat(capture): ConversationCapturePool with drop overflow"
```

---

## Phase 4：ForwardResult 携带响应正文 + 非流式采集

### Task 8: ForwardResult 加 capture 字段 + 非流式响应拷贝

**Files:**
- Modify: `backend/internal/service/gateway_service.go`（`ForwardResult` :561；`handleNonStreamingResponse` :9305）
- Test: `backend/internal/service/gateway_service_capture_test.go`（新建）

**前提约束（来自设计 §3.2）**：只在 `s.cfg.Gateway.Capture.Enabled` 为真时填充，否则字段保持 nil（零成本）。

- [ ] **Step 1: 加字段**

在 `ForwardResult`（:561）末尾追加：

```go
	// ── 归档采集（仅 gateway.capture.enabled=true 时填充，否则 nil）──
	CaptureResponse  []byte // 流式=原始 SSE 字节流；非流式=完整响应 body
	CaptureTruncated bool   // 采集 buffer 超过 max_body_bytes 被截断
```

- [ ] **Step 2: 写失败测试（非流式采集开关）**

```go
func TestNonStreamingCaptureRespectsFlag(t *testing.T) {
	body := []byte(`{"stop_reason":"end_turn"}`)
	// captureEnabled=false -> 不采集
	if got := captureResponseIfEnabled(false, body, 1024); got != nil {
		t.Fatal("must be nil when disabled")
	}
	// enabled=true -> 拷贝
	got := captureResponseIfEnabled(true, body, 1024)
	if string(got) != string(body) {
		t.Fatalf("copy mismatch: %q", got)
	}
	body[0] = 'X'
	if got[0] == 'X' {
		t.Fatal("must be independent copy")
	}
}

func TestCaptureTruncation(t *testing.T) {
	got, truncated := captureWithLimit([]byte("0123456789"), 4)
	if string(got) != "0123" || !truncated {
		t.Fatalf("got %q truncated=%v", got, truncated)
	}
	got2, truncated2 := captureWithLimit([]byte("ab"), 4)
	if string(got2) != "ab" || truncated2 {
		t.Fatalf("got %q truncated=%v", got2, truncated2)
	}
}
```

- [ ] **Step 3: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run 'TestNonStreamingCapture|TestCaptureTruncation' -v`
Expected: FAIL（未定义）。

- [ ] **Step 4: 实现 helper**

在 `gateway_service.go`（或新 `capture_record.go`）加：

```go
// captureWithLimit 返回最多 limit 字节的独立副本及是否被截断。limit<=0 视为不采集。
func captureWithLimit(src []byte, limit int) ([]byte, bool) {
	if limit <= 0 || src == nil {
		return nil, false
	}
	if len(src) <= limit {
		return snapshotBytes(src), false
	}
	dst := make([]byte, limit)
	copy(dst, src[:limit])
	return dst, true
}

// captureResponseIfEnabled 便于测试的薄封装。
func captureResponseIfEnabled(enabled bool, src []byte, limit int) []byte {
	if !enabled {
		return nil
	}
	b, _ := captureWithLimit(src, limit)
	return b
}
```

- [ ] **Step 5: 在 handleNonStreamingResponse 填充**

在 `handleNonStreamingResponse`（:9305）读到完整 `body` 后、返回 `ForwardResult` 前，加（守卫开关）：

```go
	if s.cfg != nil && s.cfg.Gateway.Capture.Enabled {
		resp, truncated := captureWithLimit(body, s.cfg.Gateway.Capture.MaxBodyBytes)
		result.CaptureResponse = resp
		result.CaptureTruncated = truncated
	}
```

（`result` 为该函数即将返回的 `*ForwardResult`；按实际变量名适配。）

- [ ] **Step 6: 运行验证通过 + 编译**

Run: `cd backend && go test ./internal/service/ -run 'TestNonStreamingCapture|TestCaptureTruncation' -v && go build ./...`
Expected: PASS，无编译错误。

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/gateway_service.go backend/internal/service/gateway_service_capture_test.go backend/internal/service/capture_record.go
git commit -m "feat(capture): ForwardResult capture fields + non-streaming capture"
```

### Task 9: 流式 tee（上游 SSE 原始字节）

**Files:**
- Modify: `backend/internal/service/gateway_service.go`（上游 SSE 读取 goroutine :8613；`streamingResult` 结构；`handleStreamingResponse` :8560 的返回映射到 `ForwardResult`）
- Test: `backend/internal/service/gateway_service_capture_test.go`

**关键约束（设计坑2）**：tee 必须挂在**上游读取侧**（`scanner.Scan()` 循环 :8616），累积的是**厂商原始 SSE 行**，免疫客户端侧 `thinking_delta↔thinking` 改写。累积上限 `MaxBodyBytes`，超出停累并标 truncated。

- [ ] **Step 1: 写失败测试（tee 累加器）**

```go
func TestSSETeeAccumulatorAppendsRawLines(t *testing.T) {
	acc := newSSETee(1024)
	acc.appendLine("event: message_start")
	acc.appendLine(`data: {"type":"message_start"}`)
	acc.appendLine("")
	out, truncated := acc.bytes()
	want := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	if string(out) != want || truncated {
		t.Fatalf("got %q truncated=%v", out, truncated)
	}
}

func TestSSETeeTruncates(t *testing.T) {
	acc := newSSETee(5)
	acc.appendLine("0123456789")
	out, truncated := acc.bytes()
	if len(out) > 5 || !truncated {
		t.Fatalf("expected truncation, got %q trunc=%v", out, truncated)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestSSETee -v`
Expected: FAIL（未定义）。

- [ ] **Step 3: 实现 tee 累加器**

在 `capture_record.go` 加：

```go
// sseTee 在上游 SSE 读取循环里按行累积原始字节（含 SSE 帧换行），
// 达到 limit 后停止累积并标记 truncated。非并发安全：仅在单个读 goroutine 内使用。
type sseTee struct {
	buf       []byte
	limit     int
	truncated bool
}

func newSSETee(limit int) *sseTee { return &sseTee{limit: limit} }

func (t *sseTee) appendLine(line string) {
	if t == nil || t.truncated {
		return
	}
	// 还原 scanner 去掉的换行：每行后加 "\n"（SSE 事件间空行即成 "\n\n"）。
	chunk := line + "\n"
	if len(t.buf)+len(chunk) > t.limit {
		remain := t.limit - len(t.buf)
		if remain > 0 {
			t.buf = append(t.buf, chunk[:remain]...)
		}
		t.truncated = true
		return
	}
	t.buf = append(t.buf, chunk...)
}

func (t *sseTee) bytes() ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	return t.buf, t.truncated
}
```

- [ ] **Step 4: 挂载到上游读取 goroutine**

在 `handleStreamingResponse`（:8560）中：
1. 函数开头（开关守卫）：`var tee *sseTee; if s.cfg != nil && s.cfg.Gateway.Capture.Enabled { tee = newSSETee(s.cfg.Gateway.Capture.MaxBodyBytes) }`
2. 上游读取 goroutine（:8616 `for scanner.Scan()`）内，`sendEvent` 之前加：`if tee != nil { tee.appendLine(scanner.Text()) }`
3. `streamingResult` 结构加字段 `captureResponse []byte` + `captureTruncated bool`；流式结束（终止事件后）：`if tee != nil { b, tr := tee.bytes(); sr.captureResponse = b; sr.captureTruncated = tr }`
4. 在 `handleStreamingResponse` 的调用方（把 `streamingResult` 映射成 `ForwardResult` 处，参照 :5916/:6179/:6869 的 `return &ForwardResult{...}`）把 `captureResponse/captureTruncated` 拷到 `ForwardResult.CaptureResponse/CaptureTruncated`。

> tee 只在读 goroutine 单线程写、结束后主循环读，需确保读 goroutine 已退出（`close(events)` 之后）再取 `bytes()`，避免竞态。实现时把 `bytes()` 取值放在主循环收到 channel 关闭/终止事件之后。

- [ ] **Step 5: 运行 tee 单测通过 + 编译 + -race**

Run: `cd backend && go test ./internal/service/ -run TestSSETee -v && go build ./... && go test -race ./internal/service/ -run 'TestSSETee|TestCapturePool'`
Expected: PASS，无 race。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/gateway_service.go backend/internal/service/capture_record.go backend/internal/service/gateway_service_capture_test.go
git commit -m "feat(capture): streaming SSE tee on upstream read path"
```

---

## Phase 5：脱敏 + Handler 采集点

### Task 10: 请求/响应头脱敏

**Files:**
- Modify: `backend/internal/service/capture_record.go`
- Test: `backend/internal/service/capture_record_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestRedactHeadersStripsCredentials(t *testing.T) {
	h := map[string][]string{
		"Authorization":     {"Bearer secret"},
		"X-Api-Key":         {"sk-xxx"},
		"Cookie":            {"a=b"},
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"tools-2024"},
		"X-Request-Id":      {"req-1"},
	}
	out := redactHeadersJSON(h)
	s := string(out)
	for _, secret := range []string{"secret", "sk-xxx", "a=b"} {
		if strings.Contains(s, secret) {
			t.Fatalf("credential leaked: %q in %s", secret, s)
		}
	}
	for _, keep := range []string{"2023-06-01", "tools-2024", "req-1"} {
		if !strings.Contains(s, keep) {
			t.Fatalf("must keep %q; got %s", keep, s)
		}
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd backend && go test ./internal/service/ -run TestRedactHeaders -v`
Expected: FAIL。

- [ ] **Step 3: 实现脱敏**

在 `capture_record.go` 加（import `encoding/json`, `net/http`, `strings`）：

```go
// 需剥离的凭证类 header（小写匹配）。
var redactedHeaderKeys = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"cookie":              {},
	"set-cookie":          {},
	"proxy-authorization": {},
	"x-goog-api-key":      {},
}

// redactHeadersJSON 剥离凭证类头后序列化为 JSON。保留影响模型行为/诊断的头。
func redactHeadersJSON(h map[string][]string) []byte {
	if len(h) == 0 {
		return nil
	}
	clean := make(map[string][]string, len(h))
	for k, v := range h {
		if _, bad := redactedHeaderKeys[strings.ToLower(k)]; bad {
			continue
		}
		clean[k] = v
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return nil
	}
	return b
}

// redactHTTPHeader 是 http.Header 的适配。
func redactHTTPHeader(h http.Header) []byte { return redactHeadersJSON(map[string][]string(h)) }
```

- [ ] **Step 4: 运行验证通过**

Run: `cd backend && go test ./internal/service/ -run TestRedactHeaders -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "feat(capture): header redaction stripping credentials"
```

### Task 11: Anthropic handler 采集点

**Files:**
- Modify: `backend/internal/handler/gateway_handler.go`（usage tap :963-1013；`GatewayHandler` 结构 :65；构造 `NewGatewayHandler` :85）

**注入方式**：给 `GatewayHandler` 加字段 `capturePool *service.ConversationCapturePool`，构造函数追加参数（wire 在 Task 13 接线）。

- [ ] **Step 1: 加字段与构造参数**

`GatewayHandler` 结构（:65）追加：

```go
	capturePool *service.ConversationCapturePool
```

`NewGatewayHandler`（:85）参数列表末尾加 `capturePool *service.ConversationCapturePool`，并在体内赋值 `h.capturePool = capturePool`。

- [ ] **Step 2: 采集点提交**

在 usage tap（:963-1013）内、`h.submitUsageRecordTask(...)` 之后追加（与计费独立、可 drop）：

```go
			if h.capturePool != nil && result.CaptureResponse != nil {
				finalBody := attemptParsedReq.Body.Bytes() // 最终上游 body（成功那次）
				rec := &service.CaptureRecord{
					CapturedAt:       time.Now().UTC(),
					Platform:         string(account.Platform),
					RequestID:        result.RequestID,
					RequestedModel:   result.Model,
					UpstreamModel:    result.UpstreamModel,
					UpstreamEndpoint: upstreamEndpoint,
					Stream:           result.Stream,
					HTTPStatus:       200, // 成功路径；错误路径见 Task 12 备注
					RawRequest:       service.SnapshotForCapture(finalBody, h.captureLimit()),
					RawResponse:      result.CaptureResponse, // Forward 内已按 limit 截断
					RequestHeaders:   service.RedactRequestHeaders(c.Request.Header),
					Truncated:        result.CaptureTruncated,
				}
				h.capturePool.Submit(rec)
			}
```

> 需要导出两个薄封装供 handler 包调用（在 `capture_record.go` 加）：
> ```go
> func SnapshotForCapture(src []byte, limit int) []byte { b, _ := captureWithLimit(src, limit); return b }
> func RedactRequestHeaders(h http.Header) []byte { return redactHTTPHeader(h) }
> ```
> `h.captureLimit()` 返回 `h.cfg.Gateway.Capture.MaxBodyBytes`（`GatewayHandler` 已持有 `configConfig`，见 :265 注入的 `configConfig`）。响应头脱敏：上游响应头若在 `result` 不可得，本期可留空（`ResponseHeaders` 后续从 Forward 透传，见 Task 12 备注）。

- [ ] **Step 3: 编译**

Run: `cd backend && go build ./internal/handler/ ./internal/service/`
Expected: 无错误（wire 未接线前，构造调用点暂时编译不过属正常——Task 13 修复；或先临时传 nil 保持可编译）。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handler/gateway_handler.go backend/internal/service/capture_record.go
git commit -m "feat(capture): submit capture record from anthropic handler tap"
```

### Task 12: OpenAI handler 采集点

**Files:**
- Modify: `backend/internal/handler/openai_gateway_handler.go`（tap :525-533、:941-948；结构与构造）

- [ ] **Step 1: 加字段/构造参数**：同 Task 11（`capturePool` 字段 + `NewOpenAIGatewayHandler` 追加参数）。

- [ ] **Step 2: 两个 tap 点提交**：在 `:533` 与 `:948` 的 `RecordUsage` 调用附近，仿 Task 11 构建 `service.CaptureRecord` 并 `h.capturePool.Submit(rec)`；`RawRequest` 用该路径的 `body`（:525/:941 已 `HashUsageRequestPayload(body)`），`RawResponse` 取 `result.CaptureResponse`，`Platform` 用该 handler 的平台判定。

- [ ] **Step 3: 编译**

Run: `cd backend && go build ./internal/handler/`
Expected: 无错误（或临时 nil）。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handler/openai_gateway_handler.go
git commit -m "feat(capture): submit capture record from openai handler taps"
```

> **备注（HTTP 状态码与响应头）**：本期成功路径固定 `HTTPStatus=200` 且 `ResponseHeaders` 可空。若需精确记录上游状态码/响应头（含错误响应），在 `ForwardResult` 增补 `UpstreamStatus int` 与 `UpstreamRespHeaders []byte`（脱敏），在 `handleStreaming/NonStreamingResponse` 填充。列为 Task 12b 可选增强，不阻塞主线。

---

## Phase 6：依赖注入（wire）

### Task 13: provider + 注入两 handler + cleanup Stop

**Files:**
- Modify: `backend/internal/service/wire.go`（provider set :658 附近）
- Modify: `backend/cmd/server/wire_gen.go`（实例化 :262；注入 :265-266；cleanup :456-459；`provideCleanup` 形参 :343）

> 说明：本仓库 `wire_gen.go` 为生成文件但已手改（含手写 cleanup）。可直接改 `wire_gen.go`；若跑 `wire` 生成，需先在 `wire.go` 注册 provider。两处都改以保持一致。

- [ ] **Step 1: 写 provider 构造函数**

在 `conversation_capture_pool.go` 加对外 provider（enabled=false 时 no-op、绝不建连、绝不阻塞启动）：

```go
// NewConversationCapturePool 是 wire provider。capture 关闭或建连失败时，
// 返回一个使用 noopArchiveWriter 的池（Submit 仍安全但 Write 无副作用），
// 或返回 nil（handler 侧已做 nil 保护）。绝不阻塞启动、绝不影响转发。
func NewConversationCapturePool(cfg *config.Config) *ConversationCapturePool {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	cc := cfg.Gateway.Capture
	writer, err := newClickHouseArchiveWriter(cc)
	if err != nil {
		logger.L().With(zap.String("component", "capture")).
			Error("capture.clickhouse_init_failed_degrade_noop", zap.Error(err))
		writer = noopArchiveWriter{}
	}
	return newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount:    cc.WorkerCount,
		QueueSize:      cc.QueueSize,
		OverflowPolicy: cc.OverflowPolicy,
		SamplePercent:  cc.OverflowSamplePercent,
	}, writer)
}
```

（import 补 `"github.com/Wei-Shaw/sub2api/internal/config"` 及仓库 logger/zap 包，按实际路径。）

- [ ] **Step 2: 注册 provider**

`internal/service/wire.go` 的 provider 列表（:658 `NewUsageRecordWorkerPool,` 旁）追加：

```go
	NewConversationCapturePool,
```

- [ ] **Step 3: wire_gen.go 实例化 + 注入**

在 `wire_gen.go:262`（`usageRecordWorkerPool := ...` 旁）加：

```go
	conversationCapturePool := service.NewConversationCapturePool(configConfig)
```

`NewGatewayHandler(...)`（:265）与 `NewOpenAIGatewayHandler(...)`（:266）调用末尾加实参 `conversationCapturePool`（与 Task 11/12 的构造参数顺序一致）。

- [ ] **Step 4: cleanup 注册 Stop**

`provideCleanup` 形参列表（:343 附近）加 `conversationCapturePool *service.ConversationCapturePool`；调用点（:298）传入 `conversationCapturePool`；在 cleanup steps（:456 `{"UsageRecordWorkerPool", ...}` 旁）加：

```go
			{"ConversationCapturePool", func() error {
				if conversationCapturePool != nil {
					conversationCapturePool.Stop()
				}
				return nil
			}},
```

- [ ] **Step 5: 编译全量**

Run: `cd backend && go build ./...`
Expected: 无错误。

- [ ] **Step 6: 全量测试**

Run: `cd backend && go test ./internal/service/ ./internal/config/ ./internal/handler/ 2>&1 | tail -30`
Expected: PASS（无 ClickHouse 环境时集成测试自动 skip）。

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/wire.go backend/internal/service/conversation_capture_pool.go backend/cmd/server/wire_gen.go backend/internal/handler/
git commit -m "feat(capture): wire capture pool into handlers + cleanup"
```

---

## Phase 7：集成、零成本断言、文档

### Task 14: 零成本断言 + 集成往返测试

**Files:**
- Test: `backend/internal/service/gateway_service_capture_test.go`
- Test: `backend/internal/service/clickhouse_archive_writer_integration_test.go`（新建，build tag 或 env 门控）

- [ ] **Step 1: 零成本断言测试**

```go
func TestCaptureDisabledZeroCost(t *testing.T) {
	// enabled=false 时 captureResponseIfEnabled 返回 nil、不分配
	if captureResponseIfEnabled(false, []byte("x"), 1024) != nil {
		t.Fatal("must not capture when disabled")
	}
	// nil 池 Submit 安全
	var p *ConversationCapturePool
	p.Submit(&CaptureRecord{}) // 不 panic
}
```

- [ ] **Step 2: 集成往返测试（env 门控，无环境 skip）**

```go
func TestClickHouseRoundTrip(t *testing.T) {
	dsn := os.Getenv("CAPTURE_CH_ADDR")
	if dsn == "" {
		t.Skip("set CAPTURE_CH_ADDR to run clickhouse integration test")
	}
	// newClickHouseArchiveWriter -> Write 一条含乱码字节的 raw_response ->
	// 查询读回 -> 逐字节相等（验证坑3：非 UTF-8 也能存回）
}
```

- [ ] **Step 3: 运行**

Run: `cd backend && go test ./internal/service/ -run 'TestCaptureDisabledZeroCost|TestClickHouseRoundTrip' -v`
Expected: 零成本 PASS；集成 SKIP（无环境）。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/gateway_service_capture_test.go backend/internal/service/clickhouse_archive_writer_integration_test.go
git commit -m "test(capture): zero-cost assertion + clickhouse round-trip integration"
```

### Task 15: 配置样例文档

**Files:**
- Modify: 仓库配置样例文件（如 `backend/config.example.yaml` / `deploy/` 下对应样例——先 `grep -rl "usage_record" --include=*.yaml` 定位）

- [ ] **Step 1: 定位样例**

Run: `cd /home/alvin/tokenstation3 && grep -rln "usage_record" --include=*.yaml --include=*.yml .`
Expected: 列出含 `gateway.usage_record` 的样例文件。

- [ ] **Step 2: 追加 capture 样例段**

在样例的 `gateway:` 下追加设计文档 §4 的 `capture:` 配置块（`enabled: false` 默认关，附注释与 §4.1 部署要点）。

- [ ] **Step 3: Commit**

```bash
git add <定位到的样例文件>
git commit -m "docs(capture): add gateway.capture config example"
```

---

## 最终验证

- [ ] **全量编译**：`cd backend && go build ./...` → 无错误
- [ ] **全量测试**：`cd backend && go test ./... 2>&1 | tail -40` → PASS（集成测试 skip）
- [ ] **race**：`cd backend && go test -race ./internal/service/ -run 'Capture|SSETee|Extract'` → 无 race
- [ ] **关时零成本冒烟**：`enabled: false` 启动服务，转发一次请求，确认 ClickHouse 无连接、无 goroutine 泄漏、转发正常
- [ ] **开时端到端**（有 CH 环境）：`enabled: true` + 配好 addr，转发一次流式 + 一次非流式，ClickHouse 查到 2 行，`raw_response` 流式为原始 SSE、`signature_present` 正确、`session_id` 来自 metadata.user_id

## 设计覆盖对照（self-review）

| 设计文档章节 | 对应任务 |
|---|---|
| §2 A1 内存队列 / overflow=drop | Task 7 |
| §3.1 零成本开关 | Task 8/9（守卫）、Task 14（断言） |
| §3.2 坑1 最终上游 body | Task 11（`attemptParsedReq.Body.Bytes()`） |
| §3.2 坑2 上游侧 tee | Task 9 |
| §3.2 坑3 字节存储 | Task 6（`String`）、Task 14（乱码往返） |
| §3.2 坑4 响应头 | Task 10 + Task 12b 备注 |
| §4 配置 | Task 1/2 |
| §4.1 部署与访问 | Task 15 样例 |
| §5 组件 | Task 5/6/7 |
| §6 表结构 | Task 6 DDL |
| §7 SDK 字段覆盖 | Task 3/4 抽取 |
| §8 流式一行 + 只存 SSE | Task 9 |
| §9 脱敏 | Task 10 |
| §10 错误处理不外溢 | Task 6/7（recover、drop、no-op 降级） |
| §11 测试 | Task 2/3/4/7/14 |

---

## 已知风险与实现注意

1. **pond v2 API 名称**：`TrySubmit` / `StopAndWait` 以 `usage_record_worker_pool.go` 现有用法为准（:134/:146/:208），有差异按现有代码适配。
2. **`NormalizeClaudeOutputEffort` 返回类型**：确认是否 `*string`，nil 取 `""`（Task 4）。
3. **`streamingResult` → `ForwardResult` 映射点**：`handleStreamingResponse`(:8560) 返回 `*streamingResult`；需追踪其被转成 `ForwardResult` 的位置把 capture 字段带出（Task 9 Step 4）。
4. **wire_gen.go 手改**：本仓库该文件已手工维护 cleanup，直接改即可；若团队用 `go generate ./...` 跑 wire，务必同步 `wire.go`。
5. **HTTP 状态/响应头精确化**：主线固定 200 + 空响应头；需要错误响应归档时做 Task 12b。
