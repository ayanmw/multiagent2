package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrAutomationNotFound 表示 Automation 查找无结果（缺失或不属于当前用户）。
var ErrAutomationNotFound = errors.New("automation not found")

// 本文件是 Automation 的唯一持久化出口，全部按 user_id 归属隔离
// （owner-scoped CRUD），与 MCP / Provider / Workspace 等管理面资源一致。

// CreateAutomation 持久化一个新的 Automation。
func CreateAutomation(db *gorm.DB, a *model.Automation) error {
	return db.Create(a).Error
}

// ListAutomations 返回某用户归属的全部 Automation（按创建时间倒序）。
func ListAutomations(db *gorm.DB, userID uint) ([]model.Automation, error) {
	var list []model.Automation
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetAutomationByName 按 (user_id, name) 查重，用于创建前的冲突检测。
// 未找到返回 ErrAutomationNotFound。
func GetAutomationByName(db *gorm.DB, userID uint, name string) (*model.Automation, error) {
	var a model.Automation
	if err := db.Where("user_id = ? AND name = ?", userID, name).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAutomationNotFound
		}
		return nil, err
	}
	return &a, nil
}

// GetAutomationByID 按主键查并校验归属；缺失或越权返回 ErrAutomationNotFound。
func GetAutomationByID(db *gorm.DB, userID, id uint) (*model.Automation, error) {
	var a model.Automation
	if err := db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAutomationNotFound
		}
		return nil, err
	}
	if a.UserID != userID {
		return nil, ErrAutomationNotFound
	}
	return &a, nil
}

// GetAutomationByWebhookToken 按 WebhookToken 查找 Automation（M4-03 外部事件匹配用）。
// 仅返回启用的自动化；未找到或无启用项返回 ErrAutomationNotFound。
func GetAutomationByWebhookToken(db *gorm.DB, token string) (*model.Automation, error) {
	if token == "" {
		return nil, ErrAutomationNotFound
	}
	var a model.Automation
	if err := db.Where("webhook_token = ? AND enabled = ?", token, true).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAutomationNotFound
		}
		return nil, err
	}
	return &a, nil
}

// UpdateAutomation 写入已存在 Automation 的变更。
func UpdateAutomation(db *gorm.DB, a *model.Automation) error {
	return db.Save(a).Error
}

// DeleteAutomation 按主键删除。
func DeleteAutomation(db *gorm.DB, id uint) error {
	return db.Delete(&model.Automation{}, id).Error
}
