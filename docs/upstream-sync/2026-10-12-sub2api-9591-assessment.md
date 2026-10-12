# 2026-10-12 上游同步与安全巡检调查

状态：范围和冲突预评估完成；用户已选择平台迁移方案 A，并纳入 GPT-6.1 Sol
订阅长上下文豁免。
尚未合并、推送或部署。Fable 隐式 3 倍撤销已由用户明确要求。

## 同步坐标

| 项目 | 坐标 |
| --- | --- |
| DEV_BASE | `591a03c550491b5d9f2e5c04bbc53dc1ae3177f3` |
| LAST_UPSTREAM / MERGE_BASE | `b8dece9000c68815a5b867ca5a1e6f236e173905` |
| UPSTREAM_PIN | `9591136d45cef26453348ed9532a7ebbce7cf99c` |
| 版本 | `v0.2.15-33-g9591136d4`，非 exact tag |
| 范围 | 186 commits，107 非 merge，339 files，+21434 / -1447 |
| 预合并 | `git merge-tree --write-tree dev upstream/main`：77 条冲突记录；没有修改工作区源码 |
| 备份 | `backup/dev-before-upstream-sync-20261012-9591` 指向 DEV_BASE |
| 分支 | `codex/upstream-sub2api-20261012-9591` |
| KIRO 参考 | `6ba76ea105e065a5aa8dd2b8d2957528ed58935b`，本轮不推进 |

本地 dev 原来落后 origin/dev 169 commits，已以 fast-forward 对齐。
原工作区 `.gitignore` / `AGENTS.md` 的用户修改保留；本地生产配置不读取、
不复制、不提交。本轮 GitHub Actions 查询不涉及生产 API 或真实 provider 调用。
Git 配置中的代理不可连接，本轮 fetch 使用命令级 `http.proxy=`，未修改全局配置。

## 安全巡检证据与修复范围

[失败运行 38232613680](https://github.com/lingchaojie/tokenstation3/actions/runs/38232613680)
是 2026-10-12 的定时 Security Scan，运行于 release
`67effdf851228fe5c241c82e383bf627fee2cfa0`。
frontend-security 成功；backend-security 的 `govulncheck ./...` exit 3。
日志列出 11 项可达 Go 标准库漏洞，其中 HTTP/2 公告同时覆盖
`golang.org/x/net@v0.58.0`；修复版本为 Go 1.27.2 / x/net v0.60.0。
这里只记录扫描的可达性结论，不声称已验证攻击可利用性。

上游已有对应修复：

- `c61f6ebaa8083f95d7caf9415a359a20715c134d`：Go 1.27.2 / x/net v0.60.0
  和相关 x/* 依赖；同步 Docker builder 与 workflow 版本校验。
- `5f93bb3b3fb30360560d2a3b9e688ba52c5b9471`：golangci-lint v2.14.0；
  对现有 HTTP/2 适配器的特定弃用告警做窄范围兼容处理，保留 PING / idle 配置。
- `d85cebecc4a1d2e5e39f1e80f9876b66a5ce0e31`：x/tools v0.50.0，支持新导出格式。

本地的 HTTP/2 使用范围比上游更广，lint 兼容必须按实际调用点复核，
不能关闭全部 staticcheck 或删除 HTTP/2 回归测试。验证需要旧代码复现扫描，
再对最终候选执行 govulncheck、生成检查、构建、unit/integration、lint。
定时巡检使用仓库默认 release 分支；仅更新 dev 不会修复下次 release 巡检。
release 发布需独立于本轮 dev 同步确认，不能把代码同步当作生产发布授权。

## 已确认：撤销 Fable 隐式 3 倍

用户要求“把之前 fable 3 倍收费的改回去修复”，撤销此前保留政策。
Fable 5.1 max 未配置倍率时恢复 1 倍；显式 effort 配置继续优先。
最终用户结算、请求在途预留、账号成本统计、模型广场与渠道表单默认提示
必须一致；输入、输出、5m/1h cache write 和 cache read 均不再默认乘 3。
不改历史用量日志、已结算扣款或已配置倍率。
先修改既有回归测试证明旧代码失败，再修改共享默认逻辑和前端提示，
保留显式倍率及其他模型、Astra Ultrafast 回归。

## 待决定：平台目录与迁移

上游 `e2939cd8f` / `874871a25` 新增 Command Code / Cline，
`3b89cc08d` / `7ffa38a8a` 引入前后端平台目录，
`eefa7dd9d` 将平台白名单移到应用层，删除数据库平台 CHECK。
现有 local 平台、KIRO direct/relay、统一 Key/default-follow 路由与额度语义
必须接入目录，不能直接复制只有上游平台的清单。Composite / Plugin
等此前排除路径继续排除。

上游新增 `242_drop_platform_check_constraints.sql` 与本地 242 冲突；
本地最高迁移已到 247。建议保留历史 SQL / checksum，将新迁移放到 248，
仅操作本地实际存在的表，移除对已排除 `composite_model_routes` 的操作。
部署迁移前必须有空库、247 升级、重复启动、非法平台拒绝和 KIRO 额度回归。

方案 A：纳入新平台与目录，沿上游取消平台 CHECK；应用层各入口和 repository
统一验证。新平台只在管理员手动配置账号/分组后可用。推荐。
方案 B：纳入新平台与目录，248 只扩展现有 CHECK 白名单；多保留数据库校验，
以后新增平台仍需要迁移。
方案 C：本轮暂不接新平台，保留现有数据库 CHECK；只兼容既有平台的目录重构。

## 待决定：GPT-6.1 Sol 订阅长上下文费用

上游 `43768680d38ccdbd8fffc2cb91d3c1fc778a61c6` 取消实际 OpenAI
OAuth-like 订阅凭据转发 GPT-6.1 Sol 时的模型长上下文附加费。
判断采用最终上游模型，不能被客户端别名或响应计费模型覆盖。
API Key 凭据仍按原 API 价卡；显式自定义区间定价、分组倍率、Fast/Flex
和本地其他价格优先级保持。
建议纳入，并将豁免贯穿重定价、账号成本和所有费用桶，覆盖 272k 边界、
缓存构成、真实凭据类型与别名/映射。另一方案是继续保留当前附加费。

## 其他集成约束与验证门禁

上游包括 EasyPay 回调字段防重用、首次安装随机管理员凭据、WebSocket
后续轮次当前分组价、协议分流/profile、模型发现映射、协议转换修复、
前端异步竞态与 TPS 展示。每个领域须审查调用链和自动合并区，保留本地
资金分离、模型展示/准入分离、缺价与显式 0、capture/WebChat 和品牌部署。

完整历史通过普通双父 merge 合入固定终点；不能只 cherry-pick 安全 commit
冒充同步完成。生成先独占，独立测试按资源隔离运行；所有代码和迁移变更
经独立 reviewer 全量复审通过后，才能非强推 dev，并等待精确 SHA 全部 CI。
main 仅 fast-forward 与 origin/main 对齐，不把 dev 发布到 main。

当前只完成调查与准备，尚无本轮最终代码测试、独立复审或 CI 成功声明。
