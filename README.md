# goMultiAgentV2

> 24 小时持续自主推进的**企业级多 Agent 协作 CodeAgent 平台**。不止是被动问答，而是基于 Loop Engineering 理念自我驱动、自我推进的 Agent 系统。

- **GitHub**：https://github.com/ayanmw/multiagent2
- **目标**：对标 OpenClaw + Claude，构建可 24h 无人值守自主工作的 Agent 平台，融入 Automations / Worktrees / Skills / Connectors / Sub-agents / Memory 的工程化能力。
- **当前里程碑**：M0（骨架）✅、M0.5（缺陷修复）✅、M1（CodeAgent 核心）✅、M2（生态）✅、M3（企业化）✅、M4（自主化）✅、M5（进化）✅、MX（质量加固）✅。

---

## 架构

```
┌──────────────┐      REST + AG-UI SSE       ┌──────────────────────────┐
│   Web (SPA)  │ ───────────────────────────▶ │      Server (Go/Gin)      │
│  Vue3/TS/Vite│ ◀─────────────────────────── │  Auth·RBAC·Agent引擎       │
│  Naive UI    │                              │  Tools·Executor·Repo(SQLite)│
└──────────────┘                              │  Automation·Telemetry     │
                                              └────────────┬─────────────┘
                                                            │ OpenAI 兼容 /v1
                                                            ▼
                                              ┌──────────────────────────┐
                                              │  Gateway (workbuddyLLMAPI) │
                                              │  OpenAI 兼容 LLM 网关        │
                                              │  codebuddy | passthrough    │
                                              │  | mock                    │
                                              └────────────┬─────────────┘
                                                           │ HTTPS / ACP
                                                           ▼
                                        外部 LLM（WorkBuddy/CodeBuddy 积分 · 或任意 OpenAI 兼容端点）
```

| 组件 | 说明 | 默认端口 |
|------|------|----------|
| **Web** | 前端单页应用（管理 / 对话工作台 / 自动化 / 监控） | 开发 5173；生产由 nginx 提供 |
| **Server** | 后端 API 与 Agent 引擎（trpc-agent-go 引擎层 + Gin HTTP） | 8080 |
| **Gateway** | 本地 OpenAI 兼容 LLM 网关，把本机 WorkBuddy 积分或任意 OpenAI 兼容服务封装为标准协议 | 8088 |

> **关于 SQLite / CGO**：后端使用纯 Go 的 [`glebarez/sqlite`](https://github.com/glebarez/sqlite) 驱动，**编译与运行均不需要 CGO、不需要安装 C 编译器**。早期 README 中「需启用 CGO / 安装 TDM-GCC」的说法已过期，请以本版为准。

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Golang 1.25 · Gin · GORM · **纯 Go SQLite（glebarez/sqlite，无需 CGO）** · trpc-agent-go v1.10.0（仅用其 Agent 引擎层） |
| 前端 | Vue3 · TypeScript · Vite · Pinia · Vue Router · Naive UI · UnoCSS |
| 网关 | WorkBuddy/CodeBuddy CLI 包装为本地 OpenAI 兼容 LLM API（`tool/workbuddyLLMAPI`，默认 `127.0.0.1:8088`，默认模型 Hy3，回退 DeepSeek-V4-Pro） |
| CLI | Go（cobra + bubbletea），二进制 `gmctl`，复用同一套 REST+SSE 协议 |
| 工程化 | 自动化 LOOP（`docs/loop/`）+ WorkBuddy Automations |

---

## 里程碑

| 阶段 | 内容 | 状态 |
|------|------|------|
| M0 / M0.5 | 骨架 · 多轮记忆 / RBAC / SessionKey 唯一 / 缺陷修复 | ✅ |
| M1 | CodeAgent 核心：Executor·SafeExecutor·CodeAct·Workspace·子代理·CodeTeam·Goal 契约·Plan-Execute·护栏·斜杠命令·状态外置·E2E | ✅ |
| M2 | 生态：Git·MCP 管理·Skills warm-start·taskrun 后台任务·Worktree 隔离·toolsearch 延迟工具箱 | ✅ |
| M3 | 企业化：审计落库·审计/用量 API·预算护栏·人工检查点·Artifact 浏览器·MCP 加密·手动迁移·可观测性·E2E | ✅ |
| M4 | 自主化：Automation 模型·Cron 调度·Webhook·Channel 层·跨天恢复·无人值守 Loop·通知·自动化前端·E2E | ✅ |
| M5 | 进化：CLI·Knowledge RAG·evolution 飞轮·evolution 前端·evaluation 回归·promptiter·A2A·飞轮×回归·E2E | ✅ |
| MX | 质量加固：工作区/MCP/Skills/任务中心前端打通·M2 测试补全·用户管理后台·安全加固（限流/CORS/日志脱敏）·部署与文档 | ✅ |
| M6 | 可运营化加固：worktree/taskrun 测试去 skip·框架依赖收敛到 engine 层·生产迁移治理·种子技能库+warm-start E2E·自动化韧性·真实模型冒烟套件 | ✅ |
| M7 | 生产交付：CI 流水线·容器化收尾（.dockerignore/HEALTHCHECK）·K8s 清单·Alertmanager 告警·Grafana 看板·日志聚合+trace 贯通·**部署文档与 quickstart（本任务）** | ✅ |
| M8 | 能力深化与产品化：A2A 流式+client·Docker 执行后端·多节点 taskrun·Knowledge PG/pgvector·评估集自举·前端重构·IM Channel·连接器市场·多租户隔离·**文档与示例（架构图+24h 演示复现手册）** | ✅*（M8-10 切 PG 条件触发延后） |

---

## 目录结构

```
multiagent2/
├── server/                  # 后端（Go 模块：github.com/ayanmw/multiagent2/server）
│   ├── cmd/server/           # 入口 main.go
│   ├── internal/             # engine / agent / tool / executor / repo / api / middleware ...
│   └── Dockerfile            # 纯 Go 多阶段构建（CGO_ENABLED=0）
├── web/                      # 前端（Vue3 + Vite + Naive UI）
│   ├── src/                  # 源码
│   ├── dist/                 # 生产构建产物（vite build 生成，已 gitignore）
│   ├── Dockerfile            # node 构建 + nginx 反代
│   └── nginx.conf            # 生产 nginx 配置（SPA + /api 反代）
├── tool/
│   ├── workbuddyLLMAPI/      # 本地 OpenAI 兼容 LLM 网关（独立 Go 模块）
│   │   └── Dockerfile
│   └── cli/                  # gmctl 命令行客户端（独立 Go 模块）
├── docs/                     # 规划文档（M0/M1 设计、框架能力全景等）
│   └── loop/                 # 自动化 LOOP 控制三件套
│       ├── PLAN.md           # 任务清单（○ 待做 / ⏳ 进行中 / ✅ 完成）
│       ├── PROGRESS.md       # 每轮执行日志
│       └── LEARNINGS.md      # 项目约定与踩坑（**所有代码改动前必读**）
├── data/                     # SQLite 数据库与 workspaces（运行时生成，已 gitignore）
├── docker-compose.yml        # 一键三服务编排（server + web + gateway）
├── .env.example              # 环境变量样例
└── README.md
```

---

## 配置（环境变量）

### Server（`server/internal/config`）

| 变量 | 默认 | 说明 |
|------|------|------|
| `PORT` | `8080` | HTTP 监听端口 |
| `JWT_SECRET` | `dev-insecure-secret-change-me` | JWT 签名密钥；**生产必须设置** |
| `PROVIDER_ENC_KEY` | 回落 `JWT_SECRET` | AES-256-GCM 主密钥（加密 Provider/MCP 敏感字段），建议独立设置 |
| `DB_PATH` | `data/codeagent.db` | SQLite 文件路径 |
| `WORKSPACE_ROOT` | `data/workspaces` | 用户代码执行根目录 |
| `ARTIFACT_ROOT` | `data/agent-state` | 工作状态外置文件（PLAN/PROGRESS/LEARNINGS）根目录 |
| `STATE_ENABLED` | `true` | 工作状态外置开关（自主 Loop 续跑依赖） |
| `AGENT_MODE` | `single` | `single` / `team`（子代理委托） |
| `TEAM_REVIEWER` | `true` | team 模式是否加入只读 Reviewer |
| `TEAM_MAX_REVIEW_ROUNDS` | `2` | 「实现→审阅→修复」回环轮数上限 |
| `GOAL_CONTRACT` | `true` | team 模式是否启用目标契约 |
| `GOAL_MAX_NUDGES` | `3` | 目标未达成时最多拦截几次过早答复 |
| `PLAN_EXECUTE` | `true` | team 模式是否启用 Plan-Execute 循环 |
| `PLAN_MAX_NUDGES` | `3` | 计划未做完时最多拦截几次过早答复 |
| `MAX_LLM_CALLS` | `32` | 单次 invocation LLM 调用上限（护栏熔断） |
| `MAX_TOOL_ITERATIONS` | `16` | 单次 invocation 工具迭代轮数上限 |
| `MAX_TOOL_RETRIES` | `2` | 单个工具失败重试次数 |
| `GUARDRAIL_DISABLED` | `false` | 关闭护栏（仅本地调试，**生产禁止**） |
| `SKILLS_ROOT` | `skills` | 共享（只读）技能根目录 |
| `SKILLS_DATA_DIR` | `data/skills` | 用户私有技能根目录 |
| `SKILL_WARM_START` | `true` | 技能 warm-start 注入 |
| `SKILL_WARM_START_MAX_CHARS` | `6000` | warm-start 注入长度上限（控长） |
| `WORKTREE_ISOLATION` | `true` | taskrun 子任务 git worktree 隔离（只 merge 不 push 远程） |
| `TOOL_SEARCH_ENABLED` | `true` | 延迟工具箱（tool_search/call_tool） |
| `KNOWLEDGE_ENABLED` | `true` | 知识库 RAG 检索注入 |
| `BUDGET_ENABLED` | `true` | 平台级预算护栏总开关 |
| `CHECKPOINT_ENABLED` | `true` | 人工检查点（human-in-the-loop） |
| `DB_AUTO_MIGRATE` | `false` | 开发期 GORM AutoMigrate fallback；**生产保持关闭**（走版本化迁移） |
| `METRICS_ENABLED` | `true` | 可观测性 `/metrics`（Prometheus 文本） |
| `WEBHOOK_RATE_LIMIT` | `10` | 单 webhook token 窗口内触发上限 |
| `WEBHOOK_RATE_WINDOW_SECONDS` | `60` | webhook 速率限制窗口 |
| `RECOVERY_MAX_ATTEMPTS` | `3` | 跨天恢复对同一未收敛运行的最大续跑次数 |
| `WEBHOOK_NOTIFY_URL` | 空 | 出站通知回调地址（空则仅站内信） |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173,http://127.0.0.1:5173` | 允许的跨域前端源（逗号分隔） |
| `RATE_LIMIT_LOGIN_ENABLED` | `true` | 登录/注册防刷 |
| `RATE_LIMIT_LOGIN_LIMIT` | `10` | 登录限流：单 IP 窗口内次数 |
| `RATE_LIMIT_LOGIN_WINDOW_SECONDS` | `60` | 登录限流窗口 |
| `RATE_LIMIT_CHAT_ENABLED` | `true` | 对话防刷 |
| `RATE_LIMIT_CHAT_LIMIT` | `30` | 对话限流：单用户窗口内次数 |
| `RATE_LIMIT_CHAT_WINDOW_SECONDS` | `60` | 对话限流窗口 |
| `RUN_MODE` | `unattended` | `unattended`（24h 自主平台安全默认）/ `attended`（有人值守调试） |
| `EVOLUTION_ENABLED` | `true` | 后台技能进化扫描 |
| `EVOLUTION_INTERVAL_SECONDS` | `3600` | 进化扫描周期 |

### Gateway（`tool/workbuddyLLMAPI/internal/config`）

| 变量 | 默认 | 说明 |
|------|------|------|
| `WB_LISTEN` | `:8080` | 网关监听地址 |
| `WB_BACKEND` | `mock` | `passthrough` / `mock` / `codebuddy` |
| `WB_BASE_URL` | `https://api.openai.com/v1` | passthrough 上游 base URL |
| `WB_API_KEY` | 空 | passthrough 上游 API Key |
| `WB_DAEMON_URL` | `http://127.0.0.1:18765` | 本机 CodeBuddy 守护进程（codebuddy 后端） |
| `WB_DAEMON_CWD` | `.` | ACP `session/new` 的 agent 工作目录 |
| `WB_DAEMON_MODEL` | `hy3` | 默认模型（请求未指定 model 时使用） |
| `WB_DAEMON_FALLBACK_MODEL` | `deepseek-v4-pro` | 回退模型（主模型失败自动切换） |
| `WB_DEFAULT_MODEL` | `codebuddy-default` | 缺省模型占位名 |
| `WB_MODELS` | 内置 CodeBuddy 真实模型目录 | `/v1/models` 返回内容 |

> **Gateway 后端选择**：`codebuddy` 后端依赖**本机**已登录的 WorkBuddy/CodeBuddy 桌面（经本地守护进程 18765 消耗积分），**只能在装了 WorkBuddy 桌面的宿主机上运行，无法放进容器**。在服务器/容器部署时，请改用 `passthrough` 后端指向任意 OpenAI 兼容端点（如自建 vLLM / 云厂商），或让 Server 的 Provider 直接填写外部 `base_url`。

---

---

## 数据库与迁移（Database & Migrations）

本平台使用 SQLite（默认 `data/codeagent.db`），表结构**仅由版本化迁移管理**，唯一真相源是 `schema_migrations` 版本表（`server/internal/repo/migrate.go` 的 `Migrations()`）。

- **生产环境**：启动只执行 `repo.RunMigrations`，按版本号升序应用尚未执行的迁移；新增/修改表结构**必须**追加一条新迁移（见下「如何新增迁移」），绝不依赖 AutoMigrate。
- **`DB_AUTO_MIGRATE` 仅限本地开发**：默认 `false`。设为 `true` 时会在迁移之后再跑一次 GORM `AutoMigrate` 兜底，便于本地改模型时免写迁移，但会造成各环境表结构漂移，**生产严禁开启**（开启时启动日志会打印 `[WARN]` 告警）。
- **启动自检**：每次启动会打印 `Schema 真相源 = schema_migrations 版本表：已应用 N 个版本，待应用 M 个。` 强化「版本表即真相源」这一保证。
- **诊断 API**：`repo.AppliedMigrations(db)` / `repo.PendingMigrations(db)` 可查询已应用/待应用版本。

### 如何新增一次结构变更

1. 修改 `internal/model` 中的结构体；
2. 在 `Migrations()` 末尾追加一条新版本（`Version` 四位数字递增，如 `0012`），`Up` 内用 `db.Migrator().AddColumn/AlterColumn/DropColumn` 或对**单个**模型调用 `db.AutoMigrate(&model.X{})` 完成变更，必要时回填数据；
3. `baselineModels()` 保持为「当前全部模型」，使全新库经 `0001_baseline` 一次建成最新结构；
4. 迁移函数必须幂等（执行成功才写版本行，失败下次重跑安全）。

---

## 快速开始（手动）

### 1. 前置条件

- Go 1.25+（**无需 C 编译器**）
- Node.js 22+（前端）
- （可选）本地 LLM 网关：`tool/workbuddyLLMAPI` + 已登录的 WorkBuddy/CodeBuddy 桌面

### 2. 启动本地 LLM 网关（可选，本地联调用）

```sh
cd tool/workbuddyLLMAPI
# 修改 start_gateway_daemon.sh 中的 CLI 路径为本机 WorkBuddy CLI 路径后执行
bash start_gateway_daemon.sh
# 网关默认监听 127.0.0.1:8088
```

### 3. 启动后端

```sh
cd server
# 生产务必设置密钥
export JWT_SECRET="$(openssl rand -hex 32)"
export PROVIDER_ENC_KEY="$(openssl rand -hex 32)"
go run ./cmd/server          # 或 go build -o bin/server ./cmd/server && ./bin/server
```

### 4. 启动前端

```sh
cd web
npm install
npm run dev                  # 开发模式（默认 5173，/api 反代到 localhost:8080）
# 或 npm run build && npm run preview   # 生产构建预览
```

> Server 启动后访问 `GET /health` 应返回 200；前端登录后即可在「Provider 管理」中添加一个 Provider：
> - `base_url` = `http://localhost:8088/v1`（指向本地网关）或任意 OpenAI 兼容端点
> - `api_key` = 任意非空字符串（codebuddy 后端不校验）
> - `protocol` = `openai`

---

## 部署（Docker Compose，推荐）

三服务一键编排：后端 API、前端静态站（nginx）、LLM 网关。

```sh
# 1) 准备环境变量（密钥务必替换为真实随机值）
cp .env.example .env
#   编辑 .env：设置 JWT_SECRET / PROVIDER_ENC_KEY；按需设置 WB_BASE_URL / WB_API_KEY

# 2) 启动（后台）
docker compose up -d --build

# 3) 访问
#   前端（nginx 反代 /api → server）：http://localhost:8081
#   后端直连（调试）：http://localhost:8080   （GET /health）
#   网关：http://localhost:8088             （GET /healthz）
```

- `web` 服务由 nginx 提供 `web/dist` 静态资源，并把 `/api/`、`/metrics`、`.well-known/` 反代到 `server:8080`，SSE 流式已开启（`proxy_buffering off` + 长 `proxy_read_timeout`）。
- `server` 数据持久化在命名卷 `server_data`（含 SQLite、workspaces、agent-state、私有技能），不会随容器销毁丢失。
- `gateway` 默认 `WB_BACKEND=passthrough`，通过 `WB_BASE_URL`/`WB_API_KEY` 对接外部 LLM；Server 端 Provider 的 `base_url` 填 `http://gateway:8088/v1` 即可经网关统一调用。
- 更多编排细节见 `docker-compose.yml` 注释与各 `Dockerfile`。

常用命令：

```sh
docker compose ps                 # 查看状态
docker compose logs -f server     # 查看后端日志
docker compose down               # 停止并移除容器（数据卷保留）
docker compose down -v            # 连数据卷一并删除（危险：清空数据）
```

---

## 生产部署（Kubernetes + 监控，推荐上线方式）

> 完整运维手册见 **[docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md)**（CI/CD · 镜像发布 · K8s 主线 · Prometheus+Grafana · 日志/trace · 密钥管理 · 备份/升级/回滚 · 排障清单）。以下为快速路径。

```sh
# 1) 密钥（勿提交真实值；或用 kubectl create secret 直接创建，见 DEPLOYMENT.md §3）
kubectl apply -f k8s/secret.yaml        # 先替换占位符

# 2) 配置 + 共享技能 + 数据卷 + 后端 + 前端 + 入口
kubectl apply -f k8s/configmap.yaml
bash k8s/gen-skills-configmap.sh        # skills/ → ConfigMap（warm-start 共享技能）
kubectl apply -f k8s/pvc.yaml k8s/backend-deployment.yaml k8s/backend-service.yaml
kubectl apply -f k8s/web-deployment.yaml k8s/web-service.yaml k8s/hpa-web.yaml
# 改 k8s/ingress.yaml 的 host 为你的域名后：
kubectl apply -f k8s/ingress.yaml

# 3) 验证
curl -s https://<你的域名>/health      # 200
curl -s https://<你的域名>/metrics     # Prometheus 指标
```

要点：

- **backend 固定 1 副本**（本地 SQLite 单文件，多副本写冲突）；横向扩展需先切 PG（见 M8 规划）。
- **后端 Service 名必须为 `server`**（前端 nginx.conf 与监控 scrape 均写死 `server:8080`）。
- ingress 已带 SSE 免缓冲注解（`proxy-buffering off` + 3600s 超时），勿删。
- **监控告警**：`monitoring/` 提供 Prometheus + Alertmanager（六类规则，webhook 直达通知中心）+ Grafana（看板自动供应）+ Loki/Promtail（日志按 trace 串联）。本地快速体验：

  ```sh
  docker compose -f monitoring/docker-compose.monitoring.yml up -d
  # Prometheus :9090 · Alertmanager :9093 · Grafana :3000 · Loki :3100
  ```

- 日志格式/告警 token 等运维变量见 `.env.example` 与 `docs/DEPLOYMENT.md`。

---

## 前端构建产物

- 开发：`npm run dev`（Vite 开发服务器，端口 5173，`/api` 经 Vite proxy 转发到 `http://localhost:8080`）。
- 生产：`npm run build` 生成 `web/dist/`（纯静态资源，已加入 `.gitignore`）。
  - 产物体积可在 `vite build` 输出中查看各 chunk；如需优化可配置 `build.rollupOptions.output.manualChunks`。
  - 类型检查（CI/交付前建议）：`npm run typecheck`（即 `vue-tsc --noEmit`）。
- 生产托管方式二选一：
  1. **Docker（推荐）**：`web/Dockerfile` 多阶段构建，node 编译后由 nginx 托管并反代 API（见上）。
  2. **静态托管**：把 `web/dist/` 交给任意静态服务器 / CDN，并将 `/api` 前缀反向代理到后端地址（SSE 需关闭缓冲）。

---

## CLI（`gmctl`）

独立 Go 模块 `tool/cli`，复用同一套 REST+SSE 协议，适合无界面环境：

```sh
cd tool/cli
go build -o gmctl .
./gmctl login            # 登录拿 token
./gmctl sessions         # 列出会话
./gmctl chat "帮我写个 hello.go" --session <key>   # 一次性
./gmctl chat --repl      # 交互式 REPL（需 TTY）
./gmctl tasks            # 查看后台任务
./gmctl eval list        # 评估集管理
```

---

## 自动化 LOOP

本项目内置一套「自主推进」协议，由 `docs/loop/` 三件套驱动：

- **PLAN.md**：任务清单，状态 `○`（待做）/ `⏳`（进行中）/ `✅`（完成）。
- **PROGRESS.md**：每轮执行日志。
- **LEARNINGS.md**：项目约定与踩坑记录（**所有代码改动前必读**）。

运行协议（由 WorkBuddy Automation `GoMultiAgentV2 Loop` 每小时触发）：每轮读三件套 → 选 PLAN.md 中第一个 `○` → 实现 → 验证（`go build/vet/test`；前端 `npm run build` + `vue-tsc --noEmit`）→ `git commit` → 标 `✅` → STOP。`GoMultiAgentV2 日报` 每日 9:00 汇总完成率。

> 详见 [AGENTS.md](./AGENTS.md) 了解对 AI Agent（含 LOOP 自动化）的协作约定。

---

## 相关文档

- `docs/loop/PLAN.md` —— 任务规划与里程碑
- `docs/loop/LEARNINGS.md` —— 必读约定（Executor 封装、危险命令策略、路径约束等）
- `docs/ARCHITECTURE.md` —— **系统架构（mermaid 架构图 + 安全/企业化/部署）**
- `docs/DEMO-24H.md` —— **24h 自主演示复现手册（场景案例 + 视频分镜脚本）**
- `examples/automations/` —— 可拷贝的自动化与场景示例（curl + goal prompt）
- `docs/DEPLOYMENT.md` —— **生产部署手册（运维向：CI/CD·K8s·监控·日志·密钥·备份·排障）**
- `docs/02-框架能力全景与自主化升级规划.md` —— 框架能力全景
- `docs/03-M1规划与M0评审.md` —— M1 阶段规划
- `tool/workbuddyLLMAPI/README.md` —— LLM 网关详细说明

---

## License

私有项目，未开放授权。未经授权禁止用于商业用途。
