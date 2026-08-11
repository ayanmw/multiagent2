package api

import (
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// notificationView 是对外返回的精简视图。
type notificationView struct {
	ID        uint   `json:"id"`
	UserID    uint   `json:"user_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	RefKind   string `json:"ref_kind"`
	RefID     string `json:"ref_id"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

// ListNotificationsHandler 处理 GET /api/notifications（需 notifications:read，owner 隔离）。
// 返回当前用户的站内信列表（倒序，分页）+ 未读计数 + 元信息，供前端通知中心渲染红点。
func ListNotificationsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		limit, offset := repo.NormalizeAuditPageSize(atoiOrZero(c.Query("limit"))), atoiOrZero(c.Query("offset"))
		list, total, err := repo.ListNotifications(db, uid, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list notifications"})
			return
		}
		unread, uerr := repo.CountUnreadNotifications(db, uid)
		if uerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count unread"})
			return
		}
		views := make([]notificationView, 0, len(list))
		for i := range list {
			n := &list[i]
			views = append(views, notificationView{
				ID:        n.ID,
				UserID:    n.UserID,
				Type:      n.Type,
				Title:     n.Title,
				Message:   n.Message,
				RefKind:   n.RefKind,
				RefID:     n.RefID,
				Read:      n.ReadAt != nil,
				CreatedAt: n.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"notifications": views,
			"total":         total,
			"unread":        unread,
			"limit":         limit,
			"offset":        offset,
		})
	}
}

// MarkNotificationReadHandler 处理 POST /api/notifications/:id/read（需 notifications:write，owner 隔离）。
func MarkNotificationReadHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		if err := repo.MarkNotificationRead(db, uid, uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark read"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// MarkAllNotificationsReadHandler 处理 POST /api/notifications/read-all（需 notifications:write，owner 隔离）。
func MarkAllNotificationsReadHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		affected, err := repo.MarkAllNotificationsRead(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark all read"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "affected": affected})
	}
}
