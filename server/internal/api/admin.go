package api

import (
	"net/http"

	"github.com/ayanmw/multiagent2/server/internal/auth"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListRolesHandler handles GET /api/admin/roles (admin only).
// It returns all roles with their assigned permissions.
func ListRolesHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		roles, err := repo.ListRoles(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list roles"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"roles": roles})
	}
}

// adminUserView 是用户管理视图的字段（含角色、状态、配额）。
type adminUserView struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleID      uint   `json:"role_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	Quota       *adminQuotaView `json:"quota,omitempty"`
}

// adminQuotaView 展示作用于该用户的最具体预算策略（M3-04 配额护栏）。
type adminQuotaView struct {
	MaxTokens int64  `json:"max_tokens"`
	Window    string `json:"window"`
	IsGlobal  bool   `json:"is_global"` // true=回落全局默认，false=用户特定策略
}

// toAdminUserView 把 model.User 转为管理视图，并附带其配额（预算）信息。
func toAdminUserView(db *gorm.DB, u *model.User) adminUserView {
	role := ""
	if u.Role.Name != "" {
		role = u.Role.Name
	}
	v := adminUserView{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		RoleID:      u.RoleID,
		Role:        role,
		Status:      string(u.Status),
		CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	// 配额：查作用于该用户的最具体预算策略（用户特定 → 全局默认），无则留空。
	if db != nil {
		if p, err := repo.GetEffectiveUserBudgetPolicy(db, u.ID); err == nil && p != nil {
			v.Quota = &adminQuotaView{
				MaxTokens: p.MaxTokens,
				Window:    p.Window,
				IsGlobal:  p.ScopeKey == "",
			}
		}
	}
	return v
}

// ListUsersHandler handles GET /api/admin/users (admin only).
// Returns all users with role, status and quota information.
func ListUsersHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := repo.ListUsers(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
			return
		}
		out := make([]adminUserView, 0, len(users))
		for i := range users {
			out = append(out, toAdminUserView(db, &users[i]))
		}
		c.JSON(http.StatusOK, gin.H{"users": out, "total": len(out)})
	}
}

// adminCreateUserRequest is the payload for POST /api/admin/users.
type adminCreateUserRequest struct {
	Username    string `json:"username" binding:"required,min=3,max=64"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"` // admin / developer / viewer；缺省 developer
}

// CreateUserHandler handles POST /api/admin/users (admin only).
// Creates a new user with the given role (defaults to developer) and active status.
func CreateUserHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req adminCreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 角色解析：缺省 developer，非法角色拒绝。
		roleName := req.Role
		if roleName == "" {
			roleName = model.RoleDeveloper
		}
		switch roleName {
		case model.RoleAdmin, model.RoleDeveloper, model.RoleViewer:
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "role 仅支持 admin / developer / viewer"})
			return
		}
		roleID, err := repo.GetRoleIDByName(db, roleName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "角色表未初始化"})
			return
		}

		if _, err := repo.GetUserByUsername(db, req.Username); err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
			return
		}
		if _, err := repo.GetUserByEmail(db, req.Email); err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}

		displayName := req.DisplayName
		if displayName == "" {
			displayName = req.Username
		}

		user := &model.User{
			Username:     req.Username,
			Email:        req.Email,
			PasswordHash: hash,
			DisplayName:  displayName,
			RoleID:       roleID,
			Status:       model.UserStatusActive,
		}
		if err := repo.CreateUser(db, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
			return
		}
		created, err := repo.GetUserByID(db, user.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load user"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"user": toAdminUserView(db, created)})
	}
}

// GetUserHandler handles GET /api/admin/users/:id (admin only).
func GetUserHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		user, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, user)})
	}
}

// adminUpdateUserRequest is the payload for PUT /api/admin/users/:id.
type adminUpdateUserRequest struct {
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"` // active / disabled
}

// UpdateUserHandler handles PUT /api/admin/users/:id (admin only).
// Updates display name, role, and/or status. Self-demotion/self-disable is blocked
// to avoid an admin locking themselves out.
func UpdateUserHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		var req adminUpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		user, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}

		// 当前操作者（admin）身份。
		currentUID, _ := c.Get(middleware.CtxUserID)
		isSelf := false
		if u, ok := currentUID.(uint); ok && u == id {
			isSelf = true
		}

		// 角色变更。
		if req.Role != "" {
			switch req.Role {
			case model.RoleAdmin, model.RoleDeveloper, model.RoleViewer:
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "role 仅支持 admin / developer / viewer"})
				return
			}
			if isSelf && req.Role != model.RoleAdmin {
				c.JSON(http.StatusBadRequest, gin.H{"error": "不能修改自己的管理员角色"})
				return
			}
			roleID, rerr := repo.GetRoleIDByName(db, req.Role)
			if rerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "角色表未初始化"})
				return
			}
			user.RoleID = roleID
		}

		// 状态变更。
		if req.Status != "" {
			switch model.UserStatus(req.Status) {
			case model.UserStatusActive, model.UserStatusDisabled:
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "status 仅支持 active / disabled"})
				return
			}
			if isSelf && req.Status == string(model.UserStatusDisabled) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "不能禁用自己的账号"})
				return
			}
			user.Status = model.UserStatus(req.Status)
		}

		if req.DisplayName != "" {
			user.DisplayName = req.DisplayName
		}

		if err := repo.UpdateUser(db, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
		updated, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, updated)})
	}
}

// DisableUserHandler handles POST /api/admin/users/:id/disable (admin only).
// Prevents disabling the last active admin and self-disable.
func DisableUserHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		user, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if user.Status == model.UserStatusDisabled {
			c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, user)})
			return
		}
		// 防自锁。
		if uid, ok := c.Get(middleware.CtxUserID); ok {
			if u, ok := uid.(uint); ok && u == id {
				c.JSON(http.StatusBadRequest, gin.H{"error": "不能禁用自己的账号"})
				return
			}
		}
		// 防锁死：最后一个活跃管理员不可被禁用。
		if user.Role.Name == model.RoleAdmin {
			cnt, cerr := repo.CountAdmins(db)
			if cerr == nil && cnt <= 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "至少需保留一名活跃管理员"})
				return
			}
		}
		user.Status = model.UserStatusDisabled
		if err := repo.UpdateUser(db, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disable user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, user)})
	}
}

// EnableUserHandler handles POST /api/admin/users/:id/enable (admin only).
func EnableUserHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		user, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if user.Status == model.UserStatusActive {
			c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, user)})
			return
		}
		user.Status = model.UserStatusActive
		if err := repo.UpdateUser(db, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enable user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": toAdminUserView(db, user)})
	}
}

// adminResetPasswordRequest is the payload for POST /api/admin/users/:id/reset-password.
type adminResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=6"`
}

// ResetPasswordHandler handles POST /api/admin/users/:id/reset-password (admin only).
// Admin sets a new password for the user; the plaintext is never returned.
func ResetPasswordHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		var req adminResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		user, err := repo.GetUserByID(db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		user.PasswordHash = hash
		if err := repo.UpdateUser(db, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reset password"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
