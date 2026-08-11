package model

import "time"

// Notification 是「平台 → 用户」的站内信（M4-07 通知中心）。
//
// 自主化 Loop（cron/webhook/recover）完成 / 失败 / 需人工检查点时，经统一通知出口
// 写入本表。前端「通知中心」按 owner 拉取并标记已读，使无人值守的 24h Loop
// 结果有可观测的落点（而不是静默成功或失败）。
//
// 设计要点：
//   - 严格 owner 隔离（user_id 索引），与 audit/checkpoint 同源策略；
//   - Type 区分成功/失败/检查点，前端可按类型着色与聚合；
//   - RefKind/RefID 关联来源（automation / automation_run / checkpoint），便于跳转；
//   - ReadAt 标记已读（nil=未读），支持红点计数。
type Notification struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"not null;index" json:"user_id"`
	Type      string     `gorm:"size:16;not null" json:"type"` // success / failure / checkpoint
	Title     string     `gorm:"size:256;not null" json:"title"`
	Message   string     `gorm:"type:text;not null" json:"message"`
	RefKind   string     `gorm:"size:32" json:"ref_kind,omitempty"` // automation / automation_run / checkpoint
	RefID     string     `gorm:"size:64" json:"ref_id,omitempty"`   // 关联对象 id（自动化 id / 运行记录 id / 检查点展示编号）
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 固定通知表名，避免 GORM 复数化规则变化影响既有库。
func (Notification) TableName() string { return "notifications" }

// 通知类型（与前端通知中心着色一致）。
const (
	NotificationTypeSuccess    = "success"
	NotificationTypeFailure    = "failure"
	NotificationTypeCheckpoint = "checkpoint"
)

// 通知来源类型（RefKind）。
const (
	NotificationRefAutomation    = "automation"
	NotificationRefAutomationRun = "automation_run"
	NotificationRefCheckpoint    = "checkpoint"
)
