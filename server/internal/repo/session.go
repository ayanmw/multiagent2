package repo

import (
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetOrCreateSession 按 SessionKey 取会话；key 为空或不存在则新建一个（自动生成 SessionKey）。
// 保证路由参数总可映射到一行会话（M0-11 的 SSE 端点依赖此行为）。
func GetOrCreateSession(db *gorm.DB, uid uint, key string) (*model.Session, error) {
	if key != "" {
		var s model.Session
		if err := db.Where("user_id = ? AND session_key = ?", uid, key).First(&s).Error; err == nil {
			return &s, nil
		} else if err != gorm.ErrRecordNotFound {
			return nil, err
		}
	}
	s := &model.Session{
		UserID:     uid,
		SessionKey: key, // 客户端传入的 session_id 即对外标识，服务端不再重新生成
		Title:      "新对话",
	}
	if err := db.Create(s).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// GetSessionByKey 查询用户归属的会话（跨用户查询返回 ErrRecordNotFound，防越权）。
// 供 M0-12 Session 管理 API 复用。
func GetSessionByKey(db *gorm.DB, uid uint, key string) (*model.Session, error) {
	var s model.Session
	if err := db.Where("user_id = ? AND session_key = ?", uid, key).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// AppendMessage 写入一条会话消息（用户或助手内容）。
func AppendMessage(db *gorm.DB, sessionID uint, role, content string) error {
	return db.Create(&model.Message{
		SessionID: sessionID,
		Role:      role,
		Content:   content,
	}).Error
}

// NewSessionKey 生成一个全局唯一的会话标识（sess- + 8 位随机）。
func NewSessionKey() string {
	return "sess-" + uuid.NewString()[:8]
}
