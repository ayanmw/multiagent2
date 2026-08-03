package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/api"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// buildRouter 构造 Gin 路由（含全部 API 路由），抽出来便于在集成测试中进程内复用。
func buildRouter(db *repo.DB, cfg *config.Config, disc *provider.Discoverer, stateStore artifact.Store, enableState bool) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "go-multi-agent-v2",
		})
	})

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
		protected.GET("/mcp", middleware.RequirePermission(db.DB, "mcp", "read"), api.ListMCPServersHandler(db.DB))
		protected.POST("/mcp", middleware.RequirePermission(db.DB, "mcp", "write"), api.CreateMCPServerHandler(db.DB))
		protected.GET("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "read"), api.GetMCPServerHandler(db.DB))
		protected.PUT("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "write"), api.UpdateMCPServerHandler(db.DB))
		protected.DELETE("/mcp/:id", middleware.RequirePermission(db.DB, "mcp", "write"), api.DeleteMCPServerHandler(db.DB))

		// Skills 技能仓库（M2-03）：用户归属的技能管理（文件系统后端，owner 隔离）。
		// 读操作需 skills:read，写操作（建/更新/删私有技能）需 skills:write（RBAC）。
		// 共享技能（仓库 skills/ 目录）对所有用户可见但只读，不可经 API 改写。
		protected.GET("/skills", middleware.RequirePermission(db.DB, "skills", "read"), api.ListSkillsHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.POST("/skills", middleware.RequirePermission(db.DB, "skills", "write"), api.CreateSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.GET("/skills/:name", middleware.RequirePermission(db.DB, "skills", "read"), api.GetSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.PUT("/skills/:name", middleware.RequirePermission(db.DB, "skills", "write"), api.UpdateSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))
		protected.DELETE("/skills/:name", middleware.RequirePermission(db.DB, "skills", "write"), api.DeleteSkillHandler(cfg.SkillsRoot(), cfg.SkillsDataDir()))

		// Agent 对话（引擎封装 trpc-agent-go，连接已启用 Model+Provider）
		// M1-08：AGENT_MODE=team 时根 Agent 换成 Orchestrator，代码落地委托 Coder 子代理。
		// M1-09：team 模式默认再加入只读 Reviewer（TEAM_REVIEWER），形成「实现→审阅→修复」回环。
		// M1-11：team 模式默认启用目标契约（GOAL_CONTRACT），Orchestrator 必须把目标
		// 推进到 complete/blocked 才允许结束，过早的最终答复会被拦截并要求继续干活。
		// M1-12：team 模式默认启用 Plan-Execute 循环（PLAN_EXECUTE），Orchestrator 必须
		// 先建计划、逐项执行完毕才允许结束；二者叠加在 Orchestrator 上。
		// M2-03：skillWarmStart 等参数驱动「技能 warm-start」，会话开始把相关 SKILL.md
		// 注入根 Agent 系统上下文（长度受 SkillWarmStartMaxChars 上限控制）。
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
		protected.POST("/chat", api.ChatHandler(db.DB, cfg.EncryptionKey, cfg.EngineTimeout(), cfg.WorkspaceRoot, teamCfg, stateStore, enableState, cfg.SkillsRoot(), cfg.SkillsDataDir(), cfg.SkillWarmStart(), cfg.SkillWarmStartMaxChars()))

		// AG-UI SSE 流式对话端点（M0-11）：事件流转 AG-UI 协议，Session 持久化
		// M0.5-06：message 改由 POST body 传递（避免明文进访问日志），故注册为 POST。
		// M1-06/07：在此端点装配 CodeAct 工具；工作目录优先取对话绑定的 workspace 目录，
		// 未绑定时回退 WorkspaceRoot/<uid>。workspace_key 经请求体传入。
		// M1-16：stateStore/enableState 驱动「工作状态外置」，使长任务中断后续跑能接上。
		// M2-03：skillWarmStart 等参数驱动「技能 warm-start」注入系统上下文。
		protected.POST("/chat/:session_id/stream", api.StreamChatHandler(db.DB, cfg.EncryptionKey, cfg.EngineTimeout(), cfg.WorkspaceRoot, teamCfg, stateStore, enableState, cfg.SkillsRoot(), cfg.SkillsDataDir(), cfg.SkillWarmStart(), cfg.SkillWarmStartMaxChars()))
	}

	return r
}

func main() {
	// Load configuration
	cfg := config.Load()

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

	r := buildRouter(db, cfg, discoverer, stateStore, enableState)

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
