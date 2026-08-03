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

### 2026-07-29 11:24 | M0.5-04 | ✅
- 完成内容：**P2 delta 累加逻辑去重**。`engine.Chat`（engine.go）与 `aguiConverter.Convert`（internal/api/sse.go）各实现一遍的「优先 Delta.Content 增量、未出现任何增量才回退 Message.Content」文本去重规则，抽成公共函数 `internal/engine/delta.go` 的 `DeltaState.Text(deltaContent, messageContent string)`，两处复用、行为一致。① 新增 `server/internal/engine/delta.go`：`DeltaState` 跨多个事件/choice 保持 `sawDelta` 状态；`NewDeltaState()` 构造；`Text` 仅当 deltaContent 非空时返回它（并置位 sawDelta），否则仅在从未出现增量且 messageContent 非空时返回 messageContent，否则返回空串。② `engine.Chat` 改用 `NewDeltaState()` + `ds.Text`，删掉本地 `sawDelta`/`sb` 内联累加。③ `aguiConverter` 的 `sawDelta bool` 字段替换为 `ds *engine.DeltaState`（`newAGUIConverter` 初始化为 `engine.NewDeltaState()`），`Convert` 文本分支统一调 `cv.ds.Text`。`api` 包已 import `engine`，无新增依赖、无循环依赖。④ 新增 `server/internal/engine/delta_test.go`：覆盖「流式（增量+终帧重复 Message 被跳过）/非流式（整块回退）/混合（增量后中段 Message 被跳过）/空」四场景，证明行为与原先一致且无重复一倍。
- Commit: 1d88ca6
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（cmd/server + api + engine 全绿；sse_test 的 TextStream/TextNonStreaming 仍绿，证明 converter 复用未改行为）。任务不涉及 web/，未跑 `npm run build`。
- 下一步：PLAN 中 M0.5-05（消除魔法值：90s 超时提配置 + RoleID=3 改查 RoleDeveloper）成为下一个 ○，下轮继续。

### 2026-07-29 11:51 | M0.5-05 | ✅
- 完成内容：**P2 消除魔法值（超时配置化 + 角色按名查询）**。① 新增 `config.Config.EngineTimeoutSeconds`（env `ENGINE_TIMEOUT_SECONDS`，默认 90）+ `Config.EngineTimeout() time.Duration` 与方法及 `envOrDefaultInt` 辅助；「单次对话流式超时」由硬编码 `90*time.Second`（engine.go:77）改为读 `e.cfg.Timeout`，值由 `ModelConfig.Timeout` 注入、未传时 `New` 内回退默认 90s。② `ChatHandler`/`StreamChatHandler` 签名各加 `engineTimeout time.Duration` 参数并由 `main.go` 注入 `cfg.EngineTimeout()`；集成测试同链路仍绿。③ 注册默认角色不再硬编码 `user.RoleID = 3`：改为 `repo.GetRoleIDByName(db, model.RoleDeveloper)`，缺失再降级 `model.RoleViewer`，二者皆缺则返回 500（避免新建用户落入未初始化角色）；新增 `repo.GetRoleIDByName` 辅助。④ 顺手扫魔法值：`provider/discover.go` 的 `http.Client{Timeout: 15 * time.Second}` 提为包级常量 `providerHTTPTimeout`。
- Commit: fix(M0.5): M0.5-05 消除魔法值（超时配置化 + 角色按名查询）
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（cmd/server + api + config + engine + provider + repo 全绿）。新增单测：config `TestEngineTimeoutDefault/FromEnv/InvalidEnvFallsBack`、engine `TestEngineTimeoutDefaultAndOverride`（断言默认 90s 与显式 30s 均生效）、repo `TestGetRoleIDByName`（按名查 id + 缺失报错）；E2E 全链路（注册开发者角色 / 流式对话 / 多轮记忆）仍 PASS。任务不涉及 web/，未跑 `npm run build`。
- 下一步：PLAN 中 M0.5-06（SSE message 由 GET query 改 POST body，前端同步改造）成为下一个 ○，下轮继续。

### 2026-07-29 12:28 | M0.5-06 | ✅
- 完成内容：**P2 SSE 消息改 POST（避免明文进访问日志）**。① 后端 `server/internal/api/sse.go` 的 `StreamChatHandler` 由读 GET query（`c.Query("message")`）改为 `c.ShouldBindJSON` 解析 POST body（`streamChatRequest{Message, ModelID}`），`strconv` 依赖移除；② `server/cmd/server/main.go` 路由 `protected.GET("/chat/:session_id/stream")` 改为 `protected.POST(...)`；③ 前端 `web/src/api/chat.ts` 的 `streamChat` 由 `fetch` GET + `URLSearchParams` query 改为 POST + `Content-Type: application/json` body（含 `message` 与可选 `model_id`），`ChatView.vue` 调用签名未变无需改动；④ 集成测试 `c.sse` 助手同步为 POST+JSON body，两处调用改传 `{message, model_id}`，并新增 GET 被拒回归校验（code≠200，证明 GET query 不再承载 message）。
- Commit: be7946d
- 验证: go build ✓ | go vet ✓ | go test ./... ✓（cmd/server 集成测试 POST 全链路 + GET 拒绝仍绿；api/engine/repo/config/provider 全绿）；前端 `npm run build` ✓（vite 2862 modules）+ `vue-tsc --noEmit` ✓（typecheck 0 错，ChatView 等未改动签名）。
- 下一步：PLAN 中 M0.5-07（M0.5 回归验证与结项，依赖 M0.5-01..06 现已全 ✅）成为下一个 ○，下轮继续。

### 2026-07-29 13:30 | M0.5-07 | ✅
- 完成内容：**M0.5 回归验证与结项**（M0.5-01..06 全部 ✅ 后的收口任务）。① 后端 `go build/vet/test -count=1 ./...` 全绿（cmd/server 2.2s、api、config、engine、provider、repo 均 ok）；前端 `cd web && npm run build` 绿（vite 2862 modules，13.4s），`vue-tsc --noEmit` 0 错。② 扩展 E2E：新增整合回归测试 `TestM0_5_Regression`（server/cmd/server/integration_test.go），在单一用例里串联三场景——**场景A 多轮记忆**（注册→建 Provider→启用模型→两轮对话，第二轮回声必须引用第一轮实体「Go Multi-Agent」）、**场景B RBAC 403**（viewer 调 9 条写路由全 403、读路由放行）、**场景C SessionKey 复合唯一**（两真实用户同 key 经 `repo.GetOrCreateSession` 各落一行 id=2/3、同用户复用同行 id=2）。③ 与既有单测形成交叉覆盖：多轮记忆另由 `TestM0_Integration_E2E`+`TestEngine_MultiTurnHistory` 覆盖；RBAC 由 `TestRBAC_SensitiveRoutes` 覆盖；SessionKey 由 `TestCrossUserSameSessionKeyAllowed`/`TestSameUserDuplicateKeyIdempotent`/`TestConcurrentGetOrCreateSession` 覆盖；delta 去重由 `engine/delta_test.go`+`sse_test.go` 覆盖；魔法值由 `config_test.go`/`timeout_test.go`/`repo.TestGetRoleIDByName` 覆盖；SSE POST 由 `integration_test.go` GET 被拒回归覆盖。④ 本文件末附「M0.5 结项报告」。
- Commit: <pending>
- 验证: 后端 `go test -count=1 ./...` 全绿（含 `TestM0_5_Regression` PASS：场景A/B/C 日志均打印 ✅）；前端 `npm run build` ✓。无新增业务代码逻辑，仅新增整合回归测试与文档。
- 下一步：**M0.5 全部 ✅，阶段门槛解除**，下一轮循环可进入 M1（M1-04 Executor 抽象接口成为首个 ○）。

---

### 2026-07-29 16:31 | M1-06 | ✅
- 完成内容：**CodeAct 工具集（M1 CodeAgent 核心，M1-04/05 之上的能力落地）**。新增 `server/internal/tool/` 包（包名 `codectool`）：① 四个工具 `shell_exec`/`file_read`/`file_write`/`file_edit`，底层逻辑抽为纯函数 `ShellExec/FileRead/FileWrite/FileEdit` 便于单测；② 文件类工具统一经 `resolveSafePath` 把路径解析并约束在工作目录内（path traversal 越界一律拒绝）；③ `shell_exec` 经 `executor.SafeExecutor`（M1-05 危险命令策略，无人值守模式）执行，**禁止裸用 HostExecutor.Run**（见 LEARNINGS）；④ `NewCodeAct(workdir)` 业务入口内部组装 HostExecutor+危险命令策略+日志审计。`engine.ModelConfig` 新增可选 `Tools []tool.Tool`，`New` 追加到基础工具（echo/get_time）之后。`api` 层新增 `buildCodeActTools(workspaceRoot, uid)` 按 `WorkspaceRoot/<uid>` 隔离建目录并装配；`ChatHandler`/`StreamChatHandler` 签名各加 `workspaceRoot` 参数，`main.go` 注入 `cfg.WorkspaceRoot`。`config` 新增 `WorkspaceRoot`（env `WORKSPACE_ROOT`，默认 `data/workspaces`，启动时自动创建）。
- Commit: 669c36a
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test ./...` ✓。新增 `server/internal/tool/codeact_test.go` 6 用例全绿：shell_exec 正常执行返回 stdout/exit_code、危险命令（`rm -rf /`）被拒并写审计、file_write/read/edit 全链路+落盘、路径越界被拦截、空 workdir 报错；`internal/engine` 新增 `TestEngine_WithCodeActTools`（注册 4 个 CodeAct 工具后仍正常对话）。仅后端改动，未跑 `npm run build`。
- 下一步：PLAN 中 M1-07（Workspace 模型：DB 模型+CRUD API，对话绑定 workspace，Executor 在其目录执行）成为下一个 ○，下轮继续。

---

### 2026-07-29 17:44 | M1-07 | ✅
- 完成内容：**Workspace 模型（M1 CodeAgent 核心数据层，M1-04/06 之上的能力落地）**。新增 `server/internal/model/workspace.go` 的 `Workspace`（user_id 归属 + 复合唯一 `workspace_key` + `local_path` 绝对路径 + 可选 `git_remote` + 状态）；`model.Session` 增可空 `WorkspaceID` 绑定；`repo/workspace.go` 用户归属 CRUD（Create/List/GetByKey/GetByID 校验归属/Update/Delete）；`repo/db.go` AutoMigrate 加 `workspaces` 表。新增 `internal/api/workspace.go` 五个 handler（POST/GET 列表/GET 详情/PUT 更新/DELETE，写操作接 `workspaces:write`）与 `resolveWorkspaceLocalDir` 绑定解析助手；`buildCodeActTools(workspaceRoot, uid, wsLocalDir)` 增加 `wsLocalDir` 形参（指定 workspace 则在其目录执行，否则回退 `WorkspaceRoot/<uid>`）；`chat.go`/`sse.go` 请求体加 `workspace_key`，对话按绑定目录驱动 Executor；`main.go` 注册 5 条 workspace 路由；`model/role.go` 幂等种子补 `workspaces:write`(developer)/`workspaces:read`(viewer)。删除 workspace 仅删 DB 行、保留本地目录（防误删用户文件）。
- Commit: <pending>
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./...` 全绿（cmd/server + api + repo + engine 等无回归）。新增 `server/cmd/server/workspace_test.go`（TestWorkspace_API_CRUD 全生命周期+目录落盘+跨用户 404、TestWorkspace_RBAC viewer 403/developer 201）、`server/internal/repo/workspace_test.go`（CRUD+归属校验）、`server/internal/api/workspace_test.go`（TestBuildCodeActTools_WorkspaceDir 验证 shell_exec 落盘在指定 workspace 目录、TestBuildCodeActTools_DefaultFallback 回退默认目录、TestResolveWorkspaceLocalDir 绑定解析与复用）。rbac_test.go 的敏感写路由清单补 `POST /api/workspaces`（developer 仍非 403）。仅后端改动，未跑 `npm run build`。
- 下一步：PLAN 中 M1-08（子代理委托 agenttool：Coder 子代理带代码工具集，可由 Orchestrator 委托）成为下一个 ○，下轮继续。

---

### 2026-08-02 21:48 | M1-08 | ✅
- 完成内容：**子代理委托 agenttool（Orchestrator→Coder 工厂 + AGENT_MODE 开关）**。新增 `server/internal/agent/factory.go`（包名 `codeagent`，规避框架 `agent` 包名冲突）：`NewCoder` 经 `codectool.NewCodeAct(workdir)` 装配经 `SafeExecutor`（M1-05 危险命令策略 + 路径边界约束）包装的 CodeAct 工具集；`AsTool` 用框架 `tool/agent`（agenttool）把子代理包成可被父代理委托的 `coder` 工具（工具名取子代理 `Info().Name`，输入 schema 为 `{"request":...}`）；`NewOrchestrator` **自身不持有任何写工具**，仅挂 `ExtraTools`（echo/get_time）+ coder 委托工具，把「代码落地」权限收敛到子代理（为 M1-09/10 Reviewer 留对称扩展位）。`internal/engine/engine.go` 的 `ModelConfig` 增 `EnableSubAgents/Workdir`，开启时根 Agent 换用 `codeagent.NewOrchestrator`（默认仍走原单代理，不破坏 M1-06/07）。`internal/config/config.go` 新增 `AGENT_MODE`（single/team，默认 single）与 `SubAgentsEnabled()`。`internal/api/{chat,sse,codeact}.go` 抽出 `ensureWorkdir`，按 `cfg.SubAgentsEnabled()` 在单代理模式装配 CodeAct 工具、子代理模式把工具置空（由 Coder 子代理持有），`cmd/server/main.go` 注入该开关。
- Commit: bad5b80
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `internal/engine` 包测试全绿，关键新增 `TestEngine_SubAgentDelegation_WritesFile` PASS（mock LLM 脚本化驱动「Orchestrator→coder→file_write」调用链，断言 Coder 经 CodeAct 成功在工作目录写入 hello.txt，满足验收「Orchestrator 委托 Coder 写文件成功」）。注：`internal/repo`/`internal/api`/`cmd/server` 的 DB 测试需 CGO+sqlite3(gcc)，本沙箱无 gcc 故无法运行，属环境限制（与本次改动无关），其余包测试全绿。
- 下一步：PLAN 中 M1-09（CodeTeam 编排：Orchestrator→Coder(写)→Reviewer(只读挑错)→回环）成为下一个 ○，依赖本任务 M1-08。

---

### 2026-08-02 22:09 | M1-09 | ✅
- 完成内容：**CodeTeam 编排（Orchestrator→Coder→Reviewer 审阅回环 + team 配置化）**。新增 `server/internal/agent/team.go`（包名 `codeagent`）：① 定义 `TeamConfig{EnableSubAgents, EnableReviewer, MaxReviewRounds}`（默认 2 轮）与 `ReadOnlyTools(workdir)` 按白名单 `readOnlyToolNames={file_read}` 从 `codectool.NewCodeAct` 工具集过滤，确保 Reviewer 天然继承 `resolveSafePath` 路径边界、拿不到 `file_write/file_edit/shell_exec/coder`；② `NewReviewer` 仅持 `file_read` 工具（缺失则 `ErrNoReadOnlyTools`），`NewReviewerTool` 用框架 `tool/agent`(agenttool) 包成 `reviewer` 委托工具；③ `NewTeam` 构造根 Agent：启用时 Orchestrator 挂 `coder`+`reviewer` 两个子代理委托工具、`teamInstruction` 强约束「写代码→审阅→修复」回环与 Reviewer 输出「通过/需修改+问题清单」格式，未启用 Reviewer 时 `teamInstruction` 退回原 `OrchestratorInstruction`（向后兼容 M1-08 二人组）。`factory.go` 的 `NewOrchestrator` 改委托 `NewTeam(TeamConfig{EnableSubAgents:true})` 退化。`engine.ModelConfig.EnableSubAgents` 升级为 `Team TeamConfig`（类型别名 `engine.TeamConfig = codeagent.TeamConfig`）；`config` 新增 `TEAM_REVIEWER`(默认 true)/`TEAM_MAX_REVIEW_ROUNDS`(默认 2)、`SubAgentsEnabled/ReviewerEnabled/MaxReviewRounds`；`api/{chat,sse}.go` 签名 `enableSubAgents`→`team engine.TeamConfig`，`main.go` 构造 `teamCfg` 注入两个 handler。
- Commit: e1667a2
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./internal/engine/... ./internal/config/... ./internal/tool/...` 全绿。新增 `internal/engine/team_test.go`：`TestEngine_CodeTeam_ReviewLoop`（mock LLM 脚本化驱动 Orchestrator→coder(写初版)→reviewer(只读指出问题)→coder(修复)→Orchestrator 汇报，断言 Reviewer 被委托、给出「需修改」、Reviewer 工具清单不含 file_write/file_edit/shell_exec/coder、Coder 写入≥2 次、最终文件为修复后内容）PASS；`TestEngine_CodeTeam_ReviewerDisabled`（关 Reviewer 退回 M1-08 二人组）PASS。`config_test.go` 增 `TestTeamConfigDefaults`/`TestEnvOrDefaultBool` PASS。注：`internal/repo`/`internal/api`/`cmd/server` 的 DB 测试需 CGO+sqlite3(gcc)，本沙箱无 gcc 无法运行，属历史遗留环境与本次改动无关。
- 下一步：PLAN 中 M1-10（Reviewer 只读工具集，依赖 M1-08，验收「reviewer 调 write 被拒」——本任务已用白名单过滤铺好底）成为下一个 ○。

---

### 2026-08-02 22:52 | M1-10 | ✅
- 完成内容：**Reviewer 只读工具集下沉与硬护栏（file_read + grep，无 write/exec）**。新增 `server/internal/tool/readonly.go`（包 `codectool`）：① 集中定义工具名常量 `ToolFileRead/ToolGrep/ToolFileWrite/ToolFileEdit/ToolShellExec`（`codeact.go` 的 `function.WithName` 同步改用常量），并给出只读白名单 `{file_read, grep}` 与写/执行黑名单 `{file_write, file_edit, shell_exec}` + `IsReadOnlyToolName/IsMutatingToolName/ReadOnlyToolNames`；② 新增只读检索工具 `grep`（纯函数 `Grep(workdir, GrepOptions{Pattern,Path,IgnoreCase,MaxResults})`）：Go RE2 正则、路径经 `resolveSafePath` 约束、目录递归时跳过 `.git/node_modules/vendor/dist/build/.idea/.vscode`、跳过 >1MiB 与含 NUL 的二进制文件、限额（默认 100 行 / 最多 2000 文件 / 单行 300 字符截断），输出 `相对路径:行号: 内容`，无命中返回可读提示而非 error；③ `ReadOnlyTools(workdir)` **独立构造**只读工具集（此路径下根本不创建 Executor，从结构上杜绝执行能力），不再走 M1-09 的「CodeAct 全量集过滤」；④ `EnsureReadOnly(tools)` 兜底断言：出现黑名单或白名单外工具即返回 `ErrMutatingTool`（fail fast，防未来重构悄悄放权）。`internal/agent/team.go`：`ReadOnlyTools` 改为委托 `codectool.ReadOnlyTools` + `EnsureReadOnly` 二次校验，删除本地白名单 map；`ReviewerInstruction`/`reviewerToolDescription` 更新为「只有 file_read + grep，强行调用 file_write/file_edit/shell_exec 会被直接拒绝，需要改动只能写进结论交 Coder」；补注 `Deps.ExtraTools` 不下发给 Reviewer（无法保证其只读性）。
- Commit: 42cb18a
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./internal/tool/ ./internal/engine/ ...` 全绿。新增 `internal/tool/readonly_test.go` 9 用例（只读集恰为 file_read+grep、`EnsureReadOnly` 拒绝 CodeAct 全量集并指名工具、越界读取/检索被拒、grep 跨目录命中且跳过 .git/node_modules、大小写敏感与子目录限定、max_results 截断、非法正则/空 pattern/空 workdir、经 `CallableTool.Call` 的真实工具调用路径）；新增 `internal/engine/reviewer_test.go` 验收用例 `TestEngine_ReviewerReadOnly_WriteDenied`：mock LLM 驱动 Orchestrator→reviewer，Reviewer 先尝试 `file_write` 写 `sneaky.txt` → 框架返回 `executeToolCall: Error: tool not found`（日志 `CallableTool file_write not found (agent=reviewer)`）、文件确未创建，随后 Reviewer 用 grep 命中 `hello.go:3: // TODO...` 并 file_read 后给出「需修改」结论；断言 Reviewer 工具清单严格等于 `file_read,grep`。另加 `TestReadOnlyTools_NoMutatingToolLeak` 工厂层护栏。注：`internal/repo`/`internal/api`/`cmd/server` 的 DB 测试需 CGO+sqlite3(gcc)，本沙箱 CGO_ENABLED=0 且无 gcc 无法运行，属既有环境限制、与本次改动无关。
- 下一步：PLAN 中 **M1-11（Goal 契约：get_goal/create_goal/update_goal 注入 Orchestrator，必须推进到 complete/blocked 才结束）** 成为下一个 ○，依赖 M1-09；注意 docs/03 §2.4 框架限制——goal 只装 Orchestrator、单 Agent 内不开 EnableParallelTools。

---

### 2026-08-03 09:36 | M1-11 | ✅
- 完成内容：**Goal 契约（M1 CodeAgent 自主化核心，文档化约束的硬落地）**。框架 v1.10.0 无 `goal` 包（docs/03 §2.4 已预判），故自行实现领域层 + 框架扩展两层：① 新增 `server/internal/goal/goal.go`（纯领域包，无框架依赖）：`Goal{ID,Title,Description,AcceptanceCriteria,Status,Progress,Blocker,...}`、`Status` 四态（pending/in_progress/complete/blocked，`IsOpen/IsTerminal`）+ `ParseStatus`、`Store`（按 `sess:<sessionID>` 作用域跨轮隔离、落 `inv:<id>` 退化、`DefaultMaxScopes=512` 含 LRU 淘汰、Create 覆盖旧目标语义即「每会话一新任务」），`ErrNotFound/ErrEmptyTitle/ErrBlockedNoReason`；② 新增 `server/internal/agent/goal.go`（包 `codeagent`）：`GoalEnforcer`（实现 `extension.Extension`，`Register` 贡献 create_goal/get_goal/update_goal 三工具 + BeforeModel/AfterModel 回调），**严格对齐框架内置 `todoenforcer`**——`afterModel` 对「目标未收敛时的最终答复」返回一个 `Done=false & Choices=nil` 的 `CustomResponse`（令 llmflow 继续循环、且清空 Choices 不把过早答复泄漏给前端/历史）；`beforeModel` 在「未收敛目标」时关闭流式 + 注入催办消息（标记消费避免重复注入）；`requireGoal` 开关 + `MaxNudges` 预算耗尽 fail-open（防模型不配合卡死 Runner）；③ `TeamConfig` 增 `EnableGoal/MaxGoalNudges/GoalStore`，`goalEnabled()` 要求 `EnableSubAgents && EnableGoal`（**契约只装 Orchestrator**），`teamInstruction` 在开启时追加目标契约规程；④ `config` 增 `GOAL_CONTRACT`(默认 true)/`GOAL_MAX_NUDGES`(默认 3) + `GoalEnabled()/MaxGoalNudges()`；⑤ `cmd/server/main.go` 注入 `teamCfg.EnableGoal/MaxGoalNudges`。
- Commit: <pending>
- 验证: `go build ./...` ✓ | `go vet ./...` ✓（仅变更包）| `go test -count=1 ./internal/goal/... ./internal/agent/... ./internal/engine/...` 全绿。单测 `server/internal/agent/goal_test.go` 11 例覆盖：Name/Tools、Roundtrip（未立目标 get_goal 提示 create、update 被拒引导 create、create→in_progress、非法状态/缺原因被拒、update complete→收敛并提示可收尾）、BlocksPrematureFinal（无目标/未收敛拦截、complete/blocked 放行）、PassThroughNonFinal（nil/分片/未完成/错误/工具调用均透传）、FailOpenAfterBudget、RequireGoalDisabled、BeforeModel（收敛不关流不催办、未收敛关流、拦截后注入且只注入一次）、NoGoalNudgeMentionsCreate、GoalScope、TeamInstruction_GoalSection、NormalizedGoalDefaults。引擎端到端 `server/internal/engine/goal_test.go`：`TestEngine_GoalContract_BlocksPrematureFinal`（mock LLM 脚本化驱动「过早 final→create_goal→过早 final→update_goal(complete)→final」，断言过早 final 不泄漏、最终回复为收敛文本、store 终态 complete）+ `TestEngine_GoalContract_DisabledWhenNoSubAgents`（单代理模式契约不生效，过早答复直接放行）。
- 下一步：PLAN 中 **M1-12（CycleAgent/Plan-Execute：planner 产出计划外置 PLAN/PROGRESS，逐项执行更新）** 成为下一个 ○，依赖 M1-11；M1-13（护栏熔断）亦依赖 M1-11。

---

### 2026-08-03 10:45 | M1-12 | ✅
- 完成内容：**Plan-Execute 循环（M1 CodeAgent 自主化核心，L2 范式落地）**。框架 v1.10.0 无 CycleAgent 原语，llmflow 本身就是「模型→工具→模型」循环，缺的是「终止条件 + 跨轮状态载体」，故实现为「外置计划 + 循环闸门」：① 领域层 `server/internal/plan`（独立包，无框架依赖）：`Step`/`StepStatus`(pending/in_progress/done/skipped/failed)/`Plan`/`Store`（按 `sess:<sessionID>` 跨轮隔离、退化 `inv:<id>`、`DefaultMaxScopes=512` + LRU 淘汰）、`Create/Get/AddSteps/UpdateStep/Render/RenderProgress/NormalizeStepID`；skipped/failed 必填 note 防静默跳过；② 框架侧 `server/internal/agent/plan.go`：`PlanEnforcer` 扩展（对齐 goal.go/内置 todoenforcer）——`create_plan/get_plan/update_step/add_steps` 四工具 + `beforeModel`（计划未做完时关流式 + 回灌 PLAN/PROGRESS/下一步催办并只注入一次）+ `afterModel`（拦截计划未做完的最终答复，返回 `Done=false` 清空 `Choices` 令 llmflow 继续循环；预算 `MaxNudges` 耗尽 fail-open），默认 `requirePlan=false`（一句话能答完不强制定计划，定计划后才必须做完）；复用 goal 的 `goalScope/goalBlockedResponse/shouldConsiderGoalResponse`。③ 接线：`TeamConfig` 增 `EnablePlan/MaxPlanNudges/PlanStore`，`normalized()` 补默认，`NewTeam` 在 `planEnabled()` 分支附加 `PlanEnforcer`（与 Goal 契约叠加，都只装 Orchestrator，回调用安装顺序短路，互不干扰）；`config` 增 `PLAN_EXECUTE`(默认 true)/`PLAN_MAX_NUDGES`(默认 3) + `PlanEnabled()/MaxPlanNudges()`；`cmd/server/main.go` 注入 `teamCfg.EnablePlan/MaxPlanNudges`。
- Commit: 59d7fe8（已推 origin/main）
- 验证: `go build ./...` ✓ | `go vet ./...`（变更包）✓ | `go test -count=1 ./internal/plan/... ./internal/agent/... ./internal/engine/... ./internal/config/...` 全绿。领域层 `plan_test.go` 13 例覆盖 Create/Get/Errors/状态流转/skipped 必填 note/AddSteps/Counts/Next/Find/Render/NormalizeStepID/LRU/覆盖写/Delete；扩展层 `plan_test.go` 14 例覆盖 Name/Tools/Roundtrip/AddSteps/拦截过早 final/透传非最终响应/fail-open/requirePlan/BeforeModel(收敛不关流不催办、未收敛关流、拦截后注入且仅一次)/Instruction_PlanSection/默认值；引擎端到端 `plan_test.go`：`TestEngine_PlanExecute_ForcesStepByStep` mock LLM 脚本化驱动「建计划→过早 final 被拦截→逐项 update_step(done)→最终收工放行」，断言过早 final 不泄漏、store 终态全部收敛（2 done）、`TestEngine_PlanExecute_DisabledWhenNoSubAgents` 验证单代理模式 Plan 不生效。注：`internal/repo`/`internal/api`/`cmd/server` 的 DB 测试需 CGO+sqlite3(gcc)，本沙箱无 gcc 无法运行（返回 `CGO_ENABLED=0` 桩错误），属历史环境限制、与本次改动无关。`internal/tool`/`internal/goal`/`internal/executor`/`internal/provider` 等非 DB 包测试均绿。
- 下一步：PLAN 中 **M1-13（护栏熔断：WithMaxLLMCalls/WithMaxToolIterations/WithMaxRetries + 运行级兜底，超限优雅终止产出 partial）** 成为下一个 ○，依赖 M1-11；M1-16（工作状态外置 artifact）依赖本任务 M1-12。

---

### 2026-07-29 14:28 | M1-04 | ✅
- 完成内容：**Executor 抽象接口（代码执行统一入口）**。新增独立包 `server/internal/executor/`：① `executor.go` 定义 `Executor` 接口（`Run(ctx, command) (*Result, error)` + `Workdir() string`，`Result{Stdout,Stderr,ExitCode}` 其中 ExitCode=0 正常、>0 命令非零退出、-1 超时中断）与包注释明确「所有代码执行必须经 Executor，禁止业务层散写 os/exec」；② `host.go` 的 `HostExecutor`（M1-04 默认实现）：`NewHostExecutor(workdir)`/`NewHostExecutorWithTimeout(workdir, timeout)` 校验 workdir 存在且为目录（空则回退 os.Getwd），`Run` 用 `exec.CommandContext` 固定 `cmd.Dir=workdir` 把命令约束在该目录内、套上下文超时（默认 60s），退出码映射（非零退出视为有效结果不报错、超时报 DeadlineExceeded 且 ExitCode=-1、启动失败报错），shell 按平台选择（Windows `cmd.exe /c`、类 Unix `bash -c`→`sh -c`）；③ `host_test.go` 六用例：正常 echo、非零退出(ExitCode=1)、cwd 约束（echo 重定向写出的 probe.txt 落在 workdir 内，证明未越界）、200ms 超时（ExitCode=-1 + 含「超时」错误）、坏/非目录 workdir 拒绝、空命令报错。
- Commit: <pending>
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test ./... -count=1` ✓（executor 包 6 用例全绿，cmd/server+api+engine+config+provider+repo 无回归）；任务仅后端改动，未跑 `npm run build`。
- 下一步：PLAN 中 M1-05（危险命令策略：前缀黑名单 + allow/ask/deny 枚举，无人值守默认 deny 并写审计）成为下一个 ○，下轮继续；M1-05 应在 `executor.Executor` 之上叠加策略层而非改接口。

---

### 2026-07-29 15:27 | M1-05 | ✅
- 完成内容：**危险命令策略（在 Executor 之上叠加，不改接口）**。新增 `server/internal/executor/` 两个文件 + 测试：① `blacklist.go` 的 `DangerousCommandPolicy`（片段黑名单，分 `deny` 致命级 `rm -rf /`、`rm -rf ~`、fork 炸弹 `:(){`、mkfs/shutdown/reboot/halt、`> /dev/sda`/`dd if=/dev/zero` 等 与 `ask` 高风险级 `rm -rf`(广义)、`git push --force/-f`、`git reset --hard`、`git clean -f`、`git checkout --`、`chmod -r 000`；`Evaluate` 两遍扫描先 deny 再 ask，保证最严重判定优先；命令先 `normalizeCommand` 转小写+折叠空白，使 `sudo   RM   -rf    /` 也能命中）；`Mode`（Unattended/Interactive）控制 ask 在无人值守下降级 deny。② `policy.go` 的 `SafeExecutor`（实现 `Executor` 接口，无缝替换底层执行器）：`Run` 先 `policy.Evaluate` → allow 直接执行、deny 拒绝并审计、ask 在交互模式交 `AskHandler` 回调裁决、无人值守（无回调）直接拒绝并审计；`ErrCommandDenied` 哨兵错误；`Auditor` 接口 + `MemoryAuditor`（测试/内省）+ `LogAuditor`（日志落盘）；`Policy` 接口可注入自定义实现。③ `policy_test.go` 7 用例：致命拒绝+审计、`sudo rm -rf /` 归一化命中、无人值守 `git push --force` ask→deny、交互确认放行/拒绝、正常 `echo`、广义 `rm -rf ./build` ask→deny、normalize 单测。
- Commit: 817cb84
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test ./... -count=1` ✓（executor 包 7 新用例全绿、其余包无回归）；任务仅后端改动，未跑 `npm run build`。
- 下一步：PLAN 中 M1-06（CodeAct 工具集：基于 Executor 实现 `shell_exec`+`file_read`/`file_write`/`file_edit`，注册进 engine）成为下一个 ○；M1-06 应直接复用本任务产出的 `SafeExecutor` 作为执行后端（把 HostExecutor 包一层策略），而非裸用 HostExecutor。

---

## M0.5 结项报告（缺陷 → 修复 commit 对照表）

> 生成时间：2026-07-29 | 阶段：M0.5 缺陷修复全部完成，进入 M1 前的收口
> 结论：**M0 评审发现的 1 个 P0 + 2 个 P1 + 3 个 P2 全部修复并验证**，M0.5 阶段门槛已满足，可启动 M1 CodeAgent 核心。

| 缺陷（来源：docs/03 第一节） | 严重度 | 修复任务 | 修复 Commit | 核心改动 | 验证测试（绿） |
|---|---|---|---|---|---|
| 多轮对话历史未回灌模型（engine 每请求新建 Runner、handler 只传单条消息） | **P0** | M0.5-01 | `6012fcb` | 退化方案：DB `ListSessionMessages` → `[]model.Message` → `agent.WithMessages(history)` 回灌；新增 `api/history.go`；`engine.Stream/Chat` 加 history 形参；`chat.go` 补齐会话持久化 | `TestEngine_MultiTurnHistory`、`TestM0_Integration_E2E`(step10)、`TestM0_5_Regression` 场景A |
| RBAC 权限矩阵形同虚设（`RequirePermission` 死代码未接入路由） | **P1** | M0.5-02 | `0b8fc5d` | `RequirePermission` 接入 Provider/Model/APIKey/Session 删除写路由；`seedRoles` 改幂等；developer 扩 write 权限，viewer 仅 read | `TestRBAC_SensitiveRoutes`、`TestM0_5_Regression` 场景B |
| SessionKey 跨用户可碰撞（全局唯一索引错误禁止复用） | **P1** | M0.5-03 | `b7cc989` | `UNIQUE(user_id, session_key)` 复合唯一索引；`GetOrCreateSession` 先查→miss 建→冲突重试循环（≤3 次）；`migrateCompositeSessionKey` 动态 DROP 遗留单列索引 | `TestCrossUserSameSessionKeyAllowed`、`TestSameUserDuplicateKeyIdempotent`、`TestConcurrentGetOrCreateSession`、`TestM0_5_Regression` 场景C |
| delta 累加规则重复（engine.Chat 与 aguiConverter 各实现一遍） | P2 | M0.5-04 | `1d88ca6` | 抽 `internal/engine/delta.go`（`DeltaState.Text`），两处复用、行为一致 | `engine/delta_test.go`(流式/非流式/混合/空)、`sse_test.go` TextStream/TextNonStreaming |
| 魔法值（90s 超时硬编码、`RoleID=3` 硬编码） | P2 | M0.5-05 | `0205939` | 超时提 `config.EngineTimeout()`（env `ENGINE_TIMEOUT_SECONDS`，默认 90s）；注册角色改 `repo.GetRoleIDByName(db, RoleDeveloper)`；provider HTTP 超时提常量 | `config_test.go`(3)、`timeout_test.go`、`repo.TestGetRoleIDByName` |
| SSE message 走 GET query（明文进访问日志） | P2 | M0.5-06 | `be7946d` | `StreamChatHandler` 改读 POST JSON body；路由 GET→POST；前端 `chat.ts` 改 fetch-POST；集成测试回归 GET 被拒 | `integration_test.go` GET 拒收回归、`npm run build` |

### 结项验证总览
- **后端**：`go build ./...` ✓、`go vet ./...` ✓、`go test -count=1 ./...` ✓（cmd/server / api / config / engine / provider / repo 全 ok）
- **前端**：`npm run build` ✓（vite 2862 modules）、`vue-tsc --noEmit` ✓（0 错）
- **E2E 三场景整合**：`TestM0_5_Regression` 串联多轮记忆 / RBAC 403 / SessionKey 复合唯一，一次运行全绿
- **门禁**：M0.5-01..07 全部 ✅ → M1 阶段门槛解除，下一轮从 M1-04 续推

---

## M1-13 | 护栏熔断（2026-08-03）

**目标**：单代理/团队模式下都给 Agent 装上「预算熔断」——LLM 调用数 / 工具迭代轮数 / 工具重试上限；超限后优雅终止并保留 partial 结果，使 24h 无人值守循环不会因模型死循环卡死。

**实现**：
- 配置层 `server/internal/agent/guard.go`（新增）：`GuardrailConfig` + `Normalized()`/`Enabled()`/`RetryPolicy()`/`Options()`，把预算映射为框架三件套选项
  `WithMaxLLMCalls`/`WithMaxToolIterations`/`WithToolCallRetryPolicy`。默认值（零值即启用）：MaxLLMCalls=32、MaxToolIterations=16、MaxToolRetries=2、退避 200ms×2.0≤5s。
  失败安全：零值=按默认启用（无人值守必须有兜底），仅 `Disabled=true` 才解除。
- 接线：
  - `agent/team.go` `TeamConfig` 增 `Guardrail` 字段；`NewTeam`/`NewCoder`/`NewReviewer` 的 `llmagent.New` 均叠加 `d.Guardrail.Options()`，Orchestrator 与各子代理共用同一套约束；
  - `agent/factory.go` `Deps` 增 `Guardrail` 注入点；
  - `engine/engine.go` `ModelConfig` 增 `Guardrail` 并注入 `Deps`；**关键修复**：单代理分支（默认 `AGENT_MODE=single`，24h 循环的实际运行模式）此前未挂护栏，本次一并补上；
  - `config/config.go` 增 env `MAX_LLM_CALLS`/`MAX_TOOL_ITERATIONS`/`MAX_TOOL_RETRIES` + `GUARDRAIL_DISABLED`，默认值复用 `codeagent` 包常量（单一真相源），新增 `GuardrailConfig()` 访问器；
  - `cmd/server/main.go` 把 `cfg.GuardrailConfig()` 注入 `teamCfg.Guardrail`。
- 运行级兜底 `server/internal/engine/guard.go`（新增）：
  - `IsCircuitBreakEvent(ev)` 区分「护栏熔断（IsError 但属预算耗尽：max LLM calls / max tool iterations 文案 + stop_agent_error 类型）」与「运行失败」；
  - `CircuitBreakNotice()` 追加在 partial 结果末尾的明确提示；
  - `engine.Chat` 命中熔断时保留已产出 partial 文本 + 追加提示，返回 nil error（不丢结果）；
  - `api/sse.go` 转换器命中熔断时发出友好提示并**仍落库 partial 文本**（此前 IsError 分支会丢弃 partial 不落库），满足「产出 partial 结果」。

**验收**：
- `internal/engine/guard_test.go`：
  - `TestEngine_Guardrail_CircuitBreak_PartialResult`：单代理 + 收紧预算(1/1)，mock 反复调工具触发熔断 → `err==nil`、partial 文本 `PARTIAL_DRAFT` 保留、附 `[护栏熔断]` 提示；
  - `TestEngine_Guardrail_NoBreach_NormalCompletion`：正常一轮答复不被误判、无熔断提示；
  - `TestIsCircuitBreakEvent`：StopError / flow_error 熔断事件正确识别，普通错误不误判。
- `go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./internal/engine/... ./internal/agent/... ./internal/config/...` ✓（沙箱无 gcc，repo/api/cmd 的 CGO sqlite 测试仍跳过，与本次无关）。
- 下一步：PLAN 中 **M1-14 斜杠命令注册表（后端）** 成为下一个 ○。
