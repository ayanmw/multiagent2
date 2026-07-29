package repo

import (
	"errors"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/gorm"
)

// ErrRoleNotFound is returned when a role lookup yields no result.
var ErrRoleNotFound = errors.New("role not found")

// GetRoleByName finds a role by its unique name.
func GetRoleByName(db *gorm.DB, name string) (*model.Role, error) {
	var r model.Role
	if err := db.Where("name = ?", name).First(&r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	return &r, nil
}

// GetRoleIDByName returns the ID of the role with the given name. It is a
// convenience wrapper for callers (e.g. assigning a default role at registration)
// that only need the numeric ID, avoiding hardcoded role IDs (M0.5-05).
func GetRoleIDByName(db *gorm.DB, name string) (uint, error) {
	r, err := GetRoleByName(db, name)
	if err != nil {
		return 0, err
	}
	return r.ID, nil
}

// GetPermissionsByRoleID returns all permissions assigned to a role.
func GetPermissionsByRoleID(db *gorm.DB, roleID uint) ([]model.RolePermission, error) {
	var perms []model.RolePermission
	if err := db.Where("role_id = ?", roleID).Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

// ListRoles returns all roles, each with its permissions preloaded.
func ListRoles(db *gorm.DB) ([]model.Role, error) {
	var roles []model.Role
	if err := db.Preload("Permissions").Order("id asc").Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}
