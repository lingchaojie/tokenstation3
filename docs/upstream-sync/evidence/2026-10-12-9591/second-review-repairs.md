# 后续复审修复与验证

复审坐标为 `ee9c34250ba034e925a8d64d5be1ad1757d037df`（代码 60fb）。
前端完整覆盖 148 路径、基础设施完整覆盖 32 路径，均给出 NOT SAFE；
后端 reviewer 在中断前以确定性 detached snapshot 复现 Cline 并发问题，
未完成全域通过报告。旧结论不代替最终版本的全新独立复审。

## 修复

1. Cline 探测的短缺写入与健康恢复，都以完整钱包 JSON scope 执行原子
   compare-and-set。原因、截止时间、写入时间及附加字段都参与比较；缺失与
   JSON null 区分。请求侧写入保持原有接口，拒绝过期探测时刷新权威调度快照，
   不产生成功日志或 outbox 事件。只有应用成功才改 detached account。
   replacement JSON 在调用方生成，避免数据库和本地快照采用不同时间。
2. Release 构建校验 setup-go 实际选择的版本，继续按被选应用源码的 go.mod
   配置工具链。旧标签无需包含新辅助脚本，当前分支的安全版本要求保持。
3. 官方用量入口对 api_protocol 去除首尾空格，与后端 GetAPIProtocol 一致；
   显式原生协议采用 base_url，自适应协议采用分协议端点。
4. 视频计费记录不显示文本 Token/s，与后端 TPS 查询的媒体过滤保持一致。

代码提交：Cline `8c28ab40d`、Release `9cdf3ecd1`、前端 `3e0c28121`，
以及 Cline snapshot 类型检查 `0bd51b01613dca9b644c9db358a555759f19d842`。
最后一项处理 golangci-lint 的 errcheck 提示，未将失败检查当作通过。

## 回归证明

- Cline：detached snapshot 在 UpdateExtra 边界插入新限制，覆盖两个钱包的
  短缺/健康恢复、同原因不同截止时间/写入时间/附加字段、CAS 错误和能力缺失。
  先观察旧行为失败，再观察修复通过。真实 PostgreSQL 矩阵 40 场景同时验证
  完整 scope 比较、缺失/null、其他钱包与 extra 保留、outbox 和权威缓存。
  详细记录见 cline-cas-fix.md。首次编译与测试 helper 修改重叠的 RED 不计证据，
  后续独占 detached RED 为有效复现。
- Release：实际 workflow 校验脚本在旧/当前版本选择及版本漂移四场景验证。
  RED 两场景失败；GREEN 15 个 helper tests 通过。使用本机真实 Go 1.27.0
  与 1.27.2 再验证四场景，退出码依次 0/0/1/1。setup-go@v6 的官方源码确认
  go-version 输出去掉 go 前缀，且设置 GOTOOLCHAIN=local。
- 前端：三个 Command Code 原生协议带空格、Cline adaptive 带空格，均验证
  官方 base_url 与中转分协议端点的正反方向；视频 formatter 和 UsageTable
  渲染分别断言不显示速率。RED 六项失败，GREEN 三文件/71 项通过。

## 前端最终源码与检查

3e0c 前端 1057 文件清单 SHA256：
`2f2af561bb0bc6e44ada470d3143d1794e54047d22d7fe993f9c2b7ebc64ee13`。
后续 Go 类型检查修复未改变任何前端字节，已逐文件确认。全量 444 files /
3549 tests、关键套件 775 / WebChat 75、lint/typecheck/build 均通过；
独立命令与时间详见 validation.json。依赖未变，不重复 audit、不新增例外。

## 证据保存

命令、起止时间、退出码和日志保存在被忽略的 .superpowers/20261012-sync。
原定时扫描日志从 job 114755486810 重新取得并存入该持久目录，共 75626 字节，
含 11 个 GO-2026 公告；SHA256 为
`f568de70ac770c7c0b57ab48d53212ebca7683a14001c50a2ccf39f7822310c3`。
原 A/B 与早期生成日志位于 /tmp，恢复时已不存在，其结果仅保留为当时的
历史观察。最终生成检查及完整后端验证使用 0bd 的新持久日志替代最终证明。
不提交原始日志、工具二进制、正式服本地说明或凭据；未连接正式服。
