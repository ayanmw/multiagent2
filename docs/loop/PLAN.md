# GoMultiAgentV2 — 自主推进任务清单

> 状态：○ 待做 | ⏳ 进行中 | ✅ 已完成 | ❌ 阻塞
> 自动化每轮读取本文件，选第一个 ○ 任务实现 → 验证 → commit → 标记 ✅ → STOP。
> **阶段门槛：必须 M0.5 全部 ✅ 后，才允许开始 M1 任务。**

---

## M0 骨架（已完成 ✅，保留供审计）

M0-01 ~ M0-19 全部 ✅（Auth / Provider·Model / AG-UI SSE 流式 / Session 持久化 / 前端登录·管理·对话工作台 / 集成验证）。详见 `docs/loop/PROGRESS.md` 与 `docs/03-M1规划与M0评审.md`。

---

## M0.5 缺陷修复（当前阶段 — M1 的硬前置门槛）

> 来源：`docs/03-M1规划与M0评审.md` 第一节评审发现的全部问题与风险（P0/P1/P2）。
> **规则：本阶段任务未全部 ✅ 之前，禁止开始任何 M1 任务。**

| # | 严重度 | 任务 | 状态 | 验证标准 | 依赖 |
|---|--------|------|------|----------|------|
| M0.5-01 | **P0** | **多轮记忆修复**：接入 trpc-agent-go 持久化 SessionService（框架 `session` 包 SQLite 后端注入 Runner）；若 v1.10.0 签名不友好，退化为「后端从 DB `ListSessionMessages` 加载历史 → 构造 `[]model.Message` 多轮传入 Runner」。修复点：`engine.go:60-63` 每请求新建内存 Runner、`sse.go:115`/`chat.go:83` 只传单条消息 | ✅ | 连续两轮对话，第二轮能正确引用第一轮提到的实体（新增自动化测试覆盖） | 无 |
| M0.5-02 | **P1** | **RBAC 落地**：`middleware.RequirePermission(resource, action)` 接入所有敏感路由——Provider 创建/更新/删除、Model 启用/禁用、Session 删除、APIKey 管理；`main.go` 路由注册处成链 | ✅ | viewer 角色调 `DELETE /api/providers/:id` → 403；developer 正常；新增权限测试 | 无 |
| M0.5-03 | **P1** | **SessionKey 唯一约束**：加 `UNIQUE(user_id, session_key)`（GORM 复合唯一索引 + 迁移），`repo/session.go GetOrCreateSession` 用 upsert/冲突处理消除竞态 | ✅ | 不同用户可用相同 key；同一用户重复 key 不新建重复行；并发调用不产生脏数据 | 无 |
| M0.5-04 | P2 | **delta 累加逻辑去重**：`engine.Chat`（engine.go:99）与 `aguiConverter.Convert`（sse.go:164）重复实现的增量累加规则抽成公共函数（如 `internal/engine/delta.go`），两处复用 | ✅ | 单测覆盖「有 delta / 无 delta 终帧 / 混合」三种流；行为与原先一致 | 无 |
| M0.5-05 | P2 | **消除魔法值**：`engine.go:73` 90s 超时提为配置项（env/config，默认 90s）；`auth.go:92` 硬编码 `RoleID=3` 改为按名称查询 `RoleDeveloper`；顺手扫全仓库其余魔法数字 | ✅ | 配置可改超时生效；空库初始化注册用户角色正确；go vet/test 绿 | 无 |
| M0.5-06 | P2 | **SSE 消息改 POST**：`sse.go:40` message 从 GET query 移到 POST body（避免明文进访问日志）；前端 `web/src/api/chat.ts`/`ChatView.vue` 同步改为 fetch-POST 流式读取 | ✅ | 对话功能不回归（流式逐字正常）；GET query 不再含 message；npm build 绿 | 无 |
| M0.5-07 | — | **M0.5 回归验证与结项**：`go build/vet/test ./...` + `cd web && npm run build` 全绿；扩展 E2E 覆盖多轮记忆/RBAC 403/SessionKey 唯一；在 PROGRESS.md 写「M0.5 结项报告」（逐条缺陷 → 修复 commit 对照表） | ✅ | 全部验证绿；结项报告落盘；此后方可进入 M1 | M0.5-01..06 |

---

## 循环执行补充约定（M1 起生效，每轮必读）

> M1 阶段的执行补充指引，循环每轮读取本文件时应一并遵守。

### 1. M1 任务出处
- M0.5 任务读 `docs/03` 第一节（缺陷 file:line）；**M1 任务读 `docs/03` 第二节（2.2 任务拆分 / 2.4 技术要点与框架风险）+ `docs/02`（框架能力全景与自主化映射）+ `LEARNINGS.md`**。M1 详细设计以 §2 为权威，PLAN.md M1 行是其精简派生。

### 2. M1 包与框架 API 约定
- 分层新增：`internal/executor`（✅ M1-04 已建）、`internal/tool`（CodeAct：shell_exec/file_read/file_write/file_edit）、`internal/agent`（Orchestrator/Coder/Reviewer 工厂）。
- 工具统一用 `tool/function.NewFunctionTool[I,O]`（见 LEARNINGS 引擎封装），注册进 `llmagent.WithTools`。
- **M1-11 Goal**：先验框架是否有 `goal` 包；v1.10.0 若无则降级为自定义工具集 `get_goal/create_goal/update_goal`，只挂 Orchestrator，不开 EnableParallelTools。
- **M1-12 CycleAgent/Plan-Execute**：优先框架 `graph`（loop 边）或自写 planner→executor for-loop，不依赖 M2 taskrun。
- 所有代码执行必须经由 `executor.SafeExecutor`（危险命令策略包装），禁止裸用 `HostExecutor.Run`（见 LEARNINGS 2026-07-29）。

### 3. M1 集成测试指引（Agent 协作 / Goal / 循环类任务）
- M1-08/09/10/11/12 验收无法纯单测，须复用 `cmd/server` 现有 mock LLM 桩（`buildRouter`/`newRBACRouter`/`e2eClient`，参考 `TestM0_Integration_E2E`、`rbac_test.go`）构造**脚本化工具调用序列**：Coder 写文件、Reviewer 调 write 被拒、Goal 未达成继续。不调真实 LLM。

### 4. M1-16 命名消歧
- M1-16 的 `PLAN.md/PROGRESS.md/LEARNINGS.md` 是**每次 run 下的 artifact 文件**（存于 workspace/run 目录或 DB artifact 表），**绝非**本仓库 `docs/loop/PLAN.md` 等循环控制文件；Agent 写状态文件时不得触碰 `docs/loop/`。

---

## M1 CodeAgent 核心（门槛：M0.5 全部 ✅ 后才可开始）

> 原 M1-01/02/03（缺陷修复）已上移为 M0.5-01/02/03，编号保留不复用。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M1-04 | **Executor 抽象接口**：定义 `Executor.Run(ctx, cmd) → (stdout, stderr, exitCode)`；`HostExecutor`（cwd 约束）实现 | ✅ | 单测覆盖正常/超时/cwd 越界 | M0.5 |
| M1-05 | **危险命令策略**：前缀黑名单（rm -rf /、git push --force 等）+ 策略枚举 allow/ask/deny，无人值守默认 deny 并写审计 | ✅ | 命中黑名单命令被拒并写审计 | M1-04 |
| M1-06 | **CodeAct 工具集**：基于 Executor 实现 `shell_exec` + `file_read/file_write/file_edit`，注册进 engine | ✅ | Agent 执行 `ls` 返回结果；读写文件成功 | M1-04 |
| M1-07 | **Workspace 模型**：User 下 Workspace（本地目录 + 可选 git remote），对话绑定 workspace，Executor 在其目录执行；DB 模型 + CRUD API | ✅ | 建 workspace→对话绑定→shell 在正确目录执行 | M1-04 |
| M1-08 | **子代理委托 agenttool**：Coder 子代理（带代码工具集）可由 Orchestrator 委托；定义 agent 工厂 | ✅ | Orchestrator 委托 Coder 写文件成功 | M1-06/07 |
| M1-09 | **CodeTeam 编排**：Orchestrator→Coder(写)→Reviewer(只读，独立挑错)→回环；team 配置化 | ✅ | 一轮内产出代码并被 review 指出问题 | M1-08 |
| M1-10 | **Reviewer 只读工具集**：reviewer 仅 read/grep，无 write/exec | ✅ | reviewer 调 write 被拒 | M1-08 |
| M1-11 | **Goal 契约**：goal 扩展注入 get_goal/create_goal/update_goal，Orchestrator 必须推进到 complete/blocked 才结束 | ✅ | Agent 不能过早给 final；未达成时继续 | M1-09 |
| M1-12 | **CycleAgent / Plan-Execute**：planner 产出计划外置 PLAN/PROGRESS，逐项执行更新 | ✅ | 中型任务能拆计划并逐步完成 | M1-11 |
| M1-13 | **护栏熔断**：`WithMaxLLMCalls/WithMaxToolIterations/WithMaxRetries` 配置 + 运行级兜底；暴露到 Agent 配置表 | ✅ | 超限后优雅终止并产出 partial 结果 | M1-11 |
| M1-14 | **斜杠命令注册表（后端）**：Command 元数据（name/desc/args/handler 或 prompt 模板），`GET /api/commands` 下发；内置 /clear /model /workspace /run /review /plan | ✅ | 前端/CLI 共用，新增命令只改后端 | M0.5 |
| M1-15 | **前端斜杠命令 UI**：输入框 `/` 触发命令浮层，选择+填参，发送 | ✅ | 输入 `/run ls` 正确触发后端 | M1-14 |
| M1-16 | **工作状态外置**：长任务维护 PLAN.md/PROGRESS.md/LEARNINGS.md（artifact 存储），Agent 先读再续跑 | ✅ | 中断后续跑能接上 | M1-12 |
| M1-17 | **集成验证 E2E**：登录→建 workspace→选模型→多轮有记忆→/run 执行→Coder/Reviewer 协同改文件→Goal 循环到 complete→刷新历史仍在 | ✅ | 全链路走通，新增 E2E 测试（引擎层实跑通过 + HTTP 层类型检查通过） | M1-04..16 |

---

## M2 生态（门槛：M1 全部 ✅ 后才可开始）

> M2 定位「生态」：把单 Agent CodeAgent 升级为多连接器 / 多技能 / 可后台扇出 / 隔离并行的平台。
> 框架实测 v1.10.0 已具备 `tool/mcp`(stdio/sse/streamable)、`tool/taskrun`(start/list/get/cancel/wait/transcript)、`plugin/toolsearch`、`skill`、`knowledge`、`artifact` 包；**仅缺持久化 session 后端**。故 M2 优先复用框架工具，session 持久化自研。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M2-01 | **Git 基础 & workspace 绑定**：workspace 自动 `git init`；Git 工具集（git_status/git_diff/git_commit/git_log/git_branch），全部经 `executor.SafeExecutor`（无人值守 deny，正常 git 子命令放行）；对话/任务变更可显式 git_commit 落盘 | ✅ | 建 workspace→自动 init→Coder 写文件→git_commit 成功→git_status 干净、git_diff 显示改动；正常 git 子命令不被安全策略误伤 | 无 |
| M2-02 | **MCP 管理中心（后端）**：model.MCPServer（user 归属 + name + transport[stdio/sse/streamable] + command/args/env 或 url/headers + 启用）；repo owner-scoped CRUD；api（POST/GET/PUT/DELETE /api/mcp，写接 mcp:write/读 mcp:read）；seedRoles 补权限；仅管理面+配置持久化+校验，不在此装载工具 | ✅ | 建 MCP 配置→列表/详情/更新/删除 owner 隔离；viewer 写 403；非法 transport/缺必填 400；无真实装载 | 无 |
| M2-03 | **Skills 仓库 & warm-start**：复用框架 `skill` 包（FSRepository，本地共享 `skills/` + 用户私有 `data/skills/<uid>/`）；管理 API（列/读/建/更新 SKILL.md，owner 隔离）；引擎层 warm-start：会话开始按 workspace/关键词检索相关 SKILL.md 注入 Orchestrator 系统上下文（控长）。**不含**自动提取（evolution 留 M5） | ✅ | 写示例 SKILL.md→列表可见→新会话注入系统提示→模型遵循；用户私有技能不串；warm-start 注入长度可控 | 无 |
| M2-04 | **taskrun 后台任务控制面 + 持久化 session**：① 自研 SQLite 持久化 session service（落盘 child session 事件/transcript，跨重启保留，顺带缓解 M0.5-01 跨重启记忆）；② 接线框架 `tool/taskrun.Tools` 到 Orchestrator（`WithSessionService` 持久化 + `WithDefaultAgentName` 默认 worker=Coder/team）；③ 与 M1 的 Goal/Plan/State 扩展兼容（子任务可带）；④ 提供 taskrun 列表/详情/取消 API | ✅ | Orchestrator 调 start_task_run→派生子任务独立 child session→子任务写文件→get_task_run 显示状态→read_task_run_transcript 读回；进程重启后 transcript 仍在（验证持久化 session） | M2-01 |
| M2-05 | **Worktree 隔离**：每个 taskrun 派生时 hook `git worktree add <dir> -b taskrun/<id>` 独立目录→子代理 Executor workdir 指向该 worktree→完成 `git commit`→回主分支 `git merge`（或保留分支）；冲突交 Reviewer/人工；**只 merge 不直接 push 远程**（用户 2026-08-03 确认）。Worktree 管理器负责 add/cleanup | ✅ | 并发两个 taskrun→各自独立 worktree→互不改主分支文件→各自 commit→主分支 merge 含两分支改动；主分支中途未被污染 | M2-01, M2-04 |
| M2-06 | **toolsearch 延迟工具箱**：将 M2-02 接入的 MCP server 工具 + 内置工具注册为命名空间工具箱；暴露 `tool_search` + `call_tool` 双控制工具给 Orchestrator；按需（名称/关键字）装载，避免全量注入 context 膨胀（用户 2026-08-03 确认：命名空间/关键字，非语义嵌入）。MCP 动态工具箱命名 `mcp__<server>__<tool>` | ✅ | 接入一个 MCP server（mock stdio）→默认不装载其全部工具→模型 tool_search 找到→call_tool 执行→结果返回；context token 不随 MCP 工具数线性膨胀 | M2-02 |

---

## M3 企业化（门槛：M2 全部 ✅ 后才可开始）

> M3 目标：把「能用的 CodeAgent」升级为「可运营、可审计、可控成本」的企业平台。
> 重点：可观测/审计、配额与预算护栏、人工检查点、artifact 规范化存储。
> 复用：M1-05 `SafeExecutor` 已有 `Auditor` 接口（Memory/Log）、M1-16 已有 `artifact.Store`；M3 把二者落库并开放 UI。详细映射见 `docs/02` §6「M3 企业化」。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M3-01 | **执行审计落库**：`SafeExecutor` 的 `Auditor` 实现 `DBAuditor`，将每次 `Run`（命令/workdir/decision/reason/owner/时间戳）写入 `audit_logs` 表；CodeAct 工具、Git 工具、taskrun 子任务统一经 `SafeExecutor` 调用确保全量覆盖；迁移建表 | ✅ | 单测覆盖 allow/deny/ask 三类均写审计；curl 跑一条 shell → `GET /api/audit` 可见该记录（owner 隔离） | M1-05 |
| M3-02 | **审计日志 API + 前端页**：`GET /api/audit`（分页 / 按用户 / 决策 / 时间筛选，接 `audit:read`；viewer 仅看自己）；前端 `AuditView` 表格 + 筛选 | ✅ | developer 看全员、viewer 只看自己；筛选生效；`vue-tsc` 通过 | M3-01 |
| M3-03 | **Token/费用计量**：对话/SSE 完成后记录 token 用量（prompt/completion/total）到 `usage_records`（按 session/user/provider 归属）；优先读上游 `usage`（网关/OpenAI 响应），无则本地估算；`GET /api/usage` 聚合 | ✅ | 一次对话后 `usage_records` 有行；`/api/usage` 返回累计；前端可展示 | M2 |
| M3-04 | **预算护栏（平台级）**：`BudgetPolicy`（按 user / session / automation 三级阈值，env/DB 配置）；超限暂停该 session/automation 的后续 LLM 调用并写审计 + 触发通知；`GET/PUT /api/budgets` 管理 | ✅ | 设极低阈值跑对话 → 第二轮被拦并返回「预算耗尽，待恢复」；管理员提额后恢复 | M3-03 |
| M3-05 | **人工检查点（human-in-the-loop）**：危险命令 `ask` 模式（M1-05）在无人值守下不直 deny，而是生成 `checkpoints` 记录（待审批任务 + 上下文）并暂停；`POST /api/checkpoints/:id/resolve{approve,reject,comment}` 后续跑或中止；前端审批列表 | ✅ | 触发需审批危险操作 → 生成 checkpoint → 前端 approve 后执行、reject 后中止；审计留痕 | M3-01 |
| M3-06 | **Artifact 浏览器**：前端 `ArtifactView` 浏览某 session 下全部 artifact（`PLAN/PROGRESS/LEARNINGS` + 报告/diff/构建产物），列表 + 查看 + 下载；新增后端 `GET /api/sessions/:id/artifacts`（List/Read，复用 M1-16 `artifact.Store`） | ✅ | 有产物的会话能看到文件列表并查看内容；与「运行态面板」互补（面板看三核心文件，浏览器看全部） | M1-16 |
| M3-07 | **MCP 敏感字段加密**：`mcp_servers` 的 `env`/`headers`（含 token）改用 AES-256-GCM 加密存储（对齐 Provider `api_key_enc`），读取时解密；前端编辑明文传参、列表不回显明文 | ✅ | 库内 env 为密文；正常装载仍解密可用；越权读取拿不到明文 | M2-02 |
| M3-08 | **手动 DB 迁移机制**：引入迁移方案（版本表 `schema_migrations` + 基线 migration，取代纯 `AutoMigrate` 仅用于开发）；首次启动执行基线，后续变更走 migration 文件；保留 `AutoMigrate` 仅作开发 fallback | ✅ | 全新库启动执行基线后表结构与当前一致；新增字段走新 migration 不靠 AutoMigrate | M0/M0.5 |
| M3-09 | **可观测性 telemetry**：接入框架 `telemetry`（OpenTelemetry），导出指标（LLM 调用数/时延/错误率、工具调用数/失败率、token 用量）到日志或 `/metrics`（Prometheus 格式）；前端「运行监控」概览卡片 | ✅ | 跑几轮对话后 `/metrics` 有指标；前端概览展示最近调用/失败率 | M3-03 |
| M3-10 | **集成验证 E2E（企业化）**：登录 → 执行命令 → 审计可见 → 超预算暂停 → 人工检查点审批 → artifact 浏览 → 指标可见 全链路；补 `audit`/`usage`/`budget`/`checkpoint` 单测与 HTTP 层测试 | ✅ | 全链路走通；新增测试绿 | M3-01..09 |

---

## M4 自主化（门槛：M3 全部 ✅ 后才可开始）

> M4 目标：让平台从「人发消息→回复」升级为「定时/事件/自触发 → 自动 Loop 推进到完成 → 跨天恢复」。
> 依托：M1 Goal 契约 + M1-16 状态外置 + M2 taskrun/Worktree + M3 预算/检查点护栏。映射见 `docs/02` §4/§5（L4/L5 + 四层架构）。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M4-01 | **Automation 数据模型与持久化**：`model.Automation`（name/owner/cron_expr 或 webhook 规则/goal_prompt/enabled/last_run/next_run），repo owner-scoped CRUD；seed 权限 `automations:write/read` | ✅ | 建/查/改/删 owner 隔离；viewer 写 403 | M3 |
| M4-02 | **Cron 调度器**：常驻 goroutine 加载启用的 Automation，按 cron 算 `next_run` 持久化；到点创建 Goal Session（带 goal_prompt）启动 Loop；失败重试 + 写审计 | ✅ | 设 `*/1 * * * *` 测试 Automation → 下一分钟自动建 session 跑 Loop → 产出结果；`next_run` 正确更新 | M4-01, M1-11, M1-16 |
| M4-03 | **Webhook 入口**：`POST /api/webhooks/:token` 接收外部事件（GitHub Issue/PR、CI 状态等）→ 匹配 Automation webhook 规则 → 触发 Loop；token 校验 + 速率限制 | ✅ | curl 打 webhook → 对应 Automation 触发 Loop；非法 token 401 | M4-01 |
| M4-04 | **Channel 层抽象**：统一入口（Web 对话 / CLI / Webhook / 定时）全部经 `Gateway`（稳定 `session_id` + 每会话串行锁）进同一 `Runner`；抽 `Channel` 接口便于扩展 IM/邮件 | ✅ | 同一 Goal 从不同 Channel 进入都走统一 Gateway 串行锁，不串会话 | M2, M4-02, M4-03 |
| M4-05 | **跨天恢复**：进程重启/中断后，扫描「未收敛 Goal Session」（artifact 状态非 complete/blocked）→ 读 PLAN/PROGRESS/LEARNINGS → 重建上下文续跑；与 M2-04 持久化 session 协同 | ✅ | 跑长 Loop 中途 kill 后端 → 重启 → 恢复任务自动续跑且接续已有进展（不重头） | M1-16, M2-04 |
| M4-06 | **无人值守 Loop 运行模式**：配置 `Mode=Unattended`（SafeExecutor deny 默认 + 预算护栏 + 检查点排队）；长任务自动推进到 `complete/blocked` 才停，无需人盯 | ✅ | 多步 Goal 在无人值守下自动跑完并产出 PR/报告；中途危险操作进检查点队列待批 | M3-04, M3-05, M4-04 |
| M4-07 | **通知/结果回发（outbound）**：Loop 完成/暂停/需检查点时经 outbound 路由通知（站内信表 `notifications` + Webhook 回调占位 + 邮件占位）；前端通知中心 | ✅ | Loop 完成 → 通知中心出现一条；webhook 目标收到回调（可用 mock） | M4-02 |
| M4-08 | **自动化管理前端**：`AutomationView` 列表/创建（cron 表达式 / 事件规则 / goal prompt）/启用停用/运行历史；检查点审批列表（复用 M3-05） | ✅ | 前端可建 cron Automation 并看到下次运行时间；运行历史可查 | M4-01, M4-07 |
| M4-09 | **集成验证 E2E（自主化）**：建定时 Automation → 到点自动跑 Loop → 产出 PR/报告 → 跨重启恢复 → 完成通知 全链路；补 cron/webhook/恢复单测 | ✅ | 全链路走通；测试绿 | M4-01..08 |

---

## M5 进化（门槛：M4 全部 ✅ 后才可开始）

> M5 目标：平台「越用越聪明」+ 多端可达 + 对外输出 Agent 能力。
> 依托：M2 Skills warm-start + M4 自主 Loop 产生的 transcript。映射见 `docs/02` §1.2（evolution/knowledge/evaluation/A2A）。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M5-01 | **CLI 骨架**：Go CLI（cobra + bubbletea）登录/对话/查看会话/查看任务，复用 REST+SSE API；两端共用协议 | ✅ | `cli chat` 能登录并对同一后端发消息拿到流式回复；`cli sessions` 列出会话 | M3 |
| M5-02 | **Knowledge RAG**：接入框架 `knowledge` 包（源加载→切片→向量化→检索）；`model.KnowledgeBase` + CRUD API + 前端管理；对话时按 workspace/关键词检索注入上下文（控长） | ✅ | 建知识库 → 上传/索引文档 → 新会话检索到相关内容并注入；向量库先用本地（sqlite/bolt），留 PG/pgvector 升级位 | M2 |
| M5-03 | **evolution 技能飞轮（后端）**：后台异步扫描已结束 session transcript → LLM 提取候选 `SKILL.md`（name/描述/步骤）→ 质量门控（长度/结构/去重）→ 写 `skill_candidates` 待审批；不自动发布 | ✅ | 跑完典型任务 → evolution 扫描 → 生成一条候选技能；质量门控拦截空泛候选 | M4, M2-03 |
| M5-04 | **evolution 前端 + 审批发布**：`EvolutionView` 候选列表/预览/审批（approve→发布为托管技能进 `skills/` 共享库；reject→丢弃）；发布后自动进入 warm-start 复用 | ✅ | 前端审批一条候选 → 技能进共享库 → 新会话可 warm-start 命中；与 M2-03 衔接 | M5-03, M2-03 |
| M5-05 | **evaluation 回归**：评估集管理（case：prompt/输入/期望/评分器）API + 运行（多次跑取稳定分）；指标（精确/召回/自定义 LLM 评分）；CLI/API 触发回归 | ✅ | 建评估集 → 跑回归 → 出分数报告；模型/Prompt 改动前后分数可对比 | M4 |
| M5-06 | **promptiter 优化**：GEPA 反射式 Prompt/技能优化——跑 eval → 定位弱项 → 生成改进建议 → 应用（写回 Agent 指令/SKILL.md）→ 再 eval 验证 | ✅ | 一轮优化后相关 eval 分数提升或持平；建议可读、可回滚 | M5-05 |
| M5-07 | **A2A 对外协议**：平台作为 A2A server 对外暴露 Agent 能力（接收外部 Agent 任务、返回结果）；或作 client 调用外部 A2A Agent；最小可用端点 + 前端连接配置 | ✅ | 外部 A2A client 能向本平台发起一个任务并拿到结果（用 mock client 验证） | M4 |
| M5-08 | **飞轮 × 回归联动**：新发布技能自动进 eval 集；回归不过则阻止发布并提示修订 | ✅ | 发布会破坏现有能力的技能 → 回归拦截 → 不发布 | M5-04, M5-05 |
| M5-09 | **集成验证 E2E（进化）**：CLI 对话 → RAG 检索 → evolution 提取并审批技能 → evaluation 回归 → promptiter 优化 全链路；补关键单测 | ○ | 全链路走通；测试绿 | M5-01..08 |

---

## MX 质量加固与遗留（可穿插拾取，不阻塞里程碑）

> 覆盖 M0-M2 已实现但「深度不足 / 缺测试 / 待企业化」的收尾项。可在任意阶段由 LOOP 或人工优先处理（建议 M3 进行中穿插 MX-05）。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| MX-01 | **前端深度打通-工作区**：对话页支持选择/切换 Workspace 并绑定当前会话（Workspace 模型 API 已有，但未与对话绑定 UI 接通） | ○ | 对话页下拉选 workspace → 消息在该目录执行 → 刷新后仍绑定 | M1-07 |
| MX-02 | **前端深度打通-MCP**：MCP 页「测试连接/装载校验」按钮（调 toolsearch 实际装载一个工具验证配置可用） | ○ | 配可用 MCP → 点测试 → 返回工具列表；配错 → 明确报错 | M2-02, M2-06 |
| MX-03 | **前端深度打通-Skills**：Skills 页支持新建/编辑 SKILL.md（目前仅浏览），owner 隔离 | ○ | 新建示例技能 → 列表可见 → warm-start 命中 | M2-03 |
| MX-04 | **前端深度打通-任务中心**：TaskCenter 渲染 transcript（目前仅列表/详情）、「取消」按钮实测接 `cancel_task_run` | ○ | 起后台任务 → 详情看到 transcript 流 → 取消生效 | M2-04 |
| MX-05 | **后端测试补全**：为 M2 各包（taskrun/worktree/mcp/skills/toolsearch）补单测 + 集成测试（目前主要靠手动 verify） | ○ | `go test ./internal/...` 各 M2 包有测试且绿 | M2 |
| MX-06 | **用户管理后台（admin）**：管理员创建/禁用/重置用户、查看用户列表与配额；前端 `AdminView` | ○ | admin 创建用户 → 该用户可登录；禁用后无法登录 | M0, M3-04 |
| MX-07 | **安全加固**：API 速率限制（登录/对话防刷）、CORS 精细化（仅放行前端域）、敏感信息不出日志 | ○ | 高频登录被限流；OPTIONS 预检通过且跨域仅白名单 | M0 |
| MX-08 | **部署与文档**：`README` 补全（架构/启动/配置）、`docker-compose.yml`（前端+后端+网关）、`.env.example`、前端 `npm run build` 产物说明 | ○ | 新人按 README 能跑起；`docker-compose up` 起三服务 | M0-M2 |

---

## 阻塞与依赖

- **阶段门槛**：M0.5-01..07 全部 ✅ 之前，任何 M1 任务不得开始（循环按「第一个 ○」自然保证，同时这是硬规则）
- M0.5-01/02/03/04/05/06 相互独立，可按顺序逐轮完成；M0.5-07 依赖前六项
- M1-04 → M1-05/06/07（Executor 是基础）
- M1-06/07 → M1-08（工具 + 工作区才能支撑子代理）
- M1-08 → M1-09/10（CodeTeam）
- M1-09 → M1-11（Goal 装在 Orchestrator 上）
- M1-11 → M1-12/13（循环 + 护栏）
- M1-14 → M1-15（命令注册表 → 前端 UI）
- M1-12 → M1-16（状态外置）
- M0.5 + M1-04..16 → M1-17（集成验证）
- M2-01 / M2-02 / M2-03 相互独立，可按顺序逐轮完成
- M2-01 → M2-04（taskrun 子任务产物需 git 管理）
- M2-01 + M2-04 → M2-05（Worktree 隔离依赖 git 基础 + taskrun 派生点）
- M2-02 → M2-06（toolsearch 依赖 MCP 接入）
- **M3 门槛**：M2 全部 ✅ 后才可开始 M3（当前已满足）
- **M4 门槛**：M3-01..10 全部 ✅ 后才可开始 M4（自主化依赖企业化护栏兜底）
- **M5 门槛**：M4-01..09 全部 ✅ 后才可开始 M5（进化飞轮依赖自主 Loop 产生 transcript）
- **MX**：质量加固项不阻塞里程碑，可由 LOOP 或人工在任意阶段优先拾取（建议 M3 进行中穿插 MX-05 测试补全）

- **阶段门槛**：M0.5-01..07 全部 ✅ 之前，任何 M1 任务不得开始（循环按「第一个 ○」自然保证，同时这是硬规则）
- M0.5-01/02/03/04/05/06 相互独立，可按顺序逐轮完成；M0.5-07 依赖前六项
- M1-04 → M1-05/06/07（Executor 是基础）
- M1-06/07 → M1-08（工具 + 工作区才能支撑子代理）
- M1-08 → M1-09/10（CodeTeam）
- M1-09 → M1-11（Goal 装在 Orchestrator 上）
- M1-11 → M1-12/13（循环 + 护栏）
- M1-14 → M1-15（命令注册表 → 前端 UI）
- M1-12 → M1-16（状态外置）
- M0.5 + M1-04..16 → M1-17（集成验证）
- M2-01 / M2-02 / M2-03 相互独立，可按顺序逐轮完成
- M2-01 → M2-04（taskrun 子任务产物需 git 管理）
- M2-01 + M2-04 → M2-05（Worktree 隔离依赖 git 基础 + taskrun 派生点）
- M2-02 → M2-06（toolsearch 依赖 MCP 接入）
