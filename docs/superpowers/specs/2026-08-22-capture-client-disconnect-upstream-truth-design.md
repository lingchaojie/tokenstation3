# 客户端取消请求的 Capture 上游事实归档设计

- 日期：2026-08-22
- 状态：设计已确认，等待实施计划与代码实现
- 适用分支：`dev`
- 关联设计：`docs/superpowers/specs/2026-08-16-capture-disk-spool-sidecar-design.md`

## 1. 背景

当前网关可以识别客户端在模型调用过程中断开连接，例如用户在 CLI 中按下
`Ctrl+C`。请求进入 capture 后，主进程和 sidecar 的 IPC attempt 已经脱离原始请求
context，因而客户端断开并不必然阻止后续 response 字节写入 spool。

但是当前最终提交逻辑会把客户端断开改写为：

- `stop_reason = "pre_commit_disconnect"`；
- `response_complete = false`；
- outcome 为 `terminal_error`。

正式服运行时策略当前不保存 `terminal_error`，所以这类 attempt 最终 Abort，部分文件
从 spool 删除，不会进入 ClickHouse。与此同时，`pre_commit_disconnect` 不是 provider
返回的官方 stop reason，把它写进模型终止原因还会混淆“上游事实”和“网关状态”。

本设计允许保存客户端主动取消时已经观察到的上游内容，同时保持真实上游故障默认
不归档，避免上游大面积 429、5xx 或不可用时产生大量无意义记录。

## 2. 已确认目标

- 客户端主动取消时，只要请求符合原有 capture 范围，就保存已经实际观察到的上游
  request/response。
- `stop_reason`、`finish_reason` 等模型终止原因只保存 provider 官方返回的原值。
- 不添加 `pre_commit_disconnect`、`client_disconnected` 或其他自定义归档值或字段。
- 客户端断开后，如果网关继续排空上游响应，后续实际收到的字节仍属于可归档的上游
  内容。
- 真实上游错误的优先级高于客户端断开；正式服保持 `terminal_error=false` 时，这些错误
  不进入 ClickHouse。
- 不改变 ClickHouse schema、现有 content 开关、平台/模型/用户/用户组过滤条件，以及
  spool 的 ACK 后清理语义。

## 3. 非目标

- 不在 ClickHouse 中精确标记或统计“客户端主动取消”。
- 不根据已知枚举对白名单化 provider stop reason；未来新增的官方值也应原样保留。
- 不把 HTTP 429/5xx、代理失败、连接失败、服务端超时或协议失败归类为客户端取消。
- 不改变 provider 选择、重试、failover、计费、usage 或客户端可见响应。
- 不在本次设计中修复 sidecar uploader 的 idle probe/upload loop 退出问题；该生产问题是
  独立缺陷，正式部署本功能前必须另行修复并验证，否则新增的 ready 记录仍可能积压。

## 4. 终态分类

最终分类必须在本次真实上游处理结束后进行，不能在刚检测到客户端断开的瞬间抢先
Commit。统一优先级为：

```text
真实上游失败 > 客户端取消 > 正常成功
```

| 观察结果 | 内部分类 | 是否归档 |
| --- | --- | --- |
| 上游正常完成 | success | 遵守现有 success outcome 开关 |
| 客户端断开，未发现真实上游失败 | client disconnect capture | 归档 |
| 客户端断开导致本地上游 context 被取消 | client disconnect capture | 归档 |
| 上游明确返回 429/5xx、官方错误事件或真实传输故障 | terminal_error | 遵守现有 terminal_error 开关；正式服当前不归档 |
| 客户端断开后，排空过程中发现明确上游错误 | terminal_error | 上游错误优先；正式服当前不归档 |
| HTTP 200 且官方终态为 refusal、content filtered 或 guardrail intervened | success/client disconnect capture | 归档，因为它是 provider 官方终态，不是服务不可用 |
| 网关本地合成错误、代理错误、协议错误或服务端超时 | terminal_error | 正式服当前不归档 |

客户端取消的内部分类只决定 Commit/Abort，不写入 ClickHouse 字段。它仍必须满足运行时
策略的 master、platform、model allowlist、user 和 group 范围；content 设置也继续决定
raw request、raw response 和 headers 是否落库。经这些范围选中的客户端取消不受
`outcomes.success` 或 `outcomes.terminal_error` 阻止。

分类使用结构化信号，例如客户端断开标志、上游 HTTP 状态、provider 错误事件和传输
结果，不依靠错误文本匹配。仅由客户端断开触发的 `context.Canceled` 不算真实上游
错误；如果存在明确的上游失败证据，则上游失败始终获胜。

## 5. 上游终止原因的来源边界

归档提取器只从实际观察到的上游 wire response 中提取模型终止原因，例如：

- Anthropic `stop_reason` 或流式 `delta.stop_reason`；
- OpenAI `choices[*].finish_reason`；
- Gemini candidate `finishReason`；
- AWS Bedrock `messageStopEvent.stopReason`。

规则如下：

1. 保存 provider 返回的原始值，不转换、不统一命名、不做枚举白名单。
2. response extractor 得到的上游值是最终权威。
3. Finalize/Commit 阶段不得用网关状态覆盖该值。
4. 未观察到 provider 官方终止原因时，`stop_reason` 保持空值。
5. 当前客户端断开路径不得再赋值 `pre_commit_disconnect`。
6. `pre_commit_disconnect` 可以继续作为管理健康状态中的 IPC 丢弃原因，但不得进入
   ClickHouse 的模型终止原因。

该边界有意牺牲由应用层补写终止原因的便利，换取字段来源可证明。即使以后 provider
增加新的官方值，只要它出现在受支持的官方 wire 字段中，就原样保存。

## 6. `response_complete` 语义

`response_complete` 描述网关是否完整观察到上游响应，而不是客户端连接是否仍然存在：

- 客户端断开后，网关继续排空并观察到完整官方终止边界：`true`；
- 客户端断开后，上游读取也被取消或未观察到完整终止边界：`false`；
- 正常请求继续使用同一“上游响应是否完整”的定义。

因此客户端取消不再无条件强制 `response_complete=false`。该字段和官方 stop reason
相互补充，但不能用自定义 stop reason 编码不完整状态。

## 7. Capture 与 spool 生命周期

请求处理中，capture attempt 先流式写入正式服本地 `spool/partial`。上游处理结束后再
应用第 4 节的分类：

```text
spool/partial
  ├─ 正常成功且策略允许 ────────────────> Commit -> spool/ready
  ├─ 客户端取消且范围匹配 ──────────────> Commit -> spool/ready
  └─ 真实上游失败且 terminal_error 关闭 -> Abort  -> 删除 partial
```

`spool/ready` 的上传和清理沿用现有可靠性边界：

1. sidecar 以固定 batch 和 deduplication token 向 ClickHouse 发起同步 INSERT；
2. 只有 ClickHouse 返回成功 2xx，batch 才视为 delivered；
3. sidecar 先持久化 batch ACK；
4. 再删除对应 ready record 和 sending metadata，并 fsync 目录；
5. 上传超时、网络错误、非 2xx 或 ClickHouse 拒绝时保留文件并重试；
6. ACK 后清理期间崩溃时，启动恢复继续清理已确认 batch。

正式服 spool 仍然只是待上传缓冲，不是 ClickHouse 写入后的永久副本。

## 8. 查询能力限制

本设计刻意不保存客户端取消事实。因此 ClickHouse 中一条
`response_complete=false` 且 `stop_reason=''` 的记录，不能单凭归档内容断定为用户
按下 `Ctrl+C`；它也可能表示没有观察到官方终态的其他情况。

如果将来需要精确统计客户端主动取消率，必须使用业务日志或独立指标体系，不能从该
归档表准确反推。本次不得用特殊 stop reason、隐藏枚举或新增列规避这个限制。

## 9. 错误处理与安全边界

- capture 继续保持 drop-safe；capture、IPC、spool 或 ClickHouse 故障不得改变转发结果。
- 不合格 attempt 的 Abort 必须关闭 writer、删除 partial 并释放 attempt slot。
- 客户端断开后继续排空上游的行为沿用各 provider 当前 usage/计费需要，不由 capture
  强行延长请求；capture 只保存网关本来已经观察到的字节。
- 不允许为确认该功能而绕过正式服既有代理链路裸调 provider API。
- 任何正式服部署、配置修改、重启或测试流量都需要单独获得用户确认。

## 10. 测试设计

### 10.1 分类矩阵

- 正常成功按 success outcome 保存或丢弃。
- 客户端取消、无官方终态：Commit，stop reason 为空，`response_complete=false`。
- 客户端取消后完整排空：Commit，官方 stop reason 原样保存，
  `response_complete=true`。
- 客户端取消触发本地 `context.Canceled`：Commit，不误判为上游错误。
- 客户端取消同时收到明确 429、503、provider 错误事件或真实传输失败：按
  `terminal_error`，上游失败优先。
- 上游大量 503 且正式服等价策略 `terminal_error=false`：不产生 ready records。
- HTTP 200 的 refusal、content filtered、guardrail intervened：作为官方终态保存。

### 10.2 stop reason 来源

- 各受支持 wire 格式的官方终止原因逐字节/逐字符串原样提取。
- 未知的新官方值不被白名单拒绝或改写。
- 没有官方终止字段时为空。
- Final/Commit 提供的自定义值不能覆盖 response extractor 的官方值。
- 现有期待 `pre_commit_disconnect` 覆盖 payload stop reason 的集成测试必须改为相反
  断言。

### 10.3 策略与内容

- 客户端取消仍遵守 master/platform/model/user/group 范围。
- 客户端取消在 success/terminal_error outcome 均关闭时仍可归档。
- raw request、raw response 和 headers 继续分别遵守 content 开关。
- 不新增 ClickHouse 列、协议字段或自定义归档枚举。

### 10.4 spool 与恢复

- 客户端取消 Commit 后记录进入 ready。
- ClickHouse 成功后持久化 ACK 并自动删除 ready 文件。
- ClickHouse 失败或超时时保留原 batch，等待重试。
- ACK 后、清理前模拟崩溃，恢复后只清理已确认 batch。
- 真实上游错误 Abort 后不残留 partial/ready 文件。

## 11. 实施与发布顺序

1. 先用测试固定现有错误行为和期望分类矩阵。
2. 引入集中终态分类，确保上游失败优先。
3. 增加客户端取消专用的内部 Commit 路径，但不扩展 ClickHouse schema。
4. 隔离 stop reason 来源并修正 `response_complete` 语义。
5. 运行 capture service、extractor、IPC、spool、sidecar 和 ClickHouse 集成测试。
6. 单独修复并验证已确认的 sidecar uploader 退出缺陷。
7. 完成本地回归后，再请求正式服部署和验证授权。

正式服验收必须同时观察：客户端取消样本是否进入 ClickHouse、明确上游错误是否仍被
过滤、成功 ACK 后 spool 是否清空，以及新的上传记录是否能持续处理而非只在重启后
短暂恢复。

## 12. 回滚

代码发布前可直接放弃功能分支。发布后如分类异常，优先回滚本功能提交，恢复客户端
取消按 terminal error Abort 的旧行为；不修改或清空现有 ClickHouse 数据，不手工删除
未确认的 spool ready records，也不重写共享分支历史。
