package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// budgetRequest 是 PUT /api/budgets 的请求体（upsert）。
type budgetRequest struct {
	Scope     string `json:"scope" binding:"required"`
	ScopeKey  string `json:"scope_key"` // 空字符串表示全局默认（仅 user 作用域适用）
	MaxTokens int64  `json:"max_tokens" binding:"required"`
	Window    string `json:"window"` // 缺省 daily
}

// ListBudgetsHandler handles GET /api/budgets (requires "budgets:read", M3-04).
// 返回全部预算策略（管理员视图），供前端管理界面渲染与调整。
func ListBudgetsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListBudgetPolicies(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询预算策略失败"})
			return
		}
		if list == nil {
			list = []model.BudgetPolicy{}
		}
		c.JSON(http.StatusOK, gin.H{"budget_policies": list})
	}
}

// UpsertBudgetHandler handles PUT /api/budgets (requires "budgets:write", M3-04).
// 按 (scope, scope_key) 唯一键 upsert 一条预算策略；用于管理员设定 / 调整平台级预算护栏。
func UpsertBudgetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req budgetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Window == "" {
			req.Window = model.BudgetWindowDaily
		}
		pol := model.BudgetPolicy{
			Scope:     req.Scope,
			ScopeKey:  req.ScopeKey,
			MaxTokens: req.MaxTokens,
			Window:    req.Window,
		}
		if err := pol.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.UpsertBudgetPolicy(db, &pol); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存预算策略失败"})
			return
		}
		// 重新读回（含自增 id），保证响应体携带完整记录。
		if saved, gerr := repo.GetBudgetPolicy(db, pol.Scope, pol.ScopeKey); gerr == nil && saved != nil {
			c.JSON(http.StatusOK, saved)
			return
		}
		c.JSON(http.StatusOK, &pol)
	}
}

// DeleteBudgetHandler handles DELETE /api/budgets/:id (requires "budgets:write", M3-04).
func DeleteBudgetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id 必须为正整数"})
			return
		}
		if err := repo.DeleteBudgetPolicy(db, uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除预算策略失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// writeBudgetBlockAudit 在预算耗尽拦截时写一条审计（M3-04），便于管理员在审计日志追溯。
func writeBudgetBlockAudit(db *gorm.DB, uid uint, ev repo.BudgetEvaluation) {
	repo.NewDBAuditor(db, uid).Record(executor.AuditEntry{
		Command:  "budget:enforce",
		Decision: executor.DecisionDeny,
		Allowed:  false,
		Reason: fmt.Sprintf("平台级预算耗尽（scope=%s 已用 %d >= 上限 %d），暂停后续 LLM 调用，待管理员提额后恢复",
			ev.Scope, ev.Used, ev.Max),
		Note: "budget_guardrail",
	})
}
