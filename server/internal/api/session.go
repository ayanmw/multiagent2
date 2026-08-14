package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// sessionView 是会话对外返回的精简视图（列表用，不含历史消息）。
type sessionView struct {
	ID           uint    `json:"id"`
	UserID       uint    `json:"user_id"`
	SessionKey   string  `json:"session_key"`
	Title        string  `json:"title"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	WorkspaceKey *string `json:"workspace_key"` // 绑定的 workspace key（MX-01）；nil 表示使用默认目录
}

// messageView 是会话中单条消息的视图。
type messageView struct {
	ID        uint   `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// sessionDetailView 是带历史消息的会话详情（GET /api/sessions/:id 返回）。
type sessionDetailView struct {
	ID           uint          `json:"id"`
	UserID       uint          `json:"user_id"`
	SessionKey   string        `json:"session_key"`
	Title        string        `json:"title"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
	WorkspaceKey *string       `json:"workspace_key"` // 绑定的 workspace key（MX-01）；nil 表示默认目录
	Messages     []messageView `json:"messages"`
}

// sessionWorkspaceKey 返回会话当前绑定的 workspace_key（无绑定返回 nil）。
func sessionWorkspaceKey(db *gorm.DB, uid uint, sess *model.Session) *string {
	if sess.WorkspaceID == nil || *sess.WorkspaceID == 0 {
		return nil
	}
	ws, err := repo.GetWorkspaceByID(db, uid, *sess.WorkspaceID)
	if err != nil {
		return nil
	}
	k := ws.Key
	return &k
}

// toSessionView 将领域模型转为列表视图。
func toSessionView(db *gorm.DB, uid uint, s *model.Session) sessionView {
	return sessionView{
		ID:           s.ID,
		UserID:       s.UserID,
		SessionKey:   s.SessionKey,
		Title:        s.Title,
		CreatedAt:    s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    s.UpdatedAt.Format(time.RFC3339),
		WorkspaceKey: sessionWorkspaceKey(db, uid, s),
	}
}

// CreateSessionHandler handles POST /api/sessions. It creates a brand-new
// session owned by the current user and returns its public session_key.
func CreateSessionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		// 标题可选；空 body 也允许（ShouldBindJSON 失败时按默认标题处理）。
		var req struct {
			Title string `json:"title"`
		}
		_ = c.ShouldBindJSON(&req)
		title := req.Title
		if title == "" {
			title = "新对话"
		}

		s := &model.Session{
			UserID:     uid,
			SessionKey: repo.NewSessionKey(),
			Title:      title,
		}
		if err := db.Create(s).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
			return
		}
		c.JSON(http.StatusCreated, toSessionView(db, uid, s))
	}
}

// ListSessionsHandler handles GET /api/sessions. It returns all sessions of the
// current user, most-recently-active first.
func ListSessionsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		sessions, err := repo.ListSessions(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
			return
		}
		views := make([]sessionView, 0, len(sessions))
		for i := range sessions {
			views = append(views, toSessionView(db, uid, &sessions[i]))
		}
		c.JSON(http.StatusOK, gin.H{
			"sessions": views,
			"total":     len(views),
		})
	}
}

// GetSessionHandler handles GET /api/sessions/:id. The :id path parameter is the
// public session_key. It returns the session with its full message history.
func GetSessionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		s, err := repo.GetSessionByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		msgs, err := repo.ListSessionMessages(db, s.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load messages"})
			return
		}

		detail := sessionDetailView{
			ID:           s.ID,
			UserID:       s.UserID,
			SessionKey:   s.SessionKey,
			Title:        s.Title,
			CreatedAt:    s.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    s.UpdatedAt.Format(time.RFC3339),
			WorkspaceKey: sessionWorkspaceKey(db, uid, s),
		}
		detail.Messages = make([]messageView, 0, len(msgs))
		for i := range msgs {
			detail.Messages = append(detail.Messages, messageView{
				ID:        msgs[i].ID,
				Role:      msgs[i].Role,
				Content:   msgs[i].Content,
				CreatedAt: msgs[i].CreatedAt.Format(time.RFC3339),
			})
		}
		c.JSON(http.StatusOK, detail)
	}
}

// RenameSessionHandler handles PUT /api/sessions/:id. The :id path parameter is
// the public session_key. It updates only the session title (owner-scoped).
func RenameSessionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		var req struct {
			Title string `json:"title" binding:"required,max=256"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s, err := repo.GetSessionByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if err := db.Model(&model.Session{}).Where("id = ? AND user_id = ?", s.ID, uid).
			Update("title", req.Title).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename session"})
			return
		}
		s.Title = req.Title
		c.JSON(http.StatusOK, toSessionView(db, uid, s))
	}
}

// BindWorkspaceHandler handles PATCH /api/sessions/:id/workspace.
// 将当前用户的会话绑定到其拥有的某 workspace（owner-scoped），或传入空 key 解除绑定。
// 绑定后该会话的后续消息均在对应 workspace 本地目录执行，刷新后仍保留（契合 MX-01）。
// 鉴权走统一中间件；与对话端点一致：绑定仅影响用户自己的会话，不要求额外 RBAC 写权限。
func BindWorkspaceHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		var req struct {
			WorkspaceKey *string `json:"workspace_key"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s, err := repo.GetSessionByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if req.WorkspaceKey == nil || *req.WorkspaceKey == "" {
			// 解除绑定，回退默认目录。
			if uerr := db.Model(&model.Session{}).Where("id = ? AND user_id = ?", s.ID, uid).
				Update("workspace_id", nil).Error; uerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unbind workspace"})
				return
			}
			s.WorkspaceID = nil
			c.JSON(http.StatusOK, toSessionView(db, uid, s))
			return
		}
		ws, werr := repo.GetWorkspaceByKey(db, uid, *req.WorkspaceKey)
		if werr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		if uerr := db.Model(&model.Session{}).Where("id = ? AND user_id = ?", s.ID, uid).
			Update("workspace_id", ws.ID).Error; uerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to bind workspace"})
			return
		}
		sid := ws.ID
		s.WorkspaceID = &sid
		c.JSON(http.StatusOK, toSessionView(db, uid, s))
	}
}

// DeleteSessionHandler handles DELETE /api/sessions/:id (owner-scoped). It
// requires the "sessions:write" permission (enforced by RequirePermission in
// the router) and removes the session plus its messages.
func DeleteSessionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		if err := repo.DeleteSession(db, uid, key); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete session"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
