# GoMultiAgentV2 — 项目复盘与未来开发计划（M6+）

> 复盘日期：2026-08-17
> 复盘范围：M0 / M0.5 / M1 / M2 / M3 / M4 / M5 / MX 全量已完成（PLAN.md 全部 ✅）
> 目的：在「里程碑全部达成」的节点上，做一次真实代码复盘，识别可运营前的真短板，并给出 M6 起的未来开发计划。
> 方法：纯只读审计（Explore 子代理摸底 + 关键项人工复核 + `go build` 实测），不改任何代码。

---

## 一、现状复盘（结论先行）

**整体判断：这是一份「文档基本被代码兑现」的成熟代码库，不是 PPT 工程。**

- **里程碑**：M0(19) → M0.5(07) → M1(04~17) → M2(01~06) → M3(01~10) → M4(01~09) → M5(01~09) → MX(01~08)，约 70 个任务，**全部 ✅**。
- **代码规模**：`server/internal` 217 个 `.go` 文件（约 3.5 万行非测试代码）；`server` 含 80+ 测试文件、369 个 `Test` 函数；`web/src` 19 个路由视图全部接入；`tool/cli` 独立 Go 模块（cobra+bubbletea，**已 `go build` 通过**）。
- **五大支柱扎实**：`engine`（框架封装）、`agent`（CodeTeam/Goal/Plan/State 扩展）、`tool`（CodeAct+Git+只读集）、`executor`（SafeExecutor+危险命令策略）、`api`（53 文件 handler 群）。
- **合规信号良好**：无裸 `os/exec`（仅 `executor/host.go` 内部与编译测试）；无密钥明文泄露；无 `not implemented` 死桩；框架锁定 `v1.10.0` 且业务代码经 `engine` 层收敛。

### 已人工复核澄清的「误报项」（避免被误导）
| 初步疑点 | 复核结论 |
|---|---|
| M5-01 CLI 缺失（Explore 子代理只搜 server/） | **已实现**，位于 `tool/cli/`（独立模块，cobra+bubbletea，`go build ./...` exit 0）。 |
| `evolution/quality.go` 大量「占位」 | 是质量门控的**检测词库**（`vaguePhrases` 含 "占位/待补充/示例占位" 等，用于拦截草稿候选），非空桩，逻辑真实。 |
| `artifact.go:79` / `store.go:67` 的「未实现」 | 是 fallback 行为注释（「后端未实现该扩展时回退 List+Read」），非死代码。 |
| `command/registry.go` 的「占位」 | 是模板参数常量 `argsPlaceholder`（=`{{args}}`），非空桩。 |

---

## 二、真实强弱项评估

### 强项（保持）
1. **自主化闭环完整**：Goal 契约 + Plan-Execute + taskrun 后台扇出 + Worktree 隔离 + 跨天恢复 + 无人值守模式，已是「24h 自主推进平台」形态。
2. **企业化护栏到位**：审计落库、Token 计量、三级预算护栏、人工检查点、MCP 加密、RBAC owner 隔离，安全底座扎实。
3. **进化飞轮已成形**：evolution 提取 → 质量门控 → 审批发布 → warm-start；evaluation 回归 + promptiter 优化 + 飞轮×回归联动，形成「越用越聪明」闭环。
4. **测试基建可用**：369 测试函数，纯逻辑包（engine/agent/tool/executor/goal/cron/metrics/a2a）实测全绿，集成测试复用 mock LLM 桩，非假绿。

### 真实短板（已逐一核验，是上线前必补项，非文档吹水）
| # | 短板 | 证据 | 风险 |
|---|------|------|------|
| S1 | **worktree / taskrun 测试被环境门控跳过** | `worktree_test.go:51` / `taskrun_test.go` 在 `executor.NewHostExecutor` 失败时 `t.Skip("executor 不可用，跳过")` | 最复杂的「隔离 / 并行派生子任务」逻辑回归保护最弱，改动极易悄悄回归 |
| S2 | **框架类型泄漏进 `api` 层** | `api/sse.go`、`gateway.go`、`taskrun.go`、`usage.go`、`history.go` 直接 import 框架 `model.Message`/`event.Event`/`session.Service` | `engine.go` 注释承诺「框架 API 收敛在 engine 层」未兑现；框架升级改动面比设想大 |
| S3 | **`DB_AUTO_MIGRATE` 仍作生产 fallback 保留** | `repo/db.go` 在迁移版本表之外仍允许 AutoMigrate 开关 | 生产若误开，会与 `schema_migrations` 版本表产生 schema 漂移 |
| S4 | **共享技能库稀薄** | `skills/` 仅 `example/SKILL.md` 一个种子；`data/skills/1`、`2` 为空目录 | warm-start 默认近乎空转，M2-03 / M5-04 价值未真正发挥 |
| S5 | **A2A 仅非流式** | `a2a.go` 注明 `message/stream` 预留未实现 | 外部 client 调长任务拿不到进度流，对外服务能力受限 |
| S6 | **智能能力仅 mock 验证** | evolution/eval/promptiter/A2A 多轮均依赖 mock resolver，无真实模型端到端验证 | 真实 LLM 下行为（尤其 promptiter 写回生产指令、evolution 质量门控误杀）无保证 |
| S7 | **前端大 View 集中 + 死代码** | `ChatView.vue` ~28KB、`AutomationView/EvolutionView/EvaluationView` >13KB；`PlaceholderView.vue` 未被任何路由引用 | 维护成本高，包体积偏大（naive-ui 全量 ~1.3MB） |

---

## 三、未来开发计划（M6 起）

> 原则：先**可运营化加固**（S1~S6），再**能力深化**（M7），最后**产品化/商业化**（M8）。
> 任务粒度对齐现有 PLAN.md 风格（一个 `feat(Mx-NN)` 一条），便于直接补进 PLAN.md 由自主 Loop 续推。

### 阶段 A — 可运营化加固（M6，上线前必做）

| # | 任务 | 对应短板 | 验收标准 | 依赖 |
|---|------|---------|----------|------|
| M6-01 | **worktree / taskrun 测试去 skip** | S1 | CI 装 git + executor 可用性探测；`TestManager_CreateAndMerge` 与 taskrun 测试在 CI 真正执行并绿；本地 `go test` 默认跑通 | 无 |
| M6-02 | **框架依赖收敛到 engine 层** | S2 | `api` 层不再直接 import 框架 `model.Message`/`event.Event`/`session.Service`；新增 `engine` DTO 适配（出/入转换）；`engine.go` 注释承诺兑现 | 无 |
| M6-03 | **生产迁移治理** | S3 | `DB_AUTO_MIGRATE` 默认关闭 + 启动时若开启则告警日志；README 明确「仅本地开发」；迁移版本表为唯一生产 schema 真相源 | M3-08 |
| M6-04 | **种子技能库 + warm-start 真实命中 E2E** | S4 | `skills/` 补 ≥3 个真实技能（如 git-flow / code-review / go-build）；新增测试证明新会话确实注入并模型遵循 | M2-03 |
| M6-05 | **自动化韧性补强** | — | Loop 运行失败指数退避重试 + 失败通知；Budget 超限通知渠道（邮件/IM 占位实现打通）；Webhook 增加签名校验选项 | M4 |
| M6-06 | **真实模型冒烟测试套件** | S6 | 至少覆盖：promptiter 写回指令不破坏对话、evolution 质量门控不误杀合格候选、eval 多次跑分数稳定 | M5 |

### 阶段 B — 能力深化（M7）

| # | 任务 | 价值 |
|---|------|------|
| M7-01 | **A2A 流式 + client 端** | 实现 `message/stream`；平台可作 A2A client 调用外部 Agent，长任务进度可见（补 S5） |
| M7-02 | **多节点 taskrun** | 引入外部队列 + lease，突破 `taskrun` inprocess 单进程限制（docs/02 已预判），支撑水平扩展 |
| M7-03 | **Knowledge RAG 升级 PG/pgvector** | 目前本地 sqlite/bolt；补齐文档库规模与并发（M5-02 已留升级位） |
| M7-04 | **Docker 执行后端** | 真正实现文件系统沙箱，替代 `HostExecutor` 仅靠 `cmd.Dir` 的弱约束（LEARNINGS 已标注为后续项） |
| M7-05 | **评估集自举** | 用 evolution 提取的技能反向生成 eval case，飞轮×回归自强化（闭环自驱动质量） |
| M7-06 | **前端重构** | 拆分大 View（ChatView/AutomationView 等按职责拆子组件）；移除 `PlaceholderView` 死代码；naive-ui 按需引入 / manualChunks 降首屏体积（补 S7） |

### 阶段 C — 产品化 / 商业化（M8）

| # | 任务 | 价值 |
|---|------|------|
| M8-01 | **多租户隔离强化** | workspace 级配额、按租户预算上限、租户间资源隔离 |
| M8-02 | **可观测性完善** | trace 全链路（OTel traceID 贯通 Gateway→Runner→工具）、Grafana 面板、日志聚合 |
| M8-03 | **连接器市场** | 预置 GitHub / GitLab / Slack / Jira 等 MCP 模板，降低接入成本 |
| M8-04 | **IM Channel** | 飞书 / 钉钉 / 企业微信 Channel（当前 connector 均 disconnected），让 Loop 可从 IM 触发与回发 |
| M8-05 | **文档与示例** | quickstart、架构图、演示视频、典型场景案例，支撑对外推广 |

---

## 四、建议的下一步

1. **本轮即可行动**：把 `M6-01 ~ M6-06` 追加进 `docs/loop/PLAN.md`（放在 MX 之后作为新阶段），下一轮自主 Loop 会自动拾取 `M6-01` 续推（当前 Loop 处于「全 ✅ → STOP」终态，需补任务清单才能继续）。
2. **优先级建议**：S1/S2/S3 是「正确性 / 可维护性」硬伤，优先于新功能；S4/S6 决定「进化飞轮是否真转得起来」，应紧随。
3. **是否要我继续**：如需，我可以（a）把 M6 任务清单写入 PLAN.md 并标记入口，或直接（b）从 M6-01 开始实现。

---

## 附：复盘方法论（可复用）

- 先读 `docs/loop/{PLAN,PROGRESS,LEARNINGS}.md` + `docs/02` 战略文档，建立「文档声称」基线。
- 用 Explore 子代理按里程碑逐条对照代码实证，再对**关键矛盾点人工复核**（grep 误报率高，必须看上下文）。
- `go build ./...` + 代表性纯逻辑包 `go test` 实测，区分「环境跳过」与「真缺失」。
- 产出「强项 / 真实短板 / 未来计划」三层结构，短板附证据路径，避免空泛。
