package repo

import (
	"log"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// DBAuditor 实现 executor.Auditor：把每次命令审计写入 audit_logs 表（M3-01 执行审计落库）。
//
// ownerUserID 在构造时确定——SafeExecutor 在请求级（chat/sse 的当前 uid）或 worker 子代理
// 级（taskrun 的 OwnerUserID）构造，owner 已知，故无需改动 executor.AuditEntry 接口即可
// 带上归属信息。db 为 nil 时退化为空操作（仍不阻断业务命令）。
type DBAuditor struct {
	db          *gorm.DB
	ownerUserID uint
}

// NewDBAuditor 构造一个落库审计器，记录到指定 owner 名下。
func NewDBAuditor(db *gorm.DB, ownerUserID uint) *DBAuditor {
	return &DBAuditor{db: db, ownerUserID: ownerUserID}
}

// Record 把一条审计条目落盘。写入失败只记录日志，绝不阻断被审计的命令执行。
func (a *DBAuditor) Record(e executor.AuditEntry) {
	if a.db == nil {
		return
	}
	rec := &model.AuditLog{
		UserID:   a.ownerUserID,
		Command:  e.Command,
		Workdir:  e.Workdir,
		Decision: e.Decision.String(),
		Reason:   e.Reason,
		Allowed:  e.Allowed,
		Note:     e.Note,
	}
	if err := a.db.Create(rec).Error; err != nil {
		log.Printf("[AUDIT] failed to persist audit log: %v", err)
	}
}

// CreateAuditLog 持久化一条审计记录（供测试/外部直接调用）。
func CreateAuditLog(db *gorm.DB, e *model.AuditLog) error {
	return db.Create(e).Error
}

// 审计日志分页大小约定：缺省 50，单页上限 200（防止前端误传超大 limit 拖垮查询）。
const (
	DefaultAuditPageSize = 50
	MaxAuditPageSize     = 200
)

// AuditLogFilter 是审计日志查询的过滤条件。
// UserID=0 表示不按归属过滤（developer/admin 看全员）；非 0 时只返回该用户记录（owner 隔离）。
type AuditLogFilter struct {
	UserID   uint      // 0 = 不过滤归属
	Decision string    // 可选：allow/deny/ask
	Command  string    // 可选：命令模糊匹配
	Start    time.Time // 可选：起始时间（含），零值表示不限
	End      time.Time // 可选：截止时间（含），零值表示不限
	Limit    int       // 分页大小，<=0 时回退 DefaultAuditPageSize，超出上限则钳到 MaxAuditPageSize
	Offset   int       // 偏移，负值按 0 处理
}

// NormalizeAuditPageSize 归一化分页大小：<=0 回退缺省值，超上限钳制。
func NormalizeAuditPageSize(limit int) int {
	if limit <= 0 {
		return DefaultAuditPageSize
	}
	if limit > MaxAuditPageSize {
		return MaxAuditPageSize
	}
	return limit
}

// ListAuditLogs 返回按条件过滤的审计日志（按时间倒序），并带总数（分页友好）。
func ListAuditLogs(db *gorm.DB, f AuditLogFilter) ([]model.AuditLog, int64, error) {
	q := db.Model(&model.AuditLog{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Decision != "" {
		q = q.Where("decision = ?", f.Decision)
	}
	if f.Command != "" {
		q = q.Where("command LIKE ?", "%"+f.Command+"%")
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
	limit := NormalizeAuditPageSize(f.Limit)
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	var list []model.AuditLog
	if err := q.Order("created_at desc").Order("id desc").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
