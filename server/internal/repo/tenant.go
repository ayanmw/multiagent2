package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrTenantNotFound 是租户查找无结果（不存在）时的哨兵错误。
var ErrTenantNotFound = errors.New("tenant not found")

// CreateTenant 新建一个租户（平台管理员动作，M8-09）。
func CreateTenant(db *gorm.DB, t *model.Tenant) error {
	return db.Create(t).Error
}

// ListTenants 返回全部租户（管理员视图，按创建时间倒序）。
func ListTenants(db *gorm.DB) ([]model.Tenant, error) {
	var list []model.Tenant
	if err := db.Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetTenant 按主键取租户；不存在返回 ErrTenantNotFound。
func GetTenant(db *gorm.DB, id uint) (*model.Tenant, error) {
	var t model.Tenant
	if err := db.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &t, nil
}

// UpdateTenant 保存租户变更（改名/描述/状态）。
func UpdateTenant(db *gorm.DB, t *model.Tenant) error {
	return db.Save(t).Error
}

// DeleteTenant 删除租户：仅当租户内没有活跃用户时允许（防误删数据归属）；
// 有用户时返回 ErrTenantNotEmpty，调用方应提示先迁移成员或禁用租户。
// 数据层面不级联删用户（软约束：租户删除只是取消聚合边界）。
var ErrTenantNotEmpty = errors.New("tenant not empty")

func DeleteTenant(db *gorm.DB, id uint) error {
	var cnt int64
	if err := db.Model(&model.User{}).Where("tenant_id = ?", id).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return ErrTenantNotEmpty
	}
	return db.Delete(&model.Tenant{}, id).Error
}

// MoveUserToTenant 把用户移入/移出租户：tid>0 加入该租户（校验租户存在且启用），
// tid==0 移出租户（TenantID 置 NULL，恢复为独立用户）。
func MoveUserToTenant(db *gorm.DB, uid, tid uint) error {
	if tid > 0 {
		t, err := GetTenant(db, tid)
		if err != nil {
			return err
		}
		if t.Status != model.TenantStatusActive {
			return errors.New("tenant disabled")
		}
	}
	// 用 struct map 避免 GORM 零值省略列：TenantID 置 0（NULL）用 Select 显式更新。
	if tid == 0 {
		return db.Model(&model.User{}).Where("id = ?", uid).
			Select("tenant_id").Update("tenant_id", nil).Error
	}
	return db.Model(&model.User{}).Where("id = ?", uid).
		Update("tenant_id", tid).Error
}

// CountTenantUsers 统计租户内用户数（管理员视图/删除前检查用）。
func CountTenantUsers(db *gorm.DB, tid uint) (int64, error) {
	var cnt int64
	err := db.Model(&model.User{}).Where("tenant_id = ?", tid).Count(&cnt).Error
	return cnt, err
}
