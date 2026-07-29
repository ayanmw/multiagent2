# GoMultiAgentV2 — 推进日志

> 每轮自动化执行后追加一条记录，格式：`### YYYY-MM-DD HH:MM | M0-XX | ✅/❌`

---

## 2026-07-28 10:43 | 循环基础设施初始化 | ✅

- 初始化 git 仓库
- 创建 PLAN.md（19 个 M0 任务，每任务 ~1 小时）
- 创建 PROGRESS.md（本文件）
- 创建 LEARNINGS.md（项目规范与约定）
- 创建 Go 项目基础骨架（go.mod + 目录结构）
- 创建两条 Automation：`GoMultiAgent Loop`（每小时） + `GoMultiAgent 日报`（每日 9:00）
- 状态：循环就绪，下个整点启动首轮

---

（以下由自动化循环追加）

---

### 2026-07-28 12:53 | M0-06 | ✅
- 完成内容：APIKey 管理（创建/列表/吊销）。model/APIKey（SHA256 哈希存储，明文仅创建时回显一次）；auth.GenerateAPIKey；repo CRUD；api 处理器 POST/GET/DELETE /api/auth/apikeys（owner 归属校验）；middleware.AuthMiddleware 升级为同时支持 Bearer JWT 与 X-API-Key 双鉴权；/api/me 改用 context 身份（兼容两种鉴权）
- Commit: f52274b
- 验证: go build/vet ✓ | runtime curl ✓（创建→回显明文；列表不含明文；无鉴权401；X-API-Key→/api/me 200 且身份正确；X-API-Key→/api/admin/roles 403；吊销后该 key→401）

### 2026-07-28 12:47 | M0-05 | ✅
- 完成内容：JWT 认证中间件 + RBAC。middleware/auth.go（AuthMiddleware 注入 user_id/role、RequireRole 角色白名单、RequirePermission 基于 RolePermission 表资源/动作校验，支持 * 通配）；repo 新增 GetPermissionsByRoleID/ListRoles；新增 admin 演示端点 GET /api/admin/roles（admin 专属）；main.go 接好受保护路由组
- Commit: e267da3
- 验证: go build ✓ | go vet ✓ | runtime curl ✓（无token→401，dev/viewer→/api/admin/roles 403，admin→200，坏token→/api/me 401）

### 2026-07-28 12:10 | M0-03 | ✅
- 完成内容：Vue3 前端项目初始化，Vite + Vue3 + TS + Pinia + Vue Router + Naive UI + UnoCSS，基础布局骨架（sidebar + header + router-view）
- Commit: f56d486
- 验证: npm run build ✓（dist 生成，2766 modules transformed）

---

### 2026-07-28 11:13 | M0-02 | ✅
- 完成内容：GORM + SQLite3 数据库底座，User/Role/RolePermission 模型 + AutoMigrate + 角色种子数据
- Commit: 0cdd747
- 验证: go build ✓ | DB 表创建 ✓ | 角色种子 3 条 + 权限 11 条 ✓

---

### 2026-07-28 11:04 | M0-01 | ✅
- 完成内容：Go 项目初始化，Gin HTTP 服务器 + /health 健康检查端点
- Commit: 6f8d152
- 验证: go build ✓ | curl /health → 200 ✓

---

### 2026-07-28 12:29 | M0-04 | ✅
- 完成内容：Auth 注册/登录 API（POST /api/auth/register、/login、/me），bcrypt 密码哈希 + HS256 JWT 签发验证，新用户默认 developer 角色
- Commit: eb7f8f6
- 验证: go build ✓ | go test ✓ (无测试) | runtime curl ✓（注册→登录→/me，错误密码401、重复注册409、无token401、弱密码400）

### 2026-07-28 13:32 | M0-07 | ✅
- 完成内容：Provider 管理 CRUD（POST/GET/PUT/DELETE /api/providers，含 GET /api/providers/:id）；model/Provider（protocol openai/anthropic/gemini、UserID 归属、APIKeyEnc 只存密文）；internal/crypto（AES-256-GCM 加解密，nonce 前置 base64）；repo CRUD + 归属校验；config 新增 EncryptionKey（32字节，由 PROVIDER_ENC_KEY 或 JWT_SECRET 派生 sha256）；handler 双通道鉴权下读 context 身份，api_key 仅创建/更新时明文入参且从不回显，返回 has_api_key 标志；db.go AutoMigrate 加 Provider；main.go 注册 5 条路由。
- Commit: 06bc2cb
- 验证: go build/vet ✓ | runtime curl ✓（创建→has_api_key:true 且明文不回显；列表/详情不含明文；u2 访问 u1 的 provider→403；更新 name 成功；非法 protocol→400；删除后列表空；DB 文件不含明文）

### 2026-07-28 14:38 | M0-08 | ✅
- 完成内容：Model 自动发现。新增 internal/provider 包（Discoverer：三协议 openai/anthropic/gemini 拉取模型列表、解密 AES-GCM 密钥、按 provider id 内存缓存 5 分钟）；api.ListProviderModelsHandler + GET /api/providers/:id/models（校验归属，返回 models + cached 标志）；main.go 注入 discoverer 并注册路由；4 个 httptest 单测覆盖 openai/anthropic/无 key/上游错误。
- Commit: 6320779
- 验证: go build/vet ✓ | go test ✓（provider 包全绿）| runtime curl ✓（register/login→201/200；创建 openai provider→api_key_enc 密文；GET models #1 cached:false 返回 2 模型；#2 cached:true 延迟 10ms→0.5ms；无 token→401）

### 2026-07-28 17:10 | M0-10 | ✅
- 完成内容：Agent 对话引擎封装。新增 internal/engine 包（engine.go 封装 trpc-agent-go Runner/LLMAgent，连接选定 Provider+Model；tools.go 提供 echo/get_time 两个基础函数工具；engine_test.go 用 httptest mock OpenAI 服务验证引擎拿到回复）；引入并锁定框架 trpc.group/trpc-go/trpc-agent-go v1.10.0（go.mod/go.sum）；api/chat.go 新增 POST /api/chat（从 DB 选已启用 Model+Provider、AES-GCM 解密 key、构造引擎、返回回复，支持显式 model_id 或自动选默认模型）；main.go 注册路由。
- Commit: <pending>
- 验证: go build ✓ | go test ✓（engine 包全绿：mock OpenAI 返回回复 + 非 openai 协议报错）| runtime curl ✓（注册→建 openai Provider→sync→启用默认模型→POST /api/chat 自动选默认模型与显式 model_id 均返回 `[mock] ... 来自临时 mock 服务的回复`）


- 完成内容：Model 管理（托管模型表）。新增 model.Model（provider_id+model_id 唯一、enabled/is_default 标志）；repo（UpsertModel 幂等同步且保留用户 enable/default、ListModelsByProvider、ListEnabledModels 供 Agent 选模型、GetModelByID 归属校验、PatchModel 事务内保证每 Provider 仅 1 个 default）；api（POST /api/providers/:id/models/sync 拉取上游→upsert→返回托管列表、GET /managed 列表、PUT /:mid 切换启用/默认、GET /api/models 返回当前用户已启用模型含 provider 名/协议）；db.go AutoMigrate 加 Model；main.go 注册 4 条路由。另加 repo/model_test.go（upsert 保留标志、单默认、按用户隔离）。
- Commit: e5dd98f
- 验证: go build/vet ✓ | go test ✓（repo+provider 包全绿）| runtime curl ✓（注册/登录→201/200；建 openai provider→has_api_key:true；sync→拉 2 模型全 disabled；PUT 启用+默认→200；GET /api/models 仅 1 启用且含 provider 信息；managed 仍 2 行仅 1 默认；改第 2 个默认→第 1 个自动取消默认；无 token→401；跨用户改→403）



### 2026-07-28 18:38 | M0-11 | ✅
- 完成内容：AG-UI SSE 流式端点 + Session 持久化。新增 internal/api/sse.go（StreamChatHandler + aguiConverter：将 engine 事件流转 AG-UI SSE 事件 RUN_STARTED/TEXT_MESSAGE_CONTENT/TOOL_CALL_START/TOOL_CALL_ARGS/TOOL_CALL_END/RUN_FINISHED/RUN_ERROR）；engine.Stream 方法（返回 <\-chan *event.Event>，桥接 Runner 输出并在 ctx 取消/超时后收尾）；model.Session/model.Message + repo.GetOrCreateSession/AppendMessage/GetSessionByKey + db.go AutoMigrate；main.go 注册 GET /api/chat/:session_id/stream。sse_test.go 覆盖文本流/工具调用/错误三类转换。
- Commit: f7791b5
- 验证: go build ✓ | go vet ✓ | go test ✓（api/engine/provider/repo 全绿）| runtime curl SSE ✓（mock OpenAI 返回 RUN_STARTED→TEXT_MESSAGE_CONTENT→RUN_FINISHED；同 session 复用 threadId；同进程内校验 Session+Message 落库成功）

### 2026-07-28 19:09 | M0-12 | ✅
- 完成内容：Session 管理 API（POST /api/sessions 新建、GET /api/sessions 列表、GET /api/sessions/:id 详情含历史消息）。复用 M0-11 的 Session/Message 持久层；:id 路径参数即对外 session_key；用户隔离 + 跨用户 404；新增 repo/session_test.go 覆盖隔离/排序/消息正序/越权；新增 internal/api/session.go + main.go 三条路由注册。
- Commit: b739055
- 验证: go build ✓ | go vet ✓ | go test ✓（repo session 单测绿）| runtime curl ✓（注册→建会话带标题/默认标题均 201；列表按最近活动倒序；详情空历史；无 token→401；错误 key→404）

### 2026-07-28 19:35 | M0-13 | ✅
- 完成内容：前端登录/注册页面。新增 src/api/client.ts（统一 HTTP 客户端 + 自动附加 JWT + ApiError）、src/api/auth.ts（register/login/me 封装）、src/stores/auth.ts（Pinia 认证仓库，token+user 持久化到 localStorage）；LoginView/RegisterView 用 Naive UI 表单 + 前端校验；router 增加 /login /register 独立路由 + beforeEach 守卫（未登录访问受保护路由跳 /login 并带 redirect，已登录访问登录页跳首页）；App.vue 改为顶层 <router-view/> 使认证页独立全屏；DefaultLayout 接入 auth 仓库显示用户名并接线「退出」。
- Commit: 9e5cddd
- 验证: npm install ✓ | npm run build ✓（LoginView/RegisterView chunk 生成）| vue-tsc typecheck ✓

### 2026-07-28 20:34 | M0-14 | ✅
- 完成内容：前端主布局。DefaultLayout 侧边栏导航改为 对话/Provider/Model/设置（内联 SVG 图标、随路由高亮、可折叠）；头部新增深色主题切换按钮 + 用户信息 + 退出；新增 stores/ui.ts（深色偏好持久化到 localStorage 并切 <html class="dark">），App.vue 的 NConfigProvider 绑定 darkTheme，uno.config.ts 用 presetUno({ dark: 'class' }) 开启 dark: 变体；路由重构为 chat/providers/models/settings 四子路由（首页默认进 /chat），并用单一 PlaceholderView.vue 占位为 M0-15/16/17 预留路由。
- Commit: 9cc9e0c
- 验证: npm run build ✓ | vue-tsc typecheck ✓（UnoCSS dark 配置从顶层移到 preset 级以通过类型检查）

### 2026-07-28 21:40 | M0-15 | ✅
- 完成内容：前端 Provider 管理页面。新增 src/api/provider.ts（list/get/create/update/delete/fetchModels 封装，api_key 仅入参不回显、响应读 has_api_key）；新增 src/views/ProvidersView.vue（NDataTable 列表：名称/协议/地址/密钥状态/描述/操作，协议与密钥用 NTag 着色；NModal 新建/编辑表单含 protocol 选择 + BaseURL + 密码型 APIKey「编辑时留空=不修改」+ 描述；NPopconfirm 删除确认；「测试连接」复用 GET /api/providers/:id/models 拉取模型并弹窗展示 id/owned_by + 缓存命中标记，成功即代表连接可达）；router 将 /providers 由 PlaceholderView 切到 ProvidersView。
- Commit: c6659c1
- 验证: npm install ✓ | npm run build ✓（ProvidersView chunk 6.39KB）| vue-tsc typecheck ✓（修掉可选字段可空告警，用 ?? '' 与局部变量收窄）

### 2026-07-28 22:42 | M0-16 | ✅
- 完成内容：前端 Model 管理页面。新增 src/api/model.ts（listManagedModels/syncProviderModels/updateModel，对齐 server/internal/api/model.go 契约）；新增 src/views/ModelsView.vue（按 Provider 分组卡片 + 每 Provider「刷新模型」按钮触发 sync 拉取上游并落库 + 模型表「启用/默认」开关，设为默认时一并启用、默认模型锁定启用开关）；router 把 /models 由 PlaceholderView 切到 ModelsView。
- Commit: ddf7d21
- 验证: npm install ✓ | npm run build ✓（ModelsView chunk 4.11KB）| vue-tsc typecheck ✓（exit 0）

### 2026-07-28 23:44 | M0-17 | ✅
- 完成内容：前端对话工作台（核心）。新增 web/src/api/session.ts（createSession/listSessions/getSession 封装，契约对齐 server/internal/api/session.go）；web/src/api/chat.ts（listEnabledModels 取可用模型 + streamChat 用 fetch+ReadableStream 手动解析 AG-UI SSE 帧，原生 EventSource 无法带 JWT 头故改用 fetch）；web/src/utils/markdown.ts（markdown-it + DOMPurify 安全渲染，html:false 防注入）；web/src/views/ChatView.vue（左侧 Session 列表可新建/切换、右侧消息气泡 + Markdown 渲染 + 流式逐字输出、顶部 Model 选择器、底部输入框 Enter 发送/Shift+Enter 换行、生成中可「停止」）；router 把 /chat 由 PlaceholderView 切到 ChatView；web/package.json 加 markdown-it@14.1.0 / dompurify@3.2.4 / @types/markdown-it。
- Commit: 528325c
- 验证: npm install ✓ | npm run build ✓（ChatView chunk 120KB 含 markdown-it+dompurify）| vue-tsc typecheck ✓（exit 0）| server go build ✓（本轮未改后端）

### 2026-07-29 00:46 | M0-18 | ✅
- 完成内容：前端对话工具栏。在消息区上方新增工具条：用 NPopselect 包裹的模型 chip 显示「当前模型名 · Provider 名」，点击即弹出下拉切换模型（切换后后续回复走新模型）；右侧「清空上下文」按钮 + 输入框支持 `/clear` 命令，二者均清空本地消息展示实现上下文重置。原顶部 model NSelect 移到该工具条，会话标题保留在 header。
- Commit: 0c25ede
- 验证: npm install ✓ | npm run build ✓（vite 2862 modules）| vue-tsc --noEmit ✓（exit 0）| go build ./... ✓（本轮未改后端）

### 2026-07-29 02:04 | M0-19 | ✅
- 完成内容：端到端集成验证（进程内 E2E 测试 TestM0_Integration_E2E）。① 抽 buildRouter 便于测试；② 新增 server/cmd/server/integration_test.go，用 httptest 本地 OpenAI 桩（/v1/models + /v1/chat/completions SSE）跑通「注册→登录→建 openai Provider→sync 模型→启用+默认→建 Session→SSE 流式对话→GET 会话详情校验历史（刷新后仍在）」全链路。验证中发现并修复两个真问题：引擎未开启流式（llmagent 默认非流式），补 agent.WithStream(true)，否则上游被当非流式请求、无法 token 级增量且 SSE 端点报 text/event-stream 解析错；AG-UI converter 与 engine.Chat 同时累加 Delta.Content + 最终 Message.Content 导致流式回复文本重复一倍，改为优先用增量、仅非流式时回退 Message。sse_test.go 增非流式用例、engine_test.go 桩改 SSE。
- Commit: <pending>
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（api/engine/cmd/server 全绿；E2E 流式回复「你好，世界！」无重复、历史持久化 2 条消息 user+assistant）| npm run build ✓（前端未改，M0 出口仍可用）

### 2026-07-29 09:48 | M0.5-01 | ✅
- 完成内容：**P0 多轮记忆修复**。框架 v1.10.0 的 `session` 包仅有 `inmemory`/`noop` 后端，**无 SQLite 持久化后端**（docs/03 称「文档称有 SQLite 后端」在 v1.10.0 不成立），故采用退化方案：后端从 DB `ListSessionMessages` 加载历史 → 构造 `[]model.Message` 多轮传入 Runner。具体：① `engine.Stream/Chat` 新增 `history []model.Message` 形参，通过 `agent.WithMessages(history)` 把历史 seed 进每次 `runner.Run`（fresh inmemory service 会先落库历史事件再追加本轮 user 消息，模型即拥有前序上下文）；② 新增 `internal/api/history.go`（`toFrameworkMessage` 角色映射 + `loadChatHistory(db, sessionID, excludeLast)` 排除当前刚写入的 user 消息避免重复）；③ `sse.go` 与 `chat.go` 在调用引擎前 `loadChatHistory(db, sess.ID, 1)` 回灌；④ `chat.go` 补齐会话持久化（此前仅 SSE 端点持久化，`/api/chat` 无记忆——本轮一并修复使两者一致）。
- Commit: 6012fcb
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（cmd/server + api + engine 全绿）。新增 `TestEngine_MultiTurnHistory`（断言 LLM 请求体 messages = [system, user:我叫小明, assistant:收到, user:我刚说了什么名字？]，证明历史回灌）；`TestM0_Integration_E2E` 改造 mock 回声首句并新增第二轮记忆断言（第二轮回复引用第一轮实体「Go Multi-Agent」）；前端未改动，不影响 `npm run build`。

### 2026-07-29 10:14 | M0.5-02 | ✅
- 完成内容：**P1 RBAC 权限矩阵落地（此前 RequirePermission 是死代码）**。`middleware.RequirePermission(db, resource, action)` 接入所有敏感写路由：① Provider 创建/更新/删除接 `providers:write`；② Model 同步/启用接 `models:write`；③ APIKey 管理（创建/列表/吊销）接 `apikeys:write`；④ 新增 `DELETE /api/sessions/:id`（此前无任何删除会话端点）+ `DeleteSessionHandler`/`repo.DeleteSession`（owner-scoped 级联删消息）接 `sessions:write`。`model.SeedRoles` 的 developer 角色新增 `providers:write`/`models:write`/`apikeys:write`，viewer 仅保留 `*_read` → 调写路由被 403。`repo/db.go` 的 `seedRoles` 改为**幂等**（角色已存在时只补齐缺失权限），使已初始化的 `data/codeagent.db` 重启即获得新权限，无需手工迁移。
- Commit: 0b8fc5d
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（全包绿）。新增 `server/cmd/server/rbac_test.go` 的 `TestRBAC_SensitiveRoutes`：viewer 调 9 条写路由全部 403、viewer 读 /api/providers 200、developer 同批路由非 403、developer 合法 POST /api/providers 返回 201（验证 RBAC 不误伤正常写）。M0-19 集成测试（developer 账号）仍全绿，证明新权限未破坏既有链路。前端未改动。
- 下一步：PLAN 中 M0.5-03（SessionKey 复合唯一索引 + GetOrCreateSession 冲突处理）成为下一个 ○，下轮继续。

### 2026-07-29 10:24 | M0.5-03 | ✅
- 完成内容：**P1 SessionKey 唯一约束（复合唯一索引 + 并发冲突处理）**。`model.Session` 的 `SessionKey` 由全局 `uniqueIndex` 改为复合唯一索引 `UNIQUE(user_id, session_key)`（`idx_user_session`，user_id priority:1 / session_key priority:2），允许不同用户复用同一 key、禁止同用户重复建行；`repo/session.go` 的 `GetOrCreateSession` 改为「先查→miss 则建→唯一约束冲突则重试查询已有行」的循环（最多 3 次，自动生成的 key 冲突时重新随机），消除并发竞态、不产生脏数据；新增 `repo/db.go` 的 `migrateCompositeSessionKey` 在 AutoMigrate 后按 sql 文本特征删除遗留的单列 `session_key` 唯一索引（`DROP INDEX IF EXISTS`，幂等安全），避免旧库仍被全局唯一索引阻断跨用户复用。
- Commit: b7cc989
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（repo 包新增 3 用例全绿：TestCrossUserSameSessionKeyAllowed 跨用户同 key 落 2 行、TestSameUserDuplicateKeyIdempotent 同用户同 key 仅 1 行、TestConcurrentGetOrCreateSession 20 并发最终仅 1 行且各 goroutine 拿到同一 id）；顺手修复 `TestListSessionsScopedAndOrdered` 的时序 flake（AppendMessage 前 sleep 10ms 确保 updated_at 严格更晚，消排序不稳定）。前端未改动。
- 下一步：PLAN 中 M0.5-04（delta 累加公共函数）成为下一个 ○，下轮继续。
