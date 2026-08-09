package model

import "gorm.io/gorm"

// AuditLog 记录一次受安全策略管控的命令执行（M3-01 执行审计落库）。
// 由 executor.SafeExecutor 的 DBAuditor 在每次 Run/RunCommand 时写入 audit_logs 表，
// 覆盖 CodeAct 工具（shell_exec）、Git 工具与 taskrun 子代理执行的全部命令。
type AuditLog struct {
	gorm.Model
	UserID   uint   `gorm:"not null;index" json:"user_id"`     // 归属用户（owner 隔离）
	Command  string `gorm:"type:text" json:"command"`          // 被执行的命令/argv 拼接串
	Workdir  string `gorm:"type:text" json:"workdir"`          // 执行所在的工作目录
	Decision string `gorm:"size:16;not null" json:"decision"`  // 策略初步判定：allow/deny/ask
	Reason   string `gorm:"type:text" json:"reason"`           // 命中原因（如威胁黑名单条目）
	Allowed  bool   `gorm:"not null;default:false" json:"allowed"` // 最终是否真正执行（ask 模式被拒则为 false）
	Note     string `gorm:"type:text" json:"note"`             // 补充信息（如交互模式确认来源）
}

// TableName overrides the default GORM table name.
func (AuditLog) TableName() string { return "audit_logs" }
