# 需求背景：上游模型调用全量归档

本目录保存"上游模型调用全量归档（ClickHouse）"项目的**需求背景**与**下游需求原始材料**，与设计文档（`../specs/2026-07-07-model-call-archive-design.md`）、实现计划（`../plans/2026-07-07-model-call-archive.md`）配套。

## 1. 项目目标

在网关转发完成后，把**每一次上游模型调用**的**原始 request + response（逐字）**异步写入一个**远端、独立**的 ClickHouse 数据库，形成一个**厂商无关、无损的全量归档库**，供开放式二次开发使用。

核心约束：
- **绝不影响转发**：与转发/计费完全隔离的第三条异步通道；任何归档侧失败都不外溢到转发。
- **默认关闭、关时零成本**：总开关 `gateway.capture.enabled`，默认 `false`。
- **只存上游相关**：只保存打到厂商的真实请求与厂商返回的真实响应，不掺客户端/身份字段。
- **忠实归档**：采集侧只做逐字保存 + 最小抽取列；归一化/过滤/转换全部放到读库后的离线二次开发。

## 2. 下游需求（本目录材料）

归档库是**开放式二次开发底座**，下面是**已知需求之一**，但库的 schema 不被它裁剪——要保存最原始的各厂家、各模型完整信息，任何未预抽字段都能从原文兜底。

### 2.1 `opus48-trajectory-acceptance-spec.pdf`

Opus 4.8 用户日志采购/验收标准（下游消费者之一）。要点：
- 交付格式为**原始 Anthropic Messages API** 的 `request` / `response`（**非** OpenAI ChatMessage），逐条 JSONL，一条 = 一次 LLM 调用。
- 保留 `thinking` 块的 `signature`、`tool_use` / `tool_result` / `redacted_thinking` 等 block 结构逐字不变；signature 覆盖率 > 65%。
- 记录级字段：`session_id`（同一客户端+用户+同一上下文对话）、`request_id`（全局唯一）、`timestamp`（ISO8601 UTC）、`thinking_effort`（客户端实际选择档位，**非** budget_tokens 反推）、`is_garbled`。
- 过滤：剔除 cron/heartbeat/no_reply、批量/合成调用，仅保留真实人类发起的 agent 轨迹。
- 质量维度、cron 检测、claude-trace 格式转换等均为**读库后离线处理**步骤。

对应到本库：session_id 来自上游 body 的 `metadata.user_id`；request/response 原文逐字入 `raw_request`/`raw_response`；SSE→完整 body 组装、cron 过滤、质量标注全部离线做。详见设计文档 §7 字段覆盖映射。

### 2.2 `sdk-example-05_filter_cron_enhanced.py`

SDK 二次开发脚本样例：多进程读 GB 级 JSONL，正则过滤 cron/heartbeat（`[cron:`、`HEART_BEAT`、`NO_REPLY` 等），按 user 轮次比例阈值决定保留/剔除。说明二次开发是**文件批处理式**的——ClickHouse 可 `INTO OUTFILE ... FORMAT JSONEachRow` 秒级导出 JSONL 直接喂此类脚本。

## 3. 未来需求可能（非当期，供 schema 前瞻）

- **多厂商多模型**：Anthropic/OpenAI/Gemini/Kiro/Grok/Antigravity/Bedrock/Vertex 等，原文按协议原样存、`platform` 打标，离线按需归一化。
- 其它训练数据采购口径（不同模型族、不同 block 密度/质量要求）。
- 检索/审计/分析类需求（模型分布、signature 覆盖率、错误响应分析等）——错误响应亦原样归档。
- 大 payload（图片/超长上下文）→ 后续可选原文落对象存储、信封留 ClickHouse（设计文档 §13）。

## 4. 关联文档

- 设计：`../specs/2026-07-07-model-call-archive-design.md`
- 实现计划：`../plans/2026-07-07-model-call-archive.md`
