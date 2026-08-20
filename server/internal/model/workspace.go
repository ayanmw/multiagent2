package model

import "gorm.io/gorm"

// WorkspaceStatus enumerates the lifecycle states of a workspace.
type WorkspaceStatus string

const (
	WorkspaceStatusActive   WorkspaceStatus = "active"
	WorkspaceStatusArchived WorkspaceStatus = "archived"
)

// Workspace represents a user-owned working directory for code-agent tasks.
// It maps to a local filesystem path (under the configured WorkspaceRoot) and
// optionally a git remote. Conversations (sessions) can be bound to a workspace
// so that the Agent's CodeAct tools execute inside the workspace directory (M1-07).
//
// The public identifier is Key (e.g. "ws-<rand>"); the composite unique index
// (user_id, workspace_key) lets the same key never collide within a user while
// staying globally unique by construction (uuid-based).
type Workspace struct {
	gorm.Model
	UserID      uint            `gorm:"not null;uniqueIndex:idx_user_workspace,priority:1" json:"user_id"`
	Key         string          `gorm:"column:workspace_key;size:64;not null;uniqueIndex:idx_user_workspace,priority:2" json:"key"`
	Name        string          `gorm:"size:128;not null" json:"name"`
	LocalPath   string          `gorm:"size:512;not null" json:"local_path"` // 绝对路径，由后端按 WorkspaceRoot/<uid>/<key> 生成
	GitRemote   string          `gorm:"size:512" json:"git_remote"`
	Description string          `gorm:"size:512" json:"description"`
	Status      WorkspaceStatus `gorm:"size:16;not null;default:active" json:"status"`
	// DiskQuotaBytes 是该 workspace 的磁盘配额上限（字节，M8-09）。0=不限（默认）。
	// 文件工具（file_write/file_edit）写入前检查目录总大小，超限拒绝写入——
	// 实现「workspace 级资源隔离」：一个 workspace 写爆不拖垮同用户其他 workspace。
	DiskQuotaBytes int64 `gorm:"not null;default:0" json:"disk_quota_bytes"`
}

// TableName overrides the default GORM table name.
func (Workspace) TableName() string {
	return "workspaces"
}
