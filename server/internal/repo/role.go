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
