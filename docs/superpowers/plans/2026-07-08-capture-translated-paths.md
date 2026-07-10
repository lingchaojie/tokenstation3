# 翻译型上游路径归档接入 + 保真修复 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Kiro（流式+非流式+错误）进入 ClickHouse 全量归档，并修复 H2（请求截断标志）/M2（thinking_effort 剥离）/L1（request_id 兜底）三处保真缺陷。OpenAI 转发不归档（不碰）。

**Architecture:** Kiro 走独立 `forwardKiroMessages`，不经标准 tee handler。复用既有采集骨架：Kiro 两条路径只负责把 `ForwardResult.CaptureResponse` 等字段填上，汇入既有 Anthropic submit 块（`gateway_handler.go:1026`）统一提交。存 Anthropic 边界（翻译后），头存真实上游头。三处保真修复作用于 Anthropic-native + Kiro 的共享 submit 点与 worker 抽取。

**Tech Stack:** Go、`github.com/tidwall/gjson`、`github.com/google/uuid`（已有）、`clickhouse-go/v2`（已有）。模块 `github.com/Wei-Shaw/sub2api`。

**关联设计:** `docs/superpowers/specs/2026-07-08-capture-translated-paths-design.md`

---

## 文件结构

- `backend/internal/service/capture_record.go` — 加 `SnapshotForCaptureWithFlag`、`CaptureRequestID`；改 `extractCaptureColumns`（effort skip）
- `backend/internal/service/capture_record_test.go` — 上述单测
- `backend/internal/service/kiro_runtime.go` — Kiro 流式取回/非流式快照/错误归档 + 真实上游头透出
- `backend/internal/service/kiro_capture.go`（新建）— Kiro 采集用 gin.Context 头暂存 helper（可单测）
- `backend/internal/service/kiro_capture_test.go`（新建）
- `backend/internal/handler/gateway_handler.go` — submit 块接入 H2/M2/L1
- `backend/internal/service/capture_integration_test.go` — 扩展 round-trip 断言 Kiro 行

**不改：** `openai_gateway_messages.go`、`openai_gateway_handler.go`（OpenAI 转发不归档）。

---

## Task 1: H2 — 请求截断标志的 helper

**Files:**
- Modify: `backend/internal/service/capture_record.go`（`SnapshotForCapture` 定义处 :298 附近）
- Test: `backend/internal/service/capture_record_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `capture_record_test.go`：
```go
func TestSnapshotForCaptureWithFlag(t *testing.T) {
	// 未超限：不截断
	b, tr := SnapshotForCaptureWithFlag([]byte("abc"), 8)
	if string(b) != "abc" || tr {
		t.Fatalf("got %q trunc=%v, want abc false", b, tr)
	}
	// 超限：截断并置标志
	b2, tr2 := SnapshotForCaptureWithFlag([]byte("0123456789"), 4)
	if string(b2) != "0123" || !tr2 {
		t.Fatalf("got %q trunc=%v, want 0123 true", b2, tr2)
	}
	// limit<=0 / nil：不采集
	if b3, tr3 := SnapshotForCaptureWithFlag(nil, 4); b3 != nil || tr3 {
		t.Fatalf("nil in -> nil,false")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestSnapshotForCaptureWithFlag -count=1`
Expected: FAIL（`SnapshotForCaptureWithFlag` undefined）

- [ ] **Step 3: 实现**

在 `capture_record.go` 的 `SnapshotForCapture` 定义（:298）旁追加：
```go
// SnapshotForCaptureWithFlag 与 SnapshotForCapture 相同，但额外返回是否被截断，
// 供调用方把请求截断并入 CaptureRecord.Truncated。
func SnapshotForCaptureWithFlag(src []byte, limit int) ([]byte, bool) {
	return captureWithLimit(src, limit)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run TestSnapshotForCaptureWithFlag -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "feat(capture): SnapshotForCaptureWithFlag exposing request truncation"
```

---

## Task 2: L1 — request_id 兜底 helper

**Files:**
- Modify: `backend/internal/service/capture_record.go`
- Test: `backend/internal/service/capture_record_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestCaptureRequestID(t *testing.T) {
	if got := CaptureRequestID("req_real"); got != "req_real" {
		t.Fatalf("passthrough failed: %q", got)
	}
	if got := CaptureRequestID("   "); got == "" || len(got) < 8 {
		t.Fatalf("empty upstream should fallback to uuid, got %q", got)
	}
	if CaptureRequestID("") == CaptureRequestID("") {
		t.Fatal("two fallbacks must differ")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestCaptureRequestID -count=1`
Expected: FAIL（`CaptureRequestID` undefined）

- [ ] **Step 3: 实现**

在 `capture_record.go` 顶部 import 加 `"github.com/google/uuid"`（若未有）；追加：
```go
// CaptureRequestID 返回上游 request_id；为空时兜底生成 UUID，
// 仅用于归档记录（不影响返回客户端）。满足 PDF「全局唯一 request_id」。
func CaptureRequestID(upstream string) string {
	if s := strings.TrimSpace(upstream); s != "" {
		return s
	}
	return "cap_" + uuid.NewString()
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run TestCaptureRequestID -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "feat(capture): CaptureRequestID fallback uuid when upstream id missing"
```

---

## Task 3: M2 — extractCaptureColumns 尊重预填 effort

**Files:**
- Modify: `backend/internal/service/capture_record.go`（`extractCaptureColumns` :332）
- Test: `backend/internal/service/capture_record_test.go`

背景：Bedrock 在 `ApplyBedrockCCCompat` 里删了 `output_config`，raw_request 抽不到 effort。改为：submit 侧预填 `ThinkingEffort` 后，worker 内**仅当为空**才回退抽取。

- [ ] **Step 1: 写失败测试**

```go
func TestExtractCaptureColumnsKeepsPrefilledEffort(t *testing.T) {
	// raw_request 无 output_config（模拟 Bedrock 剥离），但已预填 effort
	rec := &CaptureRecord{
		RawRequest:     []byte(`{"model":"claude","messages":[]}`),
		ThinkingEffort: "high",
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "high" {
		t.Fatalf("prefilled effort overwritten: %q", rec.ThinkingEffort)
	}
}

func TestExtractCaptureColumnsFallsBackToRawEffort(t *testing.T) {
	rec := &CaptureRecord{
		RawRequest: []byte(`{"output_config":{"effort":"xhigh"}}`),
	}
	extractCaptureColumns(rec)
	if rec.ThinkingEffort != "xhigh" {
		t.Fatalf("raw fallback failed: %q", rec.ThinkingEffort)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestExtractCaptureColumns -count=1`
Expected: FAIL（第一个用例：当前无条件覆盖，effort 被清成空/raw 值）

- [ ] **Step 3: 实现**

`extractCaptureColumns`（:335-338）当前：
```go
	rec.ThinkingEffort = ""
	if effort := NormalizeClaudeOutputEffort(gjson.GetBytes(rec.RawRequest, "output_config.effort").String()); effort != nil {
		rec.ThinkingEffort = *effort
	}
```
改为：
```go
	// 仅当 submit 侧未预填时，才从 raw_request 回退抽取。
	// （Bedrock/Kiro 等 body 里 output_config 可能已被剥离/翻译，故 submit 侧优先用 ParsedRequest.OutputEffort 预填。）
	if rec.ThinkingEffort == "" {
		if effort := NormalizeClaudeOutputEffort(gjson.GetBytes(rec.RawRequest, "output_config.effort").String()); effort != nil {
			rec.ThinkingEffort = *effort
		}
	}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run TestExtractCaptureColumns -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "fix(capture): keep prefilled thinking_effort, raw fallback only when empty"
```

---

## Task 4: 接入 H2/M2/L1 到共享 Anthropic submit 块

**Files:**
- Modify: `backend/internal/handler/gateway_handler.go:1026-1042`

此块被 Anthropic-native **与 Kiro**（Task 5/6 让 Kiro 填 `CaptureResponse`）共用。改三处：H2 请求截断并集、M2 从 `attemptParsedReq.OutputEffort` 预填、L1 request_id 兜底。

- [ ] **Step 1: 替换 submit 块**

把 `gateway_handler.go:1026-1042` 整块替换为：
```go
			if h.capturePool != nil && result.CaptureResponse != nil {
				rawReq, reqTrunc := service.SnapshotForCaptureWithFlag(attemptParsedReq.Body.Bytes(), h.captureLimit())
				effort := ""
				if e := service.NormalizeClaudeOutputEffort(attemptParsedReq.OutputEffort); e != nil {
					effort = *e
				}
				h.capturePool.Submit(&service.CaptureRecord{
					CapturedAt:       time.Now().UTC(),
					Platform:         string(account.Platform),
					RequestID:        service.CaptureRequestID(result.RequestID),
					RequestedModel:   result.Model,
					UpstreamModel:    result.UpstreamModel,
					UpstreamEndpoint: upstreamEndpoint,
					Stream:           result.Stream,
					HTTPStatus:       200,
					ThinkingEffort:   effort,
					RawRequest:       rawReq,
					RawResponse:      result.CaptureResponse,
					RequestHeaders:   result.CaptureRequestHeaders,
					ResponseHeaders:  result.CaptureResponseHeaders,
					Truncated:        result.CaptureTruncated || reqTrunc,
				})
			}
```

- [ ] **Step 2: 编译**

Run: `cd backend && go build ./internal/handler/...`
Expected: 无错误。

- [ ] **Step 3: 回归既有测试**

Run: `cd backend && go test ./internal/service/ -run 'Capture' -count=1`
Expected: PASS（既有采集测试不回归）

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handler/gateway_handler.go
git commit -m "fix(capture): request-trunc union + prefill effort + request_id fallback in submit"
```

---
