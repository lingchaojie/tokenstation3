# WebChat OpenAI Responses 与附件输入设计

- 日期：2026-07-16
- 状态：已确认，待实施计划
- 范围：GPT/OpenAI WebChat 请求、默认按需联网搜索、图片与文件附件输入

## 背景

当前 WebChat 根据联网搜索配置在两个 OpenAI 接口之间切换：普通消息走
`/v1/chat/completions`，配置联网搜索的消息走 `/v1/responses`。这造成三类问题：

1. 动态模型目录把 GPT 文本模型的图片能力误判为不支持，前端在发送前拦截图片附件。
2. PDF、DOCX 等二进制文件虽然上传并保存成功，但适配器只把非空 `text_preview`
   拼入提示词；没有预览的文件会被静默丢弃。
3. 同一会话根据搜索开关切换协议，使附件、工具调用、流式事件与使用量记录需要维护
   两套分支。

OpenAI 将 Responses 作为文字、多模态与内置工具的统一接口。它支持
`input_text`、`input_image`、`input_file` 与 `web_search`，因此本次将 GPT/OpenAI
WebChat 收敛到单一 Responses 请求链。

## 目标

- GPT/OpenAI WebChat 的所有消息统一发送到 `/v1/responses`。
- 联网搜索对支持它的 GPT 文本模型默认开启，但使用 `tool_choice: "auto"` 按需调用。
- 用户关闭联网搜索后，请求中完全不包含 `web_search` 工具。
- 图片作为 `input_image`，文件作为原生 `input_file` 发送，不再静默丢弃。
- 保持现有会话持久化、流式展示、使用量记录和计费语义。
- 文件读取、类型、大小或编码失败时，在调用上游前返回明确错误。

## 非目标

- 不修改 Claude、Anthropic、Kiro、Gemini 或其他厂商的附件转发行为。
- 不修复 PDF URL 导入、网页抓取或联网搜索读取远程 PDF 的问题。
- 不建设站内 OCR、RAG、向量库或 OpenAI Files API 文件生命周期管理。
- 不改变正式服配置或部署；正式服变更仍需单独确认。
- 不把供应商会话 ID 作为平台会话状态来源。

## 方案比较与决策

### 方案 A：全量 Responses + 按需搜索（采用）

所有 GPT/OpenAI WebChat 消息走 Responses。搜索开启时添加 `web_search`，并使用
`tool_choice: "auto"`。该方案统一协议，同时避免普通问候、改写和本地附件分析被迫
产生搜索调用。

### 方案 B：按内容选择 Chat Completions 或 Responses

普通文字和图片继续走 Chat Completions，文件或联网消息走 Responses。改动较小，但
继续保留两套请求与响应路径，后续功能仍需重复适配。

### 方案 C：全量 Responses + 强制搜索

所有消息走 Responses，并指定 `tool_choice` 为 `web_search`。实现直接，但每一轮都会
搜索，增加延迟、工具费用和不相关网页内容干扰。

最终采用方案 A。

## 总体架构

WebChat 继续保存供应商无关的会话与附件记录。发送 GPT/OpenAI 消息时：

1. WebChat 服务从数据库加载完整会话历史和附件元数据。
2. 服务端重新验证目标模型能力及每个附件的所有权、类型与大小。
3. OpenAI WebChat 适配器直接构造 Responses 请求。
4. 请求统一经现有 OpenAI Responses 转发路径选择账号、应用代理、调用上游。
5. 现有流式捕获与转换逻辑处理 Responses 事件，并持久化助手消息和产物。
6. 使用量继续由现有 OpenAI usage 记录逻辑落库，`upstream_endpoint` 固定记录
   `/v1/responses`。

Anthropic/Kiro 等非 OpenAI 路径保持现状。

## 请求路由

对 `provider == "openai"` 的 WebChat 模型，分发层无条件选择 Responses：

- 纯文字：Responses。
- 文字加图片：Responses。
- 文字加 PDF/DOCX/TXT：Responses。
- 只有附件：Responses。
- 开启或关闭联网搜索：均为 Responses；区别仅在是否携带 `web_search`。
- 图片生成模型：仍走 Responses，但仅使用其已声明支持的工具，不强行添加
  `web_search`。

不再根据 `WebSearch.Configured` 或附件种类决定 OpenAI WebChat 的接口。现有
Chat Completions 转发仍保留给公开 API 兼容入口和其他内部调用，不在本次删除。

## Responses 输入映射

### 文字

用户文字转换为 `input_text`。系统和助手历史保留相应角色，并继续从平台持久化记录
重建上下文。

### 图片

PNG、JPEG、WebP 和 GIF 从 WebChat 存储读取，按现有 20 MiB 单附件上限校验，转换为
Base64 data URL，并作为 `input_image.image_url` 发送。不能读取、超限或内容类型与
附件元数据不一致时，整次发送失败。

### 文件

PDF、DOCX、TXT、Markdown、JSON 和 CSV 从 WebChat 存储读取并作为
`input_file` 发送：

- `filename` 使用清洗后的原始文件名；缺失时使用稳定的通用文件名。
- `file_data` 使用带 MIME 类型的 Base64 data URL。
- PDF 设置 `detail: "auto"`，由 OpenAI 选择页面图像细节等级。
- 非 PDF 文件不发送 `detail`。
- 每个文件继续受现有 20 MiB 单附件上限约束。
- 同一 Responses 请求中所有 `input_file` 的原始文件总量不得超过 OpenAI 的
  50 MiB 请求级上限；超限时在调用上游前拒绝。图片仍按现有 WebChat 附件限制处理。

文本文件不再只依赖 `text_preview`。`text_preview` 可继续用于 UI 或兼容路径，但
Responses 请求以原文件为准，避免预览截断和重复拼接。

### 混合附件

同一条用户消息可以同时包含 `input_text`、多个 `input_image` 和多个 `input_file`。
适配器保持用户选择的附件顺序；如果现有持久化结果不能稳定表示跨种类顺序，则按
附件记录 ID 升序生成，禁止使用 map 迭代顺序。

## 联网搜索语义

### 默认值

前端对支持 `web_search` 的 GPT 文本模型将联网开关初始化为开启。用户可在发送前
关闭。切换到不支持搜索的模型时开关关闭；切回支持搜索的模型时恢复默认开启。

### 开启

请求添加：

```json
{
  "tools": [{ "type": "web_search" }],
  "tool_choice": "auto"
}
```

这表示搜索工具可用，但模型可以直接回答而不调用它。不能把开启状态转换为指定
`web_search` 的强制 `tool_choice`。

### 关闭

请求中不包含 `web_search` 工具。仅把 `tool_choice` 设为 `auto` 但仍保留工具不算
关闭，因为模型仍可能搜索。

图像生成等不支持联网搜索的模型不显示可用搜索状态，也不添加该工具。

## 模型能力

动态目录按 GPT 模型族补齐输入能力，而不是仅相信公共市场目录的展示模态：

- GPT 文本/推理模型声明 `supports_image_input = true`。
- GPT 文本/推理模型声明 `supports_file_context = true`。
- 图片生成专用模型继续使用自己的能力定义，不能因为 `gpt-` 前缀错误继承搜索或
  思考能力。
- 服务端能力验证仍是最终边界；前端能力标志仅用于交互提示。

该规则只应用于 OpenAI/GPT WebChat 条目，不扩大到名称相似的其他 provider。

## 会话状态与数据保留

Responses 请求继续设置 `store: false`。平台每轮发送自己持久化的必要会话历史，
不使用 `previous_response_id`，因此：

- 不依赖上游保存对话状态。
- 不改变现有跨模型切换语义。
- 不新增供应商响应 ID 的持久化和清理工作。

加密 reasoning 内容继续沿用现有 Responses 转换与转发逻辑，不在本次重新设计。

## 上游账号类型兼容性

正式服 OpenAI 组可能同时包含 API Key 账号和 ChatGPT/Codex OAuth 账号。API Key 账号
通常发送到 OpenAI API Responses 端点；OAuth 账号发送到 ChatGPT Codex Responses
端点。两者不能只凭相同的 `/responses` 路径就假定支持完全相同的 `input_file` 字段。

实施和上线必须增加账号类型兼容性验证：

- API Key Responses 请求按 OpenAI 官方 `input_file` 结构验证。
- OAuth Responses 请求必须通过项目现有 WebChat/OpenAI 转发流程，并使用该账号在
  正式配置中的 IP 代理验证；不得绕过服务直接裸调上游。
- 该验证会产生真实上游请求和用量，执行前另行获得用户确认。
- 如果 OAuth 上游拒绝 Base64 `input_file`，实施应停止并报告阻塞。不得静默退回文件名、
  `text_preview` 或直接丢弃文件，也不得未经新设计引入自建 PDF 解析/OCR 降级。

因此，“OAuth GPT 可以读取 PDF”是发布门槛，而不是仅靠单元测试即可证明的假设。

## 错误处理

以下错误必须在上游调用前失败，并保留可识别的 WebChat 错误类型：

- 附件不属于当前用户或会话。
- 存储对象不存在或读取失败。
- 实际读取大小超过本地限制。
- MIME 类型不在 WebChat 允许列表，或实际内容与声明种类不符。
- Base64 或 JSON 请求构造失败。
- 目标模型不支持对应输入种类。

不得在这些情况下删除附件部分后继续发送纯文字。上游 Responses 错误沿用现有错误
透传和消息失败状态处理。

## 流式响应、产物与计费

- 继续使用现有 Responses SSE 转发和 WebChat 流式捕获。
- `web_search_call`、消息文本、reasoning 和图片生成事件继续进入现有前端解析流程。
- 搜索是否实际发生以返回的 `web_search_call` 为准；开关开启不等于本轮一定搜索。
- OpenAI WebChat usage 的 `upstream_endpoint` 统一为 `/v1/responses`。
- PDF 文本、页面图像和实际搜索工具调用产生的 token/工具费用由上游 usage 按现有
  计费路径记录，不新增独立账本。

## 前端行为

- GPT 搜索开关初始为开启，并保留用户手动关闭能力。
- 文案表达“允许模型按需联网”，不能承诺每轮一定联网。
- GPT 图片和文件附件不再因为错误能力标志阻止发送。
- 上传成功只代表附件已保存；发送阶段的读取或上游错误仍在对应消息上展示。
- 其他模型的现有能力警告保持不变。

## 测试设计

### 后端能力测试

- 动态 GPT 条目补齐图片和文件能力。
- 图片生成专用模型不继承搜索能力。
- 非 OpenAI provider 不受 GPT 能力补齐影响。

### 请求适配测试

- 纯文字生成合法 Responses `input_text`。
- 图片生成 `input_image` data URL。
- PDF 生成带 `filename`、`file_data` 和 `detail: "auto"` 的 `input_file`。
- DOCX/TXT 生成不带 `detail` 的 `input_file`。
- 文字、图片和文件可以在同一用户消息中组合。
- 超限、缺失和类型不一致的附件明确失败，且不产生降级请求。

### 搜索语义测试

- 支持搜索的 GPT 模型前端默认开启搜索。
- 开启时请求含 `web_search` 且 `tool_choice == "auto"`。
- 关闭时请求不含 `web_search`。
- 不支持搜索的模型不含 `web_search`。

### 分发与回归测试

- OpenAI 纯文字、图片、文件以及混合消息全部调用 Responses 转发方法。
- OpenAI WebChat 不再调用 Chat Completions 转发方法。
- API Key 与 OAuth 两类 OpenAI 上游的 Responses 兼容性分别验证；OAuth 验证必须走
  现有服务链和正式配置代理。
- Responses 流式文本、工具事件、产物提取和使用量记录保持正常。
- `upstream_endpoint` 记录为 `/v1/responses`。
- Anthropic/Kiro 现有测试保持不变。

## 验收标准

1. GPT WebChat 上传图片后可以发送并得到基于图片内容的回答。
2. GPT WebChat 上传普通 PDF 和扫描 PDF 后，模型能够引用文件实际内容；请求中不存在
   仅显示文件名却没有文件数据的情况。
3. GPT WebChat 的纯文字、图片、文件和混合输入全部经过 `/v1/responses`。
4. 支持搜索的 GPT 模型首次进入会话时联网开关开启，但无联网必要的提示可以不产生
   `web_search_call`。
5. 用户关闭联网后，该轮请求不可能调用托管 Web Search。
6. 附件异常不会被静默忽略，用户能看到明确失败信息。
7. 现有流式展示、使用量、计费和会话历史测试全部通过。
8. 不包含任何正式服环境或配置变更。
9. 正式服使用的 OpenAI OAuth 上游已在获得用户确认后，通过真实服务链验证支持
   `input_file`；若不支持，任务明确报告阻塞而不是宣称 PDF 已修复。

## 实施边界

本设计批准后，实施计划应把修改拆成能力目录、Responses 输入适配、OpenAI 分发、
搜索默认值/开关语义和回归测试五组小步骤。每组先写失败测试，再实现最小改动。
完成本地验证后，如需发布到正式服，必须另行获得用户确认。
