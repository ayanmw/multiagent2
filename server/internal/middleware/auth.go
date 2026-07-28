package middleware

import (
	"net/http"
	"strings"

	"github.com/anmingwei/go-multi-agent-v2/internal/auth"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Context keys for values injected by AuthMiddleware.
const (
	CtxUserID   = "auth_user_id"
	CtxUserRole = "auth_user_role"
	CtxRoleID   = "auth_role_id"
	CtxUsername = "auth_username"
)

// AuthMiddleware validates the bearer JWT and injects the authenticated
// user's identity into the Gin context for downstream handlers.
//
// On failure it aborts with 401 and does NOT call c.Next().
func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid Authorization header"})
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := auth.ParseToken(jwtSecret, tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxUserRole, claims.Role)
		c.Next()
	}
}

// RequireRole aborts with 403 unless the authenticated user's role is in the
// allowed set. Must be chained after AuthMiddleware.
func RequireRole(allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleVal, ok := c.Get(CtxUserRole)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		role, _ := roleVal.(string)
		for _, a := range allowed {
			if role == a {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient role", "role": role})
	}
}

// RequirePermission aborts with 403 unless the authenticated user's role grants
// the (resource, action) permission. Wildcards "*" for either resource or
// action match anything. Must be chained after AuthMiddleware.
func RequirePermission(db *gorm.DB, resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleVal, ok := c.Get(CtxUserRole)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		role, _ := roleVal.(string)

		roleModel, err := repo.GetRoleByName(db, role)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "role not found"})
			return
		}
		perms, err := repo.GetPermissionsByRoleID(db, roleModel.ID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission lookup failed"})
			return
		}
		if hasPermission(perms, resource, action) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":    "permission denied",
			"resource": resource,
			"action":   action,
		})
	}
}

// hasPermission reports whether perms grants (resource, action), honoring
// the "*" wildcard for either dimension.
func hasPermission(perms []model.RolePermission, resource, action string) bool {
	for _, p := range perms {
		resourceOK := p.Resource == "*" || p.Resource == resource
		actionOK := p.Action == "*" || p.Action == action
		if resourceOK && actionOK {
			return true
		}
	}
	return false
}
