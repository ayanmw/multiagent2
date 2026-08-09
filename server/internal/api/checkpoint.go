package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// checkpointCanSeeAll 判断角色是否具备查看全员检查点的资格（admin / developer）。
func checkpointCanSeeAll(c *gin.Context) bool {
	role, _ := c.Get(middleware.CtxUserRole)
	roleStr, _ := role.(string)
	return roleStr == model.RoleAdmin || roleStr == model.RoleDeveloper
}

// validCheckpointStatus 校验 status 过滤值是否为已知检查点状态。
func validCheckpointStatus(s string) bool {
	switch s {
	case model.CheckpointPending, model.CheckpointApproved, model.CheckpointRejected:
		return true
	default:
		return false
	}
}

// ListCheckpointsHandler handles GET /api/checkpoints (requires "checkpoints:read", M3-05).
// 返回检查点列表（按创建时间倒序）。owner 隔离：viewer 仅看本人，admin/developer 看全员。
func ListCheckpointsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		f := repo.CheckpointFilter{
			Limit:  repo.NormalizeAuditPageSize(atoiOrZero(c.Query("limit"))),
			Offset: atoiOrZero(c.Query("offset")),
		}
		// owner 隔离：viewer 仅看本人；admin/developer 看全员。
		if !checkpointCanSeeAll(c) {
			uid, _ := currentUserID(c)
			f.UserID = uid
		}
		if s := c.Query("status"); s != "" {
			if !validCheckpointStatus(s) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "status 仅支持 pending / approved / rejected"})
				return
			}
			f.Status = s
		}
		list, total, err := repo.ListCheckpoints(db, f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询检查点失败"})
			return
		}
		if list == nil {
			list = []model.Checkpoint{}
		}
		scope := "all"
		if f.UserID != 0 {
			scope = "self"
		}
		c.JSON(http.StatusOK, gin.H{
			"checkpoints": list,
			"total":       total,
			"limit":       f.Limit,
			"offset":      f.Offset,
			"scope":       scope,
		})
	}
}

// ResolveCheckpointHandler handles POST /api/checkpoints/:id/resolve (requires "checkpoints:write", M3-05).
// action=approve 时按人类审批授权，在记录的工作目录实际执行该命令并落结果；
// action=reject 时标记拒绝，命令永不执行。两者均写审计留痕。
func ResolveCheckpointHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id 必须为正整数"})
			return
		}
		var req struct {
			Action  string `json:"action" binding:"required"`
			Comment string `json:"comment"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Action != "approve" && req.Action != "reject" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "action 仅支持 approve / reject"})
			return
		}
		cp, err := repo.GetCheckpoint(db, uint(id))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "检查点不存在"})
			return
		}
		// owner 隔离兜底：不具备全员视野的角色只能处置自己的检查点（RBAC 之外的二次校验）。
		if !checkpointCanSeeAll(c) && cp.UserID != uid {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权处置他人的检查点"})
			return
		}
		if cp.Status != model.CheckpointPending {
			c.JSON(http.StatusConflict, gin.H{"error": "该检查点已处理", "status": cp.Status})
			return
		}

		if req.Action == "reject" {
			if err := repo.ResolveCheckpoint(db, uint(id), model.CheckpointRejected, req.Comment, uid, ""); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "拒绝检查点失败"})
				return
			}
			// 审计留痕：拒绝视为未放行。
			repo.NewDBAuditor(db, cp.UserID).Record(executor.AuditEntry{
				Command:  cp.Command,
				Workdir:  cp.Workdir,
				Decision: executor.DecisionDeny,
				Allowed:  false,
				Reason:   cp.Reason,
				Note:     "checkpoint rejected id=" + cp.DisplayID(),
			})
			c.JSON(http.StatusOK, gin.H{"ok": true, "status": model.CheckpointRejected, "display_id": cp.DisplayID()})
			return
		}

		// approve：人类已显式授权，在记录的工作目录实际执行该命令（绕过危险策略）。
		host, herr := executor.NewHostExecutor(cp.Workdir)
		if herr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "构造执行器失败: " + herr.Error()})
			return
		}
		res, rerr := host.Run(c.Request.Context(), cp.Command)
		var result string
		if rerr != nil {
			result = "执行失败: " + rerr.Error()
		} else {
			result = fmt.Sprintf("exit_code: %d\n--- stdout ---\n%s\n--- stderr ---\n%s",
				res.ExitCode, res.Stdout, res.Stderr)
		}
		if err := repo.ResolveCheckpoint(db, uint(id), model.CheckpointApproved, req.Comment, uid, result); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "批准执行失败"})
			return
		}
		// 审计留痕：批准执行视为放行（note 标记检查点 id，便于追溯）。
		repo.NewDBAuditor(db, cp.UserID).Record(executor.AuditEntry{
			Command:  cp.Command,
			Workdir:  cp.Workdir,
			Decision: executor.DecisionAllow,
			Allowed:  true,
			Reason:   cp.Reason,
			Note:     "checkpoint approved id=" + cp.DisplayID(),
		})
		c.JSON(http.StatusOK, gin.H{
			"ok":         true,
			"status":     model.CheckpointApproved,
			"display_id": cp.DisplayID(),
			"result":     result,
		})
	}
}

// atoiOrZero 解析非负整数查询参数，失败/缺省返回 0（由 NormalizeAuditPageSize 钳定）。
func atoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
