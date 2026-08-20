package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// usageScopeAll / usageScopeSelf 标识本次查询的可见范围，供前端提示与筛选器渲染。
const (
	usageScopeAll  = "all"
	usageScopeSelf = "self"
)

// usageCanSeeAll 判断角色是否具备查看全员用量记录的资格（admin / developer）。
func usageCanSeeAll(c *gin.Context) bool {
	role, _ := c.Get(middleware.CtxUserRole)
	roleStr, _ := role.(string)
	return roleStr == model.RoleAdmin || roleStr == model.RoleDeveloper
}

// ListUsageHandler handles GET /api/usage (requires "usage:read", M3-03).
//
// 用量记录 owner 隔离规则（与审计日志一致）：
//   - admin / developer：默认查看全员记录，可用 user_id 参数收敛到某个用户；
//   - viewer：强制仅查看本人记录（忽略 user_id 参数，不可越权）。
//
// 支持可选查询参数做服务端过滤与分页：
//   - user_id：按归属用户过滤（仅 admin/developer 生效）
//   - provider_id：按 Provider 过滤
//   - model_id：按模型过滤
//   - session_key：按会话业务 key 过滤
//   - start / end：时间范围（毫秒时间戳或 RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"）
//   - limit / offset：分页（缺省 limit=50，上限 200）
//
// 返回体：{ usage_records, total, totals, limit, offset, scope }，
// totals 为过滤范围内的 token 累计（聚合），scope 为 all/self 供前端渲染筛选器。
func ListUsageHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		filter := repo.UsageRecordFilter{}

		// 可见范围：admin/developer 看全员（可按 user_id 收敛），其余角色强制只看自己。
		scope := usageScopeSelf
		if usageCanSeeAll(c) {
			scope = usageScopeAll
			if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
				n, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "user_id 必须为正整数"})
					return
				}
				filter.UserID = uint(n)
			}
		} else {
			// 非 admin/developer 一律仅看本人（owner 隔离兜底，忽略传入的 user_id）。
			filter.UserID = uid
		}

		if raw := strings.TrimSpace(c.Query("provider_id")); raw != "" {
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id 必须为正整数"})
				return
			}
			filter.ProviderID = uint(n)
		}
		if raw := strings.TrimSpace(c.Query("model_id")); raw != "" {
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "model_id 必须为正整数"})
				return
			}
			filter.ModelID = uint(n)
		}
		if raw := strings.TrimSpace(c.Query("session_key")); raw != "" {
			filter.SessionKey = raw
		}

		start, okStart := parseAuditTime(c.Query("start"))
		if !okStart {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start 时间格式非法"})
			return
		}
		end, okEnd := parseAuditTime(c.Query("end"))
		if !okEnd {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end 时间格式非法"})
			return
		}
		if !start.IsZero() && !end.IsZero() && end.Before(start) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end 不能早于 start"})
			return
		}
		filter.Start, filter.End = start, end

		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				filter.Limit = n
			}
		}
		if o := c.Query("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil {
				filter.Offset = n
			}
		}

		list, total, err := repo.ListUsageRecords(db, filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用量记录失败"})
			return
		}
		if list == nil {
			list = []model.UsageRecord{}
		}

		// 累计聚合（忽略分页，统计过滤范围内的全部命中记录）。
		totals, err := repo.SumUsageRecords(db, filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "聚合用量失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"usage_records": list,
			"total":         total,
			"totals": gin.H{
				"prompt_tokens":     totals.PromptTokens,
				"completion_tokens": totals.CompletionTokens,
				"total_tokens":      totals.TotalTokens,
				"records":           totals.Records,
			},
			"limit":  repo.NormalizeUsagePageSize(filter.Limit),
			"offset": filter.Offset,
			"scope":  scope,
		})
	}
}

// buildPromptText 把对话历史（引擎 ChatMessage DTO）与当前用户消息拼成一段文本，
// 供上游未返回 usage 时做本地估算（M3-03 兜底）。
func buildPromptText(history []engine.ChatMessage, current string) string {
	var sb strings.Builder
	for _, msg := range history {
		sb.WriteString(string(msg.Role))
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	sb.WriteString("user: ")
	sb.WriteString(current)
	return sb.String()
}

// recordEngineUsage 在对话结束后落库 token 用量（M3-03 Token/费用计量）。
// 优先用引擎捕获的上游 usage；若上游未给（TotalTokens==0），用 engine.EstimateUsage
// 本地粗估并标记 Estimated=true，保证 usage_records 始终有可观测行；估算也为 0 时跳过。
// workspaceKey 是会话绑定的 workspace key（M8-09，空=默认目录，不参与 workspace 聚合）。
func recordEngineUsage(db *gorm.DB, eng *engine.Engine, uid uint, sess *model.Session, p *model.Provider, m *model.Model, workspaceKey, promptText, completionText string) {
	usage := eng.LastUsage()
	estimated := false
	if usage.TotalTokens == 0 {
		usage = engine.EstimateUsage(promptText, completionText)
		estimated = true
	}
	if usage.TotalTokens == 0 {
		return
	}
	_ = repo.CreateUsageRecord(db, &model.UsageRecord{
		UserID:           uid,
		SessionID:        sess.ID,
		SessionKey:       sess.SessionKey,
		WorkspaceKey:     workspaceKey,
		ProviderID:       p.ID,
		ModelID:          m.ID,
		ModelName:        m.Name,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Estimated:        estimated,
	})

	// M3-09：同步把 token 用量写入可观测性指标（供 /metrics 与运行监控概览消费）。
	metrics.RecordTokenUsage(context.Background(), int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.TotalTokens))
}
