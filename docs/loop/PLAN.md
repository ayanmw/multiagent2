# GoMultiAgentV2 — 自主推进任务清单

> 状态：○ 待做 | ⏳ 进行中 | ✅ 已完成 | ❌ 阻塞
> 自动化每轮读取本文件，选第一个 ○ 任务实现 → 验证 → commit → 标记 ✅ → STOP。

---

## M0 骨架（已完成 ✅，保留供审计）

M0-01 ~ M0-19 全部 ✅（Auth / Provider·Model / AG-UI SSE 流式 / Session 持久化 / 前端登录·管理·对话工作台 / 集成验证）。详见 `docs/loop/PROGRESS.md` 与 `docs/03-M1规划与M0评审.md`。

### M0 评审发现的真实缺陷（已在 M1 修复）
- **P0 多轮记忆**：DB 历史从未回灌模型，模型第二句起失忆 → 由 **M1-01** 修复。
- **P1 RBAC 空转**：`RequirePermission` 未接路由 → 由 **M1-02** 修复。
- **P1 SessionKey 跨用户碰撞** → 由 **M1-03** 修复。

---

## M1 CodeAgent 核心（当前阶段）

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| M1-01 | **多轮记忆修复**：接入框架持久化 SessionService（或后端从 DB `ListSessionMessages` 回灌 engine 多轮消息），使同一 session 多轮对话模型可见历史 | ○ | 连续两轮对话，第二轮能正确引用第一轮实体 | M0 |
| M1-02 | **RBAC 落地**：Provider 写/Session 删除/模型启用等敏感路由加 `RequirePermission` 中间件链 | ○ | viewer 调 DELETE /api/providers/:id → 403；developer 正常 | M0 |
| M1-03 | **SessionKey 唯一**：`UNIQUE(user_id, session_key)` 约束 + `GetOrCreateSession` 冲突处理 | ○ | 跨用户复用 key 不新建重复行 | M0 |
| M1-04 | **Executor 抽象接口**：定义 `Executor.Run(ctx, cmd) → (stdout, stderr, exitCode)`；`HostExecutor`（cwd 约束）实现 | ○ | 单测覆盖正常/超时/cwd 越界 | M0 |
| M1-05 | **危险命令策略**：前缀黑名单（rm -rf /、git push --force 等）+ 策略枚举 allow/ask/deny，无人值守默认 deny 并写审计 | ○ | 命中黑名单命令被拒并写审计 | M1-04 |
| M1-06 | **CodeAct 工具集**：基于 Executor 实现 `shell_exec` + `file_read/file_write/file_edit`，注册进 engine | ○ | Agent 执行 `ls` 返回结果；读写文件成功 | M1-04 |
| M1-07 | **Workspace 模型**：User 下 Workspace（本地目录 + 可选 git remote），对话绑定 workspace，Executor 在其目录执行；DB 模型 + CRUD API | ○ | 建 workspace→对话绑定→shell 在正确目录执行 | M1-04 |
| M1-08 | **子代理委托 agenttool**：Coder 子代理（带代码工具集）可由 Orchestrator 委托；定义 agent 工厂 | ○ | Orchestrator 委托 Coder 写文件成功 | M1-06/07 |
| M1-09 | **CodeTeam 编排**：Orchestrator→Coder(写)→Reviewer(只读，独立挑错)→回环；team 配置化 | ○ | 一轮内产出代码并被 review 指出问题 | M1-08 |
| M1-10 | **Reviewer 只读工具集**：reviewer 仅 read/grep，无 write/exec | ○ | reviewer 调 write 被拒 | M1-08 |
| M1-11 | **Goal 契约**：goal 扩展注入 get_goal/create_goal/update_goal，Orchestrator 必须推进到 complete/blocked 才结束 | ○ | Agent 不能过早给 final；未达成时继续 | M1-09 |
| M1-12 | **CycleAgent / Plan-Execute**：planner 产出计划外置 PLAN/PROGRESS，逐项执行更新 | ○ | 中型任务能拆计划并逐步完成 | M1-11 |
| M1-13 | **护栏熔断**：`WithMaxLLMCalls/WithMaxToolIterations/WithMaxRetries` 配置 + 运行级兜底；暴露到 Agent 配置表 | ○ | 超限后优雅终止并产出 partial 结果 | M1-11 |
| M1-14 | **斜杠命令注册表（后端）**：Command 元数据（name/desc/args/handler 或 prompt 模板），`GET /api/commands` 下发；内置 /clear /model /workspace /run /review /plan | ○ | 前端/CLI 共用，新增命令只改后端 | M0 |
| M1-15 | **前端斜杠命令 UI**：输入框 `/` 触发命令浮层，选择+填参，发送 | ○ | 输入 `/run ls` 正确触发后端 | M1-14 |
| M1-16 | **工作状态外置**：长任务维护 PLAN.md/PROGRESS.md/LEARNINGS.md（artifact 存储），Agent 先读再续跑 | ○ | 中断后续跑能接上 | M1-12 |
| M1-17 | **集成验证 E2E**：登录→建 workspace→选模型→多轮有记忆→/run 执行→Coder/Reviewer 协同改文件→Goal 循环到 complete→刷新历史仍在 | ○ | 全链路走通，新增 E2E 测试 | M1-01..16 |

---

## 阻塞与依赖

- M1-01/02/03 为 M0 缺陷修复，无前置，可最先做
- M1-04 → M1-05/06/07（Executor 是基础）
- M1-06/07 → M1-08（工具 + 工作区才能支撑子代理）
- M1-08 → M1-09/10（CodeTeam）
- M1-09 → M1-11（Goal 装在 Orchestrator 上）
- M1-11 → M1-12/13（循环 + 护栏）
- M1-14 → M1-15（命令注册表 → 前端 UI）
- M1-12 → M1-16（状态外置）
- M1-01..16 → M1-17（集成验证）
