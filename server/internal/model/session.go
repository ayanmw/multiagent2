package model

import "gorm.io/gorm"

// Session 表示一次对话会话（多轮消息的集合），按用户归属。
// SessionKey 是对外公开的会话标识，用于 URL（如 /api/chat/:session_id/stream），
// 与数据库自增主键 ID 解耦，避免暴露内部行号，也方便前端自行生成会话 id。
type Session struct {
	gorm.Model
	UserID     uint   `gorm:"not null;index" json:"user_id"`
	SessionKey string `gorm:"size:64;not null;uniqueIndex" json:"session_key"` // 全局唯一，随机生成
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
