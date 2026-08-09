package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// auditScopeAll / auditScopeSelf 标识本次查询的可见范围，供前端提示与筛选器渲染。
const (
	auditScopeAll  = "all"
	auditScopeSelf = "self"
)

// auditTimeLayouts 是 start/end 查询参数支持的时间格式（按顺序尝试解析）。
// 兼容前端 NDatePicker 的时间戳（毫秒）与常见的 RFC3339 / 日期字符串。
var auditTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// parseAuditTime 解析审计时间筛选参数：支持毫秒时间戳与多种日期时间字符串。
// 空串返回零值（表示不限）；无法识别时返回 ok=false 由调用方回 400。
func parseAuditTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	// 纯数字按毫秒时间戳处理（前端 NDatePicker 直接给毫秒值）。
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(ms), true
	}
	for _, layout := range auditTimeLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// validAuditDecision 校验 decision 过滤值是否为已知策略判定。
func validAuditDecision(d string) bool {
	switch d {
	case executor.DecisionAllow.String(), executor.DecisionAsk.String(), executor.DecisionDeny.String():
		return true
	default:
		return false
	}
}

// auditCanSeeAll 判断角色是否具备查看全员审计日志的资格（admin / developer）。
func auditCanSeeAll(c *gin.Context) bool {
	role, _ := c.Get(middleware.CtxUserRole)
	roleStr, _ := role.(string)
	return roleStr == model.RoleAdmin || roleStr == model.RoleDeveloper
}

// ListAuditLogsHandler handles GET /api/audit (requires "audit:read", M3-01/M3-02).
//
// 审计日志 owner 隔离规则：
//   - admin / developer：默认查看全员日志，可用 user_id 参数收敛到某个用户；
//   - viewer：强制仅查看本人日志（忽略 user_id 参数，不可越权）。
//
// 支持可选查询参数做服务端过滤与分页：
//   - user_id：按归属用户过滤（仅 admin/developer 生效）
//   - decision：allow / deny / ask（命中策略的初步判定，非法值 400）
//   - command：命令关键词模糊匹配
//   - start / end：时间范围（毫秒时间戳或 RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"）
//   - limit / offset：分页（缺省 limit=50，上限 200）
//
// 返回体：{ audit_logs, total, limit, offset, scope }，scope 为 all/self 供前端渲染筛选器。
func ListAuditLogsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		filter := repo.AuditLogFilter{
			Decision: strings.TrimSpace(c.Query("decision")),
			Command:  strings.TrimSpace(c.Query("command")),
		}
		if filter.Decision != "" && !validAuditDecision(filter.Decision) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "decision 仅支持 allow/ask/deny"})
			return
		}

		// 可见范围：admin/developer 看全员（可按 user_id 收敛），其余角色强制只看自己。
		scope := auditScopeSelf
		if auditCanSeeAll(c) {
			scope = auditScopeAll
			if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
				n, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "user_id 必须为正整数"})
					return
				}
				filter.UserID = uint(n)
			}
		} else {
			// 非 admin/developer 一律仅看本人（owner 隔离兜底，忽略传入的 user_id）。
			filter.UserID = uid
		}

		start, okStart := parseAuditTime(c.Query("start"))
		if !okStart {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start 时间格式非法"})
			return
		}
		end, okEnd := parseAuditTime(c.Query("end"))
		if !okEnd {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end 时间格式非法"})
			return
		}
		if !start.IsZero() && !end.IsZero() && end.Before(start) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end 不能早于 start"})
			return
		}
		filter.Start, filter.End = start, end

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
		if list == nil {
			list = []model.AuditLog{}
		}
		c.JSON(http.StatusOK, gin.H{
			"audit_logs": list,
			"total":      total,
			"limit":      repo.NormalizeAuditPageSize(filter.Limit),
			"offset":     filter.Offset,
			"scope":      scope,
		})
	}
}
