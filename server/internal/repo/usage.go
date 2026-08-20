package repo

import (
	"log"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// 用量分页大小约定：与审计日志一致，缺省 50，单页上限 200。
const (
	DefaultUsagePageSize = 50
	MaxUsagePageSize     = 200
)

// UsageRecordFilter 是用量记录查询的过滤条件。
// UserID=0 表示不按归属过滤（developer/admin 看全员）；非 0 时只返回该用户记录（owner 隔离）。
// TenantID!=0 时按「租户内全部用户」过滤（M8-09 租户预算聚合，user_id IN 子查询）。
type UsageRecordFilter struct {
	UserID     uint      // 0 = 不过滤归属
	TenantID   uint      // 0 = 不过滤租户；非 0 = 聚合 users.tenant_id 下全部用户的记录（M8-09）
	ProviderID uint      // 可选：按 Provider 过滤
	ModelID    uint      // 可选：按模型过滤
	SessionID  uint      // 可选：按会话 DB id 过滤
	SessionKey string    // 可选：按会话业务 key 过滤
	// WorkspaceKey 可选：按 workspace key 过滤（M8-09 workspace 作用域预算聚合）。
	WorkspaceKey string    // 可选：按 workspace key 过滤
	Start        time.Time // 可选：起始时间（含），零值表示不限
	End          time.Time // 可选：截止时间（含），零值表示不限
	Limit        int       // 分页大小，<=0 时回退 DefaultUsagePageSize，超出上限则钳到 MaxUsagePageSize
	Offset       int       // 偏移，负值按 0 处理
}

// NormalizeUsagePageSize 归一化分页大小：<=0 回退缺省值，超上限钳制。
func NormalizeUsagePageSize(limit int) int {
	if limit <= 0 {
		return DefaultUsagePageSize
	}
	if limit > MaxUsagePageSize {
		return MaxUsagePageSize
	}
	return limit
}

// CreateUsageRecord 持久化一条用量记录（供 api 层对话结束后调用）。
func CreateUsageRecord(db *gorm.DB, rec *model.UsageRecord) error {
	if err := db.Create(rec).Error; err != nil {
		log.Printf("[USAGE] failed to persist usage record: %v", err)
		return err
	}
	return nil
}

// ListUsageRecords 返回按条件过滤的用量记录（按时间倒序），并带总数（分页友好）。
func ListUsageRecords(db *gorm.DB, f UsageRecordFilter) ([]model.UsageRecord, int64, error) {
	q := db.Model(&model.UsageRecord{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.TenantID != 0 {
		// 租户聚合：user_id IN (SELECT id FROM users WHERE tenant_id = ?)。
		// 租户 A 的用户记录与租户 B 完全按 user 归属隔离，互不污染聚合结果。
		q = q.Where("user_id IN (SELECT id FROM users WHERE tenant_id = ?)", f.TenantID)
	}
	if f.ProviderID != 0 {
		q = q.Where("provider_id = ?", f.ProviderID)
	}
	if f.ModelID != 0 {
		q = q.Where("model_id = ?", f.ModelID)
	}
	if f.SessionID != 0 {
		q = q.Where("session_id = ?", f.SessionID)
	}
	if f.SessionKey != "" {
		q = q.Where("session_key = ?", f.SessionKey)
	}
	if f.WorkspaceKey != "" {
		q = q.Where("workspace_key = ?", f.WorkspaceKey)
	}
	if !f.Start.IsZero() {
		q = q.Where("created_at >= ?", f.Start)
	}
	if !f.End.IsZero() {
		q = q.Where("created_at <= ?", f.End)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := NormalizeUsagePageSize(f.Limit)
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	var list []model.UsageRecord
	if err := q.Order("created_at desc").Order("id desc").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// UsageTotals 是用量记录的累计汇总（M3-03 「GET /api/usage 聚合」）。
type UsageTotals struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Records          int64 `json:"records"` // 参与聚合的记录数
}

// SumUsageRecords 在给定过滤条件下累计 token 用量（忽略分页，统计全部命中记录）。
func SumUsageRecords(db *gorm.DB, f UsageRecordFilter) (UsageTotals, error) {
	q := db.Model(&model.UsageRecord{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.TenantID != 0 {
		// 租户聚合：user_id IN (SELECT id FROM users WHERE tenant_id = ?)。
		// 租户 A 的用户记录与租户 B 完全按 user 归属隔离，互不污染聚合结果。
		q = q.Where("user_id IN (SELECT id FROM users WHERE tenant_id = ?)", f.TenantID)
	}
	if f.ProviderID != 0 {
		q = q.Where("provider_id = ?", f.ProviderID)
	}
	if f.ModelID != 0 {
		q = q.Where("model_id = ?", f.ModelID)
	}
	if f.SessionID != 0 {
		q = q.Where("session_id = ?", f.SessionID)
	}
	if f.SessionKey != "" {
		q = q.Where("session_key = ?", f.SessionKey)
	}
	if f.WorkspaceKey != "" {
		q = q.Where("workspace_key = ?", f.WorkspaceKey)
	}
	if !f.Start.IsZero() {
		q = q.Where("created_at >= ?", f.Start)
	}
	if !f.End.IsZero() {
		q = q.Where("created_at <= ?", f.End)
	}
	var t UsageTotals
	if err := q.Select(
		"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens," +
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens," +
			"COALESCE(SUM(total_tokens),0) AS total_tokens," +
			"COUNT(*) AS records",
	).Scan(&t).Error; err != nil {
		return t, err
	}
	return t, nil
}
