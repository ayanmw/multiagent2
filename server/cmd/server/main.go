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

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize database
	db, err := repo.NewDB(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	_ = db // will be injected into handlers in later tasks

	// Setup Gin router
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Model auto-discovery (provider /v1/models, cached 5 minutes).
	discoverer := provider.NewDiscoverer(cfg.EncryptionKey, 5*time.Minute)

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

		// API key management (owner-scoped)
		protected.POST("/auth/apikeys", api.CreateAPIKeyHandler(db.DB))
		protected.GET("/auth/apikeys", api.ListAPIKeysHandler(db.DB))
		protected.DELETE("/auth/apikeys/:id", api.RevokeAPIKeyHandler(db.DB))

		// Admin-only sub-group
		admin := protected.Group("/admin")
		admin.Use(middleware.RequireRole(model.RoleAdmin))
		{
			admin.GET("/roles", api.ListRolesHandler(db.DB))
		}

		// Provider management (user-scoped CRUD; API key encrypted at rest)
		protected.GET("/providers", api.ListProvidersHandler(db.DB))
		protected.POST("/providers", api.CreateProviderHandler(db.DB, cfg.EncryptionKey))
		protected.GET("/providers/:id", api.GetProviderHandler(db.DB))
		protected.PUT("/providers/:id", api.UpdateProviderHandler(db.DB, cfg.EncryptionKey))
		protected.DELETE("/providers/:id", api.DeleteProviderHandler(db.DB))
		protected.GET("/providers/:id/models", api.ListProviderModelsHandler(db.DB, discoverer))
		protected.POST("/providers/:id/models/sync", api.SyncProviderModelsHandler(db.DB, discoverer))
		protected.GET("/providers/:id/models/managed", api.ListManagedModelsHandler(db.DB))
		protected.PUT("/providers/:id/models/:mid", api.UpdateModelHandler(db.DB))

		// Managed model catalog (Agent may only select enabled models)
		protected.GET("/models", api.ListEnabledModelsHandler(db.DB))

		// Agent 对话（引擎封装 trpc-agent-go，连接已启用 Model+Provider）
		protected.POST("/chat", api.ChatHandler(db.DB, cfg.EncryptionKey))
	}

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
