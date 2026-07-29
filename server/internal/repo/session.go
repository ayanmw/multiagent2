package repo

import (
	"time"

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

// AppendMessage 写入一条会话消息（用户或助手内容），并轻量更新所属会话的
// updated_at，使会话列表可按最近活动时间排序（更新失败不阻断主流程）。
func AppendMessage(db *gorm.DB, sessionID uint, role, content string) error {
	if err := db.Create(&model.Message{
		SessionID: sessionID,
		Role:      role,
		Content:   content,
	}).Error; err != nil {
		return err
	}
	_ = db.Model(&model.Session{}).Where("id = ?", sessionID).Update("updated_at", time.Now()).Error
	return nil
}

// ListSessions 返回指定用户的所有会话，按最近更新时间倒序（最近活动在前）。
func ListSessions(db *gorm.DB, uid uint) ([]model.Session, error) {
	var list []model.Session
	if err := db.Where("user_id = ?", uid).Order("updated_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListSessionMessages 返回会话的全部消息，按创建时间正序（对话自然顺序）。
func ListSessionMessages(db *gorm.DB, sessionID uint) ([]model.Message, error) {
	var msgs []model.Message
	if err := db.Where("session_id = ?", sessionID).Order("created_at ASC").Find(&msgs).Error; err != nil {
		return nil, err
	}
	return msgs, nil
}

// DeleteSession 删除用户归属的会话及其全部消息（级联清理），跨用户返回
// ErrRecordNotFound 防止越权删除。供 DELETE /api/sessions/:id 调用（M0.5-02 RBAC）。
func DeleteSession(db *gorm.DB, uid uint, key string) error {
	var s model.Session
	if err := db.Where("user_id = ? AND session_key = ?", uid, key).First(&s).Error; err != nil {
		return err
	}
	// SQLite 默认关闭外键，显式先删消息再删会话，避免孤儿行。
	if err := db.Where("session_id = ?", s.ID).Delete(&model.Message{}).Error; err != nil {
		return err
	}
	return db.Delete(&s).Error
}

// NewSessionKey 生成一个全局唯一的会话标识（sess- + 8 位随机）。
func NewSessionKey() string {
	return "sess-" + uuid.NewString()[:8]
}
