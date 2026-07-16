# WebChat OpenAI Responses 与附件输入设计

- 日期：2026-07-16
- 状态：修订设计已确认，待修订实施计划
- 范围：GPT/OpenAI WebChat 请求、固定提供按需联网搜索（无前端开关）、图片与 OpenAI 官方文件输入、统一附件上传入口

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
- 对支持联网的 GPT 文本模型固定提供 `web_search`，并使用
  `tool_choice: "auto"` 按需调用。
- 前端不显示 GPT 联网搜索按钮，也不展示联网搜索说明文案。
- 图片作为 `input_image`，文件作为原生 `input_file` 发送，不再静默丢弃。
- 放开 OpenAI 官方支持且可由本地浏览器上传的文档、演示、表格、文本与代码格式；
  未知二进制仍拒绝。
- 前端把上传图片和上传文件合并为一个附件入口，并沿用现有上传图片的图标。
- 保持现有会话持久化、流式展示、使用量记录和计费语义。
- 文件读取、类型、大小或编码失败时，在调用上游前返回明确错误。

## 非目标

- 不修改 Claude、Anthropic、Kiro、Gemini 或其他厂商的附件转发行为。
- 不修复 PDF URL 导入、网页抓取或联网搜索读取远程 PDF 的问题。
- 不建设站内 OCR、RAG、向量库或 OpenAI Files API 文件生命周期管理。
- 不允许任意文件、压缩包、可执行文件、音频或视频作为 `input_file`；OpenAI 官方未列出的
  MIME/扩展名仍拒绝。
- 不解析 Google Docs/Sheets/Slides 本地占位文件；用户须先导出为 DOCX、PPTX、XLSX
  或 PDF。
- 不改变正式服配置或部署；正式服变更仍需单独确认。
- 不把供应商会话 ID 作为平台会话状态来源。

## 方案比较与决策

### 方案 A：全量 Responses + 按需搜索（采用）

所有 GPT/OpenAI WebChat 消息走 Responses。对支持搜索的模型固定添加
`web_search`，并使用 `tool_choice: "auto"`。该方案统一协议，同时避免普通问候、
改写和本地附件分析被迫产生搜索调用。

### 方案 B：按内容选择 Chat Completions 或 Responses

普通文字和图片继续走 Chat Completions，文件或联网消息走 Responses。改动较小，但
继续保留两套请求与响应路径，后续功能仍需重复适配。

### 方案 C：全量 Responses + 强制搜索

所有消息走 Responses，并指定 `tool_choice` 为 `web_search`。实现直接，但每一轮都会
搜索，增加延迟、工具费用和不相关网页内容干扰。

最终采用方案 A。

### 文件放行方案

采用“OpenAI 官方白名单 + 真实字节校验”，不采用只检查扩展名/MIME，也不把任意文件
先存储后交给上游拒绝。后端维护集中式文件类型注册表；每个条目至少包含扩展名、规范
MIME、文件类别、字节校验策略，以及是否允许旧的非 OpenAI 附件路径。

上传接口当前不绑定 provider/model，因此上传阶段允许保存 OpenAI 官方支持格式；真正
发送时再按 provider 隔离：OpenAI 使用完整注册表，非 OpenAI 只允许本次变更前已有的
图片和文件范围。非 OpenAI 遇到 OpenAI 专用附件时明确失败，不能静默省略。

## 总体架构

WebChat 继续保存供应商无关的会话与附件记录。发送 GPT/OpenAI 消息时：

1. WebChat 服务从数据库加载完整会话历史和附件元数据。
2. 服务端根据集中式注册表重新验证目标模型能力及每个附件的所有权、文件名、类型、
   真实字节与大小。
3. OpenAI WebChat 适配器直接构造 Responses 请求。
4. 请求统一经现有 OpenAI Responses 转发路径选择账号、应用代理、调用上游。
5. 现有流式捕获与转换逻辑处理 Responses 事件，并持久化助手消息和产物。
6. 使用量继续由现有 OpenAI usage 记录逻辑落库，`upstream_endpoint` 固定记录
   `/v1/responses`。

Anthropic/Kiro 等非 OpenAI 转发内容保持现状；新增格式不会被发送到这些路径。

## 请求路由

对 `provider == "openai"` 的 WebChat 模型，分发层无条件选择 Responses：

- 纯文字：Responses。
- 文字加图片：Responses。
- 文字加 PDF、Office、演示、表格、文本或代码文件：Responses。
- 只有附件：Responses。
- 支持联网搜索的 GPT 文本模型：Responses 固定携带 `web_search`，由模型按需调用。
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

OpenAI 文件输入范围以官方 File inputs 清单为权威，并由本地注册表映射为可上传扩展名：

- PDF：`.pdf`。
- Word/富文档：`.doc`、`.docx`、`.dot`、`.odt`、`.rtf`、Pages (`.pages`)。
- PowerPoint/演示：`.pot`、`.ppa`、`.pps`、`.ppt`、`.pptx`、`.pwz`、`.wiz`、
  Keynote (`.key`)。
- Excel/表格：`.xla`、`.xlb`、`.xlc`、`.xlm`、`.xls`、`.xlsx`、`.xlt`、`.xlw`、
  `.csv`、`.tsv`、`.iif`。
- 文本与代码：OpenAI 官方 MIME 清单对应的纯文本格式，包括 TXT、Markdown、JSON、
  XML、HTML、YAML、TOML、JavaScript/TypeScript、Python、Java、Go、Rust、C/C++、
  C#、Shell、SQL、PHP、Ruby、Swift、Kotlin、Scala、Lua、R、Julia、Perl、Objective-C、
  Erlang、Elixir、Haskell、Clojure、Groovy、Dart、Diff/Patch、GraphQL、Proto、Terraform
  等常见扩展名。只有注册表显式映射到官方 MIME 的扩展名才允许；不能仅因内容看起来像
  文本就接受未知扩展名。

Google Docs/Sheets/Slides 的云端 MIME 没有可验证的本地文件容器，不纳入 multipart
上传；用户需先导出为上述格式。

所有支持文件从 WebChat 存储重新读取并作为 `input_file` 发送：

- `filename` 使用清洗后的原始文件名；缺失时使用稳定的通用文件名。
- `file_data` 使用带 MIME 类型的 Base64 data URL。
- PDF 设置 `detail: "auto"`，由 OpenAI 选择页面图像细节等级。
- 非 PDF 文件不发送 `detail`。
- 每个文件继续受现有 20 MiB 单附件上限约束。
- 同一 Responses 请求中所有 `input_file` 的原始文件总量不得超过 OpenAI 的
  50 MiB 请求级上限；超限时在调用上游前拒绝。图片仍按现有 WebChat 附件限制处理。

文本文件不再只依赖 `text_preview`。`text_preview` 可继续用于 UI 或兼容路径，但
Responses 请求以原文件为准，避免预览截断和重复拼接。

### 上传类型识别与真实字节校验

上传分类接口同时接收清洗前文件名、浏览器声明 MIME 和文件字节，并输出规范 MIME、
附件种类、是否生成文本预览及注册表条目：

- 声明 MIME 为空或为 `application/octet-stream` 等泛型类型时，可由受支持扩展名选择
  候选类型，再用真实字节验证。
- 声明 MIME 非泛型时，必须与扩展名映射及真实字节一致；冲突即拒绝。
- PDF 检查 `%PDF-` 文件头。
- DOCX、PPTX、XLSX 等 OpenXML ZIP 容器检查对应核心入口；ODT 检查容器和
  `mimetype`。只读取中央目录和有严格上限的必要元数据，不解压正文。
- DOC、PPT、XLS 等旧版 Office 检查 OLE 复合文档签名，并要求扩展名与声明 MIME 匹配。
- RTF 检查 RTF 文件头；Pages/Keynote 检查 ZIP/iWork 容器特征及扩展名/MIME。
- 文本、代码、CSV/TSV/IIF 必须为合法 UTF-8 且不得含 NUL 字节。
- ZIP/RAR/7z、EXE/动态库、未知二进制、音频和视频一律拒绝。

存储附件时记录规范 MIME、清洗后的文件名、大小和哈希。构造 Responses 请求时重新
执行同一注册表校验，不能只信数据库元数据。不得执行代码、宏或嵌入对象。

### Provider 隔离

OpenAI Responses 允许注册表中全部官方格式。非 OpenAI 路径继续只允许本次设计前已
支持的 PNG/JPEG/WebP/GIF、PDF、DOCX、TXT、Markdown、JSON 和 CSV；新增 Office、
演示、表格或代码 MIME 在发送阶段返回明确的模型附件不支持错误。该规则避免 provider
无关的上传接口使其他厂商路径意外扩容或静默丢附件。

### 混合附件

同一条用户消息可以同时包含 `input_text`、多个 `input_image` 和多个 `input_file`。
适配器保持用户选择的附件顺序；如果现有持久化结果不能稳定表示跨种类顺序，则按
附件记录 ID 升序生成，禁止使用 map 迭代顺序。

## 联网搜索语义

对声明 `supports_web_search = true` 的 OpenAI/GPT 文本模型，服务端每轮固定添加：

```json
{
  "tools": [{ "type": "web_search" }],
  "tool_choice": "auto"
}
```

这表示搜索工具始终可用，但模型可以直接回答而不调用它。不能转换为指定
`web_search` 的强制 `tool_choice`。

GPT WebChat 不再提供用户级搜索开关。前端不发送 GPT `web_search` 配置；为兼容旧
WebChat 客户端，后端可以继续接受该字段，但对 OpenAI/GPT 路由忽略其开关值，并按
模型能力应用上述固定策略。Anthropic/Kiro 等非 OpenAI 路由的现有配置语义保持不变。

图像生成等不支持联网搜索的模型不添加该工具。

## 模型能力

动态目录按 GPT 模型族补齐输入能力，而不是仅相信公共市场目录的展示模态：

- GPT 文本/推理模型声明 `supports_image_input = true`。
- GPT 文本/推理模型声明 `supports_file_context = true`。
- 图片生成专用模型继续使用自己的能力定义，不能因为 `gpt-` 前缀错误继承搜索或
  思考能力。
- 服务端能力验证仍是最终边界；前端能力标志仅用于附件控件和发送前校验。

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
- 扩展名或 MIME 不在官方注册表，或文件名、声明 MIME 与实际内容不一致。
- Office/iWork/ODF 容器损坏、缺少必要入口，或文本/代码不是合法无 NUL 的 UTF-8。
- Base64 或 JSON 请求构造失败。
- 目标模型不支持对应输入种类。
- 非 OpenAI 模型附带只允许 OpenAI 原生 `input_file` 的新增格式。

不得在这些情况下删除附件部分后继续发送纯文字。上游 Responses 错误沿用现有错误
透传和消息失败状态处理。

## 流式响应、产物与计费

- 继续使用现有 Responses SSE 转发和 WebChat 流式捕获。
- `web_search_call`、消息文本、reasoning 和图片生成事件继续进入现有前端解析流程。
- 搜索是否实际发生以返回的 `web_search_call` 为准；工具存在不等于本轮一定搜索。
- OpenAI WebChat usage 的 `upstream_endpoint` 统一为 `/v1/responses`。
- PDF 文本、页面图像和实际搜索工具调用产生的 token/工具费用由上游 usage 按现有
  计费路径记录，不新增独立账本。

## 前端行为

- GPT/OpenAI 模型不显示联网搜索按钮或其他搜索配置控件。
- 不新增“按需联网”“不保证联网”或类似用户说明。
- GPT 发送请求时不附带前端 `web_search` 配置，由服务端按模型能力固定注入工具。
- 非 OpenAI 模型的现有联网控件与行为不在本次修改范围内。
- GPT 图片和文件附件不再因为错误能力标志阻止发送。
- 删除独立的“上传图片”和“上传文件”两个按钮，只保留一个附件上传按钮；按钮沿用
  当前上传图片入口的 `upload` 图标，文案和无障碍标签统一为“上传文件”。
- 使用一个隐藏文件选择器。OpenAI 模型的 `accept` 包含图片和完整官方本地文件清单；
  非 OpenAI 模型的 `accept` 保持原有图片和旧文件范围。`accept` 只提供浏览器提示，
  后端注册表仍是安全边界。
- 每次文件选择保持单文件；用户可重复点击添加多个附件，不在本次加入多选交互。
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
- DOCX、PPTX、XLSX、旧版 Office 和文本/代码生成不带 `detail` 的 `input_file`。
- 文字、图片和文件可以在同一用户消息中组合。
- 超限、缺失和类型不一致的附件明确失败，且不产生降级请求。

### 上传注册表与字节校验测试

- 注册表覆盖 OpenAI 官方本地可上传格式及其规范 MIME/扩展名映射。
- PDF、DOCX、PPTX、XLSX、ODT、RTF、DOC/PPT/XLS、Pages/Keynote 和文本/代码
  至少各有代表性有效样本。
- 泛型 MIME 可由受支持扩展名加真实字节识别；非泛型 MIME 冲突时拒绝。
- 损坏 Office、伪造 PDF、错误 ZIP 内部入口、含 NUL 的代码、未知文本扩展名、ZIP、
  可执行文件和音视频明确拒绝。
- 单文件 20 MiB 和 Responses 中全部 `input_file` 原始字节合计 50 MiB 的边界均测试。
- 非 OpenAI 模型发送新增格式时明确失败；旧格式和既有转发断言保持通过。

### 搜索语义测试

- GPT/OpenAI 模型前端不渲染联网搜索按钮或说明。
- 支持搜索的 GPT 请求始终包含 `web_search` 且 `tool_choice == "auto"`。
- 旧客户端显式传入 `enabled: false` 时，GPT 后端仍按模型能力添加按需搜索工具。
- 不支持搜索的模型不含 `web_search`。
- 非 OpenAI 模型的现有联网搜索测试保持不变。

### 前端附件入口测试

- Composer 只渲染一个附件上传按钮，并使用 `upload` 图标。
- 图片和普通文件均经同一个隐藏文件选择器及上传方法处理。
- OpenAI 与非 OpenAI 模型生成各自正确的 `accept` 范围。
- 重复选择单个文件仍可累积多个附件；本次不要求原生多选。

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
4. GPT/OpenAI 会话界面不存在联网搜索按钮或搜索说明；支持搜索的 GPT 请求固定提供
   `web_search`，但无联网必要的提示可以不产生 `web_search_call`。
5. 图片生成等不支持搜索的模型不会收到 `web_search` 工具。
6. 附件异常不会被静默忽略，用户能看到明确失败信息。
7. GPT WebChat 可上传并原生发送官方支持的 Word、PowerPoint、Excel、文本和常见代码
   格式；非 PDF 文件不携带 `detail`。
8. 未知二进制、压缩包、可执行文件和伪造/损坏文件在调用上游前被拒绝。
9. Composer 只有一个使用 `upload` 图标的文件上传入口，图片和普通文件共用该入口。
10. 非 OpenAI 附件转发范围不因本次官方白名单扩展而改变。
11. 现有流式展示、使用量、计费和会话历史测试全部通过。
12. 不包含任何正式服环境或配置变更。
13. 正式服使用的 OpenAI OAuth 上游已在获得用户确认后，通过真实服务链验证支持
   `input_file`；若不支持，任务明确报告阻塞而不是宣称 PDF 已修复。

## 实施边界

本设计批准后，修订实施计划应把修改拆成能力目录、官方文件注册表与字节校验、Responses
输入适配、OpenAI 分发、GPT 前端搜索控件移除、统一附件入口和回归测试。每组先写失败
测试，再实现最小改动。已经完成并复核的 Task 1/2 保留；被暂停的 Task 3 未提交改动须
按修订计划重新核对 RED/GREEN 证据后才能提交。
完成本地验证后，如需发布到正式服，必须另行获得用户确认。
