# GoMultiAgentV2 — 项目知识与约定

> 每条教训/决策按 `YYYY-MM-DD | 分类 | 内容` 格式追加
> 自动化每轮先读此文件，避免重复犯错

---

## 项目基础约定

| 项 | 值 |
|----|-----|
| Go 模块路径 | `github.com/anmingwei/go-multi-agent-v2` |
| 后端入口 | `server/cmd/server/main.go` |
| 后端代码根 | `server/internal/` |
| 前端代码根 | `web/` |
| 数据库 | SQLite3（文件 `data/codeagent.db`） |
| Go 版本 | ≥ 1.21 |
| Node 版本 | ≥ 18 |
| 测试命令 | `go test ./...`（后端）、`npm run build`（前端） |
| 构建命令 | `go build ./...`（后端）、`npm run build`（前端） |
| git commit 格式 | `feat(M0): <简短描述>` |

## 目录结构约定

```
server/
├── cmd/server/main.go          # 入口
├── internal/
│   ├── api/                    # HTTP handler（按资源分文件）
│   ├── middleware/              # auth/rbac/logging/cors
│   ├── service/                # 业务逻辑
│   ├── repo/                   # GORM 数据访问
│   ├── model/                  # 领域模型（与 DB 表对应）
│   ├── engine/                 # trpc-agent-go 封装
│   └── config/                 # 配置加载
├── pkg/                        # 可复用工具库
└── go.mod
```

## 技术决策记录

### 2026-07-28 | 架构 | Gin vs tRPC-Go 作为 HTTP 框架
- 决策：使用 Gin（更轻量、生态更大、对前端 API 更友好）
- trpc-agent-go 只用其 Agent 引擎部分，不作为 HTTP 框架
- 理由：tRPC-Go 是全栈 RPC 框架，与我们的 REST+SSE API 模式不匹配

### 2026-07-28 | 安全 | APIKey 存储
- 决策：数据库存 SHA256(api_key)，仅在创建时返回一次明文给用户
- AES-GCM 加密只用于 Provider 的 APIKey（需要解密后调用 LLM）

### 2026-07-28 | 前端 | 事件协议
- 决策：采用 trpc-agent-go 的 AG-UI 协议作为 SSE 事件格式
- 不自造事件协议，前端可直接对接框架标准

### 2026-07-28 | 数据库 | GORM AutoMigrate
- 决策：开发阶段用 AutoMigrate 自动建表
- 生产环境改为手动 migration（M3 引入）

---

（以下由自动化循环追加）
