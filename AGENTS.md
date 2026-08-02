# AGENTS.md

本文件面向 **AI Agent**（含 `GoMultiAgentV2 Loop` 自动化）协作本项目时必读。人类开发者也建议阅读。

## 项目定位

- **goMultiAgentV2**：24h 持续自主推进的企业级多 Agent 协作 CodeAgent 平台，基于 trpc-agent-go v1.10.0 的 Agent 引擎层构建。
- 仓库：https://github.com/ayanmw/multiagent2
- 后端模块路径：`github.com/ayanmw/multiagent2/server`（位于 `server/`）
- 网关子模块：`tool/workbuddyLLMAPI`（独立模块，包名 `workbuddyllmapi`）

## 目录布局（速查）

| 路径 | 作用 |
|------|------|
| `server/cmd/server/main.go` | 后端入口 |
| `server/internal/engine/` | 业务编排核心（**唯一允许直接调用框架 engine 层之处**） |
| `server/internal/agent/` | Agent / Team / 工厂定义 |
| `server/internal/tool/` | CodeAct 工具集（包名 `codectool`，目录名刻意不同以避免与框架 `tool` 包冲突） |
| `server/internal/executor/` | **所有代码执行的唯一出口**（封装 SafeExecutor） |
| `server/internal/repo/` | GORM 数据访问 |
| `server/internal/api/` | HTTP / SSE 接口 |
| `web/` | 前端（Vue3 + Vite + Naive UI） |
| `docs/loop/` | 自动化 LOOP 控制三件套（**最高优先级的协作协议**） |
| `data/` | SQLite 与 workspaces（运行时生成，勿手改） |

## 自动化 LOOP 协议

改动前**必须先读** `docs/loop/PLAN.md`、`PROGRESS.md`、`LEARNINGS.md`。

- 每轮只做 PLAN.md 中**第一个 `○`** 任务，做完即 STOP，等下一轮。
- 严格遵守里程碑依赖（见 PLAN.md「阻塞与依赖」）：M0 ✅ → M0.5 ✅ → M1（M1-04→05/06/07→08→09/10→11→12/13→16，M1-14→15）。
- 提交信息格式：`feat(Mx-NN): <简短中文描述>`。
- 完成后更新：PLAN.md 该任务 `○`→`✅`；PROGRESS.md 追加一条；本 automation 的 `memory.md` 追加一条。

## 构建与验证命令

```sh
# 后端（需 CGO 启用，依赖 C 编译器）
cd server
go build ./...
go vet ./...
go test -count=1 ./...

# 前端
cd web
npm install
npm run build
npm run typecheck   # vue-tsc --noEmit
```

> ⚠️ `go test` 中依赖数据库的用例需要 **CGO 启用**（`go-sqlite3` 为 cgo 依赖）。在缺少 C 编译器的环境（如部分 CI 沙箱）下，仅 DB 相关用例会失败，属环境限制而非代码问题。`go build` / `go vet` 通过即可认为编译正确。

## 必守约定（来自 LEARNINGS.md，违反即视为破坏性改动）

1. **执行出口唯一**：所有代码执行必须经 `internal/executor.Executor`，禁止散写 `os/exec`。
2. **安全默认**：必须包裹 SafeExecutor，无人值守场景默认 `deny`（危险命令需显式放行）。
3. **路径约束**：文件类工具必须经 `resolveSafePath` 做路径越界校验。
4. **框架锁版本**：trpc-agent-go 锁定 `v1.10.0`，业务代码只允许经 `internal/engine` 层使用，禁止绕过。
5. **工具定义**：统一用 `tool/function.NewFunctionTool` 定义工具，纯函数逻辑与工具包装层分离。
6. **控制文件保护**：除 `docs/loop/` 外，勿随意修改其他约定文档（M1-16 的 artifact 文件除外）。

## 提交与推送

- 主分支为 `main`，远程 `origin` = `git@github.com:ayanmw/multiagent2.git`（SSH）。
- 已配置 **post-commit hook**：在 `main` 分支提交后自动 `git push origin main`。
- 因此：在 `main` 上的任何本地提交都会自动推送到 GitHub，**无需手动 push**。
- 注意：本机 `start_gateway_daemon.sh` 内含作者本地 CLI 绝对路径（`C:/Users/anmingwei/...`），他人使用前需改为本机 WorkBuddy CLI 路径。

## 禁止事项

- 禁止把密钥、Token、本地绝对路径（含作者用户名）提交进仓库。
- 禁止在 LOOP 任务中改动 `docs/loop/` 以外的「约定文档」，除非任务明确要求。
- 禁止跳过 `go build`/`go vet` 验证直接提交。
- 禁止在自动化任务中将全部任务一次性做完——每轮仅一个 `○`。
