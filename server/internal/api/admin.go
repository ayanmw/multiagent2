package api

import (
	"net/http"

	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
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
