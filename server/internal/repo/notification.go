package repo

import (
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// 本文件是 Notification（站内信）的唯一持久化出口（M4-07 通知中心）。
// 全部按 user_id 归属隔离，与 audit/checkpoint 同源策略一致。

// CreateNotification 写入一条站内信（status 由调用方决定，未读=ReadAt 为空）。
func CreateNotification(db *gorm.DB, n *model.Notification) error {
	return db.Create(n).Error
}

// ListNotifications 返回某用户归属的全部站内信（按创建时间倒序，最新在前）。
func ListNotifications(db *gorm.DB, userID uint, limit, offset int) ([]model.Notification, int64, error) {
	var total int64
	if err := db.Model(&model.Notification{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var list []model.Notification
	if err := db.Where("user_id = ?", userID).Order("created_at desc").
		Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CountUnreadNotifications 返回某用户未读站内信数量（前端红点）。
func CountUnreadNotifications(db *gorm.DB, userID uint) (int64, error) {
	var cnt int64
	if err := db.Model(&model.Notification{}).Where("user_id = ? AND read_at IS NULL", userID).
		Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

// MarkNotificationRead 将单条通知标记为已读（owner 隔离；越权无效果）。
func MarkNotificationRead(db *gorm.DB, userID, id uint) error {
	now := time.Now()
	return db.Model(&model.Notification{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("read_at", &now).Error
}

// MarkAllNotificationsRead 将某用户全部未读通知批量标记已读（owner 隔离）。
func MarkAllNotificationsRead(db *gorm.DB, userID uint) (int64, error) {
	now := time.Now()
	res := db.Model(&model.Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", &now)
	return res.RowsAffected, res.Error
}
