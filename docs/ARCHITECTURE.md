# goMultiAgentV2 系统架构

> 本文档给出企业级多 Agent 协作 CodeAgent 平台的完整架构视图，对标 OpenClaw + Claude 的 24h 无人值守自主工作模式。配合 [README.md](./README.md) 与 [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md) 使用。

---

## 1. 总览：三层组件

平台由「前端 Web」「后端 Server」「本地/外部 LLM 网关」三层构成，Agent 引擎基于 `trpc-agent-go v1.10.0`（仅用其 Agent 引擎层，且**业务代码只经 `internal/engine` 封装层**，框架锁版本，禁止绕过）。

```mermaid
flowchart LR
    subgraph Edge[入口 Channel]
        W[Web SPA<br/>Vue3/Naive UI]
        CLI[gmctl CLI<br/>cobra+bubbletea]
        WH[Webhook<br/>GitHub/CI]
        IM[IM 机器人<br/>飞书/钉钉/企微]
        CRON[Cron 调度器]
    end

    GW[Gateway 统一入口<br/>稳定 session_id + 串行锁]
    TEAM[Agent Team<br/>Orchestrator→Coder→Reviewer]
    TR[taskrun 后台扇出<br/>+ 外部队列/lease]
    WT[Worktree 隔离<br/>git add/commit/merge]
    EX[Executor 唯一执行出口<br/>SafeExecutor + Docker 后端]

    LLM[(LLM 网关<br/>OpenAI 兼容 /v1)]
    DB[(SQLite / 可选 PG)]
    OBS[(可观测<br/>metrics+trace+logs)]

    W & CLI & WH & IM & CRON --> GW
    GW --> TEAM
    TEAM --> TR
    TR --> WT
    TEAM --> EX
    TR --> EX
    GW -.调用.-> LLM
    TEAM -.调用.-> LLM
    GW & TEAM & TR --> DB
    GW & TEAM & TR --> OBS
```

| 组件 | 技术 | 默认端口 | 作用 |
|------|------|----------|------|
| Web | Vue3 + TS + Vite + Pinia + Naive UI | 开发 5173 / 生产 nginx | 管理后台、对话工作台、自动化、监控 |
| Server | Go 1.25 + Gin + GORM + 纯 Go SQLite（`glebarez/sqlite`，**无需 CGO**） | 8080 | 鉴权·RBAC·Agent 引擎·工具·执行器·Repo·自动化·可观测 |
| Gateway | WorkBuddy/CodeBuddy CLI 包装为 OpenAI 兼容 API | 8088 | 把本机积分或任意 OpenAI 兼容端点归一为标准 `/v1` |
| CLI | `gmctl`（独立 Go 模块） | — | 无界面环境复用同一 REST+SSE 协议 |

---

## 2. 后端分层架构

业务代码严格分层，**所有代码执行必须经 `internal/executor.Executor`，禁止散写 `os/exec`**；框架 `trpc-agent-go` 只在 `internal/engine` 层被调用。

```mermaid
flowchart TD
    API[api 层<br/>HTTP/SSE handlers<br/>DTO 适配框架类型] --> MW[middleware<br/>Auth+RBAC+CORS+Logging+限流]
    API --> ENG[engine 层<br/>trpc-agent-go 封装<br/>Chat/Stream/Goal/Plan]
    ENG --> AGENT[agent 层<br/>Orchestrator/Coder/Reviewer 工厂]
    AGENT --> TOOL[tool 层 codectool<br/>shell_exec/file_read/write/edit/grep]
    AGENT --> TASKRUN[taskrun 层<br/>后台扇出 + 队列/lease]
    TOOL --> EXEC[executor 层<br/>Executor 接口 + SafeExecutor<br/>+ Host/Docker 后端]
    API --> REPO[repo 层<br/>GORM + 版本化迁移]
    REPO --> DB[(SQLite / PG)]
    ENG --> KNOW[knowledge<br/>RAG 检索]
    ENG --> SKILL[skill<br/>warm-start 注入]
```

**关键安全约定（详见 `docs/loop/LEARNINGS.md`）**：

- `Executor.Run` / `RunCommand` 是**唯一**执行出口；`SafeExecutor` 叠加危险命令策略（无人值守默认 `deny` + 写审计）。
- 文件类工具一律经 `resolveSafePath` 做路径越界校验。
- `EXECUTOR_BACKEND=host|docker` 可切换：Docker 后端提供只读根 + 网络白名单 + 目录隔离的真·文件系统沙箱（平台自身 git 基础设施保持 Host）。

---

## 3. 自主 Loop 运行流（24h 无人值守核心）

外部触发（定时/Webhook/IM）或跨天恢复，统一经 `Gateway` 进入同一 `Runner`，以「目标契约 Team」推进到 `complete/blocked` 才停。

```mermaid
sequenceDiagram
    participant Ch as Channel(cron/webhook/IM)
    participant G as Gateway
    participant O as Orchestrator
    participant C as Coder
    participant R as Reviewer
    participant TR as taskrun
    participant WT as Worktree
    participant N as 通知中心

    Ch->>G: 触发(Request{goal_prompt, Channel})
    G->>O: Run(串行锁 + 稳定 session)
    O->>O: Goal 契约检查(未收敛则继续)
    O->>C: 委托子任务(写代码)
    C->>TR: start_task_run
    TR->>WT: git worktree add (隔离目录)
    WT-->>C: 独立工作目录
    C->>C: shell_exec/file_write (SafeExecutor)
    C-->>TR: commit
    TR->>WT: merge --no-ff → 主分支(只 merge 不 push)
    O->>R: 审阅(只读 grep/file_read)
    R-->>O: 意见/通过
    O->>O: Plan-Execute 循环直至 Goal=complete
    O-->>G: 结果
    G->>N: 完成/需检查点通知
```

**护栏（避免失控）**：`MAX_LLM_CALLS` / `MAX_TOOL_ITERATIONS` 熔断；预算超限暂停后续 LLM 调用；危险命令进 `checkpoints` 人工审批队列；无人值守模式强制 `deny` 语义命令。

---

## 4. 企业化控制平面

```mermaid
flowchart LR
    A[用户/租户] --> RBAC[RBAC<br/>role+permission]
    RBAC --> BUD[预算护栏<br/>user/session/automation/tenant/workspace]
    RBAC --> CK[人工检查点<br/>human-in-the-loop]
    RBAC --> TEN[多租户隔离<br/>结构性 SQL 隔离 + 磁盘配额]
    AUD[执行审计<br/>DBAuditor 全量落库] --> LOG[(audit_logs)]
    BUD --> LOG
    CK --> LOG
```

- **多租户**：`tenants` 表 + `users.tenant_id`；用量按 `user_id IN (SELECT id FROM users WHERE tenant_id=?)` 结构性隔离，租户 A 超限不影响 B；workspace 级磁盘配额（`ErrDiskQuotaExceeded` 写入前拒写）。
- **预算作用域**：`user` / `session` / `automation` / `tenant` / `workspace` 五级，聚合统计口径与用量记录同源（`usage_records`）。
- **连接器市场**：预置 GitHub/GitLab/Slack/Jira/Postgres/Redis/Filesystem/Fetch 等 MCP 模板，`{{KEY}}` 占位符一键导入加密落库。

---

## 5. 可观测性与进化飞轮

```mermaid
flowchart LR
    subgraph OBS[可观测]
        M[/metrics<br/>Prometheus/]
        T[trace<br/>OTel traceID 贯通]
        L[logs<br/>结构化 JSON + Loki]
    end
    subgraph FLY[进化飞轮]
        E1[evolution 扫描 transcript]
        E2[候选 SKILL.md 质量门控]
        E3[审批发布 → warm-start]
        E4[evaluation 回归 + promptiter]
    end
    OBS --> FLY
    E3 --> KNOW[(Knowledge/Skills)]
    E4 --> KNOW
```

- **可观测**：`METRICS_ENABLED` 暴露 `/metrics`（LLM/工具调用数·时延·错误率·token）；`traceID` 贯通 Gateway→Runner→工具调用；结构化 JSON 日志便于 Loki 按 trace 串联。
- **进化飞轮**：后台扫描已结束 session → 提取候选技能 → 质量门控 → 审批发布 → 新会话 warm-start 命中；新技能自动进评估集，回归不过则阻止发布。

---

## 6. 部署拓扑

```mermaid
flowchart TB
    subgraph Local[单机 / Docker Compose]
        Lw[web:nginx] --> Ls[server:8080]
        Ls --> Ldb[(SQLite 卷)]
        Lg[gateway:8088] --> Ls
    end
    subgraph K8s[Kubernetes]
        Kw[web deploy+svc] --> Ks[backend deploy+svc<br/>服务名必须为 server, 单副本]
        Ks --> Kpvc[(PVC)]
        Kmon[Prometheus+Grafana+Loki] --> Ks
        King[ingress<br/>SSE 免缓冲] --> Kw
    end
```

- **Docker Compose**：三服务一键编排（`docker-compose.yml`），适合演示与中小团队。
- **Kubernetes**：`k8s/` 提供 configmap/secret/pvc/backend+web deploy/svc/ingress/hpa；**backend 固定 1 副本**（SQLite 单文件硬约束），要横向扩多副本须先切 PG（见 PLAN M8-10 条件触发）。
- 监控告警：`monitoring/` 提供 Prometheus + Alertmanager（六类规则）+ Grafana + Loki/Promtail。

详见 [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md) 与 [docs/loop/PLAN.md](./docs/loop/PLAN.md)。
