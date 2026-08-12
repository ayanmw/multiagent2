package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// evalSvc 是「评估回归」后端服务（M5-05），由 main.go 在启动时注入。
// 测试套件不调用 SetEvalService，故为 nil —— buildRouter 据此跳过评估相关路由，
// 不影响既有集成测试（与 SetEvolutionService 同款包级注入模式）。
var evalSvc *eval.Service

// SetEvalService 注入评估服务（main.go 启动时调用）。
func SetEvalService(s *eval.Service) { evalSvc = s }

// EvalService 返回已注入的评估服务（未注入时 nil）；供 main.go 判断是否挂载路由。
func EvalService() *eval.Service { return evalSvc }

// ---- 视图 ----

type evalDatasetView struct {
	ID            uint   `json:"id"`
	UserID        uint   `json:"user_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultGrader string `json:"default_grader"`
	DefaultModel  string `json:"default_model"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func toEvalDatasetView(d *model.EvalDataset) evalDatasetView {
	return evalDatasetView{
		ID:            d.ID,
		UserID:        d.UserID,
		Name:          d.Name,
		Description:   d.Description,
		DefaultGrader: string(d.DefaultGrader),
		DefaultModel:  d.DefaultModel,
		CreatedAt:     d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     d.UpdatedAt.Format(time.RFC3339),
	}
}

type evalCaseView struct {
	ID         uint   `json:"id"`
	DatasetID  uint   `json:"dataset_id"`
	Input      string `json:"input"`
	Expected   string `json:"expected"`
	Grader     string `json:"grader"`
	Model      string `json:"model"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func toEvalCaseView(c *model.EvalCase) evalCaseView {
	return evalCaseView{
		ID:         c.ID,
		DatasetID:  c.DatasetID,
		Input:      c.Input,
		Expected:   c.Expected,
		Grader:     string(c.Grader),
		Model:      c.ModelID,
		CreatedAt:  c.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  c.UpdatedAt.Format(time.RFC3339),
	}
}

type evalRunView struct {
	ID            uint     `json:"id"`
	DatasetID     uint     `json:"dataset_id"`
	UserID        uint     `json:"user_id"`
	Model         string   `json:"model"`
	Grader        string   `json:"grader"`
	Repeats       int      `json:"repeats"`
	Status        string   `json:"status"`
	ScoreAvg      float64  `json:"score_avg"`
	PassRate      float64  `json:"pass_rate"`
	TotalCases    int      `json:"total_cases"`
	TotalAttempts int      `json:"total_attempts"`
	Error         string   `json:"error"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
	CreatedAt     string   `json:"created_at"`
}

func toEvalRunView(r *model.EvalRun) evalRunView {
	var sa, fa *string
	if r.StartedAt != nil {
		s := r.StartedAt.Format(time.RFC3339)
		sa = &s
	}
	if r.FinishedAt != nil {
		s := r.FinishedAt.Format(time.RFC3339)
		fa = &s
	}
	return evalRunView{
		ID:            r.ID,
		DatasetID:     r.DatasetID,
		UserID:        r.UserID,
		Model:         r.ModelID,
		Grader:        r.Grader,
		Repeats:       r.Repeats,
		Status:        r.Status,
		ScoreAvg:      r.ScoreAvg,
		PassRate:      r.PassRate,
		TotalCases:    r.TotalCases,
		TotalAttempts: r.TotalAttempts,
		Error:         r.Error,
		StartedAt:     sa,
		FinishedAt:    fa,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
	}
}

type evalResultView struct {
	ID         uint    `json:"id"`
	RunID      uint    `json:"run_id"`
	DatasetID  uint    `json:"dataset_id"`
	CaseID     uint    `json:"case_id"`
	Attempt    int     `json:"attempt"`
	Grader     string  `json:"grader"`
	Output     string  `json:"output"`
	Score      float64 `json:"score"`
	Passed     bool    `json:"passed"`
	LatencyMs  int64   `json:"latency_ms"`
	Error      string  `json:"error"`
	CreatedAt  string  `json:"created_at"`
}

func toEvalResultView(r *model.EvalResult) evalResultView {
	return evalResultView{
		ID:         r.ID,
		RunID:      r.RunID,
		DatasetID:  r.DatasetID,
		CaseID:     r.CaseID,
		Attempt:    r.Attempt,
		Grader:     string(r.Grader),
		Output:     r.Output,
		Score:      r.Score,
		Passed:     r.Passed,
		LatencyMs:  r.LatencyMs,
		Error:      r.Error,
		CreatedAt:  r.CreatedAt.Format(time.RFC3339),
	}
}

// ---- 评估集 Dataset CRUD ----

type createEvalDatasetBody struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultGrader string `json:"default_grader"`
	DefaultModel  string `json:"default_model"`
}

// CreateEvalDatasetHandler 处理 POST /api/eval/datasets（需 evaluations:write）。
func CreateEvalDatasetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var body createEvalDatasetBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		d := &model.EvalDataset{
			UserID:        uid,
			Name:          body.Name,
			Description:   body.Description,
			DefaultGrader: model.GraderType(body.DefaultGrader),
			DefaultModel:  body.DefaultModel,
		}
		if err := d.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 同名冲突检测（同一用户内唯一）。
		if _, err := repo.GetEvalDatasetByName(db, uid, d.Name); err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "评估集名称已存在"})
			return
		} else if err != repo.ErrEvalDatasetNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check dataset name"})
			return
		}
		if err := repo.CreateEvalDataset(db, d); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create dataset"})
			return
		}
		c.JSON(http.StatusOK, toEvalDatasetView(d))
	}
}

// ListEvalDatasetsHandler 处理 GET /api/eval/datasets（需 evaluations:read）。
func ListEvalDatasetsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListEvalDatasets(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list datasets"})
			return
		}
		views := make([]evalDatasetView, 0, len(list))
		for i := range list {
			views = append(views, toEvalDatasetView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"datasets": views, "total": len(views)})
	}
}

// GetEvalDatasetHandler 处理 GET /api/eval/datasets/:id（需 evaluations:read）。
func GetEvalDatasetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		d, err := repo.GetEvalDataset(db, uid, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		c.JSON(http.StatusOK, toEvalDatasetView(d))
	}
}

type updateEvalDatasetBody struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	DefaultGrader *string `json:"default_grader"`
	DefaultModel  *string `json:"default_model"`
}

// UpdateEvalDatasetHandler 处理 PUT /api/eval/datasets/:id（需 evaluations:write）。
// 仅更新请求体中显式提供的字段（指针区分「未提供」与「置空」）。
func UpdateEvalDatasetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		d, err := repo.GetEvalDataset(db, uid, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		var body updateEvalDatasetBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if body.Name != nil {
			d.Name = *body.Name
		}
		if body.Description != nil {
			d.Description = *body.Description
		}
		if body.DefaultGrader != nil {
			d.DefaultGrader = model.GraderType(*body.DefaultGrader)
		}
		if body.DefaultModel != nil {
			d.DefaultModel = *body.DefaultModel
		}
		if err := d.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.Name != nil {
			if existing, eerr := repo.GetEvalDatasetByName(db, uid, d.Name); eerr == nil && existing.ID != d.ID {
				c.JSON(http.StatusConflict, gin.H{"error": "评估集名称已存在"})
				return
			} else if eerr != nil && eerr != repo.ErrEvalDatasetNotFound {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check dataset name"})
				return
			}
		}
		if err := repo.UpdateEvalDataset(db, d); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update dataset"})
			return
		}
		c.JSON(http.StatusOK, toEvalDatasetView(d))
	}
}

// DeleteEvalDatasetHandler 处理 DELETE /api/eval/datasets/:id（需 evaluations:write）。
// 级联清理其用例、运行与结果（repo 内保证无孤儿数据）。
func DeleteEvalDatasetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		d, err := repo.GetEvalDataset(db, uid, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		if err := repo.DeleteEvalDataset(db, d.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete dataset"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// ---- 用例 Case CRUD ----

type createEvalCaseBody struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Grader   string `json:"grader"`
	Model    string `json:"model"`
}

// CreateEvalCaseHandler 处理 POST /api/eval/datasets/:id/cases（需 evaluations:write）。
func CreateEvalCaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		// 校验归属。
		if _, err := repo.GetEvalDataset(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		var body createEvalCaseBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		cs := &model.EvalCase{
			DatasetID: id,
			Input:     body.Input,
			Expected:  body.Expected,
			Grader:    model.GraderType(body.Grader),
			ModelID:   body.Model,
		}
		if err := cs.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.CreateEvalCase(db, cs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create case"})
			return
		}
		c.JSON(http.StatusOK, toEvalCaseView(cs))
	}
}

// ListEvalCasesHandler 处理 GET /api/eval/datasets/:id/cases（需 evaluations:read）。
func ListEvalCasesHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		if _, err := repo.GetEvalDataset(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		list, err := repo.ListEvalCases(db, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list cases"})
			return
		}
		views := make([]evalCaseView, 0, len(list))
		for i := range list {
			views = append(views, toEvalCaseView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"cases": views, "total": len(views)})
	}
}

// GetEvalCaseHandler 处理 GET /api/eval/datasets/:id/cases/:caseId（需 evaluations:read）。
func GetEvalCaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		caseID, err := parseUintParam(c, "caseId")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid case id"})
			return
		}
		if _, err := repo.GetEvalDataset(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		cs, err := repo.GetEvalCase(db, id, caseID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "case not found"})
			return
		}
		c.JSON(http.StatusOK, toEvalCaseView(cs))
	}
}

type updateEvalCaseBody struct {
	Input    *string `json:"input"`
	Expected *string `json:"expected"`
	Grader   *string `json:"grader"`
	Model    *string `json:"model"`
}

// UpdateEvalCaseHandler 处理 PUT /api/eval/datasets/:id/cases/:caseId（需 evaluations:write）。
func UpdateEvalCaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		caseID, err := parseUintParam(c, "caseId")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid case id"})
			return
		}
		if _, err := repo.GetEvalDataset(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		cs, err := repo.GetEvalCase(db, id, caseID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "case not found"})
			return
		}
		var body updateEvalCaseBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if body.Input != nil {
			cs.Input = *body.Input
		}
		if body.Expected != nil {
			cs.Expected = *body.Expected
		}
		if body.Grader != nil {
			cs.Grader = model.GraderType(*body.Grader)
		}
		if body.Model != nil {
			cs.ModelID = *body.Model
		}
		if err := cs.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := repo.UpdateEvalCase(db, cs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update case"})
			return
		}
		c.JSON(http.StatusOK, toEvalCaseView(cs))
	}
}

// DeleteEvalCaseHandler 处理 DELETE /api/eval/datasets/:id/cases/:caseId（需 evaluations:write）。
func DeleteEvalCaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		caseID, err := parseUintParam(c, "caseId")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid case id"})
			return
		}
		if _, err := repo.GetEvalDataset(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		if err := repo.DeleteEvalCase(db, id, caseID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete case"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// ---- 运行 Run ----

type runEvalBody struct {
	Model   string `json:"model"`
	Grader  string `json:"grader"`
	Repeats int    `json:"repeats"`
}

// RunEvalHandler 处理 POST /api/eval/datasets/:id/run（需 evaluations:write）。
// 校验数据集与用例后创建运行记录（running），在后台异步执行评估，立即返回 run（含 id）
// 供前端轮询进度。模型/Prompt 改动前后各跑一次，对比 score_avg / pass_rate 即可判断退步。
func RunEvalHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if evalSvc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "eval service not configured"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
			return
		}
		var body runEvalBody
		// 空 body 合法（全部走数据集/用例默认）。
		_ = c.ShouldBindJSON(&body)
		// 校验运行级 grader（若指定）合法。
		if body.Grader != "" {
			if _, ok := model.ParseGraderType(body.Grader); !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "非法的评分器（应为 exact / contains / llm）"})
				return
			}
		}
		if body.Repeats < 0 {
			body.Repeats = 0
		}
		run, err := evalSvc.StartRun(c.Request.Context(), uid, id, body.Model, body.Grader, body.Repeats)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, toEvalRunView(run))
	}
}

// ListEvalRunsHandler 处理 GET /api/eval/runs（需 evaluations:read）。
// 可按 ?dataset_id= 过滤（owner 隔离）。
func ListEvalRunsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var datasetID uint
		if s := c.Query("dataset_id"); s != "" {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset_id"})
				return
			}
			datasetID = uint(v)
		}
		list, err := repo.ListEvalRuns(db, uid, datasetID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list runs"})
			return
		}
		views := make([]evalRunView, 0, len(list))
		for i := range list {
			views = append(views, toEvalRunView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"runs": views, "total": len(views)})
	}
}

// GetEvalRunHandler 处理 GET /api/eval/runs/:id（需 evaluations:read）。
func GetEvalRunHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
			return
		}
		run, err := repo.GetEvalRun(db, uid, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusOK, toEvalRunView(run))
	}
}

// ListEvalResultsHandler 处理 GET /api/eval/runs/:id/results（需 evaluations:read）。
// 返回该运行下全部尝试结果（按用例、尝试序号正序），便于前端逐条展示透明可追溯。
func ListEvalResultsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
			return
		}
		if _, err := repo.GetEvalRun(db, uid, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		list, err := repo.ListEvalResults(db, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list results"})
			return
		}
		views := make([]evalResultView, 0, len(list))
		for i := range list {
			views = append(views, toEvalResultView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"results": views, "total": len(views)})
	}
}
