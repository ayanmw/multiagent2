package api

import (
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListAuditLogsHandler handles GET /api/audit (requires "audit:read", M3-01).
//
// 审计日志 owner 隔离规则：
//   - admin / developer：查看全员日志（按 UserID=0 不过滤归属）；
//   - viewer：仅查看本人（UserID=当前 uid）名下的日志。
//
// 支持可选查询参数做服务端过滤与分页：
//   - decision：allow / deny / ask（命中策略的初步判定）
//   - command：命令关键词模糊匹配
//   - limit / offset：分页（缺省 limit=50）
//
// 返回体：{ audit_logs: [...], total: <int64> }。
func ListAuditLogsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		// 解析角色决定可见范围：developer/admin 看全员，viewer 只看自己。
		role, _ := c.Get(middleware.CtxUserRole)
		roleStr, _ := role.(string)
		filter := repo.AuditLogFilter{
			Decision: c.Query("decision"),
			Command:  c.Query("command"),
		}
		if roleStr != model.RoleAdmin && roleStr != model.RoleDeveloper {
			// 非 admin/developer 一律仅看本人（owner 隔离兜底）。
			filter.UserID = uid
		}
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				filter.Limit = n
			}
		}
		if o := c.Query("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil {
				filter.Offset = n
			}
		}

		list, total, err := repo.ListAuditLogs(db, filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询审计日志失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"audit_logs": list,
			"total":      total,
		})
	}
}
