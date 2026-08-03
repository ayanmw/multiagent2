package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// workspaceView 是 workspace 对外返回的精简视图。
type workspaceView struct {
	ID          uint   `json:"id"`
	UserID      uint   `json:"user_id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	LocalPath   string `json:"local_path"`
	GitRemote   string `json:"git_remote"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toWorkspaceView(w *model.Workspace) workspaceView {
	return workspaceView{
		ID:          w.ID,
		UserID:      w.UserID,
		Key:         w.Key,
		Name:        w.Name,
		LocalPath:   w.LocalPath,
		GitRemote:   w.GitRemote,
		Description: w.Description,
		Status:      string(w.Status),
		CreatedAt:   w.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   w.UpdatedAt.Format(time.RFC3339),
	}
}

// CreateWorkspaceHandler handles POST /api/workspaces. It creates a user-owned
// workspace, computing its local directory as <WorkspaceRoot>/<uid>/<key> and
// creating it on disk. Requires the "workspaces:write" permission (RBAC).
func CreateWorkspaceHandler(db *gorm.DB, workspaceRoot string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req struct {
			Name        string `json:"name" binding:"required,max=128"`
			GitRemote   string `json:"git_remote" binding:"max=512"`
			Description string `json:"description" binding:"max=512"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		key := "ws-" + uuid.NewString()[:8]
		localPath := filepath.Join(workspaceRoot, strconv.Itoa(int(uid)), key)
		if err := os.MkdirAll(localPath, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace directory"})
			return
		}

		// 自动 git init（M2-01）：workspace 创建时即初始化为 git 仓库，
		// 使后续代码改动可由 Coder 经 git_commit 提交管理（验收要求「建 workspace→自动 init」）。
		// best-effort：git 缺失或初始化失败不阻断 workspace 创建，仅打印告警。
		// 经 executor.SafeExecutor 执行（与 CodeAct 同款危险命令策略），禁止裸用 os/exec。
		if ex, gerr := codectool.NewGitExecutor(localPath); gerr == nil {
			if _, ierr := codectool.GitInit(context.Background(), ex); ierr != nil {
				log.Printf("[WARN] workspace %s 自动 git init 失败（已忽略）：%v", key, ierr)
			}
		}

		w := &model.Workspace{
			UserID:      uid,
			Key:         key,
			Name:        req.Name,
			LocalPath:   localPath,
			GitRemote:   req.GitRemote,
			Description: req.Description,
			Status:      model.WorkspaceStatusActive,
		}
		if err := repo.CreateWorkspace(db, w); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace"})
			return
		}
		c.JSON(http.StatusCreated, toWorkspaceView(w))
	}
}

// ListWorkspacesHandler handles GET /api/workspaces (current user's workspaces).
func ListWorkspacesHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListWorkspaces(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list workspaces"})
			return
		}
		views := make([]workspaceView, 0, len(list))
		for i := range list {
			views = append(views, toWorkspaceView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"workspaces": views, "total": len(views)})
	}
}

// GetWorkspaceHandler handles GET /api/workspaces/:id (id = workspace_key).
func GetWorkspaceHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace id"})
			return
		}
		w, err := repo.GetWorkspaceByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		c.JSON(http.StatusOK, toWorkspaceView(w))
	}
}

// UpdateWorkspaceHandler handles PUT /api/workspaces/:id. Only the provided
// fields are updated (partial update). Requires "workspaces:write".
func UpdateWorkspaceHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		w, err := repo.GetWorkspaceByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		var req struct {
			Name        *string `json:"name"`
			GitRemote   *string `json:"git_remote"`
			Description *string `json:"description"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Name != nil {
			w.Name = *req.Name
		}
		if req.GitRemote != nil {
			w.GitRemote = *req.GitRemote
		}
		if req.Description != nil {
			w.Description = *req.Description
		}
		if err := repo.UpdateWorkspace(db, w); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workspace"})
			return
		}
		c.JSON(http.StatusOK, toWorkspaceView(w))
	}
}

// DeleteWorkspaceHandler handles DELETE /api/workspaces/:id (owner-scoped).
// Only the DB row is removed; the on-disk directory is preserved to avoid
// accidental data loss (the user can clean it up manually). Requires
// "workspaces:write".
func DeleteWorkspaceHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		key := c.Param("id")
		w, err := repo.GetWorkspaceByKey(db, uid, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		if err := repo.DeleteWorkspace(db, w.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete workspace"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// resolveWorkspaceLocalDir 解析本次对话应使用的工作目录本地路径：
//   - workspaceKey 非空：按 (user, key) 查找并绑定会话到该 workspace，返回其 LocalPath；
//   - workspaceKey 为空但会话已绑定 workspace_id：复用已绑定目录；
//   - 均无：返回空串，由调用方回退到 WorkspaceRoot/<uid> 默认目录。
//
// 返回空串表示使用默认目录（不报错）。会话与 workspace 的绑定会被持久化到
// sessions.workspace_id，使后续同会话（不传 workspaceKey）仍落在同一目录。
func resolveWorkspaceLocalDir(db *gorm.DB, uid uint, workspaceKey string, sess *model.Session) (string, error) {
	var ws *model.Workspace
	var err error
	if workspaceKey != "" {
		ws, err = repo.GetWorkspaceByKey(db, uid, workspaceKey)
		if err != nil {
			return "", err // 404 / 越权
		}
	} else if sess.WorkspaceID != nil && *sess.WorkspaceID != 0 {
		ws, err = repo.GetWorkspaceByID(db, uid, *sess.WorkspaceID)
		if err != nil {
			// 已绑定但 workspace 已被删除：回退默认目录，不报错。
			return "", nil
		}
	}
	if ws == nil {
		return "", nil
	}
	// 绑定 / 更新会话的 workspace。
	sid := ws.ID
	if sess.WorkspaceID == nil || *sess.WorkspaceID != sid {
		sess.WorkspaceID = &sid
		if uerr := db.Model(&model.Session{}).Where("id = ?", sess.ID).Update("workspace_id", sid).Error; uerr != nil {
			return "", uerr
		}
	}
	return ws.LocalPath, nil
}
