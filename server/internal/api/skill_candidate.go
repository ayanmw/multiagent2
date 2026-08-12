package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/evolution"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// evolutionSvc 是「技能进化飞轮」后端服务（M5-03），由 main.go 在启动时注入。
// 测试套件不调用 SetEvolutionService，故为 nil —— buildRouter 据此跳过进化相关路由，
// 不影响既有集成测试（与 SetRecoveryNotifier 同款包级注入模式）。
var evolutionSvc *evolution.Service

// SetEvolutionService 注入进化扫描服务（main.go 启动时调用）。
func SetEvolutionService(s *evolution.Service) { evolutionSvc = s }

// EvolutionService 返回已注入的进化服务（未注入时 nil）；供 main.go 判断是否挂载路由。
func EvolutionService() *evolution.Service { return evolutionSvc }

// skillCandidateView 是候选技能的对外视图（不回显内部哈希细节以外的内容）。
type skillCandidateView struct {
	ID               uint   `json:"id"`
	UserID           uint   `json:"user_id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Body             string `json:"body"`
	SourceSessionKey string `json:"source_session_key"`
	Status           string `json:"status"`
	RejectReason     string `json:"reject_reason"`
	QualityNotes     string `json:"quality_notes"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func toSkillCandidateView(c *model.SkillCandidate) skillCandidateView {
	return skillCandidateView{
		ID:               c.ID,
		UserID:           c.UserID,
		Name:             c.Name,
		Description:      c.Description,
		Body:             c.Body,
		SourceSessionKey: c.SourceSessionKey,
		Status:           string(c.Status),
		RejectReason:     c.RejectReason,
		QualityNotes:     c.QualityNotes,
		CreatedAt:        c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:        c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ListSkillCandidatesHandler 处理 GET /api/skill-candidates（需 skill_candidates:read）。
// 列出当前用户的候选技能，可按 status 过滤；分页 limit/offset（默认 50 / 上限 200）。
func ListSkillCandidatesHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		status := c.Query("status")
		if status != "" {
			if _, ok := model.ParseSkillCandidateStatus(status); !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "非法的 status 过滤值"})
				return
			}
		}
		limit := parseIntQuery(c, "limit", 50)
		if limit > 200 {
			limit = 200
		}
		if limit <= 0 {
			limit = 50
		}
		offset := parseIntQuery(c, "offset", 0)
		if offset < 0 {
			offset = 0
		}
		list, err := repo.ListSkillCandidates(db, uid, status, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list skill candidates"})
			return
		}
		total, err := repo.CountSkillCandidates(db, uid, status)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count skill candidates"})
			return
		}
		views := make([]skillCandidateView, 0, len(list))
		for i := range list {
			views = append(views, toSkillCandidateView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"skill_candidates": views, "total": total, "limit": limit, "offset": offset})
	}
}

// ScanSkillCandidatesHandler 处理 POST /api/skill-candidates/scan（需 skill_candidates:write）。
// 触发一次全量扫描：遍历全部会话 → 提取 → 质量门控 → 去重 → 写入 pending 候选。
// 同步执行（带整体超时），返回扫描统计；后台周期扫描由 main.go 的 StartLoop 负责。
func ScanSkillCandidatesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if evolutionSvc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "evolution service not configured"})
			return
		}
		// 整体超时保护：单次扫描可能跨多个会话调 LLM，限制上限避免请求挂死。
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
		defer cancel()
		_ = uid // 扫描是全局的（按会话归属隔离落库），不限当前用户
		rep, err := evolutionSvc.Scan(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "scan failed", "detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"scanned": rep.Scanned,
			"created": rep.Created,
			"skipped": rep.Skipped,
			"errors":  rep.Errors,
		})
	}
}

// resolveSkillCandidateBody 是审批请求体。
type resolveSkillCandidateBody struct {
	Decision     string `json:"decision"`      // approve | reject
	RejectReason string `json:"reject_reason"` // 仅 reject 时使用
}

// ResolveSkillCandidateHandler 处理 POST /api/skill-candidates/:id/resolve
// （需 skill_candidates:write）。流转候选状态：approve→approved、reject→rejected。
//
// 注意（M5-03 范围）：本任务「不自动发布」——approve 仅把状态置为 approved，
// 真正的「发布为托管技能进共享库」由 M5-04 审批前端完成。此处只闭环数据态。
func ResolveSkillCandidateHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid candidate id"})
			return
		}
		var body resolveSkillCandidateBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		var status model.SkillCandidateStatus
		switch body.Decision {
		case "approve":
			status = model.SkillCandidateApproved
		case "reject":
			status = model.SkillCandidateRejected
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "decision 必须为 approve 或 reject"})
			return
		}
		cand, err := repo.UpdateSkillCandidateStatus(db, uint(id), uid, status, body.RejectReason)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve candidate"})
			return
		}
		if cand == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill candidate not found"})
			return
		}
		c.JSON(http.StatusOK, toSkillCandidateView(cand))
	}
}

// parseIntQuery 解析查询参数中的整数，失败或非正时回退默认值。
func parseIntQuery(c *gin.Context, key string, fallback int) int {
	s := c.Query(key)
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}
