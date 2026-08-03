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
| M1-15 | **前端斜杠命令 UI**：输入框 `/` 触发命令浮层，选择+填参，发送 | ○ | 输入 `/run ls` 正确触发后端 | M1-14 |
| M1-16 | **工作状态外置**：长任务维护 PLAN.md/PROGRESS.md/LEARNINGS.md（artifact 存储），Agent 先读再续跑 | ○ | 中断后续跑能接上 | M1-12 |
| M1-17 | **集成验证 E2E**：登录→建 workspace→选模型→多轮有记忆→/run 执行→Coder/Reviewer 协同改文件→Goal 循环到 complete→刷新历史仍在 | ○ | 全链路走通，新增 E2E 测试 | M1-04..16 |

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
