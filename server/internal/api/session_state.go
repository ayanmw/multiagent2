package api

import (
	"net/http"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// sessionStateView 是会话「工作状态外置」文件的对外视图（M1-16）。
// 长任务（Goal / Plan-Execute / 24h 自主 Loop）把 PLAN/PROGRESS/LEARNINGS
// 维护为 artifact，落盘后可跨进程存活；本端点让前端能直接查看 Agent 的
// 计划与进展，弥补此前「只能看最终回复、看不到过程」的缺口。
type sessionStateView struct {
	Exists    bool   `json:"exists"`
	Plan      string `json:"plan,omitempty"`
	Progress  string `json:"progress,omitempty"`
	Learnings string `json:"learnings,omitempty"`
}

// GetSessionStateHandler handles GET /api/sessions/:id/state.
// :id 即 session_key；作用域键为 "sess:<session_key>"，与 StateEnforcer
// 写入 artifact 时使用的 goalScope 保持一致。状态外置未启用时返回 exists=false。
func GetSessionStateHandler(db *gorm.DB, store artifact.Store, enableState bool) gin.HandlerFunc {
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
		// 归属校验：确保只能查看自己的会话状态。
		if _, err := repo.GetSessionByKey(db, uid, key); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		resp := sessionStateView{Exists: false}
		if !enableState || store == nil {
			c.JSON(http.StatusOK, resp)
			return
		}
		scope := "sess:" + key
		snap, err := store.Snapshot(scope)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read session state"})
			return
		}
		if snap.Any {
			resp.Exists = true
			resp.Plan = snap.Plan
			resp.Progress = snap.Progress
			resp.Learnings = snap.Learnings
		}
		c.JSON(http.StatusOK, resp)
	}
}
