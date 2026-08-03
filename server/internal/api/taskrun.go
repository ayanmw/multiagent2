package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ListTaskRunsHandler GET /api/taskruns：列出当前用户发起的后台任务（owner 隔离）。
// 读操作需 taskruns:read（RBAC，路由层 RequirePermission 强制）。
func ListTaskRunsHandler(controller taskrunruntime.Controller) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		runs, err := controller.List(c.Request.Context(), taskrunruntime.ListFilter{
			OwnerUserID: uidStr,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"runs": runs})
	}
}

// GetTaskRunHandler GET /api/taskruns/:id：获取单个后台任务详情（owner 隔离）。
func GetTaskRunHandler(controller taskrunruntime.Controller) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		id := c.Param("id")
		run, err := controller.Get(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "task run not found"})
			return
		}
		if run.OwnerUserID != uidStr {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"run": run})
	}
}

// CancelTaskRunHandler POST /api/taskruns/:id/cancel：取消后台任务（owner 隔离）。
// 写操作需 taskruns:write（RBAC，路由层 RequirePermission 强制）。
func CancelTaskRunHandler(controller taskrunruntime.Controller) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		id := c.Param("id")
		run, err := controller.Get(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "task run not found"})
			return
		}
		if run.OwnerUserID != uidStr {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		canceled, _, err := controller.Cancel(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"run": canceled})
	}
}

// GetTaskRunTranscriptHandler GET /api/taskruns/:id/transcript：读取子任务 transcript（owner 隔离）。
// 复用框架 transcript 回查契约：父 appName + OwnerUserID + ChildSessionID。
func GetTaskRunTranscriptHandler(controller taskrunruntime.Controller, sessionSvc session.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		id := c.Param("id")
		run, err := controller.Get(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "task run not found"})
			return
		}
		if run.OwnerUserID != uidStr {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if sessionSvc == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "transcript unavailable"})
			return
		}
		child, err := sessionSvc.GetSession(c.Request.Context(), session.Key{
			AppName:   run.AppName,
			UserID:    run.OwnerUserID,
			SessionID: run.ChildSessionID,
		}, session.WithEventNum(200))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if child == nil {
			c.JSON(http.StatusOK, gin.H{
				"child_session_id": run.ChildSessionID,
				"events":           []any{},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"child_session_id": run.ChildSessionID,
			"events":           child.GetEvents(),
		})
	}
}
