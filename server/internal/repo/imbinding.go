package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// IM 绑定（M8-07）数据访问层：im_bindings 表 owner-scoped CRUD。
// 绑定关系 = 「IM 平台用户 → 平台用户」；webhook 侧按 (platform, im_user_id)
// 精确匹配，管理侧按 user_id 隔离（本人只能看/删自己的绑定）。

// ErrIMBindingNotFound 是绑定不存在/越权的哨兵错误（handler 转 404/403）。
var ErrIMBindingNotFound = errors.New("im binding not found")

// CreateIMBinding 创建绑定。复合唯一 (platform, im_user_id) 冲突时返回错误
// （上层转 409 提示「该 IM 用户已绑定」）。
func CreateIMBinding(db *gorm.DB, b *model.IMBinding) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if err := db.Create(b).Error; err != nil {
		if isUniqueViolation(err) {
			return errors.New("im user already bound")
		}
		return err
	}
	return nil
}

// ListIMBindingsByUser 列出某平台用户自己的全部绑定。
func ListIMBindingsByUser(db *gorm.DB, userID uint) ([]model.IMBinding, error) {
	var rows []model.IMBinding
	if err := db.Where("user_id = ?", userID).Order("platform ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetIMBindingByPlatformUser 按 (platform, im_user_id) 查绑定（webhook 入站匹配用）。
// 未命中返回 ErrIMBindingNotFound。
func GetIMBindingByPlatformUser(db *gorm.DB, platform, imUserID string) (*model.IMBinding, error) {
	var b model.IMBinding
	if err := db.Where("platform = ? AND im_user_id = ?", platform, imUserID).First(&b).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrIMBindingNotFound
		}
		return nil, err
	}
	return &b, nil
}

// GetIMBindingByID 按 id 取绑定（越权判定由调用方比对 UserID）。
func GetIMBindingByID(db *gorm.DB, id uint) (*model.IMBinding, error) {
	var b model.IMBinding
	if err := db.First(&b, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrIMBindingNotFound
		}
		return nil, err
	}
	return &b, nil
}

// DeleteIMBinding 删除绑定（调用方已确认 owner 归属）。
func DeleteIMBinding(db *gorm.DB, id uint) error {
	res := db.Delete(&model.IMBinding{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrIMBindingNotFound
	}
	return nil
}
