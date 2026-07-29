package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/api"
	"github.com/anmingwei/go-multi-agent-v2/internal/config"
	"github.com/anmingwei/go-multi-agent-v2/internal/middleware"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/provider"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
)

// buildRouter 构造 Gin 路由（含全部 API 路由），抽出来便于在集成测试中进程内复用。
func buildRouter(db *repo.DB, cfg *config.Config, disc *provider.Discoverer) *gin.Engine {
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

		// Agent 对话（引擎封装 trpc-agent-go，连接已启用 Model+Provider）
		protected.POST("/chat", api.ChatHandler(db.DB, cfg.EncryptionKey, cfg.EngineTimeout()))

		// AG-UI SSE 流式对话端点（M0-11）：事件流转 AG-UI 协议，Session 持久化
		// M0.5-06：message 改由 POST body 传递（避免明文进访问日志），故注册为 POST。
		protected.POST("/chat/:session_id/stream", api.StreamChatHandler(db.DB, cfg.EncryptionKey, cfg.EngineTimeout()))
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

	r := buildRouter(db, cfg, discoverer)

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
