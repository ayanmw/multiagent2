package api

import (
	"net/http"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/marketplace"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 连接器市场（M8-08）：预置 MCP 服务器模板，一键导入为自己的 MCP 配置。
//
// 路由：
//   - GET  /api/mcp/templates            列表（需 mcp:read）
//   - POST /api/mcp/templates/:id/import 按模板创建配置（需 mcp:write）
//
// 模板是纯数据（internal/marketplace），不落库、不发起网络请求；导入时
// 渲染成 model.MCPServer 后走与手动创建完全相同的 repo 路径（env/headers
// AES-256-GCM 加密落库、同名冲突 409、Validate 校验）。

// mcpTemplateView 是对外模板视图：只给配置骨架与所需密钥提示，不给模板
// env/headers 具体值（与 M3-07 掩码语义一致——模板中可能含默认 header 模板）。
type mcpTemplateView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
	Transport      string   `json:"transport"`
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	URL            string   `json:"url"`
	SecretFields   []string `json:"secret_fields"`
	DefaultName    string   `json:"default_name"`
	DefaultEnabled bool     `json:"default_enabled"`
}

func toMCPTemplateView(t marketplace.Template) mcpTemplateView {
	return mcpTemplateView{
		ID:             t.ID,
		Name:           t.Name,
		Category:       t.Category,
		Description:    t.Description,
		Transport:      string(t.Transport),
		Command:        t.Command,
		Args:           t.Args,
		URL:            t.URL,
		SecretFields:   t.SecretFields,
		DefaultName:    t.DefaultName,
		DefaultEnabled: t.DefaultEnabled,
	}
}

// ListMCPTemplatesHandler 处理 GET /api/mcp/templates（需 mcp:read）。
func ListMCPTemplatesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		views := make([]mcpTemplateView, 0, len(marketplace.Templates))
		for _, t := range marketplace.Templates {
			views = append(views, toMCPTemplateView(t))
		}
		c.JSON(http.StatusOK, gin.H{"templates": views, "total": len(views)})
	}
}

// importMCPTemplateRequest 是模板导入请求体。name 不传用模板默认名；
// env/headers 提供占位符实际值（键名即模板 SecretFields），未提供则保留占位符。
type importMCPTemplateRequest struct {
	Name        *string           `json:"name"`
	Enabled     *bool             `json:"enabled"`
	Description *string           `json:"description"`
	Env         map[string]string `json:"env"`
	Headers     map[string]string `json:"headers"`
}

// ImportMCPTemplateHandler 处理 POST /api/mcp/templates/:id/import（需 mcp:write）。
// 一键导入 = 模板渲染 + 同名冲突检测 + 加密落库，成功后返回与手动创建一致的视图。
func ImportMCPTemplateHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		tmpl, found := marketplace.GetTemplate(c.Param("id"))
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}
		var req importMCPTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		m := tmpl.Render(marketplace.RenderOptions{Env: req.Env, Headers: req.Headers})
		m.UserID = uid
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			m.Name = *req.Name
		}
		if req.Enabled != nil {
			m.Enabled = *req.Enabled
		}
		if req.Description != nil {
			m.Description = *req.Description
		}
		if err := m.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 同名冲突检测（uniqueIndex idx_user_mcp 的友好前置校验，与手动创建一致）。
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
