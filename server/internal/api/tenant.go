package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// tenantView 是租户的对外视图（含成员数）。
type tenantView struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedBy   uint   `json:"created_by"`
	MemberCount int64  `json:"member_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toTenantView(db *gorm.DB, t *model.Tenant) tenantView {
	cnt, _ := repo.CountTenantUsers(db, t.ID)
	return tenantView{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Status:      string(t.Status),
		CreatedBy:   t.CreatedBy,
		MemberCount: cnt,
		CreatedAt:   t.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:   t.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

// ListTenantsHandler handles GET /api/tenants (requires "tenants:read", M8-09).
// 返回全部租户（平台管理视图），供租户管理页渲染。
func ListTenantsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListTenants(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询租户失败"})
			return
		}
		out := make([]tenantView, 0, len(list))
		for i := range list {
			out = append(out, toTenantView(db, &list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"tenants": out, "total": len(out)})
	}
}

// CreateTenantHandler handles POST /api/tenants (requires "tenants:write", M8-09).
func CreateTenantHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req struct {
			Name        string `json:"name" binding:"required,max=128"`
			Description string `json:"description" binding:"max=512"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		t := &model.Tenant{
			Name:        req.Name,
			Description: req.Description,
			Status:      model.TenantStatusActive,
			CreatedBy:   uid,
		}
		if err := t.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.CreateTenant(db, t); err != nil {
			// 租户名唯一冲突。
			if errors.Is(err, gorm.ErrDuplicatedKey) || containsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "租户名已存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建租户失败"})
			return
		}
		c.JSON(http.StatusCreated, toTenantView(db, t))
	}
}

// GetTenantHandler handles GET /api/tenants/:id (requires "tenants:read", M8-09).
func GetTenantHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
			return
		}
		t, err := repo.GetTenant(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
			return
		}
		c.JSON(http.StatusOK, toTenantView(db, t))
	}
}

// UpdateTenantHandler handles PUT /api/tenants/:id (requires "tenants:write", M8-09).
// 支持改名 / 改描述 / 停用启用。
func UpdateTenantHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
			return
		}
		t, err := repo.GetTenant(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
			return
		}
		var req struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
			Status      *string `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Name != nil {
			t.Name = *req.Name
		}
		if req.Description != nil {
			t.Description = *req.Description
		}
		if req.Status != nil {
			switch model.TenantStatus(*req.Status) {
			case model.TenantStatusActive, model.TenantStatusDisabled:
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "status 仅支持 active / disabled"})
				return
			}
			t.Status = model.TenantStatus(*req.Status)
		}
		if err := t.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.UpdateTenant(db, t); err != nil {
			if containsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "租户名已存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新租户失败"})
			return
		}
		c.JSON(http.StatusOK, toTenantView(db, t))
	}
}

// DeleteTenantHandler handles DELETE /api/tenants/:id (requires "tenants:write", M8-09).
// 仅允许删除「无成员」的租户；有成员的租户需先迁移成员或禁用（防止误删数据归属）。
func DeleteTenantHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
			return
		}
		if err := repo.DeleteTenant(db, id); err != nil {
			if errors.Is(err, repo.ErrTenantNotEmpty) {
				c.JSON(http.StatusConflict, gin.H{"error": "租户内仍有成员，请先迁移成员或停用租户"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除租户失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// AddTenantMemberHandler handles POST /api/tenants/:id/members (requires "tenants:write", M8-09).
// body: {"user_id": N}。把用户移入租户（校验租户存在且启用）。
func AddTenantMemberHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
			return
		}
		var req struct {
			UserID uint `json:"user_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 目标用户存在性校验。
		if _, err := repo.GetUserByID(db, req.UserID); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err := repo.MoveUserToTenant(db, req.UserID, id); err != nil {
			if errors.Is(err, repo.ErrTenantNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
				return
			}
			if err.Error() == "tenant disabled" {
				c.JSON(http.StatusConflict, gin.H{"error": "租户已停用，无法加入成员"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加入租户失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// RemoveTenantMemberHandler handles DELETE /api/tenants/:id/members/:uid (requires "tenants:write", M8-09).
// 把用户移出租户（TenantID 置 NULL，恢复为独立用户）。
func RemoveTenantMemberHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if _, err := parseUintParam(c, "id"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
			return
		}
		uid, err := parseUintParam(c, "uid")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		if _, err := repo.GetUserByID(db, uid); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err := repo.MoveUserToTenant(db, uid, 0); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "移出租户失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// containsUniqueViolation 兜底识别 SQLite 原生唯一约束冲突文案
// （gorm.ErrDuplicatedKey 需开启 TranslateError，此处兼容两种情形）。
func containsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "duplicate key")
}
