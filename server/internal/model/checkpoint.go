package model

import (
	"strconv"

	"gorm.io/gorm"
)

// 人工检查点状态（M3-05 human-in-the-loop）。
const (
	CheckpointPending  = "pending"  // 待审批
	CheckpointApproved = "approved" // 已批准，命令已执行
	CheckpointRejected = "rejected" // 已拒绝，命令未执行
)

// Checkpoint 是一条「待人工审批的危险命令」记录（M3-05）。
// 无人值守模式下命中 ask 危险策略的命令不再直接 deny，而是落库为 checkpoint，
// 经前端审批后（approve 实际执行 / reject 中止）再决定该命令的最终命运。
type Checkpoint struct {
	gorm.Model
	SessionID  string `gorm:"size:128;index" json:"session_id"` // 归属的对话会话
	UserID     uint   `gorm:"index" json:"user_id"`             // 触发检查点的 owner（用于 owner 隔离）
	Command    string `gorm:"type:text" json:"command"`         // 触发检查点的原始命令
	Workdir    string `gorm:"type:text" json:"workdir"`         // 命令预期执行的工作目录
	Reason     string `gorm:"type:text" json:"reason"`          // 命中 ask 策略的原因
	Context    string `gorm:"type:text" json:"context"`         // 触发上下文（agent 角色 / goal 等）
	Status     string `gorm:"size:16;not null;default:'pending'" json:"status"`
	Comment    string `gorm:"type:text" json:"comment"` // 审批意见（reject/approve 均可填）
	ResolvedBy uint   `gorm:"index" json:"resolved_by"` // 审批人 user_id
	Result     string `gorm:"type:text" json:"result"`  // approve 后命令的实际执行结果
}

// TableName overrides the default GORM table name.
func (Checkpoint) TableName() string { return "checkpoints" }

// DisplayID 返回面向前端/用户的展示编号（如 "CP-12"）。
func (c *Checkpoint) DisplayID() string {
	return "CP-" + strconv.FormatUint(uint64(c.ID), 10)
}
