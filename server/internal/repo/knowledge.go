package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// CreateKnowledgeBase 创建知识库（owner 隔离，写入归属用户）。
func CreateKnowledgeBase(db *gorm.DB, kb *model.KnowledgeBase) error {
	if err := kb.Validate(); err != nil {
		return err
	}
	return db.Create(kb).Error
}

// GetKnowledgeBase 按 id 取知识库；owner 隔离：非归属用户返回 nil（无权限）。
func GetKnowledgeBase(db *gorm.DB, id, userID uint) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	if err := db.First(&kb, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &kb, nil
}

// GetKnowledgeBaseAny 取知识库（忽略归属，仅用于内部检索/计数场景）。
func GetKnowledgeBaseAny(db *gorm.DB, id uint) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	if err := db.First(&kb, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &kb, nil
}

// ListKnowledgeBases 列出某用户的全部知识库（按更新时间倒序）。
func ListKnowledgeBases(db *gorm.DB, userID uint) ([]model.KnowledgeBase, error) {
	var list []model.KnowledgeBase
	if err := db.Order("updated_at DESC, id DESC").Find(&list, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateKnowledgeBase 更新知识库的名称/描述（owner 隔离）。未找到返回 nil。
func UpdateKnowledgeBase(db *gorm.DB, id, userID uint, name, description string) (*model.KnowledgeBase, error) {
	kb, err := GetKnowledgeBase(db, id, userID)
	if err != nil {
		return nil, err
	}
	if kb == nil {
		return nil, nil
	}
	if name != "" {
		kb.Name = name
	}
	if description != "" {
		kb.Description = description
	}
	if err := kb.Validate(); err != nil {
		return nil, err
	}
	if err := db.Save(kb).Error; err != nil {
		return nil, err
	}
	return kb, nil
}

// DeleteKnowledgeBase 删除知识库（owner 隔离）。向量数据由调用方在存储层一并清理。
func DeleteKnowledgeBase(db *gorm.DB, id, userID uint) error {
	res := db.Unscoped().Where("id = ? AND user_id = ?", id, userID).Delete(&model.KnowledgeBase{})
	if res.Error != nil {
		return res.Error
	}
	return nil
}
