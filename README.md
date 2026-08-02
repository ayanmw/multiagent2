# goMultiAgentV2

> 24 小时持续自主推进的企业级多 Agent 协作 **CodeAgent 平台**。不止是被动问答，而是基于 Loop Engineering 理念自我驱动、自我推进的 Agent 系统。

- **GitHub**：https://github.com/ayanmw/multiagent2
- **目标**：对标 OpenClaw + Claude，构建可 24h 无人值守自主工作的 Agent 平台，融入 Automations / Worktrees / Skills / Connectors / Sub-agents / Memory 的工程化能力。
- **当前里程碑**：M0（骨架）✅、M0.5（缺陷修复）✅，M1（CodeAgent 核心）进行中。

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Golang 1.25 · Gin · GORM · SQLite3（cgo）· trpc-agent-go v1.10.0（仅用其 Agent 引擎层） |
| 前端 | Vue3 · TypeScript · Vite · Pinia · Vue Router · Naive UI · UnoCSS |
| 网关 | WorkBuddy/CodeBuddy CLI 包装为本地 OpenAI 兼容 LLM API（`tool/workbuddyLLMAPI`，默认 `127.0.0.1:8088`，默认模型 Hy3，回退 DeepSeek-V4-Pro） |
| 工程化 | 自动化 LOOP（`docs/loop/`）+ WorkBuddy Automations |

---

## 目录结构

```
multiagent2/
├── server/                  # 后端（Go 模块：github.com/ayanmw/multiagent2/server）
│   ├── cmd/server/           # 入口 main.go
│   └── internal/             # engine / agent / tool / executor / repo / api ...
├── web/                      # 前端（Vue3 + Vite + Naive UI）
├── tool/workbuddyLLMAPI/     # 本地 OpenAI 兼容 LLM 网关（独立 Go 模块）
├── docs/                     # 规划文档（M0/M1 设计、框架能力全景等）
│   └── loop/                 # 自动化 LOOP 控制三件套
│       ├── PLAN.md           # 任务清单（○ 待做 / ⏳ 进行中 / ✅ 完成）
│       ├── PROGRESS.md       # 每轮执行日志
│       └── LEARNINGS.md      # 项目约定与踩坑
├── data/                     # SQLite 数据库与 workspaces
└── start_gateway_daemon.sh   # 启动本地 LLM 网关守护进程
```

---

## 快速开始

### 1. 前置条件

- Go 1.25+（编译 `go-sqlite3` 需启用 **CGO**，请确保已安装 C 编译器，如 TDM-GCC / MinGW）
- Node.js 22+（前端）
- WorkBuddy / CodeBuddy CLI（本地 LLM 网关依赖）

### 2. 启动本地 LLM 网关

```sh
# 修改 start_gateway_daemon.sh 中的 CLI 路径为本机 WorkBuddy CLI 路径后执行
sh start_gateway_daemon.sh
# 网关默认监听 127.0.0.1:8088
```

### 3. 启动后端

```sh
cd server
go run ./cmd/server
```

### 4. 启动前端

```sh
cd web
npm install
npm run dev        # 开发模式
# 或 npm run build && npm run preview  # 生产构建预览
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
- `docs/02-框架能力全景与自主化升级规划.md` —— 框架能力全景
- `docs/03-M1规划与M0评审.md` —— M1 阶段规划

---

## License

私有项目，未开放授权。未经授权禁止用于商业用途。
