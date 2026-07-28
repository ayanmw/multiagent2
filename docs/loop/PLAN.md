# GoMultiAgentV2 — M0 细粒度任务清单

> 每个任务设计为 1 小时内可完成的独立单元，含验证标准。
> 状态：○ 待做 | ⏳ 进行中 | ✅ 已完成 | ❌ 阻塞

---

## M0 骨架：项目脚手架 → Auth → Provider/Model → 流式对话

| # | 任务 | 状态 | 验证标准 | 备注 |
|---|------|------|----------|------|
| M0-01 | **Go 项目初始化**：server/go.mod（模块路径 `github.com/anmingwei/go-multi-agent-v2`）、目录骨架（cmd/internal/pkg）、main.go 启动 Gin HTTP 服务器 + 健康检查 /health | ✅ | `go build ./...` 通过 + `curl /health` 返回 200 |
| M0-02 | **GORM + SQLite3 底座**：DB 连接管理、AutoMigrate 框架、User/Role/RolePermission 基础数据模型 | ✅ | `go test ./...` 通过，表自动创建成功 |
| M0-03 | **Vue3 前端项目初始化**：Vite + Vue3 + TS + Pinia + Vue Router + Naive UI + UnoCSS，基础布局骨架（sidebar + header + router-view） | ✅ | `npm run build` 通过，首页可访问 |
| M0-04 | **Auth: 注册/登录 API**：POST /api/auth/register、POST /api/auth/login，bcrypt 密码哈希，JWT 签发与验证 | ✅ | curl 注册→登录→拿 token→访问受保护端点 |
| M0-05 | **Auth: JWT 中间件 + RBAC**：认证中间件提取 user_id + role，角色权限枚举（admin/developer/viewer），路由级权限检查 | ✅ | 未登录返回 401，viewer 访问 admin 路由返回 403 |
| M0-06 | **Auth: APIKey 管理**：POST /api/auth/apikeys（创建）、GET /api/auth/apikeys（列表）、DELETE /api/auth/apikeys/:id（吊销），SHA256 哈希存储 | ✅ | 创建 APIKey → 用 X-API-Key header 访问受保护端点 |
| M0-07 | **Provider 管理 CRUD**：POST/GET/PUT/DELETE /api/providers，protocol 字段（openai/anthropic/gemini），AES-GCM 加密存储 APIKey | ✅ | 创建 Provider → 列表可见 → 可编辑/删除；APIKey 不以明文回显 |
| M0-08 | **Model 自动发现**：GET /api/providers/:id/models 触发从 Provider 拉取模型列表（/v1/models 或对应协议端点），结果缓存 5 分钟 | ✅ | 配置 OpenAI 兼容 Provider → 调 models 接口 → 返回模型列表 |
| M0-09 | **Model 管理**：Provider 下 Model 的启用/禁用/默认标记，Agent 配置时只能选已启用的 Model | ✅ | 从 Provider 拉取的模型可手动启用/禁用 |
| M0-10 | **Agent 对话引擎封装**：engine 层封装 trpc-agent-go Runner/LLMAgent，连接选定 Provider+Model，基础 Tool 集（echo/get_time），输出 Event 流 | ✅ | 调 /api/chat 发消息 → 得到 LLM 回复 |
| M0-11 | **AG-UI SSE 流式端点**：GET /api/chat/:session_id/stream，将 Agent Event 流转换为 AG-UI 协议 SSE 事件（RUN_STARTED/TEXT_MESSAGE_CONTENT/TOOL_CALL_START/TOOL_CALL_ARGS/TOOL_CALL_END/RUN_FINISHED），Session 持久化到 DB | ✅ | curl SSE 端点 → 逐条收到标准 AG-UI 事件 |
| M0-12 | **Session 管理 API**：POST /api/sessions（新建）、GET /api/sessions（列表）、GET /api/sessions/:id（含历史消息） | ✅ | 创建 session → 对话 → 刷新页面后历史消息仍在 |
| M0-13 | **前端: 登录/注册页面**：Naive UI 表单，登录成功存储 JWT 到 localStorage，router 守卫实现未登录跳转 | ✅ | 注册→登录→跳转到主页 |
| M0-14 | **前端: 主布局**：左侧 sidebar（对话/Sessions/Provider/Model/设置导航），顶部 header（用户信息/退出），dark theme 适配 | ✅ | 各导航项切换正常，dark 主题一致 |
| M0-15 | **前端: Provider 管理页面**：表格列表 + 新建/编辑对话框（含 protocol 选择）+ 测试连接按钮 + 删除确认 | ✅ | 创建 Provider → 测试连接 → 模型列表自动加载 |
| M0-16 | **前端: Model 管理页面**：按 Provider 分组展示，每个 Provider 下手动刷新模型列表，每个 Model 可启用/禁用 | ✅ | 点击刷新 → 从 Provider 拉取 → 列表更新 |
| M0-17 | **前端: 对话工作台（核心）**：左侧 Session 列表（可新建/切换），右侧对话区（消息气泡 + Markdown 渲染 + 流式逐字输出），底部输入框 + 发送按钮 | ○ | 新建 Session → 选 Model → 发消息 → 看到流式回复 |
| M0-18 | **前端: 对话工具栏**：消息区上方显示当前 Model/Provider，可点击切换；消息区支持 /clear 清空上下文 | ○ | 切换 Model → 后续回复用新 Model；/clear 后上下文重置 |
| M0-19 | **集成验证**：端到端测试——注册→登录→创建 OpenAI Provider（可配 Ollama 本地或真实 API）→拉取模型列表→启用模型→新建 Session→发消息→收到流式回复→刷新后历史仍在 | ○ | 全链路走通 |

---

## 阻塞与依赖

- **M0-01 → M0-02 → M0-04**（Go 基础 → DB → Auth）
- **M0-04 → M0-05 → M0-06**（登录 → RBAC → APIKey）
- **M0-02 → M0-12**（DB → Session 持久化）
- **M0-07 → M0-08 → M0-09**（Provider → 模型发现 → 模型管理）
- **M0-07 → M0-10 → M0-11**（Provider → Agent 引擎 → SSE）
- **M0-03 → M0-13 → M0-14 → M0-15/16/17**（前端基础 → 页面）
- **M0-11 + M0-12 → M0-17**（SSE + Session API → 对话工作台）
- **M0-15/16/17/18 → M0-19**（全部 → 集成验证��
