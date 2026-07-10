# 翻译型上游路径归档接入 + 保真修复 设计文档

- 日期：2026-07-08
- 状态：设计待评审
- 关联设计：`2026-07-07-model-call-archive-design.md`（本文件是其增量，骨架沿用）
- 关联需求：`../requirements/opus48-trajectory-acceptance-spec.pdf`

## 1. 背景

`feat/model-call-archive`（已并入 dev）落地了「上游调用全量归档 → ClickHouse」的第三条异步通道。Review 发现 **Kiro 走独立转发实现、未接入采集**，另有三处保真缺陷：

- **Kiro**：Kiro 走独立的 `forwardKiroMessages`，不经过装 tee 的标准 handler。流式虽复用 `handleStreamingResponse`（tee 会跑）但从不 `takeCaptureResult` → 结果被丢弃；非流式根本不走 `handleNonStreamingResponse` → **两种情况都零归档**。
- **H2**：`SnapshotForCapture` 丢弃请求体截断标志，`Truncated` 只反映响应截断 → 超限请求被静默腰斩却标为完整，破坏离线可解析性。
- **M2**：Bedrock 在 `ApplyBedrockCCCompat` 里 `sjson.DeleteBytes(body,"output_config")`，归档快照在其后取，`thinking_effort` 落库为空。
- **L1**：`request_id` 仅取上游 `x-request-id`，缺失时为空，违反 PDF「全局唯一」并削弱 ORDER BY 选择性。

### 1.1 明确不做（本轮范围决策）
- **OpenAI 转发不归档**（需求方决策）。因此原 Review 提出的 **H1**（补 OpenAI-messages→Anthropic 路径归档）与 **M1**（OpenAI 存 model-map 前 body）**均不做**。
- **OpenAI 原生 Responses 透传归档保持现状不动**——它当前已在记录，本轮不碰任何 OpenAI 相关代码。
  - 遗留说明：该路径 `submitCapture` 传的是 model-map 前 `body`（M1 在此路径同样存在），`raw_request.model` 与真正发上游不一致，可从 `upstream_model` 列兜底。需求方选择保持不动，故不修。
- **L2 按字节限流**切独立 PR，不在本轮。

## 2. 目标与非目标

### 2.1 目标
- 让 **Kiro（流式+非流式）** 路径进入归档，与既有路径一致的隔离性 / drop 安全 / 关时零成本。
- 修复 H2 / M2 / L1 三处保真缺陷（作用于 Anthropic-native 与 Kiro 的 submit 点）。
- Kiro 的**终态错误响应**也归档（与 Anthropic 路径 `buildErrorCaptureRecord` 对齐）。

### 2.2 非目标
- **OpenAI 转发不归档**：不补 OpenAI-messages 路径（H1/M1 drop）；不碰已在运行的 OpenAI Responses 透传归档。
- **L2 按字节限流**（`gateway.capture.max_queue_bytes`）：独立性高，切独立 PR，不在本轮。
- 不改 DTO / pool / writer / ClickHouse schema 主体（仅 L1 影响 `request_id` 取值来源，不加列）。
- 不做离线侧的会话重建 / 格式转换 / 质量评分（仍属离线二次开发）。

## 3. 关键决策（已与需求方确认）

| 决策项 | 结论 |
|---|---|
| Kiro `raw_request`/`raw_response` 存什么 | **Anthropic 边界**：`raw_request` = 客户端 Anthropic body（model-map 后）；`raw_response` = 翻译后的 Anthropic SSE（流式）/ 组装好的 Anthropic JSON（非流式）。理由：Kiro 跑的是 Claude 模型，PDF 要 Anthropic 原生格式；流式可复用现成 tee，近零成本；`thinking_effort` 因 body 未被厂商变体剥离而有值。**代价**：存的是网关翻译产物而非厂商真实 wire（AWS EventStream），与 Anthropic-direct 的 raw 语义存在细微差异——用 `platform` 列区分，离线消费按需筛选。 |
| Kiro 流式响应头 | 存**真实上游头** `resp.Header`（脱敏后），与「只存上游相关」一致；不存网关合成的 `wrappedHeaders`（后者含我们自造的 x-request-id）。 |
| `thinking_effort` 来源 | 从 `ParsedRequest.OutputEffort`（解析期在 rewrite 前从原始 body 抽取）注入，而非事后从可能被剥离的 `raw_request` 抽。全平台通用。 |
| `request_id` 兜底 | 上游 `x-request-id` 为空时 capture 侧采 UUID；仅写入归档记录，不影响返回客户端。 |
| 错误归档 | Kiro 终态错误也归档，`http_status` 记真实码；failover 中间重试不归档。 |

## 4. 架构与数据流

不新增通道。Kiro 两条路径（流式 / 非流式）**只负责把 `ForwardResult.CaptureResponse` 等字段填上**，之后统一汇入既有 Anthropic submit 块 `gateway_handler.go:1026`（读 `attemptParsedReq.Body.Bytes()` 作 `raw_request`）。

未填 `CaptureResponse` 则照旧跳过——天然向后兼容，`capture.enabled=false` 时仍零成本（tee 不装、bridge 不写、take 守卫在 `Capture.Enabled` 内）。

## 5. 分路径实现

### 5.1 ① Kiro 流式（`kiro_runtime.go:159`）
tee 已在 `handleStreamingResponse` 内运行并 `setCaptureResult` 到 gin.Context 桥；缺的是取回。在 streaming 分支构造 `ForwardResult` 前（`:167` 附近）：
```go
if s.cfg != nil && s.cfg.Gateway.Capture.Enabled {
    if bridge, ok := takeCaptureResult(c); ok && bridge.Response != nil {
        result.CaptureResponse = bridge.Response
        result.CaptureTruncated = bridge.Truncated
        result.CaptureRequestHeaders = bridge.RequestHeaders   // 真实上游头（setCaptureResult 已从 resp.Request.Header 脱敏）
        result.CaptureResponseHeaders = bridge.ResponseHeaders  // 真实上游头 resp.Header 脱敏
    }
}
```
`setCaptureResult` 收到的 `resp` 是 `openKiroAnthropicStreamResponse` 返回的 pipe 响应，其 `resp.Header` 是 `wrappedHeaders`。为满足「存真实上游头」的决策，需让流式 tee 拿到**真实上游 resp**——在 `openKiroAnthropicStreamResponse` 里把真实上游 `resp.Header`（`executeKiroUpstreamWithParsed` 返回的那个）通过 gin.Context 或 pipe 响应的 `Request` 字段透出，供 `setCaptureResult` 脱敏。具体在实现计划里定，原则：**头取真实上游、不取 wrappedHeaders**。

### 5.2 ② Kiro 非流式（`kiro_runtime.go:266`）
`parseResult.ResponseBody` 是组装好的 Anthropic JSON。`:287` 处目前直接 `return &ForwardResult{...}`；改为先构造 `result`，capture 开启时填充采集字段，再返回：
```go
result := &ForwardResult{ /* 现有字段 */ }
if s.cfg != nil && s.cfg.Gateway.Capture.Enabled {
    captured, truncated := captureWithLimit(parseResult.ResponseBody, s.cfg.Gateway.Capture.MaxBodyBytes)
    result.CaptureResponse = captured
    result.CaptureTruncated = truncated
    result.CaptureResponseHeaders = redactHTTPHeader(resp.Header) // 真实上游头
}
return result, nil
```
（`resp` 是 `executeKiroUpstreamWithParsed` 的真实上游响应，此处直接可得。）

### 5.3 Web Search 模拟路径（Kiro）
`handleWebSearchEmulation` / web search 结果分支（`kiro_runtime.go:117`、`:196`）构造的是合成 Anthropic 响应。这些**属于网关自造响应而非上游模型调用**，与「只存上游相关」原则不符，**不归档**。实现时确保这些分支不误触发采集。

## 6. 保真修复

### 6.1 H2 — 请求截断标志并入
`SnapshotForCapture` 保留截断信息。新增便于调用方拿到并集的形态：
```go
// 返回副本 + 是否截断
func SnapshotForCaptureWithFlag(src []byte, limit int) ([]byte, bool) { return captureWithLimit(src, limit) }
```
Anthropic-native submit 点（`gateway_handler.go:1036`）改为：
```go
rawReq, reqTrunc := service.SnapshotForCaptureWithFlag(attemptParsedReq.Body.Bytes(), h.captureLimit())
// ...
Truncated: result.CaptureTruncated || reqTrunc,
```
Kiro 经同一 submit 块受益。保留原 `SnapshotForCapture` 为薄封装避免波及其它调用方。**不改** OpenAI `submitCapture`（`:177`）。

### 6.2 M2 — thinking_effort 从 ParsedRequest 注入
`CaptureRecord` 已有 `ThinkingEffort` 字段。在 Anthropic-native submit 点用 `attemptParsedReq.OutputEffort` 经现有 `NormalizeClaudeOutputEffort(raw string) *string`（`gateway_request.go:1222`，nil 取 `""`）规整后预填 `ThinkingEffort`。
`extractCaptureColumns`（worker 内）改为**仅当 `rec.ThinkingEffort == ""` 时**才从 `raw_request` 抽 `output_config.effort`。这样 Bedrock（body 已剥离）也能拿到客户端真实档位，其它路径行为不变。作用范围：Anthropic-native 与 Kiro submit 点。

### 6.3 L1 — request_id 兜底
在 submit 构造 `CaptureRecord` 时，若 `RequestID == ""` 则采 UUID（复用 `kiropkg.NewClaudeRequestID()` 或通用 uuid），仅写归档记录。集中到一个 helper：
```go
func captureRequestID(upstream string) string {
    if strings.TrimSpace(upstream) != "" { return upstream }
    return newCaptureFallbackID() // uuid
}
```

## 7. 错误归档（Kiro）

- Kiro：`handleKiroHTTPError`（`kiro_runtime.go:815`）终态处，`buildErrorCaptureRecord` + `s.capturePool.Submit`。`GatewayService.capturePool` 已存在（`gateway_service.go:670`），且已有错误归档先例（`gateway_service.go:8355`）。
- 遵守：`http_status` = 真实码；failover 中间重试在 `UpstreamFailoverError` 分支已提前返回，不会走到归档；`Submit` 非阻塞 + recover，绝不影响转发。
- OpenAI-messages 的错误归档不做（OpenAI 转发整体不归档）。

## 8. 测试

- 单测：Kiro 流式取回、Kiro 非流式快照、请求截断并集、effort 从 parsed 注入（含 Bedrock 剥离场景）、request_id uuid 兜底、Kiro 错误归档。
- 集成：扩展 `capture_integration_test.go` round-trip，断言 Kiro 行的 `platform`、`session_id`、`thinking_effort`、`signature_present`、`http_status`。
- 回归：`capture.enabled=false` 零成本断言仍成立（`TestCaptureDisabledZeroCost`）；OpenAI 路径归档行为保持现状（不新增、不删除）。

## 9. 影响文件

| 文件 | 改动 |
|---|---|
| `kiro_runtime.go` | ①② 取回/快照 + 真实上游头透出；`handleKiroHTTPError` 错误归档 |
| `gateway_handler.go` | H2 并集；M2 effort 注入；L1 兜底（Anthropic-native submit 点） |
| `capture_record.go` | H2 helper；M2 skip 逻辑；L1 helper |
| 各 `_test.go` | 上述用例 |

不改：`openai_gateway_messages.go`、`openai_gateway_handler.go`（OpenAI 转发不归档）。

## 10. 风险

- **头透出改动**：为「存真实上游头」需把真实上游 `resp.Header` 带到 Kiro 流式 tee 的 `setCaptureResult`；Kiro 流式经 pipe 包装（`openKiroAnthropicStreamResponse` 返回合成头响应），需谨慎不破坏现有转发。实现计划里单列步骤 + 测试兜底。
- **语义差异**：Kiro 存的是 Anthropic 边界产物（非 AWS EventStream 真实 wire），离线消费需靠 `platform` 区分。文档 §3 已声明。
- **遗留 M1**：OpenAI Responses 透传路径仍存 model-map 前 body 的不一致（需求方选择保持不动），`upstream_model` 列可兜底。
