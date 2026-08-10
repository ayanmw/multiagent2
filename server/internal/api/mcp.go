package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// mcpServerRequest 是创建/更新的请求体。transport 用 oneof 做基础校验，
// 跨字段必填（stdio→command / sse|streamable→url）在 handler 内用
// model.MCPServer.Validate 兜底（gin 的 oneof 只作用于单字段）。
type mcpServerRequest struct {
	Name        string            `json:"name" binding:"required,max=128"`
	Transport   string            `json:"transport" binding:"required,oneof=stdio sse streamable"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Enabled     *bool             `json:"enabled"`
	Description string            `json:"description" binding:"max=512"`
}

// mcpServerUpdateRequest 支持部分更新（仅传需要改的字段）。
type mcpServerUpdateRequest struct {
	Name        *string            `json:"name"`
	Transport   *string            `json:"transport"`
	Command     *string            `json:"command"`
	Args        *[]string          `json:"args"`
	Env         *map[string]string `json:"env"`
	URL         *string            `json:"url"`
	Headers     *map[string]string `json:"headers"`
	Enabled     *bool              `json:"enabled"`
	Description *string            `json:"description"`
}

// mcpServerView 是对外返回的精简视图。
//
// M3-07：env/headers 属敏感字段（常含 token），一律**不回显明文**，只暴露
// 「有没有配」与「配了哪些 key」，与 Provider 的 has_api_key 语义对齐。
// 前端编辑时留空即代表不修改，需要改再整份提交新值。
type mcpServerView struct {
	ID          uint     `json:"id"`
	UserID      uint     `json:"user_id"`
	Name        string   `json:"name"`
	Transport   string   `json:"transport"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	HasEnv      bool     `json:"has_env"`
	EnvKeys     []string `json:"env_keys"`
	URL         string   `json:"url"`
	HasHeaders  bool     `json:"has_headers"`
	HeaderKeys  []string `json:"header_keys"`
	Enabled     bool     `json:"enabled"`
	Description string   `json:"description"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

func toMCPServerView(m *model.MCPServer) mcpServerView {
	return mcpServerView{
		ID:          m.ID,
		UserID:      m.UserID,
		Name:        m.Name,
		Transport:   string(m.Transport),
		Command:     m.Command,
		Args:        m.Args,
		HasEnv:      m.HasEnv(),
		EnvKeys:     m.EnvKeys(),
		URL:         m.URL,
		HasHeaders:  m.HasHeaders(),
		HeaderKeys:  m.HeaderKeys(),
		Enabled:     m.Enabled,
		Description: m.Description,
		CreatedAt:   m.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   m.UpdatedAt.Format(time.RFC3339),
	}
}

// CreateMCPServerHandler 处理 POST /api/mcp（需 mcp:write）。
// 仅做管理面：持久化配置 + 校验，不在此装载 MCP 工具。
// encKey 为 AES-256 主密钥，env/headers 经 repo 层加密后落库（M3-07）。
func CreateMCPServerHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req mcpServerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		transport, ok := model.ParseMCPTransport(req.Transport)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid transport (must be stdio/sse/streamable)"})
			return
		}
		m := &model.MCPServer{
			UserID:      uid,
			Name:        req.Name,
			Transport:   transport,
			Command:     req.Command,
			Args:        req.Args,
			Env:         req.Env,
			URL:         req.URL,
			Headers:     req.Headers,
			Description: req.Description,
		}
		if req.Enabled != nil {
			m.Enabled = *req.Enabled
		}
		if err := m.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 同名冲突检测（uniqueIndex idx_user_mcp 的友好前置校验）。
		if _, derr := repo.GetMCPServerByName(db, uid, m.Name, encKey); derr == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "mcp server name already exists"})
			return
		} else if derr != repo.ErrMCPServerNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check mcp server name"})
			return
		}
		if err := repo.CreateMCPServer(db, m, encKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create mcp server"})
			return
		}
		c.JSON(http.StatusCreated, toMCPServerView(m))
	}
}

// ListMCPServersHandler 处理 GET /api/mcp（需 mcp:read），返回当前用户的全部配置。
func ListMCPServersHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListMCPServers(db, uid, encKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list mcp servers"})
			return
		}
		views := make([]mcpServerView, 0, len(list))
		for i := range list {
			views = append(views, toMCPServerView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"mcp_servers": views, "total": len(views)})
	}
}

// GetMCPServerHandler 处理 GET /api/mcp/:id（owner-scoped）。
func GetMCPServerHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		m, ok2 := lookupOwnedMCPServer(c, db, uid, encKey)
		if !ok2 {
			return
		}
		c.JSON(http.StatusOK, toMCPServerView(m))
	}
}

// UpdateMCPServerHandler 处理 PUT /api/mcp/:id（需 mcp:write，owner-scoped，部分更新）。
func UpdateMCPServerHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		m, ok2 := lookupOwnedMCPServer(c, db, uid, encKey)
		if !ok2 {
			return
		}
		var req mcpServerUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Name != nil {
			m.Name = *req.Name
		}
		if req.Transport != nil {
			transport, ok := model.ParseMCPTransport(*req.Transport)
			if !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid transport (must be stdio/sse/streamable)"})
				return
			}
			m.Transport = transport
		}
		if req.Command != nil {
			m.Command = *req.Command
		}
		if req.Args != nil {
			m.Args = *req.Args
		}
		// env/headers 未传即保持原值（repo 读路径已解密回填，重新 Seal 后密文等价）；
		// 传空 map 则视为「清空该字段」。
		if req.Env != nil {
			m.Env = *req.Env
		}
		if req.URL != nil {
			m.URL = *req.URL
		}
		if req.Headers != nil {
			m.Headers = *req.Headers
		}
		if req.Enabled != nil {
			m.Enabled = *req.Enabled
		}
		if req.Description != nil {
			m.Description = *req.Description
		}
		// 重新校验整体自洽性（transport 变更后其必填字段可能变化）。
		if err := m.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.UpdateMCPServer(db, m, encKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update mcp server"})
			return
		}
		c.JSON(http.StatusOK, toMCPServerView(m))
	}
}

// DeleteMCPServerHandler 处理 DELETE /api/mcp/:id（需 mcp:write，owner-scoped）。
func DeleteMCPServerHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		m, ok2 := lookupOwnedMCPServer(c, db, uid, encKey)
		if !ok2 {
			return
		}
		if err := repo.DeleteMCPServer(db, m.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete mcp server"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// lookupOwnedMCPServer 解析 :id、加载并校验归属；失败已写响应，返回 (nil,false)。
func lookupOwnedMCPServer(c *gin.Context, db *gorm.DB, uid uint, encKey []byte) (*model.MCPServer, bool) {
	idParam := c.Param("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return nil, false
	}
	m, err := repo.GetMCPServerByID(db, uid, uint(id), encKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
		return nil, false
	}
	return m, true
}
