package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/promptiter"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// promptIterSvc 是「GEPA 反射式 Prompt 优化」后端服务（M5-06），由 main.go 注入。
// 测试套件不调用 SetPromptIterService，故为 nil —— buildRouter 据此跳过相关路由。
var promptIterSvc *promptiter.Service

// SetPromptIterService 注入优化服务（main.go 启动时调用）。
func SetPromptIterService(s *promptiter.Service) { promptIterSvc = s }

// PromptIterService 返回已注入的服务（未注入时 nil）；供 main.go 判断是否挂载路由。
func PromptIterService() *promptiter.Service { return promptIterSvc }

// ---- 视图 ----

type promptIterRunView struct {
	ID              uint     `json:"id"`
	UserID          uint     `json:"user_id"`
	DatasetID       uint     `json:"dataset_id"`
	InstructionName string   `json:"instruction_name"`
	Role            string   `json:"role"`
	Repeats         int      `json:"repeats"`
	Threshold       float64  `json:"threshold"`
	Status          string   `json:"status"`
	Error           string   `json:"error"`
	BaselineScore   float64  `json:"baseline_score"`
	CandidateScore  float64  `json:"candidate_score"`
	BeforeContent   string   `json:"before_content"`
	AfterContent    string   `json:"after_content"`
	Reasoning       string   `json:"reasoning"`
	WeakCount       int      `json:"weak_count"`
	CreatedAt       string   `json:"created_at"`
	FinishedAt      *string  `json:"finished_at"`
}

func toPromptIterRunView(r *model.PromptIterRun) promptIterRunView {
	var fa *string
	if r.FinishedAt != nil {
		s := r.FinishedAt.Format(time.RFC3339)
		fa = &s
	}
	return promptIterRunView{
		ID:              r.ID,
		UserID:          r.UserID,
		DatasetID:       r.DatasetID,
		InstructionName: r.InstructionName,
		Role:            r.Role,
		Repeats:         r.Repeats,
		Threshold:       r.Threshold,
		Status:          r.Status,
		Error:           r.Error,
		BaselineScore:   r.BaselineScore,
		CandidateScore:  r.CandidateScore,
		BeforeContent:   r.BeforeContent,
		AfterContent:    r.AfterContent,
		Reasoning:       r.Reasoning,
		WeakCount:       r.WeakCount,
		CreatedAt:       r.CreatedAt.Format(time.RFC3339),
		FinishedAt:      fa,
	}
}

type instructionView struct {
	ID        uint     `json:"id"`
	UserID    uint     `json:"user_id"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Version   int      `json:"version"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func toInstructionView(i *model.AgentInstruction) instructionView {
	return instructionView{
		ID:        i.ID,
		UserID:    i.UserID,
		Name:      i.Name,
		Role:      i.Role,
		Content:   i.Content,
		Version:   i.Version,
		CreatedAt: i.CreatedAt.Format(time.RFC3339),
		UpdatedAt: i.UpdatedAt.Format(time.RFC3339),
	}
}

// ---- 优化运行 ----

// optimizePromptIterBody 是优化请求体。
type optimizePromptIterBody struct {
	DatasetID       uint    `json:"dataset_id"`
	InstructionName string  `json:"instruction_name"` // 默认 "default"
	Role            string  `json:"role"`             // 默认 "single"
	Repeats         int     `json:"repeats"`          // 默认 1
	Threshold       float64 `json:"threshold"`        // 弱项阈值，默认 0.5
}

// OptimizePromptIterHandler 处理 POST /api/promptiter/optimize（需 promptiter:write）。
// 异步触发一次 GEPA 反射优化（baseline→弱项→反射→应用→重评→决策），立即返回运行记录，
// 前端轮询 GET /api/promptiter/runs/:id 查看终态。
func OptimizePromptIterHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if promptIterSvc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "promptiter service not configured"})
			return
		}
		var body optimizePromptIterBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if body.DatasetID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "dataset_id 必填"})
			return
		}
		run, err := promptIterSvc.StartOptimize(c.Request.Context(), promptiter.Request{
			UserID:          uid,
			DatasetID:       body.DatasetID,
			InstructionName: body.InstructionName,
			Role:            body.Role,
			Repeats:         body.Repeats,
			Threshold:       body.Threshold,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start optimization", "detail": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, toPromptIterRunView(run))
	}
}

// ListPromptIterRunsHandler 处理 GET /api/promptiter/runs（需 promptiter:read）。
func ListPromptIterRunsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListPromptIterRuns(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list runs"})
			return
		}
		views := make([]promptIterRunView, 0, len(list))
		for i := range list {
			views = append(views, toPromptIterRunView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"runs": views, "total": len(views)})
	}
}

// GetPromptIterRunHandler 处理 GET /api/promptiter/runs/:id（需 promptiter:read）。
func GetPromptIterRunHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
			return
		}
		run, err := repo.GetPromptIterRun(db, uid, uint(id))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusOK, toPromptIterRunView(run))
	}
}

// RollbackPromptIterHandler 处理 POST /api/promptiter/runs/:id/rollback（需 promptiter:write）。
// 把该次运行回滚到优化前指令（再次写回 BeforeContent，版本自增留痕）。
func RollbackPromptIterHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if promptIterSvc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "promptiter service not configured"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
			return
		}
		run, err := promptIterSvc.Rollback(c.Request.Context(), uid, uint(id))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, toPromptIterRunView(run))
	}
}

// ---- 指令管理（可读性 + 手动编辑）----

// ListInstructionsHandler 处理 GET /api/instructions（需 instructions:read）。
func ListInstructionsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListInstructions(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list instructions"})
			return
		}
		views := make([]instructionView, 0, len(list))
		for i := range list {
			views = append(views, toInstructionView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"instructions": views, "total": len(views)})
	}
}

// GetInstructionHandler 处理 GET /api/instructions/:name（需 instructions:read）。
func GetInstructionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		name := c.Param("name")
		if name == "" {
			name = model.DefaultInstructionName
		}
		ins, err := repo.GetInstruction(db, uid, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "instruction not found"})
			return
		}
		c.JSON(http.StatusOK, toInstructionView(ins))
	}
}

// updateInstructionBody 是手动编辑指令的请求体。
type updateInstructionBody struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

// UpdateInstructionHandler 处理 PUT /api/instructions/:name（需 instructions:write）。
// 手动写回一条指令（覆盖），版本自增；promptiter 优化同样经此落库，二者统一入口。
func UpdateInstructionHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		name := c.Param("name")
		if name == "" {
			name = model.DefaultInstructionName
		}
		var body updateInstructionBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ins, err := repo.CreateOrUpdateInstruction(db, uid, name, body.Role, body.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, toInstructionView(ins))
	}
}
