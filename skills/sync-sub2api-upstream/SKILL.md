---
name: sync-sub2api-upstream
description: Safely synchronize Wei-Shaw/sub2api upstream updates into the local dev branch from discovery through CI completion. Use when the user asks to “同步上游更新”, “合并上游”, inspect a new sub2api release, or bring upstream changes into dev. Requires semantic integration and detailed code review of both conflicted and auto-merged regions instead of copying code blocks, plus refreshing dev, pinning the full upstream range, preserving local behavior, asking before ambiguous decisions, archiving, testing, independent subagent review, non-force dev/main push, and CI completion.
---

# sub2api 上游同步工作流

## 执行契约

| 项目 | 规则 |
|---|---|
| 默认目标 | 将 `Wei-Shaw/sub2api` 的最新已获取节点合入本地 `dev`。先用 `git remote -v` 确认实际远端名；下文以 `upstream` 和 `origin` 为例。 |
| 用户触发 | 用户说“同步上游更新”“合并上游”或同义表达时执行完整流程，不把任务缩减为只评估或只合一个 commit。 |
| 仓库规则 | 开始前完整读取仓库根目录及相关子目录的 `AGENTS.md`，并读取现有上游同步指南与最近一次 `docs/upstream-sync/` 归档。仓库规则优先。 |
| 上游范围 | 固定最新 `upstream/main` SHA 作为可复现终点；合并该终点的完整缺失历史，不把某个 PR merge commit 误当成唯一要合入的 commit。 |
| 本地保护 | 先识别本地特有行为、测试和历史冲突决定；冲突解决以不破坏这些行为为首要约束。不要在技能中维护具体功能清单，以当前仓库证据为准。 |
| 语义整合 | 必须真实阅读并理解合入后的代码。不得把冲突标记两侧“看起来不冲突”的代码块简单拼接，也不得假设 Git 自动合并成功就代表逻辑正确。逐条追踪调用链、控制流、数据流、副作用和错误路径。 |
| 人工门禁 | 任何需要产品或语义取舍的合并点，必须先向用户提问并等待回答。不得替用户猜测。 |
| 推送门禁 | 测试通过且独立 subagent 明确结论为无可执行问题后，才允许 push `dev`。禁止 force push。 |
| 完成定义 | 精确的已推送 `dev` SHA 对应的全部必需 CI checks 成功后才算完成。CI pending、skipped-required 或失败都不能宣称完成。 |

## 状态流程

| 阶段 | 必做动作 | 产出 / 通过条件 | 停止或提问条件 |
|---|---|---|---|
| 0. 保护现场 | 查看 `git status --short`、当前分支、worktree、远端和 dirty 文件；记录用户已有改动，不清理、不覆盖。优先在独立临时 worktree 中同步。 | 原工作区改动保持原样；同步环境可独立操作。 | 目标分支被其他 worktree 占用且无法安全操作，或现有改动与同步文件重叠。 |
| 1. 刷新 DEV | `git fetch --prune --tags origin`；让本地 `dev` 以 `--ff-only` 更新到 `origin/dev`。先比较本地/远端，禁止 reset 掉本地独有 commit。 | 本地 `dev == origin/dev`，并记录同步前 SHA。 | `dev` 与 `origin/dev` 分叉、存在本地独有 commit，或无法 fast-forward。先报告差异并询问。 |
| 2. 刷新上游 | 确认指向 `Wei-Shaw/sub2api` 的远端后执行 `git fetch --prune --tags upstream`；记录 `upstream/main` SHA 和 exact tag（如有）。 | 得到本轮固定上游终点 `UPSTREAM_PIN`。 | 上游远端缺失或 URL 不确定。 |
| 3. 寻找节点 | 从最近同步文档、`dev` first-parent merge 历史和 merge commit 第二父节点确定上次上游落点；同时计算 `merge-base(dev, UPSTREAM_PIN)`。 | 记录 `DEV_BASE`、`LAST_UPSTREAM`、`MERGE_BASE`、`UPSTREAM_PIN`。 | 文档与 Git 历史不一致，或无法可靠判断上次上游落点。 |
| 4. 对比上游 | 对 `LAST_UPSTREAM..UPSTREAM_PIN` 和 `dev..UPSTREAM_PIN` 查看 commit、stat、name-status、tags；区分“上游新增”与“dev 已经通过其他历史包含”。 | 形成准备合入的完整 commit 范围和按领域归类的变更摘要。 | 上游终点不是上次节点后代、历史被重写，或范围异常大且需改变合并策略。 |
| 5. 影响评估 | 读取改动文件、调用方、被调用方、邻近测试、同步指南和本地相对上游的差异；建立冲突/影响矩阵，覆盖运行时语义、数据、API、配置、前端契约和本地扩展点。 | 每个合入区域都能说明“上游意图 / 本地意图 / 最终控制流 / 决策 / 验证方式”。 | 出现下方“必须提问”的任一情况。只暂停相关决策，继续完成安全的只读评估。 |
| 6. 建立回滚点 | 在同步前 dev SHA 创建 `backup/dev-before-upstream-sync-<timestamp>`；创建专用 merge 分支和临时 worktree。 | 备份 SHA 与 `DEV_BASE` 一致。 | 备份名冲突或分支状态不明。 |
| 7. 执行合并 | 使用普通 merge：`git merge --no-ff --no-commit "$UPSTREAM_PIN"`。逐文件解决冲突；先理解两侧代码在完整调用链中的作用，再设计最终实现。即使是机械兼容，也不得靠复制冲突标记外的代码块完成。语义冲突必须经过用户确认。 | 无 unmerged entries；最终 merge commit 保留两个父节点；每个解决结果都能解释为何控制流与副作用正确。 | 不确定应保留哪种行为、无法解释最终逻辑，或只能靠整文件/代码块 `--ours/--theirs` 猜测。 |
| 8. 全量语义审计 | 对 merge 涉及的所有文件审查 base、ours、theirs 和最终结果，包括 Git 自动合并且没有冲突标记的区域。沿入口到出口核对调用顺序、状态读写、副作用、错误/重试/清理路径和跨层契约。显式 `git add <paths>`，不要用 `git add -A`。 | 每个合入区域都有语义审查证据；无静默错合、重复逻辑、失效调用、冲突标记、意外文件或空白错误。 | 只能证明“编译通过”但无法证明行为正确，或发现自动合并后的语义不一致。 |
| 9. 测试验证 | 先读取 CI workflow / Makefile 确定真实命令；依次执行生成一致性、构建、后端 unit、integration、lint、前端 lint/typecheck/关键测试/构建及相关全量测试。不要并行运行会写生成文件的任务。 | 所有必需检查通过；结果对应最终代码。 | 测试失败。若怀疑是基线欠账，在未合并的 `DEV_BASE` worktree A/B 复现并如实记录，不得直接忽略。 |
| 10. 文档归档 | 在 `docs/upstream-sync/` 新建本次记录并更新索引。记录节点、范围、冲突决策、测试、review 和回滚分支；保持概括，不展开当前 DEV 的具体功能清单。 | 文档可独立回答合了哪个范围、如何验证、如何回滚。 | 归档信息与实际 SHA 或测试结果不一致。 |
| 11. 独立全量复审 | 测试后启动全新 subagent，对本轮全部合入区做逐文件、逐调用链 code review；不得只看冲突文件或抽样。只给原始证据和坐标，不泄露主 agent 的“安全”结论。 | subagent 明确输出 `NO ACTIONABLE ISSUES / SAFE TO PUSH`，并证明冲突区和自动合并区都已覆盖。 | 有任何可执行问题、证据不足、抽样审查或未覆盖区域。修复或询问用户后，重跑相关测试并交给新的 subagent 复审。 |
| 12. 推送前复核 | 再次 fetch `origin` 和 `upstream`；确认远端 dev 未前进，已验证的上游 pin 未漂移。将本地 `main` 以 `--ff-only` 更新到最新 `origin/main`，不得覆盖 main 独有提交或 dirty worktree。 | `dev` 指向最终同步提交；本地 `main == origin/main`；review 与测试仍对应待推 SHA。 | origin/dev 已移动、upstream/main 新增提交、main 无法 FF。报告差异并询问是否扩展本轮范围或重新整合。 |
| 13. 推送 | subagent 门禁通过后，使用非强制 push 一起推送 `dev` 和 `main`，并记录远端返回及精确 dev SHA。 | `origin/dev` 指向预期 SHA；main push 为 FF 或 no-op。 | push rejected、branch protection 要求不同流程，或将发生非 FF。禁止 `--force`。 |
| 14. 等待 CI | 查询该精确 dev SHA 的所有 required checks，持续等待到终态。若失败，读取日志、修复、重跑本地测试和独立 review，再推新 SHA 并重新等待。 | 全部 required CI checks 对精确 SHA 为 success。 | CI 需要用户权限、外部凭据或出现无法本地解决的基础设施故障；报告 blocker，不得宣称完成。 |
| 15. 收尾 | 更新归档中的最终 dev SHA、CI 结果/链接；保留备份分支直到用户确认稳定；清理只属于本次任务且无未提交内容的临时 worktree。 | 向用户报告上游 pin、dev SHA、main 状态、测试、review、CI 和回滚点。 | 任何门禁仍未满足。 |

## 节点与对比命令

| 目的 | 建议命令 | 判断原则 |
|---|---|---|
| 查看分支关系 | `git log --graph --oneline --decorate --all -n 100` | 不根据 commit 标题猜祖先关系。 |
| 找最近上游 merge | `git log --first-parent --merges --oneline dev`，再 `git show -s --format='%H %P %s' <merge>` | 普通两父 merge 的第二父节点通常是当时上游终点；必须与归档交叉验证。 |
| 计算公共祖先 | `git merge-base dev "$UPSTREAM_PIN"` | merge-base 不一定等于上次同步节点，两者都记录。 |
| 查看上游完整区间 | `git log --reverse --oneline "$LAST_UPSTREAM..$UPSTREAM_PIN"` | 最新 PR merge commit 只是区间内节点，不是默认唯一合入对象。 |
| 查看 dev 尚缺提交 | `git log --left-right --cherry-mark --oneline "dev...$UPSTREAM_PIN"` | 识别等价 patch 与真实缺失历史。 |
| 查看范围规模 | `git rev-list --count "dev..$UPSTREAM_PIN"`；`git diff --stat "$MERGE_BASE..$UPSTREAM_PIN"` | 异常规模先解释，再决定一次合并还是分段合并。 |
| 查看文件与语义 | `git diff --name-status "$LAST_UPSTREAM..$UPSTREAM_PIN"`；按高风险文件逐个 `git diff` | 不能只看 commit 标题或 diff stat。 |

## 语义级合入审查清单

| 审查维度 | 必须执行 | 不合格表现 |
|---|---|---|
| 覆盖范围 | 审查 merge 引入的每个生产代码区域及其关键测试；同时检查显式冲突区和 Git 自动合并区。范围过大时拆分给多个 reviewer，但必须有完整覆盖清单，不得抽样代替全审。 | 只看冲突标记、只看修改行、只检查编译错误，或默认无冲突文件天然安全。 |
| 四方对照 | 对重要文件分别阅读 merge base、ours、theirs、最终结果，先还原双方设计意图，再判断最终实现。 | 把 ours 和 theirs 的代码块做文本并集，或看到两段不重叠就全部保留。 |
| 调用链 | 从真实入口追到下游：调用者、参数来源、配置/上下文、核心转换、状态读写、外部调用、返回值和消费者。必要时使用 `rg`、代码图谱、测试和提交历史。 | 只读当前函数，不知道谁调用、输出被谁使用或执行条件是什么。 |
| 控制流顺序 | 核对校验、鉴权、映射、header/body 改写、调度、重试/failover、计费、日志/capture、响应写入和清理的先后关系。 | 两段代码都被保留，但顺序变化导致身份、计费、重试、记录或响应语义改变。 |
| 数据与状态 | 核对类型、空值、默认值、schema/migration、序列化字段、DB/缓存键、前后端契约和状态生命周期。 | 字段存在就认为兼容，未检查读写两端、默认值、迁移顺序或旧数据。 |
| 错误与并发 | 核对 error wrapping/status、fallback 边界、资源关闭、defer、cancel、锁、goroutine/channel、幂等和重复写入。 | happy path 正常就结束 review，未检查失败、超时、取消和并发路径。 |
| 重复与失效逻辑 | 检查函数拆分/重命名后是否出现重复 symbol、双重执行、旧 helper 残留、死分支或漏接新 helper。 | 为“双方都保留”而重复执行副作用，或新旧路径同时存在但调用关系不明。 |
| 行为证明 | 为最终组合行为补充或更新针对性测试，同时保留本地关键行为的回归测试；说明每个测试证明哪条语义。 | 仅依赖已有测试数量、编译成功或全量测试绿灯，没有覆盖新的组合控制流。 |
| 解释门禁 | 在暂存/提交前，用简洁文字说明每个关键合入区的最终运行逻辑、为何保留/移动/删除各段代码，以及如何验证。 | 无法解释某段代码为什么在这里、是否会执行两次或会影响哪个下游。此时必须继续调查或询问用户。 |

## 必须先问用户的情况

| 类别 | 典型信号 | 提问内容 |
|---|---|---|
| 行为互斥 | 上游和本地实现不能同时保留，或默认值、开关语义相反。 | 给出双方行为、影响面、可选方案和推荐方案，等待选择。 |
| 高风险契约 | 涉及认证、授权、计费、额度、路由、支付、安全、数据删除、公开 API、默认启用策略。 | 说明可能改变的外部行为和回滚方式，取得明确确认。 |
| 数据与迁移 | migration 编号冲突、schema 不兼容、需要数据回填或非幂等操作。 | 给出编号/执行顺序/已部署数据库影响，确认处理方案。 |
| 删除本地路径 | 上游删除或重构了本地仍在使用的文件、字段、接口或调用点。 | 提供引用证据与迁移方案，确认保留、迁移或删除。 |
| 验证不足 | 无法建立测试、缺少必要环境或只能凭推断判断兼容。 | 明确缺失证据、风险与可执行的验证选项。 |
| 分支漂移 | origin/dev、origin/main 或 upstream/main 在流程中前进或分叉。 | 展示新旧 SHA 与新增范围，确认重新纳入还是保持已验证 pin。 |

提问时一次只聚合同一决策域。用户未回答前，不提交该语义决策、不 push。

## 测试与基线规则

| 检查 | 要求 |
|---|---|
| 生成代码 | 运行仓库定义的 generate consistency check；生成任务与读取同一生成文件的 build/test 串行执行。 |
| 后端 | 至少覆盖 build、unit、integration、静态检查；以当前 CI 配置为准。 |
| 前端 | 至少覆盖 lint、typecheck、关键测试、受影响测试和 production build；若仓库 CI 还有额外要求，一并执行。 |
| 全量测试 | 能运行时执行。失败不得只凭“看起来无关”归类为基线问题。 |
| A/B 基线 | 在 `DEV_BASE` 独立 worktree 用同一命令复现，比较失败测试、报错和相关 diff。只有证据一致才记录为既有欠账。 |
| 测试后改动 | 任何代码修复都使相关测试结果失效；重跑受影响测试。影响公共路径时重跑对应全量套件。 |

## 独立 subagent 复审协议

| 项目 | 要求 |
|---|---|
| 时机 | merge、冲突修复、文档归档和本地测试完成之后，push 之前。 |
| 上下文 | 提供仓库/worktree 路径、`DEV_BASE`、`UPSTREAM_PIN`、最终 HEAD、两个 merge parents、改动范围和测试原始结果。 |
| 隔离 | 使用全新 subagent；不要提供主 agent 的风险判断、预期结论或“应该没问题”等诱导信息。 |
| 审查范围 | 审查 `DEV_BASE..HEAD` 全部合入代码，不得抽样；比较 merge result、merge base 和两个 parents，逐调用链核对显式冲突、自动合并区、本地行为回归、迁移/配置/API 契约、控制流顺序、错误与并发、安全、计费/授权/路由类风险和测试缺口。 |
| 输出格式 | 按严重度列出 file/line、证据、影响和修复建议；最后必须明确 `SAFE TO PUSH` 或 `NOT SAFE TO PUSH`。 |
| 通过标准 | 没有可执行问题且明确 `SAFE TO PUSH`。任何 finding 都先处理或让用户明确决定，然后重测并让新的 subagent 再审。 |

## 归档字段

| 字段 | 内容 |
|---|---|
| 同步坐标 | 日期、`DEV_BASE`、备份分支、`LAST_UPSTREAM`、`MERGE_BASE`、`UPSTREAM_PIN`、tag、merge commit、最终 dev SHA。 |
| 范围 | 缺失 commit 数、非 merge commit 数、文件/行规模、功能类别概览。 |
| 决策 | 冲突文件/领域、双方意图、最终方案、用户确认记录（如有）。不维护 DEV 具体功能百科。 |
| 验证 | 实际命令及结果、A/B 基线欠账证据、静态检查、构建。 |
| 复审 | subagent 标识/结论、发现与修复闭环。 |
| 发布 | push 目标、精确 dev SHA、CI checks 结果和链接、完成时间。 |
| 回滚 | 备份分支/SHA，以及非 force 的回滚建议。 |

## 推送与 CI 门禁

| 动作 | 规则 |
|---|---|
| 更新 main | fetch 后仅使用 fast-forward；如果本地 main 有独有 commit 或被 dirty worktree 占用，停止并询问，不 checkout 覆盖用户文件。 |
| 推送分支 | subagent 通过后执行非强制 push，例如 `git push origin dev main`。推送前显示将更新的 ref 和 SHA。 |
| 识别 CI | 从仓库 workflow / branch protection / GitHub checks 确定 required checks，不凭名称猜测。 |
| 等待 | 用可用的 GitHub/`gh` 能力按 pushed dev SHA 轮询到终态；在等待期间给用户简短状态更新。 |
| CI 失败 | 读取失败日志，定位并修复；重跑本地相关测试与独立 review；推送新 SHA 后重新等待全部 checks。 |
| 完成报告 | 只有 required checks 全绿时，报告“同步完成”。否则报告“已 push，CI pending/blocked/failed”。 |

## 禁止事项

| 禁止 | 原因 |
|---|---|
| 将“固定上游 commit”解释为只 cherry-pick 一个 commit | 固定 SHA 是完整同步范围的可复现终点。 |
| 未询问就解决语义冲突 | 可能静默覆盖本地产品决策。 |
| 将冲突两侧未重叠的代码块直接全部粘贴到最终文件 | 文本不冲突不代表控制流、副作用、状态和调用契约兼容。 |
| 只 review Git 标出的冲突文件 | 自动合并区也可能发生静默语义冲突，必须纳入全量审查。 |
| 整文件采用 `--ours` / `--theirs` 而不逐段审计 | 容易丢失另一侧非冲突功能。 |
| 以“能编译、测试绿”代替代码逻辑审查 | 测试可能没有覆盖双方组合后的新路径和顺序问题。 |
| `git add -A` | 容易把用户文件、构建物或 graph/cache 产物混入提交。 |
| reset/checkout/clean 用户已有改动 | 同步任务无权删除用户工作。 |
| force push dev/main | 破坏共享分支历史。 |
| 测试未过、subagent 未明确通过就 push dev | 违反发布门禁。 |
| push 后不等 CI 或在 CI pending 时宣称完成 | 完整流程以精确 SHA 的 required CI 全绿为终点。 |
