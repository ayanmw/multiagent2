package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrProviderNotFound is returned when a provider lookup yields no result.
var ErrProviderNotFound = errors.New("provider not found")

// CreateProvider persists a new provider.
func CreateProvider(db *gorm.DB, p *model.Provider) error {
	return db.Create(p).Error
}

// ListProvidersByUser returns all providers owned by a user, newest first.
func ListProvidersByUser(db *gorm.DB, userID uint) ([]model.Provider, error) {
	var list []model.Provider
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetProviderByID finds a provider by primary key.
func GetProviderByID(db *gorm.DB, id uint) (*model.Provider, error) {
	var p model.Provider
	if err := db.First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProviderNotFound
		}
		return nil, err
	}
	return &p, nil
}

// UpdateProvider writes changes to an existing provider.
func UpdateProvider(db *gorm.DB, p *model.Provider) error {
	return db.Save(p).Error
}

// DeleteProvider removes a provider by primary key.
func DeleteProvider(db *gorm.DB, id uint) error {
	return db.Delete(&model.Provider{}, id).Error
}
