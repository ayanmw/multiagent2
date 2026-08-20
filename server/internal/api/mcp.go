package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/ayanmw/multiagent2/server/internal/toolsearch"
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
		} else {
			// 缺省启用（对齐 DB default:true 的既有语义；显式化以配合 repo 层
			// GORM 零值 bool 校正——不显式给 true 会被校正成不启用）。
			m.Enabled = true
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

// mcpTestTimeout 是「测试连接」端点的整体超时，需高于 toolsearch 内部单次
// MCP 连接超时（30s），避免请求半途 ctx 先到期把真实连接错误掩盖成超时。
const mcpTestTimeout = 45 * time.Second

// TestMCPServerHandler 处理 POST /api/mcp/:id/test（需 mcp:read）。
//
// 实际调用 toolsearch.LoadMCPServerTools 连接并预取工具列表，验证配置可用——
// 这正是 MX-02「前端深度打通-MCP」所需的后端能力：配可用 MCP → 点测试 → 返回工具列表；
// 配错 → 明确报错。连接/初始化失败属于「配置错误」而非服务端故障，故以 200 + ok:false
// 返回明确错误文案，前端据此展示；只有 DB/鉴权等服务端故障才返回 5xx。
//
// 仅做连接探测，测完立即 box.Close() 释放 MCP 会话连接，不把工具挂载进任何 Agent。
func TestMCPServerHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
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
		ctx, cancel := context.WithTimeout(c.Request.Context(), mcpTestTimeout)
		defer cancel()
		box, err := toolsearch.LoadMCPServerTools(ctx, *m)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":         false,
				"transport":  string(m.Transport),
				"error":      err.Error(),
			})
			return
		}
		defer box.Close()
		entries := box.List()
		tools := make([]gin.H, 0, len(entries))
		for _, e := range entries {
			tools = append(tools, gin.H{"name": e.Name, "description": e.Description})
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":         true,
			"transport":  string(m.Transport),
			"count":      len(tools),
			"tools":      tools,
		})
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
