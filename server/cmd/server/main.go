package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	inprocess "trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	fm "trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/api"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/evolution"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/promptiter"
	"github.com/ayanmw/multiagent2/server/internal/regression"
	"github.com/ayanmw/multiagent2/server/internal/knowledge"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/ayanmw/multiagent2/server/internal/scheduler"
	"github.com/ayanmw/multiagent2/server/internal/sessionstore"
	"github.com/ayanmw/multiagent2/server/internal/taskrun"
	"github.com/ayanmw/multiagent2/server/internal/toolsearch"
	"github.com/ayanmw/multiagent2/server/internal/worktree"
	"github.com/gin-gonic/gin"
)

// buildRouter 构造 Gin 路由（含全部 API 路由），抽出来便于在集成测试中进程内复用。
// toolSearchProvider 是「延迟工具箱」按需提供者（M2-06）：按当前 uid 聚合该用户启用的
// MCP 服务器工具箱（工具默认不暴露，由 tool_search/call_tool 双控制工具按需调用）。
// 传 nil 表示不挂载延迟工具箱（如集成测试）。
func buildRouter(db *repo.DB, cfg *config.Config, disc *provider.Discoverer, stateStore artifact.Store, enableState bool, taskRunController taskrunruntime.Controller, taskRunSession session.Service, toolSearchProvider engine.ToolSearchProvider, gw *api.Gateway) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "go-multi-agent-v2",
		})
	})

	// 可观测性（M3-09）：/metrics 暴露 Prometheus 文本格式指标；未启用时返回 404。
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	// A2A 协议（M5-07）：公开 Agent Card，供外部 Agent 发现本平台能力（无需鉴权）。
	r.GET("/.well-known/agent.json", api.AgentCardHandler())

	// Public auth routes
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/register", api.RegisterHandler(cfg.JWTSecret, db.DB))
		authGroup.POST("/login", api.LoginHandler(cfg.JWTSecret, db.DB))
	}

	// Protected routes (accept either a Bearer JWT or an X-API-Key header)
	protected := r.Group("/api")
	protected.Use(middleware.AuthMiddleware(cfg.JWTSecret, db.DB))
	{
		protected.GET("/me", api.MeHandler(db.DB))

		// Admin-only sub-group
		admin := protected.Group("/admin")
		admin.Use(middleware.RequireRole(model.RoleAdmin))
		{
			admin.GET("/roles", api.ListRolesHandler(db.DB))
		}

		// Provider management (user-scoped CRUD; API key encrypted at rest).
		// Write operations require the "providers:write" permission (RBAC, M0.5-02).
		protected.GET("/providers", api.ListProvidersHandler(db.DB))
		protected.POST("/providers", middleware.RequirePermission(db.DB, "providers", "write"), api.CreateProviderHandler(db.DB, cfg.EncryptionKey))
		protected.GET("/providers/:id", api.GetProviderHandler(db.DB))
		protected.PUT("/providers/:id", middleware.RequirePermission(db.DB, "providers", "write"), api.UpdateProviderHandler(db.DB, cfg.EncryptionKey))
		protected.DELETE("/providers/:id", middleware.RequirePermission(db.DB, "providers", "write"), api.DeleteProviderHandler(db.DB))
		protected.GET("/providers/:id/models", api.ListProviderModelsHandler(db.DB, disc))
		// Model catalog writes (sync + enable/disable) require "models:write".
		protected.POST("/providers/:id/models/sync", middleware.RequirePermission(db.DB, "models", "write"), api.SyncProviderModelsHandler(db.DB, disc))
		protected.GET("/providers/:id/models/managed", api.ListManagedModelsHandler(db.DB))
		protected.PUT("/providers/:id/models/:mid", middleware.RequirePermission(db.DB, "models", "write"), api.UpdateModelHandler(db.DB))

		// Managed model catalog (Agent may only select enabled models)
		protected.GET("/models", api.ListEnabledModelsHandler(db.DB))

		// API key management (owner-scoped). All operations require "apikeys:write".
		protected.POST("/auth/apikeys", middleware.RequirePermission(db.DB, "apikeys", "write"), api.CreateAPIKeyHandler(db.DB))
		protected.GET("/auth/apikeys", middleware.RequirePermission(db.DB, "apikeys", "write"), api.ListAPIKeysHandler(db.DB))
		protected.DELETE("/auth/apikeys/:id", middleware.RequirePermission(db.DB, "apikeys", "write"), api.RevokeAPIKeyHandler(db.DB))

		// Session 管理（M0-12）：新建 / 列表 / 详情（含历史消息）。
		// Deletion requires "sessions:write" (owner-scoped, M0.5-02).
		protected.POST("/sessions", api.CreateSessionHandler(db.DB))
		protected.GET("/sessions", api.ListSessionsHandler(db.DB))
		protected.GET("/sessions/:id", api.GetSessionHandler(db.DB))
		// 会话「运行状态」外置文件（PLAN/PROGRESS/LEARNINGS，M1-16），供前端查看 Agent 计划与进展。
		protected.GET("/sessions/:id/state", api.GetSessionStateHandler(db.DB, stateStore, enableState))
		// Artifact 浏览器（M3-06）：列出 / 查看 / 下载某会话作用域下的全部产物
		// （PLAN/PROGRESS/LEARNINGS + Agent 写下的报告/diff/构建产物）。复用 M1-16
		// artifact.Store，owner 隔离经 resolveArtifactSession（当前用户只能看自己会话）。
		// 列表接口无需 RBAC 权限（与 state 查看一致，仅 owner 隔离）；下载同理。
		protected.GET("/sessions/:id/artifacts", api.ListSessionArtifactsHandler(db.DB, stateStore, enableState))
		protected.GET("/sessions/:id/artifacts/:name", api.GetSessionArtifactHandler(db.DB, stateStore, enableState))
		protected.PUT("/sessions/:id", middleware.RequirePermission(db.DB, "sessions", "write"), api.RenameSessionHandler(db.DB))
		protected.DELETE("/sessions/:id", middleware.RequirePermission(db.DB, "sessions", "write"), api.DeleteSessionHandler(db.DB))

		// 斜杠命令注册表（M1-14）：前端/CLI 共用，新增命令只改后端 command.Builtin()。
		protected.GET("/commands", api.ListCommandsHandler())

		// Workspace 管理（M1-07）：用户归属的工作区 CRUD；对话可绑定 workspace，
		// 使 Agent 的 CodeAct 工具在该 workspace 本地目录执行。写操作需 workspaces:write 权限。
		protected.GET("/workspaces", api.ListWorkspacesHandler(db.DB))
		protected.POST("/workspaces", middleware.RequirePermission(db.DB, "workspaces", "write"), api.CreateWorkspaceHandler(db.DB, cfg.WorkspaceRoot))
		protected.GET("/workspaces/:id", api.GetWorkspaceHandler(db.DB))
		protected.PUT("/workspaces/:id", middleware.RequirePermission(db.DB, "workspaces", "write"), api.UpdateWorkspaceHandler(db.DB))
		protected.DELETE("/workspaces/:id", middleware.RequirePermission(db.DB, "workspaces", "write"), api.DeleteWorkspaceHandler(db.DB))

		// MCP 服务器管理中心（M2-02）：用户归属的 MCP 配置 CRUD（仅管理面 + 校验，
		// 不在此装载工具；真实装载由 M2-06 toolsearch 按需调用框架 tool/mcp）。
		// 读操作需 mcp:read，写操作需 mcp:write（RBAC）。
		// M3-07：env/headers 以 AES-256-GCM 加密落库，故各 handler 需注入 cfg.EncryptionKey。
		protected.GET("/mcp", middleware.RequirePermission(db.DB, "mcp", "read"), api.ListMCPServersHandler(db.DB, cfg.EncryptionKey))
		protected.POST("/mcp", middleware.RequirePermission(db.DB, "mcp", "write"), api.CreateMCPServerHandler(db.DB, cfg.EncryptionKey))
		protected.GET("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "read"), api.GetMCPServerHandler(db.DB, cfg.EncryptionKey))
		protected.PUT("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "write"), api.UpdateMCPServerHandler(db.DB, cfg.EncryptionKey))
		protected.DELETE("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "write"), api.DeleteMCPServerHandler(db.DB, cfg.EncryptionKey))

		// 执行审计日志（M3-01）：CodeAct/Git/taskrun 三类命令执行的审计落库查询。
		// owner 隔离：developer/admin 看全员，viewer 仅看本人；读操作需 audit:read（RBAC）。
		protected.GET("/audit", middleware.RequirePermission(db.DB, "audit", "read"), api.ListAuditLogsHandler(db.DB))

		// Token/费用计量（M3-03）：对话结束后落库的 token 用量查询与聚合。
		// owner 隔离：developer/admin 看全员，viewer 仅看本人；读操作需 usage:read（RBAC）。
		protected.GET("/usage", middleware.RequirePermission(db.DB, "usage", "read"), api.ListUsageHandler(db.DB))

		// 可观测性概览（M3-09）：返回进程内 OpenTelemetry 指标聚合快照
		// （LLM 调用/失败、工具调用/失败、token 用量），供前端「运行监控」概览卡片。
		// 与 usage 同级保护（usage:read，RBAC）。
		protected.GET("/monitoring/overview", middleware.RequirePermission(db.DB, "usage", "read"), api.MonitoringOverviewHandler())

		// 平台级预算护栏（M3-04）：管理员设定 / 查询预算策略（user/session/automation 三级阈值）。
		// 读操作需 budgets:read，写（upsert / 删除）需 budgets:write（RBAC）。
		protected.GET("/budgets", middleware.RequirePermission(db.DB, "budgets", "read"), api.ListBudgetsHandler(db.DB))
		protected.PUT("/budgets", middleware.RequirePermission(db.DB, "budgets", "write"), api.UpsertBudgetHandler(db.DB))
		protected.DELETE("/budgets/:id", middleware.RequirePermission(db.DB, "budgets", "write"), api.DeleteBudgetHandler(db.DB))

		// 人工检查点（M3-05 human-in-the-loop）：无人值守命中 ask 危险命令生成的待审批记录，
		// 经前端审批（approve 执行 / reject 中止）。读需 checkpoints:read，审批写需 checkpoints:write。
		protected.GET("/checkpoints", middleware.RequirePermission(db.DB, "checkpoints", "read"), api.ListCheckpointsHandler(db.DB))
		protected.POST("/checkpoints/:id/resolve", middleware.RequirePermission(db.DB, "checkpoints", "write"), api.ResolveCheckpointHandler(db.DB))

		// Automation 自主化任务（M4-01）：用户归属的自主化任务 CRUD（数据模型 + 持久化）。
		// 调度器（M4-02 cron）/ Webhook 入口（M4-03）后续消费本表；读需 automations:read，写需 automations:write。
		protected.GET("/automations", middleware.RequirePermission(db.DB, "automations", "read"), api.ListAutomationsHandler(db.DB))
		protected.POST("/automations", middleware.RequirePermission(db.DB, "automations", "write"), api.CreateAutomationHandler(db.DB))
		protected.GET("/automations/:id", middleware.RequirePermission(db.DB, "automations", "read"), api.GetAutomationHandler(db.DB))
		protected.PUT("/automations/:id", middleware.RequirePermission(db.DB, "automations", "write"), api.UpdateAutomationHandler(db.DB))
		protected.DELETE("/automations/:id", middleware.RequirePermission(db.DB, "automations", "write"), api.DeleteAutomationHandler(db.DB))
		// 运行历史（M4-08）：按 automation 归属列出 running/done/failed 记录，最近排前。
		protected.GET("/automations/:id/runs", middleware.RequirePermission(db.DB, "automations", "read"), api.ListAutomationRunsHandler(db.DB))

		// 通知中心（M4-07）：自主化 Loop 完成/失败/需检查点时写入的站内信。
		// 读列表需 notifications:read，标记已读写需 notifications:write（owner 隔离）。
		protected.GET("/notifications", middleware.RequirePermission(db.DB, "notifications", "read"), api.ListNotificationsHandler(db.DB))
		protected.POST("/notifications/:id/read", middleware.RequirePermission(db.DB, "notifications", "write"), api.MarkNotificationReadHandler(db.DB))
		protected.POST("/notifications/read-all", middleware.RequirePermission(db.DB, "notifications", "write"), api.MarkAllNotificationsReadHandler(db.DB))

		// Skills 技能仓库（M2-03）：用户归属的技能管理（文件系统后端，owner 隔离）。
		// 读操作需 skills:read，写操作（建/更新/删私有技能）需 skills:write（RBAC）。
		// 共享技能（仓库 skills/ 目录）对所有用户可见但只读，不可经 API 改写。
		protected.GET("/skills", middleware.RequirePermission(db.DB, "skills", "read"), api.ListSkillsHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.POST("/skills", middleware.RequirePermission(db.DB, "skills", "write"), api.CreateSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.GET("/skills/:name", middleware.RequirePermission(db.DB, "skills", "read"), api.GetSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.PUT("/skills/:name", middleware.RequirePermission(db.DB, "skills", "write"), api.UpdateSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.DELETE("/skills/:name", middleware.RequirePermission(db.DB, "skills", "write"), api.DeleteSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))

		// 知识库 RAG（M5-02）：用户归属的知识库 CRUD + 文档索引/检索（owner 隔离）。
		// 读操作需 knowledge:read，写操作（建/更新/删/索引/删文档）需 knowledge:write（RBAC）。
		protected.GET("/knowledge", middleware.RequirePermission(db.DB, "knowledge", "read"), api.ListKnowledgeBasesHandler(db.DB))
		protected.POST("/knowledge", middleware.RequirePermission(db.DB, "knowledge", "write"), api.CreateKnowledgeBaseHandler(db.DB))
		protected.GET("/knowledge/:id", middleware.RequirePermission(db.DB, "knowledge", "read"), api.GetKnowledgeBaseHandler(db.DB))
		protected.PUT("/knowledge/:id", middleware.RequirePermission(db.DB, "knowledge", "write"), api.UpdateKnowledgeBaseHandler(db.DB))
		protected.DELETE("/knowledge/:id", middleware.RequirePermission(db.DB, "knowledge", "write"), api.DeleteKnowledgeBaseHandler(db.DB))
		protected.GET("/knowledge/:id/documents", middleware.RequirePermission(db.DB, "knowledge", "read"), api.ListKnowledgeDocumentsHandler(db.DB))
		protected.POST("/knowledge/:id/documents", middleware.RequirePermission(db.DB, "knowledge", "write"), api.IndexDocumentHandler(db.DB))
		protected.DELETE("/knowledge/:id/documents/:name", middleware.RequirePermission(db.DB, "knowledge", "write"), api.DeleteKnowledgeDocumentHandler(db.DB))
		protected.POST("/knowledge/:id/search", middleware.RequirePermission(db.DB, "knowledge", "read"), api.SearchKnowledgeHandler(db.DB))

		// 后台任务管控（M2-04）：列表/详情/取消/transcript，owner 隔离 + RBAC。
		// 读操作需 taskruns:read，取消写操作需 taskruns:write。
		protected.GET("/taskruns", middleware.RequirePermission(db.DB, "taskruns", "read"), api.ListTaskRunsHandler(taskRunController))
		protected.GET("/taskruns/:id", middleware.RequirePermission(db.DB, "taskruns", "read"), api.GetTaskRunHandler(taskRunController))
		protected.POST("/taskruns/:id/cancel", middleware.RequirePermission(db.DB, "taskruns", "write"), api.CancelTaskRunHandler(taskRunController))
		protected.GET("/taskruns/:id/transcript", middleware.RequirePermission(db.DB, "taskruns", "read"), api.GetTaskRunTranscriptHandler(taskRunController, taskRunSession))

		// 技能进化飞轮（M5-03）：候选技能列表（owner 隔离，需 skill_candidates:read）；
		// 触发扫描（需 skill_candidates:write）；审批流转 approve/reject（需 skill_candidates:write）。
		// evolutionSvc 为 nil 时（测试套件未注入）跳过整组路由，不影响既有集成测试。
		if api.EvolutionService() != nil {
			protected.GET("/skill-candidates", middleware.RequirePermission(db.DB, "skill_candidates", "read"), api.ListSkillCandidatesHandler(db.DB))
			protected.POST("/skill-candidates/scan", middleware.RequirePermission(db.DB, "skill_candidates", "write"), api.ScanSkillCandidatesHandler())
			protected.POST("/skill-candidates/:id/resolve", middleware.RequirePermission(db.DB, "skill_candidates", "write"), api.ResolveSkillCandidateHandler(db.DB, cfg.SkillsRoot()))
		}

		// 评估回归（M5-05）：评估集 owner-scoped CRUD + 用例 CRUD + 运行触发（异步）
		// + 运行列表/详情 + 结果查看；读需 evaluations:read，写需 evaluations:write（RBAC）。
		// evalSvc 为 nil 时（测试套件未注入）跳过整组路由，不影响既有集成测试。
		if api.EvalService() != nil {
			protected.GET("/eval/datasets", middleware.RequirePermission(db.DB, "evaluations", "read"), api.ListEvalDatasetsHandler(db.DB))
			protected.POST("/eval/datasets", middleware.RequirePermission(db.DB, "evaluations", "write"), api.CreateEvalDatasetHandler(db.DB))
			protected.GET("/eval/datasets/:id", middleware.RequirePermission(db.DB, "evaluations", "read"), api.GetEvalDatasetHandler(db.DB))
			protected.PUT("/eval/datasets/:id", middleware.RequirePermission(db.DB, "evaluations", "write"), api.UpdateEvalDatasetHandler(db.DB))
			protected.DELETE("/eval/datasets/:id", middleware.RequirePermission(db.DB, "evaluations", "write"), api.DeleteEvalDatasetHandler(db.DB))
			protected.GET("/eval/datasets/:id/cases", middleware.RequirePermission(db.DB, "evaluations", "read"), api.ListEvalCasesHandler(db.DB))
			protected.POST("/eval/datasets/:id/cases", middleware.RequirePermission(db.DB, "evaluations", "write"), api.CreateEvalCaseHandler(db.DB))
			protected.GET("/eval/datasets/:id/cases/:caseId", middleware.RequirePermission(db.DB, "evaluations", "read"), api.GetEvalCaseHandler(db.DB))
			protected.PUT("/eval/datasets/:id/cases/:caseId", middleware.RequirePermission(db.DB, "evaluations", "write"), api.UpdateEvalCaseHandler(db.DB))
			protected.DELETE("/eval/datasets/:id/cases/:caseId", middleware.RequirePermission(db.DB, "evaluations", "write"), api.DeleteEvalCaseHandler(db.DB))
			protected.POST("/eval/datasets/:id/run", middleware.RequirePermission(db.DB, "evaluations", "write"), api.RunEvalHandler(db.DB))
			protected.GET("/eval/runs", middleware.RequirePermission(db.DB, "evaluations", "read"), api.ListEvalRunsHandler(db.DB))
			protected.GET("/eval/runs/:id", middleware.RequirePermission(db.DB, "evaluations", "read"), api.GetEvalRunHandler(db.DB))
			protected.GET("/eval/runs/:id/results", middleware.RequirePermission(db.DB, "evaluations", "read"), api.ListEvalResultsHandler(db.DB))
		}

		// GEPA 反射式 Prompt 优化（M5-06）：异步触发优化 + 运行列表/详情 + 回滚；
		// 以及可优化指令（AgentInstruction）的查看/手动编辑。读需 promptiter:read /
		// instructions:read，写需 promptiter:write / instructions:write（RBAC）。
		// PromptIterService 为 nil 时（测试套件未注入）跳过整组路由，不影响既有集成测试。
		if api.PromptIterService() != nil {
			protected.POST("/promptiter/optimize", middleware.RequirePermission(db.DB, "promptiter", "write"), api.OptimizePromptIterHandler(db.DB))
			protected.GET("/promptiter/runs", middleware.RequirePermission(db.DB, "promptiter", "read"), api.ListPromptIterRunsHandler(db.DB))
			protected.GET("/promptiter/runs/:id", middleware.RequirePermission(db.DB, "promptiter", "read"), api.GetPromptIterRunHandler(db.DB))
			protected.POST("/promptiter/runs/:id/rollback", middleware.RequirePermission(db.DB, "promptiter", "write"), api.RollbackPromptIterHandler(db.DB))
			protected.GET("/instructions", middleware.RequirePermission(db.DB, "instructions", "read"), api.ListInstructionsHandler(db.DB))
			protected.GET("/instructions/:name", middleware.RequirePermission(db.DB, "instructions", "read"), api.GetInstructionHandler(db.DB))
			protected.PUT("/instructions/:name", middleware.RequirePermission(db.DB, "instructions", "write"), api.UpdateInstructionHandler(db.DB))
		}

		// Agent 对话（引擎封装 trpc-agent-go，连接已启用 Model+Provider）
		// M1-08：AGENT_MODE=team 时根 Agent 换成 Orchestrator，代码落地委托 Coder 子代理。
		// M1-09：team 模式默认再加入只读 Reviewer（TEAM_REVIEWER），形成「实现→审阅→修复」回环。
		// M1-11：team 模式默认启用目标契约（GOAL_CONTRACT），Orchestrator 必须把目标
		// 推进到 complete/blocked 才允许结束，过早的最终答复会被拦截并要求继续干活。
		// M1-12：team 模式默认启用 Plan-Execute 循环（PLAN_EXECUTE），Orchestrator 必须
		// 先建计划、逐项执行完毕才允许结束；二者叠加在 Orchestrator 上。
	// M2-03：skillWarmStart 等参数驱动「技能 warm-start」，会话开始把相关 SKILL.md
	// 注入根 Agent 系统上下文（长度受 SkillWarmStartMaxChars 上限控制）。
	// Web 对话（/api/chat 与 SSE 端点）经统一 Gateway（M4-04）跑引擎：Gateway 持有 Team
	// 配置与全部运行时依赖，并负责稳定 session_id + 每会话串行锁 + 统一 Runner，
	// 使 Web/SSE/定时/Webhook 收敛到同一代码路径。
	protected.POST("/chat", api.ChatHandler(gw))

		// AG-UI SSE 流式对话端点（M0-11）：事件流转 AG-UI 协议，Session 持久化。
		// M0.5-06：message 改由 POST body 传递（避免明文进访问日志），故注册为 POST。
		// 事件流由统一 Gateway 经 gw.Stream 产出（详见 StreamChatHandler）。
		protected.POST("/chat/:session_id/stream", api.StreamChatHandler(gw))

		// A2A 协议任务入口（M5-07）：外部 Agent 经 JSON-RPC 2.0（method=message/send / tasks/send）
		// 调用本平台能力。经统一 Gateway 跑引擎（复用会话串行锁、多轮记忆、预算护栏与用量计量），
		// 外部任务以 ChannelA2A 标记，与 Web/CLI/定时/Webhook 收敛到同一套 Runner。
		// 鉴权沿用 AuthMiddleware：外部 A2A client 用 X-API-Key 或 Bearer 调用（Agent Card 声明 apiKey）。
		protected.POST("/a2a", api.A2AHandler(gw))
	}

	return r
}

// buildToolSearchProvider 构造「延迟工具箱」按需提供者（M2-06）。引擎在每次对话开始时调用它，
// 按当前 uid 聚合该用户「已启用」的 MCP 服务器工具箱；工具默认不暴露给模型，由 tool_search/
// call_tool 双控制工具按需检索与调用。单个服务器连接/初始化失败安全跳过（fail-open），不阻断
// 对话；仅当全部服务器都不可用时才返回空工具箱（引擎据此不挂载双控制工具）。
// encKey 用于解密 mcp_servers 的 env/headers 密文列（M3-07），解密后才能真实装载工具。
func buildToolSearchProvider(db *repo.DB, encKey []byte) engine.ToolSearchProvider {
	return func(ctx context.Context, userID uint) (*toolsearch.Toolbox, error) {
		servers, err := repo.ListMCPServers(db.DB, userID, encKey)
		if err != nil {
			return nil, err
		}
		var box *toolsearch.Toolbox
		for i := range servers {
			s := servers[i]
			if !s.Enabled {
				continue
			}
			tb, lerr := toolsearch.LoadMCPServerTools(ctx, s)
			if lerr != nil {
				// 单个服务器不可用安全跳过，而非阻断整轮对话。
				log.Printf("[WARN] toolsearch: 加载 MCP 服务器 %q 工具失败: %v", s.Name, lerr)
				continue
			}
			if box == nil {
				box = toolsearch.NewToolbox()
			}
			box.Merge(tb)
		}
		return box, nil
	}
}

// buildGateway 根据配置与运行时依赖构造统一网关（M4-04），供 Web 对话、SSE、定时、Webhook 共享
// 同一会话串行锁与同一套引擎构造。db 为 *repo.DB，内部取其底层 *gorm.DB 注入 GatewayConfig。
func buildGateway(db *repo.DB, cfg *config.Config, stateStore artifact.Store, enableState bool, taskRunController taskrunruntime.Controller, taskRunSession session.Service, toolSearchProvider engine.ToolSearchProvider) *api.Gateway {
	teamCfg := engine.TeamConfig{
		EnableSubAgents: cfg.SubAgentsEnabled(),
		EnableReviewer:  cfg.ReviewerEnabled(),
		MaxReviewRounds: cfg.MaxReviewRounds(),
		EnableGoal:      cfg.GoalEnabled(),
		MaxGoalNudges:   cfg.MaxGoalNudges(),
		EnablePlan:      cfg.PlanEnabled(),
		MaxPlanNudges:   cfg.MaxPlanNudges(),
		Guardrail:       cfg.GuardrailConfig(), // M1-13：护栏熔断预算（默认启用）
	}
	return api.NewGateway(api.GatewayConfig{
		DB:                 db.DB,
		EncKey:             cfg.EncryptionKey,
		EngineTimeout:      cfg.EngineTimeout(),
		WorkspaceRoot:      cfg.WorkspaceRoot,
		Team:               teamCfg,
		StateStore:         stateStore,
		EnableState:        enableState,
		SkillRoot:          cfg.SkillsRoot(),
		SkillDataDir:       cfg.SkillsDataDir(),
		SkillWarmStart:     cfg.SkillWarmStart(),
		SkillMaxChars:      cfg.SkillWarmStartMaxChars(),
		TaskRunController:  taskRunController,
		TaskRunSession:     taskRunSession,
		ToolSearchEnabled:  cfg.ToolSearchEnabled(),
		ToolSearchProvider: toolSearchProvider,
		CheckpointEnabled:  cfg.CheckpointEnabled(),
		ExecutorMode:       cfg.ExecutorMode(), // M4-06：执行器运行模式（默认 unattended）
		KnowledgeRetriever: buildKnowledgeRetriever(db, cfg),
	})
}

// knowledgeRetrieverAdapter 把 knowledge.Manager 适配为 engine.KnowledgeRetriever（M5-02）：
// 对话前检索该用户全部知识库的相关切片，拼接为注入文本（控长由 Manager 内部约束）。
type knowledgeRetrieverAdapter struct {
	mgr      *knowledge.Manager
	maxChars int
}

func (a *knowledgeRetrieverAdapter) Retrieve(ctx context.Context, userID, query string) (string, error) {
	return a.mgr.RetrieveContext(ctx, userID, query, a.maxChars)
}

// buildKnowledgeRetriever 按配置构造知识库检索注入器（M5-02）。KNOWLEDGE_ENABLED=false 时返回 nil，
// 引擎不做任何检索注入；开启时返回包装 knowledge.Manager 的适配器。
func buildKnowledgeRetriever(db *repo.DB, cfg *config.Config) engine.KnowledgeRetriever {
	if !cfg.KnowledgeEnabled() {
		return nil
	}
	return &knowledgeRetrieverAdapter{mgr: knowledge.NewManager(db.DB), maxChars: 4000}
}

func main() {
	// Load configuration
	cfg := config.Load()

	// 可观测性（M3-09）：初始化 OpenTelemetry MeterProvider 并开放 /metrics。
	// METRICS_ENABLED=false 时不初始化，Record* 均为空操作、/metrics 返回 404。
	if err := metrics.Init(metrics.Config{Enabled: cfg.MetricsEnabled()}); err != nil {
		log.Fatalf("Failed to initialize metrics: %v", err)
	}

	// Initialize database
	db, err := repo.NewDB(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Model auto-discovery (provider /v1/models, cached 5 minutes).
	discoverer := provider.NewDiscoverer(cfg.EncryptionKey, 5*time.Minute)

	// 工作状态文件存储（M1-16）：落盘到 cfg.ArtifactRoot()，使长任务中断/重启后可续跑。
	stateStore, stErr := artifact.NewFileStore(cfg.ArtifactRoot())
	if stErr != nil {
		log.Fatalf("Failed to initialize artifact store: %v", stErr)
	}
	enableState := cfg.StateEnabled()

	// 后台任务控制面（M2-04）：
	//   - sessionstore.New(db.DB)：把 child session 事件落盘到同一 SQLite（transcript 持久化）。
	//   - inprocess.NewFileStore：run 记录 JSON 持久化（跨重启保留）。
	//   - worker 工厂按 OwnerUserID 解析该用户的模型 + 工作目录，构建 Coder 子代理。
	//   - taskrun.NewController 组装 inprocess.Service + 内部 worker Runner（挂持久化 session）。
	taskRunSession := sessionstore.New(db.DB)
	runStore, storeErr := inprocess.NewFileStore(filepath.Join("data", "taskruns.json"))
	if storeErr != nil {
		log.Fatalf("Failed to initialize taskrun store: %v", storeErr)
	}
	workerResolver := taskrun.WorkerResolver{
		ResolveModel: func(ctx context.Context, userID string) (fm.Model, error) {
			uid, perr := strconv.ParseUint(userID, 10, 64)
			if perr != nil {
				return nil, fmt.Errorf("taskrun: 非法用户 id %q", userID)
			}
			list, lerr := repo.ListEnabledModels(db.DB, uint(uid))
			if lerr != nil {
				return nil, lerr
			}
			var m *model.Model
			for i := range list {
				if list[i].IsDefault {
					m = &list[i]
					break
				}
			}
			if m == nil && len(list) > 0 {
				m = &list[0]
			}
			if m == nil {
				return nil, fmt.Errorf("taskrun: 用户 %s 无可用模型", userID)
			}
			p, perr := repo.GetProviderByID(db.DB, m.ProviderID)
			if perr != nil {
				return nil, perr
			}
			apiKey := ""
			if p.APIKeyEnc != "" {
				if dec, derr := crypto.Decrypt(p.APIKeyEnc, cfg.EncryptionKey); derr == nil {
					apiKey = dec
				}
			}
			return openai.New(m.ModelID, openai.WithAPIKey(apiKey), openai.WithBaseURL(p.BaseURL)), nil
		},
		ResolveWorkdir: func(ctx context.Context, userID string) (string, error) {
			uid, perr := strconv.ParseUint(userID, 10, 64)
			if perr != nil {
				return "", fmt.Errorf("taskrun: 非法用户 id %q", userID)
			}
			dir := filepath.Join(cfg.WorkspaceRoot, strconv.FormatUint(uid, 10))
			if merr := os.MkdirAll(dir, 0o755); merr != nil {
				return "", merr
			}
			return dir, nil
		},
		// M2-05：worktree 隔离钩子。开启时每个 taskrun 子任务在独立 worktree（独立分支
		// taskrun/<id>）内执行，完成后 merge 回主分支并清理，绝不 push 远程。
		Worktree: &taskrun.WorktreeHook{Enabled: cfg.WorktreeIsolation(), Manager: worktree.NewManager()},
		// M3-01：worker 子代理命令经 DBAuditor 落库审计，按 OwnerUserID 归属，
		// 实现 taskrun 后台子任务执行的全量审计覆盖。
		NewAuditor: func(ownerUserID uint) executor.Auditor { return repo.NewDBAuditor(db.DB, ownerUserID) },
		// M3-05：后台任务是真正的无人值守场景——命中 ask 级危险命令时不直接 deny，
		// 而是把待审批命令写入 checkpoints 表（绑定 owner 与子任务会话）并暂停，
		// 等人在前端 approve/reject 后再决定续跑或中止。CHECKPOINT_ENABLED=false 时为 nil（退回 deny）。
		NewCheckpointer: func(ownerUserID uint, childSessionID string) executor.Checkpointer {
			if !cfg.CheckpointEnabled() {
				return nil
			}
			return func(req executor.CheckpointRequest) (string, error) {
				sid := req.SessionID
				if sid == "" {
					sid = childSessionID
				}
				cp := &model.Checkpoint{
					SessionID: sid,
					UserID:    ownerUserID,
					Command:   req.Command,
					Workdir:   req.Workdir,
					Reason:    req.Reason,
					Context:   req.Context,
					Status:    model.CheckpointPending,
				}
				if err := repo.CreateCheckpoint(db.DB, cp); err != nil {
					return "", err
				}
				return cp.DisplayID(), nil
			}
		},
	}
	workerFactory := taskrun.BuildAgentFactory(cfg.GuardrailConfig(), workerResolver, executor.ModeUnattended)
	taskRunController, ctrlErr := taskrun.NewController(
		context.Background(),
		codeagent.RoleCoder,
		workerFactory,
		runStore,
		taskRunSession,
		workerResolver.Worktree, // inprocess.Observer：子任务终态 merge 回主分支 + 清理
	)
	if ctrlErr != nil {
		log.Fatalf("Failed to initialize taskrun controller: %v", ctrlErr)
	}

	// 统一网关（M4-04）：Web 对话 / SSE / 定时 / Webhook 全部经此 Gateway，共享同一会话
	// 串行锁与同一套引擎构造（Team 取 Web 默认配置；自主化 Loop 通过 TeamOverride 强制
	// 开启子代理 + 目标契约）。
	gw := buildGateway(db, cfg, stateStore, enableState, taskRunController, taskRunSession, buildToolSearchProvider(db, cfg.EncryptionKey))

	// 技能进化飞轮（M5-03）：后台异步扫描已结束 session 的 transcript，经 LLM 提取候选
	// SKILL.md，质量门控 + 去重后写入 skill_candidates 表（pending），等待人工审批（M5-04）。
	// 模型解析复用「默认启用模型 + Provider 解密」逻辑（与对话端点一致），按会话归属用户隔离。
	evoModelResolver := func(ctx context.Context, userID uint) (engine.ModelConfig, error) {
		list, lerr := repo.ListEnabledModels(db.DB, userID)
		if lerr != nil {
			return engine.ModelConfig{}, lerr
		}
		var m *model.Model
		for i := range list {
			if list[i].IsDefault {
				m = &list[i]
				break
			}
		}
		if m == nil && len(list) > 0 {
			m = &list[0]
		}
		if m == nil {
			return engine.ModelConfig{}, fmt.Errorf("evolution: 用户 %d 无可用模型", userID)
		}
		p, perr := repo.GetProviderByID(db.DB, m.ProviderID)
		if perr != nil {
			return engine.ModelConfig{}, perr
		}
		if p.UserID != userID {
			return engine.ModelConfig{}, fmt.Errorf("evolution: 无权限使用该 Provider")
		}
		apiKey := ""
		if p.APIKeyEnc != "" {
			if dec, derr := crypto.Decrypt(p.APIKeyEnc, cfg.EncryptionKey); derr == nil {
				apiKey = dec
			}
		}
		return engine.ModelConfig{
			ModelID:  m.ModelID,
			BaseURL:  p.BaseURL,
			APIKey:   apiKey,
			Protocol: string(p.Protocol),
			Timeout:  cfg.EngineTimeout(),
		}, nil
	}
	evoExtractor := evolution.NewLLMExtractor(evoModelResolver, cfg.EngineTimeout())
	evoSvc := evolution.NewService(db.DB, evoExtractor)
	api.SetEvolutionService(evoSvc)
	if cfg.EvolutionEnabled() {
		go evoSvc.StartLoop(context.Background(), cfg.EvolutionInterval())
	}

	// 评估回归（M5-05）：评估集管理 + 运行（多次采样取稳定分）+ LLM 裁判评分。
	// 模型解析复用「默认启用模型 + Provider 解密」逻辑（与对话端点一致），并按
	// 指定 modelID 解析（用例/运行级可覆盖使用的模型）。
	evalModelResolver := func(ctx context.Context, userID uint, modelID string) (engine.ModelConfig, error) {
		list, lerr := repo.ListEnabledModels(db.DB, userID)
		if lerr != nil {
			return engine.ModelConfig{}, lerr
		}
		var m *model.Model
		if modelID != "" {
			// 按 model id 或 model_id（上游模型名）精确匹配用户启用模型。
			for i := range list {
				if strconv.FormatUint(uint64(list[i].ID), 10) == modelID || list[i].ModelID == modelID {
					m = &list[i]
					break
				}
			}
		}
		// 未指定或匹配不到时回退「默认启用模型」。
		if m == nil {
			for i := range list {
				if list[i].IsDefault {
					m = &list[i]
					break
				}
			}
		}
		if m == nil && len(list) > 0 {
			m = &list[0]
		}
		if m == nil {
			return engine.ModelConfig{}, fmt.Errorf("eval: 用户 %d 无可用模型", userID)
		}
		p, perr := repo.GetProviderByID(db.DB, m.ProviderID)
		if perr != nil {
			return engine.ModelConfig{}, perr
		}
		if p.UserID != userID {
			return engine.ModelConfig{}, fmt.Errorf("eval: 无权限使用该 Provider")
		}
		apiKey := ""
		if p.APIKeyEnc != "" {
			if dec, derr := crypto.Decrypt(p.APIKeyEnc, cfg.EncryptionKey); derr == nil {
				apiKey = dec
			}
		}
		return engine.ModelConfig{
			ModelID:  m.ModelID,
			BaseURL:  p.BaseURL,
			APIKey:   apiKey,
			Protocol: string(p.Protocol),
			Timeout:  cfg.EngineTimeout(),
		}, nil
	}
	evalRunner := eval.NewLLMRunner(evalModelResolver, cfg.EngineTimeout())
	evalJudge := eval.NewLLMJudge(evalModelResolver, cfg.EngineTimeout())
	evalSvc := eval.NewService(db.DB, evalModelResolver, evalRunner, evalJudge)
	api.SetEvalService(evalSvc)

	// 飞轮×回归联动（M5-08）：候选技能审批发布前，先登记进评估集、再跑回归自测，
	// 不过则回滚并拦截发布。regressionResolver 在 eval 模型解析基础上注入「被测技能名」
	// 作为 warm-start 关键词，使自测真正用到该技能。
	regressionResolver := func(ctx context.Context, userID uint, modelID, skillKeyword string) (engine.ModelConfig, error) {
		c, rerr := evalModelResolver(ctx, userID, modelID)
		if rerr != nil {
			return engine.ModelConfig{}, rerr
		}
		c.SkillWarmStart = cfg.SkillWarmStart()
		c.SkillRoots = []string{cfg.SkillsRoot(), filepath.Join(cfg.SkillsDataDir(), strconv.FormatUint(uint64(userID), 10))}
		if skillKeyword != "" {
			c.SkillKeywords = []string{skillKeyword}
		}
		return c, nil
	}
	regChecker := regression.NewEvalChecker(db.DB, regressionResolver, cfg.EngineTimeout(), 1.0)
	api.SetRegressionChecker(regChecker)

	// GEPA 反射式 Prompt 优化（M5-06）：复用 eval 的模型解析器 + 裁判，反射器经同一
	// 解析器调 LLM 产出改进指令；优化服务把改进后指令写回 AgentInstruction，生产对话
	// 经 engine.ModelConfig.InstructionOverride 生效。
	promptIterReflector := promptiter.NewEngineReflector(evalModelResolver, cfg.EngineTimeout())
	promptIterSvc := promptiter.NewService(db.DB, evalModelResolver, evalJudge, promptIterReflector)
	api.SetPromptIterService(promptIterSvc)

	r := buildRouter(db, cfg, discoverer, stateStore, enableState, taskRunController, taskRunSession, buildToolSearchProvider(db, cfg.EncryptionKey), gw)

	// 自主化 cron 调度器（M4-02）：常驻扫描启用的 cron 自动化，到点创建 Goal Session 跑 Loop。
	// 团队模式强制开启子代理 + 目标契约（Goal Session 语义），复用与对话端点一致的引擎构造。
	schedTeam := engine.TeamConfig{
		EnableSubAgents: true,
		EnableReviewer:  cfg.ReviewerEnabled(),
		MaxReviewRounds: cfg.MaxReviewRounds(),
		EnableGoal:      true,
		MaxGoalNudges:   cfg.MaxGoalNudges(),
		EnablePlan:      cfg.PlanEnabled(),
		MaxPlanNudges:   cfg.MaxPlanNudges(),
		Guardrail:       cfg.GuardrailConfig(), // M1-13：护栏熔断预算（无人值守必须有兜底）
	}
	// 两个 runner 共享同一 Gateway（同一会话串行锁），分别标记触发来源（cron / webhook）。
	cronRunner := api.NewAutomationLoopRunner(gw, schedTeam, api.ChannelCron)
	webhookRunner := api.NewAutomationLoopRunner(gw, schedTeam, api.ChannelWebhook)

	// 通知/结果回发出口（M4-07）：站内信落库 + 可选出站 webhook 回调（WEBHOOK_NOTIFY_URL 为空则跳过）。
	// 与 scheduler/webhook/recovery 三处自主入口解耦，便于单测注入 mock。
	notifier := notify.NewService(db.DB, cfg.WebhookNotifyURL(), log.Default())

	schedulerSvc := scheduler.New(db.DB, cronRunner)
	schedulerSvc.Notifier = notifier
	go schedulerSvc.Start(context.Background())

	// 跨天恢复（M4-05）：进程启动后扫描「未收敛 Goal Session」（automation_runs.status=running）
	// 并续跑——重发恢复提示 + 经 StateEnforcer 回灌 PLAN/PROGRESS/LEARNINGS，复用与 cron/webhook
	// 同一 Gateway（同一会话串行锁）以目标契约 TeamOverride 重建上下文续跑。与 M2-04 持久化
	// session 协同（历史消息与子任务 transcript 跨重启保留）。后台执行，不阻塞启动；
	// 恢复只针对「旧的中断会话」，不会与调度器新建的会话（新 session_key）冲突。
	api.SetRecoveryNotifier(notifier)
	go api.RecoverUnfinishedRuns(context.Background(), db.DB, gw, schedTeam, cfg.RecoveryMaxAttempts(), log.Default())

	// Webhook 外部事件入口（M4-03）：不挂鉴权中间件，完全靠 URL 中的 32B 令牌匹配
	// Automation；命中后异步启动 Goal Loop（与 cron 调度器共用同一 Gateway）。
	// 令牌校验 + 按 token 速率限制 + 防并发重入均在 handler 内完成。
	webhookLimiter := api.NewWebhookRateLimiter(cfg.WebhookRateLimit(), cfg.WebhookRateWindow())
	r.POST("/api/webhooks/:token", api.NewWebhookHandler(db.DB, webhookRunner, webhookLimiter).WithNotifier(notifier).Handle)

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down server...")
		// Close DB connection
		if sqlDB, err := db.DB.DB(); err == nil {
			sqlDB.Close()
		}
		os.Exit(0)
	}()

	log.Printf("Server starting on :%s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
