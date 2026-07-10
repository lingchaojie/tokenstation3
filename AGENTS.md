# AGENTS.md

## KIRO Upstream Work

When a user asks to sync, replace, audit, or modify upstream KIRO forwarding
gateway behavior, or asks to inspect the original KIRO implementation, read
`docs/kiro-upstream-sync.md` first and use it as the KIRO reference-tracking
guide.

## 正式服问题排查

当涉及正式服（生产环境）的问题排查、环境查看时：

- 任何对正式服环境或代码的改动，在执行前都必须先获得用户确认，不得擅自变更。
- 如果排查需要使用正式服中的账号、向上游 provider 的 API 发起请求，必须复用原有代码流程，
  按照正式服配置的 IP 代理发出请求，尽量完整模拟服务端的真实调用链路，
  不要绕过既有流程直接裸调上游 API。

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
