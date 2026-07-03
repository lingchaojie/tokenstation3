# 2026-07-03 Upstream sub2api Merge Decisions

本文件记录本次将上游 `Wei-Shaw/sub2api` 合入本地 `dev` 的取舍，避免后续同步时把本地特制能力覆盖或把上游默认能力遗漏。

## 合入范围

- 本地基线：`origin/dev` `4a26b24c2b7fd23afc1978921d0c21f3f7bd83ea`
- 上游基线：`upstream-sub2api/main` `87dfc66132c6af2ee464668fdd15209884b0d335`
- merge base：`7dc7cfce1db5d31599815ff29acf6847ead0f0b7`
- 工作分支：`merge/upstream-sub2api-dev-20260703`

原则：

- 本地没有特意改动过的上游默认功能，跟随上游更新。
- 本地已有特制行为的区域，以本地行为为准；只合入能兼容这些行为的上游增量。
- 不确定会改变线上语义的功能，不借合并默认打开。

## 主要决策

### IP geo lookup

合入上游 IP 归属地展示/查询能力。本地接受这类用户侧增强，前端 `UsageView` 继续使用本地表格结构，并接入批量查询失败事件提示。

### Anthropic API-key Bearer auth

合入上游 `anthropic_apikey_auth_scheme=authorization_bearer` 能力，但边界限制为 `platform=anthropic` 且 `type=api-key` 的账号。

不扩展到 KIRO：

- KIRO direct 继续走本地 AWS/IDC 原生 token 与 profile 解析路径。
- KIRO relay 继续走 Anthropic-compatible 反代路径的 `x-api-key` 语义。
- 如果以后要让 KIRO relay 支持 Bearer，需要单独设计显式开关，不能复用 Anthropic API-key 的开关。

本次增加了 KIRO 场景回归用例，防止后续误扩展。

### Peak rate multiplier

合入上游 peak-rate 后端能力：group schema、migration、ent、鉴权缓存快照、repository、admin service 和 gateway 计费链路都保留 peak-rate 字段。

用户侧前端不展示 peak-rate：

- 可用频道列表不展示 rate multiplier 或 peak-rate。
- 用户支付计划卡片不展示 group quota、rate multiplier、peak-rate 或模型 scope。
- 用户订阅页不展示 peak-rate。
- 用户 key 管理页不加入按 group 选择/展示 peak-rate 的上游改动，继续保留本地 provider routing 语义。
- 用户支付接口响应不暴露 peak-rate/group 展示字段。

管理侧保留 peak-rate 配置入口。原因是 peak-rate 是计费策略，不应从用户购买/订阅 UI 暴露为营销或选择项，但管理员仍需要能配置和排查。

### Migration 编号

本地 `dev` 已有 `158` 到 `164` 迁移，因此上游两个 `158` 迁移改号：

- `165_add_group_peak_rate_multiplier.sql`
- `166_enable_grok_media_generation_groups.sql`

迁移回归测试同步改为检查 `165/166`，避免文件名冲突。

## 本地特制能力保护项

本次合入显式保护以下本地行为：

- KIRO direct 与 relay 分流，direct 不走通用 Anthropic relay 行为。
- KIRO mixed scheduling 只注入 Anthropic 分组，不注入 Gemini 分组。
- KIRO group runtime 字段继续进入 API key auth cache，和 peak-rate 字段共同组成 snapshot v17。
- WebChat hidden key 不在用户 key 列表和 provider routing 中暴露。
- API key provider routing 继续以 Anthropic/OpenAI key type 为用户侧选择维度，不改成上游 group selector。
- 通用订阅、计划席位、七日额度展示、续费/切换计划语义保留。
- IkunPay/支付方式展示和本地支付计划 DTO 保留。
- 模型目录与 marketplace 的本地扩展不被上游覆盖。

## 后续同步注意事项

- 若上游继续扩展 group 展示字段，先判断是否会进入用户支付、订阅、频道、key 管理界面；用户侧默认不展示 peak-rate/rate multiplier。
- 若上游继续扩展 Anthropic API-key 透传认证，必须确认不会改变 KIRO direct/relay header。
- 若上游增加迁移，先检查本地最新 migration 编号，避免重复编号。
- 若改动 ent schema，重新生成 ent 后要确认 KIRO 字段和 peak-rate 字段都存在。
