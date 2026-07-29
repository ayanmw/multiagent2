package model

import "gorm.io/gorm"

// Session 表示一次对话会话（多轮消息的集合），按用户归属。
// SessionKey 是对外公开的会话标识，用于 URL（如 /api/chat/:session_id/stream），
// 与数据库自增主键 ID 解耦，避免暴露内部行号，也方便前端自行生成会话 id。
type Session struct {
	gorm.Model
	// 复合唯一约束 (user_id, session_key)：允许不同用户复用同一 session_key，
	// 但同一用户不能重复建相同的 key。取代原先的全局 uniqueIndex（该约束会
	// 错误地禁止跨用户复用 key）。priority 控制复合索引列顺序（user_id 在前，
	// 利于按用户过滤的查询命中索引）。迁移旧库的单列唯一索引见 repo/db.go。
	UserID     uint   `gorm:"not null;uniqueIndex:idx_user_session,priority:1" json:"user_id"`
	SessionKey string `gorm:"size:64;not null;uniqueIndex:idx_user_session,priority:2" json:"session_key"`
	Title      string `gorm:"size:256" json:"title"`
}

// TableName 覆盖 GORM 默认表名。
func (Session) TableName() string { return "sessions" }

// Message 表示会话中的一条消息，可来自用户、助手或工具。
type Message struct {
	gorm.Model
	SessionID uint   `gorm:"not null;index" json:"session_id"`
	Role      string `gorm:"size:16;not null" json:"role"` // user / assistant / tool / system
	Content   string `gorm:"type:text" json:"content"`
}

// TableName 覆盖 GORM 默认表名。
func (Message) TableName() string { return "messages" }
