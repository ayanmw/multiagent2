package api

import (
	"net/http"

	"github.com/anmingwei/go-multi-agent-v2/internal/auth"
	"github.com/anmingwei/go-multi-agent-v2/internal/middleware"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type registerRequest struct {
	Username    string `json:"username" binding:"required,min=3,max=64"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	// Account accepts either username or email.
	Account  string `json:"account" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type userView struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleID      uint   `json:"role_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

type authResponse struct {
	Token string   `json:"token"`
	User  userView `json:"user"`
}

func toUserView(u *model.User) userView {
	role := ""
	if u.Role.Name != "" {
		role = u.Role.Name
	}
	return userView{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		RoleID:      u.RoleID,
		Role:        role,
		Status:      string(u.Status),
	}
}

// RegisterHandler handles POST /api/auth/register.
func RegisterHandler(jwtSecret string, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

		user := &model.User{
			Username:     req.Username,
			Email:        req.Email,
			PasswordHash: hash,
			DisplayName:  req.DisplayName,
			Status:       model.UserStatusActive,
		}

		// Default new users to the "developer" role; fall back to viewer (id 3)
		// as the least-privilege default if the role is missing for any reason.
		user.RoleID = 3
		if role, err := repo.GetRoleByName(db, model.RoleDeveloper); err == nil {
			user.RoleID = role.ID
		}
		if user.DisplayName == "" {
			user.DisplayName = req.Username
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

		token, err := auth.GenerateToken(jwtSecret, created.ID, created.Role.Name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
			return
		}

		c.JSON(http.StatusCreated, authResponse{Token: token, User: toUserView(created)})
	}
}

// LoginHandler handles POST /api/auth/login.
func LoginHandler(jwtSecret string, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		user, err := repo.GetUserByUsername(db, req.Account)
		if err != nil {
			user, err = repo.GetUserByEmail(db, req.Account)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
				return
			}
		}

		if !auth.CheckPassword(user.PasswordHash, req.Password) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		if user.Status != model.UserStatusActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
			return
		}

		token, err := auth.GenerateToken(jwtSecret, user.ID, user.Role.Name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
			return
		}

		c.JSON(http.StatusOK, authResponse{Token: token, User: toUserView(user)})
	}
}

// MeHandler handles GET /api/me (protected). It reads the identity injected
// by AuthMiddleware, so it works for both JWT and X-API-Key authentication.
func MeHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDVal, ok := c.Get(middleware.CtxUserID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		userID, _ := userIDVal.(uint)

		user, err := repo.GetUserByID(db, userID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"user": toUserView(user)})
	}
}
