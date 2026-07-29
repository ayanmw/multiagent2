package repo

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetOrCreateSession 按 (user_id, session_key) 取会话；key 不存在则新建一个。
// 复合唯一索引 (user_id, session_key) 保证：不同用户可复用同一 session_key，
// 同一用户重复 key 不会新建重复行。并发调用下若两条请求同时 miss 并尝试插入，
// 唯一约束冲突的那条会通过重试查到另一条已建的行，从而消除竞态、不产生脏数据。
func GetOrCreateSession(db *gorm.DB, uid uint, key string) (*model.Session, error) {
	auto := key == ""
	for attempt := 0; attempt < 3; attempt++ {
		if key == "" {
			key = NewSessionKey()
		}
		var s model.Session
		err := db.Where("user_id = ? AND session_key = ?", uid, key).First(&s).Error
		if err == nil {
			return &s, nil
		}
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}
		// 未命中 -> 尝试新建；唯一约束冲突（并发插入）则重试查询已有行。
		created := &model.Session{
			UserID:     uid,
			SessionKey: key, // 客户端传入的 session_id 即对外标识，服务端不再重新生成
			Title:      "新对话",
		}
		if cerr := db.Create(created).Error; cerr == nil {
			return created, nil
		} else if isUniqueViolation(cerr) {
			if auto {
				key = "" // 自动生成的 key 重新随机，避免（极罕见）碰撞时死循环
			}
			continue
		} else {
			return nil, cerr
		}
	}
	return nil, fmt.Errorf("get_or_create_session: 超过重试次数 key=%q", key)
}

// isUniqueViolation 判断是否为唯一约束冲突（兼容 GORM 转译错误与 SQLite 原生错误）。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "errduplicatedkey")
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
