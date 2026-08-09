package repo

import (
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// CheckpointFilter 是检查点查询过滤条件。
// UserID=0 表示不按归属过滤（admin/developer 看全员）；非 0 时只返回该用户记录（owner 隔离）。
type CheckpointFilter struct {
	UserID uint   // 0 = 不过滤归属
	Status string // 可选：pending/approved/rejected
	Limit  int    // 分页大小，<=0 时回退 DefaultAuditPageSize，超出上限钳到 MaxAuditPageSize
	Offset int    // 偏移，负值按 0 处理
}

// CreateCheckpoint 持久化一条待审批检查点。
func CreateCheckpoint(db *gorm.DB, cp *model.Checkpoint) error {
	return db.Create(cp).Error
}

// GetCheckpoint 按 id 取一条检查点。
func GetCheckpoint(db *gorm.DB, id uint) (*model.Checkpoint, error) {
	var cp model.Checkpoint
	if err := db.First(&cp, id).Error; err != nil {
		return nil, err
	}
	return &cp, nil
}

// ListCheckpoints 返回按条件过滤的检查点（按创建时间倒序），并带总数（分页友好）。
func ListCheckpoints(db *gorm.DB, f CheckpointFilter) ([]model.Checkpoint, int64, error) {
	q := db.Model(&model.Checkpoint{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
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
	var list []model.Checkpoint
	if err := q.Order("created_at desc").Order("id desc").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ResolveCheckpoint 把一条 pending 检查点更新为终态（approved/rejected），
// 记录审批人、审批意见与（approve 时的）命令执行结果。
func ResolveCheckpoint(db *gorm.DB, id uint, status, comment string, resolvedBy uint, result string) error {
	return db.Model(&model.Checkpoint{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":      status,
		"comment":     comment,
		"resolved_by": resolvedBy,
		"result":      result,
		"updated_at":  time.Now(),
	}).Error
}
