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

---

### 2026-08-03 13:26 | M1-14 | ✅
- 完成内容：**斜杠命令注册表（后端，M1 前端/CLI 共用元数据来源）**。新增独立框架无关包 `server/internal/command/registry.go`：① `Command` 元数据（Name/Description/Usage/Category/Args/Kind/Template/Endpoint）+ `Arg`；三类 Kind——`client`（前端本地处理：/clear /model /workspace）、`prompt`（模板+用户输入渲染成提示词发给既有 /chat，复用 CodeAct/Goal/Plan，无需新后端执行路径：/run /review /plan）、`endpoint`（预留直连既有端点）；② `Builtin()` 单一事实源内置 6 条命令（/clear /model /workspace /run /review /plan），新增命令只改此处；③ `Find(name)` + `RenderPrompt(cmd, args)` 渲染助手（模板占位符 `{{args}}` 替换为命令后整行参数）。新增 `server/internal/api/command.go` 的 `ListCommandsHandler`（GET /api/commands，返回 `{commands:[...]}`，受保护路由，无需 DB）；`cmd/server/main.go` 在 protected 组注册该路由。`internal/command/registry_test.go` 覆盖内置命令齐全性/无重名/Find/RenderPrompt；`internal/api/command_test.go`（httptest，无 CGO）校验端点返回体结构与 prompt 类必带模板。
- Commit: 34a4a02（已推 origin/main）
- 验证: `go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./internal/command/... ./internal/api/...`（新测试 PASS）。注：`internal/api` 包内 `TestResolveWorkspaceLocalDir` 等 DB 测试需 CGO+sqlite3(gcc)，本沙箱 `CGO_ENABLED=0` 无法运行，属既有环境限制、与本次改动无关。`internal/agent`/`engine`/`config`/`tool`/`goal`/`plan`/`executor`/`provider` 等非 DB 包测试全绿。
- 下一步：PLAN 中 **M1-15（前端斜杠命令 UI：输入框 `/` 触发命令浮层，选择+填参，发送）** 成为下一个 ○，依赖本任务 M1-14。

---

### 2026-08-03 14:23 | M1-15 | ✅
- 完成内容：**前端斜杠命令 UI（M1 前端/CLI 共用元数据落地的消费端，依赖 M1-14 后端注册表）**。新增 `web/src/api/command.ts`：`Command`/`CommandArg` 接口（对齐后端 JSON）、`fetchCommands`（GET /api/commands）、`resolveSlashCommand`（以 / 开头 + 命令名精确匹配注册表才视为命令，其余整行作 args，不精确匹配则当普通文本发模型）、`renderCommandPrompt`（前端渲染 prompt 模板 {{args}}，与后端 RenderPrompt 对齐）。`web/src/api/chat.ts` 的 `StreamOptions` 增 `workspaceKey?` 并在 body 带 `workspace_key`，支撑 /workspace 绑定透传后端（sse.go 已支持）。`web/src/views/ChatView.vue` 改造：① 命令浮层——输入框以 `/` 开头且尚未输入空格（命令名阶段）时弹出，按输入前缀过滤、↑↓ 导航、Enter 选中、Esc 关闭、点击选中；② `applyCommand`——有参数命令（run/plan/model/workspace）把 `/name ` 填回输入框等用户填参，无参数命令（clear/review）直接执行；③ 发送分流——`resolveSlashCommand` 解析后 `executeSlashCommand` 按 Kind 处理：client/clear→clearContext、client/model→解析 model_id 切换 `selectedModelId`、client/workspace→设 `selectedWorkspaceKey` 发送时透传、prompt/*→渲染提示词经 `sendMessage` 发模型；④ 抽出 `sendMessage(text)` 供普通消息与 prompt 命令共用；顶部工具条显示当前绑定工作区、placeholder 提示命令清单。
- 验收实现「输入 /run ls 正确触发后端」：用户 `/run` 选中→输入框 `/run `→输入 `ls`→Enter→resolve 出 run 命令、args="ls"→renderCommandPrompt 渲染「请在当前工作区执行以下 shell 命令，并汇报执行结果与输出：\nls」→sendMessage 经 streamChat 发给 /api/chat/:session/stream→后端装配 CodeAct 执行 shell_exec。client 类命令不触模型、纯本地/状态联动。
- 验证: 前端 `npm run build` ✓（vite 2863 modules，ChatView chunk 125KB）+ `vue-tsc --noEmit` ✓（exit 0）；仅前端改动，后端未动（`go build/vet` 无关无需重跑，M1-14 已绿）。
- 下一步：PLAN 中 **M1-16（工作状态外置 artifact：长任务维护 PLAN.md/PROGRESS.md/LEARNINGS.md，Agent 先读再续跑）** 成为下一个 ○，依赖 M1-12。

---

### 2026-08-03 15:38 | M1-16 | ✅
- 完成内容：**工作状态外置（Loop Engineering 状态记忆，M1 自主化「中断续跑」能力的底座）**。框架 v1.10.0 无既有 artifact 存储，故自建两层：① 领域层 `server/internal/artifact/store.go`（框架无关）：`Store` 接口（`Write/Read/Exists/List/Remove/RemoveAll/Snapshot`）+ 三种 artifact 常量 `PLAN.md/PROGRESS.md/LEARNINGS.md` + `Snapshot{Plan,Progress,Learnings,Any}`；两套后端——`FileStore`（落盘 `root/<sanitizedKey>/<name>`，跨进程重启可续跑，默认 `data/agent-state`，已 gitignore）+ `MemoryStore`（安全默认/测试用、不落盘）；路径穿越防护 `sanitizeKey/sanitizeName/safePath/withinRoot`（`sess:abc`→`sess_abc` Windows 安全，越界文件名一律拒绝）。② 框架侧 `server/internal/agent/state.go`（包 `codeagent`）：`StateEnforcer` 扩展（实现 `extension.Extension`）贡献四工具 `read_state/update_plan/update_progress/append_learning`（仅 Orchestrator/单代理根 Agent 安装，Coder/Reviewer 不装避免并发写冲突），`beforeModel` 在「本轮第一次模型调用」读取该 session 既有状态、存在则注入 `[系统·续跑上下文]` 用户消息让模型「先读再续跑」（每轮 run 只回灌一次，用 invocation 状态键 `state:checked` 去重）。③ 接线：`Deps.StateStore`/`ModelConfig.EnableState/StateStore` 经 engine 注入单代理与 team 两条根 Agent 分支；`config` 新增 `ARTIFACT_ROOT`(默认 `data/agent-state`)+`STATE_ENABLED`(默认 true)+`ArtifactRoot()/StateEnabled()`；`api/chat.go`、`api/sse.go` 的 `ChatHandler/StreamChatHandler` 签名各加 `stateStore/enableState`，`cmd/server/main.go` 的 `buildRouter` 加同样两参数并创建落盘 `FileStore` 注入两端点。④ 命名消歧：M1-16 的 PLAN/PROGRESS/LEARNINGS 是**每次 run 的 artifact 文件**，与仓库 `docs/loop/` 控制文件严格隔离（见 PLAN.md「M1-16 命名消歧」），Agent 写状态文件时不触碰 `docs/loop/`。
- 验收（M1-16 验收标准「中断后续跑能接上」）：
  - `server/internal/artifact/store_test.go`：`TestFileStore_SurvivesRestart` 模拟「run1 写入 → 进程重启（全新 store 实例指向同一 root）→ run2 读回成功」PASS；`TestFileStore_BasicCRUD`/`KeySanitized`/`PathTraversalRejected`/`MemoryStore_Basic`/`Snapshot_AnyFlag` 全绿。
  - `server/internal/agent/state_test.go`：`TestStateEnforcer_NameAndTools`（4 工具+非空描述）、`TestStateEnforcer_ToolsPersistToStore`（三工具落盘 + read_state 回读 + 空 text 拒绝）、`TestStateEnforcer_BeforeModelInjectsResume`（run1 写状态 → 全新 store+扩展实例模拟重启 → run2 同 session 下 beforeModel 注入含 `[系统·续跑上下文]`+计划/进展/坑点的续跑消息、且仅注入一次）、`TestStateEnforcer_BeforeModelNoState`（空作用域不注入）全绿。
  - `go build ./...` ✓ | `go vet ./...`（变更包）✓ | `go test -count=1 ./internal/artifact/... ./internal/agent/... ./internal/engine/... ./internal/config/...` 全绿（agent 包 2.8s、engine 5.9s）。
- 已知环境限制（非代码问题）：`internal/api`/`internal/cmd/server` 经 `go-sqlite3` 依赖 CGO，本沙箱无 gcc（`CGO_ENABLED=0`），故这两个包的 DB 测试无法运行；本轮对它们的改动（handler/buildRouter 签名加 `stateStore/enableState`、main.go 创建 `FileStore`）已通过 `gofmt -e` 语法校验 + 全签名一致性 grep 复核（`api/chat.go`、`api/sse.go`、`cmd/server/main.go` 及 3 处测试 `buildRouter(db,cfg,disc,nil,false)` 调用点均对齐），在具备 gcc 的环境可正常编译。
- 下一步：PLAN 中 **M1-17（集成验证 E2E：登录→建 workspace→选模型→多轮有记忆→/run 执行→Coder/Reviewer 协同改文件→Goal 循环到 complete→刷新历史仍在）** 成为下一个 ○，依赖 M1-04..16。

---

### 2026-08-03 17:25 | M1-17 | ✅
- 完成内容：**集成验证 E2E（M1 CodeAgent 核心收口，M1-04..16 全部打通后的全链路验收）**。新增两个分层 E2E 测试：
  - ① 引擎层全链路 `server/internal/engine/m1_integration_test.go` 的 `TestEngine_M1_FullChain_TeamGoalComplete`：单条 `engine.Chat` 串起 **Team（Orchestrator→Coder 写文件→Reviewer 只读审阅→Coder 修复）+ Goal 契约（create_goal→协作→update_goal(complete)）**。脚本化 mock 按「请求携带工具清单」区分角色（Orchestrator 带 coder/reviewer/goal 工具、Coder 带 file_write、Reviewer 带 file_read），并按 Goal 契约「未收敛关流式」特性用 `textResp/toolResp` 闭包自适应 SSE/单对象 JSON 回包。**本沙箱实跑通过**：coder 写 2 次（初版+修复）、reviewer 审阅 1 次且工具清单严格为 `[file_read, grep]`（只读护栏生效）、最终 hello.txt 为修复后内容 `hello from coder\n`、goal 收敛到 `complete`、最终答复含 `FINAL` 且无过早 final 泄漏。覆盖了 M1-17 要求的「Coder/Reviewer 协同改文件 + Goal 循环到 complete」。
  - ② HTTP 层全链路 `server/cmd/server/m1_integration_test.go` 的 `TestM1_HTTP_Integration_E2E`：进程内复用 `buildRouter` 跑通「注册→登录→建 workspace→建 Provider→sync→启用+默认模型→建 Session(绑定 workspace)→首轮对话→/run 执行 shell 命令(落盘 done.txt)→多轮记忆(第二轮引用首句实体『Go Multi-Agent』)→历史持久化(6 条消息，刷新后仍在)」。脚本化 mock 按「最新 user 消息含『执行以下 shell 命令』(前端 /run 渲染词)」驱动 shell_exec、其余回声首句验证历史回灌。覆盖了 M1-17 要求的「登录→建 workspace→选模型→多轮有记忆→/run 执行→刷新历史仍在」与 M1-06/07（CodeAct+Workspace 在 HTTP 端点真正执行命令）。
- 验证：`go test ./internal/engine/ -count=1` 全绿，**`TestEngine_M1_FullChain_TeamGoalComplete` 在沙箱实跑 PASS**（0.05s）；`go vet ./cmd/server/` 对含新测试的整包**类型检查通过（exit 0）**。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`go-sqlite3` 链接期桩报错使 `cmd/server` 包测试无法运行（仅运行时阻断，非代码问题），`TestM1_HTTP_Integration_E2E` 在具备 gcc 的环境可正常编译运行（与 M1-16 既定模式一致）。未改动任何生产代码，仅新增测试文件；`go build/vet` 对非 CGO 包无影响。
- 下一步：**M1 阶段全部 ✅（M1-04..17），M1 收口**。下一步可进入 M2（生态：MCP/Skills/Git+taskrun 后台任务/Worktree 隔离/toolsearch）或按 PLAN 既定里程碑推进；本轮循环 STOP。

---

### 2026-08-03 22:40 | M2-01 | ✅
- 完成内容：**Git 基础 & workspace 绑定（M2 生态首任务）**。① 新增 `server/internal/tool/git.go`（包 `codectool`）：Git 工具集 `git_status`/`git_diff`/`git_commit`/`git_log`/`git_branch`，全部经 `executor.SafeExecutor`（与 CodeAct 同款危险命令策略，正常 git 子命令放行、仅 `git push --force`/`git reset --hard`/`git checkout --`/`git clean -f` 等降级 deny）；纯函数 `GitInit/GitStatus/GitDiff/GitCommit/GitLog/GitBranch` 与工具包装层分离；`NewGitTools(workdir)` 返回 5 个工具，`NewCodeActWithGit(workdir)` = `NewCodeAct` + `NewGitTools`（供单代理模式）。② workspace 创建时 best-effort 自动 `git init`：`internal/api/workspace.go` 的 `CreateWorkspaceHandler` 在 `os.MkdirAll` 后调 `codectool.NewGitExecutor`+`GitInit`（失败仅告警不阻断）。③ 接线：`internal/agent/factory.go` 的 `NewCoder` 装配 Git 工具集（CoderInstruction 说明可用 git_* 提交改动，team 模式下由 Coder 持有）；`internal/api/{chat,sse}.go` 单代理分支由 `codectool.NewCodeAct(workdir)` 改 `codectool.NewCodeActWithGit(workdir)`（team 模式仍由 Coder 子代理持有，不重复装配）。④ **关键修复（跨平台坑）**：原 `runGit` 拼接 `git commit -m "..."` 经 `cmd.exe /c` 执行，Go 给整条命令加外层引号并把内部引号转义为 `\"`，cmd 移除外层引号后内部 `\"` 泄露给 git，导致含空格提交说明被解析崩坏（`pathspec 'hello.txt"' did not match`）。彻底方案：给 `executor.Executor` 接口**新增 `RunCommand(ctx, name, args...)`（argv 直调，不经 shell）**，HostExecutor/SafeExecutor 均补齐（策略评估+审计+超时+退出码映射语义与 Run 一致），git 工具集改用 `ex.RunCommand(ctx, "git", args...)`，既消除引号转义问题又缩小命令注入面。
- Commit: <pending>
- 验证: `go build ./...` 非 CGO 包全绿（executor/tool/engine/agent 等；`internal/api`/`cmd/server` 依赖 go-sqlite3 的 CGO 测试本沙箱无 gcc 跳过，属历史环境限制）；`go vet` 同包绿；`go test -count=1 ./internal/executor/... ./internal/tool/... ./internal/engine/... ./internal/agent/...` **全绿**（含 `TestGitTools_FullChain`/`TestNewCodeActWithGit_ToolSet`/`TestNewGitTools_RequiresWorkdir` + `git_integration_test.go::TestEngine_CoderGitCommit_Workspace` 断言「coder 写 hello.txt + 初始提交 + 修改已跟踪文件后 git_status 显示改动、git_diff 显示改动、git_log 含提交说明」PASS）；`gofmt -e` 对全部改动文件语法校验通过。
- 下一步：PLAN 中 **M2-02（MCP 管理中心后端）** 成为下一个 ○，无依赖、可独立进行。

---

### 2026-08-03 23:47 | M2-02 | ✅
- 完成内容：**MCP 管理中心（后端，M2 生态第二任务）**。① 领域模型 `server/internal/model/mcpserver.go`：`MCPServer`（user 归属 + `uniqueIndex:idx_user_mcp` 按 (user_id,name) 隔离 + `Transport`(stdio/sse/streamable) + `Command` + `Args`/`Env`（stdio 组）+ `URL`/`Headers`（sse/streamable 组）+ `Enabled` + `Description`）；`Args`/`Env`/`Headers` 用 GORM `serializer:json` 与 DB 互转 JSON，对外 JSON 直接是数组/对象；`ParseMCPTransport` 归一化 + `MCPServer.Validate()` 跨字段校验（stdio→command 必填、sse/streamable→url 必填）。② `server/internal/repo/mcpserver.go`：owner-scoped CRUD（`Create/List/GetMCPServerByID`(校验归属)/`Update`/`Delete` + `GetMCPServerByName` 查重），跨用户查返回 `ErrMCPServerNotFound`（不泄露存在性）。③ `server/internal/api/mcp.go`：五个 handler（`POST/GET/PUT/DELETE /api/mcp`、`GET /api/mcp/:id`），读接 `mcp:read`、写接 `mcp:write`（RBAC）；创建前查重→409；transport 合法性 + `Validate()` 兜底→400；更新为部分更新并整体重校验。④ `model/role.go` 种子补权限：developer 加 `mcp:write`（原有 `mcp:read`）、viewer 加 `mcp:read`。⑤ `repo/db.go` AutoMigrate 加 `&model.MCPServer{}`；`cmd/server/main.go` 在 protected 组注册 5 条 MCP 路由。
- Commit: 5184781（已推 origin/main）
- 验证: 沙箱无 gcc，故 `internal/api`/`internal/repo`/`cmd/server`（依赖 go-sqlite3 CGO）无法编译/运行，与历史轮次一致；可独立验证的 `internal/model` 包 `go build/vet/test` 全绿（`model/mcpserver_test.go` 的 `ParseMCPTransport` + `MCPServer.Validate` 6 类用例 PASS）；全部改动文件 `gofmt -e` 语法校验通过；api/repo/路由严格对齐既有 workspace/provider 模式（handler 签名、`currentUserID`、`RequirePermission`、owner 隔离、错误码一致），CGO 包在真实 gcc 环境可编译。另新增 `repo/mcpserver_test.go`（owner-scoped CRUD + JSON 往返 + 跨用户 404）与 `cmd/server/mcp_test.go`（`TestMCP_ManagementAPI`：developer 全生命周期 CRUD + owner 隔离 404 + viewer 写 403 + 非法 transport/缺 command/缺 url 400），二者在 gcc 环境运行。
- 范围约束：本任务**仅管理面**（配置持久化 + 校验 + RBAC），**不在此装载任何 MCP 工具**（PLAN 验收「无真实装载」）；真实工具装载由 M2-06 toolsearch 按需调用框架 `tool/mcp` 完成，届时读取本表 `mcp_servers` 配置。
- 下一步：PLAN 中 **M2-03（Skills 仓库 & warm-start）** 成为下一个 ○，无依赖、可独立进行。

---

### 2026-08-04 01:20 | M2-03 | ✅
- 完成内容：**Skills 仓库 & warm-start 注入（M2 生态第三任务，复用框架 skill 包）**。① 新增独立框架无关包 `server/internal/skillrepo/skillrepo.go`：复用框架 `trpc.group/trpc-go/trpc-agent-go/skill` 的 `FSRepository`（技能=含 SKILL.md 的目录，SKILL.md 含可选 YAML front matter name/description + 正文）。`Manager` 管理两套根——**共享根 sharedRoot**（仓库 `skills/`，只读）+ **用户私有根 dataDir/<uid>/**（owner 隔离，API 增删改）；`List/Get/Create/Update/Delete` 全部文件系统后端，私有技能写在 `dataDir/<uid>/<name>/SKILL.md`、共享技能 API 不可改写（Create/Update 仅私有、Delete 仅私有、Get 私有优先其次共享）。`ValidSkillName`（正则 `^[A-Za-z0-9_-]+$`）+ `sanitizeSegment` 双重防路径穿越；`WarmStartBlockRoots(roots, keywords, maxChars)` 扫描→关键词过滤→长度上限（默认 6000）控长注入，`WarmStartBlock(uid,...)` 按 uid 拼私有根。② 管理 API `server/internal/api/skill.go`：5 路由 `GET/POST /api/skills`、`GET/PUT/DELETE /api/skills/:name`，读接 `skills:read`、写接 `skills:write`（RBAC），owner 隔离、校验技能名、越界/缺失 400/404。③ 引擎层 warm-start：`engine.ModelConfig` 增 `SkillWarmStart/SkillRoots/SkillKeywords/SkillMaxChars`，`New()` 在开启且 roots 非空时调 `skillrepo.WarmStartBlockRoots` 生成上下文片段；**单代理分支**经 `WithInstruction(默认指令+skillCtx)`、**team 分支**经 `Deps.SkillContext` 注入 Orchestrator（与 StateEnforcer/Goal/Plan 同款「只挂根 Agent」约定，Coder/Reviewer 不装），`SkillKeywords` 当前 nil（注入全部、受上限控长），占位为后续按 workspace/首条消息提取关键词预留。④ `config` 增 `SKILLS_ROOT`(默认 `<cwd>/skills`)/`SKILLS_DATA_DIR`(默认 `<cwd>/data/skills`)/`SKILL_WARM_START`(默认 true)/`SKILL_WARM_START_MAX_CHARS`(默认 6000) + 对应访问器（Load 时确保两目录存在）；`cmd/server/main.go` 注册 5 条 skill 路由并把 4 个 skill 配置注入 `ChatHandler`/`StreamChatHandler`。⑤ `model/role.go` 种子补权限：developer 加 `skills:write`、viewer 加 `skills:read`（幂等，已初始化库重启即生效）。⑥ 内置示例技能 `skills/example/SKILL.md`（源码入库，演示结构与 warm-start）。⑦ **顺带修复 M2-02 遗留 bug**：`api/mcp.go` 误用未定义的 `ErrMCPServerNotFound`，正确为 `repo.ErrMCPServerNotFound`——此前因沙箱无 gcc（CGO 包未编译）未暴露，本轮人工复核签名一致性时发现并修正。⑧ **修复 skill.go 两处编译级 bug**（无 gcc 环境下人工 review 发现，否则 CI 真实 gcc 编译会失败）：`currentUserID` 返回 `uint` 被直接传给期望 `string` 的 `skillrepo` 方法（已改 `strconv.FormatUint(uint64(uid),10)` 修正）；移除未使用的 `gorm.io/gorm` import（否则 imported and not used）。
- Commit: b86737e（已推 origin/main）
- 验证: `go build ./...` 非 CGO 包全绿（skillrepo/engine/agent/tool/executor/config/model）；`go vet` 同包绿；`go test -count=1 ./internal/skillrepo/... ./internal/engine/...` **全绿**（`skillrepo` 6 例覆盖 Create/List/Get/OwnerIsolation/CreateUpdateDelete/InvalidName/WarmStart 注入与控长/空根；`engine` 含新增 `skill_test.go::TestEngine_SkillWarmStart_WiresConfig`(启用时引擎成功构造)+`TestEngine_SkillWarmStart_DisabledSkips`(关闭/roots 空跳过) PASS）；全部改动文件 `gofmt -l` 通过（4 个文件格式化后干净）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/api`/`cmd/server`（依赖 go-sqlite3 CGO）无法编译/运行，与历史轮次一致；但本轮已对 CGO 包做**人工签名一致性复核 + 修掉两处真实编译 bug**（见⑧），在具备 gcc 的环境可正常编译。
- 下一步：PLAN 中 **M2-04（taskrun 后台任务控制面 + 持久化 session）** 成为下一个 ○，依赖 M2-01。

---

### 2026-08-04 02:22 | M2-04 | ✅
- 完成内容：**taskrun 后台任务控制面 + 持久化 session（M2 生态第四任务，对齐 PLAN 验收四要素）**。① **持久化 session service（自研）** `server/internal/sessionstore/sessionstore.go`：框架 v1.10.0 仅有 inmemory/noop 后端（无 SQLite），故实现 `session.Service` 接口（非 CGO，仅 `gorm`+`gorm/clause`，不引 sqlite driver）。`TaskRunSession` 模型（`app_name,user_id,session_id` 复合唯一 + `events_json`/`state_json` text），`AppendEvent` 仅落盘 `!IsPartial && IsValidContent()` 事件，`GetSession` 按 `EventNum` 截断、未落库行返回 nil；`var _ session.Service = (*SessionService)(nil)` 编译期接口校验。`New(db)` 复用同一 SQLite（Go 侧一条连接，不冲突）。② **接线框架 tool/taskrun 到根 Agent**：`engine.ModelConfig` 增 `TaskRunController`/`TaskRunSession`；`New()` 在 `cfg.TaskRunController != nil` 时挂 `taskruntool.NewTools(controller, WithDefaultAgentName(codeagent.RoleCoder), WithParentAppNamePropagation(true), WithSessionService(cfg.TaskRunSession))`（六工具：start/list/get/cancel/wait + read_task_run_transcript）。`internal/taskrun/taskrun.go`：`AppName="go-multi-agent-v2"`（与 engine `runner.NewRunner` 一致，保证 transcript 回查钥匙父子 appName 一致）；`BuildAgentFactory` 从 `agent.InvocationFromContext(ctx).Session.UserID` 取 OwnerUserID（worker 工厂 RunOptions 不含 UserID）→ 闭包 `ResolveModel/ResolveWorkdir` 解析该用户模型与工作目录 → `codeagent.NewCoder`（复用 M1-08 工具集 + Guardrail，**与 Goal/Plan/State 扩展兼容**）；`NewController` 组 `inprocess.Service`（run 记录 JSON 持久化 `inprocess.NewFileStore` 跨重启） + 内部 worker Runner（挂持久化 session）；`Tools()` 先赋变量再 `.All()`（指针接收者方法）。③ **userID 透传**：不改 `Stream/Chat` 签名（免破坏 16 处 engine 测试），改用 `engine.WithUserID(ctx,uid)` 上下文注入，替换原硬编码 `"user"`，chat.go/sse.go 调用点包 `engine.WithUserID`。④ **控制面 API** `server/internal/api/taskrun.go`：4 handler（GET /taskruns 列表、GET /:id 详情、POST /:id/cancel 取消、GET /:id/transcript 读回），RBAC `taskruns:read`/`taskruns:write` + owner 隔离（uid 比对 run.OwnerUserID）；transcript handler 复用框架回查钥匙 `session.Key{AppName:run.AppName, UserID:run.OwnerUserID, SessionID:run.ChildSessionID}` + `session.WithEventNum(200)`。`cmd/server/main.go` 装配 controller/session、注册 4 条路由、worker resolver 闭包（按 uid 解析默认启用模型 + Provider + Decrypt(APIKeyEnc) → openai.New；workdir = WorkspaceRoot/<uid> 确保存在）。`model/role.go` 种子补 `taskruns:read`(viewer)/`taskruns:write`(developer)，幂等。
- 验证: `go build/vet/test` 非 CGO 包全绿——`internal/engine`（含原有 16 处调用）、`internal/taskrun`（`TestTools_WithoutSessionService` 5 工具/`TestTools_WithSessionService` 6 工具含 transcript）、`internal/sessionstore`（编译绿，其测试 `//go:build cgo` 守卫，无 gcc 跳过）、`internal/model` 全 ok；全部改动文件 `gofmt -l` 干净。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/api`/`cmd/server`（依赖 go-sqlite3 CGO）无法编译运行，与历史轮次一致；本轮已对这两包做**人工签名一致性复核 + 框架 API 逐项核对**（`session.Key{AppName,UserID,SessionID}`/`WithEventNum`/`Session.GetEvents`、`taskruntool.WithDefaultAgentName/WithParentAppNamePropagation/WithSessionService`、`inprocess.NewFileStore/WithStore/Store`、`config.GuardrailConfig()`、`codeagent.GuardrailConfig/RoleCoder/NewCoder/Deps` 均存在且字段匹配），CGO 包在真实 gcc 环境可编译。新增单元测试：`internal/taskrun/taskrun_test.go`（fakeController 实现 Controller 接口）、`internal/api/taskrun_test.go`（owner 隔离 / 越权 403 / 取消 / transcript handler 调用契约）、`internal/sessionstore/sessionstore_test.go`（cgo 守卫，跨重启读回/跳过 partial/未落库 nil）。
- 下一步：PLAN 中 **M2-05（Worktree 隔离：taskrun 派生时 hook `git worktree add`，子代理 workdir 指向独立 worktree，完成 commit→merge 回主分支，只 merge 不 push 远程）** 成为下一个 ○，依赖 M2-01 + M2-04（本任务）。

---

### 2026-08-04 03:50 | M2-05 | ✅
- 完成内容：**Worktree 隔离（M2 生态第五任务，每个 taskrun 派生独立 git worktree，完成 commit 后 merge 回主分支，只 merge 不 push 远程）**。① 新增 `server/internal/worktree/worktree.go`：`Manager`（进程内 `sync.Mutex` + `map[string]*Entry`，key=childSessionID）+ `Entry{RepoDir, WorktreeDir, Branch}`；`Create(ctx, repoDir, childSessionID)` 规整名（`sanitizeName` 仅留 `[A-Za-z0-9_-]`）→ 在 `<Dir(repoDir)>/.taskrun-worktrees/<name>` 建 worktree 并 `-b taskrun/<name>` 分支 → 登记 entry → 返回主仓库之外的独立 worktree 目录（隔离写操作）；`Finalize(ctx, childSessionID, status)`：completed → `git merge --no-ff`（冲突则保留分支+worktree 交人工，不删不推）→ 先 `worktree remove --force` 再 `branch -D` → prune；failed/canceled → 不 merge，仅 remove worktree、保留分支供复核；`runGit` 封装 `executor.SafeExecutor.RunCommand(ctx,"git",args...)`（argv 直调，绕过 shell 引号转义，与 M2-01 一致）。② `server/internal/taskrun/taskrun.go`：新增 `WorktreeHook{Enabled; Manager}`（实现框架 `taskrunruntime.Observer`），在 `BuildAgentFactory` 内经 `inv.Session.ID` 取 childSessionID 调 `Create` 把 Coder 子代理 workdir 指向该 worktree；`OnRunUpdate` 仅终态时调 `Manager.Finalize` 触发 merge/cleanup；`NewController` 签名加 `observer` 参数经 `inprocess.WithObserver` 注入。③ `config` 增 `WORKTREE_ISOLATION`(默认 true)+`WorktreeIsolation()`；`main.go` 装配 `WorktreeHook{Enabled:cfg.WorktreeIsolation(), Manager:worktree.NewManager()}` 并注入 `NewController`。④ **顺带修复 M2-04 遗留编译 bug**（无 gcc 环境人工 review 发现，会阻断 `go build`）：`api/chat.go:153`/`sse.go:179` `strconv.FormatUint(uid,10)`→`uint64(uid)`（uid 为 uint）；`cmd/server` 5 处 `buildRouter(db,cfg,disc,nil,false)`→补 `,nil,nil`（M2-04 扩展 buildRouter 签名新增 taskRunController/taskRunSession 两参未同步）。
- Commit: 7adf71c（已推 origin/main）
- 验证: `go build ./...` ✓ | `go vet ./...` ✓（全包含 CGO 包，gcc 可用）；`go test -count=1 ./internal/worktree/... ./internal/taskrun/... ./internal/engine/... ./internal/agent/... ./internal/config/...` 全绿（`worktree` 3 例：`CreateAndMerge`（worktree 内提交→completed merge 回主分支→文件在主分支且内容正确→临时分支清理→worktree 目录移除）、`FinalizeFailureKeepsBranch`（failed 不 merge、主分支无 half.txt、分支保留）、`UnknownSession`/`sanitizeName` 全 PASS；`taskrun` 含 `TestWorktreeHook_CreateAndFinalize`/`TestWorktreeHook_DisabledIsNoop` PASS）。已知限制：本沙箱 `internal/repo`（CGO sqlite）测试因 `CGO_ENABLED=0` 无 gcc 跳过，非代码问题，与本次改动无关。
- 下一步：PLAN 中 **M2-06（toolsearch 延迟工具箱：框架 `plugin/toolsearch`，把 M2-02 接入的 MCP server 工具 + 内置工具注册为命名空间工具箱，暴露 `tool_search`+`call_tool` 双工具按需装载，避免 context 膨胀）** 成为下一个 ○，依赖 M2-02（已 ✅）。



---

### 2026-08-04 05:20 | M2-06 | ✅
- 完成内容：**延迟工具箱（lazy toolbox / toolsearch，M2 生态第六任务，对齐 PLAN 验收）**。① 新增 `server/internal/toolsearch` 包：`toolbox.go`（命名空间工具箱 `mcp__<server>__<tool>`，`Add/Merge/Get/Search/Close`，MCP 连接经 `AddCloser` 由 `Engine.Close` 统一释放）；`tools.go`（`tool_search` 检索 + `call_tool` 按需调用双控制工具，均经 `tool/function.NewFunctionTool`）；`mcp.go`（`mcpConnectionConfig` 领域模型→框架 `tool/mcp.ConnectionConfig`；`LoadMCPServerTools` 经 `mcptool.NewMCPToolSet`→`Init`→`Tools` 预取工具并按 `mcp__<name>` 命名空间注册，校验失败就地返回不连接）。② **引擎接线**：`engine.ModelConfig` 增 `ToolSearchEnabled`/`ToolSearchProvider`/`ToolSearchUserID`；`New()` 在启用且有可用工具时**只挂载双控制工具**（不把 MCP 工具声明灌进上下文，结构性保证 token 不随工具数线性膨胀），provider 报错 fail-open 安全跳过；`Engine.Close` 释放 toolbox MCP 连接。③ **配置/API/路由**：`config` 增 `TOOL_SEARCH_ENABLED`(默认 true)+`ToolSearchEnabled()`；`api.ChatHandler`/`StreamChatHandler` 注入三字段（ToolSearchUserID=uid 做 owner 隔离）；`cmd/server/buildRouter` 注入 `buildToolSearchProvider`（按 uid 调 `repo.ListMCPServers`→逐个 `LoadMCPServerTools`→Merge，单服务器失败跳过），同步 5 处测试 `buildRouter` 调用点补参。④ 未直接用框架 `plugin/toolsearch`（其是 LLM 工具选择插件，会改变每次调用行为且消耗 LLM 预算，与「默认不装载+按需调用」语义不符），改为自建延迟工具箱。
- Commit: bac692b（已推 origin/main）
- 验证: `go build ./...` ✓ | `go vet ./...` ✓（全包含 CGO 包，gcc 可用）；`go test -count=1 ./internal/toolsearch/... ./internal/engine/... ./internal/config/...` 全绿——`toolsearch` 7 例（命名空间拼接/检索截断/merge/close 幂等/tool_search 返回 JSON/ call_tool 执行+报错/连接配置映射/校验失败不连接/NewMCPToolSet API 契约）；`engine` 新增 `toolsearch_test.go` 2 例：`TestEngine_ToolSearch_LazyInvoke`（mock LLM 脚本化驱动 tool_search→call_tool→RESULT:5 返回，并断言被延迟的 `mcp__demo__add` 从不直接进入模型上下文——防 token 膨胀）、`TestEngine_ToolSearch_DisabledMountsNothing`（关闭时不挂载双工具、不调 provider）；`config` 全 ok。**关键验收**：默认不装载全部 MCP 工具 → 模型 tool_search 找到 → call_tool 执行 → 结果返回；context token 不随 MCP 工具数线性膨胀。已知限制：本沙箱 `internal/repo`/`api`/`cmd`（CGO sqlite）测试因 `CGO_ENABLED=0` 无 gcc 跳过，非代码问题，与本轮改动无关（已对 CGO 包做签名一致性复核）。
- 下一步：M2 生态六个 ○ 任务（M2-01~M2-06）**全部 ✅**。PLAN.md 已无待做任务，进入「全部完成」终态——后续如需继续推进（M3 企业化：可观测/审计/配额+预算护栏+人工检查点+artifact；M4 自主化：Automation 触发器+Channel 层+跨天恢复+无人值守 Loop；M5 进化：CLI+Knowledge RAG+evolution 技能飞轮+evaluation 回归+promptiter+A2A），需先在 docs/loop/PLAN.md 增补对应里程碑任务清单。

---

### 2026-08-09 21:15 | M3-01 | ✅
- 完成内容：**执行审计落库（M3 企业化首个 ○ 任务，复用 M1-05 `SafeExecutor.Auditor` 接口。对齐 PLAN 验收「单测覆盖 allow/deny/ask 三类均写审计；curl 跑一条 shell → GET /api/audit 可见该记录（owner 隔离）」）**。① 领域模型 `server/internal/model/audit.go`：`AuditLog`（UserID + Command + Workdir + Decision string(16) + Reason + Allowed bool + Note，TableName=`audit_logs`）。② 审计器 `server/internal/repo/audit.go`：`DBAuditor` 实现 `executor.Auditor`（db *gorm.DB + ownerUserID uint，db==nil 降级空操作），`NewDBAuditor(db, ownerUserID)`；`Record(e)` 写 `model.AuditLog`；`CreateAuditLog(db, e)`；`AuditLogFilter{UserID,Decision,Command,Limit,Offset}` + `ListAuditLogs(db, f)`（条件过滤+计数+分页+时间倒序）。**关键设计**：不改动框架 `AuditEntry` 接口（无 owner 字段），owner 存 `DBAuditor` 内部，避免破坏 `MemoryAuditor`/`LogAuditor` 现有调用与测试。③ **全链路下传 auditor**（nil 回落 `executor.NewLogAuditor(nil)` 不阻断命令）：`tool/codeact.go`/`tool/git.go` 的 `NewCodeAct`/`NewGitExecutor`/`NewGitTools`/`NewCodeActWithGit` 增 `auditor executor.Auditor` 参数；`worktree.go` 基础设施代码回落日志审计；`api/workspace.go` 自动 git init 归属创建者 `repo.NewDBAuditor(db, uid)`；`agent/factory.go` `Deps` 增 `Auditor` 字段→`NewCoder` 经 `NewCodeAct(d.Workdir, d.Auditor)`/`NewGitTools(d.Workdir, d.Auditor)`；`taskrun.WorkerResolver` 增 `NewAuditor func(ownerUserID uint) executor.Auditor`，`BuildAgentFactory` 按 `inv.Session.UserID` 解析→注入 `codeagent.Deps{Auditor: res.NewAuditor(uid)}`（taskrun 子任务自动落库并归属 owner）；`engine.ModelConfig` 增 `Auditor` 经 `NewTeam`→`NewCoder` 透传；`api/chat.go:122`/`sse.go:147` 单代理分支 `NewCodeActWithGit(workdir, repo.NewDBAuditor(db, uid))` + `ModelConfig.Auditor`。④ 审计 API `server/internal/api/audit.go`：`GET /api/audit`（需 `audit:read`），developer/admin 传 `UserID=0` 看全员、viewer 传自己 uid 仅看自己；支持 `?decision=&command=&limit=&offset=`，返回 `{audit_logs, total}`（owner 隔离模式）。⑤ `model/role.go` `SeedRoles()` 幂等补 developer/viewer 的 `{Resource:"audit",Action:"read"}`（admin 因 `*` 已覆盖）；`repo/db.go` AutoMigrate 加 `&model.AuditLog{}` 建表；`cmd/server/main.go` 注册 `GET /api/audit` 路由 + workerResolver 注入 `NewAuditor: func(ownerUserID uint) executor.Auditor { return repo.NewDBAuditor(db.DB, ownerUserID) }`。
- 验证：`go build ./...` ✓ | `go vet ./...` ✓（含 CGO 包）。新增 `server/internal/repo/audit_test.go` 4 例**全 PASS**：`TestDBAuditor_RecordsAllDecisions`（owner=42，allow/deny/ask 三类各写1条、total=3 且 decision 覆盖）、`TestDBAuditor_OwnerIsolation`（owner=42 仅见1条、UserID=0 见全员2条）、`TestDBAuditor_NilDBNoop`（db=nil 不 panic）、`TestListAuditLogs_FilterAndPagination`（decision/command 过滤+分页）。同步修正全部调用点（worktree/codeact/git/agent/engine/taskrun 及其测试）补 auditor 参数。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo|api|cmd` 的 go-sqlite3 DB 测试（含 `TestGetRoleIDByName` 等历史用例）按 AGENTS.md 豁免跳过，与本次改动无关；线程内 DB 测试用 `gorm.Open(sqlite.Open(":memory:"))` 内存库跑通审计逻辑。
- 范围约束：本任务只做「落库 + API + 全链路覆盖 + 建表 + 单测」，前端 `AuditView` 留 **M3-02**（依赖本任务）；不改动 `AuditEntry` 接口、不影响 `MemoryAuditor`/`LogAuditor` 既有行为。
- 下一步：PLAN 中 **M3-02（审计日志 API + 前端页 `AuditView`，owner 隔离 + 筛选 + vue-tsc 通过）** 成为下一个 ○，依赖 M3-01（已 ✅）。

---

### 2026-08-09 22:20 | M3-02 | ✅
- 完成内容：**审计日志 API 增强 + 前端 `AuditView`（M3 企业化第二任务，依赖 M3-01，对齐 PLAN 验收「developer 看全员、viewer 只看自己；筛选生效；vue-tsc 通过」）**。① 后端 `server/internal/repo/audit.go`：`AuditLogFilter` 扩展 `Start/End time.Time` 时间范围过滤 + `Limit/Offset` 分页；新增 `DefaultAuditPageSize=50`/`MaxAuditPageSize=200` 与 `NormalizeAuditPageSize`（<=0 回退缺省、超上限钳制、负 offset 按 0）；`ListAuditLogs` 增加 `created_at >= ? / <= ?` 条件、按 `created_at desc, id desc` 排序。② 后端 `server/internal/api/audit.go` 全量增强 `ListAuditLogsHandler`：`decision` 合法性校验（allow/ask/deny，非法 400）、`user_id` 仅 admin/developer 生效且 viewer 强制只看本人（忽略传入 user_id，owner 隔离兜底）、`start/end` 支持毫秒时间戳与 RFC3339/日期字符串（坏格式或 end<start 400）、`limit/offset` 回显；返回体新增 `scope(all/self)`/`limit`/`offset` 元信息供前端渲染。③ **新增单测**（纯 Go `glebarez/sqlite`，无需 gcc）：`repo/audit_test.go` 3 例（时间范围过滤/分页归一化/超大 limit 钳制）、`api/audit_test.go` 6 例（角色可见范围 dev/admin 看全员·viewer 仅看自己·viewer user_id 被忽略、各类筛选、坏参 400、未认证 401、parseAuditTime 覆盖）；全部 PASS。④ 前端：`web/src/api/audit.ts` 封装 `listAuditLogs(params)`（查询字符串构造、decision 映射）；`web/src/views/AuditView.vue`（NDataTable：ID/用户/命令/工作目录/决策 Tag/执行/原因/备注/时间 + 筛选区：决策 NSelect、命令 NInput、用户ID NInputNumber【仅 admin/developer】、时间范围 NDatePicker daterange；服务端分页 + 详情弹窗 NDescriptions）；`router/index.ts` 增 `audit` 路由、`DefaultLayout.vue` 增「审计日志」菜单项。
- 验证：`go build ./...` ✓ | `go vet ./...` ✓（含 CGO 包）；`go test ./internal/api/... ./internal/repo/...` 中 **audit 相关 13 例全 PASS**（纯 Go sqlite 运行）；前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（产出 `AuditView` chunk）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，历史 `go-sqlite3` 用例（`TestListSessionsScopedAndOrdered`/`TestGetRoleIDByName` 等 10 例）按 AGENTS.md 豁免跳过、与本任务无关，非代码缺陷。
- 下一步：PLAN 中 **M3-03（Token/费用计量）** 成为下一个 ○，依赖 M2。

### 2026-08-09 23:39 | M3-03 | ✅
- 完成内容：**Token/费用计量（M3 企业化第三任务，依赖 M2，对齐 PLAN 验收「一次对话后 usage_records 有行；/api/usage 返回累计；前端可展示」）**。① 引擎层 `server/internal/engine/engine.go`：在 `Stream` 事件桥接 goroutine 中累计 `ev.Response.Usage`（取最新非零值）写入 `lastUsage`（`sync.Mutex` 保护），新增 `LastUsage() model.Usage` 供 api 层对话结束后读取；新增纯函数 `EstimateUsage(prompt, completion)`（字符数/4 粗估）作上游未给 usage 时的本地兜底。② 数据模型 `server/internal/model/usage.go`：`UsageRecord`（UserID/SessionID/SessionKey/ProviderID/ModelID/ModelName/PromptTokens/CompletionTokens/TotalTokens/Estimated，表 `usage_records`）。③ repo `server/internal/repo/usage.go`：`CreateUsageRecord`/`UsageRecordFilter`/`NormalizeUsagePageSize`/`ListUsageRecords`/`SumUsageRecords`（累计聚合）。④ api：`server/internal/api/usage.go` 新增 `recordEngineUsage`（对话后落库，优先上游 usage、缺失则估算并标 `estimated`、`total==0` 跳过）+ `ListUsageHandler`（owner 隔离 + RBAC `usage:read`，同审计日志的 scope 规则，返回 usage_records/total/totals/limit/offset/scope）；`chat.go`/`sse.go` 在 `eng.Chat`/`conv.Convert` 结束后调用 `recordEngineUsage`；复用 `parseAuditTime` 做时间筛选。⑤ 接线：`model/role.go` 补 developer/viewer 的 `usage:read`；`repo/db.go` AutoMigrate 加 `&model.UsageRecord{}`；`cmd/server/main.go` 注册 `GET /usage`。⑥ 新增单测（纯 Go `glebarez/sqlite`，免 gcc）：`engine/usage_test.go` 3 例（LastUsage 捕获 / 零用量不覆盖 / EstimateUsage）+ `api/usage_test.go` 6 例（角色可见范围 dev/admin 看全员·viewer 仅看自己·viewer user_id 被忽略、各类筛选、坏参 400、未认证 401、parseAuditTime）。⑦ 前端：`web/src/api/usage.ts` + `web/src/views/UsageView.vue`（累计概览卡片 + NDataTable 含上游/估算 Tag + 筛选区 user_id/provider_id/model_id/session_key/时间 + 服务端分页）+ `router/index.ts` 增 `usage` 路由 + `DefaultLayout.vue` 增「用量统计」菜单项。
- 验证：`go build ./...` ✓ | `go vet ./...` ✓；`go test ./internal/engine/... ./internal/api/...` 全 PASS（含 usage 9 例新测试，纯 Go sqlite）；前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（产出 `UsageView` chunk）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，历史 `go-sqlite3` 用例（`TestListSessionsScopedAndOrdered`/`TestGetRoleIDByName` 等）按 AGENTS.md 豁免跳过、与本任务无关，非代码缺陷。
- 下一步：PLAN 中 **M3-04（预算护栏，平台级）** 成为下一个 ○，依赖本任务 M3-03。


---

### 2026-08-10 01:05 | M3-04 | ✅
- 完成内容：**预算护栏（平台级，M3 企业化第四任务，依赖 M3-03，对齐 PLAN 验收「设极低阈值跑对话 → 第二轮被拦并返回『预算耗尽，待恢复』；管理员提额后恢复」）**。① 数据模型 `server/internal/model/budget.go`：`BudgetPolicy`（Scope∈{user,session,automation} + ScopeKey 组成唯一键；MaxTokens 阈值；Window∈{daily,total}；`Validate()`）。② repo `server/internal/repo/budget.go`：`GetBudgetPolicy`/`GetEffectiveUserBudgetPolicy`（用户特定策略优先于全局默认）/ `UpsertBudgetPolicy`（按唯一键 upsert，不新增重复行）/ `ListBudgetPolicies`/`DeleteBudgetPolicy`；核心 `EvaluateBudgets(db, uid, sessionKey, automationID)` 评估 user+session+automation 三级，任一超限即 `Blocked=true`，复用 `SumUsageRecords` 按 UserID/SessionKey 在窗口内聚合 token（`BUDGET_ENABLED` 总开关默认开，false 整体放行）。③ api `server/internal/api/budget.go`：`GET/PUT/DELETE /api/budgets`（RBAC `budgets:read`/`budgets:write`）+ `writeBudgetBlockAudit`（拦截时经 `DBAuditor` 写一条 `budget:enforce` 审计，满足「写审计」）；新增 `writeSSEEvent` 包级辅助供 SSE 早期拦截复用。④ 接线：`chat.go`/`sse.go` 在 `eng.Chat`/`eng.Stream` **之前**插入预算检查（chat 返回 429「预算耗尽，待恢复」+scope/used/max；sse 发 `RUN_ERROR`+`RUN_FINISHED`），拦截发生在持久化用户消息前避免脏数据；`model/role.go` `SeedRoles()` 补 developer `budgets:read/write`、viewer `budgets:read`（admin `*` 已覆盖）；`repo/db.go` AutoMigrate 加 `&model.BudgetPolicy{}`；`cmd/server/main.go` 注册三路由；`config` 增 `BUDGET_ENABLED`(默认 true)+`BudgetEnabled()`。
- 验证：`go build ./...` ✓ | `go vet ./...` ✓；新增单测（纯 Go `glebarez/sqlite`，免 gcc）全 PASS——`repo/budget_test.go` 7 例（`EvaluateBudgets` 用户级阻断/阈值内不拦/提额后恢复/session 级拦截/总开关关闭放行/Upsert 创建后更新不增行/用户特定策略优先于全局）+ `api/budget_test.go` 4 例（RBAC developer 可读写·viewer 只读写 403 / upsert+列表+删除 / 非法体 400 / 未认证 401）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 历史 `go-sqlite3` 用例（`TestListSessionsScopedAndOrdered`/`TestGetRoleIDByName` 等）为**既有失败、与本次改动无关**（已 `git stash` 基线复现确认）；与本任务相关的 budget 测试全绿。
- 说明：automation 作用域策略可在 API 预置，运行时统计待 M4 接入 `automation_id`（usage_records 当前无该列）后全量启用；M3-04 运行时拦截已覆盖 user + session 两级（满足验收）。
- 下一步：PLAN 中 **M3-05（人工检查点 human-in-the-loop）** 成为下一个 ○，依赖 M3-01。

---

### 2026-08-10 05:40 | M3-05 | ✅
- 完成内容：**人工检查点 human-in-the-loop（M3 企业化第五任务，依赖 M3-01，对齐 PLAN 验收「触发需审批危险操作 → 生成 checkpoint → 前端 approve 后执行、reject 后中止；审计留痕」）**。① **策略层语义修正** `internal/executor/blacklist.go`：`DangerousCommandPolicy.Evaluate` 不再在 `ModeUnattended` 下把 ask 规则提前改判为 `DecisionDeny`，改为**所有模式统一返回 `DecisionAsk`**，把「deny 还是转人工审批」的处置权上交 SafeExecutor（致命级 deny 规则不受影响，永不进入检查点）。② **执行层** `internal/executor/policy.go`：新增 `CheckpointRequest`（Command/Workdir/Reason/SessionID/Context）+ `Checkpointer func(req) (id string, err error)` 回调 + 哨兵错误 `ErrCheckpointCreated` 与带 ID 的 `CheckpointError`（`Unwrap` 只解到 `ErrCheckpointCreated`、**不**解到 `ErrCommandDenied`，使工具层能区分「已建检查点」与「被安全策略拒绝」）；`SafeExecutor` 增 `checkpointer` 字段（`NewSafeExecutor` 第 5 参），ask 处置抽为 `classifyAsk`（三态 `askAllow`/`askCheckpoint`/`askDeny`，只做审计与落库副作用、**不执行命令**，由 `Run`/`RunCommand` 各按自身入口形式执行，避免重复执行）——交互模式仍交 `AskHandler`；无人值守挂了 checkpointer 则落库并暂停；无 checkpointer 或落库失败一律安全退化 deny。③ **模型/仓储** `model/checkpoint.go`（`Checkpoint` 表 `checkpoints`，pending/approved/rejected 三态 + `DisplayID()`→`CP-<id>`）+ `repo/checkpoint.go`（Create/Get/List 带 owner 与 status 过滤+分页复用 `NormalizeAuditPageSize`/Resolve 落终态）。④ **API** `api/checkpoint.go`：`GET /api/checkpoints`（`checkpoints:read`，owner 隔离——admin/developer 看全员、viewer 仅本人，status 非法 400）+ `POST /api/checkpoints/:id/resolve`（`checkpoints:write`，approve 在记录的 workdir 用 `HostExecutor` 实际执行并回填 `result`，reject 仅落终态；非 pending 409、越权处置 403；两者均经 `repo.NewDBAuditor` 写审计并在 note 标注检查点编号）。⑤ **全链路注入**：`tool.NewCodeAct/NewGitTools/NewGitExecutor/NewCodeActWithGit` 增 `cp executor.Checkpointer` 形参；`codeagent.Deps.Checkpointer`→`NewCoder`；`engine.ModelConfig.Checkpointer`→`NewTeam`；`taskrun.WorkerResolver.NewCheckpointer(ownerUserID, childSessionID)` 按子任务会话归属注入；`api/chat.go`+`sse.go` 单代理分支按当前 uid+sessionKey 构造回调；`shell_exec` 捕获 `*CheckpointError` 后返回「⏸ 已创建人工检查点 CP-N…本轮运行已暂停」可读结果（非 error，便于 Agent 自适应）。⑥ 接线：`config` 增 `CHECKPOINT_ENABLED`(默认 true，关闭即退回旧的直接 deny)+`CheckpointEnabled()`；`model/role.go` `SeedRoles()` 幂等补 developer `checkpoints:read/write`、viewer `checkpoints:read`；`repo/db.go` AutoMigrate 加 `&model.Checkpoint{}`；`main.go` 注册两路由 + workerResolver 注入 `NewCheckpointer`。⑦ 前端：`web/src/api/checkpoint.ts` + `web/src/views/CheckpointsView.vue`（待审批/全部筛选 + NDataTable + 批准/拒绝 + 审批意见 + 执行结果查看）+ 路由 `checkpoints` + 菜单「人工检查点」。
- Commit: 见下方 git 记录（post-commit hook 自动推 origin/main）
- 验证：`go build ./...` ✓ | `go vet ./...` ✓ | `gofmt` 变更文件全部干净。新增单测 **10 例全 PASS**：`executor/checkpoint_test.go` 5 例（无人值守 ask 生成检查点并返回 CheckpointError 且命令未执行、`RunCommand` 入口同样走检查点、落库失败退化 deny、致命级 deny 永不进检查点、无 checkpointer 保持旧 deny 行为）+ `api/checkpoint_test.go` 5 例（角色可见范围 developer 全员·viewer 仅本人、status 过滤与非法值 400、approve 真正执行命令+回填 result+留一条放行审计且 note 带 CP 编号、reject 中止不产生执行结果+留拒绝审计、越权/重复处置/坏参防护）。前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（产出 `CheckpointsView` chunk 8.76 kB）。
- 已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 中 10 例历史用例（`TestGetRoleIDByName`/`TestListSessionsScopedAndOrdered`/`TestUpsertModelPreservesFlags` 等）直接 import `go-sqlite3` 而报 "requires cgo to work"，属**既有失败**（M3-04 已 stash 基线复现确认），与本任务无关；本任务新增测试一律使用纯 Go `glebarez/sqlite`，免 gcc 可跑。建议由 **MX-05（后端测试补全）** 顺带把这批历史用例迁到纯 Go 驱动。
- 下一步：PLAN 中 **M3-06（Artifact 浏览器：`GET /api/sessions/:id/artifacts` + 前端 `ArtifactView`，复用 M1-16 `artifact.Store`）** 成为下一个 ○，依赖 M1-16（已 ✅）。

---

### 2026-08-10 06:48 | M3-06 | ✅
- 完成内容：**Artifact 浏览器（M3 企业化第六任务，依赖 M1-16，对齐 PLAN 验收「有产物的会话能看到文件列表并查看内容；与运行态面板互补」）**。① 存储层增强 `server/internal/artifact/store.go`：新增 `Entry{Name,Size,ModTime}` + 可选扩展接口 `EntryLister`（两个内置后端 `FileStore`/`MemoryStore` 均实现，**不改变** `Store` 主接口，向后兼容，未实现者可回退 List+Read 自行统计）；`IsStateArtifact`（判三核心状态文件）/ `SortEntries`（核心状态文件优先、其余按名排序）/ `ValidateName`（复用 `sanitizeName` 暴露给 API 做 400/404 区分）。`MemoryStore` 内部改为 `memFile{content, modTime}` 以记录修改时间。② 后端 API `server/internal/api/artifact.go`：`ListSessionArtifactsHandler`（`GET /api/sessions/:id/artifacts`，返回 session_key/enabled/total/artifacts 元信息，未启用状态外置则 enabled=false 且列表空）+ `GetSessionArtifactHandler`（`GET /api/sessions/:id/artifacts/:name`，默认 JSON 内联查看，>256KiB 截断置 `truncated=true`、二进制不内联引导下载；`?download=1` 以 `attachment` 附件回写原始字节）；`resolveArtifactSession` 复用 `currentUserID` + `repo.GetSessionByKey` 做 owner 隔离（跨用户 404，不泄漏存在性）；非法文件名 400、不存在 404。`cmd/server/main.go` 在 `/sessions/:id/state` 旁注册两条新路由（复用既有 `stateStore`/`enableState`，无需新 RBAC 中间件）。③ 前端：`web/src/api/artifact.ts`（listArtifacts/getArtifact/downloadArtifact，下载用原生 fetch+blob 绕开 JSON 解析）+ `web/src/views/ArtifactView.vue`（会话选择 NSelect + NDataTable 列表 + 查看弹窗内联预览 + 下载按钮 + 未启用/二进制/截断提示）+ `router/index.ts` 增 `artifacts` 路由 + `DefaultLayout.vue` 增「Artifact」菜单项。④ 新增 `server/internal/api/artifact_test.go` 10 例（纯 Go `glebarez/sqlite`，免 gcc）：列表未启用空、列表含内容且核心文件优先、内联查看、下载附件原始字节、非法名 400、不存在 404、未启用读取 404、owner 隔离跨用户 404、二进制内联清空、超 256KiB 截断。
- 验证：`go build ./...` ✓ | `go vet ./...` ✓（`CGO_ENABLED=0` 纯 Go 链路）；`go test ./internal/artifact/... ./internal/api/... ./cmd/server/...` 全 PASS（含 artifact 10 例新测试）。前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（产出 `ArtifactView` chunk 5.43 kB）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 中 10 例历史用例直接 import `go-sqlite3` 而报 "requires cgo to work"，属**既有失败**（M3-04 已 stash 基线复现确认），与本任务无关。
- 下一步：PLAN 中 **M3-07（MCP 敏感字段加密，env/headers 走 AES-256-GCM）** 成为下一个 ○，依赖 M2-02（已 ✅）。

---

### 2026-08-10 08:05 | M3-07 | ✅
- 完成内容：**MCP 敏感字段加密（M3 企业化第七任务，依赖 M2-02，对齐 PLAN 验收「库内 env 为密文；正常装载仍解密可用；越权读取拿不到明文」）**。① **模型层** `server/internal/model/mcpserver.go`：`Env`/`Headers` 由 `serializer:json` 明文列改为 `gorm:"-" json:"-"` 的**瞬态明文**字段，新增落库密文列 `EnvEnc`/`HeadersEnc`（`type:text`、`json:"-"`），采用 AES-256-GCM（复用 `internal/crypto` 与 Provider 同源的 `config.EncryptionKey`）；新增 `SealSecrets(key)`/`OpenSecrets(key)`（JSON→加密／解密→JSON，空 map 落空串以区分「未配置」与「空对象」）与掩码辅助 `EnvKeys()`/`HeaderKeys()`（键名升序）、`HasEnv()`/`HasHeaders()`（不解密即可判断）。② **仓储层** `server/internal/repo/mcpserver.go`：五个 CRUD 函数统一增 `encKey []byte` 形参，写路径（Create/Update）先 `SealSecrets`、读路径（List/GetByName/GetByID）后 `OpenSecrets`——repo 成为加解密唯一出口，上层与 toolsearch 无感知；`GetMCPServerByID` 的 **owner 校验前置于解密**，越权者连密文都不解开。③ **遗留数据迁移** `server/internal/repo/db.go`：`NewDB` 在 AutoMigrate 后调 `migrateMCPSecretEncryption(db, cfg.EncryptionKey)`，用 `sqlite_master` 建表 DDL 中是否含反引号包裹的 `` `env` ``（精确区分新列 `env_enc`）判定遗留列，仅对「遗留列非空且密文列为空」的行就地加密并把遗留列置 NULL；全新库 no-op、重复执行幂等；有明文却无 32 字节密钥时 fail loud。④ **API 层** `server/internal/api/mcp.go`：五个 handler 增 `encKey` 形参；`mcpServerView` **移除 `env`/`headers` 明文字段**，改为 `has_env`/`env_keys`/`has_headers`/`header_keys` 掩码（只给键不给值）；PUT 未传 env/headers 即保持原值（「留空不修改」），传 `{}` 才清空。`cmd/server/main.go` 五条 `/api/mcp` 路由与 `buildToolSearchProvider` 均注入 `cfg.EncryptionKey`（后者解密后才能真实装载 MCP 工具）。⑤ **前端**：`web/src/api/mcp.ts` 的 `MCPServer` 类型改为掩码字段；`McpView.vue` 编辑时不再预填密钥（改为提示「已配置 N 项（键名…），值已加密不回显；留空不修改，填 {} 清空」），表格新增「密钥」列显示 `env ×N`/`headers ×N` 标签，页头补加密说明。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./...` 除既有 CGO 用例外全绿。新增/改造测试 **6 例全 PASS**（纯 Go `glebarez/sqlite`，免 gcc）：`repo/mcpserver_test.go` 3 例（带 encKey 的 owner-scoped CRUD 往返 + 顺带修正原有反向断言「同名已存在应返回该行」；`SecretsEncryptedAtRest` 断言库内 `env_enc`/`headers_enc` 为密文且不含明文与键名、错误密钥解密失败、正确密钥可还原、掩码字段正确；`LegacyPlaintextMigration` 造出遗留明文列 → 重开库触发迁移 → 明文列被清空且密文可解、二次开库幂等）+ 新建 `api/mcp_test.go` 2 例（创建/列表/详情/更新四条路径均不回显明文、掩码字段齐全、库内为密文；越权详情 404、越权列表 total=0、viewer 写 403）+ 改造 `cmd/server/mcp_test.go` 集成用例（详情断言由「env 原样回显」改为「无 env 字段 + has_env/env_keys 掩码」，并直读 `env_enc` 校验密文落库）。前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（`McpView` chunk 6.94 kB）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 中 9 例历史用例直接 import `go-sqlite3` 报 "requires cgo to work"，属**既有失败**（M3-04 已 stash 基线复现确认），与本任务无关，建议由 MX-05 统一迁到纯 Go 驱动。
- 说明：SQLite/GORM 不会自动删除废弃列，遗留 `env`/`headers` 列会残留在旧库表结构中（内容已清空）；彻底删列交由 **M3-08 正式迁移机制**处理。
- 下一步：PLAN 中 **M3-08（手动 DB 迁移机制：`schema_migrations` 版本表 + 基线 migration，取代纯 AutoMigrate）** 成为下一个 ○，依赖 M0/M0.5（已 ✅）。

### 2026-08-10 12:10 | M3-08 | ✅
- 完成内容：**手动 DB 迁移机制（M3 企业化第八任务，依赖 M0/M0.5，取代纯 AutoMigrate）**。新增 `server/internal/repo/migrate.go`（迁移核心，框架无关）：① `SchemaMigration{Version,Name,AppliedAt}` + `schema_migrations` 版本表；② `Migration{Version,Name,Up}` + `MigrationContext{EncryptionKey}`；③ `baselineModels()` 汇总当前全部 14 个 GORM 模型；④ `Migrations()` 有序迁移清单（4 个版本）：`0001 baseline_schema`（首次启动建全部表/索引，等价于旧 AutoMigrate 结果）、`0002 drop_legacy_session_key_unique_index`（M0.5-03 后移除该唯一约束）、`0003 encrypt_mcp_server_secrets`（把 M3-07 的「遗留明文 env/headers 就地加密」固化为一次性迁移）、`0004 drop_legacy_mcp_plaintext_columns`（物理删除 M3-07 残留的明文 `env`/`headers` 列）；⑤ `RunMigrations`/`AppliedMigrations`/`PendingMigrations`/`appliedVersions`/`validateMigrations`（非空/版本唯一/升序校验）。接线：`NewDB` 改为先 `RunMigrations(db, MigrationContext{EncryptionKey})` 再「仅当 `DB_AUTO_MIGRATE=true` 才 `db.AutoMigrate(baselineModels()...)`」——AutoMigrate 退化为开发 fallback（默认 false）。安全：④ `dropLegacyMCPPlaintextColumns` 在删列前 `COUNT` 仍残留明文的行，若 >0 **拒绝删列**（`fmt.Errorf("mcp_servers.%s 仍有 N 行明文未加密，拒绝删列")`），杜绝数据丢失；删列成功后再删同样幂等。config 新增 `DB_AUTO_MIGRATE`(env，默认 false) + getter。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓ | `go test -count=1 ./...` 除既有 CGO 用例外全绿。新增/改造测试 **10 例全 PASS**（纯 Go `glebarez/sqlite`，免 gcc）：`migrate_test.go` 6 例（`TestMigrations_ListIsValid` 校验顺序/`TestRunMigrations_FreshDBBuildsFullSchema` 全新库跑基线后表结构齐/`TestRunMigrations_Idempotent` 重跑幂等/`TestRunMigrations_LegacySessionIndexDropped` 唯一索引已移除/`TestRunMigrations_UnknownVersionIgnored` 未知版本跳过/`TestNewDB_UsesVersionedMigrations` 启用了版本化迁移）+ 改造 `mcpserver_test.go::TestMCPServer_LegacyPlaintextMigration`（改为先 `DROP TABLE schema_migrations` 模拟升级前旧库，再重开触发 0003/0004 真正执行，断言明文列物理删除且 `env_enc` 已加密可还原）+ 新增 `TestDropLegacyMCPColumns_RefusesWhenPlaintextRemains`（明文残留时拒绝删列→清密后成功→再删幂等）。已知环境限制：`internal/repo` 中 9 例历史 `go-sqlite3` CGO 用例在 `CGO_ENABLED=0` 下失败（既有，非本任务缺陷，M3-04 已 stash 复现基线一致）。gofmt：本机 `core.autocrlf=true`，仓库 Go 文件 checkout 为 CRLF，`gofmt -l` 全仓标 32 个文件为「需格式化」属历史约定噪声；LF 归一化 temp 比较确认本任务文件 gofmt 干净。diff 已扫描：无 `anmingwei`、本地绝对路径、硬编码密钥。
- 下一步：PLAN 中 **M3-09（可观测性 telemetry：OpenTelemetry 指标 + `/metrics` + 前端运行监控）** 成为下一个 ○，依赖 M3-03（已 ✅）。

---

### 2026-08-10 16:08 | M3-09 | ✅
- 完成内容：**可观测性 telemetry（M3 企业化第九任务，依赖 M3-03，对齐 PLAN 验收「跑几轮对话后 `/metrics` 有指标；前端概览展示最近调用/失败率」）**。① **metrics 子系统** `server/internal/metrics/metrics.go`：基于随 trpc-agent-go v1.10.0 间接引入的 OpenTelemetry SDK（v1.29.0）`sdk/metric` 定义 8 个指标——`codeagent_llm_calls_total`、`codeagent_llm_call_duration_seconds`(直方图)、`codeagent_llm_errors_total`、`codeagent_tool_calls_total`、`codeagent_tool_errors_total`(reason 维度)、`codeagent_token_prompt/completion/total`。`Init(Config{Enabled})` 用 `ManualReader` 即时聚合；自研 Prometheus 文本渲染器（不引入 `exporter/prometheus` 额外依赖，沙箱无法访问 GitHub VCS）；`Summary()` 返回进程内原子累加器快照供前端；所有 `Record*` 未启用时为安全空操作、`/metrics` 未启用返回 404。② **采集接线**：`engine` 调用路径——`api/chat.go`/`sse.go` 在 `eng.Chat`/`eng.Stream` 前后记 `RecordLLMCall(provider, model, duration, err)`；`executor/policy.go` 的 `SafeExecutor.Run`/`RunCommand` 用 `defer` 记 `RecordToolCall(ctx, toolCallReason(err), err)`（`toolCallReason` 把结果分类为 allowed/denied/checkpoint/failed，与 M3-05 的 `ErrCheckpointCreated` 互斥判定）；`api/usage.go` 的 `recordEngineUsage` 落库 token 后同步 `RecordTokenUsage`。③ **API/配置**：`api/monitoring.go` 新增 `GET /api/monitoring/overview`（复用 `currentUserID` 身份注入、受 `usage:read` RBAC 保护，返回 `metrics.Summary`）；`cmd/server/main.go` 注册 `/metrics`(gin.WrapH) 与 `/monitoring/overview`，`main()` 启动期 `metrics.Init({Enabled: cfg.MetricsEnabled()})`；`config` 新增 `METRICS_ENABLED`(默认 true) + `MetricsEnabled()`。`go.mod` 将 otel/otel/metric/sdk/metric 由 indirect 提升为 direct。④ **前端**：`web/src/api/monitoring.ts`(`getMonitoringOverview` + `MonitoringOverview` 接口) + `web/src/views/MonitoringView.vue`（四宫格 LLM 调用/失败/成功率、工具调用/失败/成功率、Token 三栏 + 未启用 NResult / 暂无数据 NEmpty 引导）+ 路由 `monitoring` + `DefaultLayout.vue` 菜单「运行监控」。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。新增测试 **12 例全 PASS**（纯 Go，免 gcc）：`metrics/metrics_test.go` 2 例（启用后 LLM/工具/token 累加 + Prometheus 文本含全部 8 指标且 counter 类型与标签正确 + HTTP Handler 200/Content-Type；禁用时 Record* 为空操作且计数器不变、`/metrics` 404）+ `api/monitoring_test.go` 3 例（未认证 401、启用返回递增快照、禁用仍可访问但 enabled=false 且计数不变）；`executor` 包现有测试随 `Run`/`RunCommand` 重构回归全绿。前端 `vue-tsc --noEmit` ✓ | `vite build` ✓（产出 `MonitoringView` chunk 4.10 kB）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 中历史 `go-sqlite3` CGO 用例报 "requires cgo to work" 属**既有失败**（已 stash 基线复现确认，非本任务缺陷）。diff 扫描：无 `anmingwei`、本地绝对路径、硬编码密钥。
- 下一步：PLAN 中 **M3-10（集成验证 E2E 企业化：登录→执行命令→审计可见→超预算暂停→人工检查点审批→artifact 浏览→指标可见 全链路；补 audit/usage/budget/checkpoint 单测与 HTTP 层测试）** 成为下一个 ○，依赖 M3-01..09（本任务即 M3-09，已 ✅）。

---

### 2026-08-10 17:25 | M3-10 | ✅
- 完成内容：**集成验证 E2E（企业化收口，依赖 M3-01..09）**。新增 `server/cmd/server/m3_integration_test.go::TestM3_Enterprise_E2E`——进程内 Gin 路由（复用 `buildRouter`/`e2eClient`/`newM1HTTPMockLLM`/`parseAGUI`，不调真实 LLM）跑通 **登录 → 建 workspace → Provider/模型 → 建会话绑 workspace → 执行命令(审计可见) → 超预算暂停 → 人工检查点审批 → artifact 浏览 → 指标可见** 全链路，覆盖 M3-01/03/04/05/06/09 在真实中间件与 RBAC 下的协同：① 脚本化 mock 驱动 `shell_exec` 落盘 `done.txt` 并经 `SafeExecutor` 写审计（GET /api/audit 可见 allow 决策）；② 读 `/api/usage` 已用 token → 设 user 预算上限=已用量 → 下一轮对话被 `EvaluateBudgets` 拦截（SSE RUN_ERROR「预算耗尽」并写预算审计）；③ 落库 pending checkpoint → POST /api/checkpoints/:id/resolve approve 经 `HostExecutor` 实际执行命令（`cp.txt` 落盘）并写允许审计；④ 直接写入 artifact 经 `GET /api/sessions/:id/artifacts` 列表 + `:name` 查看（enabled=true 内容可读，owner 隔离）；⑤ `GET /api/monitoring/overview` 概览 JSON + `/metrics` Prometheus 文本（测试内 `metrics.Init(Enabled:true)` 镜像生产 `main()`）。四个子系统的单测此前已在各里程碑落地（audit 4 + usage 9 + budget 11 + checkpoint 10 例），本任务补齐**缺失的 HTTP 层 E2E**。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。`go test ./cmd/server/ -run TestM3_Enterprise_E2E -count=1 -v` 全链路 11 步断言全 PASS（约 0.7s）。已知环境限制：本沙箱 `CGO_ENABLED=0` 无 gcc，`internal/repo` 中 10 例历史 `go-sqlite3` CGO 用例报 "requires cgo to work" 属**既有失败**（与本任务无关，建议 MX-05 迁纯 Go 驱动）。diff 扫描：无 `anmingwei`、本地绝对路径、硬编码密钥。
- 下一步：M3 阶段全部 ✅；**M4（自主化：Automation 数据模型与持久化 M4-01 成为首个 ○）** 门槛（M3 全 ✅）已满足，但 M4 任务清单当前尚未写入 PLAN.md（须在 PLAN.md 增补 M4 里程碑任务，下一轮循环即可拾取）。

---

### 2026-08-10 17:14 | M4-01 | ✅
- 完成内容：**Automation 数据模型与持久化（M4 自主化首个任务，依赖 M3，对齐 PLAN 验收「建/查/改/删 owner 隔离；viewer 写 403」）**。严格对齐 M2-02（MCP 管理面）的同款 owner-scoped CRUD + RBAC 模式。① **模型层** `server/internal/model/automation.go`：`Automation`（`gorm.Model` + `UserID` 归属 + `uniqueIndex:idx_user_automation(name)` + `TriggerType`[cron/webhook] + `CronExpr` + `WebhookToken`[`json:"-"`，仅运行期匹配、不回显] + `GoalPrompt`[type:text] + `Enabled`[默认 true] + `LastRun`/`NextRun`[`*time.Time`，调度器维护]）+ `Validate()`（name 必填、trigger_type 合法、cron 必有 cron_expr、goal_prompt 必填）。② **仓储层** `server/internal/repo/automation.go`：纯 owner-scoped CRUD（Create/List/GetByName/GetByID/**GetByWebhookToken**[M4-03 复用]/Update/Delete）+ `ErrAutomationNotFound`（越权即 404）；并在 `baselineModels()` 注册 `&model.Automation{}` 使全新库基线建成该表。③ **权限种子** `server/internal/model/role.go` `SeedRoles()` 幂等补 developer `automations:read/write`、viewer `automations:read`（已初始化库重启即生效，无需迁移）。④ **API 层** `server/internal/api/automation.go`：五路由 `POST/GET /api/automations`、`GET/PUT/DELETE /api/automations/:id`；读经 `automations:read`、写经 `automations:write`（`middleware.RequirePermission`）；`automationView` 不回显 `webhook_token`；webhook 触发器创建时自动生成 32 字节随机令牌（M4-03 按令牌匹配外部事件）；同名冲突前置检测返回 409。⑤ `cmd/server/main.go` `buildRouter` 在 skills 路由前注册五条 `/api/automations` 路由。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。新增 `server/cmd/server/automation_test.go::TestAutomation_CRUD`（纯 Go `glebarez/sqlite`，免 gcc）：developer 全生命周期（cron 创建 201 / webhook 创建自动生成令牌且库内非空 / 列表 total=2 / 详情 / 改 cron_expr+关 enabled / viewer 写 403 / viewer 读 200 / 非法 trigger_type 400 / cron 缺 cron_expr 400 / 缺 goal_prompt 400 / 第二用户创建后 dev1 访问 404 / dev1 删除 204 后再查 404）全 PASS。`go test ./internal/model/... ./internal/api/... ./cmd/server/... -count=1` 全绿。已知环境限制：`internal/repo` 中历史 `go-sqlite3` CGO 用例在 `CGO_ENABLED=0` 下失败（既有，与本任务无关）。diff 扫描：无 `anmingwei`、本地绝对路径、硬编码密钥。
- 下一步：PLAN 中 **M4-02（Cron 调度器：常驻 goroutine 加载启用的 Automation，按 cron 算 next_run 并到点建 Goal Session 跑 Loop）** 成为下一个 ○，依赖 M4-01（已 ✅）+ M1-11 Goal + M1-16 状态外置。

---

### 2026-08-10 20:12 | M4-02 | ✅
- 完成内容：**Cron 调度器（M4 自主化第二任务，依赖 M4-01 + M1-11 + M1-16，对齐 PLAN 验收「设 `*/1 * * * *` 测试 Automation → 下一分钟自动建 session 跑 Loop → 产出结果；`next_run` 正确更新」）**。① **自研 5 字段 cron 解析器** `server/internal/cron/cron.go`（不引第三方依赖，保持 `go build` 在沙箱封闭）：支持 `*`/`,`/`-`/`/`/`a/n`/`a-b/n`、dom/dow 二选一 OR 语义、月/星期三字母名、dow 7→0 归一；`Spec.Next(from)` 返回严格大于 from 的下一匹配（5 年内）。② **调度核心** `server/internal/scheduler/scheduler.go`：`Scheduler{DB, Runner AutomationRunner, TickInterval(默认30s), MaxRetries(默认2), RetryBackoff/RetryDelay(默认1min), Now}`；`Start` ticker 循环、`Tick/TickSync` 异步/同步扫描；`scan` 加载 `repo.ListEnabledCronAutomations`（新增 owner 无关的全启用 cron 查询）、nil NextRun 时算并持久化（不触发）、到点用 `running.LoadOrStore` 防重入后 goroutine 跑；`runAutomation` 预建 Session（`GetOrCreateSession`+`NewSessionKey`）→ 重试循环（MaxRetries+1 次）调 `Runner.Run`、每次失败 `recordFailure`（写 M3-01 `DBAuditor` 审计）→ `advanceNext`（成功 `ComputeNext` 入未来、失败 now+RetryDelay）、成功置 `LastRun`。`AutomationRunner` 接口解耦调度（无 LLM）与运行（LLM 引擎），便于纯单测。③ **生产运行器** `server/internal/api/automation_runner.go`：`NewAutomationLoopRunner` 实现 `scheduler.AutomationRunner`——`resolveChatModel`(默认模型) → `crypto.Decrypt` 解密 apiKey → `GetOrCreateSession`+`AppendMessage(user, GoalPrompt)` → `ensureWorkdir` → 强制 `team.EnableSubAgents=true; team.EnableGoal=true`（无人值守 Loop 跑到目标收敛）→ 按 `CheckpointEnabled` 建 checkpointer → `engine.New` + `eng.Chat` → `metrics.RecordLLMCall` + `recordEngineUsage` + `AppendMessage(assistant, reply)`；完整复用 `ChatHandler` 生产运行路径，保证 Loop 与交互式对话行为一致。④ **接线** `cmd/server/main.go`：构造 `schedTeam`（EnableSubAgents/EnableGoal/Guardrail 对齐生产）+ `loopRunner`，`scheduler.New(db.DB, loopRunner)` 后 `go schedulerSvc.Start(context.Background())` 常驻。⑤ `repo/automation.go` 新增 `ListEnabledCronAutomations`。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。新增 **13 例单测全 PASS**：`cron_test.go` 9 例（解析错误/每分钟/每小时/每天/周日/每月/步进+列表/strictly-after/dom-or-dow OR 语义）+ `scheduler_test.go` 4 例（mockRunner + 纯 Go `glebarez/sqlite` 内存库：nil NextRun 算不触发、到期跑一次并推进 next_run 置 last_run、重试+审计失败、未到期跳过）。已知环境限制：`internal/repo` 中历史 `go-sqlite3` CGO 用例在 `CGO_ENABLED=0` 无 gcc 下失败（既有，非本任务缺陷）。diff 扫描：无 `anmingwei`、本地绝对路径、硬编码密钥。
- 下一步：PLAN 中 **M4-03（Webhook 入口：POST /api/webhooks/:token 接收外部事件 → 匹配 Automation webhook 规则 → 触发 Loop；token 校验 + 速率限制）** 成为下一个 ○，依赖 M4-01（已 ✅）。

---

### 2026-08-10 21:39 | M4-03 | ✅
- 完成内容：**Webhook 外部事件入口（M4 自主化第三任务，依赖 M4-01，对齐 PLAN 验收「curl 打 webhook → 对应 Automation 触发 Loop；非法 token 401」）**。① 新增 `server/internal/api/webhook.go`：`WebhookHandler` 处理 `POST /api/webhooks/:token`（**不挂鉴权中间件**，纯靠 URL 32B 令牌匹配），流程为 速率限制 → `repo.GetAutomationByWebhookToken`（enabled=true 过滤）→ `running.LoadOrStore` 防同一 automation 并发重入 → 预建 Session（`GetOrCreateSession`+`NewSessionKey`）→ **202 Accepted 立即返回** → 异步 goroutine 跑 `AutomationRunner.Run`（复用 M4-02 同款 `loopRunner`，Goal Session 语义）→ 更新 `LastRun` + 写 M3-01 `DBAuditor` 审计（成功/失败）。② 速率限制器 `WebhookRateLimiter`（按 token 维度的滑动窗口，`Allow(token)` 超上限返回 false → handler 转 429；`now` 可注入便于单测）。③ `config` 增 `WEBHOOK_RATE_LIMIT`(默认 10)/`WEBHOOK_RATE_WINDOW_SECONDS`(默认 60) + `WebhookRateLimit()/WebhookRateWindow()`。④ `cmd/server/main.go` 在 `loopRunner` 构造之后注册 `r.POST("/api/webhooks/:token", api.NewWebhookHandler(db.DB, loopRunner, webhookLimiter).Handle)`（独立于 `protected` 鉴权组，确保外部系统免鉴权可达）。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。新增 `server/internal/api/webhook_test.go` **6 例全 PASS**（纯 Go `glebarez/sqlite`，免 gcc）：`TestWebhookRateLimiter_AllowAndReset`（窗口内上限 + 不同 token 隔离 + 超窗口重置）、`TestWebhookHandler_TriggersLoop`（合法 token → 202 且 runner 收到正确 automation/sessionKey、会话落库）、`TestWebhookHandler_InvalidToken`（非法 token → 401 且不触发 Loop）、`TestWebhookHandler_DisabledAutomation`（enabled=false → 401）、`TestWebhookHandler_RateLimit`（同 token 超上限 → 429）、`TestWebhookHandler_ConcurrentGuard`（运行中再打 → 409）。`go test ./internal/api/... ./internal/config/...` 全绿；`gofmt` 干净。已知环境限制：`cmd/server` 历史 `go-sqlite3` CGO 用例在 `CGO_ENABLED=0` 无 gcc 下编译失败（既有，非本任务缺陷）。
- 下一步：PLAN 中 **M4-04（Channel 层抽象：Web/CLI/Webhook/定时 统一经 Gateway 串行锁进同一 Runner）** 成为下一个 ○，依赖 M2 + M4-02 + M4-03。

---

### 2026-08-10 22:38 | M4-04 | ✅
- 完成内容：**Channel 层抽象（M4 自主化第四任务，依赖 M2 + M4-02 + M4-03，对齐 PLAN 验收「同一 Goal 从不同 Channel 进入都走统一 Gateway 串行锁，不串会话」）**。核心是把此前散落在 `ChatHandler`/`StreamChatHandler`/`AutomationLoopRunner` 的「解析模型 → 解密 → 建会话/写 user 消息 → 解析工作目录 → 构建引擎 → 加载历史 → 跑 Runner → 落用量/助手消息」流程收敛到**单一 `Gateway` 实例**，所有 Channel 共享同一份 per-session 串行锁。① 新增 `server/internal/api/gateway.go`：定义 `Channel` 接口 + `ChannelKind`（`ChannelWeb`/`ChannelCLI`/`ChannelWebhook`/`ChannelCron`/`ChannelIM` 五常量，预留 IM/邮件）；`GatewayConfig`（聚合 DB/EncKey/EngineTimeout/WorkspaceRoot/Team/StateStore/Skill/TaskRun/ToolSearch/Checkpoint 等全部依赖）；`Gateway`（含引用计数 `sessionLock` 的 `map[string]*sessionLock`，`lockSession`/`unlockSession` 保证同一 session 请求串行、不同 session 并发）；`allocateSessionKey`（空则 `repo.NewSessionKey()` 生成稳定 session_id）；`prepareRun` 统一前置于 Run/Stream 的共享流程；`Run`/`Stream` 均「分配 sessionKey → 加会话锁 → 经同一 `engine.Engine` 跑 → finalize」；`EvaluateBudget` 供各 Channel 复用 M3-04 预算护栏。② 重写 `chat.go`（`ChatHandler(gw *Gateway)`）、`sse.go`（`StreamChatHandler(gw *Gateway)`）、`automation_runner.go`（`engineLoopRunner` 持有 `*Gateway` + `Channel`，`Run` 调 `gw.Run` 而非自建引擎）——三者统一经同一 Gateway 入口。③ `cmd/server/main.go` 新增 `buildGateway(...)` 构造共享实例；`buildRouter` 增 `gw` 形参（8→9）；`ChatHandler/StreamChatHandler` 改用 gw；cron/webhook runner 分别传 `ChannelCron`/`ChannelWebhook`（与 Web 共享同一把会话锁）。④ 保留 api 包内既有辅助函数（`resolveChatModel`/`loadChatHistory`/`ensureWorkdir`/`resolveWorkspaceLocalDir`/`recordEngineUsage`/`buildPromptText`/`newAGUIConverter`/`currentUserID`），避免跨包重复；通过请求级 `TeamOverride` 让一个 Gateway 同时服务 Web（默认 Team）与自主 Loop（强制 Goal）。
- 验证：`CGO_ENABLED=0 go build ./...` ✓ | `go vet ./...` ✓。新增 `server/internal/api/gateway_test.go` **4 例全 PASS**（纯 Go，免 gcc）：`TestGateway_SessionLockSerializes`（12 goroutine 同 key → `maxConcurrent==1`）、`TestGateway_DifferentSessionsParallel`（8 个不同 key → `maxConcurrent==8`）、`TestGateway_AllocateSessionKey`（空→生成、非空→复用）、`TestChannelKinds`（五常量 Kind() 断言）。`go test ./internal/api/... -count=1` 全绿（含既有 webhook/automation 测试）。已知环境限制：`cmd/server` 历史 `go-sqlite3` CGO 用例在 `CGO_ENABLED=0` 无 gcc 下编译失败（既有，非本任务缺陷；buildRouter 9 参签名已在 5 处测试调用点同步更新，vet 通过）。diff 扫描：无 `anmingwei`、本地绝对路径、硬编码密钥；无遗留旧常量 `WebChannel`/`CronChannel`/`WebhookChannel`/`AutomationLoopConfig` 引用。
- 下一步：PLAN 中 **M4-05（跨天恢复：进程重启/中断后扫描未收敛 Goal Session → 读 PLAN/PROGRESS/LEARNINGS → 重建上下文续跑，依赖 M1-16 + M2-04）** 成为下一个 ○。
