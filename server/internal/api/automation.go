package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// automationRequest 是创建/更新的请求体。
//   - trigger_type 必须为 cron / webhook（oneof 基础校验，跨字段必填在 Validate 兜底）
//   - cron 触发器 require cron_expr；goal_prompt 驱动 Loop 目标，必填
type automationRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	TriggerType string `json:"trigger_type" binding:"required,oneof=cron webhook"`
	CronExpr    string `json:"cron_expr"`
	GoalPrompt  string `json:"goal_prompt" binding:"required"`
	Enabled     *bool  `json:"enabled"`
}

// automationUpdateRequest 支持部分更新（仅传需要改的字段）。
type automationUpdateRequest struct {
	Name        *string `json:"name"`
	TriggerType *string `json:"trigger_type"`
	CronExpr    *string `json:"cron_expr"`
	GoalPrompt  *string `json:"goal_prompt"`
	Enabled     *bool   `json:"enabled"`
}

// automationView 是对外返回的精简视图（不回显 webhook_token）。
type automationView struct {
	ID          uint     `json:"id"`
	UserID      uint     `json:"user_id"`
	Name        string   `json:"name"`
	TriggerType string   `json:"trigger_type"`
	CronExpr    string   `json:"cron_expr"`
	GoalPrompt  string   `json:"goal_prompt"`
	Enabled     bool     `json:"enabled"`
	LastRun     *string  `json:"last_run"`
	NextRun     *string  `json:"next_run"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

func toAutomationView(a *model.Automation) automationView {
	v := automationView{
		ID:          a.ID,
		UserID:      a.UserID,
		Name:        a.Name,
		TriggerType: string(a.TriggerType),
		CronExpr:    a.CronExpr,
		GoalPrompt:  a.GoalPrompt,
		Enabled:     a.Enabled,
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   a.UpdatedAt.Format(time.RFC3339),
	}
	if a.LastRun != nil {
		s := a.LastRun.Format(time.RFC3339)
		v.LastRun = &s
	}
	if a.NextRun != nil {
		s := a.NextRun.Format(time.RFC3339)
		v.NextRun = &s
	}
	return v
}

// generateWebhookToken 生成 32 字节随机十六进制令牌（webhook 触发器匹配用，M4-03 消费）。
func generateWebhookToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateAutomationHandler 处理 POST /api/automations（需 automations:write）。
func CreateAutomationHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req automationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		trigger, ok := model.ParseAutomationTriggerType(req.TriggerType)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid trigger_type (must be cron or webhook)"})
			return
		}
		a := &model.Automation{
			UserID:      uid,
			Name:        req.Name,
			TriggerType: trigger,
			CronExpr:    req.CronExpr,
			GoalPrompt:  req.GoalPrompt,
		}
		if req.Enabled != nil {
			a.Enabled = *req.Enabled
		}
		if err := a.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// webhook 触发器默认生成令牌（M4-03 按令牌匹配外部事件）。
		if a.TriggerType == model.AutomationTriggerWebhook {
			tok, err := generateWebhookToken()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate webhook token"})
				return
			}
			a.WebhookToken = tok
		}
		// 同名冲突检测（uniqueIndex idx_user_automation 的友好前置校验）。
		if _, derr := repo.GetAutomationByName(db, uid, a.Name); derr == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "automation name already exists"})
			return
		} else if derr != repo.ErrAutomationNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check automation name"})
			return
		}
		if err := repo.CreateAutomation(db, a); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create automation"})
			return
		}
		c.JSON(http.StatusCreated, toAutomationView(a))
	}
}

// ListAutomationsHandler 处理 GET /api/automations（需 automations:read），返回当前用户的全部配置。
func ListAutomationsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListAutomations(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list automations"})
			return
		}
		views := make([]automationView, 0, len(list))
		for i := range list {
			views = append(views, toAutomationView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"automations": views, "total": len(views)})
	}
}

// GetAutomationHandler 处理 GET /api/automations/:id（owner-scoped）。
func GetAutomationHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		a, ok2 := lookupOwnedAutomation(c, db, uid)
		if !ok2 {
			return
		}
		c.JSON(http.StatusOK, toAutomationView(a))
	}
}

// UpdateAutomationHandler 处理 PUT /api/automations/:id（需 automations:write，owner-scoped，部分更新）。
func UpdateAutomationHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		a, ok2 := lookupOwnedAutomation(c, db, uid)
		if !ok2 {
			return
		}
		var req automationUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Name != nil {
			a.Name = *req.Name
		}
		if req.TriggerType != nil {
			trigger, ok := model.ParseAutomationTriggerType(*req.TriggerType)
			if !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid trigger_type (must be cron or webhook)"})
				return
			}
			a.TriggerType = trigger
		}
		if req.CronExpr != nil {
			a.CronExpr = *req.CronExpr
		}
		if req.GoalPrompt != nil {
			a.GoalPrompt = *req.GoalPrompt
		}
		if req.Enabled != nil {
			a.Enabled = *req.Enabled
		}
		if err := a.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.UpdateAutomation(db, a); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update automation"})
			return
		}
		c.JSON(http.StatusOK, toAutomationView(a))
	}
}

// DeleteAutomationHandler 处理 DELETE /api/automations/:id（需 automations:write，owner-scoped）。
func DeleteAutomationHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		a, ok2 := lookupOwnedAutomation(c, db, uid)
		if !ok2 {
			return
		}
		if err := repo.DeleteAutomation(db, a.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete automation"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// automationRunView 是对外返回的自动化运行记录视图（M4-08 运行历史）。
type automationRunView struct {
	ID           uint   `json:"id"`
	AutomationID uint   `json:"automation_id"`
	SessionKey   string `json:"session_key"`
	Channel      string `json:"channel"`
	Status       string `json:"status"`
	Error        string `json:"error"`
	Attempts     int    `json:"attempts"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func toAutomationRunView(r *model.AutomationRun) automationRunView {
	return automationRunView{
		ID:           r.ID,
		AutomationID: r.AutomationID,
		SessionKey:   r.SessionKey,
		Channel:      r.Channel,
		Status:       r.Status,
		Error:        r.Error,
		Attempts:     r.Attempts,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339),
	}
}

// ListAutomationRunsHandler 处理 GET /api/automations/:id/runs（需 automations:read，owner-scoped）。
// 返回该自动化（当前用户归属）的运行历史（running/done/failed），最近运行排前。
func ListAutomationRunsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		a, ok2 := lookupOwnedAutomation(c, db, uid)
		if !ok2 {
			return
		}
		runs, err := repo.ListAutomationRuns(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list automation runs"})
			return
		}
		views := make([]automationRunView, 0, len(runs))
		for i := range runs {
			if runs[i].AutomationID != a.ID {
				continue
			}
			views = append(views, toAutomationRunView(&runs[i]))
		}
		// 最近运行排前（同创建时间稳定保持原序）。
		sort.SliceStable(views, func(i, j int) bool {
			return views[i].CreatedAt > views[j].CreatedAt
		})
		c.JSON(http.StatusOK, gin.H{
			"runs":          views,
			"total":         len(views),
			"automation_id": a.ID,
		})
	}
}

// lookupOwnedAutomation 解析 :id、加载并校验归属；失败已写响应，返回 (nil,false)。
func lookupOwnedAutomation(c *gin.Context, db *gorm.DB, uid uint) (*model.Automation, bool) {
	idParam := c.Param("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return nil, false
	}
	a, err := repo.GetAutomationByID(db, uid, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "automation not found"})
		return nil, false
	}
	return a, true
}
